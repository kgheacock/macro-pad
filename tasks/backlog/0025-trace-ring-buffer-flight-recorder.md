---
id: "0025"
title: "Record a device trace ring buffer into a host JSONL flight recorder"
status: "backlog"
created: "2026-08-14"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0020", "0021", "0022", "0024"]
tags: ["firmware", "driver", "observability", "dx"]
---

# 0025 — Record a device trace ring buffer into a host JSONL flight recorder

## Problem

When a key does the wrong thing, nothing records what the firmware saw.
The only device→host signal is the debounced event. A rejected bounce, a
render that never ran, and a decoded host message all leave no trace.

`firmware/boot.py` calls `usb_cdc.enable(console=False, data=True)`, so
`print` has no channel to the host. A person debugging the board today has
the display and nothing else.

## Goals

- The firmware records a timestamped record at named points in the loop.
- Records reach the host on the existing CDC data channel, framed by task
  0024.
- The driver writes every device→host message to one JSONL file, with both
  the device time and a host wall-clock time.
- A reader orders device records and host records on one timeline.
- A record dropped because the host was not reading is counted and
  reported, never silently lost.
- Tracing is off by default. When off it allocates nothing in the loop.

## Non-goals

- OpenTelemetry, OTLP, and a collector. The device has no IP transport and
  no wall clock, so no OTel SDK can run on it.
- PCM payload bytes in the log. The log records a chunk's length only.
- Storage on the device across a reboot. Writing to `CIRCUITPY` needs
  `storage.remount`, which conflicts with the flash target in task 0019.
- Log rotation and retention.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Turn the CDC console back on and use `print`

`boot.py` sets `console=True`. The firmware calls `print`. The driver
opens the second serial port and timestamps each line.

- Good, because the firmware needs no new code. Every module can call
  `print` today.
- Good, because a person reads the log in any terminal emulator, with no
  driver running.
- Bad, because formatting a string allocates on every record. The
  resulting GC pause lands in the press path that task 0022 measures.
- Bad, because `print` blocks when the host does not drain the console
  FIFO. An unattached board then stalls the loop instead of dropping.

### Approach B — A preallocated ring buffer drained as trace frames

`firmware/trace.py` holds a fixed `bytearray` sized at import. `record`
writes a fixed-width record by index. `step` drains the buffer to the CDC
data channel as trace frames.

- Good, because the buffer is allocated once, so a record costs no
  allocation and adds no GC pause to the loop.
- Good, because a full buffer drops the oldest record and counts it, so
  the loop never blocks on an unattached host.
- Bad, because a record is a fixed-width struct. A new field is a protocol
  change that updates firmware and driver together.
- Bad, because a raw serial dump shows binary. Reading the log needs the
  driver.

### Approach C — Log on the host only, from the messages already sent

The driver logs each event and chunk with its arrival time and derives the
device state from that sequence. The firmware does not change.

- Good, because it needs no firmware change, no buffer, and no protocol
  change, so it runs against `transport.Emulator` today.
- Good, because it costs the firmware loop nothing at all.
- Bad, because it records only what already crosses the wire. A rejected
  bounce leaves no trace, and that is the bug class this task exists for.
- Bad, because arrival time is not event time. USB scheduling and host
  buffering add jitter that the log then blames on the firmware.

## Decision

Chosen: **Approach B — a preallocated ring buffer drained as trace
frames**.

The bugs that motivate this task happen when nothing crosses the wire, so
Approach C cannot see them. Task 0022 measures the loop period, so
tracing must not allocate inside the loop, which rules out Approach A.

The cost accepted is a fixed-width record: adding a field changes
`docs/wire-protocol.md` and both sides. The driver still logs every
message it receives, so Approach C's value is included in B's host half.

## Design

`firmware/trace.py` holds `Tracer(capacity, enabled=False)`. It allocates
one `bytearray` of `capacity * 12` bytes at construction. `record(code,
key, payload, now_us)` writes 12 bytes by index: code `uint8`, key
`uint8`, payload `uint16`, timestamp `uint64` monotonic microseconds. When
disabled, `record` returns at once.

A full buffer overwrites the oldest record and increments `dropped`.
`drain(write)` emits a `TRACE_DROPPED` record carrying that count first,
then the held records, then resets the count.

`MacroPad.step` in task 0022 takes an optional `Tracer` and records at
four points: host message decoded, switch read, debounce verdict, and
event written.

On the host, `driver/recorder` wraps a `Transport`. It calls `ReadMessage`
and writes one JSON object per line: the message type, its fields, the
device timestamp, and a host wall-clock time.

Device time is monotonic since boot, so the two clocks need an offset. The
recorder estimates it as the minimum of `host_arrival - device_us` over
every record seen. The minimum is the sample with the least one-way delay,
so it bounds the offset from one side and improves as records arrive. The
first line of the file is a header holding the estimate, the sample count,
and the estimator name.

Files to change:

- `firmware/trace.py` — new. `Tracer`, `record`, `drain`, `dropped`
- `firmware/app.py` — accept a `Tracer`, record at four points
- `docs/wire-protocol.md` — trace record type in the task 0024 registry,
  plus the trace code registry
- `driver/transport/wire.go` — encode and decode `TraceRecord`
- `driver/recorder/recorder.go` — new. JSONL writer and clock estimator
- `test/test_trace.py`, `driver/recorder/recorder_test.go` — new

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — `Tracer.record` allocates no memory after construction.
  **Proof:** `pytest test/test_trace.py::test_record_allocates_nothing`,
  which compares `tracemalloc` snapshots across 1000 calls
- [ ] **DoD-2** — A `Tracer` of capacity 4 that takes 6 records holds the
  last 4, reports `dropped == 2`, and emits `TRACE_DROPPED` with payload
  `2` as the first drained record. **Proof:**
  `pytest test/test_trace.py::test_drop_oldest_counts`
- [ ] **DoD-3** — A disabled `Tracer` drains zero records after 100
  `record` calls. **Proof:** `pytest test/test_trace.py::test_disabled`
- [ ] **DoD-4** — A press through `MacroPad.step` produces the switch
  read, the debounce verdict, and the event write, in that order.
  **Proof:** `pytest test/test_app.py::test_press_trace_order`
- [ ] **DoD-5** — The recorder writes one JSON line per message, each with
  a `device_us` and a `host_time` field. **Proof:**
  `go test ./driver/recorder/ -run TestRecorderJSONL` against a golden file
- [ ] **DoD-6** — The file's first line names the clock offset, the sample
  count, and the estimator. **Proof:** the golden file in
  `driver/recorder/testdata/`
- [ ] **DoD-7** — The new tests fail on `main`. **Proof:**
  `git stash && pytest test/ && go test ./driver/...` fails
- [ ] **DoD-8** — `docs/wire-protocol.md` states the 12-byte record layout
  and the trace code registry. **Proof:** `docs/wire-protocol.md`, section
  "Trace record"
- [ ] **DoD-9** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- Draining a full buffer costs one write per record and can lengthen a
  loop step. → Cap the records drained per `step` and count the rest as
  dropped.
- The two clocks drift, so one offset taken at open decays over a long
  session. → The minimum estimator re-runs on every record, so a drifting
  offset moves with it.
- Tracing changes the timing it measures. → DoD-3 proves the disabled path
  is free, so a measurement run can compare traced against untraced.

## Open questions

- [ ] Does the offset need a host→device ping for a two-sided bound? A
  ping is a new HID message, because key state is the only one today. —
  answered after the first hardware run in task 0010
- [ ] What capacity does the ring buffer need to hold one press at the
  loop period that task 0022 measures? — answered by task 0022 DoD-6

## Notes

OpenTelemetry was the starting question. It does not reach the device. No
OTLP transport exists over USB, and CircuitPython has no OTel SDK. A span
also needs an epoch timestamp, which a board with no RTC cannot produce. The Go
driver could export OTel spans once tasks 0011 to 0017 add cross-process
work worth tracing. That is a separate task, and it reads this JSONL file
rather than replacing it.
