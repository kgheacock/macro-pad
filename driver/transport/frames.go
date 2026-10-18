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
// 64, the cap of task 0063. PipBufferCount of them are pip buffers, two for
// each key, and the rest are the slots the host numbers.
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

// MaxPipSprites is the most key colors a Store pip sheet names: one disc for
// each.
const MaxPipSprites = 8

// PipSheet is one decoded Store pip sheet message: the key color of each disc
// of the sheet, and the sheet's pixels, a 256 by 64 image in the pixel format
// of a custom glyph. See "Store pip sheet" in docs/wire-protocol.md (task
// 0062).
type PipSheet struct {
	Colors []uint16
	Pixels []byte
}

// encodeStorePipSheet builds a Store pip sheet payload: the number of discs,
// their key colors (little-endian), then a zlib stream of the pixels. The
// pixels are CustomGlyphPixelsSize bytes, the same size as a glyph, so the
// stream fits one frame whenever a glyph's does.
func encodeStorePipSheet(colors []uint16, pixels []byte) ([]byte, error) {
	if len(colors) == 0 || len(colors) > MaxPipSprites {
		return nil, fmt.Errorf("transport: pip sheet names %d colors, want 1 to %d", len(colors), MaxPipSprites)
	}
	if len(pixels) != CustomGlyphPixelsSize {
		return nil, fmt.Errorf("%w: pip sheet is %d bytes, want %d", ErrInvalidGlyphSize, len(pixels), CustomGlyphPixelsSize)
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
	if out.Len() > customGlyphPayload-1 {
		return nil, fmt.Errorf("%w: pip sheet is %d bytes", ErrFrameTooLarge, out.Len())
	}
	return out.Bytes(), nil
}

func decodeStorePipSheet(payload []byte) (PipSheet, error) {
	if len(payload) < 1 {
		return PipSheet{}, errors.New("transport: pip sheet payload is empty")
	}
	n := int(payload[0])
	if n == 0 || n > MaxPipSprites || len(payload) <= 1+2*n {
		return PipSheet{}, fmt.Errorf("transport: pip sheet payload is %d bytes for %d colors", len(payload), n)
	}
	sheet := PipSheet{Colors: make([]uint16, n)}
	for i := range sheet.Colors {
		sheet.Colors[i] = binary.LittleEndian.Uint16(payload[1+2*i:])
	}
	zr, err := zlib.NewReader(bytes.NewReader(payload[1+2*n:]))
	if err != nil {
		return PipSheet{}, fmt.Errorf("transport: pip sheet: %w", err)
	}
	defer zr.Close()
	pixels, err := io.ReadAll(io.LimitReader(zr, CustomGlyphPixelsSize+1))
	if err != nil {
		return PipSheet{}, fmt.Errorf("transport: pip sheet: %w", err)
	}
	if len(pixels) != CustomGlyphPixelsSize {
		return PipSheet{}, fmt.Errorf("%w: pip sheet inflated to %d bytes, want %d", ErrInvalidGlyphSize, len(pixels), CustomGlyphPixelsSize)
	}
	sheet.Pixels = pixels
	return sheet, nil
}

// encodeShowFramesPips builds a Show frames with pips payload: two bytes for
// each key, key 0 first, the slot (or SlotUnchanged) and the count. slots and
// counts have one entry for each key.
func encodeShowFramesPips(slots, counts []byte) ([]byte, error) {
	if len(slots) == 0 {
		return nil, fmt.Errorf("%w: no key", ErrInvalidSlot)
	}
	if len(counts) != len(slots) {
		return nil, fmt.Errorf("transport: %d slots but %d counts", len(slots), len(counts))
	}
	if _, err := encodeShowFrames(slots); err != nil {
		return nil, err
	}
	out := make([]byte, 0, 2*len(slots))
	for i, slot := range slots {
		out = append(out, slot, counts[i])
	}
	return out, nil
}

func decodeShowFramesPips(payload []byte) (slots, counts []byte, err error) {
	if len(payload) == 0 || len(payload)%2 != 0 {
		return nil, nil, fmt.Errorf("transport: show frames with pips payload is %d bytes, want 2 for each key", len(payload))
	}
	for i := 0; i < len(payload); i += 2 {
		slots = append(slots, payload[i])
		counts = append(counts, payload[i+1])
	}
	return slots, counts, nil
}
