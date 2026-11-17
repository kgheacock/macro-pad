---
id: "0094"
title: "Draw two caption lines on a child key, with or without an emoji"
status: "complete"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/121"
branch: "0094-two-line-identifier-caption-on-item-keys"
related: ["0069", "0073", "0082", "0086", "0096"]
tags: ["driver", "layers", "rendering", "wire-protocol", "platform"]
---

# 0094 — Draw two caption lines on a child key, with or without an emoji

## Problem

Task 0096 needs a child key with two lines of identifier, such as `PROJ-123` over `#42`. It must still show the status color, and a session emoji when there is one. Today no key does this. `validateIconCaption` in `driver/layers/emoji.go` rejects a newline in a caption. The text icon of `driver/layers/text.go` has two lines but no emoji and no status strip.

## Goals

- A child key draws one or two lines, at most 16 runes each, with 0, 1, or 2 emoji.
- The 22 px status strip stays at y 106 to 127, with or without an emoji.
- A line that is too wide gets a smaller font, then ends in `…`. No line is set below 14 px.
- Today's keys, the one-line caption and the text icon, draw the same pixels as now.

## Non-goals

- Which identifier goes on which line, and when the emoji shows. Task 0096 decides that.
- A color for each line. Both lines are black.
- Two lines on a top-level key. Its round has no room, and the message is rejected.
- Count badges. Task 0091 draws them over the tile, and the tile does not move for them.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Allow a newline in `iconText` over the emoji

`validateIconCaption` accepts two lines. `drawIconEmoji` draws both across the top and takes a smaller hero from the `emojiLayout` table. No new field.

- Good, because the change is small. The protocol, the document, and `frameKey` stay as they are.
- Good, because a key with two lines looks like today's emoji keys, only with a smaller hero.
- Bad, because a key with no emoji and two lines is the text icon of task 0069. It has no strip, so the status color is lost on the key that is most common in task 0096.
- Bad, because the same field means two layouts, chosen by whether an emoji is present.

### Approach B — Grow the text icon

`drawIconText` takes an optional emoji sticker and a status strip. Lines stay large, at 28 px, and the sticker sits at the lower left, beside the count disc.

- Good, because the text fit code of `text.go` is already tested for two lines.
- Good, because the lines are bigger than in A, 28 px against 22 px.
- Bad, because the strip puts the key color into the frame. Today two keys with one text share one slot, whatever their colors (`TestIconTextFrames`). Each color would take its own slot, up to five.
- Bad, because every existing two-line text icon changes. The golden `icon-text.png` and the inbox keys change with them.

### Approach C — A new field `iconLines` and an id tile

`iconLines` is a list of one or two strings. It excludes `iconText` and `iconRef`, and works with `iconEmoji`. A new tile layout draws it. `iconText` keeps its rules.

- Good, because no existing key changes, and there is one rule for each field.
- Good, because the strip is part of the new tile, so the status color shows with no emoji.
- Bad, because the field goes through `Node`, `Update`, `view`, `frameKey`, the JSON of `layers.json`, and `setLayerKey`.
- Bad, because with an emoji the hero is 46 px, and the lines are at most 22 px. A one-line caption has an 80 px hero.

## Decision

Chosen: **Approach C — a new field `iconLines` and an id tile**.

The status strip must show on a key with no emoji, and existing keys must not change. Only C does both. The cost accepted: one new field in six places, and a hero of 46 px when lines are present.

## Design

Protocol rule: `iconLines` is `[]string`, one or two entries. Each entry has 1 to 16 runes (`MaxIconCaptionRunes`), is valid UTF-8, and holds no control character. It excludes `iconText` and `iconRef`. `iconEmoji` may have 0, 1, or 2 refs. A top-level key with `iconLines` is rejected in `Doc.Validate`. `validateIconCaption` still rejects a newline.

Layout, on the white tile of `drawIconEmoji`, with the strip at y 106 to 127:

- With no emoji: the lines share one size, the largest of 28, 24, 22, 20, 18, 16, 14 px that fits 120 px. They are centered in y 2 to 104. For `PROJ-123` and `#42`, that is 24 px.
- With one emoji: the lines use 22, 20, 18, 16, or 14 px in y 2 to 56. The hero is 46 px, centered at (64, 82).
- With two emoji: the hero is 44 px at (76, 82). The sticker is 28 px at (28, 90), with the 3 px outline of `drawOutline`.
- A line that does not fit at 14 px is cut by `fitWidth` and ends in `…`. A rune that Go Bold lacks draws as `?` (`replaceMissing`).

`frameKey` gets a field `lines`. `storeColor` returns `ColorNone` for it, as for an emoji tile, so a count disc sits on white. `renderIcon` gets a `lines` argument. The shared fit code of `text.go` becomes one function that both icons call.

Files to change:

- `driver/layers/doc.go` — `Node.IconLines`, `validate`, `hasContent`
- `driver/layers/controller.go` — `Update`, `view`, `nodeView`, `applyUpdate`
- `driver/layers/emoji.go`, `text.go`, `render.go`, `frames.go` — layout, shared fit, `frameKey`
- `driver/plugin/protocol.go` — `SetLayerKeyPayload.IconLines`
- `driver/README.md`, `inbox/test/fixtures/protocol.json`, `inbox/test/protocol.test.js`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — A key with `iconLines` `["PROJ-123", "#42"]` and no emoji draws an opaque tile. Its strip is the status color at y 106 to 127, and both lines are inked at 24 px. **Proof:** `cd driver && go test ./layers -run 'TestIconLines$'` passes, with `driver/layers/testdata/icon-lines.png`.
- [x] **DoD-2** — With one or two emoji, the lines lie in y 2 to 56. The emoji art lies in y 57 to 105. **Proof:** `cd driver && go test ./layers -run TestIconLinesLayout` passes.
- [x] **DoD-3** — A line of 16 runes fits 120 px, or ends in `…` at 14 px. No line uses a size below 14 px. Both lines share one size with no emoji. **Proof:** `cd driver && go test ./layers -run TestIconLinesFit` passes.
- [x] **DoD-4** — These messages are rejected and change nothing: three lines, an empty line, a line of 17 runes, or a newline in a line. So are `iconLines` with `iconText`, with `iconRef`, or on a top-level key. **Proof:** `cd driver && go test ./layers -run TestIconLinesInvalid` passes.
- [x] **DoD-5** — A one-line caption over emoji and a text icon draw the same pixels as on `main`. `validateIconCaption` still rejects a newline. **Proof:** `cd driver && go test ./layers -run 'TestIconEmoji|TestIconText'` passes, with no change to the files in `driver/layers/testdata`.
- [x] **DoD-6** — Two keys with the same lines, emoji, and color share one slot. A different line or color takes another slot. A count change stores no frame. **Proof:** `cd driver && go test ./layers -run TestIconLinesFrames` passes.
- [x] **DoD-7** — `iconLines` survives a save and a load of `layers.json`, and an old file with no `iconLines` still loads at version 1. **Proof:** `cd driver && go test ./layers -run TestIconLinesPersist` passes.
- [x] **DoD-8** — A `setLayerKey` with `iconLines` reaches the controller and draws a glyph. A bad one is logged and dropped. **Proof:** `cd driver && go test ./plugin -run TestSetLayerKeyIconLines` and `cd inbox && node --test test/protocol.test.js` pass.
- [x] **DoD-9** — `driver/README.md` records the field, the layouts, and the sizes. **Proof:** `driver/README.md`, section "Protocol", bullet `iconLines`.
- [x] **DoD-10** — Both suites pass, and the PR in the `pr` field links to this spec. **Proof:** `cd driver && go test ./...`, `cd inbox && node --test`, and the PR body.

## Risks

- A 46 px hero is small beside the 80 px hero of a one-line key → task 0096 shows an emoji only with no identifier, so a key with both is rare.
- Two lines at 14 px are small on the panel → the golden image in DoD-1 shows it. The 22 px size holds `PROJ-123` and shorter.
- A badge row from task 0091 covers y 89 to 123 → the lines end above y 86, so a badge never covers a letter.

## Open questions

- [ ] Should the lines take the yellow and green ink of the count badges? — The owner, after the first golden image.

## Notes

- Measured with Go Bold at 72 DPI: a line is 26 px high at 22 px. `PROJ-1234` is 121 px wide at 22 px and 110 px at 20 px. Two lines at 22 px take 54 px.
- `text.go` already puts two lines in black and gray on a tile, with no emoji and no strip. Approach B reuses it. Approach C reuses its fit code only.
