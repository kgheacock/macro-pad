import displayio

from firmware import glyphs
from firmware.display_render import KeyState, raw_bitmap_tile_grid, render_key


class FakeDisplay:
    """Records what a real `adafruit_st7735r.ST7735R` would have been
    told to show, via the same `show`/`refresh` calls it exposes.
    """

    def __init__(self) -> None:
        self.shown_groups = []
        self.refresh_count = 0

    def show(self, group: displayio.Group) -> None:
        self.shown_groups.append(group)

    def refresh(self, **kwargs) -> bool:
        self.refresh_count += 1
        return True


def _emoji_tile_grid(bitmap: displayio.Bitmap) -> displayio.TileGrid:
    palette = displayio.Palette(1)
    palette[0] = 0xFFFFFF
    return displayio.TileGrid(bitmap, pixel_shader=palette)


def _stub_emoji_lookup(emoji_id: str) -> displayio.TileGrid:
    return _emoji_tile_grid(displayio.Bitmap(1, 1, 1))


def test_fill_color():
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0xFF00FF)

    render_key(display, key_state, _stub_emoji_lookup)

    background = list(display.shown_groups[-1])[0]
    assert background.pixel_shader[0] == 0xFF00FF
    assert display.refresh_count == 1


def test_emoji_bitmap():
    display = FakeDisplay()
    expected_bitmap = displayio.Bitmap(8, 8, 2)
    key_state = KeyState(emoji_id="smile", color=0x000000)

    render_key(display, key_state, lambda emoji_id: _emoji_tile_grid(expected_bitmap))

    emoji_layer = list(display.shown_groups[-1])[1]
    assert emoji_layer.bitmap is expected_bitmap


def test_renders_digit():
    display = FakeDisplay()
    key_state = KeyState(emoji_id=0xF3, color=0x000000)

    def emoji_lookup(emoji_id):
        return glyphs.lookup(emoji_id, foreground=0xFFFFFF, background=0x000000)

    render_key(display, key_state, emoji_lookup)

    emoji_layer = list(display.shown_groups[-1])[1]
    expected = glyphs.lookup(0xF3, foreground=0xFFFFFF, background=0x000000)
    assert emoji_layer.bitmap is expected.bitmap


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

    def failing_emoji_lookup(emoji_id):
        raise AssertionError("emoji_lookup called despite key_state.pixels being set")

    render_key(display, key_state, failing_emoji_lookup)

    image_layer = list(display.shown_groups[-1])[1]
    assert image_layer.bitmap[0] == 0x07E0


def test_blink_toggle():
    display = FakeDisplay()
    key_state = KeyState(emoji_id="smile", color=0x000000, blink=True)

    render_key(display, key_state, _stub_emoji_lookup)
    assert len(list(display.shown_groups[0])) == 1  # emoji hidden this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert len(list(display.shown_groups[1])) == 2  # emoji visible this frame

    render_key(display, key_state, _stub_emoji_lookup)
    assert len(list(display.shown_groups[2])) == 1  # hidden again
