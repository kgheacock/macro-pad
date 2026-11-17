---
id: "0091"
title: "Show a red, a green, and a yellow count on one key, stamped by the board"
status: "ongoing"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/120"
branch: "0091-three-colored-count-badges-on-a-key"
related: ["0062", "0061", "0063", "0082", "0092"]
tags: ["driver", "firmware", "wire-protocol", "layers", "rendering", "platform"]
---

# 0091 — Show a red, a green, and a yellow count on one key, stamped by the board

## Problem

A key has one `count`. `driver/layers/pip.go` draws it as one red disc, 44 px wide, stamped by the firmware. The repo keys of task 0092 need three counts: issues in red, pull requests in green, and a client count in yellow. No wire field, sprite, or stamp exists for the second and third count.

## Goals

- A key shows up to three discs, red, green, and yellow, side by side along the bottom edge. A count of 0 draws no disc.
- A count change sends no image and takes no frame slot. One picture on one color stays one of the 52 slots.
- Digits stay legible: at least 13 px high, and `99+` for 100 or more.
- `setLayerKey` carries the three counts. A message with only `count` draws as before, as a red count.

## Non-goals

- What each count means. Task 0092 wires issues, PRs, and the client count to the repo key.
- The size of the hero emoji or the status strip. Task 0092 owns the repo key layout.
- Deleting message types 13 and 14 from the firmware. The driver stops sending them. A later task removes them.
- Badges that move or hide by count. The places are fixed.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — The host draws the discs into the frame

`renderIcon` draws three discs into the 128 by 128 image. The three counts join `frameKey` in `driver/layers/frames.go`. The board and the wire stay as they are.

- Good, because only `driver/layers` changes. No firmware flash and no wire type.
- Good, because the host blends each disc edge against the real pixels under it.
- Bad, because each combination of counts is a new frame. A count change costs one Store frame of 94 to 109 ms (task 0062).
- Bad, because `frameTable.claim` leaves the old frame in its slot until it is the least recently used. Fifty-two count changes fill all 52 slots with dead frames, and the preload then stores nothing.

### Approach B — The board stamps three discs from a sprite sheet

A new sheet holds discs and digits for the three fills. A new Show message names a slot and three counts for each key. The board copies the slot into one of the key's two pip buffers and stamps up to three discs on the copy. This extends task 0062.

- Good, because `frameKey` keeps no count. A change of any count is one Show of at most 24 bytes, with no Store frame.
- Good, because the look stays in host code. A new size or color changes `driver/layers/pip.go`, not the flash.
- Bad, because it adds two message types, a bigger sheet (76,800 bytes, against 32,768 today), and more stamp work on the board.
- Bad, because the stamp time grows. Each key needs up to 3 discs and 6 digit blits where task 0062 needed 1 disc and 2 digit blits.

### Approach C — The host uploads a ready-made badge row for each key

The host draws the whole row of badges for a key as one 112 by 34 image. It sends the image to one of six overlay buffers on the board. A Show names a slot, and the board blits the overlay on a copy.

- Good, because the board needs no digit sprites. Any font, size, or color works with no flash.
- Good, because a count change uploads about 8 KB before compression, not a 32 KB frame, and takes no slot.
- Bad, because the host must keep an upload record for each key and resend all six after a reconnect.
- Bad, because the Show may arrive before its overlay. That needs a sequence number and a miss reply, as Show miss does for slots.

## Decision

Chosen: **Approach B — the board stamps three discs from a sprite sheet**.

Task 0062 already chose this design for one count. It is the only approach that keeps a count change off the image path and out of the 52 slots, with the least new protocol. The cost accepted: a 76,800-byte sheet in RAM, two message types, and about 3 ms more stamp time for each key.

## Design

`setLayerKey` fields, in `driver/plugin/protocol.go`, `driver/layers/doc.go` (`Node`), and `driver/layers/controller.go` (`Update`): `count` stays the red count. New: `countGreen` and `countYellow`. Each is an integer of 0 or more, and 0 is the default. `layers.json` stays at version 1, and an old file loads as red counts.

Layout on the panel: discs of 34 px with a 2 px white ring, 3 px apart, 5 px above the bottom edge. All three sit at y 89 to 123. Red is at x 89 to 123, green at x 52 to 86, and yellow at x 15 to 49. A lone red count stays in today's corner. Digits are 11 by 16 px (20 px Go Bold), and `99+` is 8 by 12 px. Red uses white digits. Green and yellow use black digits.

Wire, in `docs/wire-protocol.md`:

- **Store badge sheet** (type 15): the number of background colors (1 to 5), the colors, and a zlib stream. It inflates to 76,800 bytes, a 256 by 150 image.
  The discs take 7 columns by 3 rows. Three digit sets follow at y 102, 118, and 134.
- **Show frames with badges** (type 16): four bytes for each key: slot or 255, red, green, and yellow. A byte of 100 or more draws `99+`.

Files to change:

- `driver/layers/pip.go` — `BuildBadgeSheet`, `DrawBadges` (replaces `DrawPip`), badge rectangles
- `driver/layers/render.go`, `controller.go`, `frames.go`, `doc.go` — three counts in `view`, `Node`, `Update`, `FrameOut`
- `driver/plugin/protocol.go`, `server.go` — the fields, and `withBadges` for clients
- `driver/transport/frames.go`, `device.go`, `emulator.go`, `reconnect.go`, `fanout.go`, `transport.go` — types 15 and 16
- `firmware/wire.py`, `app.py`, `frame_cache.py`, `display_render.py` — decode, stamp
- `driver/cmd/blinksend/main.go`, `tools/blink_trace.py` — a `badges` scenario
- `docs/wire-protocol.md`, `driver/README.md`, `firmware/README.md`, `inbox/test/fixtures/protocol.json`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — The host builds a badge sheet that inflates to exactly 76,800 bytes. Every digit cell lies inside the red disc of its fill. **Proof:** `cd driver && go test ./layers -run 'TestBadgeSheet|TestBadgeDigitsFitDisc'` passes, and `driver/layers/testdata/badge-sheet.png` shows every sprite.
- [x] **DoD-2** — Counts of 0 draw nothing. Each count draws only in its own rectangle, red at x 89 to 123. A count of 100 draws `99+`. **Proof:** `cd driver && go test ./layers -run TestBadges` passes, with `driver/layers/testdata/icon-badges.png`.
- [x] **DoD-3** — Changing any of the three counts from 1 to 99 on one picture sends one Show with badges. It sends no Store frame and uses one slot. **Proof:** `cd driver && go test ./layers -run TestBadgesNoStore` passes.
- [x] **DoD-4** — Types 15 and 16 round-trip in Go and Python. After a reconnect, the host sends the sheet before the first type 16. **Proof:** `cd driver && go test ./transport -run 'Badge|Reconnect'` and `.venv/bin/pytest test/test_wire.py -k badge` pass.
- [x] **DoD-5** — On the board, a Show with badges changes the copy only inside the badge rectangles. The slot bytes stay the same. Before the sheet arrives, the key shows the slot with no badge. **Proof:** `.venv/bin/pytest test/test_app.py -k badge` passes.
- [x] **DoD-6** — `setLayerKey` with only `count: 3` draws one red badge, the same as `countGreen: 0, countYellow: 0`. Negative counts drop the message and change nothing. **Proof:** `cd driver && go test ./layers ./plugin -run 'TestBadgeCounts|TestSetLayerKeyCounts'` and `cd inbox && node --test test/protocol.test.js` pass.
- [x] **DoD-7** — `docs/wire-protocol.md` defines types 15 and 16, the sheet layout, and the three fields. `driver/README.md` replaces its "Count pip" text. **Proof:** `docs/wire-protocol.md`, sections "Store badge sheet" and "Show frames with badges".
- [x] **DoD-8** — On the board, a Show of six keys with three badges each takes at most 230 ms in three runs. The span runs from `SHOW_WITH_PIPS_STARTED` to the last `REFRESH_DONE`. **Proof:** the `six keys in` line of `make blink-trace SCENARIO=badges`.
  - 2026-11-17, `make blink-trace SCENARIO=badges`, three runs: `six keys in` 167.9, 168.0 and 168.8 ms (limit 230); `push span` 34 ms.
- [x] **DoD-9** — At least 2 MB of heap stay free with the sheet loaded. **Proof:** the figure in `firmware/README.md` from `make slot-probe`.
  - 2026-11-17, `make slot-probe`: 5,935,088 bytes free after the pool (limit 2,097,152). The probe leaves out the sheet (76,800 bytes), so at least 5.8 MB stay free with it. The figure is in `firmware/README.md`.
- [ ] **DoD-10** — The owner sees three discs with legible digits on a white tile and on a key of each status color. **Proof:** `cd driver && go run ./cmd/blinksend --scenario=badges`, then the owner's note in this spec.
  - Not confirmed: it needs the board. The owner runs `cd driver && go run ./cmd/blinksend --vendor-id=... --product-id=... --scenario=badges --trace-file=/dev/null` and notes the result here. The scenario shows five status colors and a white tile, three discs each.
- [x] **DoD-11** — Both suites pass, and the PR in the `pr` field links to this spec. **Proof:** `cd driver && go test ./...`, `.venv/bin/pytest test/ -q`, `cd inbox && node --test`, and the PR body.

## Risks

- Three discs cover most of the 22 px status strip of a child key → task 0092 may drop the strip or lift the row. The row is one constant in `pip.go`.
- The 230 ms limit is an estimate. Task 0062 measured 15.4 ms for each stamp, and the extra blits add about 3 ms → the owner resets the limit after the first run.
- The sheet adds 45 KB to the heap. The garbage collector costs about 1 ms for each 32 KB (task 0063) → DoD-9 measures the free heap.
- The fixed places leave a gap when a count is 0. This is on purpose, so a color stays in one place.

## Open questions

- [ ] Are 34 px and 13 px digits legible on the panel? — The owner, in DoD-10.

## Notes

- Measured in task 0062: a Store frame takes 94 to 109 ms, and one stamp 15.4 ms. `frameTable` in `driver/layers/frames.go` has `transport.SlotCount` (52) slots, because 12 of the 64 buffers are pip buffers.
- Implementation (task 0091): the driver no longer has `StorePipSheet` or `ShowFramesWithPips` in `transport.Transport`; types 13 and 14 stay in the firmware and in the wire doc as retired. `transport.Badges` (`Red`, `Green`, `Yellow` as bytes) is the three counts, and `layers.Badges` is an alias of it. The fields for task 0092 are `count` (red), `countGreen`, and `countYellow` on `setLayerKey` (`plugin.SetLayerKeyPayload`), `layers.Update`, and `layers.Node`.
- Implementation: the sheet has a disc for each of 3 fills and each of 5 key colors (the status colors), in the first 5 of the 7 columns that fit 256 px. The fills are red `#E02020`, green `#2ECC40`, and yellow `#FFD700`. A Go sheet and `display_render.stamp_badges` gave the same pixels on the same frame in a one-off comparison. The firmware allocates the 256 by 150 sheet at start and the old 256 by 64 pip sheet only if a type 13 message arrives, so the heap grows by 45 KB.
- A strip of colored cells in place of discs is a position, not a mechanism. It would also hide the status color, so it is not an approach.
