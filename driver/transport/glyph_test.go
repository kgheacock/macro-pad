package transport

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// solidPNG builds a width x height PNG filled with c, for tests that
// don't care about the picture's content, only its dimensions and color.
func solidPNG(t *testing.T, width, height int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

func TestDecodePNGToRGBA4444_CustomGlyph(t *testing.T) {
	// Pure opaque red at 8-bit depth: R=0xFF, G=0x00, B=0x00, A=0xFF
	// packs to RGBA4444 0xFF00: alpha nibble F, red nibble F, green and
	// blue nibbles 0.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF})

	pixels, err := DecodePNGToRGBA4444(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGBA4444: %v", err)
	}
	if len(pixels) != CustomGlyphPixelsSize {
		t.Fatalf("len(pixels) = %d, want %d", len(pixels), CustomGlyphPixelsSize)
	}

	// 0xFF00, little-endian: low byte 0x00, high byte 0xFF.
	wantLo := byte(0x00)
	wantHi := byte(0xFF)
	if pixels[0] != wantLo || pixels[1] != wantHi {
		t.Fatalf("first pixel = % x, want %02x %02x (RGBA4444 opaque red)", pixels[:2], wantLo, wantHi)
	}
	last := len(pixels) - 2
	if pixels[last] != wantLo || pixels[last+1] != wantHi {
		t.Fatalf("last pixel = % x, want %02x %02x (RGBA4444 opaque red)", pixels[last:], wantLo, wantHi)
	}
}

func TestDecodePNGToRGBA4444_TransparentPixelEncodesZeroAlphaNibble(t *testing.T) {
	// A fully transparent pixel — this task's Non-goals rule out blended
	// transparency, so any alpha below half opacity must collapse to the
	// zero alpha nibble, regardless of the color underneath it.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x00})

	pixels, err := DecodePNGToRGBA4444(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGBA4444: %v", err)
	}

	// The high byte's high nibble is the alpha nibble — see
	// DecodePNGToRGBA4444's doc comment for the bit layout.
	alphaNibble := pixels[1] >> 4
	if alphaNibble != 0 {
		t.Fatalf("alpha nibble = %x, want 0 for a fully transparent pixel", alphaNibble)
	}
}

func TestDecodePNGToRGBA4444_CustomGlyphRejectsWrongSize(t *testing.T) {
	pngBytes := solidPNG(t, 64, 64, color.RGBA{R: 0xFF, A: 0xFF})

	if _, err := DecodePNGToRGBA4444(pngBytes); !errors.Is(err, ErrInvalidGlyphSize) {
		t.Fatalf("DecodePNGToRGBA4444 with a 64x64 image = %v, want errors.Is(err, ErrInvalidGlyphSize)", err)
	}
}

func TestDecodePNGToRGBA4444_CustomGlyphRejectsGarbage(t *testing.T) {
	if _, err := DecodePNGToRGBA4444([]byte("not a png")); err == nil {
		t.Fatal("DecodePNGToRGBA4444 with non-PNG bytes returned no error")
	}
}
