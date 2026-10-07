"""Computes what to draw for a key and pushes it to the key's panel.

Each key keeps cached 128x128 RGB565 frames: an "on" frame, and an "off"
frame while the key blinks. The board rebuilds them only when the key's
color or glyph changes. A blink toggle pushes the other cached frame and
composes nothing.

A frame is a `displayio.Bitmap` used as a plain 16-bit pixel buffer. It is
never shown through `displayio`. `bitmaptools` fills and blits it, and
`st7735.Panel.push` writes its bytes to the panel. The host driver already
converted the glyph to the panel's big-endian RGB565, so the board converts
no pixels; see "Set custom glyph" in docs/wire-protocol.md.

See tasks/ongoing/0043-raw-spi-panels-init-once.md for the design decision.
"""

import array
import time

import bitmaptools
import displayio

import tracer as tracer_module

FRAME_WIDTH = 128
FRAME_HEIGHT = 128
_FRAME_COLORS = 65536  # a 16-bit bitmap

# A glyph pixel with this value is transparent: the key's background shows
# through it. The driver encodes real black as 0x0001. Both byte orders read
# the same, so the check needs no byte swap.
TRANSPARENT_PIXEL = 0x0000
_TRANSPARENT_BYTES = b"\x00\x00"

# The blink's off-color for a key whose glyph has a transparent pixel, and
# for a key with no glyph at all. Fixed, not configurable.
_BLINK_OFF_COLOR = 0x0000


class KeyState:
    """One key's render target: which glyph, which background color, and
    whether it should blink.

    `color` is 16-bit RGB565, matching the wire protocol.

    `pixels`, when not `None`, is the glyph: 128x128 big-endian RGB565, 2
    bytes per pixel, with `TRANSPARENT_PIXEL` marking a transparent pixel.
    `emoji_id` is still tracked while `pixels` is set, so a key state that
    names the custom-glyph sentinel keeps the image in place. A key with no
    `pixels` shows only its color.
    """

    def __init__(
        self, emoji_id: int, color: int, blink: bool = False, pixels: bytes = None
    ) -> None:
        self.emoji_id = emoji_id
        self.color = color
        self.blink = blink
        self.pixels = pixels

        # What the cached frames below were built from. A mismatch with
        # `color` or `pixels` means the frames are stale. `pixels` is
        # compared by identity: a new glyph is a new bytes object.
        self._frame_color = None
        self._frame_pixels = None
        self._on_frame = None
        self._off_frame = None
        # Whether the glyph has a transparent pixel. A blink on such a
        # glyph, or on a key with no glyph, flips the background color. A
        # blink on an opaque glyph hides the whole glyph.
        self._transparent_glyph = True
        self._blink_visible = False


def _swap16(value: int) -> int:
    """Swap the two bytes of a 16-bit value.

    A `displayio.Bitmap` stores a 16-bit pixel in the chip's little-endian
    byte order, while the panel reads big-endian. The glyph arrives already
    big-endian, so a blit copies it as is. Only a fill color, which is a
    number, needs this.
    """
    return ((value & 0xFF) << 8) | (value >> 8)


def _has_transparent_pixel(pixels: bytes) -> bool:
    """True when the glyph has at least one `TRANSPARENT_PIXEL`.

    Searches for two zero bytes at an even offset. `bytes.find` runs in C,
    so this avoids a 16,384-step loop in Python on the board. A hit at an
    odd offset straddles two pixels, so the search goes on from the next
    byte.
    """
    start = 0
    while True:
        found = pixels.find(_TRANSPARENT_BYTES, start)
        if found < 0:
            return False
        if found % 2 == 0:
            return True
        start = found + 1


def _compose(color: int, pixels: bytes) -> displayio.Bitmap:
    """Build one frame: fill it with `color`, then blit `pixels` over it,
    skipping the transparent value. `pixels` may be `None`.
    """
    frame = displayio.Bitmap(FRAME_WIDTH, FRAME_HEIGHT, _FRAME_COLORS)
    bitmaptools.fill_region(frame, 0, 0, FRAME_WIDTH, FRAME_HEIGHT, _swap16(color))
    if pixels is not None:
        glyph = displayio.Bitmap(FRAME_WIDTH, FRAME_HEIGHT, _FRAME_COLORS)
        bitmaptools.arrayblit(glyph, array.array("H", pixels))
        bitmaptools.blit(
            frame, glyph, 0, 0, skip_source_index=TRANSPARENT_PIXEL
        )
    return frame


def _build_frames(key_state: KeyState, tracer=None, key_index=None) -> None:
    """(Re)build `key_state`'s cached frames for its current color, glyph,
    and blink flag.

    The "on" frame is the key's color with its glyph over it. The "off"
    frame exists only while the key blinks, and is built here too, so a
    blink toggle never composes:

    - A glyph with a transparent pixel, or no glyph: the same frame over a
      black background.
    - An opaque glyph: the key's color with no glyph.

    `tracer`/`key_index`, when `tracer` is set, record a `GLYPH_BUILT`
    trace record each time the "on" frame is rebuilt — see task 0042.
    """
    glyph_changed = (
        key_state._on_frame is None or key_state.pixels is not key_state._frame_pixels
    )
    if glyph_changed:
        key_state._transparent_glyph = (
            key_state.pixels is None or _has_transparent_pixel(key_state.pixels)
        )
    if glyph_changed or key_state.color != key_state._frame_color:
        key_state._on_frame = _compose(key_state.color, key_state.pixels)
        if tracer is not None:
            tracer.record(
                tracer_module.GLYPH_BUILT, key_index, 0, time.monotonic_ns() // 1000
            )
        key_state._off_frame = None
        key_state._frame_color = key_state.color
        key_state._frame_pixels = key_state.pixels

    if not key_state.blink:
        key_state._off_frame = None
    elif key_state._off_frame is None:
        if key_state._transparent_glyph:
            key_state._off_frame = _compose(_BLINK_OFF_COLOR, key_state.pixels)
        else:
            key_state._off_frame = _compose(key_state.color, None)


def render_key(
    panel, key_state: KeyState, tracer=None, key_index=None, toggle=True
) -> None:
    """Push one frame for a key to its panel.

    Rebuilds the key's cached frames first when its color, glyph, or blink
    flag changed since they were built. Then pushes the "on" frame, or for
    a blinking key, the frame it should show now.

    `toggle` says why the key is drawn. A due blink passes `True` and the
    key flips to the frame it did not push last. A redraw caused by a state
    change passes `False` and the key keeps the frame it showed, so an
    update to a blinking key leaves its blink phase alone (task 0044). The
    first paint of a blinking key shows "on" either way.

    `tracer`/`key_index`, when `tracer` is set, record a `GLYPH_BUILT`
    record when the frames are rebuilt, and a `REFRESH_DONE` record right
    after the push returns — see task 0042.
    """
    first_paint = key_state._on_frame is None
    _build_frames(key_state, tracer, key_index)

    # A steady key always shows "on". A blinking key flips on a toggle, and
    # starts from "off" so its first paint shows "on".
    if not key_state.blink or (first_paint and not toggle):
        key_state._blink_visible = True
    elif toggle:
        key_state._blink_visible = not key_state._blink_visible

    frame = key_state._on_frame if key_state._blink_visible else key_state._off_frame
    panel.push(frame)

    if tracer is not None:
        tracer.record(
            tracer_module.REFRESH_DONE, key_index, 0, time.monotonic_ns() // 1000
        )
