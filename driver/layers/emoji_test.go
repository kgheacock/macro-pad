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

// emojiTile renders an emoji icon and decodes it.
func emojiTile(t *testing.T, ref, caption string, emoji []string, count int, key uint16) *image.NRGBA {
	t.Helper()
	data, err := renderIcon(assetsDir, ref, caption, joinEmoji(emoji), count, key)
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

// TestIconEmoji is task 0073's DoD-4 and DoD-12: an emoji is an iconRef like
// any other icon, and iconEmoji draws two emoji side by side under a caption.
func TestIconEmoji(t *testing.T) {
	checkGoldenNear(t, "icon-emoji.png", onGray(emojiTile(t, emojiFox, "", nil, 0, ColorRunning)))

	caption := emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 0, ColorRunning)
	checkGoldenNear(t, "icon-emoji-caption.png", onGray(caption))
	checkGoldenNear(t, "icon-emoji-caption-pip.png", onGray(emojiTile(t, "", "macro-pad", []string{emojiFox, emojiRocket}, 2, ColorRunning)))

	// The corner shows the key color, the caption is dark, and each emoji sits
	// on its own side.
	if got := caption.NRGBAAt(0, 0); got.A != 0 {
		t.Errorf("the corner is %v, want transparent", got)
	}
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
	if n := dark(22, 20, 106, 40); n < 40 {
		t.Errorf("only %d dark pixels in the caption band", n)
	}
	painted := func(x0, x1 int) int {
		n := 0
		for y := 50; y < 98; y++ {
			for x := x0; x < x1; x++ {
				if c := caption.NRGBAAt(x, y); c.A == 0xFF && (c.R < 0xF0 || c.G < 0xF0 || c.B < 0xF0) {
					n++
				}
			}
		}
		return n
	}
	if l, r := painted(14, 62), painted(66, 114); l < 300 || r < 300 {
		t.Errorf("painted pixels left %d, right %d: want an emoji on each side", l, r)
	}

	// One emoji and no caption: a larger emoji in the middle.
	one := emojiTile(t, "", "", []string{emojiFox}, 0, ColorDone)
	if c := one.NRGBAAt(64, 64); c.A != 0xFF || (c.R > 0xF0 && c.G > 0xF0 && c.B > 0xF0) {
		t.Errorf("the centre of one emoji is %v, want the fox", c)
	}

	// A key shows it.
	out := newFakeOut()
	c := open(t, "", out)
	if err := c.SetKey(Update{ID: "s", Status: StatusRunning, IconText: "macro-pad", IconEmoji: []string{emojiFox, emojiRocket}}); err != nil {
		t.Fatal(err)
	}
	got := out.glyph(t, 0)
	for y := 0; y < transport.CustomGlyphHeight; y++ {
		for x := 0; x < transport.CustomGlyphWidth; x++ {
			if g := color.NRGBAModel.Convert(got.At(x, y)); g != color.NRGBA(caption.NRGBAAt(x, y)) {
				t.Fatalf("key 0 differs from the tile at (%d, %d): %v, want %v", x, y, g, caption.NRGBAAt(x, y))
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

// TestIconEmojiFrames is DoD-14: the round shows the key color, so one icon on
// two colors is two frames, and a count stores no frame.
func TestIconEmojiFrames(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	pair := []string{emojiFox, emojiRocket}
	for _, u := range []Update{
		{ID: "a", Status: StatusRunning, IconText: "same", IconEmoji: pair},
		{ID: "b", Status: StatusDone, IconText: "same", IconEmoji: pair},
		{ID: "c", Status: StatusDone, IconText: "same", IconEmoji: pair},
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
		if f.png != nil {
			stored++
		}
	}
	out.mu.Unlock()
	if stored != 2 {
		t.Fatalf("the board holds %d frames, want 2 (one for each color)", stored)
	}
	out.takeCalls()
	if err := c.SetKey(Update{ID: "a", Status: StatusRunning, IconText: "same", IconEmoji: pair, Count: 7}); err != nil {
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
