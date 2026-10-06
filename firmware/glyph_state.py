"""Persists each key's last render state — built-in glyph or custom
image, color, and blink — so it survives a power cycle with no driver
connected.

A key's 5-byte header (color, Emoji ID, blink) lives in the board's
`microcontroller.nvm`. One `nvm` write of all six headers takes about
50 ms, against 320 ms to 590 ms for a 5-byte file write, so `app.py` can
persist inside the gap between two blinks without freezing a blinking key
(task 0044). The 32,768 bytes of a custom glyph's pixels do not fit in the
4,096-byte `nvm`, so they stay one file per key under `glyph_state_files/`.

`nvm` survives a firmware reflash, unlike a file the reflash deletes. So
`make flash` no longer resets the state of a key. A magic byte at the start
of `nvm` rejects bytes that another layout left there. See
tasks/ongoing/0044-blink-independence-persist-off-blink-path.md for the
design decision, and tasks/ongoing/0030-custom-glyph-upload-and-persistence.md
for the earlier file-only design.

Imported flat (`import wire`), like every other module here — see
app.py's module docstring.
"""

import wire

# A record is a header, then, for a custom glyph, the glyph's pixels.
#
# Header: format (1 byte) + color (2 bytes) + emoji_id (1 byte) + blink (1
# byte). The format byte says how to read the pixels. Task 0043 changed the
# pixel format from little-endian RGBA4444 to big-endian RGB565, with 0x0000
# as the transparent value, and added the format byte at the same time.
#
# A legacy record has no format byte, so its header is 4 bytes: color,
# emoji_id, blink. A legacy record is 4 bytes, or 4 + 32,768 bytes with
# RGBA4444 pixels. A current record is at least 5 bytes, so a length alone
# tells the two apart.
FORMAT_RGB565 = 1

_HEADER_SIZE = 5
_LEGACY_HEADER_SIZE = 4


def encode_header(color, emoji_id, blink):
    """Pack the 5-byte header of one key's state: the part `NvmStorage`
    keeps in `nvm`.
    """
    return bytes(
        (FORMAT_RGB565, color & 0xFF, (color >> 8) & 0xFF, emoji_id, 1 if blink else 0)
    )


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
    buffer[0] = FORMAT_RGB565
    buffer[1] = color & 0xFF
    buffer[2] = (color >> 8) & 0xFF
    buffer[3] = emoji_id
    buffer[4] = 1 if blink else 0
    if is_custom:
        buffer.extend(pixels)
    return bytes(buffer)


def _is_legacy(data):
    """True when `data` is a record from before task 0043.

    A length alone is not enough for a custom glyph. A current custom
    record cut short by one byte, for example by a power loss during a
    write, is as long as a legacy custom record. The two differ in where
    the custom-glyph sentinel sits: a legacy header holds the Emoji ID at
    offset 2, a current one at offset 3. A legacy Blink flag is 0 or 1, so
    the sentinel cannot also sit at offset 3 of a legacy record.
    """
    if len(data) == _LEGACY_HEADER_SIZE:
        return True
    return (
        len(data) == _LEGACY_HEADER_SIZE + wire.CUSTOM_GLYPH_PIXELS_SIZE
        and data[2] == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
        and data[3] != wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
    )


def _decode_legacy(data):
    """Unpack a record from before task 0043 as color and blink only.

    A legacy custom glyph holds RGBA4444 pixels, which the board cannot
    show now and does not convert. The key keeps its color and blink, and
    drops to the power-on Emoji ID. The driver resends the glyph.
    """
    color = data[0] | (data[1] << 8)
    emoji_id = data[2]
    if emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID:
        emoji_id = 0
    return color, emoji_id, data[3] != 0, None


def decode(data):
    """Unpack bytes `encode` built back into `(color, emoji_id, blink,
    pixels)`. `pixels` is `None` unless `emoji_id` is the custom-glyph
    sentinel.

    A legacy record (see the format note above) loads as its color and
    blink alone, with no pixels.

    Raises `ValueError` when `data` is too short to hold a header, has a
    format byte this build does not know, or (for a custom-glyph record)
    is too short to hold the pixel buffer its `emoji_id` implies.
    """
    if _is_legacy(data):
        return _decode_legacy(data)

    if len(data) < _HEADER_SIZE:
        raise ValueError(
            "glyph state record is {} bytes, want at least {}".format(
                len(data), _HEADER_SIZE
            )
        )
    if data[0] != FORMAT_RGB565:
        raise ValueError("glyph state record has unknown format {}".format(data[0]))

    color = data[1] | (data[2] << 8)
    emoji_id = data[3]
    blink = data[4] != 0

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
    """Reads and writes one file per key under `glyph_state_files/`,
    relative to the CircuitPython filesystem's root — where `code.py`
    runs from.

    A missing file (no state saved yet, or the directory swept by
    `make flash`) is not an error: `read` returns `None`, and `MacroPad`
    falls back to its power-on defaults.
    """

    # Deliberately not "glyph_state" — that name collides with this
    # module's own filename at the filesystem root. The first successful
    # write creates the directory, and from then on CircuitPython's
    # import resolves `glyph_state` to that empty directory instead of
    # `glyph_state.py`, raising `AttributeError: 'module' object has no
    # attribute 'FilesystemStorage'` on every subsequent boot. Confirmed
    # live during task 0031's key-0 bring-up.
    _DIR = "glyph_state_files"

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


class NvmStorage:
    """Keeps every key's header in `microcontroller.nvm`, and a custom
    glyph's pixels in files.

    Layout of `nvm`: one magic byte, then one 5-byte header for each key,
    key 0 first, so key `k`'s header starts at offset `1 + 5 * k`. A header
    is what `glyph_state.encode_header` builds. A slot whose format byte is
    0 holds no state. A first byte other than `MAGIC` means `nvm` holds
    bytes from another layout, so every key reads as the power-on default.

    `write_many` rewrites the whole header block in one `nvm` write, however
    many keys changed. The time of an `nvm` write does not depend on its
    length, so one write costs the same for 5 bytes or 31.

    `nvm` and `pixel_files` are injected so a test can use fakes. `pixel_files`
    needs `read(key_index)` and `write(key_index, data)`, like
    `FilesystemStorage`, which is the default.
    """

    MAGIC = 0xA5

    def __init__(self, nvm=None, pixel_files=None, key_count=6):
        if nvm is None:
            import microcontroller

            nvm = microcontroller.nvm
        self._nvm = nvm
        self._pixel_files = pixel_files if pixel_files is not None else FilesystemStorage()
        self._key_count = key_count
        self._size = 1 + _HEADER_SIZE * key_count

        # A copy of the header block, so a write of one key's header keeps
        # the other keys' bytes without reading `nvm` again.
        if nvm[0] == self.MAGIC:
            self._image = bytearray(nvm[0 : self._size])
        else:
            self._image = bytearray(self._size)
            self._image[0] = self.MAGIC

    def _slot(self, key_index):
        start = 1 + _HEADER_SIZE * key_index
        return start, start + _HEADER_SIZE

    def read(self, key_index):
        """Return the record `decode` reads for `key_index`, or `None` when
        no state was saved for it.

        A custom glyph's record is its header followed by its pixels. When
        the pixel file is missing or has the wrong length, the record is
        the header alone, which `decode` rejects as a custom record with no
        pixels.
        """
        start, end = self._slot(key_index)
        header = bytes(self._image[start:end])
        if header[0] == 0:
            return None
        if header[3] != wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID:
            return header
        pixels = self._pixel_files.read(key_index)
        if pixels is None or len(pixels) != wire.CUSTOM_GLYPH_PIXELS_SIZE:
            return header
        return header + pixels

    def write_many(self, headers, pixels):
        """Persist `headers`, a dict of key index to 5-byte header, in one
        `nvm` write, and `pixels`, a dict of key index to glyph pixels, to
        the pixel files. Return the set of key indexes now persisted.

        The pixel files go first. A power loss between the two leaves the
        old header beside the new pixels, which shows the new image under
        the old color — not a corrupt key. A key whose pixel file fails to
        write keeps its old header and is left out of the result, so the
        caller does not record it as persisted.
        """
        written = set(headers)
        for key_index in sorted(pixels):
            try:
                self._pixel_files.write(key_index, pixels[key_index])
            except OSError:
                written.discard(key_index)

        for key_index in written:
            start, end = self._slot(key_index)
            self._image[start:end] = headers[key_index]
        if written:
            self._nvm[0 : self._size] = self._image
        return written
