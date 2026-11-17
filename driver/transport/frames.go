package transport

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// PoolSize is the number of 32 KB buffers the board allocates for frames:
// 64, the cap of task 0063. PipBufferCount of them are overlay buffers for the
// badges (task 0062 called them pip buffers), two for each key, and the rest
// are the slots the host numbers.
const (
	PoolSize       = 64
	PipBufferCount = 12
)

// SlotCount is the number of slots in the board's frame cache. A slot number
// is 0 to SlotCount-1. See "Store frame" in docs/wire-protocol.md (tasks 0061
// and 0062).
const SlotCount = PoolSize - PipBufferCount

// SlotUnchanged is the Show frames value that leaves a key as it is.
const SlotUnchanged = 0xFF

// ErrInvalidSlot is returned for a slot number that is neither below
// SlotCount nor, in a Show frames message, SlotUnchanged.
var ErrInvalidSlot = errors.New("transport: invalid frame slot")

// ErrFrameTooLarge is returned by StoreFrame for a glyph whose zlib stream
// does not fit one frame, which is a glyph that does not compress. The caller
// draws that key with SendCustomGlyph and SendKeyState instead.
var ErrFrameTooLarge = errors.New("transport: glyph does not compress enough for a store frame")

// ShowMiss is a device→host message: bit k of Mask is key k, which a Show
// frames message named an empty slot for. The board drew the other keys and
// left key k as it was. See "Show miss" in docs/wire-protocol.md (task 0061).
type ShowMiss struct {
	Mask byte
}

// StoredFrame is one decoded Store frame message.
type StoredFrame struct {
	Slot   byte
	Color  uint16
	Pixels []byte // nil for a frame of the color alone
}

// storeFrameHeader is the slot and the color, the bytes of a Store frame
// payload before the zlib stream.
const storeFrameHeader = 3

const showMissSize = 1

// maxStoreFrameStream is the largest zlib stream a Store frame carries: the
// frame's payload is at most as long as a Set custom glyph payload, and the
// slot and color take 3 bytes of it.
const maxStoreFrameStream = customGlyphPayload - storeFrameHeader

// encodeStoreFrame builds a Store frame's payload: the slot, the color
// (little-endian), then a zlib stream of the glyph. A nil pixels is a frame of
// the color alone, with no stream. Returns ErrInvalidSlot for a slot of
// SlotCount or more, ErrInvalidGlyphSize for pixels that are not exactly
// CustomGlyphPixelsSize bytes, and ErrFrameTooLarge for a glyph whose stream
// does not fit the frame, which is a glyph that does not compress.
func encodeStoreFrame(slot byte, color uint16, pixels []byte) ([]byte, error) {
	if slot >= SlotCount {
		return nil, fmt.Errorf("%w: %d, want 0 to %d", ErrInvalidSlot, slot, SlotCount-1)
	}
	buf := make([]byte, storeFrameHeader, storeFrameHeader+1024)
	buf[0] = slot
	binary.LittleEndian.PutUint16(buf[1:3], color)
	if pixels == nil {
		return buf, nil
	}
	if len(pixels) != CustomGlyphPixelsSize {
		return nil, fmt.Errorf("%w: got %d bytes, want %d", ErrInvalidGlyphSize, len(pixels), CustomGlyphPixelsSize)
	}
	out := bytes.NewBuffer(buf)
	zw, err := zlib.NewWriterLevel(out, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(pixels); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if stream := out.Len() - storeFrameHeader; stream > maxStoreFrameStream {
		return nil, fmt.Errorf("%w: stream is %d bytes, at most %d", ErrFrameTooLarge, stream, maxStoreFrameStream)
	}
	return out.Bytes(), nil
}

func decodeStoreFrame(payload []byte) (StoredFrame, error) {
	if len(payload) < storeFrameHeader {
		return StoredFrame{}, fmt.Errorf("transport: store frame payload is %d bytes, want at least %d", len(payload), storeFrameHeader)
	}
	f := StoredFrame{Slot: payload[0], Color: binary.LittleEndian.Uint16(payload[1:3])}
	if len(payload) == storeFrameHeader {
		return f, nil
	}
	zr, err := zlib.NewReader(bytes.NewReader(payload[storeFrameHeader:]))
	if err != nil {
		return StoredFrame{}, fmt.Errorf("transport: store frame: %w", err)
	}
	defer zr.Close()
	pixels, err := io.ReadAll(io.LimitReader(zr, CustomGlyphPixelsSize+1))
	if err != nil {
		return StoredFrame{}, fmt.Errorf("transport: store frame: %w", err)
	}
	if len(pixels) != CustomGlyphPixelsSize {
		return StoredFrame{}, fmt.Errorf("%w: inflated to %d bytes, want %d", ErrInvalidGlyphSize, len(pixels), CustomGlyphPixelsSize)
	}
	f.Pixels = pixels
	return f, nil
}

// encodeShowFrames builds a Show frames payload: one byte for each key, key 0
// first. A byte below SlotCount is the slot the key shows, and SlotUnchanged
// leaves the key alone. Returns ErrInvalidSlot for any other value, and for a
// payload with no key.
func encodeShowFrames(slots []byte) ([]byte, error) {
	if len(slots) == 0 {
		return nil, fmt.Errorf("%w: no key", ErrInvalidSlot)
	}
	for key, slot := range slots {
		if slot >= SlotCount && slot != SlotUnchanged {
			return nil, fmt.Errorf("%w: key %d names %d, want 0 to %d or %d", ErrInvalidSlot, key, slot, SlotCount-1, SlotUnchanged)
		}
	}
	return append([]byte(nil), slots...), nil
}

func decodeShowFrames(payload []byte) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("transport: show frames payload is empty")
	}
	return payload, nil
}

func encodeShowMiss(mask byte) []byte { return []byte{mask} }

// BatchIncomplete is a device→host message: bit k of GlyphMask is key k, which
// an open batch still waits for a glyph for, and bit k of StateMask is key k,
// which it still waits for a key state for. The host sends those items again.
// See "Batch incomplete" in docs/wire-protocol.md (task 0063).
type BatchIncomplete struct {
	GlyphMask byte
	StateMask byte
}

func encodeBatchIncomplete(bi BatchIncomplete) []byte {
	return []byte{bi.GlyphMask, bi.StateMask}
}

func decodeBatchIncomplete(payload []byte) (BatchIncomplete, error) {
	if len(payload) != batchIncompleteSize {
		return BatchIncomplete{}, fmt.Errorf("transport: batch incomplete payload is %d bytes, want %d", len(payload), batchIncompleteSize)
	}
	return BatchIncomplete{GlyphMask: payload[0], StateMask: payload[1]}, nil
}

func decodeShowMiss(payload []byte) (byte, error) {
	if len(payload) != showMissSize {
		return 0, fmt.Errorf("transport: show miss payload is %d bytes, want %d", len(payload), showMissSize)
	}
	return payload[0], nil
}

// The badge sheet (task 0091) is a 256 by 150 image in the pixel format of a
// custom glyph: 76,800 bytes. The layout of its sprites is the contract of
// driver/layers/pip.go and firmware/display_render.py, and "Store badge
// sheet" in docs/wire-protocol.md describes it.
const (
	BadgeSheetWidth      = 256
	BadgeSheetHeight     = 150
	BadgeSheetPixelsSize = BadgeSheetWidth * BadgeSheetHeight * 2
	// MaxBadgeColors is the most background colors a Store badge sheet names:
	// one column of discs for each.
	MaxBadgeColors = 5
)

// Badges is the three counts of a key (task 0091): the red, the green, and the
// yellow disc. A count of 0 draws no disc. A count of 100 or more draws "99+".
type Badges struct {
	Red, Green, Yellow byte
}

// Any reports whether at least one of the three discs is drawn.
func (b Badges) Any() bool { return b.Red > 0 || b.Green > 0 || b.Yellow > 0 }

// BadgeSheet is one decoded Store badge sheet message: the key color each
// column of discs is blended against, and the sheet's pixels, a 256 by 150 image
// in the pixel format of a custom glyph. See "Store badge sheet" in
// docs/wire-protocol.md (task 0091).
type BadgeSheet struct {
	Colors []uint16
	Pixels []byte
}

// encodeStoreBadgeSheet builds a Store badge sheet payload: the number of
// background colors, the colors (little-endian), then a zlib stream of the
// pixels.
func encodeStoreBadgeSheet(colors []uint16, pixels []byte) ([]byte, error) {
	if len(colors) == 0 || len(colors) > MaxBadgeColors {
		return nil, fmt.Errorf("transport: badge sheet names %d colors, want 1 to %d", len(colors), MaxBadgeColors)
	}
	if len(pixels) != BadgeSheetPixelsSize {
		return nil, fmt.Errorf("%w: badge sheet is %d bytes, want %d", ErrInvalidGlyphSize, len(pixels), BadgeSheetPixelsSize)
	}
	buf := make([]byte, 1+2*len(colors), 1+2*len(colors)+4096)
	buf[0] = byte(len(colors))
	for i, c := range colors {
		binary.LittleEndian.PutUint16(buf[1+2*i:], c)
	}
	out := bytes.NewBuffer(buf)
	zw, err := zlib.NewWriterLevel(out, zlib.BestCompression)
	if err != nil {
		return nil, err
	}
	if _, err := zw.Write(pixels); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	if out.Len() > customGlyphPayload {
		return nil, fmt.Errorf("%w: badge sheet is %d bytes", ErrFrameTooLarge, out.Len())
	}
	return out.Bytes(), nil
}

func decodeStoreBadgeSheet(payload []byte) (BadgeSheet, error) {
	if len(payload) < 1 {
		return BadgeSheet{}, errors.New("transport: badge sheet payload is empty")
	}
	n := int(payload[0])
	if n == 0 || n > MaxBadgeColors || len(payload) <= 1+2*n {
		return BadgeSheet{}, fmt.Errorf("transport: badge sheet payload is %d bytes for %d colors", len(payload), n)
	}
	sheet := BadgeSheet{Colors: make([]uint16, n)}
	for i := range sheet.Colors {
		sheet.Colors[i] = binary.LittleEndian.Uint16(payload[1+2*i:])
	}
	zr, err := zlib.NewReader(bytes.NewReader(payload[1+2*n:]))
	if err != nil {
		return BadgeSheet{}, fmt.Errorf("transport: badge sheet: %w", err)
	}
	defer zr.Close()
	pixels, err := io.ReadAll(io.LimitReader(zr, BadgeSheetPixelsSize+1))
	if err != nil {
		return BadgeSheet{}, fmt.Errorf("transport: badge sheet: %w", err)
	}
	if len(pixels) != BadgeSheetPixelsSize {
		return BadgeSheet{}, fmt.Errorf("%w: badge sheet inflated to %d bytes, want %d", ErrInvalidGlyphSize, len(pixels), BadgeSheetPixelsSize)
	}
	sheet.Pixels = pixels
	return sheet, nil
}

// encodeShowFramesBadges builds a Show frames with badges payload: four bytes
// for each key, key 0 first: the slot (or SlotUnchanged), the red count, the
// green count, and the yellow count. slots and badges have one entry for each
// key.
func encodeShowFramesBadges(slots []byte, badges []Badges) ([]byte, error) {
	if len(slots) == 0 {
		return nil, fmt.Errorf("%w: no key", ErrInvalidSlot)
	}
	if len(badges) != len(slots) {
		return nil, fmt.Errorf("transport: %d slots but %d badge sets", len(slots), len(badges))
	}
	if _, err := encodeShowFrames(slots); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 4*len(slots))
	for i, slot := range slots {
		out = append(out, slot, badges[i].Red, badges[i].Green, badges[i].Yellow)
	}
	return out, nil
}

func decodeShowFramesBadges(payload []byte) (slots []byte, badges []Badges, err error) {
	if len(payload) == 0 || len(payload)%4 != 0 {
		return nil, nil, fmt.Errorf("transport: show frames with badges payload is %d bytes, want 4 for each key", len(payload))
	}
	for i := 0; i < len(payload); i += 4 {
		slots = append(slots, payload[i])
		badges = append(badges, Badges{Red: payload[i+1], Green: payload[i+2], Yellow: payload[i+3]})
	}
	return slots, badges, nil
}
