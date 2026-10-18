---
id: "0063"
title: "Resend a batch key state that the board lost, and cap the frame pool at 64 slots"
status: "ongoing"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0063-resend-lost-batch-state-and-cap-the-pool-at-64"
related: ["0044", "0059", "0060", "0061"]
tags: ["firmware", "driver", "wire-protocol", "reliability", "memory"]
---

# 0063 — Resend a batch key state that the board lost, and cap the frame pool at 64 slots

## Problem

The board holds one HID report. A loop pass longer than the 150 ms report gap lets the next report overwrite an unread one, and that key state is lost (task 0044). A batch of task 0060 then waits for the lost state until its 2 s time-out. With the 128-slot pool of task 0061, one garbage collection takes 115 to 150 ms, and 2 of 9 batch runs timed out. Runs without the pool timed out in 0 of 9.

## Goals

- The board tells the host which glyphs and key states an open batch still waits for, and the host sends them again.
- A batch that lost a key state completes within 1.0 s of the last send, not after 2 s.
- The frame pool has 64 slots, not 128, so one garbage collection takes about 42 ms, not 115 to 150 ms.

## Non-goals

- A resend for a plain `setKeyState` that is not part of a batch. The board cannot tell what it did not receive.
- Moving the pool off the Python heap, and the second core. Task 0059 covers both.
- Key state over CDC for the batch.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — The board asks for what it lacks, and the host resends

After 500 ms with no new data, an open batch sends a "Batch incomplete" frame that names the missing glyphs and key states. The host sends those items again from the state it already keeps.

- Good, because it repairs the loss from any cause, a long pass or anything else, and it needs no custom build.
- Good, because `transport.Reconnecting` already remembers the last glyph and key state of every key. The resend needs no new store.
- Bad, because it adds a device-to-host frame type and a state machine on the board.
- Bad, because a lost report costs 500 ms plus one HID report gap, about 0.65 s, not nothing.

### Approach B — Send the batch key states over CDC

A new frame carries the key state of a batch. CDC is flow controlled, so a long pass delays a frame and loses nothing.

- Good, because it removes the loss, so there is nothing to repair.
- Good, because it also removes the 150 ms HID gap from the batch, which could save about 0.75 s.
- Bad, because it adds a host-to-device frame, a second path for key state, and a change to `minReportGap` logic in `driver/transport`.
- Bad, because other senders of `setKeyState` still use HID, and still lose reports.

### Approach C — Wait longer between HID reports

Raise `minReportGap` from 150 ms to 250 ms. A pass must then run 250 ms to lose a report.

- Good, because it is one constant in `driver/transport/device.go`.
- Good, because it protects every sender, not only batches.
- Bad, because six key states then take about 1.25 s, not 0.75 s.
- Bad, because a pass of 150 to 250 ms still happens: one collection with 128 slots is 115 to 150 ms on top of 100 ms of work.

## Decision

Chosen: **Approach A — the board asks, and the host resends**, with a pool of 64 slots.

It repairs the failure that was measured, at no cost to a batch that loses nothing. The cost accepted: a new frame type, a state machine on the board, and 0.65 s for a batch that loses a report. Approach B is the better fix, and a later task can do it.

## Design

Frame type 11, "Batch incomplete", device to host. The payload is two bytes: the mask of keys that still wait for a glyph, and the mask of keys that still wait for a key state.

- The board records the time of the last glyph or key state that an open batch received. After 500 ms with none, it sends the frame, and sends it again every 500 ms, up to three times. It records trace code 12 each time.
- The 2 s time-out stays as the last resort.
- `transport.Reconnecting` reads the frame in `forward`. For each named key it sends the glyph it holds, then the key state it holds, through the open device. It sends nothing for a key it does not know.
- The frame pool is `SLOT_COUNT = 64` on the board, and `transport.SlotCount = 64` on the host.

Files to change:

- `docs/wire-protocol.md` — the new frame, trace code 12, and 64 slots.
- `firmware/wire.py`, `firmware/app.py`, `firmware/tracer.py`, `firmware/frame_cache.py` — send the frame, and the pool size.
- `driver/transport/` — read the frame, resend in `Reconnecting`, and `SlotCount`.
- `driver/cmd/blinksend/main.go` — a `batchloss` scenario that drops one key state on purpose.
- `test/`, `driver/transport/` — tests.
- `firmware/README.md` — the figures.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — An open batch that lacks data sends "Batch incomplete" with the right masks after 500 ms, at most three times, and sends nothing when the batch is complete. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test for each case. `test_open_batch_asks_the_host_for_what_it_lacks_after_500_ms`, `test_batch_incomplete_repeats_every_500_ms_at_most_three_times`, and `test_a_complete_batch_sends_no_batch_incomplete` cover it.
- [x] **DoD-2** — The host sends the named glyphs and key states again, and nothing for an unknown key. **Proof:** `cd driver && go test ./transport -run BatchIncomplete` passes. Three tests cover the named keys, a glyph-only mask with an unknown key, and keys the host never sent.
- [x] **DoD-3** — On the board, with one key state dropped on purpose, the batch releases with no `BATCH_TIMED_OUT` record, within 1.0 s of the last report of the first pass, and one trace record of code 12 appears. **Proof:** `go run ./cmd/blinksend --scenario=batchloss` and `python3 tools/blink_trace.py report --scenario=batchloss FILE`, in three runs. They printed `batch incomplete 1, released 528 ms`, `521 ms`, and `519 ms after the last report`, and the board asked for glyphs `0x00` and key states `0x04`, which is key 2.
- [x] **DoD-4** — The pool is 64 slots on the board and on the host. **Proof:** `git grep -n "SLOT_COUNT = 64" firmware/` and `git grep -n "SlotCount = 64" driver/`.
- [x] **DoD-5** — With 64 slots, one `gc.collect()` takes at most 50 ms. **Proof:** the probe line in `firmware/README.md`: 42 ms for 64 slots.
- [x] **DoD-6** — On the board, 15 runs of `blinksend --scenario=batch` end with no `BATCH_TIMED_OUT` record. **Proof:** the 15 trace files. All 15 ended with no `BATCH_TIMED_OUT`, and the board did not need to ask in any of them.
- [x] **DoD-7** — `docs/wire-protocol.md` lists type 11, trace code 12, and 64 slots. **Proof:** `docs/wire-protocol.md`.
- [x] **DoD-8** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass. The first reports 294 passed.
- [ ] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- A resend arrives after the board released the batch, and a key draws twice → the host sends only the masks the board named, and an open batch ignores a duplicate.
- The resend itself waits for the 150 ms HID gap and is lost again → the board asks up to three times.
- A pool of 64 holds fewer frames than a full layer tree (up to 37) → the host drops the least recently used slot, and a miss costs about 100 ms for that key.

## Open questions

- [ ] Is 64 slots enough that a normal session rarely misses? — DoD-6 does not answer it. The owner watches the log.

## Notes

Measured on 2026-10-18 with PR #72's firmware (task 0061 investigation):

- `gc.collect()` took 0.7 ms with no pool, 22 ms with 32 slots, 42 ms with 64 slots, and 115 to 150 ms with 128 slots.
- `blinksend --scenario=batch` runs that ended with `BATCH_TIMED_OUT`: 0 of 9 with no pool, 1 of 12 with 32 slots, 2 of 9 with 128 slots. The first failure had payload `0x0400`: the report for key 2 never arrived.
- A pool of 128 `bytearray`s cost the same as 128 `displayio.Bitmap` objects. The collector scans every live heap block.

Measured on 2026-10-18 with this branch's firmware, a Pico Plus 2, and the tracing `code.py`:

- `blinksend --scenario=batchloss`, 3 runs: the board sent Batch incomplete once, naming key 2's state; `Reconnecting` sent it again; the batch released 528, 521, and 519 ms after the last report of the first pass. No run ended with `BATCH_TIMED_OUT`.
- `blinksend --scenario=batch`, 15 runs with 64 slots: 0 time-outs, and the board asked in none of them. The compose step had a median of 77 ms, a 90th percentile of 98 ms, and a maximum of 170 ms. A pass can still be longer than the 150 ms gap, so a loss is less likely than with 128 slots, not impossible. The resend covers it.
- One `go test -race ./transport` run hit the 10 minute test time-out once while other builds ran. It passed in 60 of 60 stress runs of the new tests, and in 25 of 25 runs of the whole package. The test that hung was not captured.
