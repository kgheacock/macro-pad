---
id: "0026"
title: "Add a make debug target that enables the serial console without touching boot.py"
status: "ongoing"
created: "2026-08-15"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0026-make-debug-console-target"
related: ["0008", "0019"]
tags: ["firmware", "dx", "makefile"]
---

# 0026 — Add a make debug target that enables the serial console without touching boot.py

## Problem

`firmware/README.md` tells the developer to hand-edit the last line of
`firmware/boot.py` to read a traceback over serial, then remember to
revert it. This session, the developer made this edit, flashed it, then
left it uncommitted until they reverted the change by hand.

## Goals

- One command puts the board into console-enabled debug mode.
- `firmware/boot.py`, the file git tracks, never changes on disk.
- The existing `make flash` command restores normal mode afterward.
  No separate "undo" command exists.

## Non-goals

- Finding or opening the right `/dev/cu.usbmodem*` port automatically.
  Confirmed live this session: after the console started, the port
  that existed before the reset became the console port. The new port
  became the data port. Port choice stays a manual, documented step.
- Resetting or power-cycling the board. No software path resets the
  RP2350. Only a physical button press resets it.

## Approaches considered

### Approach A — Toggle boot.py in place with sed, add a debug-off target

`make debug` runs `sed -i` on `firmware/boot.py`. This changes
`console=False` to `console=True`. Then it calls `flash`. A companion
`make debug-off` target changes the file back.

- Good, because it reuses `flash`'s existing rsync step untouched.
- Good, because only one `boot.py` exists, so nothing can drift from
  the real USB descriptor config.
- Bad, because it changes a tracked file. This exact problem happened
  this session and needed a manual `git checkout` command to fix it.
- Bad, because a forgotten `debug-off` step lets `console=True` reach
  `main` in a commit. This silently changes every future flash.

### Approach B — A second file, boot_debug.py, flashed in place of boot.py

Add `firmware/boot_debug.py` with `console=True`. `make debug` copies
it to the board as `boot.py` instead of the tracked one.

- Good, because `firmware/boot.py` never changes, so there is no
  revert step and nothing to forget before a commit.
- Good, because `git status` stays clean through the whole session.
- Bad, because two files now define the USB descriptor. A future edit
  to `boot.py`'s HID report needs a matching edit in `boot_debug.py`.
  Otherwise, the debug board runs a stale descriptor.
- Bad, because nothing catches that drift automatically. The mismatch
  appears only when someone debugs with the tool and sees different
  behavior.

### Approach C — Generate the debug boot.py at flash time, write it straight to the device

`make debug` pipes the tracked `firmware/boot.py` through one `sed`
substitution. It writes the result straight to
`$(CIRCUITPY_VOLUME)/boot.py`. The repo never stores this transformed
text as a file.

- Good, because it reads the real, current `boot.py` every time, so
  the debug descriptor can never drift from the shipped one.
- Good, because `git status` stays clean throughout, and the existing
  `make flash` is the exit path with no new command to remember.
- Bad, because the copy step for `boot.py` differs from `flash`'s
  whole-directory rsync call. It needs its own single-file copy line.
- Bad, because a `sed` pattern that no longer matches a future
  `boot.py` edit writes an unmodified file. No error appears.

## Decision

Chosen: **Approach C — generate the debug boot.py at flash time**.

This session's failure showed Approach A's exact risk: a stray edit to
`firmware/boot.py` stayed uncommitted until someone caught it by hand.
Approach C accepts a small, single-file copy step to remove that risk
completely. This task rejects Approach B. A debug tool that silently
drifts from the real device config creates bugs inside the tool meant
to explain bugs.

## Design

Add a `.PHONY: debug` target to `Makefile`, next to `flash`. Factor the
`CIRCUITPY`-mounted check that `flash` already runs into a shared
`check-circuitpy` prerequisite so both targets use one check. `debug`
depends on `check-circuitpy`, then runs:

```
sed 's/console=False/console=True/' firmware/boot.py > $(CIRCUITPY_VOLUME)/boot.py
```

Then `debug` runs `diff -q` against the original file. If the
substitution made no change, the build fails. It prints a reminder,
too: the developer must still find the console port and run `screen`
by hand, per the existing instructions in `firmware/README.md`.

Files to change:

- `Makefile` — `check-circuitpy` prerequisite, `.PHONY: debug` target
- `firmware/README.md` — replace the manual boot.py edit instructions
  with `make debug`; keep the port-finding and `screen` instructions

## Definition of done

- [ ] **DoD-1** — With `CIRCUITPY` mounted, `make debug` writes a
      `boot.py` file to the board with `console=True`, and
      `firmware/boot.py` in the repo stays unchanged. **Proof:** run
      `make debug`; `git status --porcelain firmware/boot.py` prints
      nothing; `grep console=True /Volumes/CIRCUITPY/boot.py` matches.
- [ ] **DoD-2** — With `CIRCUITPY` not mounted, `make debug` exits
      non-zero and names the missing volume. **Proof:** run `make
      debug` with the board disconnected; check the exit code and the
      message.
- [ ] **DoD-3** — If a developer runs `make flash` after `make debug`,
      `console=False` returns in the device's `boot.py`. **Proof:** run
      `make debug`, then `make flash`; `grep console=False
      /Volumes/CIRCUITPY/boot.py` matches.
- [ ] **DoD-4** — `firmware/README.md` documents `make debug` as the
      way to enable the console, in place of the manual edit
      instructions. **Proof:** read the file.
- [ ] **DoD-5** — The PR in the `pr` field links to this spec.
      **Proof:** PR body.

## Risks

- A future rename of the `console=False` literal in `boot.py` stops
  the `sed` match silently → the `diff -q` check fails the build
  first, so a wrong file never reaches the device.
- The board can stay in console mode if a developer skips `make flash`
  after `make debug` → `firmware/README.md` states that `make flash`
  is the exit step.

## Notes

Discovered live in the session that also delivered task 0019. A
developer made the manual `boot.py` edit to read a traceback. It
worked, then stayed uncommitted until someone caught it by hand.
