---
id: "0047"
title: "Capture mic audio with PIO and DMA, so no pause in the main loop drops samples"
status: "backlog"
created: "2026-10-06"
updated: "2026-10-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0005", "0007", "0031", "0045"]
tags: ["firmware", "audio", "pio", "dma"]
---

# 0047 — Capture mic audio with PIO and DMA, so no pause in the main loop drops samples

## Problem

The firmware has a mic capture module (`firmware/mic_capture.py`) and a ring buffer (`firmware/audio_buffer.py`).
Nothing in `firmware/app.py` calls them yet. Three defects will show when they are wired in.

`audiobusio.I2SIn.record` fills the buffer that the caller passes, then returns. Nothing captures between calls,
so any pause in the loop drops samples. A color change takes 58 ms, and a flash write takes 330 ms to 590 ms.
`RingBuffer.write` and `read_chunk` loop over every byte in Python. At 16 kHz and 16 bits, that is 32,000 bytes a
second. An empty loop costs 1.8 µs a pass on this board (16,384 passes take 30 ms), so each pass over the stream
costs at least 58 ms for each second of audio, before any real work. Both loops share the CPU with the blinks.

## Goals

- A held key captures audio with no dropped samples, while other keys blink, update, and persist.
- The main loop spends at most 1 ms to hand one 512-byte chunk to the CDC writer.
- The Audio chunk message does not change. `driver/` needs no change.

## Non-goals

- A second core for the firmware. Approach B covers it, and this task does not build it.
- Audio compression, a sample rate other than 16 kHz, or a stereo format.
- The mic wiring. Task 0010 covers it. The breakout is listed as "Needed" in `hardware/README.md`.

## Approaches considered

Three approaches follow. Each one moves the real-time work to a different place.

### Approach A — PIO I2S receive with a looping DMA ring

A PIO state machine drives BCLK and WS, and shifts in the mic data. A looping DMA transfer
(`StateMachine.background_read`) fills one ring in RAM, with no CPU. The loop reads from the ring in slices.

- Good, because the hardware does the real-time part. A 330 ms pause costs nothing if the ring is large enough.
  A 64 KB ring holds 2 s.
- Good, because it runs on stock CircuitPython 10.2.1. `rp2pio` imports on this board, and the per-byte loops go away.
- Bad, because the project must write and keep a PIO I2S program, and assemble it on the host.
  It replaces the `audiobusio` API, and it must match the exact format of the chosen mic.
- Bad, because the main loop still copies each chunk to the CDC writer. A flash write delays that copy by up to 50 ms.

### Approach B — A capture service on core 1, in a custom CircuitPython build

A C module starts core 1. Core 1 captures and chunks the audio from RAM, and Python reads finished chunks.

- Good, because Python never touches a sample, and a flash write cannot stall core 1.
- Good, because it is the first service of a general second-core host, which other work could reuse.
- Bad, because the project must own a CircuitPython fork. The 2026-10-05 spike hit an intermittent hard fault.
- Bad, because nobody has shown that core 1 and CircuitPython's flash code can run together.

### Approach C — Keep `I2SIn.record`, call it in small slices

`MicCapture.poll` records 64 samples (4 ms) on each step into a ring that uses `memoryview` slices.

- Good, because it is the smallest change. It needs no PIO program and no new hardware code.
- Good, because it keeps the tested `AudioSourceLike` interface and the existing stubs.
- Bad, because `record` blocks for its 4 ms, so each step waits. Blinks and switch scans wait with it.
- Bad, because nothing captures during any pause, so a 330 ms flash write drops 330 ms of audio.

## Decision

Chosen: **Approach A — PIO I2S receive with a looping DMA ring**.

The goal asked for core 1, but audio does not need it: DMA already gives lossless capture. Approach A fixes both
defects with stock firmware. The decision accepts a hand-written PIO program and a 50 ms copy delay after a flash
write. Approach B stays open for work that needs the CPU on core 1. This task shares its PIO stub with 0045.

## Design

`firmware/pio_i2s.py` holds the PIO program, assembled on the host, and `I2SCapture`. It owns the state machine
and a 64 KB ring. `I2SCapture.start()` runs the looping `background_read`. `drain(max_bytes)` returns a
`memoryview` slice of the new bytes, and counts an overrun when the DMA passes the read position.

`MicCapture` calls `drain` once on each step, with no blocking read. `RingBuffer` and its per-byte loops are
replaced by slice copies. `chunk_stream` keeps its rules for the final-chunk flag. `MacroPad.step` calls
`start` on a press, `stop` on a release, and then writes the chunks as Audio chunk frames.

Files to change:

- `firmware/pio_i2s.py` — new: PIO program, `I2SCapture`
- `firmware/audio_buffer.py` — slice-based ring on the DMA buffer
- `firmware/mic_capture.py` — use `I2SCapture`, no blocking `record`
- `firmware/app.py`, `firmware/code.py` — build the capture, call it on press and release
- `test/stubs/rp2pio.py`, `test/test_mic_capture.py`, `test/test_audio_buffer.py` — stub and tests
- `tools/audio_check.py`, `Makefile` — `make audio-check`
- `firmware/README.md` — record the design and the measured numbers

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The drain of a 512-byte chunk takes 1 ms or less on the board.
  **Proof:** `make audio-check` prints `drain_ms` of 1 or less
- [ ] **DoD-2** — Chunks come out in order. The last chunk after a release carries the final flag, and the stream
  ID is the same for every chunk of one recording.
  **Proof:** `python3 -m pytest test/test_mic_capture.py -k chunks_in_order`
- [ ] **DoD-3** — When the reader falls behind by more than the ring size, the capture counts an overrun and the
  next chunk starts at the oldest kept byte.
  **Proof:** `python3 -m pytest test/test_audio_buffer.py -k overrun`
- [ ] **DoD-4** — A 10 s capture of a 1 kHz test tone, with a 50 ms `nvm` write and a color change in the middle,
  loses no samples and decodes at 1000 Hz ±1.
  **Proof:** `make audio-check` prints `dropped 0` and `tone_hz` between 999 and 1001
- [ ] **DoD-5** — With a capture running, six keys blink and an update to key 4 every 2 s, and no gap between
  `REFRESH_DONE` records of one blinking key exceeds the limit of task 0044.
  **Proof:** `make blink-trace SCENARIO=single` while `make audio-check` runs, both print their limits
- [ ] **DoD-6** — The driver reassembles a captured recording through the existing Audio chunk path.
  **Proof:** `cd driver && go test ./transport -run Audio`
- [ ] **DoD-7** — `firmware/README.md` records the ring size, the PIO format, and the measured drain time.
  **Proof:** `firmware/README.md`, section "Audio capture"
- [ ] **DoD-8** — The PR in the `pr` field links to this spec.
  **Proof:** the PR body

## Risks

- The mic breakout is not in hand, so DoD-4 needs a tone source → a second PIO state machine can drive the
  pins as a loopback, or the real breakout can replace it. The task cannot close without one of them.
- The SPH0645 and ICS-43434 differ in slot format and bit depth → pick the part first, and keep the format in one constant.
- `background_read` may not loop on a `bytearray` of this size, or may need RAM and not PSRAM → a 5-minute spike
  checks it before the PIO program is written.
- The claim that `I2SIn.record` captures only while it runs comes from the API, not from a measurement on this board.

## Open questions

- [ ] Which mic: SPH0645 or ICS-43434? — owner
- [ ] Is 16 kHz, 16 bits, mono right? The stub and the wire doc imply it, but no document sets it. — owner

## Notes

- Today the capture modules are not wired into `firmware/app.py`. Tasks 0005 and 0007 are still in `tasks/ongoing/`.
- Spike results, 2026-10-06, RP2350, CircuitPython 10.2.1: `rp2pio` and `audiobusio` import. `_thread` does not.
  A PIO program kept its timing through a 158 ms `nvm` write and a 262 ms file write.
- Approach B would also remove the copy delay after a flash write, because code on core 1 that runs from RAM keeps
  going while flash is busy. The delay is at most 50 ms, and the 2 s ring absorbs it.
