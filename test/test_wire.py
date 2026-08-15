import pytest

# Imported flat, under the name the board uses. See conftest.py.
import wire


class FakeWriter:
    """Recorder standing in for a CDC serial endpoint."""

    def __init__(self):
        self.written = bytearray()

    def write(self, data):
        self.written.extend(data)
        return len(data)


def _key_state_bytes(
    key_index=0, version=wire.PROTOCOL_VERSION, color=0x0000, emoji_id=0, blink=False
):
    return bytes(
        (
            key_index,
            version,
            color & 0xFF,
            (color >> 8) & 0xFF,
            emoji_id,
            1 if blink else 0,
        )
    )


def test_decode_key_state():
    message = wire.decode_key_state(
        _key_state_bytes(key_index=3, color=0xF81F, emoji_id=0xA2, blink=True)
    )

    assert message.key_index == 3
    assert message.version == wire.PROTOCOL_VERSION
    assert message.color == 0xF81F
    assert message.emoji_id == 0xA2
    assert message.blink is True


def test_decode_key_state_rejects_version():
    with pytest.raises(wire.UnsupportedVersionError):
        wire.decode_key_state(_key_state_bytes(version=wire.PROTOCOL_VERSION + 1))


def test_decode_key_state_rejects_short_message():
    with pytest.raises(ValueError):
        wire.decode_key_state(_key_state_bytes()[:-1])


def test_encode_event_press():
    event = wire.encode_event(2, wire.PRESS, 0x0102030405060708)

    assert len(event) == wire.EVENT_SIZE
    assert event[0] == 2
    assert event[1] == wire.PRESS
    # Little-endian, matching driver/transport/wire.go's PutUint64.
    assert event[2:] == bytes((0x08, 0x07, 0x06, 0x05, 0x04, 0x03, 0x02, 0x01))


def test_encode_event_release():
    event = wire.encode_event(5, wire.RELEASE, 0)

    assert event == bytes((5, wire.RELEASE, 0, 0, 0, 0, 0, 0, 0, 0))


def test_encode_event_rejects_unknown_type():
    with pytest.raises(ValueError):
        wire.encode_event(0, 2, 0)


def test_write_frame_prefixes_type_and_length():
    writer = FakeWriter()
    payload = wire.encode_event(2, wire.PRESS, 0)

    wire.write_frame(writer, wire.MESSAGE_TYPE_EVENT, payload)

    assert len(writer.written) == wire.FRAME_HEADER_SIZE + wire.EVENT_SIZE
    header = writer.written[: wire.FRAME_HEADER_SIZE]
    # Type byte, then payload length as a little-endian uint16, matching
    # driver/transport/wire.go's writeFrame.
    assert header == bytes((wire.MESSAGE_TYPE_EVENT, wire.EVENT_SIZE, 0))
    assert writer.written[wire.FRAME_HEADER_SIZE :] == payload
