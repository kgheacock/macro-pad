package layers

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

const assetsDir = "../../assets"

// fakeOut records what a Controller sends.
type fakeOut struct {
	mu      sync.Mutex
	states  []transport.KeyState
	glyphs  map[byte][]byte
	changes []*string
}

func newFakeOut() *fakeOut { return &fakeOut{glyphs: make(map[byte][]byte)} }

func (f *fakeOut) SetKeyState(ks transport.KeyState) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states = append(f.states, ks)
	return nil
}

func (f *fakeOut) SetCustomGlyph(key byte, png []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.glyphs[key] = png
	return nil
}

func (f *fakeOut) LayerChanged(active *string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.changes = append(f.changes, active)
}

// take returns the key states sent since the last take, by key index, and
// clears them.
func (f *fakeOut) take() map[byte]transport.KeyState {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[byte]transport.KeyState)
	for _, ks := range f.states {
		out[ks.KeyIndex] = ks
	}
	f.states = nil
	return out
}

func (f *fakeOut) glyph(t *testing.T, key byte) image.Image {
	t.Helper()
	f.mu.Lock()
	data, ok := f.glyphs[key]
	f.mu.Unlock()
	if !ok {
		t.Fatalf("no custom glyph sent to key %d", key)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("key %d glyph: %v", key, err)
	}
	return img
}

func open(t *testing.T, dir string, out Out) *Controller {
	t.Helper()
	c, err := Open(Options{Dir: dir, AssetsDir: assetsDir, Out: out, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// sixKeys fills a controller with six keys; key "github" has two children.
func sixKeys(t *testing.T, c *Controller) {
	t.Helper()
	ups := []Update{
		{ID: "github", Status: StatusRunning, Count: 3, IconRef: "github.png"},
		{ID: "dev", Status: StatusDone, IconRef: "dev.png"},
		{ID: "ai", Status: StatusError, IconRef: "ai.png"},
		{ID: "gmail", Status: StatusWarn, IconRef: "gmail.png"},
		{ID: "live", IconRef: "live.png"},
		{ID: "create", Status: StatusRunning, IconRef: "create.png"},
		{ID: "build", Parent: "github", Status: StatusError, Count: 1},
		{ID: "test", Parent: "github", Status: StatusDone},
	}
	for _, u := range ups {
		if err := c.SetKey(u); err != nil {
			t.Fatalf("SetKey %+v: %v", u, err)
		}
	}
}

func TestDocValidation(t *testing.T) {
	key := func(id string) string { return `{"id":"` + id + `"}` }
	keys := func(n int) string {
		var ks []string
		for i := 0; i < n; i++ {
			ks = append(ks, key(string(rune('a'+i))))
		}
		return strings.Join(ks, ",")
	}
	children := func(n int) string {
		var cs []string
		for i := 0; i < n; i++ {
			cs = append(cs, key(string(rune('a'+i))))
		}
		return strings.Join(cs, ",")
	}
	cases := []struct {
		name string
		json string
		ok   bool
	}{
		{"empty", `{"version":1,"active":null,"keys":[]}`, true},
		{"six keys", `{"version":1,"active":null,"keys":[` + keys(6) + `]}`, true},
		{"seven keys", `{"version":1,"active":null,"keys":[` + keys(7) + `]}`, false},
		{"null keys", `{"version":1,"active":null,"keys":[null,` + key("a") + `,null]}`, true},
		{"five children", `{"version":1,"active":null,"keys":[{"id":"p","children":[` + children(5) + `]}]}`, true},
		{"six children", `{"version":1,"active":null,"keys":[{"id":"p","children":[` + children(6) + `]}]}`, false},
		{"unknown status", `{"version":1,"active":null,"keys":[{"id":"p","status":"BROKEN"}]}`, false},
		{"known statuses", `{"version":1,"active":null,"keys":[{"id":"a","status":"RUNNING"},{"id":"b","status":"DONE"},{"id":"c","status":"ERROR"},{"id":"d","status":"WARN"}]}`, true},
		{"unknown child status", `{"version":1,"active":null,"keys":[{"id":"p","children":[{"id":"c","status":"x"}]}]}`, false},
		{"grandchild", `{"version":1,"active":null,"keys":[{"id":"p","children":[{"id":"c","children":[{"id":"g"}]}]}]}`, false},
		{"duplicate key id", `{"version":1,"active":null,"keys":[` + key("a") + `,` + key("a") + `]}`, false},
		{"empty id", `{"version":1,"active":null,"keys":[{"id":""}]}`, false},
		{"negative count", `{"version":1,"active":null,"keys":[{"id":"a","count":-1}]}`, false},
		{"wrong version", `{"version":2,"active":null,"keys":[]}`, false},
		{"iconRef leaves assets", `{"version":1,"active":null,"keys":[{"id":"a","iconRef":"../secret.png"}]}`, false},
		{"iconRef absolute", `{"version":1,"active":null,"keys":[{"id":"a","iconRef":"/etc/passwd"}]}`, false},
		{"iconRef in subdirectory", `{"version":1,"active":null,"keys":[{"id":"a","iconRef":"icons/a.png"}]}`, true},
		{"active names a key with children", `{"version":1,"active":"p","keys":[{"id":"p","children":[{"id":"c"}]}]}`, true},
		{"active names a key without children", `{"version":1,"active":"p","keys":[{"id":"p"}]}`, false},
		{"active names no key", `{"version":1,"active":"zz","keys":[]}`, false},
		{"not json", `{`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.json))
			if tc.ok && err != nil {
				t.Errorf("rejected a valid file: %v", err)
			}
			if !tc.ok && err == nil {
				t.Error("accepted an invalid file")
			}
		})
	}
}

func TestRenderTopLayer(t *testing.T) {
	// Fill a controller, save it, and open a new one on the file. Opening
	// draws every key once.
	dir := t.TempDir()
	c := open(t, dir, newFakeOut())
	sixKeys(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	out2 := newFakeOut()
	open(t, dir, out2)
	states := out2.take()
	if len(states) != KeyCount {
		t.Fatalf("sent %d key states, want %d", len(states), KeyCount)
	}
	wantColor := []uint16{ColorRunning, ColorDone, ColorError, ColorWarn, ColorNone, ColorRunning}
	for i, want := range wantColor {
		ks := states[byte(i)]
		if ks.Color != want {
			t.Errorf("key %d color %#04x, want %#04x", i, ks.Color, want)
		}
		if ks.EmojiID != transport.CustomGlyphSentinelEmojiID {
			t.Errorf("key %d emoji %#02x, want the custom glyph sentinel", i, ks.EmojiID)
		}
		// The image decodes into the wire format.
		if _, err := transport.DecodePNGToRGB565(pngBytes(t, out2.glyph(t, byte(i)))); err != nil {
			t.Errorf("key %d glyph: %v", i, err)
		}
	}

	// Key 0 has count 3: its badge is opaque in the bottom-right corner. Key
	// 1 has none: that corner is transparent.
	if _, _, _, a := out2.glyph(t, 0).At(120, 120).RGBA(); a == 0 {
		t.Error("key 0 has no count badge in its corner")
	}
	if _, _, _, a := out2.glyph(t, 1).At(120, 120).RGBA(); a != 0 {
		t.Error("key 1 has a badge but a count of 0")
	}
}

func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestEnterChildLayer(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	sixKeys(t, c)
	out.take()

	c.Press(0)

	if a := c.Active(); a == nil || *a != "github" {
		t.Fatalf("active %v, want github", a)
	}
	states := out.take()
	// Two children show on keys 0 and 1, the shift icon on key 3, and the
	// other keys, which showed top-level keys, go blank.
	if got := states[0]; got.Color != ColorError {
		t.Errorf("key 0 color %#04x, want ERROR %#04x", got.Color, ColorError)
	}
	if got := states[1]; got.Color != ColorDone {
		t.Errorf("key 1 color %#04x, want DONE %#04x", got.Color, ColorDone)
	}
	if got := states[UpKey]; got.Color != ColorUp || got.EmojiID != transport.CustomGlyphSentinelEmojiID {
		t.Errorf("key %d is %+v, want the shift icon on %#04x", UpKey, got, ColorUp)
	}
	for _, k := range []byte{2, 4, 5} {
		if got, ok := states[k]; !ok || got.Color != 0 || got.EmojiID != 0 {
			t.Errorf("key %d is %+v (sent %v), want blank", k, got, ok)
		}
	}
	if _, _, _, a := out.glyph(t, UpKey).At(64, 20).RGBA(); a == 0 {
		t.Error("shift glyph has no arrow at its tip")
	}
	if len(out.changes) != 1 || out.changes[0] == nil || *out.changes[0] != "github" {
		t.Errorf("layerChanged calls %v, want one for github", out.changes)
	}
}

func TestUpReturnsToParent(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	sixKeys(t, c)
	c.Press(0)
	out.take()

	c.Press(UpKey)

	if a := c.Active(); a != nil {
		t.Fatalf("active %q, want the top layer", *a)
	}
	states := out.take()
	if len(states) != KeyCount {
		t.Errorf("sent %d keys, want all %d", len(states), KeyCount)
	}
	if states[0].Color != ColorRunning || states[UpKey].Color != ColorWarn {
		t.Errorf("top layer not restored: key 0 %+v, key %d %+v", states[0], UpKey, states[UpKey])
	}
	if last := out.changes[len(out.changes)-1]; last != nil {
		t.Errorf("last layerChanged %q, want null", *last)
	}
}

func TestPressWithoutChildren(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	sixKeys(t, c)
	out.take()
	changes := len(out.changes)

	c.Press(1) // dev has no children
	c.Press(5)
	c.Press(9) // not a key

	if a := c.Active(); a != nil {
		t.Errorf("active %q, want the top layer", *a)
	}
	if got := out.take(); len(got) != 0 {
		t.Errorf("sent %d key states for presses on keys without children", len(got))
	}
	if len(out.changes) != changes {
		t.Error("announced a layer change")
	}

	// In a child layer, a press on any key but the up key does nothing.
	c.Press(0)
	out.take()
	c.Press(0)
	c.Press(2)
	if got := out.take(); len(got) != 0 {
		t.Errorf("sent %d key states for a press on a child", len(got))
	}
	if a := c.Active(); a == nil || *a != "github" {
		t.Errorf("active %v, want github", a)
	}
}

func TestOnlyChangedKeysAreSent(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	sixKeys(t, c)
	out.take()

	if err := c.SetKey(Update{ID: "dev", Status: StatusError, IconRef: "dev.png"}); err != nil {
		t.Fatal(err)
	}
	states := out.take()
	if len(states) != 1 || states[1].Color != ColorError {
		t.Errorf("sent %+v, want key 1 alone, red", states)
	}

	// The same values again send nothing.
	if err := c.SetKey(Update{ID: "dev", Status: StatusError, IconRef: "dev.png"}); err != nil {
		t.Fatal(err)
	}
	if got := out.take(); len(got) != 0 {
		t.Errorf("sent %+v for an update that changed nothing", got)
	}
}

func TestReservedSlotIsNotDrawn(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	two := 2
	if err := c.SetKey(Update{ID: "host", Slot: &two}); err != nil {
		t.Fatal(err)
	}
	if got := out.take(); len(got) != 0 {
		t.Errorf("a reserved slot drew %+v", got)
	}

	// A streamer without a slot skips it.
	for _, id := range []string{"a", "b", "c"} {
		if err := c.SetKey(Update{ID: id, Status: StatusDone}); err != nil {
			t.Fatal(err)
		}
	}
	keys := c.Snapshot().Keys
	if keys[0].ID != "a" || keys[1].ID != "b" || keys[2].ID != "host" || keys[3].ID != "c" {
		t.Errorf("keys are %s, want a b host c", ids(keys))
	}
}

func ids(keys []*Node) string {
	var s []string
	for _, k := range keys {
		if k == nil {
			s = append(s, "-")
		} else {
			s = append(s, k.ID)
		}
	}
	return strings.Join(s, " ")
}

func TestSlotTaken(t *testing.T) {
	c := open(t, "", newFakeOut())
	one := 1
	if err := c.SetKey(Update{ID: "host", Slot: &one}); err != nil {
		t.Fatal(err)
	}
	err := c.SetKey(Update{ID: "other", Slot: &one, Status: StatusDone})
	if !errors.Is(err, ErrSlotTaken) {
		t.Fatalf("got %v, want ErrSlotTaken", err)
	}
	if k := c.Snapshot().Keys[1]; k.ID != "host" {
		t.Errorf("slot 1 holds %q after a refused update, want host", k.ID)
	}

	// The same id may update its own slot, and move to a free one.
	if err := c.SetKey(Update{ID: "host", Slot: &one, Status: StatusWarn}); err != nil {
		t.Errorf("update of own slot: %v", err)
	}
	four := 4
	if err := c.SetKey(Update{ID: "host", Slot: &four, Status: StatusWarn}); err != nil {
		t.Errorf("move to a free slot: %v", err)
	}
	if got := ids(c.Snapshot().Keys); got != "- - - - host -" {
		t.Errorf("keys are %s", got)
	}

	bad := 6
	if err := c.SetKey(Update{ID: "x", Slot: &bad}); err == nil {
		t.Error("slot 6 accepted")
	}
}

func TestSetKeyRejectsBadUpdates(t *testing.T) {
	c := open(t, "", newFakeOut())
	sixKeys(t, c)
	for name, u := range map[string]Update{
		"unknown status":       {ID: "dev", Status: "BROKEN"},
		"iconRef leaves":       {ID: "dev", IconRef: "../../etc/passwd"},
		"child of nobody":      {ID: "c", Parent: "nobody"},
		"empty id":             {},
		"seventh key":          {ID: "seventh"},
		"sixth child":          {ID: "c6", Parent: "dev"},
		"child with a slot":    {ID: "x", Parent: "github", Slot: new(int)},
		"negative count":       {ID: "dev", Count: -2},
		"child duplicate list": {ID: "build", Parent: "github", Status: "nope"},
	} {
		before := c.Snapshot()
		if name == "sixth child" {
			for i := 0; i < MaxChildren; i++ {
				c.SetKey(Update{ID: string(rune('a' + i)), Parent: "dev"})
			}
			before = c.Snapshot()
		}
		if err := c.SetKey(u); err == nil {
			t.Errorf("%s: accepted", name)
		}
		if after := c.Snapshot(); len(after.Keys) != len(before.Keys) || ids(after.Keys) != ids(before.Keys) {
			t.Errorf("%s: changed the object", name)
		}
	}
}

func TestPersistAndRestore(t *testing.T) {
	dir := t.TempDir()
	out := newFakeOut()
	c, err := Open(Options{Dir: dir, AssetsDir: assetsDir, Out: out, Logf: t.Logf, SaveInterval: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	sixKeys(t, c)
	c.Press(0)

	// A dirty object reaches the disk within a few save intervals.
	path := filepath.Join(dir, FileName)
	deadline := time.Now().Add(2 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && strings.Contains(string(data), `"active": "github"`) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("layers.json did not hold the active layer within 2 s")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}

	// A restart shows the same layer.
	out2 := newFakeOut()
	c2 := open(t, dir, out2)
	if a := c2.Active(); a == nil || *a != "github" {
		t.Fatalf("restored active %v, want github", a)
	}
	states := out2.take()
	if states[0].Color != ColorError || states[UpKey].Color != ColorUp {
		t.Errorf("restart did not draw the child layer: key 0 %+v, key %d %+v", states[0], UpKey, states[UpKey])
	}
}

func TestCloseSavesDirtyObject(t *testing.T) {
	dir := t.TempDir()
	c := open(t, dir, newFakeOut()) // the 5 s save never runs in this test
	sixKeys(t, c)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Errorf("saved file does not parse: %v", err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Errorf("state dir holds %d files, want only layers.json", len(entries))
	}
}

func TestCorruptFileMovedAside(t *testing.T) {
	for name, content := range map[string]string{
		"not json":       `{"version": 1, "keys": [`,
		"seven keys":     `{"version":1,"active":null,"keys":[null,null,null,null,null,null,null]}`,
		"unknown status": `{"version":1,"active":null,"keys":[{"id":"a","status":"BAD"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, FileName)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			out := newFakeOut()
			c := open(t, dir, out)

			bad, err := os.ReadFile(path + ".bad")
			if err != nil || string(bad) != content {
				t.Errorf("layers.json.bad not the old file: %q, %v", bad, err)
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("layers.json still there: %v", err)
			}
			if c.Active() != nil || len(out.take()) != 0 {
				t.Error("daemon did not start empty")
			}
			// It still works.
			if err := c.SetKey(Update{ID: "a", Status: StatusDone}); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestIconRefCannotLeaveAssets(t *testing.T) {
	dir := t.TempDir()
	assets := filepath.Join(dir, "assets")
	if err := os.Mkdir(assets, 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.png")
	data, err := os.ReadFile(filepath.Join(assetsDir, "github.png"))
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(secret, data, 0o644)
	if err := os.Symlink(secret, filepath.Join(assets, "link.png")); err != nil {
		t.Skip("no symlinks:", err)
	}
	os.WriteFile(filepath.Join(assets, "ok.png"), data, 0o644)

	if _, err := renderGlyph(assets, "ok.png", 0); err != nil {
		t.Errorf("a file under assets: %v", err)
	}
	for _, ref := range []string{"../secret.png", "link.png", "/etc/passwd"} {
		if _, err := renderGlyph(assets, ref, 0); err == nil {
			t.Errorf("iconRef %q rendered", ref)
		}
	}
}

func TestBadgeText(t *testing.T) {
	for count, want := range map[int]string{1: "1", 42: "42", 99: "99", 100: "99+", 5000: "99+"} {
		if got := badgeText(count); got != want {
			t.Errorf("badgeText(%d) = %q, want %q", count, got, want)
		}
	}
	// Every character the badge draws has a bitmap.
	for _, r := range "0123456789+" {
		if _, ok := digitFont[r]; !ok {
			t.Errorf("no bitmap for %q", r)
		}
	}
}

func TestMissingIconStillShowsColor(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	if err := c.SetKey(Update{ID: "a", Status: StatusDone, IconRef: "nope.png"}); err != nil {
		t.Fatal(err)
	}
	ks := out.take()[0]
	if ks.Color != ColorDone || ks.EmojiID == transport.CustomGlyphSentinelEmojiID {
		t.Errorf("got %+v, want the color alone", ks)
	}
}
