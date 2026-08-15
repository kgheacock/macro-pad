"""Tests for firmware/glyphs.py. See
tasks/ongoing/0023-glyph-table-and-digits.md for the design decision.
"""

from pathlib import Path

from PIL import Image

from firmware import glyphs

GLYPHS_DIR = Path(__file__).parent.parent / "hardware" / "glyphs"

FOREGROUND = 0xFFFFFF
BACKGROUND = 0x000000


def _source_pixels(filename):
    image = Image.open(GLYPHS_DIR / filename).convert("1")
    packed = image.tobytes()
    width, height = image.size
    bytes_per_row = (width + 7) // 8
    pixels = []
    for y in range(height):
        row_offset = y * bytes_per_row
        for x in range(width):
            byte = packed[row_offset + x // 8]
            pixels.append((byte >> (7 - (x % 8))) & 1)
    return pixels


def _bitmap_pixels(bitmap):
    return [bitmap[i] for i in range(bitmap.width * bitmap.height)]


def test_digit_matches_source():
    tile_grid = glyphs.lookup(0xF3, foreground=FOREGROUND, background=BACKGROUND)

    assert _bitmap_pixels(tile_grid.bitmap) == _source_pixels("3.png")


def test_unknown_id_returns_placeholder():
    placeholder = glyphs.lookup(
        glyphs.PLACEHOLDER_ID, foreground=FOREGROUND, background=BACKGROUND
    )

    result = glyphs.lookup(0x77, foreground=FOREGROUND, background=BACKGROUND)

    assert result.bitmap is placeholder.bitmap


def test_lookup_caches_bitmap():
    first = glyphs.lookup(0xF1, foreground=FOREGROUND, background=BACKGROUND)
    second = glyphs.lookup(0xF1, foreground=BACKGROUND, background=FOREGROUND)

    assert first.bitmap is second.bitmap
