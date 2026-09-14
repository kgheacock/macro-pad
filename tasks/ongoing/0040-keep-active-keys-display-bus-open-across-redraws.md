---
id: "0040"
title: "Keep the active key's display bus open across redraws instead of rebuilding it every time"
status: "ongoing"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0040-keep-active-keys-display-bus-open-across-redraws"
related: ["0010", "0032", "0033"]
tags: ["firmware", "display", "hardware", "bring-up"]
---

# 0040 — Keep the active key's display bus open across redraws instead of rebuilding it every time

## Problem

On real hardware, a color set through `keystate.html` does not hold. The
panel shows the right color for one frame, its inverse color for the next,
then goes black. `firmware/display_render.py`'s `render_key_with_builder`
builds a fresh `ST7735R`/`FourWire` object on every redraw, even for the
same key. This object build pulses the display's hardware reset line.
`render_key_with_builder` then releases the object right after each
redraw. This makes the reset-then-invert sequence unreliable each time,
and it breaks bring-up for task 0010.

## Goals

- Setting a key's color, then setting it again (and again), holds the
  correct color every time on real hardware. No flash to the color's
  inverse, no revert to black.
- The one-display-bus-at-a-time hardware limit (task 0032) still holds: two
  display objects are never open at once.
- `MacroPad` and `render_key` still pass the full pytest suite with no board
  attached.

## Non-goals

- Removing the reset pulse when a redraw switches from one key to a
  different key. That pulse still runs; keys still share one physical reset
  line (`pins.DISPLAY_RST`). Fixing that needs its own task, once task 0010
  wires up a second key to test against.
- Rewiring the board with a dedicated reset line per key.
- Explaining, at the ST7735R register level, why the reset-then-invert
  sequence is unreliable. This task only needs the fix to work.

## Approaches considered

### Approach A — Build once, never release

Build each key's display object the first time it redraws, and cache it
forever. Never call `displayio.release_displays()`.

- Good, because it is the smallest possible change: one cache dict.
- Good, because it fixes the reported bug for one key — confirmed live,
  color held correctly across repeat redraws with no release in between.
- Bad, because it crashes the instant a second key redraws: confirmed live
  as `RuntimeError: Too many display busses; forgot
  displayio.release_displays()?`, since task 0032's one-bus limit is a real
  hardware constraint, not a style preference.
- Bad, because it leaks the SPI/CS/RST pins to whichever key redraws first,
  silently breaking every other key with no error until that key's own
  first redraw.

### Approach B — One active key owns the bus; switching keys releases it

`MacroPad` tracks which key currently owns the open display bus. A redraw
for that same key reuses the live object. A redraw for a different key
releases the current one first, then builds the new one.

- Good, because it fixes the reported bug: a redraw of the same key never
  rebuilds, so the reset-then-invert sequence only runs once per key
  switch, not once per redraw.
- Good, because it keeps task 0032's one-bus limit safe: a switch always
  releases the old bus before building the new one.
- Bad, because `MacroPad` needs new state (which key currently owns the
  bus) that today's design does not have.
- Bad, because switching keys still pulses the shared reset line, so one
  key can still glitch when another key's turn comes up. This task accepts
  that cost — see Non-goals.

### Approach C — Give each key its own reset line

Wire a dedicated reset GPIO (or a small GPIO expander) per key, instead of
sharing `pins.DISPLAY_RST` across all six.

- Good, because it is the only approach that removes cross-key
  interference at its root, not just around it.
- Good, because every key already has its own chip-select and switch pin;
  reset is the one line still shared.
- Bad, because it needs physical rewiring of the board, not just a
  firmware change, and the RP2350 pinout in `pins.py` has no spare GPIOs
  budgeted for five more reset lines.
- Bad, because it does not fix the reported bug by itself. The same key,
  rebuilt on every redraw, still reruns an unreliable reset-then-invert
  sequence each time.

## Decision

Chosen: **Approach B — one active key owns the bus**. Only one physical key
is wired today (task 0010 is still unstarted for the rest), and the
reported bug is about that one key losing its color across repeat
redraws, not about two keys interfering with each other yet. Approach B
fixes exactly that, with no hardware change, and keeps Approach A's
one-bus crash from happening. The cost accepted: a key can still glitch
when a different key's redraw takes the bus, left for a later task once a
second key exists to test against.

## Design

`MacroPad` gains `_active_key_index` and `_active_display`, both `None` at
start. `_render_dirty_keys` replaces its call to
`display_render.render_key_with_builder` with logic that checks
`_active_key_index` against the key about to redraw: on a match, it reuses
`_active_display`; on a mismatch (or `None`), it calls
`displayio.release_displays()` if a display is open, builds the new one,
and stores both. `render_key` itself (build vs. update scene graph) does
not change.

Files to change:

- `firmware/app.py` — add `_active_key_index`/`_active_display` to
  `MacroPad`; change `_render_dirty_keys`'s redraw call.
- `firmware/display_render.py` — drop `render_key_with_builder`, now that
  `app.py` owns build/release directly; `render_key` is unchanged.
- `test/test_app.py` — cover: two redraws of the same key build once;
  redrawing a different key releases the first display before building
  the second.
- `firmware/README.md` — document the "active key owns the bus" lifecycle,
  replacing the "build-release every redraw" text task 0032 wrote, and
  link this spec.

## Definition of done

- [x] **DoD-1** — Two consecutive redraws of the same key build the
  display exactly once. **Proof:** a `test/test_app.py` test asserts the
  display-builder function is called once across two redraws of one key.
  `test_active_key_reuses_display_bus_across_redraws` asserts
  `displays.calls == [3]` while both redraws still ran.
- [x] **DoD-2** — Redrawing a different key releases the first display
  before building the second. **Proof:** a `test/test_app.py` test asserts
  release happens before the second build, and both are never open at once.
  `test_switching_keys_releases_bus_before_building_next` asserts the
  builder observed `displayio.release_display_count` already incremented
  at the moment it was called; `test_display_build_failure_leaves_no_active_key`
  covers the Risks note below, that a build failure after release leaves
  `_active_key_index` at `None`, not the new key's index.
- [x] **DoD-3** — The full test suite passes. **Proof:** `pytest test/`
  passes with no board attached. `.venv/bin/pytest test/ -q` → 82 passed.
- [ ] **DoD-4** — On real hardware, five consecutive `keystate.html` Set
  clicks to the same key, each a different color, all hold correctly. No
  flash to the color's inverse, no revert to black. **Proof:** a person
  watches the panel through all five clicks and records the result in this
  spec's Notes.
  Not confirmed — this proof needs a person watching the real board
  through `keystate.html`, which this implementation pass did not have
  access to. Still open.
- [x] **DoD-5** — `firmware/README.md` documents the new bus lifecycle and
  links this spec. **Proof:** `firmware/README.md`. The "Display bus"
  section now documents `_active_key_index`/`_active_display` and links
  this spec; the blink-latency and per-key-switch-latency sections were
  also updated since they described the old per-redraw rebuild.
- [ ] **DoD-6** — The PR in the `pr` field links to this spec. **Proof:**
  PR body.

## Risks

- A second physical key, once task 0010 wires it up, will still glitch the
  first key's panel on every switch between them → tracked as this task's
  accepted Non-goal; needs its own follow-on task once a second key exists
  to test against.
- An exception between releasing the old display and finishing the new
  key's render could leave `_active_key_index` pointing at a key with no
  open display → DoD-2's test should also cover a build failure leaving
  `_active_key_index` at `None`, not the new key's index.

## Open questions

- [ ] Does keeping the bus open remove the invert flakiness entirely, or
  only lower how often it happens? — confirm with DoD-4's hardware test.

## Notes

Found while testing task 0039 (unrelated: firmware glyph table removal) on
real hardware. Confirmed live: `MacroPad.key_states[0].color` stayed
correct through every redraw the whole time — the bug is not in `KeyState`
or `_apply_host_report`, since debug logging showed the software state
never changes on its own. Confirmed live: forcing Approach A (never
release) crashed with `RuntimeError: Too many display busses; forgot
displayio.release_displays()?` the moment a second key (with no physical
panel attached) tried its own first build — proof that today's
build-then-release-every-redraw path already runs once per key at boot,
for every key task 0010 has not wired up yet.
