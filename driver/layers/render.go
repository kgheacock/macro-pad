package layers

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"path/filepath"
	"strings"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// Key colors, RGB565, for each status. Picked by rendering every channel
// icon on every color; pure blue hid the dark icons, so RUNNING is lifted.
const (
	ColorRunning uint16 = 0x3AFF // blue #3D5CFF
	ColorDone    uint16 = 0x16C9 // green #12D84A
	ColorError   uint16 = 0xF800 // red
	ColorWarn    uint16 = 0xFFE0 // yellow
	ColorNone    uint16 = 0x0000 // black: no status
	// ColorUp is the dark background behind the shift icon and the arrows.
	ColorUp uint16 = 0x2104 // #202020
)

// ShiftIcon is the file under the assets directory that key UpKey shows in
// a child layer. LeftIcon and RightIcon are the arrows of a child layer with
// pages (task 0078), drawn like it.
const (
	ShiftIcon = "shift.png"
	LeftIcon  = "left.png"
	RightIcon = "right.png"
)

// StatusColor returns the key color for a status.
func StatusColor(status string) uint16 {
	switch status {
	case StatusRunning:
		return ColorRunning
	case StatusDone:
		return ColorDone
	case StatusError:
		return ColorError
	case StatusWarn:
		return ColorWarn
	}
	return ColorNone
}

// assetPath resolves a validated iconRef under dir. It returns an error
// when the resolved file, after symlinks, is not under dir.
func assetPath(dir, ref string) (string, error) {
	if err := ValidateIconRef(ref); err != nil {
		return "", err
	}
	full := filepath.Join(dir, filepath.FromSlash(ref))
	realDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return "", err
	}
	realFull, err := filepath.EvalSymlinks(full)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realDir, realFull)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("iconRef %q leaves assets/", ref)
	}
	return realFull, nil
}

// RenderGlyph is renderGlyph for a tool that stores frames itself, as
// `blinksend --scenario=pips` does: the same picture a layer shows.
func RenderGlyph(assetsDir, ref string, count int, keyColor uint16) ([]byte, error) {
	return renderGlyph(assetsDir, ref, count, keyColor)
}

// renderGlyph builds the 128x128 PNG for one key: the icon at ref under
// assetsDir, if ref is not empty, on a white round, and a count pip when count
// is above zero. Transparent pixels show the key color behind them. keyColor is
// the color the edges of the round and the pip are blended against, so that no
// edge pixel is half transparent (task 0062).
func renderGlyph(assetsDir, ref string, count int, keyColor uint16) ([]byte, error) {
	return renderIcon(assetsDir, ref, "", "", count, keyColor)
}

// renderIcon is renderGlyph for an icon that is a PNG at ref, the words in text
// (task 0069), or the emoji in emoji, joined by line breaks, with text as their
// caption (task 0073). A text icon is a plain white tile, so the key color
// cannot show; the pip is blended against ColorNone, as the board draws it on a
// frame stored for a text icon. An emoji icon is a white tile too, with the key
// color only in its strip (task 0082), so its pip is blended the same way.
func renderIcon(assetsDir, ref, text, emoji string, count int, keyColor uint16) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, transport.CustomGlyphWidth, transport.CustomGlyphHeight))
	if emoji != "" {
		refs := splitEmoji(emoji)
		if ref != "" {
			return nil, errors.New("iconRef and iconEmoji are exclusive")
		}
		if err := ValidateIconEmoji(refs); err != nil {
			return nil, err
		}
		if err := validateIconCaption(text); err != nil {
			return nil, err
		}
		if err := drawIconEmoji(canvas, assetsDir, refs, text, keyColor); err != nil {
			return nil, err
		}
		text = ""
		keyColor = ColorNone
	}
	if text != "" {
		if ref != "" {
			return nil, errors.New("iconRef and iconText are exclusive")
		}
		if err := ValidateIconText(text); err != nil {
			return nil, err
		}
		if err := drawIconText(canvas, text); err != nil {
			return nil, err
		}
		keyColor = ColorNone
	}
	if ref != "" {
		icon, err := loadIcon(assetsDir, ref)
		if err != nil {
			return nil, err
		}
		drawIconRound(canvas, keyColor)
		draw.Draw(canvas, canvas.Bounds(), icon, icon.Bounds().Min, draw.Over)
	}
	if count > 0 {
		if err := DrawPip(canvas, keyColor, count); err != nil {
			return nil, err
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
