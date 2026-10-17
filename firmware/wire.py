"""Encodes and decodes the messages in docs/wire-protocol.md.

This is the firmware half of the contract that `driver/transport/wire.go`
implements on the host side. Both files build against the same document,
so a change to one message's layout changes both, and both refuse to
guess at field offsets when the version byte does not match.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import time

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

# How long one `CustomGlyphReader.feed` call can keep reading while a frame is
# in progress. The loop does not scan the switches meanwhile. 8 ms drained a
# 32 KB frame in about 125 ms on the board (task 0058).
READ_BUDGET_NS = 8000000

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
    """

    def __init__(self, clock=None, budget_ns=READ_BUDGET_NS):
        self._frame = bytearray(FRAME_HEADER_SIZE + CUSTOM_GLYPH_PAYLOAD_SIZE)
        self._view = memoryview(self._frame)
        self._filled = 0
        # Payload bytes of a frame this reader drops, still to discard.
        self._skip = 0
        self._bad_length = False
        self._clock = clock if clock is not None else time.monotonic_ns
        self._budget_ns = budget_ns

    def feed(self, reader):
        """Read whatever is available from `reader`.

        Returns a decoded `CustomGlyph` once a full Set custom glyph
        frame has arrived, `None` otherwise. A frame of a type this
        reader does not know is dropped, by its declared length, matching
        `driver/transport/wire.go`'s `readFrame`/`decodeMessage` split. A
        glyph frame of the wrong length is dropped the same way, and then
        raises `ValueError`, so the next frame starts clean.
        """
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
            if (
                self._frame[0] == MESSAGE_TYPE_SET_CUSTOM_GLYPH
                and length == CUSTOM_GLYPH_PAYLOAD_SIZE
            ):
                return False
            self._bad_length = self._frame[0] == MESSAGE_TYPE_SET_CUSTOM_GLYPH
            self._bad_length_size = length
            self._filled = 0
            self._skip = length
            return length == 0

        total = FRAME_HEADER_SIZE + CUSTOM_GLYPH_PAYLOAD_SIZE
        n = min(available, total - self._filled)
        got = reader.readinto(self._view[self._filled : self._filled + n])
        self._filled += got or 0
        return self._filled == total

    def _finish(self):
        if self._filled:
            self._filled = 0
            return CustomGlyph(
                key_index=self._frame[FRAME_HEADER_SIZE],
                pixels=bytes(self._view[FRAME_HEADER_SIZE + 1 :]),
            )
        if self._bad_length:
            self._bad_length = False
            raise ValueError(
                "custom glyph payload is {} bytes, want {}".format(
                    self._bad_length_size, CUSTOM_GLYPH_PAYLOAD_SIZE
                )
            )
        return None
