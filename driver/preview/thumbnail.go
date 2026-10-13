package preview

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// Size is a key's screen edge in pixels.
const Size = transport.CustomGlyphWidth

// Thumbnail fits src inside a Size x Size black square, keeping its
// aspect ratio and centering it. Each output pixel averages the source
// pixels it covers, so text and edges thin out instead of aliasing.
func Thumbnail(src image.Image) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, Size, Size))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)

	b := src.Bounds()
	sw, sh := b.Dx(), b.Dy()
	if sw <= 0 || sh <= 0 {
		return dst
	}
	w, h := Size, Size
	if sw >= sh {
		h = max(1, sh*Size/sw)
	} else {
		w = max(1, sw*Size/sh)
	}
	ox, oy := (Size-w)/2, (Size-h)/2

	for y := 0; y < h; y++ {
		y0 := b.Min.Y + y*sh/h
		y1 := max(y0+1, b.Min.Y+(y+1)*sh/h)
		for x := 0; x < w; x++ {
			x0 := b.Min.X + x*sw/w
			x1 := max(x0+1, b.Min.X+(x+1)*sw/w)
			var r, g, bl, n uint64
			for sy := y0; sy < y1; sy++ {
				for sx := x0; sx < x1; sx++ {
					cr, cg, cb, _ := src.At(sx, sy).RGBA()
					r += uint64(cr)
					g += uint64(cg)
					bl += uint64(cb)
					n++
				}
			}
			dst.SetRGBA(ox+x, oy+y, color.RGBA{
				R: uint8(r / n >> 8),
				G: uint8(g / n >> 8),
				B: uint8(bl / n >> 8),
				A: 0xFF,
			})
		}
	}
	return dst
}

// staleBar is the strip along a stale snapshot's bottom edge.
var staleBar = color.RGBA{R: 0xFF, G: 0xA0, B: 0x00, A: 0xFF}

// Stale returns a copy of img dimmed to half brightness with an amber
// strip along the bottom, marking a snapshot of a window that cannot be
// captured right now.
func Stale(img *image.RGBA) *image.RGBA {
	out := image.NewRGBA(img.Bounds())
	for i := 0; i < len(img.Pix); i += 4 {
		out.Pix[i] = img.Pix[i] / 2
		out.Pix[i+1] = img.Pix[i+1] / 2
		out.Pix[i+2] = img.Pix[i+2] / 2
		out.Pix[i+3] = 0xFF
	}
	b := out.Bounds()
	draw.Draw(out, image.Rect(b.Min.X, b.Max.Y-4, b.Max.X, b.Max.Y), image.NewUniform(staleBar), image.Point{}, draw.Src)
	return out
}

// EncodePNG encodes img for setCustomGlyph.
func EncodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// changedFraction returns the share of pixels whose RGB565 value differs
// between a and b, the only difference the key's screen can show. a and b
// must share bounds.
func changedFraction(a, b *image.RGBA) float64 {
	n, diff := 0, 0
	for i := 0; i+3 < len(a.Pix) && i+3 < len(b.Pix); i += 4 {
		n++
		if rgb565(a.Pix[i:i+3]) != rgb565(b.Pix[i:i+3]) {
			diff++
		}
	}
	if n == 0 {
		return 1
	}
	return float64(diff) / float64(n)
}

func rgb565(p []byte) uint16 {
	return uint16(p[0]>>3)<<11 | uint16(p[1]>>2)<<5 | uint16(p[2]>>3)
}
