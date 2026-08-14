---
id: "0010"
title: "Bring up one key end-to-end on real hardware"
status: "backlog"
created: "2026-08-03"
updated: "2026-08-03"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0001", "0002", "0003", "0005", "0006", "0007", "0008", "0009"]
tags: ["firmware", "hardware", "bring-up"]
---

# 0010 — Bring up one key end-to-end on real hardware

## Problem

Once hardware arrives, nothing proves that the modules built against mocks
in tasks 0001 and 0003 through 0009 work against real silicon: the
display, the switch, the mic, and USB enumeration.

## Goals

- One key, wired on a breadboard, renders an emoji, debounces a real
  press, and streams real mic audio to a host over USB.
- Every module built against a mock or fake interface — tasks 0003, 0005,
  0006, 0007, 0008 — gets re-verified against the real hardware object it
  stands in for.
- Any interface mismatch found here becomes a follow-up task, not a silent
  patch.

## Non-goals

- Wiring all 6 keys. One key proves the pipeline; wiring the rest is a
  follow-up.
- The custom PCB or the enclosure. `hardware/README.md` marks both "Later
  — once the design is confirmed on breadboard", which this task produces.

## Approaches considered

### Approach A — Breadboard bring-up of one key

Wire one switch, one ScreenKey display, and the mic breakout to the RP2350
on a breadboard, per `hardware/README.md`'s header-or-socket rule. Then
run the software pipeline end-to-end.

- Good, because it matches `hardware/README.md`'s own stated path: confirm
  on breadboard before the PCB or enclosure.
- Good, because it is the smallest hardware setup that still exercises
  every firmware module in this plan.
- Bad, because breadboard wiring is less reliable than a PCB, so a failure
  could be a wiring fault, not a firmware bug, which adds debugging
  ambiguity.
- Bad, because it still waits on parts marked "Needed" in
  `hardware/README.md`, such as the mic breakout and headers.

### Approach B — Bring up all 6 keys directly

Wire the full 6-key board on breadboard, and validate the entire device at
once.

- Good, because it catches cross-key interaction bugs, such as SPI bus
  contention across 6 displays, that one key cannot reveal.
- Good, because it needs one bring-up pass instead of two.
- Bad, because it multiplies the wiring and the parts needed before any
  validation can start, while `hardware/README.md` shows several parts
  still "Needed".
- Bad, because a failure is harder to trace to one root cause across 6
  simultaneous new wiring paths.

### Approach C — Skip the breadboard, order a custom PCB first

Design and order a 6-key PCB now. Bring up on that instead of a
breadboard.

- Good, because it skips a wiring step and ends closer to the final
  product.
- Good, because it removes breadboard wiring reliability as a concern.
- Bad, because it directly contradicts `hardware/README.md`, which marks
  the PCB "Later — once the design is confirmed on breadboard" — an
  unconfirmed design baked into a PCB order is costly to fix.
- Bad, because PCB fab turnaround, JLCPCB or PCBWay, adds days to weeks
  before any firmware validation can start.

## Decision

Chosen: **Approach A — Breadboard bring-up of one key**.

It follows `hardware/README.md`'s own sequencing, breadboard before PCB,
and is the smallest setup that still exercises every module built in this
plan. This choice accepts that wiring faults may be mistaken for firmware
bugs during debugging.

## Design

No new firmware modules. This task wires one key and runs the existing
pipeline — task 0003 debounce, task 0006 render, task 0007 mic capture,
task 0008 USB — against real hardware, and fixes any interface mismatch
found in each module's `*Like` protocol.

Files to change:

- `firmware/*.py` — adjust interfaces where the real driver API differs
  from the mock; exact files depend on what bring-up finds
- `hardware/README.md` — update part statuses to reflect what was wired
- `docs/bring-up-log.md` — new, records what was tested and what broke

## Definition of done

- [ ] **DoD-1** — One key renders its configured emoji and color on the
  real ST7735 display. **Proof:** photo or video linked in
  `docs/bring-up-log.md`
- [ ] **DoD-2** — A real press and release on that key produces a
  debounced event over CDC serial. **Proof:** serial capture linked in
  `docs/bring-up-log.md`
- [ ] **DoD-3** — Holding the key streams real mic audio over CDC serial,
  with a final-chunk flag on release. **Proof:** captured audio file
  linked in `docs/bring-up-log.md`
- [ ] **DoD-4** — The host OS enumerates the device with both a HID and a
  CDC interface. **Proof:** `lsusb -v` or `system_profiler
  SPUSBDataType` output in `docs/bring-up-log.md`
- [ ] **DoD-5** — Every interface mismatch found gets filed as a follow-up
  task. **Proof:** new files in `tasks/backlog/`, linked from
  `docs/bring-up-log.md`
- [ ] **DoD-6** — `hardware/README.md`'s parts table reflects the real
  bring-up status. **Proof:** `hardware/README.md`
- [ ] **DoD-7** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- Parts still marked "Needed" in `hardware/README.md` — the mic breakout
  and headers — are not yet purchased → this task is blocked until they
  arrive.

## Open questions

- [ ] Which parts are still outstanding at task start? — hardware owner,
  check `hardware/README.md`'s parts table.
