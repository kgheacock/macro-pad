package transport

import (
	"io"
	"sync"
)

// emulatorQueueSize bounds how many device→host messages the emulator
// holds before a test reads them. It only needs to cover what a single
// test injects between ReadEvent/ReadAudioChunk calls.
const emulatorQueueSize = 32

// Emulator is an in-memory Transport for driver tests. No board is
// attached: key-state, press/release, and audio-chunk messages travel over
// io.Pipe, encoded and decoded exactly as docs/wire-protocol.md specifies.
// Test code calls InjectEvent or InjectAudioChunk to simulate the device
// side, and LastKeyState to inspect what the driver sent.
type Emulator struct {
	keyStateW *io.PipeWriter
	eventW    *io.PipeWriter
	audioW    *io.PipeWriter

	mu           sync.Mutex
	lastKeyState KeyState
	haveKeyState bool

	eventQueue chan Event
	audioQueue chan AudioChunk
}

// NewEmulator creates an Emulator ready for use. Call Close when done.
func NewEmulator() *Emulator {
	ksR, ksW := io.Pipe()
	evR, evW := io.Pipe()
	auR, auW := io.Pipe()

	e := &Emulator{
		keyStateW:  ksW,
		eventW:     evW,
		audioW:     auW,
		eventQueue: make(chan Event, emulatorQueueSize),
		audioQueue: make(chan AudioChunk, emulatorQueueSize),
	}

	go e.receiveKeyStates(ksR)
	go e.receiveEvents(evR)
	go e.receiveAudioChunks(auR)

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

func (e *Emulator) receiveEvents(r *io.PipeReader) {
	defer r.Close()
	defer close(e.eventQueue)
	for {
		ev, err := decodeEvent(r)
		if err != nil {
			return
		}
		e.eventQueue <- ev
	}
}

func (e *Emulator) receiveAudioChunks(r *io.PipeReader) {
	defer r.Close()
	defer close(e.audioQueue)
	for {
		c, err := decodeAudioChunk(r)
		if err != nil {
			return
		}
		e.audioQueue <- c
	}
}

// SendKeyState implements Transport. It encodes ks per
// docs/wire-protocol.md and delivers it to the emulator's device side.
func (e *Emulator) SendKeyState(ks KeyState) error {
	return encodeKeyState(e.keyStateW, ks)
}

// ReadEvent implements Transport. It blocks until a press/release event is
// injected with InjectEvent, or the emulator is closed.
func (e *Emulator) ReadEvent() (Event, error) {
	ev, ok := <-e.eventQueue
	if !ok {
		return Event{}, io.EOF
	}
	return ev, nil
}

// ReadAudioChunk implements Transport. It blocks until an audio chunk is
// injected with InjectAudioChunk, or the emulator is closed.
func (e *Emulator) ReadAudioChunk() (AudioChunk, error) {
	c, ok := <-e.audioQueue
	if !ok {
		return AudioChunk{}, io.EOF
	}
	return c, nil
}

// InjectEvent simulates the device emitting a press/release event, for
// driver tests with no board attached.
func (e *Emulator) InjectEvent(ev Event) error {
	return encodeEvent(e.eventW, ev)
}

// InjectAudioChunk simulates the device emitting one chunk of a buffered
// recording, for driver tests with no board attached.
func (e *Emulator) InjectAudioChunk(c AudioChunk) error {
	return encodeAudioChunk(e.audioW, c)
}

// LastKeyState returns the most recent key-state message the emulator's
// device side received, and whether one has arrived yet.
func (e *Emulator) LastKeyState() (KeyState, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastKeyState, e.haveKeyState
}

// Close releases the emulator's pipes. Any blocked ReadEvent or
// ReadAudioChunk call returns io.EOF.
func (e *Emulator) Close() error {
	e.keyStateW.Close()
	e.eventW.Close()
	e.audioW.Close()
	return nil
}

var _ Transport = (*Emulator)(nil)
