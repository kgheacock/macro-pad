"""Buffers mic samples and drains them as fixed-size audio chunks.

`firmware/README.md` buffers mic audio in a ring buffer while a key is
held, then streams it to the host in fixed-size chunks with a final-chunk
flag, matching the Audio chunk message in docs/wire-protocol.md. This
module supplies that buffering and chunking; it does not read real I2S
audio or encode the wire message itself.

See tasks/ongoing/0005-audio-ring-buffer-chunking.md for the design
decision.
"""

# docs/wire-protocol.md's Framing section gives the frame header a 2-byte
# little-endian Length field, so a frame's payload cannot exceed 65535
# bytes. The audio chunk payload spends 2 of those bytes on the stream ID
# and the final-chunk flag (docs/wire-protocol.md's Audio chunk message),
# leaving 65533 for PCM data. 512 sits comfortably under that ceiling.
DEFAULT_CHUNK_SIZE = 512


class RingBuffer:
    """A fixed-capacity circular byte buffer for buffered mic samples.

    Writing past capacity overwrites the oldest unread bytes, trading
    audio loss for a bounded memory footprint on a resource-constrained
    MCU — the tradeoff Approach A in the task spec accepts.
    """

    def __init__(self, capacity_bytes):
        if capacity_bytes <= 0:
            raise ValueError("capacity_bytes must be positive")

        self._buffer = bytearray(capacity_bytes)
        self._capacity = capacity_bytes
        self._write_pos = 0
        self._read_pos = 0
        self._count = 0

    def __len__(self):
        return self._count

    @property
    def is_empty(self):
        return self._count == 0

    def write(self, data):
        """Append `data` to the buffer, wrapping and overwriting the
        oldest unread bytes once the buffer is full."""
        for byte in data:
            self._buffer[self._write_pos] = byte
            self._write_pos = (self._write_pos + 1) % self._capacity
            if self._count < self._capacity:
                self._count += 1
            else:
                self._read_pos = (self._read_pos + 1) % self._capacity

    def read_chunk(self, chunk_size):
        """Remove and return up to `chunk_size` bytes, oldest first.

        Returns fewer than `chunk_size` bytes when that much isn't
        buffered yet."""
        n = min(chunk_size, self._count)
        result = bytearray(n)
        for i in range(n):
            result[i] = self._buffer[self._read_pos]
            self._read_pos = (self._read_pos + 1) % self._capacity
        self._count -= n
        return bytes(result)


def chunk_stream(buffer, chunk_size, released):
    """Yield `(chunk, is_final)` pairs draining `buffer` in `chunk_size`
    pieces, matching the audio-chunk format from task 0002.

    Drains every full `chunk_size` piece currently buffered. Once fewer
    than `chunk_size` bytes remain, it waits for more unless `released`
    is set, in which case it drains the remainder as one final chunk. The
    last chunk yielded — whether a full piece or the remainder — carries
    `is_final=True` only when `released` is set and the buffer is empty
    afterward.
    """
    while len(buffer) >= chunk_size:
        chunk = buffer.read_chunk(chunk_size)
        is_final = released and buffer.is_empty
        yield chunk, is_final
        if is_final:
            return

    if released and not buffer.is_empty:
        yield buffer.read_chunk(len(buffer)), True
