---
id: "0009"
title: "Add a key-to-pin mapping config"
status: "ongoing"
created: "2026-08-03"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0009-key-pin-mapping-config"
related: ["0001", "0003", "0006"]
tags: ["firmware", "hardware"]
---

# 0009 — Add a key-to-pin mapping config

## Problem

`hardware/README.md` now has a confirmed [Pinout](../hardware/README.md#pinout)
table — GPIO assignments for the shared SPI bus, per-key CS, DC, RST, the
I2S mic, and per-key backlight PWM — but no firmware config consumes it
yet. The table assumes the ScreenKey *Module* variant's dedicated `KEY`
pin; the SKU itself is still unconfirmed in hand (`hardware/README.md`'s
own warning). Tasks 0003 and 0006 both need a key-to-pin mapping to call
into.

## Goals

- One config module holds the key-to-GPIO-pin, key-to-SPI-chip-select, and
  key-to-backlight-pin mapping, apart from the modules that use it.
- The module transcribes `hardware/README.md`'s confirmed Pinout table
  directly, so updating the config means editing one file if that table is
  ever revised, with no logic changes elsewhere.
- Tasks 0003 and 0006 import from this config instead of writing pin
  numbers inline.

## Non-goals

- Confirming which ScreenKey SKU is physically in hand — Module vs.
  LCD-only. That is `hardware/README.md`'s open item, owned outside
  firmware; this config assumes the Module variant the confirmed Pinout
  table is built against.
- Validating the mapping against a real board. Task 0010 covers that.

## Approaches considered

### Approach A — Single config module sourced from the confirmed pinout

`firmware/pins.py` holds a `KEYS` list of per-key pin tuples plus
module-level constants for the shared pins, transcribed directly from
`hardware/README.md`'s Pinout table, used everywhere a pin is needed.

- Good, because one file needs an update if `hardware/README.md`'s Pinout
  table ever changes — for instance, if the physical SKU turns out to be
  the LCD-only variant — with no change to logic elsewhere.
- Good, because tasks 0003 and 0006 can write and test their code now,
  against a stable import path and real pin values, not guesses.
- Bad, because the config still assumes the Module SKU variant is what
  ships — if the physical part in hand turns out to be the LCD-only
  variant, every value here needs rework.
- Bad, because the file cannot itself verify the physical SKU — task
  0010's bring-up is what confirms that.

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

### Approach C — Defer the config file until the SKU is physically confirmed

Do not write `pins.py` now. Block tasks 0003 and 0006 on
`hardware/README.md`'s open item — the physical SKU check, not the pinout
design, which is already confirmed.

- Good, because there is no assumed-SKU value to later distrust or forget
  to update if the physical part turns out to be the LCD-only variant.
- Good, because no work is wasted if the physical SKU forces a
  differently-shaped mapping than the confirmed Pinout table assumes.
- Bad, because it blocks tasks 0003 and 0006 from a concrete import path
  to test against, which weakens their unit tests to abstract stand-ins.
- Bad, because it again defeats the purpose of planning firmware work
  before hardware exists.

## Decision

Chosen: **Approach A — Single config module sourced from the confirmed
pinout**.

A single file that transcribes `hardware/README.md`'s confirmed Pinout
table gives tasks 0003 and 0006 something concrete to import now, and
confines any future rework — if the physical SKU turns out to be the
LCD-only variant — to one file. This choice accepts the risk that the
config still assumes the Module variant until task 0010's physical
bring-up confirms it.

## Design

`firmware/pins.py` exposes `KEYS: list[KeyPins]`, a `NamedTuple` of
`(switch_pin: str, display_cs_pin: str, backlight_pin: str)` per key, plus
module-level constants for the pins shared across keys: `SPI_SCK`,
`SPI_MOSI`, `DISPLAY_DC`, `DISPLAY_RST`, and the I2S mic's `MIC_BCLK`,
`MIC_WS`, `MIC_DATA`. Values come from `hardware/README.md`'s
[Pinout](../hardware/README.md#pinout) table, confirmed against the
ScreenKey *Module* variant.

Files to change:

- `firmware/pins.py` — new
- `hardware/README.md` — repoint the Pinout section's link from this spec
  to `firmware/pins.py`, now that it exists

## Definition of done

- [ ] **DoD-1** — `firmware/pins.py` defines exactly 6 `KeyPins` entries,
  one per key. **Proof:** `len(KEYS) == 6` in `tests/test_pins.py`
- [ ] **DoD-2** — Each entry has distinct, non-empty `switch_pin`,
  `display_cs_pin`, and `backlight_pin` values, matching
  `hardware/README.md`'s Pinout table. **Proof:**
  `pytest tests/test_pins.py::test_pins_unique`
- [ ] **DoD-3** — A comment in `firmware/pins.py` links to
  `hardware/README.md`'s Pinout table. **Proof:** `firmware/pins.py`,
  header comment
- [ ] **DoD-4** — `hardware/README.md` links back to `firmware/pins.py`.
  **Proof:** `hardware/README.md`
- [ ] **DoD-5** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- The pinout assumes the ScreenKey *Module* variant (dedicated `KEY` pin)
  → if the SKU in hand turns out to be the LCD-only variant,
  `hardware/README.md`'s Pinout table and this config both need rework.
