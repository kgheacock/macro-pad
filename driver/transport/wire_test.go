package transport

import (
	"bytes"
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
