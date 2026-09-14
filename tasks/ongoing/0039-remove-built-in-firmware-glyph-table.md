---
id: "0039"
title: "Remove the built-in firmware glyph table now that the API renders emoji"
status: "ongoing"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0039-remove-built-in-firmware-glyph-table"
related: ["0023", "0030", "0034", "0037"]
tags: ["firmware", "driver", "display"]
---

# 0039 — Remove the built-in firmware glyph table now that the API renders emoji

## Problem

`firmware/glyphs.py` holds a built-in bitmap for digits 1-6, generated from
PNGs in `hardware/glyphs/` (task 0023). Task 0030's custom glyph message,
plus 0034's `macrodriver emoji` and 0036's "Set emoji" button, now render
any character on a key from the driver side. The firmware table is a
second, redundant way to put a glyph on a key.

## Goals

- `firmware/glyphs.py` renders only the placeholder box and the custom-image
  sentinel. It holds no digit bitmaps.
- No file under `hardware/glyphs/` or `tools/gen_glyphs.py` remains.
- `docs/wire-protocol.md`'s Emoji ID table lists no digit IDs.
- The keystate page's Reset button sends no digit glyph ID.

## Non-goals

- Changing how a custom glyph (0030) or a rendered emoji (0034, 0036) reaches
  a key. Both already bypass the built-in table.
- Adding a replacement visual for "which key is this" on reset. See Decision.

## Approaches considered

### Approach A — Delete the table, drop the reset digit

Hand-write `firmware/glyphs.py` down to the placeholder box only. Delete
`hardware/glyphs/`, `tools/gen_glyphs.py`, and the digit rows in
`docs/wire-protocol.md`. Change keystate.html's Reset to send a neutral
color and blink off, with no glyph ID.

- Good, because it removes the whole PNG-to-bitmap build pipeline, not just
  the digit entries — one less generated file to keep in sync.
- Good, because Reset becomes a plain `setKeyState` call, the same shape
  `keystate.html` already sends for Set.
- Bad, because the CRUD page loses the digit that marks each row's key
  index on reset.
- Bad, because it touches five files (firmware, docs, tests, the web page,
  and `driver/README.md`) for what reads like a one-file change.

### Approach B — Replace the digit with a client-rendered custom glyph

Approach A's firmware and doc deletions stay the same. But keystate.html's
Reset button changes: it rasterizes the key's digit on an offscreen canvas,
the same technique its "Set emoji" button already uses. It then sends the
result as a custom glyph, not a built-in ID.

- Good, because the CRUD page keeps today's per-key visual on reset, so
  nobody has to relearn the tool.
- Good, because it reuses code the page already has (the canvas-to-
  `setCustomGlyph` path), not a new rendering system.
- Bad, because every Reset now sends a 32,768-byte CDC message instead of a
  6-byte HID one — a slower reset than today's.
- Bad, because it adds a second canvas-drawing branch (digit text, next to
  emoji character) to keep in sync with `docs/wire-protocol.md`.

### Approach C — Render the reset digit through the daemon

Add a new plugin message that asks `macropadd` to render text on its own.
It uses the same Go image pipeline task 0037 built for static HTML, then
sends the result as a custom glyph.

- Good, because the rendering logic lives once, in Go, reusable by
  `macrodriver` and any future plugin, not duplicated in browser JS.
- Good, because it sidesteps browser font differences between the digit and
  whatever emoji font drew the row's other glyph.
- Bad, because it adds a new plugin message kind and daemon handler for a
  reset button — real new surface area for a small, cosmetic feature.
- Bad, because `keystate.html` is a static page with no build step and no
  server component of its own. A round trip to the daemon for a digit
  breaks that model.

## Decision

Chosen: **Approach A — delete the table, drop the reset digit**. The task's
goal is to remove the on-firmware glyph table, not to preserve the reset
button's look. Approaches B and C both add new code to keep one incidental
visual. That cost is not worth paying for a button whose only job is to
clear a key. The cost accepted: the CRUD page's Reset no longer marks a row
with its key index.

## Design

`firmware/glyphs.py` stops being generated. It keeps `PLACEHOLDER_ID` and a
`lookup(emoji_id, foreground, background)` that always returns the
placeholder box, so `code.py`'s `emoji_lookup` and `display_render.py` need
no change.

Files to change:

- `firmware/glyphs.py` — hand-write the placeholder-only version. Drop the
  "generated" docstring.
- `hardware/glyphs/` — delete the directory (six PNGs).
- `tools/gen_glyphs.py` — delete.
- `test/test_glyphs.py` — drop `test_digit_matches_source` and
  `test_lookup_caches_bitmap` (both need a real digit ID). Keep
  `test_unknown_id_returns_placeholder`.
- `docs/wire-protocol.md` — remove the `0xF1`-`0xF6` rows from the Emoji IDs
  table. Keep `0x00` and `0xFE`.
- `driver/plugin/web/keystate.html` — change `sendReset` to send
  `{color: <neutral>, emojiId: 0, blink: false}`. Delete `RESET_EMOJI_BASE`
  and its comment.
- `driver/README.md` — rewrite the Reset paragraph (around line 420) to
  describe the new neutral-color behavior, and drop the "digit glyph" phrase
  from the emoji ID field's description.

## Definition of done

- [x] **DoD-1** — `firmware/glyphs.py` has no `SOURCES`-style digit entries.
  **Proof:** `grep -n "0xF1\|0xF2\|0xF3\|0xF4\|0xF5\|0xF6" firmware/glyphs.py`
  returns nothing
- [x] **DoD-2** — `hardware/glyphs/` and `tools/gen_glyphs.py` no longer
  exist. **Proof:** `git status` shows both deleted, and `ls hardware/glyphs`
  fails
- [x] **DoD-3** — `docs/wire-protocol.md`'s Emoji ID table lists only
  `0x00` and `0xFE`. **Proof:** the table under "## Emoji IDs"
- [x] **DoD-4** — Resetting a key from `keystate.html` sends no glyph ID.
  **Proof:** `grep -n "RESET_EMOJI_BASE" driver/plugin/web/keystate.html`
  returns nothing
- [x] **DoD-5** — `test/test_glyphs.py` passes with only the placeholder
  test left. **Proof:** `pytest test/test_glyphs.py` passes with 1 test
- [x] **DoD-6** — The full firmware test suite still passes with the
  smaller table. **Proof:** `pytest test/` passes (78 passed)
- [ ] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:**
  PR body

## Risks

- A firmware build still on the CIRCUITPY drive expects `firmware/glyphs.py`
  to hold digit IDs → `make flash` overwrites it with the new file on the
  next deploy. No board runs stale code past that point.

## Notes

`glyph_state.py`'s persisted format is unaffected. It stores whatever
`emoji_id` a key last had, built-in or not. A key already showing a digit
ID keeps replaying it until the next `setKeyState`. That is existing 0031
behavior, not something this task changes.
