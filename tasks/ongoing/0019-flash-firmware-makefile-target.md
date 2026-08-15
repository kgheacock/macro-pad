---
id: "0019"
title: "Add a make flash target that copies firmware onto the connected board"
status: "ongoing"
created: "2026-08-14"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/16"
branch: "0019-flash-firmware-makefile-target"
related: []
tags: []
---

# 0019 — Add a make flash target that copies firmware onto the connected board

## Problem

`firmware/README.md` tells the developer to copy the contents of
`firmware/` onto the `CIRCUITPY` drive by hand. Each firmware change
repeats this manual step. A stray `__pycache__` folder or a missed file
gives no clear error until the board misbehaves.

## Goals

- `make flash` copies the current `firmware/` source onto the mounted
  `CIRCUITPY` drive.
- When the board is not connected as `CIRCUITPY`, `make flash` fails
  with a clear message.
- When a USB volume on the machine is slow to answer a stat call,
  `make flash` does not hang.

## Non-goals

- Reflashing CircuitPython itself. `make firmware-uf2` plus the manual
  BOOTSEL drag-and-drop already does this.
- Checking that the copied code runs correctly on the board.
- Support for Linux or Windows. This project is developed on macOS.

## Approaches considered

### Approach A — Shell target using `diskutil` and `rsync`

`make flash` finds the `CIRCUITPY` volume with `diskutil info`, then
copies `firmware/` onto it with `rsync`, apart from `modules/`,
`__pycache__/`, and `README.md`.

- Good, because it adds no new dependency. `rsync` and `diskutil` ship
  with macOS, and the Makefile already shells out this way for
  `firmware-uf2`.
- Good, because `diskutil info` finds the volume without calling
  `mount` or listing `/Volumes`. When a USB volume answers stat calls
  slowly, both of those hang on this machine — confirmed live with the
  RP2350 board attached.
- Bad, because the volume lookup is macOS-only. It breaks silently if
  the project ever moves to Linux or WSL.
- Bad, because `rsync --delete` on the wrong path can remove files
  outside `firmware/`. The recipe must check the volume name before
  any delete runs.

### Approach B — Drive the copy through `mpremote` or `circup`

Add `mpremote` (or `circup`) as a pinned Python dependency. `make
flash` calls it to push `firmware/` onto the board.

- Good, because these tools target board file transfer directly. They
  can later run code or soft-reset the board, not just copy files.
- Good, because a purpose-built tool can confirm each write. A raw
  file copy does not.
- Bad, because it adds the project's first runtime Python dependency
  for what is otherwise a one-line copy. `pyproject.toml` today only
  pins `pytest`.
- Bad, because `mpremote` targets MicroPython, not CircuitPython. Its
  support for CircuitPython is best-effort, so its behavior on this
  exact board is unverified.

### Approach C — Document a manual `cp` command, no Makefile target

Replace the prose instruction in `firmware/README.md` with a
copy-paste `cp` command. Add no Makefile target.

- Good, because it adds no code and no new way to delete the wrong
  files.
- Good, because it needs no volume-detection logic at all.
- Bad, because it does not give the `make flash` command this task
  asks for. The repeated manual step, and its small mistakes, stay
  exactly as they are today.
- Bad, because a bad copy still gives no automatic failure signal. The
  developer finds out only by testing the board afterward.

## Decision

Chosen: **Approach A — Shell target using `diskutil` and `rsync`**.

This is a single-developer macOS project. The Makefile already shells
out directly with no dependency layer. Approach A also uses
`diskutil`, the one volume-lookup path confirmed not to hang on this
hardware. The cost accepted is a macOS-only target. If the project
ever needs Linux or WSL support, revisit this decision.

## Design

Add a `flash` target to `Makefile`, next to `firmware-uf2`.

- `Makefile` — add `CIRCUITPY_VOLUME := /Volumes/CIRCUITPY`, a
  `.PHONY: flash` target that runs `diskutil info` on
  `$(CIRCUITPY_VOLUME)` to confirm the volume exists and is named
  `CIRCUITPY` before it runs `rsync -rc --delete
  --exclude=modules/ --exclude=__pycache__/ --exclude=README.md
  firmware/ $(CIRCUITPY_VOLUME)/`. A missing or misnamed volume exits
  non-zero with a message that names the expected path.
- `firmware/README.md` — replace the manual copy instruction with
  `make flash`.

## Definition of done

- [ ] **DoD-1** — With `CIRCUITPY` mounted, `make flash` copies every
      `.py` file from `firmware/` onto it. **Proof:** run `make
      flash`, then `diff` the two directory listings.
- [ ] **DoD-2** — With `CIRCUITPY` not mounted, `make flash` exits
      non-zero and names the missing volume. **Proof:** run `make
      flash` with the board disconnected; check the exit code and the
      message.
- [ ] **DoD-3** — The volume lookup uses `diskutil`, not `mount` or a
      listing of `/Volumes`. **Proof:** read the `flash` recipe in
      `Makefile`.
- [ ] **DoD-4** — `firmware/modules/` is not copied onto the board.
      **Proof:** run `make flash` with `firmware/modules/` present
      locally; confirm it is absent from `CIRCUITPY` afterward.
- [ ] **DoD-5** — `firmware/README.md` documents `make flash` as the
      copy step. **Proof:** read the file.
- [ ] **DoD-6** — The PR in the `pr` field links to this spec.
      **Proof:** PR body.

## Risks

- `diskutil info`'s output format can change across macOS versions →
  parse one stable field (`Volume Name`) and fail closed if it is
  missing.
- `rsync --delete` can remove files a developer added on the board
  directly (test data, logs) → document this in `firmware/README.md`
  next to the `make flash` instruction.

## Notes

On this machine, `mount` and `ls /Volumes` both hang while the RP2350
board is attached. Both commands ask the operating system to list
every mounted volume. The operating system waits for a stat response
from each volume before it returns the list. The volume of the
RP2350 board answers stat slowly, so the wait does not end. `diskutil
info` and `diskutil list` do not hang, since they use DiskArbitration
instead of this stat-based list. For this reason, the `flash` target
must use `diskutil`, not just for style.
