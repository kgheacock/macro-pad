package transport

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
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
	r := newReconnecting(bs.open, 0, "", nil)
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
	r := newReconnecting(bs.open, 0, "", nil)
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
	r := newReconnecting(bs.open, 0, "", nil)
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
	r := newReconnecting(bs.open, 0, "", nil)
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
	r := newReconnecting(bs.open, 0, "", nil)

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

func fullGlyph(fill byte) []byte {
	pixels := make([]byte, CustomGlyphPixelsSize)
	for i := range pixels {
		pixels[i] = fill
	}
	return pixels
}

// connected opens a Reconnecting on dir whose first board is already
// attached, and waits for the first connect.
func connected(t *testing.T, dir string, logf func(string, ...any)) (*Reconnecting, *fakeBoard) {
	t.Helper()
	bs := make(boards, 1)
	board := newFakeBoard()
	bs <- board
	r := newReconnecting(bs.open, 0, dir, logf)
	waitFor(t, "the connect", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.dev != nil })
	return r, board
}

func TestReconnecting_SavesStateFilesAReaderCanCheck(t *testing.T) {
	dir := t.TempDir()
	r, _ := connected(t, dir, nil)
	defer r.Close()

	r.SendKeyState(key(0, 0xF800, 0, true))
	r.SendKeyState(key(1, 0x07E0, 3, false))
	glyph := fullGlyph(0xAB)
	r.SendCustomGlyph(2, glyph)
	r.SendKeyState(key(2, 0x001F, CustomGlyphSentinelEmojiID, false))

	raw, err := os.ReadFile(filepath.Join(dir, "keys.json"))
	if err != nil {
		t.Fatal(err)
	}
	var got storedState
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("keys.json does not parse: %v\n%s", err, raw)
	}
	want := []storedKey{
		{KeyIndex: 0, Color: 0xF800, EmojiID: 0, Blink: true},
		{KeyIndex: 1, Color: 0x07E0, EmojiID: 3},
		{KeyIndex: 2, Color: 0x001F, EmojiID: CustomGlyphSentinelEmojiID},
	}
	if !reflect.DeepEqual(got.Keys, want) {
		t.Fatalf("keys.json keys = %+v, want %+v", got.Keys, want)
	}

	pixels, err := os.ReadFile(filepath.Join(dir, "glyph-2.bin"))
	if err != nil || !bytes.Equal(pixels, glyph) {
		t.Fatalf("glyph-2.bin: err %v, %d bytes, want the glyph's %d bytes", err, len(pixels), len(glyph))
	}

	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Fatalf("temporary file %s left behind", e.Name())
		}
	}
}

func TestReconnecting_ALaterRunReplaysSavedStateToTheFirstBoard(t *testing.T) {
	dir := t.TempDir()
	r, _ := connected(t, dir, nil)
	r.SendKeyState(key(0, 0xF800, 0, true))
	r.SendCustomGlyph(2, fullGlyph(0x11))
	r.SendKeyState(key(2, 0x001F, CustomGlyphSentinelEmojiID, false))
	r.Close()

	// A new daemon: nothing is sent before the board connects.
	r2, board := connected(t, dir, nil)
	defer r2.Close()
	waitFor(t, "the replay", func() bool { return len(board.sent()) >= 3 })

	want := []string{
		"key 0 color 0xf800 emoji 0 blink true",
		"glyph 2 32768 bytes",
		"key 2 color 0x001f emoji 254 blink false",
	}
	if got := board.sent(); !reflect.DeepEqual(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
}

func TestReconnecting_ABuiltInEmojiRemovesTheGlyphFile(t *testing.T) {
	dir := t.TempDir()
	r, _ := connected(t, dir, nil)
	defer r.Close()

	r.SendCustomGlyph(0, fullGlyph(0x22))
	if _, err := os.Stat(filepath.Join(dir, "glyph-0.bin")); err != nil {
		t.Fatalf("glyph file after a glyph: %v", err)
	}
	r.SendKeyState(key(0, 0x001F, 7, false))

	if _, err := os.Stat(filepath.Join(dir, "glyph-0.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("glyph file after a built-in Emoji ID: err %v, want it removed", err)
	}
}

func TestReconnecting_AMissingGlyphFileKeepsTheColorAndBlink(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keys.json"),
		[]byte(`{"keys":[{"key_index":1,"color":63488,"emoji_id":254,"blink":true}]}`), 0o644)
	var logs []string
	var mu sync.Mutex
	logf := func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(format, args...))
	}

	r, board := connected(t, dir, logf)
	defer r.Close()
	waitFor(t, "the replay", func() bool { return len(board.sent()) >= 1 })

	if got, want := board.sent(), []string{"key 1 color 0xf800 emoji 0 blink true"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("replay = %q, want %q", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(strings.Join(logs, "\n"), "glyph-1.bin is missing") {
		t.Fatalf("logs = %q, want a warning that glyph-1.bin is missing", logs)
	}
}

func TestReconnecting_ACorruptKeysFileIsMovedAside(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "keys.json"), []byte("{not json"), 0o644)

	r, board := connected(t, dir, nil)
	defer r.Close()

	if _, err := os.Stat(filepath.Join(dir, "keys.json.bad")); err != nil {
		t.Fatalf("keys.json.bad: %v", err)
	}
	r.SendKeyState(key(0, 0x001F, 0, false))
	if got := board.sent(); len(got) != 1 {
		t.Fatalf("sends after a corrupt file = %q, want just the new one", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "keys.json")); err != nil {
		t.Fatalf("keys.json after the next send: %v", err)
	}
}

// slowBoard blocks in SendCustomGlyph, as a Device does while a 32 KB glyph
// crosses the CDC port, until it is closed.
type slowBoard struct {
	*fakeBoard
	started chan struct{}
	release chan struct{}
}

func (b *slowBoard) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	close(b.started)
	<-b.release
	return io.ErrClosedPipe
}

func (b *slowBoard) Close() error {
	b.fakeBoard.Close()
	select {
	case <-b.release:
	default:
		close(b.release)
	}
	return nil
}

func TestReconnecting_ASlowGlyphUploadDoesNotBlockCloseOrTheSavedState(t *testing.T) {
	dir := t.TempDir()
	board := &slowBoard{fakeBoard: newFakeBoard(), started: make(chan struct{}), release: make(chan struct{})}
	open := func(ctx context.Context) (Transport, error) { return board, nil }
	r := newReconnecting(open, 0, dir, nil)
	waitFor(t, "the connect", func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.dev != nil })

	go r.SendCustomGlyph(2, fullGlyph(0x33))
	<-board.started

	// The glyph is on its way. Its state is already on disk, and Close
	// does not wait for the upload.
	if _, err := os.Stat(filepath.Join(dir, "glyph-2.bin")); err != nil {
		t.Fatalf("glyph file during the upload: %v", err)
	}
	closed := make(chan struct{})
	go func() { r.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatal("Close blocked behind a glyph upload")
	}
}
