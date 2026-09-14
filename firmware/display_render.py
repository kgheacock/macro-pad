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


EmojiLookup = Callable[[str, int], displayio.TileGrid]

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

    `color` is 16-bit RGB565, matching the wire protocol and
    `glyph_state.py`'s persisted format — `render_key` widens it to the
    24-bit RGB888 `displayio.Palette` expects; see
    `_rgb565_to_rgb888`.

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

        # The persistent displayio scene graph `render_key` builds once and
        # mutates on every later call — see task 0033. `_group` is `None`
        # until the first `render_key` call; that is the signal `render_key`
        # uses to choose its build path over its update path. The
        # `_rendered_*` fields snapshot what the scene graph currently shows,
        # so an update call can tell which of `color`/`emoji_id`/`pixels`
        # changed since the last render without re-deriving it from scratch.
        self._group = None
        self._background_palette = None
        self._glyph_tile_grid = None
        self._rendered_color = None
        self._rendered_emoji_id = None
        self._rendered_pixels = None


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


def _rgb565_to_rgb888(color565: int) -> int:
    """Widen one wire-format RGB565 color to the 24-bit RGB888 int
    `displayio.Palette` expects.

    `displayio.Palette.__setitem__` treats a bare int as 0xRRGGBB — it
    does not know the wire's 16-bit RGB565 packing (5 bits red, 6 green,
    5 blue; see docs/wire-protocol.md). Assigning a raw RGB565 value
    directly leaves the top byte always 0 (RGB565's max is 0xFFFF), so
    the display's red channel came out zero regardless of what was sent
    — confirmed live during task 0031's key-0 bring-up, where RGB565 red
    (0xF800) rendered as green. Each channel is rescaled from its own bit
    width to a full 8 bits, not just left-shifted, so 0x1F (5-bit max)
    becomes 0xFF (8-bit max) instead of 0xF8.
    """
    r5 = (color565 >> 11) & 0x1F
    g6 = (color565 >> 5) & 0x3F
    b5 = color565 & 0x1F
    r8 = (r5 * 255) // 31
    g8 = (g6 * 255) // 63
    b8 = (b5 * 255) // 31
    return (r8 << 16) | (g8 << 8) | b8


def _build_glyph(key_state: KeyState, emoji_lookup: EmojiLookup) -> displayio.TileGrid:
    if key_state.pixels is not None:
        return raw_bitmap_tile_grid(key_state.pixels)
    return emoji_lookup(key_state.emoji_id, key_state.color)


def _build_scene(key_state: KeyState, emoji_lookup: EmojiLookup) -> None:
    """Build a key's `Group`, background `Palette`, and glyph `TileGrid`
    for the first time, and store them on `key_state`.

    Run once per key — see `render_key`. Every later call goes through
    `_update_scene` instead, which mutates these same objects rather than
    replacing them, so displayio's own per-`TileGrid` dirty tracking can
    shrink a later `refresh()` to the region that actually changed.
    """
    group = displayio.Group()

    background_bitmap = displayio.Bitmap(1, 1, 1)
    background_palette = displayio.Palette(1)
    background_palette[0] = _rgb565_to_rgb888(key_state.color)
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

    glyph_tile_grid = _build_glyph(key_state, emoji_lookup)
    glyph_tile_grid.hidden = not key_state._blink_visible
    group.append(glyph_tile_grid)

    key_state._group = group
    key_state._background_palette = background_palette
    key_state._glyph_tile_grid = glyph_tile_grid
    key_state._rendered_color = key_state.color
    key_state._rendered_emoji_id = key_state.emoji_id
    key_state._rendered_pixels = key_state.pixels


def _update_scene(key_state: KeyState, emoji_lookup: EmojiLookup) -> None:
    """Mutate a previously built scene graph in place for the next frame.

    A blink-only toggle touches nothing but the glyph `TileGrid`'s
    `hidden` flag, leaving the background layer's own dirty state
    untouched — that is the redraw-area win this task exists for. A
    color or glyph change still updates in place rather than rebuilding
    the `Group`, so the `Group` and background `TileGrid` objects stay
    the same displayio instances across every call.
    """
    color_changed = key_state.color != key_state._rendered_color
    if color_changed:
        key_state._background_palette[0] = _rgb565_to_rgb888(key_state.color)

    using_pixels = key_state.pixels is not None
    glyph_source_changed = key_state.pixels is not key_state._rendered_pixels or (
        not using_pixels and key_state.emoji_id != key_state._rendered_emoji_id
    )
    # A built-in emoji's own background is baked into its bitmap to match
    # key_state.color (task 0023's Open questions), so a color change
    # while showing one needs a fresh glyph too, not just the background
    # layer above. A custom image (key_state.pixels set) already fills
    # the whole panel and ignores key_state.color, so it does not.
    if glyph_source_changed or (not using_pixels and color_changed):
        new_glyph = _build_glyph(key_state, emoji_lookup)
        index = list(key_state._group).index(key_state._glyph_tile_grid)
        key_state._group[index] = new_glyph
        key_state._glyph_tile_grid = new_glyph

    key_state._rendered_color = key_state.color
    key_state._rendered_emoji_id = key_state.emoji_id
    key_state._rendered_pixels = key_state.pixels

    if key_state.blink:
        key_state._blink_visible = not key_state._blink_visible
    key_state._glyph_tile_grid.hidden = not key_state._blink_visible


def render_key(
    display: DisplayLike,
    key_state: KeyState,
    emoji_lookup: EmojiLookup,
) -> None:
    """Compose and push one frame for a key.

    The first call for a given `key_state` builds its scene graph
    (`_build_scene`); every later call mutates that same graph in place
    (`_update_scene`) instead of rebuilding it — see task 0033. The image
    itself comes from `key_state.pixels` when set, or from
    `emoji_lookup`, which the caller supplies (see this task's non-goals
    — emoji asset sourcing is out of scope), otherwise. `emoji_lookup`
    also receives `key_state.color` (RGB565), so a glyph's own background
    can match the key's — see task 0023's Open questions.
    """
    if key_state._group is None:
        _build_scene(key_state, emoji_lookup)
    else:
        _update_scene(key_state, emoji_lookup)

    display.root_group = key_state._group
    display.refresh()
