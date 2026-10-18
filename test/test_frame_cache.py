import pytest

# Imported flat, under the names the board uses. See conftest.py.
import display_render
import frame_cache

PIXEL_COUNT = 128 * 128


def _be(value):
    return bytes((value >> 8, value & 0xFF))


def test_the_pool_has_64_slots_of_32_kb():
    cache = frame_cache.FrameCache()

    assert frame_cache.SLOT_COUNT == cache.slot_count == 64
    assert len(cache.view(0)) == 32768
    assert len(cache.view(63)) == 32768


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


# --- The pool in the memory of the glyphcore module (task 0059) ---------------

from fake_glyphcore import FakeGlyphcore  # noqa: E402


def test_with_the_module_the_slots_are_in_its_memory_not_bitmaps():
    core = FakeGlyphcore()

    cache = frame_cache.FrameCache(slot_count=8, core=core)

    assert len(core.slots) == 8
    assert cache.view(3).obj is core.slots[3]
    assert cache._bitmaps is None


def test_a_stored_frame_is_copied_into_the_slot_in_the_panels_byte_order():
    core = FakeGlyphcore()
    cache = frame_cache.FrameCache(slot_count=4, core=core)
    pixels = bytearray(_be(0x07E0) * PIXEL_COUNT)
    pixels[0:2] = b"\x00\x00"

    cache.store(2, 0x001F, bytes(pixels))

    frame = bytes(core.slots[2])
    assert frame[0:2] == _be(0x001F)
    assert frame[2:4] == _be(0x07E0)
    assert cache.filled(2) and cache.has_glyph(2) and cache.color(2) == 0x001F
    assert bytes(core.slots[1]) == bytes(32768)  # no other slot changed


def test_a_glyph_view_from_core_1_composes_like_bytes():
    core = FakeGlyphcore()
    cache = frame_cache.FrameCache(slot_count=2, core=core)
    pixels = bytearray(_be(0x07E0) * PIXEL_COUNT)
    pixels[0:2] = b"\x00\x00"
    core.glyphs[0][:] = pixels

    cache.store(0, 0x001F, core.glyph_view(0))
    cache.store(1, 0x001F, bytes(pixels))

    assert bytes(core.slots[0]) == bytes(core.slots[1])


def test_without_the_module_the_pool_stays_on_the_heap():
    cache = frame_cache.FrameCache(slot_count=2, core=None)

    assert cache._bitmaps is not None
    assert len(cache.view(0)) == 32768
