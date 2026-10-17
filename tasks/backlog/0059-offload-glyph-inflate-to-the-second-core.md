---
id: "0059"
title: "Offload custom-glyph inflate to the RP2350's second core"
status: "backlog"
created: "2026-10-17"
updated: "2026-10-17"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0058", "0045", "0048", "0057"]
tags: ["firmware", "performance", "multicore", "circuitpython"]
---

# 0059 — Offload custom-glyph inflate to the RP2350's second core

## Problem

A six-key layer redraw takes 0.8 s after task 0058. The board does all the work for one key on one core, in series: receive, inflate, `_compose`, push. One key takes about 140 ms. The board has a second core that does nothing. CircuitPython 10.2.1 has no `_thread`, `multiprocessing`, or core-1 module for this board, so Python cannot reach it.

## Goals

- Core 1 inflates a type 6 glyph stream while core 0 composes the previous key.
- Six keys with the icons from `assets/` take at most 0.65 s on the board, from 0.80 s to 0.82 s today.
- Stock CircuitPython still runs the firmware, at the 0.8 s speed of task 0058.

## Non-goals

- A glyph cache on the board.
- Key state over CDC, and the 150 ms HID report gap.
- Moving the SPI push. Task 0045 already does it by DMA.
- Any change to `docs/wire-protocol.md`.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — A custom CircuitPython build with a core-1 module

Build CircuitPython 10.2.1 for `pimoroni_pico_plus2` with a native module. The module starts core 1 with the Pico SDK call `multicore_launch_core1`. Core 1 runs inflate on a buffer that Python hands over, and core 0 polls for the result.

- Good, because inflate leaves core 0. A key then costs about 90 ms on core 0 (estimate: 10 ms receive, 75 ms compose, 5 ms push), against 140 ms today.
- Good, because the firmware stays in Python. `wire.py`, `app.py`, and `display_render.py` keep their structure.
- Bad, because the project must build and flash its own CircuitPython. It carries that patch through every CircuitPython upgrade.
- Bad, because core 1 must stay safe during a flash write, must use only buffers that Python keeps alive, and shares the slow PSRAM with core 0.

### Approach B — Move the firmware to MicroPython

MicroPython's `rp2` port runs `_thread` on core 1. A thread would inflate while the main loop composes.

- Good, because the second core needs no C code and no custom build of the interpreter.
- Good, because it is a supported feature of the port, not a project patch.
- Bad, because the firmware uses CircuitPython modules that MicroPython does not have: `displayio.Bitmap`, `bitmaptools`, `usb_hid`, `usb_cdc`, and `rp2pio`. This is a rewrite of most of `firmware/`.
- Bad, because it is not known whether a MicroPython thread runs Python in parallel with the main loop. The port may hold a global lock.

### Approach C — Cut the serial work on core 0

Use no second core. The driver blends the key color into the glyph on the host, so every glyph is opaque. The board then skips the fill and blit in `_compose` for it, which is about 57 ms of the 75 ms.

- Good, because it needs no new build. The change is in `driver/layers/render.go` and `firmware/display_render.py`.
- Good, because the estimate is close to Approach A: about 85 ms per key (10 ms receive, 45 ms inflate, 30 ms build).
- Bad, because a change of key color needs a new glyph, so the host resends one. The layer controller already does this.
- Bad, because blink on an opaque glyph hides the whole image, not only the background. Task 0041 chose the current blink behavior.

## Decision

Chosen: **Approach A — a custom CircuitPython build with a core-1 module**, after a spike.

The owner asked for the second core, and only this approach removes inflate from the critical path of core 0. The cost accepted: the project maintains a patched CircuitPython. The spike decides it. If the spike shows less than 40 ms saved per key, stop and do Approach C in its own task.

## Design

Measure first. The spike builds CircuitPython with a module `glyphcore` that has one call: `submit(stream, out)` starts an inflate on core 1, and `poll()` returns the inflated length, or `None` while it runs. The spike records the time of one 32 KB inflate on core 1, and the effect on the loop of core 0.

After the spike passes:

- `firmware/wire.py` calls `glyphcore.submit` for a type 6 frame, and returns to the loop. A later `feed` call collects the glyph.
- A glyph that core 1 does not finish within a set time is dropped, and the key keeps its image.
- When `import glyphcore` fails, `wire.py` uses `zlib.decompress` on core 0, as task 0058 does.

Files to change:

- `firmware/wire.py` — hand the stream to core 1, with the fallback.
- `firmware/app.py` — pass the pending glyph through the loop.
- `tools/` — a script that builds the patched CircuitPython.
- `firmware/README.md` — the build recipe, the flash steps, and the figures.
- `test/test_wire.py`, `test/stubs/` — a fake `glyphcore`.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The spike inflates one 32 KB glyph on core 1 and returns pixels equal to `zlib.decompress` on core 0. **Proof:** the figures in this spec's Notes, and the build recipe in `firmware/README.md`.
- [ ] **DoD-2** — The spike records the time of one inflate on core 1, and the time one key costs core 0 with it. **Proof:** this spec's Notes, with the trace file of `blinksend --scenario=icons`.
- [ ] **DoD-3** — Six keys with `blinksend --scenario=icons` take at most 0.65 s on the patched build. **Proof:** the line `six keys in N s`, in three runs, each with N at most 0.65.
- [ ] **DoD-4** — When `import glyphcore` fails, the firmware inflates on core 0, and the glyph is the same. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a test for each path.
- [ ] **DoD-5** — When core 1 does not answer within the set time, the board drops the glyph, the key keeps its image, and the loop goes on. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a fake module that never answers.
- [ ] **DoD-6** — Three runs of `blinksend --scenario=icons` in a row report no `TRACE_DROPPED` record and no error. **Proof:** the three JSONL trace files.
- [ ] **DoD-7** — `firmware/README.md` records the build recipe, the flash steps, and the figures of DoD-2 and DoD-3. **Proof:** `firmware/README.md`.
- [ ] **DoD-8** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass.
- [ ] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- Core 1 holds off a flash write, or a flash write halts core 1 → run core-1 code from RAM, and use the SDK lockout calls. Test with `make flash` while the board runs.
- Core 1 reads a buffer that Python's garbage collector frees → keep one preallocated buffer for the whole run, and never pass a temporary.
- Both cores wait on the same slow PSRAM, so the second core gains less than the estimate → the spike measures it before the task builds anything else.

## Open questions

- [ ] Does the `rp2` port of CircuitPython 10.2.1 already use core 1? — the spike reads `ports/raspberrypi` and answers it.
- [ ] How long does one 32 KB inflate take on core 1, with core 0 busy in PSRAM? — DoD-2.

## Notes

Measured by task 0058 on 2026-10-17, on a Pico Plus 2 with CircuitPython 10.2.1:

- Six keys with `icons`: 0.80 s to 0.82 s. One key takes about 140 ms: 70 to 80 ms of `_compose`, and 55 to 78 ms of receive and inflate.
- A 32 KB `zlib.decompress` took 32 to 65 ms in a probe. `bytes(32768)` alone took about 11 ms, so the heap is slow.
- The module list of the board page on `circuitpython.org` has no `_thread`, `multiprocessing`, or core-1 module. It lists `rp2pio`.
- The per-key times for Approaches A and C are estimates from these figures. None is measured.
