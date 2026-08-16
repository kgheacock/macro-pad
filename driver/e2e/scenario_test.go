package e2e

import (
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// expectPressTimeout bounds how long runDigitScenario waits for each
// key's press once it asks the operator for one.
const expectPressTimeout = 10 * time.Second

// runDigitScenario proves task 0020's example story: each key shows its
// own digit, 1 through 6, and a pressed key changes its background color
// while it keeps that digit. It is the one scenario source both
// keys_test.go (against real hardware, gated by the hardware build tag)
// and pad_emulator_test.go (against transport.Emulator) run — see task
// 0020's Goals.
func runDigitScenario(t *testing.T, pad *Pad) {
	t.Helper()

	for _, k := range pad.Keys() {
		if err := k.Set(Digit(k.Index()+1), Slate); err != nil {
			t.Fatalf("key %d: Set: %v", k.Index(), err)
		}
		k.On(transport.EventPress, func() {
			k.SetColor(Amber)
		})
		k.On(transport.EventRelease, func() {
			k.SetColor(Slate)
		})
	}

	pad.Ask("Press each key once, left to right.")
	for _, k := range pad.Keys() {
		k.ExpectPress(t, expectPressTimeout)
	}
	pad.Confirm(t, "Did every key keep its number while the background turned amber?")
}
