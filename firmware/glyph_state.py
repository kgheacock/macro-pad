"""Persists each key's last render state — built-in glyph or custom
image, color, and blink — so it survives a power cycle with no driver
connected.

A firmware reflash (`make flash`) resets this by design: `glyph_state/`
lives outside the source tree `make flash`'s `rsync --delete` syncs from,
so that command wipes it on every run, with no logic in this module or
the Makefile aware of reflashing at all. See
tasks/ongoing/0030-custom-glyph-upload-and-persistence.md for the design
decision.

Imported flat (`import wire`), like every other module here — see
app.py's module docstring.
"""

import wire

_HEADER_SIZE = 4  # color (2 bytes) + emoji_id (1 byte) + blink (1 byte)


def encode(color, emoji_id, blink, pixels=None):
    """Pack one key's state into the bytes its storage file holds.

    `pixels` must be `wire.CUSTOM_GLYPH_PIXELS_SIZE` bytes when
    `emoji_id` is `wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID`, and must be
    `None` otherwise — `emoji_id` is what tells `decode` whether to
    expect a trailing pixel buffer, so the two must agree here too.
    """
    is_custom = emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
    if is_custom and (pixels is None or len(pixels) != wire.CUSTOM_GLYPH_PIXELS_SIZE):
        raise ValueError(
            "custom glyph state needs {} bytes of pixels, got {}".format(
                wire.CUSTOM_GLYPH_PIXELS_SIZE, 0 if pixels is None else len(pixels)
            )
        )
    if not is_custom and pixels is not None:
        raise ValueError("pixels given for a non-custom emoji_id {}".format(emoji_id))

    buffer = bytearray(_HEADER_SIZE)
    buffer[0] = color & 0xFF
    buffer[1] = (color >> 8) & 0xFF
    buffer[2] = emoji_id
    buffer[3] = 1 if blink else 0
    if is_custom:
        buffer.extend(pixels)
    return bytes(buffer)


def decode(data):
    """Unpack bytes `encode` built back into `(color, emoji_id, blink,
    pixels)`. `pixels` is `None` unless `emoji_id` is the custom-glyph
    sentinel.

    Raises `ValueError` when `data` is too short to hold a header, or
    (for a custom-glyph record) too short to hold the pixel buffer its
    `emoji_id` implies.
    """
    if len(data) < _HEADER_SIZE:
        raise ValueError(
            "glyph state record is {} bytes, want at least {}".format(
                len(data), _HEADER_SIZE
            )
        )

    color = data[0] | (data[1] << 8)
    emoji_id = data[2]
    blink = data[3] != 0

    pixels = None
    if emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID:
        want = _HEADER_SIZE + wire.CUSTOM_GLYPH_PIXELS_SIZE
        if len(data) != want:
            raise ValueError(
                "custom glyph state record is {} bytes, want {}".format(
                    len(data), want
                )
            )
        pixels = bytes(data[_HEADER_SIZE:want])

    return color, emoji_id, blink, pixels


class FilesystemStorage:
    """Reads and writes one file per key under `glyph_state/`, relative
    to the CircuitPython filesystem's root — where `code.py` runs from.

    A missing file (no state saved yet, or the directory swept by
    `make flash`) is not an error: `read` returns `None`, and `MacroPad`
    falls back to its power-on defaults.
    """

    _DIR = "glyph_state"

    def _path(self, key_index):
        return "{}/{}.bin".format(self._DIR, key_index)

    def read(self, key_index):
        try:
            with open(self._path(key_index), "rb") as f:
                return f.read()
        except OSError:
            return None

    def write(self, key_index, data):
        try:
            import os

            os.mkdir(self._DIR)
        except OSError:
            pass  # the directory already exists
        with open(self._path(key_index), "wb") as f:
            f.write(data)
