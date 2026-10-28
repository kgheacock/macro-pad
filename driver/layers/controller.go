package layers

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
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
	// BeginBatch tells the board which keys the next glyphs and key states
	// belong to, so it draws them together. Bit k of each mask is key k.
	BeginBatch(glyphMask, stateMask byte) error
	// LayerChanged reports the path of the active layer, empty for the top
	// layer (task 0086), and the page of it the pad shows, from 0, of pages.
	// On the top layer page and pages are 0 (task 0078).
	LayerChanged(path []string, page, pages int)
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
	// PreloadIdle is how long the pad must go without a press before the
	// preload stores a frame. Zero means DefaultPreloadIdle. It only matters
	// when Out implements FrameOut.
	PreloadIdle time.Duration
}

// Update is one setLayerKey message: the full values of one key or child.
type Update struct {
	ID string
	// Parent is the id of the top-level key a child belongs to. It is a
	// shorthand for a Path of one id.
	Parent string
	// Path is the ids from a top-level key down to the parent of a child at
	// any depth (task 0086). Empty, with an empty Parent, for a top-level
	// key. An update may not carry both.
	Path []string
	// Slot is the key position (0 to 5) a top-level key must take. Nil
	// leaves a known key where it is, and puts a new one in the first
	// free slot. A child has no slot.
	Slot    *int
	Icon    byte
	Status  string
	Count   int
	IconRef string
	// IconText is words drawn as the icon instead of IconRef (task 0069), or
	// the caption over IconEmoji.
	IconText string
	// IconEmoji is one or two pictures under assets/emoji/ (task 0073).
	IconEmoji []string
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
	// iconText is the words of a text icon, in place of iconRef, or the
	// caption over iconEmoji.
	iconText string
	// iconEmoji is the emoji refs joined by line breaks, so view stays
	// comparable (task 0073).
	iconEmoji string
	// round draws iconEmoji on the white round of task 0073, as a top-level
	// key does; a key in a child layer draws it as a hero, a sticker, and a
	// strip (task 0082).
	round bool
	count int
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
	mu     sync.Mutex
	doc    Doc
	dirty  bool
	sendMu sync.Mutex
	shown  [KeyCount]view
	// frameOut is Out as a FrameOut, or nil when the board has no frame cache.
	// frames, slotShown, and the preload's work all run under sendMu: frames
	// is the host's record of the board's slots, and slotShown is the slot
	// each key shows, or noSlot.
	frameOut  FrameOut
	frames    *frameTable
	slotShown [KeyCount]byte
	// lastPress, kicks, and passes are guarded by mu. The preload pauses until
	// PreloadIdle after lastPress; a pass that has seen kick number n sets
	// passes to n.
	lastPress   time.Time
	kicks       int
	passes      int
	kick        chan struct{}
	preloadDone chan struct{}
	closeOnce   sync.Once
	stop        chan struct{}
	done        chan struct{}
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
		frames:    newFrameTable(),
		kick:      make(chan struct{}, 1),

		preloadDone: make(chan struct{}),
	}
	for i := range c.slotShown {
		c.slotShown[i] = noSlot
	}
	if fo, ok := opts.Out.(FrameOut); ok {
		c.frameOut = fo
	}
	if opts.Dir != "" {
		c.path = filepath.Join(opts.Dir, FileName)
		if err := c.load(); err != nil {
			return nil, err
		}
	}

	c.sendMu.Lock()
	if c.frameOut != nil {
		c.storePipSheet()
	}
	c.mu.Lock()
	want := desired(c.doc)
	c.mu.Unlock()
	c.flush(want)
	c.sendMu.Unlock()

	go c.saveLoop(interval)
	if c.frameOut != nil {
		idle := opts.PreloadIdle
		if idle <= 0 {
			idle = DefaultPreloadIdle
		}
		go c.preloadLoop(idle)
		c.kickPreload()
	} else {
		close(c.preloadDone)
	}
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

// Active returns the id of the node whose children the pad shows, or nil
// for the top layer.
func (c *Controller) Active() *string {
	active, _, _ := c.Layer()
	return active
}

// Layer returns the id of the active layer, nil for the top layer, the page
// of it the pad shows, and its page count. On the top layer page and pages
// are 0.
func (c *Controller) Layer() (active *string, page, pages int) {
	path, page, pages := c.LayerPath()
	return lastID(path), page, pages
}

// LayerPath returns the path of the active layer, empty for the top layer,
// the page of it the pad shows, and its page count (task 0086).
func (c *Controller) LayerPath() (path []string, page, pages int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return layerOf(c.doc)
}

// lastID returns the last id of path, or nil for an empty path.
func lastID(path []string) *string {
	if len(path) == 0 {
		return nil
	}
	a := path[len(path)-1]
	return &a
}

// layerOf returns the path of the active layer of d, its page, and its page
// count.
func layerOf(d Doc) (path []string, page, pages int) {
	if d.Active == nil {
		return nil, 0, 0
	}
	if n := d.open(); n != nil {
		pages = PageCount(len(n.Children))
	}
	return append([]string{}, d.Active...), d.Page, pages
}

// parentPath returns the path to the parent of the node u names, empty for
// a top-level key.
func (u Update) parentPath() ([]string, error) {
	if u.Parent != "" && len(u.Path) > 0 {
		return nil, errors.New("layers: an update has a parent or a path, not both")
	}
	if u.Parent != "" {
		return []string{u.Parent}, nil
	}
	return u.Path, nil
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
	node := Node{ID: u.ID, Icon: u.Icon, Status: u.Status, Count: u.Count, IconRef: u.IconRef, IconText: u.IconText, IconEmoji: u.IconEmoji}
	if u.ID == "" {
		return errors.New("layers: update has no id")
	}

	path, err := u.parentPath()
	if err != nil {
		return err
	}
	if len(path) > 0 {
		if u.Slot != nil {
			return errors.New("layers: a child has no slot")
		}
		parent := d.nodeAt(path)
		if parent == nil {
			return fmt.Errorf("layers: no key %q for child %q", strings.Join(path, "/"), u.ID)
		}
		if cur := parent.child(u.ID); cur != nil {
			node.Children = cur.Children
			*cur = node
			return nil
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

// RemoveKey removes one node. With a parent, it removes that child of the
// top-level key parent; see RemoveNode.
func (c *Controller) RemoveKey(id, parent string) error {
	var path []string
	if parent != "" {
		path = []string{parent}
	}
	return c.RemoveNode(id, path)
}

// RemoveNode removes one node with its children. With a path, the ids from a
// top-level key down to its parent, it removes that child, and the children
// after it move up one position. Without, it removes the top-level key, and
// frees its slot. An unknown id changes nothing and sends nothing. When the
// removal takes away the open layer, the pad goes up to the deepest layer
// left on its path (task 0086).
func (c *Controller) RemoveNode(id string, path []string) error {
	return c.mutate(func(d *Doc) error { return applyRemove(d, id, path) })
}

func applyRemove(d *Doc, id string, path []string) error {
	if len(path) > 0 {
		parent := d.nodeAt(path)
		if parent == nil {
			return errNoChange
		}
		for i := range parent.Children {
			if parent.Children[i].ID == id {
				parent.Children = append(parent.Children[:i], parent.Children[i+1:]...)
				return nil
			}
		}
		return errNoChange
	}
	i := d.find(id)
	if i < 0 {
		return errNoChange
	}
	d.Keys[i] = nil
	return nil
}

// Press handles a press on a key; see PressNode. A press on a child returns
// the ids of its parent and of the child; any other press returns empty ids.
func (c *Controller) Press(keyIndex byte) (parent, child string) {
	path, child := c.PressNode(keyIndex)
	if p := lastID(path); p != nil {
		parent = *p
	}
	return parent, child
}

// PressNode handles a press on a key, the debounced press-down. On the top
// layer a key with children opens its child layer, on its first page. In a
// child layer a child with children opens its layer (task 0086), the up key
// goes back one level, to the page it showed, the left arrow shows the page
// before, and the right arrow the next page (task 0078). Every other press
// changes nothing and sends nothing. A press on a child with no children
// returns the path to its parent and the child's id, as the pad showed them;
// any other press returns nil and an empty id.
func (c *Controller) PressNode(keyIndex byte) (path []string, child string) {
	// A press pauses the preload, so its Store frame messages do not hold up
	// the switch (task 0061).
	c.mu.Lock()
	c.lastPress = time.Now()
	c.mu.Unlock()
	err := c.mutate(func(d *Doc) error {
		if d.Active != nil {
			if int(keyIndex) >= KeyCount {
				return errNoChange
			}
			k := d.open()
			if k == nil {
				return errNoChange
			}
			switch pk := pageLayout(len(k.Children), d.Page)[keyIndex]; pk.role {
			case roleChild:
				ch := &k.Children[pk.child]
				if len(ch.Children) > 0 {
					d.Active = append(d.Active, ch.ID)
					d.ParentPages = append(d.ParentPages, d.Page)
					d.Page = 0
					return nil
				}
				path, child = append([]string{}, d.Active...), ch.ID
				return errNoChange
			case roleUp:
				d.up()
			case roleLeft:
				d.Page--
			case roleRight:
				d.Page++
			default:
				return errNoChange
			}
			return nil
		}
		if int(keyIndex) >= KeyCount {
			return errNoChange
		}
		k := d.Keys[keyIndex]
		if k == nil || len(k.Children) == 0 {
			return errNoChange
		}
		d.Active = []string{k.ID}
		d.Page = 0
		d.ParentPages = nil
		return nil
	})
	if err != nil {
		c.logf("layers: press on key %d: %v", keyIndex, err)
	}
	return path, child
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
	next.settle()
	if err := next.Validate(); err != nil {
		c.mu.Unlock()
		return err
	}
	if !reflect.DeepEqual(c.doc, next) {
		c.dirty = true
	}
	oldPath, oldPage, oldPages := layerOf(c.doc)
	path, page, pages := layerOf(next)
	layerChanged := !slices.Equal(oldPath, path) || oldPage != page || oldPages != pages
	c.doc = next
	want := desired(next)
	c.mu.Unlock()

	c.flush(want)
	if layerChanged {
		c.out.LayerChanged(path, page, pages)
	}
	c.kickPreload()
	return nil
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
				out[i].round = out[i].iconEmoji != ""
			}
		}
		return out
	}
	n := d.open()
	if n == nil {
		return out
	}
	children := n.Children
	for i, pk := range pageLayout(len(children), d.Page) {
		switch pk.role {
		case roleChild:
			out[i] = nodeView(&children[pk.child])
		case roleUp:
			out[i] = view{kind: kindShift, color: ColorUp, iconRef: ShiftIcon}
		case roleLeft:
			out[i] = view{kind: kindShift, color: ColorUp, iconRef: LeftIcon}
		case roleRight:
			out[i] = view{kind: kindShift, color: ColorUp, iconRef: RightIcon}
		}
	}
	return out
}

func nodeView(n *Node) view {
	return view{kind: kindKey, color: StatusColor(n.Status), icon: n.Icon, iconRef: n.IconRef, iconText: n.IconText, iconEmoji: joinEmoji(n.IconEmoji), count: n.Count}
}

// keyDraw is one key to send: its key state, and the glyph that goes before
// it, when it has one.
type keyDraw struct {
	key   byte
	want  view
	state transport.KeyState
	png   []byte
}

// flush sends every key whose view differs from what it shows. A key never
// drawn stays untouched when it should be blank, so a reserved slot, or a
// key another process draws, keeps what it shows. The caller holds sendMu.
//
// When two or more keys change, flush sends Begin batch first, so the board
// holds the new frames and draws all of them together (task 0060). One key
// goes out alone, with no wait.
func (c *Controller) flush(want [KeyCount]view) {
	if c.frameOut != nil {
		c.flushFrames(want)
		return
	}
	start := time.Now()
	var draws []keyDraw
	for i, w := range want {
		have := c.shown[i]
		if w == have || (w.kind == kindBlank && have.kind == kindUnknown) {
			continue
		}
		draws = append(draws, c.prepare(byte(i), w))
	}
	if len(draws) == 0 {
		return
	}

	if len(draws) >= 2 {
		var glyphMask, stateMask byte
		for _, d := range draws {
			if d.png != nil {
				glyphMask |= 1 << d.key
			}
			stateMask |= 1 << d.key
		}
		if err := c.out.BeginBatch(glyphMask, stateMask); err != nil {
			c.logf("layers: begin batch: %v", err)
		}
	}

	sent := 0
	for _, d := range draws {
		if err := c.send(d); err != nil {
			c.logf("layers: key %d: %v", d.key, err)
			c.shown[d.key] = view{}
			continue
		}
		c.shown[d.key] = d.want
		sent++
	}
	if sent > 0 {
		c.logf("layers: drew %d keys in %s", sent, time.Since(start).Round(time.Millisecond))
	}
}

// prepare builds what one key needs on the wire: its key state, and its
// glyph when it has one. A glyph that cannot be drawn leaves the color
// alone, rather than nothing.
func (c *Controller) prepare(key byte, w view) keyDraw {
	d := keyDraw{key: key, want: w}
	d.state = transport.KeyState{KeyIndex: key, Version: transport.ProtocolVersion, Color: w.color, EmojiID: w.icon}
	if w.kind == kindBlank {
		d.state.Color, d.state.EmojiID = 0, 0
		return d
	}
	if w.iconRef != "" || w.iconText != "" || w.iconEmoji != "" || w.count > 0 {
		png, err := renderIcon(c.assetsDir, w.iconRef, w.iconText, w.iconEmoji, w.round, w.count, w.color)
		if err != nil {
			c.logf("layers: key %d: draw %q: %v", key, w.iconRef+w.iconText+w.iconEmoji, err)
		} else {
			d.png = png
			d.state.EmojiID = transport.CustomGlyphSentinelEmojiID
		}
	}
	return d
}

func (c *Controller) send(d keyDraw) error {
	if d.png != nil {
		glyphStart := time.Now()
		if err := c.out.SetCustomGlyph(d.key, d.png); err != nil {
			return err
		}
		c.logf("layers: key %d: glyph sent in %s", d.key, time.Since(glyphStart).Round(time.Millisecond))
	}
	return c.out.SetKeyState(d.state)
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
		<-c.preloadDone
		err = c.save()
	})
	return err
}
