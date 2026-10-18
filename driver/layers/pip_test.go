package layers

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// checkGolden compares img with testdata/name. UPDATE_GOLDEN=1 writes the file.
func checkGolden(t *testing.T, name string, img image.Image) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if os.Getenv("UPDATE_GOLDEN") != "" {
		var buf bytes.Buffer
		if err := png.Encode(&buf, img); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("%v (UPDATE_GOLDEN=1 writes it)", err)
	}
	defer f.Close()
	want, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	if want.Bounds() != img.Bounds() {
		t.Fatalf("%s is %v, the render is %v", name, want.Bounds(), img.Bounds())
	}
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			if color.NRGBAModel.Convert(want.At(x, y)) != color.NRGBAModel.Convert(img.At(x, y)) {
				t.Fatalf("%s differs at (%d, %d): file %v, render %v (UPDATE_GOLDEN=1 rewrites it)", name, x, y, want.At(x, y), img.At(x, y))
			}
		}
	}
}

// onGray draws img over mid-gray at 3 times the size, so a golden file shows
// the transparent pixels.
func onGray(img image.Image) *image.NRGBA {
	b := img.Bounds()
	out := image.NewNRGBA(image.Rect(0, 0, b.Dx()*3, b.Dy()*3))
	draw.Draw(out, out.Bounds(), image.NewUniform(color.NRGBA{R: 0x80, G: 0x80, B: 0x80, A: 0xFF}), image.Point{}, draw.Src)
	for y := 0; y < b.Dy(); y++ {
		for x := 0; x < b.Dx(); x++ {
			c := img.At(b.Min.X+x, b.Min.Y+y)
			for dy := 0; dy < 3; dy++ {
				for dx := 0; dx < 3; dx++ {
					if _, _, _, a := c.RGBA(); a > 0 {
						out.Set(x*3+dx, y*3+dy, c)
					}
				}
			}
		}
	}
	return out
}

// TestPipSheet is task 0062's DoD-1: the sheet has five discs and 13 character
// cells, and it is exactly the size of a glyph.
func TestPipSheet(t *testing.T) {
	sheet, err := BuildPipSheet()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sheet.Pixels); got != transport.CustomGlyphPixelsSize {
		t.Fatalf("the sheet is %d bytes, want %d", got, transport.CustomGlyphPixelsSize)
	}
	if len(sheet.Colors) != 5 {
		t.Fatalf("the sheet has %d discs, want 5", len(sheet.Colors))
	}
	for i := range sheet.Colors {
		disc := image.Rect(pipDisc*i, 0, pipDisc*(i+1), pipDisc)
		centre := sheet.Image.NRGBAAt(disc.Min.X+pipDisc/2, pipDisc/2)
		if centre != pipRed {
			t.Errorf("disc %d: centre is %v, want the red %v", i, centre, pipRed)
		}
		if a := sheet.Image.NRGBAAt(disc.Min.X, 0).A; a != 0 {
			t.Errorf("disc %d: the corner has alpha %d, want a transparent corner", i, a)
		}
	}
	cells := []struct {
		r     rune
		small bool
	}{{'0', false}, {'1', false}, {'2', false}, {'3', false}, {'4', false}, {'5', false}, {'6', false}, {'7', false}, {'8', false}, {'9', false}, {'+', false}, {'9', true}, {'+', true}}
	for _, c := range cells {
		rect := pipSpriteRect(c.r, c.small)
		white := 0
		for y := rect.Min.Y; y < rect.Max.Y; y++ {
			for x := rect.Min.X; x < rect.Max.X; x++ {
				px := sheet.Image.NRGBAAt(x, y)
				if px.A != 0xFF {
					t.Fatalf("cell %q small=%v is not opaque at (%d, %d)", c.r, c.small, x, y)
				}
				if px.R > 0xE0 && px.G > 0xE0 && px.B > 0xE0 {
					white++
				}
			}
		}
		if white < 4 {
			t.Errorf("cell %q small=%v has %d white pixels, want a drawn character", c.r, c.small, white)
		}
	}
	checkGolden(t, "pip-sheet.png", onGray(sheet.Image))
}

// TestPipSheetCrossesTheWire sends the sheet through the emulator, and the
// emulator's board receives it whole.
func TestPipSheetCrossesTheWire(t *testing.T) {
	sheet, err := BuildPipSheet()
	if err != nil {
		t.Fatal(err)
	}
	e := transport.NewEmulator()
	t.Cleanup(func() { e.Close() })
	go e.StorePipSheet(sheet.Colors, sheet.Pixels)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := e.PipSheet(); ok {
			if !bytes.Equal(got.Pixels, sheet.Pixels) || len(got.Colors) != len(sheet.Colors) {
				t.Fatal("the board received another sheet")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("the board received no sheet")
}

func blankCanvas() *image.NRGBA {
	return image.NewNRGBA(image.Rect(0, 0, transport.CustomGlyphWidth, transport.CustomGlyphHeight))
}

// TestPip is task 0062's DoD for the host drawing: a count of 0 draws no pip,
// and a count above 99 draws "99+".
func TestPip(t *testing.T) {
	zero := blankCanvas()
	if err := DrawPip(zero, ColorDone, 0); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(zero.Pix, blankCanvas().Pix) {
		t.Error("a count of 0 drew a pip")
	}

	draw99 := func(count int) *image.NRGBA {
		img := blankCanvas()
		if err := DrawPip(img, ColorDone, count); err != nil {
			t.Fatal(err)
		}
		return img
	}
	a, b, c := draw99(99), draw99(100), draw99(5000)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Error("100 draws the same pip as 99")
	}
	if !bytes.Equal(b.Pix, c.Pix) {
		t.Error("100 and 5000 do not both draw 99+")
	}
	if cells := pipCells(100); len(cells) != 3 || cells[2].src != pipSpriteRect('+', true) {
		t.Errorf("100 has cells %v, want the small 9, 9, and +", cells)
	}
	if cells := pipCells(7); len(cells) != 1 || cells[0].src != pipSpriteRect('7', false) {
		t.Errorf("7 has cells %v, want one big 7", cells)
	}
}

// TestPipOpaque is DoD for the 1-bit transparency of task 0041: over an icon
// with a transparent background, no pixel of the pip's disc is transparent.
func TestPipOpaque(t *testing.T) {
	for _, key := range PipColors {
		data, err := renderGlyph(assetsDir, "build.png", 42, key)
		if err != nil {
			t.Fatal(err)
		}
		pixels, err := transport.DecodePNGToRGB565(data)
		if err != nil {
			t.Fatal(err)
		}
		disc := pipDiscRect()
		centre := image.Pt(disc.Min.X+pipDisc/2, disc.Min.Y+pipDisc/2)
		for y := disc.Min.Y; y < disc.Max.Y; y++ {
			for x := disc.Min.X; x < disc.Max.X; x++ {
				if math.Hypot(float64(x-centre.X), float64(y-centre.Y)) > pipDisc/2-1 {
					continue
				}
				i := (y*transport.CustomGlyphWidth + x) * 2
				if pixels[i] == 0 && pixels[i+1] == 0 {
					t.Fatalf("key %#04x: pixel (%d, %d) of the disc is transparent", key, x, y)
				}
			}
		}
	}
}

// TestIconRound is task 0062's round: every icon sits on a white round, the
// round fills what the icon leaves transparent, and the corners stay
// transparent.
func TestIconRound(t *testing.T) {
	refs, err := filepath.Glob(filepath.Join(assetsDir, "*.png"))
	if err != nil || len(refs) == 0 {
		t.Fatalf("no icons in %s: %v", assetsDir, err)
	}
	centre := float64(transport.CustomGlyphWidth) / 2
	for _, path := range refs {
		ref := filepath.Base(path)
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		icon, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		data, err := renderGlyph(assetsDir, ref, 0, ColorError)
		if err != nil {
			t.Fatalf("%s: %v", ref, err)
		}
		got, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, a := got.At(0, 0).RGBA(); a != 0 {
			t.Errorf("%s: the corner is not transparent", ref)
		}
		for y := 0; y < transport.CustomGlyphHeight; y++ {
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				if math.Hypot(float64(x)+0.5-centre, float64(y)+0.5-centre) > iconRoundRadius-2 {
					continue
				}
				if _, _, _, ia := icon.At(x, y).RGBA(); ia != 0 {
					continue
				}
				if c := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA); c != pipWhite {
					t.Fatalf("%s: (%d, %d) is %v where the icon is transparent, want white", ref, x, y, c)
				}
			}
		}
		if ref == ShiftIcon {
			for y := 0; y < transport.CustomGlyphHeight; y++ {
				for x := 0; x < transport.CustomGlyphWidth; x++ {
					r, g, b, a := icon.At(x, y).RGBA()
					if a > 0x8000 && r>>8 > 0xF0 && g>>8 > 0xF0 && b>>8 > 0xF0 {
						t.Fatalf("%s has a white pixel at (%d, %d), which a white round hides", ref, x, y)
					}
				}
			}
		}
	}

	// The golden file: one icon on each status color, and one with a pip.
	var tiles []image.Image
	for _, key := range append(append([]uint16(nil), PipColors...), ColorUp) {
		data, err := renderGlyph(assetsDir, "github.png", 12, key)
		if err != nil {
			t.Fatal(err)
		}
		img, err := png.Decode(bytes.NewReader(data))
		if err != nil {
			t.Fatal(err)
		}
		tile := image.NewNRGBA(image.Rect(0, 0, transport.CustomGlyphWidth, transport.CustomGlyphHeight))
		draw.Draw(tile, tile.Bounds(), image.NewUniform(rgb565ToNRGBA(key)), image.Point{}, draw.Src)
		draw.Draw(tile, tile.Bounds(), img, image.Point{}, draw.Over)
		tiles = append(tiles, tile)
	}
	sheet := image.NewNRGBA(image.Rect(0, 0, transport.CustomGlyphWidth*len(tiles), transport.CustomGlyphHeight))
	for i, tile := range tiles {
		draw.Draw(sheet, image.Rect(i*transport.CustomGlyphWidth, 0, (i+1)*transport.CustomGlyphWidth, transport.CustomGlyphHeight), tile, image.Point{}, draw.Src)
	}
	checkGolden(t, "icon-round.png", sheet)
}

// TestPipGolden is the golden file of the pip: the counts 1, 9, 10, 42, 99, and
// 100 over a red key, a white key, and an icon.
func TestPipGolden(t *testing.T) {
	counts := []int{1, 9, 10, 42, 99, 100}
	rows := []struct {
		key uint16
		ref string
	}{{ColorError, ""}, {ColorNone, ""}, {ColorRunning, "github.png"}}
	size := transport.CustomGlyphWidth
	sheet := image.NewNRGBA(image.Rect(0, 0, size*len(counts), size*len(rows)))
	for r, row := range rows {
		for c, count := range counts {
			data, err := renderGlyph(assetsDir, row.ref, count, row.key)
			if err != nil {
				t.Fatal(err)
			}
			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatal(err)
			}
			cell := image.Rect(c*size, r*size, (c+1)*size, (r+1)*size)
			draw.Draw(sheet, cell, image.NewUniform(rgb565ToNRGBA(row.key)), image.Point{}, draw.Src)
			draw.Draw(sheet, cell, img, image.Point{}, draw.Over)
		}
	}
	checkGolden(t, "pips.png", sheet)
}
