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
	// badges is the badges of each Show frames with badges, and sheets is how
	// many badge sheets arrived.
	badges [][]transport.Badges
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

func (f *fakeFrameOut) ShowFramesWithBadges(slots []byte, badges []transport.Badges) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, fmt.Sprintf("show %v", slots))
	f.badges = append(f.badges, append([]transport.Badges(nil), badges...))
	return nil
}

func (f *fakeFrameOut) StoreBadgeSheet(colors []uint16, pixels []byte) error {
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

// TestBadgesNoStore is task 0091's DoD-3: a change of any of the three counts
// from 1 to 99 on one icon and color sends one Show frames with badges, no Store
// frame, and uses one slot.
func TestBadgesNoStore(t *testing.T) {
	out := newFakeFrameOut()
	c := openFrames(t, out, time.Millisecond)
	if out.sheets != 1 {
		t.Fatalf("the board got %d badge sheets, want 1 before the first Show", out.sheets)
	}
	update := func(red, green, yellow int) error {
		return c.SetKey(Update{ID: "github", Status: StatusRunning, IconRef: "github.png", Count: red, CountGreen: green, CountYellow: yellow})
	}
	if err := update(1, 1, 1); err != nil {
		t.Fatal(err)
	}
	if !c.WaitPreload(10 * time.Second) {
		t.Fatal("the preload did not finish")
	}
	out.takeCalls()
	stored := func() int {
		out.mu.Lock()
		defer out.mu.Unlock()
		return len(out.slots)
	}
	before := stored()

	for which := 0; which < 3; which++ {
		for count := 2; count <= 99; count++ {
			counts := [3]int{1, 1, 1}
			counts[which] = count
			if err := update(counts[0], counts[1], counts[2]); err != nil {
				t.Fatal(err)
			}
			calls, _ := out.takeCalls()
			if len(calls) != 1 || !strings.HasPrefix(calls[0], "show ") {
				t.Fatalf("count %d of %d: calls = %q, want one Show frames with badges and no Store", count, which, calls)
			}
			out.mu.Lock()
			got := out.badges[len(out.badges)-1][0]
			out.mu.Unlock()
			want := transport.Badges{Red: 1, Green: 1, Yellow: 1}
			switch which {
			case 0:
				want.Red = byte(count)
			case 1:
				want.Green = byte(count)
			case 2:
				want.Yellow = byte(count)
			}
			if got != want {
				t.Fatalf("count %d of %d: the Show named %v for key 0, want %v", count, which, got, want)
			}
		}
		if err := update(1, 1, 1); err != nil {
			t.Fatal(err)
		}
		out.takeCalls()
	}
	if got := stored(); got != before {
		t.Errorf("the board holds %d frames after the changes, want the %d it held before", got, before)
	}
	// Counts of 0 draw no discs: the frame shows as it is, with no Store.
	if err := update(0, 0, 0); err != nil {
		t.Fatal(err)
	}
	calls, _ := out.takeCalls()
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "show ") {
		t.Fatalf("counts 0: calls = %q, want one Show frames with badges", calls)
	}
	out.mu.Lock()
	if got := out.badges[len(out.badges)-1][0]; got.Any() {
		t.Errorf("counts 0: the Show named %v for key 0", got)
	}
	out.mu.Unlock()
}
