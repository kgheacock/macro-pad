// Command blinksend sends one scripted run of key-state updates to the
// macro pad and records the board's trace of it to a JSONL file. `make
// blink-trace` runs it, then reads the file with tools/blink_trace.py. See
// tasks/ongoing/0044-blink-independence-persist-off-blink-path.md.
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
	scenario := fs.String("scenario", "single", "which run to send: single, burst, busyburst, or warmup (send nothing)")
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
	case "warmup":
		// Sends nothing. The first connection after the board reloads
		// code.py has given an unreadable trace stream and no decoded
		// reports, and the next connection has worked every time, so
		// `make blink-trace` opens the board once before it measures.
		script = func(transport.Transport) error { return nil }
	default:
		fmt.Fprintf(stderr, "blinksend: unknown scenario %q, want single, burst, busyburst or warmup\n", *scenario)
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
