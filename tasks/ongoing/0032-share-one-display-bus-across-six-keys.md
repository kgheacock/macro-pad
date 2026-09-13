---
id: "0032"
title: "Share the RP2350's single display bus across all 6 keys' displays"
status: "ongoing"
created: "2026-09-12"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0032-share-one-display-bus-across-six-keys"
related: ["0006", "0010", "0022"]
tags: ["firmware", "display", "hardware", "bring-up"]
---

# 0032 — Share the RP2350's single display bus across all 6 keys' displays

## Problem

Live testing for task 0010 found that this board's CircuitPython build
allows only 1 concurrent `displayio` display bus, not 6.
`firmware/code.py` builds a `fourwire.FourWire` and `ST7735R` for every
key in `pins.KEYS`. The second one raises `RuntimeError: Too many
display busses`. Only key 0 renders today. `firmware/code.py` keeps a
temporary `BRING_UP_KEYS = pins.KEYS[:1]` scope, so key 0 stays
testable.

## Goals

- All 6 keys render independently within one `code.py` run.
- The render path passes the existing pytest suite against fakes, with
  no board attached.
- `code.py` drops the `BRING_UP_KEYS` stopgap once all 6 keys render.

## Non-goals

- Wiring the remaining 5 keys. This task's fix must work with any number
  of wired keys when it lands. Task 0010 owns the physical wiring.
- Changing the display panel or driver chip. Approach A keeps `ST7735R`.

## Approaches considered

### Approach A — Per-frame display-bus construct and release

Instead of holding 6 persistent `fourwire.FourWire` and `ST7735R`
objects, build one for whichever key is due to redraw, draw it, then
release it before the next key's turn.

- Good, because it keeps the stock, pinned CircuitPython build in
  `Makefile`'s `firmware-uf2` target — no custom firmware to build or
  maintain.
- Good, because 1 open bus at a time is already proven safe on this
  board. It carries no unverified crash risk, unlike the raised bus
  limit in Approach C.
- Bad, because opening a fresh SPI display bus on every redraw is slower
  than one persistent bus. Each `ST7735R(...)` construction runs its
  full panel init sequence again. This adds latency that task 0022's
  loop-period budget does not include.
- Bad, because it changes `app.py`'s render step and
  `display_render.py`'s `render_key`. Existing tests in
  `test_display_render.py` and `test_app.py` already exercise both
  against today's pre-built-display shape.

### Approach B — Bypass displayio and drive the panels directly over SPI

Replace `adafruit_st7735r.ST7735R` and `displayio` with a lower-level
driver, such as `adafruit_rgb_display`'s `ST7735R`, which talks to the
panel through `adafruit_bus_device.spi_device.SPIDevice` and raw pixel
writes, not through `displayio`'s bus-object pool.

- Good, because it removes the bus-count ceiling entirely.
  `SPIDevice` shares one `busio.SPI` bus across many peripherals with
  independent software chip-select — exactly this board's wiring.
- Good, because it keeps the stock CircuitPython build, same as
  Approach A.
- Bad, because it deletes `display_render.py`'s `displayio.Group`,
  `TileGrid`, and `Bitmap` composition model (`render_key`,
  `raw_bitmap_tile_grid`, and `KeyState`'s image handling), built in
  task 0006. This is a rewrite of that module, not an extension.
- Bad, because `glyphs.py`'s generated 1-bit glyph bitmaps and
  `wire.py`'s custom-glyph RGB565 buffer, from tasks 0023 and 0030, are
  shaped for `displayio.Bitmap`. A raw-framebuffer driver needs its own
  conversion path for both.

### Approach C — Compile CircuitPython from source with a raised display limit

Clone `adafruit/circuitpython`, add `#define CIRCUITPY_DISPLAY_LIMIT (6)`
to the `pimoroni_pico_plus2` board's `mpconfigboard.h`, and build a
custom UF2 in place of the one `make firmware-uf2` downloads.

- Good, because `code.py`, `display_render.py`, and every existing test
  stay unchanged. The fix lives entirely outside this repository's code.
- Good, because the change itself is small and documented: one
  `#define`, then `make -j10 BOARD=pimoroni_pico_plus2` from
  `ports/raspberrypi`.
- Bad, because [adafruit/circuitpython#6395](https://github.com/adafruit/circuitpython/issues/6395)
  reports a hard crash on RP2040 and ESP32-S3 when a raised limit
  creates a display bus past the first couple. This is the same shared
  `displayio` C code that the RP2350 port uses, and the issue is still
  open, with no fix.
- Bad, because it replaces `make firmware-uf2`'s pinned, official
  download with a custom build. The project must then maintain the
  toolchain and rebuild it every time CircuitPython is upgraded.

## Decision

Chosen: **Approach A — Per-frame display-bus construct and release**.

Approach C's core mechanism is to raise `CIRCUITPY_DISPLAY_LIMIT`. That
change has a known, unresolved crash for exactly the case this task
needs: more than a couple of buses. This board's port shares that same
C code. That risk is not worth accepting for a fix that also demands a
new custom-build maintenance burden the project does not have today.
Approach B removes the ceiling most cleanly but rewrites task 0006's
entire render module and its tests, a bigger cost than 6 working
displays requires. Approach A stays on the stock, pinned firmware and
pays only a proven-safe redraw-latency cost, which this task accepts and
must measure.

## Design

`app.py`'s render step calls `render_key(display, key_state,
emoji_lookup)` against a `displays[index]` that `code.py` builds once
and holds for the process's life. This task moves bus construction
inside the per-key redraw. A small helper builds a `fourwire.FourWire`
and `ST7735R` for the key that needs a redraw. It calls `render_key`,
then releases the bus before it returns.

Files to change:

- `firmware/code.py` — remove the persistent `displays` list, and drop
  the `BRING_UP_KEYS` stopgap.
- `firmware/app.py` — the render step takes a bus-builder callable
  instead of a list of live displays.
- `firmware/display_render.py` — `render_key`, or a new wrapper, owns
  the construct-draw-release sequence.
- `test/test_app.py`, `test/test_display_render.py` — fakes model a
  builder callable, not a pre-built display list.
- `firmware/README.md` — document the per-redraw bus lifecycle and link
  this spec.

## Definition of done

- [ ] **DoD-1** — With 2 keys wired, both displays render independent
  content within one `code.py` run, with no "Too many display busses"
  error. **Proof:** photo or video of both displays showing different
  content, linked from this spec.
- [ ] **DoD-2** — `code.py` builds from the full `pins.KEYS`, not a
  scoped subset. **Proof:** `grep -n BRING_UP_KEYS firmware/code.py`
  finds nothing.
- [ ] **DoD-3** — The display-render and app test suites pass against
  the new design. **Proof:** `.venv/bin/pytest test/test_display_render.py
  test/test_app.py -q` passes.
- [ ] **DoD-4** — The redraw latency of the new per-frame bus
  construction is measured and recorded. **Proof:** a figure in
  `firmware/README.md`, comparable to task 0010's loop-period entry.
- [ ] **DoD-5** — `firmware/README.md` documents the per-redraw bus
  lifecycle and links this spec. **Proof:** `firmware/README.md`.
- [ ] **DoD-6** — The PR in the `pr` field links to this spec.
  **Proof:** PR body.

## Risks

- Per-frame `ST7735R` construction can be too slow to finish inside one
  main-loop tick when many keys are dirty at once, such as several keys
  blinking together → DoD-4's measurement decides whether a redraw
  budget or a max-dirty-per-tick cap is needed. File that as its own
  follow-up if the measurement shows this.
- Releasing a display bus mid-refresh, with `auto_refresh=False` and a
  pending transfer, can corrupt the next display's init sequence on the
  shared SPI lines → confirm that `refresh()` blocks until the transfer
  completes before release, during this design's first real test.

## Open questions

- [ ] Does `ST7735R`'s panel init sequence run in full on every
  construction, or does re-constructing over an already-initialized
  panel skip part of it? — confirm this by timing DoD-4's measurement
  against a single held-open baseline.

## Notes

Research backing Approach C's rejection: `CIRCUITPY_DISPLAY_LIMIT` is
documented in [todbot's "Multiple Displays in CircuitPython & Compiling
Custom CircuitPython"](https://todbot.com/blog/2022/05/19/multiple-displays-in-circuitpython-compiling-custom-circuitpython/).
The crash risk is [adafruit/circuitpython#6395](https://github.com/adafruit/circuitpython/issues/6395).

Found live during task 0010's single-key bring-up: `firmware/code.py`
currently sets `BRING_UP_KEYS = pins.KEYS[:1]` as a temporary stopgap
that references this task, so key 0 stays testable while this design is
pending.
