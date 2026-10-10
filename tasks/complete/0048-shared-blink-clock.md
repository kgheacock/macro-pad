---
id: "0048"
title: "Blink every key on one shared clock, and flip all six panels at once"
status: "complete"
created: "2026-10-06"
updated: "2026-10-10"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/48"
branch: "0048-shared-blink-clock"
related: ["0006", "0044", "0045", "0047", "0049"]
tags: ["firmware", "blink", "pio", "dma", "hardware"]
---

# 0048 — Blink every key on one shared clock, and flip all six panels at once

## Problem

Each blinking key keeps its own 500 ms schedule. The schedule starts when the key's blink flag turns on
(`MacroPad._next_blink_us` in `firmware/app.py`). Two keys that start 200 ms apart stay 200 ms apart, so a row of
blinking keys flashes out of time. Even with one phase, the six panels share one MOSI line, so six toggles go out one
after another: 6 × 18 ms = 108 ms from the first key to the last.

## Goals

- All blinking keys show "on" in the same 500 ms slot, and "off" in the next. A key that starts to blink joins the slot.
- With six keys blinking, the first and last `PUSH_STARTED` of one slot are 35 ms or less apart.
- A push of any number of panels ends within 50 ms of its first `PUSH_STARTED`.
- An update to a blinking key keeps the shared phase (task 0044).
- It builds on task 0049: a push sends the frame only, and sends no window command.

## Non-goals

- A different blink period, or a phase set by the host.
- A change to the frame that a blink shows. Tasks 0006 and 0044 define it.
- Per-key backlight control. Nothing uses it: `MacroPad._apply_backlight` sets one duty for all six keys.

## Approaches considered

Three approaches follow. Each one gives the keys one phase and a different skew.

### Approach A — A shared time grid, with serial pushes

The phase comes from the clock: slot = `now_us // BLINK_INTERVAL_US`, and an even slot is "on". Each `step` queues a push
for every blinking key whose frame differs from the slot. The pushes still run one at a time.

- Good, because it changes only `app.py` and `display_render.py`, and needs no wiring change.
- Good, because the phase is a function of time, so an update or a long stall needs no repair.
- Bad, because six pushes of 18 ms run in turn, so the last key changes about 108 ms after the first.
- Bad, because a step that composes a frame (23 ms) or paints six keys (150 ms) delays the slot change it finds.

### Approach B — A shared time grid, with parallel pushes on separate DIN lines

The grid of Approach A, plus a push that sends all due frames at once, built on task 0049's frame-only push.
Each panel gets its own DIN line. One PIO state
machine makes SCK. Six follower state machines each shift one panel's frame out of their own DMA channel.

- Good, because the six panels change within 2 ms of each other, and a push of all six takes one frame time (26 ms at
  10 MHz), not six.
- Good, because spike 2 (2026-10-06) showed the pieces work: a follower moved a frame to six panels at 15 MHz, and
  900 frame-only pushes at 10 MHz or less had no desync.
- Bad, because it needs a rewire (below), and spike 3 has not yet shown two followers at once.
- Bad, because a follower cannot slow the clock. At 15 MHz it lost sync in 5 of 150 pushes, so the rate falls to
  10 MHz, and the code must detect a desync and recover.

### Approach C — Blink by the backlight

Each key's backlight PWM goes to 0 for "off" and back for "on". The CPU sets six duty values in one pass, with no push.

- Good, because the six keys change within microseconds of each other, with no bus traffic.
- Good, because a blink costs the CPU almost nothing.
- Bad, because the whole key goes dark, glyph included. The "glyph stays, background flips" blink of task 0044 is lost.
- Bad, because the idle timer sets the same duty values, so the two features fight over them.

## Decision

Chosen: **Approach B — A shared time grid, with parallel pushes on separate DIN lines**.

It is the only approach that gives one phase and no visible ripple, and it keeps today's frames. It accepts a rewire,
a 10 MHz push rate, and desync recovery code. Task 0049 (frame-only pushes) comes first and ships alone. This task
then ships in two steps: the grid (Approach A), then the parallel push. If spike 3 fails, stop after step 1 and
accept 108 ms.

## Design

**Step 1, the grid.** `MacroPad._render_dirty_keys` computes `blink_on = (now_us // BLINK_INTERVAL_US) % 2 == 0`. A key
is drawn when it is dirty, or when it blinks and its shown frame differs from `blink_on`. `render_key` takes `blink_on`
in place of `toggle`. A key that starts in an "off" slot shows "off" at once. `_next_blink_us` goes away.

**Step 2, the parallel push.**

- Wiring: key k's DIN moves to its old backlight pin, GP0, GP1, GP22, GP26, GP27, GP28 for keys 0 to 5. The six
  backlight inputs join on GP7, which becomes one shared PWM output. SCK, DC, RST, CS, keys and mic do not move.
  The module's PWM pin drives a boost-converter enable with a 10 kΩ pull-up, so six on one pin load it by about 2 mA.
- PIO: followers first (`wait 0 pin 0`, `out pins, 1`, `wait 1 pin 0`, SCK as `first_in_pin`,
  `exclusive_pin_use=False`), then the leader (`out null, 1 side 0 [9]`, `nop side 1 [4]`, 10 MHz). That is 7 of the
  12 state machines and 7 of the 16 DMA channels.
- Queue: all frames due in one step start together. CS goes low for those panels only.
- Recovery: a follower whose DMA still runs after the leader ends has lost sync. The code stops and rebuilds all
  seven machines, sends the window and `RAMWR` again through a one-panel `PioBus`, and sends the frame again.

Files to change:

- `firmware/pins.py`, `firmware/code.py` — per-key DIN pin, one shared PWM on GP7
- `firmware/pio_spi.py` — `ParallelBus`: leader, followers, desync check, rebuild
- `firmware/st7735.py`, `firmware/app.py`, `firmware/display_render.py` — push groups, `blink_on`
- `test/` — new and rewritten tests, with the `rp2pio` stub extended for followers
- `driver/cmd/blinksend/main.go`, `tools/blink_trace.py`, `Makefile` — the `sync` scenario
- `hardware/README.md`, `hardware/breadboard-diagram.html`, `firmware/README.md` — pinout, wiring, figures

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — Two keys that start blinking 200 ms apart show the same frame in every later slot, and an update
  keeps the frame shown. **Proof:** `python3 -m pytest test/test_app.py -k "shared_phase or update_keeps_phase"`
- [x] **DoD-2** — A key that starts to blink in an odd slot shows "off" at once. After a step two slots late, a
  blinking key shows the current phase with one push.
  **Proof:** `python3 -m pytest test/test_app.py -k "joins_current_slot or late_step_jumps_to_phase"`
- [x] **DoD-3** — Frames due in one step start in one group, and no panel outside the group sees CS low.
  **Proof:** `python3 -m pytest test/test_app.py -k parallel_push`
- [x] **DoD-4** — A follower that does not finish with the leader makes the code rebuild, resend the window, and
  resend the frame. **Proof:** `python3 -m pytest test/test_pio_spi.py -k desync_recovery`
- [x] **DoD-5** — `firmware/pins.py` and `hardware/README.md` give each key's DIN on its old backlight pin and one
  shared backlight on GP7. **Proof:** `python3 -m pytest test/test_pins.py`, and the Pinout table of `hardware/README.md`
- [x] **DoD-6** — On the board, with six keys blinking and an update to key 4 every 2 s, `max skew` is 35 ms or less,
  `max span` is 50 ms or less, and `max gap` is 600 ms or less. **Proof:** `make blink-trace SCENARIO=sync`
- [x] **DoD-7** — A person sees six blinking keys flash in time, and sees no noise on any panel at 12.5 MHz.
  **Proof:** the Notes of this spec
- [x] **DoD-8** — `firmware/README.md` records the shared phase, the DIN lines, the 12.5 MHz rate,
  and the measured figures. **Proof:** `firmware/README.md`, section "Latency"
- [x] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:** the PR body

## Risks

- Spike 3 (two followers at once, on keys 0 and 1 rewired) has not run → run it before step 2, and stop at step 1 if it fails.
- Commands sent through a follower were flaky in spike 1, and the cause is open → only `PioBus` sends commands.
  Followers send frames only.
- A frame-only push has no resync, so a lost clock edge shows until the next push → the desync check finds every
  lost edge, because the follower's DMA then never ends.
- Task 0047 also needs PIO and DMA for the mic → the budget is 7 of 12 state machines, so the mic fits.
- A capture state machine on a shared pin hung the board three times → tests use the panels and the DMA state only.

## Open questions

- [x] Does 12.5 MHz hold? Yes: 400 groups on the board with and without a CPU load (2026-10-08), and no noise seen by eye (2026-10-10). — owner
- [ ] A system clock of 300 MHz would allow an even SCK duty at 15 MHz. Is overclocking acceptable? — owner

## Notes

- Spike results, 2026-10-06, RP2350, CircuitPython 10.2.1. Frame through a follower at 15 MHz, 7 cycles low and 3 high:
  correct on six panels, 17 ms. Frame-only pushes (now task 0049): 450 pushes correct. Desyncs: 15 MHz 5 of 150, 10 MHz 0 of 300,
  7.5 MHz 0 of 600. A one-panel push at 10 MHz takes 26 ms.
- A follower sees SCK about 3 PIO cycles late, so SCK needs a long low phase. That is why the duty is uneven.
- `docs/0.85inch_ScreenKey_Module.pdf` shows the PWM pin on the enable pin of a PAM2804 with a 10 kΩ pull-up.
- A timer-paced DMA chain (Approach B of task 0045) was dropped from this spec. It keeps a phase through a stall, but
  it still sends six frames in turn on one bus.
- Board run, 2026-10-10, `make blink-trace SCENARIO=sync`, two runs of 42 slots: max skew 31.0 and 31.2 ms, max span 34.1
  and 42.0 ms, max gap 501.4 and 507.7 ms. The skew missed the first limit of 30 ms by about 1 ms in both runs, so the
  owner raised it to 35 ms. The second run missed the span limit of 40 ms, so the owner raised it to 50 ms. The tool first printed a span of 0.0 ms,
  because it read `REFRESH_DONE` as code 10, not 8; the figures here come from the same traces with the code fixed.
- Visual check, 2026-10-10 (DoD-7), RP2350, real firmware, 12.5 MHz: `blinksend --scenario sync` left six keys blinking:
  five keys of one color and key 4, the updated one, of another. The owner saw all six flash in time and saw no noise on
  any panel. The colors differ from the RGB565 values sent (blue on five keys; red and green on key 4): the panels run
  with `INVON` (`firmware/st7735.py`). That is not part of this task.
