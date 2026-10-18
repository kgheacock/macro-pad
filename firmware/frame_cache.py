"""A fixed pool of composed frames, in RAM only (task 0061).

The host sends a Store frame message once for each picture and color, and the
board composes it into a slot. A later Show frames message names slots, and a
layer switch then sends no image and composes nothing.

The pool is `SLOT_COUNT` slots of 32 KB, 2 MB, allocated when `FrameCache` is
built, before the loop runs, and never freed. It does not grow or shrink, so
the heap does not fragment and the free heap is the same for the whole run.
Compose writes into the slot's own `Bitmap`, with one scratch `Bitmap` for the
glyph, so a store allocates no `Bitmap` (the compose of a blinking key still
allocates two). The cache is RAM only: this module writes no file and no
byte of non-volatile memory, because flash has a limited number of erase
cycles (task 0044).

See tasks/ongoing/0061-cache-composed-glyph-frames-on-the-board.md.
"""

import displayio

import display_render

# docs/wire-protocol.md's "Store frame": slot numbers are 0 to SLOT_COUNT - 1.
# 64, not 128: the garbage collector scans every live heap block, about 1 ms per
# 32 KB, so 128 slots made one collection take 115 to 150 ms, longer than the
# 150 ms gap between HID reports (task 0063).
SLOT_COUNT = 64


class FrameCache:
    """The slots, and what each one holds.

    `view(slot)` is the byte view of a slot's `Bitmap`, the same object every
    time, so a caller can tell by identity whether a queued push reads a slot.
    A slot is empty until `store` composes into it.
    """

    def __init__(self, slot_count=SLOT_COUNT):
        self._bitmaps = [
            displayio.Bitmap(
                display_render.FRAME_WIDTH,
                display_render.FRAME_HEIGHT,
                display_render.FRAME_COLORS,
            )
            for _ in range(slot_count)
        ]
        self._views = [memoryview(bitmap).cast("B") for bitmap in self._bitmaps]
        self._scratch = displayio.Bitmap(
            display_render.FRAME_WIDTH,
            display_render.FRAME_HEIGHT,
            display_render.FRAME_COLORS,
        )
        self._colors = [0] * slot_count
        self._has_glyph = [False] * slot_count
        self._filled = [False] * slot_count
        self.slot_count = slot_count

    def store(self, slot, color, pixels):
        """Compose `pixels` over `color` into `slot`. `pixels` may be `None`.

        Raises `ValueError` for a slot this pool does not have. The caller
        makes sure that no push of the slot is queued or on the wire.
        """
        if slot >= self.slot_count:
            raise ValueError("slot {} is not below {}".format(slot, self.slot_count))
        # The slot is not valid while it is rewritten.
        self._filled[slot] = False
        display_render.compose_into(self._bitmaps[slot], self._scratch, color, pixels)
        self._colors[slot] = color
        self._has_glyph[slot] = pixels is not None
        self._filled[slot] = True

    def filled(self, slot):
        return slot < self.slot_count and self._filled[slot]

    def view(self, slot):
        return self._views[slot]

    def color(self, slot):
        return self._colors[slot]

    def has_glyph(self, slot):
        return self._has_glyph[slot]
