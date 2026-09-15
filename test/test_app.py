import board
import displayio
import pytest

# Imported flat, under the names the board uses. See conftest.py.
import glyph_state
import pins
import tracer as tracer_module
import wire
from app import (
    BLINK_INTERVAL_US,
    DEFAULT_COLOR,
    DEFAULT_EMOJI_ID,
    Backlight,
    MacroPad,
    make_switch,
)
from display_render import _rgb565_to_rgb888
from idle_timer import IdleTimer

DEBOUNCE_WINDOW_US = 7500  # the Debouncer's 7.5 ms default


class FakeDisplay:
    """Records what a real `ST7735R` would have been told to show."""

    def __init__(self):
        self.shown_groups = []

    @property
    def root_group(self):
        return self.shown_groups[-1]

    @root_group.setter
    def root_group(self, group):
        self.shown_groups.append(group)

    def refresh(self, **kwargs):
        return True


class FakeDisplayBuilder:
    """Stands in for `code.py`'s per-key-switch display-bus builder
    callable (task 0032, changed by task 0040).

    Real hardware builds a fresh `ST7735R` bus each time a different key
    takes over the shared bus, so no two keys ever share one display
    object. This still hands back the same `FakeDisplay` for a given key
    index on every call, so a test can read that key's whole render
    history through `per_key`. `calls` and `release_count_at_call`
    record when and how often the builder itself ran, so a test can tell
    a bus reuse (no call) apart from a rebuild (a call), and confirm a
    release happened first when one was expected.
    """

    def __init__(self, key_count):
        self.per_key = [FakeDisplay() for _ in range(key_count)]
        self.calls = []
        self.release_count_at_call = []
        self._fail_next = set()

    def fail_next(self, key_index):
        """Test helper: make the next call for `key_index` raise instead
        of returning a display, simulating a hardware bus-build failure.
        """
        self._fail_next.add(key_index)

    def __call__(self, key_index):
        self.calls.append(key_index)
        self.release_count_at_call.append(displayio.release_display_count)
        if key_index in self._fail_next:
            self._fail_next.discard(key_index)
            raise RuntimeError("display bus build failed")
        return self.per_key[key_index]


class FakeSerial:
    """CDC data endpoint that keeps every byte written to it, and hands
    back bytes a test queues with `feed`, mirroring `usb_cdc.data`'s
    read-and-write shape now that the loop reads host→device custom
    glyph frames from it. See test/stubs/usb_cdc.py.
    """

    def __init__(self):
        self.written = bytearray()
        self.in_waiting = 0
        self._to_read = bytearray()

    def write(self, data):
        self.written.extend(data)
        return len(data)

    def read(self, size=1):
        chunk = bytes(self._to_read[:size])
        self._to_read = self._to_read[size:]
        self.in_waiting = len(self._to_read)
        return chunk

    def feed(self, data):
        """Test helper: queue bytes to be returned by later read() calls."""
        self._to_read.extend(data)
        self.in_waiting = len(self._to_read)


class FakeBacklight:
    def __init__(self):
        self.duty_cycle = 1.0


class FakePWM:
    def __init__(self):
        self.duty_cycle = 0


class FakeHID:
    """HID device that hands back reports a test queued."""

    def __init__(self):
        self.queued = []

    def feed(self, report):
        self.queued.append(bytes(report))

    def get_last_received_report(self, report_id=None):
        if not self.queued:
            return None
        return self.queued.pop(0)


class RecordingEmojiLookup:
    """Emoji lookup that records the IDs the loop asked it for."""

    def __init__(self):
        self.requested_ids = []

    def __call__(self, emoji_id, color):
        self.requested_ids.append(emoji_id)
        palette = displayio.Palette(1)
        palette[0] = 0xFFFFFF
        return displayio.TileGrid(displayio.Bitmap(1, 1, 1), pixel_shader=palette)


class FakeGlyphStorage:
    """In-memory stand-in for `glyph_state.FilesystemStorage`. Passing the
    same instance to two `_build_pad` calls simulates a reboot: the
    second `MacroPad` reads whatever the first one wrote.
    """

    def __init__(self):
        self._files = {}

    def read(self, key_index):
        return self._files.get(key_index)

    def write(self, key_index, data):
        self._files[key_index] = data


def _build_pad(idle_timer=None, tracer=None, storage=None):
    switches = [make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS]
    displays = FakeDisplayBuilder(len(pins.KEYS))
    backlights = [FakeBacklight() for _ in pins.KEYS]
    hid_device = FakeHID()
    serial = FakeSerial()
    emoji_lookup = RecordingEmojiLookup()
    storage = storage if storage is not None else FakeGlyphStorage()

    pad = MacroPad(
        switches=switches,
        build_display=displays,
        backlights=backlights,
        hid_device=hid_device,
        serial=serial,
        emoji_lookup=emoji_lookup,
        idle_timer=idle_timer,
        tracer=tracer,
        storage=storage,
    )
    return pad, switches, displays, backlights, hid_device, serial, emoji_lookup, storage


def _key_state_report(
    key_index, color, emoji_id, blink=False, version=wire.PROTOCOL_VERSION
):
    return bytes(
        (
            key_index,
            version,
            color & 0xFF,
            (color >> 8) & 0xFF,
            emoji_id,
            1 if blink else 0,
        )
    )


def _background_color(display):
    return list(display.shown_groups[-1])[0].pixel_shader[0]


def _framed_event(key_index, event_type, timestamp_us):
    writer = FakeSerial()
    payload = wire.encode_event(key_index, event_type, timestamp_us)
    wire.write_frame(writer, wire.MESSAGE_TYPE_EVENT, payload)
    return bytes(writer.written)


def _parse_frames(data):
    """Split a raw CDC byte stream into (message_type, payload) frames."""
    frames = []
    i = 0
    while i < len(data):
        message_type = data[i]
        length = data[i + 1] | (data[i + 2] << 8)
        payload = data[i + 3 : i + 3 + length]
        frames.append((message_type, payload))
        i += wire.FRAME_HEADER_SIZE + length
    return frames


def _decode_trace_record(payload):
    code = payload[0]
    key = payload[1]
    trace_payload = payload[2] | (payload[3] << 8)
    timestamp = int.from_bytes(payload[4:12], "little")
    return code, key, trace_payload, timestamp


def _custom_glyph_pixels(fill_byte):
    return bytes([fill_byte]) * wire.CUSTOM_GLYPH_PIXELS_SIZE


def _custom_glyph_frame_with_pixels(key_index, pixels):
    """Build the raw framed bytes a host would write to CDC for one Set
    custom glyph message, ready to feed to a `FakeSerial`.
    """
    writer = FakeSerial()
    payload = bytes((key_index,)) + pixels
    wire.write_frame(writer, wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH, payload)
    return bytes(writer.written)


def _custom_glyph_frame(key_index, fill_byte):
    return _custom_glyph_frame_with_pixels(key_index, _custom_glyph_pixels(fill_byte))


def _rgba4444_solid_pixels(r4, g4, b4):
    """A 128x128 raw RGBA4444 buffer (task 0041), solid opaque
    (r4, g4, b4) — one little-endian pixel repeated. See
    driver/transport/glyph.go's DecodePNGToRGBA4444 for the bit layout.
    """
    pixel = bytes(((g4 << 4) | b4, 0xF0 | r4))
    return pixel * (wire.CUSTOM_GLYPH_PIXELS_SIZE // 2)


def _rgba4444_pixels_with_transparent_corner(r4, g4, b4):
    """`_rgba4444_solid_pixels`, except pixel 0 (the top-left corner) is
    fully transparent (alpha nibble 0) instead of opaque (r4, g4, b4).
    """
    pixels = bytearray(_rgba4444_solid_pixels(r4, g4, b4))
    pixels[0:2] = bytes((0x00, 0x00))
    return bytes(pixels)


def test_key_state_applies_to_one_key():
    pad, _, displays, _, hid_device, _, emoji_lookup, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    emoji_lookup.requested_ids.clear()

    hid_device.feed(_key_state_report(key_index=3, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert pad.key_states[3].color == 0xF81F
    assert pad.key_states[3].emoji_id == 0xA2
    # _background_color reads back what render_key handed displayio, which
    # is 24-bit RGB888 — key_states[3].color above stays 16-bit RGB565,
    # the wire/persisted format. See display_render._rgb565_to_rgb888.
    assert _background_color(displays.per_key[3]) == _rgb565_to_rgb888(0xF81F)
    assert len(displays.per_key[3].shown_groups) == 2
    assert emoji_lookup.requested_ids == [0xA2]

    for index in (0, 1, 2, 4, 5):
        assert pad.key_states[index].color == DEFAULT_COLOR
        assert pad.key_states[index].emoji_id == DEFAULT_EMOJI_ID
        assert len(displays.per_key[index].shown_groups) == 1  # not redrawn


def test_key_state_rejects_version_and_keeps_state():
    pad, _, displays, _, hid_device, _, _, _ = _build_pad()

    pad.step(0)
    hid_device.feed(
        _key_state_report(
            key_index=3, color=0xF81F, emoji_id=0xA2, version=wire.PROTOCOL_VERSION + 1
        )
    )
    pad.step(1000)

    assert pad.key_states[3].color == DEFAULT_COLOR
    assert pad.key_states[3].emoji_id == DEFAULT_EMOJI_ID
    assert len(displays.per_key[3].shown_groups) == 1  # not redrawn


def test_key_state_ignores_unknown_key_index():
    pad, _, _, _, hid_device, _, _, _ = _build_pad()

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=99, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert all(state.color == DEFAULT_COLOR for state in pad.key_states)


def test_key_state_color_only_change_still_refreshes_glyph():
    """Task 0033: a report that changes only `color` (same `emoji_id`)
    must still call `emoji_lookup` again. A built-in emoji bakes
    `key_state.color` into its own bitmap as its background (task 0023),
    so leaving the glyph `TileGrid` alone here would leave its background
    stale even though the panel's own background layer updated.
    """
    pad, _, displays, _, hid_device, _, emoji_lookup, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(_key_state_report(key_index=2, color=0x0000, emoji_id=7))
    pad.step(1000)
    emoji_lookup.requested_ids.clear()

    hid_device.feed(_key_state_report(key_index=2, color=0xF81F, emoji_id=7))
    pad.step(2000)

    assert pad.key_states[2].emoji_id == 7
    assert _background_color(displays.per_key[2]) == _rgb565_to_rgb888(0xF81F)
    assert emoji_lookup.requested_ids == [7]


def test_key_state_glyph_only_change_leaves_background_untouched():
    """Task 0033: a report that changes only `emoji_id` (same `color`)
    must rebuild the glyph `TileGrid` without touching the background
    layer's own color.
    """
    pad, _, displays, _, hid_device, _, emoji_lookup, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(_key_state_report(key_index=4, color=0x001F, emoji_id=7))
    pad.step(1000)
    background_before = _background_color(displays.per_key[4])
    emoji_lookup.requested_ids.clear()

    hid_device.feed(_key_state_report(key_index=4, color=0x001F, emoji_id=9))
    pad.step(2000)

    assert pad.key_states[4].emoji_id == 9
    assert _background_color(displays.per_key[4]) == background_before
    assert emoji_lookup.requested_ids == [9]


def test_active_key_reuses_display_bus_across_redraws():
    """Task 0040, DoD-1: two consecutive redraws of the same key must
    build its display bus exactly once. Rebuilding on every redraw
    pulsed the display's hardware reset line on real hardware, and the
    color did not hold — see 0040's Problem.
    """
    pad, _, displays, _, hid_device, _, _, _ = _build_pad()
    pad.step(0)  # power-on paint of all six keys
    displays.calls.clear()

    hid_device.feed(_key_state_report(key_index=3, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)
    hid_device.feed(_key_state_report(key_index=3, color=0x001F, emoji_id=0xA2))
    pad.step(2000)

    assert displays.calls == [3]  # built once, reused for the second redraw
    # 1 append from the power-on paint, plus 1 per redraw below — both
    # redraws still ran even though only the first one built a bus.
    assert len(displays.per_key[3].shown_groups) == 3


def test_switching_keys_releases_bus_before_building_next():
    """Task 0040, DoD-2: redrawing a different key must release the
    current display bus before building the next one — this board
    allows only 1 concurrent bus (task 0032).
    """
    pad, _, displays, _, hid_device, _, _, _ = _build_pad()
    pad.step(0)  # power-on paint of all six keys; key 5 ends up active
    displays.calls.clear()
    displays.release_count_at_call.clear()
    released_before = displayio.release_display_count

    hid_device.feed(_key_state_report(key_index=2, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert displays.calls == [2]
    # release_count_at_call records displayio.release_display_count at the
    # moment key 2's bus was built — one higher than before the switch
    # proves the release ran first, so the two busses were never open at
    # once.
    assert displays.release_count_at_call == [released_before + 1]
    assert pad._active_key_index == 2


def test_display_build_failure_leaves_no_active_key():
    """Task 0040 Risks: if build_display raises after the old bus was
    released, `_active_key_index` must land on `None`, not on the key
    that failed to build — otherwise the next redraw would wrongly
    assume that key's (nonexistent) bus was still open and skip
    rebuilding it.
    """
    pad, _, displays, _, hid_device, _, _, _ = _build_pad()
    pad.step(0)  # power-on paint of all six keys

    hid_device.feed(_key_state_report(key_index=2, color=0xF81F, emoji_id=0xA2))
    displays.fail_next(2)

    with pytest.raises(RuntimeError):
        pad.step(1000)

    assert pad._active_key_index is None
    assert pad._active_display is None


def test_blink_redraw_does_not_call_emoji_lookup_again():
    """Task 0033: a blink-only redraw toggles the existing glyph
    `TileGrid`'s `hidden` flag; it must not ask `emoji_lookup` for a new
    glyph, since neither the emoji nor the color changed.
    """
    pad, _, displays, _, hid_device, _, emoji_lookup, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(
        _key_state_report(key_index=5, color=0x001F, emoji_id=7, blink=True)
    )
    pad.step(1000)  # the state change itself
    emoji_lookup.requested_ids.clear()

    pad.step(1000 + BLINK_INTERVAL_US)  # blink interval elapsed

    assert len(displays.per_key[5].shown_groups) == 3
    assert emoji_lookup.requested_ids == []


def test_key_state_accepts_report_with_report_id_prefix():
    pad, _, _, _, hid_device, _, _, _ = _build_pad()

    pad.step(0)
    # boot.py declares report ID 1. Whether the core prefixes it is this
    # task's open question, so both lengths must decode the same way.
    hid_device.feed(
        bytes((1,)) + _key_state_report(key_index=2, color=0x07E0, emoji_id=0x11)
    )
    pad.step(1000)

    assert pad.key_states[2].color == 0x07E0
    assert pad.key_states[2].emoji_id == 0x11


def test_press_writes_event():
    pad, switches, _, _, _, serial, _, _ = _build_pad()

    pad.step(0)  # every switch open
    assert serial.written == b""

    switches[0].value = False  # pull-up: closed switch reads low
    pad.step(DEBOUNCE_WINDOW_US * 2)

    assert len(serial.written) == wire.FRAME_HEADER_SIZE + wire.EVENT_SIZE
    assert bytes(serial.written) == _framed_event(
        0, wire.PRESS, DEBOUNCE_WINDOW_US * 2
    )


def test_press_trace_order():
    tr = tracer_module.Tracer(capacity=32, enabled=True)
    pad, switches, _, _, _, serial, _, _ = _build_pad(tracer=tr)

    pad.step(0)  # power-on: every switch's initial reading, not an edge
    assert serial.written == b""
    serial.written = bytearray()

    switches[0].value = False  # pull-up: closed switch reads low
    pad.step(DEBOUNCE_WINDOW_US * 2)

    frames = _parse_frames(bytes(serial.written))
    trace_records = [
        _decode_trace_record(payload)
        for message_type, payload in frames
        if message_type == wire.MESSAGE_TYPE_TRACE
    ]
    key0_codes = [code for code, key, _, _ in trace_records if key == 0]

    assert key0_codes == [
        tracer_module.SWITCH_READ,
        tracer_module.DEBOUNCE_VERDICT,
        tracer_module.EVENT_WRITTEN,
    ]


def test_release_writes_event():
    pad, switches, _, _, _, serial, _, _ = _build_pad()

    pad.step(0)
    switches[2].value = False
    pad.step(DEBOUNCE_WINDOW_US * 2)
    switches[2].value = True
    pad.step(DEBOUNCE_WINDOW_US * 4)

    frame_size = wire.FRAME_HEADER_SIZE + wire.EVENT_SIZE
    assert len(serial.written) == frame_size * 2
    assert bytes(serial.written[frame_size:]) == _framed_event(
        2, wire.RELEASE, DEBOUNCE_WINDOW_US * 4
    )


def test_bounce_writes_one_event():
    pad, switches, _, _, _, serial, _, _ = _build_pad()

    pad.step(0)

    # Five contact bounces, all inside one debounce window.
    for bounce in range(5):
        switches[1].value = bounce % 2 == 1  # low, high, low, high, low
        pad.step(100 + bounce * 100)

    assert len(serial.written) == wire.FRAME_HEADER_SIZE + wire.EVENT_SIZE
    assert bytes(serial.written) == _framed_event(1, wire.PRESS, 100)


def test_idle_dims_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, _, _, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(0)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)

    pad.step(4999)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)


def test_key_event_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, switches, _, backlights, _, _, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    switches[4].value = False
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def test_host_message_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, hid_device, _, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    hid_device.feed(_key_state_report(key_index=0, color=0xF800, emoji_id=1))
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def test_blink_key_redraws_only_after_blink_interval_elapses():
    """A blinking key must not redraw on every `step` call — task 0022's
    main loop calls `step` as fast as it can with no pacing of its own,
    so redrawing every call toggled visibility far faster than a human
    can see as a blink (confirmed live during task 0031's key-0
    bring-up). It should redraw once immediately (the state change
    itself), then again only once BLINK_INTERVAL_US has elapsed.
    """
    pad, _, displays, _, hid_device, _, _, _ = _build_pad()

    pad.step(0)  # power-on: every key, including 5, renders once
    assert len(displays.per_key[5].shown_groups) == 1

    hid_device.feed(
        _key_state_report(key_index=5, color=0x001F, emoji_id=7, blink=True)
    )
    pad.step(1000)  # the state change itself: always redraws
    assert len(displays.per_key[5].shown_groups) == 2

    pad.step(2000)  # far short of BLINK_INTERVAL_US since the last toggle
    pad.step(3000)
    assert len(displays.per_key[5].shown_groups) == 2

    pad.step(1000 + BLINK_INTERVAL_US)  # interval elapsed
    assert len(displays.per_key[5].shown_groups) == 3

    assert len(displays.per_key[0].shown_groups) == 1


def test_backlight_scales_fraction_to_pwm_duty_cycle():
    pwm = FakePWM()
    backlight = Backlight(pwm)

    assert backlight.duty_cycle == 1.0
    assert pwm.duty_cycle == 0xFFFF

    backlight.duty_cycle = 0.1
    assert backlight.duty_cycle == 0.1
    assert pwm.duty_cycle == int(0.1 * 0xFFFF)


def test_custom_glyph_applies_to_one_key():
    pad, _, displays, _, _, serial, _, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0xAB))
    pad.step(1000)

    assert pad.key_states[3].emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
    assert pad.key_states[3].pixels == _custom_glyph_pixels(0xAB)
    assert len(displays.per_key[3].shown_groups) == 2

    for index in (0, 1, 2, 4, 5):
        assert pad.key_states[index].pixels is None
        assert len(displays.per_key[index].shown_groups) == 1  # not redrawn


def test_custom_glyph_ignores_unknown_key_index():
    pad, _, _, _, _, serial, _, _ = _build_pad()

    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=99, fill_byte=0xAB))
    pad.step(1000)

    assert all(state.pixels is None for state in pad.key_states)


def test_custom_glyph_arrives_across_multiple_steps():
    pad, _, displays, _, _, serial, _, _ = _build_pad()

    pad.step(0)
    frame = _custom_glyph_frame(key_index=2, fill_byte=0xCD)

    # Feed the frame in two pieces; the reader must not decode anything
    # until the whole frame has arrived, and must pick up where it left
    # off on the next step.
    split = len(frame) // 2
    serial.feed(frame[:split])
    pad.step(1000)
    assert pad.key_states[2].pixels is None
    assert len(displays.per_key[2].shown_groups) == 1  # not yet redrawn

    serial.feed(frame[split:])
    pad.step(2000)
    assert pad.key_states[2].pixels == _custom_glyph_pixels(0xCD)
    assert len(displays.per_key[2].shown_groups) == 2


def test_built_in_glyph_replaces_custom_image():
    pad, _, _, _, hid_device, serial, _, _ = _build_pad()

    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=1, fill_byte=0xAB))
    pad.step(1000)
    assert pad.key_states[1].pixels is not None

    hid_device.feed(_key_state_report(key_index=1, color=0xF800, emoji_id=0xF1))
    pad.step(2000)

    assert pad.key_states[1].pixels is None
    assert pad.key_states[1].emoji_id == 0xF1


def test_key_state_naming_custom_glyph_sentinel_keeps_image_and_blinks():
    pad, _, _, _, hid_device, serial, _, _ = _build_pad()

    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=1, fill_byte=0xAB))
    pad.step(1000)
    assert pad.key_states[1].pixels == _custom_glyph_pixels(0xAB)

    hid_device.feed(
        _key_state_report(
            key_index=1,
            color=0xF800,
            emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
            blink=True,
        )
    )
    pad.step(2000)

    assert pad.key_states[1].pixels == _custom_glyph_pixels(0xAB)
    assert pad.key_states[1].blink is True


def test_custom_glyph_transparent_pixel_shows_key_color():
    """Task 0041 DoD-1: a custom glyph's transparent pixel must show the
    key's own color, not a baked-in one.
    """
    pad, _, displays, _, hid_device, serial, _, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(_key_state_report(key_index=4, color=0x07E0, emoji_id=0x00))
    pad.step(1000)  # applies the key's color

    pixels = _rgba4444_pixels_with_transparent_corner(0xF, 0x0, 0x0)  # opaque red
    serial.feed(_custom_glyph_frame_with_pixels(key_index=4, pixels=pixels))
    pad.step(2000)  # applies the custom glyph

    group = displays.per_key[4].shown_groups[-1]
    background, glyph = list(group)[0], list(group)[1]

    assert background.pixel_shader[0] == _rgb565_to_rgb888(0x07E0)
    corner_index = glyph.bitmap[0]
    assert glyph.pixel_shader.is_transparent(corner_index)
    assert glyph.hidden is False


def test_custom_glyph_blink_toggles_background_when_transparent():
    """Task 0041 DoD-2: while a key showing a transparent-pixel custom
    glyph blinks, its opaque glyph pixels must stay on screen every
    frame — the glyph is never hidden — and only the background color
    alternates between the key's color and black.
    """
    pad, _, displays, _, hid_device, serial, _, _ = _build_pad()

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=4, color=0x07E0, emoji_id=0x00))
    pad.step(1000)

    pixels = _rgba4444_pixels_with_transparent_corner(0xF, 0x0, 0x0)  # opaque red
    serial.feed(_custom_glyph_frame_with_pixels(key_index=4, pixels=pixels))
    pad.step(2000)

    hid_device.feed(
        _key_state_report(
            key_index=4,
            color=0x07E0,
            emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
            blink=True,
        )
    )
    pad.step(3000)  # applies blink

    group = displays.per_key[4].shown_groups[-1]
    background, glyph = list(group)[0], list(group)[1]
    opaque_pixel = glyph.bitmap[128 * 128 - 1]  # not the transparent corner

    assert glyph.hidden is False
    first_bg = background.pixel_shader[0]

    pad.step(3000 + BLINK_INTERVAL_US)  # blink interval elapsed
    assert glyph.hidden is False
    assert glyph.bitmap[128 * 128 - 1] == opaque_pixel
    second_bg = background.pixel_shader[0]
    assert second_bg != first_bg
    assert {first_bg, second_bg} == {_rgb565_to_rgb888(0x07E0), 0x000000}

    pad.step(3000 + 2 * BLINK_INTERVAL_US)  # interval elapsed again
    assert glyph.hidden is False
    assert background.pixel_shader[0] == first_bg


def test_custom_glyph_opaque_keeps_whole_image_blink_toggle():
    """Task 0041 DoD-3: a custom glyph with no transparent pixel keeps
    today's whole-image blink toggle — the glyph's `hidden` flag
    alternates, not the background.
    """
    pad, _, displays, _, hid_device, serial, _, _ = _build_pad()

    pad.step(0)
    pixels = _rgba4444_solid_pixels(0xF, 0x0, 0x0)  # fully opaque, no transparency
    serial.feed(_custom_glyph_frame_with_pixels(key_index=4, pixels=pixels))
    pad.step(1000)

    hid_device.feed(
        _key_state_report(
            key_index=4,
            color=0x07E0,
            emoji_id=wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
            blink=True,
        )
    )
    pad.step(2000)  # applies blink

    group = displays.per_key[4].shown_groups[-1]
    background, glyph = list(group)[0], list(group)[1]
    first_hidden = glyph.hidden
    first_bg = background.pixel_shader[0]

    pad.step(2000 + BLINK_INTERVAL_US)
    assert glyph.hidden != first_hidden
    assert background.pixel_shader[0] == first_bg

    pad.step(2000 + 2 * BLINK_INTERVAL_US)
    assert glyph.hidden == first_hidden
    assert background.pixel_shader[0] == first_bg


def test_custom_glyph_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, _, serial, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    serial.feed(_custom_glyph_frame(key_index=0, fill_byte=0xAB))
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def test_reboot_restores_persisted_state():
    storage = FakeGlyphStorage()
    pad, _, _, _, hid_device, serial, _, _ = _build_pad(storage=storage)

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=4, color=0xF800, emoji_id=0xF2, blink=True))
    pad.step(1000)
    serial.feed(_custom_glyph_frame(key_index=1, fill_byte=0x42))
    pad.step(2000)

    # A fresh MacroPad, same storage: the reboot.
    rebooted, _, displays, _, _, _, _, _ = _build_pad(storage=storage)

    assert rebooted.key_states[4].color == 0xF800
    assert rebooted.key_states[4].emoji_id == 0xF2
    assert rebooted.key_states[4].blink is True
    assert rebooted.key_states[1].pixels == _custom_glyph_pixels(0x42)
    assert rebooted.key_states[1].emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID

    for index in (0, 2, 3, 5):
        assert rebooted.key_states[index].color == DEFAULT_COLOR
        assert rebooted.key_states[index].emoji_id == DEFAULT_EMOJI_ID
        assert rebooted.key_states[index].pixels is None

    rebooted.step(0)  # power-on paint reflects the restored state with no driver connected
    assert len(displays.per_key[1].shown_groups) == 1


def test_second_state_leaves_no_trace_of_first():
    storage = FakeGlyphStorage()
    pad, _, _, _, _, serial, _, _ = _build_pad(storage=storage)

    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0x11))
    pad.step(1000)
    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0x22))
    pad.step(2000)

    rebooted, _, _, _, _, _, _, _ = _build_pad(storage=storage)

    assert rebooted.key_states[3].pixels == _custom_glyph_pixels(0x22)
    # Exactly one stored record for the key — no trace of the first state.
    assert len(storage._files) == 1
    assert storage._files[3] == glyph_state.encode(
        rebooted.key_states[3].color,
        wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID,
        False,
        _custom_glyph_pixels(0x22),
    )
