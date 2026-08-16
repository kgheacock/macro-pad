package transport

import (
	"bytes"
	"fmt"
	"image/png"
)

// DecodePNGToRGB565 decodes a PNG file's bytes and returns its pixels as
// a CustomGlyphPixelsSize-byte raw RGB565 buffer, row-major,
// little-endian per pixel — the format SendCustomGlyph and "Set custom
// glyph" in docs/wire-protocol.md expect.
//
// This is Approach A from task 0030's spec: the driver, not the
// firmware, decides how a PNG's colors map to RGB565, so firmware never
// needs an image decoder of its own. Returns ErrInvalidGlyphSize when
// the decoded image is not exactly CustomGlyphWidth × CustomGlyphHeight,
// checked before any pixel conversion runs, so a wrong-sized image never
// reaches the wire.
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
			r, g, b, _ := img.At(x, y).RGBA()
			// r, g, b are 16-bit-scaled regardless of the source image's
			// own bit depth; keep the top 5, 6, and 5 bits of each to
			// pack RGB565.
			rgb565 := uint16(r>>11)<<11 | uint16(g>>10)<<5 | uint16(b>>11)
			pixels[i] = byte(rgb565)
			pixels[i+1] = byte(rgb565 >> 8)
			i += 2
		}
	}
	return pixels, nil
}
