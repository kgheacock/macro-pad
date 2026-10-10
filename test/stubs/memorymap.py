"""Fake `memorymap` module. See ../README.md for how these stubs are used.

`AddressRange` reads and writes a 4-byte register held in `registers`, a dict
from address to value. A register nobody set reads as 0. `writes` keeps every
`(address, value)` written, in order, so a test can tell which registers a
function touched.
"""

import struct

registers = {}
writes = []


def reset():
    registers.clear()
    del writes[:]


class AddressRange:
    def __init__(self, *, start, length):
        assert length == 4, "the stub models 32-bit registers only"
        self.start = start

    def __getitem__(self, index):
        return struct.pack("<I", registers.get(self.start, 0))[index]

    def __setitem__(self, index, value):
        (word,) = struct.unpack("<I", bytes(value))
        registers[self.start] = word
        writes.append((self.start, word))
