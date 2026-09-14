package main

import (
	"bytes"
	"errors"
	"image"
	"image/color"
	"image/png"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

// realRendererAvailable reports whether python3, WeasyPrint, and
// PyMuPDF are all importable on this machine. DoD-2 and DoD-3 exercise
// the real render_html.py pipeline; installing WeasyPrint's system
// libraries (Cairo, Pango) is this task's accepted cost (see the task
// spec's Risks section), not something every machine running `go test`
// is expected to have, so these two tests skip instead of failing when
// the real renderer is absent.
func realRendererAvailable(t *testing.T) bool {
	t.Helper()
	return exec.Command("python3", "-c", "import weasyprint, fitz").Run() == nil
}

// stubRenderHTML replaces renderHTMLPNG for the duration of the test
// with one that returns pngBytes (or err) with no real python3,
// WeasyPrint, or PyMuPDF call, and restores the original on cleanup.
func stubRenderHTML(t *testing.T, pngBytes []byte, err error) {
	t.Helper()
	orig := renderHTMLPNG
	renderHTMLPNG = func(file, script string) ([]byte, error) { return pngBytes, err }
	t.Cleanup(func() { renderHTMLPNG = orig })
}

// decodePNG decodes pngBytes into an image.Image, failing the test on
// any decode error.
func decodePNG(t *testing.T, pngBytes []byte) image.Image {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("png.Decode: %v", err)
	}
	return img
}

// pixelsEqual reports whether a and b have the same bounds and the same
// color at every pixel.
func pixelsEqual(a, b image.Image) bool {
	if a.Bounds() != b.Bounds() {
		return false
	}
	bounds := a.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			if a.At(x, y) != b.At(x, y) {
				return false
			}
		}
	}
	return true
}

// TestRunHtml_Emulate proves `macrodriver html --emulate` ends with the
// emulator's last custom glyph holding the rendered PNG's pixels — task
// 0037's DoD-1.
func TestRunHtml_Emulate(t *testing.T) {
	dev := transport.NewEmulator()
	defer dev.Close()
	stubEmulatedConn(t, dev)
	stubRenderHTML(t, solidPNG(t, transport.CustomGlyphWidth, transport.CustomGlyphHeight, color.RGBA{B: 0xFF, A: 0xFF}), nil)

	var stdout, stderr bytes.Buffer
	code := runHtml([]string{"--key", "0", "--file", "testdata/sample.html", "--emulate"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runHtml exit code = %d, stderr = %q", code, stderr.String())
	}

	// The server applies a setCustomGlyph message to the emulator on its
	// own goroutine — see driver/plugin's TestServer_SetCustomGlyph and
	// its waitForCondition helper — so poll instead of reading
	// LastCustomGlyph once, immediately after runHtml returns.
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

// TestRunHtml_MissingPython3 proves a missing python3 produces one
// clean error line, not a stack trace — task 0037's DoD-4, matching
// TestRunEmoji_MissingPython3's shape.
func TestRunHtml_MissingPython3(t *testing.T) {
	orig := lookPath
	lookPath = func(file string) (string, error) {
		return "", errors.New("exec: \"python3\": executable file not found in $PATH")
	}
	t.Cleanup(func() { lookPath = orig })

	dev := transport.NewEmulator()
	defer dev.Close()
	stubEmulatedConn(t, dev)

	var stdout, stderr bytes.Buffer
	code := runHtml([]string{"--key", "0", "--file", "testdata/sample.html", "--emulate"}, &stdout, &stderr)
	if code == 0 {
		t.Fatal("runHtml exit code = 0, want non-zero when python3 is missing")
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

// TestRunHtml_ScriptInert proves a <script> tag in the input HTML has
// no effect on the rendered image — task 0037's DoD-2. It runs the real
// render_html.py, not a stub, since the property under test is
// WeasyPrint's own behavior.
func TestRunHtml_ScriptInert(t *testing.T) {
	if !realRendererAvailable(t) {
		t.Skip("python3, WeasyPrint, or PyMuPDF not importable on this machine; see task 0037's Risks")
	}

	withoutScript, err := renderHTMLPNG("testdata/script_off.html", "../../../tools/render_html.py")
	if err != nil {
		t.Fatalf("renderHTMLPNG(script_off.html): %v", err)
	}
	withScript, err := renderHTMLPNG("testdata/script_on.html", "../../../tools/render_html.py")
	if err != nil {
		t.Fatalf("renderHTMLPNG(script_on.html): %v", err)
	}

	if !pixelsEqual(decodePNG(t, withoutScript), decodePNG(t, withScript)) {
		t.Fatal("renders differ: the <script> tag in script_on.html changed the rendered image, want it to have no effect")
	}
}

// TestRunHtml_RemoteURLDoesNotHang proves a remote <img src> naming an
// unreachable host does not make the render hang — task 0037's DoD-3.
func TestRunHtml_RemoteURLDoesNotHang(t *testing.T) {
	if !realRendererAvailable(t) {
		t.Skip("python3, WeasyPrint, or PyMuPDF not importable on this machine; see task 0037's Risks")
	}

	const timeout = 10 * time.Second
	done := make(chan struct{})
	var pngBytes []byte
	var err error
	go func() {
		pngBytes, err = renderHTMLPNG("testdata/remote_img.html", "../../../tools/render_html.py")
		close(done)
	}()

	select {
	case <-done:
		if err != nil {
			t.Fatalf("renderHTMLPNG(remote_img.html): %v", err)
		}
		if _, decodeErr := png.Decode(bytes.NewReader(pngBytes)); decodeErr != nil {
			t.Fatalf("png.Decode: %v", decodeErr)
		}
	case <-time.After(timeout):
		t.Fatalf("renderHTMLPNG(remote_img.html) did not return within %s, want no hang on an unreachable URL", timeout)
	}
}
