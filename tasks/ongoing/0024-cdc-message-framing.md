---
id: "0024"
title: "Add a type and length header to every device→host CDC message"
status: "ongoing"
created: "2026-08-14"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0024-cdc-message-framing"
related: ["0002", "0014", "0020", "0021", "0022", "0025"]
tags: ["protocol", "driver", "firmware"]
---

# 0024 — Add a type and length header to every device→host CDC message

## Problem

`docs/wire-protocol.md` sends press/release events and audio chunks on one
CDC data channel. Neither message carries a field that says which message
follows. A reader of the real stream cannot tell a 10-byte event from an
audio chunk. Byte 0 is a key index in one and a stream ID in the other.

`driver/transport/emulator.go` hides this. It gives each message its own
`io.Pipe`, so `ReadEvent` and `ReadAudioChunk` never share a stream. Task
0021 opens one serial port and has no such separation.

## Goals

- One reader over one byte stream decodes both device→host messages.
- The reader knows the message type before it reads any field.
- An unknown message type is skipped by its length, not guessed at.
- A truncated message is an error, not a partial value.
- The emulator carries both messages on one stream, so tests and hardware
  use the same decode path.

## Non-goals

- The host→device HID path. A report ID and a fixed transfer size already
  frame it.
- The trace record type. Task 0025 adds one using this header.
- Device discovery and reconnect. Task 0021 owns `Open`.
- A message demultiplexer for callers. Task 0020 owns the facade.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — One type byte in front of each message

Each device→host message gains a leading byte that names its type. The
reader reads the byte, then calls the existing decode function.

- Good, because it costs one byte per message and no change to the three
  encode and decode functions in `driver/transport/wire.go`.
- Good, because the firmware writes one extra byte per event, so the loop
  in task 0022 needs no buffering change.
- Bad, because an unknown type has no length, so the reader cannot skip
  it. An older driver must close the stream when firmware adds a type.
- Bad, because a lost byte misaligns the stream forever. Every byte value
  is legal inside a PCM payload, so the wrong parse still succeeds.

### Approach B — A type and length header in front of each message

Each device→host message gains a 3-byte header: a type byte and a
little-endian `uint16` payload length. The reader reads the header, reads
exactly that many bytes, then decodes them.

- Good, because an unknown type is skipped by its length, so firmware can
  add a message type that an older driver ignores.
- Good, because the audio chunk's own length field folds into the header,
  so the format has one length concept instead of two.
- Bad, because it costs 3 bytes for every 10-byte event, which is a 30%
  increase on the event path.
- Bad, because a lost byte still misaligns the stream. The reader then
  reads a length from payload bytes and consumes a wrong count.

### Approach C — COBS frames ended by a zero byte

Each message is COBS-encoded and terminated with `0x00`. The type byte
rides inside the frame. The reader splits the stream on `0x00`.

- Good, because resynchronization is defined. After any corruption the
  reader skips to the next `0x00` and is aligned again.
- Good, because a serial buffer holding half a message from before the
  board rebooted costs one frame, not the whole session.
- Bad, because the firmware must COBS-encode PCM in Python at chunk rate.
  Task 0007 has no measurement, so the cost is unknown and untestable now.
- Bad, because a raw serial dump is no longer readable by eye, which
  removes the cheapest hardware bring-up check in task 0010.

## Decision

Chosen: **Approach B — a type and length header**.

Task 0025 adds a message type, and later firmware will add more, so the
reader must skip a type it does not know. Approach A cannot skip. Approach
C pays its cost on the audio path, which no task has built or measured.

The cost accepted is that a lost byte misaligns the stream with no
recovery. `Open` in task 0021 is the only recovery path. Approach C stays
open as a later task: COBS wraps this header without changing it.

## Design

`docs/wire-protocol.md` gains a "Framing" section and a message type
registry. Type `1` is the press/release event, type `2` is the audio
chunk. The audio chunk drops its own chunk-length field, because the
header carries the length.

The version byte stays at `1`. The document states that version `1` may
change until firmware runs on hardware, and no firmware has.

`Transport` cannot keep two blocking readers on one stream: `ReadEvent`
would consume an audio chunk. Both methods are replaced by one
`ReadMessage() (Message, error)`, where `Message` holds a type and the
decoded value. The emulator writes both messages to one pipe.

Files to change:

- `docs/wire-protocol.md` — new "Framing" section and type registry
- `driver/transport/wire.go` — `MessageType`, `writeFrame`, `readFrame`,
  skip on unknown type
- `driver/transport/transport.go` — `Message`, `ReadMessage` replaces
  `ReadEvent` and `ReadAudioChunk`
- `driver/transport/emulator.go` — one pipe and one queue for both
  device→host messages
- `driver/transport/emulator_test.go` — mixed-stream, unknown-type, and
  truncation cases

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — One stream holding an event, an audio chunk, then an
  event decodes to those three values in that order. **Proof:**
  `go test ./driver/transport/ -run TestReadMessageMixedStream`
- [ ] **DoD-2** — A frame with type `0xFE` is skipped by its length, and
  the next known message decodes. **Proof:**
  `go test ./driver/transport/ -run TestReadMessageUnknownType`
- [ ] **DoD-3** — A frame whose header declares 40 bytes and whose stream
  ends after 10 returns `io.ErrUnexpectedEOF` and no value. **Proof:**
  `go test ./driver/transport/ -run TestReadMessageTruncated`
- [ ] **DoD-4** — `Transport` has no `ReadEvent` and no `ReadAudioChunk`.
  **Proof:** `go doc ./driver/transport Transport`
- [ ] **DoD-5** — The new tests fail on `main`. **Proof:**
  `git stash && go test ./driver/transport/` fails
- [ ] **DoD-6** — `docs/wire-protocol.md` states the 3-byte header, the
  type registry, and that the audio chunk no longer carries its own
  length. **Proof:** `docs/wire-protocol.md`, section "Framing"
- [ ] **DoD-7** — The PR body links to this spec. **Proof:** the PR in the
  `pr` field

## Risks

- Task 0021 and task 0020 both build on `Transport`. → Land this task
  before either starts, and list it in their `related` field.
- A lost byte gives silent wrong parses. → Task 0025 logs every decoded
  message, so a wrong parse shows in the log instead of hiding.

## Open questions

- [ ] Does the firmware write the header and payload in one `write` call,
  or two? Two calls risk an interleave if audio ever writes from another
  task. — answered by task 0022
