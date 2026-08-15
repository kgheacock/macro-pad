package transport

import (
	"io"
	"testing"
	"time"
)

func TestFanout_DeliversToEverySubscriber(t *testing.T) {
	emu := NewEmulator()
	defer emu.Close()

	f := NewFanout(emu)
	go f.Run()

	a := f.Subscribe()
	b := f.Subscribe()

	ev := Event{KeyIndex: 3, Type: EventPress, Timestamp: 42}
	if err := emu.InjectEvent(ev); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}

	for name, sub := range map[string]Transport{"a": a, "b": b} {
		msg, err := readWithTimeout(t, sub)
		if err != nil {
			t.Fatalf("subscriber %s: ReadMessage: %v", name, err)
		}
		if msg.Type != MessageTypeEvent || msg.Event != ev {
			t.Fatalf("subscriber %s got %+v, want event %+v", name, msg, ev)
		}
	}
}

func TestFanout_SlowSubscriberDoesNotBlockAnother(t *testing.T) {
	emu := NewEmulator()
	defer emu.Close()

	f := NewFanout(emu)
	go f.Run()

	slow := f.Subscribe() // never read from
	healthy := f.Subscribe()

	const n = fanoutQueueSize + 5

	// Drain healthy concurrently with injection, so its own bounded queue
	// never fills — the point of the test is whether slow's full queue
	// stalls delivery to healthy, not whether healthy can out-buffer a
	// burst on its own. Errors travel back over a channel instead of
	// calling t.Fatal directly: this goroutine is not the test goroutine.
	got := make(chan error, 1)
	go func() {
		for i := 0; i < n; i++ {
			if _, err := healthy.ReadMessage(); err != nil {
				got <- err
				return
			}
		}
		got <- nil
	}()

	for i := 0; i < n; i++ {
		ev := Event{KeyIndex: byte(i % 6), Type: EventPress, Timestamp: uint64(i)}
		if err := emu.InjectEvent(ev); err != nil {
			t.Fatalf("InjectEvent %d: %v", i, err)
		}
	}

	select {
	case err := <-got:
		if err != nil {
			t.Fatalf("healthy subscriber: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for healthy subscriber to receive all messages")
	}

	_ = slow // deliberately undrained, proving it did not stall healthy
}

func TestFanout_UnderlyingCloseEOFsSubscribers(t *testing.T) {
	emu := NewEmulator()

	f := NewFanout(emu)
	go f.Run()

	sub := f.Subscribe()

	if err := emu.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	deadline := time.After(time.Second)
	for {
		_, err := sub.ReadMessage()
		if err == io.EOF {
			return
		}
		if err != nil {
			t.Fatalf("ReadMessage: got %v, want io.EOF", err)
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for io.EOF")
		default:
		}
	}
}

func TestFanout_SubscriberCloseUnblocksItsOwnRead(t *testing.T) {
	emu := NewEmulator()
	defer emu.Close()

	f := NewFanout(emu)
	go f.Run()

	sub := f.Subscribe()

	done := make(chan error, 1)
	go func() {
		_, err := sub.ReadMessage()
		done <- err
	}()

	if err := sub.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	select {
	case err := <-done:
		if err != io.EOF {
			t.Fatalf("ReadMessage after Close returned %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for the blocked ReadMessage to unblock")
	}
}

func readWithTimeout(t *testing.T, tr Transport) (Message, error) {
	t.Helper()
	type result struct {
		msg Message
		err error
	}
	ch := make(chan result, 1)
	go func() {
		msg, err := tr.ReadMessage()
		ch <- result{msg, err}
	}()
	select {
	case r := <-ch:
		return r.msg, r.err
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for ReadMessage")
		return Message{}, nil
	}
}
