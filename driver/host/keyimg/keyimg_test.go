package keyimg

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func TestHex(t *testing.T) {
	c, err := Hex("#3D5CFF")
	if err != nil {
		t.Fatal(err)
	}
	if c != (Color{0x3D, 0x5C, 0xFF}) {
		t.Errorf("Hex = %v", c)
	}
	if c.RGB565() != 0x3AFF { // driver/README.md's RUNNING blue
		t.Errorf("RGB565 = %#04x, want 0x3AFF", c.RGB565())
	}
	if c.String() != "#3D5CFF" {
		t.Errorf("String = %s", c)
	}
	if _, err := Hex("12D84A"); err != nil {
		t.Errorf("Hex without #: %v", err)
	}
	for _, bad := range []string{"", "#FFF", "#GGGGGG", "#1234567"} {
		if _, err := Hex(bad); err == nil {
			t.Errorf("Hex(%q) = nil error", bad)
		}
	}
}

func TestDim(t *testing.T) {
	c := Color{200, 100, 50}
	if got := c.Dim(0.5); got != (Color{100, 50, 25}) {
		t.Errorf("Dim(0.5) = %v", got)
	}
	if got := c.Dim(2); got != c {
		t.Errorf("Dim(2) = %v, want clamped to %v", got, c)
	}
	if got := c.Dim(-1); got != (Color{}) {
		t.Errorf("Dim(-1) = %v, want black", got)
	}
}

func TestThumbnail_Letterboxes(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for y := 0; y < 200; y++ {
		for x := 0; x < 400; x++ {
			src.SetRGBA(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	got := Thumbnail(src)
	if got.Bounds() != image.Rect(0, 0, Size, Size) {
		t.Fatalf("bounds = %v", got.Bounds())
	}
	// A 2:1 image fills rows 32..95 and leaves the bars black.
	if r, _, _, _ := got.At(64, 10).RGBA(); r != 0 {
		t.Errorf("top bar not black")
	}
	if r, _, _, _ := got.At(64, 64).RGBA(); r>>8 < 250 {
		t.Errorf("center not white: %d", r>>8)
	}
}

func TestStale(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, Size, Size))
	for i := range src.Pix {
		src.Pix[i] = 200
	}
	got := Stale(src)
	if c := got.RGBAAt(10, 10); c.R != 100 {
		t.Errorf("dimmed pixel = %v, want R 100", c)
	}
	if c := got.RGBAAt(10, Size-1); c != (color.RGBA{0xFF, 0xB0, 0x00, 0xFF}) {
		t.Errorf("strip pixel = %v, want amber", c)
	}
}

func TestText_DrawsOnBackground(t *testing.T) {
	fg, bg := MustHex("#FFFFFF"), MustHex("#000080")
	img := Text("work\n2/3", fg, bg)
	if img.Bounds() != image.Rect(0, 0, Size, Size) {
		t.Fatalf("bounds = %v", img.Bounds())
	}
	if r, g, b, _ := img.At(0, 0).RGBA(); r != 0 || g != 0 || b>>8 != 0x80 {
		t.Errorf("corner is not the background")
	}
	lit := 0
	for y := 0; y < Size; y++ {
		for x := 0; x < Size; x++ {
			if r, _, _, _ := img.At(x, y).RGBA(); r>>8 == 0xFF {
				lit++
			}
		}
	}
	if lit == 0 {
		t.Error("no foreground pixels drawn")
	}
}

func TestPNG_RoundTrips(t *testing.T) {
	b, err := PNG(Thumbnail(image.NewRGBA(image.Rect(0, 0, 10, 10))))
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != Size {
		t.Errorf("decoded width %d", img.Bounds().Dx())
	}
}
