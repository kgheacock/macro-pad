package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// layerServer returns a server on an emulator with an empty layer tree
// that draws through it.
func layerServer(t *testing.T) (*Server, *layers.Controller, *transport.Emulator, *logCollector) {
	t.Helper()
	dev := transport.NewEmulator()
	t.Cleanup(func() { dev.Close() })
	s := NewServer(dev, dev)
	logs := &logCollector{}
	s.SetLogf(logs.logf)
	ctl, err := layers.Open(layers.Options{AssetsDir: "../../assets", Out: s, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ctl.Close() })
	s.SetLayers(ctl)
	return s, ctl, dev, logs
}

// layerChanges returns the active layer of each layerChanged message conn
// has received so far, in order. A null layer is "".
func layerChanges(t *testing.T, conn *fakeConn) []string {
	t.Helper()
	var out []string
	for _, m := range conn.messages(t) {
		if m.Kind != KindLayerChanged {
			continue
		}
		if m.LayerChanged == nil {
			t.Fatal("layerChanged message with no payload")
		}
		if m.LayerChanged.Active == nil {
			out = append(out, "")
		} else {
			out = append(out, *m.LayerChanged.Active)
		}
	}
	return out
}

func waitForLayerChanges(t *testing.T, conn *fakeConn, want []string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		got := layerChanges(t, conn)
		if len(got) == len(want) {
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("layerChanged messages %q, want %q", got, want)
				}
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("layerChanged messages %q, want %q", got, want)
		}
		time.Sleep(time.Millisecond)
	}
}

func click(t *testing.T, dev *transport.Emulator, key byte, at uint64) {
	t.Helper()
	dev.InjectEvent(transport.Event{KeyIndex: key, Type: transport.EventPress, Timestamp: at})
	dev.InjectEvent(transport.Event{KeyIndex: key, Type: transport.EventRelease, Timestamp: at + 50_000})
}

func TestLayerChanged(t *testing.T) {
	s, ctl, dev, _ := layerServer(t)
	for _, u := range []layers.Update{
		{ID: "github", Status: layers.StatusRunning},
		{ID: "build", Parent: "github", Status: layers.StatusError},
	} {
		if err := ctl.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}

	// A client learns the active layer when it connects.
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	waitForLayerChanges(t, conn, []string{""})

	// And after each change: a press opens the child layer, and a press on
	// the up key returns.
	click(t, dev, 0, 0)
	waitForLayerChanges(t, conn, []string{"", "github"})

	// A client that connects now learns the open layer.
	late := newFakeConn()
	if s.addClient(late) == nil {
		t.Fatal("late client rejected")
	}
	waitForLayerChanges(t, late, []string{"github"})

	click(t, dev, layers.UpKey, 1_000_000)
	waitForLayerChanges(t, conn, []string{"", "github", ""})
	waitForLayerChanges(t, late, []string{"github", ""})
}

func TestLayerKeysReachClients(t *testing.T) {
	s, ctl, _, _ := layerServer(t)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}

	// The layer tree draws through the server, so a client such as the
	// virtual pad sees each key it sets.
	if err := ctl.SetKey(layers.Update{ID: "github", Status: layers.StatusDone, IconRef: "github.png"}); err != nil {
		t.Fatal(err)
	}
	waitForCondition(t, time.Second, func() bool {
		var glyph, state bool
		for _, m := range conn.messages(t) {
			glyph = glyph || m.Kind == KindSetCustomGlyph && m.SetCustomGlyph.KeyIndex == 0
			state = state || m.Kind == KindSetKeyState && m.SetKeyState.KeyIndex == 0 && m.SetKeyState.Color == layers.ColorDone
		}
		return glyph && state
	})
}

func TestSlotReserved(t *testing.T) {
	s, ctl, _, logs := layerServer(t)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	two := 2
	setLayerKey := func(p SetLayerKeyPayload) {
		conn.send(t, Message{Kind: KindSetLayerKey, SetLayerKey: &p})
	}

	// A message with only an id and a slot reserves the slot.
	setLayerKey(SetLayerKeyPayload{ID: "host", Slot: &two})
	waitForCondition(t, time.Second, func() bool { return ctl.Snapshot().Keys[2] != nil })

	// A second id on that slot is refused, and the daemon logs it.
	setLayerKey(SetLayerKeyPayload{ID: "other", Slot: &two, Status: layers.StatusDone})
	waitForCondition(t, time.Second, func() bool { return logs.has("setLayerKey") })
	if k := ctl.Snapshot().Keys[2]; k.ID != "host" {
		t.Fatalf("slot 2 holds %q, want host", k.ID)
	}

	// A message with no slot skips the reserved one.
	for _, id := range []string{"a", "b", "c"} {
		setLayerKey(SetLayerKeyPayload{ID: id, Status: layers.StatusDone})
	}
	waitForCondition(t, time.Second, func() bool { return ctl.Snapshot().Keys[3] != nil })
	keys := ctl.Snapshot().Keys
	if keys[0].ID != "a" || keys[1].ID != "b" || keys[2].ID != "host" || keys[3].ID != "c" {
		t.Errorf("slots hold %s %s %s %s, want a b host c", keys[0].ID, keys[1].ID, keys[2].ID, keys[3].ID)
	}
}

// TestSetLayerKeyIconText is task 0069: a setLayerKey message with iconText
// reaches the layer tree and the key draws a glyph, and one with both iconRef
// and iconText is logged and dropped.
func TestSetLayerKeyIconText(t *testing.T) {
	s, ctl, _, logs := layerServer(t)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	conn.send(t, Message{Kind: KindSetLayerKey, SetLayerKey: &SetLayerKeyPayload{ID: "t", Status: layers.StatusWarn, IconText: "macro-pad\ndriver"}})
	waitForCondition(t, time.Second, func() bool { return ctl.Snapshot().Keys[0] != nil })
	if got := ctl.Snapshot().Keys[0].IconText; got != "macro-pad\ndriver" {
		t.Fatalf("iconText is %q", got)
	}
	waitForCondition(t, time.Second, func() bool {
		for _, m := range conn.messages(t) {
			if m.Kind == KindSetCustomGlyph && m.SetCustomGlyph.KeyIndex == 0 {
				return true
			}
		}
		return false
	})

	conn.send(t, Message{Kind: KindSetLayerKey, SetLayerKey: &SetLayerKeyPayload{ID: "t", IconRef: "github.png", IconText: "x"}})
	waitForCondition(t, time.Second, func() bool { return logs.has("setLayerKey id=\"t\" dropped") })
	if got := ctl.Snapshot().Keys[0].IconText; got != "macro-pad\ndriver" {
		t.Errorf("a rejected message changed iconText to %q", got)
	}
}

func TestSetLayerKeyWithoutLayersIsLogged(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)
	logs := &logCollector{}
	s.SetLogf(logs.logf)
	conn := newFakeConn()
	s.addClient(conn)

	conn.send(t, Message{Kind: KindSetLayerKey, SetLayerKey: &SetLayerKeyPayload{ID: "x"}})
	waitForCondition(t, time.Second, func() bool { return logs.has("setLayerKey dropped") })
}

// layerTreeWithChild sets a "github" key with one child "build".
func layerTreeWithChild(t *testing.T, ctl *layers.Controller) {
	t.Helper()
	for _, u := range []layers.Update{
		{ID: "github", Status: layers.StatusRunning},
		{ID: "build", Parent: "github", Status: layers.StatusError},
	} {
		if err := ctl.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
}

// The layer opens on the press-down, with no release, well inside the 400 ms
// that the double-press window used to add (task 0058).
func TestLayerOpensOnPressDownWithoutWaiting(t *testing.T) {
	s, ctl, dev, _ := layerServer(t)
	layerTreeWithChild(t, ctl)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	waitForLayerChanges(t, conn, []string{""})

	start := time.Now()
	dev.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventPress, Timestamp: 0})
	waitForLayerChanges(t, conn, []string{"", "github"})

	if elapsed := time.Since(start); elapsed >= doublePressWindow/2 {
		t.Fatalf("layer opened after %s, want well under the %s double-press window", elapsed, doublePressWindow)
	}
}

// A key held for a long time still navigates, once. The resolver calls it a
// long press, and plugins still see that signal.
func TestLayerKeyHeldLongNavigatesOnceAndPluginsSeeLongPress(t *testing.T) {
	s, ctl, dev, _ := layerServer(t)
	layerTreeWithChild(t, ctl)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	waitForLayerChanges(t, conn, []string{""})

	dev.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventPress, Timestamp: 0})
	dev.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventRelease, Timestamp: 700_000})

	waitForCondition(t, time.Second, func() bool {
		for _, m := range conn.messages(t) {
			if m.Kind == KindSignal && m.Signal.Name == SignalLongPress {
				return true
			}
		}
		return false
	})
	time.Sleep(50 * time.Millisecond)
	waitForLayerChanges(t, conn, []string{"", "github"})
}

// Two quick presses on a layer key act twice, in order: open, then no-op on
// a key of the child layer that is not the up key. No double-press window
// merges them into one.
func TestLayerPressesAreAppliedInOrder(t *testing.T) {
	s, ctl, dev, _ := layerServer(t)
	layerTreeWithChild(t, ctl)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	waitForLayerChanges(t, conn, []string{""})

	dev.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventPress, Timestamp: 0})
	dev.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventRelease, Timestamp: 40_000})
	dev.InjectEvent(transport.Event{KeyIndex: layers.UpKey, Type: transport.EventPress, Timestamp: 100_000})
	dev.InjectEvent(transport.Event{KeyIndex: layers.UpKey, Type: transport.EventRelease, Timestamp: 140_000})

	waitForLayerChanges(t, conn, []string{"", "github", ""})
}

func TestRemoveLayerKey(t *testing.T) {
	s, ctl, _, logs := layerServer(t)
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	for _, u := range []layers.Update{
		{ID: "github", Status: layers.StatusRunning},
		{ID: "dev", Status: layers.StatusDone},
	} {
		if err := ctl.SetKey(u); err != nil {
			t.Fatal(err)
		}
	}
	stateCount := func() int {
		n := 0
		for _, m := range conn.messages(t) {
			if m.Kind == KindSetKeyState {
				n++
			}
		}
		return n
	}
	waitForCondition(t, time.Second, func() bool { return stateCount() >= 2 })
	before := stateCount()

	// The tree drops "github", and only its key redraws: blank.
	conn.send(t, Message{Kind: KindRemoveLayerKey, RemoveLayerKey: &RemoveLayerKeyPayload{ID: "github"}})
	waitForCondition(t, time.Second, func() bool { return ctl.Snapshot().Keys[0] == nil })
	waitForCondition(t, time.Second, func() bool { return stateCount() == before+1 })
	msgs := conn.messages(t)
	last := msgs[len(msgs)-1]
	if last.Kind != KindSetKeyState || last.SetKeyState.KeyIndex != 0 || last.SetKeyState.Color != 0 {
		t.Errorf("last message %+v, want key 0 blank", last)
	}
	if k := ctl.Snapshot().Keys[1]; k == nil || k.ID != "dev" {
		t.Errorf("slot 1 holds %+v, want dev untouched", k)
	}

	// An unknown id changes nothing and is not logged as an error.
	conn.send(t, Message{Kind: KindRemoveLayerKey, RemoveLayerKey: &RemoveLayerKeyPayload{ID: "nope"}})
	conn.send(t, Message{Kind: KindRemoveLayerKey, RemoveLayerKey: &RemoveLayerKeyPayload{ID: "x", Parent: "nope"}})
	time.Sleep(50 * time.Millisecond)
	if n := stateCount(); n != before+1 {
		t.Errorf("%d key states, want %d", n, before+1)
	}
	if logs.has("removeLayerKey") {
		t.Error("an unknown id was logged as dropped")
	}
}

func TestRemoveLayerKeyWithoutLayersIsLogged(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)
	logs := &logCollector{}
	s.SetLogf(logs.logf)
	conn := newFakeConn()
	s.addClient(conn)

	conn.send(t, Message{Kind: KindRemoveLayerKey, RemoveLayerKey: &RemoveLayerKeyPayload{ID: "x"}})
	waitForCondition(t, time.Second, func() bool { return logs.has("removeLayerKey dropped") })
}

// The inbox daemon's tests check its messages against this file, so the Go
// structs must read the same examples (task 0066).
func TestInboxProtocolFixtures(t *testing.T) {
	data, err := os.ReadFile("../../inbox/test/fixtures/protocol.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		RemoveLayerKey []Message `json:"removeLayerKey"`
		SetLayerKey    []Message `json:"setLayerKey"`
		LayerChanged   []Message `json:"layerChanged"`
		ChildPressed   []Message `json:"childPressed"`
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	if len(fixtures.RemoveLayerKey) != 3 || len(fixtures.SetLayerKey) == 0 {
		t.Fatalf("fixtures hold %d removeLayerKey and %d setLayerKey messages", len(fixtures.RemoveLayerKey), len(fixtures.SetLayerKey))
	}
	for _, m := range fixtures.RemoveLayerKey {
		if m.Kind != KindRemoveLayerKey || m.RemoveLayerKey == nil || m.RemoveLayerKey.ID == "" {
			t.Errorf("fixture %+v is not a removeLayerKey message", m)
		}
	}
	if p := fixtures.RemoveLayerKey[0].RemoveLayerKey; p.ID != "build" || p.Parent != "github" {
		t.Errorf("first fixture is %+v, want build under github", p)
	}
	if p := fixtures.RemoveLayerKey[2].RemoveLayerKey; p.ID != "code" || !reflect.DeepEqual(p.Path, []string{"apps", "app.pad"}) {
		t.Errorf("third fixture is %+v, want code under apps/app.pad", p)
	}
	// The inbox decodes what the server encodes (task 0078).
	for _, m := range fixtures.LayerChanged {
		data, err := json.Marshal(layerChangedMessage(m.LayerChanged.Path, m.LayerChanged.Page, m.LayerChanged.Pages))
		if err != nil {
			t.Fatal(err)
		}
		var back Message
		if err := json.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back.LayerChanged, m.LayerChanged) {
			t.Errorf("layerChanged fixture %+v does not round-trip: %s", m.LayerChanged, data)
		}
	}
	if len(fixtures.ChildPressed) != 2 {
		t.Fatalf("fixtures hold %d childPressed messages, want 2", len(fixtures.ChildPressed))
	}
	wants := []Message{
		childPressedMessage(1, []string{"inbox"}, "macro-pad.driver"),
		childPressedMessage(0, []string{"apps", "app.pad"}, "code"),
	}
	for i, want := range wants {
		m := fixtures.ChildPressed[i]
		if m.Kind != KindChildPressed || m.ChildPressed == nil {
			t.Fatalf("fixture %+v is not a childPressed message", m)
		}
		if !reflect.DeepEqual(m.ChildPressed, want.ChildPressed) {
			t.Errorf("childPressed fixture %+v, want %+v", *m.ChildPressed, *want.ChildPressed)
		}
	}
}

// childPresses returns "parent/child@key" for each childPressed message conn
// has received so far, in order.
func childPresses(t *testing.T, conn *fakeConn) []string {
	t.Helper()
	var out []string
	for _, m := range conn.messages(t) {
		if m.Kind != KindChildPressed {
			continue
		}
		p := m.ChildPressed
		if p == nil {
			t.Fatal("childPressed message with no payload")
		}
		out = append(out, fmt.Sprintf("%s/%s@%d", p.Parent, p.Child, p.KeyIndex))
	}
	return out
}

// TestPressNamesChild is task 0078's DoD-6: a press on a child names the
// child of the page the press saw, even right after an arrow, and a press on
// an arrow or the up key names none.
func TestPressNamesChild(t *testing.T) {
	s, ctl, dev, _ := layerServer(t)
	if err := ctl.SetKey(layers.Update{ID: "inbox"}); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 7; i++ {
		if err := ctl.SetKey(layers.Update{ID: fmt.Sprintf("c%d", i), Parent: "inbox"}); err != nil {
			t.Fatal(err)
		}
	}
	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("client rejected")
	}
	waitForLayerChanges(t, conn, []string{""})

	// Open, a child of page 1, the right arrow, and at once a child of page 2.
	for i, key := range []byte{0, 4, layers.RightKey, 1, layers.LeftKey, layers.UpKey} {
		dev.InjectEvent(transport.Event{KeyIndex: key, Type: transport.EventPress, Timestamp: uint64(i) * 100_000})
		dev.InjectEvent(transport.Event{KeyIndex: key, Type: transport.EventRelease, Timestamp: uint64(i)*100_000 + 40_000})
	}
	waitForLayerChanges(t, conn, []string{"", "inbox", "inbox", "inbox", ""})

	want := []string{"inbox/c4@4", "inbox/c6@1"}
	if got := childPresses(t, conn); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("childPressed %v, want %v", got, want)
	}
	var pages []int
	for _, m := range conn.messages(t) {
		if m.Kind == KindLayerChanged {
			pages = append(pages, m.LayerChanged.Page, m.LayerChanged.Pages)
		}
	}
	if fmt.Sprint(pages) != fmt.Sprint([]int{0, 0, 0, 2, 1, 2, 0, 2, 0, 0}) {
		t.Errorf("layerChanged page, pages: %v", pages)
	}
}
