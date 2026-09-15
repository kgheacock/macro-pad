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
| 1 | 32,768 | Pixels | 128×128 image, row-major, RGBA4444, 2 bytes per pixel, little-endian |

32,768 bytes is 128 × 128 pixels × 2 bytes per pixel — the whole image in
one frame, under the frame header's `uint16` `Length` field's limit, so
this message needs no multi-frame reassembly on either side. The driver
decodes the source PNG and converts its colors to RGBA4444 itself;
firmware never parses an image format, it only copies length-prefixed
bytes into a bitmap and a file. See
[task 0030](../tasks/ongoing/0030-custom-glyph-upload-and-persistence.md)
for the design decision, including why this task keeps the source PNG's
fidelity a driver-side concern instead of an on-device one.

Each pixel packs a 4-bit alpha nibble (bits 15-12), then 4 bits each of
red, green, and blue (bits 11-8, 7-4, 3-0). Alpha is one bit wide in
practice, not four: the driver collapses every source pixel to either
fully opaque (nibble `0xF`) or fully transparent (nibble `0x0`) before it
reaches the wire — there is no blended transparency. A driver-rendered
emoji ([task 0034](../tasks/complete/0034-emoji-character-to-custom-glyph-image.md))
keeps its glyph's real transparent pixels; a plain photo upload (task
0030's Approach A path) has no transparent pixels at all, since a source
image with no alpha channel decodes as fully opaque everywhere. See
[task 0041](../tasks/ongoing/0041-color-and-blink-behind-custom-glyph.md)
for the design decision, including why this task chose a wire
pixel-format change over baking the key's color into the image at render
time.

A transparent pixel shows the key's own Color underneath it, set by the
same Key state message that names this image's key — see [Key
state](#key-state-hid-host--device) above — instead of a fixed color
baked into the image. While such a key blinks, its opaque pixels stay on
screen every frame; only the color behind its transparent pixels
alternates between Color and black. An image with no transparent pixel
blinks the way every custom glyph did before task 0041: the whole image
alternately shows and hides.

Once firmware has stored and rendered this image, the key's state — for
the purpose of [Emoji IDs](#emoji-ids) — becomes the reserved sentinel
`0xFE`, "this key's last custom image," not the numeric Emoji ID of
whatever built-in glyph the key showed before. A later ordinary Key state
message naming a built-in Emoji ID switches the key back to the glyph
table, replacing the custom image. A later Key state message that names
`0xFE` itself instead keeps the custom image in place, and applies only
that message's Color and Blink fields — see [Emoji IDs](#emoji-ids).

## Emoji IDs

Reserved values for the Key state message's Emoji ID field. Every other
value is unreserved, for a later task's emoji set.

| ID | Glyph |
|---|---|
| `0x00` | Blank — a plain background-colored tile, drawn for any ID this table does not reserve |
| `0xFE` | This key's last custom image — set internally once a Set custom glyph message is applied. A Key state message may also name it, to toggle Color or Blink on the image already in place, without resending it. What Blink does to the image depends on whether it has a transparent pixel — see [Set custom glyph](#set-custom-glyph-cdc-host--device) |

`firmware/glyphs.py` draws a plain background-colored tile for any Emoji
ID a Key state message carries, `0x00` included — it holds no glyph
bitmap of its own. A key first reaches `0xFE` by way of a [Set custom
glyph](#set-custom-glyph-cdc-host--device) message, which bypasses
`firmware/glyphs.py` entirely. A driver may then send an ordinary Key
state message naming `0xFE` to toggle that key's Blink or Color while
keeping the image; firmware keeps `pixels` set instead of clearing it, as
it would for a built-in Emoji ID. Sending `0xFE` for a key with no stored
image is defined, not an error: with no pixels to show, the key falls
through to `firmware/glyphs.py` like any other unreserved ID, and renders
blank. See [`firmware/README.md`](../firmware/README.md#glyphs) for how a
glyph reaches a key.

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
