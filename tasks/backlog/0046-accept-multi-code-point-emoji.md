---
id: "0046"
title: "Accept one emoji of several code points, such as ❤️, in the key state page and the CLI"
status: "backlog"
created: "2026-10-06"
updated: "2026-10-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0034", "0038", "0041"]
tags: ["driver", "web", "emoji"]
---

# 0046 — Accept one emoji of several code points, such as ❤️, in the key state page and the CLI

## Problem

The "Emoji char" field of `driver/plugin/web/keystate.html` rejects ❤️. This emoji is U+2764 plus the
variation selector U+FE0F: two code points, two UTF-16 units. `singleEmojiCodepoint` accepts only
`Array.from(value).length === 1`. `macrodriver emoji --char` rejects it too, in `validateSingleCodepoint`.

Many common emoji have more than one code point: ❤️ ☺️ ✌️ (variation selector), 👍🏽 (skin tone),
1️⃣ (keycap), 🇺🇸 (flag), and 👨‍👩‍👧 (joined family). A single code point outside the BMP, such as 😀,
already works. It uses two UTF-16 units, but it is one code point.

## Goals

- The page accepts one emoji of any length: ❤️, 👍🏽, 1️⃣, 🇺🇸, and 👨‍👩‍👧.
- The page still rejects a letter, an empty field, and two emoji.
- `macrodriver emoji` accepts one code point followed by U+FE0F, and draws ❤️ like ❤.
- `macrodriver emoji` rejects a sequence that its renderer cannot draw, with a message that says why.

## Non-goals

- A wider Emoji ID on the wire. The page and the CLI send a custom glyph image, not an ID.
- A plugin API call that takes an emoji string. Task 0038 covers that.
- Shaping sequences in Pillow, which needs `libraqm`. Approach C covers it.

## Approaches considered

Three approaches follow. Each one accepts a different set of emoji.

### Approach A — Allow a variation selector only

The page and the CLI accept one code point, with an optional U+FE0F after it.

- Good, because it is the smallest change: one condition in JavaScript and one in Go.
- Good, because the renderer draws ❤️ correctly today. The test on 2026-10-06 matched ❤.
- Bad, because 👍🏽, 1️⃣, 🇺🇸, and 👨‍👩‍👧 stay rejected, although the browser can draw all of them.
- Bad, because the page still has two different rules for what counts as one emoji.

### Approach B — Accept one emoji sequence in the page, a variation selector in the CLI

The page tests the field with `/^(?:\p{RGI_Emoji}|\p{Extended_Pictographic})$/v`. One regular expression
replaces the code point count and the pictograph test. The CLI follows Approach A.

- Good, because the page accepts every standard emoji, including flags, keycaps, and joined sequences.
  The test in Node 22 accepted all of them and rejected `a` and two emoji.
- Good, because the browser canvas shapes sequences correctly, so the page needs no new renderer.
- Bad, because the `v` flag needs Chrome 112, Safari 17, or Firefox 116. An older browser throws a
  `SyntaxError`, so the code needs a fallback.
- Bad, because the page and the CLI accept different sets, until Approach C is built.

### Approach C — Accept one emoji sequence in both, and shape it with `libraqm`

The CLI uses a grapheme check (Go package `rivo/uniseg`) and Pillow with the `raqm` layout engine.

- Good, because the page and the CLI accept and draw the same set.
- Good, because it also fixes the CLI drawing of skin tones and joined sequences.
- Bad, because it needs a system library (`brew install libraqm`) and a Pillow build with `raqm`.
  This machine has neither: `features.check("raqm")` is `False`.
- Bad, because it adds a Go dependency, and nobody has shown that `raqm` shapes Apple Color Emoji correctly.

## Decision

Chosen: **Approach B — one emoji sequence in the page, a variation selector in the CLI**.

The report is about the page, and the browser already draws every sequence correctly. The CLI renderer draws
only the variation-selector case: the 2026-10-06 test showed a skin tone as two images, a flag and a keycap
as blank, and a joined family clipped. The decision accepts that the CLI refuses those sequences until Approach C.

## Design

New `driver/plugin/web/emoji.js` defines `isSingleEmoji(value)` and exports it for Node. It builds the
`v`-flag regular expression in a `try` block. If the browser throws, it falls back to one code point
(from `Array.from`) with an optional U+FE0F, and the `\p{Extended_Pictographic}` test.
`keystate.html` loads it with `<script src="emoji.js">`, and `singleEmojiCodepoint` calls it.
The input `maxlength` rises from 4 to 32 UTF-16 units, because a joined family takes 8.

In `driver/cmd/macrodriver/emoji.go`, `validateSingleCodepoint` becomes `validateSingleEmoji`. It accepts one rune,
or two runes when the second is U+FE0F. The error text names skin tones, flags, keycaps, and joined sequences.

Files to change:

- `driver/plugin/web/emoji.js` — new: `isSingleEmoji`
- `driver/plugin/web/emoji.test.js` — new: cases for `node --test`
- `driver/plugin/web/keystate.html` — load `emoji.js`, call it, raise `maxlength`
- `driver/cmd/macrodriver/emoji.go` — `validateSingleEmoji`, error text
- `driver/cmd/macrodriver/emoji_test.go` — new cases
- `driver/README.md` — which sequences the page and the CLI accept

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — `isSingleEmoji` accepts ❤️, ❤, 😀, 👍🏽, 1️⃣, 🇺🇸, and 👨‍👩‍👧.
  **Proof:** `node --test driver/plugin/web/emoji.test.js`
- [ ] **DoD-2** — `isSingleEmoji` rejects `a`, an empty string, a space, 😀😀, and ❤️❤️.
  **Proof:** `node --test driver/plugin/web/emoji.test.js`
- [ ] **DoD-3** — When the `v` flag is not available, `isSingleEmoji` still accepts ❤️ and 😀 and rejects `a`.
  **Proof:** the fallback case in `node --test driver/plugin/web/emoji.test.js`
- [ ] **DoD-4** — In the page, typing ❤️ or 👨‍👩‍👧 and clicking "Set emoji" sends a `setCustomGlyph` message. The log
  shows no "is not a single emoji character" line.
  **Proof:** run `go run ./driver/cmd/macropadd --emulate`, open `keystate.html`, read the page log
- [ ] **DoD-5** — `validateSingleEmoji` accepts ❤, ❤️, and 😀. It rejects an empty string, `ab`, and 👍🏽, and the
  error for 👍🏽 names skin tones.
  **Proof:** `cd driver && go test ./cmd/macrodriver -run TestValidateSingleEmoji`
- [ ] **DoD-6** — `macrodriver emoji --emulate --char ❤️` exits with code 0 and the emulator holds one custom glyph.
  **Proof:** `cd driver && go test ./cmd/macrodriver -run TestRunEmojiVariationSelector`
- [ ] **DoD-7** — `driver/README.md` lists the sequences that the page accepts and the ones that the CLI accepts.
  **Proof:** `driver/README.md`, section on `macrodriver emoji`
- [ ] **DoD-8** — The PR in the `pr` field links to this spec.
  **Proof:** the PR body

## Risks

- A browser without the `v` flag rejects sequences → the fallback keeps ❤️ working, and DoD-3 tests it.
- A joined sequence may render wider than the canvas → DoD-4 uses 👨‍👩‍👧 and a person checks the image.
- A user pastes a sequence into the CLI and gets a refusal → the message says that the page accepts it.

## Open questions

- [ ] Is a CLI that refuses skin tones and flags acceptable, or is `libraqm` (Approach C) worth installing? — owner

## Notes

- Test, 2026-10-06, `tools/render_emoji.py`, Pillow without `raqm`: ❤ and ❤️ drew the same heart. 👍🏽 drew a thumb
  and a brown square. 1️⃣ and 🇺🇸 drew nothing. 👨‍👩‍👧 drew three clipped glyphs. 😀 drew correctly.
- Test, Node 22: `/^\p{RGI_Emoji}$/v` accepted ❤️, 👍🏽, 1️⃣, and 🇺🇸. It rejected `a`, 😀😀, and a bare ❤.
  The extra `\p{Extended_Pictographic}` alternative keeps a bare ❤ valid, as the page does today.
