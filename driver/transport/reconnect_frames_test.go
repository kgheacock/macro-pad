package transport

import (
	"errors"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func countCalls(calls []string, prefix string) int {
	n := 0
	for _, c := range calls {
		if strings.HasPrefix(c, prefix) {
			n++
		}
	}
	return n
}

// TestReconnecting_StoresEveryFrameAgainAfterAReconnect is task 0061's DoD-5:
// a board that reconnects holds no frame, so Reconnecting clears its record
// of what the board holds and stores every frame again, and the next Show
// draws with no further store.
func TestReconnecting_StoresEveryFrameAgainAfterAReconnect(t *testing.T) {
	bs := make(boards, 2)
	first, second := newFakeBoard(), newFakeBoard()
	bs <- first
	r := newReconnecting(bs.open, 0, "", nil)
	defer r.Close()
	waitFor(t, "the first connect", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.dev != nil })

	for slot := byte(0); slot < 4; slot++ {
		if err := r.StoreFrame(slot*10, uint16(slot), iconPixels(slot+1)); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.StoreFrame(77, 0xF800, nil); err != nil {
		t.Fatal(err)
	}
	if err := r.ShowFrames([]byte{0, 10, 20, 30, 77, SlotUnchanged}); err != nil {
		t.Fatal(err)
	}
	if got := countCalls(first.sent(), "store"); got != 5 {
		t.Fatalf("first board got %d stores, want 5: %q", got, first.sent())
	}

	first.disconnect()
	bs <- second
	waitFor(t, "every frame stored again", func() bool { return countCalls(second.sent(), "store") >= 5 })

	var stores []string
	for _, c := range second.sent() {
		if strings.HasPrefix(c, "store") {
			stores = append(stores, c)
		}
	}
	want := []string{
		"store 0 color 0x0000 32768 bytes",
		"store 10 color 0x0001 32768 bytes",
		"store 20 color 0x0002 32768 bytes",
		"store 30 color 0x0003 32768 bytes",
		"store 77 color 0xf800 0 bytes",
	}
	if !reflect.DeepEqual(stores, want) {
		t.Fatalf("second board stores = %q, want %q", stores, want)
	}

	// The next switch draws: one Show, and no store before it.
	before := len(second.sent())
	if err := r.ShowFrames([]byte{30, 20, 10, 0, SlotUnchanged, 77}); err != nil {
		t.Fatal(err)
	}
	after := second.sent()[before:]
	if want := []string{"show [30 20 10 0 255 77]"}; !reflect.DeepEqual(after, want) {
		t.Fatalf("calls after the reconnect's Show = %q, want %q", after, want)
	}
}

func TestReconnecting_AShowOfAFrameStoredWhileNoBoardWasThereStoresItFirst(t *testing.T) {
	bs := make(boards, 1)
	r := newReconnecting(bs.open, 0, "", nil)
	defer r.Close()

	// No board yet: the frame is only remembered.
	if err := r.StoreFrame(5, 0x001F, iconPixels(9)); err != nil {
		t.Fatal(err)
	}
	if err := r.ShowFrames([]byte{5}); err != nil {
		t.Fatal(err)
	}

	board := newFakeBoard()
	bs <- board
	waitFor(t, "the frame and the key replayed", func() bool {
		c := board.sent()
		return countCalls(c, "store") >= 1 && countCalls(c, "glyph") >= 1
	})
	calls := board.sent()
	if got := countCalls(calls, "store 5 "); got != 1 {
		t.Fatalf("the board got %d stores of slot 5, want 1: %q", got, calls)
	}
}

func TestReconnecting_AShowMissStoresTheFramesOfThoseKeysAgainAndShowsThem(t *testing.T) {
	r, board := connected(t, "", nil)
	defer r.Close()

	r.StoreFrame(3, 0x07E0, iconPixels(4))
	r.StoreFrame(4, 0x001F, nil)
	r.ShowFrames([]byte{3, 4})
	before := len(board.sent())

	// The board lost slot 3, as after a code reload that kept USB up.
	board.in <- Message{Type: MessageTypeShowMiss, ShowMiss: ShowMiss{Mask: 0b01}}
	waitFor(t, "the repair", func() bool { return len(board.sent()) >= before+2 })

	got := board.sent()[before:]
	want := []string{
		"store 3 color 0x07e0 32768 bytes",
		"show [3 255 255 255 255 255 255 255]",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("repair = %q, want %q", got, want)
	}
}

func TestReconnecting_AGlyphThatDoesNotCompressIsRefusedEvenWithNoBoard(t *testing.T) {
	bs := make(boards)
	r := newReconnecting(bs.open, 0, "", nil)
	defer r.Close()

	noise := make([]byte, CustomGlyphPixelsSize)
	rand.New(rand.NewSource(2)).Read(noise)
	if err := r.StoreFrame(1, 0, noise); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("StoreFrame(noise) = %v, want ErrFrameTooLarge", err)
	}
	if err := r.StoreFrame(SlotCount, 0, nil); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("StoreFrame(slot %d) = %v, want ErrInvalidSlot", SlotCount, err)
	}
}

func TestReconnecting_AShownFrameIsTheKeysSavedState(t *testing.T) {
	dir := t.TempDir()
	r, _ := connected(t, dir, nil)
	pixels := iconPixels(0x31)
	r.StoreFrame(8, 0x001F, pixels)
	r.StoreFrame(9, 0xF800, nil)
	r.ShowFrames([]byte{8, 9})
	r.Close()

	r2, board := connected(t, dir, nil)
	defer r2.Close()
	waitFor(t, "the replay", func() bool { return len(board.sent()) >= 3 })
	want := []string{
		"glyph 0 32768 bytes",
		"key 0 color 0x001f emoji 254 blink false",
		"key 1 color 0xf800 emoji 0 blink false",
	}
	if got := board.sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay after a restart = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(dir, glyphFileName(newGlyphRec(pixels).hash))); err != nil {
		t.Fatalf("glyph file of a shown frame: %v", err)
	}
}
