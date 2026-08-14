"""Computes what to draw for a key: background color, emoji bitmap, and
blink visibility. Renders through `DisplayLike`, a small interface that
both a real `adafruit_st7735r.ST7735R` display and a test fake satisfy.

See tasks/ongoing/0006-display-render-module.md for the design decision.
"""

from typing import Callable, Protocol

import displayio


class DisplayLike(Protocol):
    """The subset of `adafruit_st7735r.ST7735R` this module calls.

    `ST7735R` subclasses `busdisplay.BusDisplay` and adds no methods of
    its own, so `show(group)` and `refresh(**kwargs)` — the two calls
    below — are exactly the ones an `ST7735R` instance exposes. (The
    older `fill`/`draw_bitmap` style API belongs to `adafruit_rgb_display`,
    a different, non-displayio driver.)
    """

    def show(self, group: displayio.Group) -> None: ...

    def refresh(self, **kwargs) -> bool: ...


EmojiLookup = Callable[[str], displayio.TileGrid]


class KeyState:
    """One key's render target: which emoji, which background color, and
    whether it should blink.
    """

    def __init__(self, emoji_id: str, color: int, blink: bool = False) -> None:
        self.emoji_id = emoji_id
        self.color = color
        self.blink = blink
        self._blink_visible = True


def render_key(
    display: DisplayLike,
    key_state: KeyState,
    emoji_lookup: EmojiLookup,
) -> None:
    """Compose and push one frame for a key.

    Background fill and blink visibility are decided here; the emoji
    bitmap itself comes from `emoji_lookup`, which the caller supplies
    (see this task's non-goals — emoji asset sourcing is out of scope).
    """
    group = displayio.Group()

    background_bitmap = displayio.Bitmap(1, 1, 1)
    background_palette = displayio.Palette(1)
    background_palette[0] = key_state.color
    group.append(
        displayio.TileGrid(background_bitmap, pixel_shader=background_palette)
    )

    if key_state.blink:
        key_state._blink_visible = not key_state._blink_visible

    if key_state._blink_visible:
        group.append(emoji_lookup(key_state.emoji_id))

    display.show(group)
    display.refresh()
