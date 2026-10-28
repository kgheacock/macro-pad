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

	"github.com/kgheacock/macro-pad/driver/transport"
)

const (
	emojiFox    = "emoji/1f98a.png"
	emojiRocket = "emoji/1f680.png"
)

// emojiTile renders an emoji icon as a key in a child layer draws it, and
// decodes it.
func emojiTile(t *testing.T, ref, caption string, emoji []string, count int, key uint16) *image.NRGBA {
	t.Helper()
	return emojiIcon(t, ref, caption, emoji, false, count, key)
}

// emojiIcon renders an emoji icon, on the round when round is set, and
// decodes it.
func emojiIcon(t *testing.T, ref, caption string, emoji []string, round bool, count int, key uint16) *image.NRGBA {
	t.Helper()
	data, err := renderIcon(assetsDir, ref, caption, joinEmoji(emoji), round, count, key)
	if err != nil {
		t.Fatalf("%q %q %q: %v", ref, caption, emoji, err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	draw.Draw(out, out.Bounds(), img, image.Point{}, draw.Src)
	return out
}

// checkGoldenNear is checkGolden that lets each channel differ by 2: the font
// and the scaler round differently on amd64 and arm64.
func checkGoldenNear(t *testing.T, name string, img *image.NRGBA) {
	t.Helper()
	if os.Getenv("UPDATE_GOLDEN") != "" {
		checkGolden(t, name, img)
		return
	}
	f, err := os.Open(filepath.Join("testdata", name))
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
	near := func(a, b uint8) bool { return a-b <= 2 || b-a <= 2 }
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y++ {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x++ {
			w := color.NRGBAModel.Convert(want.At(x, y)).(color.NRGBA)
			g := img.NRGBAAt(x, y)
			if !near(w.R, g.R) || !near(w.G, g.G) || !near(w.B, g.B) || !near(w.A, g.A) {
				t.Fatalf("%s differs at (%d, %d): file %v, render %v (UPDATE_GOLDEN=1 rewrites it)", name, x, y, w, g)
			}
		}
	}
}

// TestIconEmoji is task 0073's DoD-4 and task 0082's DoD-1, DoD-2, and DoD-6:
// an emoji iconRef keeps the round, and iconEmoji draws the hero, the sticker,
// the caption, and the status strip.
func TestIconEmoji(t *testing.T) {
	checkGoldenNear(t, "icon-emoji.png", onGray(emojiTile(t, emojiFox, "", nil, 0, ColorRunning)))

	caption := emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 0, ColorRunning)
	checkGoldenNear(t, "icon-emoji-caption.png", onGray(caption))
	checkGoldenNear(t, "icon-emoji-caption-pip.png", onGray(emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 2, ColorRunning)))
	checkGoldenNear(t, "icon-emoji-alone.png", onGray(emojiTile(t, "", "", []string{emojiFox, emojiRocket}, 0, ColorDone)))

	// The caption is dark, across the top.
	dark := func(x0, y0, x1, y1 int) int {
		n := 0
		for y := y0; y < y1; y++ {
			for x := x0; x < x1; x++ {
				if c := caption.NRGBAAt(x, y); c.A == 0xFF && c.R < 0x60 && c.G < 0x60 && c.B < 0x60 {
					n++
				}
			}
		}
		return n
	}
	if n := dark(4, 2, 124, 26); n < 80 {
		t.Errorf("only %d dark pixels in the caption band", n)
	}

	// One emoji and no caption: a large emoji in the middle.
	one := emojiTile(t, "", "", []string{emojiFox}, 0, ColorDone)
	if c := one.NRGBAAt(64, 64); c.A != 0xFF || (c.R > 0xF0 && c.G > 0xF0 && c.B > 0xF0) {
		t.Errorf("the centre of one emoji is %v, want the fox", c)
	}

	// A key in a child layer shows the tile.
	out := newFakeOut()
	c := open(t, "", out)
	pair := []string{emojiFox, emojiRocket}
	for _, u := range []Update{
		{ID: "p", Status: StatusRunning, IconText: "macro-pad", IconEmoji: pair},
		{ID: "s", Parent: "p", Status: StatusRunning, IconText: "macro-pad", IconEmoji: pair},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	c.Press(0)
	sameImage(t, "the child", out.glyph(t, byte(childKeys[0])), caption)
}

// TestIconEmojiTopLevel is task 0082's DoD-8: a top-level key keeps task
// 0073's round, with its emoji side by side under the caption, and the key
// color around it.
func TestIconEmojiTopLevel(t *testing.T) {
	pair := []string{emojiFox, emojiRocket}
	round := emojiIcon(t, "", "macro-pad", pair, true, 0, ColorRunning)
	checkGoldenNear(t, "icon-emoji-round-caption.png", onGray(round))
	checkGoldenNear(t, "icon-emoji-round-caption-pip.png", onGray(emojiIcon(t, "", "macro-pad", pair, true, 2, ColorRunning)))
	if got := round.NRGBAAt(0, 0); got.A != 0 {
		t.Errorf("the corner is %v, want transparent, so the key color shows", got)
	}

	out := newFakeOut()
	c := open(t, "", out)
	if err := c.SetKey(Update{ID: "p", Status: StatusRunning, IconText: "macro-pad", IconEmoji: pair}); err != nil {
		t.Fatal(err)
	}
	sameImage(t, "the top-level key", out.glyph(t, 0), round)

	// The same key keeps the round once it has children, and its child gets
	// the tile.
	if err := c.SetKey(Update{ID: "s", Parent: "p", Status: StatusRunning, IconText: "macro-pad", IconEmoji: pair}); err != nil {
		t.Fatal(err)
	}
	sameImage(t, "the parent", out.glyph(t, 0), round)
}

// sameImage fails unless got has the pixels of want.
func sameImage(t *testing.T, name string, got image.Image, want *image.NRGBA) {
	t.Helper()
	for y := 0; y < transport.CustomGlyphHeight; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			if g := color.NRGBAModel.Convert(got.At(x, y)); g != color.NRGBA(want.NRGBAAt(x, y)) {
				t.Fatalf("%s differs at (%d, %d): %v, want %v", name, x, y, g, want.NRGBAAt(x, y))
			}
		}
	}
}

// TestIconEmojiInvalid is DoD-13: a bad iconEmoji is rejected and changes
// nothing.
func TestIconEmojiInvalid(t *testing.T) {
	c := open(t, "", newFakeOut())
	keep := Update{ID: "k", IconText: "keep", IconEmoji: []string{emojiFox}}
	if err := c.SetKey(keep); err != nil {
		t.Fatal(err)
	}
	before := c.Snapshot()
	for name, u := range map[string]Update{
		"three refs":              {ID: "k", IconEmoji: []string{emojiFox, emojiFox, emojiFox}},
		"a ref outside emoji/":    {ID: "k", IconEmoji: []string{"github.png"}},
		"a ref leaving assets/":   {ID: "k", IconEmoji: []string{"emoji/../../x.png"}},
		"the folder alone":        {ID: "k", IconEmoji: []string{"emoji/"}},
		"iconEmoji and iconRef":   {ID: "k", IconRef: "github.png", IconEmoji: []string{emojiFox}},
		"a caption of two lines":  {ID: "k", IconText: "a\nb", IconEmoji: []string{emojiFox}},
		"a caption of 17 runes":   {ID: "k", IconText: strings.Repeat("é", MaxIconCaptionRunes+1), IconEmoji: []string{emojiFox}},
		"a control character":     {ID: "k", IconText: "a\tb", IconEmoji: []string{emojiFox}},
		"a line break in the ref": {ID: "k", IconEmoji: []string{"emoji/a\nb.png"}},
	} {
		if err := c.SetKey(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if after := c.Snapshot(); after.Keys[0] == nil || !reflect.DeepEqual(after.Keys[0], before.Keys[0]) {
			t.Errorf("%s: changed key 0 to %+v", name, after.Keys[0])
		}
	}
	if err := ValidateIconEmoji([]string{emojiFox, emojiRocket}); err != nil {
		t.Error(err)
	}
	if err := validateIconCaption(strings.Repeat("é", MaxIconCaptionRunes)); err != nil {
		t.Error(err)
	}
}

// TestIconEmojiFrames is task 0073's DoD-14 and task 0082's DoD-4: the strip
// shows the key color, so one icon on two colors is two frames, each stored
// over ColorNone, and a count stores no frame.
func TestIconEmojiFrames(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	pair := []string{emojiFox, emojiRocket}
	for _, u := range []Update{
		{ID: "p", Status: StatusRunning},
		{ID: "a", Parent: "p", Status: StatusRunning, IconText: "same", IconEmoji: pair},
		{ID: "b", Parent: "p", Status: StatusDone, IconText: "same", IconEmoji: pair},
		{ID: "c", Parent: "p", Status: StatusDone, IconText: "same", IconEmoji: pair},
	} {
		if err := c.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	c.Press(0)
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	out.mu.Lock()
	stored := 0
	for slot, f := range out.slots {
		// The up key's shift icon is the only other frame with a picture.
		if f.png != nil && f.color != ColorUp {
			stored++
			if f.color != ColorNone {
				t.Errorf("slot %d is stored over %#04x, want ColorNone", slot, f.color)
			}
		}
	}
	out.mu.Unlock()
	if stored != 2 {
		t.Fatalf("the board holds %d frames, want 2 (one for each color)", stored)
	}
	out.takeCalls()
	if err := c.SetKey(Update{ID: "a", Parent: "p", Status: StatusRunning, IconText: "same", IconEmoji: pair, Count: 7}); err != nil {
		t.Fatal(err)
	}
	calls, _ := out.takeCalls()
	for _, call := range calls {
		if strings.HasPrefix(call, "store ") {
			t.Fatalf("a count stored a frame: %q", calls)
		}
	}
}

// TestIconEmojiPersist is DoD-14: iconEmoji survives a save and a load.
func TestIconEmojiPersist(t *testing.T) {
	dir := t.TempDir()
	c := open(t, dir, newFakeOut())
	pair := []string{emojiFox, emojiRocket}
	if err := c.SetKey(Update{ID: "s", Status: StatusWarn, IconText: "macro-pad", IconEmoji: pair}); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"iconEmoji"`) {
		t.Fatalf("the file has no iconEmoji:\n%s", data)
	}
	c2 := open(t, dir, newFakeOut())
	if k := c2.Snapshot().Keys[0]; !reflect.DeepEqual(k.IconEmoji, pair) || k.IconText != "macro-pad" {
		t.Errorf("restored %+v", k)
	}
}

// TestIconEmojiLayout is task 0082's DoD-3: the tile is opaque, the strip is
// the status color, and the hero is far bigger than task 0073's 34 px of art.
func TestIconEmojiLayout(t *testing.T) {
	for _, status := range []uint16{ColorRunning, ColorDone, ColorError, ColorWarn, ColorNone} {
		img := emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 0, status)
		want := rgb565ToNRGBA(status)
		for y := 0; y < transport.CustomGlyphHeight; y++ {
			for x := 0; x < transport.CustomGlyphWidth; x++ {
				c := img.NRGBAAt(x, y)
				if c.A != 0xFF {
					t.Fatalf("%#04x: (%d, %d) is %v, want opaque", status, x, y, c)
				}
				if y >= emojiStripY && c != want {
					t.Fatalf("%#04x: the strip at (%d, %d) is %v, want %v", status, x, y, c, want)
				}
			}
		}
	}

	// The hero alone, so the sticker does not widen the box.
	hero := emojiTile(t, "", "macro-pad", []string{emojiFox}, 0, ColorNone)
	box := image.Rectangle{}
	for y := 26; y < emojiStripY; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			if c := hero.NRGBAAt(x, y); c.R < 0xF0 || c.G < 0xF0 || c.B < 0xF0 {
				box = box.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	if max(box.Dx(), box.Dy()) < 76 {
		t.Errorf("the hero spans %v, want at least 76 px", box)
	}

	// The sticker has a white outline where it covers the hero: the fox's
	// orange is gone just outside the rocket.
	pair := emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 0, ColorNone)
	white := 0
	for y := 60; y < 112; y++ {
		for x := 0; x < 50; x++ {
			if c := pair.NRGBAAt(x, y); c.R == 0xFF && c.G == 0xFF && c.B == 0xFF && hero.NRGBAAt(x, y) != c {
				white++
			}
		}
	}
	if white < 40 {
		t.Errorf("only %d pixels of outline cover the hero", white)
	}
}
