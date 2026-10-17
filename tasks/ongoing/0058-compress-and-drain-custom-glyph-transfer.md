---
id: "0058"
title: "Cut custom-glyph transfer time: zlib-compress the pixels and drain the CDC port faster"
status: "ongoing"
created: "2026-10-17"
updated: "2026-10-17"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/69"
branch: "0058-compress-and-drain-custom-glyph-transfer"
related: ["0030", "0039", "0042", "0057"]
tags: ["firmware", "driver", "wire-protocol", "performance"]
---

# 0058 — Cut custom-glyph transfer time: zlib-compress the pixels and drain the CDC port faster

## Problem

A layer change sends up to six custom glyphs, and each one takes about 450 ms to reach the panel. A six-key layer redraw takes 2.5 to 3 s (task 0057). The CDC transfer is about 235 ms of each glyph, which is more than half. It runs at about 140 KB/s, and a bare read loop on the same board reaches about 550 KB/s.

## Goals

- A custom glyph reaches the board in less time. The firmware drains the CDC port at the board's measured limit, not one 256-byte read per loop pass.
- The driver sends a glyph as a zlib stream when that is smaller than the raw pixels.
- A six-key redraw with real icons takes at most 1.3 s on the board. Before this task it took 2.5 s or more.

## Non-goals

- A glyph cache on the board. Task 0057 can follow with one.
- The 90 to 100 ms `_compose` step, the stale-pixel redraw, and the 150 ms HID report gap (`minReportGap`).
- Key state over CDC.
- A custom CircuitPython build.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Faster read loop only

`CustomGlyphReader.feed` reads into one preallocated buffer with `readinto`. It keeps reading until the frame is complete or a time budget of about 8 ms ends. The wire format does not change.

- Good, because it needs no wire change. An old driver and a new board still work together.
- Good, because a prototype already cut a glyph from about 450 ms to about 320 ms.
- Bad, because the bare-loop ceiling is about 550 KB/s. A 32 KB frame still needs about 58 ms.
- Bad, because the loop stalls the switch scan for up to the time budget on each pass.

### Approach B — Compress the pixels on the host

`driver/transport` deflates the pixels with `compress/zlib` and sends them as a new frame type. The board inflates them with the built-in `zlib` module. The read loop stays as it is.

- Good, because the 13 sample icons in `assets/` deflate to 1,213 bytes on average, against 32,768 raw.
- Good, because the transfer shrinks by 95% or more for icons, whatever the loop speed.
- Bad, because a photo or a gradient does not compress. That glyph keeps the full 32 KB transfer at the slow read rate.
- Bad, because it adds a wire message type and an inflate step on the board. The cost of that step is not measured yet.

### Approach C — Send a smaller picture

The driver reduces each glyph to an 8-bit palette image of 16 KB. The board expands it to RGB565.

- Good, because it halves the bytes for every image, including a photo.
- Good, because it needs no inflate code on the board.
- Bad, because the palette limits a glyph to 256 colors. This loses quality.
- Bad, because the expand step on the board is a Python loop over 16,384 pixels, or needs new `bitmaptools` calls.

## Decision

Chosen: **Approach B — compress the pixels on the host**, with the read loop of Approach A included.

The goal is a short redraw for real icons, and only compression cuts their transfer to a few milliseconds. The faster loop stays because a glyph that does not compress still needs it. The cost accepted: one new frame type, and an old board silently drops it.

## Design

Frame type `6`, "Set custom glyph (compressed)", is host to device. The payload is the key index, then a zlib stream. The stream inflates to exactly 32,768 bytes. Type `5` stays as it is.

- The driver sends type `6` when the zlib stream is smaller than the 32,768 raw bytes. Otherwise it sends type `5`.
- The board drops a type `6` frame that does not inflate to 32,768 bytes. The key keeps its old image.
- `CustomGlyphReader.feed` reads into one preallocated buffer. It makes at most one copy of the pixels. The time budget is a named constant.

Files to change:

- `docs/wire-protocol.md` — add type `6` to the Type registry and a "Set custom glyph (compressed)" section.
- `driver/transport/wire.go` — add the type, the encoder, and the choice between type `5` and `6`.
- `driver/transport/emulator.go` — decode type `6` the same way as the board.
- `firmware/wire.py` — decode type `6`; rewrite `CustomGlyphReader.feed`.
- `firmware/app.py` — pass the decoded glyph through unchanged.
- `driver/cmd/blinksend/main.go` — keep the `glyphs` scenario (gradient, worst case). Add an `icons` scenario that sends six real icons from `assets/`.
- `test/test_wire.py`, `driver/transport/` tests — cover both paths.
- `firmware/README.md` — record the before and after figures.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The driver sends a glyph as type `6` when the zlib stream is smaller than 32,768 bytes, and as type `5` when it is not. **Proof:** `cd driver && go test ./transport/...` passes, with one test for each case.
  **Missing:** not started. See Notes, "Decision under review".
- [ ] **DoD-2** — The board decodes a type `6` frame to the same pixels as the type `5` frame for the same image. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a test that compares the two.
  **Missing:** not started. See Notes, "Decision under review".
- [ ] **DoD-3** — The board drops a type `6` frame that does not inflate to 32,768 bytes, and the key keeps its image. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a test that feeds a short stream.
  **Missing:** not started. See Notes, "Decision under review".
- [x] **DoD-4** — One `feed` call returns after the time budget even when the frame is not complete. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a test that uses a fake serial port and a fake clock. `test_custom_glyph_reader_stops_reading_when_the_time_budget_ends` covers it.
- [ ] **DoD-5** — On the board, `blinksend --scenario=icons` redraws six real icons in at most 1.3 s of host wall time. **Proof:** the line `six keys in N s` that the command prints, in three runs, each with N at most 1.3.
  **Missing:** the drain loop alone gives 1.21 s to 1.53 s over 6 runs, median 1.38 s. Two of the six runs are at most 1.3 s, and three consecutive runs did not all pass. See Notes.
- [x] **DoD-6** — On the board, `blinksend --scenario=glyphs` (pseudo-random pixels that do not compress) takes at most 1.9 s for six keys. **Proof:** the line `six keys in N s`, in three runs, each with N at most 1.9. The runs gave 1.38 s, 1.28 s, and 1.24 s.
- [ ] **DoD-7** — `docs/wire-protocol.md` lists type `6` and its payload layout. **Proof:** `docs/wire-protocol.md`, Type registry and the new section.
  **Missing:** not started. See Notes, "Decision under review".
- [x] **DoD-8** — `firmware/README.md` records the figures of DoD-5 and DoD-6 next to the 2.5 s starting figure. **Proof:** `firmware/README.md`.
- [x] **DoD-9** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass. The first reports 217 passed.
- [x] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- An old board skips type `6` by its `Length`, so the glyph never appears → flash the firmware before you run the new driver, and say so in the README.
- A glyph that does not compress stays near 110 ms to 120 ms per transfer → DoD-6 limits it.
- A drain loop of 8 ms delays the switch scan and the key events → DoD-4 bounds it, and a key press during an upload is checked by hand on the board.
- Inflating 32 KB can fragment the heap → read `gc.mem_free()` before and after 100 glyphs, never inside a timing probe.

## Open questions

- [x] Does `zlib.decompress` exist on the board's CircuitPython 10.2.1, and how long does it take for 32 KB? — Yes. It took 32 to 65 ms for four real streams, and `bytes(32768)` alone took about 11 ms.
- [ ] Does the 550 KB/s ceiling come from the RP2350 USB stack or from the macOS host driver? — optional. It does not change the design.

## Notes

Measured on 2026-10-17 on a Pico Plus 2 (CircuitPython 10.2.1), with the Tracer and `blinksend --scenario=glyphs`:

- One glyph took about 450 ms: CDC transfer 235 ms, payload copy and decode 40 ms, `_compose` 90 to 100 ms, SPI push 25 ms, and a stale-pixel redraw of 30 ms.
- `feed` makes one read per loop pass. Each read returns at most 256 bytes, so a frame needs 129 reads. A pass takes about 1.8 ms.
- A drain loop of 8 ms in `feed` gave about 320 ms per glyph, and six keys in 1.7 s.
- A bare loop on the board gave 550 KB/s with `readinto`, and 290 KB/s with the `in_waiting`, `read`, `extend` pattern. The size of the host write (64 B to 64 KB) did not change this.
- The 13 sample icons deflate to 300 to 3,978 bytes, with a mean of 1,213 bytes.
- Do not call `gc.mem_free()` inside a timing probe. It walks the heap and adds about 20 ms.

### Decision under review (2026-10-17)

The decision chose compression with the faster read loop. The read loop alone now gives these figures, on the real firmware:

- Six keys with noise glyphs: 1.24 s to 1.38 s, from 2.5 s.
- Six keys with real icons: 1.21 s to 1.53 s, median 1.38 s.
- The first glyph writes in about 78 ms. The bare-loop ceiling is about 58 ms.

Compression sends about 1.2 KB, but the board then inflates 32 KB in 32 to 65 ms. A glyph now spends 60 to 80 ms in transfer, so compression saves about 30 ms per glyph, or about 0.15 s for six keys. That is about 10% of the redraw, for a new frame type that an old board drops.

The `_compose` step is now the largest part, at 90 to 100 ms per key. Task 0058 lists it as a non-goal. The owner decides whether to build compression anyway, to drop DoD-1, 2, 3, and 7, or to move the work to a new task.

The smooth 128 by 128 gradient that the first `glyphs` scenario used deflates to 3,300 bytes. It is not a worst case, so the scenario now sends pseudo-random pixels.
