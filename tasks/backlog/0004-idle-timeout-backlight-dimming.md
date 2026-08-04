---
id: "0004"
title: "Add idle-timeout backlight dimming"
status: "backlog"
created: "2026-08-03"
updated: "2026-08-03"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0001"]
tags: ["firmware"]
---

# 0004 — Add idle-timeout backlight dimming

## Problem

`firmware/README.md` commits to dimming the PWM backlight after an idle
window (default 5 minutes) with no host update or key event, but no
timer or state logic exists yet.

## Goals

- A state machine tracks the last activity time and computes the current
  backlight duty cycle.
- The default idle window of 5 minutes is configurable.
- The module has unit tests that use a fake clock. It needs no board or
  PWM hardware.

## Non-goals

- Driving the real PWM peripheral. Task 0010 covers that, once hardware
  exists.
- A brightness curve beyond one dim level. `firmware/README.md` asks for a
  dim state, not a multi-step fade.

## Approaches considered

### Approach A — Single-threshold timer

Track `last_activity_us`. On each check, return the dim duty cycle once
`now - last_activity_us` exceeds the idle window, else the full duty cycle.

- Good, because it has only two states, bright and dim, which are simple to
  test.
- Good, because it matches the exact requirement in `firmware/README.md`,
  with no unstated behavior.
- Bad, because the jump from full to dim brightness is abrupt, with no
  fade.
- Bad, because every host update or key event must call `touch()`, an easy
  call site to miss.

### Approach B — Fade curve over multiple steps

Ramp the duty cycle down over a fade window once idle begins, instead of
one jump.

- Good, because the visual transition is smoother.
- Good, because it could signal "about to dim" to the host before full dim.
- Bad, because `firmware/README.md` does not ask for a fade — this adds
  scope with no backing requirement.
- Bad, because it adds states and parameters to test and tune with no
  hardware display to check the result against.

### Approach C — Host-driven dimming

Render at full brightness always. The host tracks idle time and sends a
duty-cycle value to the device.

- Good, because firmware needs no timer logic at all.
- Good, because all idle policy lives in one place, easier to change later.
- Bad, because `firmware/README.md` places idle-timeout dimming in the
  firmware's scope, independent of the host.
- Bad, because the device stays undimmed if the host process is not
  running or has crashed.

## Decision

Chosen: **Approach A — Single-threshold timer**.

It is the smallest change that matches the requirement already committed
to in `firmware/README.md`. This choice accepts the visual cost of an
abrupt brightness change instead of a fade.

## Design

`firmware/idle_timer.py` exposes `IdleTimer(idle_window_us=300_000_000)`
with `touch(timestamp_us)` and `duty_cycle(timestamp_us) -> float`, which
returns `1.0` for full brightness or a configurable dim fraction.

Files to change:

- `firmware/idle_timer.py` — new
- `tests/test_idle_timer.py` — new

## Definition of done

- [ ] **DoD-1** — `duty_cycle()` returns full brightness before the idle
  window elapses. **Proof:**
  `pytest tests/test_idle_timer.py::test_bright_before_window`
- [ ] **DoD-2** — `duty_cycle()` returns the dim value once the idle window
  elapses with no `touch()` call. **Proof:**
  `pytest tests/test_idle_timer.py::test_dim_after_window`
- [ ] **DoD-3** — Calling `touch()` resets the window. **Proof:**
  `pytest tests/test_idle_timer.py::test_touch_resets`
- [ ] **DoD-4** — The default idle window is 5 minutes, matching
  `firmware/README.md`. **Proof:** `firmware/idle_timer.py`, default
  argument
- [ ] **DoD-5** — The new tests fail on `main`. **Proof:**
  `git stash && pytest tests/test_idle_timer.py` fails
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field
