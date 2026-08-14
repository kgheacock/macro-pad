# firmware

CircuitPython firmware for the Pimoroni Pico Plus 2 (RP2350). Copy the
contents of this folder to the `CIRCUITPY` drive to flash the device.

## Setup

The board needs CircuitPython itself installed before this folder's
contents mean anything. From the repo root, run:

```bash
make firmware-uf2
```

This downloads the pinned CircuitPython 10.2.1 build for board id
`pimoroni_pico_plus2` into `firmware/modules/` (gitignored — re-run the
target any time it's missing). Hold `BOOTSEL` while plugging in the
board, drag the UF2 onto the `RPI-RP2` drive that appears, then wait for
it to reboot as `CIRCUITPY`.

## Scope

The firmware runs on the microcontroller only. It does the following:

- Renders the emoji, background color, and blink state for each key.
- Debounces the switch input (5 ms to 10 ms).
- Emits raw, timestamped press and release events. It does not classify
  single, double, or long presses — the host does that.
- Dims the PWM backlight after an idle window with no host update or key
  event (default: 5 minutes).
- Captures mic audio through I2S (`audiobusio.I2SIn`) while a key is held,
  and buffers it in a ring buffer.
- Streams the buffered audio to the host in fixed-size chunks. A 1-byte
  flag on the final chunk marks the end of the recording.
- Enumerates as a USB-C composite device:
  - HID, for the primary action.
  - CDC serial, for state, raw events, and audio.

See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for the byte layout
of every message sent or received over these channels.

## Out of scope

- Click-pattern resolution (single vs. double vs. long press). The host
  resolves this from the raw timestamped events.
- The host controller itself (see [`driver/`](../driver/)).
