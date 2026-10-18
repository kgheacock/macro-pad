---
id: "0059"
title: "Offload glyph inflate to the second core, keep the frame pool off the Python heap, and resend lost key states"
status: "ongoing"
created: "2026-10-17"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/75"
branch: "0059-offload-glyph-inflate-to-the-second-core"
related: ["0058", "0060", "0061", "0044", "0045", "0048", "0057"]
tags: ["firmware", "driver", "performance", "multicore", "circuitpython", "memory", "reliability"]
---

# 0059 — Offload glyph inflate to the second core, keep the frame pool off the Python heap, and resend lost key states

## Problem

A six-key layer redraw takes 0.8 s after task 0058. The board does all the work for one key on one core, in series: receive, inflate, `_compose`, push. One key takes about 140 ms. The board has a second core that does nothing. CircuitPython 10.2.1 has no `_thread`, `multiprocessing`, or core-1 module for this board, so Python cannot reach it.

Task 0061 found two more limits. First, the garbage collector scans every live heap block, about 1 ms per 32 KB. With a pool of 128 slots on the heap, one collection takes 115 to 150 ms. That is longer than the 150 ms HID report gap. Second, the board holds one HID report, so a long pass lets the next report overwrite an unread one. The key state is lost, and a batch of task 0060 waits for it until the 2 s time-out. In a test, 2 of 9 batch runs timed out with 128 slots, and none of 9 without the pool.

## Goals

- Core 1 inflates a type 6 glyph stream while core 0 composes the previous key.
- Six keys with the icons from `assets/` take at most 0.65 s on the board, from 0.80 s to 0.82 s today.
- One garbage collection takes at most 10 ms with the 128-slot pool of task 0061 in use.
- A key state that the board loses is resent. A batch that lost one completes within 1.0 s of the last send, not 2 s.
- Stock CircuitPython still runs the firmware, at the 0.8 s speed of task 0058.

## Non-goals

- A glyph cache on the board. Task 0061 builds it. This task changes where its pool lives.
- Key state over CDC, and the 150 ms HID report gap.
- A resend for a plain `setKeyState` that is not part of a batch. The board cannot tell that one is missing.
- Moving the SPI push. Task 0045 already does it by DMA.
- Any change to `docs/wire-protocol.md`.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — A custom CircuitPython build with a core-1 module

Build CircuitPython 10.2.1 for `pimoroni_pico_plus2` with a native module. The module starts core 1 with the Pico SDK call `multicore_launch_core1`. Core 1 runs inflate on a buffer that Python hands over, and core 0 polls for the result. The module also owns the memory of the frame pool. It allocates the 128 slots of 32 KB outside the Python heap, so the garbage collector does not scan them, and it gives Python a view of each slot.

- Good, because inflate leaves core 0. A key then costs about 90 ms on core 0 (estimate: 10 ms receive, 75 ms compose, 5 ms push), against 140 ms today.
- Good, because a pool outside the heap takes the collection back to about 1 ms. A pool on the heap costs 115 to 150 ms for each collection at 128 slots, and 42 ms at 64 slots. This is an inference from the heap test, and the spike measures it.
- Good, because core 1 can inflate straight into a slot. Core 0 then makes no 32 KB result to collect later.
- Good, because the firmware stays in Python. `wire.py`, `app.py`, and `display_render.py` keep their structure.
- Bad, because the project must build and flash its own CircuitPython. It carries that patch through every CircuitPython upgrade.
- Bad, because compose runs into a bitmap that Python owns, so each stored frame costs a 32 KB copy into the pool, about 6 ms.
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
- Bad, because it does nothing about the collector. A 128-slot pool on the heap still costs 115 to 150 ms for each collection.

## Decision

Chosen: **Approach A — a custom CircuitPython build with a core-1 module**, after a spike.

The owner asked for the second core, and only this approach removes inflate from the critical path of core 0. The cost accepted: the project maintains a patched CircuitPython. The spike decides it. If the spike shows less than 40 ms saved per key and no gain from the pool outside the heap, stop and do Approach C in its own task.

The resend fix does not need the custom build. It ships first, on stock CircuitPython, because no pool size removes the loss: even 32 slots lost one report in 12 runs.

## Design

Measure first. The spike builds CircuitPython with a module `glyphcore`. `submit(stream, slot)` starts an inflate on core 1 into a slot of the pool, and `poll()` returns the inflated length, or `None` while it runs. `slot_view(slot)` gives a view of the slot's memory. The spike records three figures:

- the time of one 32 KB inflate on core 1, and the effect on the loop of core 0;
- the time of `gc.collect()` with 128 slots in module memory, to compare with 115 to 150 ms on the heap;
- the time to copy a composed 32 KB frame into a slot.

After the spike passes:

- `firmware/wire.py` calls `glyphcore.submit` for a type 6 frame, and returns to the loop. A later `feed` call collects the glyph.
- A glyph that core 1 does not finish within a set time is dropped, and the key keeps its image.
- When `import glyphcore` fails, `wire.py` uses `zlib.decompress` on core 0, as task 0058 does. The pool then lives on the heap, with 64 slots.

The resend fix, first and on stock CircuitPython:

- A new device-to-host frame, "Batch incomplete", carries the masks of the glyphs and key states that an open batch still waits for. The board sends it when 500 ms pass with no new data for the batch, and sends it again each 500 ms, up to three times.
- The host keeps the data of the last batch until the next flush. On "Batch incomplete" it sends the named items again.
- The 2 s time-out stays as the last resort.

Files to change:

- `firmware/wire.py` — hand the stream to core 1, with the fallback; the "Batch incomplete" frame.
- `firmware/app.py` — pass the pending glyph through the loop; send "Batch incomplete".
- `firmware/frame_cache.py` — take the slots from `glyphcore` when it exists (task 0061).
- `driver/transport/`, `driver/layers/controller.go` — keep the last batch, and resend on "Batch incomplete".
- `docs/wire-protocol.md` — the new frame, as the next free type.
- `tools/` — a script that builds the patched CircuitPython.
- `firmware/README.md` — the build recipe, the flash steps, and the figures.
- `test/test_wire.py`, `test/test_app.py`, `test/stubs/` — a fake `glyphcore`, and tests for the resend.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The spike inflates one 32 KB glyph on core 1 and returns pixels equal to `zlib.decompress` on core 0. **Proof:** the figures in this spec's Notes, and the build recipe in `firmware/README.md`.
  - Not confirmed: needs the board. `tools/glyphcore_spike.py` compares the pixels of core 1 with `zlib.decompress`; the host test `test/test_glyphcore_inflate.py` already compares the C inflater with `zlib` on 44 streams.
- [ ] **DoD-2** — The spike records the time of one inflate on core 1, the time one key costs core 0 with it, the time of `gc.collect()` with 128 slots in module memory, and the time of a 32 KB copy into a slot. **Proof:** this spec's Notes, with the trace file of `blinksend --scenario=icons`.
  - Not confirmed: needs the board: `make glyphcore-spike`, then `blinksend --scenario=icons`.
- [ ] **DoD-3** — Six keys with `blinksend --scenario=icons` take at most 0.65 s on the patched build. **Proof:** the line `six keys in N s`, in three runs, each with N at most 0.65.
  - Not confirmed: needs the board with the custom build.
- [x] **DoD-4** — When `import glyphcore` fails, the firmware inflates on core 0, and the glyph is the same. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a test for each path. `test_without_the_module_the_reader_inflates_on_core_0` and `test_the_glyph_from_core_1_has_the_same_pixels_as_from_core_0` cover the two paths, with `test/fake_glyphcore.py`. 368 tests pass.
- [x] **DoD-5** — When core 1 does not answer within the set time, the board drops the glyph, the key keeps its image, and the loop goes on. **Proof:** `.venv/bin/pytest test/test_wire.py` passes, with a fake module that never answers. `test_a_glyph_that_core_1_never_finishes_is_dropped_and_the_reader_goes_on` (`test/test_wire.py`) and `test_a_glyph_that_core_1_never_finishes_is_dropped_and_the_key_keeps_its_image` (`test/test_app.py`) use a fake module that never answers.
- [ ] **DoD-6** — Three runs of `blinksend --scenario=icons` in a row report no `TRACE_DROPPED` record and no error. **Proof:** the three JSONL trace files.
  - Not confirmed: needs the board with the custom build.
- [ ] **DoD-7** — `firmware/README.md` records the build recipe, the flash steps, and the figures of DoD-2 and DoD-3. **Proof:** `firmware/README.md`.
  - Not confirmed: `firmware/README.md` has the recipe and the flash steps; the figures of DoD-2 and DoD-3 are not measured yet.
- [ ] **DoD-8** — With the 128-slot pool in module memory, `gc.collect()` takes at most 10 ms. **Proof:** the probe line in `firmware/README.md`, in three runs.
  - Not confirmed: needs the board: `make glyphcore-spike` prints the median collection with 128 slots in module memory.
- [x] **DoD-9** — When a batch lacks a key state, the board sends "Batch incomplete" with the right mask, and the host sends that state again. **Proof:** `.venv/bin/pytest test/test_app.py` and `cd driver && go test ./layers -run Resend` pass, each with a test that drops one report. Task 0063 built the resend, so the proof names changed: `.venv/bin/pytest test/test_app.py -k batch_incomplete` (4 tests) and `cd driver && go test ./transport -run BatchIncomplete` pass. `go test ./layers -run Resend` finds no test, because the code is in `transport`.
- [ ] **DoD-10** — On the board, 15 runs of `blinksend --scenario=batch` with the 128-slot or 64-slot pool of task 0061 end with no `BATCH_TIMED_OUT` record, and each completes within 1.0 s of its last send. **Proof:** the 15 JSONL trace files.
  - Not confirmed: needs the board. It also needs the module pool in use, and the host `SlotCount` is still 64.
- [x] **DoD-11** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass. `.venv/bin/pytest test/ -q`: 368 passed. `cd driver && go test ./...`: all packages ok.
- [x] **DoD-12** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- Core 1 holds off a flash write, or a flash write halts core 1 → run core-1 code from RAM, and use the SDK lockout calls. Test with `make flash` while the board runs.
- Core 1 reads a buffer that Python's garbage collector frees → keep one preallocated buffer for the whole run, and never pass a temporary.
- Both cores wait on the same slow PSRAM, so the second core gains less than the estimate → the spike measures it before the task builds anything else.
- A pool outside the heap does not shorten the scan after all, for example if the module memory is scanned too → DoD-8 measures it. If it fails, the pool stays at 64 slots on the heap.
- A resend arrives when the board has already released the batch, and a key draws twice → the host sends an item again only for a mask the board named, and the board ignores a state for a closed batch.
- A pass longer than 150 ms outside a batch still loses a plain key state, silently → out of scope. The board cannot tell what it did not receive.

## Open questions

- [x] Does the `rp2` port of CircuitPython 10.2.1 already use core 1? — No for this board. Only `common-hal/picodvi/Framebuffer_RP2040.c` (RP2040 only) and `common-hal/usb_host/Port.c` (off by default) call `multicore_launch_core1`. `pico_multicore` is already compiled in (`LIB_PICO_MULTICORE=1`), so the module needs no change to the port.
- [ ] How long does one 32 KB inflate take on core 1, with core 0 busy in PSRAM? — DoD-2.
- [ ] Does module memory outside the heap escape the garbage collector scan, and can the DMA push read it directly? — the spike, DoD-2 and DoD-8.

## Notes

Measured by task 0058 on 2026-10-17, on a Pico Plus 2 with CircuitPython 10.2.1:

- Six keys with `icons`: 0.80 s to 0.82 s. One key takes about 140 ms: 70 to 80 ms of `_compose`, and 55 to 78 ms of receive and inflate.
- A 32 KB `zlib.decompress` took 32 to 65 ms in a probe. `bytes(32768)` alone took about 11 ms, so the heap is slow.
- The module list of the board page on `circuitpython.org` has no `_thread`, `multiprocessing`, or core-1 module. It lists `rp2pio`.
- The per-key times for Approaches A and C are estimates from these figures. None is measured.

Measured by task 0061 on 2026-10-18, with PR #72's firmware:

- `gc.collect()` with no pool took 0.7 ms (9.8 ms after 640 KB of garbage). With 8 slots it took 5.5 ms, with 32 slots 22 ms, with 64 slots 42 ms, and with 128 slots 115 to 150 ms.
- A pool of 128 `bytearray(32768)` cost the same as 128 `displayio.Bitmap` objects, about 115 ms. The collector scans every live heap block.
- `blinksend --scenario=batch`, runs that ended with `BATCH_TIMED_OUT`: 0 of 9 with no pool, 0 of 9 with 8 slots, 1 of 12 with 32 slots, 2 of 9 with 128 slots, and 1 of 15 with 64 slots and a collection only while idle. The first failure had payload `0x0400`: the report for key 2 never arrived.
- The 90th percentile of the compose step was 94 ms with no pool, 101 ms with 8 slots, 128 ms with 32 slots, and 155 ms with 64 slots and an idle-only collection. The maximum was 324 ms with 128 slots.

Built by task 0059 on 2026-10-18 (the board was not attached; the measurements are open):

- The module is a user C module (`tools/usermod/glyphcore/`), built with `USER_C_MODULES`. The tree of CircuitPython has no patch. Homebrew's `arm-none-eabi-gcc` 16.2 has no newlib, so the build uses the Arm GNU toolchain 14.3.rel1. The stock build and the glyphcore build both link: 878,996 and 885,112 of 1,044,480 bytes of flash.
- `objdump` of `firmware.elf`: `core1_main`, the inflater, and the flash wrappers are in RAM. The only call that leaves them is `memset`, also in RAM. Every caller of `flash_range_erase` and `flash_range_program` in the port goes through the wrapper.
- The pool uses `SLOT_COUNT = 64` on the board and on the host, as task 0063 set. `glyphcore.alloc(128)` works, and the spike measures `gc.collect()` with 128 slots. A pool of 128 in use needs a host `SlotCount` of 128 and a way for the host to learn the size, which this task does not add.
- Design choice: the stream is copied into the module (one 32 KB buffer), so the reader frees its frame buffer at once and reads the next frame while core 1 inflates. Each key has two glyph buffers that alternate, and `glyph_view` returns a new `memoryview` on each call, because `display_render` rebuilds a key's frames only when `key_state.pixels` is a different object.
