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

// TestSetKeyState proves SetKeyState builds a setKeyState message from
// all three fields at once, unlike SetEmoji and SetState which each
// leave one or two of them at a reset value.
func TestSetKeyState(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	if err := c.SetKeyState(2, 0x07E0, 0xF3, true); err != nil {
		t.Fatalf("SetKeyState: %v", err)
	}
	if len(ws.written) != 1 {
		t.Fatalf("got %d messages, want 1", len(ws.written))
	}

	msg := ws.written[0]
	if msg.Kind != plugin.KindSetKeyState || msg.SetKeyState == nil {
		t.Fatalf("got %+v, want a setKeyState message", msg)
	}
	want := plugin.SetKeyStatePayload{
		KeyIndex: 2,
		Color:    0x07E0,
		EmojiID:  0xF3,
		Blink:    true,
	}
	if *msg.SetKeyState != want {
		t.Fatalf("got %+v, want %+v", *msg.SetKeyState, want)
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

func TestSetCustomGlyph(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	pngBytes := []byte{0x89, 'P', 'N', 'G'} // stand-in bytes; SetCustomGlyph forwards them unexamined
	if err := c.SetCustomGlyph(4, pngBytes); err != nil {
		t.Fatalf("SetCustomGlyph: %v", err)
	}
	msg := ws.written[0]
	if msg.Kind != plugin.KindSetCustomGlyph || msg.SetCustomGlyph == nil {
		t.Fatalf("got %+v, want a setCustomGlyph message", msg)
	}
	if msg.SetCustomGlyph.KeyIndex != 4 || string(msg.SetCustomGlyph.Image) != string(pngBytes) {
		t.Fatalf("got %+v, want KeyIndex 4, Image %v", msg.SetCustomGlyph, pngBytes)
	}
}

// TestSetCustomGlyphBlink proves SetCustomGlyphBlink sends only a
// setKeyState message naming transport.CustomGlyphSentinelEmojiID, no
// setCustomGlyph payload — so a blink toggle never resends the image.
func TestSetCustomGlyphBlink(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	if err := c.SetCustomGlyphBlink(4, true); err != nil {
		t.Fatalf("SetCustomGlyphBlink: %v", err)
	}
	if len(ws.written) != 1 {
		t.Fatalf("got %d messages, want 1", len(ws.written))
	}

	msg := ws.written[0]
	if msg.Kind != plugin.KindSetKeyState || msg.SetKeyState == nil {
		t.Fatalf("got %+v, want a setKeyState message", msg)
	}
	if msg.SetCustomGlyph != nil {
		t.Fatalf("got a setCustomGlyph payload %+v, want none", msg.SetCustomGlyph)
	}
	want := plugin.SetKeyStatePayload{
		KeyIndex: 4,
		EmojiID:  transport.CustomGlyphSentinelEmojiID,
		Blink:    true,
	}
	if *msg.SetKeyState != want {
		t.Fatalf("got %+v, want %+v", *msg.SetKeyState, want)
	}
}

// TestSetCustomGlyphBlink_KeepsCurrentColor proves a blink toggle resends
// whatever color an earlier SetKeyState call set for the key, instead of
// resetting it to 0 — a message naming the sentinel still replaces
// Color, per docs/wire-protocol.md.
func TestSetCustomGlyphBlink_KeepsCurrentColor(t *testing.T) {
	ws := &fakeWS{}
	c := &Conn{ws: ws}

	if err := c.SetKeyState(4, 0x07E0, 0xF3, false); err != nil {
		t.Fatalf("SetKeyState: %v", err)
	}
	if err := c.SetCustomGlyphBlink(4, true); err != nil {
		t.Fatalf("SetCustomGlyphBlink: %v", err)
	}

	got := ws.written[1].SetKeyState
	if got.Color != 0x07E0 {
		t.Fatalf("got Color %#04x, want 0x07e0 (the color SetKeyState set)", got.Color)
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
