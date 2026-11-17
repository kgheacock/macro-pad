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

// isInk reports whether a pixel of a cell of fill is at least half way from the
// fill to the ink.
func isInk(px color.NRGBA, fill Fill) bool {
	dist := func(a, b color.NRGBA) int {
		abs := func(x int) int {
			if x < 0 {
				return -x
			}
			return x
		}
		return abs(int(a.R)-int(b.R)) + abs(int(a.G)-int(b.G)) + abs(int(a.B)-int(b.B))
	}
	return 2*dist(px, badgeFills[fill]) >= dist(badgeInk[fill], badgeFills[fill])
}

// TestBadgeSheet is task 0091's DoD-1: the sheet is 76,800 bytes, it has a disc
// of each of the three fills on each of five key colors, and a cell of each
// character of each fill, and the cells are opaque with ink in them.
func TestBadgeSheet(t *testing.T) {
	sheet, err := BuildBadgeSheet()
	if err != nil {
		t.Fatal(err)
	}
	if got := len(sheet.Pixels); got != 76800 {
		t.Fatalf("the sheet is %d bytes, want 76,800", got)
	}
	if sheet.Image.Bounds().Dx() != 256 || sheet.Image.Bounds().Dy() != 150 {
		t.Fatalf("the sheet is %v, want 256 by 150", sheet.Image.Bounds())
	}
	if len(sheet.Colors) != 5 {
		t.Fatalf("the sheet has %d columns of discs, want 5", len(sheet.Colors))
	}
	for fill := FillRed; fill < fillCount; fill++ {
		for i := range sheet.Colors {
			disc := badgeDiscSprite(i, fill)
			if !disc.In(image.Rect(0, 0, 238, 102)) {
				t.Fatalf("disc %d of fill %d at %v is outside the disc rows", i, fill, disc)
			}
			centre := sheet.Image.NRGBAAt(disc.Min.X+badgeDisc/2, disc.Min.Y+badgeDisc/2)
			if centre != badgeFills[fill] {
				t.Errorf("fill %d disc %d: centre is %v, want %v", fill, i, centre, badgeFills[fill])
			}
			if a := sheet.Image.NRGBAAt(disc.Min.X, disc.Min.Y).A; a != 0 {
				t.Errorf("fill %d disc %d: the corner has alpha %d, want a transparent corner", fill, i, a)
			}
			if ring := sheet.Image.NRGBAAt(disc.Min.X+badgeDisc/2, disc.Min.Y); ring.A == 0 {
				t.Errorf("fill %d disc %d: no ring at the top", fill, i)
			}
		}
		for _, c := range []struct {
			r     rune
			small bool
		}{{'0', false}, {'1', false}, {'2', false}, {'3', false}, {'4', false}, {'5', false}, {'6', false}, {'7', false}, {'8', false}, {'9', false}, {'+', false}, {'9', true}, {'+', true}} {
			rect := badgeSpriteRect(fill, c.r, c.small)
			if !rect.In(image.Rect(0, badgeCellY[fill], BadgeSheetWidth, badgeCellY[fill]+16)) {
				t.Fatalf("cell %q small=%v of fill %d is at %v, outside its set", c.r, c.small, fill, rect)
			}
			ink := 0
			for y := rect.Min.Y; y < rect.Max.Y; y++ {
				for x := rect.Min.X; x < rect.Max.X; x++ {
					px := sheet.Image.NRGBAAt(x, y)
					if px.A != 0xFF {
						t.Fatalf("cell %q small=%v of fill %d is not opaque at (%d, %d)", c.r, c.small, fill, x, y)
					}
					if isInk(px, fill) {
						ink++
					}
				}
			}
			if ink < 4 {
				t.Errorf("cell %q small=%v of fill %d has %d ink pixels, want a drawn character", c.r, c.small, fill, ink)
			}
		}
	}
	checkGoldenNear(t, "badge-sheet.png", onGray(sheet.Image))
}

// TestBadgeDigitsAreLegible is task 0091's goal of digits at least 13 px high.
func TestBadgeDigitsAreLegible(t *testing.T) {
	sheet, err := BuildBadgeSheet()
	if err != nil {
		t.Fatal(err)
	}
	for fill := FillRed; fill < fillCount; fill++ {
		for _, r := range "0123456789" {
			rect := badgeSpriteRect(fill, r, false)
			top, bottom := rect.Max.Y, rect.Min.Y
			for y := rect.Min.Y; y < rect.Max.Y; y++ {
				for x := rect.Min.X; x < rect.Max.X; x++ {
					if isInk(sheet.Image.NRGBAAt(x, y), fill) {
						top, bottom = min(top, y), max(bottom, y+1)
					}
				}
			}
			if h := bottom - top; h < 13 {
				t.Errorf("digit %q of fill %d is %d px high, want at least 13", r, fill, h)
			}
		}
	}
}

// TestBadgeDigitsFitDisc is task 0091's DoD-1: every digit cell, of every count
// on every fill, lies inside the inside of the disc, which is inside the ring.
func TestBadgeDigitsFitDisc(t *testing.T) {
	inner := float64(badgeDisc)/2 - badgeRing
	for fill := FillRed; fill < fillCount; fill++ {
		disc := badgeDiscRect(fill)
		centre := image.Pt(disc.Min.X+badgeDisc/2, disc.Min.Y+badgeDisc/2)
		for count := 1; count <= MaxBadgeCount+1; count++ {
			for _, c := range badgeCells(fill, count) {
				rect := image.Rectangle{Min: c.dst, Max: c.dst.Add(c.src.Size())}
				for _, corner := range []image.Point{rect.Min, {rect.Max.X, rect.Min.Y}, {rect.Min.X, rect.Max.Y}, rect.Max} {
					if d := math.Hypot(float64(corner.X-centre.X), float64(corner.Y-centre.Y)); d > inner {
						t.Fatalf("count %d on fill %d: a cell corner %v is %.1f px from the centre, past the %.0f px inside of the disc", count, fill, corner, d, inner)
					}
				}
			}
		}
	}
}

// TestBadgeSheetCrossesTheWire sends the sheet through the emulator, and the
// emulator's board receives it whole.
func TestBadgeSheetCrossesTheWire(t *testing.T) {
	sheet, err := BuildBadgeSheet()
	if err != nil {
		t.Fatal(err)
	}
	e := transport.NewEmulator()
	t.Cleanup(func() { e.Close() })
	go e.StoreBadgeSheet(sheet.Colors, sheet.Pixels)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, ok := e.BadgeSheet(); ok {
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

// changedBounds returns the smallest rectangle that holds every pixel of img
// that differs from a blank canvas.
func changedBounds(img *image.NRGBA) image.Rectangle {
	var out image.Rectangle
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			if img.NRGBAAt(x, y) != (color.NRGBA{}) {
				out = out.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return out
}

// TestBadges is task 0091's DoD-2: a count of 0 draws nothing, each count draws
// only in its own rectangle, and a count above 99 draws "99+".
func TestBadges(t *testing.T) {
	zero := blankCanvas()
	if err := DrawBadges(zero, ColorDone, Badges{}); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(zero.Pix, blankCanvas().Pix) {
		t.Error("counts of 0 drew a badge")
	}

	// The firmware lands the sprites at the same places (BADGE_MARGIN).
	for fill, want := range map[Fill]image.Rectangle{FillRed: image.Rect(89, 89, 123, 123), FillGreen: image.Rect(52, 89, 86, 123), FillYellow: image.Rect(15, 89, 49, 123)} {
		if got := badgeDiscRect(fill); got != want {
			t.Errorf("the disc of fill %d is at %v, want %v", fill, got, want)
		}
	}

	only := func(fill Fill, count byte) Badges {
		var b Badges
		switch fill {
		case FillRed:
			b.Red = count
		case FillGreen:
			b.Green = count
		case FillYellow:
			b.Yellow = count
		}
		return b
	}
	for fill := FillRed; fill < fillCount; fill++ {
		for _, count := range []byte{1, 9, 10, 99, 100, 255} {
			img := blankCanvas()
			if err := DrawBadges(img, ColorDone, only(fill, count)); err != nil {
				t.Fatal(err)
			}
			if got := changedBounds(img); got.Empty() || !got.In(badgeDiscRect(fill)) {
				t.Errorf("fill %d count %d changed %v, want pixels only in %v", fill, count, got, badgeDiscRect(fill))
			}
		}
	}

	// The three together change nothing outside their rectangles, and each one
	// draws what it draws alone.
	all := blankCanvas()
	if err := DrawBadges(all, ColorDone, Badges{Red: 7, Green: 8, Yellow: 9}); err != nil {
		t.Fatal(err)
	}
	for fill := FillRed; fill < fillCount; fill++ {
		alone := blankCanvas()
		if err := DrawBadges(alone, ColorDone, only(fill, byte(7+int(fill)))); err != nil {
			t.Fatal(err)
		}
		r := badgeDiscRect(fill)
		for y := r.Min.Y; y < r.Max.Y; y++ {
			for x := r.Min.X; x < r.Max.X; x++ {
				if all.NRGBAAt(x, y) != alone.NRGBAAt(x, y) {
					t.Fatalf("fill %d differs at (%d, %d) when the others are drawn", fill, x, y)
				}
			}
		}
	}

	draw99 := func(count byte) *image.NRGBA {
		img := blankCanvas()
		if err := DrawBadges(img, ColorDone, Badges{Red: count}); err != nil {
			t.Fatal(err)
		}
		return img
	}
	a, b, c := draw99(99), draw99(100), draw99(255)
	if bytes.Equal(a.Pix, b.Pix) {
		t.Error("100 draws the same badge as 99")
	}
	if !bytes.Equal(b.Pix, c.Pix) {
		t.Error("100 and 255 do not both draw 99+")
	}
	if cells := badgeCells(FillRed, 100); len(cells) != 3 || cells[2].src != badgeSpriteRect(FillRed, '+', true) || cells[0].src != badgeSpriteRect(FillRed, '9', true) {
		t.Errorf("100 has cells %v, want the small 9, 9, and +", cells)
	}
	if cells := badgeCells(FillGreen, 7); len(cells) != 1 || cells[0].src != badgeSpriteRect(FillGreen, '7', false) {
		t.Errorf("7 has cells %v, want one big 7 of the green set", cells)
	}

	// The golden file: the counts of 0 to three discs over a red key, a white
	// key, and an icon.
	sets := []Badges{{Red: 1}, {Green: 9}, {Yellow: 10}, {Red: 3, Green: 12}, {Red: 42, Green: 7, Yellow: 99}, {Red: 100, Green: 100, Yellow: 100}}
	rows := []struct {
		key uint16
		ref string
	}{{ColorError, ""}, {ColorNone, ""}, {ColorRunning, "github.png"}, {ColorWarn, ""}, {ColorDone, ""}}
	size := transport.CustomGlyphWidth
	sheet := image.NewNRGBA(image.Rect(0, 0, size*len(sets), size*len(rows)))
	for r, row := range rows {
		for c, counts := range sets {
			data, err := renderGlyph(assetsDir, row.ref, counts, row.key)
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
	checkGoldenNear(t, "icon-badges.png", sheet)
}

// TestBadgesOpaque is DoD for the 1-bit transparency of task 0041: over an icon
// with a transparent background, no pixel of a disc is transparent.
func TestBadgesOpaque(t *testing.T) {
	for _, key := range BadgeColors {
		data, err := renderGlyph(assetsDir, "build.png", Badges{Red: 42, Green: 7, Yellow: 1}, key)
		if err != nil {
			t.Fatal(err)
		}
		pixels, err := transport.DecodePNGToRGB565(data)
		if err != nil {
			t.Fatal(err)
		}
		for fill := FillRed; fill < fillCount; fill++ {
			disc := badgeDiscRect(fill)
			centre := image.Pt(disc.Min.X+badgeDisc/2, disc.Min.Y+badgeDisc/2)
			for y := disc.Min.Y; y < disc.Max.Y; y++ {
				for x := disc.Min.X; x < disc.Max.X; x++ {
					if math.Hypot(float64(x-centre.X), float64(y-centre.Y)) > badgeDisc/2-1 {
						continue
					}
					i := (y*transport.CustomGlyphWidth + x) * 2
					if pixels[i] == 0 && pixels[i+1] == 0 {
						t.Fatalf("key %#04x: pixel (%d, %d) of disc %d is transparent", key, x, y, fill)
					}
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
	// Task 0073: the emoji set follows the same rules.
	emoji, err := filepath.Glob(filepath.Join(assetsDir, "emoji", "*.png"))
	if err != nil || len(emoji) < 128 {
		t.Fatalf("%d emoji in %s/emoji, want at least 128: %v", len(emoji), assetsDir, err)
	}
	refs = append(refs, emoji...)
	centre := float64(transport.CustomGlyphWidth) / 2
	for _, path := range refs {
		rel, _ := filepath.Rel(assetsDir, path)
		ref := filepath.ToSlash(rel)
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		icon, err := png.Decode(f)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		// Task 0069: no solid icon pixel lies outside the round, and the two
		// icons with a dark disc of their own keep 3 px of white around it.
		reach := 0.0
		for y := 0; y < transport.CustomGlyphHeight; y++ {
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				if _, _, _, ia := icon.At(x, y).RGBA(); ia >= 0x8000 {
					reach = math.Max(reach, math.Hypot(float64(x)+0.5-centre, float64(y)+0.5-centre))
				}
			}
		}
		if reach > iconRoundRadius-0.5 {
			t.Errorf("%s: a solid pixel lies %.1f px from the centre, past the round of %d", ref, reach, iconRoundRadius)
		}
		if (ref == "github.png" || ref == "create.png") && reach > iconRoundRadius-3 {
			t.Errorf("%s: its dark disc reaches %.1f px, want at most %d to leave 3 px of white round", ref, reach, iconRoundRadius-3)
		}
		data, err := renderGlyph(assetsDir, ref, Badges{}, ColorError)
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
				if c := color.NRGBAModel.Convert(got.At(x, y)).(color.NRGBA); c != badgeWhite {
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

	// The golden file: one icon on each status color, and one with badges.
	var tiles []image.Image
	for _, key := range append(append([]uint16(nil), BadgeColors...), ColorUp) {
		data, err := renderGlyph(assetsDir, "github.png", Badges{Red: 12, Green: 3, Yellow: 99}, key)
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
