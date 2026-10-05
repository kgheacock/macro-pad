package transport

import (
	"bytes"
	"fmt"
	"image/png"
)

// TransparentPixel is the RGB565 value that marks a transparent pixel in a
// custom glyph buffer. BlackPixel is what an opaque pixel whose own RGB565
// value is 0x0000 encodes as instead, so real black stays distinguishable
// from transparent. 0x0001 is a barely-blue black; the panel cannot show
// the difference.
const (
	TransparentPixel uint16 = 0x0000
	BlackPixel       uint16 = 0x0001
)

// DecodePNGToRGB565 decodes a PNG file's bytes and returns its pixels as
// a CustomGlyphPixelsSize-byte raw RGB565 buffer, row-major, big-endian
// per pixel — the format SendCustomGlyph and "Set custom glyph" in
// docs/wire-protocol.md expect. Each 16-bit pixel packs 5 bits of red
// (bits 15-11), 6 of green (10-5), and 5 of blue (4-0). Big-endian is the
// byte order the ST7735R panel reads, so firmware copies the buffer to the
// panel with no byte swap and converts no pixel itself (task 0043).
//
// Transparency is one bit wide, as in task 0041's Approach B: a pixel at
// or above half opacity is opaque, and anything below is TransparentPixel,
// whatever color lies under it. An opaque pixel that would encode as
// TransparentPixel encodes as BlackPixel instead. Returns
// ErrInvalidGlyphSize when the decoded image is not exactly
// CustomGlyphWidth × CustomGlyphHeight, checked before any pixel
// conversion runs, so a wrong-sized image never reaches the wire.
func DecodePNGToRGB565(pngBytes []byte) ([]byte, error) {
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		return nil, fmt.Errorf("transport: decode png: %w", err)
	}

	bounds := img.Bounds()
	if bounds.Dx() != CustomGlyphWidth || bounds.Dy() != CustomGlyphHeight {
		return nil, fmt.Errorf("%w: got %dx%d, want %dx%d", ErrInvalidGlyphSize, bounds.Dx(), bounds.Dy(), CustomGlyphWidth, CustomGlyphHeight)
	}

	pixels := make([]byte, CustomGlyphPixelsSize)
	i := 0
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			// r, g, b, a are 16-bit-scaled regardless of the source
			// image's own bit depth, and premultiplied by alpha; keep the
			// top 5, 6, and 5 bits of red, green, and blue.
			pixel := TransparentPixel
			if a >= 0x8000 {
				pixel = uint16(r>>11)<<11 | uint16(g>>10)<<5 | uint16(b>>11)
				if pixel == TransparentPixel {
					pixel = BlackPixel
				}
			}
			pixels[i] = byte(pixel >> 8)
			pixels[i+1] = byte(pixel)
			i += 2
		}
	}
	return pixels, nil
}
