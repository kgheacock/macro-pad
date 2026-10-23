package layers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPageLayout is task 0078's DoD-1.
func TestPageLayout(t *testing.T) {
	for n, want := range map[int]int{1: 1, 5: 1, 6: 2, 9: 2, 10: 3, 13: 3, 14: 4, 17: 4, 18: 5, 20: 5} {
		if got := PageCount(n); got != want {
			t.Errorf("PageCount(%d) = %d, want %d", n, got, want)
		}
	}

	// Each row is one page, keys 1 to 6 (index 0 to 5): a child number from
	// 1, "up", "<", ">", or "." for blank.
	cases := []struct {
		n     int
		pages []string
	}{
		{5, []string{"1 2 3 up 4 5"}},
		{6, []string{"1 2 3 up 4 >", "5 6 . < . ."}},
		{9, []string{"1 2 3 up 4 >", "5 6 7 < 8 9"}},
		{10, []string{"1 2 3 up 4 >", "5 6 7 < 8 >", "9 10 . < . ."}},
		{20, []string{
			"1 2 3 up 4 >",
			"5 6 7 < 8 >",
			"9 10 11 < 12 >",
			"13 14 15 < 16 >",
			"17 18 19 < 20 .",
		}},
	}
	for _, tc := range cases {
		if got := PageCount(tc.n); got != len(tc.pages) {
			t.Fatalf("%d children: %d pages, want %d", tc.n, got, len(tc.pages))
		}
		for page, want := range tc.pages {
			if got := layoutString(pageLayout(tc.n, page)); got != want {
				t.Errorf("%d children, page %d: %q, want %q", tc.n, page+1, got, want)
			}
		}
	}
}

func layoutString(l [KeyCount]pageKey) string {
	var out []string
	for _, k := range l {
		switch k.role {
		case roleChild:
			out = append(out, fmt.Sprint(k.child+1))
		case roleUp:
			out = append(out, "up")
		case roleLeft:
			out = append(out, "<")
		case roleRight:
			out = append(out, ">")
		default:
			out = append(out, ".")
		}
	}
	return strings.Join(out, " ")
}

// withChildren gives key "p" in slot 0 n children, c1 to cn, each with its
// number as a text icon.
func withChildren(t *testing.T, c *Controller, n int) {
	t.Helper()
	slot := 0
	if err := c.SetKey(Update{ID: "p", Slot: &slot, IconRef: "github.png"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		if err := c.SetKey(Update{ID: fmt.Sprintf("c%d", i), Parent: "p", IconText: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
}

// shownLayout reads what each key shows from the controller: a child's
// number, "up", "<", ">", or "." for blank.
func shownLayout(c *Controller) string {
	c.sendMu.Lock()
	defer c.sendMu.Unlock()
	var out []string
	for _, v := range c.shown {
		switch {
		case v.kind == kindShift && v.iconRef == ShiftIcon:
			out = append(out, "up")
		case v.kind == kindShift && v.iconRef == LeftIcon:
			out = append(out, "<")
		case v.kind == kindShift && v.iconRef == RightIcon:
			out = append(out, ">")
		case v.kind == kindKey:
			out = append(out, v.iconText)
		default:
			out = append(out, ".")
		}
	}
	return strings.Join(out, " ")
}

func wantLayout(t *testing.T, c *Controller, want string) {
	t.Helper()
	if got := shownLayout(c); got != want {
		t.Fatalf("pad shows %q, want %q", got, want)
	}
}

// TestPaging is task 0078's DoD-3.
func TestPaging(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	withChildren(t, c, 7)

	c.Press(0)
	wantLayout(t, c, "1 2 3 up 4 >")
	if _, page, pages := c.Layer(); page != 0 || pages != 2 {
		t.Fatalf("page %d of %d, want 0 of 2", page, pages)
	}

	if p, ch := c.Press(RightKey); p != "" || ch != "" {
		t.Errorf("right arrow named child %q of %q", ch, p)
	}
	wantLayout(t, c, "5 6 7 < . .")

	// A press on a child of page 2 names it, and changes nothing.
	if p, ch := c.Press(1); p != "p" || ch != "c6" {
		t.Errorf("press on key 1 of page 2 named %q of %q, want c6 of p", ch, p)
	}
	// A blank key does nothing and names nothing.
	if _, ch := c.Press(RightKey); ch != "" {
		t.Errorf("blank key named %q", ch)
	}
	wantLayout(t, c, "5 6 7 < . .")

	c.Press(LeftKey)
	wantLayout(t, c, "1 2 3 up 4 >")
	if p, ch := c.Press(4); p != "p" || ch != "c4" {
		t.Errorf("press on key 4 of page 1 named %q of %q, want c4 of p", ch, p)
	}

	c.Press(UpKey)
	if a, page, pages := c.Layer(); a != nil || page != 0 || pages != 0 {
		t.Fatalf("after up: active %v page %d of %d, want the top layer", a, page, pages)
	}

	// Every move told the client, with its page.
	want := [][2]int{{0, 2}, {1, 2}, {0, 2}, {0, 0}}
	if fmt.Sprint(out.pages) != fmt.Sprint(want) {
		t.Errorf("layerChanged pages %v, want %v", out.pages, want)
	}

	// The layer opens on page 1 again.
	c.Press(0)
	wantLayout(t, c, "1 2 3 up 4 >")
}

// TestPagingFiveChildrenHasNoArrows checks that five children show as a
// child layer always has: one page, the up key, and no arrows.
func TestPagingFiveChildrenHasNoArrows(t *testing.T) {
	c := open(t, "", newFakeOut())
	withChildren(t, c, 5)
	c.Press(0)
	wantLayout(t, c, "1 2 3 up 4 5")
	c.Press(RightKey)
	wantLayout(t, c, "1 2 3 up 4 5")
}

// TestPagingRemove is task 0078's DoD-4.
func TestPagingRemove(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	withChildren(t, c, 10)
	c.Press(0)
	c.Press(RightKey)
	c.Press(RightKey)
	wantLayout(t, c, "9 10 . < . .")

	// Page 3 empties: the pad moves to page 2, now the last.
	for _, id := range []string{"c9", "c10"} {
		if err := c.RemoveKey(id, "p"); err != nil {
			t.Fatal(err)
		}
	}
	wantLayout(t, c, "5 6 7 < 8 .")
	if _, page, pages := c.Layer(); page != 1 || pages != 2 {
		t.Fatalf("page %d of %d, want 1 of 2", page, pages)
	}

	// At five children there is one page, and no arrows.
	for _, id := range []string{"c8", "c7", "c6"} {
		if err := c.RemoveKey(id, "p"); err != nil {
			t.Fatal(err)
		}
	}
	wantLayout(t, c, "1 2 3 up 4 5")
	if _, page, pages := c.Layer(); page != 0 || pages != 1 {
		t.Fatalf("page %d of %d, want 0 of 1", page, pages)
	}
}

// TestPageSaved is task 0078's DoD-5.
func TestPageSaved(t *testing.T) {
	dir := t.TempDir()
	c, err := Open(Options{Dir: dir, AssetsDir: assetsDir, Out: newFakeOut(), Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	withChildren(t, c, 7)
	c.Press(0)
	c.Press(RightKey)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, FileName))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"page": 1`) {
		t.Fatalf("layers.json does not hold page 1:\n%s", data)
	}

	c2 := open(t, dir, newFakeOut())
	wantLayout(t, c2, "5 6 7 < . .")
	c2.Close()

	// A page the layer does not have moves the file aside.
	bad := strings.Replace(string(data), `"page": 1`, `"page": 5`, 1)
	if err := os.WriteFile(filepath.Join(dir, FileName), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	c3 := open(t, dir, newFakeOut())
	if a, _, _ := c3.Layer(); a != nil {
		t.Errorf("a file with page 5 of 2 loaded, active %q", *a)
	}
	if _, err := os.Stat(filepath.Join(dir, FileName+".bad")); err != nil {
		t.Errorf("the file was not moved aside: %v", err)
	}
}

// TestPreloadOrder is task 0078's DoD-8: the top layer and the first page
// of every key come before any later page.
func TestPreloadOrder(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	for slot, id := range []string{"a", "b"} {
		s := slot
		if err := c.SetKey(Update{ID: id, Slot: &s, IconText: id}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 10; i++ {
		if err := c.SetKey(Update{ID: fmt.Sprintf("a%d", i), Parent: "a", IconText: fmt.Sprintf("a%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 6; i++ {
		if err := c.SetKey(Update{ID: fmt.Sprintf("b%d", i), Parent: "b", IconText: fmt.Sprintf("b%d", i)}); err != nil {
			t.Fatal(err)
		}
	}

	frames := neededFrames(c.Snapshot())
	at := make(map[string]int)
	for i, k := range frames {
		name := k.text
		if name == "" {
			name = k.ref
		}
		if _, ok := at[name]; !ok {
			at[name] = i
		}
	}
	firstPages := []string{"a", "b", ShiftIcon, RightIcon, "a1", "a2", "a3", "a4", "b1", "b2", "b3", "b4"}
	later := []string{LeftIcon, "a5", "a8", "a9", "a10", "b5", "b6"}
	for _, f := range firstPages {
		for _, l := range later {
			fi, ok1 := at[f]
			li, ok2 := at[l]
			if !ok1 || !ok2 {
				t.Fatalf("frames %v lack %q or %q", at, f, l)
			}
			if fi > li {
				t.Errorf("frame %q (%d) comes after %q (%d)", f, fi, l, li)
			}
		}
	}
	// a9 and a10 are on page 3, after every page 2 frame.
	if at["a9"] < at["b6"] {
		t.Errorf("page 3 frame a9 (%d) before page 2 frame b6 (%d)", at["a9"], at["b6"])
	}
}
