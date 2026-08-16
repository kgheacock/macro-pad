package api

import (
	"io"
	"testing"

	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// fakeWS is a wsConn test double. It records every WriteJSON call, so a
// test can inspect the message SetEmoji, SetState, or Signal built with
// no live WebSocket connection.
type fakeWS struct {
	written []plugin.Message
}

func (f *fakeWS) WriteJSON(v any) error {
	f.written = append(f.written, v.(plugin.Message))
	return nil
}

func (f *fakeWS) ReadJSON(v any) error { return io.EOF }

func (f *fakeWS) Close() error { return nil }

// TestSetState_Alert proves SetState(0, "Alert", nil) builds a
// setKeyState message whose fields become the Key state HID message's
// byte layout for key 0, per docs/wire-protocol.md: Key index 0,
// Version 1 (filled in by the driver's plugin server, not this package),
// the named state's Color and Blink flag, Emoji ID left at the
// placeholder value 0.
func TestSetState_Alert(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	if err := c.SetState(0, "Alert", nil); err != nil {
		t.Fatalf("SetState: %v", err)
	}
	if len(ws.written) != 1 {
		t.Fatalf("got %d messages, want 1", len(ws.written))
	}

	msg := ws.written[0]
	if msg.Kind != plugin.KindSetKeyState || msg.SetKeyState == nil {
		t.Fatalf("got %+v, want a setKeyState message", msg)
	}

	// The plugin server fills in Version itself (see
	// driver/plugin/protocol.go's toKeyState) before this reaches the
	// wire; reconstruct that step here to check the full Key state byte
	// layout the message will become.
	got := transport.KeyState{
		KeyIndex: msg.SetKeyState.KeyIndex,
		Version:  transport.ProtocolVersion,
		Color:    msg.SetKeyState.Color,
		EmojiID:  msg.SetKeyState.EmojiID,
		Blink:    msg.SetKeyState.Blink,
	}
	want := transport.KeyState{
		KeyIndex: 0,
		Version:  transport.ProtocolVersion,
		Color:    0xF800, // red
		EmojiID:  0,      // placeholder glyph
		Blink:    true,
	}
	if got != want {
		t.Fatalf("got key state %+v, want %+v", got, want)
	}
}

func TestSetState_ColorOverride(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	override := uint16(0x001F) // blue
	if err := c.SetState(3, "Done", &override); err != nil {
		t.Fatalf("SetState: %v", err)
	}

	got := ws.written[0].SetKeyState
	if got.KeyIndex != 3 || got.Color != override || got.Blink != false {
		t.Fatalf("got %+v, want KeyIndex 3, Color %#04x (override), Blink false", got, override)
	}
}

func TestSetState_UnknownState(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	err := c.SetState(0, "NotARealState", nil)
	if err == nil {
		t.Fatal("got nil error, want one for an unrecognized named state")
	}
	if len(ws.written) != 0 {
		t.Fatalf("got %d messages sent, want 0 for a rejected state", len(ws.written))
	}
}

func TestSetEmoji(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	if err := c.SetEmoji(2, 0xF3); err != nil {
		t.Fatalf("SetEmoji: %v", err)
	}
	got := ws.written[0].SetKeyState
	if got.KeyIndex != 2 || got.EmojiID != 0xF3 {
		t.Fatalf("got %+v, want KeyIndex 2, EmojiID 0xF3", got)
	}
}

func TestSignal(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	if err := c.Signal(1, plugin.SignalProcessDone); err != nil {
		t.Fatalf("Signal: %v", err)
	}
	msg := ws.written[0]
	if msg.Kind != plugin.KindSignal || msg.Signal == nil {
		t.Fatalf("got %+v, want a signal message", msg)
	}
	if msg.Signal.KeyIndex != 1 || msg.Signal.Name != plugin.SignalProcessDone {
		t.Fatalf("got %+v, want {KeyIndex: 1, Name: %q}", msg.Signal, plugin.SignalProcessDone)
	}
}
