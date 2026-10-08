---
id: "0049"
title: "Send the window commands once at boot, and push only the frame after that"
status: "ongoing"
created: "2026-10-06"
updated: "2026-10-08"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0049-frame-only-panel-push"
related: ["0043", "0045", "0048"]
tags: ["firmware", "spi", "pio"]
---

# 0049 — Send the window commands once at boot, and push only the frame after that

## Problem

Every push sends `CASET`, `RASET` and `RAMWR` before the frame (`Panel.start_push` in `firmware/st7735.py`). The window
never changes: it is always the full 128×128 frame at the same offset. The five short blocking writes hold the CPU
for 1.2 ms, and each toggle of a blinking key pays it. Task 0048 also needs pushes with no commands, because a
follower state machine on a separate DIN line cannot send them reliably.

## Goals

- After boot, a push sends the frame and nothing else.
- `Panel.start_push` holds the CPU 0.5 ms or less, down from 1.2 ms.
- A push that failed, or an unknown panel state, is repaired by the next push.
- The panels show the same images as today.

## Non-goals

- Parallel pushes, or a change to the SPI rate. Task 0048 covers both.
- A change to the init sequence.

## Approaches considered

Three approaches follow. Each one drops a different part of the per-push commands.

### Approach A — Window and RAMWR once, then frames only

`init_panels` sends `CASET`, `RASET` and `RAMWR` once for each panel. After that the panel stays in write mode, and its
address pointer wraps at the end of the window, so each push is the frame alone.

- Good, because a push holds the CPU only for the DMA start, and the bus carries no DC changes.
- Good, because spike 2 (2026-10-06) showed it: 450 frame-only pushes, with CS toggled each time, displayed
  correctly on all six panels.
- Bad, because nothing resyncs a panel that left write mode, for example after a brownout or a half-sent push.
- Bad, because it depends on a datasheet behavior, the pointer wrap, that one test on one board has shown.

### Approach B — The window once, and RAMWR before every push

`CASET` and `RASET` go out once at boot. Each push sends `RAMWR`, then the frame. `RAMWR` sets the pointer to the start
of the window.

- Good, because every push starts from a known pointer, so a slip never lasts beyond one frame.
- Good, because it is the normal use of the panel, so it needs no behavior beyond the datasheet.
- Bad, because each push still has two blocking writes and one DC change, about 0.4 ms of CPU.
- Bad, because it does not work for task 0048: followers send frames only, so a command per push would stop the
  parallel push or tear down the followers each time.

### Approach C — Keep the commands, and send them less often

Keep today's push. Cache the window on the panel, and skip the commands for a push that follows another push of the
same panel within one second.

- Good, because a panel that lost its state is repaired within one second.
- Good, because it saves the commands for blinking keys, which push twice a second.
- Bad, because the first push after one second of idle still pays 1.2 ms, and a key update often comes after idle.
- Bad, because it adds a timer per panel and a rule that a person must understand, to save what Approach A saves
  with no rule.

## Decision

Chosen: **Approach A — Window and RAMWR once, then frames only**.

It gives the lowest CPU cost, and it is the only approach that task 0048's parallel push can use. It accepts that a
panel which left write mode stays wrong until the code sends the window again. The code does that after a failed
push, and at boot. Spike 2 found no drift in 450 pushes.

## Design

`st7735.Panel` gets `setup_window()`: under the panel's CS line it sends `CASET`, `RASET` with the offsets, and
`RAMWR`, then sets CS high. `init_panels` calls it for each panel after `init()`. `start_push` sets DC high, CS low, and
starts the frame. It sends no command.

`Panel` keeps `_needs_window`. `start_push` sets it before it starts the frame and clears it after the frame starts. A
push that raises leaves it set, and the next `start_push` calls `setup_window()` first.

Files to change:

- `firmware/st7735.py` — `setup_window`, `_needs_window`, frame-only `start_push`, `init_panels`
- `firmware/README.md` — the frame-only push, and the measured figure
- `test/spibus.py`, `test/test_st7735.py`, `test/test_app.py` — new expectations for the commands
- `tools/dma_spike.py` — a limit on `start_push_us`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — After `init_panels`, each panel has received one `CASET`, one `RASET` with its offsets, and one
  `RAMWR`. **Proof:** `python3 -m pytest test/test_st7735.py -k window_once`
- [x] **DoD-2** — A push after `init_panels` sends the frame bytes and no command.
  **Proof:** `python3 -m pytest test/test_st7735.py -k frame_only_push`
- [x] **DoD-3** — After a push that raised, the next push sends the window and `RAMWR` first.
  **Proof:** `python3 -m pytest test/test_st7735.py -k window_after_failed_push`
- [ ] **DoD-4** — On the board, `start_push` holds the CPU 500 us or less.
  **Proof:** `make dma-spike` prints `start_push` of 500 us or less for every trial
  - Not confirmed: no board was attached. `tools/dma_spike.py` now fails a trial over 500 us (tested), but nobody has run it on the board.
- [ ] **DoD-5** — After `make blink-trace SCENARIO=single`, the six panels show no shifted or wrapped image, and
  `max gap` is 600 ms or less. **Proof:** the `max gap` line, and the Notes of this spec
  - Not confirmed: no board was attached, so `make blink-trace SCENARIO=single` did not run and no one looked at the panels.
- [ ] **DoD-6** — `firmware/README.md` records the frame-only push and the measured `start_push` time.
  **Proof:** `firmware/README.md`, section "Latency"
  - Partly done: the README describes the frame-only push, but the measured `start_push` time is missing until DoD-4 runs on the board.
- [ ] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:** the PR body

## Risks

- A panel that loses write mode after boot, for example from a brownout, shows a wrong image until the next failed push →
  the open question below asks whether a timed resync is worth it.
- The pointer wrap is shown on one board and one panel batch → DoD-5 checks all six panels after a long run.
- The shared DC line must be high for every push → `start_push` sets it each time.

## Open questions

- [ ] Should the code also send the window again every 60 s, while the bus is idle? — owner

## Notes

- Spike 2, 2026-10-06, RP2350, 10 MHz or less: after one `CASET`, `RASET` and `RAMWR`, 450 frame-only pushes, with CS
  low and high around each one, gave a correct box frame on all six panels.
- Today's push time: the five command writes take about 200 us each, and `start_push` holds the CPU 1.2 ms to 1.3 ms
  (task 0045, `make dma-spike`).
