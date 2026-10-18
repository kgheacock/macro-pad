"""Encodes and decodes the messages in docs/wire-protocol.md.

This is the firmware half of the contract that `driver/transport/wire.go`
implements on the host side. Both files build against the same document,
so a change to one message's layout changes both, and both refuse to
guess at field offsets when the version byte does not match.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import time
import zlib

# The module of the second core (task 0059), in the custom CircuitPython build.
# Stock CircuitPython has none, and the firmware then inflates on core 0.
try:
    import glyphcore
except ImportError:
    glyphcore = None

PROTOCOL_VERSION = 1

KEY_STATE_SIZE = 6  # docs/wire-protocol.md's Key state message
EVENT_SIZE = 10  # docs/wire-protocol.md's Press/release event
FRAME_HEADER_SIZE = 3  # docs/wire-protocol.md's Framing: type + length

PRESS = 0
RELEASE = 1

MESSAGE_TYPE_EVENT = 1  # docs/wire-protocol.md's Framing type registry
MESSAGE_TYPE_PONG = 3  # docs/wire-protocol.md's Framing type registry
MESSAGE_TYPE_TRACE = 4  # docs/wire-protocol.md's Framing type registry
MESSAGE_TYPE_SET_CUSTOM_GLYPH = 5  # docs/wire-protocol.md's Framing type registry
# Same image as type 5, as a zlib stream (task 0058).
MESSAGE_TYPE_SET_CUSTOM_GLYPH_COMPRESSED = 6
# Begin batch: names the keys whose update the board draws together (task 0060).
MESSAGE_TYPE_BEGIN_BATCH = 7
BEGIN_BATCH_PAYLOAD_SIZE = 2  # glyph mask + key state mask
# Store frame: composes a glyph over a color into a slot of the frame cache
# (task 0061). Payload: slot (1), color (2, little-endian), zlib stream (0 or
# more bytes; none means a frame of the color alone).
MESSAGE_TYPE_STORE_FRAME = 8
STORE_FRAME_HEADER_SIZE = 3
# Show frames: for each key a slot number, or SLOT_UNCHANGED (task 0061).
MESSAGE_TYPE_SHOW_FRAMES = 9
SHOW_FRAMES_MAX_KEYS = 32
SLOT_UNCHANGED = 0xFF
# Show miss, device to host: the mask of the keys that named an empty slot.
MESSAGE_TYPE_SHOW_MISS = 10
# Batch incomplete, device to host: the masks of the glyphs and the key states
# that an open batch still waits for (task 0063).
MESSAGE_TYPE_BATCH_INCOMPLETE = 11

# How long one `CustomGlyphReader.feed` call can keep reading while a frame is
# in progress. The loop does not scan the switches meanwhile. 8 ms drained a
# 32 KB frame in about 125 ms on the board (task 0058).
READ_BUDGET_NS = 8000000

# Core 1 inflates into one of GLYPH_BUFFERS buffers of the glyphcore module:
# two for each of the first GLYPH_KEYS keys, so a new glyph never overwrites the
# pixels that the key still draws from, and one to inflate a Store frame into.
GLYPH_KEYS = 7
GLYPH_BUFFERS = 2 * GLYPH_KEYS + 1
_STAGING_BUFFER = 2 * GLYPH_KEYS
# A glyph that core 1 has not finished after this long is dropped, and the key
# keeps its image. A glyph takes under 100 ms on core 1.
INFLATE_TIMEOUT_NS = 500_000_000

PING_KEY_INDEX = 255  # docs/wire-protocol.md's Ping section

CUSTOM_GLYPH_WIDTH = 128
CUSTOM_GLYPH_HEIGHT = 128
# docs/wire-protocol.md's "Set custom glyph": 128x128 pixels, RGB565
# big-endian, 2 bytes each. The value 0x0000 marks a transparent pixel.
CUSTOM_GLYPH_PIXELS_SIZE = CUSTOM_GLYPH_WIDTH * CUSTOM_GLYPH_HEIGHT * 2
CUSTOM_GLYPH_PAYLOAD_SIZE = 1 + CUSTOM_GLYPH_PIXELS_SIZE  # key index + pixels

# docs/wire-protocol.md's "Emoji IDs": a key showing its last custom
# image, not a built-in glyph table entry.
CUSTOM_GLYPH_SENTINEL_EMOJI_ID = 0xFE


class UnsupportedVersionError(ValueError):
    """A key state message carried a version byte this build cannot read.

    Mirrors `transport.ErrUnsupportedVersion` on the driver side. It
    subclasses `ValueError` so one `except ValueError` covers both this
    and a short or malformed message.
    """


class KeyState:
    """One decoded key state message from the host.

    `emoji_id` is the wire's 1-byte index into the firmware's glyph
    table, not a glyph itself — task 0023 supplies the lookup that turns
    it into a bitmap.
    """

    def __init__(self, key_index, version, color, emoji_id, blink):
        self.key_index = key_index
        self.version = version
        self.color = color
        self.emoji_id = emoji_id
        self.blink = blink


def decode_key_state(data):
    """Decode a 6-byte key state message sent by the host over HID.

    Raises `UnsupportedVersionError` when the version byte is not
    `PROTOCOL_VERSION`, and `ValueError` when the message is short. The
    version check comes before any field read after it, because a format
    change moves the offsets of `color`, `emoji_id`, and `blink`.
    """
    if len(data) != KEY_STATE_SIZE:
        raise ValueError(
            "key state message is {} bytes, want {}".format(
                len(data), KEY_STATE_SIZE
            )
        )

    version = data[1]
    if version != PROTOCOL_VERSION:
        raise UnsupportedVersionError(
            "got version {}, want {}".format(version, PROTOCOL_VERSION)
        )

    return KeyState(
        key_index=data[0],
        version=version,
        color=data[2] | (data[3] << 8),
        emoji_id=data[4],
        blink=data[5] != 0,
    )


def encode_event(key_index, event_type, timestamp_us):
    """Build the 10-byte press or release event the host reads from CDC.

    `event_type` is `PRESS` or `RELEASE`. `timestamp_us` is monotonic
    microseconds, written little-endian to match the RP2350's byte order.
    """
    if event_type not in (PRESS, RELEASE):
        raise ValueError("event type {} is not PRESS or RELEASE".format(event_type))

    buffer = bytearray(EVENT_SIZE)
    buffer[0] = key_index
    buffer[1] = event_type
    buffer[2:EVENT_SIZE] = timestamp_us.to_bytes(8, "little")
    return bytes(buffer)


def write_frame(writer, message_type, payload):
    """Write one device→host frame: a type byte, a little-endian uint16
    payload length, then the payload itself, to `writer`.

    Mirrors `driver/transport/wire.go`'s `writeFrame`, so `Device.ReadMessage`
    on the host side can decode what this writes. See "Framing" in
    docs/wire-protocol.md.
    """
    header = bytes((message_type, len(payload) & 0xFF, (len(payload) >> 8) & 0xFF))
    writer.write(header)
    writer.write(payload)


def encode_pong(nonce):
    """Build a Pong reply's payload: `nonce`, unchanged.

    Pass this to `write_frame` with `MESSAGE_TYPE_PONG` to write the
    framed reply a caller reads back with `driver/transport`'s Pong
    decoder.
    """
    return bytes((nonce,))


class CustomGlyph:
    """One decoded Set custom glyph message: a key index and its raw
    128x128 big-endian RGB565 pixel buffer. See "Set custom glyph" in
    docs/wire-protocol.md.
    """

    def __init__(self, key_index, pixels):
        self.key_index = key_index
        self.pixels = pixels


def decode_custom_glyph(payload):
    """Decode a Set custom glyph message's frame payload.

    Raises `ValueError` when `payload` is not exactly
    `CUSTOM_GLYPH_PAYLOAD_SIZE` bytes.
    """
    if len(payload) != CUSTOM_GLYPH_PAYLOAD_SIZE:
        raise ValueError(
            "custom glyph payload is {} bytes, want {}".format(
                len(payload), CUSTOM_GLYPH_PAYLOAD_SIZE
            )
        )
    return CustomGlyph(key_index=payload[0], pixels=bytes(payload[1:]))


class BeginBatch:
    """One decoded Begin batch message. Bit `k` of `glyph_mask` says key `k`
    gets a Set custom glyph, and bit `k` of `state_mask` says it gets a key
    state. See "Begin batch" in docs/wire-protocol.md.
    """

    def __init__(self, glyph_mask, state_mask):
        self.glyph_mask = glyph_mask
        self.state_mask = state_mask


class StoreFrame:
    """One decoded Store frame message: the slot, the color the frame is
    composed over, and the glyph's pixels, or `None` for a frame of the color
    alone. See "Store frame" in docs/wire-protocol.md.
    """

    def __init__(self, slot, color, pixels):
        self.slot = slot
        self.color = color
        self.pixels = pixels


class ShowFrames:
    """One decoded Show frames message: `slots[k]` is the slot key `k` shows,
    or `SLOT_UNCHANGED`. See "Show frames" in docs/wire-protocol.md.
    """

    def __init__(self, slots):
        self.slots = slots


def encode_show_miss(mask):
    """Build a Show miss payload: the mask of the keys that named an empty
    slot, bit `k` for key `k`. Pass it to `write_frame` with
    `MESSAGE_TYPE_SHOW_MISS`.
    """
    return bytes((mask & 0xFF,))


def encode_batch_incomplete(glyph_mask, state_mask):
    """Build a Batch incomplete payload: the glyph mask, then the key state
    mask, bit `k` for key `k`. Pass it to `write_frame` with
    `MESSAGE_TYPE_BATCH_INCOMPLETE`.
    """
    return bytes((glyph_mask & 0xFF, state_mask & 0xFF))


def _inflate_glyph(stream):
    """Inflate a type 6 zlib stream to a glyph's pixel buffer.

    Raises `ValueError` for a stream that does not inflate, or does not
    inflate to exactly `CUSTOM_GLYPH_PIXELS_SIZE` bytes. CircuitPython's
    `zlib` raises a different exception for each kind of bad stream, so any
    failure of `decompress` counts as one.
    """
    try:
        pixels = zlib.decompress(stream)
    except Exception:
        raise ValueError("compressed custom glyph does not inflate")
    if len(pixels) != CUSTOM_GLYPH_PIXELS_SIZE:
        raise ValueError(
            "compressed custom glyph inflated to {} bytes, want {}".format(
                len(pixels), CUSTOM_GLYPH_PIXELS_SIZE
            )
        )
    return pixels


_UNSET = object()
_GLYPH = 0  # what a pending inflate becomes: a CustomGlyph...
_STORE = 1  # ...or a StoreFrame


class CustomGlyphReader:
    """Incrementally reads one framed CDC message from the host.

    A Set custom glyph payload is up to 32,769 bytes. `feed` reads it with
    `readinto` straight into one preallocated buffer, and reads exactly the
    bytes the current frame needs, so the next frame stays in the port.

    While a frame is in progress, one `feed` call keeps reading until the
    frame is complete or `READ_BUDGET_NS` has passed. This drains the port
    at its own pace, 550 KB/s on the board, instead of one 256-byte read
    per `MacroPad.step` pass, 140 KB/s (task 0058). The budget bounds how
    long one call can hold up the switch scan. An idle `feed`, with nothing
    in progress and nothing waiting, returns at once.

    With a `core1` (the `glyphcore` module, or a stand-in), a type 6 or type 8
    stream goes to the second core, and `feed` goes on to read the next frame
    while core 1 inflates (task 0059). A later `feed` returns the message once
    core 1 is done. Messages come out in the order they arrived: a frame that
    arrives behind a stream in progress waits in the buffer until that stream
    is done. A stream that core 1 does not finish within `timeout_ns` is
    dropped. Without a `core1`, or when core 1 is busy, `feed` inflates on
    core 0, as before.
    """

    def __init__(
        self,
        clock=None,
        budget_ns=READ_BUDGET_NS,
        core1=_UNSET,
        timeout_ns=None,
    ):
        self._frame = bytearray(FRAME_HEADER_SIZE + CUSTOM_GLYPH_PAYLOAD_SIZE)
        self._view = memoryview(self._frame)
        self._filled = 0
        # Header and payload bytes of the glyph frame in progress, and its type.
        self._total = 0
        self._frame_type = 0
        # Payload bytes of a frame this reader drops, still to discard.
        self._skip = 0
        self._bad_length = False
        self._clock = clock if clock is not None else time.monotonic_ns
        self._budget_ns = budget_ns
        self._core1 = glyphcore if core1 is _UNSET else core1
        self._timeout_ns = INFLATE_TIMEOUT_NS if timeout_ns is None else timeout_ns
        # The inflate on core 1 in progress: (kind, info, buffer, started).
        self._pending = None
        # True while a complete frame waits in `_frame` behind a pending one.
        self._stalled = False
        # Which of a key's two glyph buffers holds its current glyph.
        self._current = [1] * GLYPH_KEYS
        # The number of glyphs dropped because core 1 was too slow.
        self.dropped = 0
        if self._core1 is not None:
            # A reload can leave a job from the last run in the module.
            self._core1.cancel()

    def feed(self, reader):
        """Read whatever is available from `reader`.

        Returns a decoded `CustomGlyph` once a full Set custom glyph
        frame has arrived, a `BeginBatch` once a full Begin batch frame
        has arrived, a `StoreFrame` or a `ShowFrames` once a full frame of
        that type has arrived (task 0061), `None` otherwise. A frame of a type this
        reader does not know is dropped, by its declared length, matching
        `driver/transport/wire.go`'s `readFrame`/`decodeMessage` split. A
        glyph frame of the wrong length is dropped the same way, and then
        raises `ValueError`, so the next frame starts clean.
        """
        if self._pending is not None or self._stalled:
            message = self._advance()
            if message is not None or self._stalled:
                return message
        if not reader.in_waiting and not self._filled and not self._skip:
            return None

        start = self._clock()
        while True:
            available = reader.in_waiting
            if available and self._consume(reader, available):
                return self._finish()
            if self._clock() - start >= self._budget_ns:
                return None

    def _consume(self, reader, available):
        """Read up to `available` bytes of the current frame. Returns True
        when the frame is complete or fully dropped."""
        if self._skip:
            n = min(available, self._skip, len(self._frame))
            got = reader.readinto(self._view[:n])
            self._skip -= got or 0
            return self._skip == 0

        if self._filled < FRAME_HEADER_SIZE:
            n = min(available, FRAME_HEADER_SIZE - self._filled)
            got = reader.readinto(self._view[self._filled : self._filled + n])
            self._filled += got or 0
            if self._filled < FRAME_HEADER_SIZE:
                return False
            length = self._frame[1] | (self._frame[2] << 8)
            frame_type = self._frame[0]
            # A type 6 stream is at least a key index and one byte, and
            # smaller than the raw payload, or the driver would send type 5.
            if (
                frame_type == MESSAGE_TYPE_SET_CUSTOM_GLYPH
                and length == CUSTOM_GLYPH_PAYLOAD_SIZE
            ) or (
                frame_type == MESSAGE_TYPE_SET_CUSTOM_GLYPH_COMPRESSED
                and 2 <= length < CUSTOM_GLYPH_PAYLOAD_SIZE
            ):
                self._frame_type = frame_type
                self._total = FRAME_HEADER_SIZE + length
                return False
            if (
                frame_type == MESSAGE_TYPE_BEGIN_BATCH
                and length == BEGIN_BATCH_PAYLOAD_SIZE
            ):
                self._frame_type = frame_type
                self._total = FRAME_HEADER_SIZE + length
                return False
            if (
                frame_type == MESSAGE_TYPE_STORE_FRAME
                and STORE_FRAME_HEADER_SIZE <= length <= CUSTOM_GLYPH_PAYLOAD_SIZE
            ) or (
                frame_type == MESSAGE_TYPE_SHOW_FRAMES
                and 1 <= length <= SHOW_FRAMES_MAX_KEYS
            ):
                self._frame_type = frame_type
                self._total = FRAME_HEADER_SIZE + length
                return False
            self._bad_length = frame_type in (
                MESSAGE_TYPE_SET_CUSTOM_GLYPH,
                MESSAGE_TYPE_SET_CUSTOM_GLYPH_COMPRESSED,
                MESSAGE_TYPE_BEGIN_BATCH,
                MESSAGE_TYPE_STORE_FRAME,
                MESSAGE_TYPE_SHOW_FRAMES,
            )
            self._bad_length_size = length
            self._filled = 0
            self._skip = length
            return length == 0

        n = min(available, self._total - self._filled)
        got = reader.readinto(self._view[self._filled : self._filled + n])
        self._filled += got or 0
        return self._filled == self._total

    def _finish(self):
        if self._filled:
            if self._pending is not None:
                # Behind a stream that core 1 inflates. Hold it, so that the
                # messages keep their order, and read no more until it is done.
                self._stalled = True
                return None
            return self._dispatch()
        if self._bad_length:
            self._bad_length = False
            raise ValueError(
                "frame payload is {} bytes, which its type does not allow".format(
                    self._bad_length_size
                )
            )
        return None

    def _advance(self):
        """Move the pending inflate and the held frame on. Returns the message
        of a finished inflate, or of the held frame once nothing is pending.
        """
        if self._pending is not None:
            message = self._collect()
            if message is not None or self._pending is not None:
                return message
        if self._stalled:
            self._stalled = False
            return self._dispatch()
        return None

    def _dispatch(self):
        """Turn the complete frame in `_frame` into a message, or start its
        inflate on core 1 and return `None`."""
        total = self._filled
        self._filled = 0
        base = FRAME_HEADER_SIZE
        if self._frame_type == MESSAGE_TYPE_BEGIN_BATCH:
            return BeginBatch(
                glyph_mask=self._frame[base],
                state_mask=self._frame[base + 1],
            )
        if self._frame_type == MESSAGE_TYPE_SHOW_FRAMES:
            return ShowFrames(slots=bytes(self._frame[base:total]))
        if self._frame_type == MESSAGE_TYPE_STORE_FRAME:
            stream = self._view[base + STORE_FRAME_HEADER_SIZE : total]
            slot = self._frame[base]
            color = self._frame[base + 1] | (self._frame[base + 2] << 8)
            if not len(stream):
                return StoreFrame(slot=slot, color=color, pixels=None)
            return self._inflate(_STORE, (slot, color), _STAGING_BUFFER, stream)
        key_index = self._frame[base]
        stream = self._view[base + 1 : total]
        if self._frame_type == MESSAGE_TYPE_SET_CUSTOM_GLYPH:
            return CustomGlyph(key_index=key_index, pixels=bytes(stream))
        buffer = _STAGING_BUFFER
        if key_index < GLYPH_KEYS:
            buffer = 2 * key_index + 1 - self._current[key_index]
        return self._inflate(_GLYPH, key_index, buffer, stream)

    def _inflate(self, kind, info, buffer, stream):
        """Inflate `stream`: on core 1 when it takes the job, else here."""
        if self._core1 is not None:
            try:
                self._core1.submit(stream, buffer)
            except OSError:
                # A flash write is on, or the module holds a result. Do it here.
                pass
            else:
                self._pending = (kind, info, buffer, self._clock())
                return None
        return self._message(kind, info, _inflate_glyph(stream))

    def _collect(self):
        """Poll the pending inflate. Returns its message when done, `None`
        while it runs, and `None` after it dropped a stream that took too long.
        Raises `ValueError` for a stream that does not inflate.
        """
        kind, info, buffer, started = self._pending
        try:
            length = self._core1.poll()
        except ValueError:
            self._pending = None
            raise ValueError("compressed custom glyph does not inflate")
        if length is None:
            if self._clock() - started >= self._timeout_ns:
                self._pending = None
                self._core1.cancel()
                self.dropped += 1
            return None
        self._pending = None
        if kind == _GLYPH and info < GLYPH_KEYS:
            self._current[info] = buffer & 1
        return self._message(kind, info, self._core1.glyph_view(buffer))

    @staticmethod
    def _message(kind, info, pixels):
        if kind == _STORE:
            return StoreFrame(slot=info[0], color=info[1], pixels=pixels)
        return CustomGlyph(key_index=info, pixels=pixels)
