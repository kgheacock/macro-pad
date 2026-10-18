"""A fixed pool of composed frames, in RAM only (task 0061).

The host sends a Store frame message once for each picture and color, and the
board composes it into a slot. A later Show frames message names slots, and a
layer switch then sends no image and composes nothing.

The pool is `SLOT_COUNT` slots of 32 KB, 2 MB, allocated when `FrameCache` is
built, before the loop runs, and never freed. It does not grow or shrink, so
the heap does not fragment and the free heap is the same for the whole run.
Compose writes into the slot's own `Bitmap`, with one scratch `Bitmap` for the
glyph, so a store allocates no `Bitmap` (the compose of a blinking key still
allocates two). With the `glyphcore` module (task 0059) the slots are in its
memory, outside the heap: compose writes into one `Bitmap`, and a 32 KB copy
puts the frame in the slot. The cache is RAM only: this module writes no file and no
byte of non-volatile memory, because flash has a limited number of erase
cycles (task 0044).

Task 0062 adds the count pip. The last `PIP_BUFFERS` buffers of the pool are
pip buffers, two for each key, and the host numbers only the first `SLOT_COUNT`
slots. A Show frames with pips message copies a slot into a pip buffer and
stamps the pip on the copy, so a slot never holds a pip and one icon on one
color is one slot, whatever its count.

See tasks/complete/0061-cache-composed-glyph-frames-on-the-board.md and
tasks/ongoing/0062-red-circle-count-pip-with-a-real-font.md.
"""

import array

import bitmaptools
import displayio

import display_render

# The module of the second core (task 0059). It holds the slots in memory the
# garbage collector does not scan; without it the slots are `Bitmap`s on the
# heap.
try:
    import glyphcore
except ImportError:
    glyphcore = None

_UNSET = object()

# docs/wire-protocol.md's "Store frame": slot numbers are 0 to SLOT_COUNT - 1.
# The pool is 64 buffers, not 128: the garbage collector scans every live heap
# block, about 1 ms per 32 KB, so 128 slots made one collection take 115 to
# 150 ms, longer than the 150 ms gap between HID reports (task 0063). Twelve of
# the 64 are pip buffers (task 0062), so the host numbers 52.
POOL_SIZE = 64
PIP_BUFFERS = 12
SLOT_COUNT = POOL_SIZE - PIP_BUFFERS


class FrameCache:
    """The slots, and what each one holds.

    `view(slot)` is the byte view of a slot's `Bitmap`, the same object every
    time, so a caller can tell by identity whether a queued push reads a slot.
    A slot is empty until `store` composes into it.
    """

    def __init__(self, slot_count=SLOT_COUNT, core=_UNSET, pip_buffers=PIP_BUFFERS):
        core = glyphcore if core is _UNSET else core
        pool = slot_count + pip_buffers
        if core is not None:
            core.alloc(pool)
            self._compose_bitmap = displayio.Bitmap(
                display_render.FRAME_WIDTH,
                display_render.FRAME_HEIGHT,
                display_render.FRAME_COLORS,
            )
            self._compose_view = memoryview(self._compose_bitmap).cast("B")
            self._bitmaps = None
            self._views = [core.slot_view(slot) for slot in range(pool)]
        else:
            self._compose_bitmap = None
            self._bitmaps = [
                displayio.Bitmap(
                    display_render.FRAME_WIDTH,
                    display_render.FRAME_HEIGHT,
                    display_render.FRAME_COLORS,
                )
                for _ in range(pool)
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
        self.pip_buffers = pip_buffers
        # The pip sprites (task 0062): the sheet's bitmap, allocated here so the
        # heap is the same for the whole run, and the key color of each disc.
        # `None` until a Store pip sheet message arrives.
        self._sheet = displayio.Bitmap(
            display_render.PIP_SHEET_WIDTH,
            display_render.PIP_SHEET_HEIGHT,
            display_render.FRAME_COLORS,
        )
        self._sheet_colors = None

    def store(self, slot, color, pixels):
        """Compose `pixels` over `color` into `slot`. `pixels` may be `None`.

        Raises `ValueError` for a slot this pool does not have. The caller
        makes sure that no push of the slot is queued or on the wire.
        """
        if slot >= self.slot_count:
            raise ValueError("slot {} is not below {}".format(slot, self.slot_count))
        # The slot is not valid while it is rewritten.
        self._filled[slot] = False
        if self._bitmaps is None:
            display_render.compose_into(
                self._compose_bitmap, self._scratch, color, pixels
            )
            self._views[slot][:] = self._compose_view
        else:
            display_render.compose_into(
                self._bitmaps[slot], self._scratch, color, pixels
            )
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

    def set_sheet(self, colors, pixels):
        """Keep the pip sprites. `pixels` is the sheet, 128x128 items that this
        reads as 256x64, big-endian RGB565 as in a glyph. `colors[i]` is the key
        color that disc `i` is blended against.
        """
        data = pixels if isinstance(pixels, memoryview) else array.array("H", pixels)
        bitmaptools.arrayblit(self._sheet, data)
        self._sheet_colors = list(colors)

    def has_sheet(self):
        return self._sheet_colors is not None

    def pip_buffer(self, key, which):
        """The pool index of buffer `which` (0 or 1) of key `key`, or `None` when
        this pool has no pip buffer for the key. `view` takes it like a slot.
        """
        index = 2 * key + which
        if self.pip_buffers <= index:
            return None
        return self.slot_count + index

    def stamp(self, buffer, slot, count):
        """Copy the frame in `slot` into pip buffer `buffer` (from `pip_buffer`)
        and stamp a pip with `count` on the copy. The slot is not changed. The
        disc is the one for the slot's color, or the first when the sheet has
        none for it. The caller makes sure that no push of the buffer is queued
        or on the wire.
        """
        colors = self._sheet_colors
        color = self._colors[slot]
        disc = colors.index(color) if color in colors else 0
        if self._bitmaps is None:
            # The slots are outside the heap, so compose into the one bitmap.
            self._compose_view[:] = self._views[slot]
            display_render.stamp_pip(self._compose_bitmap, self._sheet, disc, count)
            self._views[buffer][:] = self._compose_view
        else:
            self._views[buffer][:] = self._views[slot]
            display_render.stamp_pip(self._bitmaps[buffer], self._sheet, disc, count)
