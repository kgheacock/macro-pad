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
  - HID, for the key state message the host sends.
  - CDC serial (data channel), for raw events and audio the device sends.

`boot.py` configures both interfaces, including the HID report
descriptor. See [`docs/wire-protocol.md`](../docs/wire-protocol.md) for
the byte layout of every message sent or received over these channels.

## Entry point

CircuitPython runs `boot.py` first, then `code.py`. `code.py` is this
firmware's entry point.

`code.py` only builds the real hardware objects — switches, displays,
backlights, the HID device, and the CDC data channel — and passes them to
`app.MacroPad`, then calls `run()`. It holds no loop logic of its own.

The loop lives in `app.py`. `MacroPad` takes every hardware object as a
constructor argument, so the same loop runs under pytest against the
fakes in [`../test/stubs/`](../test/stubs/) with no board attached. One
`step(now_us)` call does this, in order:

1. Decode one HID key state message and update that key's render state.
2. Read every switch, debounce it, and write a 10-byte event per accepted
   transition to the CDC data channel.
3. Redraw the keys that changed, plus every key that blinks.
4. Set each backlight from the idle timer.

`wire.py` encodes and decodes the messages in
[`docs/wire-protocol.md`](../docs/wire-protocol.md). It is the firmware
half of the contract `driver/transport/wire.go` implements on the host
side, including the same refusal to read a key state message whose
version byte is not the one this build was written against.

Modules under `firmware/` import each other flat (`import wire`, not
`from firmware import wire`), because this folder's contents are copied
to the root of the `CIRCUITPY` drive, where no `firmware` package exists.

## Loop period

**Not yet measured.** No board is wired up yet. Task
[`0010`](../tasks/backlog/0010-hardware-bring-up-single-key.md) records
the figure with the displays attached.

To measure it, temporarily re-enable the serial console by changing the
last line of `boot.py` to `usb_cdc.enable(console=True, data=True)`, then
copy this file to the `CIRCUITPY` drive as `code.py` in place of the real
one:

```python
import time

import board
import pins
from app import MacroPad, blank_glyph, make_switch


class NullDisplay:
    def show(self, group): pass
    def refresh(self, **kwargs): return True


class NullBacklight:
    duty_cycle = 1.0


class NullSerial:
    def write(self, data): return len(data)


class NullHID:
    def get_last_received_report(self, report_id=None): return None


pad = MacroPad(
    switches=[make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS],
    displays=[NullDisplay() for _ in pins.KEYS],
    backlights=[NullBacklight() for _ in pins.KEYS],
    hid_device=NullHID(),
    serial=NullSerial(),
    emoji_lookup=blank_glyph,
)

ITERATIONS = 1000
start = time.monotonic_ns()
for _ in range(ITERATIONS):
    pad.step(time.monotonic_ns() // 1000)
elapsed_ms = (time.monotonic_ns() - start) / 1_000_000

print("loop period: {:.3f} ms".format(elapsed_ms / ITERATIONS))
```

Read the printed line from the host:

```bash
screen "$(ls /dev/cu.usbmodem*)" 115200
```

This measures the loop with the displays absent, which is the floor. A
real SPI refresh across six panels adds to it — that is the cost the
task [`0022`](../tasks/ongoing/0022-firmware-main-loop.md) decision
accepted, and the number task 0010 must check.

Record the result here as a line of the form:

```
Measured loop period: N.NNN ms (RP2350, displays absent, 1000 iterations)
```

## Out of scope

- Click-pattern resolution (single vs. double vs. long press). The host
  resolves this from the raw timestamped events.
- The host controller itself (see [`driver/`](../driver/)).
