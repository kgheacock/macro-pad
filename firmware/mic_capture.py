"""Wraps `audiobusio.I2SIn` and feeds captured samples into task 0005's
ring buffer while a key is held. Reads through `AudioSourceLike`, a small
interface that both a real `audiobusio.I2SIn` and a test fake satisfy.

See tasks/ongoing/0007-i2s-mic-capture-module.md for the design decision.
"""

from typing import Protocol

from audio_buffer import DEFAULT_CHUNK_SIZE, RingBuffer


class AudioSourceLike(Protocol):
    """The subset of `audiobusio.I2SIn` this module calls.

    `record(destination, count)` is the only capture method
    `test/stubs/audiobusio.py`'s `I2SIn` fake exposes, matching real
    CircuitPython's `I2SIn.record(destination, destination_length)`: it
    fills `destination` with up to `count` samples and returns the number
    actually written. `readinto`, considered during design, is not a
    method either the stub or the real driver exposes, so this module
    does not call it.
    """

    def record(self, destination: bytearray, count: int) -> int: ...


class MicCapture:
    """Pumps samples from an `AudioSourceLike` into a `RingBuffer` while
    a key is held.

    `start()` and `stop()` are meant to be called from the debounced
    press and release events in task 0003. Call `poll()` on every
    main-loop tick to keep pulling samples while capture is active; it is
    a no-op when `stop()` has been called since the last `start()`.
    """

    def __init__(
        self,
        source: AudioSourceLike,
        buffer: RingBuffer,
        chunk_size: int = DEFAULT_CHUNK_SIZE,
    ) -> None:
        self._source = source
        self._buffer = buffer
        self._scratch = bytearray(chunk_size)
        self._active = False

    @property
    def active(self) -> bool:
        return self._active

    def start(self) -> None:
        """Begin capture. A second call before a matching `stop()` is a
        no-op, so a held key that re-fires a press event does not restart
        capture mid-buffer."""
        if self._active:
            return
        self._active = True
        self.poll()

    def stop(self) -> None:
        """Halt capture. Samples already written to the ring buffer are
        left in place for the caller to drain."""
        self._active = False

    def poll(self) -> None:
        """Pull one chunk from the source into the ring buffer. No-op
        when capture is not active."""
        if not self._active:
            return
        count = self._source.record(self._scratch, len(self._scratch))
        self._buffer.write(self._scratch[:count])
