---
id: "0033"
title: "Mutate the display scene graph in place instead of rebuilding it every render_key call"
status: "ongoing"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/33"
branch: "0033-mutate-display-scene-graph-in-place"
related: ["0006", "0022", "0031"]
tags: ["firmware", "display", "performance", "bring-up"]
---

# 0033 — Mutate the display scene graph in place instead of rebuilding it every render_key call

## Problem

Live testing for task 0031 found that a blinking key visibly wipes
top-to-bottom on the panel. `firmware/display_render.py`'s `render_key`
builds a brand-new `displayio.Group`, `Bitmap`, and `TileGrid` on every
call and reassigns `display.root_group`. displayio cannot diff a fresh
object graph against the last frame, so it repaints the full 128x128
panel every call. A debug-console timing probe measured
`display.refresh()` at 43-45ms per call, about 5x the SPI clock's own
transfer-time floor. The gap is fixed per-row driver overhead, not
bandwidth. A higher SPI baudrate cannot close it. Task 0031 already
tried 24MHz to 32MHz, with little effect.

## Goals

- A blinking key that uses a real, non-full-screen glyph no longer wipes
  the full panel. Only the glyph's own area redraws.
- `render_key` and `KeyState` still pass the full pytest suite with no
  board attached.
- The reserved placeholder glyph (emoji ID `0x00`) still renders exactly
  as it does today.

## Non-goals

- Making the placeholder glyph's own redraw fast. It is a full-panel
  image by design — see "Emoji IDs" in `docs/wire-protocol.md`. This
  task does not change that.
- Sharing the display bus across all 6 keys. Task 0032 owns that.
- Raising the SPI baudrate further. Task 0031 set `DISPLAY_BAUDRATE =
  32_000_000`. This task targets redraw area, not clock speed.

## Approaches considered

### Approach A — Persistent scene graph, mutated in place

Build each key's `Group`, background `Palette`, and glyph `TileGrid`
once. On a later call, update the existing objects instead of building
new ones. For a blink toggle, set the glyph `TileGrid`'s `hidden`
property. For a color change, write a new color into the existing
`Palette`.

- Good, because displayio's own per-`TileGrid` dirty tracking can then
  shrink the redraw to the changed region, which shrinks blink cost for
  any glyph smaller than the full panel.
- Good, because it keeps `render_key`'s public shape and every glyph
  source — `glyphs.py`, `wire.py`'s custom-glyph buffer — unchanged.
- Bad, because `KeyState` gains a mutable displayio graph on top of the
  plain data it holds today. A missed update path becomes a stale
  pixel bug, not a crash.
- Bad, because it does not help the placeholder glyph, still a
  full-panel `TileGrid` by design.

### Approach B — Bypass displayio, drive the panel with a hand-rolled SPI writer

Replace `adafruit_st7735r.ST7735R` and `displayio` with a lower-level
driver that owns its own framebuffer and can batch a full-frame push
into fewer, larger SPI transactions than displayio's per-row calls.

- Good, because it can cut per-row command overhead directly. If the
  overhead comes from transaction count, not a fixed per-transfer cost
  the RP2350's SPI peripheral pays regardless of batching, this
  approach helps.
- Good, because it gives full control over exactly what bytes cross the
  wire, useful for confirming the root cause with certainty.
- Bad, because it deletes task 0006's whole composition model:
  `render_key`, `raw_bitmap_tile_grid`, and `KeyState`'s image
  handling. It also deletes every test built against that model. This
  repeats task 0032's Approach B's rejected cost.
- Bad, because the root cause is unproven past correlation. If the
  RP2350 pays a fixed per-transfer cost regardless of row count, this
  rewrite cannot close the gap either.

### Approach C — Accept the cost, tune the blink rate around it

Make no rendering change. Document the 43-45ms full-panel cost as an
`ST7735R`/`displayio` characteristic on this board. If needed, widen
`BLINK_INTERVAL_US` so the redraw is a smaller fraction of each cycle.

- Good, because it adds no code and no new failure mode to
  `KeyState` or `render_key`.
- Good, because `BLINK_INTERVAL_US` already keeps the redraw at 500ms's
  8.6% of frame time. A small widen is a one-line change.
- Bad, because the wipe stays visible on every key-state change, not
  only on blink. `_apply_host_report` marks a key dirty and calls
  `render_key` once per change. Each call costs the same 43-45ms.
- Bad, because the real cause — a full object-graph rebuild every call
  — stays unaddressed. A future feature that redraws more often, such
  as a status animation, pays the same cost.

## Decision

Chosen: **Approach A — Persistent scene graph, mutated in place**.

Approach B's rewrite cost matches task 0032's already-rejected Approach
B, and its root-cause fix is unproven. Approach C leaves every
non-blink key-state change at the same full-panel cost it pays today.
Approach A keeps today's composition model and test surface. It is
also the one change here that gives displayio a chance to redraw less
than the full panel. The cost it accepts is a `KeyState` that now owns
live displayio objects, not just data.

## Design

`render_key` splits into a build step, run once per key, and an update
step, run on every later call. `KeyState` holds the built `Group`,
`Palette`, and glyph `TileGrid` alongside its existing data fields.
`app.py`'s `_render_dirty_keys` calls build on a key's first render and
update after that.

Files to change:

- `firmware/display_render.py` — split `render_key` into build and
  update; `KeyState` gains the persistent displayio objects.
- `firmware/app.py` — `_render_dirty_keys` picks build vs. update per
  key.
- `test/test_display_render.py`, `test/test_app.py` — cover a
  color-only change, a glyph change, and a blink-only toggle as
  separate cases against the new split.
- `firmware/README.md` — document the persistent scene graph and link
  this spec.

## Definition of done

- [ ] **DoD-1** — A blinking key with a real, non-full-screen glyph
  redraws in measurably less than the 43-45ms full-panel baseline.
  **Proof:** a recorded millisecond figure in `firmware/README.md`,
  taken with the same debug-console timing method task 0031 used.
  **Not confirmed.** This implementation session had no board attached
  to measure against. `firmware/README.md`'s new "Persistent display
  scene graph" section records the figure as not yet measured and names
  the exact probe to run — the same open question this spec's Open
  questions section already flagged as needing hardware.
- [x] **DoD-2** — The placeholder glyph (`0x00`) still renders
  correctly. Its cost is documented as unchanged by design.
  **Proof:** `firmware/README.md`'s entry for DoD-1 states this.
- [x] **DoD-3** — `render_key`, `KeyState`, and the app render step pass
  the full test suite. **Proof:** `.venv/bin/pytest test/ -q` passes.
- [x] **DoD-4** — Dedicated tests cover a color-only change, a glyph
  change, and a blink-only toggle against the new split.
  **Proof:** test names in `test/test_display_render.py` and
  `test/test_app.py`.
- [x] **DoD-5** — `firmware/README.md` documents the persistent scene
  graph and links this spec. **Proof:** `firmware/README.md`.
- [ ] **DoD-6** — The PR in the `pr` field links to this spec.
  **Proof:** PR body.

## Risks

- displayio's per-`TileGrid` dirty tracking, on this board's
  CircuitPython build, can fail to shrink `refresh()`'s time the way
  this design assumes → measure on hardware early, with the same
  debug-console probe, before finishing the rest of the refactor.
- A persistent, mutable scene graph is more stateful than today's
  rebuild-every-call design, and a missed update path is a silent stale
  pixel, not a test failure → DoD-4's dedicated tests must cover every
  field `KeyState` can change on its own.

## Open questions

- [ ] Does this CircuitPython build's `BusDisplay.refresh()` redraw
  only a `TileGrid`'s own dirty bounds, or does it always redraw the
  union of every dirty `TileGrid` in the `Group`, including the
  full-panel background? — confirm on hardware before finishing the
  design.

## Notes

Measured live during task 0031's key-0 bring-up, via a temporary
`time.monotonic_ns()` probe around `display.root_group` assignment and
`display.refresh()`, read over `make debug`'s console port.
`root_group` composition took 0.2-0.3ms. `refresh()` took 43-45ms,
consistently, both before and after raising `DISPLAY_BAUDRATE` from
24MHz to 32MHz. Per-row math (43ms / 128 rows ≈ 0.34ms/row) against the
32MHz clock's own per-row transfer-time floor (≈0.06ms/row) puts about
80% of the cost in fixed per-row overhead, not bit transfer.
