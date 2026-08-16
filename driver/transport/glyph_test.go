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

func TestDecodePNGToRGB565_CustomGlyph(t *testing.T) {
	// Pure red at 8-bit depth: R=0xFF, G=0x00, B=0x00 packs to RGB565
	// 0xF800.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.RGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF})

	pixels, err := DecodePNGToRGB565(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}
	if len(pixels) != CustomGlyphPixelsSize {
		t.Fatalf("len(pixels) = %d, want %d", len(pixels), CustomGlyphPixelsSize)
	}

	// 0xF800, little-endian: low byte 0x00, high byte 0xF8.
	wantLo := byte(0x00)
	wantHi := byte(0xF8)
	if pixels[0] != wantLo || pixels[1] != wantHi {
		t.Fatalf("first pixel = % x, want %02x %02x (RGB565 red)", pixels[:2], wantLo, wantHi)
	}
	last := len(pixels) - 2
	if pixels[last] != wantLo || pixels[last+1] != wantHi {
		t.Fatalf("last pixel = % x, want %02x %02x (RGB565 red)", pixels[last:], wantLo, wantHi)
	}
}

func TestDecodePNGToRGB565_CustomGlyphRejectsWrongSize(t *testing.T) {
	pngBytes := solidPNG(t, 64, 64, color.RGBA{R: 0xFF, A: 0xFF})

	if _, err := DecodePNGToRGB565(pngBytes); !errors.Is(err, ErrInvalidGlyphSize) {
		t.Fatalf("DecodePNGToRGB565 with a 64x64 image = %v, want errors.Is(err, ErrInvalidGlyphSize)", err)
	}
}

func TestDecodePNGToRGB565_CustomGlyphRejectsGarbage(t *testing.T) {
	if _, err := DecodePNGToRGB565([]byte("not a png")); err == nil {
		t.Fatal("DecodePNGToRGB565 with non-PNG bytes returned no error")
	}
}
