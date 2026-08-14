---
id: "0001"
title: "Add a test harness with CircuitPython hardware mocks"
status: "complete"
created: "2026-08-03"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/1"
branch: "0001-test-harness-circuitpython-mocks"
related: ["0003", "0004", "0005", "0006", "0007", "0008", "0009"]
tags: ["firmware", "testing"]
---

# 0001 — Add a test harness with CircuitPython hardware mocks

## Problem

No firmware logic can run in a unit test today. The CircuitPython-only
modules (`board`, `digitalio`, `usb_hid`, `usb_cdc`, `audiobusio`,
`displayio`) do not exist on a dev machine, and no test framework is chosen.
This blocks every firmware task that needs tests before the board exists.

## Goals

- `pytest` runs firmware unit tests on a dev machine with no board attached.
- Fake versions of `board`, `digitalio`, `usb_hid`, `usb_cdc`, `audiobusio`,
  and `displayio` exist for import.
- `test/README.md` tells a contributor how to add a test.

## Non-goals

- Testing real SPI, I2S, or USB behavior against silicon. Task 0010 covers
  that.
- A CircuitPython device-side test runner.

## Approaches considered

### Approach A — Hand-written stub modules

Add a `tests/stubs/` package with a small fake class for each CircuitPython
module. Put `tests/stubs` on `sys.path` before firmware code imports it.

- Good, because it adds no dependency, and the team controls all behavior.
- Good, because each stub stays small — only the methods firmware calls.
- Bad, because someone must update the stubs by hand if the real API changes.
- Bad, because a stub gives no proof that it matches real hardware timing.

### Approach B — Adopt Adafruit-Blinka

Install the Blinka compatibility layer. It provides `board` and `digitalio`
on a desktop by talking to a real GPIO backend where one exists.

- Good, because it is maintained upstream and matches the real API surface.
- Good, because test code needs no changed import paths.
- Bad, because Blinka targets real GPIO backends. On a Mac dev machine,
  `audiobusio` and `displayio` are unsupported or need extra platform code.
- Bad, because it adds an external dependency with its own version risk.

### Approach C — Test only on-device, once hardware exists

Skip host-side mocking. Wait for the board, then run tests on it directly.

- Good, because it needs no mock-maintenance effort now.
- Good, because a passing test proves real hardware behavior, not an
  approximation.
- Bad, because it blocks every pure-logic firmware task (debounce,
  idle-timeout, audio framing) until hardware ships — the exact problem
  this plan exists to avoid.
- Bad, because no CI run is possible without a physical board on a runner.

## Decision

Chosen: **Approach A — Hand-written stub modules**.

Firmware touches a small, fixed set of methods across six CircuitPython
modules. Hand-written stubs cost less to build and reason about than
Blinka's larger, partly-unsupported surface. This choice accepts the cost of
updating stubs by hand when the real API changes.

## Design

New `tests/stubs/` package, one file per faked module (`board.py`,
`digitalio.py`, `usb_hid.py`, `usb_cdc.py`, `audiobusio.py`, `displayio.py`),
each holding only the classes and functions firmware code imports.
`tests/conftest.py` puts `tests/stubs` on `sys.path` before any firmware
import.

Files to change:

- `tests/stubs/*.py` — new, one fake module per CircuitPython module
- `tests/conftest.py` — new, path setup
- `pyproject.toml` — new, pytest config
- `test/README.md` — record the framework and the stub location

## Definition of done

- [x] **DoD-1** — `pytest` runs with zero collection errors on a machine
  with no CircuitPython installed. **Proof:** `pytest --collect-only` exits 0
- [x] **DoD-2** — `import board, digitalio, usb_hid, usb_cdc, audiobusio,
  displayio` succeeds in a test file. **Proof:**
  `pytest tests/test_stubs_import.py` passes
- [x] **DoD-3** — A sample test that uses a stubbed
  `digitalio.DigitalInOut` fails on `main` and passes on this branch.
  **Proof:** `git stash && pytest tests/test_stubs_import.py` fails
- [x] **DoD-4** — `test/README.md` names the framework and the stub
  location. **Proof:** `test/README.md`
- [x] **DoD-5** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- Stub behavior may not match real hardware → tests pass but the device
  fails. Task 0010 validates against real hardware and reports any gap.

## Open questions

- [x] Should CI run these tests on every push? — repo owner
