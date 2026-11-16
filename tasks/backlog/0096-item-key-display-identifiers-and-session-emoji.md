---
id: "0096"
title: "An item key shows the yellow id, the green id, and a session emoji only when both are absent"
status: "backlog"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0073", "0081", "0082", "0085", "0092", "0093", "0094", "0095"]
tags: ["inbox", "repos", "emoji", "layers", "ui"]
---

# 0096 — An item key shows the yellow id, the green id, and a session emoji only when both are absent

## Problem

The repo layer (task 0095) holds items that mix three sources. The only child key the inbox draws today is `channelIcon` in `inbox/src/render.js`: the last folder segment as one caption over a repo emoji and a session emoji. It shows no ticket id or PR number. A merged item needs both ids on one key, and a session without a ticket needs a face that Keith knows.

## Goals

- Line 1 is the yellow id, for example `PROJ-123`. Line 2 is the green id, for example `#42`, when both exist. With one id, the key shows that id alone.
- A session emoji shows only when the yellow and the green id are both null. Its caption is the 8-hex session id.
- The strip colour shows the worst state of the item: CI for a PR, the agent for a session.
- A long id keeps its end, because the number tells ids apart.

## Non-goals

- The caption layout of the driver. Task 0094 adds the `iconLines` field, which carries two lines on a child key.
- The merge and the press (task 0095), the repo key (0092), and the yellow provider (0093).
- A new emoji asset or a new status colour.

## Approaches considered

### Approach A — Two-line caption over the repo emoji

The id lines go in `iconLines` (task 0094). The hero emoji is the repo emoji, so the status strip of task 0082 stays. A session-only key swaps the hero for the session emoji.

- Good, because it reuses the tile, the hero, and the strip of `driver/layers/emoji.go`, so the strip carries the status.
- Good, because it adds no asset, and the repo emoji matches the repo key (task 0092).
- Bad, because every key of one layer shows the same hero, which tells Keith nothing.
- Bad, because it needs `iconLines` of task 0094, and two lines shrink the hero to 46 px.

### Approach B — A text tile with the ids only

Send `iconText` of two lines and no `iconEmoji`. `drawIconText` in `driver/layers/text.go` already takes two lines (`MaxIconTextLines`), so no driver change is needed.

- Good, because the ids get the whole tile, in a larger font.
- Good, because it works on today's driver, so it ships first.
- Bad, because a text tile is plain white. The key colour cannot show, so a failing PR looks like a passing one.
- Bad, because a session-only key still needs an emoji tile. Two tile styles share one layer.

### Approach C — A kind glyph as the hero

The hero is a fixed emoji per kind: a ticket for yellow, a merge glyph for green. `assets/emoji/extra.txt` lists such emoji, and the apps plugin uses it (task 0085).

- Good, because the hero says what the key is, so Keith reads the kind before the id.
- Good, because the strip still carries the status.
- Bad, because two PNGs must be drawn by `tools/emoji/build_emoji_assets.mjs` and kept in the set.
- Bad, because a merged item has two kinds and one hero, so a rule must pick one.

## Decision

Chosen: **Approach A — two-line caption over the repo emoji**, sent as `iconLines`.

The status strip is the one signal that Keith cannot lose, and only the emoji tile has it. The task accepts a repeated hero on every key of a layer, and a dependency on task 0094.

## Design

`itemKey(item, { repo, set })` in a new `inbox/src/plugins/repos-item-key.js` returns `{ status, count, iconLines, iconEmoji }`. The repo-layer renderer of task 0095 spreads it into `setLayerKey`, with `id` and `path`.

**Caption.**

- `lines` is `[yellow, green]` with the nulls left out. If `lines` is empty and `session` is set, it is `[session]`.
- An item with no yellow, green, or session is a bug, and `itemKey` throws.
- `iconLines` is `lines`, one or two strings. The key never sets `iconText` or a newline: `validateIconCaption` rejects it (task 0094). Only `captionOf` knows this field name.
- Each line is cut to `MAX_CAPTION` (16) from `inbox/src/render.js`. `clipTail` keeps 9 head runes, `…`, and 6 tail runes, so `PLATFORM-INFRA-12345` ends in `12345`.

**Emoji.**

- With a yellow or a green id, `iconEmoji` is `[emojiFor(componentInput(repo), set).iconRef]`, as in `inbox/src/emoji.js`. It never holds a session emoji, also when `item.session` is set.
- With neither id, `iconEmoji` is `[emojiFor(sessionInput(session), set).iconRef]`, the `session:<8hex>` input.

**Status.** `count` is always 0.

| Source | Value | `status` |
|---|---|---|
| `ci` | failing, pending, passing, none | `ERROR`, `RUNNING`, `DONE`, `''` |
| `agent` | waiting, running, done | `WARN`, `RUNNING`, `DONE` |
| yellow only | no machine state | `''` |

The strip takes the worst of `ci` and `agent`, in the order `ERROR`, `WARN`, `RUNNING`, `DONE`, `''`. So a PR that passes, with a session that waits, shows `WARN`.

Files to change:

- `inbox/src/plugins/repos-item-key.js` — new: `itemKey`, `clipTail`, `STATUS`
- `inbox/src/plugins/repos-items.js` (task 0095) — spread `itemKey` into each item node
- `inbox/src/render.js` — pass `iconLines` through
- `inbox/test/repos-item-key.test.js` — new
- `inbox/README.md` — table "Item keys"

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — An item with yellow `PROJ-123` and green `#42` gives `iconLines` `['PROJ-123', '#42']` and no `iconText`. **Proof:** `cd inbox && node --test --test-name-pattern=TestItemKeyTwoLines test/repos-item-key.test.js`
- [ ] **DoD-2** — An item with one id gives `iconLines` of one entry. An item with no id and no session throws. **Proof:** `node --test --test-name-pattern=TestItemKeyOneLine test/repos-item-key.test.js`
- [ ] **DoD-3** — With both ids null, `iconEmoji` is the one emoji of `session:<8hex>`, and `iconLines` is `[<8-hex id>]`. **Proof:** `node --test --test-name-pattern=TestItemKeySessionOnly test/repos-item-key.test.js`
- [ ] **DoD-4** — With a yellow or a green id, `iconEmoji` is only the repo emoji. It never holds the session emoji, also when `session` is set. **Proof:** `node --test --test-name-pattern=TestItemKeyNoSessionEmoji test/repos-item-key.test.js`
- [ ] **DoD-5** — The status table above holds for each `ci` and `agent` value, and the worst one wins. **Proof:** `node --test --test-name-pattern=TestItemKeyStatus test/repos-item-key.test.js`
- [ ] **DoD-6** — A 20-rune id gives a line of 16 runes with `…` that ends in the last 5 runes. **Proof:** `node --test --test-name-pattern=TestItemKeyClip test/repos-item-key.test.js`
- [ ] **DoD-7** — For 30 generated items, each key has at most 2 `iconLines` entries of at most 16 runes, no control character, one emoji ref that is a file under `assets/emoji/`, `count` 0, and no `iconRef` or `iconText`. **Proof:** `node --test --test-name-pattern=TestItemKeyWire test/repos-item-key.test.js`
- [ ] **DoD-8** — The repo layer sends the `itemKey` fields for each item node. The existing suite still passes. **Proof:** `node --test --test-name-pattern=TestRepoLayerItemKeys test/repos-item-key.test.js` and `node --test`
- [ ] **DoD-9** — On the board, a one-line key, a two-line key, and a session-only key draw as above, and a failing PR shows a red strip. **Proof:** Keith's check, in Notes
- [ ] **DoD-10** — `inbox/README.md` "Item keys" holds the caption rule, the emoji rule, and the status table. The PR body links to this spec. **Proof:** `inbox/README.md`; the PR in the `pr` field

## Risks

- Task 0094 may lower the line width or the line count → `captionOf` and `MAX_CAPTION` are the only places to change.
- `inbox/src/render.js` must pass `iconLines` through from a plugin's `icon()`. Task 0094 does not change that file → this task adds the pass-through and a test.
- Two lines may be too small to read at 128 pixels → the board check of DoD-9 decides, and the clip limit can drop.
- Each distinct caption, emoji, and colour takes one frame slot (task 0061) → one layer holds at most 20 keys, under the 52 slots.

## Open questions

- [ ] Should a yellow-only item show `''`, not a colour? The default is `''`. — kgheacock

## Notes

Board check: not done yet.
