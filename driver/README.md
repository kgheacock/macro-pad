# driver

The host controller. It runs on the computer the macro pad plugs into, and
talks to the firmware over USB HID and CDC serial.

See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for the byte layout
of every message sent or received over these channels.

**Status: deferred.** This pass covers hardware and firmware only. No driver
code exists yet.

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
