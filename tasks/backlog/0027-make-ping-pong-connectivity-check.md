---
id: "0027"
title: "Add a make ping-pong target that proves host and device talk over USB"
status: "backlog"
created: "2026-08-15"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0008", "0019", "0021", "0022", "0024", "0026"]
tags: ["firmware", "driver", "protocol", "dx", "bring-up"]
---

# 0027 — Add a make ping-pong target that proves host and device talk over USB

## Problem

`transport.Device`, built in task 0021, has never sent a byte to a real
board. `firmware/app.py`, built in task 0022, has never read a byte from
a real host. Task 0010's full bring-up waits on parts, and task 0020's
end-to-end harness waits on displays and glyphs. No command today proves
that the HID and CDC channels task 0008 wired up carry data at all.

## Goals

- One command flashes a small firmware image that answers a ping, with
  no displays, mic, or switches involved.
- A host command sends the ping and confirms the matching pong within a
  bounded time.
- The host command prints PASS or FAIL and exits with a matching code.
- `make ping-pong` needs no console and no manual port lookup.
- `firmware/code.py` and `firmware/boot.py` stay unchanged on disk;
  `make flash` restores the real app afterward.

## Non-goals

- Testing the full `MacroPad` app: displays, the mic, or switch
  debounce. Task 0010 owns that.
- Fixing `app.py`'s `_scan_switches`, which writes an event with no
  frame header — found while reading the code for this spec. This is a
  separate bug and belongs in a follow-up task.
- Audio chunk traffic. No firmware captures audio yet.
- Any transport but `transport.Device` over real USB. `transport.Emulator`
  already proves the byte layout in memory.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Reuse Key State and Event, with no protocol document change

The host sends a Key State message for a reserved key index, 255, with a
nonce packed into the emoji ID byte. A small firmware image, watching for
that index, answers with the existing Event message, carrying the same
index and the nonce in the timestamp field.

- Good, because it adds no message type. `driver/transport/wire.go` and
  `firmware/wire.py` keep their current encode and decode functions
  unchanged.
- Good, because `transport.Device`'s `ReadMessage` already decodes an
  Event, so the host command needs no new decoder.
- Bad, because the reserved index and the nonce placement live only in
  this task's code, not in `docs/wire-protocol.md`, so a future reader
  has no place to learn the convention.
- Bad, because packing a nonce into a timestamp field reuses a field the
  document defines for a different purpose, so a real event can be
  mistaken for a pong.

### Approach B — A documented Pong message, sent from a minimal firmware image

`docs/wire-protocol.md` gains a reserved key index for the ping and a
type-3 Pong message for the reply, in a new "Ping" section. A firmware
image, separate from `app.py`, answers over the same HID and CDC
channels the real app uses. A small Go command drives `transport.Device`
and confirms the nonce comes back unchanged.

- Good, because the message lives in the same document as every other
  message, so `docs/wire-protocol.md` stays the one source for the wire
  format.
- Good, because it exercises the exact channel `transport.Device` and
  `boot.py`'s descriptor were built for, and this also answers task
  0021's open question about the board's vendor and product ID.
- Bad, because it touches `docs/wire-protocol.md`,
  `driver/transport/wire.go`, and `firmware/wire.py` for one message
  that exists only to confirm connectivity.
- Bad, because the minimal firmware image duplicates `app.py`'s
  HID-read and CDC-write calls outside the tested main loop, so the two
  paths can drift apart.

### Approach C — Round-trip a line through the existing debug console

`make ping-pong` runs `make debug`, opens the console port task 0026
already prints, sends one Python line, and confirms the line's output
comes back within a timeout.

- Good, because it adds no firmware code and no protocol change. Task
  0026 already ships everything it needs.
- Good, because it needs no vendor or product ID lookup, the exact
  piece task 0021 leaves open.
- Bad, because it proves the console UART works, not the HID and CDC
  channels the real product and `transport.Device` depend on.
- Bad, because task 0026 states that finding the console port is a
  manual step, so a fully unattended round trip still needs new
  port-discovery code.

## Decision

Chosen: **Approach B — a documented Pong message, sent from a minimal
firmware image**.

This check exists to prove the real product channel works: the one
`transport.Device` and `boot.py`'s descriptor carry. Approach C tests a
different USB interface, so it cannot prove that. Approach B accepts new
code in three files, and a firmware image that runs its own small HID
and CDC path next to `app.py`'s, in exchange for a message a future
reader can find documented, and a check that also answers task 0021's
open vendor and product ID question.

## Design

`docs/wire-protocol.md` gains a "Ping" section: key index 255 in the
existing Key State message signals a ping, and carries a caller-chosen
nonce in the Emoji ID byte. Type 3 in the Framing registry is Pong, a
1-byte payload that echoes that nonce.

`firmware/wire.py` gains `encode_pong(nonce)`, which writes the 3-byte
frame header before the payload. `firmware/ping_pong.py` is new: a loop
with no displays, mic, or switches, that reads one HID report and, on
index 255, writes the pong.

`driver/transport/wire.go` gains `MessageTypePong` and a `Pong` struct.
`driver/cmd/pingpong/main.go` is new: it opens `transport.Device`, sends
the ping, waits on `ReadMessage` for the matching nonce, prints PASS or
FAIL, and exits with a matching code.

`Makefile` gains `.PHONY: ping-pong`, which depends on `check-circuitpy`.
It writes `firmware/boot.py` and `firmware/ping_pong.py` to
`$(CIRCUITPY_VOLUME)` as `boot.py` and `code.py`, runs the host command,
then prints a reminder to run `make flash`.

Files to change:

- `docs/wire-protocol.md` — new "Ping" section, type-3 Pong in the
  Framing registry
- `firmware/wire.py` — `encode_pong`, frame-header write
- `firmware/ping_pong.py` — new, the minimal image
- `driver/transport/wire.go`, `driver/transport/transport.go` —
  `MessageTypePong`, `Pong`, decode support
- `driver/transport/wire_test.go` — new, a round-trip test for the Pong
  frame
- `driver/cmd/pingpong/main.go` — new, the host command
- `Makefile` — `.PHONY: ping-pong` target
- `firmware/README.md`, `driver/README.md` — document the command

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — `docs/wire-protocol.md` documents key index 255 as the
      ping and type 3 as Pong, with the nonce field. **Proof:** read
      `docs/wire-protocol.md`, section "Ping".
- [ ] **DoD-2** — A Pong frame built with a given nonce decodes to that
      same nonce. **Proof:** `go test ./driver/transport/... -run
      TestPong`
- [ ] **DoD-3** — With the board attached and its vendor and product ID
      set, `make ping-pong` flashes the minimal image, receives a
      matching pong, prints PASS, and exits 0. **Proof:** run `make
      ping-pong` with the board attached.
- [ ] **DoD-4** — With no board attached, `make ping-pong` exits
      non-zero within 5 s and names the missing device. **Proof:** run
      `make ping-pong` with the board unplugged; check the exit code,
      the message, and the elapsed time.
- [ ] **DoD-5** — A pong that carries the wrong nonce makes the host
      command print FAIL and exit non-zero, not PASS. **Proof:** `go
      test ./driver/cmd/pingpong/... -run TestWrongNonce`, against a
      fake transport.
- [ ] **DoD-6** — `make ping-pong` leaves `firmware/code.py` and
      `firmware/boot.py` unchanged on disk, and `make flash` afterward
      restores the real `code.py` on the board. **Proof:** `git status
      --porcelain firmware/` prints nothing after `make ping-pong`; run
      `make flash`, then diff `firmware/code.py` against the board's
      copy.
- [ ] **DoD-7** — `firmware/README.md` and `driver/README.md` name
      `make ping-pong` as the connectivity check, next to `make debug`.
      **Proof:** both files.
- [ ] **DoD-8** — The PR in the `pr` field links to this spec.
      **Proof:** PR body.

## Risks

- The board's vendor and product ID are still unknown, per task 0021's
  open question → finding them, from `system_profiler SPUSBDataType`,
  is this task's first live step, and `driver/cmd/pingpong` takes them
  as flags, so the value lands in one place.
- `app.py`'s `_scan_switches` writes an Event with no frame header,
  found while reading the code for this spec → this task's image writes
  its own framed Pong and shares no code with that path, but the
  mismatch stays live in `app.py` until a follow-up task fixes it.
- A soft reload after the image write can outlast a short timeout →
  `transport.Open` already retries until its context expires, per task
  0021's design, and the host command reuses that call.

## Open questions

- [ ] Which vendor and product ID does the board enumerate with? —
      implementer, from `system_profiler SPUSBDataType`, at the start
      of this task.
- [ ] Does a wrong flag value for the vendor or product ID need an
      error distinct from "no device attached"? — implementer, while
      writing `driver/cmd/pingpong`.

## Notes

Found while reading `firmware/app.py` for this spec: `_scan_switches`
writes `wire.encode_event`'s raw bytes straight to `usb_cdc.data`, with
no type-and-length header. `transport.Device`'s `ReadMessage`, built in
task 0021, expects every device→host message wrapped in that header. A
real key press today desyncs that reader. This is worth its own
follow-up task.
