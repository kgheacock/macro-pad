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
with `circup` — `adafruit_st7735r`, for the real display driver — survives
a reflash. The target fails with a clear error if `CIRCUITPY` isn't
mounted.

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

`code.py` only builds the real hardware objects — switches, a
display-bus builder, backlights, the HID device, and the CDC data
channel — and passes them to `app.MacroPad`, then calls `run()`. It
holds no loop logic of its own.

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
   "Display bus," below, for when a redraw reuses the open bus and when
   it releases and rebuilds it.
5. Set each backlight from the idle timer.

`wire.py` encodes and decodes the messages in
[`docs/wire-protocol.md`](../docs/wire-protocol.md). It is the firmware
half of the contract `driver/transport/wire.go` implements on the host
side, including the same refusal to read a key state message whose
version byte is not the one this build was written against.

Modules under `firmware/` import each other flat (`import wire`, not
`from firmware import wire`), because this folder's contents are copied
to the root of the `CIRCUITPY` drive, where no `firmware` package exists.

## Display bus

This board's CircuitPython build allows only 1 concurrent `displayio`
display bus — a 2nd `fourwire.FourWire` raises `RuntimeError: Too many
display busses`, confirmed live during task 0010's bring-up. All 6
keys' displays share one `busio.SPI` bus with a distinct chip-select
pin each, so only one key's `fourwire.FourWire`/`ST7735R` pair can
exist at a time.

`code.py` reflects this: it holds no persistent list of display
objects. Instead, its `build_display(key_index)` builds one key's bus
on demand, and `app.MacroPad` tracks which key currently owns the open
bus in `_active_key_index`/`_active_display`. A redraw for that same
key reuses the live display and calls `display_render.render_key`
directly — no rebuild, no `displayio.release_displays()` call, so the
key's hardware reset line stays untouched. A redraw for a different key
releases the current bus first, then builds the new one, before
`render_key` runs. See
[`tasks/ongoing/0032-share-one-display-bus-across-six-keys.md`](../tasks/ongoing/0032-share-one-display-bus-across-six-keys.md)
for why the one-bus-at-a-time limit exists, and
[`tasks/ongoing/0040-keep-active-keys-display-bus-open-across-redraws.md`](../tasks/ongoing/0040-keep-active-keys-display-bus-open-across-redraws.md)
for why a redraw of the same key no longer pays a rebuild at all: on
real hardware, rebuilding the bus on every redraw pulsed the reset line
each time, and a key's color did not reliably hold across repeat
redraws. Switching from one key to a different key still pulses that
shared reset line — see 0040's Non-goals for why that cost remains.

## Persistent display scene graph

`display_render.render_key` builds each key's `displayio.Group`,
background `Palette`, and glyph `TileGrid` once, on that key's `KeyState`,
and mutates those same objects on every later call instead of replacing
them: the background `Palette`'s color is rewritten in place, the glyph
`TileGrid` is swapped for a freshly built one only when its source
(`emoji_id`, `pixels`, or — for a built-in emoji, whose bitmap bakes in
`key_state.color` as its background, see task 0023 — `color`) actually
changed, and a blink toggle flips the glyph `TileGrid`'s `hidden` flag
rather than adding or removing it from the `Group`. A custom image with a
transparent pixel is the one exception: its glyph `TileGrid` never
hides, and a blink instead rewrites the background `Palette` between
`key_state.color` and black — see [task
0041](../tasks/ongoing/0041-color-and-blink-behind-custom-glyph.md).
Rebuilding a fresh
object graph on every call, as `render_key` did before, gives displayio
nothing to diff against the last frame, so it always redraws the full
128×128 panel; mutating the same objects in place lets its own
per-`TileGrid` dirty tracking shrink a blink-only redraw to the glyph's
own area. See
[`tasks/ongoing/0033-mutate-display-scene-graph-in-place.md`](../tasks/ongoing/0033-mutate-display-scene-graph-in-place.md)
for the design decision.

**Blink redraw latency: not yet measured on hardware.** Task 0033's
Design assumes this CircuitPython build's `BusDisplay.refresh()` redraws
only a `TileGrid`'s own dirty bounds rather than the union of every
dirty `TileGrid` in the `Group` — its Open questions flags this as
unconfirmed. Task 0040 means a blinking key's own `BusDisplay` now
stays open across its consecutive blink toggles (as long as no other
key's redraw takes the bus in between), the same persistent `Group`
attached throughout, rather than a fresh `BusDisplay` on every call —
but whether `refresh()`'s dirty-bounds behavior itself holds is still
unconfirmed. Confirm both with the real board: reuse task 0031's
`time.monotonic_ns()` probe around `display.refresh()`, but on a
blinking key whose glyph is smaller than the full panel (not emoji ID
`0x00`, whose full-panel placeholder glyph's cost this task does not
change by design), across several consecutive blink toggles once the
scene graph is already built.

Record the result here as a line of the form:

```
Measured blink redraw latency: N.NNN ms (RP2350, <emoji id>, glyph smaller than full panel)
```

## Glyphs

`firmware/glyphs.py` renders a plain background-colored tile for any
emoji ID — it holds no glyph bitmap of its own.
`display_render.render_key` draws through it via the `emoji_lookup`
callable, so this module never appears in the render loop directly. See
[`docs/wire-protocol.md`](../docs/wire-protocol.md#emoji-ids) for the
reserved IDs.

Rendering any other glyph — an emoji character or an arbitrary image —
happens on the driver side and reaches a key as a [Set custom
glyph](../docs/wire-protocol.md#set-custom-glyph-cdc-host--device)
message. See [task
0039](../tasks/complete/0039-remove-built-in-firmware-glyph-table.md) for
the design decision.

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
    build_display=lambda key_index: NullDisplay(),
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

## Per-key-switch display-bus latency

**Not yet measured.** `build_display(key_index)` constructs a fresh
`fourwire.FourWire` and `ST7735R`, which runs the panel's full init
sequence — a cost the held-open display list task 0032 replaced never
paid. Task 0040 changed when this cost is paid: `app.MacroPad` only
calls `build_display` when a redraw's key differs from
`_active_key_index`, not on every redraw of the same key, so it lands
once per key switch instead of once per redraw. This needs the real
board to measure: time one `build_display` call end to end with a
console attached (see "Loop period," above, for how to reach the
console), and record how many key switches can happen in one main-loop
tick before that latency, multiplied by the number of keys switching
in, threatens task 0022's loop-period budget — several different keys
blinking in turn is the worst case named in task 0032's Risks.

Record the result here as a line of the form:

```
Measured per-key-switch display-bus latency: N.NNN ms (RP2350, one key, ST7735R init included)
```

## Custom-glyph paint latency

**Not yet measured.** Setting a key's image over "Set custom glyph" takes
about 1 second to appear on the panel, and no measurement yet says which
stage of that path — CDC transfer + decode, flash persist, glyph-build,
SPI refresh — holds the time. `firmware/tracer.py`'s `CUSTOM_GLYPH_DECODED`,
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
`CUSTOM_GLYPH_DECODED` → `PERSIST_DONE` → `GLYPH_BUILT` → `REFRESH_DONE`
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
