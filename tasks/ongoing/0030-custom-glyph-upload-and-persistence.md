---
id: "0030"
title: "Custom glyph upload: send an arbitrary 128x128 image, persist the last one per key"
status: "ongoing"
created: "2026-08-15"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0030-custom-glyph-upload-and-persistence"
related: ["0023", "0022", "0029"]
tags: ["firmware", "driver", "display", "storage"]
---

# 0030 — Custom glyph upload: send an arbitrary 128x128 image, persist the last one per key

## Problem

A key's glyph is one of seven IDs baked into `firmware/glyphs.py` at flash
time (task 0023). To show a new image, a person must add a PNG to
`hardware/glyphs/`, regenerate `firmware/glyphs.py`, and reflash. No driver
call can set an arbitrary image at runtime. No key remembers its last
state across a power cycle.

## Goals

- A driver call sends an arbitrary 128×128 image for one key, and the
  firmware displays it in place of the built-in glyph table.
- After a power cycle, each key redraws its own last-set state — built-in
  glyph or custom image, color, and blink — with no driver connected. A
  firmware reflash (`make flash`) is a different event, and may reset this
  state; see Non-goals.
- Only the most recent state per key persists. An older state leaves no
  trace once a newer one replaces it.

## Non-goals

- Images other than 128×128. `tools/gen_glyphs.py`'s `GLYPH_SIZE` fixes this
  today. This task keeps that constraint for custom images too.
- Multiple stored states per key, or a way to list or replay past states.
  Goal 3 asks for exactly one, the most recent.
- Animated or multi-frame images. `display_render.render_key`'s blink flag
  already covers the one motion effect this pad supports.
- Persisted state surviving a firmware reflash. `make flash` resets every
  key to the built-in table by design; see Design and Decision.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Host decodes the image, wire and flash carry raw pixels

The driver decodes the PNG file with Go's `image/png` package, into a raw
128×128 RGB565 buffer. The driver sends this buffer in a new host-to-device
CDC message, framed like the existing device-to-host messages
(`docs/wire-protocol.md`, "Framing"), but in the reverse direction.
128×128×2 bytes is 32,768 bytes, under the limit of the frame header's
`uint16` `Length` field. One frame carries a full image, so the design
needs no new chunking scheme. Firmware writes the same bytes to one file
per key on the `CIRCUITPY` flash filesystem. Each write overwrites the
prior file for that key. Firmware reads the file back at boot.

- Good, because firmware never parses an image format. It only copies
  length-prefixed bytes into a bitmap and a file — the same shape
  `firmware/wire.py` already handles for every other message.
- Good, because the frame's existing `Length` field already covers one
  full image. No new multi-frame reassembly code is needed on either side.
- Bad, because the driver, not the plugin author, decides how a PNG's
  colors map to RGB565. Two drivers can render the same source file
  differently if their conversion code diverges.
- Bad, because a write of up to 32,768 bytes per key adds a step inside or
  near `MacroPad.step`. Task 0022 does not yet report this loop's
  per-iteration budget against real displays.

### Approach B — Firmware decodes the PNG itself, wire carries the file verbatim

The wire carries the PNG file's own bytes, chunked over a new host→device
CDC message. Firmware decodes the file on the device, with a bundled or
hand-written decoder limited to this task's fixed 128×128 case. Firmware
then writes the pixels to the display and to flash.

- Good, because the artifact on the wire and in flash is the exact file a
  plugin author supplied, not a host-side reinterpretation of it.
- Good, because any language can generate a PNG and send it. A plugin
  author needs no wire-specific pixel format.
- Bad, because CircuitPython has no general PNG decoder. DEFLATE
  decompression plus a full decoded frame in RAM is a real cost, and task
  0022 does not yet report a loop-period measurement to weigh it against.
- Bad, because a malformed or adversarial PNG becomes an on-device parsing
  problem, with no sandbox between that failure and the render loop.

### Approach C — The driver, not the firmware, remembers the last state

Firmware's storage and wire protocol are untouched. `macropadd` keeps a
per-key cache of the last state on the host. It replays this cache to the
board the next time the board connects.

- Good, because no flash-write code, remount handling, or write-wear risk
  reaches the firmware. Today's render path keeps its current tests, with
  no change.
- Good, because "no history, only the most recent" is a plain overwrite on
  the host filesystem, with no embedded storage budget to design around.
- Bad, because it does not meet Goal 2. A key must redraw its last state
  with no driver connected, but a host-side cache cannot act before a host
  is present.
- Bad, because it adds an ordering dependency. The daemon must reconnect
  and replay before a person expects the key to already show its image.
  Today's one-`Device`-per-daemon design (task 0028) does not guarantee
  this order.

## Decision

Chosen: **Approach A — host decodes, wire and flash carry raw pixels**.

Goal 2 requires a key to show its last state with no driver connected. This
rules out Approach C. Between Approach A and Approach B, CircuitPython on
this board's RP2350 has no general PNG decoder, and task 0022's
loop-period measurement does not yet exist to prove that a DEFLATE decode
fits the budget. Approach A keeps that risk off the firmware. The cost
accepted is that image fidelity depends on the driver's own PNG-to-RGB565
conversion, not on the untouched source file.

The persisted file format needs no version tag. Persisted state lives
inside the tree `make flash`'s `rsync --delete` already wipes on every
reflash (see Design), so an old-format file can never survive to be read
by firmware built against a newer one. A power cycle is a different
event from a reflash, and still restores each key's last state.

## Design

Files to change:

- `docs/wire-protocol.md` — add a host→device "Set custom glyph" message on
  the CDC channel, framed like the existing device→host messages: key
  index plus a 32,768-byte RGB565 payload. Reserve an Emoji ID sentinel
  (for example `0xFE`) meaning "this key's last custom image."
- `firmware/wire.py` — decode the new message type.
- `firmware/code.py` / `boot.py` — the CDC channel becomes read-and-write.
  Today firmware only writes to it.
- `firmware/display_render.py`, `firmware/app.py` — a raw-bitmap render
  path alongside the existing 1-bit glyph path.
- New firmware storage module — write the last full per-key state (color,
  built-in ID or custom pixels, blink) to one file per key, under
  `firmware/glyph_state/`. Overwrite the file on every change, and read it
  back at boot to restore state.
- `firmware/glyph_state/` — add to `.gitignore`, matching `firmware/modules/`.
  A gitignored directory is never in the source tree `make flash` syncs
  from, so its existing `rsync --delete` wipes this one on every reflash,
  with no new Makefile logic.
- `driver/transport/wire.go` — encode the new message. `Transport` gains a
  method that takes a key index and PNG bytes, and does the decode.
- `driver/plugin/protocol.go`, `server.go` — let a plugin supply image
  bytes (for example base64 in the existing JSON envelope) for a key.

## Definition of done

- [ ] **DoD-1** — A driver call sends a 128×128 PNG for one key, and the
  firmware's render state for that key holds the decoded pixels. **Proof:**
  a firmware pytest feeds a raw RGB565 payload through `wire.py` and
  `display_render`, and asserts the resulting bitmap's pixels match.
- [ ] **DoD-2** — A firmware reboot restores the last state each key held
  before power-off, built-in or custom. **Proof:** a firmware pytest sets a
  state, re-instantiates `MacroPad` fresh with the same fake storage
  backing, and asserts the restored render matches.
- [ ] **DoD-3** — Setting a second state for a key leaves no trace of the
  first. **Proof:** a firmware pytest sets two states in sequence for one
  key, reboots as in DoD-2, and asserts only the second remains, and that
  the key's storage holds exactly one file.
- [ ] **DoD-4** — An image that is not 128×128 is rejected without
  crashing the render loop. **Proof:** a driver test asserts the encoder
  refuses a wrong-sized image before it reaches the wire.
- [ ] **DoD-5** — `docs/wire-protocol.md` documents the new message, its
  framing, and the reserved Emoji ID sentinel. **Proof:**
  `docs/wire-protocol.md`, "Emoji IDs" and the new message's section.
- [ ] **DoD-6** — Driver tests cover the new transport message and plugin
  protocol addition. **Proof:** `go test ./driver/...` passes. `git stash
  && go test ./driver/... -run CustomGlyph` fails on `main`.
- [ ] **DoD-7** — `make flash` removes every persisted per-key state file,
  so no firmware version reads a file an earlier version wrote. **Proof:**
  run `make flash`, then check that `CIRCUITPY/glyph_state/` holds no
  files from before the run.
- [ ] **DoD-8** — The PR in the `pr` field links to this spec. **Proof:**
  PR body

## Risks

- Flash wear: a custom image write is up to 32,768 bytes per key. A plugin
  that sets the same key on every event can wear flash faster than today's
  read-mostly pattern. → Write only when the new state differs from the
  one already stored.
- Loop budget: task 0022 does not yet report `MacroPad.step`'s period with
  real displays attached. A CDC read and an occasional flash write inside
  or near that loop add a new cost against a budget that is not yet known.
  → Measure before merge, not after.
- Flash capacity: six keys at up to 32,768 bytes each is at least 196,608
  bytes, before the built-in table and CircuitPython itself. The confirmed
  board is a Pimoroni Pico Plus 2, RP2350B with 16MB flash and 8MB PSRAM
  (`docs/ppico_plus_2_pinout_diagram.pdf`). 196,608 bytes is under 2% of
  16MB, so raw capacity is not a real constraint. CircuitPython's own
  filesystem overhead on this flash chip is still unmeasured.

## Notes

This task follows a question from testing task 0029's virtual pad: can a
plugin use an image other than the built-in emoji table?
`docs/wire-protocol.md` already reserves room for this: "every other
[Emoji ID] value is unreserved, for a later task's emoji set." No version
ships against real hardware yet, so this task's wire change carries a low
compatibility cost today.
