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
	keyStateW *io.PipeWriter
	msgW      *io.PipeWriter

	mu           sync.Mutex
	lastKeyState KeyState
	haveKeyState bool

	msgQueue chan Message
}

// NewEmulator creates an Emulator ready for use. Call Close when done.
func NewEmulator() *Emulator {
	ksR, ksW := io.Pipe()
	mR, mW := io.Pipe()

	e := &Emulator{
		keyStateW: ksW,
		msgW:      mW,
		msgQueue:  make(chan Message, emulatorQueueSize),
	}

	go e.receiveKeyStates(ksR)
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
	e.msgW.Close()
	return nil
}

var _ Transport = (*Emulator)(nil)
