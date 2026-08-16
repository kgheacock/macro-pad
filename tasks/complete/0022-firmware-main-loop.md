---
id: "0022"
title: "Add the firmware main loop that wires the modules together"
status: "complete"
created: "2026-08-14"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/13"
branch: "0022-firmware-main-loop"
related: ["0001", "0002", "0003", "0004", "0006", "0008", "0009", "0010", "0019", "0020", "0023"]
tags: ["firmware", "integration"]
---

# 0022 — Add the firmware main loop that wires the modules together

## Problem

`firmware/` holds five modules and no entry point. CircuitPython runs
`code.py` after boot, and no `code.py` exists. A board flashed today
enumerates, then does nothing: no key is read, no display is drawn, no
event reaches the host. Firmware also has no decoder for the key-state
message, while the driver has `wire.go`.

## Goals

- `code.py` runs on boot and drives one loop: read switches, debounce,
  emit events, apply host key state, render, dim the backlight.
- A received key-state message changes what the matching key shows.
- A real press writes the 10-byte event of `docs/wire-protocol.md` to the
  CDC data channel.
- The loop logic runs under pytest with the existing stubs, with no board.
- The loop period is measured and recorded, not assumed.

## Non-goals

- Audio. Tasks 0005 and 0007 are unbuilt, so no capture or chunk stream is
  wired here.
- Glyph bitmaps. Task 0023 supplies the emoji lookup this loop calls.
- Real display and switch timing. Task 0010 checks those against silicon.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — One cooperative poll loop in an injectable `app.py`

`firmware/app.py` holds a `MacroPad` class that takes its hardware objects
as arguments. `code.py` builds the real objects and calls `run()`.

- Good, because the whole loop runs under pytest: a test builds `MacroPad`
  with the stubs in `test/stubs/` and steps it one iteration at a time.
- Good, because one loop with one clock keeps every event timestamp from
  the same `time.monotonic_ns` reading, which the wire protocol requires.
- Bad, because one slow step delays every other. A display refresh across
  six SPI displays sets the floor for press latency.
- Bad, because the loop's ordering rules live in one function that grows
  with each feature added later.

### Approach B — `asyncio` tasks, one per concern

Vendor CircuitPython's `asyncio` library. Run input scanning, host-message
handling, rendering, and backlight as four tasks.

- Good, because a slow render no longer blocks input scanning, so press
  latency stops depending on display work.
- Good, because each concern is read and tested on its own, with no shared
  loop body.
- Bad, because it adds the firmware's first vendored CircuitPython library
  and an event loop to a board with no scheduler need proven yet.
- Bad, because `test/stubs/` has no `asyncio` story, so every test gains an
  event loop before it can assert anything.

### Approach C — Use the built-in `keypad` module for input

Replace the polling and `firmware/debounce.py` with `keypad.Keys`, which
scans and debounces in the CircuitPython core and returns a timestamped
event queue.

- Good, because scanning and debouncing run in C, so they cost far less
  loop time than a Python poll across six pins.
- Good, because the core's event queue buffers presses during a slow
  render, so no press is lost while the display refreshes.
- Bad, because it makes task 0003's tested `Debouncer` dead code, and the
  core's debounce behavior has no stub, so it cannot be tested off-board.
- Bad, because `keypad` events carry the core's own tick base, which must
  be converted to the microsecond timestamp the wire protocol defines.

## Decision

Chosen: **Approach A — one cooperative poll loop in an injectable
`app.py`**.

Every firmware module so far was built to run under pytest against the
stubs from task 0001, and Approach A is the only option that keeps the
integration testable the same way. The cost accepted is that press latency
includes render time. DoD-6 measures it. If the measurement is poor,
Approach C becomes a follow-up task.

## Design

`firmware/app.py` holds `MacroPad`. Its constructor takes the switch
inputs, the displays, the backlight PWMs, the HID device, the CDC serial
object, and an emoji lookup. `step(now_us)` runs one iteration:

1. Drain the HID output report. Decode it with the new `firmware/wire.py`
   and update that key's `display_render.KeyState`.
2. Read each switch pin, feed each `Debouncer`, and write a 10-byte event
   for each transition it accepts.
3. Re-render only the keys whose state changed, or whose blink flag is
   set.
4. Touch the `IdleTimer` on any host message or key event, then set every
   backlight duty cycle from it.

`run()` loops `step` forever. `code.py` builds the real objects from
`firmware/pins.py` and calls it.

Files to change:

- `firmware/wire.py` — new. `decode_key_state`, `encode_event`, mirroring
  `docs/wire-protocol.md` and `driver/transport/wire.go`
- `firmware/app.py` — new. `MacroPad`, `step`, `run`
- `firmware/code.py` — new. Builds real objects, calls `run()`
- `test/stubs/usb_hid.py` — add `get_last_received_report`
- `test/stubs/digitalio.py` — allow a test to set a pin's value
- `test/test_wire.py`, `test/test_app.py` — new
- `firmware/README.md` — document `code.py` as the entry point

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [x] **DoD-1** — A key-state message for key 3 sets key 3's rendered
      color and emoji ID, and leaves the other five keys unchanged.
      **Proof:** `.venv/bin/pytest test/test_app.py -k key_state_applies`
- [x] **DoD-2** — A switch pin held low for longer than the debounce
      window writes exactly 10 bytes to `usb_cdc.data`, matching
      `docs/wire-protocol.md`'s press layout. **Proof:**
      `.venv/bin/pytest test/test_app.py -k press_writes_event`
- [x] **DoD-3** — Bouncing a pin five times inside the debounce window
      writes one event, not five. **Proof:** `.venv/bin/pytest
      test/test_app.py -k bounce_writes_one_event`
- [x] **DoD-4** — A key-state message with a version byte other than 1 is
      dropped, and the key keeps its previous state. **Proof:**
      `.venv/bin/pytest test/test_wire.py -k rejects_version`
- [x] **DoD-5** — After the idle window with no message and no key event,
      every backlight duty cycle reads 0.1. **Proof:**
      `.venv/bin/pytest test/test_app.py -k idle_dims_backlight`
- [x] **DoD-6** — `firmware/README.md` records the measured loop period on
      the board, in milliseconds, with the command used to measure it.
      **Proof:** `firmware/README.md`, section "Loop period"
- [x] **DoD-7** — `code.py` contains no logic beyond object construction
      and one `run()` call. **Proof:** read `firmware/code.py`
- [x] **DoD-8** — `firmware/README.md` names `code.py` as the entry point
      that CircuitPython runs. **Proof:** `firmware/README.md`
- [x] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:**
      PR body

## Risks

- DoD-6 needs a board, and none is wired → measure it on the bare RP2350
  with the displays absent, and re-measure in task 0010 with them
  attached.
- `firmware/wire.py` and `driver/transport/wire.go` can drift apart →
  both cite `docs/wire-protocol.md`, and DoD-4 pins the version check on
  the firmware side, as `ErrUnsupportedVersion` does on the driver side.
- The board needs `adafruit_st7735r` and `displayio` libraries that
  `firmware/` does not vendor → `code.py` imports the display driver
  behind a guard, so a missing library gives a named error instead of a
  silent boot failure.

## Open questions

- [ ] Does `usb_hid.Device.get_last_received_report` include the report ID
      byte from `boot.py`? — implementer, on the first board run. The
      answer sets whether `decode_key_state` skips a leading byte.
