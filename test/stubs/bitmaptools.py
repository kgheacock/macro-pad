"""Fake `bitmaptools` module. See ../README.md for how these stubs are used.

It implements the three calls `firmware/display_render.py` makes, on the
16-bit `displayio.Bitmap` stub. Every call is appended to `calls`, so a
test can tell a cached-frame push (no call) apart from a rebuild.
"""

import array

calls = []


def _pixel_offset(bitmap, x, y):
    return (y * bitmap.width + x) * 2


def fill_region(bitmap, x1, y1, x2, y2, value):
    """Fill the region from (x1, y1) up to, but not including, (x2, y2)."""
    calls.append(("fill_region", x1, y1, x2, y2, value))
    pixel = bytes((value & 0xFF, value >> 8))
    for y in range(y1, y2):
        start = _pixel_offset(bitmap, x1, y)
        bitmap[start : start + (x2 - x1) * 2] = pixel * (x2 - x1)


def arrayblit(bitmap, data, x1=0, y1=0, x2=None, y2=None, skip_index=None):
    """Copy a whole array of 16-bit values into `bitmap`.

    The real call reads `data` item by item, in the item size of its
    typecode, so this accepts only an `array.array("H")`.
    """
    calls.append(("arrayblit", len(data)))
    if not isinstance(data, array.array) or data.typecode != "H":
        raise TypeError("arrayblit needs an array of uint16")
    if len(data) != bitmap.width * bitmap.height:
        raise ValueError("array length does not match the bitmap")
    bitmap[:] = data.tobytes()


def blit(
    dest,
    source,
    x,
    y,
    *,
    x1=0,
    y1=0,
    x2=None,
    y2=None,
    skip_source_index=None,
    skip_dest_index=None,
):
    """Copy `source` onto `dest` at (x, y), skipping source pixels whose
    value is `skip_source_index`.
    """
    calls.append(("blit", x, y, skip_source_index))
    x2 = source.width if x2 is None else x2
    y2 = source.height if y2 is None else y2
    for sy in range(y1, y2):
        for sx in range(x1, x2):
            src = _pixel_offset(source, sx, sy)
            value = source[src] | (source[src + 1] << 8)
            if skip_source_index is not None and value == skip_source_index:
                continue
            dst = _pixel_offset(dest, x + sx - x1, y + sy - y1)
            dest[dst : dst + 2] = source[src : src + 2]
