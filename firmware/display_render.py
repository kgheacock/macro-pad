"""Computes what to draw for a key and hands it to the key's panel queue.

Each key keeps cached 128x128 RGB565 frames: an "on" frame, and an "off"
frame while the key blinks. The board rebuilds them only when the key's
color or glyph changes. A blink toggle pushes the other cached frame and
composes nothing.

A frame is a `displayio.Bitmap` used as a plain 16-bit pixel buffer. It is
never shown through `displayio`. `bitmaptools` fills and blits it, and
`st7735.Panel.start_push` sends its bytes to the panel by DMA. The host driver
already converted the glyph to the panel's big-endian RGB565, so the board
converts no pixels; see "Set custom glyph" in docs/wire-protocol.md.

A frame is never changed after it is built. A new color or glyph composes a
new `Bitmap` in place of the cached one, the "back" frame, and the old one
stays whole until the panel has sent it, so a rebuild cannot change a frame
that is on the wire (task 0045).

See tasks/ongoing/0043-raw-spi-panels-init-once.md and
tasks/complete/0045-double-buffered-dma-panel-push.md for the design decisions.
"""

import array
import time

import bitmaptools
import displayio

import tracer as tracer_module

FRAME_WIDTH = 128
FRAME_HEIGHT = 128
FRAME_COLORS = 65536  # a 16-bit bitmap
_FRAME_COLORS = FRAME_COLORS

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
        # True while `_on_frame` is a slot of the frame cache (task 0061),
        # which `show_cached` set. A frame built by `_build_frames` is not.
        self._cached = False


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


def compose_into(frame, scratch, color: int, pixels: bytes) -> None:
    """Fill `frame` with `color`, then blit `pixels` over it, skipping the
    transparent value. `pixels` may be `None`, and then `scratch` may be too.

    `frame` and `scratch` are 128x128 16-bit `Bitmap`s that the caller owns.
    `scratch` takes the glyph before the blit, and holds nothing of value
    after. This allocates no `Bitmap`: the frame cache (task 0061) composes
    into its slots with it, 128 frames from one scratch bitmap.
    """
    bitmaptools.fill_region(frame, 0, 0, FRAME_WIDTH, FRAME_HEIGHT, _swap16(color))
    if pixels is not None:
        bitmaptools.arrayblit(scratch, array.array("H", pixels))
        bitmaptools.blit(
            frame, scratch, 0, 0, skip_source_index=TRANSPARENT_PIXEL
        )


def _compose(color: int, pixels: bytes) -> memoryview:
    """Build one frame: fill it with `color`, then blit `pixels` over it,
    skipping the transparent value. `pixels` may be `None`.

    Returns a view of the new `Bitmap` with one item per byte, which is what
    the DMA sends. A `Bitmap` of 16-bit pixels has 16-bit items, and the DMA
    would send a pixel as one item in the wrong size and order. The view keeps
    the `Bitmap` alive.

    A key that blinks composes here, a new `Bitmap` for each call (tasks 0044
    and 0048). A cached frame composes with `compose_into`.
    """
    frame = displayio.Bitmap(FRAME_WIDTH, FRAME_HEIGHT, _FRAME_COLORS)
    glyph = None
    if pixels is not None:
        glyph = displayio.Bitmap(FRAME_WIDTH, FRAME_HEIGHT, _FRAME_COLORS)
    compose_into(frame, glyph, color, pixels)
    return memoryview(frame).cast("B")


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
        key_state._cached = False

    if not key_state.blink:
        key_state._off_frame = None
    elif key_state._off_frame is None:
        if key_state._transparent_glyph:
            key_state._off_frame = _compose(_BLINK_OFF_COLOR, key_state.pixels)
        else:
            key_state._off_frame = _compose(key_state.color, None)


def show_cached(
    panel, key_state: KeyState, frame, color: int, emoji_id: int
) -> None:
    """Hand a frame of the frame cache (task 0061) to a key's panel queue.

    `frame` is the byte view of a slot, and `color` the color it was composed
    over. The key's state becomes what a Set custom glyph and a Key state
    would have left, so a later Key state that names the same color keeps the
    frame, and one that names another color builds a plain color frame. The key
    does not blink: a key that blinks is not cached. The cached frame is never
    changed here, and `app.MacroPad` does not overwrite a slot while a push
    that reads it is queued or on the wire.
    """
    key_state.color = color
    key_state.emoji_id = emoji_id
    key_state.blink = False
    key_state.pixels = None
    key_state._on_frame = frame
    key_state._off_frame = None
    key_state._frame_color = color
    key_state._frame_pixels = None
    key_state._transparent_glyph = True
    key_state._blink_visible = True
    key_state._cached = True
    panel.submit(frame)


def drop_cached(key_state: KeyState) -> None:
    """Forget a cached frame, so the next render builds the key's frame from
    its color and glyph. `app.MacroPad` calls this when a key state replaces
    the picture of a key that shows a cached frame.
    """
    if key_state._cached:
        key_state._cached = False
        key_state._on_frame = None


def blink_is_due(key_state: KeyState, blink_on: bool) -> bool:
    """True when `key_state` blinks and the frame it shows is not the one
    `blink_on` asks for. A steady key is never due.
    """
    return key_state.blink and key_state._blink_visible != blink_on


def prepare_key(key_state: KeyState, tracer=None, key_index=None) -> None:
    """Build `key_state`'s cached frames and submit nothing.

    `app.MacroPad` calls this for a key held by a batch (task 0060), so the
    composing happens as the data arrives, and the later `render_key` of the
    release only picks the cached frame of the blink slot.
    """
    _build_frames(key_state, tracer, key_index)


def render_key(
    panel, key_state: KeyState, tracer=None, key_index=None, blink_on=True
) -> None:
    """Hand one frame for a key to its panel queue.

    Rebuilds the key's cached frames first when its color, glyph, or blink
    flag changed since they were built. Then submits the "on" frame, or for
    a blinking key, the frame that `blink_on` names. `panel.submit(frame)`
    only queues the frame; `app.MacroPad` sends it when the bus is free.

    `blink_on` is the shared blink phase (task 0048): every blinking key
    shows "on" in the same slot and "off" in the next, whatever started its
    blink or when it was last drawn. A key that starts to blink, and an
    update to a blinking key, both draw the frame of the current slot. A
    steady key always shows "on".

    `tracer`/`key_index`, when `tracer` is set, record a `GLYPH_BUILT`
    record when the frames are rebuilt. `app.MacroPad` records `PUSH_STARTED`
    and `REFRESH_DONE` when the push starts and ends — see tasks 0042 and 0045.
    """
    _build_frames(key_state, tracer, key_index)

    key_state._blink_visible = blink_on or not key_state.blink

    frame = key_state._on_frame if key_state._blink_visible else key_state._off_frame
    panel.submit(frame)
