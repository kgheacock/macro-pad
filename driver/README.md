# driver

The host controller. It runs on the computer the macro pad plugs into, and
talks to the firmware over USB HID and CDC serial.

See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for the byte layout
of every message sent or received over these channels.

**Status: in progress.** [`transport/`](transport/) defines the `Transport`
interface, an in-memory emulator for driver tests, and `Device`, the
hardware implementation. No other driver code exists yet.

## Testing without hardware

No board is wired up yet (see
[task 0010](../tasks/backlog/0010-hardware-bring-up-single-key.md)).
`transport.Emulator`
implements `Transport` in memory, encoding and decoding the exact byte
layout in [`docs/wire-protocol.md`](../docs/wire-protocol.md) instead of a
simplified stand-in. Test code calls `InjectEvent` or `InjectAudioChunk` to
simulate the device side, and `LastKeyState` to inspect what the driver
sent, all without a board attached.

Run the driver tests from the repository root:

```bash
go test ./driver/transport/...
```

Task 0010 checks `transport.Device`, below, against real hardware.

## Connecting to a real board

`transport.Device` implements `Transport` over a real macro pad, using the
operating system's own HID and CDC class drivers instead of claiming USB
interfaces directly:

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
defer cancel()
dev, err := transport.Open(ctx, transport.Options{
	// VendorID and ProductID are not filled in above: task 0021's open
	// questions leave them for the hardware owner to confirm at bring-up,
	// from `system_profiler SPUSBDataType`.
	VendorID:  0x0000,
	ProductID: 0x0000,
})
```

`Open` matches `Options.VendorID` and `Options.ProductID` against attached
HID devices, reads the match's USB serial number, then opens the CDC
serial port carrying the same serial number — never by listing mounted
volumes. It retries until `ctx` is done, so a caller can hold `Open`
across the reboot that flashing new firmware causes. If more than one
matching device is attached, set `Options.SerialNumber` to pick one;
otherwise `Open` returns `ErrAmbiguousDevice`.

**cgo requirement.** `Device` binds `github.com/sstallion/go-hid`, a cgo
wrapper around hidapi, to reach the HID output report. Building or testing
any package that imports `transport` therefore requires `CGO_ENABLED=1`
and a C toolchain (Xcode Command Line Tools on macOS). `go.bug.st/serial`,
which carries the CDC channel, is pure Go and does not add this
requirement on its own.

**macOS only.** `Open`'s HID/CDC device-matching relies on macOS binding
its class drivers to the composite device task 0008 describes; the other
platforms hidapi and go.bug.st/serial support are untested here and out of
scope for this project.

## Connectivity check

`driver/cmd/pingpong` sends a ping — a Key state message with the
reserved index `255` and a random nonce in the Emoji ID byte — over
`transport.Device`, then waits for the matching pong and prints `PASS` or
`FAIL`, exiting with a matching code. Run it with `make ping-pong` from
the repo root, next to `make debug`; that target flashes the minimal
firmware image in `firmware/ping_pong.py` first, so no board setup beyond
having `CIRCUITPY` mounted is needed. See
[`docs/wire-protocol.md`](../docs/wire-protocol.md#ping) for the Ping and
Pong message layout, and [`firmware/README.md`](../firmware/README.md)
for the firmware side of the check.

## Scope

Language: Go.

The driver does the following:

- Sends the emoji, task ID, color, and blink state for each key to the
  firmware.
- Resolves press timing from the firmware's raw, timestamped events —
  classifies single press, double press, and long press (hold).
- Maps resolved events to host-defined actions.
- Decodes the chunked audio stream and reassembles it using the final-chunk
  flag.

## Out of scope

- Rendering, debouncing, and raw event emission. The firmware
  ([`firmware/`](../firmware/)) does that.
