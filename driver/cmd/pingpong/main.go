// Command pingpong drives transport.Device to prove the macro pad's HID
// and CDC channels carry data. It sends a ping carrying a random nonce and
// waits for the matching pong, printing PASS or FAIL and exiting with a
// matching code. See docs/wire-protocol.md's "Ping" section and `make
// ping-pong` in the repo root Makefile.
package main

import (
	"context"
	"crypto/rand"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kgheacock/macro-pad/driver/transport"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("pingpong", flag.ContinueOnError)
	fs.SetOutput(stderr)
	vendorID := fs.Uint("vendor-id", 0, "USB vendor ID of the macro pad, e.g. 0x2E8A")
	productID := fs.Uint("product-id", 0, "USB product ID of the macro pad, e.g. 0x0009")
	serialNumber := fs.String("serial", "", "USB serial number, to pick one device when more than one matches")
	timeout := fs.Duration("timeout", 5*time.Second, "how long to wait for the device and its pong")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	dev, err := transport.Open(ctx, transport.Options{
		VendorID:     uint16(*vendorID),
		ProductID:    uint16(*productID),
		SerialNumber: *serialNumber,
	})
	if err != nil {
		fmt.Fprintln(stdout, "FAIL")
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer dev.Close()

	nonce, err := randomNonce()
	if err != nil {
		fmt.Fprintln(stdout, "FAIL")
		fmt.Fprintln(stderr, err)
		return 1
	}

	ok, err := ping(ctx, dev, nonce, stdout)
	if err != nil {
		fmt.Fprintln(stdout, "FAIL")
		fmt.Fprintln(stderr, err)
		return 1
	}
	if !ok {
		return 1
	}
	return 0
}

// pingResult carries one ReadMessage outcome back to ping's select, so a
// blocked read can't stop ping from returning once ctx is done.
type pingResult struct {
	msg transport.Message
	err error
}

// ping sends a Key state message with transport.PingKeyIndex and nonce,
// then waits for a Pong to come back. It reports PASS to out and returns
// true only when the pong's nonce matches; any other outcome — a wrong
// nonce, a non-Pong message, a read error, or ctx expiring — reports FAIL
// and returns false.
func ping(ctx context.Context, t transport.Transport, nonce byte, out io.Writer) (bool, error) {
	if err := t.SendKeyState(transport.KeyState{
		KeyIndex: transport.PingKeyIndex,
		Version:  transport.ProtocolVersion,
		EmojiID:  nonce,
	}); err != nil {
		return false, fmt.Errorf("send ping: %w", err)
	}

	resCh := make(chan pingResult, 1)
	go func() {
		msg, err := t.ReadMessage()
		resCh <- pingResult{msg: msg, err: err}
	}()

	select {
	case <-ctx.Done():
		fmt.Fprintln(out, "FAIL")
		return false, ctx.Err()
	case res := <-resCh:
		if res.err != nil {
			fmt.Fprintln(out, "FAIL")
			return false, fmt.Errorf("read pong: %w", res.err)
		}
		if res.msg.Type != transport.MessageTypePong || res.msg.Pong.Nonce != nonce {
			fmt.Fprintln(out, "FAIL")
			return false, nil
		}
		fmt.Fprintln(out, "PASS")
		return true, nil
	}
}

func randomNonce() (byte, error) {
	var b [1]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, fmt.Errorf("generate nonce: %w", err)
	}
	return b[0], nil
}
