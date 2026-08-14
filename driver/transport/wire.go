package transport

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrUnsupportedVersion is returned when a key-state message's version byte
// does not match ProtocolVersion, instead of guessing at field offsets.
var ErrUnsupportedVersion = errors.New("transport: unsupported protocol version")

// MessageType identifies which device→host message a frame carries. See
// the type registry in the "Framing" section of docs/wire-protocol.md.
type MessageType byte

const (
	MessageTypeEvent      MessageType = 1
	MessageTypeAudioChunk MessageType = 2
)

const (
	keyStateSize    = 6
	eventSize       = 10
	frameHeaderSize = 3 // type + payload length, little-endian uint16
)

func encodeKeyState(w io.Writer, ks KeyState) error {
	if ks.Version != ProtocolVersion {
		return fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, ks.Version, ProtocolVersion)
	}
	buf := make([]byte, keyStateSize)
	buf[0] = ks.KeyIndex
	buf[1] = ks.Version
	binary.LittleEndian.PutUint16(buf[2:4], ks.Color)
	buf[4] = ks.EmojiID
	if ks.Blink {
		buf[5] = 1
	}
	_, err := w.Write(buf)
	return err
}

func decodeKeyState(r io.Reader) (KeyState, error) {
	buf := make([]byte, keyStateSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return KeyState{}, err
	}
	ks := KeyState{
		KeyIndex: buf[0],
		Version:  buf[1],
		Color:    binary.LittleEndian.Uint16(buf[2:4]),
		EmojiID:  buf[4],
		Blink:    buf[5] != 0,
	}
	if ks.Version != ProtocolVersion {
		return KeyState{}, fmt.Errorf("%w: got %d, want %d", ErrUnsupportedVersion, ks.Version, ProtocolVersion)
	}
	return ks, nil
}

// writeFrame writes one device→host frame: a type byte, a little-endian
// uint16 payload length, then the payload itself. See "Framing" in
// docs/wire-protocol.md.
func writeFrame(w io.Writer, t MessageType, payload []byte) error {
	header := make([]byte, frameHeaderSize)
	header[0] = byte(t)
	binary.LittleEndian.PutUint16(header[1:3], uint16(len(payload)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(payload) == 0 {
		return nil
	}
	_, err := w.Write(payload)
	return err
}

// readFrame reads one frame's header and exactly the payload bytes its
// length declares, whether or not t is a known MessageType. A short read
// on either the header or the payload returns io.ErrUnexpectedEOF (or
// io.EOF at a clean boundary before the header), never a partial value.
func readFrame(r io.Reader) (t MessageType, payload []byte, err error) {
	header := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return 0, nil, err
	}
	n := binary.LittleEndian.Uint16(header[1:3])
	payload = make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, payload); err != nil {
			return 0, nil, err
		}
	}
	return MessageType(header[0]), payload, nil
}

// decodeMessage decodes a frame's payload into a Message. ok is false when
// t is not a known MessageType; the caller has already consumed the
// payload by its declared length and should read the next frame.
func decodeMessage(t MessageType, payload []byte) (msg Message, ok bool, err error) {
	switch t {
	case MessageTypeEvent:
		ev, err := decodeEvent(payload)
		if err != nil {
			return Message{}, false, err
		}
		return Message{Type: t, Event: ev}, true, nil
	case MessageTypeAudioChunk:
		c, err := decodeAudioChunk(payload)
		if err != nil {
			return Message{}, false, err
		}
		return Message{Type: t, AudioChunk: c}, true, nil
	default:
		return Message{}, false, nil
	}
}

// readMessage reads frames from r until one decodes to a known
// MessageType, skipping any unknown type by the length its header
// declares.
func readMessage(r io.Reader) (Message, error) {
	for {
		t, payload, err := readFrame(r)
		if err != nil {
			return Message{}, err
		}
		msg, ok, err := decodeMessage(t, payload)
		if err != nil {
			return Message{}, err
		}
		if ok {
			return msg, nil
		}
	}
}

func encodeEvent(ev Event) []byte {
	buf := make([]byte, eventSize)
	buf[0] = ev.KeyIndex
	buf[1] = byte(ev.Type)
	binary.LittleEndian.PutUint64(buf[2:10], ev.Timestamp)
	return buf
}

func decodeEvent(payload []byte) (Event, error) {
	if len(payload) != eventSize {
		return Event{}, fmt.Errorf("transport: event payload is %d bytes, want %d", len(payload), eventSize)
	}
	return Event{
		KeyIndex:  payload[0],
		Type:      EventType(payload[1]),
		Timestamp: binary.LittleEndian.Uint64(payload[2:10]),
	}, nil
}

// encodeAudioChunk builds an audio chunk's frame payload: stream ID, PCM
// samples, then the final-chunk flag. The chunk's length is no longer
// self-described; the frame header carries it.
func encodeAudioChunk(c AudioChunk) []byte {
	buf := make([]byte, 0, 2+len(c.PCM))
	buf = append(buf, c.StreamID)
	buf = append(buf, c.PCM...)
	final := byte(0)
	if c.Final {
		final = 1
	}
	return append(buf, final)
}

func decodeAudioChunk(payload []byte) (AudioChunk, error) {
	if len(payload) < 2 {
		return AudioChunk{}, fmt.Errorf("transport: audio chunk payload is %d bytes, want at least 2", len(payload))
	}
	return AudioChunk{
		StreamID: payload[0],
		PCM:      payload[1 : len(payload)-1],
		Final:    payload[len(payload)-1] != 0,
	}, nil
}
