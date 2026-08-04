"""Fake `audiobusio` module. See ../README.md for how these stubs are used."""


class I2SIn:
    """Fake I2S mic input. Fills the caller's buffer with silence."""

    def __init__(self, bit_clock, word_select, data, *, sample_rate=16000, bits_per_sample=16):
        self.sample_rate = sample_rate
        self.bits_per_sample = bits_per_sample
        self._deinited = False

    def record(self, buffer, count):
        for i in range(min(count, len(buffer))):
            buffer[i] = 0
        return count

    def deinit(self):
        self._deinited = True

    def __enter__(self):
        return self

    def __exit__(self, *exc_info):
        self.deinit()
