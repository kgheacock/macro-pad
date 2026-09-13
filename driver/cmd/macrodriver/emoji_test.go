package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// solidPNG builds a width x height PNG filled with c, matching
// driver/plugin's server_test.go helper of the same name — this package
// needs its own copy since Go test helpers aren't exported across
// packages.
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

// stubRender replaces renderEmojiPNG for the duration of the test with
// one that returns pngBytes (or err) with no real python3 or Pillow
// call, and restores the original on cleanup.
func stubRender(t *testing.T, pngBytes []byte, err error) {
	t.Helper()
	orig := renderEmojiPNG
	renderEmojiPNG = func(char, script string) ([]byte, error) { return pngBytes, err }
	t.Cleanup(func() { renderEmojiPNG = orig })
}

// stubEmulatedConn points --emulate at dev, an emulator the test owns,
// instead of the package default's own throwaway emulator. This lets a
// test call dev.LastCustomGlyph() after runEmoji returns.
func stubEmulatedConn(t *testing.T, dev *transport.Emulator) {
	t.Helper()
	srv := plugin.NewServer(dev, dev)
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)

	orig := newEmulatedConn
	newEmulatedConn = func() (*api.Conn, func(), error) {
		addr := strings.TrimPrefix(ts.URL, "http://")
		conn, err := api.Dial(addr)
		if err != nil {
			return nil, nil, err
		}
		return conn, func() { conn.Close() }, nil
	}
	t.Cleanup(func() { newEmulatedConn = orig })
}

// TestRunEmoji_Emulate proves `macrodriver emoji --emulate` ends with
// the emulator's last custom glyph holding the rendered PNG's pixels —
// task 0034's DoD-1.
func TestRunEmoji_Emulate(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	stubEmulatedConn(t, dev)
	stubRender(t, solidPNG(t, transport.CustomGlyphWidth, transport.CustomGlyphHeight, color.RGBA{R: 0xFF, A: 0xFF}), nil)

	var stdout, stderr bytes.Buffer
	code := runEmoji([]string{"--key", "0", "--char", "😍", "--emulate"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runEmoji exit code = %d, stderr = %q", code, stderr.String())
	}

	// The server applies a setCustomGlyph message to the emulator on its
	// own goroutine — see driver/plugin's TestServer_SetCustomGlyph and
	// its waitForCondition helper — so poll instead of reading
	// LastCustomGlyph once, immediately after runEmoji returns.
	deadline := time.Now().Add(time.Second)
	var got transport.CustomGlyph
	var ok bool
	for time.Now().Before(deadline) {
		if got, ok = dev.LastCustomGlyph(); ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !ok {
		t.Fatal("LastCustomGlyph: ok = false, want a custom glyph after --emulate")
	}
	if len(got.Pixels) != transport.CustomGlyphPixelsSize {
		t.Fatalf("LastCustomGlyph Pixels length = %d, want %d", len(got.Pixels), transport.CustomGlyphPixelsSize)
	}
	allZero := true
	for _, b := range got.Pixels {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("LastCustomGlyph Pixels are all zero, want non-empty pixels")
	}
}

// TestRunEmoji_RejectsMultiCodepoint proves a ZWJ sequence or a
// multi-character --char is rejected before any wire traffic — task
// 0034's DoD-2.
func TestRunEmoji_RejectsMultiCodepoint(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	stubEmulatedConn(t, dev)
	stubRender(t, nil, errors.New("renderEmojiPNG must not run for a rejected --char"))

	cases := []string{
		"👨‍👩‍👧", // family emoji, ZWJ sequence
		"🇺🇸",    // flag, two regional-indicator codepoints
		"😍😍",    // more than one emoji
	}
	for _, char := range cases {
		var stdout, stderr bytes.Buffer
		code := runEmoji([]string{"--key", "0", "--char", char, "--emulate"}, &stdout, &stderr)
		if code == 0 {
			t.Fatalf("runEmoji(%q) exit code = 0, want non-zero", char)
		}
		if _, ok := dev.LastCustomGlyph(); ok {
			t.Fatalf("runEmoji(%q): LastCustomGlyph is set, want no wire traffic for a rejected --char", char)
		}
	}
}

// TestRunEmoji_MissingPython3 proves a missing python3 produces one
// clean error line, not a stack trace — task 0034's DoD-3.
func TestRunEmoji_MissingPython3(t *testing.T) {
	orig := lookPath
	lookPath = func(file string) (string, error) {
		return "", errors.New("exec: \"python3\": executable file not found in $PATH")
	}
	t.Cleanup(func() { lookPath = orig })

	dev := transport.NewEmulator()
	defer dev.Close()
	stubEmulatedConn(t, dev)

	var stdout, stderr bytes.Buffer
	code := runEmoji([]string{"--key", "0", "--char", "😍", "--emulate"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("runEmoji exit code = 0, want non-zero when python3 is missing")
	}
	if !strings.Contains(stderr.String(), "python3") {
		t.Fatalf("stderr = %q, want it to name python3 as the missing dependency", stderr.String())
	}
	lines := strings.Count(strings.TrimSpace(stderr.String()), "\n") + 1
	if lines != 1 {
		t.Fatalf("stderr had %d lines, want exactly one line naming the missing dependency: %q", lines, stderr.String())
	}
	if _, ok := dev.LastCustomGlyph(); ok {
		t.Fatal("LastCustomGlyph is set, want no wire traffic when render fails")
	}
}
