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
| Press/release event | Device → Host | CDC serial | 10 bytes |
| Audio chunk | Device → Host | CDC serial | 2 + N bytes |
| Pong | Device → Host | CDC serial | 1 byte |

Sizes for the three device→host messages are the payload only. Every
device→host message is wrapped in the 3-byte frame header described in
[Framing](#framing).

## Framing

Both device→host messages share one CDC data channel. Neither carries a
field that says which message follows it — byte 0 is a key index in one
and a stream ID in the other — so a reader cannot tell them apart on its
own. Every device→host message is prefixed with a 3-byte frame header:

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Type | Identifies the message that follows — see the type registry below |
| 1 | 2 | Length | Length of the payload that follows, little-endian `uint16` |

The reader reads the header, reads exactly `Length` payload bytes, then
decodes them if it knows `Type`. A type it does not know is skipped by
`Length`, not guessed at, so firmware can add a message type that an older
driver ignores. A stream that ends before `Length` payload bytes arrive is
a truncated message, not a partial value.

### Type registry

| Type | Message |
|---|---|
| 1 | Press/release event |
| 2 | Audio chunk |
| 3 | Pong |

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

`firmware/glyphs.py` maps each ID here to a bitmap; see
[`firmware/README.md`](../firmware/README.md) for how to add one.

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
