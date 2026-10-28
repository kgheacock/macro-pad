package layers

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// textTile renders a text icon with no count and decodes it.
func textTile(t *testing.T, text string) *image.NRGBA {
	t.Helper()
	data, err := renderIcon(assetsDir, "", text, "", false, 0, ColorError)
	if err != nil {
		t.Fatalf("%q: %v", text, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, image.Point{}, draw.Src)
	return out
}

// TestIconText is task 0069's DoD-1: a text icon is words in black on a plain
// white tile, with no round and no transparent pixel, and a key shows it.
func TestIconText(t *testing.T) {
	cases := []string{
		"Hello",
		"macro-pad\nmain",
		"a-rather-long-channel-name-here",
		"é я 😀",
	}
	var tiles []image.Image
	for _, text := range cases {
		tile := textTile(t, text)
		if got := tile.NRGBAAt(0, 0); got != pipWhite {
			t.Errorf("%q: the corner is %v, want plain white", text, got)
		}
		if got := tile.NRGBAAt(64, 100); got != pipWhite {
			t.Errorf("%q: (64, 100) is %v, want white (no round)", text, got)
		}
		dark := 0
		for y := 0; y < transport.CustomGlyphHeight; y++ {
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				if tile.NRGBAAt(x, y).R < 0x80 {
					dark++
				}
			}
		}
		if dark < 40 {
			t.Errorf("%q: only %d dark pixels, want drawn text", text, dark)
		}
		tiles = append(tiles, tile)
	}
	sheet := image.NewNRGBA(image.Rect(0, 0, transport.CustomGlyphWidth*len(tiles), transport.CustomGlyphHeight))
	for i, tile := range tiles {
		draw.Draw(sheet, image.Rect(i*transport.CustomGlyphWidth, 0, (i+1)*transport.CustomGlyphWidth, transport.CustomGlyphHeight), tile, image.Point{}, draw.Src)
	}
	checkGolden(t, "icon-text.png", sheet)

	// A key shows it: the glyph the controller sends for key 0 is the tile.
	out := newFakeOut()
	c := open(t, "", out)
	if err := c.SetKey(Update{ID: "t", Status: StatusRunning, IconText: "Hello"}); err != nil {
		t.Fatal(err)
	}
	got := out.glyph(t, 0)
	want := textTile(t, "Hello")
	for y := 0; y < transport.CustomGlyphHeight; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			if g := color.NRGBAModel.Convert(got.At(x, y)); g != color.NRGBA(want.NRGBAAt(x, y)) {
				t.Fatalf("key 0 differs from the tile at (%d, %d): %v, want %v", x, y, g, want.NRGBAAt(x, y))
			}
		}
	}
}

// TestIconTextInvalid is DoD-2: a bad text is rejected and changes nothing.
func TestIconTextInvalid(t *testing.T) {
	c := open(t, "", newFakeOut())
	if err := c.SetKey(Update{ID: "k", IconText: "keep"}); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()
	if before.Keys[0] == nil || before.Keys[0].IconText != "keep" {
		t.Fatalf("key 0 is %+v, want the text %q", before.Keys[0], "keep")
	}
	for name, u := range map[string]Update{
		"both iconRef and iconText": {ID: "k", IconRef: "github.png", IconText: "x"},
		"33 runes":                  {ID: "k", IconText: strings.Repeat("é", MaxIconTextRunes+1)},
		"3 lines":                   {ID: "k", IconText: "a\nb\nc"},
		"a control character":       {ID: "k", IconText: "a\tb"},
		"a carriage return":         {ID: "k", IconText: "a\r\nb"},
		"only white space":          {ID: "k", IconText: " \n "},
		"invalid UTF-8":             {ID: "k", IconText: "a\xffb"},
	} {
		if err := c.SetKey(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if after := c.Snapshot(); after.Keys[0] == nil || !reflect.DeepEqual(after.Keys[0], before.Keys[0]) {
			t.Errorf("%s: changed key 0 to %+v", name, after.Keys[0])
		}
	}
	for name, text := range map[string]string{
		"empty":     "",
		"32 runes":  strings.Repeat("é", MaxIconTextRunes),
		"two lines": "parent\nname",
	} {
		if err := ValidateIconText(text); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// TestIconTextFit is DoD-4: long text is cut with an ellipsis, a rune the font
// lacks draws as "?", every pixel is opaque, the name is black and the parent
// gray, and nothing is drawn outside the box above the pip.
func TestIconTextFit(t *testing.T) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	if got := replaceMissing(f, "é я 😀"); got != "é я ?" {
		t.Errorf("replaceMissing = %q, want %q", got, "é я ?")
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{Size: 14, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("channel", 10)
	cut := fitWidth(face, long, textBoxX1-textBoxX0)
	if !strings.HasSuffix(cut, string(textEllipsis)) || textWidth(face, cut) > textBoxX1-textBoxX0 || utf8.RuneCountInString(cut) < 8 {
		t.Errorf("fitWidth = %q (%d px), want a cut that ends in an ellipsis and fits", cut, textWidth(face, cut))
	}
	if short := fitWidth(face, "main", textBoxX1-textBoxX0); short != "main" {
		t.Errorf("fitWidth cut a short text to %q", short)
	}

	for _, text := range []string{"Hello", "a-rather-long-channel-name-here", "parent\nname", "WWWWWWWWWWWWWWWW\nWWWWWWWWWWWWWWW", "é я 😀"} {
		tile := textTile(t, text)
		twoLines := strings.Contains(text, "\n")
		// Each line is a block of rows with ink; its darkest pixel is the
		// color of the line, as the strokes are wider than one pixel.
		var blocks []uint8
		inInk := false
		for y := 0; y < transport.CustomGlyphHeight; y++ {
			darkest := uint8(0xFF)
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				px := tile.NRGBAAt(x, y)
				if px.A != 0xFF {
					t.Fatalf("%q: (%d, %d) has alpha %d", text, x, y, px.A)
				}
				if px == pipWhite {
					continue
				}
				if x < textBoxX0 || x >= textBoxX1 || y < textBoxY0 || y >= textBoxY1 {
					t.Fatalf("%q: (%d, %d) is %v, outside the text box", text, x, y, px)
				}
				if px.R < darkest {
					darkest = px.R
				}
			}
			switch {
			case darkest == 0xFF:
				inInk = false
			case !inInk:
				inInk = true
				blocks = append(blocks, darkest)
			case darkest < blocks[len(blocks)-1]:
				blocks[len(blocks)-1] = darkest
			}
		}
		want := []uint8{textName.R}
		if twoLines {
			want = []uint8{textParent.R, textName.R}
		}
		if !bytes.Equal(blocks, want) {
			t.Errorf("%q: the darkest pixel of each line is %v, want %v (parent gray, name black)", text, blocks, want)
		}
	}
}

// TestIconTextPip checks that the count pip lies below the text, so it covers
// no letter.
func TestIconTextPip(t *testing.T) {
	plain := textTile(t, "parent\nname")
	data, err := renderIcon(assetsDir, "", "parent\nname", "", false, 42, ColorDone)
	if err != nil {
		t.Fatal(err)
	}
	img, _ := png.Decode(bytes.NewReader(data))
	disc := pipDiscRect()
	for y := 0; y < transport.CustomGlyphHeight; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			inPip := image.Pt(x, y).In(disc)
			same := color.NRGBAModel.Convert(img.At(x, y)) == color.NRGBA(plain.NRGBAAt(x, y))
			if !inPip && !same {
				t.Fatalf("(%d, %d) changed outside the pip sprite", x, y)
			}
		}
	}
	if disc.Min.Y < textBoxY1 {
		t.Errorf("the pip starts at y %d, above the text box end %d", disc.Min.Y, textBoxY1)
	}
}

// TestIconTextFrames is DoD-5: two keys with one text share a slot whatever
// their colors, another text takes another slot, and a count stores no frame.
func TestIconTextFrames(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	for _, u := range []Update{
		{ID: "a", Status: StatusRunning, IconText: "same"},
		{ID: "b", Status: StatusDone, IconText: "same"},
		{ID: "c", Status: StatusDone, IconText: "other"},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	out.mu.Lock()
	stored := 0
	for _, f := range out.slots {
		if f.png != nil { // the keys with no content have color-only frames
			stored++
		}
	}
	out.mu.Unlock()
	if stored != 2 {
		t.Fatalf("the board holds %d frames, want 2 (one for each text)", stored)
	}
	if ka, kb := frameKeyOf(nodeView(&Node{Status: StatusRunning, IconText: "same"})), frameKeyOf(nodeView(&Node{Status: StatusDone, IconText: "same"})); ka != kb {
		t.Errorf("the same text on two colors has two frame keys: %+v and %+v", ka, kb)
	}
	out.takeCalls()
	if err := c.SetKey(Update{ID: "a", Status: StatusRunning, IconText: "same", Count: 7}); err != nil {
		t.Fatal(err)
	}
	calls, _ := out.takeCalls()
	for _, call := range calls {
		if strings.HasPrefix(call, "store ") {
			t.Fatalf("a count stored a frame: %q", calls)
		}
	}
}

// TestIconTextPersist is DoD-6: iconText survives a save and a load, and a file
// from before it loads.
func TestIconTextPersist(t *testing.T) {
	dir := t.TempDir()
	c := open(t, dir, newFakeOut())
	if err := c.SetKey(Update{ID: "t", Status: StatusDone, IconText: "parent\nname"}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetKey(Update{ID: "g", IconRef: "github.png"}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"iconText": "parent\nname"`) {
		t.Fatalf("the file has no iconText:\n%s", data)
	}
	c2 := open(t, dir, newFakeOut())
	if got := c2.Snapshot().Keys[0].IconText; got != "parent\nname" {
		t.Errorf("restored iconText %q", got)
	}

	// A file from before iconText has no such field.
	old := t.TempDir()
	legacy := `{"version":1,"active":null,"keys":[{"id":"g","icon":0,"status":"DONE","count":2,"iconRef":"github.png"}]}`
	if err := os.WriteFile(filepath.Join(old, FileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	c3 := open(t, old, newFakeOut())
	if k := c3.Snapshot().Keys; len(k) == 0 || k[0].IconRef != "github.png" || k[0].IconText != "" {
		t.Errorf("the old file loaded as %+v", k)
	}
}

// TestInboxTextIcon checks that the text `inbox` sends for a channel, with a
// count pip, draws and decodes for the board (task 0066, now by text).
func TestInboxTextIcon(t *testing.T) {
	data, err := renderIcon(assetsDir, "", "macro-pad\ndriver", "", false, 3, StatusColor(StatusWarn))
	if err != nil {
		t.Fatalf("renderIcon: %v", err)
	}
	pixels, err := transport.DecodePNGToRGB565(data)
	if err != nil {
		t.Fatalf("DecodePNGToRGB565: %v", err)
	}
	if want := transport.CustomGlyphWidth * transport.CustomGlyphHeight * 2; len(pixels) != want {
		t.Errorf("%d bytes of pixels, want %d", len(pixels), want)
	}
}
