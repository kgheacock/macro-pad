package transport

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// SlotCount is the number of slots in the board's frame cache. A slot number
// is 0 to SlotCount-1. See "Store frame" in docs/wire-protocol.md (task 0061).
const SlotCount = 64

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
