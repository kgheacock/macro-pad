package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/kgheacock/macro-pad/driver/api"
	"github.com/kgheacock/macro-pad/driver/plugin"
)

// renderHTMLPNG runs the render_html.py helper at script against file and
// returns the 128x128 PNG bytes it writes to a temp file. It is a
// package var so a test can stub the whole render step and exercise
// runHtml's wire-sending path with no real python3, WeasyPrint, or
// PyMuPDF installed. See task 0037's DoD-1.
var renderHTMLPNG = func(file, script string) ([]byte, error) {
	if _, err := lookPath("python3"); err != nil {
		return nil, fmt.Errorf("macrodriver html: python3 not found on PATH — install Python 3 and WeasyPrint (pip install weasyprint pymupdf) to render HTML")
	}

	tmp, err := os.CreateTemp("", "macrodriver-html-*.png")
	if err != nil {
		return nil, fmt.Errorf("macrodriver html: create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	tmp.Close()
	defer os.Remove(tmpPath)

	cmd := exec.Command("python3", script, file, tmpPath)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if strings.Contains(msg, "No module named") && strings.Contains(msg, "weasyprint") {
			return nil, fmt.Errorf("macrodriver html: WeasyPrint not installed — run `pip install weasyprint` to render HTML")
		}
		if strings.Contains(msg, "No module named") && strings.Contains(msg, "fitz") {
			return nil, fmt.Errorf("macrodriver html: PyMuPDF not installed — run `pip install pymupdf` to render HTML")
		}
		if msg == "" {
			msg = err.Error()
		}
		return nil, fmt.Errorf("macrodriver html: render %s: %s", script, msg)
	}

	pngBytes, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("macrodriver html: read rendered PNG: %w", err)
	}
	return pngBytes, nil
}

// runHtml implements `macrodriver html`: it renders --file to a 128x128
// PNG with render_html.py, then sends it through api.Conn.SetCustomGlyph
// — task 0030's setCustomGlyph wire path, unchanged. --emulate sends to
// an in-process emulator instead of dialing --addr, for testing and for
// local use with no macropadd daemon running.
func runHtml(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("html", flag.ContinueOnError)
	fs.SetOutput(stderr)
	key := fs.Int("key", 0, "0-based key index to show the rendered HTML on")
	file := fs.String("file", "", "path to a static HTML file to render and send")
	addr := fs.String("addr", fmt.Sprintf("127.0.0.1:%d", plugin.DefaultPort), "host:port of the running macropadd plugin server")
	emulate := fs.Bool("emulate", false, "send to an in-process emulator instead of dialing --addr")
	script := fs.String("script", "tools/render_html.py", "path to the render_html.py helper, run from the repo root")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if *file == "" {
		fmt.Fprintln(stderr, "macrodriver html: --file is required")
		return 2
	}

	pngBytes, err := renderHTMLPNG(*file, *script)
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
