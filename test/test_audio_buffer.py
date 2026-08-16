from firmware.audio_buffer import RingBuffer, chunk_stream


def test_full_chunk():
    buffer = RingBuffer(capacity_bytes=16)
    buffer.write(bytes(range(8)))

    assert buffer.read_chunk(4) == bytes(range(4))


def test_final_chunk_flag():
    buffer = RingBuffer(capacity_bytes=16)
    buffer.write(bytes(range(10)))

    chunks = list(chunk_stream(buffer, chunk_size=4, released=True))

    assert [c for c, _ in chunks] == [bytes(range(4)), bytes(range(4, 8)), bytes(range(8, 10))]
    assert [is_final for _, is_final in chunks] == [False, False, True]
    assert buffer.is_empty


def test_chunk_stream_waits_for_full_chunk_until_released():
    buffer = RingBuffer(capacity_bytes=16)
    buffer.write(bytes(range(4)))

    assert list(chunk_stream(buffer, chunk_size=4, released=False)) == [(bytes(range(4)), False)]

    buffer.write(bytes([9, 9]))
    assert list(chunk_stream(buffer, chunk_size=4, released=False)) == []
    assert len(buffer) == 2

    assert list(chunk_stream(buffer, chunk_size=4, released=True)) == [(bytes([9, 9]), True)]


def test_overwrite_oldest():
    buffer = RingBuffer(capacity_bytes=4)
    buffer.write(bytes([1, 2, 3, 4]))
    buffer.write(bytes([5, 6]))

    assert buffer.read_chunk(4) == bytes([3, 4, 5, 6])
