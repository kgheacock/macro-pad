package transport

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// defaultSettleDelay is how long Reconnecting waits after it opens the
// board before it replays state. CircuitPython reloads code.py when the
// board is replugged, and a report that arrives during the restart is
// dropped (see cmd/pingpong's pingResendInterval), so replaying at once can
// lose the first keys. 1 s is a starting value, not a measured one.
const defaultSettleDelay = time.Second

// reconnectRetryDelay paces Reconnecting's retries after an open fails
// for a reason other than the board being absent.
const reconnectRetryDelay = time.Second

// pipSheetRec is the pip sheet that Reconnecting sends to each board it
// connects.
type pipSheetRec struct {
	colors []uint16
	pixels []byte
}

// frameRec is one frame in the board's cache: a color, and a glyph when the
// frame has one.
type frameRec struct {
	color    uint16
	hasGlyph bool
	glyph    glyphRec
	gen      int
}

// Reconnecting is a Transport that keeps a device connected across power
// cuts and replugs, and replays each key's last state to it every time it
// connects.
//
// The board keeps no key state across a power cycle (task 0044): a write to
// its flash froze every blinking key for 0.4 s to 0.7 s and wore the flash.
// So the host holds the state. Reconnecting remembers what each key shows
// as the board would hold it, and after a reconnect it sends the same
// messages again.
//
// SendKeyState and SendCustomGlyph never fail because the board is absent:
// they remember the state and report success, and the next reconnect sends
// it. ReadMessage returns messages from whichever device is connected, and
// io.EOF only after Close.
//
// StoreFrame and ShowFrames (task 0061) work on the board's frame cache. The
// cache lives in the board's RAM, so a board that resets has none. Reconnecting
// keeps the frames it stored, and after a reconnect it clears its record of
// what the board holds and stores every frame again, in the background. The
// slot numbers do not change, so the caller's own table stays right. A Show
// frames that names a slot the board does not hold yet stores it first, and a
// Show miss from the board stores the frames it names again.
type Reconnecting struct {
	open   func(ctx context.Context) (Transport, error)
	settle time.Duration
	logf   func(format string, args ...any)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	queue  chan Message

	// sendMu serialises everything that goes to the device: each send, and
	// a whole replay, so a send that arrives during a replay waits and then
	// reaches the device after the replayed state, not before it. A glyph
	// takes seconds to cross, so nothing but a send may wait on sendMu.
	sendMu sync.Mutex
	// mu guards dev, keys, glyphs, and the state directory. It is held only
	// for in-memory and disk updates, never across a write to the device,
	// so Close and a send's bookkeeping never wait on a slow upload.
	mu sync.Mutex
	// dev is the connected device, or nil while none is.
	dev Transport
	// keys is the key state each key shows on the board, including the
	// custom-glyph sentinel for a key that shows a glyph.
	keys map[byte]KeyState
	// glyphs is the custom glyph each key shows, when it shows one.
	glyphs map[byte]glyphRec
	// frames is every frame stored in the board's cache, by slot, and gen says
	// which connection of the board holds it.
	frames map[byte]*frameRec
	// shown is the slot each key last showed by a Show frames, until a key
	// state or a glyph replaces it.
	shown map[byte]byte
	// pips is the count each key in shown last showed with, when above 0 (task
	// 0062). sheet is the pip sheet the board must hold before it can draw one,
	// sent again at every connect.
	pips  map[byte]byte
	sheet *pipSheetRec
	// gen counts the connections. A frame whose gen is not the current one is
	// not on the board.
	gen int
	// store is the state directory, or nil when state is kept in memory only.
	store *stateStore

	closeOnce sync.Once
}

var _ Transport = (*Reconnecting)(nil)

// NewReconnecting starts connecting to the macro pad that opts names and
// returns at once. stateDir, when not empty, is a directory on the host
// where the remembered state is saved on every change and loaded at
// start, so a restart of the daemon or a reboot of the host loses nothing:
// keys.json holds each key's key state, and glyph-N.bin holds key N's
// custom glyph as raw pixels. logf, when not nil, reports each connect,
// disconnect, replay, and state-file problem.
func NewReconnecting(opts Options, stateDir string, logf func(format string, args ...any)) *Reconnecting {
	open := func(ctx context.Context) (Transport, error) {
		d, err := Open(ctx, opts)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
	return newReconnecting(open, defaultSettleDelay, stateDir, logf)
}

func newReconnecting(open func(context.Context) (Transport, error), settle time.Duration, stateDir string, logf func(string, ...any)) *Reconnecting {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Reconnecting{
		open:   open,
		settle: settle,
		logf:   logf,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		queue:  make(chan Message, deviceQueueSize),
		keys:   make(map[byte]KeyState),
		glyphs: make(map[byte]glyphRec),
		frames: make(map[byte]*frameRec),
		shown:  make(map[byte]byte),
		pips:   make(map[byte]byte),
	}
	if stateDir != "" {
		r.store = &stateStore{dir: stateDir}
		r.loadState()
	}
	go r.run()
	return r
}

// loadState reads the state directory into keys and glyphs. A keys.json
// that does not parse is moved aside, not overwritten, and the daemon starts
// with no state.
func (r *Reconnecting) loadState() {
	keys, glyphs, warnings, err := r.store.load()
	if err != nil {
		bad := filepath.Join(r.store.dir, stateFileName+".bad")
		if rerr := os.Rename(filepath.Join(r.store.dir, stateFileName), bad); rerr == nil {
			r.logf("state: %v; moved it to %s and starting with no state", err, bad)
		} else {
			r.logf("state: %v; starting with no state", err)
		}
		return
	}
	for _, w := range warnings {
		r.logf("state: %s", w)
	}
	r.keys, r.glyphs = keys, glyphs
	if err := r.store.prune(glyphs); err != nil {
		r.logf("state: %v", err)
	}
	if len(keys) > 0 {
		r.logf("state: loaded %d keys from %s", len(keys), r.store.dir)
	}
}

// saveState writes what changed to the state directory. A failure is
// reported and does not fail the send: the state is still in memory.
func (r *Reconnecting) saveState(err error) {
	if err != nil {
		r.logf("state: %v", err)
	}
}

// run connects, replays, reads until the device goes, and starts over,
// until Close.
func (r *Reconnecting) run() {
	defer close(r.done)
	defer close(r.queue)

	for r.ctx.Err() == nil {
		dev, err := r.open(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			r.logf("reconnect: %v", err)
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(reconnectRetryDelay):
			}
			continue
		}

		if !r.connect(dev) {
			dev.Close()
			return
		}
		r.logf("device connected")
		r.mu.Lock()
		gen := r.gen
		r.mu.Unlock()
		go r.restoreFrames(dev, gen)

		r.forward(dev)

		r.mu.Lock()
		r.dev = nil
		r.mu.Unlock()
		dev.Close()
		if r.ctx.Err() == nil {
			r.logf("device disconnected, waiting for it to return")
		}
	}
}

// connect waits out the settle delay, replays the remembered state to dev,
// and makes dev the device sends go to. It reports false when Close came
// first.
func (r *Reconnecting) connect(dev Transport) bool {
	select {
	case <-r.ctx.Done():
		return false
	case <-time.After(r.settle):
	}

	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	sheet := r.sheet
	r.mu.Unlock()
	if sheet != nil {
		if err := dev.StorePipSheet(sheet.colors, sheet.pixels); err != nil {
			r.logf("pips: send the pip sheet: %v", err)
		}
	}

	r.mu.Lock()
	keys := make([]int, 0, len(r.keys))
	states := make(map[byte]KeyState, len(r.keys))
	glyphs := make(map[byte]glyphRec, len(r.glyphs))
	for k, ks := range r.keys {
		keys = append(keys, int(k))
		states[k] = ks
	}
	for k, g := range r.glyphs {
		glyphs[k] = g
	}
	r.mu.Unlock()

	sort.Ints(keys)
	for _, k := range keys {
		key := byte(k)
		// A glyph goes first: the board sets a key to the custom-glyph
		// sentinel when it receives one, and the key state that follows
		// sets the color and blink on top of it.
		if g, ok := glyphs[key]; ok {
			dev.SendCustomGlyph(key, g.pixels)
		}
		dev.SendKeyState(states[key])
	}
	if len(keys) > 0 {
		r.logf("replayed the state of %d keys", len(keys))
	}

	r.mu.Lock()
	r.gen++
	r.dev = dev
	r.mu.Unlock()
	return true
}

// forward moves dev's messages to the queue until dev errors or Close.
func (r *Reconnecting) forward(dev Transport) {
	for {
		msg, err := dev.ReadMessage()
		if err != nil {
			return
		}
		if msg.Type == MessageTypeShowMiss {
			// Sending waits for the board, so it must not hold up the reads.
			go r.handleShowMiss(dev, msg.ShowMiss.Mask)
		}
		if msg.Type == MessageTypeBatchIncomplete {
			go r.handleBatchIncomplete(dev, msg.BatchIncomplete)
		}
		select {
		case r.queue <- msg:
		case <-r.ctx.Done():
			return
		}
	}
}

// SendKeyState implements Transport. It remembers ks, and sends it when a
// device is connected.
func (r *Reconnecting) SendKeyState(ks KeyState) error {
	asked := time.Now()
	r.sendMu.Lock()
	defer r.sendMu.Unlock()
	waited := time.Since(asked)

	r.mu.Lock()
	// A built-in Emoji ID replaces a custom image on the board, so the
	// image is no longer part of the state to replay.
	var glyph *glyphRec
	if ks.EmojiID == CustomGlyphSentinelEmojiID {
		if g, ok := r.glyphs[ks.KeyIndex]; ok {
			glyph = &g
		}
	}
	r.saveKeysLocked(r.recordKeyLocked(ks, glyph))
	delete(r.shown, ks.KeyIndex)
	delete(r.pips, ks.KeyIndex)
	dev := r.dev
	r.mu.Unlock()

	if dev == nil {
		r.logf("setKeyState key=%d sendMu wait=%s, no device connected", ks.KeyIndex, waited.Round(time.Millisecond))
		return nil
	}
	wrote := time.Now()
	err := dev.SendKeyState(ks)
	r.logf("setKeyState key=%d sendMu wait=%s board write=%s err=%v", ks.KeyIndex, waited.Round(time.Millisecond), time.Since(wrote).Round(time.Millisecond), err)
	return err
}

// recordKeyLocked remembers that key ks.KeyIndex shows ks, with glyph when it
// shows one, and saves what changed. The state directory takes a write only for
// a change: a glyph file when its hash has no file yet, and keys.json when the
// key state differs from what the key showed before (task 0061). The caller
// holds mu. It saves the glyph file at once, and leaves keys.json to the
// caller, which writes it once for all the keys it changed (saveKeysLocked).
// It reports whether the key state changed.
func (r *Reconnecting) recordKeyLocked(ks KeyState, glyph *glyphRec) bool {
	previous, hadKey := r.keys[ks.KeyIndex]
	previousGlyph, hadGlyph := r.glyphs[ks.KeyIndex]
	r.keys[ks.KeyIndex] = ks
	if glyph != nil {
		r.glyphs[ks.KeyIndex] = *glyph
	} else if ks.EmojiID != CustomGlyphSentinelEmojiID {
		delete(r.glyphs, ks.KeyIndex)
	}
	current, hasGlyph := r.glyphs[ks.KeyIndex]
	changed := !hadKey || previous != ks || hadGlyph != hasGlyph || (hasGlyph && previousGlyph.hash != current.hash)
	if r.store != nil && glyph != nil {
		_, err := r.store.saveGlyph(*glyph)
		r.saveState(err)
	}
	return changed
}

// saveKeysLocked writes keys.json when changed. The caller holds mu.
func (r *Reconnecting) saveKeysLocked(changed bool) {
	if r.store != nil && changed {
		r.saveState(r.store.saveKeys(r.keys, r.glyphs))
	}
}

// SendCustomGlyph implements Transport. It remembers the glyph, and sends
// it when a device is connected.
func (r *Reconnecting) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	g := newGlyphRec(append([]byte(nil), pixels...))
	r.mu.Lock()
	// The board sets a key that receives a glyph to the sentinel Emoji ID
	// and leaves its color and blink alone.
	ks, ok := r.keys[keyIndex]
	if !ok {
		ks = KeyState{KeyIndex: keyIndex, Version: ProtocolVersion}
	}
	ks.EmojiID = CustomGlyphSentinelEmojiID
	r.saveKeysLocked(r.recordKeyLocked(ks, &g))
	delete(r.shown, keyIndex)
	delete(r.pips, keyIndex)
	dev := r.dev
	r.mu.Unlock()

	if dev == nil {
		return nil
	}
	return dev.SendCustomGlyph(keyIndex, pixels)
}

// StoreFrame implements Transport. It remembers the frame, and sends it when
// a device is connected. It checks that the glyph fits one frame whether or not
// a board is connected, so a caller learns of ErrFrameTooLarge at once.
func (r *Reconnecting) StoreFrame(slot byte, color uint16, pixels []byte) error {
	if _, err := encodeStoreFrame(slot, color, pixels); err != nil {
		return err
	}
	rec := &frameRec{color: color}
	if pixels != nil {
		rec.hasGlyph = true
		rec.glyph = newGlyphRec(append([]byte(nil), pixels...))
	}

	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	r.frames[slot] = rec
	dev, gen := r.dev, r.gen
	r.mu.Unlock()
	if dev == nil {
		return nil
	}
	return r.storeLocked(dev, gen, slot)
}

// ShowFrames implements Transport. Every key that names a slot shows the frame
// stored there, and Reconnecting remembers it as that key's state: a later
// reconnect replays it, and the state directory takes a write only when a key
// state changed. A slot the board does not hold yet, as after a reconnect, is
// stored first.
func (r *Reconnecting) ShowFrames(slots []byte) error {
	return r.show(slots, nil)
}

// StorePipSheet implements Transport. It remembers the sheet, and sends it when
// a device is connected and again at every connect (task 0062).
func (r *Reconnecting) StorePipSheet(colors []uint16, pixels []byte) error {
	if _, err := encodeStorePipSheet(colors, pixels); err != nil {
		return err
	}
	rec := &pipSheetRec{colors: append([]uint16(nil), colors...), pixels: append([]byte(nil), pixels...)}

	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	r.sheet = rec
	dev := r.dev
	r.mu.Unlock()
	if dev == nil {
		return nil
	}
	return dev.StorePipSheet(rec.colors, rec.pixels)
}

// ShowFramesWithPips implements Transport. It is ShowFrames with a count for
// each key, which Reconnecting remembers with the slot, so that a reconnect
// stamps the pip again (task 0062).
func (r *Reconnecting) ShowFramesWithPips(slots, counts []byte) error {
	if _, err := encodeShowFramesPips(slots, counts); err != nil {
		return err
	}
	return r.show(slots, counts)
}

// show is ShowFrames and ShowFramesWithPips. counts is nil for the first.
func (r *Reconnecting) show(slots, counts []byte) error {
	if _, err := encodeShowFrames(slots); err != nil {
		return err
	}
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	changed := false
	for key, slot := range slots {
		if slot == SlotUnchanged {
			continue
		}
		rec := r.frames[slot]
		if rec == nil {
			// Nothing was stored there. The board answers with a Show miss.
			continue
		}
		ks := KeyState{KeyIndex: byte(key), Version: ProtocolVersion, Color: rec.color}
		var glyph *glyphRec
		if rec.hasGlyph {
			ks.EmojiID = CustomGlyphSentinelEmojiID
			g := rec.glyph
			glyph = &g
		}
		if r.recordKeyLocked(ks, glyph) {
			changed = true
		}
		r.shown[byte(key)] = slot
		if counts != nil && counts[key] > 0 {
			r.pips[byte(key)] = counts[key]
		} else {
			delete(r.pips, byte(key))
		}
	}
	r.saveKeysLocked(changed)
	dev, gen := r.dev, r.gen
	var missing []byte
	if dev != nil {
		seen := make(map[byte]bool)
		for _, slot := range slots {
			if slot == SlotUnchanged || seen[slot] {
				continue
			}
			seen[slot] = true
			if rec := r.frames[slot]; rec != nil && rec.gen != gen {
				missing = append(missing, slot)
			}
		}
	}
	r.mu.Unlock()
	if dev == nil {
		return nil
	}

	for _, slot := range missing {
		if err := r.storeLocked(dev, gen, slot); err != nil {
			return err
		}
	}
	if counts != nil {
		return dev.ShowFramesWithPips(slots, counts)
	}
	return dev.ShowFrames(slots)
}

// storeLocked sends the frame in slot to dev and notes that dev holds it. The
// caller holds sendMu.
func (r *Reconnecting) storeLocked(dev Transport, gen int, slot byte) error {
	r.mu.Lock()
	rec := r.frames[slot]
	r.mu.Unlock()
	if rec == nil {
		return nil
	}
	var pixels []byte
	if rec.hasGlyph {
		pixels = rec.glyph.pixels
	}
	if err := dev.StoreFrame(slot, rec.color, pixels); err != nil {
		return err
	}
	r.mu.Lock()
	if r.frames[slot] == rec {
		rec.gen = gen
	}
	r.mu.Unlock()
	return nil
}

// restoreFrames stores every frame again on dev, a board that just connected
// and holds none. It takes sendMu for one frame at a time, so a send that
// arrives meanwhile waits for at most one frame, not for all of them. It
// stops when dev goes away or a store fails.
func (r *Reconnecting) restoreFrames(dev Transport, gen int) {
	r.mu.Lock()
	slots := make([]int, 0, len(r.frames))
	for slot := range r.frames {
		slots = append(slots, int(slot))
	}
	r.mu.Unlock()
	sort.Ints(slots)

	restored := 0
	for _, s := range slots {
		slot := byte(s)
		r.sendMu.Lock()
		r.mu.Lock()
		rec := r.frames[slot]
		current := r.dev == dev && r.gen == gen && r.ctx.Err() == nil
		stale := rec != nil && rec.gen != gen
		r.mu.Unlock()
		if !current {
			r.sendMu.Unlock()
			return
		}
		if stale {
			if err := r.storeLocked(dev, gen, slot); err != nil {
				r.sendMu.Unlock()
				r.logf("frames: store slot %d again: %v", slot, err)
				return
			}
			restored++
		}
		r.sendMu.Unlock()
	}
	if restored > 0 {
		r.logf("frames: stored %d frames again", restored)
	}
	r.showPipsAgain(dev, gen)
}

// showPipsAgain stamps the pips of the keys that showed one, on a board that
// just got its frames again. The replay of the key states draws the frames
// with no pip.
func (r *Reconnecting) showPipsAgain(dev Transport, gen int) {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	if r.dev != dev || r.gen != gen || r.sheet == nil || len(r.pips) == 0 {
		r.mu.Unlock()
		return
	}
	slots, counts := r.pipShowLocked()
	r.mu.Unlock()
	if err := dev.ShowFramesWithPips(slots, counts); err != nil {
		r.logf("pips: show again: %v", err)
	}
}

// pipShowLocked builds a Show frames with pips payload for the keys that show
// a pip, and leaves the others unchanged. The caller holds mu.
func (r *Reconnecting) pipShowLocked() (slots, counts []byte) {
	slots = make([]byte, 8)
	counts = make([]byte, 8)
	for i := range slots {
		slots[i] = SlotUnchanged
	}
	for key, count := range r.pips {
		if slot, ok := r.shown[key]; ok && key < 8 && r.frames[slot] != nil {
			slots[key], counts[key] = slot, count
		}
	}
	return slots, counts
}

// handleShowMiss answers a Show miss: the board named some keys whose slot it
// holds nothing in, for example after a code reload that kept USB up. It stores
// the frames of those keys again and shows them.
func (r *Reconnecting) handleShowMiss(dev Transport, mask byte) {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	if r.dev != dev {
		r.mu.Unlock()
		return
	}
	gen := r.gen
	slots := make([]byte, 8)
	for i := range slots {
		slots[i] = SlotUnchanged
	}
	var again []byte
	var counts []byte
	for key := byte(0); key < 8; key++ {
		if mask&(1<<key) == 0 {
			continue
		}
		slot, ok := r.shown[key]
		if !ok || r.frames[slot] == nil {
			continue
		}
		slots[key] = slot
		if r.sheet != nil {
			if counts == nil {
				counts = make([]byte, 8)
			}
			counts[key] = r.pips[key]
		}
		r.frames[slot].gen = 0
		again = append(again, slot)
	}
	r.mu.Unlock()
	if len(again) == 0 {
		return
	}
	r.logf("frames: the board missed keys %#02x, storing their frames again", mask)
	for _, slot := range again {
		if err := r.storeLocked(dev, gen, slot); err != nil {
			r.logf("frames: store slot %d again: %v", slot, err)
			return
		}
	}
	var err error
	if counts != nil {
		err = dev.ShowFramesWithPips(slots, counts)
	} else {
		err = dev.ShowFrames(slots)
	}
	if err != nil {
		r.logf("frames: show again: %v", err)
	}
}

// handleBatchIncomplete answers a Batch incomplete: an open batch on the board
// still waits for some glyphs and key states, usually because a loop pass was
// longer than the gap between two HID reports and the board lost one (task
// 0044). It sends the glyph and the key state that it holds for each key the
// board names, a glyph first as a replay does. A key it does not know, or a
// glyph it no longer holds, is skipped. The 2 s time-out on the board stays as
// the last resort (task 0063).
func (r *Reconnecting) handleBatchIncomplete(dev Transport, bi BatchIncomplete) {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	type item struct {
		key      byte
		pixels   []byte
		state    KeyState
		hasPix   bool
		hasState bool
	}
	r.mu.Lock()
	if r.dev != dev {
		r.mu.Unlock()
		return
	}
	var items []item
	for key := byte(0); key < 8; key++ {
		wantGlyph := bi.GlyphMask&(1<<key) != 0
		wantState := bi.StateMask&(1<<key) != 0
		if !wantGlyph && !wantState {
			continue
		}
		it := item{key: key}
		if g, ok := r.glyphs[key]; ok && wantGlyph {
			it.pixels, it.hasPix = g.pixels, true
		}
		if ks, ok := r.keys[key]; ok && wantState {
			it.state, it.hasState = ks, true
		}
		if it.hasPix || it.hasState {
			items = append(items, it)
		}
	}
	r.mu.Unlock()

	if len(items) == 0 {
		return
	}
	r.logf("batch: the board lacks glyphs %#02x and key states %#02x, sending them again", bi.GlyphMask, bi.StateMask)
	for _, it := range items {
		if it.hasPix {
			if err := dev.SendCustomGlyph(it.key, it.pixels); err != nil {
				r.logf("batch: glyph %d again: %v", it.key, err)
				return
			}
		}
		if it.hasState {
			if err := dev.SendKeyState(it.state); err != nil {
				r.logf("batch: key state %d again: %v", it.key, err)
				return
			}
		}
	}
}

// BeginBatch implements Transport. It sends the message when a device is
// connected, and keeps nothing: a batch only holds the draw of the glyphs
// and key states that follow it, and a replay after a reconnect sends one
// key at a time.
func (r *Reconnecting) BeginBatch(glyphMask, stateMask byte) error {
	r.sendMu.Lock()
	defer r.sendMu.Unlock()

	r.mu.Lock()
	dev := r.dev
	r.mu.Unlock()
	if dev == nil {
		return nil
	}
	return dev.BeginBatch(glyphMask, stateMask)
}

// ReadMessage implements Transport. It returns the next message from
// whichever device is connected, and io.EOF once Close has run.
func (r *Reconnecting) ReadMessage() (Message, error) {
	msg, ok := <-r.queue
	if !ok {
		return Message{}, io.EOF
	}
	return msg, nil
}

// Close implements Transport. It stops reconnecting and closes the
// connected device, if any.
func (r *Reconnecting) Close() error {
	var err error
	r.closeOnce.Do(func() {
		r.cancel()
		r.mu.Lock()
		dev := r.dev
		r.mu.Unlock()
		if dev != nil {
			err = dev.Close()
		}
		<-r.done
	})
	return err
}
