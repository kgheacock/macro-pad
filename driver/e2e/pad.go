// Package e2e is the driver-API facade a hardware scenario is written
// against: Pad wraps a transport.Transport so a scenario names a key and
// a glyph instead of building a transport.KeyState by hand, and can wait
// for a press without hand-rolling a read loop. Attach opens a real
// board; NewPad, given transport.NewEmulator() and a ScriptedOperator,
// runs the same scenario source with no board attached. See
// tasks/ongoing/0020-e2e-hardware-test-harness.md for the design
// decision, and driver/README.md for how to write and run a scenario.
package e2e

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// KeyCount is how many keys the macro pad exposes. See docs/wire-protocol.md.
const KeyCount = 6

// DefaultVendorID and DefaultProductID match the USB descriptor confirmed
// at bring-up (task 0021's open question); see "Connecting to a real
// board" in driver/README.md. Override with the MACROPAD_VENDOR_ID and
// MACROPAD_PRODUCT_ID environment variables, in the same 0x-prefixed hex
// the Makefile's PINGPONG_VENDOR_ID and PINGPONG_PRODUCT_ID use.
const (
	DefaultVendorID  = 0x2E8A
	DefaultProductID = 0x10A3
)

// hardwareOpenTimeout bounds how long Attach waits for the board to
// enumerate, including the reboot make flash causes, so a missing board
// fails make e2e in well under the 30s task 0020's DoD-5 requires.
const hardwareOpenTimeout = 20 * time.Second

// Pad is the driver-API facade a scenario is written against. It owns the
// one goroutine allowed to call t.ReadMessage, and fans each decoded
// event out to that key's registered On handlers and ExpectPress
// waiters — so a Key.On handler and a Key.ExpectPress call on the same
// key each see every matching event, instead of stealing it from each
// other, and neither has to have started watching before the event
// arrived: a Key remembers that it has been pressed.
type Pad struct {
	t  transport.Transport
	op Operator

	keys [KeyCount]*Key
}

// NewPad wraps t as a Pad dispatching through op, and starts the
// goroutine that reads t's device→host messages for the life of the Pad.
// Attach is the entry point for a real board; a scenario run with no
// board attached calls NewPad directly, with transport.NewEmulator() and
// a ScriptedOperator.
func NewPad(t transport.Transport, op Operator) *Pad {
	p := &Pad{t: t, op: op}
	for i := range p.keys {
		p.keys[i] = newKey(p, i)
	}
	go p.dispatch()
	return p
}

// dispatch reads t's device→host messages until it errors, which happens
// once Close runs, delivering each key event to that key alone.
func (p *Pad) dispatch() {
	for {
		msg, err := p.t.ReadMessage()
		if err != nil {
			return
		}
		if msg.Type != transport.MessageTypeEvent {
			continue
		}
		if int(msg.Event.KeyIndex) >= len(p.keys) {
			continue
		}
		p.keys[msg.Event.KeyIndex].deliver(msg.Event.Type)
	}
}

// Attach opens the macro pad's real transport.Device, retrying discovery
// for hardwareOpenTimeout so a scenario can run right after make flash
// reboots the board, and wires it to a HumanOperator reading and writing
// standard input and output. It fails t if the device does not enumerate
// in time.
func Attach(t testing.TB) *Pad {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), hardwareOpenTimeout)
	defer cancel()

	dev, err := transport.Open(ctx, transport.Options{
		VendorID:     envHexOr("MACROPAD_VENDOR_ID", DefaultVendorID),
		ProductID:    envHexOr("MACROPAD_PRODUCT_ID", DefaultProductID),
		SerialNumber: os.Getenv("MACROPAD_SERIAL"),
	})
	if err != nil {
		t.Fatalf("e2e: attach: %v", err)
	}

	op, err := NewHumanOperator(t, os.Stdin, os.Stdout)
	if err != nil {
		dev.Close()
		t.Fatalf("e2e: attach: %v", err)
	}

	return NewPad(dev, op)
}

// envHexOr reads name as a 0x-prefixed (or bare) hex uint16, falling back
// to def when the variable is unset or does not parse.
func envHexOr(name string, def uint16) uint16 {
	v := strings.TrimPrefix(os.Getenv(name), "0x")
	if v == "" {
		return def
	}
	n, err := strconv.ParseUint(v, 16, 16)
	if err != nil {
		return def
	}
	return uint16(n)
}

// Keys returns all six of the pad's keys, in index order.
func (p *Pad) Keys() []*Key {
	ks := make([]*Key, len(p.keys))
	copy(ks, p.keys[:])
	return ks
}

// Ask tells the operator to do something and returns immediately — it
// does not wait for the action to finish. A scenario waits for the
// result itself, with Key.ExpectPress or Pad.Confirm.
func (p *Pad) Ask(prompt string) {
	p.op.Ask(prompt)
}

// Confirm asks the operator a yes/no question and fails t if the answer
// is no.
func (p *Pad) Confirm(t testing.TB, prompt string) {
	t.Helper()
	if !p.op.Confirm(prompt) {
		t.Fatalf("e2e: operator did not confirm: %s", prompt)
	}
}

// Close releases Pad's underlying transport and the operator's run log.
func (p *Pad) Close() error {
	p.op.Close()
	return p.t.Close()
}

// Key is one of the macro pad's six keys, addressed by a scenario through
// Pad.Keys.
type Key struct {
	pad   *Pad
	index int

	mu    sync.Mutex
	glyph Glyph
	blink bool
	sent  bool

	handlerMu sync.Mutex
	onPress   []func()
	onRelease []func()

	pressOnce   sync.Once
	pressSignal chan struct{}
}

func newKey(pad *Pad, index int) *Key {
	return &Key{pad: pad, index: index, pressSignal: make(chan struct{})}
}

// Index returns the key's 0-based index, matching transport.Event.KeyIndex.
func (k *Key) Index() int { return k.index }

// Set sends a key-state message giving this key glyph and color, with
// blink off. It is the first call a scenario makes for a key; SetColor
// resends the glyph Set most recently gave it.
func (k *Key) Set(g Glyph, c Color) error {
	return k.send(g, c, false)
}

// SetColor resends this key's last-sent glyph and blink flag with a new
// color, so a scenario reacting to a press does not have to repeat the
// glyph it already set. Set must run at least once first.
func (k *Key) SetColor(c Color) error {
	k.mu.Lock()
	g, blink, sent := k.glyph, k.blink, k.sent
	k.mu.Unlock()
	if !sent {
		return fmt.Errorf("e2e: key %d: SetColor called before Set", k.index)
	}
	return k.send(g, c, blink)
}

func (k *Key) send(g Glyph, c Color, blink bool) error {
	k.mu.Lock()
	k.glyph, k.blink, k.sent = g, blink, true
	k.mu.Unlock()

	return k.pad.t.SendKeyState(transport.KeyState{
		KeyIndex: byte(k.index),
		Version:  transport.ProtocolVersion,
		Color:    uint16(c),
		EmojiID:  byte(g),
		Blink:    blink,
	})
}

// deliver runs every handler this key has registered for evt, then, for a
// press, marks pressSignal — exactly once, the first time — so a
// Key.ExpectPress call made before or after this delivery both see it:
// reading from an already-closed channel never blocks, so there is no
// window in which a press can arrive between "the scenario asked" and
// "ExpectPress started watching" and be missed. It runs on Pad's single
// dispatch goroutine, so two keys are never delivered to at once.
func (k *Key) deliver(evt transport.EventType) {
	k.handlerMu.Lock()
	handlers := k.onPress
	if evt == transport.EventRelease {
		handlers = k.onRelease
	}
	k.handlerMu.Unlock()

	for _, h := range handlers {
		h()
	}

	if evt == transport.EventPress {
		k.pressOnce.Do(func() { close(k.pressSignal) })
	}
}

// On registers handler to run, on Pad's dispatch goroutine, each time
// this key reports evt. It returns immediately. Two waiters on the same
// key — two On registrations, or an On alongside ExpectPress — each see
// every matching event, instead of one stealing it from the other.
func (k *Key) On(evt transport.EventType, handler func()) {
	k.handlerMu.Lock()
	defer k.handlerMu.Unlock()
	if evt == transport.EventRelease {
		k.onRelease = append(k.onRelease, handler)
	} else {
		k.onPress = append(k.onPress, handler)
	}
}

// ExpectPress waits up to timeout for this key to have reported a press —
// at any point, including before this call — and fails t, naming this
// key's index and the timeout, if none ever arrives.
func (k *Key) ExpectPress(t testing.TB, timeout time.Duration) {
	t.Helper()
	select {
	case <-k.pressSignal:
	case <-time.After(timeout):
		t.Fatalf("e2e: key %d: no press within %s", k.index, timeout)
	}
}
