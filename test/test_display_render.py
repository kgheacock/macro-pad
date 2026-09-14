import displayio

from firmware import glyphs
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


def _solid_pixels(rgb565):
    lo = rgb565 & 0xFF
    hi = (rgb565 >> 8) & 0xFF
    return bytes((lo, hi)) * (128 * 128)


def test_raw_bitmap_tile_grid_unpacks_pixels():
    pixels = _solid_pixels(0xF81F)

    tile_grid = raw_bitmap_tile_grid(pixels)

    assert tile_grid.bitmap.width == 128
    assert tile_grid.bitmap.height == 128
    assert tile_grid.bitmap[0] == 0xF81F
    assert tile_grid.bitmap[128 * 128 - 1] == 0xF81F
    assert tile_grid.pixel_shader.input_colorspace == displayio.Colorspace.RGB565


def test_render_key_prefers_pixels_over_emoji_lookup():
    display = FakeDisplay()
    pixels = _solid_pixels(0x07E0)
    key_state = KeyState(emoji_id=0xF3, color=0x000000, pixels=pixels)

    def failing_emoji_lookup(emoji_id, color):
        raise AssertionError("emoji_lookup called despite key_state.pixels being set")

    render_key(display, key_state, failing_emoji_lookup)

    image_layer = list(display.shown_groups[-1])[1]
    assert image_layer.bitmap[0] == 0x07E0


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
    pixels = _solid_pixels(0x07E0)
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
    key_state = KeyState(emoji_id=0xF3, color=0x000000, pixels=_solid_pixels(0xF81F))

    render_key(display, key_state, _stub_emoji_lookup)
    group = display.shown_groups[-1]
    old_glyph = list(group)[1]

    key_state.pixels = _solid_pixels(0x07E0)
    render_key(display, key_state, _stub_emoji_lookup)

    new_glyph = list(group)[1]
    assert new_glyph is not old_glyph
    assert new_glyph.bitmap[0] == 0x07E0
