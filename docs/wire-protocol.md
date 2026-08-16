# Host/device wire protocol

This document defines every message that crosses the USB link between the
driver (host) and the firmware (device). It is the contract both sides build
against. See [`firmware/README.md`](../firmware/README.md) and
[`driver/README.md`](../driver/README.md) for how each side uses it.

All messages are fixed-width binary structs. Multi-byte fields are
little-endian, matching the RP2350's native byte order.

## Messages

| Message | Sender | Channel | Size |
|---|---|---|---|
| Key state | Host → Device | HID | 6 bytes |
| Set custom glyph | Host → Device | CDC serial | 1 + 32,768 bytes |
| Press/release event | Device → Host | CDC serial | 10 bytes |
| Audio chunk | Device → Host | CDC serial | 2 + N bytes |
| Pong | Device → Host | CDC serial | 1 byte |
| Trace record | Device → Host | CDC serial | 12 bytes |

Sizes for every message but Key state are the payload only. Every message
on the CDC data channel, in either direction, is wrapped in the 3-byte
frame header described in [Framing](#framing).

## Framing

The CDC data channel carries messages in both directions — Set custom
glyph, host → device, and every other CDC message, device → host — and
none of them carries a field that says which message it is: byte 0 is a
key index in some and a stream ID in another, so a reader cannot tell them
apart on its own. Every CDC message, in either direction, is prefixed with
a 3-byte frame header:

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Type | Identifies the message that follows — see the type registry below |
| 1 | 2 | Length | Length of the payload that follows, little-endian `uint16` |

The reader reads the header, reads exactly `Length` payload bytes, then
decodes them if it knows `Type`. A type it does not know is skipped by
`Length`, not guessed at, so either side can add a message type the other
end's build does not know yet. A stream that ends before `Length` payload
bytes arrive is a truncated message, not a partial value.

### Type registry

| Type | Message | Direction |
|---|---|---|
| 1 | Press/release event | Device → Host |
| 2 | Audio chunk | Device → Host |
| 3 | Pong | Device → Host |
| 4 | Trace record | Device → Host |
| 5 | Set custom glyph | Host → Device |

The host→device key-state message is not framed this way. It rides on
HID, where a report ID and a fixed transfer size already identify it.

### Key state (HID, host → device)

Sent whenever the driver wants a key's emoji, color, or blink state to
change. One message covers one key.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Key index | 0-based index of the target key |
| 1 | 1 | Version | Protocol version this message was built against — see [Versioning](#versioning) |
| 2 | 2 | Color | Background color, RGB565, little-endian |
| 4 | 1 | Emoji ID | Index into the firmware's emoji bitmap table — see [Emoji IDs](#emoji-ids) |
| 5 | 1 | Blink flag | `0` = steady, `1` = blink |

### Set custom glyph (CDC, host → device)

Sent whenever the driver wants a key to show an arbitrary image instead of
a built-in glyph table entry. One message replaces one key's image
entirely — there is no way to patch part of it. Framed per
[Framing](#framing) as type `5`.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Key index | 0-based index of the target key |
| 1 | 32,768 | Pixels | 128×128 image, row-major, RGB565, 2 bytes per pixel, little-endian |

32,768 bytes is 128 × 128 pixels × 2 bytes per pixel — the whole image in
one frame, under the frame header's `uint16` `Length` field's limit, so
this message needs no multi-frame reassembly on either side. The driver
decodes the source PNG and converts its colors to RGB565 itself; firmware
never parses an image format, it only copies length-prefixed bytes into a
bitmap and a file. See
[task 0030](../tasks/ongoing/0030-custom-glyph-upload-and-persistence.md)
for the design decision, including why this task keeps the source PNG's
fidelity a driver-side concern instead of an on-device one.

Once firmware has stored and rendered this image, the key's state — for
the purpose of [Emoji IDs](#emoji-ids) — becomes the reserved sentinel
`0xFE`, "this key's last custom image," not the numeric Emoji ID of
whatever built-in glyph the key showed before. A later ordinary Key state
message naming a built-in Emoji ID switches the key back to the glyph
table, replacing the custom image.

## Emoji IDs

Reserved values for the Key state message's Emoji ID field. Every other
value is unreserved, for a later task's emoji set.

| ID | Glyph |
|---|---|
| `0x00` | Placeholder — a filled box, drawn for any ID this table does not reserve |
| `0xF1` | Digit 1 |
| `0xF2` | Digit 2 |
| `0xF3` | Digit 3 |
| `0xF4` | Digit 4 |
| `0xF5` | Digit 5 |
| `0xF6` | Digit 6 |
| `0xFE` | This key's last custom image — set internally once a Set custom glyph message is applied, never sent by the driver itself |

`firmware/glyphs.py` maps each built-in ID here to a bitmap; see
[`firmware/README.md`](../firmware/README.md) for how to add one. `0xFE`
maps to a stored pixel buffer instead — see [Set custom
glyph](#set-custom-glyph-cdc-host--device).

### Press/release event (CDC, device → host)

Sent whenever the firmware's debounced input detects a raw press or
release. The firmware does not classify single, double, or long presses —
the driver resolves that from a sequence of these events.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Key index | 0-based index of the key that changed |
| 1 | 1 | Event type | `0` = press, `1` = release |
| 2 | 8 | Timestamp | Monotonic time of the event, in microseconds, little-endian `uint64` |

### Audio chunk (CDC, device → host)

Sent while the firmware streams buffered mic audio for a held key. A
recording is one or more chunks; the driver reassembles them in order and
stops at the final-chunk flag. This message does not carry its own length
field — the frame header's `Length` gives the payload size, so the PCM
payload's length `N` is `Length - 2`.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Stream ID | Identifies which recording this chunk belongs to |
| 1 | N | PCM payload | Raw audio samples for this chunk |
| 1 + N | 1 | Final-chunk flag | `0` = more chunks follow, `1` = last chunk in the recording |

## Ping

`make ping-pong` uses a Ping and a Pong to prove the HID and CDC channels
carry data, with no key, display, or protocol change of its own. See
[`firmware/README.md`](../firmware/README.md) and
[`driver/README.md`](../driver/README.md) for the command.

A ping is an ordinary Key state message with Key index `255`, a value no
real key ever uses — the six keys are indexed `0` through `5`. The caller
picks a nonce and writes it into the Emoji ID byte; Color and the Blink
flag are ignored. The Version byte still must match
[Versioning](#versioning): a ping built against a version the firmware
does not recognize is dropped, exactly like any other Key state message.

### Pong (CDC, device → host)

Sent once, in answer to a ping, on the same CDC data channel as every
other device→host message, framed per [Framing](#framing) as type `3`.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Nonce | The Emoji ID byte the ping carried, echoed back unchanged |

A Pong whose nonce does not match the one just sent belongs to another
exchange, not this one, and is not treated as a match.

## Trace record

Sent by `firmware/trace.py`'s `Tracer` when tracing is enabled — off by
default, and never sent otherwise. Each record marks one point in
`MacroPad.step`, so a person debugging the board can see what the
firmware saw at a point that otherwise never crosses the wire, such as a
rejected bounce. `driver/recorder` writes each one, plus its host
arrival time, to a JSONL flight-recorder file. See
[task 0025](../tasks/ongoing/0025-trace-ring-buffer-flight-recorder.md)
for the design decision.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Code | Which point in the loop this record marks — see the trace code registry below |
| 1 | 1 | Key | 0-based key index the record concerns, or `0` when the code carries no key |
| 2 | 2 | Payload | Meaning depends on Code — see below, little-endian `uint16` |
| 4 | 8 | Timestamp | Monotonic device time the record was taken, in microseconds, little-endian `uint64` |

### Trace code registry

| Code | Name | Payload |
|---|---|---|
| 0 | `TRACE_DROPPED` | Number of records overwritten in the ring buffer since the last drain, before this one |
| 1 | `HOST_MESSAGE_DECODED` | The decoded Key state message's Emoji ID |
| 2 | `SWITCH_READ` | `1` if the pin read pressed, `0` if released |
| 3 | `DEBOUNCE_VERDICT` | `0` = accepted press, `1` = accepted release, `0xFF` = rejected as a bounce |
| 4 | `EVENT_WRITTEN` | The Press/release event's Event type byte: `0` = press, `1` = release |

`TRACE_DROPPED` is emitted by `drain`, not recorded during the loop, so
its Timestamp is always `0` — it marks drops counted since the last
drain, not one point in time — and it always precedes the records it
was counted alongside. `SWITCH_READ` and
`DEBOUNCE_VERDICT` are recorded together, only when a switch's raw pin
reading changes from the previous `step` — not on every `step` for every
switch — so a rejected bounce leaves the same pair of records a press
does, distinguished by `DEBOUNCE_VERDICT`'s payload.

## Versioning

The key state message's version byte identifies the wire format both sides
build against. It exists because the format is a fixed-width struct with no
self-describing fields — a field addition, removal, resize, or reorder
changes byte offsets for every field after it, and firmware built against
one layout cannot safely parse a message built against another.

A breaking change to any message in this document raises the version byte.
Firmware and driver must update together: a driver sending a version the
firmware does not recognize, or a firmware built against a version the
driver does not expect, must not guess at field offsets.

This document defines version `1`. No message in this version has shipped
against real hardware yet, so version `1` may still change until tasks
0006, 0007, and 0008 build against it.
