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

import time

import displayio

import tracer as tracer_module


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

# A custom glyph's raw pixel buffer is 128x128 RGBA4444 — see "Set custom
# glyph" in docs/wire-protocol.md. Matches wire.CUSTOM_GLYPH_WIDTH/HEIGHT,
# not imported here so this module stays usable with a stub emoji_id type
# in tests that never touch wire.py.
_CUSTOM_GLYPH_WIDTH = 128
_CUSTOM_GLYPH_HEIGHT = 128
_CUSTOM_GLYPH_PIXEL_COUNT = _CUSTOM_GLYPH_WIDTH * _CUSTOM_GLYPH_HEIGHT
_RGB565_COLOR_COUNT = 65536  # every value a 16-bit RGB565 pixel can hold

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

    `pixels`, when not `None`, is a 128x128 raw RGBA4444 buffer that
    replaces the built-in glyph table lookup entirely — see task 0030 and
    task 0041. `emoji_id` is still tracked while `pixels` is set, so
    persistence (`glyph_state.py`) can tell a custom image apart from a
    built-in one with no second flag.
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
        # Whether the current glyph TileGrid was built from a pixel
        # buffer with at least one transparent pixel — see
        # _pixels_have_transparent_pixel. Set whenever the glyph is
        # (re)built; only meaningful once _group is not None.
        self._transparent_glyph = False


def _pixels_have_transparent_pixel(pixels: bytes) -> bool:
    """True when a 128x128 raw RGBA4444 pixel buffer has at least one
    alpha-zero pixel.

    The alpha nibble is the high nibble of each pixel's high byte — see
    driver/transport/glyph.go's DecodePNGToRGBA4444, which builds this
    buffer.
    """
    for i in range(_CUSTOM_GLYPH_PIXEL_COUNT):
        if pixels[2 * i + 1] >> 4 == 0:
            return True
    return False


def _opaque_bitmap_tile_grid(pixels: bytes) -> displayio.TileGrid:
    """Build a whole-image glyph TileGrid from a 128x128 raw RGBA4444
    buffer with no transparent pixel.

    Each pixel's alpha nibble is discarded and its 4-bit red, green, and
    blue channels are widened to RGB565 and fed straight into the
    bitmap's raw value, exactly as this module did before task 0041's
    RGBA4444 change — a blink still hides this whole TileGrid, since
    there is no transparent pixel for a background layer to show through.
    """
    bitmap = displayio.Bitmap(
        _CUSTOM_GLYPH_WIDTH, _CUSTOM_GLYPH_HEIGHT, _RGB565_COLOR_COUNT
    )
    for i in range(_CUSTOM_GLYPH_PIXEL_COUNT):
        lo = pixels[2 * i]
        hi = pixels[2 * i + 1]
        r4 = hi & 0x0F
        g4 = lo >> 4
        b4 = lo & 0x0F
        r5 = (r4 * 31) // 15
        g6 = (g4 * 63) // 15
        b5 = (b4 * 31) // 15
        bitmap[i] = (r5 << 11) | (g6 << 5) | b5

    return displayio.TileGrid(
        bitmap,
        pixel_shader=displayio.ColorConverter(
            input_colorspace=displayio.Colorspace.RGB565
        ),
    )


def _transparent_bitmap_tile_grid(pixels: bytes) -> displayio.TileGrid:
    """Build a palette-indexed glyph TileGrid from a 128x128 raw RGBA4444
    buffer that has at least one transparent pixel.

    An alpha-zero pixel maps to palette index 0, marked transparent with
    `Palette.make_transparent`, so the background layer beneath shows
    through it — task 0041's DoD-1. The palette holds one entry per
    distinct opaque color the image actually uses (at most the 4096
    values RGBA4444's 4-bit-per-channel color depth allows), not a fixed
    65536-entry table, since most of that table would go unused for any
    real glyph.
    """
    bitmap = displayio.Bitmap(
        _CUSTOM_GLYPH_WIDTH, _CUSTOM_GLYPH_HEIGHT, _CUSTOM_GLYPH_PIXEL_COUNT + 1
    )
    color_indices = {}
    for i in range(_CUSTOM_GLYPH_PIXEL_COUNT):
        lo = pixels[2 * i]
        hi = pixels[2 * i + 1]
        if hi >> 4 == 0:
            bitmap[i] = 0
            continue
        key = (hi & 0x0F, lo >> 4, lo & 0x0F)
        index = color_indices.get(key)
        if index is None:
            index = len(color_indices) + 1
            color_indices[key] = index
        bitmap[i] = index

    palette = displayio.Palette(len(color_indices) + 1)
    palette.make_transparent(0)
    for (r4, g4, b4), index in color_indices.items():
        palette[index] = ((r4 * 17) << 16) | ((g4 * 17) << 8) | (b4 * 17)

    return displayio.TileGrid(bitmap, pixel_shader=palette)


def raw_bitmap_tile_grid(pixels: bytes) -> displayio.TileGrid:
    """Build a glyph TileGrid from a 128x128 raw RGBA4444 pixel buffer.

    `pixels` is 32,768 bytes: one little-endian uint16 per pixel,
    row-major, matching "Set custom glyph" in docs/wire-protocol.md.
    Delegates to `_transparent_bitmap_tile_grid` when the buffer has at
    least one transparent pixel, or `_opaque_bitmap_tile_grid` otherwise
    — task 0041's DoD-1 and DoD-3.
    """
    if _pixels_have_transparent_pixel(pixels):
        return _transparent_bitmap_tile_grid(pixels)
    return _opaque_bitmap_tile_grid(pixels)


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


# The blink's off-color, for a transparent-pixel glyph's background
# layer. Fixed, not configurable — see this task's Non-goals.
_BLINK_OFF_COLOR = 0x000000


def _build_glyph(key_state: KeyState, emoji_lookup: EmojiLookup) -> displayio.TileGrid:
    if key_state.pixels is not None:
        return raw_bitmap_tile_grid(key_state.pixels)
    return emoji_lookup(key_state.emoji_id, key_state.color)


def _glyph_is_transparent(key_state: KeyState) -> bool:
    """Whether `key_state`'s current glyph is a custom image with at
    least one transparent pixel — the case where a blink toggles the
    background layer's color instead of hiding the glyph. Only a custom
    image (`pixels` set) can be transparent; an `emoji_lookup` glyph is
    always a whole opaque tile.
    """
    return key_state.pixels is not None and _pixels_have_transparent_pixel(
        key_state.pixels
    )


def _build_scene(
    key_state: KeyState,
    emoji_lookup: EmojiLookup,
    tracer=None,
    key_index=None,
) -> None:
    """Build a key's `Group`, background `Palette`, and glyph `TileGrid`
    for the first time, and store them on `key_state`.

    Run once per key — see `render_key`. Every later call goes through
    `_update_scene` instead, which mutates these same objects rather than
    replacing them, so displayio's own per-`TileGrid` dirty tracking can
    shrink a later `refresh()` to the region that actually changed.

    `tracer`/`key_index`, when both set, record a `GLYPH_BUILT` trace
    record right after `_build_glyph` returns — see task 0042.
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
    if tracer is not None:
        tracer.record(
            tracer_module.GLYPH_BUILT, key_index, 0, time.monotonic_ns() // 1000
        )
    key_state._transparent_glyph = _glyph_is_transparent(key_state)
    if key_state._transparent_glyph:
        # The glyph layer never hides — its transparent pixels already
        # show the background layer beneath, so the blink toggles that
        # layer's own color instead. See task 0041's DoD-1 and DoD-2.
        glyph_tile_grid.hidden = False
        if key_state.blink and not key_state._blink_visible:
            background_palette[0] = _BLINK_OFF_COLOR
    else:
        glyph_tile_grid.hidden = not key_state._blink_visible
    group.append(glyph_tile_grid)

    key_state._group = group
    key_state._background_palette = background_palette
    key_state._glyph_tile_grid = glyph_tile_grid
    key_state._rendered_color = key_state.color
    key_state._rendered_emoji_id = key_state.emoji_id
    key_state._rendered_pixels = key_state.pixels


def _update_scene(
    key_state: KeyState,
    emoji_lookup: EmojiLookup,
    tracer=None,
    key_index=None,
) -> None:
    """Mutate a previously built scene graph in place for the next frame.

    A blink-only toggle on an opaque glyph touches nothing but the glyph
    `TileGrid`'s `hidden` flag, leaving the background layer's own dirty
    state untouched — that is the redraw-area win task 0033 exists for. A
    blink-only toggle on a transparent-pixel glyph instead rewrites the
    background `Palette` in place between `key_state.color` and black,
    and never touches `hidden` — task 0041's DoD-2. A color or glyph
    change still updates in place rather than rebuilding the `Group`, so
    the `Group` and background `TileGrid` objects stay the same displayio
    instances across every call.
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
        if tracer is not None:
            tracer.record(
                tracer_module.GLYPH_BUILT, key_index, 0, time.monotonic_ns() // 1000
            )
        index = list(key_state._group).index(key_state._glyph_tile_grid)
        key_state._group[index] = new_glyph
        key_state._glyph_tile_grid = new_glyph
        key_state._transparent_glyph = _glyph_is_transparent(key_state)

    key_state._rendered_color = key_state.color
    key_state._rendered_emoji_id = key_state.emoji_id
    key_state._rendered_pixels = key_state.pixels

    if key_state.blink:
        key_state._blink_visible = not key_state._blink_visible

    if key_state._transparent_glyph:
        # Recomputed unconditionally, not just when color_changed above:
        # a blink-only frame leaves key_state.color untouched but must
        # still flip the background between it and black every call.
        key_state._glyph_tile_grid.hidden = False
        blinked_off = key_state.blink and not key_state._blink_visible
        key_state._background_palette[0] = (
            _BLINK_OFF_COLOR if blinked_off else _rgb565_to_rgb888(key_state.color)
        )
    else:
        key_state._glyph_tile_grid.hidden = not key_state._blink_visible


def render_key(
    display: DisplayLike,
    key_state: KeyState,
    emoji_lookup: EmojiLookup,
    tracer=None,
    key_index=None,
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

    `tracer`/`key_index`, when both set, record a `GLYPH_BUILT` trace
    record when `_build_scene`/`_update_scene` (re)builds the glyph, and a
    `REFRESH_DONE` record right after `display.refresh()` returns — see
    task 0042.
    """
    if key_state._group is None:
        _build_scene(key_state, emoji_lookup, tracer, key_index)
    else:
        _update_scene(key_state, emoji_lookup, tracer, key_index)

    display.root_group = key_state._group
    display.refresh()

    if tracer is not None:
        tracer.record(
            tracer_module.REFRESH_DONE, key_index, 0, time.monotonic_ns() // 1000
        )
