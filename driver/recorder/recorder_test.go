package recorder

import (
	"bytes"
	"os"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// fixedClock hands back one time.Time per call, in order, so a test can
// control the host arrival time a real clock would otherwise make
// nondeterministic.
func fixedClock(times ...time.Time) func() time.Time {
	i := 0
	return func() time.Time {
		t := times[i]
		if i < len(times)-1 {
			i++
		}
		return t
	}
}

func TestRecorderJSONL(t *testing.T) {
	emu := transport.NewEmulator()

	base := time.Date(2026, 8, 15, 12, 0, 0, 0, time.UTC)
	var buf bytes.Buffer
	rec := New(emu, &buf)
	rec.now = fixedClock(
		base,
		base.Add(1*time.Millisecond),
		base.Add(2*time.Millisecond),
	)

	if err := emu.InjectEvent(transport.Event{KeyIndex: 2, Type: transport.EventPress, Timestamp: 1_000_000}); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}
	if err := emu.InjectTraceRecord(transport.TraceRecord{
		Code:      transport.TraceDebounceVerdict,
		Key:       2,
		Payload:   0xFF,
		Timestamp: 1_000_500,
	}); err != nil {
		t.Fatalf("InjectTraceRecord: %v", err)
	}
	if err := emu.InjectPong(0x5A); err != nil {
		t.Fatalf("InjectPong: %v", err)
	}
	if err := emu.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if err := rec.Run(); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := rec.Close(); err != nil {
		t.Fatalf("recorder Close: %v", err)
	}

	golden, err := os.ReadFile("testdata/recorder.golden.jsonl")
	if err != nil {
		t.Fatalf("read golden file: %v", err)
	}
	if buf.String() != string(golden) {
		t.Fatalf("recorder output =\n%s\nwant\n%s", buf.String(), golden)
	}
}

func TestRecorder_EOFIsNotAnError(t *testing.T) {
	emu := transport.NewEmulator()
	var buf bytes.Buffer
	rec := New(emu, &buf)

	if err := emu.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := rec.Run(); err != nil {
		t.Fatalf("Run on a closed transport returned %v, want nil", err)
	}
}
