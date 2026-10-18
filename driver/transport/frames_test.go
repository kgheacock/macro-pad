package transport

import (
	"bytes"
	"errors"
	"math/rand"
	"reflect"
	"testing"
	"time"
)

// iconPixels is a flat picture with a transparent corner: it deflates to about
// 1 KB, as the icons of a layer do.
func iconPixels(fill byte) []byte {
	pixels := make([]byte, CustomGlyphPixelsSize)
	for i := range pixels {
		pixels[i] = fill
	}
	pixels[0], pixels[1] = 0, 0
	return pixels
}

func TestStoreFrameRoundTripsAGlyphAndAColor(t *testing.T) {
	pixels := iconPixels(0x5A)
	payload, err := encodeStoreFrame(17, 0xF81F, pixels)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) >= 4096 {
		t.Fatalf("a flat icon took %d bytes, want a small stream", len(payload))
	}
	if payload[0] != 17 || payload[1] != 0x1F || payload[2] != 0xF8 {
		t.Fatalf("header = % x, want slot 17 and the color little-endian", payload[:3])
	}
	got, err := decodeStoreFrame(payload)
	if err != nil {
		t.Fatal(err)
	}
	if got.Slot != 17 || got.Color != 0xF81F || !bytes.Equal(got.Pixels, pixels) {
		t.Fatalf("decoded slot %d color %#04x, %d bytes; want slot 17 color 0xf81f and the glyph", got.Slot, got.Color, len(got.Pixels))
	}
}

func TestStoreFrameOfAColorAloneHasNoStream(t *testing.T) {
	payload, err := encodeStoreFrame(0, 0x07E0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 3 {
		t.Fatalf("payload is %d bytes, want 3", len(payload))
	}
	got, err := decodeStoreFrame(payload)
	if err != nil || got.Pixels != nil || got.Color != 0x07E0 {
		t.Fatalf("decoded %+v, err %v; want the color and no pixels", got, err)
	}
}

func TestStoreFrameRefusesASlotAbove127AndAWrongSizedGlyph(t *testing.T) {
	if _, err := encodeStoreFrame(SlotCount, 0, nil); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("slot %d: err %v, want ErrInvalidSlot", SlotCount, err)
	}
	if _, err := encodeStoreFrame(SlotCount-1, 0, make([]byte, 10)); !errors.Is(err, ErrInvalidGlyphSize) {
		t.Fatalf("short glyph: err %v, want ErrInvalidGlyphSize", err)
	}
}

func TestStoreFrameRefusesAGlyphThatDoesNotCompress(t *testing.T) {
	noise := make([]byte, CustomGlyphPixelsSize)
	rand.New(rand.NewSource(1)).Read(noise)
	if _, err := encodeStoreFrame(3, 0, noise); !errors.Is(err, ErrFrameTooLarge) {
		t.Fatalf("noise: err %v, want ErrFrameTooLarge", err)
	}
}

func TestShowFramesNamesASlotOrUnchangedForEachKey(t *testing.T) {
	slots := []byte{0, 51, SlotUnchanged, 5, SlotUnchanged, 40}
	payload, err := encodeShowFrames(slots)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeShowFrames(payload)
	if err != nil || !reflect.DeepEqual(got, slots) {
		t.Fatalf("decoded %v, err %v; want %v", got, err, slots)
	}
	if _, err := encodeShowFrames([]byte{0, 64}); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("slot 64: err %v, want ErrInvalidSlot", err)
	}
	if _, err := encodeShowFrames(nil); !errors.Is(err, ErrInvalidSlot) {
		t.Fatalf("no key: err %v, want ErrInvalidSlot", err)
	}
}

func TestEmulatorKeepsStoredFramesAndTheLastShow(t *testing.T) {
	e := NewEmulator()
	defer e.Close()

	pixels := iconPixels(0x21)
	if err := e.StoreFrame(9, 0x001F, pixels); err != nil {
		t.Fatal(err)
	}
	if err := e.StoreFrame(10, 0x07E0, nil); err != nil {
		t.Fatal(err)
	}
	if err := e.ShowFrames([]byte{9, 10, SlotUnchanged}); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		_, shows := e.LastShowFrames()
		if shows == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the emulator never received the Show frames")
		}
		time.Sleep(time.Millisecond)
	}
	if f, ok := e.StoredFrame(9); !ok || f.Color != 0x001F || !bytes.Equal(f.Pixels, pixels) {
		t.Fatalf("slot 9 = %+v, %v; want the stored glyph", f, ok)
	}
	if f, ok := e.StoredFrame(10); !ok || f.Pixels != nil {
		t.Fatalf("slot 10 = %+v, %v; want a color-only frame", f, ok)
	}
	if slots, _ := e.LastShowFrames(); !reflect.DeepEqual(slots, []byte{9, 10, SlotUnchanged}) {
		t.Fatalf("last show = %v", slots)
	}
}

func TestShowMissReachesReadMessage(t *testing.T) {
	e := NewEmulator()
	defer e.Close()

	if err := e.InjectShowMiss(0b101); err != nil {
		t.Fatal(err)
	}
	msg, err := e.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != MessageTypeShowMiss || msg.ShowMiss.Mask != 0b101 {
		t.Fatalf("message = %+v, want a Show miss of keys 0 and 2", msg)
	}
}

func TestBatchIncompleteReachesReadMessage(t *testing.T) {
	e := NewEmulator()
	defer e.Close()

	if err := e.InjectBatchIncomplete(0b0100, 0b0110); err != nil {
		t.Fatal(err)
	}
	msg, err := e.ReadMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != MessageTypeBatchIncomplete || msg.BatchIncomplete != (BatchIncomplete{GlyphMask: 0b0100, StateMask: 0b0110}) {
		t.Fatalf("message = %+v, want a Batch incomplete with glyph mask 0b0100 and state mask 0b0110", msg)
	}
}

func TestDecodeBatchIncompleteRejectsAWrongLength(t *testing.T) {
	if _, err := decodeBatchIncomplete([]byte{1}); err == nil {
		t.Fatal("decodeBatchIncomplete with a 1-byte payload returned no error")
	}
	if _, err := decodeBatchIncomplete([]byte{1, 2, 3}); err == nil {
		t.Fatal("decodeBatchIncomplete with a 3-byte payload returned no error")
	}
}

// TestPoolIs64Buffers: the pool stays at the 64 buffers of task 0063. Twelve are
// the pip buffers of task 0062, two for each key, so the host numbers 52.
func TestPoolIs64Buffers(t *testing.T) {
	if PoolSize != 64 || PipBufferCount != 12 || SlotCount != 52 {
		t.Fatalf("pool %d, pip buffers %d, slots %d; want 64, 12, 52", PoolSize, PipBufferCount, SlotCount)
	}
}

func TestPipSheetCodecRoundTrips(t *testing.T) {
	pixels := make([]byte, CustomGlyphPixelsSize)
	for i := range pixels {
		pixels[i] = byte(i % 7)
	}
	colors := []uint16{0x3AFF, 0x16C9, 0xF800}
	payload, err := encodeStorePipSheet(colors, pixels)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeStorePipSheet(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Colors) != 3 || got.Colors[1] != 0x16C9 || !bytes.Equal(got.Pixels, pixels) {
		t.Fatalf("decoded %+v", got.Colors)
	}
	if _, err := encodeStorePipSheet(nil, pixels); err == nil {
		t.Error("a sheet with no colors was accepted")
	}
	if _, err := encodeStorePipSheet(colors, pixels[:10]); err == nil {
		t.Error("a sheet of the wrong size was accepted")
	}
	if _, err := decodeStorePipSheet(payload[:3]); err == nil {
		t.Error("a short payload was decoded")
	}
}

func TestShowFramesPipsCodecRoundTrips(t *testing.T) {
	slots := []byte{0, 5, SlotUnchanged, 51, 2, 3}
	counts := []byte{0, 7, 0, 100, 255, 1}
	payload, err := encodeShowFramesPips(slots, counts)
	if err != nil {
		t.Fatal(err)
	}
	if len(payload) != 12 {
		t.Fatalf("payload is %d bytes, want 2 for each key", len(payload))
	}
	gs, gc, err := decodeShowFramesPips(payload)
	if err != nil || !bytes.Equal(gs, slots) || !bytes.Equal(gc, counts) {
		t.Fatalf("decoded %v %v, %v", gs, gc, err)
	}
	if _, err := encodeShowFramesPips([]byte{SlotCount}, []byte{1}); err == nil {
		t.Error("a slot above the last was accepted")
	}
	if _, err := encodeShowFramesPips([]byte{1, 2}, []byte{1}); err == nil {
		t.Error("slots and counts of different length were accepted")
	}
	if _, _, err := decodeShowFramesPips([]byte{1, 2, 3}); err == nil {
		t.Error("an odd payload was decoded")
	}
}

func TestPipsCrossAnEmulator(t *testing.T) {
	e := NewEmulator()
	defer e.Close()
	pixels := make([]byte, CustomGlyphPixelsSize)
	go func() {
		e.StorePipSheet([]uint16{1, 2}, pixels)
		e.StoreFrame(3, 0xF800, nil)
		e.ShowFramesWithPips([]byte{3, SlotUnchanged}, []byte{42, 0})
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, n := e.LastShowFrames(); n == 1 {
			if _, ok := e.PipSheet(); !ok {
				t.Fatal("the Show arrived before the sheet")
			}
			if c := e.LastShowCounts(); len(c) != 2 || c[0] != 42 {
				t.Fatalf("counts = %v", c)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no Show with pips arrived")
}
