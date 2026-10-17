import pytest

# Imported flat, under the names the board uses. See conftest.py.
import display_render
import frame_cache

PIXEL_COUNT = 128 * 128


def _be(value):
    return bytes((value >> 8, value & 0xFF))


def test_the_pool_has_128_slots_of_32_kb():
    cache = frame_cache.FrameCache()

    assert frame_cache.SLOT_COUNT == cache.slot_count == 128
    assert len(cache.view(0)) == 32768
    assert len(cache.view(127)) == 32768


def test_every_slot_is_empty_until_it_is_stored():
    cache = frame_cache.FrameCache(slot_count=4)

    assert [cache.filled(slot) for slot in range(6)] == [False] * 6
    cache.store(2, 0x1234, None)
    assert [cache.filled(slot) for slot in range(6)] == [False, False, True, False, False, False]


def test_a_stored_color_fills_the_slot_in_the_panels_byte_order():
    cache = frame_cache.FrameCache(slot_count=2)

    cache.store(1, 0xF81F, None)

    assert bytes(cache.view(1)) == _be(0xF81F) * PIXEL_COUNT
    assert cache.color(1) == 0xF81F
    assert not cache.has_glyph(1)


def test_a_stored_glyph_covers_the_color_except_its_transparent_pixels():
    cache = frame_cache.FrameCache(slot_count=2)
    pixels = bytearray(_be(0x07E0) * PIXEL_COUNT)
    pixels[0:2] = b"\x00\x00"

    cache.store(0, 0x001F, bytes(pixels))

    frame = bytes(cache.view(0))
    assert frame[0:2] == _be(0x001F)
    assert frame[2:4] == _be(0x07E0)
    assert cache.has_glyph(0)


def test_a_slot_keeps_its_view_object_when_it_is_stored_again():
    cache = frame_cache.FrameCache(slot_count=2)
    view = cache.view(0)

    cache.store(0, 0x1111, None)
    cache.store(0, 0x2222, None)

    assert cache.view(0) is view
    assert bytes(view) == _be(0x2222) * PIXEL_COUNT


def test_one_slot_does_not_change_another():
    cache = frame_cache.FrameCache(slot_count=3)

    cache.store(0, 0x1111, None)
    cache.store(1, 0x2222, None)

    assert bytes(cache.view(0)) == _be(0x1111) * PIXEL_COUNT
    assert bytes(cache.view(2)) == bytes(32768)


def test_a_slot_the_pool_does_not_have_raises():
    cache = frame_cache.FrameCache(slot_count=2)

    with pytest.raises(ValueError):
        cache.store(2, 0, None)
    assert not cache.filled(2)


def test_compose_into_matches_a_composed_frame():
    pixels = bytearray(_be(0x07E0) * PIXEL_COUNT)
    pixels[0:2] = b"\x00\x00"
    cache = frame_cache.FrameCache(slot_count=1)
    cache.store(0, 0x001F, bytes(pixels))

    composed = display_render._compose(0x001F, bytes(pixels))

    assert bytes(cache.view(0)) == bytes(composed)
