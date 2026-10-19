package layers

import (
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// FrameOut is what a Controller draws through when the board keeps a cache of
// composed frames (task 0061). plugin.Server implements it. An Out that does
// not implement it gets a Set custom glyph and a key state for each key
// instead, and never a Show frames.
type FrameOut interface {
	// StoreFrame composes png over color into the board's slot, and draws
	// nothing. A nil png is a frame of the color alone.
	StoreFrame(slot byte, color uint16, png []byte) error
	// ShowFramesWithPips draws, for each key k, the frame in slots[k] with a
	// count pip of counts[k] stamped on it, or leaves the key alone for
	// transport.SlotUnchanged. A count of 0 draws the frame as it is. The panels
	// change together (task 0062).
	ShowFramesWithPips(slots, counts []byte) error
	// StorePipSheet sends the sprites of the count pip. colors is the key
	// color each disc of the sheet is blended against.
	StorePipSheet(colors []uint16, pixels []byte) error
}

// DefaultPreloadIdle is how long the pad must go without a press before the
// preload stores another frame.
const DefaultPreloadIdle = 2 * time.Second

const noSlot = transport.SlotUnchanged

// frameKey is what one frame on the board shows: one picture over one color.
// A glyph is the icon at ref under the assets directory, if ref is not empty,
// on a white round, or the words in text on a white tile (task 0069). The count
// pip is not part of it: the board stamps the pip on a copy of the frame (task
// 0062).
type frameKey struct {
	ref   string
	text  string
	color uint16
}

// frameKeyOf returns the frame a view needs. The icon byte of a view is not
// part of it: the board has no glyph table, so the byte draws nothing.
func frameKeyOf(w view) frameKey {
	if w.kind == kindBlank || w.kind == kindUnknown {
		return frameKey{}
	}
	if w.iconText != "" {
		// The tile of a text icon is opaque, so the key color cannot change the
		// frame, and two keys with one text share a slot (task 0069).
		return frameKey{text: w.iconText}
	}
	return frameKey{ref: w.iconRef, color: w.color}
}

// hasGlyph reports whether the frame has a picture over its color.
func (k frameKey) hasGlyph() bool { return k.ref != "" || k.text != "" }

// label names the picture of the frame in a log line.
func (k frameKey) label() string {
	if k.text != "" {
		return k.text
	}
	return k.ref
}

// frameTable is the host's record of the board's slots: which frame each one
// holds, and how recently it was used. The host owns the slot numbers, and the
// board is only an array of them. When every slot is in use, the least
// recently used one that is not pinned is overwritten.
type frameTable struct {
	slotOf map[frameKey]byte
	keyOf  [transport.SlotCount]*frameKey
	used   [transport.SlotCount]uint64
	tick   uint64
}

func newFrameTable() *frameTable {
	return &frameTable{slotOf: make(map[frameKey]byte)}
}

// lookup returns the slot that holds k, and marks it as used.
func (t *frameTable) lookup(k frameKey) (byte, bool) {
	slot, ok := t.slotOf[k]
	if ok {
		t.tick++
		t.used[slot] = t.tick
	}
	return slot, ok
}

// has reports whether a slot holds k, without marking it as used.
func (t *frameTable) has(k frameKey) bool {
	_, ok := t.slotOf[k]
	return ok
}

// claim picks a slot for k and records it there: the lowest free slot, or with
// evict the least recently used slot that pinned does not name. It reports
// false when no slot is available.
func (t *frameTable) claim(k frameKey, evict bool, pinned func(byte) bool) (byte, bool) {
	slot := -1
	for i := range t.keyOf {
		if t.keyOf[i] == nil {
			slot = i
			break
		}
	}
	if slot < 0 {
		if !evict {
			return 0, false
		}
		for i := range t.keyOf {
			if pinned(byte(i)) {
				continue
			}
			if slot < 0 || t.used[i] < t.used[slot] {
				slot = i
			}
		}
		if slot < 0 {
			return 0, false
		}
		delete(t.slotOf, *t.keyOf[slot])
	}
	kk := k
	t.keyOf[slot] = &kk
	t.slotOf[k] = byte(slot)
	t.tick++
	t.used[slot] = t.tick
	return byte(slot), true
}

// release forgets the frame in slot, after a store that failed.
func (t *frameTable) release(slot byte) {
	if k := t.keyOf[slot]; k != nil {
		delete(t.slotOf, *k)
		t.keyOf[slot] = nil
	}
}

// pinnedSlot reports whether a key shows slot. The caller holds sendMu.
func (c *Controller) pinnedSlot(slot byte) bool {
	for _, s := range c.slotShown {
		if s == slot {
			return true
		}
	}
	return false
}

// storeFrame renders the frame k and stores it in a slot of the board's cache.
// With evict it may overwrite the least recently used frame that no key shows.
// It returns false when the frame is not in the cache, because the table is
// full and eviction is off, or the store failed. The caller holds sendMu.
func (c *Controller) storeFrame(k frameKey, evict bool) (byte, bool) {
	slot, ok := c.frames.claim(k, evict, c.pinnedSlot)
	if !ok {
		return 0, false
	}
	var png []byte
	if k.hasGlyph() {
		var err error
		png, err = renderIcon(c.assetsDir, k.ref, k.text, 0, k.color)
		if err != nil {
			// A glyph that cannot be drawn leaves the color alone, rather than
			// nothing.
			c.logf("layers: draw %q: %v", k.label(), err)
			png = nil
		}
	}
	start := time.Now()
	if err := c.frameOut.StoreFrame(slot, k.color, png); err != nil {
		c.frames.release(slot)
		c.logf("layers: store frame %q in slot %d: %v", k.label(), slot, err)
		return 0, false
	}
	c.logf("layers: stored frame %q in slot %d in %s", k.label(), slot, time.Since(start).Round(time.Millisecond))
	return slot, true
}

// storePipSheet sends the pip sprites to the board. A board that does not get
// them draws a frame with no pip. The caller holds sendMu.
func (c *Controller) storePipSheet() {
	sheet, err := BuildPipSheet()
	if err == nil {
		err = c.frameOut.StorePipSheet(sheet.Colors, sheet.Pixels)
	}
	if err != nil {
		c.logf("layers: pip sheet: %v", err)
	}
}

// flushFrames is flush for a board with a frame cache: every key whose frame
// changed names a slot in one Show frames message, after the frames that the
// board does not hold yet are stored. A layer whose frames are all stored
// sends no image at all. A key whose frame cannot be stored, such as a
// picture that does not compress, is drawn the way an Out with no cache draws
// it. The caller holds sendMu.
func (c *Controller) flushFrames(want [KeyCount]view) {
	start := time.Now()
	slots := make([]byte, KeyCount)
	counts := make([]byte, KeyCount)
	for i := range slots {
		slots[i] = transport.SlotUnchanged
	}
	var changed, fallback []int
	for i, w := range want {
		have := c.shown[i]
		if w == have || (w.kind == kindBlank && have.kind == kindUnknown) {
			continue
		}
		if have.kind != kindUnknown && c.slotShown[i] != noSlot && frameKeyOf(w) == frameKeyOf(have) && w.count == have.count {
			// The same picture on the same color with the same pip: nothing to
			// redraw.
			c.shown[i] = w
			continue
		}
		changed = append(changed, i)
	}
	for _, i := range changed {
		k := frameKeyOf(want[i])
		slot, ok := c.frames.lookup(k)
		if !ok {
			slot, ok = c.storeFrame(k, true)
		}
		if !ok {
			fallback = append(fallback, i)
			continue
		}
		slots[i] = slot
		counts[i] = byte(min(want[i].count, 255))
	}

	shown := 0
	if len(fallback) < len(changed) {
		err := c.frameOut.ShowFramesWithPips(slots, counts)
		for _, i := range changed {
			if slots[i] == transport.SlotUnchanged {
				continue
			}
			if err != nil {
				c.shown[i] = view{}
				c.slotShown[i] = noSlot
				continue
			}
			c.shown[i] = want[i]
			c.slotShown[i] = slots[i]
			shown++
		}
		if err != nil {
			c.logf("layers: show frames: %v", err)
		}
	}

	for _, i := range fallback {
		d := c.prepare(byte(i), want[i])
		if err := c.send(d); err != nil {
			c.logf("layers: key %d: %v", i, err)
			c.shown[i] = view{}
		} else {
			c.shown[i] = want[i]
			shown++
		}
		c.slotShown[i] = noSlot
	}
	if shown > 0 {
		c.logf("layers: drew %d keys in %s", shown, time.Since(start).Round(time.Millisecond))
	}
}

// neededFrames returns every frame the layer tree can show, the top layer
// first, then the child layer of each key that has children, without repeats.
func neededFrames(d Doc) []frameKey {
	seen := make(map[frameKey]bool)
	var out []frameKey
	add := func(doc Doc) {
		for _, w := range desired(doc) {
			k := frameKeyOf(w)
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	top := d.clone()
	top.Active = nil
	add(top)
	for _, k := range top.Keys {
		if k == nil || len(k.Children) == 0 {
			continue
		}
		layer := d.clone()
		id := k.ID
		layer.Active = &id
		add(layer)
	}
	return out
}

// kickPreload asks the preload to look for frames that are not on the board.
func (c *Controller) kickPreload() {
	if c.frameOut == nil {
		return
	}
	c.mu.Lock()
	c.kicks++
	c.mu.Unlock()
	select {
	case c.kick <- struct{}{}:
	default:
	}
}

// WaitPreload waits until the preload has stored every frame of the layer
// tree that it can, or timeout passes. It reports whether it finished. A
// Controller with no frame cache has nothing to preload and returns true.
func (c *Controller) WaitPreload(timeout time.Duration) bool {
	if c.frameOut == nil {
		return true
	}
	deadline := time.Now().Add(timeout)
	for {
		c.mu.Lock()
		done := c.passes == c.kicks
		c.mu.Unlock()
		if done {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(time.Millisecond)
	}
}

// preloadLoop stores the frames of every node of the layer tree, one at a time,
// while the pad is idle, so the first visit to a layer draws as fast as a
// revisit. A press pauses it: it stores nothing until `idle` has passed since
// the last press. It runs until Close.
func (c *Controller) preloadLoop(idle time.Duration) {
	defer close(c.preloadDone)
	for {
		select {
		case <-c.stop:
			return
		case <-c.kick:
		}
		c.mu.Lock()
		target := c.kicks
		c.mu.Unlock()
		if !c.preloadPass(idle) {
			return
		}
		c.mu.Lock()
		c.passes = target
		c.mu.Unlock()
	}
}

// sleepOrStop waits for d, and reports false if the Controller closed first.
func (c *Controller) sleepOrStop(d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-c.stop:
		return false
	case <-t.C:
		return true
	}
}

// idleFor returns how much longer the pad must stay quiet before the preload
// may store a frame, or zero.
func (c *Controller) idleFor(idle time.Duration) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.lastPress.IsZero() {
		return 0
	}
	if left := idle - time.Since(c.lastPress); left > 0 {
		return left
	}
	return 0
}

// preloadPass stores the frames that the table lacks. It fills free slots and
// evicts none. It reports false if the Controller closed.
func (c *Controller) preloadPass(idle time.Duration) bool {
	c.mu.Lock()
	doc := c.doc.clone()
	c.mu.Unlock()

	stored := 0
	for _, k := range neededFrames(doc) {
		for {
			if left := c.idleFor(idle); left > 0 {
				if !c.sleepOrStop(left) {
					return false
				}
				continue
			}
			c.sendMu.Lock()
			// A press may have come while this waited for sendMu.
			if c.idleFor(idle) > 0 {
				c.sendMu.Unlock()
				continue
			}
			break
		}
		select {
		case <-c.stop:
			c.sendMu.Unlock()
			return false
		default:
		}
		if c.frames.has(k) {
			c.sendMu.Unlock()
			continue
		}
		_, ok := c.storeFrame(k, false)
		c.sendMu.Unlock()
		if ok {
			stored++
		}
	}
	if stored > 0 {
		c.logf("layers: preloaded %d frames", stored)
	}
	return true
}
