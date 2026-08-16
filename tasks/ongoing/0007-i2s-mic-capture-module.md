---
id: "0007"
title: "Add the I2S mic capture module"
status: "ongoing"
created: "2026-08-03"
updated: "2026-08-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/30"
branch: "0007-i2s-mic-capture-module"
related: ["0001", "0002", "0003", "0005"]
tags: ["firmware", "audio"]
---

# 0007 — Add the I2S mic capture module

## Problem

`firmware/README.md` commits to capturing mic audio through I2S
(`audiobusio.I2SIn`) into the ring buffer while a key is held, but no
capture code exists, and the mic breakout is not in hand yet.
`hardware/README.md` marks it "Needed".

## Goals

- A capture module wraps `audiobusio.I2SIn` and feeds audio into task
  0005's ring buffer while a key is held.
- The start-on-press, stop-on-release control logic is testable against a
  fake I2S source.
- The module is ready to take a real `audiobusio.I2SIn` object once the
  mic breakout arrives.

## Non-goals

- The real I2S wiring, sample-rate tuning, or gain calibration against the
  physical mic. Task 0010 covers that, once hardware exists.
- Choosing the mic part, SPH0645 or ICS-43434. `hardware/README.md` leaves
  that open.

## Approaches considered

### Approach A — Inject an I2S source interface

Define a small `AudioSourceLike` protocol (`readinto(buffer) -> int`) that
both a real `audiobusio.I2SIn` and a test fake satisfy. The capture module
calls only that interface.

- Good, because the same start and stop control logic runs in tests and on
  real hardware.
- Good, because it repeats the pattern already chosen in task 0006, which
  keeps the codebase consistent.
- Bad, because `audiobusio.I2SIn`'s exact read behavior, blocking or
  buffered, cannot be checked until hardware exists, so the fake may model
  it wrong.
- Bad, because the sample-rate and bit-depth assumptions are guessed now,
  and may need revision once a mic part is chosen.

### Approach B — Full audio pipeline simulation

Build a fake I2S source that generates synthetic waveforms, such as sine
tones, and assert on captured sample values, not only call counts.

- Good, because it catches sample-format bugs, such as endianness or bit
  depth, earlier than a call-count-only test.
- Good, because it gives a repeatable fixture for a future audio-quality
  regression test.
- Bad, because it adds real fixture complexity for a benefit that stays
  speculative until the real mic's output format is confirmed.
- Bad, because it risks encoding a wrong assumption, such as bit depth,
  that needs rework once real hardware defines the true format.

### Approach C — Defer all capture code until the mic breakout arrives

Write no capture code now. Wait for task 0010.

- Good, because it carries zero risk of rework from a guessed interface or
  sample format.
- Good, because it spends no time on speculative design before the part is
  in hand.
- Bad, because it blocks the capture module and its start and stop logic
  until a part arrives, again defeating this plan's purpose.
- Bad, because it leaves press and release-triggered start and stop logic,
  which needs no real I2S data, untested for no reason.

## Decision

Chosen: **Approach A — Inject an I2S source interface**.

It repeats the display module's proven pattern from task 0006 and lets
start and stop control logic get tested now, isolating only the true
hardware dependency: real I2S sample data. This choice accepts that
sample-rate and format assumptions may need revision once the mic part is
chosen.

## Design

`firmware/mic_capture.py` exposes
`MicCapture(source: AudioSourceLike, buffer: RingBuffer)` with `start()`
and `stop()`, called from the debounced press and release events in task
0003. `AudioSourceLike` is a `Protocol` with `readinto(buffer) -> int`.
Tests use a `FakeAudioSource` that yields synthetic byte sequences.

Files to change:

- `firmware/mic_capture.py` — new
- `tests/test_mic_capture.py` — new, uses a `FakeAudioSource` test double

## Definition of done

- [ ] **DoD-1** — `start()` begins writing source data into the ring
  buffer. **Proof:**
  `pytest tests/test_mic_capture.py::test_start_writes_buffer`
- [ ] **DoD-2** — `stop()` halts writes and leaves already-buffered data
  intact. **Proof:**
  `pytest tests/test_mic_capture.py::test_stop_halts_writes`
- [ ] **DoD-3** — Calling `start()` twice without a `stop()` between is a
  no-op the second time. **Proof:**
  `pytest tests/test_mic_capture.py::test_double_start_noop`
- [ ] **DoD-4** — `AudioSourceLike` lists only methods present on
  `audiobusio.I2SIn`. **Proof:** `firmware/mic_capture.py` docstring cites
  the driver method names
- [ ] **DoD-5** — The new tests fail on `main`. **Proof:**
  `git stash && pytest tests/test_mic_capture.py` fails
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- The mic part is not yet chosen (`hardware/README.md`) → sample-rate and
  format assumptions may need revision once that line item resolves.
- `AudioSourceLike` may not match the real `audiobusio.I2SIn` API → task
  0010 re-validates.
