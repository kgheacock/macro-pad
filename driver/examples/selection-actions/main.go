// Command selection-actions is task 0077's plugin: select text in any app,
// press its pad key, and an action runs on the text. A single press
// summarises, a double press translates, and a long press asks Claude. The
// answer lands in an inbox channel, one child per action, so the person
// keeps working while it runs.
//
// It is a standalone binary for now. Tasks 0055 (the plugin host) and 0053
// (selection capture as a host service) are not on main yet; once they are,
// it moves to driver/plugins/selectionactions.
//
//	go run ./driver/examples/selection-actions --config ~/.config/macropad/selection-actions.json
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// maxAnswerBytes bounds one answer. The inbox is for short messages.
const maxAnswerBytes = 16 * 1024

// flashFor is how long the key shows WARN after a press with nothing
// selected.
const flashFor = time.Second

// action is one command a click on the key runs.
type action struct {
	// Click is "single", "double", or "long".
	Click string `json:"click"`
	// Name is the action's name, and the last part of its inbox channel.
	Name string `json:"name"`
	// Command is the argv to run, with no shell. The selection is its
	// standard input, and its standard output is the answer.
	Command []string `json:"command"`
}

// config is the JSON file --config names. Every field is optional.
type config struct {
	// ID is the key's id in the layer tree.
	ID string `json:"id"`
	// Slot is the top-level key, 0 to 5.
	Slot *int `json:"slot"`
	// Label is the key's iconText: at most two lines.
	Label string `json:"label"`
	// Channel is the inbox channel; an answer goes to CHANNEL.NAME.
	Channel string `json:"channel"`
	// Timeout stops a command that runs too long, such as "60s".
	Timeout string `json:"timeout"`
	// Inbox is the inbox command.
	Inbox   string   `json:"inbox"`
	Actions []action `json:"actions"`

	timeout time.Duration
}

// defaultActions run Claude Code headless, so they use the person's own
// Claude login and need no API key.
var defaultActions = []action{
	{Click: "single", Name: "summarise", Command: []string{"claude", "-p",
		"Summarise the text on standard input in three sentences or fewer. Reply with the summary only."}},
	{Click: "double", Name: "translate", Command: []string{"claude", "-p",
		"Translate the text on standard input into English. If it is already English, translate it into French. Reply with the translation only."}},
	{Click: "long", Name: "ask", Command: []string{"claude", "-p",
		"Explain the text on standard input, and answer any question in it. Be brief."}},
}

// clickSignals maps a config click onto the signal the daemon's resolver
// broadcasts for it.
var clickSignals = map[string]string{
	"single": plugin.SignalSinglePress,
	"double": plugin.SignalDoublePress,
	"long":   plugin.SignalLongPress,
}

// loadConfig reads path, or uses the defaults when path is empty, and
// checks it. Every problem is reported at once.
func loadConfig(path string) (config, error) {
	var c config
	if path != "" {
		b, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		dec := json.NewDecoder(strings.NewReader(string(b)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&c); err != nil {
			return c, fmt.Errorf("%s: %w", path, err)
		}
	}
	if c.ID == "" {
		c.ID = "selection"
	}
	if c.Slot == nil {
		slot := 3
		c.Slot = &slot
	}
	if c.Label == "" {
		c.Label = "selection"
	}
	if c.Channel == "" {
		c.Channel = "selection"
	}
	if c.Timeout == "" {
		c.Timeout = "60s"
	}
	if c.Inbox == "" {
		c.Inbox = "inbox"
	}
	if len(c.Actions) == 0 {
		c.Actions = defaultActions
	}

	var problems []string
	if *c.Slot < 0 || *c.Slot > 5 {
		problems = append(problems, fmt.Sprintf("slot %d is not 0 to 5", *c.Slot))
	}
	d, err := time.ParseDuration(c.Timeout)
	if err != nil || d <= 0 {
		problems = append(problems, fmt.Sprintf("timeout %q is not a positive duration", c.Timeout))
	}
	c.timeout = d
	seen := map[string]bool{}
	for i, a := range c.Actions {
		if _, ok := clickSignals[a.Click]; !ok {
			problems = append(problems, fmt.Sprintf("action %d: click %q is not single, double, or long", i, a.Click))
		} else if seen[a.Click] {
			problems = append(problems, fmt.Sprintf("action %d: a second action on click %q", i, a.Click))
		}
		seen[a.Click] = true
		if a.Name == "" || strings.ContainsAny(a.Name, ". \t\n") {
			problems = append(problems, fmt.Sprintf("action %d: name %q must be one word with no dot", i, a.Name))
		}
		if len(a.Command) == 0 {
			problems = append(problems, fmt.Sprintf("action %d: command is empty", i))
		}
	}
	if len(problems) > 0 {
		return c, errors.New(strings.Join(problems, "; "))
	}
	return c, nil
}

// selector reads the frontmost app's selected text. An empty string and no
// error means nothing is selected.
type selector func(ctx context.Context) (string, error)

// runner runs argv with stdin and returns its standard output and error.
type runner func(ctx context.Context, argv []string, stdin string) (stdout, stderr string, err error)

// execRunner runs argv as a child process.
func execRunner(ctx context.Context, argv []string, stdin string) (string, string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	return out.String(), errOut.String(), err
}

// writer is the part of *websocket.Conn the plugin writes with. A test
// passes a fake.
type writer interface {
	WriteJSON(v any) error
}

// selPlugin is one key's worth of state. handle runs on the read loop; each
// press runs on its own goroutine, so the loop keeps reading.
type selPlugin struct {
	cfg    config
	conn   writer
	sel    selector
	run    runner
	logf   func(format string, args ...any)
	writeM sync.Mutex

	mu       sync.Mutex
	topLayer bool
	busy     bool

	wg sync.WaitGroup
}

func newPlugin(cfg config, conn writer, sel selector, run runner, logf func(string, ...any)) *selPlugin {
	return &selPlugin{cfg: cfg, conn: conn, sel: sel, run: run, logf: logf, topLayer: true}
}

// setStatus sends the key with status, keeping its slot and label.
func (p *selPlugin) setStatus(status string) {
	slot := *p.cfg.Slot
	p.writeM.Lock()
	defer p.writeM.Unlock()
	err := p.conn.WriteJSON(plugin.Message{
		Kind: plugin.KindSetLayerKey,
		SetLayerKey: &plugin.SetLayerKeyPayload{
			ID: p.cfg.ID, Slot: &slot, Status: status, IconText: p.cfg.Label,
		},
	})
	if err != nil {
		p.logf("key: %v", err)
	}
}

// handle reacts to one message from the daemon.
func (p *selPlugin) handle(msg plugin.Message) {
	switch msg.Kind {
	case plugin.KindLayerChanged:
		if msg.LayerChanged != nil {
			p.mu.Lock()
			p.topLayer = msg.LayerChanged.Active == nil
			p.mu.Unlock()
		}
	case plugin.KindSignal:
		s := msg.Signal
		if s == nil || int(s.KeyIndex) != *p.cfg.Slot {
			return
		}
		for _, a := range p.cfg.Actions {
			if clickSignals[a.Click] == s.Name {
				p.press(a)
				return
			}
		}
	}
}

// press starts a on its own goroutine, unless the pad shows a child layer,
// where the key is not this plugin's, or an action already runs.
func (p *selPlugin) press(a action) {
	p.mu.Lock()
	if !p.topLayer {
		p.mu.Unlock()
		return
	}
	if p.busy {
		p.mu.Unlock()
		p.logf("%s: ignored, an action is still running", a.Name)
		return
	}
	p.busy = true
	p.mu.Unlock()

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() {
			p.mu.Lock()
			p.busy = false
			p.mu.Unlock()
		}()
		p.do(a)
	}()
}

// do captures the selection, runs a on it, and sends the answer to the
// inbox.
func (p *selPlugin) do(a action) {
	ctx, cancel := context.WithTimeout(context.Background(), p.cfg.timeout)
	defer cancel()

	text, err := p.sel(ctx)
	if err != nil {
		p.logf("%s: selection: %v", a.Name, err)
		p.setStatus(layers.StatusError)
		return
	}
	if strings.TrimSpace(text) == "" {
		p.logf("%s: nothing selected", a.Name)
		p.setStatus(layers.StatusWarn)
		time.Sleep(flashFor)
		p.setStatus("")
		return
	}

	p.setStatus(layers.StatusRunning)
	out, errOut, err := p.run(ctx, a.Command, text)
	answer := strings.TrimSpace(out)
	status := "info"
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		status, answer = "error", fmt.Sprintf("%s failed: no answer after %s", a.Name, p.cfg.Timeout)
	case err != nil:
		status, answer = "error", fmt.Sprintf("%s failed: %s", a.Name, reason(err, errOut))
	case answer == "":
		status, answer = "error", fmt.Sprintf("%s failed: the command printed nothing", a.Name)
	}
	answer = truncate(answer, maxAnswerBytes)

	// The inbox send gets its own deadline, so a command that used the
	// whole timeout still reports.
	sendCtx, sendCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer sendCancel()
	channel := p.cfg.Channel + "." + a.Name
	_, sendErr, err := p.run(sendCtx, []string{p.cfg.Inbox, "send", "--channel", channel, "--status", status, "--", answer}, "")
	if err != nil {
		p.logf("%s: inbox send: %s", a.Name, reason(err, sendErr))
		p.setStatus(layers.StatusError)
		return
	}
	if status == "error" {
		p.logf("%s", answer)
		p.setStatus(layers.StatusError)
		return
	}
	p.logf("%s: answer sent to %s", a.Name, channel)
	p.setStatus("")
}

// reason is the first line of stderr, or err when stderr is empty.
func reason(err error, stderr string) string {
	if line, _, _ := strings.Cut(strings.TrimSpace(stderr), "\n"); line != "" {
		return line
	}
	return err.Error()
}

// truncate cuts s to at most n bytes on a rune boundary, ending in "…".
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	const ell = "…"
	cut := n - len(ell)
	for cut > 0 && !utf8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ell
}

func utf8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// checkCommands logs every command that is not on PATH. A login service's
// PATH is short, so the log names it.
func checkCommands(cfg config, logf func(string, ...any)) {
	names := []string{cfg.Inbox}
	for _, a := range cfg.Actions {
		names = append(names, a.Command[0])
	}
	for _, n := range names {
		if _, err := exec.LookPath(n); err != nil {
			logf("%s is not on PATH (%s); its action fails until it is", n, os.Getenv("PATH"))
		}
	}
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(run(ctx, os.Args[1:], os.Stderr))
}

func run(ctx context.Context, args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("selection-actions", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	cfgPath := fs.String("config", "", "JSON config file; without one, key 3 runs the three default actions")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(stderr, "selection-actions:", err)
		return 2
	}
	logger := log.New(stderr, "selection-actions: ", log.LstdFlags)
	checkCommands(cfg, logger.Printf)

	conn, _, err := websocket.DefaultDialer.DialContext(ctx, "ws://"+*addr+"/", nil)
	if err != nil {
		logger.Printf("connect %s: %v", *addr, err)
		return 1
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close()
	}()

	p := newPlugin(cfg, conn, captureSelection, execRunner, logger.Printf)
	p.setStatus("")
	logger.Printf("connected to %s, key %d", *addr, *cfg.Slot)
	for {
		var msg plugin.Message
		if err := conn.ReadJSON(&msg); err != nil {
			if ctx.Err() != nil {
				return 0
			}
			logger.Printf("read: %v", err)
			return 1
		}
		p.handle(msg)
	}
}
