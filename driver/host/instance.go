package host

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kgheacock/macro-pad/driver/host/macos"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// instance is one configured plugin instance, and the Host it is handed.
type instance struct {
	r      *Runner
	id     string
	page   string
	keys   []int
	raw    json.RawMessage
	plugin Plugin

	queue    chan func()
	disabled atomic.Bool
	handlers handlers
	// ctx ends when the host stops or the instance is disabled, which
	// stops its Go and Every goroutines.
	ctx    context.Context
	cancel context.CancelFunc

	// state is the last state set per owned key. Guarded by r.mu.
	state map[int]KeyState
}

func newInstance(r *Runner, ic InstanceConfig, p Plugin) *instance {
	return &instance{
		r:      r,
		id:     ic.ID,
		page:   ic.page(),
		keys:   slices.Clone(ic.Keys),
		raw:    ic.Config,
		plugin: p,
		queue:  make(chan func(), instanceQueueSize),
		state:  map[int]KeyState{},
	}
}

// start calls the plugin's Start. An error or a panic disables the
// instance.
func (in *instance) start(ctx context.Context) {
	in.ctx, in.cancel = context.WithCancel(ctx)
	var err error
	ok := false
	in.r.safeCall(in.id, func() {
		err = in.plugin.Start(in.ctx, in)
		ok = true
	})
	if !ok {
		err = fmt.Errorf("start panicked")
	}
	if err != nil {
		in.disable(err)
	}
}

// disable turns the instance off: it gets no more handler calls, its Go
// and Every goroutines see their context end, and its Key calls fail.
func (in *instance) disable(err error) {
	in.disabled.Store(true)
	in.cancel()
	in.r.opt.Logf("[%s] disabled: %v", in.id, err)
}

// loop runs the instance's handler calls one at a time until ctx is done.
func (in *instance) loop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case fn := <-in.queue:
			in.r.safeCall(in.id, fn)
		}
	}
}

// enqueue queues fn on the instance's goroutine. A disabled instance gets
// nothing; a full queue drops fn and logs it.
func (in *instance) enqueue(fn func()) {
	if in.disabled.Load() {
		return
	}
	select {
	case in.queue <- fn:
	default:
		in.r.opt.Logf("[%s] queue full, dropped a call", in.id)
	}
}

func (in *instance) dispatchKey(fns []func(int), key int) {
	for _, fn := range fns {
		fn := fn
		in.enqueue(func() { fn(key) })
	}
}

func (in *instance) dispatchBool(fns []func(bool), v bool) {
	for _, fn := range fns {
		fn := fn
		in.enqueue(func() { fn(v) })
	}
}

func (in *instance) owns(key int) bool { return slices.Contains(in.keys, key) }

// Host implementation.

func (in *instance) ID() string   { return in.id }
func (in *instance) Keys() []int  { return slices.Clone(in.keys) }
func (in *instance) Page() string { in.r.mu.Lock(); defer in.r.mu.Unlock(); return in.r.active }

func (in *instance) Pages() []string { return slices.Clone(in.r.pages) }

func (in *instance) Config(v any) error {
	if len(in.raw) == 0 {
		return nil
	}
	dec := json.NewDecoder(bytes.NewReader(in.raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("instance %q: config: %w", in.id, err)
	}
	return nil
}

func (in *instance) Log(format string, args ...any) {
	in.r.opt.Logf("[%s] "+format, append([]any{in.id}, args...)...)
}

func (in *instance) Key(i int) Key {
	if !in.owns(i) {
		return NotOwnedKey
	}
	return NewKey(func(change func(*KeyState) error) error {
		if in.disabled.Load() {
			return ErrDisabled
		}
		in.r.mu.Lock()
		defer in.r.mu.Unlock()
		st := in.state[i]
		if err := change(&st); err != nil {
			return err
		}
		in.state[i] = st
		if in.r.ownerLocked(i) == in {
			in.r.sendLocked(st.Messages(i)...)
		}
		return nil
	})
}

func (in *instance) OnPress(fn func(int)) {
	in.handlers.add(func(h *handlerSet) { h.press = append(h.press, fn) })
}
func (in *instance) OnRelease(fn func(int)) {
	in.handlers.add(func(h *handlerSet) { h.release = append(h.release, fn) })
}

func (in *instance) OnClick(kind ClickKind, fn func(int)) {
	in.handlers.add(func(h *handlerSet) {
		if h.click == nil {
			h.click = map[ClickKind][]func(int){}
		}
		h.click[kind] = append(h.click[kind], fn)
	})
}

func (in *instance) OnSignal(name string, fn func(int, plugin.SignalPayload)) {
	in.handlers.add(func(h *handlerSet) {
		if h.signal == nil {
			h.signal = map[string][]func(int, plugin.SignalPayload){}
		}
		h.signal[name] = append(h.signal[name], fn)
	})
}

func (in *instance) Visible() bool {
	in.r.mu.Lock()
	defer in.r.mu.Unlock()
	return in.r.visibleLocked(in)
}

func (in *instance) OnVisible(fn func(bool)) {
	in.handlers.add(func(h *handlerSet) { h.visible = append(h.visible, fn) })
}

func (in *instance) ShowPage(name string) error { return in.r.showPage(name) }

func (in *instance) OnPageChange(fn func(string)) {
	in.handlers.add(func(h *handlerSet) { h.pageChange = append(h.pageChange, fn) })
}

func (in *instance) Publish(topic string, v any) { in.r.publish(topic, v) }

func (in *instance) Subscribe(topic string, fn func(any)) {
	in.handlers.add(func(h *handlerSet) {
		if h.subs == nil {
			h.subs = map[string][]func(any){}
		}
		h.subs[topic] = append(h.subs[topic], fn)
	})
}

func (in *instance) Go(fn func(context.Context)) {
	ctx := in.ctx
	go in.r.safeCall(in.id, func() { fn(ctx) })
}

func (in *instance) Every(d time.Duration, fn func()) {
	in.Go(func(ctx context.Context) {
		t := time.NewTicker(d)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				in.enqueue(fn)
			}
		}
	})
}

func (in *instance) Require(p macos.Permission) error {
	return RequireFrom(in.r.opt.Permissions(), p)
}

// RequireFrom is Host.Require against a given PermissionState. hosttest's
// fake uses it too.
func RequireFrom(s macos.PermissionState, p macos.Permission) error {
	if !s.Supported {
		return fmt.Errorf("needs %s, which needs macOS: %w", p, macos.ErrUnsupported)
	}
	if !s.Has(p) {
		return fmt.Errorf("needs %s: grant it to %s in System Settings > Privacy & Security > %s, then restart the host", p, macos.Executable(), p)
	}
	return nil
}

// handlers holds an instance's registered handlers. A plugin registers
// them in Start, but may add more later, so reads take a copy under mu.
type handlers struct {
	mu sync.Mutex
	h  handlerSet
}

type handlerSet struct {
	press, release []func(int)
	click          map[ClickKind][]func(int)
	signal         map[string][]func(int, plugin.SignalPayload)
	visible        []func(bool)
	pageChange     []func(string)
	subs           map[string][]func(any)
}

func (h *handlers) add(f func(*handlerSet)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(&h.h)
}

func (h *handlers) press() []func(int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.press)
}

func (h *handlers) release() []func(int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.release)
}

func (h *handlers) click(kind ClickKind) []func(int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.click[kind])
}

func (h *handlers) signal(name string) []func(int, plugin.SignalPayload) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.signal[name])
}

func (h *handlers) visible() []func(bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.visible)
}

func (h *handlers) pageChange() []func(string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.pageChange)
}

func (h *handlers) subscribers(topic string) []func(any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.h.subs[topic])
}
