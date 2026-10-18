---
id: "0062"
title: "Draw the count pip on the board, and put a white round behind each icon"
status: "ongoing"
created: "2026-10-17"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0062-red-circle-count-pip-with-a-real-font"
related: ["0057", "0059", "0061", "0063", "0041"]
tags: ["driver", "firmware", "wire-protocol", "layers", "rendering", "ui"]
---

# 0062 — Draw the count pip on the board, and put a white round behind each icon

## Problem

The count pip is a dark box with a number from a 3 by 5 pixel font, scaled up five times (`drawBadge` in `driver/layers/render.go`). The edges are blocky and the digits are hard to read. The count is also part of the glyph. Since task 0061, each count of one icon on one color takes its own slot in the board's 64-slot cache, and each new count costs a Store frame of 94 to 109 ms. A key with a changing count evicts the clean frames that layer switches need. Most icons are black and sit straight on the key color, so they have little contrast on blue or red.

## Goals

- A key with a count above 0 shows a red circle with a white number in the bottom-right corner. The edges are smooth and the number is legible on the 128 by 128 panel.
- A count change sends no image. One icon on one color uses one slot, whatever the count.
- The pip shows on a key of any status color, including red. A count of 0 shows no pip, and a count above 99 shows `99+`.
- Each icon, the shift icon included, sits on an opaque white round, so it has contrast on any key color. The shift icon becomes black, like the others, so it shows on the round.

## Non-goals

- A round behind a count-only glyph, which has no icon.
- A pip on a key that blinks. Blinking keys are not cached (tasks 0044 and 0048), so they keep a host-drawn pip.
- A pip on the shift key, or in any other position.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Draw the pip into the glyph on the host

`renderGlyph` draws an antialiased circle and the number with `golang.org/x/image` (Go Bold). The frame key keeps the count. Nothing changes on the board.

- Good, because only `driver/layers` changes. There is no wire change and no flash of the board.
- Good, because the host blends the antialiased edge against the real pixels under it.
- Bad, because each count is a new frame. Counts 1 to 99 of one icon and color could take 99 of the 64 slots.
- Bad, because each count change costs a Store frame (94 to 109 ms), and the user waits for it.

### Approach B — The board stamps host-made sprites on a copy of the cached frame

The host renders a sheet of pip sprites once and sends it. A new Show message names a slot and a count for each key. The board copies the slot into a per-key buffer, blits the sprites on it, and pushes the buffer.

- Good, because the cache holds clean frames only. One icon on one color is one slot, and a count change is one Show of about 6 bytes.
- Good, because the look stays in host code. The firmware holds no font, and a new size or color needs no flash.
- Bad, because it adds two frame types, 12 pool slots as pip buffers, and about 30 KB of sprites in RAM.
- Bad, because the board has no per-pixel alpha. The sprite edge is blended against one background, so it is only exact over that background.

### Approach C — The board renders the pip itself

The firmware draws the circle and the digits with a font file in flash, over a copy of the cached frame.

- Good, because the host sends only a count, and any font size or count renders.
- Good, because it needs no sprite sheet and no sheet message.
- Bad, because the board needs a font file, a draw loop, and a text library, with no `displayio` in use today.
- Bad, because circles on the board have no antialiasing, which is the defect that this task exists to fix.

## Decision

Chosen: **Approach B — the board stamps host-made sprites on a copy of the cached frame**.

The goal is a count change with no image on the wire and no extra slot. Only B and C do that, and C cannot draw a smooth edge. The cost accepted: two new frame types, 12 of the 64 slots, and an edge that is exact only against the key color.

## Design

Messages, after type 12 (the board skips a type it does not know, so update the firmware before the driver):

- **Store pip sheet** (type 13, host to device): the number of discs, their key colors, and one zlib stream that inflates to exactly 32,768 bytes, a 256 by 64 image. The board inflates it on core 0, since it arrives once. The sheet holds one 44 by 44 disc for each of the five status colors, with the 2 pixel white ring and the red `#E02020` fill. It holds the digits `0` to `9` and `+` at 22 pixels, and `9` and `+` at 16 pixels for `99+`. The host draws it at start-up with `golang.org/x/image` v0.24.0 or older. Digit edges are blended against the red, so they are exact. Disc edges are blended against the key color of their disc. The layout goes in `docs/wire-protocol.md`.
- **Show frames with pips** (type 14, host to device): two bytes for each key, a slot or 255 for "unchanged", and a count. A count of 0 shows the slot as it is. A count above 99 draws `99+`. Type 9 stays.

The board: for a key with a count, it copies the slot (5.9 ms measured) into the key's idle pip buffer, blits the disc of the slot's color, blits the digits, and submits the buffer. Each key owns two buffers, so a push never reads a buffer that the next count rewrites. The 12 buffers are the top 12 slots of the pool, so the pool stays at 64 (task 0063) and numbered slots become 0 to 51. The board records a new trace code, `SHOW_WITH_PIPS_STARTED` (13), before the copies, so that the time to the last push includes the stamps. A Show before the sheet arrives draws the slot with no pip. A key with a pip is in the same state as after type 9, with no blink.

The host: `frameKey` loses the count, and `flushFrames` sends type 14. `transport.Reconnecting` sends the sheet again before it stores the frames. A key that cannot be cached still takes `renderGlyph`, which draws the pip with the same code over the key color, so the 1-bit transparency of task 0041 sees an opaque edge.

The round: `renderGlyph` draws a white disc of radius `iconRoundRadius` (60 pixels, a first guess) at the panel centre, under every icon. It takes the key color, and blends the antialiased edge of the disc against it, so every edge pixel is opaque (task 0041). The round is part of the glyph, so it is in the cached frame and costs the board nothing. `assets/shift.png` is white today, so it changes to black, with its alpha kept. The shift key keeps its dark color `ColorUp` in the corners. Only the corners stay transparent, so a blink acts as before. The pip centre lies about 54 pixels from the panel centre, so the pip crosses the edge of the round. The ring is white so the pip matches the round where they overlap.

Files to change: `docs/wire-protocol.md`; `firmware/wire.py`, `firmware/app.py`, `firmware/display_render.py`, `firmware/frame_cache.py`; `driver/transport/`, `driver/plugin/server.go`; `driver/layers/render.go`, `pip.go`, `frames.go`, `controller.go`; `assets/shift.png`; `firmware/tracer.py`, `tools/blink_trace.py`, `Makefile`; `driver/go.mod`, `driver/go.sum`; `driver/cmd/blinksend/main.go` (a `pips` scenario); tests; both READMEs.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — The host builds a sheet of five discs and 13 digit sprites that inflates to exactly 32,768 bytes. **Proof:** `cd driver && go test ./layers -run TestPipSheet` passes, and `driver/layers/testdata/pip-sheet.png` shows every sprite.
- [x] **DoD-2** — Every icon, `shift.png` included, sits on a white round. `shift.png` has no white pixel. Every pixel inside the round where the icon is transparent is white. **Proof:** `cd driver && go test ./layers -run TestIconRound` passes, and `driver/layers/testdata/icon-round.png` shows icons over the five status colors.
- [x] **DoD-3** — `docs/wire-protocol.md` defines types 13 and 14 and the sheet layout. The Go and Python codecs round-trip both. **Proof:** `cd driver && go test ./transport -run Pip` and `.venv/bin/pytest test/test_wire.py -k pip` pass.
- [x] **DoD-4** — On the board, a Show with counts 1, 42, and 100 submits a frame that differs from the slot only inside the pip box, and the slot bytes are unchanged. A count of 0 submits the slot view itself. **Proof:** `.venv/bin/pytest test/test_app.py -k pip` passes.
- [x] **DoD-5** — A Show with a count before the sheet draws the slot with no pip and does not fail. **Proof:** `.venv/bin/pytest test/test_app.py -k pip_without_sheet` passes.
- [x] **DoD-6** — Counts 1 to 99 on one icon and color use one slot. A count change sends type 14 and no Store frame. **Proof:** `cd driver && go test ./layers -run PipNoStore` passes.
- [x] **DoD-7** — After a reconnect, the host sends the sheet before the first type 14. **Proof:** `cd driver && go test ./transport -run Reconnect` passes, with a test for it.
- [x] **DoD-8** — A key that is not cached draws a pip with the code that builds the sheet, and `digitFont` is gone. `driver/go.mod` pins `golang.org/x/image` at v0.24.0 or older. **Proof:** `cd driver && go test ./layers -run Pip` passes, `git grep -n digitFont driver/` lists nothing, and `go build ./...` passes with `go1.22.0`.
- [ ] **DoD-9** — On the board, a Show of six keys with counts takes at most 150 ms from `FRAMES_SHOWN` to the last `REFRESH_DONE`. **Proof:** the line `six keys in N ms` of `tools/blink_trace.py report --scenario=pips`, in three runs. It counts from `SHOW_WITH_PIPS_STARTED`, so it includes the stamps.
  Missing: not run. It needs the board, `make flash`, and `make blink-trace SCENARIO=pips`, which unmount `CIRCUITPY`. The scenario, the `SHOW_WITH_PIPS_STARTED` record, and the report are in place and tested without a board.
- [ ] **DoD-10** — At least 2 MB of heap stay free with the sheet loaded. **Proof:** the figure in `firmware/README.md`, from `make slot-probe`.
  Missing: not run. It needs `make slot-probe` on the board. The sheet adds one 32 KB `Bitmap` to the heap, and the pool is the same 64 buffers as before.
- [ ] **DoD-11** — On the board, counts 1, 12, and 100 show a red circle and a legible number on a red key and a white key, and every icon shows on its round. **Proof:** the owner's check, recorded in this spec.
  Missing: the owner's check of the panel is not done.
- [x] **DoD-12** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass.
- [ ] **DoD-13** — `docs/wire-protocol.md`, `firmware/README.md`, and `driver/README.md` describe the pip. The PR in the `pr` field links to this spec. **Proof:** those three files and the PR body.

## Risks

- A disc edge is blended against the key color, but the round makes the pixels under part of the edge white. A faint ring in the key color shows there → the host builds the sheet, so a second set of discs blended against white is a change in host code only. DoD-11 shows if it is needed.
- A white part of an icon merges with the round. `gmail.png` has 1,917 white pixels (its envelope) and `dev.png` has 6 → the colored strokes of `gmail.png` stay, and the owner checks it in DoD-11.
- The blit time of a pip is not measured. The copy is 5.9 ms. If six pips take more than 150 ms, DoD-9 fails → time one pip on the board before the full run.
- The push span of task 0061 (58 ms, limit 40) is not solved. This task does not claim to meet that limit.
- Without `glyphcore`, each extra `Bitmap` on the heap adds about 1 ms to `gc.collect()` (task 0063) → the 12 buffers come out of the 64, not on top of them.

## Open questions

- [ ] Are 44 pixels, `#E02020`, and a 2 pixel ring the right size and color on the panel? — the owner, from DoD-11.
- [ ] Is 60 pixels the right radius for the round? It decides how much of the pip lies over white, and so whether the second set of discs is needed. — the owner, from DoD-11.

## Notes

- The panel treats a pixel at half opacity or more as opaque, and any other as transparent (`transport.DecodePNGToRGB565`). A composed frame is opaque, so the board may stamp an antialiased edge on it.
- Measured by tasks 0059 and 0061: a Store frame takes 94 to 109 ms, a 32 KB copy 5.9 ms, and a compose 44 ms on core 0.
- `golang.org/x/image` v0.24.0 requires Go 1.18. Check the `GoVersion` of any other version before `go get`.
