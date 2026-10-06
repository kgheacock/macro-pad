package transport

import (
	"context"
	"io"
	"sort"
	"sync"
	"time"
)

// defaultSettleDelay is how long Reconnecting waits after it opens the
// board before it replays state. CircuitPython reloads code.py when the
// board is replugged, and a report that arrives during the restart is
// dropped (see cmd/pingpong's pingResendInterval), so replaying at once can
// lose the first keys. 1 s is a starting value, not a measured one.
const defaultSettleDelay = time.Second

// reconnectRetryDelay paces Reconnecting's retries after an open fails
// for a reason other than the board being absent.
const reconnectRetryDelay = time.Second

// Reconnecting is a Transport that keeps a device connected across power
// cuts and replugs, and replays each key's last state to it every time it
// connects.
//
// The board keeps no key state across a power cycle (task 0044): a write to
// its flash froze every blinking key for 0.4 s to 0.7 s and wore the flash.
// So the host holds the state. Reconnecting remembers what each key shows
// as the board would hold it, and after a reconnect it sends the same
// messages again.
//
// SendKeyState and SendCustomGlyph never fail because the board is absent:
// they remember the state and report success, and the next reconnect sends
// it. ReadMessage returns messages from whichever device is connected, and
// io.EOF only after Close.
type Reconnecting struct {
	open   func(ctx context.Context) (Transport, error)
	settle time.Duration
	logf   func(format string, args ...any)

	ctx    context.Context
	cancel context.CancelFunc
	done   chan struct{}
	queue  chan Message

	// mu guards dev, keys, and glyphs, and is held for a whole replay, so
	// a send that arrives during one waits and then reaches the device
	// after the replayed state, not before it.
	mu sync.Mutex
	// dev is the connected device, or nil while none is.
	dev Transport
	// keys is the key state each key shows on the board, including the
	// custom-glyph sentinel for a key that shows a glyph.
	keys map[byte]KeyState
	// glyphs is the custom glyph each key shows, when it shows one.
	glyphs map[byte][]byte

	closeOnce sync.Once
}

var _ Transport = (*Reconnecting)(nil)

// NewReconnecting starts connecting to the macro pad that opts names and
// returns at once. logf, when not nil, reports each connect, disconnect,
// and replay.
func NewReconnecting(opts Options, logf func(format string, args ...any)) *Reconnecting {
	open := func(ctx context.Context) (Transport, error) {
		d, err := Open(ctx, opts)
		if err != nil {
			return nil, err
		}
		return d, nil
	}
	return newReconnecting(open, defaultSettleDelay, logf)
}

func newReconnecting(open func(context.Context) (Transport, error), settle time.Duration, logf func(string, ...any)) *Reconnecting {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &Reconnecting{
		open:   open,
		settle: settle,
		logf:   logf,
		ctx:    ctx,
		cancel: cancel,
		done:   make(chan struct{}),
		queue:  make(chan Message, deviceQueueSize),
		keys:   make(map[byte]KeyState),
		glyphs: make(map[byte][]byte),
	}
	go r.run()
	return r
}

// run connects, replays, reads until the device goes, and starts over,
// until Close.
func (r *Reconnecting) run() {
	defer close(r.done)
	defer close(r.queue)

	for r.ctx.Err() == nil {
		dev, err := r.open(r.ctx)
		if err != nil {
			if r.ctx.Err() != nil {
				return
			}
			r.logf("reconnect: %v", err)
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(reconnectRetryDelay):
			}
			continue
		}

		if !r.connect(dev) {
			dev.Close()
			return
		}
		r.logf("device connected")

		r.forward(dev)

		r.mu.Lock()
		r.dev = nil
		r.mu.Unlock()
		dev.Close()
		if r.ctx.Err() == nil {
			r.logf("device disconnected, waiting for it to return")
		}
	}
}

// connect waits out the settle delay, replays the remembered state to dev,
// and makes dev the device sends go to. It reports false when Close came
// first.
func (r *Reconnecting) connect(dev Transport) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	select {
	case <-r.ctx.Done():
		return false
	case <-time.After(r.settle):
	}

	keys := make([]int, 0, len(r.keys))
	for k := range r.keys {
		keys = append(keys, int(k))
	}
	sort.Ints(keys)
	for _, k := range keys {
		key := byte(k)
		// A glyph goes first: the board sets a key to the custom-glyph
		// sentinel when it receives one, and the key state that follows
		// sets the color and blink on top of it.
		if pixels, ok := r.glyphs[key]; ok {
			dev.SendCustomGlyph(key, pixels)
		}
		dev.SendKeyState(r.keys[key])
	}
	if len(keys) > 0 {
		r.logf("replayed the state of %d keys", len(keys))
	}

	r.dev = dev
	return true
}

// forward moves dev's messages to the queue until dev errors or Close.
func (r *Reconnecting) forward(dev Transport) {
	for {
		msg, err := dev.ReadMessage()
		if err != nil {
			return
		}
		select {
		case r.queue <- msg:
		case <-r.ctx.Done():
			return
		}
	}
}

// SendKeyState implements Transport. It remembers ks, and sends it when a
// device is connected.
func (r *Reconnecting) SendKeyState(ks KeyState) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.keys[ks.KeyIndex] = ks
	// A built-in Emoji ID replaces a custom image on the board, so the
	// image is no longer part of the state to replay.
	if ks.EmojiID != CustomGlyphSentinelEmojiID {
		delete(r.glyphs, ks.KeyIndex)
	}
	if r.dev == nil {
		return nil
	}
	return r.dev.SendKeyState(ks)
}

// SendCustomGlyph implements Transport. It remembers the glyph, and sends
// it when a device is connected.
func (r *Reconnecting) SendCustomGlyph(keyIndex byte, pixels []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.glyphs[keyIndex] = append([]byte(nil), pixels...)
	// The board sets a key that receives a glyph to the sentinel Emoji ID
	// and leaves its color and blink alone.
	ks, ok := r.keys[keyIndex]
	if !ok {
		ks = KeyState{KeyIndex: keyIndex, Version: ProtocolVersion}
	}
	ks.EmojiID = CustomGlyphSentinelEmojiID
	r.keys[keyIndex] = ks

	if r.dev == nil {
		return nil
	}
	return r.dev.SendCustomGlyph(keyIndex, pixels)
}

// ReadMessage implements Transport. It returns the next message from
// whichever device is connected, and io.EOF once Close has run.
func (r *Reconnecting) ReadMessage() (Message, error) {
	msg, ok := <-r.queue
	if !ok {
		return Message{}, io.EOF
	}
	return msg, nil
}

// Close implements Transport. It stops reconnecting and closes the
// connected device, if any.
func (r *Reconnecting) Close() error {
	var err error
	r.closeOnce.Do(func() {
		r.cancel()
		r.mu.Lock()
		dev := r.dev
		r.mu.Unlock()
		if dev != nil {
			err = dev.Close()
		}
		<-r.done
	})
	return err
}
