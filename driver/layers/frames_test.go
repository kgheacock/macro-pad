package layers

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// fakeFrameOut is a fakeOut whose board keeps a frame cache. It records a
// "store N" for each Store frame and a "show [..]" for each Show frames, in
// calls, and keeps the frame of each slot.
type fakeFrameOut struct {
	*fakeOut
	slots map[byte]fakeFrame
	// storeErr, when set, is what every StoreFrame returns.
	storeErr error
	// counts is the counts of each Show frames with pips, and sheets is how
	// many pip sheets arrived.
	counts [][]byte
	sheets int
}

type fakeFrame struct {
	color uint16
	png   []byte
}

func newFakeFrameOut() *fakeFrameOut {
	return &fakeFrameOut{fakeOut: newFakeOut(), slots: make(map[byte]fakeFrame)}
}

func (f *fakeFrameOut) StoreFrame(slot byte, color uint16, png []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.storeErr != nil {
		return f.storeErr
	}
	f.slots[slot] = fakeFrame{color: color, png: png}
	f.calls = append(f.calls, fmt.Sprintf("store %d", slot))
	return nil
}

func (f *fakeFrameOut) ShowFramesWithPips(slots, counts []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("show %v", slots))
	f.counts = append(f.counts, append([]byte(nil), counts...))
	return nil
}

func (f *fakeFrameOut) StorePipSheet(colors []uint16, pixels []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sheets++
	return nil
}

func (f *fakeFrameOut) countCalls(prefix string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

func openFrames(t *testing.T, out Out, idle time.Duration) *Controller {
	t.Helper()
	c, err := Open(Options{AssetsDir: assetsDir, Out: out, Logf: t.Logf, PreloadIdle: idle})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

// TestPreload_ALayerSwitchAfterThePreloadSendsAShowAndNoStore is task 0061's
// DoD-3.
func TestPreload_ALayerSwitchAfterThePreloadSendsAShowAndNoStore(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	sixKeys(t, c)
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	if out.countCalls("store") == 0 {
		t.Fatal("the preload stored no frame")
	}
	out.takeCalls()

	// Into the child layer of "github", and back, twice.
	for i := 0; i < 2; i++ {
		c.Press(0)
		calls, batches := out.takeCalls()
		if len(calls) != 1 || !strings.HasPrefix(calls[0], "show ") || len(batches) != 0 {
			t.Fatalf("open the layer (visit %d): calls = %q, want one Show frames and no Store, no glyph, no state", i, calls)
		}
		c.Press(byte(UpKey))
		calls, _ = out.takeCalls()
		if len(calls) != 1 || !strings.HasPrefix(calls[0], "show ") {
			t.Fatalf("up (visit %d): calls = %q, want one Show frames", i, calls)
		}
	}
}

func TestPreload_AShowNamesOnlyTheKeysThatChange(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	sixKeys(t, c)
	c.WaitPreload(10 * time.Second)
	out.takeCalls()

	c.Press(0) // the child layer: up key at 3, children at 0, 1; keys 2, 4, 5 are blank
	calls, _ := out.takeCalls()
	if len(calls) != 1 {
		t.Fatalf("calls = %q", calls)
	}
	var slots []int
	trimmed := strings.Trim(strings.TrimPrefix(calls[0], "show "), "[]")
	for _, f := range strings.Fields(trimmed) {
		var n int
		fmt.Sscan(f, &n)
		slots = append(slots, n)
	}
	if len(slots) != KeyCount {
		t.Fatalf("show = %q, want a slot or unchanged for each of the %d keys", calls[0], KeyCount)
	}
	for k, s := range slots {
		if s == transport.SlotUnchanged {
			t.Fatalf("key %d unchanged in %q: every key of the child layer differs from the top layer", k, calls[0])
		}
	}
}

func TestPreload_APressPausesTheStores(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, 400*time.Millisecond)
	sixKeys(t, c)
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	before := out.countCalls("store")

	c.Press(4) // no children: draws nothing, but it is a press
	if err := c.SetKey(Update{ID: "lint", Parent: "github", Status: StatusWarn}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(150 * time.Millisecond)
	if got := out.countCalls("store"); got != before {
		t.Fatalf("%d stores 150 ms after a press, want the %d from before: the preload must pause", got, before)
	}
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not resume")
	}
	if got := out.countCalls("store"); got <= before {
		t.Fatalf("%d stores after the pause, want more than %d: the new child's frame", got, before)
	}
}

func TestFrames_ARevisitedLayerSendsNoStoreEvenWithoutAPreload(t *testing.T) {
	out := newFakeFrameOut()
	// A long idle time: the preload stores nothing during this test.
	c := openFrames(t, out, time.Hour)
	c.Press(0) // sets the press time, so the preload waits
	sixKeys(t, c)
	c.Press(0)
	c.Press(byte(UpKey))
	out.takeCalls()

	c.Press(0)
	c.Press(byte(UpKey))
	calls, _ := out.takeCalls()
	if len(calls) != 2 || !strings.HasPrefix(calls[0], "show ") || !strings.HasPrefix(calls[1], "show ") {
		t.Fatalf("calls = %q, want two Show frames and no Store", calls)
	}
}

func TestFrames_AFrameThatCannotBeStoredIsDrawnWithAGlyphAndAState(t *testing.T) {
	out := newFakeFrameOut()
	out.storeErr = transport.ErrFrameTooLarge
	c := openFrames(t, out, time.Hour)
	if err := c.SetKey(Update{ID: "ai", Status: StatusError, IconRef: "ai.png"}); err != nil {
		t.Fatal(err)
	}
	calls, _ := out.takeCalls()
	if len(calls) != 2 || calls[0] != "glyph 0" || calls[1] != "state 0" {
		t.Fatalf("calls = %q, want the glyph and the state of key 0 and no Show", calls)
	}
}

func TestFrameTable_EvictsTheLeastRecentlyUsedFrameThatNoKeyShows(t *testing.T) {
	table := newFrameTable()
	pinned := func(slot byte) bool { return slot == 0 }
	for i := 0; i < transport.SlotCount; i++ {
		slot, ok := table.claim(frameKey{color: uint16(i + 1)}, true, pinned)
		if !ok || int(slot) != i {
			t.Fatalf("claim %d = slot %d, %v; want the lowest free slot", i, slot, ok)
		}
	}
	// Slot 1 was used longest ago among the unpinned, until it is looked up.
	table.lookup(frameKey{color: 2})
	slot, ok := table.claim(frameKey{color: 1000}, true, pinned)
	if !ok || slot != 2 {
		t.Fatalf("a full table gave slot %d, %v; want slot 2, the least recently used that is not pinned (slot 0 is pinned, slot 1 was used)", slot, ok)
	}
	if table.has(frameKey{color: 3}) {
		t.Fatal("the evicted frame is still in the table")
	}
	if _, ok := table.claim(frameKey{color: 1001}, false, pinned); ok {
		t.Fatal("a claim with no eviction took a slot of a full table")
	}
}

func TestFrameTable_NeverNamesASlotAbove127(t *testing.T) {
	table := newFrameTable()
	for i := 0; i < 3*transport.SlotCount; i++ {
		slot, ok := table.claim(frameKey{color: uint16(i + 1)}, true, func(byte) bool { return false })
		if !ok || slot >= transport.SlotCount {
			t.Fatalf("claim %d = slot %d, %v", i, slot, ok)
		}
	}
}

// TestPipNoStore is task 0062's DoD-6: counts 1 to 99 on one icon and color use
// one slot, and a count change sends a Show frames with pips and no Store frame.
func TestPipNoStore(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	if out.sheets != 1 {
		t.Fatalf("the board got %d pip sheets, want 1 before the first Show", out.sheets)
	}
	if err := c.SetKey(Update{ID: "github", Status: StatusRunning, IconRef: "github.png", Count: 1}); err != nil {
		t.Fatal(err)
	}
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	out.takeCalls()

	for count := 2; count <= 99; count++ {
		if err := c.SetKey(Update{ID: "github", Status: StatusRunning, IconRef: "github.png", Count: count}); err != nil {
			t.Fatal(err)
		}
		calls, _ := out.takeCalls()
		if len(calls) != 1 || !strings.HasPrefix(calls[0], "show ") {
			t.Fatalf("count %d: calls = %q, want one Show frames with pips and no Store", count, calls)
		}
		out.mu.Lock()
		got := out.counts[len(out.counts)-1][0]
		out.mu.Unlock()
		if int(got) != count {
			t.Fatalf("count %d: the Show named count %d for key 0", count, got)
		}
	}
	// A count of 0 shows the frame as it is: the pip goes, with no Store.
	if err := c.SetKey(Update{ID: "github", Status: StatusRunning, IconRef: "github.png", Count: 0}); err != nil {
		t.Fatal(err)
	}
	calls, _ := out.takeCalls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "show ") {
		t.Fatalf("count 0: calls = %q, want one Show frames with pips", calls)
	}
}
