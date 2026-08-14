package transport

import (
	"bytes"
	"errors"
	"io"
	"testing"
	"time"
)

func TestEmulator_KeyStateRoundTrip(t *testing.T) {
	ks := KeyState{
		KeyIndex: 2,
		Version:  ProtocolVersion,
		Color:    0xF800, // red, RGB565
		EmojiID:  7,
		Blink:    true,
	}

	// The wire bytes must match docs/wire-protocol.md's Key state layout
	// exactly: index, version, color (little-endian uint16), emoji ID,
	// blink flag.
	var buf bytes.Buffer
	if err := encodeKeyState(&buf, ks); err != nil {
		t.Fatalf("encodeKeyState: %v", err)
	}
	want := []byte{2, ProtocolVersion, 0x00, 0xF8, 7, 1}
	if got := buf.Bytes(); !bytes.Equal(got, want) {
		t.Fatalf("wire bytes = % x, want % x", got, want)
	}

	e := NewEmulator()
	defer e.Close()

	if err := e.SendKeyState(ks); err != nil {
		t.Fatalf("SendKeyState: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		if got, ok := e.LastKeyState(); ok {
			if got != ks {
				t.Fatalf("LastKeyState = %+v, want %+v", got, ks)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for emulator to receive key state")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestEmulator_InjectPressEvent(t *testing.T) {
	e := NewEmulator()
	defer e.Close()

	want := Event{KeyIndex: 1, Type: EventPress, Timestamp: 123456}
	if err := e.InjectEvent(want); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}

	msg, err := e.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Type != MessageTypeEvent || msg.Event != want {
		t.Fatalf("ReadMessage = %+v, want Event %+v", msg, want)
	}
}

func TestEmulator_AudioFinalChunk(t *testing.T) {
	e := NewEmulator()
	defer e.Close()

	chunks := []AudioChunk{
		{StreamID: 5, PCM: []byte{1, 2, 3}, Final: false},
		{StreamID: 5, PCM: []byte{4, 5}, Final: false},
		{StreamID: 5, PCM: []byte{6}, Final: true},
	}
	for _, c := range chunks {
		if err := e.InjectAudioChunk(c); err != nil {
			t.Fatalf("InjectAudioChunk: %v", err)
		}
	}

	var got []AudioChunk
	for {
		msg, err := e.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if msg.Type != MessageTypeAudioChunk {
			t.Fatalf("ReadMessage type = %v, want MessageTypeAudioChunk", msg.Type)
		}
		got = append(got, msg.AudioChunk)
		if msg.AudioChunk.Final {
			break
		}
	}

	if len(got) != len(chunks) {
		t.Fatalf("read %d chunks before final flag, want %d", len(got), len(chunks))
	}
	for i, c := range got {
		if !bytes.Equal(c.PCM, chunks[i].PCM) || c.StreamID != chunks[i].StreamID || c.Final != chunks[i].Final {
			t.Fatalf("chunk %d = %+v, want %+v", i, c, chunks[i])
		}
	}
	if !got[len(got)-1].Final {
		t.Fatal("last chunk read did not carry the final-chunk flag")
	}
}

func TestEmulator_VersionMismatch(t *testing.T) {
	e := NewEmulator()
	defer e.Close()

	ks := KeyState{KeyIndex: 0, Version: ProtocolVersion + 1, Color: 0, EmojiID: 0}
	err := e.SendKeyState(ks)
	if err == nil {
		t.Fatal("SendKeyState with unrecognized version returned no error")
	}
	if !errors.Is(err, ErrUnsupportedVersion) {
		t.Fatalf("SendKeyState error = %v, want errors.Is(err, ErrUnsupportedVersion)", err)
	}

	if _, ok := e.LastKeyState(); ok {
		t.Fatal("emulator recorded a key state despite the version mismatch")
	}
}

func TestReadMessageMixedStream(t *testing.T) {
	var buf bytes.Buffer

	ev1 := Event{KeyIndex: 1, Type: EventPress, Timestamp: 100}
	chunk := AudioChunk{StreamID: 5, PCM: []byte{9, 8, 7}, Final: true}
	ev2 := Event{KeyIndex: 2, Type: EventRelease, Timestamp: 200}

	if err := writeFrame(&buf, MessageTypeEvent, encodeEvent(ev1)); err != nil {
		t.Fatalf("writeFrame event 1: %v", err)
	}
	if err := writeFrame(&buf, MessageTypeAudioChunk, encodeAudioChunk(chunk)); err != nil {
		t.Fatalf("writeFrame audio chunk: %v", err)
	}
	if err := writeFrame(&buf, MessageTypeEvent, encodeEvent(ev2)); err != nil {
		t.Fatalf("writeFrame event 2: %v", err)
	}

	got1, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage 1: %v", err)
	}
	if got1.Type != MessageTypeEvent || got1.Event != ev1 {
		t.Fatalf("message 1 = %+v, want Event %+v", got1, ev1)
	}

	got2, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage 2: %v", err)
	}
	if got2.Type != MessageTypeAudioChunk || !bytes.Equal(got2.AudioChunk.PCM, chunk.PCM) ||
		got2.AudioChunk.StreamID != chunk.StreamID || got2.AudioChunk.Final != chunk.Final {
		t.Fatalf("message 2 = %+v, want AudioChunk %+v", got2, chunk)
	}

	got3, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage 3: %v", err)
	}
	if got3.Type != MessageTypeEvent || got3.Event != ev2 {
		t.Fatalf("message 3 = %+v, want Event %+v", got3, ev2)
	}
}

func TestReadMessageUnknownType(t *testing.T) {
	var buf bytes.Buffer

	if err := writeFrame(&buf, MessageType(0xFE), []byte{1, 2, 3, 4}); err != nil {
		t.Fatalf("writeFrame unknown type: %v", err)
	}

	want := Event{KeyIndex: 3, Type: EventPress, Timestamp: 42}
	if err := writeFrame(&buf, MessageTypeEvent, encodeEvent(want)); err != nil {
		t.Fatalf("writeFrame event: %v", err)
	}

	got, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if got.Type != MessageTypeEvent || got.Event != want {
		t.Fatalf("readMessage = %+v, want Event %+v", got, want)
	}
}

func TestReadMessageTruncated(t *testing.T) {
	var buf bytes.Buffer

	header := []byte{byte(MessageTypeEvent), 40, 0} // declares a 40-byte payload
	buf.Write(header)
	buf.Write(make([]byte, 10)) // stream ends after 10 payload bytes

	msg, err := readMessage(&buf)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("readMessage error = %v, want io.ErrUnexpectedEOF", err)
	}
	if msg.Type != 0 || msg.Event != (Event{}) || msg.AudioChunk.StreamID != 0 ||
		msg.AudioChunk.PCM != nil || msg.AudioChunk.Final {
		t.Fatalf("readMessage returned %+v on truncation, want zero value", msg)
	}
}
