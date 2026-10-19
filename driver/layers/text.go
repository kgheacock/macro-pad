package layers

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/font/sfnt"
	"golang.org/x/image/math/fixed"
)

// A text icon (task 0069) is words drawn in black and gray on a plain white
// tile, with no round. The tile is opaque, so the key color never shows around
// it, and the edges of the letters are blended against the white.
const (
	// MaxIconTextRunes and MaxIconTextLines bound the text of an icon.
	MaxIconTextRunes = 32
	MaxIconTextLines = 2

	// The text goes in a box that ends above the count pip, which starts at y
	// 79 (pipDiscRect), so a count never covers a letter.
	textBoxX0 = 8
	textBoxX1 = 120
	textBoxY0 = 10
	textBoxY1 = 76

	// textLineGap is the space between the two lines, in pixels.
	textLineGap = 2

	textEllipsis = '…'
)

var (
	// textName is the color of the name, the last line, and of a text of one
	// line. textParent is the color of the first of two lines.
	textName   = color.NRGBA{R: 0x00, G: 0x00, B: 0x00, A: 0xFF}
	textParent = color.NRGBA{R: 0x6B, G: 0x6B, B: 0x6B, A: 0xFF}

	// A line takes the largest size here that fits the box. The sizes are
	// pixels, as pipBigPx is.
	textSizesOne = []int{44, 36, 28, 22, 18}
	textSizesTwo = []int{28, 22, 18, 14}
)

// ValidateIconText accepts an empty text, or a text of at most MaxIconTextRunes
// runes on at most MaxIconTextLines lines that has something to show. A control
// character other than the line break is rejected, as is invalid UTF-8.
func ValidateIconText(text string) error {
	if text == "" {
		return nil
	}
	if !utf8.ValidString(text) {
		return errors.New("iconText is not valid UTF-8")
	}
	if n := utf8.RuneCountInString(text); n > MaxIconTextRunes {
		return fmt.Errorf("iconText has %d runes, at most %d", n, MaxIconTextRunes)
	}
	if n := strings.Count(text, "\n") + 1; n > MaxIconTextLines {
		return fmt.Errorf("iconText has %d lines, at most %d", n, MaxIconTextLines)
	}
	for _, r := range text {
		if r != '\n' && unicode.IsControl(r) {
			return fmt.Errorf("iconText has the control character %U", r)
		}
	}
	if strings.TrimSpace(text) == "" {
		return errors.New("iconText has nothing to show")
	}
	return nil
}

// textLine is one line of a text icon on its way to the tile.
type textLine struct {
	text  string
	color color.NRGBA
	sizes []int
	size  int // index into sizes
	face  font.Face
	w     int // width in pixels at the chosen size
}

// drawIconText fills img with white and draws text on it. The text is already
// valid (ValidateIconText).
func drawIconText(img *image.NRGBA, text string) error {
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return err
	}
	var lines []*textLine
	for _, raw := range strings.Split(text, "\n") {
		if s := strings.TrimSpace(raw); s != "" {
			lines = append(lines, &textLine{text: s, color: textName})
		}
	}
	if len(lines) == 0 {
		return errors.New("iconText has nothing to show")
	}
	sizes := textSizesOne
	if len(lines) > 1 {
		sizes = textSizesTwo
		lines[0].color = textParent
	}
	for _, l := range lines {
		l.sizes = sizes
	}

	faces := make(map[int]font.Face)
	faceOf := func(px int) (font.Face, error) {
		if face, ok := faces[px]; ok {
			return face, nil
		}
		face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: float64(px), DPI: 72, Hinting: font.HintingFull})
		if err != nil {
			return nil, err
		}
		faces[px] = face
		return face, nil
	}
	defer func() {
		for _, face := range faces {
			face.Close()
		}
	}()

	// Each line takes the largest size that fits the box, then the larger of
	// two lines steps down until both fit the height of the box.
	boxW := textBoxX1 - textBoxX0
	boxH := textBoxY1 - textBoxY0
	for _, l := range lines {
		l.text = replaceMissing(f, l.text)
		for l.size = 0; l.size < len(l.sizes); l.size++ {
			face, err := faceOf(l.sizes[l.size])
			if err != nil {
				return err
			}
			l.face = face
			if l.w = textWidth(face, l.text); l.w <= boxW {
				break
			}
		}
		if l.size == len(l.sizes) {
			l.size = len(l.sizes) - 1
		}
	}
	height := func() int {
		h := textLineGap * (len(lines) - 1)
		for _, l := range lines {
			h += lineHeight(l.face)
		}
		return h
	}
	for height() > boxH {
		big := lines[0]
		for _, l := range lines {
			if l.size < len(l.sizes)-1 && (big.size >= len(big.sizes)-1 || l.sizes[l.size] > big.sizes[big.size]) {
				big = l
			}
		}
		if big.size >= len(big.sizes)-1 {
			break
		}
		big.size++
		face, err := faceOf(big.sizes[big.size])
		if err != nil {
			return err
		}
		big.face = face
	}
	for _, l := range lines {
		l.text = fitWidth(l.face, l.text, boxW)
		l.w = textWidth(l.face, l.text)
	}

	y := textBoxY0 + (boxH-height())/2
	for _, l := range lines {
		m := l.face.Metrics()
		d := font.Drawer{
			Dst:  img,
			Src:  image.NewUniform(l.color),
			Face: l.face,
			Dot: fixed.Point26_6{
				X: fixed.I(textBoxX0 + (boxW-l.w)/2),
				Y: fixed.I(y) + m.Ascent,
			},
		}
		d.DrawString(l.text)
		y += lineHeight(l.face) + textLineGap
	}
	return nil
}

// lineHeight is the height of one line of text in face, in pixels.
func lineHeight(face font.Face) int {
	m := face.Metrics()
	return (m.Ascent + m.Descent).Ceil()
}

func textWidth(face font.Face, s string) int {
	return font.MeasureString(face, s).Ceil()
}

// replaceMissing swaps each rune that the font has no glyph for with "?".
// Emoji, CJK, and combining marks that the font lacks therefore show "?".
func replaceMissing(f *opentype.Font, s string) string {
	var buf sfnt.Buffer
	var b strings.Builder
	for _, r := range s {
		if idx, err := f.GlyphIndex(&buf, r); err != nil || idx == 0 {
			r = '?'
		}
		b.WriteRune(r)
	}
	return b.String()
}

// fitWidth cuts s so that it is at most w pixels wide in face, and ends the
// cut text with "…".
func fitWidth(face font.Face, s string, w int) string {
	if textWidth(face, s) <= w {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		runes = runes[:len(runes)-1]
		cut := strings.TrimRightFunc(string(runes), unicode.IsSpace) + string(textEllipsis)
		if textWidth(face, cut) <= w {
			return cut
		}
	}
	return string(textEllipsis)
}
