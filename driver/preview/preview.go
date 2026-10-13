package preview

import (
	"context"
	"errors"
	"image"
	"sync"
	"time"
)

// Sink receives each key's new picture. *api.Conn satisfies it.
type Sink interface {
	SetCustomGlyph(key int, pngBytes []byte) error
}

// Options tunes a Previewer. A zero field takes its default.
type Options struct {
	// Interval is how often every mapped window is captured. Default 5s.
	// Each changed key costs one 32 KB push to the board, so a shorter
	// interval mostly queues pushes behind each other.
	Interval time.Duration
	// FocusPoll is how often the frontmost window is checked. When it
	// changes, the window just left is captured at once, so its snapshot
	// is as fresh as possible before it is minimized or its desktop is
	// switched away. Default 500ms.
	FocusPoll time.Duration
	// MinChange is the share of pixels that must change on the key's
	// screen before a new picture is sent. Default 0.005 (82 pixels), so
	// a blinking cursor alone does not cost a push.
	MinChange float64
	// Logf receives one line per problem. Default: discard.
	Logf func(format string, args ...any)
}

func (o Options) withDefaults() Options {
	if o.Interval <= 0 {
		o.Interval = 5 * time.Second
	}
	if o.FocusPoll <= 0 {
		o.FocusPoll = 500 * time.Millisecond
	}
	if o.MinChange <= 0 {
		o.MinChange = 0.005
	}
	if o.Logf == nil {
		o.Logf = func(string, ...any) {}
	}
	return o
}

// keyState is what a Previewer remembers about one key.
type keyState struct {
	window   WindowID    // window the key's selector resolved to last
	snapshot *image.RGBA // last live thumbnail of window
	shown    *image.RGBA // last picture sent to the key
	missing  bool        // a "no window matches" line is already logged
}

// Previewer keeps each target key showing its window.
type Previewer struct {
	cap  Capturer
	sink Sink
	opt  Options

	mu      sync.Mutex
	targets []Target
	keys    map[int]*keyState
	focused WindowID
}

// New returns a Previewer showing targets through sink.
func New(c Capturer, sink Sink, targets []Target, opt Options) *Previewer {
	return &Previewer{
		cap:     c,
		sink:    sink,
		opt:     opt.withDefaults(),
		targets: append([]Target(nil), targets...),
		keys:    make(map[int]*keyState),
	}
}

// SetTargets replaces the key-to-window mapping. A key whose window
// changes forgets its old snapshot on the next refresh.
func (p *Previewer) SetTargets(targets []Target) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.targets = append([]Target(nil), targets...)
}

// Run refreshes every key at once, then every Interval, and checks focus
// every FocusPoll, until ctx is done.
func (p *Previewer) Run(ctx context.Context) error {
	p.Refresh()
	refresh := time.NewTicker(p.opt.Interval)
	defer refresh.Stop()
	focus := time.NewTicker(p.opt.FocusPoll)
	defer focus.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-refresh.C:
			p.Refresh()
		case <-focus.C:
			p.CheckFocus()
		}
	}
}

// Refresh captures every target's window and sends each key whose
// picture changed. A window that is not on screen, or fails to capture,
// shows its last snapshot, dimmed (see Stale).
func (p *Previewer) Refresh() {
	p.mu.Lock()
	defer p.mu.Unlock()
	windows, err := p.cap.Windows()
	if err != nil {
		p.opt.Logf("preview: window list: %v", err)
		return
	}
	for _, t := range p.targets {
		p.refreshKey(t, windows)
	}
}

// CheckFocus reads the frontmost window. When it changed since the last
// check, the window that lost focus is captured now: this is the
// snapshot a key keeps if that window is then minimized or left on
// another desktop.
func (p *Previewer) CheckFocus() {
	p.mu.Lock()
	defer p.mu.Unlock()
	id, err := p.cap.Focused()
	if err != nil {
		p.opt.Logf("preview: focused window: %v", err)
		return
	}
	left := p.focused
	p.focused = id
	if left == 0 || left == id {
		return
	}
	p.snapshotLocked(left)
}

// SnapshotWindow captures id now and sends it to every key showing it. A
// switcher calls it just before it raises another window or switches
// desktop, so the key keeps a picture taken a moment before the window
// was hidden.
func (p *Previewer) SnapshotWindow(id WindowID) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.snapshotLocked(id)
}

func (p *Previewer) snapshotLocked(id WindowID) {
	var hit []Target
	for _, t := range p.targets {
		if ks := p.keys[t.Key]; ks != nil && ks.window == id {
			hit = append(hit, t)
		}
	}
	if len(hit) == 0 {
		return
	}
	windows, err := p.cap.Windows()
	if err != nil {
		p.opt.Logf("preview: window list: %v", err)
		return
	}
	for _, t := range hit {
		p.refreshKey(t, windows)
	}
}

func (p *Previewer) refreshKey(t Target, windows []Window) {
	ks := p.keys[t.Key]
	if ks == nil {
		ks = &keyState{}
		p.keys[t.Key] = ks
	}
	w, ok := t.Selector.resolve(windows, ks.window)
	if !ok {
		if !ks.missing {
			p.opt.Logf("preview: key %d: no window matches %+v", t.Key, t.Selector)
			ks.missing = true
		}
		return
	}
	ks.missing = false
	if w.ID != ks.window {
		ks.window = w.ID
		ks.snapshot = nil
	}

	if w.OnScreen {
		thumb, err := p.capture(w.ID)
		if err == nil {
			ks.snapshot = thumb
			p.show(t.Key, ks, thumb)
			return
		}
		p.opt.Logf("preview: key %d: capture window %d (%s): %v", t.Key, w.ID, w.App, err)
	}
	if ks.snapshot != nil {
		p.show(t.Key, ks, Stale(ks.snapshot))
	}
}

// errBlank marks a capture with nothing in it, which ScreenCaptureKit can
// return for a window in the middle of hiding.
var errBlank = errors.New("blank capture")

func (p *Previewer) capture(id WindowID) (*image.RGBA, error) {
	// Twice the key's size, so Thumbnail's averaging smooths text.
	img, err := p.cap.Capture(id, 2*Size, 2*Size)
	if err != nil {
		return nil, err
	}
	thumb := Thumbnail(img)
	if isBlack(thumb) {
		return nil, errBlank
	}
	return thumb, nil
}

func (p *Previewer) show(key int, ks *keyState, img *image.RGBA) {
	if ks.shown != nil && changedFraction(ks.shown, img) < p.opt.MinChange {
		return
	}
	data, err := EncodePNG(img)
	if err != nil {
		p.opt.Logf("preview: key %d: encode: %v", key, err)
		return
	}
	if err := p.sink.SetCustomGlyph(key, data); err != nil {
		p.opt.Logf("preview: key %d: send: %v", key, err)
		return
	}
	ks.shown = img
}

func isBlack(img *image.RGBA) bool {
	for i := 0; i < len(img.Pix); i += 4 {
		if img.Pix[i] != 0 || img.Pix[i+1] != 0 || img.Pix[i+2] != 0 {
			return false
		}
	}
	return true
}
