package layers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// SaveInterval is how often a dirty object is written to disk.
const SaveInterval = 5 * time.Second

// FileName is the file in the state directory that holds the object. A
// file that does not parse moves to FileName + ".bad".
const FileName = "layers.json"

// Out is where a Controller sends what the pad shows. plugin.Server
// implements it, so a client such as the virtual pad sees every key.
type Out interface {
	SetKeyState(transport.KeyState) error
	SetCustomGlyph(keyIndex byte, png []byte) error
	// LayerChanged reports the active layer: nil for the top layer.
	LayerChanged(active *string)
}

// Options configures Open.
type Options struct {
	// Dir is the state directory. Empty keeps the object in memory only.
	Dir string
	// AssetsDir is the directory iconRef paths resolve under.
	AssetsDir string
	// Out receives every key change.
	Out Out
	// Logf reports dropped updates and failed sends. May be nil.
	Logf func(format string, args ...any)
	// SaveInterval overrides the default SaveInterval, for tests.
	SaveInterval time.Duration
}

// Update is one setLayerKey message: the full values of one key or child.
type Update struct {
	ID string
	// Parent is the id of the key a child belongs to. Empty for a
	// top-level key.
	Parent string
	// Slot is the key position (0 to 5) a top-level key must take. Nil
	// leaves a known key where it is, and puts a new one in the first
	// free slot. A child has no slot.
	Slot    *int
	Icon    byte
	Status  string
	Count   int
	IconRef string
}

// ErrSlotTaken is returned for an update that names a slot another id
// holds.
var ErrSlotTaken = errors.New("layers: slot held by another id")

// errNoChange ends a mutation that changes nothing and needs no redraw.
var errNoChange = errors.New("no change")

type viewKind int

const (
	kindUnknown viewKind = iota // nothing sent to this key yet
	kindBlank
	kindKey
	kindShift
)

// view is what one key shows. It is comparable, so the controller sends a
// key only when its view changes.
type view struct {
	kind    viewKind
	color   uint16
	icon    byte
	iconRef string
	count   int
}

// Controller owns the object, draws it, and saves it.
type Controller struct {
	out       Out
	assetsDir string
	path      string
	logf      func(format string, args ...any)

	// mu guards doc and dirty. sendMu serializes redraws and guards
	// shown. A mutation takes sendMu first, so two mutations never send
	// their keys in an interleaved order, and Active never waits on a send.
	mu        sync.Mutex
	doc       Doc
	dirty     bool
	sendMu    sync.Mutex
	shown     [KeyCount]view
	closeOnce sync.Once
	stop      chan struct{}
	done      chan struct{}
}

// Open loads the object from opts.Dir, draws the keys it holds, and starts
// saving it every SaveInterval. A file that does not parse moves aside and
// the Controller starts empty.
func Open(opts Options) (*Controller, error) {
	logf := opts.Logf
	if logf == nil {
		logf = func(string, ...any) {}
	}
	interval := opts.SaveInterval
	if interval <= 0 {
		interval = SaveInterval
	}
	c := &Controller{
		out:       opts.Out,
		assetsDir: opts.AssetsDir,
		logf:      logf,
		doc:       newDoc(),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
	}
	if opts.Dir != "" {
		c.path = filepath.Join(opts.Dir, FileName)
		if err := c.load(); err != nil {
			return nil, err
		}
	}

	c.sendMu.Lock()
	c.mu.Lock()
	want := desired(c.doc)
	c.mu.Unlock()
	c.flush(want)
	c.sendMu.Unlock()

	go c.saveLoop(interval)
	return c, nil
}

func (c *Controller) load() error {
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	d, err := Parse(data)
	if err != nil {
		bad := c.path + ".bad"
		if rerr := os.Rename(c.path, bad); rerr != nil {
			c.logf("layers: %v; could not move it aside: %v", err, rerr)
		} else {
			c.logf("layers: %v; moved %s to %s", err, c.path, bad)
		}
		return nil
	}
	c.doc = d
	return nil
}

// Active returns the id of the key whose children the pad shows, or nil
// for the top layer.
func (c *Controller) Active() *string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.doc.Active == nil {
		return nil
	}
	a := *c.doc.Active
	return &a
}

// Snapshot returns a copy of the object.
func (c *Controller) Snapshot() Doc {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.doc.clone()
}

// SetKey upserts one key or child with its full values. A top-level key
// keeps its children. An update that cannot apply is returned as an error
// and changes nothing.
func (c *Controller) SetKey(u Update) error {
	return c.mutate(func(d *Doc) error { return applyUpdate(d, u) })
}

func applyUpdate(d *Doc, u Update) error {
	node := Node{ID: u.ID, Icon: u.Icon, Status: u.Status, Count: u.Count, IconRef: u.IconRef}
	if u.ID == "" {
		return errors.New("layers: update has no id")
	}

	if u.Parent != "" {
		if u.Slot != nil {
			return errors.New("layers: a child has no slot")
		}
		p := d.find(u.Parent)
		if p < 0 {
			return fmt.Errorf("layers: no key %q for child %q", u.Parent, u.ID)
		}
		parent := d.Keys[p]
		for i := range parent.Children {
			if parent.Children[i].ID == u.ID {
				parent.Children[i] = node
				return nil
			}
		}
		parent.Children = append(parent.Children, node)
		return nil
	}

	cur := d.find(u.ID)
	target := cur
	switch {
	case u.Slot != nil:
		slot := *u.Slot
		if slot < 0 || slot >= KeyCount {
			return fmt.Errorf("layers: slot %d out of range 0 to %d", slot, KeyCount-1)
		}
		if holder := d.Keys[slot]; holder != nil && holder.ID != u.ID {
			return fmt.Errorf("%w: slot %d holds %q, not %q", ErrSlotTaken, slot, holder.ID, u.ID)
		}
		target = slot
	case cur < 0:
		for i, k := range d.Keys {
			if k == nil {
				target = i
				break
			}
		}
		if target < 0 {
			return fmt.Errorf("layers: no free slot for %q", u.ID)
		}
	}

	if cur >= 0 {
		node.Children = d.Keys[cur].Children
		d.Keys[cur] = nil
	}
	d.Keys[target] = &node
	return nil
}

// Press handles a press on a key, the debounced press-down. On the top layer a key with
// children opens its child layer. In a child layer the up key returns to
// the top layer. Every other press changes nothing and sends nothing.
func (c *Controller) Press(keyIndex byte) {
	err := c.mutate(func(d *Doc) error {
		if d.Active != nil {
			if int(keyIndex) != UpKey {
				return errNoChange
			}
			d.Active = nil
			return nil
		}
		if int(keyIndex) >= KeyCount {
			return errNoChange
		}
		k := d.Keys[keyIndex]
		if k == nil || len(k.Children) == 0 {
			return errNoChange
		}
		id := k.ID
		d.Active = &id
		return nil
	})
	if err != nil {
		c.logf("layers: press on key %d: %v", keyIndex, err)
	}
}

// mutate applies fn to a copy of the object, and swaps the copy in when it
// validates. It then draws the keys that changed, and reports a new active
// layer.
func (c *Controller) mutate(fn func(d *Doc) error) error {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()

	c.mu.Lock()
	next := c.doc.clone()
	if err := fn(&next); err != nil {
		c.mu.Unlock()
		if errors.Is(err, errNoChange) {
			return nil
		}
		return err
	}
	next.padKeys()
	if err := next.Validate(); err != nil {
		c.mu.Unlock()
		return err
	}
	if !reflect.DeepEqual(c.doc, next) {
		c.dirty = true
	}
	activeChanged := !sameActive(c.doc.Active, next.Active)
	c.doc = next
	want := desired(next)
	active := next.Active
	c.mu.Unlock()

	c.flush(want)
	if activeChanged {
		c.out.LayerChanged(active)
	}
	return nil
}

func sameActive(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// desired returns what each key must show for d.
func desired(d Doc) [KeyCount]view {
	var out [KeyCount]view
	for i := range out {
		out[i] = view{kind: kindBlank}
	}
	if d.Active == nil {
		for i, k := range d.Keys {
			if k != nil && k.hasContent() {
				out[i] = nodeView(k)
			}
		}
		return out
	}
	p := d.find(*d.Active)
	if p < 0 {
		return out
	}
	out[UpKey] = view{kind: kindShift, color: ColorUp, iconRef: ShiftIcon}
	positions := [MaxChildren]int{0, 1, 2, 4, 5}
	for j := range d.Keys[p].Children {
		out[positions[j]] = nodeView(&d.Keys[p].Children[j])
	}
	return out
}

func nodeView(n *Node) view {
	return view{kind: kindKey, color: StatusColor(n.Status), icon: n.Icon, iconRef: n.IconRef, count: n.Count}
}

// flush sends every key whose view differs from what it shows. A key never
// drawn stays untouched when it should be blank, so a reserved slot, or a
// key another process draws, keeps what it shows. The caller holds sendMu.
func (c *Controller) flush(want [KeyCount]view) {
	start := time.Now()
	sent := 0
	defer func() {
		if sent > 0 {
			c.logf("layers: drew %d keys in %s", sent, time.Since(start).Round(time.Millisecond))
		}
	}()
	for i, w := range want {
		have := c.shown[i]
		if w == have || (w.kind == kindBlank && have.kind == kindUnknown) {
			continue
		}
		if err := c.send(byte(i), w); err != nil {
			c.logf("layers: key %d: %v", i, err)
			c.shown[i] = view{}
			continue
		}
		c.shown[i] = w
		sent++
	}
}

func (c *Controller) send(key byte, w view) error {
	ks := transport.KeyState{KeyIndex: key, Version: transport.ProtocolVersion, Color: w.color, EmojiID: w.icon}
	if w.kind == kindBlank {
		ks.Color, ks.EmojiID = 0, 0
		return c.out.SetKeyState(ks)
	}
	if w.iconRef != "" || w.count > 0 {
		png, err := renderGlyph(c.assetsDir, w.iconRef, w.count)
		if err != nil {
			// Show the color alone rather than nothing.
			c.logf("layers: key %d: draw %q: %v", key, w.iconRef, err)
		} else {
			glyphStart := time.Now()
			if err := c.out.SetCustomGlyph(key, png); err != nil {
				return err
			}
			c.logf("layers: key %d: glyph sent in %s", key, time.Since(glyphStart).Round(time.Millisecond))
			ks.EmojiID = transport.CustomGlyphSentinelEmojiID
		}
	}
	return c.out.SetKeyState(ks)
}

func (c *Controller) saveLoop(interval time.Duration) {
	defer close(c.done)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-c.stop:
			return
		case <-t.C:
			if err := c.save(); err != nil {
				c.logf("layers: save: %v", err)
			}
		}
	}
}

// save writes the object when it changed since the last save. It writes a
// temporary file, then renames it, so a crash leaves the old file or the
// new one.
func (c *Controller) save() error {
	if c.path == "" {
		return nil
	}
	c.mu.Lock()
	if !c.dirty {
		c.mu.Unlock()
		return nil
	}
	data, err := json.MarshalIndent(c.doc, "", "  ")
	c.dirty = false
	c.mu.Unlock()
	if err == nil {
		err = writeFileAtomic(c.path, append(data, '\n'))
	}
	if err != nil {
		c.mu.Lock()
		c.dirty = true
		c.mu.Unlock()
	}
	return err
}

func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	cerr := f.Close()
	if err := errors.Join(werr, cerr); err != nil {
		os.Remove(f.Name())
		return err
	}
	if err := os.Rename(f.Name(), path); err != nil {
		os.Remove(f.Name())
		return err
	}
	return nil
}

// Close stops the saver and saves a dirty object. It is safe to call more
// than once.
func (c *Controller) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.stop)
		<-c.done
		err = c.save()
	})
	return err
}
