"""Encodes and decodes the messages in docs/wire-protocol.md.

This is the firmware half of the contract that `driver/transport/wire.go`
implements on the host side. Both files build against the same document,
so a change to one message's layout changes both, and both refuse to
guess at field offsets when the version byte does not match.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

PROTOCOL_VERSION = 1

KEY_STATE_SIZE = 6  # docs/wire-protocol.md's Key state message
EVENT_SIZE = 10  # docs/wire-protocol.md's Press/release event
FRAME_HEADER_SIZE = 3  # docs/wire-protocol.md's Framing: type + length

PRESS = 0
RELEASE = 1

MESSAGE_TYPE_EVENT = 1  # docs/wire-protocol.md's Framing type registry
MESSAGE_TYPE_PONG = 3  # docs/wire-protocol.md's Framing type registry

PING_KEY_INDEX = 255  # docs/wire-protocol.md's Ping section


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
