package layers

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// sbixGlyph is one glyph of a strike of a test font: a picture of type tag
// ("png ", "dupe", or Apple's other kinds), or nothing when tag is empty.
type sbixGlyph struct {
	tag  string
	data []byte
}

func pngGlyph(data []byte) sbixGlyph { return sbixGlyph{"png ", data} }

func dupeGlyph(of uint16) sbixGlyph {
	return sbixGlyph{"dupe", binary.BigEndian.AppendUint16(nil, of)}
}

// sbixStrike is one strike of a test font.
type sbixStrike struct {
	ppem   uint16
	glyphs []sbixGlyph
}

// writeSbixFont writes a font with a format 12 cmap, maxp, and an sbix table
// of the strikes, wrapped in a collection when ttc is set. Glyph 0 is
// .notdef, with no picture; strike i holds glyphs 1 to len(glyphs).
func writeSbixFont(t *testing.T, name string, ttc bool, cmap map[rune]uint16, strikes []sbixStrike) {
	t.Helper()
	be := binary.BigEndian
	numGlyphs := len(strikes[0].glyphs) + 1

	var cm bytes.Buffer
	runes := []rune{}
	for r := range cmap {
		runes = append(runes, r)
	}
	slices.Sort(runes)
	binary.Write(&cm, be, []uint16{0, 1, 3, 10})
	binary.Write(&cm, be, uint32(12))
	binary.Write(&cm, be, []uint16{12, 0})
	binary.Write(&cm, be, []uint32{uint32(16 + 12*len(runes)), 0, uint32(len(runes))})
	for _, r := range runes {
		binary.Write(&cm, be, []uint32{uint32(r), uint32(r), uint32(cmap[r])})
	}

	var maxp bytes.Buffer
	binary.Write(&maxp, be, uint32(0x00005000))
	binary.Write(&maxp, be, uint16(numGlyphs))

	// Each strike: ppem, ppi, numGlyphs+1 offsets from its start, the data.
	var blobs [][]byte
	for _, st := range strikes {
		head := uint32(4 + 4*(numGlyphs+1))
		var data bytes.Buffer
		offsets := []uint32{head, head} // .notdef is empty
		for _, g := range st.glyphs {
			if g.tag != "" {
				binary.Write(&data, be, []int16{0, 0})
				data.WriteString(g.tag)
				data.Write(g.data)
			}
			offsets = append(offsets, head+uint32(data.Len()))
		}
		var out bytes.Buffer
		binary.Write(&out, be, []uint16{st.ppem, 72})
		binary.Write(&out, be, offsets)
		out.Write(data.Bytes())
		blobs = append(blobs, out.Bytes())
	}
	var sbix bytes.Buffer
	binary.Write(&sbix, be, []uint16{1, 1})
	binary.Write(&sbix, be, uint32(len(blobs)))
	next := uint32(8 + 4*len(blobs))
	for _, b := range blobs {
		binary.Write(&sbix, be, next)
		next += uint32(len(b))
	}
	for _, b := range blobs {
		sbix.Write(b)
	}

	tables := []struct {
		tag  string
		data []byte
	}{{"cmap", cm.Bytes()}, {"maxp", maxp.Bytes()}, {"sbix", sbix.Bytes()}}
	base := 0
	if ttc {
		base = 16
	}
	var out bytes.Buffer
	if ttc {
		out.WriteString("ttcf")
		binary.Write(&out, be, []uint16{1, 0})
		binary.Write(&out, be, []uint32{1, uint32(base)})
	}
	binary.Write(&out, be, uint32(0x00010000))
	binary.Write(&out, be, []uint16{uint16(len(tables)), 0, 0, 0})
	at := base + 12 + 16*len(tables)
	for _, tb := range tables {
		out.WriteString(tb.tag)
		binary.Write(&out, be, []uint32{0, uint32(at), uint32(len(tb.data))})
		at += (len(tb.data) + 3) &^ 3
	}
	for _, tb := range tables {
		out.Write(tb.data)
		out.Write(make([]byte, (4-len(tb.data)%4)%4))
	}
	if err := os.WriteFile(name, out.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

var magenta = color.NRGBA{0xff, 0x00, 0xff, 0xff}

// strikePNG is a 160x160 picture with a magenta 60x80 box off its centre, as
// a strike of Apple Color Emoji has margins of its own.
func strikePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 160, 160))
	for y := 20; y < 100; y++ {
		for x := 30; x < 90; x++ {
			img.SetNRGBA(x, y, magenta)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// testEmojiFont writes a font with the fox (glyph 1, a PNG) and the rocket
// (glyph 2, a dupe of the fox), and uses it until the test ends.
func testEmojiFont(t *testing.T, ttc bool) {
	t.Helper()
	name := filepath.Join(t.TempDir(), "emoji.ttc")
	writeSbixFont(t, name, ttc, map[rune]uint16{0x1f98a: 1, 0x1f680: 2}, []sbixStrike{
		{20, []sbixGlyph{{}, {}}},
		{160, []sbixGlyph{pngGlyph(strikePNG(t)), dupeGlyph(1)}},
		{96, []sbixGlyph{{}, {}}},
	})
	UseEmojiFont(name)
	t.Cleanup(func() { UseEmojiFont("") })
	if err := EmojiFontErr(); err != nil {
		t.Fatal(err)
	}
}

// TestEmojiFont is task 0074's DoD-2: an emoji ref draws the font's picture,
// fitted to the round, and the Fluent file when the font has none.
func TestEmojiFont(t *testing.T) {
	fluentFox := emojiTile(t, emojiFox, "", nil, 0, ColorRunning)
	fluentBear := emojiTile(t, "", "", []string{"emoji/1f43b.png"}, 0, ColorRunning)
	github := emojiTile(t, "github.png", "", nil, 0, ColorRunning)
	if fluentFox.NRGBAAt(64, 64) == magenta {
		t.Fatal("the fox is magenta before UseEmojiFont")
	}
	for _, ttc := range []bool{false, true} {
		testEmojiFont(t, ttc)

		icon := fontIcon(emojiFox)
		if icon == nil {
			t.Fatalf("ttc %v: no picture for the fox", ttc)
		}
		// The box is 60x80, so its corners lie 50 px from the centre.
		reach, minX, maxX := 0.0, 128, -1
		for y := 0; y < 128; y++ {
			for x := 0; x < 128; x++ {
				if _, _, _, a := icon.At(x, y).RGBA(); a >= 0x8000 {
					reach = math.Max(reach, math.Hypot(float64(x)+0.5-64, float64(y)+0.5-64))
					minX, maxX = min(minX, x), max(maxX, x)
				}
			}
		}
		if reach < 48.5 || reach > 51.5 {
			t.Errorf("ttc %v: the fox reaches %.1f px, want 50", ttc, reach)
		}
		if l, r := 64-minX, maxX+1-64; l-r > 1 || r-l > 1 {
			t.Errorf("ttc %v: the fox spans x %d..%d, not centred", ttc, minX, maxX)
		}

		if img := emojiTile(t, emojiFox, "", nil, 0, ColorRunning); img.NRGBAAt(64, 64) != magenta {
			t.Errorf("ttc %v: iconRef fox centre is %v, want the font's", ttc, img.NRGBAAt(64, 64))
		}
		pair := emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 0, ColorRunning)
		for _, spot := range emojiLayout[true][1] {
			if pair.NRGBAAt(spot.x, spot.y) != magenta {
				t.Errorf("ttc %v: iconEmoji at (%d, %d) is %v, want the font's (the rocket is a dupe)", ttc, spot.x, spot.y, pair.NRGBAAt(spot.x, spot.y))
			}
		}
		if img := emojiTile(t, "", "", []string{"emoji/1f43b.png"}, 0, ColorRunning); !bytes.Equal(img.Pix, fluentBear.Pix) {
			t.Errorf("ttc %v: the bear, which the font lacks, is not the Fluent picture", ttc)
		}
		if img := emojiTile(t, "github.png", "", nil, 0, ColorRunning); !bytes.Equal(img.Pix, github.Pix) {
			t.Errorf("ttc %v: github.png changed with an emoji font", ttc)
		}
	}
}

// TestEmojiFontMissing is DoD-3: a host with no font draws the Fluent files.
func TestEmojiFontMissing(t *testing.T) {
	want := emojiTile(t, emojiFox, "", nil, 0, ColorRunning)
	UseEmojiFont(filepath.Join(t.TempDir(), "none.ttc"))
	defer UseEmojiFont("")
	if err := EmojiFontErr(); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("EmojiFontErr() = %v, want not exist", err)
	}
	if got := emojiTile(t, emojiFox, "", nil, 0, ColorRunning); !bytes.Equal(got.Pix, want.Pix) {
		t.Error("with no font, the fox is not the Fluent picture")
	}

	bad := filepath.Join(t.TempDir(), "bad.ttc")
	if err := os.WriteFile(bad, []byte("not a font at all, just some bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	UseEmojiFont(bad)
	if EmojiFontErr() == nil {
		t.Error("a file that is not a font reads without error")
	}
	if got := emojiTile(t, emojiFox, "", nil, 0, ColorRunning); !bytes.Equal(got.Pix, want.Pix) {
		t.Error("with a bad font, the fox is not the Fluent picture")
	}
}

// TestEmojiFontStrikes: a glyph whose largest strike is not a PNG draws from
// the largest one that is.
func TestEmojiFontStrikes(t *testing.T) {
	small := image.NewNRGBA(image.Rect(0, 0, 40, 40))
	for i := range small.Pix {
		small.Pix[i] = 0xff
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, small); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "emoji.ttc")
	writeSbixFont(t, name, true, map[rune]uint16{0x1f98a: 1}, []sbixStrike{
		{40, []sbixGlyph{pngGlyph(buf.Bytes())}},
		{160, []sbixGlyph{{"emjc", []byte{1, 2, 3, 4}}}},
		{96, []sbixGlyph{pngGlyph(strikePNG(t))}},
	})
	UseEmojiFont(name)
	defer UseEmojiFont("")
	icon := fontIcon(emojiFox)
	if icon == nil {
		t.Fatal("no picture for the fox")
	}
	if got := color.NRGBAModel.Convert(icon.At(64, 64)).(color.NRGBA); got != magenta {
		t.Errorf("centre is %v, want the magenta of the 96 px strike", got)
	}
}

func TestRefRune(t *testing.T) {
	for ref, want := range map[string]rune{
		"emoji/1f98a.png":      0x1f98a,
		"emoji/2693-fe0f.png":  0x2693,
		"emoji/1f3f3-200d.png": -1,
		"emoji/zz.png":         -1,
		"emoji/110000.png":     -1,
	} {
		got, ok := refRune(ref)
		if !ok {
			got = -1
		}
		if got != want {
			t.Errorf("refRune(%q) = %x, want %x", ref, got, want)
		}
	}
}
