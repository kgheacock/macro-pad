"""A stand-in for the `glyphcore` module of the custom CircuitPython build
(task 0059). It is not in `test/stubs/`, which every test puts on the path: the
firmware imports `glyphcore` when it finds one, and most tests run without it,
as on stock CircuitPython. A test that wants core 1 passes an instance as
`core1=` / `core=`, or sets `wire.glyphcore` with `monkeypatch`.

It keeps the contract of the real module: one job at a time; `submit` copies
the stream and raises `OSError` while a job or an uncollected result exists;
`poll` returns `None` until `polls_before_done` calls have passed; the glyph and
slot buffers are bytearrays that outlive the call; `glyph_view` returns a new
`memoryview` of 16-bit items on each call.
"""

import zlib

GLYPH_BYTES = 32768
GLYPH_BUFFERS = 16


class FakeGlyphcore:
    def __init__(self, polls_before_done=1, never_answers=False):
        self.polls_before_done = polls_before_done
        self.never_answers = never_answers
        self.glyphs = [bytearray(GLYPH_BYTES) for _ in range(GLYPH_BUFFERS)]
        self.slots = []
        self.submitted = []  # (stream, buffer) of every accepted job
        self.cancels = 0
        self.refuse = False  # True makes submit raise OSError, as in a flash write
        self._job = None
        self._polls = 0

    def alloc(self, frame_slots):
        self.slots = [bytearray(GLYPH_BYTES) for _ in range(frame_slots)]

    def slot_view(self, slot):
        return memoryview(self.slots[slot])

    def glyph_view(self, index):
        return memoryview(self.glyphs[index]).cast("H")

    def has_transparent(self, pixels):
        data = bytes(pixels)
        return any(data[i : i + 2] == b"\x00\x00" for i in range(0, len(data), 2))

    def submit(self, stream, index):
        if self.refuse or self._job is not None:
            raise OSError(16)
        self._job = (bytes(stream), index)
        self._polls = 0
        self.submitted.append(self._job)

    def poll(self):
        if self._job is None or self.never_answers:
            return None
        self._polls += 1
        if self._polls <= self.polls_before_done:
            return None
        stream, index = self._job
        self._job = None
        try:
            pixels = zlib.decompress(stream)
        except zlib.error:
            raise ValueError("glyph does not inflate")
        if len(pixels) != GLYPH_BYTES:
            raise ValueError("glyph is not 32768 bytes")
        self.glyphs[index][:] = pixels
        return GLYPH_BYTES

    def cancel(self):
        self.cancels += 1
        self._job = None
        return True
