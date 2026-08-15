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
	defer dev.Close()

	fmt.Fprintf(stdout, "macropadd: listening on 127.0.0.1:%d\n", *port)

	srv := plugin.NewServer(dev)
	if err := srv.Run(ctx, *port); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
