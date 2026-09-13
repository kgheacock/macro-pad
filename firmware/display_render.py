"""Computes what to draw for a key: background color, emoji bitmap, and
blink visibility. Renders through `DisplayLike`, a small interface that
both a real `adafruit_st7735r.ST7735R` display and a test fake satisfy.

See tasks/ongoing/0006-display-render-module.md for the design decision.
"""

try:
    from typing import Callable, Protocol
except ImportError:
    # This board's CircuitPython build ships no `typing` module. Protocol
    # is only ever used as a base class below, and Callable only to build
    # the EmojiLookup alias, itself only ever used as an annotation, so
    # placeholders that support the same syntax are enough.
    class Protocol:
        pass

    class _Subscriptable:
        def __getitem__(self, item):
            return None

    Callable = _Subscriptable()

import displayio


class DisplayLike(Protocol):
    """The subset of `adafruit_st7735r.ST7735R` this module calls.

    `ST7735R` subclasses `busdisplay.BusDisplay` and adds no methods of
    its own, so setting `root_group` and calling `refresh(**kwargs)` —
    the two calls below — are exactly what an `ST7735R` instance exposes.
    (`BusDisplay.show(group)`, used in older CircuitPython, was removed
    in favor of the `root_group` property. The older `fill`/`draw_bitmap`
    style API belongs to `adafruit_rgb_display`, a different,
    non-displayio driver.)
    """

    root_group: displayio.Group

    def refresh(self, **kwargs) -> bool: ...


EmojiLookup = Callable[[str], displayio.TileGrid]

# A custom glyph's raw pixel buffer is 128x128 RGB565 — see "Set custom
# glyph" in docs/wire-protocol.md. Matches wire.CUSTOM_GLYPH_WIDTH/HEIGHT,
# not imported here so this module stays usable with a stub emoji_id type
# in tests that never touch wire.py.
_CUSTOM_GLYPH_WIDTH = 128
_CUSTOM_GLYPH_HEIGHT = 128
_CUSTOM_GLYPH_COLOR_COUNT = 65536  # every value a 16-bit RGB565 pixel can hold

# Matches code.py's DISPLAY_WIDTH/DISPLAY_HEIGHT (the real ST7735R panel
# size). A bare 1x1 TileGrid only ever draws its bitmap's native 1x1
# pixels — `test/stubs/displayio.py`'s FakeDisplay has no real canvas to
# expose that, so unit tests never caught it — leaving the rest of the
# 128x128 screen showing whatever the panel's power-on state was instead
# of the key's background color. Tiling the same 1x1 bitmap across a
# _DISPLAY_WIDTH x _DISPLAY_HEIGHT grid below fills the whole panel.
_DISPLAY_WIDTH = 128
_DISPLAY_HEIGHT = 128


class KeyState:
    """One key's render target: which emoji, which background color, and
    whether it should blink.

    `pixels`, when not `None`, is a 128x128 raw RGB565 buffer that
    replaces the built-in glyph table lookup entirely — see task 0030.
    `emoji_id` is still tracked while `pixels` is set, so persistence
    (`glyph_state.py`) can tell a custom image apart from a built-in one
    with no second flag.
    """

    def __init__(
        self, emoji_id: str, color: int, blink: bool = False, pixels: bytes = None
    ) -> None:
        self.emoji_id = emoji_id
        self.color = color
        self.blink = blink
        self.pixels = pixels
        self._blink_visible = True


def raw_bitmap_tile_grid(pixels: bytes) -> displayio.TileGrid:
    """Build a TileGrid from a 128x128 raw RGB565 pixel buffer.

    `pixels` is 32,768 bytes: one little-endian uint16 per pixel,
    row-major, matching "Set custom glyph" in docs/wire-protocol.md.
    """
    bitmap = displayio.Bitmap(
        _CUSTOM_GLYPH_WIDTH, _CUSTOM_GLYPH_HEIGHT, _CUSTOM_GLYPH_COLOR_COUNT
    )
    for i in range(_CUSTOM_GLYPH_WIDTH * _CUSTOM_GLYPH_HEIGHT):
        bitmap[i] = pixels[2 * i] | (pixels[2 * i + 1] << 8)

    return displayio.TileGrid(
        bitmap,
        pixel_shader=displayio.ColorConverter(
            input_colorspace=displayio.Colorspace.RGB565
        ),
    )


def render_key(
    display: DisplayLike,
    key_state: KeyState,
    emoji_lookup: EmojiLookup,
) -> None:
    """Compose and push one frame for a key.

    Background fill and blink visibility are decided here. The image
    itself comes from `key_state.pixels` when set, or from
    `emoji_lookup`, which the caller supplies (see this task's non-goals
    — emoji asset sourcing is out of scope), otherwise.
    """
    group = displayio.Group()

    background_bitmap = displayio.Bitmap(1, 1, 1)
    background_palette = displayio.Palette(1)
    background_palette[0] = key_state.color
    group.append(
        displayio.TileGrid(
            background_bitmap,
            pixel_shader=background_palette,
            width=_DISPLAY_WIDTH,
            height=_DISPLAY_HEIGHT,
        )
    )

    if key_state.blink:
        key_state._blink_visible = not key_state._blink_visible

    if key_state._blink_visible:
        if key_state.pixels is not None:
            group.append(raw_bitmap_tile_grid(key_state.pixels))
        else:
            group.append(emoji_lookup(key_state.emoji_id))

    display.root_group = group
    display.refresh()
