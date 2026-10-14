package host

import (
	"testing"

	"github.com/kgheacock/macro-pad/driver/host/keyimg"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

func TestMessages_ImageAlwaysResetsColorAndBlink(t *testing.T) {
	png, err := keyimg.PNG(keyimg.Thumbnail(keyimg.Text("x", keyimg.Color{}, keyimg.Color{})))
	if err != nil {
		t.Fatal(err)
	}
	// An image with no color and no blink must still send the
	// follow-up, or a blink the board holds from before keeps going.
	msgs := KeyState{PNG: png}.Messages(2)
	if len(msgs) != 2 || msgs[0].Kind != plugin.KindSetCustomGlyph {
		t.Fatalf("got %+v, want a glyph then a key state", msgs)
	}
	want := plugin.SetKeyStatePayload{KeyIndex: 2, EmojiID: transport.CustomGlyphSentinelEmojiID}
	if got := *msgs[1].SetKeyState; got != want {
		t.Errorf("follow-up = %+v, want %+v", got, want)
	}
}

func TestSetBlinkFalse_OnImageKeyStopsBlink(t *testing.T) {
	var st KeyState
	k := NewKey(func(change func(*KeyState) error) error { return change(&st) })
	if err := k.SetImage(keyimg.Text("x", keyimg.Color{}, keyimg.Color{})); err != nil {
		t.Fatal(err)
	}
	k.SetBlink(true)
	k.SetBlink(false)
	msgs := st.Messages(0)
	if last := msgs[len(msgs)-1].SetKeyState; last == nil || last.Blink {
		t.Errorf("after SetBlink(false), last message = %+v, want blink off", last)
	}
}
