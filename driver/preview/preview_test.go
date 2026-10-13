package preview

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// fakeCapturer serves a fixed window list and one solid color per window.
type fakeCapturer struct {
	windows  []Window
	colors   map[WindowID]color.RGBA
	fail     map[WindowID]error
	focused  WindowID
	captures []WindowID
}

func (f *fakeCapturer) Windows() ([]Window, error) { return append([]Window(nil), f.windows...), nil }

func (f *fakeCapturer) Focused() (WindowID, error) { return f.focused, nil }

func (f *fakeCapturer) Capture(id WindowID, maxW, maxH int) (image.Image, error) {
	f.captures = append(f.captures, id)
	if err := f.fail[id]; err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, maxW, maxH/2))
	c := f.colors[id]
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3] = c.R, c.G, c.B, 0xFF
	}
	return img, nil
}

func (f *fakeCapturer) setOnScreen(id WindowID, on bool) {
	for i := range f.windows {
		if f.windows[i].ID == id {
			f.windows[i].OnScreen = on
		}
	}
}

type push struct {
	key int
	img image.Image
}

type fakeSink struct{ pushes []push }

func (s *fakeSink) SetCustomGlyph(key int, data []byte) error {
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	s.pushes = append(s.pushes, push{key, img})
	return nil
}

var (
	red  = color.RGBA{R: 0xF0, A: 0xFF}
	blue = color.RGBA{B: 0xF0, A: 0xFF}
)

func newFixture() (*fakeCapturer, *fakeSink, *Previewer) {
	c := &fakeCapturer{
		windows: []Window{
			{ID: 10, App: "Safari", Title: "Docs", OnScreen: true},
			{ID: 20, App: "iTerm2", Title: "build", OnScreen: true},
		},
		colors: map[WindowID]color.RGBA{10: red, 20: blue},
		fail:   map[WindowID]error{},
	}
	s := &fakeSink{}
	p := New(c, s, []Target{
		{Key: 0, Selector: Selector{App: "Safari"}},
		{Key: 1, Selector: Selector{App: "iTerm2", Title: "build"}},
	}, Options{})
	return c, s, p
}

// center returns the middle pixel of img, inside the letterboxed picture.
func center(img image.Image) color.RGBA {
	r, g, b, a := img.At(Size/2, Size/2).RGBA()
	return color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
}

// TestLivePreviewPushed proves each on-screen window is captured and sent
// to its key as a PNG the daemon accepts.
func TestLivePreviewPushed(t *testing.T) {
	_, s, p := newFixture()
	p.Refresh()
	if len(s.pushes) != 2 {
		t.Fatalf("got %d pushes, want 2", len(s.pushes))
	}
	for i, want := range []color.RGBA{red, blue} {
		got := s.pushes[i]
		if got.key != i || center(got.img) != want {
			t.Errorf("push %d: key %d color %v, want key %d color %v", i, got.key, center(got.img), i, want)
		}
		var buf bytes.Buffer
		if err := png.Encode(&buf, got.img); err != nil {
			t.Fatal(err)
		}
		if _, err := transport.DecodePNGToRGB565(buf.Bytes()); err != nil {
			t.Errorf("push %d: daemon would reject it: %v", i, err)
		}
	}
}

// TestUnchangedNotResent proves a window whose picture did not change
// costs no push on the next refresh.
func TestUnchangedNotResent(t *testing.T) {
	c, s, p := newFixture()
	p.Refresh()
	p.Refresh()
	if len(s.pushes) != 2 {
		t.Fatalf("got %d pushes after two refreshes, want 2", len(s.pushes))
	}
	c.colors[10] = blue
	p.Refresh()
	if len(s.pushes) != 3 || s.pushes[2].key != 0 {
		t.Fatalf("got %+v, want one new push for key 0", s.pushes)
	}
}

// TestHiddenWindowShowsStaleSnapshot proves a window that goes off screen
// keeps its last snapshot, dimmed, sent once, and is not captured.
func TestHiddenWindowShowsStaleSnapshot(t *testing.T) {
	c, s, p := newFixture()
	p.Refresh()
	c.setOnScreen(10, false)
	c.colors[10] = color.RGBA{} // what a hidden window would capture as
	c.captures = nil
	p.Refresh()
	p.Refresh()

	for _, id := range c.captures {
		if id == 10 {
			t.Fatalf("captured hidden window 10")
		}
	}
	if len(s.pushes) != 3 {
		t.Fatalf("got %d pushes, want 3 (two live, one stale)", len(s.pushes))
	}
	last := s.pushes[2]
	want := color.RGBA{R: red.R / 2, A: 0xFF}
	if last.key != 0 || center(last.img) != want {
		t.Errorf("stale push: key %d color %v, want key 0 color %v", last.key, center(last.img), want)
	}
	r, g, b, _ := last.img.At(Size/2, Size-1).RGBA()
	if got := (color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), 0xFF}); got != staleBar {
		t.Errorf("bottom edge %v, want stale bar %v", got, staleBar)
	}
}

// TestWindowBackLive proves a window that comes back on screen is shown
// live again.
func TestWindowBackLive(t *testing.T) {
	c, s, p := newFixture()
	p.Refresh()
	c.setOnScreen(10, false)
	p.Refresh()
	c.setOnScreen(10, true)
	p.Refresh()
	last := s.pushes[len(s.pushes)-1]
	if last.key != 0 || center(last.img) != red {
		t.Errorf("got key %d color %v, want key 0 live red", last.key, center(last.img))
	}
}

// TestNeverSeenWindowSendsNothing proves a window that was off screen
// from the start leaves its key alone.
func TestNeverSeenWindowSendsNothing(t *testing.T) {
	c, s, p := newFixture()
	c.setOnScreen(10, false)
	p.Refresh()
	for _, ps := range s.pushes {
		if ps.key == 0 {
			t.Fatalf("pushed key 0 with no snapshot")
		}
	}
}

// TestCaptureErrorFallsBack proves a failed capture shows the last
// snapshot instead of nothing.
func TestCaptureErrorFallsBack(t *testing.T) {
	c, s, p := newFixture()
	p.Refresh()
	c.fail[10] = errors.New("boom")
	p.Refresh()
	if len(s.pushes) != 3 || center(s.pushes[2].img) != (color.RGBA{R: red.R / 2, A: 0xFF}) {
		t.Fatalf("got %+v, want a stale push for key 0", s.pushes)
	}
}

// TestBlankCaptureFallsBack proves an all-black capture, which a window
// in the middle of hiding can return, does not replace the snapshot.
func TestBlankCaptureFallsBack(t *testing.T) {
	c, s, p := newFixture()
	p.Refresh()
	c.colors[10] = color.RGBA{}
	p.Refresh()
	last := s.pushes[len(s.pushes)-1]
	if center(last.img) != (color.RGBA{R: red.R / 2, A: 0xFF}) {
		t.Errorf("got %v, want the stale red snapshot", center(last.img))
	}
}

// TestSnapshotOnLeave proves that when focus moves off a mapped window,
// that window is captured at once, before the next refresh.
func TestSnapshotOnLeave(t *testing.T) {
	c, s, p := newFixture()
	p.Refresh()
	c.focused = 10
	p.CheckFocus()
	c.colors[10] = blue // the page changed while it had focus
	c.focused = 20
	c.captures = nil
	p.CheckFocus()

	if len(c.captures) != 1 || c.captures[0] != 10 {
		t.Fatalf("captured %v on focus change, want [10]", c.captures)
	}
	last := s.pushes[len(s.pushes)-1]
	if last.key != 0 || center(last.img) != blue {
		t.Errorf("got key %d color %v, want key 0 blue", last.key, center(last.img))
	}

	// Minimized right after: the key keeps the leave snapshot.
	c.setOnScreen(10, false)
	p.Refresh()
	last = s.pushes[len(s.pushes)-1]
	if center(last.img) != (color.RGBA{B: blue.B / 2, A: 0xFF}) {
		t.Errorf("got %v, want the stale leave snapshot", center(last.img))
	}
}

// TestSnapshotWindow proves a switcher can take the leave snapshot itself.
func TestSnapshotWindow(t *testing.T) {
	c, _, p := newFixture()
	p.Refresh()
	c.captures = nil
	p.SnapshotWindow(20)
	if len(c.captures) != 1 || c.captures[0] != 20 {
		t.Fatalf("captured %v, want [20]", c.captures)
	}
}

// TestKeepsResolvedWindow proves a key stays on the window it first
// resolved to while that window matches, even when another window of the
// same app comes to the front.
func TestKeepsResolvedWindow(t *testing.T) {
	c, _, p := newFixture()
	p.Refresh()
	c.windows = append([]Window{{ID: 30, App: "Safari", OnScreen: true}}, c.windows...)
	c.colors[30] = blue
	c.captures = nil
	p.Refresh()
	for _, id := range c.captures {
		if id == 30 {
			t.Fatalf("key 0 moved to window 30")
		}
	}
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		want Target
		err  bool
	}{
		{"0=Safari", Target{0, Selector{App: "Safari"}}, false},
		{"1=iTerm2:build", Target{1, Selector{App: "iTerm2", Title: "build"}}, false},
		{"2=#4512", Target{2, Selector{ID: 4512}}, false},
		{"3 = Visual Studio Code : main.go", Target{3, Selector{App: "Visual Studio Code", Title: "main.go"}}, false},
		{"Safari", Target{}, true},
		{"x=Safari", Target{}, true},
		{"0=", Target{}, true},
		{"0=#abc", Target{}, true},
	}
	for _, tc := range cases {
		got, err := ParseTarget(tc.in)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("ParseTarget(%q) = %+v, %v; want %+v, err %v", tc.in, got, err, tc.want, tc.err)
		}
	}
}

// TestThumbnailLetterbox proves a wide window fills the key's width and
// is centered between black bars.
func TestThumbnailLetterbox(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 1600, 800))
	for i := 0; i < len(src.Pix); i += 4 {
		src.Pix[i], src.Pix[i+3] = 0xFF, 0xFF
	}
	th := Thumbnail(src)
	if th.Bounds() != image.Rect(0, 0, Size, Size) {
		t.Fatalf("bounds %v", th.Bounds())
	}
	if c := th.RGBAAt(0, Size/2); c.R != 0xFF {
		t.Errorf("middle row %v, want red", c)
	}
	if c := th.RGBAAt(Size/2, 0); c.R != 0 {
		t.Errorf("top row %v, want black bar", c)
	}
	if c := th.RGBAAt(Size/2, Size-1); c.R != 0 {
		t.Errorf("bottom row %v, want black bar", c)
	}
}
