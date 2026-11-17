package layers

import (
	"bytes"
	"image"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// glyphBoundsWith returns the smallest rectangle that holds every pixel of the glyph
// of key that differs between a render with the update's counts and one with
// none.
func glyphBoundsWith(t *testing.T, u Update) (image.Rectangle, []byte) {
	t.Helper()
	out := newFakeOut()
	c := open(t, "", out)
	if err := c.SetKey(u); err != nil {
		t.Fatal(err)
	}
	out.mu.Lock()
	data := out.glyphs[0]
	out.mu.Unlock()
	return changedAgainstPlain(t, u, data), data
}

func changedAgainstPlain(t *testing.T, u Update, data []byte) image.Rectangle {
	t.Helper()
	plain := u
	plain.Count, plain.CountGreen, plain.CountYellow = 0, 0, 0
	out := newFakeOut()
	c := open(t, "", out)
	// A key with no count and no picture sends no glyph, so give it a text.
	if err := c.SetKey(plain); err != nil {
		t.Fatal(err)
	}
	out.mu.Lock()
	base := out.glyphs[0]
	out.mu.Unlock()
	if base == nil {
		t.Fatal("the plain key sent no glyph")
	}
	a, b := decodeNRGBA(t, base), decodeNRGBA(t, data)
	var diff image.Rectangle
	for y := 0; y < a.Bounds().Dy(); y++ {
		for x := 0; x < a.Bounds().Dx(); x++ {
			if a.NRGBAAt(x, y) != b.NRGBAAt(x, y) {
				diff = diff.Union(image.Rect(x, y, x+1, y+1))
			}
		}
	}
	return diff
}

func decodeNRGBA(t *testing.T, data []byte) *image.NRGBA {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	out := image.NewNRGBA(img.Bounds())
	for y := 0; y < img.Bounds().Dy(); y++ {
		for x := 0; x < img.Bounds().Dx(); x++ {
			out.Set(x, y, img.At(x, y))
		}
	}
	return out
}

// TestBadgeCounts is task 0091's DoD-6 for the layers: an update with only
// count draws one red badge, the same as one with countGreen and countYellow of
// 0, and each other count draws in its own place.
func TestBadgeCounts(t *testing.T) {
	base := Update{ID: "a", Status: StatusDone, IconText: "repo"}

	redOnly, redData := glyphBoundsWith(t, Update{ID: base.ID, Status: base.Status, IconText: base.IconText, Count: 3})
	if redOnly.Empty() || !redOnly.In(badgeDiscRect(FillRed)) {
		t.Errorf("count 3 changed %v, want one badge in %v", redOnly, badgeDiscRect(FillRed))
	}
	_, zeroData := glyphBoundsWith(t, Update{ID: base.ID, Status: base.Status, IconText: base.IconText, Count: 3, CountGreen: 0, CountYellow: 0})
	if !bytes.Equal(redData, zeroData) {
		t.Error("count 3 alone draws another picture than count 3 with countGreen 0 and countYellow 0")
	}

	green, _ := glyphBoundsWith(t, Update{ID: base.ID, Status: base.Status, IconText: base.IconText, CountGreen: 5})
	if green.Empty() || !green.In(badgeDiscRect(FillGreen)) {
		t.Errorf("countGreen 5 changed %v, want one badge in %v", green, badgeDiscRect(FillGreen))
	}
	yellow, _ := glyphBoundsWith(t, Update{ID: base.ID, Status: base.Status, IconText: base.IconText, CountYellow: 120})
	if yellow.Empty() || !yellow.In(badgeDiscRect(FillYellow)) {
		t.Errorf("countYellow 120 changed %v, want one badge in %v", yellow, badgeDiscRect(FillYellow))
	}
	all, _ := glyphBoundsWith(t, Update{ID: base.ID, Status: base.Status, IconText: base.IconText, Count: 1, CountGreen: 2, CountYellow: 3})
	if want := badgeDiscRect(FillYellow).Union(badgeDiscRect(FillRed)); !all.In(want) || !all.Overlaps(badgeDiscRect(FillGreen)) {
		t.Errorf("three counts changed %v, want discs in %v", all, want)
	}
}

// TestBadgeCountsKeepTheKey is the controller side of DoD-6: a key with only a
// green count has content, and a negative count drops the update and changes
// nothing.
func TestBadgeCountsKeepTheKey(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	if err := c.SetKey(Update{ID: "a", Slot: intp(2), CountYellow: 4}); err != nil {
		t.Fatal(err)
	}
	if k := c.Snapshot().Keys[2]; k == nil || k.CountYellow != 4 || !k.hasContent() {
		t.Fatalf("key 2 is %+v, want a key with countYellow 4", k)
	}
	before := c.Snapshot()
	out.takeCalls()
	for _, u := range []Update{
		{ID: "a", CountYellow: 4, Count: -1},
		{ID: "a", CountYellow: 4, CountGreen: -1},
		{ID: "a", CountYellow: -4},
		{ID: "b", Count: 1, CountYellow: -1},
	} {
		if err := c.SetKey(u); err == nil {
			t.Errorf("update %+v was accepted", u)
		}
	}
	if !reflect.DeepEqual(c.Snapshot(), before) {
		t.Error("a negative count changed the object")
	}
	if calls, _ := out.takeCalls(); len(calls) != 0 {
		t.Errorf("a negative count drew %q", calls)
	}
}

// TestBadgeCountsFile: layers.json stays at version 1. A file from before task
// 0091 has no countGreen or countYellow and loads as red counts, and a key
// with only a red count saves without them.
func TestBadgeCountsFile(t *testing.T) {
	dir := t.TempDir()
	old := `{"version": 1, "keys": [{"id": "a", "icon": 0, "status": "ERROR", "count": 7, "iconRef": ""}, null, null, null, null, null]}`
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	c := open(t, dir, newFakeOut())
	k := c.Snapshot().Keys[0]
	if k == nil || k.Count != 7 || k.CountGreen != 0 || k.CountYellow != 0 {
		t.Fatalf("key 0 is %+v, want a red count of 7", k)
	}
	if err := c.SetKey(Update{ID: "b", Slot: intp(1), CountGreen: 2, CountYellow: 3}); err != nil {
		t.Fatal(err)
	}
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `"version": 1`) || !strings.Contains(text, `"countGreen": 2`) || !strings.Contains(text, `"countYellow": 3`) {
		t.Errorf("the saved file is %s, want version 1 and both counts", text)
	}
	if strings.Count(text, "countGreen") != 1 {
		t.Errorf("a key with no green count saved one: %s", text)
	}
}

func intp(n int) *int { return &n }
