---
id: "0009"
title: "Add a key-to-pin mapping config"
status: "backlog"
created: "2026-08-03"
updated: "2026-08-03"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0001", "0003", "0006"]
tags: ["firmware", "hardware"]
---

# 0009 — Add a key-to-pin mapping config

## Problem

`hardware/README.md` flags the ScreenKey module SKU as unconfirmed — the
Module variant has a dedicated `KEY` pin, the LCD-only variant does not —
so firmware cannot hard-code a pin table yet. Tasks 0003 and 0006 both need
a key-to-pin mapping to call into.

## Goals

- One config module holds the key-to-GPIO-pin and key-to-SPI-chip-select
  mapping, apart from the modules that use it.
- Updating the config means editing one file once the SKU and wiring are
  confirmed, with no logic changes elsewhere.
- Tasks 0003 and 0006 import from this config instead of writing pin
  numbers inline.

## Non-goals

- Confirming the SKU or the wiring. That is `hardware/README.md`'s open
  item, owned outside firmware.
- Validating the mapping against a real board. Task 0010 covers that.

## Approaches considered

### Approach A — Single config module with placeholder values

`firmware/pins.py` holds a `KEYS` list of `(gpio_pin, spi_cs_pin)` tuples,
with a comment marking them unconfirmed, used everywhere a pin is needed.

- Good, because one file needs an update once `hardware/README.md`'s SKU
  question resolves, with no change to logic elsewhere.
- Good, because tasks 0003 and 0006 can write and test their code now,
  against a stable import path.
- Bad, because a developer could treat the placeholder pin numbers as
  final and forget to check them later.
- Bad, because the file does not itself resolve the unconfirmed SKU — that
  decision still blocks the final values.

### Approach B — Runtime-detected mapping

Firmware probes the `KEY` pin at boot to detect which module variant is
present, and picks a mapping accordingly.

- Good, because firmware adapts on its own if both module variants ever
  ship in different units.
- Good, because it removes a manual step from the build configuration.
- Bad, because `hardware/README.md` describes buying one confirmed SKU,
  not supporting both variants at once — this solves a problem that does
  not exist yet.
- Bad, because it adds boot-time probing logic and its own failure mode,
  ambiguous detection, for a speculative need.

### Approach C — Defer the config file until the SKU is confirmed

Do not write `pins.py` now. Block tasks 0003 and 0006 on
`hardware/README.md`'s open item.

- Good, because there is no placeholder value to later distrust or forget
  to update.
- Good, because no work is wasted if the final mapping looks nothing like
  a guess.
- Bad, because it blocks tasks 0003 and 0006 from a concrete import path
  to test against, which weakens their unit tests to abstract stand-ins.
- Bad, because it again defeats the purpose of planning firmware work
  before hardware exists.

## Decision

Chosen: **Approach A — Single config module with placeholder values**.

A single, clearly marked placeholder file gives tasks 0003 and 0006
something concrete to import now, and confines the eventual real-value
update to one file. This choice accepts the risk that placeholder values
must stay visibly marked so no one mistakes them for confirmed wiring.

## Design

`firmware/pins.py` exposes `KEYS: list[KeyPins]`, a `NamedTuple` of
`(switch_pin: str, display_cs_pin: str)`, with a module-level comment that
links to `hardware/README.md`'s SKU question. Values are placeholders
(`"GP0"` through `"GP11"`) until hardware confirms wiring.

Files to change:

- `firmware/pins.py` — new
- `hardware/README.md` — link to `firmware/pins.py` from the SKU
  confirmation row

## Definition of done

- [ ] **DoD-1** — `firmware/pins.py` defines exactly 6 `KeyPins` entries,
  one per key. **Proof:** `len(KEYS) == 6` in `tests/test_pins.py`
- [ ] **DoD-2** — Each entry has distinct, non-empty `switch_pin` and
  `display_cs_pin` values. **Proof:**
  `pytest tests/test_pins.py::test_pins_unique`
- [ ] **DoD-3** — A comment in `firmware/pins.py` links to
  `hardware/README.md`'s SKU confirmation note. **Proof:**
  `firmware/pins.py`, header comment
- [ ] **DoD-4** — `hardware/README.md` links back to `firmware/pins.py`.
  **Proof:** `hardware/README.md`
- [ ] **DoD-5** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- Placeholder values could be mistaken for final ones → the header comment
  and DoD-3 keep the open question visible until `hardware/README.md`'s
  SKU line resolves.
