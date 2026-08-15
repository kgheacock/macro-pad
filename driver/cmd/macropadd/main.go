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

	"github.com/kgheacock/macro-pad/driver/plugin"
	"github.com/kgheacock/macro-pad/driver/recorder"
	"github.com/kgheacock/macro-pad/driver/transport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("macropadd", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vendorID := fs.Uint("vendor-id", 0, "USB vendor ID of the macro pad, e.g. 0x2E8A")
	productID := fs.Uint("product-id", 0, "USB product ID of the macro pad, e.g. 0x0009")
	serialNumber := fs.String("serial", "", "USB serial number, to pick one device when more than one matches")
	port := fs.Int("port", plugin.DefaultPort, "TCP port the plugin WebSocket server binds on 127.0.0.1")
	traceFile := fs.String("trace-file", "", "write every device message to this JSONL flight-recorder file (see task 0025); empty disables recording")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	dev, err := transport.Open(ctx, transport.Options{
		VendorID:     uint16(*vendorID),
		ProductID:    uint16(*productID),
		SerialNumber: *serialNumber,
	})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
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

	srv := plugin.NewServer(fan.Subscribe())
	runErr := srv.Run(ctx, *port)

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
