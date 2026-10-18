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


def _framed_begin_batch(glyph_mask, state_mask):
    writer = FakeWriter()
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_BEGIN_BATCH, bytes((glyph_mask, state_mask))
    )
    return bytes(writer.written)


def test_begin_batch_frame_decodes_to_its_two_masks():
    framed = _framed_begin_batch(0b101010, 0b010101)
    reader = _StallingReader(framed, chunk_size=len(framed))

    message = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000)).feed(reader)

    assert isinstance(message, wire.BeginBatch)
    assert message.glyph_mask == 0b101010
    assert message.state_mask == 0b010101
    assert framed[0] == wire.MESSAGE_TYPE_BEGIN_BATCH == 7


def test_begin_batch_between_two_glyphs_leaves_both_glyphs_intact():
    framed = _framed_glyph(1) + _framed_begin_batch(3, 3) + _framed_glyph(2)
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    first = glyph_reader.feed(reader)
    batch = glyph_reader.feed(reader)
    second = glyph_reader.feed(reader)

    assert first.key_index == 1
    assert (batch.glyph_mask, batch.state_mask) == (3, 3)
    assert second.key_index == 2
    assert second.pixels == bytes([0xAB]) * wire.CUSTOM_GLYPH_PIXELS_SIZE


def test_begin_batch_of_the_wrong_length_raises_and_the_next_frame_decodes():
    writer = FakeWriter()
    wire.write_frame(writer, wire.MESSAGE_TYPE_BEGIN_BATCH, b"\x01\x02\x03")
    framed = bytes(writer.written) + _framed_begin_batch(1, 1)
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).glyph_mask == 1


def _framed_store_frame(slot, color, stream=b""):
    writer = FakeWriter()
    payload = bytes((slot, color & 0xFF, color >> 8)) + stream
    wire.write_frame(writer, wire.MESSAGE_TYPE_STORE_FRAME, payload)
    return bytes(writer.written)


def _framed_show_frames(slots):
    writer = FakeWriter()
    wire.write_frame(writer, wire.MESSAGE_TYPE_SHOW_FRAMES, bytes(slots))
    return bytes(writer.written)


def test_frame_types_follow_those_of_task_0060():
    assert wire.MESSAGE_TYPE_STORE_FRAME == 8
    assert wire.MESSAGE_TYPE_SHOW_FRAMES == 9
    assert wire.MESSAGE_TYPE_SHOW_MISS == 10


def test_store_frame_decodes_the_slot_the_color_and_the_inflated_glyph():
    pixels = _patterned_pixels()
    framed = _framed_store_frame(77, 0xF81F, zlib.compress(pixels, 9))

    glyph_reader, reader = _read_all(framed)
    message = glyph_reader.feed(reader)

    assert isinstance(message, wire.StoreFrame)
    assert (message.slot, message.color) == (77, 0xF81F)
    assert message.pixels == pixels


def test_store_frame_of_a_color_alone_has_no_pixels():
    glyph_reader, reader = _read_all(_framed_store_frame(0, 0x07E0))

    message = glyph_reader.feed(reader)

    assert isinstance(message, wire.StoreFrame)
    assert (message.slot, message.color, message.pixels) == (0, 0x07E0, None)


def test_store_frame_that_does_not_inflate_raises_and_the_next_frame_decodes():
    framed = _framed_store_frame(1, 0, b"not a zlib stream") + _framed_show_frames([1])
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).slots == bytes((1,))


def test_store_frame_shorter_than_its_header_is_dropped():
    writer = FakeWriter()
    wire.write_frame(writer, wire.MESSAGE_TYPE_STORE_FRAME, b"\x01\x02")
    framed = bytes(writer.written) + _framed_show_frames([3])
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).slots == bytes((3,))


def test_show_frames_decodes_a_slot_or_unchanged_for_each_key():
    slots = [0, 127, wire.SLOT_UNCHANGED, 5, wire.SLOT_UNCHANGED, 64]
    glyph_reader, reader = _read_all(_framed_show_frames(slots))

    message = glyph_reader.feed(reader)

    assert isinstance(message, wire.ShowFrames)
    assert list(message.slots) == slots


def test_show_frames_with_no_key_is_dropped():
    framed = _framed_show_frames([]) + _framed_show_frames([2])
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).slots == bytes((2,))


def test_show_miss_payload_is_the_mask_of_the_missed_keys():
    writer = FakeWriter()
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_SHOW_MISS, wire.encode_show_miss(0b100101)
    )

    assert bytes(writer.written) == bytes((10, 1, 0, 0b100101))


# --- The second core (task 0059) ---------------------------------------------

from fake_glyphcore import FakeGlyphcore  # noqa: E402


def _core1_reader(core1, framed, timeout_ns=500_000_000, clock=None):
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(
        clock=clock or _TickingClock(step_ns=1000), core1=core1, timeout_ns=timeout_ns
    )
    return glyph_reader, reader


def _feed_until_message(glyph_reader, reader, limit=20):
    for _ in range(limit):
        message = glyph_reader.feed(reader)
        if message is not None:
            return message
    raise AssertionError("no message after {} feeds".format(limit))


def _framed_store(slot, color, stream):
    writer = FakeWriter()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_STORE_FRAME,
        bytes((slot, color & 0xFF, color >> 8)) + stream,
    )
    return bytes(writer.written)


def test_without_the_module_the_reader_inflates_on_core_0():
    """On stock CircuitPython `import glyphcore` fails, and nothing changes."""
    assert wire.glyphcore is None

    glyph_reader, reader = _read_all(
        _framed_compressed(3, zlib.compress(_patterned_pixels(), 9))
    )
    glyph = glyph_reader.feed(reader)

    assert glyph.key_index == 3
    assert isinstance(glyph.pixels, bytes)
    assert glyph.pixels == _patterned_pixels()


def test_with_the_module_the_reader_hands_the_stream_to_core_1(monkeypatch):
    core1 = FakeGlyphcore()
    monkeypatch.setattr(wire, "glyphcore", core1)
    framed = _framed_compressed(3, zlib.compress(_patterned_pixels(), 9))
    reader = _StallingReader(framed, chunk_size=len(framed))
    glyph_reader = wire.CustomGlyphReader(clock=_TickingClock(step_ns=1000))

    assert glyph_reader.feed(reader) is None
    assert len(core1.submitted) == 1


def test_the_glyph_from_core_1_has_the_same_pixels_as_from_core_0():
    pixels = _patterned_pixels()
    stream = zlib.compress(pixels, 9)

    on_core_0, reader = _read_all(_framed_compressed(3, stream))
    expected = on_core_0.feed(reader)
    on_core_1, reader = _core1_reader(FakeGlyphcore(), _framed_compressed(3, stream))
    glyph = _feed_until_message(on_core_1, reader)

    assert glyph.key_index == expected.key_index == 3
    assert glyph.pixels.format == "H"
    assert bytes(glyph.pixels) == expected.pixels == pixels


def test_feed_reads_the_next_frame_while_core_1_inflates():
    core1 = FakeGlyphcore(polls_before_done=5)
    stream = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(1, stream) + _framed_compressed(2, stream)
    glyph_reader, reader = _core1_reader(core1, framed)

    assert glyph_reader.feed(reader) is None  # key 1 is submitted
    assert glyph_reader.feed(reader) is None  # key 2 is read and waits
    assert reader.in_waiting == 0
    assert len(core1.submitted) == 1


def test_messages_leave_in_the_order_they_arrived():
    core1 = FakeGlyphcore(polls_before_done=2)
    stream = zlib.compress(_patterned_pixels(), 9)
    framed = (
        _framed_compressed(1, stream)
        + _framed_begin_batch(0b10, 0b10)
        + _framed_compressed(2, stream)
    )
    glyph_reader, reader = _core1_reader(core1, framed)

    kinds = []
    for _ in range(30):
        message = glyph_reader.feed(reader)
        if message is not None:
            kinds.append(type(message).__name__)

    assert kinds == ["CustomGlyph", "BeginBatch", "CustomGlyph"]


def test_a_key_inflates_into_the_buffer_it_does_not_draw_from():
    core1 = FakeGlyphcore(polls_before_done=0)
    first = _patterned_pixels()
    second = bytes([0x56, 0x78]) * (wire.CUSTOM_GLYPH_PIXELS_SIZE // 2)
    framed = _framed_compressed(2, zlib.compress(first, 9)) + _framed_compressed(
        2, zlib.compress(second, 9)
    )
    glyph_reader, reader = _core1_reader(core1, framed)

    one = _feed_until_message(glyph_reader, reader)
    two = _feed_until_message(glyph_reader, reader)

    assert [buffer for _, buffer in core1.submitted] == [4, 5]
    assert bytes(one.pixels) == first  # still whole after the second glyph
    assert bytes(two.pixels) == second
    assert one.pixels is not two.pixels


def test_a_third_glyph_for_a_key_returns_to_the_first_buffer_as_a_new_view():
    core1 = FakeGlyphcore(polls_before_done=0)
    stream = zlib.compress(_patterned_pixels(), 9)
    framed = b"".join(_framed_compressed(0, stream) for _ in range(3))
    glyph_reader, reader = _core1_reader(core1, framed)

    views = [_feed_until_message(glyph_reader, reader).pixels for _ in range(3)]

    assert [buffer for _, buffer in core1.submitted] == [0, 1, 0]
    assert views[0] is not views[2]  # the identity check of display_render sees it


def test_a_store_frame_inflates_into_the_staging_buffer():
    core1 = FakeGlyphcore(polls_before_done=0)
    pixels = _patterned_pixels()
    framed = _framed_store(9, 0xF81F, zlib.compress(pixels, 9))
    glyph_reader, reader = _core1_reader(core1, framed)

    message = _feed_until_message(glyph_reader, reader)

    assert isinstance(message, wire.StoreFrame)
    assert (message.slot, message.color) == (9, 0xF81F)
    assert bytes(message.pixels) == pixels
    assert core1.submitted[0][1] == wire._STAGING_BUFFER


def test_a_store_frame_without_a_stream_needs_no_inflate():
    core1 = FakeGlyphcore()
    glyph_reader, reader = _core1_reader(core1, _framed_store(3, 0x07E0, b""))

    message = glyph_reader.feed(reader)

    assert (message.slot, message.color, message.pixels) == (3, 0x07E0, None)
    assert core1.submitted == []


def test_when_core_1_is_busy_the_reader_inflates_on_core_0():
    core1 = FakeGlyphcore()
    core1.refuse = True
    pixels = _patterned_pixels()
    glyph_reader, reader = _core1_reader(
        core1, _framed_compressed(3, zlib.compress(pixels, 9))
    )

    glyph = glyph_reader.feed(reader)

    assert glyph.key_index == 3
    assert glyph.pixels == pixels
    assert core1.submitted == []


def test_a_stream_that_core_1_cannot_inflate_raises_and_the_next_frame_decodes():
    core1 = FakeGlyphcore(polls_before_done=0)
    good = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(1, b"\x78\x9c\xff\xff\xff") + _framed_compressed(2, good)
    glyph_reader, reader = _core1_reader(core1, framed)

    with pytest.raises(ValueError):
        _feed_until_message(glyph_reader, reader)

    assert _feed_until_message(glyph_reader, reader).key_index == 2


def test_a_glyph_that_core_1_never_finishes_is_dropped_and_the_reader_goes_on():
    core1 = FakeGlyphcore(never_answers=True)
    stream = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(1, stream) + _framed_begin_batch(0b100, 0)
    clock = _TickingClock(step_ns=100_000_000)  # 100 ms per call
    glyph_reader, reader = _core1_reader(core1, framed, timeout_ns=500_000_000, clock=clock)

    messages = []
    for _ in range(40):
        message = glyph_reader.feed(reader)
        if message is not None:
            messages.append(message)

    assert glyph_reader.dropped == 1
    assert core1.cancels >= 2  # one when the reader was built, one for the drop
    # No glyph came out. The frame behind it did, and the reader is free.
    assert [type(m).__name__ for m in messages] == ["BeginBatch"]


def test_the_reader_clears_a_job_that_a_reload_left_in_the_module():
    core1 = FakeGlyphcore()

    wire.CustomGlyphReader(core1=core1)

    assert core1.cancels == 1


# --- Key state over CDC (task 0059) --------------------------------------------


def _framed_key_state(key_index=0, color=0x0000, emoji_id=0, blink=False, version=wire.PROTOCOL_VERSION):
    writer = FakeWriter()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_KEY_STATE,
        _key_state_bytes(key_index, version, color, emoji_id, blink),
    )
    return bytes(writer.written)


def test_a_key_state_frame_decodes_like_the_hid_report():
    framed = _framed_key_state(key_index=3, color=0xF81F, emoji_id=0xA2, blink=True)
    glyph_reader, reader = _read_all(framed)

    message = glyph_reader.feed(reader)

    assert isinstance(message, wire.KeyState)
    assert (message.key_index, message.color, message.emoji_id, message.blink) == (
        3, 0xF81F, 0xA2, True
    )
    assert message.version == wire.PROTOCOL_VERSION


def test_a_key_state_frame_of_the_wrong_length_is_dropped_and_the_next_frame_decodes():
    writer = FakeWriter()
    wire.write_frame(writer, wire.MESSAGE_TYPE_KEY_STATE, bytes(5))
    framed = bytes(writer.written) + _framed_key_state(key_index=2)
    glyph_reader, reader = _read_all(framed)

    with pytest.raises(ValueError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).key_index == 2


def test_a_key_state_frame_of_another_version_raises_and_the_next_frame_decodes():
    framed = _framed_key_state(version=wire.PROTOCOL_VERSION + 1) + _framed_key_state(key_index=4)
    glyph_reader, reader = _read_all(framed)

    with pytest.raises(wire.UnsupportedVersionError):
        glyph_reader.feed(reader)

    assert glyph_reader.feed(reader).key_index == 4


def test_a_key_state_behind_a_glyph_on_core_1_leaves_after_it():
    core1 = FakeGlyphcore(polls_before_done=2)
    stream = zlib.compress(_patterned_pixels(), 9)
    framed = _framed_compressed(1, stream) + _framed_key_state(key_index=1)
    glyph_reader, reader = _core1_reader(core1, framed)

    kinds = []
    for _ in range(30):
        message = glyph_reader.feed(reader)
        if message is not None:
            kinds.append(type(message).__name__)

    assert kinds == ["CustomGlyph", "KeyState"]
