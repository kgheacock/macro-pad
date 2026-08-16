from audio_buffer import RingBuffer
from mic_capture import MicCapture


class FakeAudioSource:
    """Fills `destination` from a scripted list of byte sequences, one
    per `record()` call, and records how each call was made."""

    def __init__(self, chunks):
        self._chunks = list(chunks)
        self.record_calls = []

    def record(self, destination, count):
        self.record_calls.append(count)
        chunk = self._chunks.pop(0) if self._chunks else b""
        n = min(len(chunk), count, len(destination))
        for i in range(n):
            destination[i] = chunk[i]
        return n


def test_start_writes_buffer():
    source = FakeAudioSource([b"\x01\x02\x03\x04"])
    buffer = RingBuffer(64)
    capture = MicCapture(source, buffer, chunk_size=4)

    capture.start()

    assert buffer.read_chunk(4) == b"\x01\x02\x03\x04"


def test_stop_halts_writes():
    source = FakeAudioSource([b"\x01\x02", b"\x03\x04"])
    buffer = RingBuffer(64)
    capture = MicCapture(source, buffer, chunk_size=2)

    capture.start()
    capture.stop()
    capture.poll()  # would pull the second chunk if still active

    assert len(source.record_calls) == 1
    assert len(buffer) == 2
    assert buffer.read_chunk(2) == b"\x01\x02"


def test_double_start_noop():
    source = FakeAudioSource([b"\x01\x02", b"\x03\x04"])
    buffer = RingBuffer(64)
    capture = MicCapture(source, buffer, chunk_size=2)

    capture.start()
    capture.start()

    assert len(source.record_calls) == 1
    assert len(buffer) == 2
