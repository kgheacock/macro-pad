package layers

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"os"
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
	ColorNone    uint16 = 0xFFFF // white: no status
	// ColorUp is the dark background behind the shift icon.
	ColorUp uint16 = 0x2104 // #202020
)

// ShiftIcon is the file under the assets directory that key UpKey shows in
// a child layer.
const ShiftIcon = "shift.png"

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

// renderGlyph builds the 128x128 PNG for one key: the icon at ref under
// assetsDir, if ref is not empty, on a white round, and a count pip when count
// is above zero. Transparent pixels show the key color behind them. keyColor is
// the color the edges of the round and the pip are blended against, so that no
// edge pixel is half transparent (task 0062).
func renderGlyph(assetsDir, ref string, count int, keyColor uint16) ([]byte, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, transport.CustomGlyphWidth, transport.CustomGlyphHeight))
	if ref != "" {
		p, err := assetPath(assetsDir, ref)
		if err != nil {
			return nil, err
		}
		f, err := os.Open(p)
		if err != nil {
			return nil, err
		}
		defer f.Close()
		icon, err := png.Decode(f)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ref, err)
		}
		b := icon.Bounds()
		if b.Dx() != transport.CustomGlyphWidth || b.Dy() != transport.CustomGlyphHeight {
			return nil, fmt.Errorf("%s: %w: got %dx%d", ref, transport.ErrInvalidGlyphSize, b.Dx(), b.Dy())
		}
		drawIconRound(canvas, keyColor)
		draw.Draw(canvas, canvas.Bounds(), icon, b.Min, draw.Over)
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
