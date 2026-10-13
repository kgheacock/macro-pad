package host

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/kgheacock/macro-pad/driver/host/macos"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// Conn is the host's connection to macropadd. *api.Conn satisfies it.
type Conn interface {
	ReadMessage() (plugin.Message, error)
	Send(plugin.Message) error
	Close() error
}

// Options tunes a Runner. Dial is required; a zero field otherwise takes
// its default.
type Options struct {
	// Dial opens one connection to macropadd.
	Dial func(ctx context.Context) (Conn, error)
	// Logf writes one log line. Default: the standard logger.
	Logf func(format string, args ...any)
	// Permissions reports the binary's macOS grants. Default:
	// macos.Permissions.
	Permissions func() macos.PermissionState
	// MinBackoff and MaxBackoff bound the wait between redials. Defaults:
	// 250ms and 5s.
	MinBackoff, MaxBackoff time.Duration
}

// Queue sizes. A full queue drops the next item and logs it, matching
// the plugin server's drop-rather-than-block rule (task 0028).
const (
	// instanceQueueSize bounds one instance's pending handler calls.
	instanceQueueSize = 64
	// outQueueSize bounds messages waiting to be written to macropadd.
	outQueueSize = 256
)

// SignalPageChanged is the signal the host broadcasts after a page
// switch. Its keyIndex is the new page's index in Pages.
const SignalPageChanged = "pageChanged"

// Runner is the plugin host: every instance, the active page, and the
// connection.
type Runner struct {
	opt       Options
	instances []*instance
	pages     []string

	// mu guards active, connected, and every instance's key cache, so a
	// Key call, a page switch, and a repaint never interleave.
	mu        sync.Mutex
	active    string
	connected bool

	connMu sync.Mutex
	conn   Conn

	out chan plugin.Message
}

// New builds a Runner for cfg, validated against factories. Nothing runs
// until Run.
func New(cfg Config, factories []Factory, opt Options) (*Runner, error) {
	if err := cfg.Validate(factories); err != nil {
		return nil, err
	}
	if opt.Dial == nil {
		return nil, errors.New("host: Options.Dial is required")
	}
	if opt.Logf == nil {
		opt.Logf = log.Printf
	}
	if opt.Permissions == nil {
		opt.Permissions = macos.Permissions
	}
	if opt.MinBackoff <= 0 {
		opt.MinBackoff = 250 * time.Millisecond
	}
	if opt.MaxBackoff <= 0 {
		opt.MaxBackoff = 5 * time.Second
	}

	byName := map[string]Factory{}
	for _, f := range factories {
		byName[f.Name] = f
	}
	r := &Runner{
		opt:    opt,
		pages:  cfg.Pages(),
		out:    make(chan plugin.Message, outQueueSize),
		active: cfg.StartPage,
	}
	if r.active == "" {
		r.active = r.pages[0]
	}
	for _, ic := range cfg.Plugins {
		r.instances = append(r.instances, newInstance(r, ic, byName[ic.Use].New()))
	}
	return r, nil
}

// Run starts every instance, then connects to macropadd and routes its
// messages until ctx is done, redialing whenever the connection drops.
func (r *Runner) Run(ctx context.Context) error {
	r.logPermissions()

	// Each instance's goroutine starts after its Start returns, so an
	// Every tick queued during Start never runs alongside it.
	for _, in := range r.instances {
		in.start(ctx)
		go in.loop(ctx)
	}
	go r.writeLoop(ctx)

	backoff := r.opt.MinBackoff
	wasDown := false
	for ctx.Err() == nil {
		conn, err := r.opt.Dial(ctx)
		if err != nil {
			if !wasDown {
				r.opt.Logf("host: cannot reach macropadd, retrying: %v", err)
				wasDown = true
			}
			select {
			case <-ctx.Done():
			case <-time.After(backoff):
			}
			backoff = min(2*backoff, r.opt.MaxBackoff)
			continue
		}
		backoff = r.opt.MinBackoff
		wasDown = false
		r.opt.Logf("host: connected")
		r.attach(conn)

		stop := context.AfterFunc(ctx, func() { conn.Close() })
		err = r.readLoop(conn)
		stop()
		r.detach()
		conn.Close()
		if ctx.Err() == nil {
			r.opt.Logf("host: connection lost, reconnecting: %v", err)
			wasDown = true
		}
	}
	return nil
}

// logPermissions logs one line per missing macOS grant, once, at start.
func (r *Runner) logPermissions() {
	p := r.opt.Permissions()
	if !p.Supported {
		return
	}
	for _, perm := range []macos.Permission{macos.Accessibility, macos.ScreenRecording} {
		if !p.Has(perm) {
			r.opt.Logf("host: %s is not granted to %s; plugins that need it stay off (System Settings > Privacy & Security > %s)", perm, macos.Executable(), perm)
		}
	}
}

// attach makes conn current and repaints every key whose owner has
// state, so a daemon restart loses nothing a plugin set.
func (r *Runner) attach(conn Conn) {
	r.connMu.Lock()
	r.conn = conn
	r.connMu.Unlock()

	r.mu.Lock()
	defer r.mu.Unlock()
	r.connected = true
	for k := 0; k < NumKeys; k++ {
		if in := r.ownerLocked(k); in != nil {
			if st, ok := in.state[k]; ok {
				r.sendLocked(st.Messages(k)...)
			}
		}
	}
}

func (r *Runner) detach() {
	r.mu.Lock()
	r.connected = false
	r.mu.Unlock()
	r.connMu.Lock()
	r.conn = nil
	r.connMu.Unlock()
}

// sendLocked queues msgs for macropadd. Call with r.mu held. While
// disconnected it sends nothing: the cache holds the state, and attach
// repaints it.
func (r *Runner) sendLocked(msgs ...plugin.Message) {
	if !r.connected {
		return
	}
	for _, m := range msgs {
		select {
		case r.out <- m:
		default:
			r.opt.Logf("host: send queue full, dropped a %s", m.Kind)
		}
	}
}

// writeLoop writes queued messages to the current connection.
func (r *Runner) writeLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case m := <-r.out:
			r.connMu.Lock()
			c := r.conn
			r.connMu.Unlock()
			if c == nil {
				continue
			}
			if err := c.Send(m); err != nil {
				r.opt.Logf("host: send %s: %v", m.Kind, err)
			}
		}
	}
}

// readLoop routes every message from conn until it errors.
func (r *Runner) readLoop(conn Conn) error {
	for {
		msg, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		r.route(msg)
	}
}

// route hands one message from macropadd to the instances it concerns.
// setKeyState and setCustomGlyph rebroadcasts, the host's own writes
// among them, concern no instance.
func (r *Runner) route(msg plugin.Message) {
	switch msg.Kind {
	case plugin.KindEvent:
		if msg.Event == nil {
			return
		}
		key := int(msg.Event.KeyIndex)
		in := r.owner(key)
		if in == nil {
			return
		}
		switch msg.Event.Type {
		case "press":
			in.dispatchKey(in.handlers.press(), key)
		case "release":
			in.dispatchKey(in.handlers.release(), key)
		}

	case plugin.KindSignal:
		if msg.Signal == nil {
			return
		}
		s := *msg.Signal
		key := int(s.KeyIndex)
		if kind, ok := clickSignals[s.Name]; ok {
			if in := r.owner(key); in != nil {
				in.dispatchKey(in.handlers.click(kind), key)
			}
			return
		}
		owners := r.ownersOnAnyPage(key)
		if len(owners) == 0 {
			owners = r.instances
		}
		for _, in := range owners {
			for _, fn := range in.handlers.signal(s.Name) {
				fn := fn
				in.enqueue(func() { fn(key, s) })
			}
		}
	}
}

// owner returns the instance that owns key on the active page, or nil.
func (r *Runner) owner(key int) *instance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ownerLocked(key)
}

func (r *Runner) ownerLocked(key int) *instance {
	for _, in := range r.instances {
		if in.owns(key) && (in.page == EveryPage || in.page == r.active) {
			return in
		}
	}
	return nil
}

// ownersOnAnyPage returns every instance that owns key, on any page.
func (r *Runner) ownersOnAnyPage(key int) []*instance {
	var out []*instance
	for _, in := range r.instances {
		if in.owns(key) {
			out = append(out, in)
		}
	}
	return out
}

// visibleLocked reports whether in's page shows now. Call with r.mu held.
func (r *Runner) visibleLocked(in *instance) bool {
	return in.page == EveryPage || in.page == r.active
}

// showPage switches the active page: it repaints every key whose owner
// changes (clearing a key nobody owns on the new page), tells instances
// leaving or entering view, tells every instance the new page, and
// broadcasts SignalPageChanged.
func (r *Runner) showPage(name string) error {
	idx := -1
	for i, pg := range r.pages {
		if pg == name {
			idx = i
		}
	}
	if idx < 0 {
		return fmt.Errorf("host: no page %q", name)
	}

	r.mu.Lock()
	if r.active == name {
		r.mu.Unlock()
		return nil
	}
	var before [NumKeys]*instance
	for k := range before {
		before[k] = r.ownerLocked(k)
	}
	old := r.active
	r.active = name
	for k := 0; k < NumKeys; k++ {
		now := r.ownerLocked(k)
		if now == before[k] {
			continue
		}
		st := KeyState{}
		if now != nil {
			st = now.state[k] // the zero value clears a key with no state yet
		}
		r.sendLocked(st.Messages(k)...)
	}
	r.sendLocked(plugin.Message{
		Kind:   plugin.KindSignal,
		Signal: &plugin.SignalPayload{KeyIndex: byte(idx), Name: SignalPageChanged},
	})
	r.mu.Unlock()

	for _, in := range r.instances {
		switch in.page {
		case old:
			in.dispatchBool(in.handlers.visible(), false)
		case name:
			in.dispatchBool(in.handlers.visible(), true)
		}
		for _, fn := range in.handlers.pageChange() {
			fn := fn
			in.enqueue(func() { fn(name) })
		}
	}
	return nil
}

// publish delivers v to every subscriber of topic.
func (r *Runner) publish(topic string, v any) {
	for _, in := range r.instances {
		for _, fn := range in.handlers.subscribers(topic) {
			fn := fn
			in.enqueue(func() { fn(v) })
		}
	}
}

// safeCall runs fn, logging a panic with id and a stack instead of
// letting it stop the host.
func (r *Runner) safeCall(id string, fn func()) {
	defer func() {
		if p := recover(); p != nil {
			r.opt.Logf("[%s] panic: %v\n%s", id, p, debug.Stack())
		}
	}()
	fn()
}
