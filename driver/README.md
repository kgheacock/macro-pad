# driver

The host controller. It runs on the computer the macro pad plugs into, and
talks to the firmware over USB HID and CDC serial.

See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for the byte layout
of every message sent or received over these channels.

**Status: in progress.** [`transport/`](transport/) defines the `Transport`
interface, an in-memory emulator for driver tests, and `Device`, the
hardware implementation. [`plugin/`](plugin/) implements the local
WebSocket API described below, served by the
[`macropadd`](cmd/macropadd/) daemon.

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
	// Confirmed at bring-up (task 0021's open question) from
	// `ioreg -p IOUSB -l` with the board attached, running CircuitPython.
	VendorID:  0x2E8A,
	ProductID: 0x10A3,
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

## Plugin API

Nothing stops two processes from opening the same `transport.Device` at
once, and third-party automation has no way to react to a key press or set
a key's color and emoji without opening the raw HID and CDC connection
itself. `driver/cmd/macropadd` is the one long-running daemon meant to hold
that connection; every other process — a plugin, written in any language —
reaches the board through it instead.

Run it with:

```bash
go run ./driver/cmd/macropadd --vendor-id=0x2E8A --product-id=0x10A3
```

`macropadd` opens one `transport.Device` (see "Connecting to a real
board," above) and runs `plugin.Server`, which accepts WebSocket
connections on `127.0.0.1:8765` by default (override with `--port`). A
WebSocket needs no OS-specific client code — a plugin can be a Python or
Node script, a shell script speaking a WebSocket library, or a browser
tab. The server accepts connections from `localhost` only; it never binds
a non-loopback interface, regardless of `--port`.

### Protocol

Every message on the connection is JSON, shaped by
[`plugin/protocol.go`](plugin/protocol.go):

- **`event`** — device to client. Wraps one decoded `transport.Event`:

  ```json
  {"kind": "event", "event": {"keyIndex": 2, "type": "press", "timestamp": 123456}}
  ```

- **`setKeyState`** — client to device. Becomes one
  `transport.Transport.SendKeyState` call; the server fills in the wire
  protocol's version byte, so a plugin never has to track it:

  ```json
  {"kind": "setKeyState", "setKeyState": {"keyIndex": 2, "color": 63488, "emojiId": 7, "blink": true}}
  ```

### Bounds

A slow or stalled client cannot grow the daemon's memory past a fixed
bound, and cannot block delivery to other clients:

- **16 clients** (`maxClients`) — the server rejects a connection past
  this cap, with a WebSocket close reason, instead of accepting it.
- **32 messages per client** (`clientQueueSize`) — each client gets a
  fixed-size, buffered send queue, sized like `deviceQueueSize` in
  `driver/transport`. The dispatch loop's send to it is non-blocking: a
  full queue drops the next message for that client only, so one slow
  client never stalls delivery to the rest.
- **8 drops** (`maxDrops`) — a client whose queue has dropped this many
  messages is disconnected. The daemon keeps running for every other
  client.

Total memory for queued-but-undelivered messages therefore never exceeds
`maxClients × clientQueueSize` messages.

**Out of scope for this API:** click-pattern resolution (single, double,
long press — see tasks 0011–0013), binary audio streaming to a client, and
auth beyond the `localhost` bind and the client cap.

### Recording a trace alongside the plugin API

`transport.Device.ReadMessage` supports exactly one caller — it drains
one internal channel, not a broadcast. `macropadd` needs two, once task
0025's flight recorder is turned on: `plugin.Server` for its clients, and
`driver/recorder.Recorder` for the JSONL file. `transport.Fanout`
(`transport/fanout.go`) is the one caller of `ReadMessage` in that case;
it hands each of them their own subscription — itself a `Transport` — so
neither one calling `ReadMessage` starves the other. Pass `--trace-file`
to turn recording on:

```bash
go run ./driver/cmd/macropadd --vendor-id=0x2E8A --product-id=0x10A3 \
  --trace-file=/tmp/macropad-trace.jsonl
```

`--trace-file` is empty, and recording off, by default — the recorder
buffers every line in memory until the daemon stops, per task 0025's
design, so leaving it on for a long session is a deliberate choice, not
a default.

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
