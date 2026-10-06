package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"reflect"
	"sync"
	"testing"
	"time"
)

// fakeBoard is a Transport that records what it is sent and delivers the
// messages a test hands it. disconnect makes ReadMessage return io.EOF, as a
// Device does when its CDC port goes away.
type fakeBoard struct {
	mu    sync.Mutex
	calls []string

	in   chan Message
	once sync.Once
}

func newFakeBoard() *fakeBoard { return &fakeBoard{in: make(chan Message, 8)} }

func (b *fakeBoard) record(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.calls = append(b.calls, fmt.Sprintf(format, args...))
}

func (b *fakeBoard) sent() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.calls...)
}

func (b *fakeBoard) SendKeyState(ks KeyState) error {
	b.record("key %d color %#04x emoji %d blink %v", ks.KeyIndex, ks.Color, ks.EmojiID, ks.Blink)
	return nil
}

func (b *fakeBoard) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	b.record("glyph %d %d bytes", keyIndex, len(pixels))
	return nil
}

func (b *fakeBoard) ReadMessage() (Message, error) {
	msg, ok := <-b.in
	if !ok {
		return Message{}, io.EOF
	}
	return msg, nil
}

func (b *fakeBoard) Close() error {
	b.disconnect()
	return nil
}

func (b *fakeBoard) disconnect() { b.once.Do(func() { close(b.in) }) }

// boards hands newReconnecting one fake board each time it opens, in the
// order a test gives them, and blocks like Open does while there is none.
type boards chan *fakeBoard

func (bs boards) open(ctx context.Context) (Transport, error) {
	select {
	case b := <-bs:
		return b, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func key(index byte, color uint16, emoji byte, blink bool) KeyState {
	return KeyState{KeyIndex: index, Version: ProtocolVersion, Color: color, EmojiID: emoji, Blink: blink}
}

func TestReconnecting_ReplaysEachKeysLastStateAfterAReconnect(t *testing.T) {
	bs := make(boards, 2)
	first, second := newFakeBoard(), newFakeBoard()
	bs <- first
	r := newReconnecting(bs.open, 0, nil)
	defer r.Close()

	waitFor(t, "the first connect", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.dev != nil })
	r.SendKeyState(key(0, 0x001F, 0, false))
	r.SendKeyState(key(0, 0xF800, 0, true)) // replaces the first
	r.SendKeyState(key(1, 0x07E0, 3, false))
	r.SendCustomGlyph(2, make([]byte, 8))
	r.SendKeyState(key(2, 0xFFFF, CustomGlyphSentinelEmojiID, true))

	first.disconnect()
	bs <- second
	waitFor(t, "the replay", func() bool { return len(second.sent()) >= 4 })

	want := []string{
		"key 0 color 0xf800 emoji 0 blink true",
		"key 1 color 0x07e0 emoji 3 blink false",
		"glyph 2 8 bytes",
		"key 2 color 0xffff emoji 254 blink true",
	}
	if got := second.sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
}

func TestReconnecting_ABuiltInEmojiEndsTheGlyphsReplay(t *testing.T) {
	bs := make(boards, 2)
	first, second := newFakeBoard(), newFakeBoard()
	bs <- first
	r := newReconnecting(bs.open, 0, nil)
	defer r.Close()
	waitFor(t, "the first connect", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.dev != nil })

	r.SendCustomGlyph(0, make([]byte, 8))
	r.SendKeyState(key(0, 0x001F, 7, false)) // a built-in Emoji ID replaces the image

	first.disconnect()
	bs <- second
	waitFor(t, "the replay", func() bool { return len(second.sent()) >= 1 })

	want := []string{"key 0 color 0x001f emoji 7 blink false"}
	if got := second.sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
}

func TestReconnecting_ASendWhileTheBoardIsAbsentIsReplayed(t *testing.T) {
	bs := make(boards, 1)
	r := newReconnecting(bs.open, 0, nil)
	defer r.Close()

	if err := r.SendKeyState(key(4, 0xF800, 0, false)); err != nil {
		t.Fatalf("SendKeyState with no board: %v", err)
	}

	board := newFakeBoard()
	bs <- board
	waitFor(t, "the replay", func() bool { return len(board.sent()) >= 1 })

	want := []string{"key 4 color 0xf800 emoji 0 blink false"}
	if got := board.sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
}

func TestReconnecting_MessagesPassThroughAcrossReconnects(t *testing.T) {
	bs := make(boards, 2)
	first, second := newFakeBoard(), newFakeBoard()
	bs <- first
	r := newReconnecting(bs.open, 0, nil)
	defer r.Close()

	first.in <- Message{Type: MessageTypeEvent, Event: Event{KeyIndex: 1}}
	first.disconnect()
	bs <- second
	second.in <- Message{Type: MessageTypeEvent, Event: Event{KeyIndex: 2}}

	for _, want := range []byte{1, 2} {
		msg, err := r.ReadMessage()
		if err != nil {
			t.Fatalf("ReadMessage: %v", err)
		}
		if msg.Event.KeyIndex != want {
			t.Fatalf("event key = %d, want %d", msg.Event.KeyIndex, want)
		}
	}
}

func TestReconnecting_CloseEndsReadMessage(t *testing.T) {
	bs := make(boards, 1)
	bs <- newFakeBoard()
	r := newReconnecting(bs.open, 0, nil)

	done := make(chan error, 1)
	go func() { _, err := r.ReadMessage(); done <- err }()
	r.Close()

	select {
	case err := <-done:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("ReadMessage after Close = %v, want io.EOF", err)
		}
	case <-time.After(time.Second):
		t.Fatal("ReadMessage did not unblock after Close")
	}
}
