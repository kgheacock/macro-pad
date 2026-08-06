# driver

The host controller. It runs on the computer the macro pad plugs into, and
talks to the firmware over USB HID and CDC serial.

See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for the byte layout
of every message sent or received over these channels.

**Status: in progress.** [`transport/`](transport/) defines the `Transport`
interface and an in-memory emulator for driver tests. No other driver code
exists yet.

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

A later task wires `Transport` to a real HID/CDC implementation. Task 0010
checks that implementation against real hardware.

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
