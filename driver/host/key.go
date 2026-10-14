package host

import (
	"fmt"
	"image"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/host/keyimg"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// KeyState is everything the host remembers about one key: what a Key
// call last set. The zero value is a key that is off.
type KeyState struct {
	Color uint16 // RGB565
	Emoji byte
	Blink bool
	PNG   []byte // a 128x128 PNG, or nil for no image
}

// Messages builds the messages that show s on key.
func (s KeyState) Messages(key int) []plugin.Message {
	k := byte(key)
	if s.PNG == nil {
		return []plugin.Message{setKeyState(k, s.Color, s.Emoji, s.Blink)}
	}
	msgs := []plugin.Message{{
		Kind:           plugin.KindSetCustomGlyph,
		SetCustomGlyph: &plugin.SetCustomGlyphPayload{KeyIndex: k, Image: s.PNG},
	}}
	// The board keeps a key's color and blink across a glyph push, so
	// always follow it with the cached ones, or a blink set before (or on
	// another page) would carry on behind this image.
	return append(msgs, setKeyState(k, s.Color, transport.CustomGlyphSentinelEmojiID, s.Blink))
}

func setKeyState(k byte, color uint16, emoji byte, blink bool) plugin.Message {
	return plugin.Message{
		Kind:        plugin.KindSetKeyState,
		SetKeyState: &plugin.SetKeyStatePayload{KeyIndex: k, Color: color, EmojiID: emoji, Blink: blink},
	}
}

// NewKey returns a Key whose every call applies its change through
// apply. The host and hosttest's fake both build their keys with it, so
// a plugin's tests see the same KeyState the host would send.
func NewKey(apply func(change func(*KeyState) error) error) Key {
	return funcKey(apply)
}

type funcKey func(change func(*KeyState) error) error

func (k funcKey) SetColor(c keyimg.Color) error {
	return k(func(s *KeyState) error {
		*s = KeyState{Color: c.RGB565()}
		return nil
	})
}

func (k funcKey) SetState(name string) error {
	color, blink, ok := api.NamedState(name)
	if !ok {
		return fmt.Errorf("%w: %q", api.ErrUnknownState, name)
	}
	return k(func(s *KeyState) error {
		*s = KeyState{Color: color, Blink: blink}
		return nil
	})
}

func (k funcKey) SetBlink(on bool) error {
	return k(func(s *KeyState) error {
		s.Blink = on
		return nil
	})
}

func (k funcKey) SetImage(img image.Image) error {
	png, err := keyimg.PNG(keyimg.Thumbnail(img))
	if err != nil {
		return err
	}
	return k.SetPNG(png)
}

func (k funcKey) SetPNG(png []byte) error {
	// Fail here, in the plugin's call, rather than as a dropped message
	// in the daemon's log.
	if _, err := transport.DecodePNGToRGB565(png); err != nil {
		return err
	}
	return k(func(s *KeyState) error {
		s.PNG = png
		s.Emoji = 0
		return nil
	})
}

func (k funcKey) Clear() error {
	return k(func(s *KeyState) error {
		*s = KeyState{}
		return nil
	})
}

// notOwned is the Key an instance gets for a key it does not own.
type notOwned struct{}

func (notOwned) SetColor(keyimg.Color) error { return ErrNotOwner }
func (notOwned) SetState(string) error       { return ErrNotOwner }
func (notOwned) SetBlink(bool) error         { return ErrNotOwner }
func (notOwned) SetImage(image.Image) error  { return ErrNotOwner }
func (notOwned) SetPNG([]byte) error         { return ErrNotOwner }
func (notOwned) Clear() error                { return ErrNotOwner }

// NotOwnedKey is the Key every call of which returns ErrNotOwner.
// hosttest's fake hands it out for a key its instance does not own.
var NotOwnedKey Key = notOwned{}
