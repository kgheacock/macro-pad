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
	// Pure opaque red: R=0xFF, G=0x00, B=0x00 packs to RGB565 0xF800, and
	// the buffer is big-endian, so the high byte 0xF8 comes first.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0xFF, G: 0x00, B: 0x00, A: 0xFF})

	pixels, err := DecodePNGToRGB565(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}
	if len(pixels) != CustomGlyphPixelsSize {
		t.Fatalf("len(pixels) = %d, want %d", len(pixels), CustomGlyphPixelsSize)
	}

	want := []byte{0xF8, 0x00}
	if !bytes.Equal(pixels[:2], want) {
		t.Fatalf("first pixel = % x, want % x (big-endian RGB565 opaque red)", pixels[:2], want)
	}
	last := len(pixels) - 2
	if !bytes.Equal(pixels[last:], want) {
		t.Fatalf("last pixel = % x, want % x (big-endian RGB565 opaque red)", pixels[last:], want)
	}
}

func TestDecodePNGToRGB565_ChannelPacking(t *testing.T) {
	// One distinct value per channel, so a swapped or mis-shifted channel
	// shows up: R=0xFF -> 0x1F, G=0x80 -> 0x20, B=0x08 -> 0x01.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0xFF, G: 0x80, B: 0x08, A: 0xFF})

	pixels, err := DecodePNGToRGB565(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}

	got := uint16(pixels[0])<<8 | uint16(pixels[1])
	want := uint16(0x1F)<<11 | uint16(0x20)<<5 | uint16(0x01)
	if got != want {
		t.Fatalf("pixel = %#04x, want %#04x", got, want)
	}
}

func TestDecodePNGToRGB565_TransparentPixelEncodesZero(t *testing.T) {
	// A fully transparent pixel — task 0041's Non-goals rule out blended
	// transparency, so any alpha below half opacity must collapse to
	// TransparentPixel, regardless of the color underneath it.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0x00})

	pixels, err := DecodePNGToRGB565(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}

	for i := 0; i < len(pixels); i += 2 {
		if pixels[i] != 0x00 || pixels[i+1] != 0x00 {
			t.Fatalf("pixel %d = % x, want 00 00 (transparent)", i/2, pixels[i:i+2])
		}
	}
}

func TestDecodePNGToRGB565_OpaqueBlackEncodesAsBlackPixel(t *testing.T) {
	// Real black must not collide with the transparent marker 0x0000.
	pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0, G: 0, B: 0, A: 0xFF})

	pixels, err := DecodePNGToRGB565(pngBytes)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}

	want := []byte{byte(BlackPixel >> 8), byte(BlackPixel)}
	if !bytes.Equal(pixels[:2], want) {
		t.Fatalf("opaque black pixel = % x, want % x", pixels[:2], want)
	}
	if want[0] != 0x00 || want[1] != 0x01 {
		t.Fatalf("BlackPixel = % x, want 00 01", want)
	}
}

func TestDecodePNGToRGB565_AlphaThresholdIsHalfOpacity(t *testing.T) {
	cases := []struct {
		name        string
		alpha       uint8
		transparent bool
	}{
		{"just below half", 0x7F, true},
		{"at half", 0x80, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pngBytes := solidPNG(t, CustomGlyphWidth, CustomGlyphHeight, color.NRGBA{R: 0, G: 0xFF, B: 0, A: c.alpha})

			pixels, err := DecodePNGToRGB565(pngBytes)
			if err != nil {
				t.Fatalf("DecodePNGToRGB565: %v", err)
			}

			gotTransparent := pixels[0] == 0 && pixels[1] == 0
			if gotTransparent != c.transparent {
				t.Fatalf("alpha %#02x: transparent = %v, want %v (pixel % x)", c.alpha, gotTransparent, c.transparent, pixels[:2])
			}
		})
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
