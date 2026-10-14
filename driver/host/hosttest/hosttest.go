// Package hosttest is a fake host.Host for plugin tests. Handlers run
// synchronously on the test's goroutine, so a test sends input and
// checks the keys with no connection, no timing, and no locks. See task
// 0055.
package hosttest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/host"
	"github.com/kgheacock/macro-pad/driver/host/macos"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// Host is a fake host.Host owning a fixed set of keys.
type Host struct {
	t     testing.TB
	ctx   context.Context
	id    string
	keys  []int
	raw   json.RawMessage
	state map[int]host.KeyState

	visible bool
	page    string
	pages   []string
	perms   macos.PermissionState

	press, release []func(int)
	click          map[host.ClickKind][]func(int)
	signal         map[string][]func(int, plugin.SignalPayload)
	onVisible      []func(bool)
	onPage         []func(string)
	subs           map[string][]func(any)
	every          []func()

	// Published records every Publish call, in order.
	Published []Publication
}

// Publication is one recorded Publish call.
type Publication struct {
	Topic string
	Value any
}

// New returns a fake host owning keys, visible, on the one page
// host.DefaultPage, with every macOS grant held.
func New(t testing.TB, keys ...int) *Host {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return &Host{
		t:       t,
		ctx:     ctx,
		id:      "test",
		keys:    keys,
		state:   map[int]host.KeyState{},
		visible: true,
		page:    host.DefaultPage,
		pages:   []string{host.DefaultPage},
		perms:   macos.PermissionState{Supported: true, Accessibility: true, ScreenRecording: true},
		click:   map[host.ClickKind][]func(int){},
		signal:  map[string][]func(int, plugin.SignalPayload){},
		subs:    map[string][]func(any){},
	}
}

// SetConfig sets what Config decodes. v is marshaled to JSON.
func (h *Host) SetConfig(v any) *Host {
	b, err := json.Marshal(v)
	if err != nil {
		h.t.Fatal(err)
	}
	h.raw = b
	return h
}

// SetPages sets the page list; the first page shows.
func (h *Host) SetPages(pages ...string) *Host {
	h.pages = pages
	h.page = pages[0]
	return h
}

// SetPermissions sets what Require checks against.
func (h *Host) SetPermissions(p macos.PermissionState) *Host {
	h.perms = p
	return h
}

// Start calls p.Start with this host.
func (h *Host) Start(p host.Plugin) error {
	return p.Start(h.ctx, h)
}

// Input.

// Press delivers a raw press on key.
func (h *Host) Press(key int) { h.each(h.press, key) }

// Release delivers a raw release on key.
func (h *Host) Release(key int) { h.each(h.release, key) }

// Click delivers a resolved click on key.
func (h *Host) Click(key int, kind host.ClickKind) { h.each(h.click[kind], key) }

// Signal delivers a signal named name on key.
func (h *Host) Signal(key int, name string) {
	for _, fn := range h.signal[name] {
		fn(key, plugin.SignalPayload{KeyIndex: byte(key), Name: name})
	}
}

// Deliver runs every Subscribe handler for topic with v, as another
// instance's Publish would.
func (h *Host) Deliver(topic string, v any) {
	for _, fn := range h.subs[topic] {
		fn(v)
	}
}

// SetVisible shows or hides the instance's page and runs its OnVisible
// handlers.
func (h *Host) SetVisible(v bool) {
	h.visible = v
	for _, fn := range h.onVisible {
		fn(v)
	}
}

// Tick runs every function passed to Every once.
func (h *Host) Tick() {
	for _, fn := range h.every {
		fn()
	}
}

func (h *Host) each(fns []func(int), key int) {
	if !h.visible || !slices.Contains(h.keys, key) {
		return
	}
	for _, fn := range fns {
		fn(key)
	}
}

// Output.

// KeyState returns what the plugin last set on key.
func (h *Host) KeyState(key int) host.KeyState { return h.state[key] }

// host.Host.

func (h *Host) ID() string      { return h.id }
func (h *Host) Keys() []int     { return slices.Clone(h.keys) }
func (h *Host) Page() string    { return h.page }
func (h *Host) Pages() []string { return slices.Clone(h.pages) }
func (h *Host) Visible() bool   { return h.visible }

func (h *Host) Config(v any) error {
	if len(h.raw) == 0 {
		return nil
	}
	// Decode exactly as the host does, so a config typo fails here too.
	dec := json.NewDecoder(bytes.NewReader(h.raw))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func (h *Host) Log(format string, args ...any) {
	h.t.Logf("[%s] "+format, append([]any{h.id}, args...)...)
}

func (h *Host) Key(i int) host.Key {
	if !slices.Contains(h.keys, i) {
		return host.NotOwnedKey
	}
	return host.NewKey(func(change func(*host.KeyState) error) error {
		st := h.state[i]
		if err := change(&st); err != nil {
			return err
		}
		h.state[i] = st
		return nil
	})
}

func (h *Host) OnPress(fn func(int))   { h.press = append(h.press, fn) }
func (h *Host) OnRelease(fn func(int)) { h.release = append(h.release, fn) }
func (h *Host) OnClick(kind host.ClickKind, fn func(int)) {
	h.click[kind] = append(h.click[kind], fn)
}
func (h *Host) OnSignal(name string, fn func(int, plugin.SignalPayload)) {
	h.signal[name] = append(h.signal[name], fn)
}
func (h *Host) OnVisible(fn func(bool))      { h.onVisible = append(h.onVisible, fn) }
func (h *Host) OnPageChange(fn func(string)) { h.onPage = append(h.onPage, fn) }

// ShowPage switches the fake's page and runs every OnPageChange handler.
// The instance stays visible.
func (h *Host) ShowPage(name string) error {
	if !slices.Contains(h.pages, name) {
		return fmt.Errorf("hosttest: no page %q", name)
	}
	h.page = name
	for _, fn := range h.onPage {
		fn(name)
	}
	return nil
}

func (h *Host) Publish(topic string, v any) {
	h.Published = append(h.Published, Publication{Topic: topic, Value: v})
}

func (h *Host) Subscribe(topic string, fn func(any)) { h.subs[topic] = append(h.subs[topic], fn) }

// Go runs fn on a goroutine, with a context that ends when the test does.
func (h *Host) Go(fn func(context.Context)) { go fn(h.ctx) }

// Every records fn; Tick runs it.
func (h *Host) Every(d time.Duration, fn func()) { h.every = append(h.every, fn) }

func (h *Host) Require(p macos.Permission) error { return host.RequireFrom(h.perms, p) }

var _ host.Host = (*Host)(nil)
