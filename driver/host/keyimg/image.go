package keyimg

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"strings"

	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

// Size is the width and height, in pixels, of every key's panel.
const Size = 128

// Thumbnail scales src to fit inside Size x Size, keeping its aspect
// ratio, and centers it on black.
func Thumbnail(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)

	b := src.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return dst
	}
	w, h := Size, Size
	if b.Dx() >= b.Dy() {
		h = max(1, Size*b.Dy()/b.Dx())
	} else {
		w = max(1, Size*b.Dx()/b.Dy())
	}
	x0, y0 := (Size-w)/2, (Size-h)/2
	draw.CatmullRom.Scale(dst, image.Rect(x0, y0, x0+w, y0+h), src, b, draw.Over, nil)
	return dst
}

// staleStrip is the height of the amber strip Stale draws along the
// bottom edge.
const staleStrip = 12

// Stale returns a copy of img, scaled to Size x Size, at half brightness
// with an amber strip along the bottom: the look task 0054 gives a key
// whose window is hidden and whose picture is the last one taken.
func Stale(img image.Image) *image.RGBA {
	src := img
	if img.Bounds().Dx() != Size || img.Bounds().Dy() != Size {
		src = Thumbnail(img)
	}
	dst := image.NewRGBA(image.Rect(0, 0, Size, Size))
	sb := src.Bounds()
	for y := 0; y < Size; y++ {
		for x := 0; x < Size; x++ {
			r, g, b, _ := src.At(sb.Min.X+x, sb.Min.Y+y).RGBA()
			dst.SetRGBA(x, y, color.RGBA{uint8(r >> 9), uint8(g >> 9), uint8(b >> 9), 0xFF})
		}
	}
	amber := image.NewUniform(color.RGBA{0xFF, 0xB0, 0x00, 0xFF})
	draw.Draw(dst, image.Rect(0, Size-staleStrip, Size, Size), amber, image.Point{}, draw.Src)
	return dst
}

// maxTextScale caps how far Text enlarges its font, so a single
// character does not fill the whole key.
const maxTextScale = 4

// Text draws s, one line per "\n", centered on a Size x Size image with
// background bg. It uses a built-in 7x13 bitmap font, enlarged by the
// largest whole factor (up to 4) that still fits every line.
func Text(s string, fg, bg Color) image.Image {
	dst := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(rgba(bg)), image.Point{}, draw.Src)

	lines := strings.Split(s, "\n")
	face := basicfont.Face7x13
	lineH := face.Metrics().Height.Ceil()
	textW := 0
	for _, l := range lines {
		textW = max(textW, font.MeasureString(face, l).Ceil())
	}
	textH := lineH * len(lines)
	if textW == 0 {
		return dst
	}

	small := image.NewRGBA(image.Rect(0, 0, textW, textH))
	d := font.Drawer{Dst: small, Src: image.NewUniform(rgba(fg)), Face: face}
	for i, l := range lines {
		w := font.MeasureString(face, l).Ceil()
		d.Dot = fixed.P((textW-w)/2, i*lineH+face.Metrics().Ascent.Ceil())
		d.DrawString(l)
	}

	scale := min(Size/textW, Size/textH, maxTextScale)
	if scale < 1 {
		// Too wide to fit even at 1x: shrink it to fit instead.
		fit := Thumbnail(small)
		draw.Draw(dst, dst.Bounds(), fit, image.Point{}, draw.Over)
		return dst
	}
	w, h := textW*scale, textH*scale
	x0, y0 := (Size-w)/2, (Size-h)/2
	draw.NearestNeighbor.Scale(dst, image.Rect(x0, y0, x0+w, y0+h), small, small.Bounds(), draw.Over, nil)
	return dst
}

// PNG encodes img as PNG file bytes, the form a setCustomGlyph message
// carries. Call Thumbnail first for an image that is not Size x Size.
func PNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func rgba(c Color) color.RGBA {
	return color.RGBA{c.R, c.G, c.B, 0xFF}
}
