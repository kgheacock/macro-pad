package transport

import (
	"io"
	"sync"
)

// emulatorQueueSize bounds how many device→host messages the emulator
// holds before a test reads them. It only needs to cover what a single
// test injects between ReadMessage calls.
const emulatorQueueSize = 32

// Emulator is an in-memory Transport for driver tests. No board is
// attached: key-state and device→host messages travel over io.Pipe,
// encoded and decoded exactly as docs/wire-protocol.md specifies. Both
// device→host message types share one pipe, matching how a real CDC
// stream carries them. Test code calls InjectEvent or InjectAudioChunk to
// simulate the device side, and LastKeyState to inspect what the driver
// sent.
type Emulator struct {
	keyStateW    *io.PipeWriter
	customGlyphW *io.PipeWriter
	msgW         *io.PipeWriter

	mu              sync.Mutex
	lastKeyState    KeyState
	haveKeyState    bool
	lastCustomGlyph CustomGlyph
	haveCustomGlyph bool
	lastBeginBatch  [2]byte
	haveBeginBatch  bool
	frames          map[byte]StoredFrame
	lastShow        []byte
	shows           int

	msgQueue chan Message
}

// NewEmulator creates an Emulator ready for use. Call Close when done.
func NewEmulator() *Emulator {
	ksR, ksW := io.Pipe()
	cgR, cgW := io.Pipe()
	mR, mW := io.Pipe()

	e := &Emulator{
		keyStateW:    ksW,
		customGlyphW: cgW,
		msgW:         mW,
		msgQueue:     make(chan Message, emulatorQueueSize),
		frames:       make(map[byte]StoredFrame),
	}

	go e.receiveKeyStates(ksR)
	go e.receiveCustomGlyphs(cgR)
	go e.receiveMessages(mR)

	return e
}

func (e *Emulator) receiveKeyStates(r *io.PipeReader) {
	defer r.Close()
	for {
		ks, err := decodeKeyState(r)
		if err != nil {
			return
		}
		e.mu.Lock()
		e.lastKeyState = ks
		e.haveKeyState = true
		e.mu.Unlock()
	}
}

func (e *Emulator) receiveCustomGlyphs(r *io.PipeReader) {
	defer r.Close()
	for {
		t, payload, err := readFrame(r)
		if err != nil {
			return
		}
		var cg CustomGlyph
		switch t {
		case MessageTypeSetCustomGlyph:
			cg, err = decodeCustomGlyph(payload)
		case MessageTypeSetCustomGlyphCompressed:
			cg, err = decodeCompressedCustomGlyph(payload)
		case MessageTypeBeginBatch:
			glyphMask, stateMask, err := decodeBeginBatch(payload)
			if err == nil {
				e.mu.Lock()
				e.lastBeginBatch = [2]byte{glyphMask, stateMask}
				e.haveBeginBatch = true
				e.mu.Unlock()
			}
			continue
		case MessageTypeStoreFrame:
			if f, err := decodeStoreFrame(payload); err == nil {
				e.mu.Lock()
				e.frames[f.Slot] = f
				e.mu.Unlock()
			}
			continue
		case MessageTypeShowFrames:
			if slots, err := decodeShowFrames(payload); err == nil {
				e.mu.Lock()
				e.lastShow = append([]byte(nil), slots...)
				e.shows++
				// The emulator's board draws each named key, so what a test
				// reads back as the last key state and glyph is what a Set
				// custom glyph and a key state would have left.
				for key, slot := range slots {
					f, ok := e.frames[slot]
					if slot == SlotUnchanged || !ok {
						continue
					}
					ks := KeyState{KeyIndex: byte(key), Version: ProtocolVersion, Color: f.Color}
					if f.Pixels != nil {
						ks.EmojiID = CustomGlyphSentinelEmojiID
						e.lastCustomGlyph = CustomGlyph{KeyIndex: byte(key), Pixels: f.Pixels}
						e.haveCustomGlyph = true
					}
					e.lastKeyState, e.haveKeyState = ks, true
				}
				e.mu.Unlock()
			}
			continue
		default:
			continue
		}
		if err != nil {
			continue
		}
		e.mu.Lock()
		e.lastCustomGlyph = cg
		e.haveCustomGlyph = true
		e.mu.Unlock()
	}
}

func (e *Emulator) receiveMessages(r *io.PipeReader) {
	defer r.Close()
	defer close(e.msgQueue)
	for {
		msg, err := readMessage(r)
		if err != nil {
			return
		}
		e.msgQueue <- msg
	}
}

// SendKeyState implements Transport. It encodes ks per
// docs/wire-protocol.md and delivers it to the emulator's device side.
func (e *Emulator) SendKeyState(ks KeyState) error {
	return encodeKeyState(e.keyStateW, ks)
}

// SendCustomGlyph implements Transport. It encodes a Set custom glyph
// message per docs/wire-protocol.md and delivers it to the emulator's
// device side.
func (e *Emulator) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	t, payload, err := encodeCustomGlyphFrame(keyIndex, pixels)
	if err != nil {
		return err
	}
	return writeFrame(e.customGlyphW, t, payload)
}

// BeginBatch implements Transport. It encodes a Begin batch message per
// docs/wire-protocol.md and delivers it to the emulator's device side, on the
// same pipe as the custom glyphs.
func (e *Emulator) BeginBatch(glyphMask, stateMask byte) error {
	return writeFrame(e.customGlyphW, MessageTypeBeginBatch, encodeBeginBatch(glyphMask, stateMask))
}

// StoreFrame implements Transport. It encodes a Store frame message per
// docs/wire-protocol.md and delivers it to the emulator's device side, which
// keeps the frame in a slot table.
func (e *Emulator) StoreFrame(slot byte, color uint16, pixels []byte) error {
	payload, err := encodeStoreFrame(slot, color, pixels)
	if err != nil {
		return err
	}
	return writeFrame(e.customGlyphW, MessageTypeStoreFrame, payload)
}

// ShowFrames implements Transport. It encodes a Show frames message per
// docs/wire-protocol.md and delivers it to the emulator's device side.
func (e *Emulator) ShowFrames(slots []byte) error {
	payload, err := encodeShowFrames(slots)
	if err != nil {
		return err
	}
	return writeFrame(e.customGlyphW, MessageTypeShowFrames, payload)
}

// StoredFrame returns the frame the emulator's device side holds in slot, and
// whether a Store frame message for it has arrived.
func (e *Emulator) StoredFrame(slot byte) (StoredFrame, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	f, ok := e.frames[slot]
	return f, ok
}

// LastShowFrames returns the slots of the most recent Show frames message the
// emulator's device side received, and how many such messages have arrived.
func (e *Emulator) LastShowFrames() (slots []byte, count int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]byte(nil), e.lastShow...), e.shows
}

// InjectShowMiss simulates the device answering a Show frames that named an
// empty slot, for driver tests with no board attached.
func (e *Emulator) InjectShowMiss(mask byte) error {
	return writeFrame(e.msgW, MessageTypeShowMiss, encodeShowMiss(mask))
}

// LastBeginBatch returns the glyph mask and key state mask of the most
// recent Begin batch message the emulator's device side received, and
// whether one has arrived yet.
func (e *Emulator) LastBeginBatch() (glyphMask, stateMask byte, ok bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastBeginBatch[0], e.lastBeginBatch[1], e.haveBeginBatch
}

// LastCustomGlyph returns the most recent Set custom glyph message the
// emulator's device side received, and whether one has arrived yet.
func (e *Emulator) LastCustomGlyph() (CustomGlyph, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastCustomGlyph, e.haveCustomGlyph
}

// ReadMessage implements Transport. It blocks until a message is injected
// with InjectEvent or InjectAudioChunk, or the emulator is closed.
func (e *Emulator) ReadMessage() (Message, error) {
	msg, ok := <-e.msgQueue
	if !ok {
		return Message{}, io.EOF
	}
	return msg, nil
}

// InjectEvent simulates the device emitting a press/release event, for
// driver tests with no board attached.
func (e *Emulator) InjectEvent(ev Event) error {
	return writeFrame(e.msgW, MessageTypeEvent, encodeEvent(ev))
}

// InjectAudioChunk simulates the device emitting one chunk of a buffered
// recording, for driver tests with no board attached.
func (e *Emulator) InjectAudioChunk(c AudioChunk) error {
	return writeFrame(e.msgW, MessageTypeAudioChunk, encodeAudioChunk(c))
}

// InjectTraceRecord simulates the device emitting one trace record, for
// driver tests with no board attached.
func (e *Emulator) InjectTraceRecord(tr TraceRecord) error {
	return writeFrame(e.msgW, MessageTypeTrace, encodeTraceRecord(tr))
}

// InjectPong simulates the device answering a ping, for driver tests with
// no board attached.
func (e *Emulator) InjectPong(nonce byte) error {
	return writeFrame(e.msgW, MessageTypePong, encodePong(nonce))
}

// LastKeyState returns the most recent key-state message the emulator's
// device side received, and whether one has arrived yet.
func (e *Emulator) LastKeyState() (KeyState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastKeyState, e.haveKeyState
}

// Close releases the emulator's pipes. Any blocked ReadMessage call
// returns io.EOF.
func (e *Emulator) Close() error {
	e.keyStateW.Close()
	e.customGlyphW.Close()
	e.msgW.Close()
	return nil
}

var _ Transport = (*Emulator)(nil)
