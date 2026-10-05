import displayio

from firmware import glyphs
from firmware import tracer as tracer_module
from firmware.display_render import (
    KeyState,
    _rgb565_to_rgb888,
    raw_bitmap_tile_grid,
    render_key,
)


class FakeDisplay:
    """Records what a real `adafruit_st7735r.ST7735R` would have been
    told to show, via the same `root_group` assignment and `refresh`
    call it exposes.
    """

    def __init__(self) -> None:
        self.shown_groups = []
        self.refresh_count = 0

    @property
    def root_group(self) -> displayio.Group:
        return self.shown_groups[-1]

    @root_group.setter
    def root_group(self, group: displayio.Group) -> None:
        self.shown_groups.append(group)

    def refresh(self, **kwargs) -> bool:
        self.refresh_count += 1
        return True


def _emoji_tile_grid(bitmap: displayio.Bitmap) -> displayio.TileGrid:
    palette = displayio.Palette(1)
    palette[0] = 0xFFFFFF
    return displayio.TileGrid(bitmap, pixel_shader=palette)


def _stub_emoji_lookup(emoji_id: str, color: int) -> displayio.TileGrid:
    return _emoji_tile_grid(displayio.Bitmap(1, 1, 1))


class FakeTracer:
    """Records every `record` call's arguments, in call order — task 0042
    needs only the order and identity of the codes fired, not a real ring
    buffer.
    """

    def __init__(self) -> None:
        self.records = []

    def record(self, code, key, payload, now_us) -> None:
        self.records.append((code, key, payload, now_us))


def test_fill_color():
    display = FakeDisplay()
    # 0xF81F is RGB565 magenta (R=0x1F, G=0, B=0x1F) — render_key must
    # widen it to the 24-bit 0xFF00FF displayio.Palette expects, not
    # assign the 16-bit value directly. See _rgb565_to_rgb888.
    key_state = KeyState(emoji_id="smile", color=0xF81F)

    render_key(display, key_state, _stub_emoji_lookup)

    background = list(display.shown_groups[-1])[0]
    assert background.pixel_shader[0] == 0xFF00FF
    assert display.refresh_count == 1


def test_render_key_records_glyph_built_then_refresh_done():
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0x0000)
    tracer = FakeTracer()

    render_key(display, key_state, _stub_emoji_lookup, tracer, key_index=3)

    codes = [code for code, _, _, _ in tracer.records]
    assert codes == [tracer_module.GLYPH_BUILT, tracer_module.REFRESH_DONE]
    assert all(key == 3 for _, key, _, _ in tracer.records)


def test_render_key_blink_only_update_skips_glyph_built():
    """An update that only toggles blink visibility never calls
    `_build_glyph` again, so it must not record a second `GLYPH_BUILT` —
    only the rebuild that actually happened should count toward the
    glyph-build stage's duration.
    """
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0x0000, blink=True)
    tracer = FakeTracer()

    render_key(display, key_state, _stub_emoji_lookup, tracer, key_index=1)
    tracer.records.clear()
    render_key(display, key_state, _stub_emoji_lookup, tracer, key_index=1)

    codes = [code for code, _, _, _ in tracer.records]
    assert codes == [tracer_module.REFRESH_DONE]


def test_rgb565_to_rgb888():
    assert _rgb565_to_rgb888(0x0000) == 0x000000
    assert _rgb565_to_rgb888(0xFFFF) == 0xFFFFFF
    assert _rgb565_to_rgb888(0xF800) == 0xFF0000  # pure red
    assert _rgb565_to_rgb888(0x07E0) == 0x00FF00  # pure green
    assert _rgb565_to_rgb888(0x001F) == 0x0000FF  # pure blue
    # Confirmed live during task 0031: sending RGB565 red (0xF800) with
    # no conversion rendered as green, since a bare int assigned to
    # displayio.Palette is read as 0xRRGGBB and RGB565's max (0xFFFF)
    # always leaves that top byte zero.
    assert _rgb565_to_rgb888(0xFEA0) == 0xFFD600  # api.Conn.SetState's amber


def test_emoji_bitmap():
    display = FakeDisplay()
    expected_bitmap = displayio.Bitmap(8, 8, 2)
    key_state = KeyState(emoji_id="smile", color=0x000000)

    render_key(
        display, key_state, lambda emoji_id, color: _emoji_tile_grid(expected_bitmap)
    )

    emoji_layer = list(display.shown_groups[-1])[1]
    assert emoji_layer.bitmap is expected_bitmap


def test_renders_digit():
    display = FakeDisplay()
    key_state = KeyState(emoji_id=0xF3, color=0x000000)

    def emoji_lookup(emoji_id, color):
        return glyphs.lookup(emoji_id, foreground=0xFFFFFF, background=color)

    render_key(display, key_state, emoji_lookup)

    emoji_layer = list(display.shown_groups[-1])[1]
    expected = glyphs.lookup(0xF3, foreground=0xFFFFFF, background=0x000000)
    assert emoji_layer.bitmap is expected.bitmap


def test_emoji_lookup_receives_key_color():
    """`code.py`'s real emoji_lookup backgrounds a glyph to match the
    key's own color (task 0023's Open questions) — render_key must pass
    key_state.color through, not just emoji_id.
    """
    display = FakeDisplay()
    key_state = KeyState(emoji_id=0xF3, color=0x07E0)
    received = []

    def emoji_lookup(emoji_id, color):
        received.append((emoji_id, color))
        return _emoji_tile_grid(displayio.Bitmap(1, 1, 1))

    render_key(display, key_state, emoji_lookup)

    assert received == [(0xF3, 0x07E0)]


def _opaque_pixel(r4, g4, b4):
    """One little-endian RGBA4444 pixel: alpha nibble 0xF (opaque), the
    given 4-bit red, green, and blue channels.
    """
    hi = 0xF0 | r4
    lo = (g4 << 4) | b4
    return bytes((lo, hi))


def _transparent_pixel():
    """One little-endian RGBA4444 pixel with alpha nibble 0 — see
    DecodePNGToRGBA4444 in driver/transport/glyph.go for the bit layout.
    """
    return bytes((0x00, 0x00))


def _solid_opaque_pixels(r4, g4, b4):
    return _opaque_pixel(r4, g4, b4) * (128 * 128)


def _expected_rgb565(r4, g4, b4):
    """The RGB565 value `_opaque_bitmap_tile_grid` widens an opaque
    RGBA4444 pixel's channels to.
    """
    r5 = (r4 * 31) // 15
    g6 = (g4 * 63) // 15
    b5 = (b4 * 31) // 15
    return (r5 << 11) | (g6 << 5) | b5


def test_raw_bitmap_tile_grid_unpacks_pixels():
    pixels = _solid_opaque_pixels(0xF, 0x0, 0xF)  # opaque magenta

    tile_grid = raw_bitmap_tile_grid(pixels)

    assert tile_grid.bitmap.width == 128
    assert tile_grid.bitmap.height == 128
    assert tile_grid.bitmap[0] == _expected_rgb565(0xF, 0x0, 0xF)
    assert tile_grid.bitmap[128 * 128 - 1] == _expected_rgb565(0xF, 0x0, 0xF)
    assert tile_grid.pixel_shader.input_colorspace == displayio.Colorspace.RGB565


def test_render_key_prefers_pixels_over_emoji_lookup():
    display = FakeDisplay()
    pixels = _solid_opaque_pixels(0x0, 0xF, 0x0)  # opaque green
    key_state = KeyState(emoji_id=0xF3, color=0x000000, pixels=pixels)

    def failing_emoji_lookup(emoji_id, color):
        raise AssertionError("emoji_lookup called despite key_state.pixels being set")

    render_key(display, key_state, failing_emoji_lookup)

    image_layer = list(display.shown_groups[-1])[1]
    assert image_layer.bitmap[0] == _expected_rgb565(0x0, 0xF, 0x0)


def test_first_render_builds_scene_later_renders_reuse_it():
    """Task 0033: `render_key` must build a key's `Group`, background
    `Palette`, and glyph `TileGrid` only on the first call. Every later
    call has to mutate those same objects — a fresh object graph on every
    call is exactly what stopped displayio from shrinking `refresh()` to
    the changed region.
    """
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0x000000)

    render_key(display, key_state, _stub_emoji_lookup)
    group = display.shown_groups[-1]
    background = list(group)[0]
    glyph = list(group)[1]

    render_key(display, key_state, _stub_emoji_lookup)

    assert display.shown_groups[-1] is group
    assert list(group)[0] is background
    assert list(group)[1] is glyph


def test_blink_toggle_flips_hidden_without_rebuilding():
    """A blink-only frame — no color, emoji, or pixels change — must
    leave the `Group` at 2 children and toggle only the glyph
    `TileGrid`'s `hidden` flag, not add or remove layers. Rebuilding
    anything here is exactly the full-panel-redraw cost this task exists
    to avoid.
    """
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0x000000, blink=True)

    render_key(display, key_state, _stub_emoji_lookup)
    group = display.shown_groups[-1]
    glyph = list(group)[1]
    assert len(group) == 2
    assert glyph.hidden is True  # emoji hidden this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert list(group)[1] is glyph
    assert glyph.hidden is False  # emoji visible this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert list(group)[1] is glyph
    assert glyph.hidden is True  # hidden again


def test_color_only_change_mutates_background_palette_in_place():
    """A color change on a key showing a custom image (`pixels` set) must
    rewrite the existing background `Palette` in place and leave the
    glyph `TileGrid` untouched — a custom image ignores `key_state.color`
    entirely, so nothing about it needs to change.
    """
    display = FakeDisplay()
    pixels = _solid_opaque_pixels(0x0, 0xF, 0x0)  # opaque green
    key_state = KeyState(emoji_id=0xF3, color=0x0000, pixels=pixels)

    def failing_emoji_lookup(emoji_id, color):
        raise AssertionError("emoji_lookup called despite key_state.pixels being set")

    render_key(display, key_state, failing_emoji_lookup)
    group = display.shown_groups[-1]
    background = list(group)[0]
    glyph = list(group)[1]

    key_state.color = 0xF81F
    render_key(display, key_state, failing_emoji_lookup)

    assert list(group)[0] is background
    assert list(group)[1] is glyph
    assert background.pixel_shader[0] == _rgb565_to_rgb888(0xF81F)


def test_color_change_in_emoji_mode_also_refreshes_glyph():
    """A built-in emoji's own bitmap bakes in `key_state.color` as its
    background (task 0023's Open questions). A color-only change must
    still rebuild the glyph `TileGrid` through `emoji_lookup`, not just
    the background layer, or the glyph's background goes stale.
    """
    display = FakeDisplay()
    key_state = KeyState(emoji_id=0xF3, color=0x0000)
    received = []

    def emoji_lookup(emoji_id, color):
        received.append((emoji_id, color))
        return _emoji_tile_grid(displayio.Bitmap(1, 1, 1))

    render_key(display, key_state, emoji_lookup)
    group = display.shown_groups[-1]
    old_glyph = list(group)[1]

    key_state.color = 0x07E0
    render_key(display, key_state, emoji_lookup)

    assert received == [(0xF3, 0x0000), (0xF3, 0x07E0)]
    assert list(group)[1] is not old_glyph


def test_glyph_change_replaces_glyph_tile_grid():
    """Changing `emoji_id` must swap in a fresh glyph `TileGrid` — built
    through `emoji_lookup` again — without rebuilding the `Group` or the
    background layer.
    """
    display = FakeDisplay()
    key_state = KeyState(emoji_id=0xF3, color=0x000000)

    def emoji_lookup(emoji_id, color):
        return glyphs.lookup(emoji_id, foreground=0xFFFFFF, background=color)

    render_key(display, key_state, emoji_lookup)
    group = display.shown_groups[-1]
    background = list(group)[0]
    old_glyph = list(group)[1]

    key_state.emoji_id = 0xA0
    render_key(display, key_state, emoji_lookup)

    expected = glyphs.lookup(0xA0, foreground=0xFFFFFF, background=0x000000)
    new_glyph = list(group)[1]
    assert display.shown_groups[-1] is group
    assert list(group)[0] is background
    assert new_glyph is not old_glyph
    assert new_glyph.bitmap is expected.bitmap


def test_pixels_change_replaces_glyph_tile_grid():
    """Uploading a new custom image (`pixels` reassigned) must swap in a
    fresh glyph `TileGrid` built from the new pixel buffer, without
    calling `emoji_lookup`.
    """
    display = FakeDisplay()
    key_state = KeyState(
        emoji_id=0xF3, color=0x000000, pixels=_solid_opaque_pixels(0xF, 0x0, 0xF)
    )

    render_key(display, key_state, _stub_emoji_lookup)
    group = display.shown_groups[-1]
    old_glyph = list(group)[1]

    key_state.pixels = _solid_opaque_pixels(0x0, 0xF, 0x0)
    render_key(display, key_state, _stub_emoji_lookup)

    new_glyph = list(group)[1]
    assert new_glyph is not old_glyph
    assert new_glyph.bitmap[0] == _expected_rgb565(0x0, 0xF, 0x0)


def _corner_transparent_pixels(r4, g4, b4):
    """A 128x128 RGBA4444 buffer that is solid opaque (r4, g4, b4) except
    for pixel 0 (the top-left corner), which is fully transparent — the
    task 0041 DoD-1/DoD-2 fixture.
    """
    pixels = bytearray(_solid_opaque_pixels(r4, g4, b4))
    pixels[0:2] = _transparent_pixel()
    return bytes(pixels)


def test_transparent_pixel_shows_background_color():
    """DoD-1: a custom glyph with a transparent pixel must show
    key_state.color behind that pixel, not a baked-in color. The
    transparent pixel's bitmap value must index a palette entry marked
    transparent, with the background layer beneath already painted
    key_state.color, and the glyph must not be hidden.
    """
    display = FakeDisplay()
    pixels = _corner_transparent_pixels(0xF, 0x0, 0x0)  # opaque red, transparent corner
    key_state = KeyState(emoji_id=0xF3, color=0x07E0, pixels=pixels)  # green background

    render_key(display, key_state, _stub_emoji_lookup)

    group = display.shown_groups[-1]
    background, glyph = list(group)[0], list(group)[1]

    assert background.pixel_shader[0] == _rgb565_to_rgb888(0x07E0)
    corner_index = glyph.bitmap[0]
    assert glyph.pixel_shader.is_transparent(corner_index)
    assert glyph.hidden is False


def test_transparent_glyph_blink_toggles_background_not_glyph():
    """DoD-2: while a key with a transparent-pixel glyph blinks, its
    opaque glyph pixels must stay on screen every frame — the glyph
    TileGrid is never hidden — and only the background layer's color
    alternates between key_state.color and black.
    """
    display = FakeDisplay()
    pixels = _corner_transparent_pixels(0xF, 0x0, 0x0)  # opaque red, transparent corner
    key_state = KeyState(emoji_id=0xF3, color=0x07E0, blink=True, pixels=pixels)

    render_key(display, key_state, _stub_emoji_lookup)
    group = display.shown_groups[-1]
    background, glyph = list(group)[0], list(group)[1]
    opaque_pixel = glyph.bitmap[128 * 128 - 1]  # not the transparent corner

    assert glyph.hidden is False
    assert background.pixel_shader[0] == 0x000000  # off this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert glyph.hidden is False
    assert glyph.bitmap[128 * 128 - 1] == opaque_pixel
    assert background.pixel_shader[0] == _rgb565_to_rgb888(0x07E0)  # on this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert glyph.hidden is False
    assert glyph.bitmap[128 * 128 - 1] == opaque_pixel
    assert background.pixel_shader[0] == 0x000000  # off again


def test_opaque_pixels_keep_whole_image_blink_toggle():
    """DoD-3: a custom glyph with no transparent pixel must keep today's
    whole-image blink toggle — the glyph TileGrid's `hidden` flag
    alternates, and the background layer is never touched.
    """
    display = FakeDisplay()
    pixels = _solid_opaque_pixels(0xF, 0x0, 0x0)  # fully opaque, no transparency
    key_state = KeyState(emoji_id=0xF3, color=0x07E0, blink=True, pixels=pixels)

    render_key(display, key_state, _stub_emoji_lookup)
    group = display.shown_groups[-1]
    background, glyph = list(group)[0], list(group)[1]
    assert glyph.hidden is True  # emoji hidden this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert glyph.hidden is False
    assert background.pixel_shader[0] == _rgb565_to_rgb888(0x07E0)

    render_key(display, key_state, _stub_emoji_lookup)
    assert glyph.hidden is True
    assert background.pixel_shader[0] == _rgb565_to_rgb888(0x07E0)
