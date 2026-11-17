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

// The count badges (tasks 0062 and 0091) are up to three discs along the
// bottom edge of a key: red for the count, green for countGreen, and yellow
// for countYellow. Each disc has a white ring and a number. The host draws
// the sprites once and sends them to the board in a Store badge sheet message.
// The board stamps them on a copy of a cached frame. A key that cannot be
// cached takes the same sprites through DrawBadges. The layout below is the
// contract with firmware/display_render.py, and docs/wire-protocol.md ("Store
// badge sheet") describes it.
const (
	badgeDisc    = 34 // diameter of a disc, ring included
	badgeRing    = 2  // width of the white ring
	badgeGap     = 3  // distance between two discs
	badgeMargin  = 5  // distance from the discs to the bottom and right edge of the panel; firmware BADGE_MARGIN says the same
	badgeBigW    = 11 // cell of a digit of a count up to 99
	badgeBigH    = 16
	badgeSmallW  = 8 // cell of a character of "99+"
	badgeSmallH  = 12
	badgeBigPx   = 20 // font size of the big cells, in pixels
	badgeSmallPx = 14

	// BadgeSheetWidth and BadgeSheetHeight are the size of the sheet in pixels:
	// 76,800 bytes.
	BadgeSheetWidth  = transport.BadgeSheetWidth
	BadgeSheetHeight = transport.BadgeSheetHeight
	// MaxBadgeCount is the largest count shown in full. A larger count shows
	// as "99+".
	MaxBadgeCount = 99

	// The sheet has a disc for each fill (a row) and each key color (a column)
	// at (badgeDisc*column, badgeDisc*fill): up to badgeColumns columns fit,
	// and the wire names at most transport.MaxBadgeColors of them. Below them
	// is a set of characters for each fill, at badgeCellY[fill]: "0" to "9" and
	// "+" at (badgeBigW*i, y), then the small "9" and "+".
	badgeColumns = BadgeSheetWidth / badgeDisc

	// A pixel of a disc edge counts as part of the disc when at least badgeEdgeMin
	// of its badgeSamples*badgeSamples samples lie inside the circle.
	badgeSamples = 4
	badgeEdgeMin = 4

	// iconRoundRadius is the radius of the white round behind an icon, in
	// pixels. Every icon under assets/ lies inside it (task 0069).
	iconRoundRadius = 58
)

// Fill is the color of a badge: the row of its discs on the sheet, and its place
// on the key, from the right.
type Fill int

const (
	FillRed Fill = iota
	FillGreen
	FillYellow
	fillCount
)

var (
	badgeFills = [fillCount]color.NRGBA{
		FillRed:    {R: 0xE0, G: 0x20, B: 0x20, A: 0xFF},
		FillGreen:  {R: 0x2E, G: 0xCC, B: 0x40, A: 0xFF},
		FillYellow: {R: 0xFF, G: 0xD7, B: 0x00, A: 0xFF},
	}
	// badgeInk is the color of the digits on each fill: white on red, black on
	// green and yellow.
	badgeInk = [fillCount]color.NRGBA{
		FillRed:    {R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF},
		FillGreen:  {A: 0xFF},
		FillYellow: {A: 0xFF},
	}
	// badgeCellY is the top of the characters of each fill on the sheet. The
	// three discs rows take y 0 to 101.
	badgeCellY = [fillCount]int{FillRed: 102, FillGreen: 118, FillYellow: 134}

	badgeWhite = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}

	// BadgeColors are the key colors the sheet has a column of discs for, in
	// order: the five status colors.
	BadgeColors = []uint16{ColorRunning, ColorDone, ColorError, ColorWarn, ColorNone}
)

// Badges is the three counts of a key. See transport.Badges.
type Badges = transport.Badges

// BadgeSheet is the sprites of the badges: the key color each column of discs
// is blended against, the sheet as an image, and its pixels in the format of a
// custom glyph, which is what the Store badge sheet message carries.
type BadgeSheet struct {
	Colors []uint16
	Image  *image.NRGBA
	Pixels []byte
}

var (
	badgeOnce  sync.Once
	badgeSheet BadgeSheet
	badgeErr   error
)

// BuildBadgeSheet draws the sheet. The result is the same each time, so it is
// built once.
func BuildBadgeSheet() (BadgeSheet, error) {
	badgeOnce.Do(func() { badgeSheet, badgeErr = buildBadgeSheet() })
	return badgeSheet, badgeErr
}

func buildBadgeSheet() (BadgeSheet, error) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		return BadgeSheet{}, err
	}
	big, err := opentype.NewFace(f, &opentype.FaceOptions{Size: badgeBigPx, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return BadgeSheet{}, err
	}
	small, err := opentype.NewFace(f, &opentype.FaceOptions{Size: badgeSmallPx, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return BadgeSheet{}, err
	}
	img := image.NewNRGBA(image.Rect(0, 0, BadgeSheetWidth, BadgeSheetHeight))
	for fill := FillRed; fill < fillCount; fill++ {
		for i, c := range BadgeColors {
			draw.Draw(img, badgeDiscSprite(i, fill), discSprite(c, fill), image.Point{}, draw.Src)
		}
		for _, r := range "0123456789+" {
			draw.Draw(img, badgeSpriteRect(fill, r, false), characterCell(big, r, badgeBigW, badgeBigH, fill), image.Point{}, draw.Src)
		}
		for _, r := range "9+" {
			draw.Draw(img, badgeSpriteRect(fill, r, true), characterCell(small, r, badgeSmallW, badgeSmallH, fill), image.Point{}, draw.Src)
		}
	}
	return BadgeSheet{
		Colors: append([]uint16(nil), BadgeColors...),
		Image:  img,
		Pixels: transport.ImageToRGB565(img),
	}, nil
}

// badgeDiscSprite is where the disc of a fill on the key color in column
// column is on the sheet.
func badgeDiscSprite(column int, fill Fill) image.Rectangle {
	return image.Rect(badgeDisc*column, badgeDisc*int(fill), badgeDisc*(column+1), badgeDisc*(int(fill)+1))
}

// badgeSpriteRect is where the cell of character r of a fill is on the sheet. r
// is a digit or "+"; small cells exist only for "9" and "+".
func badgeSpriteRect(fill Fill, r rune, small bool) image.Rectangle {
	index := int(r - '0')
	if r == '+' {
		index = 10
	}
	y := badgeCellY[fill]
	if !small {
		return image.Rect(badgeBigW*index, y, badgeBigW*(index+1), y+badgeBigH)
	}
	x := badgeBigW * 11
	if r == '+' {
		x += badgeSmallW
	}
	return image.Rect(x, y, x+badgeSmallW, y+badgeSmallH)
}

// discSprite draws the disc of a fill for a key color: the fill inside, a white
// ring, and an edge blended against the key color. A pixel with too little
// disc in it is transparent, so the corners keep what lies under them. The disc
// for ColorNone is blended against white instead: it is the disc of a text icon
// and of an emoji icon, whose badges sit on a white tile (task 0082).
func discSprite(key uint16, fill Fill) *image.NRGBA {
	keyColor := rgb565ToNRGBA(key)
	if key == ColorNone {
		keyColor = badgeWhite
	}
	img := image.NewNRGBA(image.Rect(0, 0, badgeDisc, badgeDisc))
	r := float64(badgeDisc) / 2
	for y := 0; y < badgeDisc; y++ {
		for x := 0; x < badgeDisc; x++ {
			var sum [3]int
			inside := 0
			for sy := 0; sy < badgeSamples; sy++ {
				for sx := 0; sx < badgeSamples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/badgeSamples
					py := float64(y) + (float64(sy)+0.5)/badgeSamples
					d := math.Hypot(px-r, py-r)
					c := keyColor
					switch {
					case d <= r-badgeRing:
						c = badgeFills[fill]
						inside++
					case d <= r:
						c = badgeWhite
						inside++
					}
					sum[0] += int(c.R)
					sum[1] += int(c.G)
					sum[2] += int(c.B)
				}
			}
			if inside < badgeEdgeMin {
				continue
			}
			n := badgeSamples * badgeSamples
			img.SetNRGBA(x, y, color.NRGBA{R: uint8(sum[0] / n), G: uint8(sum[1] / n), B: uint8(sum[2] / n), A: 0xFF})
		}
	}
	return img
}

// characterCell draws a character in the ink of a fill on the fill, centred in
// a w by h cell. The edges are blended against the fill, which is what lies
// under them on the disc.
func characterCell(face font.Face, r rune, w, h int, fill Fill) *image.NRGBA {
	cell := image.NewNRGBA(image.Rect(0, 0, w, h))
	draw.Draw(cell, cell.Bounds(), image.NewUniform(badgeFills[fill]), image.Point{}, draw.Src)
	advance, _ := face.GlyphAdvance(r)
	bounds, _, _ := face.GlyphBounds(r)
	d := font.Drawer{
		Dst:  cell,
		Src:  image.NewUniform(badgeInk[fill]),
		Face: face,
		Dot: fixed.Point26_6{
			X: (fixed.I(w) - advance) / 2,
			Y: fixed.I(h)/2 - (bounds.Min.Y+bounds.Max.Y)/2,
		},
	}
	d.DrawString(string(r))
	return cell
}

// badgeText returns the text of a badge for a count above 0.
func badgeText(count int) string {
	if count > MaxBadgeCount {
		return strconv.Itoa(MaxBadgeCount) + "+"
	}
	return strconv.Itoa(count)
}

// badgeDiscRect is where the disc of a fill lies on the panel: red at the right,
// then green, then yellow, 3 px apart (task 0091).
func badgeDiscRect(fill Fill) image.Rectangle {
	x := transport.CustomGlyphWidth - badgeMargin - badgeDisc - int(fill)*(badgeDisc+badgeGap)
	y := transport.CustomGlyphHeight - badgeMargin - badgeDisc
	return image.Rect(x, y, x+badgeDisc, y+badgeDisc)
}

// badgeCell is one character of a badge: where its cell is on the sheet, and the
// panel pixel it goes to.
type badgeCell struct {
	src image.Rectangle
	dst image.Point
}

// badgeCells returns the cells of the badge of a fill for a count above 0.
// "99+" uses the small cells.
func badgeCells(fill Fill, count int) []badgeCell {
	text := badgeText(count)
	small := count > MaxBadgeCount
	w, h := badgeBigW, badgeBigH
	if small {
		w, h = badgeSmallW, badgeSmallH
	}
	disc := badgeDiscRect(fill)
	x := disc.Min.X + (badgeDisc-len(text)*w)/2
	y := disc.Min.Y + (badgeDisc-h)/2
	cells := make([]badgeCell, 0, len(text))
	for i, r := range text {
		cells = append(cells, badgeCell{src: badgeSpriteRect(fill, r, small), dst: image.Pt(x+i*w, y)})
	}
	return cells
}

// counts returns the three counts as a list in the order of the fills.
func counts(b Badges) [fillCount]int {
	return [fillCount]int{FillRed: int(b.Red), FillGreen: int(b.Green), FillYellow: int(b.Yellow)}
}

// DrawBadges draws the badges along the bottom edge of img, a 128 by 128 image,
// the way the board stamps them: for each count above 0, the disc blended
// against keyColor, then the characters. A count of 0 draws nothing.
func DrawBadges(img *image.NRGBA, keyColor uint16, b Badges) error {
	if !b.Any() {
		return nil
	}
	sheet, err := BuildBadgeSheet()
	if err != nil {
		return err
	}
	column := 0
	for i, c := range BadgeColors {
		if c == keyColor {
			column = i
		}
	}
	for fill, count := range counts(b) {
		if count <= 0 {
			continue
		}
		f := Fill(fill)
		disc := badgeDiscRect(f)
		draw.Draw(img, disc, sheet.Image, badgeDiscSprite(column, f).Min, draw.Over)
		for _, c := range badgeCells(f, count) {
			draw.Draw(img, image.Rectangle{Min: c.dst, Max: c.dst.Add(c.src.Size())}, sheet.Image, c.src.Min, draw.Src)
		}
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
			for sy := 0; sy < badgeSamples; sy++ {
				for sx := 0; sx < badgeSamples; sx++ {
					px := float64(x) + (float64(sx)+0.5)/badgeSamples
					py := float64(y) + (float64(sy)+0.5)/badgeSamples
					if math.Hypot(px-cx, py-cy) <= iconRoundRadius {
						inside++
					}
				}
			}
			if inside < badgeEdgeMin {
				continue
			}
			n := badgeSamples * badgeSamples
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
