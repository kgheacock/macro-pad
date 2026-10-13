// Package keyimg holds the color and image helpers plugins share: parsing
// a "#RRGGBB" color, converting it to the wire's RGB565, and turning any
// image into the 128x128 PNG a key shows. It is pure Go and builds on any
// OS. See task 0055.
package keyimg

import (
	"fmt"
	"strconv"
	"strings"
)

// Color is a 24-bit RGB color. The zero value is black.
type Color struct {
	R, G, B uint8
}

// Hex parses s as "#RRGGBB" (the "#" is optional).
func Hex(s string) (Color, error) {
	h := strings.TrimPrefix(s, "#")
	if len(h) != 6 {
		return Color{}, fmt.Errorf("keyimg: color %q: want #RRGGBB", s)
	}
	v, err := strconv.ParseUint(h, 16, 32)
	if err != nil {
		return Color{}, fmt.Errorf("keyimg: color %q: want #RRGGBB", s)
	}
	return Color{R: uint8(v >> 16), G: uint8(v >> 8), B: uint8(v)}, nil
}

// MustHex is Hex for a color literal in source. It panics on a bad string.
func MustHex(s string) Color {
	c, err := Hex(s)
	if err != nil {
		panic(err)
	}
	return c
}

// RGB565 packs c into the 16-bit color the wire protocol's Key state
// message carries.
func (c Color) RGB565() uint16 {
	return uint16(c.R>>3)<<11 | uint16(c.G>>2)<<5 | uint16(c.B>>3)
}

// Dim scales each channel of c by f, clamped to 0..1. Dim(0.25) gives the
// "inactive" look a switcher uses for keys that are not the current
// target.
func (c Color) Dim(f float64) Color {
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return Color{R: uint8(float64(c.R) * f), G: uint8(float64(c.G) * f), B: uint8(float64(c.B) * f)}
}

// String formats c as "#RRGGBB".
func (c Color) String() string {
	return fmt.Sprintf("#%02X%02X%02X", c.R, c.G, c.B)
}

// Palette is six distinct colors, one per key, for a plugin whose config
// names none. Blue and green match the state database's RUNNING and DONE
// colors (driver/README.md, "State database").
var Palette = [6]Color{
	MustHex("#3D5CFF"),
	MustHex("#12D84A"),
	MustHex("#FF8C00"),
	MustHex("#B04CFF"),
	MustHex("#00C8D7"),
	MustHex("#FF3D7F"),
}
