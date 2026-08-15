---
id: "0021"
title: "Implement the hardware Transport over real HID and CDC serial"
status: "ongoing"
created: "2026-08-14"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/15"
branch: "0021-hardware-transport-implementation"
related: ["0002", "0008", "0010", "0013", "0014", "0020", "0024"]
tags: ["driver", "usb", "hardware"]
---

# 0021 — Implement the hardware Transport over real HID and CDC serial

## Problem

`transport.Emulator` is the only `Transport`. No driver code can reach an
attached board. Task 0014 named this work "a later task" and it was never
written. Tasks 0013 and 0020 both assume it exists.

## Goals

- `transport.Open` finds the attached macro pad and returns a `Transport`
  that carries real key-state, event, and audio-chunk traffic.
- Discovery matches the device by USB vendor and product ID, never by a
  volume listing.
- With no board attached, `Open` returns an error naming the device it
  looked for, within a bounded wait.
- After the board reboots, a bounded retry re-opens the same device.
- The same encode and decode functions in `driver/transport/wire.go` serve
  both the emulator and the hardware path.

## Non-goals

- Proving traffic against silicon. No board is wired yet, so task 0010
  owns that check.
- Linux and Windows support. This project is developed on macOS.
- Reconnect-in-place after an unplug mid-session. `Open` is the only
  recovery path in this version.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Class drivers: a hidapi binding plus a serial library

Use a cgo hidapi binding for the HID output report, and `go.bug.st/serial`
for the CDC channel. Both talk to the operating system's own class drivers.

- Good, because macOS already binds its HID and CDC class drivers to a
  composite device, so no interface claiming or driver removal is needed.
- Good, because CDC appears as a `/dev/cu.usbmodem*` file, which a person
  can open with `screen` to debug the same traffic by hand.
- Bad, because hidapi needs cgo, so the driver stops building with
  `CGO_ENABLED=0` and cross-compiling gains a toolchain requirement.
- Bad, because it adds two dependencies with two failure modes, and the
  two channels must be matched to one physical device by serial number.

### Approach B — Claim both interfaces directly with libusb (`gousb`)

Bypass the class drivers. Claim the HID and CDC interfaces and read and
write their bulk and interrupt endpoints directly.

- Good, because one library covers both channels, so device matching is a
  single enumeration with no serial-number correlation.
- Good, because raw endpoint access exposes the real packet boundaries,
  which makes a framing bug visible instead of hidden by a class driver.
- Bad, because macOS binds its HID class driver to the interface at
  enumeration, so claiming it fails without a kext or an entitlement.
- Bad, because it still needs cgo for libusb, so it pays Approach A's build
  cost without gaining a pure-Go build.

### Approach C — Drop host HID: carry key state over CDC as well

Raise the wire protocol to version 2. Send key state over the same CDC
serial channel with a framing byte, and delete the host's HID path.

- Good, because it removes cgo entirely. `go.bug.st/serial` is pure Go, so
  the driver builds and cross-compiles with no toolchain.
- Good, because one channel needs one open, one error path, and one
  reconnect rule, which is less code than two.
- Bad, because a stream channel needs framing and resynchronization that
  HID's report boundaries give for free, and a desynchronized reader must
  find the next message start.
- Bad, because it discards task 0008's shipped descriptor and forces a
  firmware change, so both sides must move together for no user-visible
  gain.

## Decision

Chosen: **Approach A — class drivers, a hidapi binding plus
`go.bug.st/serial`**.

It is the only option macOS permits without an entitlement, and it keeps
task 0008's composite descriptor as designed. The cost accepted is cgo:
the driver no longer builds with `CGO_ENABLED=0`, and the two channels
must be correlated by USB serial number.

## Design

`driver/transport/device.go` adds `Device`, a `Transport` over real
hardware.

- `Open(ctx, Options) (*Device, error)` enumerates HID devices, matches
  `Options.VendorID` and `Options.ProductID`, reads the matched device's
  USB serial number, then opens the `/dev/cu.usbmodem*` port carrying the
  same serial number. It retries until `ctx` expires, so a caller can
  survive the reboot that `make flash` causes.
- `SendKeyState` writes one output report with `encodeKeyState`, prefixed
  by `boot.py`'s `KEY_STATE_REPORT_ID`.
- `ReadMessage` decodes one frame from the CDC stream, using the type and
  length header that task 0024 adds. `Device` does not route by type. Task
  0020's facade owns the fan-out to per-type callers.
- `Close` releases both handles and unblocks both readers with `io.EOF`,
  matching `Emulator`.

Files to change:

- `driver/transport/device.go` — new. `Device`, `Open`, `Options`
- `driver/transport/device_test.go` — new. Discovery, timeout, and error
  cases against a fake enumerator
- `driver/transport/transport.go` — add `Close() error` to `Transport`.
  Task 0020 lists the same change; whichever lands first makes it

Task 0024 is a prerequisite. It replaces `ReadEvent` and `ReadAudioChunk`
with `ReadMessage`, and defines the header this reader needs.
- `driver/go.mod` — add the hidapi binding and `go.bug.st/serial`
- `driver/README.md` — document `Open`, the cgo requirement, and the
  macOS-only scope

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — `var _ Transport = (*Device)(nil)` compiles, so `Device`
      satisfies the interface the emulator satisfies. **Proof:** `go build
      ./driver/...`
- [ ] **DoD-2** — With no matching device present, `Open` returns an error
      naming the vendor and product ID it looked for. **Proof:** `go test
      ./driver/transport/... -run TestOpen_NoDevice`
- [ ] **DoD-3** — `Open` returns within 200 ms of its context deadline
      when no device appears. **Proof:** `go test ./driver/transport/...
      -run TestOpen_RespectsDeadline`
- [ ] **DoD-4** — A device that appears on the third enumeration attempt
      is opened, not skipped. **Proof:** `go test ./driver/transport/...
      -run TestOpen_RetriesUntilPresent`
- [ ] **DoD-5** — Discovery calls no volume listing. **Proof:** `grep -rn
      "Volumes\|diskutil\|mount" driver/transport/` returns nothing.
- [ ] **DoD-6** — A CDC stream carrying one event followed by one audio
      chunk decodes to those two messages, in that order, through
      `ReadMessage`. **Proof:** `go test
      ./driver/transport/... -run TestDevice_ReadsMixedStream`
- [ ] **DoD-7** — `Close` unblocks a pending `ReadMessage` with `io.EOF`.
      **Proof:** `go test ./driver/transport/... -run
      TestDevice_CloseUnblocksReader`
- [ ] **DoD-8** — `driver/README.md` records the cgo requirement and the
      macOS-only scope. **Proof:** `driver/README.md`
- [ ] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:**
      PR body

## Risks

- The reader tests need the stream split from a real device, which does
  not exist yet → `Device` reads from an `io.Reader` seam, so the tests
  above feed bytes directly and task 0010 supplies the real stream.
- cgo breaks a `CGO_ENABLED=0` build → documented in `driver/README.md`,
  and no CI target depends on a static build today.
- A second CircuitPython board attached at the same time matches the same
  vendor and product ID → `Options` accepts an optional serial number, and
  `Open` errors when two devices match and none is named.

## Open questions

- [ ] Which vendor and product ID does the board enumerate with after task
      0008's descriptor, and does `boot.py` need to set them? — hardware
      owner, from `system_profiler SPUSBDataType` at bring-up.
- [ ] Does CircuitPython's HID output report arrive at firmware through
      `get_last_received_report`, and does it include the report ID byte? —
      implementer, confirmed during task 0022.
