package e2e

import "fmt"

// Glyph names a Key state message's Emoji ID by what it draws, so a
// scenario writes Digit(3) instead of the raw byte 0xF3 from
// docs/wire-protocol.md's Emoji IDs table.
type Glyph byte

// Digit returns the reserved Emoji ID for one of the six digits, 1
// through 6. It panics outside that range: a scenario names a digit that
// exists, not one it hopes firmware will add.
func Digit(n int) Glyph {
	if n < 1 || n > 6 {
		panic(fmt.Sprintf("e2e: Digit(%d): must be 1 through 6", n))
	}
	return Glyph(0xF0 + byte(n))
}

// Color is a Key state message's background color: RGB565, matching the
// Color field in docs/wire-protocol.md.
type Color uint16

// rgb565 packs 8-bit red, green, and blue into the 5-6-5 layout
// docs/wire-protocol.md's Color field carries.
func rgb565(r, g, b byte) Color {
	return Color(uint16(r>>3)<<11 | uint16(g>>2)<<5 | uint16(b>>3))
}

// Slate and Amber are the two colors the digit scenario in
// driver/README.md uses: Slate for a key at rest, Amber while it is held.
var (
	Slate = rgb565(0x33, 0x39, 0x45)
	Amber = rgb565(0xff, 0xa5, 0x00)
)
