import zlib

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


def _custom_glyph_payload(key_index=2, fill=0xAB):
    return bytes((key_index,)) + bytes([fill]) * wire.CUSTOM_GLYPH_PIXELS_SIZE


def test_decode_custom_glyph():
    payload = _custom_glyph_payload(key_index=3, fill=0xCD)

    glyph = wire.decode_custom_glyph(payload)

    assert glyph.key_index == 3
    assert len(glyph.pixels) == wire.CUSTOM_GLYPH_PIXELS_SIZE
    assert glyph.pixels == bytes([0xCD]) * wire.CUSTOM_GLYPH_PIXELS_SIZE


def test_decode_custom_glyph_rejects_wrong_size():
    with pytest.raises(ValueError):
        wire.decode_custom_glyph(_custom_glyph_payload()[:-1])


def test_custom_glyph_reader_returns_none_until_full_frame_arrives():
    writer = FakeWriter()
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH, _custom_glyph_payload(key_index=4)
    )
    framed = bytes(writer.written)

    class FeedReader:
        def __init__(self, data):
            self._data = data
            self.in_waiting = 0

        def queue(self, n):
            self.in_waiting = n

        def read(self, size):
            chunk = self._data[:size]
            self._data = self._data[size:]
            self.in_waiting = 0
            return chunk

        def readinto(self, buffer):
            chunk = self._data[: len(buffer)]
            buffer[: len(chunk)] = chunk
            self._data = self._data[len(chunk) :]
            self.in_waiting = 0
            return len(chunk)

    reader = FeedReader(framed)
    custom_glyph_reader = wire.CustomGlyphReader()

    # Feed the header first, in isolation: not enough to decode yet.
    reader.queue(wire.FRAME_HEADER_SIZE)
    assert custom_glyph_reader.feed(reader) is None

    # The rest arrives in one later step.
    reader.queue(len(framed) - wire.FRAME_HEADER_SIZE)
    glyph = custom_glyph_reader.feed(reader)

    assert glyph is not None
    assert glyph.key_index == 4
    assert len(glyph.pixels) == wire.CUSTOM_GLYPH_PIXELS_SIZE


def test_custom_glyph_reader_skips_unknown_frame_type():
    writer = FakeWriter()
    wire.write_frame(writer, wire.MESSAGE_TYPE_EVENT, wire.encode_event(0, wire.PRESS, 0))
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH, _custom_glyph_payload(key_index=1)
    )

    class FeedReader:
        def __init__(self, data):
            self._data = data

        @property
        def in_waiting(self):
            return len(self._data)

        def read(self, size):
            chunk = self._data[:size]
            self._data = self._data[size:]
            return chunk

        def readinto(self, buffer):
            chunk = self._data[: len(buffer)]
            buffer[: len(chunk)] = chunk
            self._data = self._data[len(chunk) :]
            return len(chunk)

    reader = FeedReader(bytes(writer.written))
    custom_glyph_reader = wire.CustomGlyphReader()

    # The unknown (event) frame is fully buffered and dropped in one feed.
    first = custom_glyph_reader.feed(reader)
    assert first is None

    second = custom_glyph_reader.feed(reader)
    assert second is not None
    assert second.key_index == 1


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


def test_custom_glyph_reader_consumes_a_wrong_length_frame_before_raising():
    """A glyph frame of the wrong length raises `ValueError`, and the
    frame is already out of the buffer, so the next frame still decodes.
    """
    writer = FakeWriter()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH,
        _custom_glyph_payload(key_index=1)[:-2],
    )
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH, _custom_glyph_payload(key_index=2)
    )

    class AllAtOnceReader:
        def __init__(self, data):
            self._data = data
            self.in_waiting = len(data)

        def read(self, size):
            chunk, self._data = self._data[:size], self._data[size:]
            self.in_waiting = len(self._data)
            return chunk

        def readinto(self, buffer):
            chunk = self._data[: len(buffer)]
            buffer[: len(chunk)] = chunk
            self._data = self._data[len(chunk) :]
            self.in_waiting = len(self._data)
            return len(chunk)

    reader = AllAtOnceReader(bytes(writer.written))
    custom_glyph_reader = wire.CustomGlyphReader()

    with pytest.raises(ValueError):
        custom_glyph_reader.feed(reader)

    assert custom_glyph_reader.feed(reader).key_index == 2


class _StallingReader:
    """A serial port that hands over `chunk_size` bytes, then nothing more
    until `release` — a host that pauses in the middle of a frame."""

    def __init__(self, data, chunk_size):
        self._data = data
        self._chunk_size = chunk_size
        self._released = 0
        self.in_waiting = 0
        self.release(chunk_size)

    def release(self, n):
        self.in_waiting = min(n, len(self._data))

    def readinto(self, buffer):
        n = min(len(buffer), self.in_waiting)
        buffer[:n] = self._data[:n]
        self._data = self._data[n:]
        self.in_waiting -= n
        return n


class _TickingClock:
    """Each call returns a time `step_ns` later than the one before."""

    def __init__(self, step_ns):
        self._now = 0
        self._step_ns = step_ns
        self.calls = 0

    def __call__(self):
        self.calls += 1
        self._now += self._step_ns
        return self._now


def _framed_glyph(key_index):
    writer = FakeWriter()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH,
        _custom_glyph_payload(key_index=key_index),
    )
    return bytes(writer.written)


def test_custom_glyph_reader_stops_reading_when_the_time_budget_ends():
    """A frame that stalls midway makes one `feed` return `None` once the
    budget has passed, instead of waiting for the rest of the frame."""
    reader = _StallingReader(_framed_glyph(2), chunk_size=1000)
    clock = _TickingClock(step_ns=1000000)  # 1 ms per clock call
    glyph_reader = wire.CustomGlyphReader(clock=clock, budget_ns=8000000)

    assert glyph_reader.feed(reader) is None

    # About 8 clock calls at 1 ms each, plus the start call: the call
    # ended at the budget, not after the frame.
    assert clock.calls <= 12


def test_custom_glyph_reader_finishes_a_stalled_frame_on_a_later_feed():
    framed = _framed_glyph(5)
    reader = _StallingReader(framed, chunk_size=1000)
    glyph_reader = wire.CustomGlyphReader(
        clock=_TickingClock(step_ns=1000000), budget_ns=8000000
    )

    assert glyph_reader.feed(reader) is None
    reader.release(len(framed))
    glyph = glyph_reader.feed(reader)

    assert glyph is not None
    assert glyph.key_index == 5
    assert glyph.pixels == bytes([0xAB]) * wire.CUSTOM_GLYPH_PIXELS_SIZE


def test_custom_glyph_reader_idle_feed_does_not_read_the_clock():
    reader = _StallingReader(b"", chunk_size=0)
    clock = _TickingClock(step_ns=1000000)
    glyph_reader = wire.CustomGlyphReader(clock=clock)

    assert glyph_reader.feed(reader) is None
    assert clock.calls == 0


def test_custom_glyph_reader_leaves_the_next_frame_in_the_port():
    """The reader takes exactly one frame's bytes, so a second frame that
    is already waiting is still there for the next `feed`."""
    framed = _framed_glyph(1) + _framed_glyph(2)
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    first = glyph_reader.feed(reader)

    assert first.key_index == 1
    assert reader.in_waiting == len(framed) // 2
    assert glyph_reader.feed(reader).key_index == 2


def _patterned_pixels():
    """128x128 RGB565 pixels with runs and a transparent corner, like an icon."""
    row = bytes([0x12, 0x34]) * 100 + bytes([0x00, 0x00]) * 28
    return row * wire.CUSTOM_GLYPH_HEIGHT


def _framed_compressed(key_index, stream):
    writer = FakeWriter()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH_COMPRESSED,
        bytes((key_index,)) + stream,
    )
    return bytes(writer.written)


def _read_all(framed):
    reader = _StallingReader(framed, chunk_size=len(framed))
    return wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000)), reader


def test_compressed_glyph_decodes_to_the_same_pixels_as_a_raw_glyph():
    pixels = _patterned_pixels()
    stream = zlib.compress(pixels, 9)
    assert len(stream) < 1000

    glyph_reader, reader = _read_all(_framed_compressed(3, stream))
    compressed = glyph_reader.feed(reader)

    raw_writer = FakeWriter()
    wire.write_frame(
        raw_writer, wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH, bytes((3,)) + pixels
    )
    glyph_reader, reader = _read_all(bytes(raw_writer.written))
    raw = glyph_reader.feed(reader)

    assert compressed.key_index == raw.key_index == 3
    assert compressed.pixels == raw.pixels == pixels


def test_compressed_glyph_that_inflates_to_the_wrong_size_raises_and_the_next_frame_decodes():
    short = zlib.compress(_patterned_pixels()[:-2], 9)
    good = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(1, short) + _framed_compressed(2, good)

    glyph_reader, reader = _read_all(framed)
    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    glyph = glyph_reader.feed(reader)
    assert glyph.key_index == 2
    assert glyph.pixels == _patterned_pixels()


def test_compressed_glyph_with_a_corrupt_stream_raises_and_the_next_frame_decodes():
    good = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(1, b"\x78\x9c\xff\xff\xff") + _framed_compressed(
        2, good
    )

    glyph_reader, reader = _read_all(framed)
    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).key_index == 2


def test_compressed_glyph_frame_as_long_as_a_raw_one_is_dropped():
    """The driver sends type 6 only for a stream smaller than the raw
    payload, so a longer frame cannot be a glyph."""
    stream = bytes(wire.CUSTOM_GLYPH_PAYLOAD_SIZE)  # 1 + this = too long
    good = _framed_glyph(2)
    framed = _framed_compressed(1, stream) + good

    glyph_reader, reader = _read_all(framed)
    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).key_index == 2


def test_compressed_glyph_frame_arrives_over_several_feeds():
    stream = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(4, stream)
    reader = _StallingReader(framed, chunk_size=7)
    glyph_reader = wire.CustomGlyphReader(
        clock=_TickingClock(step_ns=1000000), budget_ns=8000000
    )

    assert glyph_reader.feed(reader) is None
    reader.release(len(framed))
    glyph = glyph_reader.feed(reader)

    assert glyph.key_index == 4
    assert glyph.pixels == _patterned_pixels()
