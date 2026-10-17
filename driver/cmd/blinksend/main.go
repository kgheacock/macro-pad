// Command blinksend sends one scripted run of key-state updates to the
// macro pad and records the board's trace of it to a JSONL file. `make
// blink-trace` runs it, then reads the file with tools/blink_trace.py. See
// tasks/complete/0044-blink-independence-persist-off-blink-path.md.
//
// The board must run firmware with tracing on; `tools/blink_trace.py
// install` puts such a code.py on it.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/kgheacock/macro-pad/driver/recorder"
	"github.com/kgheacock/macro-pad/driver/transport"
)

const (
	// settle is how long a run waits before its first update and after its
	// last, so the board has drawn what came before and traced what came
	// after.
	settle = time.Second

	// singleUpdates and singleSpacing are the DoD-5 scenario: 10 updates to
	// key 4, 2 s apart, while keys 0 to 2 blink.
	singleUpdates = 10
	singleSpacing = 2 * time.Second

	// syncUpdates and syncSpacing are task 0048's DoD-6 scenario: 10 updates
	// to key 4, 2 s apart, while all six keys blink.
	syncUpdates = 10
	syncSpacing = 2 * time.Second

	// burstUpdates is the DoD-6 scenario: 6 updates sent back to back.
	// transport.Device spaces them by minReportGap, so the run measures
	// that gap.
	burstUpdates = 6
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("blinksend", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vendorID := fs.Uint("vendor-id", 0, "USB vendor ID of the macro pad, e.g. 0x2E8A")
	productID := fs.Uint("product-id", 0, "USB product ID of the macro pad, e.g. 0x10A3")
	serialNumber := fs.String("serial", "", "USB serial number, to pick one device when more than one matches")
	cdcPort := fs.String("cdc-port", "", "CDC serial port to use directly, bypassing discovery")
	scenario := fs.String("scenario", "single", "which run to send: single, burst, busyburst, sync, glyphs, icons, batch, or warmup (send nothing)")
	assetsDir := fs.String("assets-dir", "../assets", "directory of 128x128 PNG icons, for the icons scenario")
	traceFile := fs.String("trace-file", "", "write the board's trace to this JSONL file (required)")
	openTimeout := fs.Duration("open-timeout", 30*time.Second, "how long to wait for the device, which reloads after a code.py copy")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *traceFile == "" {
		fmt.Fprintln(stderr, "blinksend: --trace-file is required")
		return 2
	}
	var script func(transport.Transport) error
	switch *scenario {
	case "single":
		script = sendSingle
	case "burst":
		script = sendBurst
	case "busyburst":
		script = sendBusyBurst
	case "sync":
		script = sendSync
	case "glyphs":
		script = func(dev transport.Transport) error { return sendGlyphs(dev, stdout, false, noiseGlyph) }
	case "icons":
		script = func(dev transport.Transport) error {
			return sendGlyphs(dev, stdout, false, func(key byte) ([]byte, error) { return iconGlyph(*assetsDir, key) })
		}
	case "batch":
		// The icons scenario, as a layer change sends it: Begin batch first.
		script = func(dev transport.Transport) error {
			return sendGlyphs(dev, stdout, true, func(key byte) ([]byte, error) { return iconGlyph(*assetsDir, key) })
		}
	case "warmup":
		// Sends nothing. The first connection after the board reloads
		// code.py has given an unreadable trace stream and no decoded
		// reports, and the next connection has worked every time, so
		// `make blink-trace` opens the board once before it measures.
		script = func(transport.Transport) error { return nil }
	default:
		fmt.Fprintf(stderr, "blinksend: unknown scenario %q, want single, burst, busyburst, sync, glyphs, icons, batch or warmup\n", *scenario)
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *openTimeout)
	defer cancel()
	dev, err := transport.Open(ctx, transport.Options{
		VendorID:     uint16(*vendorID),
		ProductID:    uint16(*productID),
		SerialNumber: *serialNumber,
		CDCPort:      *cdcPort,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}

	f, err := os.Create(*traceFile)
	if err != nil {
		dev.Close()
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer f.Close()

	// The recorder is dev's one reader. dev.Close ends its loop, and only
	// then is its buffer written out.
	rec := recorder.New(dev, f)
	recDone := make(chan error, 1)
	go func() { recDone <- rec.Run() }()

	time.Sleep(settle)
	scriptErr := script(dev)
	time.Sleep(settle)

	dev.Close()
	if err := <-recDone; err != nil {
		fmt.Fprintln(stderr, "blinksend: recorder:", err)
		return 1
	}
	if err := rec.Close(); err != nil {
		fmt.Fprintln(stderr, "blinksend: recorder:", err)
		return 1
	}
	if scriptErr != nil {
		fmt.Fprintln(stderr, "blinksend:", scriptErr)
		return 1
	}
	fmt.Fprintf(stdout, "blinksend: %s scenario sent, trace in %s\n", *scenario, *traceFile)
	return 0
}

func keyState(key byte, color uint16, blink bool) transport.KeyState {
	return transport.KeyState{
		KeyIndex: key,
		Version:  transport.ProtocolVersion,
		Color:    color,
		Blink:    blink,
	}
}

// sendSingle makes keys 0 to 2 blink, then updates key 4 ten times, 2 s
// apart, in alternating colors so every update changes the key.
func sendSingle(dev transport.Transport) error {
	for key := byte(0); key < 3; key++ {
		if err := dev.SendKeyState(keyState(key, 0x001F, true)); err != nil {
			return err
		}
	}
	time.Sleep(settle)

	colors := [2]uint16{0xF800, 0x07E0}
	for i := 0; i < singleUpdates; i++ {
		if err := dev.SendKeyState(keyState(4, colors[i%2], false)); err != nil {
			return err
		}
		time.Sleep(singleSpacing)
	}
	return nil
}

// sendBurst updates six keys with no pause of its own. SendKeyState holds
// each report back until minReportGap has passed since the one before.
func sendBurst(dev transport.Transport) error {
	for i := 0; i < burstUpdates; i++ {
		if err := dev.SendKeyState(keyState(byte(i), uint16(0x0800*(i+1)), false)); err != nil {
			return err
		}
	}
	return nil
}

// busyBurstEmojiBase tags each report of the busy burst: key k carries Emoji
// ID busyBurstEmojiBase+k, which the board's HOST_MESSAGE_DECODED record
// repeats as its payload, so tools/blink_trace.py can tell a burst report
// from a setup report.
const busyBurstEmojiBase = 0x20

// sendBusyBurst makes keys 0 to 2 blink, then sends one update to each of
// the six keys back to back while they do. A blinking key costs the board a
// push every 500 ms, so its loop is slower than when idle, and a report that
// arrives before the loop reads the last one overwrites it.
func sendBusyBurst(dev transport.Transport) error {
	for key := byte(0); key < 3; key++ {
		if err := dev.SendKeyState(keyState(key, 0xF800, true)); err != nil {
			return err
		}
	}
	time.Sleep(settle)

	for key := byte(0); key < burstUpdates; key++ {
		ks := keyState(key, uint16(0x0800*(int(key)+1)), key < 3)
		ks.EmojiID = busyBurstEmojiBase + key
		if err := dev.SendKeyState(ks); err != nil {
			return err
		}
	}
	return nil
}

// sendGlyphs sends one custom glyph to each of the six keys, the way a layer
// change does: the glyph, then a key state that names it. It prints how long
// each SendCustomGlyph write blocked, which is how long the board took to
// drain the 32 KB frame from the CDC buffer, and the time for all six keys
// (task 0057's layer redraw took 3 to 6 s; task 0058 shortens it).
//
// With batch, it sends Begin batch first, naming all six keys, so the board
// draws them together (task 0060). `tools/blink_trace.py report
// --scenario=batch` reads the span of the six pushes from the trace.
func sendGlyphs(dev transport.Transport, stdout io.Writer, batch bool, glyph func(key byte) ([]byte, error)) error {
	total := time.Now()
	if batch {
		const allKeys = 0b111111
		if err := dev.BeginBatch(allKeys, allKeys); err != nil {
			return err
		}
	}
	for key := byte(0); key < 6; key++ {
		pixels, err := glyph(key)
		if err != nil {
			return err
		}
		start := time.Now()
		if err := dev.SendCustomGlyph(key, pixels); err != nil {
			return err
		}
		wrote := time.Since(start)
		ks := keyState(key, 0x0000, false)
		ks.EmojiID = transport.CustomGlyphSentinelEmojiID
		if err := dev.SendKeyState(ks); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "blinksend: key %d: glyph write %s, glyph+state %s\n",
			key, wrote.Round(time.Millisecond), time.Since(start).Round(time.Millisecond))
	}
	fmt.Fprintf(stdout, "blinksend: six keys in %.2f s\n", time.Since(total).Seconds())
	return nil
}

// noiseGlyph returns a CustomGlyphWidth × CustomGlyphHeight RGB565 picture of
// pseudo-random pixels that depends on key. It does not compress, so it is
// the worst case for any scheme that sends fewer bytes. No pixel is
// transparent.
func noiseGlyph(key byte) ([]byte, error) {
	pixels := make([]byte, transport.CustomGlyphPixelsSize)
	state := uint32(key)*2654435761 + 1
	for i := 0; i < len(pixels); i += 2 {
		state = state*1664525 + 1013904223
		v := uint16(state>>16) | 1
		pixels[i], pixels[i+1] = byte(v>>8), byte(v)
	}
	return pixels, nil
}

// iconGlyph returns the key-th PNG in dir, in file name order, as RGB565
// pixels: the same pictures a layer shows.
func iconGlyph(dir string, key byte) ([]byte, error) {
	names, err := filepath.Glob(filepath.Join(dir, "*.png"))
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	if int(key) >= len(names) {
		return nil, fmt.Errorf("%s holds %d PNG files, want at least %d", dir, len(names), key+1)
	}
	data, err := os.ReadFile(names[key])
	if err != nil {
		return nil, err
	}
	return transport.DecodePNGToRGB565(data)
}

// sendSync makes all six keys blink, then updates key 4 ten times, 2 s apart,
// in alternating colors, keeping it blinking. The update is the second report
// decoded for key 4, which tools/blink_trace.py uses as the start of its
// window: the setup decodes key 4 once, and each update decodes it again.
func sendSync(dev transport.Transport) error {
	for key := byte(0); key < 6; key++ {
		if err := dev.SendKeyState(keyState(key, 0x001F, true)); err != nil {
			return err
		}
	}
	time.Sleep(settle)

	colors := [2]uint16{0xF800, 0x07E0}
	for i := 0; i < syncUpdates; i++ {
		if err := dev.SendKeyState(keyState(4, colors[i%2], true)); err != nil {
			return err
		}
		time.Sleep(syncSpacing)
	}
	return nil
}
