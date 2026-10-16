package layers

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strconv"
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

// digitFont is a 3x5 bitmap per character, one row per entry, bit 2 on the
// left. The count badge needs only digits and "+".
var digitFont = map[rune][5]uint8{
	'0': {0b111, 0b101, 0b101, 0b101, 0b111},
	'1': {0b010, 0b110, 0b010, 0b010, 0b111},
	'2': {0b111, 0b001, 0b111, 0b100, 0b111},
	'3': {0b111, 0b001, 0b111, 0b001, 0b111},
	'4': {0b101, 0b101, 0b111, 0b001, 0b001},
	'5': {0b111, 0b100, 0b111, 0b001, 0b111},
	'6': {0b111, 0b100, 0b111, 0b101, 0b111},
	'7': {0b111, 0b001, 0b010, 0b010, 0b010},
	'8': {0b111, 0b101, 0b111, 0b101, 0b111},
	'9': {0b111, 0b101, 0b111, 0b001, 0b111},
	'+': {0b000, 0b010, 0b111, 0b010, 0b000},
}

const (
	badgeScale  = 5 // screen pixels per font pixel
	badgePadX   = 6
	badgePadY   = 5
	badgeMargin = 4
	// maxBadgeCount is the largest count the badge shows in full. A larger
	// count shows as "99+".
	maxBadgeCount = 99
)

var (
	badgeBackground = color.NRGBA{R: 0, G: 0, B: 16, A: 255}
	badgeForeground = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
)

// badgeText returns the text the count badge shows.
func badgeText(count int) string {
	if count > maxBadgeCount {
		return strconv.Itoa(maxBadgeCount) + "+"
	}
	return strconv.Itoa(count)
}

// drawBadge draws count in a dark box at the bottom-right of img.
func drawBadge(img *image.NRGBA, count int) {
	text := []rune(badgeText(count))
	charW := 3 * badgeScale
	gap := badgeScale
	textW := len(text)*charW + (len(text)-1)*gap
	textH := 5 * badgeScale
	boxW, boxH := textW+2*badgePadX, textH+2*badgePadY

	b := img.Bounds()
	box := image.Rect(b.Max.X-badgeMargin-boxW, b.Max.Y-badgeMargin-boxH, b.Max.X-badgeMargin, b.Max.Y-badgeMargin)
	draw.Draw(img, box, image.NewUniform(badgeBackground), image.Point{}, draw.Src)

	x0 := box.Min.X + badgePadX
	y0 := box.Min.Y + badgePadY
	for i, r := range text {
		glyph := digitFont[r]
		cx := x0 + i*(charW+gap)
		for row := 0; row < 5; row++ {
			for col := 0; col < 3; col++ {
				if glyph[row]&(1<<(2-col)) == 0 {
					continue
				}
				cell := image.Rect(cx+col*badgeScale, y0+row*badgeScale, cx+(col+1)*badgeScale, y0+(row+1)*badgeScale)
				draw.Draw(img, cell, image.NewUniform(badgeForeground), image.Point{}, draw.Src)
			}
		}
	}
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
// assetsDir, if ref is not empty, and a count badge when count is above
// zero. Transparent pixels show the key color behind them.
func renderGlyph(assetsDir, ref string, count int) ([]byte, error) {
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
		draw.Draw(canvas, canvas.Bounds(), icon, b.Min, draw.Src)
	}
	if count > 0 {
		drawBadge(canvas, count)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, canvas); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
