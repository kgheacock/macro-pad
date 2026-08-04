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
| Audio chunk | Device → Host | CDC serial | 4 + N bytes |

### Key state (HID, host → device)

Sent whenever the driver wants a key's emoji, color, or blink state to
change. One message covers one key.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Key index | 0-based index of the target key |
| 1 | 1 | Version | Protocol version this message was built against — see [Versioning](#versioning) |
| 2 | 2 | Color | Background color, RGB565, little-endian |
| 4 | 1 | Emoji ID | Index into the firmware's emoji bitmap table |
| 5 | 1 | Blink flag | `0` = steady, `1` = blink |

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
stops at the final-chunk flag.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Stream ID | Identifies which recording this chunk belongs to |
| 1 | 2 | Chunk length | Length `N` of the PCM payload that follows, little-endian `uint16` |
| 3 | N | PCM payload | Raw audio samples for this chunk |
| 3 + N | 1 | Final-chunk flag | `0` = more chunks follow, `1` = last chunk in the recording |

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
