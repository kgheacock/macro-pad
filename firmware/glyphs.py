"""Renders a plain background-colored tile for any emoji ID.

The built-in digit glyph table that once lived here, and the placeholder
box that stood in for an unknown ID, were both removed once the driver
could already render any character to a key; see
tasks/complete/0039-remove-built-in-firmware-glyph-table.md. See
tasks/complete/0023-glyph-table-and-digits.md for the original glyph
table's design decision.
"""

import displayio

PLACEHOLDER_ID = 0x00

_WIDTH = 128
_HEIGHT = 128

_bitmap = None


def _blank_bitmap():
    global _bitmap
    if _bitmap is None:
        _bitmap = displayio.Bitmap(_WIDTH, _HEIGHT, 2)
    return _bitmap


def lookup(emoji_id, foreground, background):
    """Return a TileGrid filled with `background`, ignoring `emoji_id`.

    Every ID draws a plain tile — no built-in glyph remains. Rendering an
    emoji character or an arbitrary image now happens on the driver side;
    see tasks/complete/0039-remove-built-in-firmware-glyph-table.md.
    """
    palette = displayio.Palette(2)
    palette[0] = background
    palette[1] = foreground

    return displayio.TileGrid(_blank_bitmap(), pixel_shader=palette)
