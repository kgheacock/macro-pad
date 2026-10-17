---
id: "0060"
title: "Draw a layer update all at once: the board holds new frames until the batch is complete"
status: "complete"
created: "2026-10-17"
updated: "2026-10-17"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/70"
branch: "0060-draw-a-layer-update-all-at-once"
related: ["0057", "0058", "0048", "0045", "0044"]
tags: ["firmware", "driver", "wire-protocol", "layers", "display"]
---

# 0060 — Draw a layer update all at once: the board holds new frames until the batch is complete

## Problem

A layer change sends each changed key in turn: a glyph over CDC, then a key state over HID. The board draws each key when its data arrives. The six panels change one after another, and the user sees the layer paint key by key. In a trace of six icons, the first `PUSH_STARTED` records of the six keys are 732 ms apart.

## Goals

- The six panels of one layer change start to draw within 40 ms of each other.
- A layer change that changes one key still draws at once, with no wait.
- The six-key change takes at most 1.0 s, as it takes 0.8 s today.

## Non-goals

- A faster layer change. This task changes when the panels draw, not how long the data takes.
- A glyph cache, and the second core (task 0059).
- Key state over CDC. The 150 ms gap between HID reports (`minReportGap`) stays.
- Fewer than two groups of pushes. One group holds at most three panels (`pio_spi.MAX_GROUP`), so six keys go out as two groups.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — The board holds new frames until the batch is complete

The driver sends a "Begin batch" frame that names which keys get a glyph and which get a key state. The board composes each frame as the data arrives, but queues no push for a key in the batch. When every named glyph and key state has arrived, the board queues all the frames together.

- Good, because it reuses the glyph and key-state messages. A raw or compressed glyph of any size works.
- Good, because the board decides the moment of the draw. HID and CDC have no common order, and the masks make that order unimportant.
- Bad, because it adds a frame type, and a state machine on the board that waits for a count, with a time-out.
- Bad, because the HID gap still makes the batch last about 0.9 s.

### Approach B — Hide the update with the backlight

The driver turns the six backlights off, sends the layer as it does now, and turns them on again. Each panel has a PWM backlight (`app.Backlight`).

- Good, because it is the smallest change: one message, and no staging on the board.
- Good, because the board keeps its present draw path.
- Bad, because the panels still change one after another. The user sees a dark pause instead of the paint, and then a light that comes on all at once.
- Bad, because a layer change that changes one key would also go dark. The driver needs a rule for when to hide, and the dark pause is longer than the change.

### Approach C — One frame carries the whole layer

A new "Set layer" frame holds every changed key: index, color, blink, and a compressed glyph. The board decodes it and queues every frame at the end of the frame. No HID report is needed.

- Good, because one frame is atomic. There is no order between channels to manage.
- Good, because it removes the 150 ms HID gap. The batch could take about 0.75 s.
- Bad, because a frame holds at most 65,535 bytes (`uint16` `Length`), so six glyphs that do not compress do not fit. The driver needs a second path for them.
- Bad, because the board must buffer up to 65,538 bytes, and a half frame has no time-out in the reader today.

## Decision

Chosen: **Approach A — the board holds new frames until the batch is complete**.

The goal is that the panels change together, and only A and C do that. A keeps the one-frame-per-glyph limit of task 0058 and has no size limit to work around. The cost accepted: one new frame type, a time-out on the board, and the 0.9 s HID floor that C would remove.

## Design

Frame type `7`, "Begin batch", host to device. The payload is two bytes: a mask of the keys that get a glyph, and a mask of the keys that get a key state. Bit `k` is key `k`.

- The board holds the frames of the named keys. A key outside the masks draws as now.
- The board releases the batch when every named glyph is decoded and every named key state is applied. It then queues the frames of all the named keys in one pass, and starts the pushes as groups.
- After 2 s the board releases what it has, and records a trace record. A new "Begin batch" while one is open releases the open one first.
- A blinking key in the batch draws the frame of the blink slot that is current at the release (task 0048).
- `layers.Controller.flush` sends "Begin batch" when two or more keys change, and sends nothing extra for one key.

Files to change:

- `docs/wire-protocol.md` — add type `7`.
- `firmware/wire.py`, `firmware/app.py` — read the frame, hold and release.
- `firmware/tracer.py` — add a code for a batch released by time-out.
- `driver/transport/` — a `BeginBatch` call on `Transport`, `Device`, `Emulator`, `Reconnecting`, and the fan-out.
- `driver/plugin/server.go`, `driver/layers/controller.go` — pass the call through, and use it in `flush`.
- `driver/cmd/blinksend/main.go` — a `batch` scenario.
- `test/test_wire.py`, `test/test_app.py`, `driver/layers/layers_test.go` — cover the cases below.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — On the board, the first `PUSH_STARTED` records of the six keys of a batch are at most 40 ms apart. **Proof:** the line `push span N ms` that the `batch` scenario report prints, in three runs, each with N at most 40.
- [x] **DoD-2** — The six-key batch takes at most 1.0 s. **Proof:** the line `six keys in N s` of `blinksend --scenario=batch`, in three runs, each with N at most 1.0.
- [x] **DoD-3** — The board draws no key of a batch before the last named glyph and key state have arrived. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test that feeds five of six and checks that no panel has a new frame.
- [x] **DoD-4** — After 2 s the board draws the keys it has, and records the time-out. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test that steps a fake clock past 2 s.
- [x] **DoD-5** — A key outside the masks draws at once, while a batch is open. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test for that key.
- [x] **DoD-6** — A blinking key of a batch draws the frame of the blink slot at the release, in step with the other blinking keys. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test at two blink slots.
- [x] **DoD-7** — A layer change of one key sends no "Begin batch", and a change of two or more keys sends one first. **Proof:** `cd driver && go test ./layers -run Batch` passes.
- [x] **DoD-8** — `docs/wire-protocol.md` lists type `7` and its two masks. **Proof:** `docs/wire-protocol.md`, Type registry and the new section.
- [x] **DoD-9** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass.
- [x] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- A named glyph or key state never arrives, and the layer stays old for 2 s → the time-out releases what the board has. Test it with DoD-4.
- Firmware that does not know type `7` skips it by its `Length`, and the layer draws key by key → flash the firmware before the driver, and say so in the README.
- A batch of six holds six staged frames, 32 KB each → measure the free heap before and after 100 batches. The board has about 7 MB.
- The panels draw in two groups of three, about 30 ms apart → the 40 ms limit of DoD-1 allows it.

## Open questions

- [ ] Should `Reconnecting` replay the state after a replug as one batch? — it replays one key at a time today. The owner decides.

## Notes

Measured by task 0058 on 2026-10-17, on a Pico Plus 2, with `blinksend --scenario=icons`:

- The first `PUSH_STARTED` of keys 0 to 5 were at 76, 215, 362, 521, 661, and 809 ms. The span is 732 ms.
- One group holds at most three panels (`pio_spi.MAX_GROUP`): CircuitPython 10.2.1 puts the state machines that share a pin on one PIO block, and the leader takes one of four. A push of six panels is two groups, and the second starts after the first ends, about 30 ms later.
- The board already starts every queued frame as one group (`MacroPad._start_group`). The spread comes from frames that reach the queue 130 ms apart.
