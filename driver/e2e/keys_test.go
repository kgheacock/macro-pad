//go:build hardware

package e2e

import "testing"

// TestDigitsAndPressColor runs the digit scenario against a real,
// attached board: Attach discovers it (retrying across the reboot make
// flash causes), then runDigitScenario — the same scenario source
// TestDigitsAndPressColor_Emulator runs in pad_emulator_test.go — prompts
// a person to press each key and asks for their verdict. Run with `make
// e2e`, or directly with `go test -tags hardware ./driver/e2e/...` once a
// board is flashed and attached. See driver/README.md.
func TestDigitsAndPressColor(t *testing.T) {
	pad := Attach(t)
	defer pad.Close()

	runDigitScenario(t, pad)
}
