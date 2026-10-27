package layers

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// withApps gives key "apps" in slot 0 the children a1 to an, and gives each
// app the grandchildren code and run, as task 0085 would. Every node has
// its id as a text icon.
func withApps(t *testing.T, c *Controller, n int) {
	t.Helper()
	slot := 0
	if err := c.SetKey(Update{ID: "apps", Slot: &slot, IconText: "apps"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		app := fmt.Sprintf("a%d", i)
		if err := c.SetKey(Update{ID: app, Parent: "apps", IconText: app}); err != nil {
			t.Fatal(err)
		}
		for _, action := range []string{"code", "run"} {
			if err := c.SetKey(Update{ID: action, Path: []string{"apps", app}, IconText: action}); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func wantPath(t *testing.T, c *Controller, want []string, wantPage int) {
	t.Helper()
	path, page, _ := c.LayerPath()
	if !reflect.DeepEqual(path, want) || page != wantPage {
		t.Fatalf("layer %v page %d, want %v page %d", path, page, want, wantPage)
	}
}

// TestNestedPress is task 0086's DoD-4, DoD-5, and DoD-6.
func TestNestedPress(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	withApps(t, c, 6)

	c.Press(0)
	wantLayout(t, c, "a1 a2 a3 up a4 >")
	c.Press(RightKey)
	wantLayout(t, c, "a5 a6 . < . .")

	// A child with children opens its layer and names no child.
	if path, child := c.PressNode(1); path != nil || child != "" {
		t.Fatalf("press on a6 named %v/%q, want nothing", path, child)
	}
	wantPath(t, c, []string{"apps", "a6"}, 0)
	wantLayout(t, c, "code run . up . .")
	out.mu.Lock()
	last := out.paths[len(out.paths)-1]
	out.mu.Unlock()
	if !reflect.DeepEqual(last, []string{"apps", "a6"}) {
		t.Fatalf("LayerChanged got %v, want apps/a6", last)
	}

	// A grandchild with no children is a child press, with the whole path.
	if path, child := c.PressNode(1); !reflect.DeepEqual(path, []string{"apps", "a6"}) || child != "run" {
		t.Fatalf("press on run named %v/%q, want apps/a6/run", path, child)
	}
	if parent, child := c.Press(0); parent != "a6" || child != "code" {
		t.Fatalf("Press named %q/%q, want a6/code", parent, child)
	}

	// Up goes back one level, to the page it showed.
	c.Press(UpKey)
	wantPath(t, c, []string{"apps"}, 1)
	wantLayout(t, c, "a5 a6 . < . .")
	c.Press(LeftKey)
	c.Press(UpKey)
	wantPath(t, c, nil, 0)
	wantLayout(t, c, "apps . . . . .")
}

// TestNestedUpdateKeepsChildren is task 0086's DoD-3.
func TestNestedUpdateKeepsChildren(t *testing.T) {
	c := open(t, "", newFakeOut())
	withApps(t, c, 1)
	if err := c.SetKey(Update{ID: "a1", Parent: "apps", IconText: "a1", Status: StatusError}); err != nil {
		t.Fatal(err)
	}
	app := c.Snapshot().Keys[0].Children[0]
	if app.Status != StatusError || len(app.Children) != 2 {
		t.Fatalf("a1 is %+v, want ERROR with two children", app)
	}
	if err := c.SetKey(Update{ID: "x", Path: []string{"apps", "nope"}}); err == nil {
		t.Error("an update under a missing path applied")
	}
	if err := c.SetKey(Update{ID: "x", Parent: "apps", Path: []string{"apps"}}); err == nil {
		t.Error("an update with a parent and a path applied")
	}
	// Level MaxDepth holds no children.
	if err := c.SetKey(Update{ID: "h", Path: []string{"apps", "a1", "code"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.SetKey(Update{ID: "i", Path: []string{"apps", "a1", "code", "h"}}); err == nil {
		t.Error("a node on level 5 applied")
	}
}

// TestNestedRemove is task 0086's DoD-7.
func TestNestedRemove(t *testing.T) {
	out := newFakeOut()
	c := open(t, "", out)
	withApps(t, c, 6)
	c.Press(0)
	c.Press(RightKey)
	c.Press(1) // a6

	// The last grandchild goes: up to the apps layer, on its page.
	for _, id := range []string{"code", "run"} {
		if err := c.RemoveNode(id, []string{"apps", "a6"}); err != nil {
			t.Fatal(err)
		}
	}
	wantPath(t, c, []string{"apps"}, 1)

	// The open node goes.
	if err := c.SetKey(Update{ID: "code", Path: []string{"apps", "a5"}, IconText: "code"}); err != nil {
		t.Fatal(err)
	}
	c.Press(0) // a5
	wantPath(t, c, []string{"apps", "a5"}, 0)
	if err := c.RemoveKey("a5", "apps"); err != nil {
		t.Fatal(err)
	}
	// Five apps fit one page, so the page clamps to 0.
	wantPath(t, c, []string{"apps"}, 0)
	wantLayout(t, c, "a1 a2 a3 up a4 a6")

	// An ancestor goes.
	c.Press(0) // a1
	wantPath(t, c, []string{"apps", "a1"}, 0)
	if err := c.RemoveKey("apps", ""); err != nil {
		t.Fatal(err)
	}
	wantPath(t, c, nil, 0)

	// A missing path changes nothing.
	if err := c.RemoveNode("code", []string{"zz", "a1"}); err != nil {
		t.Fatal(err)
	}
}

// TestNestedPersist is task 0086's DoD-2.
func TestNestedPersist(t *testing.T) {
	dir := t.TempDir()
	c := open(t, dir, newFakeOut())
	withApps(t, c, 6)
	c.Press(0)
	c.Press(RightKey)
	c.Press(0) // a5
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	c2 := open(t, dir, newFakeOut())
	wantPath(t, c2, []string{"apps", "a5"}, 0)
	c2.Press(UpKey)
	wantPath(t, c2, []string{"apps"}, 1)
}

// TestOldActiveStringLoads is task 0086's DoD-2: a layers.json saved before
// nesting names active as a string.
func TestOldActiveStringLoads(t *testing.T) {
	d, err := Parse([]byte(`{"version":1,"active":"p","page":1,"keys":[{"id":"p","children":[` +
		`{"id":"c"},{"id":"d"},{"id":"e"},{"id":"f"},{"id":"g"},{"id":"h"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(d.Active, []string{"p"}) || d.Page != 1 {
		t.Fatalf("active %v page %d, want [p] page 1", d.Active, d.Page)
	}
}

// TestMaxNodes is task 0086's DoD-1.
func TestMaxNodes(t *testing.T) {
	c := open(t, "", newFakeOut())
	slot := 0
	if err := c.SetKey(Update{ID: "p", Slot: &slot}); err != nil {
		t.Fatal(err)
	}
	added := 1
	var err error
	for i := 0; i < MaxChildren && err == nil; i++ {
		child := fmt.Sprintf("c%d", i)
		if err = c.SetKey(Update{ID: child, Parent: "p"}); err != nil {
			break
		}
		added++
		for j := 0; j < MaxChildren; j++ {
			if err = c.SetKey(Update{ID: fmt.Sprintf("g%d", j), Path: []string{"p", child}}); err != nil {
				break
			}
			added++
		}
	}
	if err == nil || added != MaxNodes {
		t.Fatalf("added %d nodes before %v, want %d and an error", added, err, MaxNodes)
	}
}

// TestNeededFramesCoversGrandchildren checks that the preload walks every
// level (task 0086).
func TestNeededFramesCoversGrandchildren(t *testing.T) {
	c := open(t, "", newFakeOut())
	withApps(t, c, 1)
	if err := c.SetKey(Update{ID: "deep", Path: []string{"apps", "a1"}, IconText: "deep"}); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range neededFrames(c.Snapshot()) {
		if strings.Contains(fmt.Sprintf("%+v", k), "deep") {
			found = true
		}
	}
	if !found {
		t.Error("the preload skips a grandchild")
	}
}
