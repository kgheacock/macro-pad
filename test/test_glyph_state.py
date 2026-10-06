import pytest

# Imported flat, under the names the board uses. See conftest.py.
import glyph_state
import wire


def test_encode_decode_round_trip_built_in():
    data = glyph_state.encode(color=0xF800, emoji_id=0xF3, blink=True)

    color, emoji_id, blink, pixels = glyph_state.decode(data)

    assert color == 0xF800
    assert emoji_id == 0xF3
    assert blink is True
    assert pixels is None


def test_encode_decode_round_trip_custom_glyph():
    pixels_in = bytes([0xAB]) * wire.CUSTOM_GLYPH_PIXELS_SIZE

    data = glyph_state.encode(
        color=0x001F,
        emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        blink=False,
        pixels=pixels_in,
    )
    color, emoji_id, blink, pixels_out = glyph_state.decode(data)

    assert color == 0x001F
    assert emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
    assert blink is False
    assert pixels_out == pixels_in


def test_encode_rejects_missing_pixels_for_sentinel():
    with pytest.raises(ValueError):
        glyph_state.encode(color=0, emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID, blink=False)


def test_encode_rejects_pixels_for_non_sentinel():
    pixels = bytes([0]) * wire.CUSTOM_GLYPH_PIXELS_SIZE
    with pytest.raises(ValueError):
        glyph_state.encode(color=0, emoji_id=0xF1, blink=False, pixels=pixels)


def test_decode_rejects_short_header():
    with pytest.raises(ValueError):
        glyph_state.decode(bytes((1, 2, 3)))


def test_decode_rejects_short_custom_glyph_record():
    truncated = glyph_state.encode(
        color=0,
        emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        blink=False,
        pixels=bytes([0]) * wire.CUSTOM_GLYPH_PIXELS_SIZE,
    )[:-1]

    with pytest.raises(ValueError):
        glyph_state.decode(truncated)


def _legacy_record(color, emoji_id, blink, pixels=None):
    """A record as `glyph_state.encode` wrote it before task 0043: a
    4-byte header with no format byte, then RGBA4444 pixels for a custom
    glyph.
    """
    header = bytes((color & 0xFF, color >> 8, emoji_id, 1 if blink else 0))
    return header + (pixels if pixels is not None else b"")


def test_encode_starts_with_the_format_byte():
    data = glyph_state.encode(color=0xF800, emoji_id=0xF3, blink=True)

    assert data == bytes((glyph_state.FORMAT_RGB565, 0x00, 0xF8, 0xF3, 1))


def test_decode_rejects_unknown_format():
    data = bytearray(glyph_state.encode(color=0xF800, emoji_id=0xF3, blink=True))
    data[0] = 0x7F

    with pytest.raises(ValueError):
        glyph_state.decode(bytes(data))


def test_legacy_record_is_color_only():
    """A legacy custom-glyph record holds RGBA4444 pixels. It loads as its
    color and blink, with no pixels and the power-on Emoji ID.
    """
    legacy = _legacy_record(
        color=0xF81F,
        emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        blink=True,
        pixels=bytes([0xAB]) * wire.CUSTOM_GLYPH_PIXELS_SIZE,
    )

    color, emoji_id, blink, pixels = glyph_state.decode(legacy)

    assert color == 0xF81F
    assert emoji_id == 0
    assert blink is True
    assert pixels is None


def test_legacy_built_in_record_still_loads():
    color, emoji_id, blink, pixels = glyph_state.decode(
        _legacy_record(color=0x07E0, emoji_id=0xF3, blink=False)
    )

    assert (color, emoji_id, blink, pixels) == (0x07E0, 0xF3, False, None)


def test_legacy_record_with_a_color_byte_equal_to_the_format_byte():
    legacy = _legacy_record(
        color=0x0001,
        emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        blink=False,
        pixels=bytes(wire.CUSTOM_GLYPH_PIXELS_SIZE),
    )

    assert glyph_state.decode(legacy)[0] == 0x0001


def test_current_custom_record_cut_short_by_one_byte_is_not_read_as_legacy():
    """It is as long as a legacy custom record, so only the sentinel's
    offset tells them apart.
    """
    truncated = glyph_state.encode(
        color=0x1234,
        emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        blink=False,
        pixels=bytes(wire.CUSTOM_GLYPH_PIXELS_SIZE),
    )[:-1]
    assert len(truncated) == 4 + wire.CUSTOM_GLYPH_PIXELS_SIZE

    with pytest.raises(ValueError):
        glyph_state.decode(truncated)


class FakeGlyphStorage:
    """In-memory stand-in for `glyph_state.FilesystemStorage`, so a test
    can inspect exactly what would have been written without touching a
    real filesystem.
    """

    def __init__(self):
        self._files = {}

    def read(self, key_index):
        return self._files.get(key_index)

    def write(self, key_index, data):
        self._files[key_index] = data


def test_fake_storage_round_trip():
    storage = FakeGlyphStorage()
    assert storage.read(0) is None

    data = glyph_state.encode(color=0x1234, emoji_id=0xF1, blink=True)
    storage.write(0, data)

    assert storage.read(0) == data


class FakeNvm:
    """`microcontroller.nvm`: 4096 erased bytes, and a count of the writes."""

    def __init__(self):
        self._bytes = bytearray(b"\xff" * 4096)
        self.writes = 0

    def __getitem__(self, index):
        return self._bytes[index]

    def __setitem__(self, index, value):
        self._bytes[index] = bytes(value)
        self.writes += 1


class FakePixelFiles:
    def __init__(self):
        self.files = {}
        self.fail = False

    def read(self, key_index):
        return self.files.get(key_index)

    def write(self, key_index, data):
        if self.fail:
            raise OSError("read-only filesystem")
        self.files[key_index] = data


def test_nvm_storage_reads_nothing_from_erased_nvm():
    storage = glyph_state.NvmStorage(nvm=FakeNvm(), pixel_files=FakePixelFiles())

    assert all(storage.read(key) is None for key in range(6))


def test_nvm_storage_restores_every_key_after_a_reboot():
    nvm = FakeNvm()
    storage = glyph_state.NvmStorage(nvm=nvm, pixel_files=FakePixelFiles())
    headers = {
        0: glyph_state.encode_header(0x001F, 0, False),
        3: glyph_state.encode_header(0xF800, 0xF2, True),
        5: glyph_state.encode_header(0x07E0, 7, False),
    }

    written = storage.write_many(headers, {})

    assert written == {0, 3, 5}
    assert nvm.writes == 1
    rebooted = glyph_state.NvmStorage(nvm=nvm, pixel_files=FakePixelFiles())
    assert glyph_state.decode(rebooted.read(0)) == (0x001F, 0, False, None)
    assert glyph_state.decode(rebooted.read(3)) == (0xF800, 0xF2, True, None)
    assert glyph_state.decode(rebooted.read(5)) == (0x07E0, 7, False, None)
    assert rebooted.read(1) is None


def test_nvm_storage_with_a_bad_magic_byte_reads_the_default():
    nvm = FakeNvm()
    storage = glyph_state.NvmStorage(nvm=nvm, pixel_files=FakePixelFiles())
    storage.write_many({2: glyph_state.encode_header(0x001F, 0, False)}, {})
    nvm._bytes[0] = glyph_state.NvmStorage.MAGIC ^ 0xFF

    rebooted = glyph_state.NvmStorage(nvm=nvm, pixel_files=FakePixelFiles())

    assert all(rebooted.read(key) is None for key in range(6))


def test_nvm_storage_keeps_a_custom_glyph_in_files():
    nvm = FakeNvm()
    files = FakePixelFiles()
    storage = glyph_state.NvmStorage(nvm=nvm, pixel_files=files)
    pixels = bytes(range(256)) * (wire.CUSTOM_GLYPH_PIXELS_SIZE // 256)
    header = glyph_state.encode_header(0x001F, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID, True)

    storage.write_many({1: header}, {1: pixels})

    assert files.files == {1: pixels}
    rebooted = glyph_state.NvmStorage(nvm=nvm, pixel_files=files)
    assert glyph_state.decode(rebooted.read(1)) == (
        0x001F,
        wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        True,
        pixels,
    )


def test_nvm_storage_with_a_missing_pixel_file_is_a_corrupt_record():
    nvm = FakeNvm()
    storage = glyph_state.NvmStorage(nvm=nvm, pixel_files=FakePixelFiles())
    header = glyph_state.encode_header(0x001F, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID, False)
    storage.write_many({1: header}, {})

    with pytest.raises(ValueError):
        glyph_state.decode(storage.read(1))


def test_nvm_storage_skips_the_header_of_a_key_whose_pixels_failed_to_write():
    nvm = FakeNvm()
    files = FakePixelFiles()
    files.fail = True
    storage = glyph_state.NvmStorage(nvm=nvm, pixel_files=files)
    custom = glyph_state.encode_header(0x001F, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID, False)
    steady = glyph_state.encode_header(0xF800, 0, False)

    written = storage.write_many(
        {1: custom, 2: steady}, {1: bytes(wire.CUSTOM_GLYPH_PIXELS_SIZE)}
    )

    assert written == {2}
    assert storage.read(1) is None
    assert storage.read(2) == steady
