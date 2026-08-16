package e2e

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// TestDigitsAndPressColor_Emulator runs the same scenario source
// keys_test.go's hardware-gated TestDigitsAndPressColor runs, against
// transport.Emulator with no board attached. A ScriptedOperator stands in
// for the person: its queued Ask action presses each key in turn and
// asserts, from the key-state message the digit scenario's On(Press)
// handler sends, that the pressed key keeps its digit while its color
// turns Amber — the exact story task 0020's Goals name.
func TestDigitsAndPressColor_Emulator(t *testing.T) {
	emu := transport.NewEmulator()
	defer emu.Close()

	op := mustScriptedOperator(t)

	// pressErrs carries the first mismatch pressEachKeyAndAssertColor
	// finds back to this goroutine. It runs on its own goroutine — started
	// from ScriptedOperator.Ask, which must return immediately so the
	// scenario's own Key.ExpectPress calls are already watching — so it
	// cannot call t.Fatalf itself; only the test's own goroutine may.
	pressErrs := make(chan error, 1)
	op.OnAsk(func() {
		pressErrs <- pressEachKeyAndAssertColor(emu)
	})
	op.AnswerConfirm(true)

	pad := NewPad(emu, op)
	defer pad.Close()

	runDigitScenario(t, pad)

	select {
	case err := <-pressErrs:
		if err != nil {
			t.Fatalf("press and assert color: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for the scripted key presses to finish")
	}
}

// pressEachKeyAndAssertColor presses each key in order, waits for its
// key-state message to turn Amber while keeping its digit, then releases
// it before moving to the next key, returning the first mismatch found.
// Pad's dispatch goroutine (pad.go) processes one event, and runs that
// key's On handlers to completion, before reading the next — so unlike a
// design with one goroutine per waiter, no other key's handler can land a
// SendKeyState call in between and disturb what this loop observes.
func pressEachKeyAndAssertColor(emu *transport.Emulator) error {
	for i := 0; i < KeyCount; i++ {
		if err := emu.InjectEvent(transport.Event{KeyIndex: byte(i), Type: transport.EventPress, Timestamp: uint64(i)}); err != nil {
			return fmt.Errorf("inject press %d: %w", i, err)
		}
		amber := transport.KeyState{
			KeyIndex: byte(i),
			Version:  transport.ProtocolVersion,
			Color:    uint16(Amber),
			EmojiID:  byte(Digit(i + 1)),
		}
		if err := pollKeyState(emu, amber, time.Second); err != nil {
			return err
		}

		if err := emu.InjectEvent(transport.Event{KeyIndex: byte(i), Type: transport.EventRelease, Timestamp: uint64(i)}); err != nil {
			return fmt.Errorf("inject release %d: %w", i, err)
		}
		slate := amber
		slate.Color = uint16(Slate)
		if err := pollKeyState(emu, slate, time.Second); err != nil {
			return err
		}
	}
	return nil
}

// pollKeyState polls emu's last key state until it matches want or
// timeout elapses. Set and SetColor land asynchronously — through the
// digit scenario's On handler, run on Pad's dispatch goroutine once it
// reads the injected event — so this waits for that goroutine to catch
// up, the same way plugin.Server's own tests wait for its dispatch loop
// (waitForCondition in driver/plugin/server_test.go).
func pollKeyState(emu *transport.Emulator, want transport.KeyState, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if got, ok := emu.LastKeyState(); ok && got == want {
			return nil
		}
		if time.Now().After(deadline) {
			got, _ := emu.LastKeyState()
			return fmt.Errorf("key state: got %+v, want %+v within %s", got, want, timeout)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestKey_SetColorKeepsGlyph proves task 0020's DoD-2: SetColor alone
// resends the key's current glyph and blink flag, so a scenario reacting
// to a press does not have to repeat the glyph it already set.
func TestKey_SetColorKeepsGlyph(t *testing.T) {
	emu := transport.NewEmulator()
	defer emu.Close()

	pad := NewPad(emu, mustScriptedOperator(t))
	defer pad.Close()

	k := pad.Keys()[2]
	if err := k.Set(Digit(3), Slate); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := k.SetColor(Amber); err != nil {
		t.Fatalf("SetColor: %v", err)
	}

	// SendKeyState's return does not mean LastKeyState already reflects
	// it: Emulator decodes the message on its own goroutine, over an
	// io.Pipe whose Write unblocks once the matching Read has consumed
	// the bytes, not once that goroutine has gone on to record them. Poll
	// for it, the same way TestServer_SetKeyState does in
	// driver/plugin/server_test.go.
	want := transport.KeyState{
		KeyIndex: 2,
		Version:  transport.ProtocolVersion,
		Color:    uint16(Amber),
		EmojiID:  byte(Digit(3)),
	}
	if err := pollKeyState(emu, want, time.Second); err != nil {
		t.Fatal(err)
	}
}

// TestPad_FanOutTwoWaiters proves task 0020's DoD-3: two waiters on the
// same key — an On registration and an ExpectPress call — both receive
// one press, instead of one stealing it from the other. ExpectPress runs
// after the press is already injected, proving it is not just a listener
// that happened to already be watching: a Key remembers it was pressed.
func TestPad_FanOutTwoWaiters(t *testing.T) {
	emu := transport.NewEmulator()
	defer emu.Close()

	pad := NewPad(emu, mustScriptedOperator(t))
	defer pad.Close()

	k := pad.Keys()[0]

	onFired := make(chan struct{})
	k.On(transport.EventPress, func() { close(onFired) })

	if err := emu.InjectEvent(transport.Event{KeyIndex: 0, Type: transport.EventPress, Timestamp: 1}); err != nil {
		t.Fatalf("InjectEvent: %v", err)
	}

	k.ExpectPress(t, time.Second)

	select {
	case <-onFired:
	case <-time.After(time.Second):
		t.Fatal("On handler did not also receive the press")
	}
}

// TestExpectPress_Timeout proves task 0020's DoD-4: ExpectPress fails
// with a message naming the key index and the timeout when no press
// arrives. recordingTB stands in for *testing.T so the failure can be
// inspected instead of ending the test.
func TestExpectPress_Timeout(t *testing.T) {
	emu := transport.NewEmulator()
	defer emu.Close()

	pad := NewPad(emu, mustScriptedOperator(t))
	defer pad.Close()

	k := pad.Keys()[4]

	rt := &recordingTB{TB: t}
	k.ExpectPress(rt, 10*time.Millisecond)

	if !rt.failed {
		t.Fatal("ExpectPress did not fail on timeout")
	}
	if !strings.Contains(rt.msg, "4") || !strings.Contains(rt.msg, "10ms") {
		t.Fatalf("failure message %q does not name the key index and timeout", rt.msg)
	}
}

// recordingTB is a minimal testing.TB stand-in that records Fatalf's
// message instead of ending the goroutine, so a test can inspect what a
// timeout reports without failing itself. It only overrides the two
// methods Key.ExpectPress calls.
type recordingTB struct {
	testing.TB
	failed bool
	msg    string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.failed = true
	r.msg = fmt.Sprintf(format, args...)
}

func mustScriptedOperator(t testing.TB) *ScriptedOperator {
	t.Helper()
	op, err := NewScriptedOperator(t)
	if err != nil {
		t.Fatalf("NewScriptedOperator: %v", err)
	}
	return op
}
