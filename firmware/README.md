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
apart from `modules/`, `__pycache__/`, `lib/`, and this `README.md`.
`--delete` means any other file on the drive that isn't in `firmware/`
is removed, including test data or logs a developer added on the board
directly. `lib/` is excluded so a CircuitPython library installed there
with `circup` survives a reflash. The firmware needs none: `st7735.py`
drives the panels itself, so `adafruit_st7735r` is no longer required. The target fails with a clear error if `CIRCUITPY` isn't
mounted.

## Scope

The firmware runs on the microcontroller only. It does the following:

- Renders the glyph, background color, and blink state for each key.
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

`code.py` only builds the real hardware objects — switches, six
`st7735.Panel`s, backlights, the HID device, and the CDC data channel —
pulses the shared reset line once and initialises each panel once (see
"Panels," below), and passes everything to `app.MacroPad`, then calls
`run()`. It holds no loop logic of its own.

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
4. Redraw the keys that changed, plus every key that blinks — see
   "Panels" and "Cached frames," below.
5. Set each backlight from the idle timer.

`wire.py` encodes and decodes the messages in
[`docs/wire-protocol.md`](../docs/wire-protocol.md). It is the firmware
half of the contract `driver/transport/wire.go` implements on the host
side, including the same refusal to read a key state message whose
version byte is not the one this build was written against.

Modules under `firmware/` import each other flat (`import wire`, not
`from firmware import wire`), because this folder's contents are copied
to the root of the `CIRCUITPY` drive, where no `firmware` package exists.

## Panels

All six panels share one `busio.SPI` bus and one DC line and one RST line.
Each panel has its own chip-select line. This board's CircuitPython build
allows only 1 concurrent `displayio` display bus, so `displayio` had to
release the bus and build a new one on every key switch. Each build
pulsed the shared RST line and reran the panel's 763 ms init, so an
update to one key blanked all six. Tasks 0032 and 0040 worked within that
limit, and 0040 only fixed repeat redraws of the same key.

Task 0043 drops `displayio` for the panels. `st7735.py` has one `Panel`
for each key. `code.py` pulses RST once at boot, holds it high, and runs
each panel's init once (`st7735.init_panels`). After that the only thing
the firmware sends a panel is `Panel.push(frame)`: it takes the shared bus
under that panel's CS line, sets the draw window with the panel's column
and row offsets, and writes the frame. It sends no init command and
touches no RST, so a key's update never reaches another panel.

`DISPLAY_BAUDRATE` in `code.py` is 16 MHz. A frame push takes 21 ms at
16 MHz and 79 ms at 4 MHz. On 2026-10-06 a test pattern of color bars and
1 px columns rendered cleanly on all six wired panels at 4, 8, 12, 16, and
20 MHz. The RP2350 steps its SPI clock down from 150 MHz, so 16 MHz runs at
15 MHz. Above about 19 MHz a push stays near 17 ms, because the CPU that
feeds the FIFO is the limit, not the wire. A higher rate buys little unless
a DMA transfer does the feeding.

See [`tasks/ongoing/0043-raw-spi-panels-init-once.md`](../tasks/ongoing/0043-raw-spi-panels-init-once.md)
for the design decision. It replaces
[0032](../tasks/ongoing/0032-share-one-display-bus-across-six-keys.md),
[0033](../tasks/ongoing/0033-mutate-display-scene-graph-in-place.md), and
[0040](../tasks/ongoing/0040-keep-active-keys-display-bus-open-across-redraws.md).

## Cached frames

`display_render.render_key` keeps two cached 128×128 frames for each key
on its `KeyState`: an "on" frame, and an "off" frame while the key
blinks. A frame is a 16-bit `displayio.Bitmap` used as a plain pixel
buffer; `displayio` never shows it. The board builds a frame with
`bitmaptools`: it fills the key's color, then blits the glyph over it and
skips the transparent value `0x0000`.

The board rebuilds the frames only when the key's color or glyph changes,
or when the key starts blinking. A blink toggle pushes the other cached
frame and calls no fill or blit. The host driver converts a glyph to the
panel's big-endian RGB565 before it sends it, so the board converts no
pixel and swaps no byte.

- "On" frame: the key's color with the glyph over it.
- "Off" frame, for a glyph with a transparent pixel, or for a key with no
  glyph: the same frame over black. The glyph stays and the color behind
  it blinks. A key with no glyph blinks between its color and black.
- "Off" frame, for a glyph with no transparent pixel: the key's color
  alone, so the whole glyph blinks.

Two frames for each of six keys use 384 KB of the board's heap, which has
about 8 MB free.

**Latency, measured on the board** from the trace of `macropadd
--trace-file`, with `HOST_MESSAGE_DECODED` as the start:

```
Measured color change: 46.5 ms to 54.4 ms to REFRESH_DONE, blink push: 23.5 ms (RP2350, key 0, 16 MHz, 2026-10-05)
```

The 46 ms is a 23 ms compose (`GLYPH_BUILT`) and a 23 ms push. A push of a
cached frame alone takes 23 ms. At 4 MHz a push takes 81 ms, so a color
change takes about 105 ms and a blink push 81 ms. `code.py` now sets 16 MHz,
so the figures above apply.
Persisting to flash takes 240 ms to 360 ms and runs after the redraw, in
the same `step`, so it delays the next `step` and not the new image.

## Custom glyphs and persisted state

A driver call can send an arbitrary 128×128 image for one key over CDC —
"Set custom glyph" in [`docs/wire-protocol.md`](../docs/wire-protocol.md)
— instead of a plain color. `wire.py`'s `CustomGlyphReader` buffers this
message's bytes across as many `step` calls as it takes to arrive, since
at up to 32,769 bytes it is too large to read in one iteration without
stalling the switch scan. A message of the wrong length, or for a key
this pad does not have, is dropped. The pixels are 128×128 big-endian
RGB565, with `0x0000` for a transparent pixel and `0x0001` for real
black; see "Set custom glyph" in the wire protocol. Every Emoji ID draws
no glyph: the board has no built-in glyph table (task 0039), so a key
shows its color until a custom glyph arrives.

`glyph_state.py` persists each key's last state — built-in or custom,
color, and blink — to one file per key under `glyph_state_files/`, so a key
redraws its own last state after a power cycle with no driver connected.
A firmware reflash (`make flash`) resets this: `glyph_state/` is not part
of the source tree that command syncs from (see its `.gitignore` entry),
so its `rsync --delete` removes the directory from the board on every
run. `MacroPad`'s `storage` constructor argument injects a fake for this
in tests, the same way `panels` and the other hardware arguments do;
`code.py` relies on the real `glyph_state.FilesystemStorage` default.

A record starts with a format byte (task 0043). A record from before
that task has none and holds RGBA4444 pixels, which the board cannot
show: it loads as its color and blink alone, and the driver resends the
glyph.

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

## Soldering check

`firmware/pin_voltage_check.py` verifies a soldered header pin by driving
it HIGH and LOW from the REPL while you read it with a multimeter in DC
voltage mode — more diagnostic than a continuity check, since a pin that
follows its neighbor instead of the console print reveals a solder
bridge, not just an open joint. It is not part of `code.py`'s loop; after
`make flash` puts it on the `CIRCUITPY` drive, connect over serial,
interrupt `code.py` with Ctrl-C, then run:

```python
import pin_voltage_check
pin_voltage_check.check_pin("GP13")
```

See the module's docstring for how to read the result, and
`pin_voltage_check.HEADER_PINS` for the full list of pins
`firmware/pins.py` assigns.

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
from app import MacroPad, make_switch


class NullPanel:
    def push(self, frame): pass


class NullBacklight:
    duty_cycle = 1.0


class NullSerial:
    def write(self, data): return len(data)


class NullHID:
    def get_last_received_report(self, report_id=None): return None


pad = MacroPad(
    switches=[make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS],
    panels=[NullPanel() for _ in pins.KEYS],
    backlights=[NullBacklight() for _ in pins.KEYS],
    hid_device=NullHID(),
    serial=NullSerial(),
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

## Custom-glyph paint latency

**Not yet measured.** Setting a key's image over "Set custom glyph" takes
about 1 second to appear on the panel, and no measurement yet says which
stage of that path — CDC transfer + decode, glyph-build, SPI refresh, flash
persist — holds the time. `firmware/tracer.py`'s `CUSTOM_GLYPH_DECODED`,
`PERSIST_DONE`, `GLYPH_BUILT`, and `REFRESH_DONE` trace codes mark the end
of each of those four stages, in order, for one custom-glyph paint — see
[`docs/wire-protocol.md`](../docs/wire-protocol.md#trace-record)'s trace
code registry and [task
0042](../tasks/ongoing/0042-instrument-custom-glyph-paint-latency.md) for
the design decision.

To capture one, the board needs tracing turned on, which `code.py` does
not do by default. Temporarily add a `Tracer` to its `MacroPad(...)` call:

```python
import tracer as tracer_module
# ...
macro_pad = MacroPad(
    # ...
    tracer=tracer_module.Tracer(capacity=64, enabled=True),
)
```

Run `make flash` to put the edited `code.py` on the board, then start
`macropadd` with `--trace-file` set (see
[`driver/README.md`](../driver/README.md#recording-a-trace-alongside-the-plugin-api)):

```bash
go run ./driver/cmd/macropadd --vendor-id=0x2E8A --product-id=0x10A3 \
  --trace-file=/tmp/macropad-custom-glyph-trace.jsonl
```

With `macropadd` running, send one custom image to a key through
`driver/plugin/web/keystate.html`, then stop `macropadd`. The JSONL file
holds one line per trace record, each with the device's Timestamp and
`driver/recorder`'s estimated host arrival time; diff consecutive
`CUSTOM_GLYPH_DECODED` → `GLYPH_BUILT` → `REFRESH_DONE` → `PERSIST_DONE`
Timestamps for the key that received the image to get each stage's
duration. Revert the `code.py` edit above and run `make flash` again
afterward — leaving tracing on is a deliberate choice, not a default (see
task 0025's Design), and this task's Risks call for re-confirming the ~1s
figure with tracing off so the reported bottleneck is not an artifact of
tracing itself.

Record the result here as a line of the form:

```
Measured custom-glyph paint latency: decode+transfer N.NNN ms, persist N.NNN ms, glyph build N.NNN ms, refresh N.NNN ms (RP2350)
```

## Out of scope

- Click-pattern resolution (single vs. double vs. long press). The host
  resolves this from the raw timestamped events.
- The host controller itself (see [`driver/`](../driver/)).
