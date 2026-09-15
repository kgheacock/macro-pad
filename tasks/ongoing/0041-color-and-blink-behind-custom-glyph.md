---
id: "0041"
title: "Show a key's color behind its custom glyph, and blink only the background"
status: "ongoing"
created: "2026-09-14"
updated: "2026-09-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0041-color-and-blink-behind-custom-glyph"
related: ["0030", "0033", "0035", "0038", "0039"]
tags: ["firmware", "driver", "display", "wire-protocol"]
---

# 0041 — Show a key's color behind its custom glyph, and blink only the background

## Problem

A key with a custom glyph shows the glyph's baked-in background color, not
the key's chosen color. `tools/render_emoji.py` bakes a fixed black
background into every rendered emoji. A blink also hides the whole glyph
image, so the character disappears instead of only the background.

## Goals

- A key's chosen color shows through a custom glyph's transparent pixels,
  not a fixed baked-in color.
- While a key blinks, its glyph's opaque pixels stay on screen. Only the
  background color changes.

## Non-goals

- Partial or blended transparency. A pixel is either background or glyph,
  chosen by one alpha bit.
- A configurable off color for the blink's background. The off color
  stays fixed at black.
- Multi-key blink scheduling. This task keeps task 0035's single-key path.
- A new upload tool for task 0030's plain-photo path, beyond the pixel
  format change.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Bake the key's color into the emoji image at render time

`tools/render_emoji.py` fills the canvas with the key's current color
instead of black, before it draws the emoji glyph on top.

- Good, because it needs no wire-protocol change and no firmware change.
- Good, because it reuses the color that `driver/api.Conn` already tracks
  for `SetCustomGlyphBlink` (task 0035).
- Bad, because a color change still costs a full 32,768-byte re-upload
  and a flash write, not a 6-byte Key state message.
- Bad, because it does not solve the second goal. `SetCustomGlyphBlink`
  still hides the whole image, so the character disappears during the
  blink's off half.

### Approach B — Give the wire pixel format an alpha channel

The Set custom glyph pixel format moves from opaque RGB565 to RGBA4444:
4 bits of alpha, 4 bits each for red, green, and blue, still 16 bits per
pixel. `tools/render_emoji.py` keeps the glyph's real alpha channel
instead of flattening it onto black. Firmware builds a `Bitmap` with a
transparent palette entry for alpha-zero pixels, and toggles the
background layer's own color between `key_state.color` and black for a
blink, instead of hiding the glyph layer.

- Good, because a color change and a blink toggle both stay a 6-byte Key
  state message, matching task 0035's cost goal.
- Good, because it solves both goals with one mechanism: a transparent
  pixel always shows the background, and the glyph layer never hides.
- Bad, because it changes an already-shipped wire message's pixel format
  for every custom glyph, not only driver-rendered emoji. Task 0030's
  plain-photo uploads need a defined alpha value too.
- Bad, because `driver/transport/glyph.go` and
  `firmware/display_render.py` both need a real rewrite, not a guard
  clause — a bigger, riskier change than task 0035's.

### Approach C — The driver renders two full images and alternates them

No wire or firmware change. The driver renders the same emoji twice, once
on the key's color and once on black, and calls `SetCustomGlyph` on each
half of the blink interval.

- Good, because firmware and the wire protocol stay exactly as they are
  today.
- Good, because it works the same way for task 0030's plain-photo
  uploads, with no new pixel format to learn.
- Bad, because the CDC link carries a 32,768-byte frame on every blink
  toggle, for as long as the key blinks — the exact cost task 0035's
  Decision rejected.
- Bad, because `firmware/glyph_state.py` writes flash on every change it
  sees, so a blink timer looks like a real change each cycle — the
  flash-wear risk task 0030's Risks and task 0035's Decision both named.

## Decision

Chosen: **Approach B — Give the wire pixel format an alpha channel**.

The second goal rules out Approach A and Approach C. Both keep every
custom glyph one opaque image, so a blink can only hide the whole image,
not the background alone. Approach B's cost is a wire pixel-format
change, accepted because it is the only mechanism that keeps a blink
toggle at 6 bytes.

## Design

Files to change:

- `tools/render_emoji.py` — draw the glyph on a transparent canvas, and
  encode each pixel as RGBA4444 instead of flattening it onto a black
  RGB565 canvas.
- `driver/transport/glyph.go` — replace `DecodePNGToRGB565` with an
  RGBA4444 encoder. `CustomGlyphPixelsSize` stays the same size: 16 bits
  per pixel, no wire growth.
- `firmware/display_render.py` — `_build_glyph` builds a `Bitmap` and
  `Palette` with a transparent entry for alpha-zero pixels, when the
  buffer has at least one transparent pixel. Blink then toggles the
  background layer's own color between `key_state.color` and black. A
  custom image with no transparent pixel keeps today's whole-image hide,
  so a plain photo upload (task 0030) still blinks the way it does now.
- `docs/wire-protocol.md` — record the RGBA4444 pixel format, the alpha
  convention for a plain photo upload (fully opaque), and the new blink
  meaning for a glyph with a transparent pixel.

## Definition of done

- [x] **DoD-1** — A custom glyph with a transparent pixel shows
  `key_state.color` behind that pixel, not a baked-in color. **Proof:** a
  firmware pytest builds a glyph with one transparent corner pixel and
  asserts the rendered corner equals `_rgb565_to_rgb888(key_state.color)`.
  Confirmed: `test/test_display_render.py::test_transparent_pixel_shows_background_color`
  and `test/test_app.py::test_custom_glyph_transparent_pixel_shows_key_color`.
- [x] **DoD-2** — While such a key blinks, its opaque glyph pixels stay
  on screen every frame. Only the background color changes. **Proof:** a
  firmware pytest steps `MacroPad` across two blink intervals and asserts
  an opaque glyph pixel never changes, while a background pixel
  alternates between `key_state.color` and black.
  Confirmed: `test/test_display_render.py::test_transparent_glyph_blink_toggles_background_not_glyph`
  and `test/test_app.py::test_custom_glyph_blink_toggles_background_when_transparent`.
- [x] **DoD-3** — A custom glyph with no transparent pixel keeps today's
  whole-image blink toggle. **Proof:** a firmware pytest sends an
  all-opaque buffer with `Blink=true` and asserts the glyph `TileGrid`'s
  `hidden` flag still toggles across two blink intervals.
  Confirmed: `test/test_display_render.py::test_opaque_pixels_keep_whole_image_blink_toggle`
  and `test/test_app.py::test_custom_glyph_opaque_keeps_whole_image_blink_toggle`.
- [x] **DoD-4** — `tools/render_emoji.py` keeps the glyph's real alpha
  channel, not a black canvas. **Proof:** a driver test decodes the PNG
  for one emoji and asserts a known-transparent pixel's encoded alpha
  nibble is 0.
  Confirmed: `driver/transport/glyph_test.go::TestDecodePNGToRGBA4444_TransparentPixelEncodesZeroAlphaNibble`
  (`go test ./transport/...` passes).
- [x] **DoD-5** — Tests cover the transparent-pixel path and the
  fully-opaque path. **Proof:** `pytest test/test_app.py -k custom_glyph`
  passes. `git stash && pytest test/test_app.py -k custom_glyph` fails on
  `main`.
  Confirmed: `.venv/bin/pytest test/test_app.py -k custom_glyph` passes
  (8 tests) against this branch. Checking out `main`'s
  `firmware/display_render.py` and `test/stubs/displayio.py` while
  keeping this branch's `test/test_app.py` fails 2 of those 8 tests
  (`test_custom_glyph_transparent_pixel_shows_key_color`,
  `test_custom_glyph_blink_toggles_background_when_transparent`) — a
  bare `git stash` on a clean, committed tree has nothing to stash, so
  this checkout swap is the equivalent proof.
- [x] **DoD-6** — `docs/wire-protocol.md` records the RGBA4444 pixel
  format and the new blink meaning. **Proof:** `docs/wire-protocol.md`,
  "Set custom glyph" and "Emoji IDs".
  Confirmed: both sections updated with the RGBA4444 bit layout, the
  one-bit alpha convention, and the transparent-pixel blink meaning.
- [ ] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:**
  PR body.

## Risks

- A key's custom image, persisted under the old RGB565 convention before
  this task, decodes as random alpha and color once this firmware ships
  → re-upload each such key's image after the flash. The device has one
  user, so a manual step is acceptable.
- A fully-opaque check on every new glyph upload costs one pass over
  16,384 pixels → cheap on the RP2350 at upload time, not per frame.

## Notes

Found while reading `firmware/glyphs.py` for this task: since task 0039
removed the built-in glyph table, a key with no custom image shows the
same color on both its background and glyph layers. Its blink is
invisible today, for the same root cause this task fixes.
