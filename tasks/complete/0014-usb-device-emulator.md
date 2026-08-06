---
id: "0014"
title: "Emulate the composite USB device for driver tests"
status: "complete"
created: "2026-08-04"
updated: "2026-08-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/3"
branch: "0014-usb-device-emulator"
related: ["0001", "0002", "0008", "0013"]
tags: ["driver", "testing", "usb"]
---

# 0014 — Emulate the composite USB device for driver tests

## Problem

No driver code can be tested against a device today. No board is wired up
yet, and task 0013 already plans to send real Key state HID messages. Task
0001 solved this problem for firmware. The driver side has no equivalent.

## Goals

- Driver tests exchange key-state, press/release, and audio-chunk messages
  against a software stand-in for the device, with no board attached.
- The stand-in encodes and decodes the exact byte layout in
  `docs/wire-protocol.md`, not a simplified version.
- Test code injects a synthetic press event or audio chunk and reads the
  result back.
- A `Transport` interface separates driver logic from the physical
  connection, so the same driver code can later run against real hardware.

## Non-goals

- A real OS-level HID/CDC transport implementation. This task builds the
  interface and its test stand-in only. A later task wires the interface to
  a real HID/serial library.
- Checking real USB enumeration or timing against silicon. Task 0010
  covers that.
- Firmware-side testing. Task 0001 already covers that.
- tmux-based session ownership (tasks 0011, 0012). That work carries no
  wire-protocol traffic.

## Approaches considered

### Approach A — In-process fake `Transport` (Go interface plus an in-memory,
byte-accurate emulator)

Define a `driver/transport.Transport` interface for the three wire-protocol
exchanges. Add an in-memory emulator that implements it over `io.Pipe`. The
emulator encodes and decodes real bytes per `docs/wire-protocol.md`.

- Good, because it runs on any dev machine, including macOS, with no kernel
  access or root privilege. Task 0001 used the same fix for firmware.
- Good, because it encodes and decodes the real wire bytes, so it catches
  driver-side framing bugs, not only logic bugs.
- Bad, because it never exercises the OS HID/serial driver stack, so a real
  permissions or enumeration bug still waits for task 0010.
- Bad, because the emulator's protocol behavior is hand-written and can
  drift from firmware's real behavior over time.

### Approach B — Standalone emulator process over a pty and a Unix socket

Build a separate emulator binary that runs firmware protocol logic. Expose
CDC traffic over a real pseudo-terminal pair and HID-equivalent traffic over
a Unix domain socket. The driver's transport config points at these paths
instead of a real device.

- Good, because a real pty exercises partial reads and framing in the same
  way that a real CDC connection does.
- Good, because the emulator runs as its own process, so a person can drive
  it by hand for a demo, not only from `go test`.
- Bad, because HID has no natural OS-level stand-in on macOS, so the driver
  needs a second code path for the socket-based fake HID channel.
- Bad, because it adds process and socket lifecycle management that every
  test run must start, wait on, and tear down.

### Approach C — OS-level virtual USB gadget (Linux `dummy_hcd` or
`raw-gadget`)

Use the Linux kernel USB gadget subsystem to enumerate a real composite HID
and CDC device, backed by a user-space program. The unmodified production
driver then talks to it exactly as it talks to real hardware.

- Good, because it is the highest-fidelity option: real enumeration, real
  report descriptor bytes, and the exact code path used with real hardware.
- Good, because it can also check task 0008's firmware descriptor work on
  the same virtual bus.
- Bad, because it needs Linux kernel modules and often root access. The dev
  machine in this repo is macOS, so every run needs a Linux VM or a
  container.
- Bad, because ConfigFS and gadget setup are fragile in CI and cost far
  more to build than the driver-testing need at hand.

## Decision

Chosen: **Approach A — in-process fake `Transport`**.

The dev machine is macOS. macOS has no kernel gadget support, and driver
code does not exist yet. This task is the right point to add a `Transport`
interface that both real and test code share. This choice accepts that no
OS-level HID/CDC path is checked until a later task builds the real
transport and task 0010 checks it against hardware.

## Design

`driver/transport` defines a `Transport` interface: `SendKeyState`,
`ReadEvent`, and `ReadAudioChunk`. These match `docs/wire-protocol.md`.
`driver/transport/wire.go` holds the shared encode and decode functions for
each message. `driver/transport/emulator.go` implements `Transport`
in-memory over `io.Pipe`. It adds test-only methods to inject a
press/release event or an audio chunk, and to inspect the last key-state
message sent.

Files to change:

- `driver/go.mod` — new, unless task 0011 already created it
- `driver/transport/transport.go` — new, `Transport` interface and message
  structs
- `driver/transport/wire.go` — new, encode and decode functions
- `driver/transport/emulator.go` — new, in-memory emulator
- `driver/transport/emulator_test.go` — new, round-trip tests
- `driver/README.md` — document the emulator and how to run driver tests
  without hardware

## Definition of done

- [x] **DoD-1** — `driver/transport` defines a `Transport` interface
  covering key-state send, event read, and audio-chunk read. **Proof:**
  `driver/transport/transport.go`
- [x] **DoD-2** — A key-state message written and read through the emulator
  matches `docs/wire-protocol.md`'s Key state byte layout, including the
  version byte. **Proof:** `go test ./driver/transport/... -run
  TestEmulator_KeyStateRoundTrip`
- [x] **DoD-3** — Test code injects a synthetic press event and reads it
  back through `Transport.ReadEvent`, with no board attached. **Proof:**
  `go test ./driver/transport/... -run TestEmulator_InjectPressEvent`
- [x] **DoD-4** — Test code injects multiple audio chunks and
  `Transport.ReadAudioChunk` stops at the final-chunk flag. **Proof:** `go
  test ./driver/transport/... -run TestEmulator_AudioFinalChunk`
- [x] **DoD-5** — Sending a key-state message with an unrecognized version
  byte returns a named error instead of a silent mismatch. **Proof:** `go
  test ./driver/transport/... -run TestEmulator_VersionMismatch`
- [x] **DoD-6** — `driver/README.md` documents the emulator and the command
  to run driver tests without hardware. **Proof:** `driver/README.md`
- [x] **DoD-7** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- The emulator's protocol behavior can drift from real firmware timing and
  behavior → task 0010's hardware bring-up is the cross-check once the
  board exists.
- The `Transport` interface shape can change once a real HID/CDC
  implementation exists → accepted, since only this task's tests depend on
  it today.

## Open questions

- [ ] Should the real HID/CDC transport get its own task now, or wait until
  tasks 0011–0013 land? — repo owner
