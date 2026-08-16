# firmware

CircuitPython firmware for the Pimoroni Pico Plus 2 (RP2350). Run `make
flash` from the repo root to copy this folder onto the `CIRCUITPY`
drive.

## Setup

The board needs CircuitPython itself installed before this folder's
contents mean anything. From the repo root, run:

```bash
make firmware-uf2
```

This downloads the pinned CircuitPython 10.2.1 build for board id
`pimoroni_pico_plus2` into `firmware/modules/` (gitignored — re-run the
target any time it's missing). Hold `BOOTSEL` while plugging in the
board, drag the UF2 onto the `RP2350` drive that appears, then wait for
it to reboot as `CIRCUITPY`.

With `CIRCUITPY` mounted, run:

```bash
make flash
```

This copies `firmware/` onto the `CIRCUITPY` drive with `rsync --delete`,
apart from `modules/`, `__pycache__/`, and this `README.md`. `--delete`
means any file on the drive that isn't in `firmware/` is removed,
including test data or logs a developer added on the board directly. The
target fails with a clear error if `CIRCUITPY` isn't mounted.

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
2. Decode one Set custom glyph message from the CDC data channel, if a
   full one has arrived, and update that key's render state — see
   "Custom glyphs and persisted state," below.
3. Read every switch, debounce it, and write a 10-byte event per accepted
   transition to the CDC data channel.
4. Redraw the keys that changed, plus every key that blinks.
5. Set each backlight from the idle timer.

`wire.py` encodes and decodes the messages in
[`docs/wire-protocol.md`](../docs/wire-protocol.md). It is the firmware
half of the contract `driver/transport/wire.go` implements on the host
side, including the same refusal to read a key state message whose
version byte is not the one this build was written against.

Modules under `firmware/` import each other flat (`import wire`, not
`from firmware import wire`), because this folder's contents are copied
to the root of the `CIRCUITPY` drive, where no `firmware` package exists.

## Glyphs

`firmware/glyphs.py` maps a wire-protocol emoji ID to a one-bit glyph
bitmap. `display_render.render_key` draws through it via the
`emoji_lookup` callable, so this module never appears in the render loop
directly. See [`docs/wire-protocol.md`](../docs/wire-protocol.md#emoji-ids)
for the reserved IDs.

The file is generated, not hand-written. To add or change a glyph:

1. Add or replace a 128×128 PNG in [`../hardware/glyphs/`](../hardware/glyphs/),
   named after the emoji ID's source, and add its emoji ID to
   `tools/gen_glyphs.py`'s `SOURCES` dict if it's new.
2. Install the one build-time dependency this needs (not required to run
   the test suite otherwise) and regenerate:

   ```bash
   .venv/bin/pip install pillow
   .venv/bin/python3 tools/gen_glyphs.py
   ```

3. Commit both the PNG and the regenerated `firmware/glyphs.py`.

Running the generator again with no source changes must leave
`firmware/glyphs.py` byte-identical — that's what keeps a hand-edit of
the generated file visible in review.

## Custom glyphs and persisted state

A driver call can send an arbitrary 128×128 image for one key over CDC —
"Set custom glyph" in [`docs/wire-protocol.md`](../docs/wire-protocol.md)
— instead of one of `firmware/glyphs.py`'s built-in IDs. `wire.py`'s
`CustomGlyphReader` buffers this message's bytes across as many `step`
calls as it takes to arrive, since at up to 32,769 bytes it is too large
to read in one iteration without stalling the switch scan.
`display_render.raw_bitmap_tile_grid` renders the result the same way
`glyphs.lookup` renders a built-in one.

`glyph_state.py` persists each key's last state — built-in or custom,
color, and blink — to one file per key under `glyph_state/`, so a key
redraws its own last state after a power cycle with no driver connected.
A firmware reflash (`make flash`) resets this: `glyph_state/` is not part
of the source tree that command syncs from (see its `.gitignore` entry),
so its `rsync --delete` removes the directory from the board on every
run. `MacroPad`'s `storage` constructor argument injects a fake for this
in tests, the same way `emoji_lookup` and the other hardware arguments
do; `code.py` relies on the real `glyph_state.FilesystemStorage` default.

## Connectivity check

`make ping-pong`, run from the repo root, proves the HID and CDC channels
task 0008's composite descriptor carries actually move data, with no
displays, mic, or switches involved and no console needed. It writes
`firmware/boot.py` and `firmware/ping_pong.py` to the `CIRCUITPY` drive as
`boot.py` and `code.py`, then runs a host command that sends a ping and
waits for the matching pong, printing `PASS` or `FAIL` and exiting with a
matching code. `firmware/code.py` and `firmware/boot.py` stay unchanged on
disk; run `make flash` afterward to put the real `code.py` back on the
board. See [`docs/wire-protocol.md`](../docs/wire-protocol.md#ping) for
the Ping and Pong message layout, and
[`driver/README.md`](../driver/README.md) for the host side of the check.

## Loop period

**Not yet measured.** No board is wired up yet. Task
[`0010`](../tasks/backlog/0010-hardware-bring-up-single-key.md) records
the figure with the displays attached.

To measure it, temporarily re-enable the serial console by running `make
debug` from the repo root. This writes a `boot.py` to the `CIRCUITPY`
drive with `console=True`; the tracked `firmware/boot.py` stays
unchanged. Find the console port yourself — the port that existed
before the board reset becomes the console port; the new port that
appears is the data port. Then copy this file to the `CIRCUITPY` drive
as `code.py` in place of the real one:

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

Run `make flash` afterward to put the real `code.py` and a
`console=False` `boot.py` back on the board. There is no separate
"undo" command — `make flash` is the exit path from debug mode.

Record the result here as a line of the form:

```
Measured loop period: N.NNN ms (RP2350, displays absent, 1000 iterations)
```

## Out of scope

- Click-pattern resolution (single vs. double vs. long press). The host
  resolves this from the raw timestamped events.
- The host controller itself (see [`driver/`](../driver/)).
