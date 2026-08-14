---
id: "0020"
title: "Add a single-command end-to-end test harness driven through the driver API"
status: "backlog"
created: "2026-08-14"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0010", "0013", "0014", "0019", "0021", "0022", "0023"]
tags: ["driver", "testing", "hardware", "dx"]
---

# 0020 — Add a single-command end-to-end test harness driven through the driver API

## Problem

Nothing exercises firmware and driver together. `driver/transport` asserts
wire bytes against an emulator, and task 0010 plans a one-off manual
bring-up. Neither gives a repeatable command that flashes the board and
then checks device behavior a person can see. Writing such a check today
also has no home and no vocabulary.

## Goals

- One command flashes the current firmware and runs every end-to-end
  scenario against the attached board.
- A scenario reads as the user story it proves. Example: each key shows
  its number 1 to 6, and a pressed key changes background color while it
  keeps the number.
- A scenario names the key, not the byte layout. It never builds a
  `KeyState` struct by hand.
- The same scenario source runs against `transport.Emulator`, so a
  scenario compiles and passes with no board attached.
- Where the current driver API blocks an ergonomic scenario, this task
  changes the driver API.

## Non-goals

- The real HID and CDC transport. This task defines the facade over
  `transport.Transport`; task 0021 implements the hardware backend.
- The firmware `code.py` main loop (task 0022) and the glyph bitmaps
  (task 0023). This task reserves the digit IDs in the protocol document
  only.
- Audio scenarios. Task 0007 has no firmware yet, so no chunk stream
  exists to assert against.
- Unattended CI. Every hardware scenario ends in a person's verdict.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — A `driver/e2e` Go package behind a `hardware` build tag

`make e2e` runs `make flash`, then `go test -tags hardware -count=1
./driver/e2e/...`. Scenarios are Go tests written against a `Pad` facade.

- Good, because a scenario calls the same public driver API that task
  0013 exposes, so an awkward scenario is direct evidence of an awkward
  API.
- Good, because `go test` already supplies run selection, per-test
  timeouts, and failure reporting, so this task writes no runner.
- Bad, because `go test` buffers output, so operator prompts need `-v`
  and explicit writes to `os.Stdout` to appear at the right moment.
- Bad, because each new scenario needs a Go compile, so a person with no
  Go toolchain cannot run a factory check on an assembled unit.

### Approach B — A `macrodriver e2e` binary that runs YAML scenarios

A new subcommand reads a scenario file of declarative steps: `set`,
`prompt`, `expect_press`, `confirm`.

- Good, because a scenario is data, so the same file ships as a factory
  test script for an assembled unit.
- Good, because the runner owns the terminal, so prompts and a run log
  need no `go test` workaround.
- Bad, because it builds a second execution engine — step parsing,
  timeouts, failure reporting — that duplicates `go test`.
- Bad, because a declarative step cannot express "on press, change this
  key's color", the exact story this task must prove, so it needs an
  escape hatch back into Go.

### Approach C — A hardware `Transport` behind an env var, no new package

Add a real-device `Transport` and gate the existing
`driver/transport` tests on `MACROPAD_HARDWARE=1`.

- Good, because it adds no package. One interface, two implementations,
  one suite.
- Good, because every emulator test then re-runs against silicon, which
  is the mock-versus-real check task 0010 asks for.
- Bad, because those tests assert bytes. "The key shows 3 and turns
  amber" is not a byte assertion, and the package has no place for a
  person's verdict.
- Bad, because a runtime env-var gate makes the suite a silent no-op when
  the variable is unset, so `go test ./...` stays green while the
  hardware path is broken.

## Decision

Chosen: **Approach A — a `driver/e2e` package behind a `hardware` build
tag**.

The goal that decides it is the last one: this harness must inform the
driver API. Only Approach A writes scenarios against that API, so friction
in a scenario becomes a driver change. The facade takes a
`transport.Transport`, so Approach C's emulator benefit still applies. The
cost accepted is that `make e2e` needs a person at the terminal and cannot
run in CI.

## Design

`driver/e2e` holds a `Pad` facade over `transport.Transport`.

```go
pad := e2e.Attach(t)               // opens the device, waits out re-enumeration
defer pad.Close()

for _, k := range pad.Keys() {     // 6 keys light one at a time
    k.Set(e2e.Digit(k.Index()+1), e2e.Slate)
    k.On(e2e.Press, func() { k.SetColor(e2e.Amber) })
    k.On(e2e.Release, func() { k.SetColor(e2e.Slate) })
}

pad.Ask("Press each key once, left to right.")
for _, k := range pad.Keys() {
    k.ExpectPress(t, 10*time.Second)
}
pad.Confirm(t, "Did every key keep its number while the background turned amber?")
```

Three driver changes make that source legal:

1. **A `Key` holds its last sent state**, so `SetColor` resends the digit
   and blink flag the caller did not repeat.
2. **`Pad` owns one `ReadEvent` loop and fans events out**, so `On` and
   `ExpectPress` can both wait. Today two callers of `ReadEvent` steal
   from each other. Task 0013's `UseAction` reuses this dispatcher.
3. **Named glyph and color constants**, so a scenario writes `Digit(3)`,
   not emoji ID `0xF3`.

Files to change:

- `driver/e2e/pad.go` — new. `Attach`, `Pad`, `Key`, per-key state,
  event fan-out
- `driver/e2e/operator.go` — new. `Ask`, `Confirm`, a scripted operator
  for emulator runs, and a run log at `driver/e2e/runs/`
- `driver/e2e/glyph.go` — new. `Digit`, `Slate`, `Amber`, RGB565 values
- `driver/e2e/keys_test.go` — new. The scenario above, `//go:build
  hardware`
- `driver/e2e/pad_emulator_test.go` — new. The same scenario against
  `transport.Emulator`, no build tag
- `driver/transport/transport.go` — add `Close() error`; document that
  `ReadEvent` has one owner
- `docs/wire-protocol.md` — reserve emoji IDs `0xF1` to `0xF6` for
  digits 1 to 6
- `Makefile` — add the `e2e` target
- `driver/README.md` — how to write and run a scenario

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — The digit scenario runs end to end against
      `transport.Emulator` with a scripted operator, and asserts the
      pressed key's key-state message keeps emoji ID `0xF3` while its
      color changes to `Amber`. **Proof:** `go test ./driver/e2e/... -run
      TestDigitsAndPressColor_Emulator -v`
- [ ] **DoD-2** — `k.SetColor(...)` alone resends the key's current emoji
      ID and blink flag. **Proof:** `go test ./driver/e2e/... -run
      TestKey_SetColorKeepsGlyph`
- [ ] **DoD-3** — Two waiters on the same key both receive one press.
      **Proof:** `go test ./driver/e2e/... -run TestPad_FanOutTwoWaiters`
- [ ] **DoD-4** — `ExpectPress` fails with a message naming the key index
      and the timeout when no press arrives. **Proof:** `go test
      ./driver/e2e/... -run TestExpectPress_Timeout`
- [ ] **DoD-5** — With no board attached, `make e2e` exits non-zero and
      names the missing device. It never hangs longer than 30 s. **Proof:**
      run `time make e2e` with the board unplugged; check exit code,
      message, and elapsed time.
- [ ] **DoD-6** — `make e2e` runs `make flash` before the tests. **Proof:**
      read the `e2e` recipe in `Makefile`.
- [ ] **DoD-7** — Every scenario run writes a log of each prompt and each
      operator verdict. **Proof:** a file under `driver/e2e/runs/` after
      DoD-1.
- [ ] **DoD-8** — `docs/wire-protocol.md` reserves emoji IDs `0xF1` to
      `0xF6` for digits 1 to 6. **Proof:** `docs/wire-protocol.md`.
- [ ] **DoD-9** — `driver/README.md` shows the digit scenario and the
      `make e2e` command. **Proof:** `driver/README.md`.
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:**
      PR body.

## Risks

- Three parts of the pipeline do not exist: the hardware `Transport`
  (task 0021), the firmware `code.py` main loop (task 0022), and the
  digit glyphs (task 0023) → every item above is verified against the
  emulator, so this task ships without all three. The hardware pass
  belongs to task 0010's definition of done.
- `make flash` reboots the board, so the device disappears and returns →
  `Attach` retries discovery for a bounded window instead of failing on
  the first miss.
- Device discovery on macOS can hang. Task 0019 records that `mount` and
  `ls /Volumes` both hang while the board is attached → discovery uses
  the USB VID and PID, never a volume listing.
- A scenario that ends in a person's verdict can be ticked without
  looking → the run log records the exact question asked, so a reviewer
  sees which verdict was given.

## Open questions

- [ ] Does `make e2e` re-flash on every run, or skip the flash when
      `firmware/` is unchanged? — owner, after the first week of use.
- [ ] Which VID and PID does the board enumerate with after task 0008's
      descriptor? — hardware owner, at bring-up.

## Notes

The three driver changes in Design come from writing the scenario source
first and then finding what the current API could not express. That order
is the method this task recommends for later scenarios.
