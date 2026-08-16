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

// PingKeyIndex is the reserved Key state index that signals a ping,
// answered with a Pong. No real key uses it — the six keys are indexed 0
// through 5. See "Ping" in docs/wire-protocol.md.
const PingKeyIndex = 255

// Pong is a device→host message answering a ping. Nonce echoes back the
// value the ping's Key state message carried in its Emoji ID byte. See
// "Ping" in docs/wire-protocol.md.
type Pong struct {
	Nonce byte
}

// TraceCode identifies which point in the firmware loop a TraceRecord
// marks. See the trace code registry in "Trace record" in
// docs/wire-protocol.md.
type TraceCode byte

const (
	TraceDropped            TraceCode = 0
	TraceHostMessageDecoded TraceCode = 1
	TraceSwitchRead         TraceCode = 2
	TraceDebounceVerdict    TraceCode = 3
	TraceEventWritten       TraceCode = 4
)

// TraceRecord is a device→host message emitted by firmware/trace.py's
// Tracer, when tracing is enabled. Payload's meaning depends on Code; see
// "Trace record" in docs/wire-protocol.md.
type TraceRecord struct {
	Code      TraceCode
	Key       byte
	Payload   uint16
	Timestamp uint64 // monotonic device time the record was taken, in microseconds
}

// Message is one decoded device→host message read from ReadMessage. Type
// says which of Event, AudioChunk, Pong, or Trace is populated.
type Message struct {
	Type       MessageType
	Event      Event
	AudioChunk AudioChunk
	Pong       Pong
	Trace      TraceRecord
}

// Transport is the driver's connection to a device, real or emulated. It
// covers the two wire-protocol exchanges: sending key state, and reading
// the device→host messages framed per "Framing" in docs/wire-protocol.md.
type Transport interface {
	// SendKeyState writes one key-state message to the device over HID.
	SendKeyState(KeyState) error

	// ReadMessage blocks until one device→host message arrives over CDC
	// serial, or the transport is closed. A frame whose type this package
	// does not know is skipped by its declared length and never returned.
	//
	// ReadMessage supports exactly one caller: Device and Emulator each
	// drain one internal channel, not duplicate it, so two goroutines
	// calling it on the same Transport steal messages from each other
	// instead of each seeing every one. A caller that needs more than one
	// reader wraps the Transport in a Fanout and gives each reader its own
	// Subscribe subscription instead.
	ReadMessage() (Message, error)

	// Close releases the transport's underlying handles. A ReadMessage
	// call blocked when Close runs returns io.EOF.
	Close() error
}
