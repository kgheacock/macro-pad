---
id: "0008"
title: "Add the USB composite (HID + CDC) descriptor"
status: "complete"
created: "2026-08-03"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/9"
branch: "0008-usb-composite-descriptor"
related: ["0002"]
tags: ["firmware", "usb"]
---

# 0008 — Add the USB composite (HID + CDC) descriptor

## Problem

`firmware/README.md` commits to enumerating as a USB-C composite device
with HID for the primary action and CDC serial for state, events, and
audio, but no `boot.py` or USB descriptor configuration exists, and
enumeration cannot run without the RP2350 board attached.

## Goals

- `boot.py` configures the RP2350 to enumerate as a composite HID and CDC
  device.
- The configuration is written and reviewable now, even though only task
  0010 can verify enumeration on real hardware.
- The HID report descriptor matches the key-state format from task 0002.

## Non-goals

- Verifying real host-side enumeration. Task 0010 covers that, once
  hardware exists.
- Driver-side USB handling, which belongs to `driver/`'s own scope.

## Approaches considered

### Approach A — CircuitPython's built-in usb_hid and usb_cdc config

Use CircuitPython's documented `usb_hid.enable()` and `usb_cdc.enable()`
calls in `boot.py`, with a custom HID report descriptor that matches task
0002's format.

- Good, because it uses the officially supported CircuitPython USB
  configuration path, documented and stable across versions.
- Good, because it needs minimal code, one `boot.py` file.
- Bad, because CircuitPython's default USB device limits mean this needs
  care if the board later exposes other USB devices, such as mass storage.
- Bad, because report descriptor correctness can only be confirmed once a
  real host enumerates the device in task 0010.

### Approach B — Vendor-specific USB class instead of HID

Drop the standard HID class. Expose only a CDC serial channel, and send
key state as a custom serial protocol too.

- Good, because it uses one channel instead of two, a simpler descriptor.
- Good, because it avoids HID report descriptor size and format
  constraints entirely.
- Bad, because it contradicts `firmware/README.md`'s explicit choice of
  HID for the primary action — HID gives the host driver free OS-level key
  support that a CDC-only scheme would lose.
- Bad, because it loses standard OS-level HID recognition, a stated design
  intent.

### Approach C — Configure TinyUSB directly

Bypass `usb_hid` and `usb_cdc`. Set TinyUSB descriptors by hand for finer
control.

- Good, because it gives full control over descriptor details that
  CircuitPython's built-in API might not expose.
- Good, because the descriptor could end up smaller or more optimized.
- Bad, because CircuitPython wraps TinyUSB specifically so firmware does
  not need this level of control — bypassing it forfeits that stability
  across CircuitPython updates.
- Bad, because it needs much more code and MCU-specific knowledge for a
  benefit, descriptor size, that `firmware/README.md` does not ask for.

## Decision

Chosen: **Approach A — CircuitPython's built-in usb_hid and usb_cdc
config**.

It is the documented, supported path for the exact HID and CDC composite
device `firmware/README.md` already asks for, and it needs the least code.
This choice accepts that this task can write and review the descriptor,
but not verify enumeration, until task 0010.

## Design

`firmware/boot.py` calls `usb_hid.enable((KEY_STATE_DEVICE,))` with a
custom `usb_hid.Device` report descriptor sized to task 0002's HID message
format, plus `usb_cdc.enable(console=False, data=True)` for the event and
audio channel.

Files to change:

- `firmware/boot.py` — new
- `firmware/README.md` — record the two USB interfaces and the descriptor
  location

## Definition of done

- [x] **DoD-1** — `boot.py` defines a `usb_hid.Device` report descriptor
  with a report size that matches task 0002's HID message layout.
  **Proof:** `firmware/boot.py`
- [x] **DoD-2** — `boot.py` enables a CDC data channel
  (`usb_cdc.enable(data=True)`). **Proof:** `firmware/boot.py`
- [x] **DoD-3** — `firmware/README.md` names both USB interfaces and links
  to `docs/wire-protocol.md`. **Proof:** `firmware/README.md`
- [x] **DoD-4** — The file parses without error under CircuitPython's
  `boot.py` syntax rules. **Proof:** `python -m py_compile firmware/boot.py`
- [x] **DoD-5** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- Real enumeration behavior stays unverified until hardware exists → task
  0010 confirms the host sees both interfaces correctly.
