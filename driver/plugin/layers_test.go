package plugin

import (
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
