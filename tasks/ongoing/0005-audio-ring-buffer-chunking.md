---
id: "0005"
title: "Add the audio ring buffer and chunking logic"
status: "ongoing"
created: "2026-08-03"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/26"
branch: "0005-audio-ring-buffer-chunking"
related: ["0001", "0002"]
tags: ["firmware", "audio"]
---

# 0005 — Add the audio ring buffer and chunking logic

## Problem

`firmware/README.md` commits to buffering mic audio in a ring buffer while
a key is held, then streaming it to the host in fixed-size chunks with a
final-chunk flag, but no buffering or chunking logic exists.

## Goals

- A ring buffer accepts written samples and drains in fixed-size chunks.
- The final chunk of a recording carries a flag, matching
  `firmware/README.md` and the format from task 0002.
- The module has unit tests that use synthetic sample data. It needs no
  I2S or mic hardware.

## Non-goals

- Reading real I2S audio. Task 0007 covers that, once hardware exists.
- Audio compression or format conversion. `firmware/README.md` asks for
  raw buffered PCM chunks only.

## Approaches considered

### Approach A — Fixed-capacity ring buffer, drain to chunks

A circular byte buffer exposes `write(samples)` and `read_chunk(size)`. The
caller marks the final chunk when the key releases and the buffer drains
empty.

- Good, because memory use stays bounded, matching the "ring buffer"
  language in `firmware/README.md`.
- Good, because the chunk size decouples from the buffer size, so task
  0002's wire-protocol chunk size can change on its own.
- Bad, because a host that drains too slowly loses unread samples to
  overwrite, an audio-loss failure mode.
- Bad, because the buffer capacity is a sizing decision made without
  hardware to measure real memory headroom against.

### Approach B — Unbounded list buffer

Append samples to a growing list while the key is held, then chunk the
whole list on release.

- Good, because it never drops samples, since nothing gets overwritten.
- Good, because it needs no wraparound math.
- Bad, because unbounded growth risks running out of memory on a
  resource-constrained MCU during a long key hold — the exact risk a ring
  buffer avoids.
- Bad, because it contradicts `firmware/README.md`'s explicit choice of a
  ring buffer.

### Approach C — Stream directly to host, no local buffer

Send each I2S sample block to the host as it arrives. Skip local buffering.

- Good, because it uses minimal RAM, with no buffer to size or manage.
- Good, because the code path is the simplest of the three.
- Bad, because it contradicts `firmware/README.md`, which buffers before
  streaming, likely to smooth over USB transfer timing gaps.
- Bad, because it ties audio capture timing directly to USB transfer
  timing, so a USB stall drops audio instead of queuing it.

## Decision

Chosen: **Approach A — Fixed-capacity ring buffer, drain to chunks**.

It matches `firmware/README.md`'s explicit ring-buffer design and bounds
memory use on a constrained MCU. This choice accepts that a host too slow
to drain the buffer loses the oldest unread audio.

## Design

`firmware/audio_buffer.py` exposes `RingBuffer(capacity_bytes)` with
`write(bytes)`, `read_chunk(chunk_size) -> bytes`, and an `is_empty`
property. A helper, `chunk_stream(buffer, released, chunk_size=DEFAULT_CHUNK_SIZE)`,
yields `(chunk, is_final)` pairs that match the audio-chunk format from
task 0002. `DEFAULT_CHUNK_SIZE` is a module constant, sized against the
2-byte `Length` field in `docs/wire-protocol.md`'s frame header — see
DoD-4.

Files to change:

- `firmware/audio_buffer.py` — new
- `test/test_audio_buffer.py` — new, following this repo's existing
  `test/` convention (`pyproject.toml`'s `testpaths`), not a new `tests/`
  directory

## Definition of done

- [ ] **DoD-1** — `read_chunk()` returns exactly `chunk_size` bytes when
  enough data is buffered. **Proof:**
  `pytest test/test_audio_buffer.py::test_full_chunk`
- [ ] **DoD-2** — The last chunk of a drained buffer carries the final
  flag. **Proof:** `pytest test/test_audio_buffer.py::test_final_chunk_flag`
- [ ] **DoD-3** — Writing past capacity overwrites the oldest unread bytes,
  not the newest. **Proof:**
  `pytest test/test_audio_buffer.py::test_overwrite_oldest`
- [ ] **DoD-4** — `DEFAULT_CHUNK_SIZE` is wired in as `chunk_stream`'s
  actual default, and stays under the payload ceiling that
  `docs/wire-protocol.md`'s 2-byte frame `Length` field allows once the
  1-byte stream ID and 1-byte final-chunk flag are subtracted (65535 - 2
  = 65533 bytes). **Proof:** `firmware/audio_buffer.py`'s
  `DEFAULT_CHUNK_SIZE` is `chunk_stream`'s default `chunk_size` value,
  and is ≤ 65533; `pytest
  test/test_audio_buffer.py::test_chunk_stream_defaults_to_default_chunk_size`
- [ ] **DoD-5** — The new tests fail on `main`. **Proof:**
  `git stash -u && pytest test/test_audio_buffer.py` fails
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field
