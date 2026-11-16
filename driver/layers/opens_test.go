package layers

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// withRepos gives key "repos" in slot 0 n children, r0 to r(n-1), each with
// its own text icon and the Opens flag (task 0088).
func withRepos(t *testing.T, c *Controller, n int) {
	t.Helper()
	slot := 0
	if err := c.SetKey(Update{ID: "repos", Slot: &slot, IconText: "repos", Opens: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		if err := c.SetKey(Update{ID: fmt.Sprintf("r%d", i), Parent: "repos", IconText: fmt.Sprintf("r%d", i), Opens: true}); err != nil {
			t.Fatal(err)
		}
	}
}

// TestChildCap is task 0088's DoD-1.
func TestChildCap(t *testing.T) {
	c := open(t, "", newFakeOut())
	withRepos(t, c, MaxChildren)
	err := c.SetKey(Update{ID: "extra", Parent: "repos", IconText: "x"})
	if err == nil || !strings.Contains(err.Error(), "129 children, at most 128") {
		t.Fatalf("the 129th child gave %v, want an error naming 129 children and 128", err)
	}
	if n := len(c.Snapshot().Keys[0].Children); n != MaxChildren {
		t.Fatalf("the layer holds %d children after a refused one, want %d", n, MaxChildren)
	}
}

// TestWorstCaseTreeFits is task 0088's DoD-3: repos with an item under each
// repo, apps with five actions under each of 20 apps, and a basic key with
// 20 children make 399 nodes.
func TestWorstCaseTreeFits(t *testing.T) {
	c := open(t, "", newFakeOut())
	nodes := 0
	set := func(u Update) {
		t.Helper()
		if err := c.SetKey(u); err != nil {
			t.Fatalf("node %d (%s): %v", nodes+1, u.ID, err)
		}
		nodes++
	}
	slots := []int{0, 1, 2}
	set(Update{ID: "repos", Slot: &slots[0], IconText: "repos"})
	for i := 0; i < MaxChildren; i++ {
		repo := fmt.Sprintf("r%d", i)
		set(Update{ID: repo, Parent: "repos", IconText: repo})
		set(Update{ID: "item", Path: []string{"repos", repo}, IconText: "item"})
	}
	set(Update{ID: "apps", Slot: &slots[1], IconText: "apps"})
	for i := 0; i < 20; i++ {
		app := fmt.Sprintf("a%d", i)
		set(Update{ID: app, Parent: "apps", IconText: app})
		for _, act := range []string{"a1", "a2", "a3", "a4", "a5"} {
			set(Update{ID: act, Path: []string{"apps", app}, IconText: act})
		}
	}
	set(Update{ID: "basic", Slot: &slots[2], IconText: "basic"})
	for i := 0; i < 20; i++ {
		set(Update{ID: fmt.Sprintf("b%d", i), Parent: "basic", IconText: "b"})
	}
	if nodes != 399 {
		t.Fatalf("the worst case has %d nodes, want 399", nodes)
	}
	// The saved file reads back whole, so a restart draws the same tree.
	data, err := json.Marshal(c.Snapshot())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(data); err != nil {
		t.Fatalf("the 399-node file does not load: %v", err)
	}
	c.Press(0)
	wantPath(t, c, []string{"repos"}, 0)
}

// TestPagingOf128Children is task 0088's DoD-4.
func TestPagingOf128Children(t *testing.T) {
	if got := PageCount(MaxChildren); got != 32 {
		t.Fatalf("PageCount(%d) = %d, want 32", MaxChildren, got)
	}
	c := open(t, "", newFakeOut())
	withRepos(t, c, MaxChildren)
	c.Press(0)
	wantPath(t, c, []string{"repos"}, 0)
	for page := 1; page < 32; page++ {
		c.Press(RightKey)
		wantPath(t, c, []string{"repos"}, page)
	}
	if got := layoutString(pageLayout(MaxChildren, 31)); got != "125 126 127 < 128 ." {
		t.Fatalf("page 32 shows %q, want children 125 to 128 and the left arrow", got)
	}
	c.Press(4) // child 128, index 127: it has Opens, so it opens a layer
	wantPath(t, c, []string{"repos", "r127"}, 0)
}

// TestFrames_AWalkOverAllPagesNeverFallsBack is task 0088's DoD-5.
func TestFrames_AWalkOverAllPagesNeverFallsBack(t *testing.T) {
	out := newFakeFrameOut()
	// A long idle time: the preload stores nothing, so every store below
	// belongs to a page turn.
	c := openFrames(t, out, time.Hour)
	withRepos(t, c, MaxChildren)
	c.Press(0)
	out.takeCalls()
	for page := 1; page < 32; page++ {
		c.Press(RightKey)
		calls, _ := out.takeCalls()
		stores, shows := 0, 0
		for _, call := range calls {
			switch {
			case strings.HasPrefix(call, "store "):
				stores++
			case strings.HasPrefix(call, "show "):
				shows++
			default:
				t.Fatalf("page %d: %q is a fallback draw, want only Store and Show frames", page+1, call)
			}
		}
		if shows != 1 {
			t.Fatalf("page %d: %d Show frames, want 1 (calls %q)", page+1, shows, calls)
		}
		if stores > 5 {
			t.Fatalf("page %d: %d Store frames, want at most 4 children and 1 new arrow (calls %q)", page+1, stores, calls)
		}
	}
}

// TestOpens_PressOpensAnEmptyLayer is task 0088's DoD-6.
func TestOpens_PressOpensAnEmptyLayer(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	withRepos(t, c, 3)
	c.Press(0)
	wantPath(t, c, []string{"repos"}, 0)
	out.mu.Lock()
	out.paths, out.pages = nil, nil
	out.mu.Unlock()

	parent, child := c.Press(1) // r1: Opens, no children
	if parent != "" || child != "" {
		t.Fatalf("the press on an Opens node returned (%q, %q), want no childPressed", parent, child)
	}
	wantPath(t, c, []string{"repos", "r1"}, 0)
	out.mu.Lock()
	defer out.mu.Unlock()
	if len(out.paths) != 1 || strings.Join(out.paths[0], "/") != "repos/r1" || out.pages[0] != [2]int{0, 1} {
		t.Fatalf("layerChanged paths %v pages %v, want one change to repos/r1 on page 0 of 1", out.paths, out.pages)
	}
}

// TestOpens_PressWithoutOpensStillSendsAChildPress keeps the old rule: a
// leaf without Opens opens nothing and reports the press.
func TestOpens_PressWithoutOpensStillSendsAChildPress(t *testing.T) {
	c := open(t, "", newFakeOut())
	withChildren(t, c, 3)
	c.Press(0)
	parent, child := c.Press(1)
	if parent != "p" || child != "c2" {
		t.Fatalf("a press on a plain leaf returned (%q, %q), want (p, c2)", parent, child)
	}
	wantPath(t, c, []string{"p"}, 0)
}

// TestOpens_RemoveKeepsLayer is task 0088's DoD-7, first and second part.
func TestOpens_RemoveKeepsLayer(t *testing.T) {
	c := open(t, "", newFakeOut())
	withRepos(t, c, 2)
	c.Press(0)
	c.Press(0) // r0
	wantPath(t, c, []string{"repos", "r0"}, 0)
	if err := c.SetKey(Update{ID: "item", Path: []string{"repos", "r0"}, IconText: "item"}); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveNode("item", []string{"repos", "r0"}); err != nil {
		t.Fatal(err)
	}
	wantPath(t, c, []string{"repos", "r0"}, 0)

	// A node without Opens still sends the pad up.
	c2 := open(t, "", newFakeOut())
	withApps(t, c2, 1)
	c2.Press(0)
	c2.Press(0)
	wantPath(t, c2, []string{"apps", "a1"}, 0)
	for _, id := range []string{"code", "run"} {
		if err := c2.RemoveNode(id, []string{"apps", "a1"}); err != nil {
			t.Fatal(err)
		}
	}
	wantPath(t, c2, []string{"apps"}, 0)

	// An update that drops Opens from the open, empty node sends the pad up.
	if err := c.SetKey(Update{ID: "r0", Parent: "repos", IconText: "r0"}); err != nil {
		t.Fatal(err)
	}
	wantPath(t, c, []string{"repos"}, 0)
}

// TestOpens_Persists is task 0088's DoD-7, third part.
func TestOpens_Persists(t *testing.T) {
	dir := t.TempDir()
	c := open(t, dir, newFakeOut())
	withRepos(t, c, 3)
	c.Press(0)
	c.Press(2) // r2
	wantPath(t, c, []string{"repos", "r2"}, 0)
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := open(t, dir, newFakeOut())
	wantPath(t, c2, []string{"repos", "r2"}, 0)
	c2.Press(UpKey)
	wantPath(t, c2, []string{"repos"}, 0)
}

// TestSetKeyOnAFullTree is task 0088's DoD-10: one SetKey on a tree at
// MaxNodes takes under 5 ms. The update is a child, which the top layer does
// not draw, so the time is the tree work.
func TestSetKeyOnAFullTree(t *testing.T) {
	c := open(t, "", newFakeOut())
	slots := []int{0, 1}
	var err error
	for k, id := range []string{"p", "q"} {
		if err = c.SetKey(Update{ID: id, Slot: &slots[k], IconText: id}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; err == nil; i++ {
		parent := []string{"p", "q"}[i%2]
		if i < 2*MaxChildren {
			err = c.SetKey(Update{ID: fmt.Sprintf("c%d", i/2), Parent: parent, IconText: "c"})
		} else {
			j := i - 2*MaxChildren
			err = c.SetKey(Update{ID: fmt.Sprintf("g%d", j/2), Path: []string{parent, "c0"}, IconText: "g"})
		}
	}
	if n := countNodes(c.Snapshot()); n != MaxNodes {
		t.Fatalf("the tree holds %d nodes, want %d", n, MaxNodes)
	}
	const runs = 20
	start := time.Now()
	for i := 0; i < runs; i++ {
		if err := c.SetKey(Update{ID: "c1", Parent: "p", IconText: fmt.Sprintf("c%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	if each := time.Since(start) / runs; each > 5*time.Millisecond {
		t.Fatalf("one SetKey on a %d-node tree took %v, want under 5 ms", MaxNodes, each)
	}
}

func countNodes(d Doc) int {
	var walk func(n *Node) int
	walk = func(n *Node) int {
		total := 1
		for i := range n.Children {
			total += walk(&n.Children[i])
		}
		return total
	}
	total := 0
	for _, k := range d.Keys {
		if k != nil {
			total += walk(k)
		}
	}
	return total
}
