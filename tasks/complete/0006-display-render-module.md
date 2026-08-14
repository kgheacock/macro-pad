---
id: "0006"
title: "Add the display render module"
status: "complete"
created: "2026-08-03"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/8"
branch: "0006-display-render-module"
related: ["0001", "0002", "0009"]
tags: ["firmware", "display"]
---

# 0006 — Add the display render module

## Problem

`firmware/README.md` commits to rendering per-key emoji, background color,
and blink state, but no rendering code exists, and the physical ST7735
displays are not wired up yet. `hardware/README.md` marks the module SKU
unconfirmed.

## Goals

- A render module computes what to draw for a key — emoji, color, blink
  on or off — from key state, apart from the real display driver call.
- The module runs against a fake display object, so its logic is testable
  before hardware exists.
- The module is ready to take a real `adafruit_st7735r` display object once
  task 0010 confirms the wiring.

## Non-goals

- The real SPI or `adafruit_st7735r` driver integration and timing. Task
  0010 covers that, once hardware exists and the SKU is confirmed.
- Emoji asset sourcing. This task assumes an emoji ID to bitmap lookup
  already exists or is stubbed.

## Approaches considered

### Approach A — Inject a display interface

Define a small `DisplayLike` protocol (`fill`, `draw_bitmap`, dimensions)
that both a real `adafruit_st7735r` object and a test fake satisfy. The
render module calls only that interface.

- Good, because the same render code runs in tests and on real hardware,
  with no test-only branch.
- Good, because the small interface, three to four methods, is quick to
  fake now and to check against the real driver later.
- Bad, because the fake interface may not capture real ST7735 quirks, such
  as color order or refresh timing, so a mismatch could surface only on
  real hardware.
- Bad, because the real display library's method surface is guessed in
  advance, with some rework possible once hardware arrives.

### Approach B — Framebuffer-only rendering

Render into an in-memory pixel buffer, shaped like a `displayio.Bitmap`,
and treat pushing it to a physical display as a separate, later step.

- Good, because it fully decouples rendering logic from any display driver
  API, which makes pixel-exact testing easy.
- Good, because framebuffer output is directly assertable in a test.
- Bad, because it adds a bespoke buffer type now for a benefit,
  pixel-exact testing, that `firmware/README.md` does not ask for.
- Bad, because it still needs a translation layer to the real driver
  later, so it moves work rather than removing it.

### Approach C — Defer all rendering code until hardware arrives

Write no rendering code now. Wait for task 0010.

- Good, because it carries zero risk of rework from a guessed interface.
- Good, because it spends no time on speculative design.
- Bad, because it blocks the render module until hardware ships, which
  defeats the purpose of this plan.
- Bad, because it leaves the emoji, color, and blink selection logic,
  which needs no display at all, untested for no reason.

## Decision

Chosen: **Approach A — Inject a display interface**.

A small injected interface lets the state-selection logic — which emoji,
which color, blink on or off — get tested now, while isolating the one
part, a live ST7735 object, that truly needs hardware. This choice accepts
some risk of interface mismatch once real hardware is wired.

## Design

`firmware/display_render.py` exposes
`render_key(display: DisplayLike, key_state: KeyState, emoji_lookup) -> None`,
where `KeyState` holds the emoji ID, color, and blink flag, and
`emoji_lookup` maps an emoji ID to a ready-made `displayio.TileGrid` (the
"lookup already exists or is stubbed" this task's non-goals call for).
`render_key` builds a `displayio.Group` — a solid-color background tile
plus the looked-up emoji tile when the blink phase is visible — and pushes
it with `display.show(group)` / `display.refresh()`.

`DisplayLike` was originally drafted as a `Protocol` with `fill(color)`
and `draw_bitmap(bitmap)`. Extracting the string table from
`firmware/modules/adafruit-circuitpython-pimoroni_pico_plus2-en_US-10.2.1.uf2`
during implementation showed that build ships `busdisplay.BusDisplay` and
`fourwire.FourWire`, not the older `displayio.Display`/`FourWire` pair,
and does not bundle `adafruit_st7735r` at all (it's a separate library
copied into `lib/`). `adafruit_st7735r.ST7735R` subclasses `BusDisplay`
and adds no methods of its own — there is no `fill()` or `draw_bitmap()`
on that class; those belong to the older, non-displayio
`adafruit_rgb_display` driver. `DisplayLike` was redesigned around
`show(group)` and `refresh(**kwargs)` instead, which `BusDisplay` (and so
`ST7735R`) actually exposes. See the resolved risk note below.

Files to change:

- `firmware/display_render.py` — new
- `test/test_display_render.py` — new, uses a `FakeDisplay` test double

## Definition of done

- [x] **DoD-1** — `render_key()` sets the background layer to the key's
  configured color. **Proof:**
  `pytest test/test_display_render.py::test_fill_color`
- [x] **DoD-2** — `render_key()` draws the bitmap that matches the key's
  emoji ID. **Proof:**
  `pytest test/test_display_render.py::test_emoji_bitmap`
- [x] **DoD-3** — When blink is on, alternating calls to `render_key()`
  toggle visibility. **Proof:**
  `pytest test/test_display_render.py::test_blink_toggle`
- [x] **DoD-4** — `DisplayLike` lists only methods present on
  `adafruit_st7735r.ST7735R`. **Proof:** `firmware/display_render.py`
  docstring cites the driver method names
- [x] **DoD-5** — The new tests fail on `main`. **Proof:**
  `git stash -u && pytest test/test_display_render.py` fails
- [x] **DoD-6** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- `DisplayLike` may not match the real `adafruit_st7735r` API once
  hardware arrives → task 0010 re-validates and adjusts the interface
  against the real driver. **Resolved during this task**: checking the
  pinned CircuitPython firmware's string table showed the fill/draw_bitmap
  shape was wrong even before hardware exists (see Design). `DisplayLike`
  was redesigned around `show`/`refresh`. Task 0010 should still confirm
  this against the real `ST7735R` object once hardware is wired, since the
  firmware's core build doesn't include the driver library itself.
