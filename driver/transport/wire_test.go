package transport

import (
	"bytes"
	"compress/zlib"
	"errors"
	"testing"
)

func TestPong_RoundTrip(t *testing.T) {
	nonce := byte(0x5A)

	var buf bytes.Buffer
	if err := writeFrame(&buf, MessageTypePong, encodePong(nonce)); err != nil {
		t.Fatalf("writeFrame pong: %v", err)
	}

	msg, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Type != MessageTypePong || msg.Pong.Nonce != nonce {
		t.Fatalf("readMessage = %+v, want Pong{Nonce: %#x}", msg, nonce)
	}
}

func TestPong_WrongSizeRejected(t *testing.T) {
	if _, err := decodePong([]byte{1, 2}); err == nil {
		t.Fatal("decodePong with a 2-byte payload returned no error")
	}
}

func TestTraceRecord_RoundTrip(t *testing.T) {
	want := TraceRecord{Code: TraceDebounceVerdict, Key: 3, Payload: 0xFF, Timestamp: 0x0102030405060708}

	var buf bytes.Buffer
	if err := writeFrame(&buf, MessageTypeTrace, encodeTraceRecord(want)); err != nil {
		t.Fatalf("writeFrame trace record: %v", err)
	}

	msg, err := readMessage(&buf)
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	if msg.Type != MessageTypeTrace || msg.Trace != want {
		t.Fatalf("readMessage = %+v, want Trace{%+v}", msg, want)
	}
}

func TestTraceRecord_WrongSizeRejected(t *testing.T) {
	if _, err := decodeTraceRecord([]byte{1, 2, 3}); err == nil {
		t.Fatal("decodeTraceRecord with a 3-byte payload returned no error")
	}
}

func TestCustomGlyph_RoundTrip(t *testing.T) {
	pixels := bytes.Repeat([]byte{0xAB, 0xCD}, CustomGlyphPixelsSize/2)

	payload, err := encodeCustomGlyph(4, pixels)
	if err != nil {
		t.Fatalf("encodeCustomGlyph: %v", err)
	}

	var buf bytes.Buffer
	if err := writeFrame(&buf, MessageTypeSetCustomGlyph, payload); err != nil {
		t.Fatalf("writeFrame custom glyph: %v", err)
	}

	frameType, framePayload, err := readFrame(&buf)
	if err != nil {
		t.Fatalf("readFrame: %v", err)
	}
	if frameType != MessageTypeSetCustomGlyph {
		t.Fatalf("frame type = %v, want MessageTypeSetCustomGlyph", frameType)
	}

	got, err := decodeCustomGlyph(framePayload)
	if err != nil {
		t.Fatalf("decodeCustomGlyph: %v", err)
	}
	if got.KeyIndex != 4 || !bytes.Equal(got.Pixels, pixels) {
		t.Fatalf("decodeCustomGlyph = %+v, want KeyIndex 4 and matching pixels", got)
	}
}

func TestCustomGlyph_EncodeRejectsWrongPixelSize(t *testing.T) {
	if _, err := encodeCustomGlyph(0, make([]byte, CustomGlyphPixelsSize-1)); !errors.Is(err, ErrInvalidGlyphSize) {
		t.Fatalf("encodeCustomGlyph with a short pixel buffer = %v, want errors.Is(err, ErrInvalidGlyphSize)", err)
	}
}

func TestCustomGlyph_DecodeRejectsWrongSize(t *testing.T) {
	if _, err := decodeCustomGlyph([]byte{1, 2, 3}); err == nil {
		t.Fatal("decodeCustomGlyph with a 3-byte payload returned no error")
	}
}

// noisePixels returns pixels that do not deflate.
func noisePixels() []byte {
	pixels := make([]byte, CustomGlyphPixelsSize)
	state := uint32(1)
	for i := range pixels {
		state = state*1664525 + 1013904223
		pixels[i] = byte(state >> 24)
	}
	return pixels
}

func TestCustomGlyphFrame_CompressesAFlatGlyph(t *testing.T) {
	pixels := bytes.Repeat([]byte{0xAB, 0xCD}, CustomGlyphPixelsSize/2)

	frameType, payload, err := encodeCustomGlyphFrame(4, pixels)
	if err != nil {
		t.Fatalf("encodeCustomGlyphFrame: %v", err)
	}
	if frameType != MessageTypeSetCustomGlyphCompressed {
		t.Fatalf("frame type = %v, want MessageTypeSetCustomGlyphCompressed", frameType)
	}
	if len(payload) >= customGlyphPayload/10 {
		t.Fatalf("payload is %d bytes, want under %d for a flat glyph", len(payload), customGlyphPayload/10)
	}

	got, err := decodeCompressedCustomGlyph(payload)
	if err != nil {
		t.Fatalf("decodeCompressedCustomGlyph: %v", err)
	}
	if got.KeyIndex != 4 || !bytes.Equal(got.Pixels, pixels) {
		t.Fatalf("decodeCompressedCustomGlyph = key %d, want key 4 and matching pixels", got.KeyIndex)
	}
}

func TestCustomGlyphFrame_SendsAGlyphThatDoesNotCompressAsType5(t *testing.T) {
	pixels := noisePixels()

	frameType, payload, err := encodeCustomGlyphFrame(2, pixels)
	if err != nil {
		t.Fatalf("encodeCustomGlyphFrame: %v", err)
	}
	if frameType != MessageTypeSetCustomGlyph {
		t.Fatalf("frame type = %v, want MessageTypeSetCustomGlyph", frameType)
	}
	got, err := decodeCustomGlyph(payload)
	if err != nil {
		t.Fatalf("decodeCustomGlyph: %v", err)
	}
	if got.KeyIndex != 2 || !bytes.Equal(got.Pixels, pixels) {
		t.Fatalf("decodeCustomGlyph = key %d, want key 2 and matching pixels", got.KeyIndex)
	}
}

func TestCustomGlyphFrame_RejectsWrongPixelSize(t *testing.T) {
	if _, _, err := encodeCustomGlyphFrame(0, make([]byte, CustomGlyphPixelsSize-1)); !errors.Is(err, ErrInvalidGlyphSize) {
		t.Fatalf("encodeCustomGlyphFrame with a short pixel buffer = %v, want errors.Is(err, ErrInvalidGlyphSize)", err)
	}
}

func TestCompressedCustomGlyph_RejectsAStreamOfTheWrongInflatedSize(t *testing.T) {
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	zw.Write(make([]byte, CustomGlyphPixelsSize-2))
	zw.Close()

	_, err := decodeCompressedCustomGlyph(append([]byte{1}, buf.Bytes()...))
	if !errors.Is(err, ErrInvalidGlyphSize) {
		t.Fatalf("decodeCompressedCustomGlyph = %v, want errors.Is(err, ErrInvalidGlyphSize)", err)
	}
	if _, err := decodeCompressedCustomGlyph([]byte{1, 0x78, 0x9c, 0xff}); err == nil {
		t.Fatal("decodeCompressedCustomGlyph with a corrupt stream returned no error")
	}
}
