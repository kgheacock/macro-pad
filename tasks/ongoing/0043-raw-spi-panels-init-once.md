---
id: "0043"
title: "Drive the six panels over raw SPI, initialised once, so one key's update never touches another"
status: "ongoing"
created: "2026-10-05"
updated: "2026-10-05"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0043-raw-spi-panels-init-once"
related: ["0010", "0030", "0032", "0033", "0040", "0041", "0042"]
tags: ["firmware", "display", "driver", "hardware", "performance"]
---

# 0043 — Drive the six panels over raw SPI, initialised once, so one key's update never touches another

## Problem

An update to one key resets all six panels. This board allows one displayio
bus at a time, so each key switch releases the bus and builds a new one. The
build pulses the shared RST line and reruns the full panel init (763 ms,
measured). The other five panels lose their image. Task 0040 only fixed
repeat redraws of the same key.

## Goals

- An update to key N leaves the image on the other five panels unchanged.
- After boot, no key switch pulses RST or sends an init command.
- A blink toggle takes at most 30 ms. A color change takes at most 100 ms.
- The board converts no pixels. Today a glyph build takes 447 ms to 31 s.

## Non-goals

- One shared backlight PWM pin. A later task can free five GPIOs.
- The 12-bit pixel mode. The spikes did not test it.
- A custom CircuitPython build.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Keep displayio, hold RST high

Pass `reset=None` to `FourWire`. Pulse RST once at boot, then hold it high.
Each panel still gets its own software reset from the init sequence.

- Good, because it is about 10 lines in `code.py`, with no rewrite.
- Good, because it stops one key from resetting the others.
- Bad, because every key switch still reruns the 763 ms init, so a blink on
  two keys costs over 1.5 s per toggle.
- Bad, because glyph builds in Python stay slow (447 ms to 31 s).

### Approach B — Raise the displayio display limit to 6

Build CircuitPython with `CIRCUITPY_DISPLAY_LIMIT` set to 6. Each key keeps
its own display for the whole run.

- Good, because the spike measured 46–52 ms per full refresh and 4 ms for a
  small update, with no switch cost.
- Good, because `display_render.py` and its tests stay as they are.
- Bad, because each `FourWire` claims its own DC pin. Five keys need new
  wires, and no key is wired yet.
- Bad, because the custom build hard-faulted twice in six teardowns. It also
  needs a custom firmware that this repo must build and flash.

### Approach C — Raw SPI driver, panels initialised once

A new driver talks to each panel through the one SPI bus and a CS line.
Each panel gets its init once at boot. The driver then pushes cached frames.
The host driver converts the image, so the board only copies bytes.

- Good, because a push of a cached frame takes 21 ms at 16 MHz, against
  50 ms for displayio.
- Good, because a key switch costs nothing extra, and DC stays one shared wire.
- Good, because it needs only the stock CircuitPython build.
- Bad, because it replaces `display_render.py`'s scene graph from task 0033.
- Bad, because it changes the glyph format on the wire and on flash.
- Bad, because a color change costs 66 ms. That is slower than displayio.
- Bad, because the USB link carries only about 455 KB/s, so a full frame costs
  94 ms. The board must cache frames.

## Decision

Chosen: **Approach C — Raw SPI driver, panels initialised once**.

Only C removes the per-switch cost (A pays 763 ms) and needs neither new
wires nor a custom build (B). The decision accepts a rewrite of
`display_render.py`, a protocol change, and a color change 16 ms slower than
displayio.

## Design

`code.py` pulses RST once, holds it high, and initialises each panel once.
`st7735.py` holds one `Panel` per key. Its `push(frame)` sets the window with
the existing column and row offsets, then writes the frame under CS.

`display_render.py` keeps two cached 128×128 frames per key: "on" and "off".
The board rebuilds them only when the color or the glyph changes. It fills the
background, then blits the glyph and skips the transparent value. A blink
pushes the other cached frame.

Transparent pixels use the value `0x0000`. Real black uses `0x0001`. The
driver sends big-endian RGB565, so the board needs no byte swap.

The "Set custom glyph" payload keeps its 32,768 bytes, in the new format. The
flash record gets a format byte. A legacy RGBA4444 record loads as color only.

Files to change:

- `firmware/st7735.py` — new: init sequence, `Panel.push`
- `firmware/display_render.py` — replace the scene graph with cached frames
- `firmware/app.py` — take panels, not `build_display`
- `firmware/code.py` — one RST pulse, six panels
- `firmware/glyph_state.py`, `firmware/wire.py` — new glyph format
- `driver/transport/glyph.go` — emit RGB565 with the transparent marker
- `docs/wire-protocol.md`, `firmware/README.md` — record the change
- `test/` — new `test_st7735.py`; update `test_app.py`, `test_display_render.py`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — After boot, redrawing keys 0, 1, 0 sends no init command and
  makes no RST change. **Proof:** `pytest test/test_app.py::test_key_switch_sends_no_init_or_reset`
- [ ] **DoD-2** — A blink toggle pushes a cached frame and calls no fill or
  blit. A color change rebuilds the frames once. **Proof:**
  `pytest test/test_display_render.py::test_blink_uses_cached_frame test/test_display_render.py::test_color_change_rebuilds_once`
- [ ] **DoD-3** — A glyph message of the wrong length, or for an unknown key,
  is dropped. A legacy RGBA4444 flash record loads as color only. **Proof:**
  `pytest test/test_wire.py test/test_glyph_state.py::test_legacy_record_is_color_only`
- [ ] **DoD-4** — The driver writes `0x0000` for a transparent pixel, `0x0001`
  for black, and big-endian RGB565. **Proof:** `cd driver && go test ./transport/...`
- [ ] **DoD-5** — The new firmware tests fail on `main`'s `firmware/`. The full
  suites pass here. **Proof:** `git checkout main -- firmware && pytest test/test_app.py test/test_st7735.py`
  fails, then `git checkout HEAD -- firmware && pytest test/ && (cd driver && go test ./...)` passes
- [ ] **DoD-6** — On the board, a color change on a key that did not paint last
  takes at most 100 ms from `HOST_MESSAGE_DECODED` to `REFRESH_DONE`. A blink
  push takes at most 30 ms. **Proof:** run `macropadd --trace-file=/tmp/t.jsonl`,
  then read the two records in `/tmp/t.jsonl`
- [ ] **DoD-7** — At the baud rate set in `code.py`, key 0 shows the right color
  and a transparent glyph. The target is 16 MHz. A person records the rate.
  **Proof:** the Notes of this spec
- [ ] **DoD-8** — With two keys wired, ten color changes on key 1 leave key 0's
  image unchanged. **Proof:** a person watches both panels and records the
  result in the Notes of this spec
- [ ] **DoD-9** — `firmware/README.md` and `docs/wire-protocol.md` describe the
  new design. Tasks 0032, 0033, and 0040 link here as their replacement.
  **Proof:** those five files
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- Boot runs six 763 ms inits, about 4.6 s → measure it in DoD-6's trace. If it
  is too long, paint key 0 first.
- A legacy flash record loses its glyph → the key still shows its color, and
  the driver resends the glyph.
- No second key is wired, so DoD-8 stays open → wire a second key under 0010.
- Panels may not render above 4 MHz on the breadboard → DoD-7 records the rate
  that works.
- Six keys with two cached frames each use 384 KB → the board has about 8 MB
  of free heap, measured in the spikes.

## Open questions

- [ ] Which baud rate renders correctly on the breadboard? — kgheacock, with a
  wired key
- [ ] Should the transparent marker move into the frame header? — kgheacock

## Notes

Spike results come from the board on 2026-10-05, with stock CircuitPython
10.2.1 and no panel attached. The spike scripts were throwaway. Key numbers:

- Full init: 763 ms per panel.
- Raw SPI full frame: 80 ms at 4 MHz, 22 ms at 16 MHz, 18.3 ms at 24 MHz.
- Cached frame push: 21.3 ms at 16 MHz.
- Fill plus blit of a glyph: 44 ms. The check found 0 wrong pixels of 16,384.
- One 32 KB frame over USB and SPI: 94 ms (72 ms USB read).
