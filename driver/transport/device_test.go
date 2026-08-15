package transport

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// nopWriteCloser discards writes. It stands in for the HID handle in
// tests that only exercise the CDC read path.
type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

// fakeHIDBackend is a hidBackend test double. listFunc and openFunc let
// each test script discovery without a real hidapi device attached.
type fakeHIDBackend struct {
	listFunc func() ([]hidCandidate, error)
	openFunc func(vid, pid uint16, serialNumber string) (io.WriteCloser, error)
}

func (f *fakeHIDBackend) list(vid, pid uint16) ([]hidCandidate, error) {
	return f.listFunc()
}

func (f *fakeHIDBackend) open(vid, pid uint16, serialNumber string) (io.WriteCloser, error) {
	if f.openFunc != nil {
		return f.openFunc(vid, pid, serialNumber)
	}
	return nopWriteCloser{}, nil
}

// fakeSerialBackend is a serialBackend test double, paired with
// fakeHIDBackend.
type fakeSerialBackend struct {
	findPortFunc func(serialNumber string) (string, error)
	openFunc     func(portName string) (io.ReadCloser, error)
}

func (f *fakeSerialBackend) findPort(serialNumber string) (string, error) {
	if f.findPortFunc != nil {
		return f.findPortFunc(serialNumber)
	}
	return "/dev/cu.usbmodemFAKE", nil
}

func (f *fakeSerialBackend) open(portName string) (io.ReadCloser, error) {
	if f.openFunc != nil {
		return f.openFunc(portName)
	}
	return io.NopCloser(bytes.NewReader(nil)), nil
}

func TestOpen_NoDevice(t *testing.T) {
	hidB := &fakeHIDBackend{listFunc: func() ([]hidCandidate, error) { return nil, nil }}
	serialB := &fakeSerialBackend{}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	opts := Options{VendorID: 0x1234, ProductID: 0x5678, pollInterval: time.Millisecond}

	_, err := open(ctx, opts, hidB, serialB)
	if err == nil {
		t.Fatal("open with no device present returned no error")
	}
	if !strings.Contains(err.Error(), "1234") || !strings.Contains(err.Error(), "5678") {
		t.Fatalf("open error = %q, want it to name vendor ID 1234 and product ID 5678", err)
	}
}

func TestOpen_RespectsDeadline(t *testing.T) {
	hidB := &fakeHIDBackend{listFunc: func() ([]hidCandidate, error) { return nil, nil }}
	serialB := &fakeSerialBackend{}

	const deadline = 50 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()
	opts := Options{VendorID: 1, ProductID: 2, pollInterval: 10 * time.Millisecond}

	start := time.Now()
	_, err := open(ctx, opts, hidB, serialB)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("open with no device present returned no error")
	}
	if elapsed > deadline+200*time.Millisecond {
		t.Fatalf("open took %v after a %v deadline, want it to return within 200ms of the deadline", elapsed, deadline)
	}
}

func TestOpen_RetriesUntilPresent(t *testing.T) {
	var calls int
	hidB := &fakeHIDBackend{
		listFunc: func() ([]hidCandidate, error) {
			calls++
			if calls < 3 {
				return nil, nil
			}
			return []hidCandidate{{SerialNumber: "SN123"}}, nil
		},
	}
	serialB := &fakeSerialBackend{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	opts := Options{VendorID: 0x1234, ProductID: 0x5678, pollInterval: time.Millisecond}

	d, err := open(ctx, opts, hidB, serialB)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer d.Close()

	if calls != 3 {
		t.Fatalf("hid list called %d times before the device was opened, want 3", calls)
	}
}

func TestOpen_AmbiguousDevice(t *testing.T) {
	hidB := &fakeHIDBackend{
		listFunc: func() ([]hidCandidate, error) {
			return []hidCandidate{{SerialNumber: "SN1"}, {SerialNumber: "SN2"}}, nil
		},
	}
	serialB := &fakeSerialBackend{}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	opts := Options{VendorID: 1, ProductID: 2, pollInterval: time.Millisecond}

	_, err := open(ctx, opts, hidB, serialB)
	if !errors.Is(err, ErrAmbiguousDevice) {
		t.Fatalf("open error = %v, want errors.Is(err, ErrAmbiguousDevice)", err)
	}
}

func TestDevice_ReadsMixedStream(t *testing.T) {
	var buf bytes.Buffer
	ev := Event{KeyIndex: 3, Type: EventPress, Timestamp: 42}
	chunk := AudioChunk{StreamID: 1, PCM: []byte{9, 8}, Final: true}

	if err := writeFrame(&buf, MessageTypeEvent, encodeEvent(ev)); err != nil {
		t.Fatalf("writeFrame event: %v", err)
	}
	if err := writeFrame(&buf, MessageTypeAudioChunk, encodeAudioChunk(chunk)); err != nil {
		t.Fatalf("writeFrame audio chunk: %v", err)
	}

	d := newDevice(nopWriteCloser{}, io.NopCloser(&buf))
	defer d.Close()

	got1, err := d.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage 1: %v", err)
	}
	if got1.Type != MessageTypeEvent || got1.Event != ev {
		t.Fatalf("message 1 = %+v, want Event %+v", got1, ev)
	}

	got2, err := d.ReadMessage()
	if err != nil {
		t.Fatalf("ReadMessage 2: %v", err)
	}
	if got2.Type != MessageTypeAudioChunk || !bytes.Equal(got2.AudioChunk.PCM, chunk.PCM) ||
		got2.AudioChunk.StreamID != chunk.StreamID || got2.AudioChunk.Final != chunk.Final {
		t.Fatalf("message 2 = %+v, want AudioChunk %+v", got2, chunk)
	}
}

func TestDevice_CloseUnblocksReader(t *testing.T) {
	r, _ := io.Pipe()
	d := newDevice(nopWriteCloser{}, r)

	errCh := make(chan error, 1)
	go func() {
		_, err := d.ReadMessage()
		errCh <- err
	}()

	// Give the background reader goroutine time to block inside
	// readMessage before Close runs, so this test exercises the unblock
	// path rather than a read that never started.
	time.Sleep(10 * time.Millisecond)

	if err := d.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-errCh:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadMessage after Close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadMessage did not unblock after Close")
	}
}

func TestDevice_SendKeyState(t *testing.T) {
	var written []byte
	hid := &captureWriteCloser{}
	d := newDevice(hid, io.NopCloser(bytes.NewReader(nil)))
	defer d.Close()

	ks := KeyState{KeyIndex: 2, Version: ProtocolVersion, Color: 0xF800, EmojiID: 7, Blink: true}
	if err := d.SendKeyState(ks); err != nil {
		t.Fatalf("SendKeyState: %v", err)
	}

	written = hid.written
	want := []byte{keyStateReportID, 2, ProtocolVersion, 0x00, 0xF8, 7, 1}
	if !bytes.Equal(written, want) {
		t.Fatalf("HID write = % x, want % x", written, want)
	}
}

type captureWriteCloser struct {
	written []byte
}

func (c *captureWriteCloser) Write(p []byte) (int, error) {
	c.written = append(c.written, p...)
	return len(p), nil
}

func (c *captureWriteCloser) Close() error { return nil }
