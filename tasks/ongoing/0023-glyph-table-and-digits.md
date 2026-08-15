---
id: "0023"
title: "Add the glyph table and the digit glyphs 1 to 6"
status: "ongoing"
created: "2026-08-14"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/18"
branch: "0023-glyph-table-and-digits"
related: ["0002", "0006", "0013", "0020", "0022"]
tags: ["firmware", "display", "assets"]
---

# 0023 — Add the glyph table and the digit glyphs 1 to 6

## Problem

`display_render.render_key` takes an `emoji_lookup` callable, and nothing
supplies one. Task 0006 put asset sourcing out of scope. The wire
protocol's emoji ID is therefore a byte that indexes a table that does not
exist, so no key can draw anything. Task 0020's harness needs the digits 1
to 6 before it can prove a single scenario.

## Goals

- One module maps an emoji ID to a `displayio.TileGrid` that
  `render_key` draws.
- IDs `0xF1` to `0xF6` draw the digits 1 to 6, readable on the 0.85 inch
  ScreenKey.
- An unknown ID draws a defined placeholder, not an exception.
- The table runs under pytest against the existing `displayio` stub.
- Firmware and driver name the same IDs from one written source.

## Non-goals

- Real emoji art. The digits unblock task 0020. A later task chooses the
  emoji set and its pipeline.
- Color art. This task ships one-bit glyphs, foreground and background
  only.
- Changing `render_key`. It already takes the lookup this task supplies.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Generate a Python module of packed bitmaps

A script converts source images into `firmware/glyphs.py`, which holds one
packed `bytes` literal per glyph. `lookup(emoji_id)` unpacks the literal
into a `displayio.Bitmap` and returns a `TileGrid`.

- Good, because it adds no CircuitPython library and no file read, so a
  glyph draws from memory that is already loaded.
- Good, because a glyph is in the repository as reviewable source, so a
  changed pixel shows in a diff and a test asserts it with no fixtures.
- Bad, because a color glyph at 128 by 128 pixels would add roughly 64 kB
  of source per glyph, so the generated file cannot grow into real emoji
  art.
- Bad, because changing a glyph needs the generator script and its source
  image, so art edits are a build step, not a file swap.

### Approach B — Load BMP files from the board with `adafruit_imageload`

Ship a `glyphs/` folder of BMP files onto `CIRCUITPY`. `lookup` loads the
matching file at runtime and caches the result.

- Good, because art changes are a file copy, so a color emoji set drops in
  with no code change and no generator.
- Good, because it handles color art at the size real emoji need, which
  Approach A cannot.
- Bad, because it adds the firmware's first vendored CircuitPython library
  and a stub for it before any test can run.
- Bad, because the first draw of each glyph reads the filesystem inside
  the task 0022 loop, which adds an unmeasured delay to that key's first
  frame.

### Approach C — Render digits from a bitmap font at runtime

Vendor `adafruit_bitmap_font` and a BDF font. `lookup` maps an ID to a
character and draws the glyph from the font.

- Good, because a font covers every digit and letter in a few kilobytes,
  far less than one packed bitmap per glyph.
- Good, because glyph size follows the font, so the same source scales to
  a different display without new assets.
- Bad, because a font has no emoji, so the real goal needs a second
  mechanism and this one becomes a digits-only detour.
- Bad, because it adds a library and a font file to serve six glyphs that
  Approach A holds in a few hundred bytes.

## Decision

Chosen: **Approach A — a generated module of packed bitmaps**.

The need now is six one-bit digits, and Approach A delivers them with no
library, no filesystem read, and no new stub, which keeps task 0022's loop
period unaffected. The cost accepted is that the generated file cannot
hold color emoji. When real art lands, revisit this decision and expect
Approach B.

## Design

`firmware/glyphs.py` holds `GLYPHS`, a dict from emoji ID to a packed
one-bit bitmap plus its width and height. `lookup(emoji_id, foreground,
background) -> displayio.TileGrid` unpacks a glyph into a
`displayio.Bitmap` with a two-color `displayio.Palette`, and caches each
unpacked bitmap by ID. An unknown ID returns the glyph at ID `0x00`, a
filled box.

`tools/gen_glyphs.py` regenerates the module from the PNG files in
`hardware/glyphs/`. The generated file carries a header comment naming the
script and the source folder.

Task 0020 reserves `0xF1` to `0xF6` for the digits in
`docs/wire-protocol.md`. This task adds `0x00`, the placeholder, to that
same "Emoji IDs" section. If 0023 lands first, it writes the section and
0020 cites it.

Files to change:

- `firmware/glyphs.py` — new, generated. `GLYPHS`, `lookup`
- `tools/gen_glyphs.py` — new. PNG to packed bitmap generator
- `hardware/glyphs/1.png` to `6.png` — new. Source art
- `docs/wire-protocol.md` — "Emoji IDs" section: add `0x00`
- `test/test_glyphs.py` — new
- `firmware/README.md` — how to add a glyph and regenerate the module

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — `lookup(0xF3, ...)` returns a `TileGrid` whose bitmap
      matches the pixels of `hardware/glyphs/3.png`. **Proof:**
      `.venv/bin/pytest test/test_glyphs.py -k digit_matches_source`
- [ ] **DoD-2** — `lookup` on an unregistered ID returns the placeholder
      glyph and raises nothing. **Proof:** `.venv/bin/pytest
      test/test_glyphs.py -k unknown_id_returns_placeholder`
- [ ] **DoD-3** — Two `lookup` calls for the same ID return grids backed
      by the same `Bitmap` object. **Proof:** `.venv/bin/pytest
      test/test_glyphs.py -k lookup_caches_bitmap`
- [ ] **DoD-4** — `render_key` draws a digit through `lookup` with no
      change to `firmware/display_render.py`. **Proof:**
      `.venv/bin/pytest test/test_display_render.py -k renders_digit` and
      `git diff --stat` shows `display_render.py` unchanged
- [ ] **DoD-5** — Re-running `tools/gen_glyphs.py` leaves
      `firmware/glyphs.py` byte-identical. **Proof:** `python3
      tools/gen_glyphs.py && git diff --exit-code firmware/glyphs.py`
- [ ] **DoD-6** — `docs/wire-protocol.md`'s "Emoji IDs" section reserves
      `0x00` for the placeholder. **Proof:** `docs/wire-protocol.md`
- [ ] **DoD-7** — `firmware/README.md` records how to add a glyph and
      regenerate the module. **Proof:** `firmware/README.md`
- [ ] **DoD-8** — The PR in the `pr` field links to this spec. **Proof:**
      PR body

## Risks

- Digit art sized for the wrong display is unreadable → the generator
  reads its target size from one constant, and task 0010 confirms it on
  the real ScreenKey.
- The generated module is edited by hand and then overwritten → DoD-5
  makes the regeneration check part of review, and the file header names
  the script.
- The ScreenKey's real resolution is unconfirmed, since
  `hardware/README.md` marks the SKU unconfirmed → generate at the
  documented 128 by 128 pixels, and regenerate if bring-up finds
  otherwise.

## Open questions

- [ ] Which foreground and background colors do the digits use by default,
      or does the caller always pass them? — owner, when task 0020's
      scenario is written.
