// Command window-switcher is a macropadd plugin that binds each key to a
// macOS desktop, app, or window, and brings it forward when the key is
// pressed. A desktop is reached through its Ctrl+N "Switch to Desktop"
// shortcut; an app is activated; a window is raised through the
// Accessibility API. Each key lights in its own color, bright for the
// last target switched to and dim for the rest.
//
// With no --config, key N switches to desktop N+1. See README.md in this
// directory for the config format and the macOS settings it needs.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("window-switcher", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	configPath := fs.String("config", "", "JSON config file binding keys to desktops, apps, or windows (default: key N switches to desktop N+1)")
	dryRun := fs.Bool("dry-run", false, "print the osascript command for each switch instead of running it")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	cfg := defaultConfig()
	if *configPath != "" {
		var err error
		if cfg, err = loadConfig(*configPath); err != nil {
			fmt.Fprintln(stderr, err)
			return 2
		}
	} else if err := cfg.validate(); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	var r runner = osascriptRunner{}
	if *dryRun {
		r = printRunner{w: stdout}
	}

	conn, err := api.Dial(*addr)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer conn.Close()

	fmt.Fprintf(stdout, "window-switcher: connected to %s, switching on %s\n", *addr, cfg.Trigger)
	for _, b := range cfg.Keys {
		fmt.Fprintf(stdout, "window-switcher: key %d -> %s\n", b.Key, b.describe())
	}
	return newSwitcher(cfg, conn, r, stdout, stderr).watch()
}

// keyConn is the subset of *api.Conn the switcher needs. A test
// substitutes a fake, so the press-to-switch reaction is checked with no
// live macropadd process or WebSocket connection.
type keyConn interface {
	ReadMessage() (plugin.Message, error)
	SetKeyState(key int, color uint16, emojiID byte, blink bool) error
}

type switcher struct {
	cfg            Config
	byKey          map[int]Binding
	conn           keyConn
	run            runner
	stdout, stderr io.Writer
	active         int // key of the last target switched to, -1 for none
}

func newSwitcher(cfg Config, conn keyConn, r runner, stdout, stderr io.Writer) *switcher {
	s := &switcher{cfg: cfg, byKey: map[int]Binding{}, conn: conn, run: r, stdout: stdout, stderr: stderr, active: -1}
	for _, b := range cfg.Keys {
		s.byKey[b.Key] = b
	}
	return s
}

// watch lights every bound key dim, then blocks reading messages until
// the connection errors, switching on every trigger for a bound key.
func (s *switcher) watch() int {
	for _, b := range s.cfg.Keys {
		s.light(b, false)
	}
	for {
		msg, err := s.conn.ReadMessage()
		if err != nil {
			fmt.Fprintln(s.stderr, err)
			return 1
		}
		key, ok := s.triggerKey(msg)
		if !ok {
			continue
		}
		b, ok := s.byKey[key]
		if !ok {
			continue
		}
		if err := s.run.Run(scriptFor(b)); err != nil {
			fmt.Fprintf(s.stderr, "window-switcher: key %d -> %s: %v\n", key, b.describe(), err)
			continue
		}
		fmt.Fprintf(s.stdout, "window-switcher: key %d -> %s\n", key, b.describe())
		if s.active == key {
			continue
		}
		if prev, ok := s.byKey[s.active]; ok {
			s.light(prev, false)
		}
		s.active = key
		s.light(b, true)
	}
}

// triggerKey reports the key msg triggers a switch on, if any: a raw
// press event when the trigger is "press", otherwise the matching
// resolved signal.
func (s *switcher) triggerKey(msg plugin.Message) (int, bool) {
	if s.cfg.Trigger == "press" {
		if msg.Kind == plugin.KindEvent && msg.Event != nil && msg.Event.Type == "press" {
			return int(msg.Event.KeyIndex), true
		}
		return 0, false
	}
	if msg.Kind == plugin.KindSignal && msg.Signal != nil && msg.Signal.Name == s.cfg.Trigger {
		return int(msg.Signal.KeyIndex), true
	}
	return 0, false
}

func (s *switcher) light(b Binding, active bool) {
	col := b.color
	if !active {
		col = dim(col)
	}
	if err := s.conn.SetKeyState(b.Key, col, 0, false); err != nil {
		fmt.Fprintln(s.stderr, err)
	}
}

// printRunner is --dry-run's runner: it prints each osascript command
// line instead of running it, so the plugin can be tried on any OS.
type printRunner struct{ w io.Writer }

func (p printRunner) Run(s switchScript) error {
	fmt.Fprintf(p.w, "window-switcher: would run %q\n", s.argv())
	return nil
}
