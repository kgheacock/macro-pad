// Command macropadd is the long-running daemon that holds the macro pad's
// only transport.Device connection and exposes it to third-party plugins
// over a local WebSocket API. See driver/plugin and driver/README.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"

	"github.com/kgheacock/macro-pad/driver/layers"
	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/recorder"
	"github.com/kgheacock/macro-pad/driver/transport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// defaultStateDir is where macropadd keeps its state files unless told
// otherwise: macro-pad under the user's config directory, which is
// ~/Library/Application Support on macOS. It is empty, so state stays in
// memory, when the config directory is unknown.
func defaultStateDir() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "macro-pad")
}

func run(args []string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runCtx(ctx, args, stdout, stderr, nil)
}

// runCtx is run with the lifetime in ctx. A test passes onEmulator to get
// the emulator that --emulate creates, so it can read what the daemon sent.
func runCtx(ctx context.Context, args []string, stdout, stderr io.Writer, onEmulator func(*transport.Emulator)) int {
	fs := flag.NewFlagSet("macropadd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vendorID := fs.Uint("vendor-id", 0, "USB vendor ID of the macro pad, e.g. 0x2E8A")
	productID := fs.Uint("product-id", 0, "USB product ID of the macro pad, e.g. 0x0009")
	serialNumber := fs.String("serial", "", "USB serial number, to pick one device when more than one matches")
	keyStateOverHID := fs.Bool("key-state-over-hid", false, "send key states as HID reports, 150 ms apart, for firmware older than task 0059; the default is a Key state frame over CDC")
	cdcPort := fs.String("cdc-port", "", "CDC serial port to use directly (e.g. /dev/cu.usbmodem2103), bypassing discovery; needed when the board exposes more than one CDC port, such as a debug console alongside the data channel")
	port := fs.Int("port", plugin.DefaultPort, "TCP port the plugin WebSocket server binds on 127.0.0.1")
	stateDir := fs.String("state-dir", defaultStateDir(), "directory where the last state of every key and the layer tree (layers.json) are saved and loaded, so a daemon restart or a host reboot loses nothing; empty keeps it in memory only")
	assetsDir := fs.String("assets-dir", "assets", "directory the iconRef of a layer key resolves under, and where a child layer finds shift.png (see task 0057)")
	traceFile := fs.String("trace-file", "", "write every device message to this JSONL flight-recorder file (see task 0025); empty disables recording")
	emulate := fs.Bool("emulate", false, "run against an in-memory emulator instead of a real board, so a virtual pad plugin (see driver/plugin/web/virtualpad.html) can inject presses with no hardware attached")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	var dev transport.Transport
	var injector plugin.Injector
	if *emulate {
		emu := transport.NewEmulator()
		dev, injector = emu, emu
		if onEmulator != nil {
			onEmulator(emu)
		}
	} else {
		// The board keeps no key state across a power cycle (task 0044), so
		// this transport remembers each key's last state and replays it
		// every time the board connects. It also waits for a board that is
		// not attached yet, and survives a replug.
		dev = transport.NewReconnecting(transport.Options{
			VendorID:     uint16(*vendorID),
			ProductID:    uint16(*productID),
			SerialNumber: *serialNumber,
			CDCPort:      *cdcPort,

			KeyStateOverHID: *keyStateOverHID,
		}, *stateDir, func(format string, args ...any) {
			fmt.Fprintf(stdout, "macropadd: "+format+"\n", args...)
		})
	}

	// dev.ReadMessage supports exactly one caller. plugin.Server and, when
	// enabled, the recorder each need their own read of every device
	// message, so Fanout is the one caller and hands each of them their
	// own subscription instead. See driver/transport/fanout.go.
	fan := transport.NewFanout(dev)
	go fan.Run()

	// recDone signals once the recorder has flushed every buffered line
	// to traceF. dev.Close, below, is what makes that happen: it ends
	// the recorder's ReadMessage loop. traceF must not close until
	// recDone fires, or a buffered line can be lost.
	var recDone chan struct{}
	var traceF *os.File
	if *traceFile != "" {
		f, err := os.Create(*traceFile)
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		traceF = f
		recDone = make(chan struct{})

		rec := recorder.New(fan.Subscribe(), f)
		go func() {
			defer close(recDone)
			if err := rec.Run(); err != nil {
				fmt.Fprintln(stderr, "macropadd: recorder:", err)
				return
			}
			if err := rec.Close(); err != nil {
				fmt.Fprintln(stderr, "macropadd: recorder:", err)
			}
		}()
		fmt.Fprintf(stdout, "macropadd: recording to %s\n", *traceFile)
	}

	fmt.Fprintf(stdout, "macropadd: listening on 127.0.0.1:%d\n", *port)

	srv := plugin.NewServer(fan.Subscribe(), injector)
	srv.SetLogf(func(format string, args ...any) {
		fmt.Fprintf(stdout, "macropadd: "+format+"\n", args...)
	})

	// The layer tree draws through srv, so a plugin sees every key it
	// sets, and srv hands it each single press and setLayerKey message.
	ctl, err := layers.Open(layers.Options{
		Dir:       *stateDir,
		AssetsDir: *assetsDir,
		Out:       srv,
		Logf: func(format string, args ...any) {
			fmt.Fprintf(stdout, "macropadd: "+format+"\n", args...)
		},
	})
	if err != nil {
		dev.Close()
		fmt.Fprintln(stderr, "macropadd: layers:", err)
		return 1
	}
	srv.SetLayers(ctl)

	runErr := srv.Run(ctx, *port)

	// Close saves the layer tree one last time, before the device closes.
	if err := ctl.Close(); err != nil {
		fmt.Fprintln(stderr, "macropadd: layers:", err)
	}
	dev.Close()
	if recDone != nil {
		<-recDone
		traceF.Close()
	}

	if runErr != nil {
		fmt.Fprintln(stderr, runErr)
		return 1
	}
	return 0
}
