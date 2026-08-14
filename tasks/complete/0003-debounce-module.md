---
id: "0003"
title: "Add a debounce module for switch input"
status: "complete"
created: "2026-08-03"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/6"
branch: "0003-debounce-module"
related: ["0001"]
tags: ["firmware"]
---

# 0003 — Add a debounce module for switch input

## Problem

`firmware/README.md` commits to a 5 ms to 10 ms debounce window on switch
input, but no debounce logic exists, and it cannot run on real hardware
yet.

## Goals

- A pure function or class takes raw GPIO transitions and timestamps, and
  emits debounced press and release events.
- The debounce window is a configurable parameter, not a fixed constant.
- The module has unit tests that use a fake clock. It needs no board.

## Non-goals

- Reading the real GPIO pin. Tasks 0009 and 0010 cover that.
- Classifying a single, double, or long press. `firmware/README.md` places
  that on the host driver.

## Approaches considered

### Approach A — Time-window debounce

On each raw transition, ignore further transitions until N milliseconds
pass since the last accepted one.

- Good, because the algorithm is simple to read and to test.
- Good, because it matches the single-switch, no-ghosting design in
  `hardware/README.md`.
- Bad, because a long window can delay a fast legitimate double-press,
  which the host later needs to classify.
- Bad, because it does not filter contact bounce that outlasts the window.

### Approach B — Majority-vote sampling

Sample the pin every 1 ms. Accept a state change only when N consecutive
samples agree.

- Good, because it resists a noisy, slow-settling contact better than a
  single time window.
- Good, because the sample count gives a second tuning knob beyond time.
- Bad, because it needs a periodic sample loop, which costs more code and
  more CPU time than an edge-triggered approach.
- Bad, because its latency equals the sample count times the interval,
  which must still fit the 5 ms to 10 ms budget from `firmware/README.md`.

### Approach C — Hardware RC debounce

Add an RC filter on each switch line so the GPIO signal arrives clean.

- Good, because it costs zero firmware CPU time.
- Good, because it removes debounce as a software concern.
- Bad, because it needs a hardware change not yet designed, which conflicts
  with this plan's premise that hardware is not ready.
- Bad, because the window is fixed in hardware. Firmware alone cannot tune
  or fix it later.

## Decision

Chosen: **Approach A — Time-window debounce**.

It matches the exact behavior `firmware/README.md` already commits to, and
it needs no timer loop, which keeps the module a pure, easily-tested
function. This choice accepts that very long, slow bounces outside the
window pass through unfiltered.

## Design

`firmware/debounce.py` exposes `Debouncer(window_ms)` with a method
`feed(pin_state, timestamp_us)` that returns `None`, `"press"`, or
`"release"`. The module imports no CircuitPython module — timestamps are
passed in by the caller.

Files to change:

- `firmware/debounce.py` — new
- `tests/test_debounce.py` — new, uses task 0001's test harness

## Definition of done

- [x] **DoD-1** — `feed()` ignores a second transition inside the
  configured window, and accepts one after it. **Proof:**
  `pytest tests/test_debounce.py::test_within_window_ignored`
- [x] **DoD-2** — A press, then a release after the window, emits both
  events in order. **Proof:**
  `pytest tests/test_debounce.py::test_press_release_sequence`
- [x] **DoD-3** — The window is configurable, and defaults inside 5 ms to
  10 ms, per `firmware/README.md`. **Proof:** `firmware/debounce.py`,
  `Debouncer.__init__` default value
- [x] **DoD-4** — The new tests fail on `main`. **Proof:**
  `git stash && pytest tests/test_debounce.py` fails
- [x] **DoD-5** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- The chosen window value is untested against a real switch → task 0010
  re-validates it against the physical mechanical switch.
