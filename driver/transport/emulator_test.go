package transport

import (
	"bytes"
	"errors"
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

	got, err := e.ReadEvent()
	if err != nil {
		t.Fatalf("ReadEvent: %v", err)
	}
	if got != want {
		t.Fatalf("ReadEvent = %+v, want %+v", got, want)
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
		c, err := e.ReadAudioChunk()
		if err != nil {
			t.Fatalf("ReadAudioChunk: %v", err)
		}
		got = append(got, c)
		if c.Final {
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
