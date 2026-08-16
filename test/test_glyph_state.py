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
