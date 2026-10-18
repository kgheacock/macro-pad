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
   "Custom glyphs and key state," below.
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
each panel's init and draw window once (`st7735.init_panels`). After that the
only thing the firmware sends a panel is a push of a frame, under that
panel's CS line. It sends no init command, no window command, and touches no
RST, so a key's update never reaches another panel. Task 0045 changed how the
frame goes out; see "Pushes by DMA." Task 0049 moved the window to boot; see
"Frame-only pushes."

`DISPLAY_BAUDRATE` in `code.py` was 16 MHz for the `busio.SPI` driver of
task 0043. A frame push took 21 ms at 16 MHz and 79 ms at 4 MHz. On 2026-10-06 a test pattern of color bars and
1 px columns rendered cleanly on all six wired panels at 4, 8, 12, 16, and
20 MHz. The RP2350 steps its SPI clock down from 150 MHz, so 16 MHz runs at
15 MHz. Above about 19 MHz a push stays near 17 ms, because the CPU that
feeds the FIFO is the limit, not the wire. A higher rate buys little unless
a DMA transfer does the feeding. Task 0045 adds that DMA transfer.

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

A blinking key keeps its phase when an update arrives: the redraw shows the
frame of the current blink slot, in the new color (task 0044, changed by task
0048; see "Shared blink phase" in "Latency"). Only a blink that shows the
wrong frame for the slot is redrawn.

## Pushes by DMA

A push used to keep the CPU in `spi.write` for the whole transfer: 21 ms for
one frame. Six blinking keys held it for 126 ms of every 500 ms, and every
color change, flash write, or report waited behind them. Task 0045 sends the
frame from the DMA instead.

`pio_spi.PioBus` is a transmit-only SPI bus on one PIO state machine. It owns
SCK (GP2) and one MOSI line. Task 0048 moved that line: it is one panel's DIN
line (GP7 is now the shared backlight), and `PioBus` sends the init and window
commands. Frames go by `ParallelBus`; see "Shared blink phase" in "Latency".
The program is two instructions, `out pins, 1 side 0`
and `nop side 1`, assembled on the host because `adafruit_pioasm` is not on
the board. That is SPI mode 0, most significant bit first, two PIO clocks per
bit. The state machine runs at twice the baud rate, so `DISPLAY_BAUDRATE` of
15 MHz is a whole divider of 5 from the 150 MHz system clock, and the SCK
edges are evenly spaced. `PioBus.start(data)` calls `background_write` and
returns; `PioBus.done` is true when the DMA has ended and the FIFO is empty.
DC and every CS line stay with the CPU.

`Panel.start_push(frame)` starts the frame and returns with DC high, CS low
and `busy` set. `Panel.poll()` sets CS high once the bus is `done`. Until
then the panel holds its frame, so the frame stays alive.

**Frame-only pushes** (task 0049). `init_panels` calls `Panel.setup_window()`
for each panel after its init. That sends `CASET`, `RASET` (with the panel's
offsets) and `RAMWR` once. The window is always the full 128 x 128 frame, and
a panel stays in write mode after `RAMWR`, so its address pointer wraps at
the end of each frame. A push after boot sends the frame and no command.
`Panel._needs_window` is set while a push starts and cleared once the frame
has started. A push that raises leaves it set, so the next `start_push`
calls `setup_window()` first. A panel that leaves write mode some other way,
for example by a brownout, stays wrong until a push fails.

In task 0045 all six panels shared SCK and MOSI, so one push was on the wire
at a time. Task 0048 gave each panel its own DIN line, and up to three queued
frames now go out at once (see "Latency"). `MacroPad.step` keeps a queue of keys. `_service_pushes` ends the push on the
wire when it is done, then starts the next queued key. It runs at the start
and the end of every `step`. A key has at most one frame in the queue, and a
newer frame replaces an older one that has not started and keeps its place.
`PUSH_STARTED` (trace code 9) marks a start, and `REFRESH_DONE` marks an end.

**No frame is changed after it is built.** `display_render._compose` makes a
new `Bitmap` for each rebuild, so a color change composes the new "back"
frame while the old one, which may be on the wire, stays whole. The cached
frames are byte views (`memoryview(bitmap).cast("B")`), because the DMA sends
the items of its buffer, and a `Bitmap` of 16-bit pixels has 16-bit items.
The blink schedule is still the CPU's: a flash write or a compose delays the
next toggle. Task 0045 shortens each delay, but does not remove it.

## Latency

**CPU time per push, by DMA** (task 0045's DoD-1, `make dma-spike`, RP2350,
15 MHz, 2026-10-06, 5 pushes of a cached frame):

```
start_push 1190 us to 1251 us, total 18 ms, cpu_free_ms 17
```

A push is on the wire for 18 ms, and the CPU is free for 17 ms of it. The 1.2
ms that `start_push` holds the CPU is the draw window: five short blocking
writes of 200 us each, before the frame starts. Before task 0045 the CPU
held the whole 21 ms. A color change still costs a 23 ms compose, but the
compose now overlaps the previous push.

Task 0049 removes those five writes from every push after boot. On
2026-10-08 (RP2350, 15 MHz, 5 pushes of a cached frame) `make dma-spike` gave:

```
start_push 305 us, total 17 ms, cpu_free_ms 17
```

`start_push` held the CPU 305 us in every trial, down from 1190 us to 1251 us.
The limit is 500 us. What is left is the DC and CS writes and the DMA start.
`make blink-trace SCENARIO=single` gave `max gap 504.6 ms (limit 600)`.

**Blink gap with the DMA push** (task 0045's DoD-4, `SCENARIO=single`, keys 0
to 2 blinking, 10 updates 2 s apart, measured between `PUSH_STARTED` records):

```
max gap 506.6 ms (limit 600)
max gap 507.5 ms (limit 600)
```

Task 0044 measured 532.8 ms with the `spi.write` push, between `REFRESH_DONE`
records. `SCENARIO=burst` and `SCENARIO=busyburst` both gave `decoded 6/6`.

**Rate.** `DISPLAY_BAUDRATE` is 15 MHz, a whole PIO divider (5). On 2026-10-06 a
person saw a test pattern of 8 color bars, 1 px columns, and a mark for each
key on all six panels with no noise at 15 MHz. 25 MHz was not tried.

**Shared blink phase, DIN lines, and the 12.5 MHz push** (task 0048).

*Shared phase.* Every blinking key shows "on" in an even 500 ms slot of one
clock (`now_us // BLINK_INTERVAL_US`) and "off" in an odd slot. No key keeps a
schedule of its own, so keys that start 200 ms apart flash in time. A key that
starts to blink in an odd slot shows "off" at once, an update to a blinking
key shows the frame of the current slot, and a `step` that comes many slots
late draws the current slot, not the missed ones.

*DIN lines.* Each key has its own DIN (MOSI) line, and the six panels share
SCK, DC, and RST. Keys 0 to 3 and 5 use the pin that carried the key's backlight
PWM: GP27, GP22, GP28, GP26, GP1. Key 4 uses GP12, because GP0's breadboard
row never carried a signal. The six backlight inputs join on one PWM pin, GP7.
`firmware/pins.py` and the Pinout table of `hardware/README.md` list the lines.

*Parallel push.* Frames due in one step start together, in groups of at most
three: one PIO block holds a leader that makes SCK and three followers that
each shift one panel's frame onto its DIN line. CircuitPython puts the state
machines that share SCK on one block of four, so a push of six panels is two
groups. The leader clocks a byte only when every follower of the group has
asked for it, so a follower whose DMA waits for the CPU holds the clock back,
and no bit is lost. A group on the wire takes about 26 ms. Commands never go
through a follower. `PioBus` sends them on one panel's DIN line at 15 MHz.

*Rate.* SCK is 12.5 MHz: 7 PIO cycles low and 5 high, in a bit of 12 cycles of
the 150 MHz system clock. A follower sees SCK about 3 cycles late, so SCK needs
the long low phase. In spike 2 (2026-10-06) 10 MHz gave 0 desyncs in 300 pushes
and 15 MHz gave 5 in 150. At 12.5 MHz 400 groups held on the board, with and
without a CPU load (2026-10-08). A person has not yet looked at the panels at
12.5 MHz for noise (the visual check of task 0048's DoD-7).

*Recovery.* If a follower does not finish with the leader, or the group does not
end in time, `ParallelBus.done` tears the machines down and sets `desynced`.
The code then rebuilds the group, sends the window again, and sends the frames
again. `MacroPad.resyncs` counts these. At startup `code.py` stops state
machines left over from an earlier run.

*Measured on the board* (task 0048's DoD-6, `make blink-trace SCENARIO=sync`,
RP2350, 2026-10-10, 42 slots in each of two runs: six keys blinking, 10 updates
to key 4 two seconds apart):

```
max skew 31.0 ms (limit 35)   max span 34.1 ms (limit 50)   max gap 501.4 ms (limit 600)
max skew 31.2 ms (limit 35)   max span 42.0 ms (limit 50)   max gap 507.7 ms (limit 600)
```

*Skew* is the time between the first and the last `PUSH_STARTED` of one slot,
for the five keys that did not get the update. *Span* is the time from the
first `PUSH_STARTED` of a group to its last `REFRESH_DONE`. *Gap* is the time
between two `PUSH_STARTED` of one blinking key. The skew limit was 30 ms at
first. Both runs missed it by about 1 ms, and the owner raised it to 35 ms.
The second run missed the span limit of 40 ms (42.0 ms), and the owner raised
it to 50 ms. The first printout of these runs said a span of 0.0 ms. That was wrong:
`tools/blink_trace.py` looked for `REFRESH_DONE` as code 10 and the firmware
sends code 8. The figures above come from the same two traces, read with the
right code.

**Display latency, measured on the board** from the trace of `macropadd
--trace-file`, with `HOST_MESSAGE_DECODED` as the start:

```
Measured color change: 46.5 ms to 54.4 ms to REFRESH_DONE, blink push: 23.5 ms (RP2350, key 0, 16 MHz, 2026-10-05)
```

The 46 ms is a 23 ms compose (`GLYPH_BUILT`) and a 23 ms push. A push of a
cached frame alone takes 23 ms. At 4 MHz a push takes 81 ms, so a color
change takes about 105 ms and a blink push 81 ms. `code.py` now sets 16 MHz,
so the figures above apply.
**The board keeps no key state, and why.** A write of a 5-byte state file
to flash took 320 ms to 590 ms on the board. While `step` waits for it, no
blinking key toggles, so one update froze every blinker for 0.4 s to 0.7 s
and moved their phases. A write to `microcontroller.nvm` took 48 ms to 53 ms
(spike, 2026-10-06, 20 samples), which fits an idle gap between blinks but
not every case, and it wears the flash: `nvm` is one sector with no wear
leveling, and a status plugin that changes a key every few seconds can use
the flash's erase cycles in days. So the board writes nothing (task 0044).
After a power cycle every key shows the power-on default until the host
driver replays each key's last state; see "Custom glyphs and key state."

**Reports in a burst** (task 0044's DoD-6, RP2350, 2026-10-06). The board holds
one HID report, and its loop reads one per pass, so a report that arrives
before the pass that follows the last one overwrites it. A pass that redraws a
key takes about 46 ms, plus about 21 ms for each blinking key that is due.

| Run                                               | Gap    | Result                          |
|---------------------------------------------------|--------|---------------------------------|
| `SCENARIO=burst`, idle board                      | 50 ms  | `decoded 6/6` (7 records, one repeat) |
| `SCENARIO=busyburst`, keys 0 to 2 blinking        | 50 ms  | `decoded 5/6`, key 2 lost       |
| `SCENARIO=busyburst`, keys 0 to 2 blinking        | 150 ms | `decoded 6/6`, in 3 runs of 3   |
| `SCENARIO=burst`, idle board                      | 150 ms | `decoded 6/6`                   |

The lost report was a key's background color, seen on the board as a missing
background. `transport.minReportGap` is 150 ms because of it. Six blinkers
due in one pass could take longer than 150 ms; that case was not run. The
first `make blink-trace` runs printed `decoded 0/6`, or an unreadable
first trace record, when the board had just reloaded `code.py`. The target
now waits 10 s and then opens the board once, with no traffic, before it
measures; every run after that worked.

**Blink gap while key 4 updates** (task 0044's DoD-5, `SCENARIO=single`, keys 0
to 2 blinking, 10 updates 2 s apart, 16 MHz):

```
max gap 532.8 ms (limit 650)
```

At 4 MHz with a flash write the largest gap was 950 ms.

A blinking glyph with a transparent background flips the background between
the key's color and black. With a black key color both frames look the same, so
the key does not appear to blink.

`make blink-trace SCENARIO=single`, `burst`, or `busyburst`, run from the repo
root, puts a tracing `code.py` on the board, unmounts `CIRCUITPY` for the run,
sends the scripted run with `driver/cmd/blinksend`, prints
`tools/blink_trace.py`'s figures, and mounts the volume again. Run `make
flash` afterward to restore the real `code.py`.

## Custom glyphs and key state

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

The board holds every key's state in RAM only. After a power cycle each key
shows the power-on default: no glyph, color `0x0000`, no blink. The host
driver restores them. `macropadd`'s `transport.Reconnecting` remembers each
key's last key state and custom glyph, and sends them again each time the
board connects, so a replug or a power cut on a running host brings the keys
back within a second or so of the board's boot. The daemon also keeps its
memory in files on the host (`keys.json` and `glyph-<hash>.bin`, see
[`driver/README.md`](../driver/README.md)), so a daemon restart or a host
reboot restores the keys too. With no driver running, the keys stay at the
default. A one-shot `macrodriver` call therefore lasts until the next power
cycle.

Task 0030 persisted this state in files, and task 0044 first moved the
headers to `nvm` and then removed board-side persistence altogether: a flash
write froze the blinking keys and wore the flash. `make flash` and a power
cycle now behave alike, and `boot.py` no longer remounts the filesystem for
writing.

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

### Measured figures (task 0058)

Measured on 2026-10-17 on a Pico Plus 2 (CircuitPython 10.2.1), with
`make flash`, the tracing `code.py` of `tools/blink_trace.py install`, and
`go run ./cmd/blinksend --scenario=icons` or `--scenario=glyphs` from
`driver/`. Both send one glyph and one key state to each of the six keys
and print `six keys in N s`. `glyphs` sends pseudo-random pixels that do not
compress. `icons` sends the first six PNG files in `assets/`.

Before task 0058, one glyph took about 450 ms and six keys took 2.5 s:

| Stage of one glyph | Time |
|---|---|
| CDC transfer | about 235 ms |
| Payload copy and decode | about 40 ms |
| `_compose` (fill, array copy, arrayblit, blit) | 90 to 100 ms |
| SPI push | about 25 ms |
| Redraw with the old pixels, before the glyph decodes | about 30 ms |

`CustomGlyphReader.feed` made one read per loop pass. Each read returned at
most 256 bytes, so a frame needed 129 reads, and a pass took about 1.8 ms.

After the drain loop (`wire.READ_BUDGET_NS`, `readinto` into one buffer),
six keys take:

| Scenario | Runs | Six keys |
|---|---|---|
| `glyphs` (noise) | 3 | 1.24 s, 1.28 s, 1.38 s |
| `icons` | 6 | 1.21 s to 1.53 s, median 1.38 s |

The first glyph writes in about 78 ms. A bare read loop on the same board
reached 550 KB/s, which is about 58 ms for 32 KB. A key then waits for the
previous key, because the loop builds a frame and cannot read while it does.
One `blinksend` run in 11 stopped with an `IOHIDDeviceSetReport` error on the
host, and the board kept working.

After compression (frame type 6), six keys with `icons` take 0.80 s to
0.82 s over 6 runs, on the same board with the same icons. The trace of one
run shows each key at about 140 ms:

- `_compose`, from `GLYPH_DECODED` to `GLYPH_BUILT`: 70 to 80 ms.
- Receive and inflate, from `GLYPH_BUILT` to the next `GLYPH_DECODED`:
  55 to 78 ms. A 32 KB `zlib.decompress` alone took 32 to 65 ms in a probe,
  and `bytes(32768)` alone took about 11 ms, so the heap is slow.
- The glyph now arrives before the key-state report, so the redraw with the
  old pixels is gone. The DMA pushes of the earlier keys overlap the build of
  the next key.

The 13 icons in `assets/` deflate to 1,213 bytes on average. A flat icon is
about 3% of a raw frame. Inflate is now the largest cost after `_compose`,
and the loop cannot read the next frame while it inflates.

## Drawing a layer change together (task 0060)

A layer change sends each changed key as a glyph over CDC and a key state over
HID, one key after another. Without help the board draws each key when its data
arrives, and the panels change one by one: the first `PUSH_STARTED` records of
the six keys were 732 ms apart with `--scenario=icons`.

The driver now sends "Begin batch" (frame type 7, see the wire protocol) before
a change of two or more keys. It names the keys that get a glyph and the keys
that get a key state. `MacroPad` then:

- Composes a named key as its data arrives (`display_render.prepare_key`), but
  queues no push for it. A frame of a named key that waited in the queue leaves
  it.
- Releases the batch when every named glyph is decoded and every named key
  state is applied. `_release_batch` queues the frames of all the named keys in
  one pass, so `_start_group` starts the pushes together: two groups, because
  `pio_spi.MAX_GROUP` is 3.
- Draws a blinking key of the batch with the frame of the blink slot at the
  release, in step with the other blinking keys (task 0048).
- Releases what it has 2 s after the Begin batch (`BATCH_TIMEOUT_US`), and
  records `BATCH_TIMED_OUT` (code 10). A new Begin batch releases an open one
  first. A key outside both masks draws at once.
- Reads the CDC channel before the HID report in each `step`, so a Begin batch
  and the key state that follows it in the same pass belong together.

Flash the firmware before you run a driver that sends Begin batch. Firmware that
does not know type 7 skips the frame, and the layer draws key by key.

Measured on 2026-10-17 on a Pico Plus 2 (CircuitPython 10.2.1), with `make
flash`, the tracing `code.py`, and `go run ./cmd/blinksend --scenario=batch`.
`tools/blink_trace.py report --scenario=batch` prints `push span`: the time
between the first `PUSH_STARTED` of the first key and of the last key, from the
first decoded glyph on. The `batch` scenario is the `icons` scenario with Begin
batch first.

| Run | Push span | Six keys |
|---|---|---|
| 1 | 35.4 ms | 0.82 s |
| 2 | 32.6 ms | 0.82 s |
| 3 | 31.5 ms | 0.81 s |

Keys 0 to 2 start within 1 ms of each other, and keys 3 to 5 start 29 to 31 ms
later, as the second group. The six-key time is the same as for `icons`: the
HID gap (`minReportGap`, 150 ms) sets it, and Begin batch does not change when
the data arrives.

## Caching composed frames (task 0061)

A layer switch used to send every image again: 140 ms for each key, so about
0.8 s for six. The board now keeps composed frames in a fixed pool, and a
switch sends slot numbers.

- **The pool.** `frame_cache.FrameCache` allocates `SLOT_COUNT = 64` slots of
  32 KB (128×128 pixels, 16 bits each), 2 MB, when `MacroPad` is built, before
  the loop. It never frees a slot, and it does not grow or shrink, so the heap
  does not fragment and the free heap is the same for the whole run. The
  cache is RAM only: the firmware writes no file and no `nvm` byte for it.
  `grep -nE "open\(|nvm|storage\." firmware/*.py` lists nothing.
- **Store frame** (type 8). `MacroPad._store_frame` composes the glyph over the
  color into the slot's own `Bitmap` (`display_render.compose_into`), with one
  scratch `Bitmap` for the glyph. It allocates no `Bitmap`. It draws nothing.
  A push that reads the slot, queued or on the wire, ends first, because the
  compose changes the bytes the DMA reads (`SLOT_WAIT_NS` bounds the wait). The
  host does not store into a slot that a key shows, so this wait is rare.
- **Show frames** (type 9). `MacroPad._show_frames` hands the byte view of each
  named slot to its key (`display_render.show_cached`) in one pass, so
  `_start_group` starts the pushes together, two groups of three. It sets the
  key's state to what a Set custom glyph and a Key state would have left, and
  records `FRAMES_SHOWN` (code 11). It does no inflate and no compose.
- **Show miss** (type 10). A key that names an empty slot, or one above 127,
  keeps its image. The board draws the other keys, and writes a Show miss with
  the mask of the missed keys.
- **Blinking keys are not cached.** A Set custom glyph and a blinking Key state
  take the paths of tasks 0044 and 0048, which compose a new `Bitmap` for each
  frame and write no slot. A key that blinks after a Show blinks between its
  cached frame and black.

`make slot-probe` measures the pool (`tools/slot_probe.py`): the time to
allocate the slots, the free heap after, and the time of a 32 KB copy. It calls
`gc.mem_free()` once before and once after the allocation, never inside a timed
section, because it walks the heap and costs about 20 ms. The task stops if
fewer than 2 MB are free after the pool: the firmware does not shrink the pool
by itself.

Probe figures with 128 slots, before task 0063 cut the pool to 64 (`make slot-probe`,
2026-10-18, Pico Plus 2, CircuitPython 10.2.1). The probe builds a `FrameCache` and nothing else, so the figures do
not include the heap that the panels and the reader take:

| Figure | Value |
|---|---|
| Time to allocate 128 slots | 1,596 ms |
| Free heap before the pool | 8,282,624 bytes |
| Free heap after 128 slots | 4,176,288 bytes (limit 2,097,152) |
| Time of a 32 KB copy (`target[:] = source`) | 5,767 us (best 5,615, worst 5,798, 5 trials) |

The allocation takes 1.6 s at start-up, before the loop. A 32 KB copy takes
5.8 ms, so copying a frame is cheaper than an inflate (32 to 65 ms).

`go run ./cmd/blinksend --scenario=cached` stores two sets of six frames and
shows them in turn. `tools/blink_trace.py report --scenario=cached` prints
`six keys in N ms` (from the board's `FRAMES_SHOWN` to the last `REFRESH_DONE`,
limit 150) and `push span N ms` (limit 40) for the worst of the six Shows.
Three runs on 2026-10-18 (the Show write took 0.1 to 0.4 ms on the host):

| Run | Six keys in | Push span |
|---|---|---|
| 1 | 108.6 ms | 58.2 ms |
| 2 | 108.7 ms | 58.1 ms |
| 3 | 108.6 ms | 58.1 ms |

A Show of six stored frames is inside 150 ms, and the board does no inflate and
no compose for it: the trace shows `FRAMES_SHOWN`, then the first push 6 ms
later. The push span is over its limit of 40 ms. Each group of three pushes
took 48 ms from `PUSH_STARTED` to `REFRESH_DONE` in most of the Shows, and 25 ms
in the others (slots 9 to 11, the three frames stored last). The same board
gave 24 ms for a group in `--scenario=batch` a few minutes later. A group is on
the wire for 26 ms at 10 MHz, so a 48 ms group means the DMA read the slot
frames at half speed. The cause is not found: the next step is to time a group
from slot frames and from fresh frames on the board, one slot at a time.

The pool also slowed the older paths, and the cause is the garbage collector
(task 0063). It scans every live heap block, about 1 ms for each 32 KB, whether
the block is a `Bitmap` or a `bytearray`. Time of one `gc.collect()` on the board
(2026-10-18, a probe that prints over `usb_cdc.data`):

| Live pool | `gc.collect()` |
|---|---|
| none | 0.7 ms |
| 8 slots | 5.5 ms |
| 16 slots | 11 ms |
| 32 slots | 22 ms |
| 64 slots | 42 ms |
| 128 slots | 115 to 150 ms |

A collection that lands in a loop pass makes the pass longer than the 150 ms gap
between HID reports. The board holds one report, so the next one overwrites an
unread one and a Key state is lost (task 0044). `--scenario=batch` runs that ended
with `BATCH_TIMED_OUT`: 0 of 9 with no pool, 1 of 12 with 32 slots, 2 of 9 with
128 slots. The pool is 64 slots, and an open batch that lacks data now asks the
host for it:

- After 500 ms with no new glyph or Key state, the board sends Batch incomplete
  with the pending masks, and records trace code `BATCH_INCOMPLETE` (12). It asks
  again every 500 ms, at most three times, and the 2 s time-out stays.
- `transport.Reconnecting` sends again the glyph and the Key state it holds for
  each named key. `blinksend --scenario=batchloss` drops the Key state of key 2
  on purpose, and `tools/blink_trace.py report --scenario=batchloss` prints how
  long after the last report the batch released (limit 1,000 ms).

## The count pip (task 0062)

A count change used to send a new image: each count of one icon on one color
was its own frame, and a Store frame costs 94 to 109 ms. The pip is no longer
part of a frame. The board stamps it on a copy.

- **The pool.** `frame_cache.POOL_SIZE = 64` buffers, as in task 0063. The last
  `PIP_BUFFERS = 12` are pip buffers, two for each of six keys, so the host
  numbers `SLOT_COUNT = 52` slots. A Store frame or a Show for a slot from 52
  up is dropped or answered with a Show miss. The pool is the same 2 MB, and on
  the heap the garbage collector scans the same number of blocks as before.
- **Store pip sheet** (type 13). `MacroPad` hands the message to
  `FrameCache.set_sheet`, which blits the 256×64 sheet into one `Bitmap` that the
  cache allocates when it is built, so the heap does not change for the run. The
  sheet inflates on core 0: it arrives once, and it is not worth a job on core 1.
- **Show frames with pips** (type 14). For a key with a count above 0,
  `MacroPad._pip_frame` copies the slot into the key's idle pip buffer (a 32 KB
  copy took 5.9 ms), `display_render.stamp_pip` blits the disc for the slot's
  color and then the characters, and `show_cached` queues that buffer. The key
  alternates its two buffers, so a push that still reads the last one is not
  changed, and `_wait_for_view` holds a stamp that would still change a buffer
  on the wire. A count of 0 queues the slot's own view, as type 9 does. Before
  the sheet arrives, a count draws the slot with no pip.
- **Trace.** `SHOW_WITH_PIPS_STARTED` (code 13) is recorded before the copies,
  and `FRAMES_SHOWN` after the stamps, so `tools/blink_trace.py report
  --scenario=pips` prints a `six keys in` that includes the stamps.

The layout of the sheet is in `docs/wire-protocol.md` and `driver/layers/pip.go`
(`pipSpriteRect`), and `display_render.py` repeats it: a disc of 44×44 for each
key color at `(44 * i, 0)`, big cells of 14×20 for `0` to `9` and `+` at
`(14 * index, 44)`, and small cells of 10×14 for the `9` and `+` of `99+` after
them. `test/test_app.py` builds a sheet with one value for each sprite and
checks the pixel positions, so a change to one side that the other does not
follow fails a test.

`make blink-trace SCENARIO=pips` stores the sheet and six frames, and shows them
six times with a count on every key. Its figures are not recorded here yet: the
board check of task 0062 (DoD-9 and DoD-10) is the owner's.

## The second core: the `glyphcore` module (task 0059)

Stock CircuitPython 10.2.1 cannot reach the second core of the RP2350. The
`glyphcore` module (`tools/usermod/glyphcore/`) is a user C module for a custom
build. With it, core 1 inflates a type 6 or type 8 stream while core 0 reads the
next frame and composes the previous one, and the frame pool lives outside the
Python heap. Without it, the firmware runs the code of tasks 0058 and 0061
unchanged: `wire.py`, `display_render.py`, and `frame_cache.py` each do
`try: import glyphcore` and fall back to `zlib.decompress` on core 0 and to
`Bitmap` slots on the heap.

What the module does:

- **Inflate on core 1.** `submit(stream, buffer)` copies the zlib stream and wakes
  core 1. It inflates, from RAM, into one of 16 glyph buffers in PSRAM. `poll()`
  returns `None` while core 1 runs, then the length. `glyph_view(buffer)` gives
  a `memoryview` of 16-bit items, which `bitmaptools.arrayblit` reads without a
  copy. Two buffers belong to each key, so a new glyph never overwrites the
  pixels that the key draws from. `cancel()` and `last_us()` complete it.
- **A pool off the heap.** `alloc(slots)` takes the slots and the glyph buffers
  from the port heap, not the Python heap: the garbage collector does not scan
  them. `slot_view(slot)` gives the byte view that the DMA push reads.
  `FrameCache` composes into one `Bitmap` and copies the 32 KB into the slot.
- **Flash writes.** A write to `CIRCUITPY` or to `nvm` calls `flash_range_erase`
  and `flash_range_program`, and core 1 reads PSRAM through the same window
  as flash. The module wraps both calls at link time (`--wrap`): the wrapper
  waits for the job on core 1 to end, and refuses a new job until the call
  returns. A refused job inflates on core 0.
- **Order and time-out.** `CustomGlyphReader` keeps the order of the messages: a
  frame behind a stream on core 1 waits in the buffer. A stream that core 1 has
  not finished after `INFLATE_TIMEOUT_NS` (500 ms) is dropped, the key keeps its
  image, and an open batch asks the host for the glyph again (task 0063).

### Build

The recipe needs the Arm GNU toolchain 14.x. Homebrew's `arm-none-eabi-gcc` has
no newlib and fails with `cannot read spec file 'nosys.specs'`. Download
`arm-gnu-toolchain-14.3.rel1-darwin-arm64-arm-none-eabi.tar.xz` from
developer.arm.com, unpack it, and put its `bin/` first on the `PATH`. Then, from
the repo root:

```bash
make circuitpython-glyphcore
```

`tools/build_circuitpython.sh` clones CircuitPython 10.2.1 (about 3 GB with the
submodules of the raspberrypi port) into `../circuitpython-build`, and builds
`pimoroni_pico_plus2` with `USER_C_MODULES=tools/usermod`. The tree of
CircuitPython has no patch. The UF2 is copied to
`firmware/modules/circuitpython-pimoroni_pico_plus2-10.2.1-glyphcore.uf2`. The
build adds 6 KB of flash (885,112 of 1,044,480 bytes used) and 10 KB of RAM.

### Flash

1. Hold `BOOTSEL`, plug in the board, and copy the UF2 onto the `RP2350` drive.
   The board restarts as `CIRCUITPY`. The `CIRCUITPY` drive survives the change
   of the interpreter.
2. `make flash` copies the firmware.
3. `import glyphcore` in the REPL confirms the build. To go back to stock
   CircuitPython, copy `adafruit-circuitpython-pimoroni_pico_plus2-en_US-10.2.1.uf2`
   the same way.

### Checks without a board

- `cc` compiles `inflate.c` on the host, and `test/test_glyphcore_inflate.py`
  compares it with `zlib` on 44 streams, damaged ones included.
- `nm` and `objdump` on `firmware.elf`: `core1_main`, the inflater, and the flash
  wrappers are at `0x2000....` (RAM), and the only call out of them is `memset`,
  which is in RAM too.

### Measured figures

Measured on 2026-10-18 on a Pico Plus 2 with the custom build (the UF2 of this
branch, `make glyphcore-spike`, three runs, and `make blink-trace
SCENARIO=icons` and `SCENARIO=batch`). The glyph is the icon of `assets/` that
compresses worst: a stream of 3,823 bytes.

| What | Figure |
|---|---|
| One inflate, `zlib.decompress` on core 0 | 45.9 ms |
| One inflate on core 1 (`glyphcore.last_us()`) | 12.7 to 12.9 ms |
| `submit` call on core 0 (copy of the stream) | 0.8 ms |
| One compose on core 0, alone | 43.9 to 44.1 ms |
| The same compose while core 1 inflates | 49.7 to 50.0 ms (6 ms slower) |
| A 32 KB copy of a frame into a slot | 5.9 ms |
| `gc.collect()` with 128 slots in module memory | 2.62, 2.72, 2.72 ms (three runs) |

The pixels from core 1 equal those of `zlib.decompress` in every trial. The
probe line for DoD-8: `gc.collect() with 128 slots in module memory 2716 us
(median; limit 10000)`. On the heap the same pool took 115 to 150 ms.

`blinksend --scenario=icons`, three runs with the HID gap in the way (key
state over HID): `six keys in 0.80 s`, `0.79 s`, and `0.81 s`. The 0.65 s of
the spec was not met, and the board was not the limit: the run sends a glyph
and then a Key state for each key, and the driver waits 150 ms (`minReportGap`)
between two HID reports. Five gaps and the last key take 0.75 s or more
whatever the board does, and the run was already at that floor after task 0058.

With the Key state sent as a type 12 frame over CDC (the default since this
task), three runs gave `six keys in 0.56 s` each, with `compose` at 59 to 74 ms
for each key (from `CUSTOM_GLYPH_DECODED` to `GLYPH_BUILT`) and no
`TRACE_DROPPED` record. `--scenario=batchloss` still released the batch after
one resend (515 ms after the last report), and `--scenario=burst` decoded 6 of 6.

`blinksend --scenario=batch`, 15 runs with the 64-slot pool in module memory and
key state over CDC: no `BATCH_TIMED_OUT`, `six keys in 0.46 to 0.51 s`, and a
push span of 32.6 to 35.9 ms (limit 40) in all 15. Over HID the same 15 runs gave
`six keys in 0.77 to 0.83 s` and a push span of 31.0 to 35.0 ms.

## Out of scope

- Click-pattern resolution (single vs. double vs. long press). The host
  resolves this from the raw timestamped events.
- The host controller itself (see [`driver/`](../driver/)).
