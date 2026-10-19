package layers

import (
	"image"
	"image/color"
	"image/draw"
	"math"
	"strconv"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// The count pip (task 0062) is a red disc with a white ring and a white
// number, in the bottom-right corner of a key. The host draws its sprites once
// and sends them to the board in a Store pip sheet message. The board stamps
// them on a copy of a cached frame. A key that cannot be cached takes the same
// sprites through DrawPip. The layout below is the contract with
// firmware/display_render.py, and docs/wire-protocol.md ("Store pip sheet")
// describes it.
const (
	pipDisc    = 44 // diameter of the disc, ring included
	pipRing    = 2  // width of the white ring
	pipMargin  = 5  // distance from the disc sprite to the corner of the panel; firmware PIP_MARGIN says the same
	pipBigW    = 14 // cell of a digit of a count up to 99
	pipBigH    = 20
	pipSmallW  = 10 // cell of a character of "99+"
	pipSmallH  = 14
	pipBigPx   = 22 // font size of the big cells, in pixels
	pipSmallPx = 16

	// PipSheetWidth and PipSheetHeight are the size of the sheet in pixels. It
	// is 16,384 pixels, the size of a custom glyph.
	PipSheetWidth  = 256
	PipSheetHeight = 64
	// MaxPipCount is the largest count shown in full. A larger count shows
	// as "99+".
	MaxPipCount = 99

	// The sheet has a disc for each key color at (pipDisc*i, 0), the big
	// characters "0" to "9" and "+" at (pipBigW*i, pipCellY), and the small
	// "9" and "+" after them.
	pipCellY = pipDisc

	// A pixel of a disc edge counts as part of the disc when at least pipEdgeMin
	// of its pipSamples*pipSamples samples lie inside the circle.
	pipSamples = 4
	pipEdgeMin = 4

	// iconRoundRadius is the radius of the white round behind an icon, in
	// pixels. Every icon under assets/ lies inside it (task 0069).
	iconRoundRadius = 58
)

var (
	pipRed   = color.NRGBA{R: 0xE0, G: 0x20, B: 0x20, A: 0xFF}
	pipWhite = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// PipColors are the key colors the sheet has a disc for, in order: the
	// five status colors.
	PipColors = []uint16{ColorRunning, ColorDone, ColorError, ColorWarn, ColorNone}
)

// PipSheet is the sprites of the pip: the key color each disc is blended
// against, the sheet as an image, and its pixels in the format of a custom
// glyph, which is what the Store pip sheet message carries.
type PipSheet struct {
	Colors []uint16
	Image  *image.NRGBA
	Pixels []byte
}

var (
	pipOnce  sync.Once
	pipSheet PipSheet
	pipErr   error
)

// BuildPipSheet draws the sheet. The result is the same each time, so it is
// built once.
func BuildPipSheet() (PipSheet, error) {
	pipOnce.Do(func() { pipSheet, pipErr = buildPipSheet() })
	return pipSheet, pipErr
}

func buildPipSheet() (PipSheet, error) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return PipSheet{}, err
	}
	big, err := opentype.NewFace(f, &opentype.FaceOptions{Size: pipBigPx, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return PipSheet{}, err
	}
	small, err := opentype.NewFace(f, &opentype.FaceOptions{Size: pipSmallPx, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return PipSheet{}, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, PipSheetWidth, PipSheetHeight))
	for i, c := range PipColors {
		draw.Draw(img, image.Rect(pipDisc*i, 0, pipDisc*(i+1), pipDisc), discSprite(c), image.Point{}, draw.Src)
	}
	for _, r := range "0123456789+" {
		src := pipSpriteRect(r, false)
		draw.Draw(img, src, characterCell(big, r, pipBigW, pipBigH), image.Point{}, draw.Src)
	}
	for _, r := range "9+" {
		src := pipSpriteRect(r, true)
		draw.Draw(img, src, characterCell(small, r, pipSmallW, pipSmallH), image.Point{}, draw.Src)
	}
	return PipSheet{
		Colors: append([]uint16(nil), PipColors...),
		Image:  img,
		Pixels: transport.ImageToRGB565(img),
	}, nil
}

// pipSpriteRect is where the cell of character r is on the sheet. r is a digit
// or "+"; small cells exist only for "9" and "+".
func pipSpriteRect(r rune, small bool) image.Rectangle {
	index := int(r - '0')
	if r == '+' {
		index = 10
	}
	if !small {
		return image.Rect(pipBigW*index, pipCellY, pipBigW*(index+1), pipCellY+pipBigH)
	}
	x := pipBigW * 11
	if r == '+' {
		x += pipSmallW
	}
	return image.Rect(x, pipCellY, x+pipSmallW, pipCellY+pipSmallH)
}

// discSprite draws the disc for a key color: red inside, a white ring, and an
// edge blended against the key color. A pixel with too little disc in it is
// transparent, so the corners keep what lies under them.
func discSprite(key uint16) *image.NRGBA {
	keyColor := rgb565ToNRGBA(key)
	img := image.NewNRGBA(image.Rect(0, 0, pipDisc, pipDisc))
	r := float64(pipDisc) / 2
	for y := 0; y < pipDisc; y++ {
		for x := 0; x < pipDisc; x++ {
			var sum [3]int
			inside := 0
			for sy := 0; sy < pipSamples; sy++ {
				for sx := 0; sx < pipSamples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/pipSamples
					py := float64(y) + (float64(sy)+0.5)/pipSamples
					d := math.Hypot(px-r, py-r)
					c := keyColor
					switch {
					case d <= r-pipRing:
						c = pipRed
						inside++
					case d <= r:
						c = pipWhite
						inside++
					}
					sum[0] += int(c.R)
					sum[1] += int(c.G)
					sum[2] += int(c.B)
				}
			}
			if inside < pipEdgeMin {
				continue
			}
			n := pipSamples * pipSamples
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(sum[0] / n), G: uint8(sum[1] / n), B: uint8(sum[2] / n), A: 0xFF})
		}
	}
	return img
}

// characterCell draws a white character on red, centred in a w by h cell. The
// edges are blended against the red, which is what lies under them on the disc.
func characterCell(face font.Face, r rune, w, h int) *image.NRGBA {
	cell := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(cell, cell.Bounds(), image.NewUniform(pipRed), image.Point{}, draw.Src)
	advance, _ := face.GlyphAdvance(r)
	bounds, _, _ := face.GlyphBounds(r)
	d := font.Drawer{
		Dst:  cell,
		Src:  image.NewUniform(pipWhite),
		Face: face,
		Dot: fixed.Point26_6{
			X: (fixed.I(w) - advance) / 2,
			Y: fixed.I(h)/2 - (bounds.Min.Y+bounds.Max.Y)/2,
		},
	}
	d.DrawString(string(r))
	return cell
}

// pipText returns the text of the pip for a count above 0.
func pipText(count int) string {
	if count > MaxPipCount {
		return strconv.Itoa(MaxPipCount) + "+"
	}
	return strconv.Itoa(count)
}

// pipDiscRect is where the disc lies on the panel.
func pipDiscRect() image.Rectangle {
	x := transport.CustomGlyphWidth - pipMargin - pipDisc
	y := transport.CustomGlyphHeight - pipMargin - pipDisc
	return image.Rect(x, y, x+pipDisc, y+pipDisc)
}

// pipCell is one character of the pip: where its cell is on the sheet, and the
// panel pixel it goes to.
type pipCell struct {
	src image.Rectangle
	dst image.Point
}

// pipCells returns the cells of the pip for a count above 0. "99+" uses the
// small cells.
func pipCells(count int) []pipCell {
	text := pipText(count)
	small := count > MaxPipCount
	w, h := pipBigW, pipBigH
	if small {
		w, h = pipSmallW, pipSmallH
	}
	disc := pipDiscRect()
	x := disc.Min.X + (pipDisc-len(text)*w)/2
	y := disc.Min.Y + (pipDisc-h)/2
	cells := make([]pipCell, 0, len(text))
	for i, r := range text {
		cells = append(cells, pipCell{src: pipSpriteRect(r, small), dst: image.Pt(x+i*w, y)})
	}
	return cells
}

// DrawPip draws the count pip in the bottom-right corner of img, a 128 by 128
// image, the way the board stamps it: the disc blended against keyColor, then
// the characters. A count of 0 draws nothing.
func DrawPip(img *image.NRGBA, keyColor uint16, count int) error {
	if count <= 0 {
		return nil
	}
	sheet, err := BuildPipSheet()
	if err != nil {
		return err
	}
	draw.Draw(img, pipDiscRect(), discSprite(keyColor), image.Point{}, draw.Over)
	for _, c := range pipCells(count) {
		draw.Draw(img, image.Rectangle{Min: c.dst, Max: c.dst.Add(c.src.Size())}, sheet.Image, c.src.Min, draw.Src)
	}
	return nil
}

// drawIconRound draws the white round behind an icon: a disc of
// iconRoundRadius at the centre of img, with an edge blended against keyColor,
// so that every pixel of it is opaque (task 0041). The corners stay
// transparent.
func drawIconRound(img *image.NRGBA, keyColor uint16) {
	keyNRGBA := rgb565ToNRGBA(keyColor)
	cx := float64(img.Bounds().Dx()) / 2
	cy := float64(img.Bounds().Dy()) / 2
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			inside := 0
			for sy := 0; sy < pipSamples; sy++ {
				for sx := 0; sx < pipSamples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/pipSamples
					py := float64(y) + (float64(sy)+0.5)/pipSamples
					if math.Hypot(px-cx, py-cy) <= iconRoundRadius {
						inside++
					}
				}
			}
			if inside < pipEdgeMin {
				continue
			}
			n := pipSamples * pipSamples
			mix := func(white, key uint8) uint8 {
				return uint8((int(white)*inside + int(key)*(n-inside)) / n)
			}
			img.SetNRGBA(x, y, color.NRGBA{R: mix(0xFF, keyNRGBA.R), G: mix(0xFF, keyNRGBA.G), B: mix(0xFF, keyNRGBA.B), A: 0xFF})
		}
	}
}

// rgb565ToNRGBA expands an RGB565 color to 8 bits for each channel, so that
// converting it back gives the same RGB565 value.
func rgb565ToNRGBA(c uint16) color.NRGBA {
	r, g, b := uint8(c>>11), uint8(c>>5)&0x3F, uint8(c)&0x1F
	return color.NRGBA{R: r<<3 | r>>2, G: g<<2 | g>>4, B: b<<3 | b>>2, A: 0xFF}
}
