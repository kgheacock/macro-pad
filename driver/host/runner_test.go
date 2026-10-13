package host

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/host/keyimg"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// fakeConn is one in-memory connection to a fake macropadd. A test feeds
// it messages with deliver and reads what the host sent with sent.
type fakeConn struct {
	in     chan plugin.Message
	closed chan struct{}
	once   sync.Once

	mu  sync.Mutex
	out []plugin.Message
}

func newFakeConn() *fakeConn {
	return &fakeConn{in: make(chan plugin.Message, 64), closed: make(chan struct{})}
}

func (c *fakeConn) ReadMessage() (plugin.Message, error) {
	select {
	case m := <-c.in:
		return m, nil
	case <-c.closed:
		return plugin.Message{}, errors.New("closed")
	}
}

func (c *fakeConn) Send(m plugin.Message) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.out = append(c.out, m)
	return nil
}

func (c *fakeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *fakeConn) sent() []plugin.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]plugin.Message(nil), c.out...)
}

func (c *fakeConn) press(key int) {
	c.in <- plugin.Message{Kind: plugin.KindEvent, Event: &plugin.EventPayload{KeyIndex: byte(key), Type: "press"}}
}

func (c *fakeConn) signal(key int, name string) {
	c.in <- plugin.Message{Kind: plugin.KindSignal, Signal: &plugin.SignalPayload{KeyIndex: byte(key), Name: name}}
}

// keyStates returns the setKeyState payloads conn received for key.
func keyStates(c *fakeConn, key int) []plugin.SetKeyStatePayload {
	var out []plugin.SetKeyStatePayload
	for _, m := range c.sent() {
		if m.Kind == plugin.KindSetKeyState && int(m.SetKeyState.KeyIndex) == key {
			out = append(out, *m.SetKeyState)
		}
	}
	return out
}

// funcPlugin is a Plugin whose Start is a function.
type funcPlugin func(ctx context.Context, h Host) error

func (f funcPlugin) Start(ctx context.Context, h Host) error { return f(ctx, h) }

// harness runs a Runner against fake connections.
type harness struct {
	t     *testing.T
	r     *Runner
	conns chan *fakeConn
	hosts sync.Map // instance id -> Host

	logMu sync.Mutex
	logs  []string
}

// run builds and runs a Runner for cfg. starts maps an instance id to
// what its Start does; an id with no entry starts with no handlers.
func run(t *testing.T, cfg Config, starts map[string]func(h Host) error) (*harness, *fakeConn) {
	t.Helper()
	hs := &harness{t: t, conns: make(chan *fakeConn, 4)}
	var factories []Factory
	for i := range cfg.Plugins {
		id := cfg.Plugins[i].ID
		use := "test-" + id
		cfg.Plugins[i].Use = use
		factories = append(factories, Factory{Name: use, New: func() Plugin {
			return funcPlugin(func(ctx context.Context, h Host) error {
				hs.hosts.Store(id, h)
				if f := starts[id]; f != nil {
					return f(h)
				}
				return nil
			})
		}})
	}
	r, err := New(cfg, factories, Options{
		Dial: func(ctx context.Context) (Conn, error) {
			select {
			case c := <-hs.conns:
				return c, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		},
		Logf: func(format string, args ...any) {
			hs.logMu.Lock()
			hs.logs = append(hs.logs, fmt.Sprintf(format, args...))
			hs.logMu.Unlock()
		},
		MinBackoff: time.Millisecond,
		MaxBackoff: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	hs.r = r
	conn := newFakeConn()
	hs.conns <- conn

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	waitFor(t, "connect", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.connected })
	return hs, conn
}

func (hs *harness) host(id string) Host {
	v, ok := hs.hosts.Load(id)
	if !ok {
		hs.t.Fatalf("no instance %q", id)
	}
	return v.(Host)
}

func (hs *harness) logged(substr string) bool {
	hs.logMu.Lock()
	defer hs.logMu.Unlock()
	for _, l := range hs.logs {
		if strings.Contains(l, substr) {
			return true
		}
	}
	return false
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

// recorder collects values from handlers running on other goroutines.
type recorder[T any] struct {
	mu   sync.Mutex
	vals []T
}

func (r *recorder[T]) add(v T) { r.mu.Lock(); r.vals = append(r.vals, v); r.mu.Unlock() }

func (r *recorder[T]) get() []T {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]T(nil), r.vals...)
}

func (r *recorder[T]) len() int { return len(r.get()) }

func TestRouter_RoutesToOwner(t *testing.T) {
	var a, b, c recorder[int]
	cfg := Config{Plugins: []InstanceConfig{
		{ID: "a", Keys: []int{0, 1}},
		{ID: "b", Keys: []int{2}},
		{ID: "c", Page: "other", Keys: []int{0}},
	}}
	_, conn := run(t, cfg, map[string]func(Host) error{
		"a": func(h Host) error { h.OnPress(a.add); return nil },
		"b": func(h Host) error { h.OnPress(b.add); return nil },
		"c": func(h Host) error { h.OnPress(c.add); return nil },
	})

	conn.press(0)
	conn.press(2)
	conn.press(5) // unowned
	conn.press(1)
	waitFor(t, "presses", func() bool { return a.len() == 2 && b.len() == 1 })
	time.Sleep(20 * time.Millisecond)

	if got := a.get(); got[0] != 0 || got[1] != 1 {
		t.Errorf("a got %v, want [0 1]", got)
	}
	if got := b.get(); got[0] != 2 {
		t.Errorf("b got %v, want [2]", got)
	}
	if got := c.get(); len(got) != 0 {
		t.Errorf("c, on a hidden page, got %v, want nothing", got)
	}
}

func TestRouter_ClickGoesToActiveOwner(t *testing.T) {
	var single, double recorder[int]
	cfg := Config{Plugins: []InstanceConfig{{ID: "a", Keys: []int{3}}}}
	_, conn := run(t, cfg, map[string]func(Host) error{
		"a": func(h Host) error {
			h.OnClick(Single, single.add)
			h.OnClick(Double, double.add)
			return nil
		},
	})
	conn.signal(3, plugin.SignalDoublePress)
	conn.signal(3, plugin.SignalSinglePress)
	waitFor(t, "clicks", func() bool { return single.len() == 1 && double.len() == 1 })
}

func TestRouter_SignalReachesHiddenOwner(t *testing.T) {
	var got recorder[string]
	cfg := Config{Plugins: []InstanceConfig{
		{ID: "shown", Page: "one", Keys: []int{4}},
		{ID: "hidden", Page: "two", Keys: []int{4}},
		{ID: "listener", Page: "one"},
	}}
	_, conn := run(t, cfg, map[string]func(Host) error{
		"hidden": func(h Host) error {
			h.OnSignal(plugin.SignalProcessWaiting, func(k int, s plugin.SignalPayload) { got.add(fmt.Sprintf("hidden %d", k)) })
			return nil
		},
		"listener": func(h Host) error {
			h.OnSignal(plugin.SignalProcessWaiting, func(k int, s plugin.SignalPayload) { got.add(fmt.Sprintf("listener %d", k)) })
			return nil
		},
	})
	conn.signal(4, plugin.SignalProcessWaiting) // owned: owners only
	conn.signal(5, plugin.SignalProcessWaiting) // unowned: every listener
	waitFor(t, "signals", func() bool { return got.len() == 3 })
	time.Sleep(20 * time.Millisecond)
	want := map[string]bool{"hidden 4": true, "listener 5": true, "hidden 5": true}
	for _, g := range got.get() {
		if !want[g] {
			t.Errorf("unexpected delivery %q; got %v", g, got.get())
		}
	}
}

func TestKey_RejectsNonOwner(t *testing.T) {
	cfg := Config{Plugins: []InstanceConfig{{ID: "a", Keys: []int{0}}}}
	hs, conn := run(t, cfg, nil)
	h := hs.host("a")

	k := h.Key(3)
	for name, err := range map[string]error{
		"SetColor": k.SetColor(keyimg.MustHex("#FF0000")),
		"SetState": k.SetState("Done"),
		"SetBlink": k.SetBlink(true),
		"Clear":    k.Clear(),
	} {
		if !errors.Is(err, ErrNotOwner) {
			t.Errorf("%s on an unowned key = %v, want ErrNotOwner", name, err)
		}
	}
	if err := h.Key(0).SetColor(keyimg.MustHex("#FF0000")); err != nil {
		t.Fatalf("SetColor on an owned key: %v", err)
	}
	waitFor(t, "key 0 write", func() bool { return len(keyStates(conn, 0)) == 1 })
	for _, m := range conn.sent() {
		if m.SetKeyState != nil && m.SetKeyState.KeyIndex == 3 {
			t.Errorf("sent %+v for unowned key 3", *m.SetKeyState)
		}
	}
}

func TestPages(t *testing.T) {
	var vis recorder[string]
	var pages recorder[string]
	cfg := Config{StartPage: "one", Plugins: []InstanceConfig{
		{ID: "a", Page: "one", Keys: []int{0}},
		{ID: "b", Page: "two", Keys: []int{0}},
		{ID: "c", Page: "two", Keys: []int{1}},
		{ID: "pager", Page: EveryPage, Keys: []int{5}},
	}}
	starts := map[string]func(Host) error{}
	for _, id := range []string{"a", "b", "c", "pager"} {
		id := id
		starts[id] = func(h Host) error {
			h.OnVisible(func(v bool) { vis.add(fmt.Sprintf("%s=%v", id, v)) })
			h.OnPageChange(func(p string) { pages.add(id + ":" + p) })
			return nil
		}
	}
	hs, conn := run(t, cfg, starts)

	red, blue := keyimg.MustHex("#FF0000"), keyimg.MustHex("#0000FF")
	if err := hs.host("a").Key(0).SetColor(red); err != nil {
		t.Fatal(err)
	}
	if err := hs.host("b").Key(0).SetColor(blue); err != nil { // hidden: cached only
		t.Fatal(err)
	}
	waitFor(t, "a's write", func() bool { return len(keyStates(conn, 0)) == 1 })
	time.Sleep(20 * time.Millisecond)
	if ks := keyStates(conn, 0); len(ks) != 1 || ks[0].Color != red.RGB565() {
		t.Fatalf("before the switch, key 0 got %+v, want only a's red", ks)
	}
	if hs.host("b").Visible() || !hs.host("a").Visible() || !hs.host("pager").Visible() {
		t.Fatal("Visible wrong before the switch")
	}

	if err := hs.host("pager").ShowPage("two"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "repaint", func() bool { return len(keyStates(conn, 0)) == 2 && len(keyStates(conn, 1)) == 1 })
	if ks := keyStates(conn, 0); ks[1].Color != blue.RGB565() {
		t.Errorf("after the switch, key 0 = %+v, want b's blue", ks[1])
	}
	if ks := keyStates(conn, 1); ks[0] != (plugin.SetKeyStatePayload{KeyIndex: 1}) {
		t.Errorf("after the switch, key 1 (c, no state yet) = %+v, want cleared", ks[0])
	}
	if ks := keyStates(conn, 5); len(ks) != 0 {
		t.Errorf("pager's key 5 shows on every page and should not be resent; got %+v", ks)
	}
	var sawSignal bool
	for _, m := range conn.sent() {
		sawSignal = sawSignal || (m.Kind == plugin.KindSignal && m.Signal.Name == SignalPageChanged && m.Signal.KeyIndex == 1)
	}
	if !sawSignal {
		t.Error("no pageChanged signal with keyIndex 1")
	}

	waitFor(t, "OnVisible", func() bool { return vis.len() == 3 })
	want := map[string]bool{"a=false": true, "b=true": true, "c=true": true}
	for _, v := range vis.get() {
		if !want[v] {
			t.Errorf("unexpected OnVisible %q", v)
		}
	}
	waitFor(t, "OnPageChange", func() bool { return pages.len() == 4 })
	if got := hs.host("a").Page(); got != "two" {
		t.Errorf("Page() = %q, want two", got)
	}
	if err := hs.host("a").ShowPage("nope"); err == nil {
		t.Error("ShowPage of an unknown page = nil, want an error")
	}
}

func TestRouter_RecoversPanic(t *testing.T) {
	var a, b recorder[int]
	cfg := Config{Plugins: []InstanceConfig{{ID: "a", Keys: []int{0}}, {ID: "b", Keys: []int{1}}}}
	hs, conn := run(t, cfg, map[string]func(Host) error{
		"a": func(h Host) error {
			h.OnPress(func(k int) {
				a.add(k)
				if a.len() == 1 {
					panic("boom")
				}
			})
			return nil
		},
		"b": func(h Host) error { h.OnPress(b.add); return nil },
	})
	conn.press(0)
	conn.press(1)
	conn.press(0)
	waitFor(t, "presses after a panic", func() bool { return a.len() == 2 && b.len() == 1 })
	if !hs.logged("[a] panic: boom") {
		t.Errorf("panic not logged with its instance id; logs: %v", hs.logs)
	}
}

func TestRouter_SlowPluginIsolated(t *testing.T) {
	var bAt recorder[time.Time]
	cfg := Config{Plugins: []InstanceConfig{{ID: "slow", Keys: []int{0}}, {ID: "fast", Keys: []int{1}}}}
	_, conn := run(t, cfg, map[string]func(Host) error{
		"slow": func(h Host) error { h.OnPress(func(int) { time.Sleep(time.Second) }); return nil },
		"fast": func(h Host) error { h.OnPress(func(int) { bAt.add(time.Now()) }); return nil },
	})
	conn.press(0)
	sent := time.Now()
	conn.press(1)
	waitFor(t, "fast handler", func() bool { return bAt.len() == 1 })
	if d := bAt.get()[0].Sub(sent); d > 50*time.Millisecond {
		t.Errorf("fast handler ran %v after its press, want under 50ms", d)
	}
}

func TestReconnect_Repaints(t *testing.T) {
	cfg := Config{Plugins: []InstanceConfig{
		{ID: "a", Keys: []int{0}},
		{ID: "hidden", Page: "two", Keys: []int{1}},
	}}
	hs, first := run(t, cfg, nil)
	green := keyimg.MustHex("#00FF00")
	if err := hs.host("a").Key(0).SetColor(green); err != nil {
		t.Fatal(err)
	}
	if err := hs.host("hidden").Key(1).SetState("Waiting"); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "first write", func() bool { return len(keyStates(first, 0)) == 1 })

	second := newFakeConn()
	hs.conns <- second
	first.Close()
	waitFor(t, "repaint on the new connection", func() bool { return len(keyStates(second, 0)) == 1 })
	if ks := keyStates(second, 0)[0]; ks.Color != green.RGB565() {
		t.Errorf("repainted key 0 = %+v, want green", ks)
	}
	time.Sleep(20 * time.Millisecond)
	if ks := keyStates(second, 1); len(ks) != 0 {
		t.Errorf("hidden key 1 repainted: %+v", ks)
	}
}

func TestBus(t *testing.T) {
	var b, c recorder[any]
	cfg := Config{Plugins: []InstanceConfig{{ID: "a"}, {ID: "b"}, {ID: "c"}}}
	hs, _ := run(t, cfg, map[string]func(Host) error{
		"b": func(h Host) error { h.Subscribe("t", b.add); return nil },
		"c": func(h Host) error { h.Subscribe("t", c.add); h.Subscribe("other", c.add); return nil },
	})
	hs.host("a").Publish("t", 42)
	waitFor(t, "delivery", func() bool { return b.len() == 1 && c.len() == 1 })
	if b.get()[0] != 42 || c.get()[0] != 42 {
		t.Errorf("got %v and %v, want 42", b.get(), c.get())
	}
}

func TestStartError_DisablesOnlyThatInstance(t *testing.T) {
	var a, b recorder[int]
	cfg := Config{Plugins: []InstanceConfig{{ID: "a", Keys: []int{0}}, {ID: "b", Keys: []int{1}}}}
	hs, conn := run(t, cfg, map[string]func(Host) error{
		"a": func(h Host) error { h.OnPress(a.add); return errors.New("no grant") },
		"b": func(h Host) error { h.OnPress(b.add); return nil },
	})
	conn.press(0)
	conn.press(1)
	waitFor(t, "b", func() bool { return b.len() == 1 })
	time.Sleep(20 * time.Millisecond)
	if a.len() != 0 {
		t.Error("a disabled instance received a press")
	}
	if !hs.logged("[a] disabled: no grant") {
		t.Error("disable not logged")
	}
}

func TestEvery_RunsOnInstanceGoroutine(t *testing.T) {
	var n recorder[int]
	cfg := Config{Plugins: []InstanceConfig{{ID: "a"}}}
	run(t, cfg, map[string]func(Host) error{
		"a": func(h Host) error { h.Every(time.Millisecond, func() { n.add(1) }); return nil },
	})
	waitFor(t, "ticks", func() bool { return n.len() >= 3 })
}

func TestConfig_Validate(t *testing.T) {
	cfg, err := ParseConfig([]byte(`{
		"startPage": "missing",
		"plugins": [
			{"id": "a", "use": "x", "keys": [0, 1]},
			{"id": "a", "use": "x", "keys": [1]},
			{"id": "b", "use": "nope", "keys": [7]},
			{"id": "p", "use": "x", "page": "*", "keys": [0]}
		]
	}`))
	if err != nil {
		t.Fatal(err)
	}
	err = cfg.Validate([]Factory{{Name: "x"}})
	if err == nil {
		t.Fatal("Validate = nil, want errors")
	}
	for _, want := range []string{
		"a: duplicate id",
		"key 1 on page \"main\" is already owned by a",
		"unknown plugin \"nope\"",
		"key 7 is outside 0..5",
		"key 0 on page \"main\" is already owned by p on every page",
		"startPage \"missing\"",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("errors miss %q:\n%v", want, err)
		}
	}
}

func TestConfig_PagesInOrder(t *testing.T) {
	cfg := Config{Plugins: []InstanceConfig{{Page: "b"}, {Page: "*"}, {}, {Page: "b"}}}
	if got := strings.Join(cfg.Pages(), ","); got != "b,main" {
		t.Errorf("Pages = %s, want b,main", got)
	}
}
