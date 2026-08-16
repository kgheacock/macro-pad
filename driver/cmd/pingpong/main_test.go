package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// fakeTransport is a transport.Transport test double. It records the key
// state ping sends and hands back a scripted ReadMessage result, with no
// board attached.
type fakeTransport struct {
	sentKeyState transport.KeyState
	readMsg      transport.Message
	readErr      error
}

func (f *fakeTransport) SendKeyState(ks transport.KeyState) error {
	f.sentKeyState = ks
	return nil
}

func (f *fakeTransport) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	return nil
}

func (f *fakeTransport) ReadMessage() (transport.Message, error) {
	return f.readMsg, f.readErr
}

func (f *fakeTransport) Close() error { return nil }

var _ transport.Transport = (*fakeTransport)(nil)

func TestMatchingNonce(t *testing.T) {
	const nonce = 0x42
	ft := &fakeTransport{
		readMsg: transport.Message{Type: transport.MessageTypePong, Pong: transport.Pong{Nonce: nonce}},
	}

	var out bytes.Buffer
	ok, err := ping(context.Background(), ft, nonce, &out)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if !ok {
		t.Fatal("ping with a matching nonce reported FAIL")
	}
	if got := strings.TrimSpace(out.String()); got != "PASS" {
		t.Fatalf("output = %q, want PASS", got)
	}
	if ft.sentKeyState.KeyIndex != transport.PingKeyIndex || ft.sentKeyState.EmojiID != nonce {
		t.Fatalf("sent key state = %+v, want KeyIndex %d and EmojiID %#x", ft.sentKeyState, transport.PingKeyIndex, byte(nonce))
	}
}

func TestWrongNonce(t *testing.T) {
	ft := &fakeTransport{
		readMsg: transport.Message{Type: transport.MessageTypePong, Pong: transport.Pong{Nonce: 0xAB}},
	}

	var out bytes.Buffer
	ok, err := ping(context.Background(), ft, 0x12, &out)
	if err != nil {
		t.Fatalf("ping: %v", err)
	}
	if ok {
		t.Fatal("ping with a mismatched nonce reported PASS")
	}
	if got := strings.TrimSpace(out.String()); got != "FAIL" {
		t.Fatalf("output = %q, want FAIL", got)
	}
}

func TestReadMessageError(t *testing.T) {
	wantErr := errors.New("boom")
	ft := &fakeTransport{readErr: wantErr}

	var out bytes.Buffer
	ok, err := ping(context.Background(), ft, 0x12, &out)
	if ok {
		t.Fatal("ping reported PASS despite a ReadMessage error")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("ping error = %v, want it to wrap %v", err, wantErr)
	}
	if got := strings.TrimSpace(out.String()); got != "FAIL" {
		t.Fatalf("output = %q, want FAIL", got)
	}
}
