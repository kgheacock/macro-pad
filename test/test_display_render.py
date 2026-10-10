import bitmaptools

import tracer as tracer_module

# Imported flat, under the names the board uses. See conftest.py.
import display_render
from display_render import KeyState, render_key

PIXEL_COUNT = 128 * 128


class FakePanel:
    """Records each frame `render_key` pushes, both as bytes and as the
    object itself, so a test can tell a cached frame from a new one.
    """

    def __init__(self):
        self.frames = []
        self.frame_objects = []

    def submit(self, frame):
        self.frame_objects.append(frame)
        self.frames.append(bytes(memoryview(frame)))


def _be(color):
    """One pixel's big-endian bytes, as the panel reads them."""
    return bytes((color >> 8, color & 0xFF))


def _solid(color):
    return _be(color) * PIXEL_COUNT


def _glyph(color, transparent_corner=False):
    """A glyph of one opaque color, with pixel 0 optionally transparent."""
    pixels = bytearray(_solid(color))
    if transparent_corner:
        pixels[0:2] = _be(display_render.TRANSPARENT_PIXEL)
    return bytes(pixels)


def _pixel(frame, index):
    return (frame[2 * index] << 8) | frame[2 * index + 1]


class FakeTracer:
    """Records every `record` call's arguments, in call order — task 0042
    needs only the order and identity of the codes fired.
    """

    def __init__(self):
        self.records = []

    def record(self, code, key, payload, now_us):
        self.records.append((code, key, payload, now_us))


def setup_function():
    bitmaptools.calls.clear()


def test_color_fills_the_frame_big_endian():
    panel = FakePanel()

    render_key(panel, KeyState(emoji_id=0, color=0xF81F))

    assert panel.frames == [_solid(0xF81F)]


def test_color_with_a_zero_byte_is_not_swapped():
    panel = FakePanel()

    render_key(panel, KeyState(emoji_id=0, color=0x1200))

    assert panel.frames == [_solid(0x1200)]


def test_glyph_draws_over_the_color():
    panel = FakePanel()

    render_key(
        panel, KeyState(emoji_id=0xFE, color=0x07E0, pixels=_glyph(0xF800))
    )

    assert panel.frames == [_solid(0xF800)]


def test_transparent_pixel_shows_the_key_color():
    panel = FakePanel()

    render_key(
        panel,
        KeyState(
            emoji_id=0xFE,
            color=0x07E0,
            pixels=_glyph(0xF800, transparent_corner=True),
        ),
    )

    frame = panel.frames[0]
    assert _pixel(frame, 0) == 0x07E0
    assert _pixel(frame, 1) == 0xF800
    assert _pixel(frame, PIXEL_COUNT - 1) == 0xF800


def test_real_black_is_drawn_not_skipped():
    panel = FakePanel()

    render_key(
        panel, KeyState(emoji_id=0xFE, color=0x07E0, pixels=_glyph(0x0001))
    )

    assert _pixel(panel.frames[0], 0) == 0x0001


def test_blink_uses_cached_frame():
    """DoD-2: once a blinking key's frames are built, a change of slot pushes
    the other cached frame and calls no fill or blit.
    """
    panel = FakePanel()
    key = KeyState(
        emoji_id=0xFE,
        color=0x07E0,
        blink=True,
        pixels=_glyph(0xF800, transparent_corner=True),
    )
    render_key(panel, key, blink_on=True)  # builds both frames
    bitmaptools.calls.clear()

    render_key(panel, key, blink_on=False)
    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    assert bitmaptools.calls == []
    assert panel.frame_objects[1] is panel.frame_objects[3]
    assert panel.frame_objects[0] is panel.frame_objects[2]
    assert panel.frame_objects[0] is not panel.frame_objects[1]


def test_color_change_rebuilds_once():
    """DoD-2: a color change rebuilds the frames once, and a later redraw
    of the same state rebuilds nothing.
    """
    panel = FakePanel()
    key = KeyState(
        emoji_id=0xFE,
        color=0x07E0,
        pixels=_glyph(0xF800, transparent_corner=True),
    )
    render_key(panel, key)
    bitmaptools.calls.clear()

    key.color = 0x001F
    render_key(panel, key)

    names = [call[0] for call in bitmaptools.calls]
    assert names.count("fill_region") == 1
    assert names.count("blit") == 1
    assert _pixel(panel.frames[-1], 0) == 0x001F

    bitmaptools.calls.clear()
    render_key(panel, key)

    assert bitmaptools.calls == []
    assert panel.frames[-1] == panel.frames[-2]


def test_color_change_on_a_key_with_no_glyph_fills_once_and_blits_nothing():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x07E0)
    render_key(panel, key)
    bitmaptools.calls.clear()

    key.color = 0x001F
    render_key(panel, key)

    assert [call[0] for call in bitmaptools.calls] == ["fill_region"]


def test_new_glyph_rebuilds_the_frame():
    panel = FakePanel()
    key = KeyState(emoji_id=0xFE, color=0x07E0, pixels=_glyph(0xF800))
    render_key(panel, key)

    key.pixels = _glyph(0x001F)
    render_key(panel, key)

    assert panel.frames[-1] == _solid(0x001F)


def test_removing_the_glyph_rebuilds_the_frame_as_color_only():
    panel = FakePanel()
    key = KeyState(emoji_id=0xFE, color=0x07E0, pixels=_glyph(0xF800))
    render_key(panel, key)

    key.pixels = None
    key.emoji_id = 3
    render_key(panel, key)

    assert panel.frames[-1] == _solid(0x07E0)


def test_first_paint_of_a_blinking_key_shows_the_on_frame():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x07E0, blink=True)

    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    assert panel.frames == [_solid(0x07E0), _solid(0x0000)]


def test_blink_of_a_transparent_glyph_flips_the_background_not_the_glyph():
    panel = FakePanel()
    key = KeyState(
        emoji_id=0xFE,
        color=0x07E0,
        blink=True,
        pixels=_glyph(0xF800, transparent_corner=True),
    )

    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)
    render_key(panel, key, blink_on=True)

    on, off = panel.frames[0], panel.frames[1]
    assert _pixel(on, 0) == 0x07E0
    assert _pixel(off, 0) == 0x0000
    assert _pixel(on, 5) == _pixel(off, 5) == 0xF800
    assert panel.frames[2] == on


def test_blink_of_an_opaque_glyph_hides_the_whole_glyph():
    panel = FakePanel()
    key = KeyState(emoji_id=0xFE, color=0x07E0, blink=True, pixels=_glyph(0xF800))

    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    assert panel.frames == [_solid(0xF800), _solid(0x07E0)]


def test_blink_of_an_opaque_glyph_on_a_black_key_still_hides_the_glyph():
    panel = FakePanel()
    key = KeyState(emoji_id=0xFE, color=0x0000, blink=True, pixels=_glyph(0xF800))

    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    assert panel.frames == [_solid(0xF800), _solid(0x0000)]


def test_blink_of_a_key_with_no_glyph_flips_between_color_and_black():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x07E0, blink=True)

    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    assert panel.frames == [_solid(0x07E0), _solid(0x0000)]


def test_blink_turned_on_later_builds_the_off_frame_once():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x07E0)
    render_key(panel, key)
    bitmaptools.calls.clear()

    key.blink = True
    render_key(panel, key)
    assert [call[0] for call in bitmaptools.calls] == ["fill_region"]
    bitmaptools.calls.clear()

    render_key(panel, key, blink_on=False)
    render_key(panel, key, blink_on=True)
    assert bitmaptools.calls == []


def test_blink_turned_off_shows_the_on_frame_again():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x07E0, blink=True)
    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)  # off

    key.blink = False
    render_key(panel, key, blink_on=False)

    assert panel.frames[-1] == _solid(0x07E0)
    assert key._off_frame is None


def test_color_change_while_blinking_rebuilds_both_frames():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x07E0, blink=True)
    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    key.color = 0x001F
    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)

    assert panel.frames[-2] == _solid(0x001F)
    assert panel.frames[-1] == _solid(0x0000)


def test_has_transparent_pixel_finds_an_aligned_zero_pixel():
    assert display_render._has_transparent_pixel(_glyph(0xF800, transparent_corner=True))
    assert not display_render._has_transparent_pixel(_glyph(0xF800))


def test_has_transparent_pixel_ignores_zero_bytes_split_across_two_pixels():
    # Pixel 0x1200 then pixel 0x0034: the zero bytes sit side by side but
    # belong to two opaque pixels.
    pixels = (_be(0x1200) + _be(0x0034)) * (PIXEL_COUNT // 2)

    assert not display_render._has_transparent_pixel(pixels)


def test_has_transparent_pixel_finds_a_late_aligned_pixel_after_a_split_pair():
    pixels = bytearray((_be(0x1200) + _be(0x0034)) * (PIXEL_COUNT // 2))
    pixels[-2:] = _be(0x0000)

    assert display_render._has_transparent_pixel(bytes(pixels))


def test_swap16():
    assert display_render._swap16(0x1234) == 0x3412
    assert display_render._swap16(0xF800) == 0x00F8


def test_render_key_records_glyph_built():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x0000)
    tracer = FakeTracer()

    render_key(panel, key, tracer, key_index=3)

    codes = [code for code, _, _, _ in tracer.records]
    assert codes == [tracer_module.GLYPH_BUILT]
    assert all(key_index == 3 for _, key_index, _, _ in tracer.records)


def test_render_key_blink_only_redraw_skips_glyph_built():
    """A blink toggle composes nothing, so it must not record a second
    `GLYPH_BUILT` — only a rebuild that happened counts toward the
    glyph-build stage's duration.
    """
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x0000, blink=True)
    tracer = FakeTracer()

    render_key(panel, key, tracer, key_index=1)
    tracer.records.clear()
    render_key(panel, key, tracer, key_index=1)

    assert tracer.records == []


def test_render_key_records_nothing_without_a_tracer():
    panel = FakePanel()

    render_key(panel, KeyState(emoji_id=0, color=0x0000))

    assert len(panel.frames) == 1


def test_a_blinking_key_draws_the_frame_of_the_slot_it_is_given():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x001F, blink=True)

    render_key(panel, key, blink_on=True)
    render_key(panel, key, blink_on=False)
    key.color = 0xF800
    render_key(panel, key, blink_on=False)  # a state change in an "off" slot
    render_key(panel, key, blink_on=True)

    assert _pixel(panel.frames[2], 0) == 0x0000
    assert _pixel(panel.frames[3], 0) == 0xF800


def test_a_key_that_starts_to_blink_in_an_off_slot_draws_off_at_once():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x001F, blink=True)

    render_key(panel, key, blink_on=False)

    assert panel.frames == [_solid(0x0000)]


def test_a_steady_key_draws_on_in_any_slot():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x001F)

    render_key(panel, key, blink_on=False)

    assert panel.frames == [_solid(0x001F)]


def test_blink_is_due_when_the_shown_frame_is_not_the_slot_frame():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x001F, blink=True)
    render_key(panel, key, blink_on=True)

    assert display_render.blink_is_due(key, True) is False
    assert display_render.blink_is_due(key, False) is True


def test_a_steady_key_is_never_due_to_blink():
    panel = FakePanel()
    key = KeyState(emoji_id=0, color=0x001F)
    render_key(panel, key)

    assert display_render.blink_is_due(key, False) is False
    assert display_render.blink_is_due(key, True) is False
