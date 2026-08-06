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

const (
	keyStateSize    = 6
	eventSize       = 10
	audioHeaderSize = 3 // stream ID + chunk length
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

func encodeEvent(w io.Writer, ev Event) error {
	buf := make([]byte, eventSize)
	buf[0] = ev.KeyIndex
	buf[1] = byte(ev.Type)
	binary.LittleEndian.PutUint64(buf[2:10], ev.Timestamp)
	_, err := w.Write(buf)
	return err
}

func decodeEvent(r io.Reader) (Event, error) {
	buf := make([]byte, eventSize)
	if _, err := io.ReadFull(r, buf); err != nil {
		return Event{}, err
	}
	return Event{
		KeyIndex:  buf[0],
		Type:      EventType(buf[1]),
		Timestamp: binary.LittleEndian.Uint64(buf[2:10]),
	}, nil
}

func encodeAudioChunk(w io.Writer, c AudioChunk) error {
	header := make([]byte, audioHeaderSize)
	header[0] = c.StreamID
	binary.LittleEndian.PutUint16(header[1:3], uint16(len(c.PCM)))
	if _, err := w.Write(header); err != nil {
		return err
	}
	if len(c.PCM) > 0 {
		if _, err := w.Write(c.PCM); err != nil {
			return err
		}
	}
	final := byte(0)
	if c.Final {
		final = 1
	}
	_, err := w.Write([]byte{final})
	return err
}

func decodeAudioChunk(r io.Reader) (AudioChunk, error) {
	header := make([]byte, audioHeaderSize)
	if _, err := io.ReadFull(r, header); err != nil {
		return AudioChunk{}, err
	}
	n := binary.LittleEndian.Uint16(header[1:3])
	pcm := make([]byte, n)
	if n > 0 {
		if _, err := io.ReadFull(r, pcm); err != nil {
			return AudioChunk{}, err
		}
	}
	finalByte := make([]byte, 1)
	if _, err := io.ReadFull(r, finalByte); err != nil {
		return AudioChunk{}, err
	}
	return AudioChunk{
		StreamID: header[0],
		PCM:      pcm,
		Final:    finalByte[0] != 0,
	}, nil
}
