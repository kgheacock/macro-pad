"""Tests for firmware/glyphs.py. See
tasks/complete/0023-glyph-table-and-digits.md for the placeholder's
original design decision.
"""

from firmware import glyphs

FOREGROUND = 0xFFFFFF
BACKGROUND = 0x000000


def test_unknown_id_returns_placeholder():
    placeholder = glyphs.lookup(
        glyphs.PLACEHOLDER_ID, foreground=FOREGROUND, background=BACKGROUND
    )

    result = glyphs.lookup(0x77, foreground=FOREGROUND, background=BACKGROUND)

    assert result.bitmap is placeholder.bitmap
