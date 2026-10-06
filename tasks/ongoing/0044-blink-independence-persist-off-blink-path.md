---
id: "0044"
title: "Keep blinking keys independent: keep no key state on the board, and replay it from the host"
status: "ongoing"
created: "2026-10-06"
updated: "2026-10-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/45"
branch: "0044-blink-independence-persist-off-blink-path"
related: ["0030", "0042", "0043"]
tags: ["firmware", "blink", "latency"]
---

# 0044 — Keep blinking keys independent: keep no key state on the board, and replay it from the host

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
- After a power cut or a replug while the host runs, every key shows its last state again. The host restores it.
- The board wears no flash for key state.
- Every report in a burst of updates reaches the board.
- An update to a blinking key keeps that key on its 500 ms toggle schedule.

## Non-goals

- One shared blink phase for all keys. This is issue 2 and has its own task.
- The SPI rate. `code.py` already sets 16 MHz, confirmed on all six panels on 2026-10-06.
- A faster custom-glyph upload, or a change to `BLINK_INTERVAL_US`.
- A move away from CircuitPython. That is a separate spike.

## Approaches considered

Four approaches follow. Each one solves the freeze in a different way.

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

### Approach D — Keep no state on the board, and replay it from the host

The board holds key state in RAM only. `macropadd` remembers each key's last key state and custom glyph.
When the board connects, it sends them again.

- Good, because the board never writes flash. The freeze and the wear are gone, and so is the gap rule.
- Good, because the board loses a whole storage format, `glyph_state.py`, and `boot.py` no longer remounts the filesystem for writing.
- Good, because it fits the Stream Deck portability goal of task 0013: a Stream Deck keeps no state either.
- Bad, because with no driver running, a power cycle leaves every key at the power-on default.
  A one-shot `macrodriver` call lasts only until the next power cycle.
- Bad, because the keys show the default for a few seconds after a replug, until the board has booted and the driver has replayed.
- Bad, because a daemon restart or a host reboot loses the memory, unless the daemon saves it to disk. It does: see Design.

## Decision

Chosen: **Approach D — Keep no state on the board, and replay it from the host**.

The owner chose it after Approach C, on 2026-10-06, because of flash wear. An `nvm` write is one sector erase with no wear leveling.
At an assumed 100,000 erase cycles, a plugin that changes a key every few seconds would use them in days.
Approach C also needed a gap rule and still forced a 50 ms freeze when blinkers were out of phase.
The decision accepts that the board shows the default until a driver connects. The owner added, on 2026-10-06, that a host always exists,
so the daemon saves its memory to disk.
Approach C was built first, reviewed, and removed.

## Design

The board writes nothing. `MacroPad` starts every key at the power-on default. `glyph_state.py`, its tests, the `storage`
argument, and the persist gate are gone. `boot.py` no longer remounts the filesystem.

`transport.Reconnecting` wraps the real device. It opens the board, waits `defaultSettleDelay` (1 s, a starting value
that no measurement backs), and sends each key's last state: its custom glyph first, then its key state. It reads the board's
messages for as long as the board stays connected, and opens it again when it goes. It remembers the state the board holds:
a custom glyph sets the key to the sentinel Emoji ID, and a built-in Emoji ID ends the key's glyph. A send while no board is connected is
remembered, reports success, and is replayed. `macropadd` uses it in place of `transport.Open`.

The daemon saves the memory on every change to a state directory (`--state-dir`, default `macro-pad` under the user's config directory):
`keys.json` holds each key's key state, and `glyph-N.bin` holds key N's glyph as raw pixels. It loads them at start and replays them to the
first board it connects to. Files are written by rename, a `keys.json` that does not parse is moved to `keys.json.bad`, and a missing or
wrong-size glyph file leaves the key with its color and blink and no glyph. A send's bookkeeping and `Close` never wait behind a glyph
upload, which takes seconds: only the writes to the board take turns.

Two smaller fixes ride along:

- `render_key` takes a `toggle` flag. A redraw caused by a state change does not toggle `_blink_visible`.
  `_render_dirty_keys` keeps `_next_blink_us` for such a redraw. Only a due blink toggles the frame.
- `transport.Device.SendKeyState` waits at least `minReportGap` after the previous report. The board holds
  one report, so a faster burst overwrites itself. Set `minReportGap` from the DoD-6 measurement.

Files to change:

- `firmware/app.py`, `firmware/boot.py`, `firmware/glyph_state.py` (deleted) — no persistence, no remount
- `firmware/display_render.py` — `render_key(..., toggle=True)`
- `driver/transport/reconnect.go`, `driver/transport/statestore.go`, `driver/cmd/macropadd/main.go` — `Reconnecting`, state files
- `driver/transport/device.go` — `minReportGap`
- `tools/blink_trace.py`, `driver/cmd/blinksend/main.go`, `Makefile` — `make blink-trace`
- `test/test_app.py`, `test/test_display_render.py`, `driver/transport/reconnect_test.go`, `driver/transport/device_test.go` — tests
- `firmware/README.md`, `docs/wire-protocol.md`, `tasks/ongoing/0030-custom-glyph-upload-and-persistence.md` — record the change

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — The board code writes no key state: no `nvm`, no state file, no filesystem remount.
  **Proof:** `git grep -n "nvm\|glyph_state\|remount" -- 'firmware/*.py'` returns nothing
- [x] **DoD-2** — After a reconnect, `Reconnecting` sends each key's last key state, and a custom glyph before its key state.
  A built-in Emoji ID ends a key's glyph.
  **Proof:** `cd driver && go test ./transport -run 'TestReconnecting_Replays|TestReconnecting_ABuiltIn'`
- [x] **DoD-3** — A send while the board is absent reports success and is replayed on the next connect.
  **Proof:** `cd driver && go test ./transport -run TestReconnecting_ASendWhileTheBoardIsAbsentIsReplayed`
- [x] **DoD-4** — An update to a blinking key draws the frame that the key showed, with the new color,
  and the next toggle stays on the old schedule.
  **Proof:** `python3 -m pytest test/test_app.py -k update_keeps_blink_phase`
- [ ] **DoD-5** — At 16 MHz, with keys 0 to 2 blinking, 10 updates to key 4, 2 s apart, leave every gap
  between `REFRESH_DONE` records of keys 0 to 2 at 650 ms or less. Before the change, at 4 MHz, the largest gap was 950 ms.
  **Proof:** `make blink-trace SCENARIO=single` prints a `max gap` within this limit
  (board, `CIRCUITPY` unmounted)
  Not run: it needs the board. `tools/blink_trace.py`'s analysis passes its tests on synthetic traces only.
- [ ] **DoD-6** — A burst of 6 updates, sent 50 ms apart through the driver, gives 6 `HOST_MESSAGE_DECODED` records.
  **Proof:** `make blink-trace SCENARIO=burst` prints `decoded 6/6`
  Not run: it needs the board. `minReportGap` is 50 ms, a starting value and not a measured one.
- [x] **DoD-7** — `SendKeyState` called twice at once waits `minReportGap` before the second write.
  **Proof:** `cd driver && go test ./transport -run TestSendKeyStateSpacesReports`
- [x] **DoD-8** — `firmware/README.md` records that the board keeps no state, why, and how the host replays it.
  Task 0030 states that the board no longer persists state.
  **Proof:** `firmware/README.md`, sections "Latency" and "Custom glyphs and key state"; `tasks/ongoing/0030-custom-glyph-upload-and-persistence.md`
- [ ] **DoD-9** — The temporary tracer edit is gone from `firmware/code.py`.
  **Proof:** `git grep "TEMPORARY (blink spike)"` returns nothing
  The edit is gone: `git grep "TEMPORARY (blink spike)" -- firmware tools Makefile driver` finds nothing. The
  proof as written still finds this line of the spec, so it cannot return nothing.
- [x] **DoD-10** — The PR in the `pr` field links to this spec.
  **Proof:** the PR body
- [ ] **DoD-11** — With `macropadd` running and keys set, unplugging and replugging the board brings every key back to its last state.
  **Proof:** on the board: set a color on key 0 and a blinking color on key 1, replug the USB cable, and see both keys return
  without a new call. Record the time from replug to the last key in `firmware/README.md`, and set `defaultSettleDelay` from it.
  Not run: it needs the board.
- [x] **DoD-12** — The daemon saves each key's state to files on the host, and a new daemon replays them to the first board.
  A built-in Emoji ID removes the glyph file. A corrupt `keys.json` is moved aside. A missing glyph file keeps the color and blink.
  A slow glyph upload does not block `Close`.
  **Proof:** `cd driver && go test ./transport -run TestReconnecting`
  Checked on the real board on 2026-10-06: `macropadd --state-dir` loaded 4 keys, including a custom glyph, and replayed them 3 s after start.
  The files were `keys.json` and a 32,768-byte `glyph-2.bin` with the expected RGB565 pixels. Whether the panels showed the state was not seen.

## Risks

- Replay races the board's boot: a report that arrives while `code.py` restarts is dropped → `defaultSettleDelay` waits 1 s. DoD-11 measures it.
- Replaying six keys costs 6 reports at `minReportGap`, and a glyph costs about 32 KB over CDC → DoD-11 records the time to the last key.
- A glyph upload to the board takes seconds, and the first version of `Reconnecting` held its lock throughout, so every other send and `Close` hung → the lock is split, and a test covers it.
- A one-shot `macrodriver` call does not survive a power cycle → documented in `firmware/README.md`.
- A 50 ms burst needs the firmware to read each report in time → DoD-6 measures it. Raise `minReportGap` if it fails.
- A mounted `CIRCUITPY` on macOS reloads the board and breaks CDC and HID → unmount it before `make blink-trace`.

## Open questions

- [x] Should `macropadd` save its memory to disk, so a host reboot also restores the keys? Yes, and it does.
- [x] Is a default-color power-on acceptable with no driver running? Yes: the owner says a host always exists.
- [x] Is a 2 s limit right? Moot: the board no longer writes.
- [x] Should `make flash` still reset key state? Moot: it behaves like a power cycle.

## Notes

- Spike, 2026-10-06, RP2350, 4 MHz, 20 samples. Update to a steady key: build 25 ms to 35 ms, push 80 ms.
  Write of a 5-byte file today: 320 ms to 590 ms. In-place file write: 190 ms to 270 ms.
  `nvm` write: 48 ms to 53 ms for 5 bytes or 30 bytes. The first six `nvm` writes took 1 ms. They wrote to erased bytes.
  An in-place write of unchanged bytes still took 126 ms. `step` already skips unchanged writes.
- Clock spike, 2026-10-06: a frame push takes 79 ms at 4 MHz, 42 ms at 8 MHz, 29 ms at 12 MHz, 21 ms at
  16 MHz, and 17 ms to 18 ms at 20 MHz and above. The CPU that feeds the FIFO limits a push above about 19 MHz.
  The RP2350 steps its SPI clock down from 150 MHz, so 16 MHz runs at 15 MHz. A color change at 16 MHz is about
  55 ms: a 23 ms compose and a push of 21 ms or more. A DMA push could go below 17 ms. See the native-core spike.
- Approach C was built and measured in part, then removed. Its numbers stay above in the spike note.
- The PIO spike showed a PIO blink keeps time through flash writes. It gates the backlight, so it hides the
  whole key. This task does not use it.
- Run `go run ./driver/cmd/macropadd` instead of a built binary. A binary built in the scratchpad exited with code 137.
