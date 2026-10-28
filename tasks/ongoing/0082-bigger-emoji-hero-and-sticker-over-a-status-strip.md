---
id: "0082"
title: "Bigger emoji: a hero emoji, the session emoji as a sticker, and the status as a strip"
status: "ongoing"
created: "2026-10-27"
updated: "2026-10-27"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/105"
branch: "claude/project-thread-4y55wb"
related: ["0062", "0069", "0073", "0074", "0081"]
tags: ["driver", "emoji", "render"]
---

# 0082 — Bigger emoji: hero, sticker, and a status strip

## Problem

An `iconEmoji` key (task 0073) shows a caption and one or two emoji on the
white round, and the status color in the four corners around it. On the
0.85" panel both the caption and the emoji are too small to be useful:

- two emoji are scaled to 48 px each, and one to 64 px;
- every picture in `assets/emoji/` (and every one `fitEmoji` makes from the
  emoji font, task 0074) fills only about 90 of its 128 px, so two emoji show
  about 34 px of art, and one about 45 px;
- the round is narrow at the top, so the caption is at most 84 px wide and
  20 px tall, and "macro-pad" already drops to 16 px;
- the count pip covers half of the second emoji.

Keith wants the emoji much bigger. He picked layout **1 Overlap** from two
rounds of mock-ups (in the project files, `specs/key-layouts/0082-round2.png`).

## Goals

- The first emoji (the repository, task 0081) is the hero, about 80 px of art.
- The second emoji (the session) is a smaller sticker, about 40 px of art,
  overlapping the bottom left of the hero with a white outline.
- The caption is one line across the top of the key, up to 120 px wide.
- The status color is a strip across the bottom of the key.
- No change to the protocol, the firmware, the inbox, or `iconRef` icons.

## Non-goals

- `iconRef` icons (including a single `emoji/...` ref) and text icons keep
  their look.
- Moving or resizing the count pip: the board stamps it in a fixed place.
- A bigger caption limit than 16 runes.

## Approaches considered

### Approach A: hero, sticker, caption, and strip on an opaque tile (chosen)

Layout 1 Overlap: a white tile, the caption on top, the hero, the sticker,
and a 22 px strip of the status color at the bottom. Every picture is cropped
to its art before it is scaled.

- Good, because the hero is about 2.3 times as wide as today's emoji and the
  caption has the whole width of the key.
- Good, because it needs only the host's renderer.
- Bad, because a small sticker carries the session, which Keith's eye must
  learn to read.

### Approach B: two equal emoji over a strip (layout H)

- Good, because both emoji stay the same size, about 58 px of art.
- Bad, because neither emoji is as big as a hero; Keith preferred the
  different sizes.

### Approach C: the caption inside a thicker strip, with the count beside it (layout B)

- Good, because the emoji get the whole top of the key.
- Bad, because the board would have to stamp the count in a new place, a
  firmware and protocol change.

## Decision

Chosen: **Approach A**. Keith picked it from the mock-ups. The cost is that
the status color shows only as a strip, not around the whole picture.

## Design

All in `driver/layers/emoji.go` (`drawIconEmoji`) and `render.go`.

- **Tile.** The whole 128x128 key is opaque white. A strip of the status color
  covers y 106 to 128 (22 px; 14 px in the first version). With no status the strip is black, as the corners were.
- **Crop.** Each picture is cut to the box of its pixels with alpha above
  0x08 before it is scaled, so the size below is the size of the art. The
  longer side of the art takes the size; the shorter side keeps the ratio.
- **With a caption**, centred at y 14, black `gobold`, the largest of 22, 20,
  18, 16, 14 px that fits 120 px, cut with `…`:
  - two emoji: the hero 80 px centred at (62, 64), the sticker 40 px centred
    at (24, 82);
  - one emoji: 80 px centred at (56, 66).
- **With no caption:**
  - two emoji: the hero 96 px centred at (70, 50), the sticker 48 px centred
    at (28, 78);
  - one emoji: 100 px centred at (60, 56).
- **Sticker.** A 3 px white outline (the art's alpha grown by 3 px, with an
  antialiased edge) is drawn under the sticker, so it reads where it covers
  the hero.
- **Pip.** The tile is opaque and the pip sits on white, so an `iconEmoji`
  frame is stored over `ColorNone` and its pip drawn against `ColorNone`, as a
  text icon's is (task 0069). `frameKey` keeps the status color, since the
  strip is part of the picture: one icon on two statuses is still two frames.
- **Top-level keys keep the round.** Only a key in a child layer (level 2 or
  deeper) draws the tile above. A top-level `iconEmoji` key draws task 0073's
  layout as before: the emoji side by side on the white round (48 px each, or
  64 px for one), the caption above them in the narrow top of the round, the
  key color around it, and its frame stored over the key color. `view` and
  `frameKey` carry a `round` flag, set by `desired` for the top layer.
- **Pip disc.** The `ColorNone` disc is blended against white, not black, so
  its edge does not speckle on the white tile. It is the disc of text and
  emoji icons; on a round icon with no status the change is one pixel of edge
  over the black corner. The sheet's size and colors do not change.

Files to change: `driver/layers/emoji.go`, `render.go`, `frames.go`, `pip.go`,
`emoji_test.go`, `emojifont_test.go`, the goldens `testdata/icon-emoji-caption.png`
and `icon-emoji-caption-pip.png`, a new golden `testdata/icon-emoji-alone.png`,
`driver/layers/doc.go`, and `driver/README.md`.

## Definition of done

- [ ] **DoD-1**: `iconEmoji` with a caption draws layout 1 Overlap: the caption
  on top, the hero, the sticker, and the status strip, with and without a
  count. Proof: `go test ./layers -run TestIconEmoji` against
  `testdata/icon-emoji-caption.png` and `icon-emoji-caption-pip.png`.
- [ ] **DoD-2**: with no caption the hero and sticker grow. Proof: the golden
  `testdata/icon-emoji-alone.png` in `TestIconEmoji`.
- [ ] **DoD-3**: the hero's art is at least 76 px wide or tall with a caption
  (today 34 px), and the strip's pixels are the status color. Proof: the
  checks in `TestIconEmojiLayout`.
- [ ] **DoD-4**: one icon on two statuses is still two frames, a count stores
  no frame, and the frame is stored over `ColorNone`. Proof:
  `go test ./layers -run TestIconEmojiFrames`.
- [ ] **DoD-5**: a picture from the emoji font (task 0074) takes the hero's and
  the sticker's place. Proof: `go test ./layers -run TestEmojiFont`.
- [ ] **DoD-6**: `iconRef` icons and text icons are unchanged but for the edge
  of a pip on a key with no status. Proof: `icon-emoji.png` and `icon-text.png`
  are untouched; `icon-round.png`, `pip-sheet.png`, and `pips.png` change only
  in the `ColorNone` disc's edge.
- [ ] **DoD-8**: a top-level `iconEmoji` key draws exactly as before task 0082,
  and keeps the round once it has children, while its children draw the tile.
  Proof: `go test ./layers -run 'TestIconEmoji'`, `TestIconEmojiTopLevel`
  against `icon-emoji-round-caption.png` and `icon-emoji-round-caption-pip.png`,
  which are task 0073's goldens unchanged.
- [ ] **DoD-7**: on the board, Keith can read the caption and tell the hero and
  sticker apart on 5 inbox keys. Proof: Keith's note in this spec.

## Notes

- 2026-10-27: Keith said both the label and the emoji were too small, asked
  for layouts that grow the emoji, and suggested the status as a bottom strip.
  Round 1 (`specs/key-layouts/0082-layouts-1.png`, `-2.png`) had 8 layouts;
  Keith picked H, then asked to combine it with A's hero and sticker. Round 2
  (`0082-round2.png`) had 5; Keith picked 1 Overlap.
- 2026-10-27: after PR #105, Keith asked for a taller strip: 14 px became
  22 px, and the sticker moved up 8 px (10 px with no caption) so it and its outline clear it.
- 2026-10-28: Keith found the child keys good but the top keys looked better
  with the round and logo, and asked that top-level keys keep the old style
  and child keys (and deeper) use the new layout. Added the round flag (DoD-8).
