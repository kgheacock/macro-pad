---
id: "0002"
title: "Define the host/device wire protocol"
status: "backlog"
created: "2026-08-03"
updated: "2026-08-03"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0006", "0007", "0008"]
tags: ["firmware", "driver", "protocol"]
---

# 0002 — Define the host/device wire protocol

## Problem

Firmware and driver talk over USB (HID and CDC serial), but no message
format exists for per-key state pushes, raw press/release events, or
chunked audio. Tasks 0006, 0007, 0008, and all driver work have no contract
to build against.

## Goals

- One document defines every message: host→device key state, device→host
  press/release event, device→host audio chunk.
- Each message states its byte layout, not only a description.
- Firmware and driver can build against the document independently.

## Non-goals

- Implementing the protocol in firmware or driver code. This spec is the
  contract only.
- USB descriptor and enumeration details. Task 0008 covers that.

## Approaches considered

### Approach A — Fixed-width binary struct

Define a fixed-size binary record for each message type, sent as raw bytes
over the HID report or the CDC channel.

- Good, because parsing stays cheap on a resource-constrained MCU.
- Good, because a fixed size makes audio chunk framing simple.
- Bad, because a fixed width wastes bytes on short messages, such as a
  color-only update.
- Bad, because a field addition breaks the format unless a version byte
  guards it.

### Approach B — JSON-lines over CDC serial

Send newline-delimited JSON objects for every message, including audio as
base64-encoded chunks.

- Good, because a serial terminal can read the messages directly.
- Good, because a new field does not break an existing parser.
- Bad, because JSON parsing costs more CPU time and RAM on the RP2350 than
  a fixed struct, a real constraint during audio streaming.
- Bad, because base64 adds about 33% to the audio payload size.

### Approach C — Adopt an existing raw-HID keyboard protocol

Reuse an open keyboard-firmware raw-HID report format, such as a
QMK/VIA-style scheme.

- Good, because the design is proven, with some inspection tooling already
  built.
- Good, because it saves design work.
- Bad, because those schemes cover keypresses only. They have no framing
  for audio streaming or per-key color, so most of the format still needs
  custom work.
- Bad, because they assume a fixed report size that may not fit the audio
  use case.

## Decision

Chosen: **Approach A — Fixed-width binary struct**.

The device is memory-constrained and streams audio, so a fixed-width struct
keeps parsing cheap and framing simple. This choice accepts a version byte
and no self-describing fields — both sides must update together when the
format changes.

## Design

A shared document, `docs/wire-protocol.md`, defines three message types
over two channels:

- HID (host→device): 1-byte key index, 1-byte version, 2-byte RGB565 color,
  1-byte emoji ID, 1-byte blink flag.
- CDC (device→host): press/release event — 1-byte key index, 1-byte event
  type, 8-byte monotonic timestamp in microseconds.
- CDC (device→host): audio chunk — 1-byte stream ID, 2-byte chunk length,
  N-byte PCM payload, 1-byte final-chunk flag.

Files to change:

- `docs/wire-protocol.md` — new, the message layouts above
- `firmware/README.md` — link to the protocol document
- `driver/README.md` — link to the protocol document

## Definition of done

- [ ] **DoD-1** — `docs/wire-protocol.md` defines the byte layout for all
  three message types, with field sizes. **Proof:** `docs/wire-protocol.md`
- [ ] **DoD-2** — Each message type states which side sends it and over
  which channel. **Proof:** `docs/wire-protocol.md`, message table
- [ ] **DoD-3** — The document states the version byte's role and how a
  breaking change raises it. **Proof:** `docs/wire-protocol.md`, section
  "Versioning"
- [ ] **DoD-4** — `firmware/README.md` and `driver/README.md` link to the
  new document. **Proof:** both files
- [ ] **DoD-5** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- A format change after firmware and driver both build against it forces
  rework on both sides → the version byte and a frozen v1 before tasks
  0006–0008 start reduce this.
