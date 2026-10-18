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
| Set custom glyph (compressed) | Host → Device | CDC serial | 1 + up to 32,767 bytes |
| Begin batch | Host → Device | CDC serial | 2 bytes |
| Store frame | Host → Device | CDC serial | 3 + up to 32,766 bytes |
| Show frames | Host → Device | CDC serial | 1 to 32 bytes |
| Show miss | Device → Host | CDC serial | 1 byte |
| Batch incomplete | Device → Host | CDC serial | 2 bytes |
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
| 6 | Set custom glyph (compressed) | Host → Device |
| 7 | Begin batch | Host → Device |
| 8 | Store frame | Host → Device |
| 9 | Show frames | Host → Device |
| 10 | Show miss | Device → Host |
| 11 | Batch incomplete | Device → Host |

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
| 1 | 32,768 | Pixels | 128×128 image, row-major, RGB565, 2 bytes per pixel, big-endian; `0x0000` is a transparent pixel |

32,768 bytes is 128 × 128 pixels × 2 bytes per pixel — the whole image in
one frame, under the frame header's `uint16` `Length` field's limit, so
this message needs no multi-frame reassembly on either side. The driver
decodes the source PNG and converts its colors to RGB565 itself;
firmware never parses an image format or converts a pixel, it only
copies the bytes into a bitmap. See
[task 0030](../tasks/ongoing/0030-custom-glyph-upload-and-persistence.md)
for the design decision, including why this task keeps the source PNG's
fidelity a driver-side concern instead of an on-device one.

Each pixel packs 5 bits of red (bits 15-11), 6 of green (bits 10-5), and
5 of blue (bits 4-0), and the two bytes of a pixel go out high byte first.
That is the byte order the panel reads, so firmware writes the buffer to
the panel with no byte swap. Task 0043 changed this format from
little-endian RGBA4444. Transparency is one bit wide: the driver collapses
every source pixel to either opaque or transparent before it reaches the
wire — there is no blended transparency. The value `0x0000` marks a
transparent pixel. An opaque pixel whose own RGB565 value is `0x0000`
(pure black) is sent as `0x0001` instead, a black the panel cannot tell
apart. A driver-rendered emoji
([task 0034](../tasks/complete/0034-emoji-character-to-custom-glyph-image.md))
keeps its glyph's real transparent pixels; a plain photo upload (task
0030's Approach A path) has no transparent pixels at all, since a source
image with no alpha channel decodes as fully opaque everywhere. See
[task 0041](../tasks/complete/0041-color-and-blink-behind-custom-glyph.md)
for the design decision, including why this task chose a wire
pixel-format change over baking the key's color into the image at render
time, and [task 0043](../tasks/ongoing/0043-raw-spi-panels-init-once.md)
for why the pixel format changed again.

A firmware build that reads this format cannot read the old one. A
Set custom glyph message from a driver built before task 0043 carries the
right number of bytes, so firmware cannot refuse it, and it draws as
noise. Update the driver and firmware together.

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

### Set custom glyph (compressed) (CDC, host → device)

Carries the same image as Set custom glyph, as a zlib stream. Framed per
[Framing](#framing) as type `6`. The effect on the key is the same as for
type `5`, in every detail above.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Key index | 0-based index of the target key |
| 1 | 1 to 32,767 | Stream | A zlib stream (RFC 1950) that inflates to exactly 32,768 bytes, the Pixels field of Set custom glyph |

The driver sends type `6` when its stream is smaller than the raw payload,
and type `5` otherwise. A flat icon deflates to about 1 KB, so a frame is
about 3% of a type `5` frame. A noisy picture does not shrink, and goes
out as type `5`. The `Length` of a type `6` frame is at least 2 and less
than the `Length` of a type `5` frame, 32,769.

The firmware drops a type `6` frame that does not inflate, or that does
not inflate to exactly 32,768 bytes. The key keeps the image it had. The
frame is consumed by its `Length`, so the next frame starts clean.

Firmware that does not know type `6` skips the frame by its `Length`, as
Framing says, and the key does not change. Update the firmware before the
driver. See [task 0058](../tasks/complete/0058-compress-and-drain-custom-glyph-transfer.md)
for the design decision, and for why inflating on the board costs 32 to
65 ms, which limits what compression saves.

### Begin batch (CDC, host → device)

Sent before the glyphs and key states of a layer change, so the board draws
all the changed keys together and not one after another. Framed per
[Framing](#framing) as type `7`. A key that changes without a Begin batch
draws as soon as its data arrives.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Glyph mask | Bit `k` is `1` when key `k` gets a Set custom glyph (type `5` or `6`) in this batch |
| 1 | 1 | State mask | Bit `k` is `1` when key `k` gets a Key state message in this batch |

The six keys use bits 0 to 5. The board ignores bits for keys it does not
have.

The board holds the draw of every key that a mask names. It still reads and
composes the new image as the data arrives, but it queues no push for the
key. Keys outside both masks draw at once, as before. The board releases the
batch when every named glyph has been decoded and every named Key state has
been applied. It then queues the frames of all the named keys in one pass, so
the pushes start together: one push group holds at most three panels, so six
keys go out as two groups, about 30 ms apart. A key that blinks draws the frame
of the blink slot that is current at the release.

The two channels have no common order. A Key state message can reach the
board before or after the glyphs that follow the Begin batch, and the masks
make that order unimportant.

While a batch is open and no new glyph or Key state has arrived for 500 ms,
the board asks the host for what it lacks with a [Batch incomplete](#batch-incomplete-cdc-device--host)
frame, at most three times. The board releases a batch that is not complete 2 s
after the Begin batch arrived. It draws the keys it has, and records `BATCH_TIMED_OUT` (see the
[trace code registry](#trace-code-registry)). A new Begin batch that arrives
while one is open releases the open one first. A Begin batch with a payload
that is not 2 bytes is dropped, and the frame is consumed by its `Length`.

The driver sends a Begin batch when a layer change changes two or more keys,
and sends nothing extra for one key. Firmware that does not know type `7`
skips the frame by its `Length`, as Framing says, and the layer draws key by
key. Update the firmware before the driver. See [task
0060](../tasks/complete/0060-draw-a-layer-update-all-at-once.md) for the
design decision.

### Batch incomplete (CDC, device → host)

Sent by a board with an open batch that lacks data. Framed per
[Framing](#framing) as type `11`.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Glyph mask | Bit `k` is `1` when key `k` still waits for its Set custom glyph |
| 1 | 1 | State mask | Bit `k` is `1` when key `k` still waits for its Key state |

The board holds one HID report. A loop pass longer than the gap between two
reports lets the next report overwrite an unread one, and that Key state is
lost (task 0044). A garbage collection of 115 to 150 ms with 128 slots in use
did this in 2 of 9 batch runs. The board sends the frame after 500 ms with no
new data for the batch, and again every 500 ms, at most three times in all. It
records `BATCH_INCOMPLETE` (see the [trace code registry](#trace-code-registry))
each time.

The host sends again the Set custom glyph and the Key state that it holds for
each key the frame names, a glyph first. It sends nothing for a key it never
sent, and a key that the frame does not name is left alone. The 2 s time-out
stays as the last resort. A resend for a plain Key state outside a batch does
not exist: the board cannot tell that it lost one. Firmware that does not
send the frame leaves the host as it was. See [task
0063](../tasks/complete/0063-resend-lost-batch-state-and-cap-the-pool-at-64.md)
for the design decision.

### Store frame (CDC, host → device)

Composes one picture over one color into a slot of the board's frame cache. It
draws nothing. Framed per [Framing](#framing) as type `8`.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Slot | `0` to `63` |
| 1 | 2 | Color | Background color, RGB565, little-endian |
| 3 | 0 to 32,766 | Stream | A zlib stream (RFC 1950) that inflates to exactly 32,768 bytes, the Pixels field of Set custom glyph. An empty stream is a frame of the color alone, with no glyph |

The board keeps a fixed pool of 64 slots in RAM, one frame in each: the
picture, with a transparent pixel (`0x0000`) showing the color, as a Set custom
glyph and a Key state would draw the key. A Store frame replaces the frame in
its slot. The board allocates the pool when it starts, before its loop, and
never frees, grows, or shrinks it, and never writes it to flash. A board that
resets has an empty cache.

The host owns the slot numbers. It picks a slot for each frame, and when all
64 are in use it overwrites the least recently used one. The board is only an
array. A Store frame for a slot above `63`, or with a stream that does not
inflate to exactly 32,768 bytes, is dropped, and the frame is consumed by its
`Length`: the slot keeps what it held.

The `Length` of a Store frame is at least 3 and at most 32,769. A glyph that
does not compress to fit is not a Store frame: the host draws that key with
Set custom glyph and a Key state. A key that blinks is not cached either: it
keeps the paths of Set custom glyph and Key state (tasks 0044 and 0048).

The board must not change a slot while a push that reads it is queued or on the
wire, so a Store frame for such a slot waits for that push (at most about 55 ms).
The host never stores into a slot that a key shows, so this wait is rare. A
Store frame takes about 140 ms of the board's time, the same as a Set custom
glyph, because the board inflates the stream and composes the frame. Firmware
that does not know type `8` skips the frame by its `Length`, as Framing says.
Update the firmware before the driver.

### Show frames (CDC, host → device)

Names a slot for each key. The board draws the named keys together. Framed per
[Framing](#framing) as type `9`.

| Offset | Size | Field | Description |
|---|---|---|---|
| `k` | 1 | Slot of key `k` | `0` to `63`, or `255` for "unchanged" |

The payload has one byte for each key, key `0` first, and at least one byte. The
board ignores the bytes of keys it does not have. A key whose byte is `255`
keeps what it shows. For every other key, the board queues the push of the frame
in the slot in one pass, so the pushes start together: one push group holds at
most three panels, so six keys go out as two groups, about 30 ms apart. A Show
frames needs no Begin batch, because one message is already atomic. It releases
an open batch first.

After a Show frames, the key's state is what a Set custom glyph and a Key state
would have left: the frame's color, `0xFE` as its Emoji ID when the frame has a
glyph, and no blink. A later Key state that names the same color keeps the
frame. A later Key state with another color, or a built-in Emoji ID, builds a
plain color frame.

A key that names an empty slot, or a slot above `63`, keeps the image it had.
The board draws the other keys, and answers with a Show miss. The board records
`FRAMES_SHOWN` (see the [trace code registry](#trace-code-registry)) for the
keys it drew.

A Show frames with no byte is dropped, and the frame is consumed by its
`Length`. Firmware that does not know type `9` skips the frame, and no key
changes. See [task 0061](../tasks/ongoing/0061-cache-composed-glyph-frames-on-the-board.md)
for the design decision.

### Show miss (CDC, device → host)

Sent when a Show frames named at least one empty slot. Framed per
[Framing](#framing) as type `10`.

| Offset | Size | Field | Description |
|---|---|---|---|
| 0 | 1 | Mask | Bit `k` is `1` when key `k` named an empty slot and kept its image |

The host stores the frames of those keys again, and sends a Show frames for
them. This repairs a table that the host and the board disagree on, for example
after a code reload that kept USB up. After a reconnect the host does not wait
for a miss: it clears its record of what the board holds and stores every frame
again.

## Emoji IDs

Reserved values for the Key state message's Emoji ID field. Every other
value is unreserved, for a later task's emoji set.

| ID | Glyph |
|---|---|
| `0x00` | Blank — a plain background-colored tile, drawn for any ID this table does not reserve |
| `0xFE` | This key's last custom image — set internally once a Set custom glyph message is applied. A Key state message may also name it, to toggle Color or Blink on the image already in place, without resending it. What Blink does to the image depends on whether it has a transparent pixel — see [Set custom glyph](#set-custom-glyph-cdc-host--device) |

Firmware has no glyph table (task 0039), so a Key state message draws no
glyph for any Emoji ID it carries, `0x00` included: the key shows its
Color. A key first reaches `0xFE` by way of a [Set custom
glyph](#set-custom-glyph-cdc-host--device) message. A driver may then send
an ordinary Key state message naming `0xFE` to toggle that key's Blink or
Color while keeping the image; firmware keeps `pixels` set instead of
clearing it, as it would for a built-in Emoji ID. Sending `0xFE` for a key
with no stored image is defined, not an error: with no pixels to show, the
key shows only its Color. See
[`firmware/README.md`](../firmware/README.md#cached-frames) for how
firmware draws a key.

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
| 5 | `CUSTOM_GLYPH_DECODED` | `0`, unused — the Key field already names the target key |
| 6 | `PERSIST_DONE` | `0`, unused. No longer emitted since task 0044: the board keeps no state. |
| 7 | `GLYPH_BUILT` | `0`, unused |
| 8 | `REFRESH_DONE` | `0`, unused. Since task 0045, the end of a push to the panel |
| 9 | `PUSH_STARTED` | `0`, unused. The start of a push to the panel, task 0045 |
| 10 | `BATCH_TIMED_OUT` | The pending masks of a batch that the board released after 2 s, task 0060: the glyph mask in the low byte, the Key state mask in the high byte. The Key field is `0xFF` |
| 11 | `FRAMES_SHOWN` | The keys a Show frames drew, task 0061: bit `k` for key `k`. The Key field is `0xFF`. The board records it as it queues the pushes, so the `PUSH_STARTED` records of the keys follow it |
| 12 | `BATCH_INCOMPLETE` | The pending masks of a batch that asked the host for more, task 0063: the glyph mask in the low byte, the Key state mask in the high byte. The Key field is `0xFF` |

`TRACE_DROPPED` is emitted by `drain`, not recorded during the loop, so
its Timestamp is always `0` — it marks drops counted since the last
drain, not one point in time — and it always precedes the records it
was counted alongside. `SWITCH_READ` and
`DEBOUNCE_VERDICT` are recorded together, only when a switch's raw pin
reading changes from the previous `step` — not on every `step` for every
switch — so a rejected bounce leaves the same pair of records a press
does, distinguished by `DEBOUNCE_VERDICT`'s payload.

`CUSTOM_GLYPH_DECODED`, `GLYPH_BUILT`, and `REFRESH_DONE` mark the
custom-glyph paint pipeline's stages for one Set custom glyph message: CDC
transfer and decode end at `CUSTOM_GLYPH_DECODED`; composing the key's
cached frames from the raw pixel buffer ends at `GLYPH_BUILT`; the SPI
push to the panel starts at `PUSH_STARTED` (task 0045); and it ends at
`REFRESH_DONE`. A push goes out by DMA, so the two records are one DMA transfer
apart, about 22 ms at 15 MHz, and the board does other work between them. Only
one push is on the wire at a time, so a key's record can come after the
records of other keys' pushes that were queued before it. Until task 0044,
persisting the new state to flash ended at `PERSIST_DONE`, after the paint.

A Key state message records `HOST_MESSAGE_DECODED`, then the last
two of those codes. Each Timestamp is its own point-in-time reading, not the
`step` iteration's shared `now_us`, so the gap between two consecutive
records is that stage's duration — see [task
0042](../tasks/ongoing/0042-instrument-custom-glyph-paint-latency.md) for
the design decision.

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
