package plugin

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// solidPNG builds a width x height PNG filled with c, for a test that
// only cares about SetCustomGlyphPayload's dimensions, not its picture.
func solidPNG(t *testing.T, width, height int, c color.Color) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, c)
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("png.Encode: %v", err)
	}
	return buf.Bytes()
}

// fakeConn is a wsConn test double. It marshals and unmarshals JSON the
// same way *websocket.Conn does, so a test proves the server's JSON
// schema is correct without depending on a real socket. writeBlock, when
// set, makes WriteJSON park until it is closed, so a slow client can be
// simulated deterministically instead of relying on OS socket buffering.
type fakeConn struct {
	mu          sync.Mutex
	written     [][]byte
	closed      bool
	closeCode   int
	closeReason string

	writeBlock chan struct{}
	closeCh    chan struct{}

	incoming chan []byte
}

func newFakeConn() *fakeConn {
	return &fakeConn{
		closeCh:  make(chan struct{}),
		incoming: make(chan []byte, 8),
	}
}

func (f *fakeConn) ReadJSON(v any) error {
	select {
	case b, ok := <-f.incoming:
		if !ok {
			return io.EOF
		}
		return json.Unmarshal(b, v)
	case <-f.closeCh:
		return io.EOF
	}
}

func (f *fakeConn) WriteJSON(v any) error {
	if f.writeBlock != nil {
		select {
		case <-f.writeBlock:
		case <-f.closeCh:
			return io.EOF
		}
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	f.mu.Lock()
	f.written = append(f.written, b)
	f.mu.Unlock()
	return nil
}

func (f *fakeConn) WriteClose(code int, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closeCode, f.closeReason = code, reason
	return nil
}

func (f *fakeConn) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !f.closed {
		f.closed = true
		close(f.closeCh)
	}
	return nil
}

func (f *fakeConn) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeConn) rawMessages() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([][]byte, len(f.written))
	copy(out, f.written)
	return out
}

func (f *fakeConn) messages(t *testing.T) []Message {
	t.Helper()
	raw := f.rawMessages()
	msgs := make([]Message, len(raw))
	for i, b := range raw {
		if err := json.Unmarshal(b, &msgs[i]); err != nil {
			t.Fatalf("unmarshal written message %d: %v", i, err)
		}
	}
	return msgs
}

func (f *fakeConn) send(t *testing.T, m Message) {
	t.Helper()
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal message to send: %v", err)
	}
	f.incoming <- b
}

// waitForCondition polls cond until it returns true or timeout elapses,
// failing t if it never does.
func waitForCondition(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestServer_DeliversEvent(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	ev := transport.Event{KeyIndex: 3, Type: transport.EventPress, Timestamp: 12345}
	if err := dev.InjectEvent(ev); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}

	waitForCondition(t, time.Second, func() bool { return len(conn.rawMessages()) > 0 })

	got := conn.messages(t)[0]
	want := eventMessage(ev)
	if got.Kind != want.Kind || got.Event == nil || *got.Event != *want.Event {
		t.Fatalf("got message %+v, want %+v", got, want)
	}
}

func TestServer_SetKeyState(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	conn.send(t, Message{
		Kind: KindSetKeyState,
		SetKeyState: &SetKeyStatePayload{
			KeyIndex: 2,
			Color:    0xF800,
			EmojiID:  7,
			Blink:    true,
		},
	})

	waitForCondition(t, time.Second, func() bool {
		_, ok := dev.LastKeyState()
		return ok
	})

	got, _ := dev.LastKeyState()
	want := transport.KeyState{
		KeyIndex: 2,
		Version:  transport.ProtocolVersion,
		Color:    0xF800,
		EmojiID:  7,
		Blink:    true,
	}
	if got != want {
		t.Fatalf("got key state %+v, want %+v", got, want)
	}
}

func TestServer_SetCustomGlyph(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	pngBytes := solidPNG(t, transport.CustomGlyphWidth, transport.CustomGlyphHeight, color.RGBA{G: 0xFF, A: 0xFF})
	conn.send(t, Message{
		Kind: KindSetCustomGlyph,
		SetCustomGlyph: &SetCustomGlyphPayload{
			KeyIndex: 5,
			Image:    pngBytes,
		},
	})

	waitForCondition(t, time.Second, func() bool {
		_, ok := dev.LastCustomGlyph()
		return ok
	})

	got, _ := dev.LastCustomGlyph()
	if got.KeyIndex != 5 {
		t.Fatalf("LastCustomGlyph KeyIndex = %d, want 5", got.KeyIndex)
	}
	if len(got.Pixels) != transport.CustomGlyphPixelsSize {
		t.Fatalf("LastCustomGlyph Pixels length = %d, want %d", len(got.Pixels), transport.CustomGlyphPixelsSize)
	}
}

func TestServer_SetCustomGlyph_RejectsWrongSizeImage(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	pngBytes := solidPNG(t, 64, 64, color.RGBA{G: 0xFF, A: 0xFF})
	conn.send(t, Message{
		Kind: KindSetCustomGlyph,
		SetCustomGlyph: &SetCustomGlyphPayload{
			KeyIndex: 5,
			Image:    pngBytes,
		},
	})

	// Give the read pump a moment to process (and reject) the message,
	// then prove it never reached the transport.
	time.Sleep(20 * time.Millisecond)
	if _, ok := dev.LastCustomGlyph(); ok {
		t.Fatal("server sent a custom glyph despite a wrong-sized source image")
	}
}

func TestServer_InjectEvent_AppliesToInjector(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	conn.send(t, Message{
		Kind:        KindInjectEvent,
		InjectEvent: &InjectEventPayload{KeyIndex: 4, Type: "release"},
	})

	waitForCondition(t, time.Second, func() bool { return len(conn.rawMessages()) > 0 })

	got := conn.messages(t)[0]
	if got.Kind != KindEvent || got.Event == nil {
		t.Fatalf("got message %+v, want a decoded event message", got)
	}
	if got.Event.KeyIndex != 4 || got.Event.Type != "release" {
		t.Fatalf("got event %+v, want keyIndex 4 release", got.Event)
	}
}

func TestServer_InjectEvent_NilInjector(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, nil)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	conn.send(t, Message{
		Kind:        KindInjectEvent,
		InjectEvent: &InjectEventPayload{KeyIndex: 2, Type: "press"},
	})

	// A nil Injector drops the injectEvent message above instead of
	// applying it. Prove that by injecting a distinguishable event
	// straight through dev, and checking it is the only message this
	// client ever receives.
	sentinel := transport.Event{KeyIndex: 5, Type: transport.EventPress, Timestamp: 999}
	if err := dev.InjectEvent(sentinel); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}

	waitForCondition(t, time.Second, func() bool { return len(conn.rawMessages()) > 0 })

	msgs := conn.messages(t)
	if len(msgs) != 1 {
		t.Fatalf("got %d messages, want 1 (a nil Injector must drop injectEvent, not apply it)", len(msgs))
	}
	if msgs[0].Event == nil || msgs[0].Event.KeyIndex != sentinel.KeyIndex {
		t.Fatalf("got %+v, want only the sentinel event", msgs[0])
	}
}

func TestInjectEventPayload_ToEvent(t *testing.T) {
	cases := []struct {
		name    string
		payload *InjectEventPayload
		want    transport.EventType
		wantErr bool
	}{
		{name: "press", payload: &InjectEventPayload{KeyIndex: 1, Type: "press"}, want: transport.EventPress},
		{name: "release", payload: &InjectEventPayload{KeyIndex: 1, Type: "release"}, want: transport.EventRelease},
		{name: "nil payload", payload: nil, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ev, err := c.payload.toEvent()
			if c.wantErr {
				if err == nil {
					t.Fatal("got nil error, want one")
				}
				return
			}
			if err != nil {
				t.Fatalf("toEvent: %v", err)
			}
			if ev.KeyIndex != c.payload.KeyIndex || ev.Type != c.want {
				t.Fatalf("got %+v, want KeyIndex %d Type %v", ev, c.payload.KeyIndex, c.want)
			}
		})
	}
}

func TestServer_SetKeyState_BroadcastsToOtherClients(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	sender := newFakeConn()
	if s.addClient(sender) == nil {
		t.Fatal("addClient rejected the sending client")
	}
	observer := newFakeConn()
	if s.addClient(observer) == nil {
		t.Fatal("addClient rejected the observing client")
	}

	want := SetKeyStatePayload{KeyIndex: 5, Color: 0x07E0, EmojiID: 3, Blink: true}
	sender.send(t, Message{Kind: KindSetKeyState, SetKeyState: &want})

	waitForCondition(t, time.Second, func() bool { return len(observer.rawMessages()) > 0 })

	got := observer.messages(t)[0]
	if got.Kind != KindSetKeyState || got.SetKeyState == nil || *got.SetKeyState != want {
		t.Fatalf("observer got %+v, want the sender's setKeyState rebroadcast", got)
	}
}

func TestServer_SlowClientDoesNotBlockOthers(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	slow := newFakeConn()
	slow.writeBlock = make(chan struct{}) // never closed: every write to slow parks here
	if s.addClient(slow) == nil {
		t.Fatal("addClient rejected the slow client")
	}

	healthy := newFakeConn()
	if s.addClient(healthy) == nil {
		t.Fatal("addClient rejected the healthy client")
	}

	// More than enough events to fill slow's queue and push it past
	// maxDrops, so the test also proves the healthy client is unaffected
	// by the slow one being disconnected mid-stream.
	const n = clientQueueSize + maxDrops + 5
	for i := 0; i < n; i++ {
		ev := transport.Event{KeyIndex: byte(i % 6), Type: transport.EventPress, Timestamp: uint64(i)}
		if err := dev.InjectEvent(ev); err != nil {
			t.Fatalf("InjectEvent %d: %v", i, err)
		}
	}

	waitForCondition(t, time.Second, func() bool { return len(healthy.rawMessages()) >= n })
}

func TestServer_DisconnectsStalledClient(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	stalled := newFakeConn()
	stalled.writeBlock = make(chan struct{})
	c := s.addClient(stalled)
	if c == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	const n = clientQueueSize + maxDrops + 5
	for i := 0; i < n; i++ {
		ev := transport.Event{KeyIndex: byte(i % 6), Type: transport.EventPress, Timestamp: uint64(i)}
		if err := dev.InjectEvent(ev); err != nil {
			t.Fatalf("InjectEvent %d: %v", i, err)
		}
	}

	waitForCondition(t, time.Second, stalled.isClosed)

	s.mu.Lock()
	_, stillRegistered := s.clients[c]
	s.mu.Unlock()
	if stillRegistered {
		t.Fatal("client is still registered after its queue should have overflowed maxDrops times")
	}

	// The daemon keeps running: a second client connects and is served
	// after the stalled one was dropped.
	healthy := newFakeConn()
	if s.addClient(healthy) == nil {
		t.Fatal("addClient rejected a client added after the stalled one was disconnected")
	}
	if err := dev.InjectEvent(transport.Event{KeyIndex: 1, Type: transport.EventPress, Timestamp: 999}); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}
	waitForCondition(t, time.Second, func() bool { return len(healthy.rawMessages()) > 0 })
}

func TestServer_ResolvesLongPress(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	press := transport.Event{KeyIndex: 0, Type: transport.EventPress, Timestamp: 0}
	release := transport.Event{KeyIndex: 0, Type: transport.EventRelease, Timestamp: 600_000} // 600ms later
	if err := dev.InjectEvent(press); err != nil {
		t.Fatalf("InjectEvent press: %v", err)
	}
	if err := dev.InjectEvent(release); err != nil {
		t.Fatalf("InjectEvent release: %v", err)
	}

	waitForCondition(t, time.Second, func() bool {
		for _, m := range conn.messages(t) {
			if m.Kind == KindSignal {
				return true
			}
		}
		return false
	})

	var got *SignalPayload
	for _, m := range conn.messages(t) {
		if m.Kind == KindSignal {
			got = m.Signal
			break
		}
	}
	if got == nil {
		t.Fatal("no signal message observed")
	}
	if got.KeyIndex != 0 || got.Name != SignalLongPress {
		t.Fatalf("got signal %+v, want {KeyIndex: 0, Name: %q}", got, SignalLongPress)
	}
}

func TestServer_ResolvesSinglePress(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	press := transport.Event{KeyIndex: 1, Type: transport.EventPress, Timestamp: 0}
	release := transport.Event{KeyIndex: 1, Type: transport.EventRelease, Timestamp: 50_000} // 50ms hold
	if err := dev.InjectEvent(press); err != nil {
		t.Fatalf("InjectEvent press: %v", err)
	}
	if err := dev.InjectEvent(release); err != nil {
		t.Fatalf("InjectEvent release: %v", err)
	}

	// No second press follows, so the doublePressWindow timer must fire
	// and resolve this as a single press.
	waitForCondition(t, 2*time.Second, func() bool {
		for _, m := range conn.messages(t) {
			if m.Kind == KindSignal {
				return true
			}
		}
		return false
	})

	var got *SignalPayload
	for _, m := range conn.messages(t) {
		if m.Kind == KindSignal {
			got = m.Signal
			break
		}
	}
	if got == nil {
		t.Fatal("no signal message observed")
	}
	if got.KeyIndex != 1 || got.Name != SignalSinglePress {
		t.Fatalf("got signal %+v, want {KeyIndex: 1, Name: %q}", got, SignalSinglePress)
	}
}

func TestServer_ResolvesDoublePress(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	conn := newFakeConn()
	if s.addClient(conn) == nil {
		t.Fatal("addClient rejected the only connected client")
	}

	events := []transport.Event{
		{KeyIndex: 2, Type: transport.EventPress, Timestamp: 0},
		{KeyIndex: 2, Type: transport.EventRelease, Timestamp: 50_000},
		{KeyIndex: 2, Type: transport.EventPress, Timestamp: 100_000},
		{KeyIndex: 2, Type: transport.EventRelease, Timestamp: 150_000},
	}
	for _, ev := range events {
		if err := dev.InjectEvent(ev); err != nil {
			t.Fatalf("InjectEvent %+v: %v", ev, err)
		}
	}

	waitForCondition(t, time.Second, func() bool {
		for _, m := range conn.messages(t) {
			if m.Kind == KindSignal {
				return true
			}
		}
		return false
	})

	var got *SignalPayload
	for _, m := range conn.messages(t) {
		if m.Kind == KindSignal {
			got = m.Signal
			break
		}
	}
	if got == nil {
		t.Fatal("no signal message observed")
	}
	if got.KeyIndex != 2 || got.Name != SignalDoublePress {
		t.Fatalf("got signal %+v, want {KeyIndex: 2, Name: %q}", got, SignalDoublePress)
	}
}

func TestServer_SignalBroadcast(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	sender := newFakeConn()
	if s.addClient(sender) == nil {
		t.Fatal("addClient rejected the sending client")
	}
	observer := newFakeConn()
	if s.addClient(observer) == nil {
		t.Fatal("addClient rejected the observing client")
	}

	want := SignalPayload{KeyIndex: 0, Name: SignalProcessDone}
	sender.send(t, Message{Kind: KindSignal, Signal: &want})

	waitForCondition(t, time.Second, func() bool { return len(observer.rawMessages()) > 0 })

	got := observer.messages(t)[0]
	if got.Kind != KindSignal || got.Signal == nil || *got.Signal != want {
		t.Fatalf("observer got %+v, want the sender's signal rebroadcast %+v", got, want)
	}
}

func TestServer_SignalNoListener(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	// No client is connected. Broadcasting must not error or panic.
	s.Broadcast(signalMessage(0, SignalProcessDone))
}

func TestServer_RejectsOverCap(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	s := NewServer(dev, dev)

	for i := 0; i < maxClients; i++ {
		if s.addClient(newFakeConn()) == nil {
			t.Fatalf("addClient rejected client %d, want it accepted (maxClients is %d)", i, maxClients)
		}
	}

	over := newFakeConn()
	if s.addClient(over) != nil {
		t.Fatal("addClient accepted a connection past maxClients")
	}
	if !over.isClosed() {
		t.Fatal("connection rejected for being past maxClients was not closed")
	}
	over.mu.Lock()
	reason := over.closeReason
	over.mu.Unlock()
	if reason == "" {
		t.Fatal("connection rejected for being past maxClients got no close reason")
	}

	s.mu.Lock()
	got := len(s.clients)
	s.mu.Unlock()
	if got != maxClients {
		t.Fatalf("got %d registered clients after the rejection, want %d", got, maxClients)
	}
}
