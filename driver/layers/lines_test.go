package layers

import (
	"bytes"
	"image"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"golang.org/x/image/font/gofont/gobold"
	"golang.org/x/image/font/opentype"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// linesTile renders an id tile (task 0094) with a red count of count, and
// decodes it.
func linesTile(t *testing.T, lines, emoji []string, count int, key uint16) *image.NRGBA {
	t.Helper()
	data, err := renderIcon(assetsDir, "", "", joinEmoji(lines), joinEmoji(emoji), false, Badges{Red: byte(count)}, key)
	if err != nil {
		t.Fatalf("%q %q: %v", lines, emoji, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, image.Point{}, draw.Src)
	return out
}

// planOf returns the plan the tile of lines gets.
func planOf(t *testing.T, lines []string, withEmoji bool) linesPlan {
	t.Helper()
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	set := newFaceSet(f)
	t.Cleanup(set.close)
	plan, err := planLines(set, lines, withEmoji)
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

// darkIn counts the dark pixels of img in rows y0 to y1.
func darkIn(img *image.NRGBA, y0, y1 int) int {
	n := 0
	for y := y0; y < y1; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			if c := img.NRGBAAt(x, y); c.R < 0x60 && c.G < 0x60 && c.B < 0x60 {
				n++
			}
		}
	}
	return n
}

var idLines = []string{"PROJ-123", "#42"}

// TestIconLines is task 0094's DoD-1: two lines with no emoji draw an opaque
// tile whose strip is the status color, with both lines inked at 24 px.
func TestIconLines(t *testing.T) {
	img := linesTile(t, idLines, nil, 0, ColorRunning)
	checkGoldenNear(t, "icon-lines.png", onGray(img))

	want := rgb565ToNRGBA(ColorRunning)
	for y := 0; y < transport.CustomGlyphHeight; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			c := img.NRGBAAt(x, y)
			if c.A != 0xFF {
				t.Fatalf("(%d, %d) is %v, want opaque", x, y, c)
			}
			if y >= emojiStripY && c != want {
				t.Fatalf("the strip at (%d, %d) is %v, want %v", x, y, c, want)
			}
		}
	}

	plan := planOf(t, idLines, false)
	if plan.size != 24 {
		t.Errorf("the lines are %d px, want 24", plan.size)
	}
	for i, top := range plan.tops {
		if n := darkIn(img, top, top+lineHeight(plan.faces)); n < 40 {
			t.Errorf("line %d has only %d dark pixels", i, n)
		}
	}
	if n := darkIn(img, 0, plan.tops[0]-1) + darkIn(img, plan.tops[1]+lineHeight(plan.faces)+1, emojiStripY); n != 0 {
		t.Errorf("%d dark pixels outside the two lines", n)
	}

	// With emoji, a golden image for the owner to look at.
	pair := []string{emojiFox, emojiRocket}
	checkGoldenNear(t, "icon-lines-emoji.png", onGray(linesTile(t, idLines, pair, 0, ColorDone)))

	// A key in a child layer draws it.
	out := newFakeOut()
	c := open(t, "", out)
	for _, u := range []Update{
		{ID: "p", Status: StatusRunning, IconText: "repos"},
		{ID: "k", Parent: "p", Status: StatusRunning, IconLines: idLines},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	c.Press(0)
	sameImage(t, "the child", out.glyph(t, byte(childKeys[0])), img)
}

// TestIconLinesLayout is DoD-2: with one or two emoji the lines lie in y 2 to
// 56 and the pictures in y 57 to 105.
func TestIconLinesLayout(t *testing.T) {
	for name, emoji := range map[string][]string{
		"one": {emojiFox},
		"two": {emojiFox, emojiRocket},
	} {
		img := linesTile(t, idLines, emoji, 0, ColorNone)

		plan := planOf(t, idLines, true)
		if plan.tops[0] < 2 || plan.tops[1]+lineHeight(plan.faces) > 57 {
			t.Errorf("%s: the lines span y %d to %d", name, plan.tops[0], plan.tops[1]+lineHeight(plan.faces))
		}
		colored := func(y0, y1 int) (n int) {
			for y := y0; y < y1; y++ {
				for x := 0; x < transport.CustomGlyphWidth; x++ {
					if c := img.NRGBAAt(x, y); c.R != c.G || c.G != c.B {
						n++
					}
				}
			}
			return n
		}
		// Black text is gray on white, so a color is a picture.
		if n := colored(0, 57); n != 0 {
			t.Errorf("%s: %d colored pixels above y 57", name, n)
		}
		if n := colored(57, emojiStripY); n < 300 {
			t.Errorf("%s: only %d colored pixels in y 57 to 105", name, n)
		}
		if n := darkIn(img, 2, 57); n < 60 {
			t.Errorf("%s: only %d dark pixels in the lines band", name, n)
		}
		// The strip is the status color, whatever the sticker's outline does.
		for y := emojiStripY; y < transport.CustomGlyphHeight; y++ {
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				if c := img.NRGBAAt(x, y); c != rgb565ToNRGBA(ColorNone) {
					t.Fatalf("%s: the strip at (%d, %d) is %v", name, x, y, c)
				}
			}
		}
	}

	// The hero is 46 px alone and 44 px with a sticker.
	box := func(img *image.NRGBA) image.Rectangle {
		var r image.Rectangle
		for y := 57; y < emojiStripY; y++ {
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				if c := img.NRGBAAt(x, y); c.R < 0xF0 || c.G < 0xF0 || c.B < 0xF0 {
					r = r.Union(image.Rect(x, y, x+1, y+1))
				}
			}
		}
		return r
	}
	if r := box(linesTile(t, idLines, []string{emojiFox}, 0, ColorNone)); max(r.Dx(), r.Dy()) < 40 || max(r.Dx(), r.Dy()) > 46 {
		t.Errorf("the hero spans %v, want 40 to 46 px", r)
	}
}

// TestIconLinesFit is DoD-3: a line of 16 runes fits 120 px or ends in "…" at
// 14 px, no line is set below 14 px, and with no emoji the lines share a size.
func TestIconLinesFit(t *testing.T) {
	f, err := opentype.Parse(gobold.TTF)
	if err != nil {
		t.Fatal(err)
	}
	set := newFaceSet(f)
	defer set.close()

	wide := strings.Repeat("W", MaxIconCaptionRunes)
	thin := strings.Repeat("i", MaxIconCaptionRunes)
	for _, withEmoji := range []bool{false, true} {
		for _, lines := range [][]string{{wide}, {thin}, {thin, wide}, {"PROJ-1234", "#42"}, {"é" + wide[1:], "?"}} {
			plan, err := planLines(set, lines, withEmoji)
			if err != nil {
				t.Fatal(err)
			}
			if plan.size < 14 {
				t.Errorf("%q: size %d, want at least 14", lines, plan.size)
			}
			for i, s := range plan.texts {
				if w := textWidth(plan.faces, s); w > linesMaxW {
					t.Errorf("%q line %d is %d px wide", lines, i, w)
				}
				if strings.HasSuffix(s, "…") && plan.size != 14 {
					t.Errorf("%q line %d is cut at %d px", lines, i, plan.size)
				}
				if utf8.RuneCountInString(s) > MaxIconCaptionRunes {
					t.Errorf("%q line %d has %d runes", lines, i, utf8.RuneCountInString(s))
				}
			}
		}
	}
	if p := planOf(t, []string{thin, wide}, false); p.size != 14 || !strings.HasSuffix(p.texts[1], "…") || p.texts[0] != thin {
		t.Errorf("a narrow and a wide line: size %d, texts %q", p.size, p.texts)
	}
	if p := planOf(t, []string{"#42"}, false); p.size != 28 {
		t.Errorf("a short line is %d px, want 28", p.size)
	}
	if p := planOf(t, []string{"PROJ-1234", "#42"}, true); p.size != 20 {
		t.Errorf("PROJ-1234 over an emoji is %d px, want 20", p.size)
	}
	// A rune that Go Bold lacks draws as "?".
	if p := planOf(t, []string{"a☃b"}, false); p.texts[0] != "a?b" {
		t.Errorf("got %q", p.texts[0])
	}
}

// TestIconLinesInvalid is DoD-4: a bad iconLines is rejected and changes
// nothing.
func TestIconLinesInvalid(t *testing.T) {
	c := open(t, "", newFakeOut())
	for _, u := range []Update{
		{ID: "p", Status: StatusRunning, IconText: "repos"},
		{ID: "k", Parent: "p", Status: StatusDone, IconLines: []string{"keep"}},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	before := c.Snapshot()
	long := strings.Repeat("x", MaxIconCaptionRunes+1)
	for name, u := range map[string]Update{
		"three lines":         {ID: "k", Parent: "p", IconLines: []string{"a", "b", "c"}},
		"an empty line":       {ID: "k", Parent: "p", IconLines: []string{"a", ""}},
		"a blank line":        {ID: "k", Parent: "p", IconLines: []string{" "}},
		"17 runes":            {ID: "k", Parent: "p", IconLines: []string{long}},
		"a newline":           {ID: "k", Parent: "p", IconLines: []string{"a\nb"}},
		"a tab":               {ID: "k", Parent: "p", IconLines: []string{"a\tb"}},
		"invalid UTF-8":       {ID: "k", Parent: "p", IconLines: []string{"a\xffb"}},
		"with iconText":       {ID: "k", Parent: "p", IconLines: []string{"a"}, IconText: "b"},
		"with iconRef":        {ID: "k", Parent: "p", IconLines: []string{"a"}, IconRef: "github.png"},
		"a top-level key":     {ID: "top", IconLines: []string{"a"}},
		"a third emoji":       {ID: "k", Parent: "p", IconLines: []string{"a"}, IconEmoji: []string{emojiFox, emojiRocket, emojiFox}},
		"an emoji outside it": {ID: "k", Parent: "p", IconLines: []string{"a"}, IconEmoji: []string{"github.png"}},
	} {
		if err := c.SetKey(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if after := c.Snapshot(); !reflect.DeepEqual(after, before) {
			t.Errorf("%s: changed the tree to %+v", name, after)
		}
	}
	// The same lines on a top-level key that exists are rejected too.
	if err := c.SetKey(Update{ID: "p", Status: StatusRunning, IconLines: []string{"a"}}); err == nil {
		t.Error("a top-level key took iconLines")
	}
	if !reflect.DeepEqual(c.Snapshot(), before) {
		t.Error("the top-level key changed")
	}

	for name, lines := range map[string][]string{
		"none":       nil,
		"one":        {"a"},
		"two":        idLines,
		"16 runes":   {strings.Repeat("é", MaxIconCaptionRunes), "x"},
		"a space in": {"a b"},
	} {
		if err := ValidateIconLines(lines); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
	// iconText over emoji is still one line.
	if err := validateIconCaption("a\nb"); err == nil {
		t.Error("a newline in a caption was accepted")
	}
	// Painting a newline in a line is refused too, below the validation.
	if _, err := renderIcon(assetsDir, "", "", "a\nb\nc", "", false, Badges{}, ColorNone); err == nil {
		t.Error("three lines rendered")
	}
	if _, err := renderIcon(assetsDir, "", "", "a", "", true, Badges{}, ColorNone); err == nil {
		t.Error("lines rendered on a round")
	}
	if _, err := renderIcon(assetsDir, "", "x", "a", "", false, Badges{}, ColorNone); err == nil {
		t.Error("lines rendered with iconText")
	}
}

// TestIconLinesFrames is DoD-6: the same lines, emoji, and color share a slot,
// another line or color takes another, and a count stores no frame.
func TestIconLinesFrames(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	pair := []string{emojiFox, emojiRocket}
	for _, u := range []Update{
		{ID: "p", Status: StatusRunning},
		{ID: "a", Parent: "p", Status: StatusRunning, IconLines: idLines, IconEmoji: pair},
		{ID: "b", Parent: "p", Status: StatusRunning, IconLines: idLines, IconEmoji: pair, Count: 4},
		{ID: "c", Parent: "p", Status: StatusRunning, IconLines: []string{"PROJ-124", "#42"}, IconEmoji: pair},
		{ID: "d", Parent: "p", Status: StatusDone, IconLines: idLines, IconEmoji: pair},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	c.Press(0)
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	stored := func() int {
		out.mu.Lock()
		defer out.mu.Unlock()
		n := 0
		for slot, f := range out.slots {
			// The up key's shift icon is the only other frame with a picture.
			if f.png != nil && f.color != ColorUp {
				n++
				if f.color != ColorNone {
					t.Errorf("slot %d is stored over %#04x, want ColorNone", slot, f.color)
				}
			}
		}
		return n
	}
	if n := stored(); n != 3 {
		t.Fatalf("the board holds %d frames, want 3 (a and b share one)", n)
	}
	out.takeCalls()
	if err := c.SetKey(Update{ID: "a", Parent: "p", Status: StatusRunning, IconLines: idLines, IconEmoji: pair, Count: 9, CountYellow: 2}); err != nil {
		t.Fatal(err)
	}
	calls, _ := out.takeCalls()
	for _, call := range calls {
		if strings.HasPrefix(call, "store ") {
			t.Fatalf("a count stored a frame: %q", calls)
		}
	}
	if n := stored(); n != 3 {
		t.Errorf("the board holds %d frames after a count, want 3", n)
	}

	// With no emoji the color is in the strip, so it is part of the frame too.
	a := frameKeyOf(view{kind: kindKey, color: ColorDone, iconLines: "x\ny"})
	b := frameKeyOf(view{kind: kindKey, color: ColorError, iconLines: "x\ny"})
	if a == b || a.storeColor() != ColorNone || !a.hasGlyph() {
		t.Errorf("frames %+v and %+v", a, b)
	}
}

// TestIconLinesPersist is DoD-7: iconLines survives a save and a load, and a
// file from before it loads at version 1.
func TestIconLinesPersist(t *testing.T) {
	dir := t.TempDir()
	c := open(t, dir, newFakeOut())
	pair := []string{emojiFox}
	for _, u := range []Update{
		{ID: "p", Status: StatusRunning, IconText: "repos"},
		{ID: "k", Parent: "p", Status: StatusWarn, IconLines: idLines, IconEmoji: pair},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"iconLines"`) || !strings.Contains(string(data), `"version": 1`) {
		t.Fatalf("the file has no iconLines, or is not version 1:\n%s", data)
	}
	c2 := open(t, dir, newFakeOut())
	k := c2.Snapshot().Keys[0]
	if len(k.Children) != 1 || !reflect.DeepEqual(k.Children[0].IconLines, idLines) || !reflect.DeepEqual(k.Children[0].IconEmoji, pair) {
		t.Errorf("restored %+v", k)
	}

	old := t.TempDir()
	legacy := `{"version":1,"active":null,"keys":[{"id":"p","icon":0,"status":"DONE","count":0,"iconRef":"","iconText":"repos","children":[{"id":"k","icon":0,"status":"DONE","count":2,"iconRef":"","iconText":"x"}]}]}`
	if err := os.WriteFile(filepath.Join(old, FileName), []byte(legacy), 0o644); err != nil {
		t.Fatal(err)
	}
	c3 := open(t, old, newFakeOut())
	if k := c3.Snapshot().Keys[0]; k == nil || len(k.Children) != 1 || k.Children[0].IconLines != nil || k.Children[0].IconText != "x" {
		t.Errorf("the old file loaded as %+v", k)
	}
	// A file that gives a top-level key iconLines does not load as is.
	bad := t.TempDir()
	if err := os.WriteFile(filepath.Join(bad, FileName), []byte(`{"version":1,"active":null,"keys":[{"id":"p","icon":0,"status":"","count":0,"iconRef":"","iconLines":["a"]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var d Doc
	raw, _ := os.ReadFile(filepath.Join(bad, FileName))
	if err := d.UnmarshalJSON(raw); err == nil {
		d.padKeys()
		if err := d.Validate(); err == nil {
			t.Error("a top-level key with iconLines validated")
		}
	}
}
