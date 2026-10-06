---
id: "0044"
title: "Keep blinking keys independent: persist key state with one cheap nvm write in an idle gap"
status: "backlog"
created: "2026-10-06"
updated: "2026-10-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0030", "0042", "0043"]
tags: ["firmware", "blink", "latency"]
---

# 0044 — Keep blinking keys independent: persist key state with one cheap nvm write in an idle gap

## Problem

Each key must act on its own. Today an update to one key freezes every blinking key for 0.4 s to 0.7 s.
The cause is the flash write in `MacroPad.step`. The board takes 320 ms to 590 ms for each write of a 5-byte state file.
The blinkers' gap between toggles grows from 500 ms to 700 ms to 950 ms, and their phases shift.
These numbers come from 4 MHz SPI. `code.py` now sets 16 MHz, which cuts a frame push from 79 ms to 21 ms
but leaves the flash write unchanged.

The same spike found two more faults. A burst of 6 reports in 250 ms decoded only 2, because the board
keeps one HID report. An update to a blinking key flips it to the opposite frame.

## Goals

- An update to one key delays no other blinking key by more than 150 ms.
- Every update stays durable. No rule blocks a write because a key blinks.
- Every report in a burst of updates reaches the board.
- An update to a blinking key keeps that key on its 500 ms toggle schedule.

## Non-goals

- One shared blink phase for all keys. This is issue 2 and has its own task.
- The SPI rate. `code.py` already sets 16 MHz, confirmed on all six panels on 2026-10-06.
- A faster custom-glyph upload, or a change to `BLINK_INTERVAL_US`.
- A move away from CircuitPython. That is a separate spike.

## Approaches considered

Three approaches follow. Each one solves the freeze in a different way.

### Approach A — Write only while no key blinks

`step` keeps changed keys in RAM. It writes them in the first `step` in which no key blinks.

- Good, because no blinker exists during a write. The freeze cannot happen.
- Good, because it needs no new storage format.
- Bad, because while any key blinks, no update is durable. A long "Waiting" blink delays every write.
- Bad, because a reset in that time restores the old state of every key that changed.

### Approach B — Debounce the file write to a quiet moment

`step` writes the file 2 s after the last change, and only when the next blink is far enough away.

- Good, because it is a small change inside `_persist_pending_keys`.
- Good, because it cuts flash wear when a plugin sends many updates to one key.
- Bad, because the idle gap between blink rounds is about 370 ms with six in-phase blinkers at 16 MHz. A file
  write takes 190 ms to 590 ms on this board, so it rarely fits. The freeze happens 2 s later.
- Bad, because an update in the 2 s window is lost on power loss.

### Approach C — One `microcontroller.nvm` write for all pending keys, placed in an idle gap

The 5-byte header of each key lives in `nvm`. A write of all six headers takes about 50 ms.
`step` writes when no blink is due within 100 ms, or when a change has waited 2 s.

- Good, because it is 6 to 12 times cheaper than the file write: 50 ms against 320 ms to 590 ms. It is the same
  50 ms for 5 bytes or 30 bytes.
- Good, because every update is durable within about 2 s, with no blink condition.
- Bad, because it adds a second storage format with a magic byte. `nvm` survives `make flash`, so `make flash`
  no longer resets the state of a key (task 0030, DoD-7).
- Bad, because custom-glyph pixels do not fit in the 4096-byte `nvm`. They stay in files, and a glyph upload still stalls.

## Decision

Chosen: **Approach C — One `nvm` write, placed in an idle gap**.

It keeps every update durable, which is the cost that the owner refused in Approach A.
At 16 MHz, six in-phase blinkers push for 126 ms of each 500 ms. The measured 50 ms write fits the 370 ms gap.
But blinkers that were updated at different times are out of phase, and one of them can be due every 83 ms.
Then no 100 ms gap exists, and the 2 s limit forces the write. The decision accepts a 50 ms freeze in that
case, and the change of `make flash` behavior. Issue 2 (one shared phase) removes the case.

## Design

`glyph_state.py` gets `NvmStorage`. It holds one 5-byte header per key at offset `5 * key`, behind a magic byte.
A bad magic reads as the power-on default. Pixel data of a custom glyph stays in `glyph_state_files/`.

`MacroPad._persist_pending_keys` runs when no blink is due in the next `PERSIST_BUDGET_US` (100 ms),
or when the oldest pending change is older than `PERSIST_MAX_WAIT_US` (2 s). It writes all pending headers in one `nvm` write.

Two smaller fixes ride along:

- `render_key` takes a `toggle` flag. A redraw caused by a state change does not toggle `_blink_visible`.
  `_render_dirty_keys` keeps `_next_blink_us` for such a redraw. Only a due blink toggles the frame.
- `transport.Device.SendKeyState` waits at least `minReportGap` after the previous report. The board holds
  one report, so a faster burst overwrites itself. Set `minReportGap` from the DoD-6 measurement.

Files to change:

- `firmware/glyph_state.py` — `NvmStorage`, magic byte
- `firmware/app.py` — gate and batch `_persist_pending_keys`, keep the blink schedule on a state redraw
- `firmware/display_render.py` — `render_key(..., toggle=True)`
- `driver/transport/device.go` — `minReportGap`
- `tools/blink_trace.py`, `driver/cmd/blinksend/main.go`, `Makefile` — `make blink-trace`
- `test/test_app.py`, `test/test_glyph_state.py`, `driver/transport/device_test.go` — new tests
- `firmware/README.md`, `tasks/ongoing/0030-custom-glyph-upload-and-persistence.md` — record the change

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — Any number of pending keys cost one `nvm` write in one `step`.
  **Proof:** `python3 -m pytest test/test_app.py -k persist_batches_one_nvm_write`
- [ ] **DoD-2** — A new `MacroPad` on the same `nvm` restores color, Emoji ID, and blink of every key.
  A bad magic byte gives the power-on default.
  **Proof:** `python3 -m pytest test/test_glyph_state.py -k nvm`
- [ ] **DoD-3** — With a blink due in 50 ms, `step` does not write. When the change is 2 s old, it writes.
  **Proof:** `python3 -m pytest test/test_app.py -k persist_waits_for_idle_gap`
- [ ] **DoD-4** — An update to a blinking key draws the frame that the key showed, with the new color,
  and the next toggle stays on the old schedule.
  **Proof:** `python3 -m pytest test/test_app.py -k update_keeps_blink_phase`
- [ ] **DoD-5** — At 16 MHz, with keys 0 to 2 blinking, 10 updates to key 4, 2 s apart, leave every gap
  between `REFRESH_DONE` records of keys 0 to 2 at 650 ms or less, and every write between `REFRESH_DONE` and
  `PERSIST_DONE` at 100 ms or less. Before the change, at 4 MHz, the largest gap was 950 ms.
  **Proof:** `make blink-trace SCENARIO=single` prints `max gap` and `max persist` within these limits
  (board, `CIRCUITPY` unmounted)
- [ ] **DoD-6** — A burst of 6 updates, sent 50 ms apart through the driver, gives 6 `HOST_MESSAGE_DECODED` records.
  **Proof:** `make blink-trace SCENARIO=burst` prints `decoded 6/6`
- [ ] **DoD-7** — `SendKeyState` called twice at once waits `minReportGap` before the second write.
  **Proof:** `cd driver && go test ./transport -run TestSendKeyStateSpacesReports`
- [ ] **DoD-8** — `firmware/README.md` records the `nvm` layout, the gap rule, and the measured times.
  Task 0030 states that `make flash` no longer resets key state.
  **Proof:** `firmware/README.md`, section "Latency"; `tasks/ongoing/0030-custom-glyph-upload-and-persistence.md`
- [ ] **DoD-9** — The temporary tracer edit is gone from `firmware/code.py`.
  **Proof:** `git grep "TEMPORARY (blink spike)"` returns nothing
- [ ] **DoD-10** — The PR in the `pr` field links to this spec.
  **Proof:** the PR body

## Risks

- `nvm` survives `make flash`, so an old layout can reach new firmware → the magic byte rejects it.
- `nvm` wears out like flash → a write happens only when a header changed, and at most once per 2 s. Estimate the cycles in DoD-8.
- A 50 ms burst needs the firmware to read each report in time → DoD-6 measures it. Raise `minReportGap` if it fails.
- A mounted `CIRCUITPY` on macOS reloads the board and breaks CDC and HID → unmount it before `make blink-trace`.

## Open questions

- [ ] Is a 2 s limit right? A longer limit raises the loss on power cut and lowers wear. — owner
- [ ] Should `make flash` still reset key state? It needs a boot-time or `boot.py` rule. — owner

## Notes

- Spike, 2026-10-06, RP2350, 4 MHz, 20 samples. Update to a steady key: build 25 ms to 35 ms, push 80 ms.
  Write of a 5-byte file today: 320 ms to 590 ms. In-place file write: 190 ms to 270 ms.
  `nvm` write: 48 ms to 53 ms for 5 bytes or 30 bytes. The first six `nvm` writes took 1 ms. They wrote to erased bytes.
  An in-place write of unchanged bytes still took 126 ms. `step` already skips unchanged writes.
- Clock spike, 2026-10-06: a frame push takes 79 ms at 4 MHz, 42 ms at 8 MHz, 29 ms at 12 MHz, 21 ms at
  16 MHz, and 17 ms to 18 ms at 20 MHz and above. The CPU that feeds the FIFO limits a push above about 19 MHz.
  The RP2350 steps its SPI clock down from 150 MHz, so 16 MHz runs at 15 MHz. A color change at 16 MHz is about
  55 ms: a 23 ms compose and a push of 21 ms or more. A DMA push could go below 17 ms. See the native-core spike.
- The PIO spike showed a PIO blink keeps time through flash writes. It gates the backlight, so it hides the
  whole key. This task does not use it.
- Run `go run ./driver/cmd/macropadd` instead of a built binary. A binary built in the scratchpad exited with code 137.
