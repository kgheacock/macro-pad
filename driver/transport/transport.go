// Package transport defines the driver-side interface to the wire protocol
// described in docs/wire-protocol.md, and an in-memory emulator that
// implements it without a physical device attached.
package transport

// ProtocolVersion is the version byte this package builds against. It
// matches the "Versioning" section of docs/wire-protocol.md.
const ProtocolVersion = 1

// KeyState is a host→device message that sets one key's emoji, color, and
// blink state. See "Key state" in docs/wire-protocol.md.
type KeyState struct {
	KeyIndex byte
	Version  byte
	Color    uint16 // RGB565
	EmojiID  byte
	Blink    bool
}

// EventType identifies whether a press/release event is a press or a
// release.
type EventType byte

const (
	EventPress   EventType = 0
	EventRelease EventType = 1
)

// Event is a device→host message reporting a raw, debounced press or
// release. See "Press/release event" in docs/wire-protocol.md.
type Event struct {
	KeyIndex  byte
	Type      EventType
	Timestamp uint64 // monotonic time of the event, in microseconds
}

// AudioChunk is a device→host message carrying one chunk of a buffered
// audio recording. See "Audio chunk" in docs/wire-protocol.md.
type AudioChunk struct {
	StreamID byte
	PCM      []byte
	Final    bool
}

// Transport is the driver's connection to a device, real or emulated. It
// covers the three wire-protocol exchanges: sending key state, reading
// press/release events, and reading audio chunks.
type Transport interface {
	// SendKeyState writes one key-state message to the device over HID.
	SendKeyState(KeyState) error

	// ReadEvent blocks until one press/release event arrives over CDC
	// serial, or the transport is closed.
	ReadEvent() (Event, error)

	// ReadAudioChunk blocks until one audio chunk arrives over CDC serial,
	// or the transport is closed.
	ReadAudioChunk() (AudioChunk, error)
}
