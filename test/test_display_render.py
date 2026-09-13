import displayio

from firmware import glyphs
from firmware.display_render import (
    KeyState,
    _rgb565_to_rgb888,
    raw_bitmap_tile_grid,
    render_key,
    render_key_with_builder,
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


def test_render_key_with_builder_builds_draws_and_releases():
    """Task 0032: `build_display` must be called exactly once, its
    display rendered exactly once, and the bus released exactly once —
    this board allows only 1 concurrent display bus, so any other order
    or count would leave a bus open when the next key needs one.
    """
    display = FakeDisplay()
    built = []
    released_before = displayio.release_display_count

    def build_display():
        built.append(display)
        return display

    key_state = KeyState(emoji_id="smile", color=0xF81F)

    render_key_with_builder(build_display, key_state, _stub_emoji_lookup)

    assert built == [display]
    assert display.refresh_count == 1
    assert displayio.release_display_count == released_before + 1


def test_blink_toggle():
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0x000000, blink=True)

    render_key(display, key_state, _stub_emoji_lookup)
    assert len(list(display.shown_groups[0])) == 1  # emoji hidden this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert len(list(display.shown_groups[1])) == 2  # emoji visible this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert len(list(display.shown_groups[2])) == 1  # hidden again
