package main

import (
	"bytes"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"unicode/utf8"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/transport"
)

// errMultiCodepointEmoji is returned when --char is not exactly one
// Unicode codepoint. Emoji sequences — flags, skin-tone modifiers,
// family emoji joined by ZWJ — are out of scope; see task 0034's
// Non-goals.
var errMultiCodepointEmoji = errors.New("macrodriver emoji: --char must be exactly one Unicode codepoint (emoji sequences are not supported)")

// validateSingleCodepoint rejects any --char value that isn't exactly
// one rune, before render or wire work starts.
func validateSingleCodepoint(s string) error {
	if utf8.RuneCountInString(s) != 1 {
		return errMultiCodepointEmoji
	}
	return nil
}

// lookPath wraps exec.LookPath as a package var so a test can simulate a
// missing python3 with no need to actually remove it from the test
// machine's PATH. See task 0034's DoD-3.
var lookPath = exec.LookPath

// renderEmojiPNG runs the render_emoji.py helper at script against char
// and returns the 128x128 PNG bytes it writes to a temp file. It is a
// package var so a test can stub the whole render step and exercise
// runEmoji's wire-sending path with no real python3 or Pillow installed.
var renderEmojiPNG = func(char, script string) ([]byte, error) {
	if _, err := lookPath("python3"); err != nil {
		return nil, fmt.Errorf("macrodriver emoji: python3 not found on PATH — install Python 3 and Pillow (pip install pillow) to render an emoji")
	}

	tmp, err := os.CreateTemp("", "macrodriver-emoji-*.png")
	if err != nil {
		return nil, fmt.Errorf("macrodriver emoji: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("python3", script, char, tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "No module named") && strings.Contains(msg, "PIL") {
			return nil, fmt.Errorf("macrodriver emoji: Pillow not installed — run `pip install pillow` to render an emoji")
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("macrodriver emoji: render %s: %s", script, msg)
	}

	pngBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("macrodriver emoji: read rendered PNG: %w", err)
	}
	return pngBytes, nil
}

// newEmulatedConn backs --emulate: an in-process plugin.Server over a
// transport.Emulator, with no running macropadd daemon needed. It is a
// package var so a test can supply its own transport.Emulator and, after
// runEmoji returns, call Emulator.LastCustomGlyph() on it — see task
// 0034's DoD-1.
var newEmulatedConn = func() (*api.Conn, func(), error) {
	dev := transport.NewEmulator()
	srv := plugin.NewServer(dev, dev)
	ts := httptest.NewServer(srv)
	addr := strings.TrimPrefix(ts.URL, "http://")

	conn, err := api.Dial(addr)
	if err != nil {
		ts.Close()
		dev.Close()
		return nil, nil, fmt.Errorf("macrodriver emoji: dial in-process emulator: %w", err)
	}
	return conn, func() { conn.Close(); ts.Close(); dev.Close() }, nil
}

// runEmoji implements `macrodriver emoji`: it renders --char to a
// 128x128 PNG with render_emoji.py, then sends it through
// api.Conn.SetCustomGlyph — task 0030's setCustomGlyph wire path,
// unchanged. --emulate sends to an in-process emulator instead of
// dialing --addr, for testing and for local use with no macropadd
// daemon running.
func runEmoji(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("emoji", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.Int("key", 0, "0-based key index to show the emoji on")
	char := fs.String("char", "", "a single Unicode emoji character to render and send")
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	emulate := fs.Bool("emulate", false, "send to an in-process emulator instead of dialing --addr")
	script := fs.String("script", "tools/render_emoji.py", "path to the render_emoji.py helper, run from the repo root")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := validateSingleCodepoint(*char); err != nil {
		fmt.Fprintln(stderr, err)
		return 2
	}

	pngBytes, err := renderEmojiPNG(*char, *script)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	var conn *api.Conn
	var closeConn func()
	if *emulate {
		conn, closeConn, err = newEmulatedConn()
	} else {
		conn, err = api.Dial(*addr)
		if err == nil {
			closeConn = func() { conn.Close() }
		}
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer closeConn()

	if err := conn.SetCustomGlyph(*key, pngBytes); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
