package transport

import (
	"bytes"
	"fmt"
	"image/png"
)

// DecodePNGToRGBA4444 decodes a PNG file's bytes and returns its pixels
// as a CustomGlyphPixelsSize-byte raw RGBA4444 buffer, row-major,
// little-endian per pixel — the format SendCustomGlyph and "Set custom
// glyph" in docs/wire-protocol.md expect. Each 16-bit pixel packs a
// 4-bit alpha nibble (bits 15-12), then 4 bits each of red, green, and
// blue.
//
// This is Approach B from task 0041's spec: the driver, not the
// firmware, decides how a PNG's alpha channel maps onto the wire's
// one-bit transparency (see the package's Non-goals — no blended
// transparency), so firmware never needs an image decoder of its own.
// An alpha value at or above half opacity encodes as fully opaque
// (nibble 0xF); anything below encodes as fully transparent (nibble
// 0x0) — see task 0041's DoD-4. Returns ErrInvalidGlyphSize when the
// decoded image is not exactly CustomGlyphWidth × CustomGlyphHeight,
// checked before any pixel conversion runs, so a wrong-sized image never
// reaches the wire.
func DecodePNGToRGBA4444(pngBytes []byte) ([]byte, error) {
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
			// image's own bit depth; keep the top 4 bits of each to pack
			// RGBA4444.
			var alpha uint16
			if a >= 0x8000 {
				alpha = 0xF
			}
			rgba4444 := alpha<<12 | uint16(r>>12)<<8 | uint16(g>>12)<<4 | uint16(b>>12)
			pixels[i] = byte(rgba4444)
			pixels[i+1] = byte(rgba4444 >> 8)
			i += 2
		}
	}
	return pixels, nil
}
