"""Fake `usb_cdc` module. See ../README.md for how these stubs are used."""


class Serial:
    """Fake CDC serial endpoint. Buffers writes and queued reads for tests."""

    def __init__(self):
        self.connected = True
        self.in_waiting = 0
        self._written = bytearray()
        self._to_read = bytearray()

    def write(self, data):
        self._written.extend(data)
        return len(data)

    def read(self, size=1):
        chunk = bytes(self._to_read[:size])
        self._to_read = self._to_read[size:]
        self.in_waiting = len(self._to_read)
        return chunk

    def feed(self, data):
        """Test helper: queue bytes to be returned by the next read()."""
        self._to_read.extend(data)
        self.in_waiting = len(self._to_read)


data = Serial()
console = Serial()
