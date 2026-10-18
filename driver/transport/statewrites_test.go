package transport

import (
	"strings"
	"testing"
)

// writeCounts returns how many times each state file was written, from the
// store's own count.
func writeCounts(r *Reconnecting) map[string]int {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make(map[string]int, len(r.store.writes))
	for name, n := range r.store.writes {
		out[name] = n
	}
	return out
}

func glyphWrites(counts map[string]int) (files, writes int) {
	for name, n := range counts {
		if strings.HasPrefix(name, glyphFilePrefix) {
			files++
			writes += n
		}
	}
	return files, writes
}

// TestStateWrites_SixLayerSwitchesWriteNoGlyphFileAfterTheFirstOfEach is task
// 0061's DoD-8. Two layers of six frames take turns on the pad. A glyph file
// is written once, when a key first shows its picture, and keys.json only when
// a key state changes.
func TestStateWrites_SixLayerSwitchesWriteNoGlyphFileAfterTheFirstOfEach(t *testing.T) {
	r, _ := connected(t, t.TempDir(), nil)
	defer r.Close()

	layerA := []byte{0, 1, 2, 3, 4, 5}
	layerB := []byte{6, 7, 8, 9, 10, 11}
	for slot := byte(0); slot < 12; slot++ {
		if err := r.StoreFrame(slot, uint16(0x100*uint16(slot)), iconPixels(slot+1)); err != nil {
			t.Fatal(err)
		}
	}
	// A frame in the cache is not a key state: nothing is saved for it yet.
	if files, _ := glyphWrites(writeCounts(r)); files != 0 {
		t.Fatalf("%d glyph files written by Store frame alone, want none", files)
	}

	if err := r.ShowFrames(layerA); err != nil {
		t.Fatal(err)
	}
	if err := r.ShowFrames(layerB); err != nil {
		t.Fatal(err)
	}
	afterFirst := writeCounts(r)
	if files, writes := glyphWrites(afterFirst); files != 12 || writes != 12 {
		t.Fatalf("after one visit to each layer: %d glyph files, %d writes, want 12 and 12", files, writes)
	}
	if afterFirst[stateFileName] != 2 {
		t.Fatalf("keys.json written %d times for two changing Shows, want 2", afterFirst[stateFileName])
	}

	// Six more switches, A and B in turn. No key state is new to the board,
	// but each switch changes every key, so keys.json is written once for each.
	for i := 0; i < 6; i++ {
		layer := layerA
		if i%2 == 1 {
			layer = layerB
		}
		if err := r.ShowFrames(layer); err != nil {
			t.Fatal(err)
		}
	}
	after := writeCounts(r)
	if files, writes := glyphWrites(after); files != 12 || writes != 12 {
		t.Fatalf("after six more switches: %d glyph files, %d writes, want 12 and 12", files, writes)
	}
	if got := after[stateFileName]; got != 2+6 {
		t.Fatalf("keys.json written %d times, want one for each of the 8 switches", got)
	}

	// A Show that changes nothing writes nothing.
	if err := r.ShowFrames(layerB); err != nil {
		t.Fatal(err)
	}
	if err := r.ShowFrames([]byte{SlotUnchanged, 7, SlotUnchanged}); err != nil {
		t.Fatal(err)
	}
	if got := writeCounts(r)[stateFileName]; got != after[stateFileName] {
		t.Fatalf("keys.json written %d times, want it left alone by a Show that changes no key", got)
	}
}

func TestStateWrites_ASameKeyStateWritesNothing(t *testing.T) {
	r, _ := connected(t, t.TempDir(), nil)
	defer r.Close()

	glyph := fullGlyph(0x61)
	r.SendCustomGlyph(2, glyph)
	r.SendKeyState(key(2, 0x001F, CustomGlyphSentinelEmojiID, false))
	r.SendKeyState(key(0, 0xF800, 0, false))
	first := writeCounts(r)

	for i := 0; i < 5; i++ {
		r.SendCustomGlyph(2, glyph)
		r.SendKeyState(key(2, 0x001F, CustomGlyphSentinelEmojiID, false))
		r.SendKeyState(key(0, 0xF800, 0, false))
	}
	after := writeCounts(r)
	if first[stateFileName] != after[stateFileName] {
		t.Fatalf("keys.json written %d times, was %d: a repeat of a state must write nothing", after[stateFileName], first[stateFileName])
	}
	if files, writes := glyphWrites(after); files != 1 || writes != 1 {
		t.Fatalf("%d glyph files, %d writes, want 1 and 1", files, writes)
	}
}
