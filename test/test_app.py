import zlib
import board
import bitmaptools
import pytest

# Imported flat, under the names the board uses. See conftest.py.
import display_render
import pins
import st7735
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
from idle_timer import IdleTimer
from spibus import FakeBus

DEBOUNCE_WINDOW_US = 7500  # the Debouncer's 7.5 ms default
PIXEL_COUNT = 128 * 128


class FakePanel:
    """Records the frames pushed to one key's panel, as bytes.

    A push is on the wire for `polls_to_finish` calls to `poll`, and the
    default of 0 means it is done when `step` first asks. `wire_log`, when a
    test shares one list between panels, records each start and end in
    order. `sent` holds the bytes of each frame as they were when its push
    ended, which is when the DMA has finished reading the frame.
    """

    def __init__(self, polls_to_finish=0, wire_log=None, name=None):
        self.frames = []
        self.sent = []
        self.busy = False
        self.polls_to_finish = polls_to_finish
        self._wire_log = wire_log
        self._name = name
        self._frame = None
        self._polls = 0

    def start_push(self, frame):
        assert not self.busy, "a push is already on the wire"
        self.busy = True
        self._frame = frame
        self._polls = 0
        self.frames.append(bytes(memoryview(frame)))
        if self._wire_log is not None:
            self._wire_log.append(("start", self._name))

    def poll(self):
        if self.busy:
            if self._polls < self.polls_to_finish:
                self._polls += 1
                return False
            self.sent.append(bytes(memoryview(self._frame)))
            self.busy = False
            self._frame = None
            if self._wire_log is not None:
                self._wire_log.append(("end", self._name))
        return True


class FakePanels:
    """One `FakePanel` per key, indexable like the `panels` list
    `MacroPad` takes. `per_key` is the same list, for a test that reads
    one key's push history.
    """

    def __init__(self, key_count):
        self.per_key = [FakePanel() for _ in range(key_count)]

    def __getitem__(self, key_index):
        return self.per_key[key_index]

    def __len__(self):
        return len(self.per_key)


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

    def readinto(self, buffer):
        chunk = self._to_read[: len(buffer)]
        buffer[: len(chunk)] = chunk
        self._to_read = self._to_read[len(chunk) :]
        self.in_waiting = len(self._to_read)
        return len(chunk)

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


def _build_pad(idle_timer=None, tracer=None, panels=None, push_bus=None):
    switches = [make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS]
    panels = panels if panels is not None else FakePanels(len(pins.KEYS))
    backlights = [FakeBacklight() for _ in pins.KEYS]
    hid_device = FakeHID()
    serial = FakeSerial()

    pad = MacroPad(
        switches=switches,
        panels=panels,
        backlights=backlights,
        hid_device=hid_device,
        serial=serial,
        idle_timer=idle_timer,
        tracer=tracer,
        push_bus=push_bus,
    )
    return pad, switches, panels, backlights, hid_device, serial


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


def _solid_frame(color):
    """The bytes of a frame filled with one RGB565 color, big-endian."""
    return bytes((color >> 8, color & 0xFF)) * PIXEL_COUNT


def _last_frame(panel):
    return panel.frames[-1]


def _frame_pixel(frame, index):
    return (frame[2 * index] << 8) | frame[2 * index + 1]


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


def _rgb565_solid_pixels(color):
    """A 128x128 big-endian RGB565 glyph (task 0043), one opaque color."""
    return bytes((color >> 8, color & 0xFF)) * PIXEL_COUNT


def _rgb565_pixels_with_transparent_corner(color):
    """`_rgb565_solid_pixels`, except pixel 0 (the top-left corner) is
    transparent (0x0000) instead of `color`.
    """
    pixels = bytearray(_rgb565_solid_pixels(color))
    pixels[0:2] = bytes((0x00, 0x00))
    return bytes(pixels)


def test_key_state_applies_to_one_key():
    pad, _, panels, _, hid_device, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys

    hid_device.feed(_key_state_report(key_index=3, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert pad.key_states[3].color == 0xF81F
    assert pad.key_states[3].emoji_id == 0xA2
    assert len(panels.per_key[3].frames) == 2
    assert _last_frame(panels.per_key[3]) == _solid_frame(0xF81F)

    for index in (0, 1, 2, 4, 5):
        assert pad.key_states[index].color == DEFAULT_COLOR
        assert pad.key_states[index].emoji_id == DEFAULT_EMOJI_ID
        assert len(panels.per_key[index].frames) == 1  # not redrawn


def test_key_state_rejects_version_and_keeps_state():
    pad, _, panels, _, hid_device, _ = _build_pad()

    pad.step(0)
    hid_device.feed(
        _key_state_report(
            key_index=3, color=0xF81F, emoji_id=0xA2, version=wire.PROTOCOL_VERSION + 1
        )
    )
    pad.step(1000)

    assert pad.key_states[3].color == DEFAULT_COLOR
    assert pad.key_states[3].emoji_id == DEFAULT_EMOJI_ID
    assert len(panels.per_key[3].frames) == 1  # not redrawn


def test_key_state_ignores_unknown_key_index():
    pad, _, _, _, hid_device, _ = _build_pad()

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=99, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert all(state.color == DEFAULT_COLOR for state in pad.key_states)


def test_color_only_change_rebuilds_the_frame():
    pad, _, panels, _, hid_device, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(_key_state_report(key_index=2, color=0x0000, emoji_id=7))
    pad.step(1000)

    hid_device.feed(_key_state_report(key_index=2, color=0xF81F, emoji_id=7))
    pad.step(2000)

    assert pad.key_states[2].emoji_id == 7
    assert _last_frame(panels.per_key[2]) == _solid_frame(0xF81F)


def test_emoji_id_only_change_pushes_the_same_picture_without_composing():
    """A built-in Emoji ID draws no glyph, so a change of Emoji ID alone
    leaves the frame as it was: the board composes nothing.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(_key_state_report(key_index=4, color=0x001F, emoji_id=7))
    pad.step(1000)
    bitmaptools.calls.clear()

    hid_device.feed(_key_state_report(key_index=4, color=0x001F, emoji_id=9))
    pad.step(2000)

    assert pad.key_states[4].emoji_id == 9
    assert _last_frame(panels.per_key[4]) == _solid_frame(0x001F)
    assert bitmaptools.calls == []


def test_key_switch_sends_no_init_or_reset():
    """DoD-1: after boot, redrawing keys 0, 1, 0 sends no init command and
    makes no RST change. Task 0040 only kept repeat redraws of one key
    from resetting the panels. Redrawing a different key did.
    """
    bus = FakeBus(key_count=len(pins.KEYS))
    panels = [bus.panel(i, colstart=2, rowstart=3) for i in range(len(pins.KEYS))]
    st7735.init_panels(bus.rst, panels, sleep=lambda seconds: None)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    pad.step(0)  # power-on paint of all six keys
    commands_before = len(bus.records)
    frames_before = len(bus.frames)
    rst_log_before = list(bus.rst.log)

    for key_index, color in ((0, 0xF800), (1, 0x07E0), (0, 0x001F)):
        hid_device.feed(_key_state_report(key_index, color, emoji_id=0))
        pad.step(1000)

    assert len(bus.frames) > frames_before, "the redraws wrote nothing"
    assert bus.records[commands_before:] == []  # frame-only pushes (task 0049)
    assert bus.rst.log == rst_log_before
    assert bus.image(0) == _solid_frame(0x001F)
    assert bus.image(1) == _solid_frame(0x07E0)


def test_update_to_one_key_writes_nothing_to_the_other_panels():
    bus = FakeBus(key_count=len(pins.KEYS))
    panels = [bus.panel(i) for i in range(len(pins.KEYS))]
    st7735.init_panels(bus.rst, panels, sleep=lambda seconds: None)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    pad.step(0)
    frame_counts = [bus.frame_count(i) for i in range(len(pins.KEYS))]
    other_commands = {i: len(bus.commands(i)) for i in range(len(pins.KEYS)) if i != 2}

    hid_device.feed(_key_state_report(key_index=2, color=0xF81F, emoji_id=0))
    pad.step(1000)

    assert bus.frame_count(2) == frame_counts[2] + 1
    for index, count in other_commands.items():
        assert len(bus.commands(index)) == count
        assert bus.frame_count(index) == frame_counts[index]


def test_blink_redraw_composes_nothing():
    """A blink-only redraw pushes the other cached frame. It must not
    fill or blit, since neither the color nor the glyph changed.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(
        _key_state_report(key_index=5, color=0x001F, emoji_id=7, blink=True)
    )
    pad.step(1000)  # the state change itself builds the frames
    bitmaptools.calls.clear()

    pad.step(1000 + BLINK_INTERVAL_US)  # blink interval elapsed

    assert len(panels.per_key[5].frames) == 3
    assert bitmaptools.calls == []
    assert panels.per_key[5].frames[-1] != panels.per_key[5].frames[-2]


def test_key_state_accepts_report_with_report_id_prefix():
    pad, _, _, _, hid_device, _ = _build_pad()

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
    pad, switches, _, _, _, serial = _build_pad()

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
    pad, switches, _, _, _, serial = _build_pad(tracer=tr)

    pad.step(0)  # power-on: every switch's initial reading, not an edge
    # Power-on also paints all six keys, tracing GLYPH_BUILT/REFRESH_DONE
    # for each — this test only covers the switch-edge codes below, so
    # that render noise is dropped rather than asserted empty.
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
    pad, switches, _, _, _, serial = _build_pad()

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
    pad, switches, _, _, _, serial = _build_pad()

    pad.step(0)

    # Five contact bounces, all inside one debounce window.
    for bounce in range(5):
        switches[1].value = bounce % 2 == 1  # low, high, low, high, low
        pad.step(100 + bounce * 100)

    assert len(serial.written) == wire.FRAME_HEADER_SIZE + wire.EVENT_SIZE
    assert bytes(serial.written) == _framed_event(1, wire.PRESS, 100)


def test_idle_dims_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(0)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)

    pad.step(4999)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)


def test_key_event_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, switches, _, backlights, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    switches[4].value = False
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def test_host_message_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, hid_device, _ = _build_pad(idle_timer=idle_timer)

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
    itself), then again only when the next blink slot starts.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()

    pad.step(0)  # power-on: every key, including 5, renders once
    assert len(panels.per_key[5].frames) == 1

    hid_device.feed(
        _key_state_report(key_index=5, color=0x001F, emoji_id=7, blink=True)
    )
    pad.step(1000)  # the state change itself: always redraws
    assert len(panels.per_key[5].frames) == 2

    pad.step(2000)  # the same blink slot
    pad.step(3000)
    assert len(panels.per_key[5].frames) == 2

    pad.step(BLINK_INTERVAL_US)  # the next slot starts
    assert len(panels.per_key[5].frames) == 3

    assert len(panels.per_key[0].frames) == 1


def test_backlight_scales_fraction_to_pwm_duty_cycle():
    pwm = FakePWM()
    backlight = Backlight(pwm)

    assert backlight.duty_cycle == 1.0
    assert pwm.duty_cycle == 0xFFFF

    backlight.duty_cycle = 0.1
    assert backlight.duty_cycle == 0.1
    assert pwm.duty_cycle == int(0.1 * 0xFFFF)


def test_custom_glyph_applies_to_one_key():
    pad, _, panels, _, _, serial = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0xAB))
    pad.step(1000)

    assert pad.key_states[3].emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
    assert pad.key_states[3].pixels == _custom_glyph_pixels(0xAB)
    assert len(panels.per_key[3].frames) == 2

    for index in (0, 1, 2, 4, 5):
        assert pad.key_states[index].pixels is None
        assert len(panels.per_key[index].frames) == 1  # not redrawn


def test_custom_glyph_paint_trace_order():
    """DoD-1: one custom-glyph paint fires three of the task-0042 trace
    codes, in stage order, for the key the Set custom glyph message named.
    The board no longer persists (task 0044), so `PERSIST_DONE` is not one.
    """
    tr = tracer_module.Tracer(capacity=32, enabled=True)
    pad, _, _, _, _, serial = _build_pad(tracer=tr)

    pad.step(0)  # power-on paint of all six keys
    serial.written = bytearray()

    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0xAB))
    pad.step(1000)

    frames = _parse_frames(bytes(serial.written))
    trace_records = [
        _decode_trace_record(payload)
        for message_type, payload in frames
        if message_type == wire.MESSAGE_TYPE_TRACE
    ]
    key3_codes = [code for code, key, _, _ in trace_records if key == 3]

    assert key3_codes == [
        tracer_module.CUSTOM_GLYPH_DECODED,
        tracer_module.GLYPH_BUILT,
        tracer_module.PUSH_STARTED,
        tracer_module.REFRESH_DONE,
    ]




def test_custom_glyph_ignores_unknown_key_index():
    pad, _, _, _, _, serial = _build_pad()

    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=99, fill_byte=0xAB))
    pad.step(1000)

    assert all(state.pixels is None for state in pad.key_states)


def test_custom_glyph_arrives_across_multiple_steps():
    pad, _, panels, _, _, serial = _build_pad()

    pad.step(0)
    frame = _custom_glyph_frame(key_index=2, fill_byte=0xCD)

    # Feed the frame in two pieces; the reader must not decode anything
    # until the whole frame has arrived, and must pick up where it left
    # off on the next step.
    split = len(frame) // 2
    serial.feed(frame[:split])
    pad.step(1000)
    assert pad.key_states[2].pixels is None
    assert len(panels.per_key[2].frames) == 1  # not yet redrawn

    serial.feed(frame[split:])
    pad.step(2000)
    assert pad.key_states[2].pixels == _custom_glyph_pixels(0xCD)
    assert len(panels.per_key[2].frames) == 2


def test_built_in_glyph_replaces_custom_image():
    pad, _, _, _, hid_device, serial = _build_pad()

    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=1, fill_byte=0xAB))
    pad.step(1000)
    assert pad.key_states[1].pixels is not None

    hid_device.feed(_key_state_report(key_index=1, color=0xF800, emoji_id=0xF1))
    pad.step(2000)

    assert pad.key_states[1].pixels is None
    assert pad.key_states[1].emoji_id == 0xF1


def test_key_state_naming_custom_glyph_sentinel_keeps_image_and_blinks():
    pad, _, _, _, hid_device, serial = _build_pad()

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


def test_custom_glyph_draws_over_the_key_color():
    """Task 0041 DoD-1, as task 0043 draws it: a transparent pixel shows
    the key's own color, and an opaque pixel shows the glyph.
    """
    pad, _, panels, _, hid_device, serial = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    hid_device.feed(_key_state_report(key_index=4, color=0x07E0, emoji_id=0x00))
    pad.step(1000)  # applies the key's color

    pixels = _rgb565_pixels_with_transparent_corner(0xF800)  # opaque red
    serial.feed(_custom_glyph_frame_with_pixels(key_index=4, pixels=pixels))
    pad.step(2000)  # applies the custom glyph

    frame = _last_frame(panels.per_key[4])
    assert _frame_pixel(frame, 0) == 0x07E0
    assert _frame_pixel(frame, 1) == 0xF800
    assert _frame_pixel(frame, PIXEL_COUNT - 1) == 0xF800


def test_custom_glyph_blink_toggles_background_when_transparent():
    """Task 0041 DoD-2: while a key showing a transparent-pixel custom
    glyph blinks, its opaque glyph pixels stay on screen every frame, and
    only the color behind its transparent pixels alternates between the
    key's color and black.
    """
    pad, _, panels, _, hid_device, serial = _build_pad()

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=4, color=0x07E0, emoji_id=0x00))
    pad.step(1000)

    pixels = _rgb565_pixels_with_transparent_corner(0xF800)  # opaque red
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
    first = _last_frame(panels.per_key[4])

    pad.step(3000 + BLINK_INTERVAL_US)  # blink interval elapsed
    second = _last_frame(panels.per_key[4])
    assert _frame_pixel(first, 1) == _frame_pixel(second, 1) == 0xF800
    assert {_frame_pixel(first, 0), _frame_pixel(second, 0)} == {0x07E0, 0x0000}

    pad.step(3000 + 2 * BLINK_INTERVAL_US)  # interval elapsed again
    assert _last_frame(panels.per_key[4]) == first


def test_custom_glyph_opaque_keeps_whole_image_blink_toggle():
    """Task 0041 DoD-3: a custom glyph with no transparent pixel blinks by
    showing and hiding the whole image, over the key's color.
    """
    pad, _, panels, _, hid_device, serial = _build_pad()

    pad.step(0)
    pixels = _rgb565_solid_pixels(0xF800)  # fully opaque, no transparency
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
    first = _last_frame(panels.per_key[4])

    pad.step(2000 + BLINK_INTERVAL_US)
    assert {first, _last_frame(panels.per_key[4])} == {
        _solid_frame(0xF800),
        _solid_frame(0x07E0),
    }

    pad.step(2000 + 2 * BLINK_INTERVAL_US)
    assert _last_frame(panels.per_key[4]) == first


def test_custom_glyph_of_the_wrong_length_is_dropped():
    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)
    writer = FakeSerial()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH,
        bytes((3,)) + bytes(wire.CUSTOM_GLYPH_PIXELS_SIZE - 2),
    )

    serial.feed(bytes(writer.written))
    pad.step(1000)

    assert pad.key_states[3].pixels is None
    assert len(panels.per_key[3].frames) == 1  # not redrawn

    # The reader consumed the bad frame, so the next good one still lands.
    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0xAB))
    pad.step(2000)
    assert pad.key_states[3].pixels == _custom_glyph_pixels(0xAB)


def _compressed_glyph_frame(key_index, stream):
    writer = FakeSerial()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH_COMPRESSED,
        bytes((key_index,)) + stream,
    )
    return bytes(writer.written)


def test_compressed_custom_glyph_applies_to_one_key():
    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)

    pixels = _rgb565_solid_pixels(0x1234)
    serial.feed(_compressed_glyph_frame(2, zlib.compress(pixels, 9)))
    pad.step(1000)

    assert pad.key_states[2].emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
    assert pad.key_states[2].pixels == pixels
    assert len(panels.per_key[2].frames) == 2


def test_compressed_custom_glyph_that_does_not_inflate_keeps_the_old_image():
    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)
    serial.feed(_custom_glyph_frame(key_index=3, fill_byte=0xAB))
    pad.step(1000)
    frames_before = len(panels.per_key[3].frames)

    serial.feed(_compressed_glyph_frame(3, b"\x78\x9c\xff\xff\xff"))
    pad.step(2000)

    assert pad.key_states[3].pixels == _custom_glyph_pixels(0xAB)
    assert len(panels.per_key[3].frames) == frames_before


def test_custom_glyph_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, _, serial = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    serial.feed(_custom_glyph_frame(key_index=0, fill_byte=0xAB))
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def _begin_batch_frame(glyph_mask, state_mask):
    """The raw framed bytes of a Begin batch message (task 0060)."""
    writer = FakeSerial()
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_BEGIN_BATCH, bytes((glyph_mask, state_mask))
    )
    return bytes(writer.written)


ALL_KEYS = 0b111111


def _paint_power_on(pad):
    """Run the power-on paint of all six keys to the end."""
    pad.step(0)
    pad.step(1)


def _frame_counts(panels):
    return [len(panel.frames) for panel in panels.per_key]


def test_batch_draws_no_key_until_the_last_glyph_and_key_state_arrive():
    """DoD-3: five of six glyphs and five of six key states change no panel."""
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    assert _frame_counts(panels) == [1] * 6

    serial.feed(_begin_batch_frame(ALL_KEYS, ALL_KEYS))
    pad.step(1000)
    for key in range(5):
        serial.feed(_custom_glyph_frame(key_index=key, fill_byte=0x11 + key))
        pad.step(2000 + key)
        hid_device.feed(_key_state_report(key, 0x0000, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID))
        pad.step(3000 + key)

    assert _frame_counts(panels) == [1] * 6

    serial.feed(_custom_glyph_frame(key_index=5, fill_byte=0x22))
    pad.step(4000)
    assert _frame_counts(panels) == [1] * 6  # the glyph of key 5 is in, its state is not

    hid_device.feed(_key_state_report(5, 0x0000, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID))
    pad.step(5000)

    assert _frame_counts(panels) == [2] * 6
    assert panels.per_key[3].frames[-1] == _custom_glyph_pixels(0x14)


def test_batch_queues_every_key_in_one_pass_so_the_pushes_start_in_two_groups():
    pad, bus, hid_device = _parallel_pad()
    serial = pad._serial
    _paint_power_on(pad)
    groups_before = len(bus.groups)

    serial.feed(_begin_batch_frame(0, ALL_KEYS))
    pad.step(1000)
    for key in range(6):
        hid_device.feed(_key_state_report(key, 0xF800, 0))
        pad.step(2000 + key)
        if key < 5:
            assert len(bus.groups) == groups_before

    assert bus.groups[groups_before:] == [[0, 1, 2], [3, 4, 5]]


def test_batch_of_state_only_keys_does_not_wait_for_a_glyph():
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b11))
    pad.step(1000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(2000)
    assert _frame_counts(panels) == [1] * 6
    hid_device.feed(_key_state_report(1, 0x07E0, 0))
    pad.step(3000)

    assert _frame_counts(panels) == [2, 2, 1, 1, 1, 1]
    assert panels.per_key[0].frames[-1] == _solid_frame(0xF800)
    assert panels.per_key[1].frames[-1] == _solid_frame(0x07E0)


def test_batch_is_released_after_two_seconds_with_what_the_board_has():
    """DoD-4: a fake clock steps past 2 s, the board draws the keys it has and
    records the time-out with the masks that were still pending.
    """
    tracer = _FakeTracer()
    pad, _, panels, _, hid_device, serial = _build_pad(tracer=tracer)
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b111))
    pad.step(10_000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(20_000)
    hid_device.feed(_key_state_report(1, 0x07E0, 0))
    pad.step(30_000)  # key 2 never arrives
    assert _frame_counts(panels) == [1] * 6

    pad.step(10_000 + 2_000_000 - 1)
    assert _frame_counts(panels) == [1] * 6
    assert not any(code == tracer_module.BATCH_TIMED_OUT for code, _, _ in tracer.records)

    pad.step(10_000 + 2_000_000)

    assert _frame_counts(panels) == [2, 2, 1, 1, 1, 1]
    timeouts = [r for r in tracer.records if r[0] == tracer_module.BATCH_TIMED_OUT]
    assert timeouts == [(tracer_module.BATCH_TIMED_OUT, 0xFF, 0b100 << 8)]

    # The batch is closed: a later key state for key 2 draws at once.
    hid_device.feed(_key_state_report(2, 0x001F, 0))
    pad.step(3_000_000)
    assert _frame_counts(panels)[2] == 2


def test_a_completed_batch_records_no_time_out():
    tracer = _FakeTracer()
    pad, _, _, _, hid_device, serial = _build_pad(tracer=tracer)
    _paint_power_on(pad)
    serial.feed(_begin_batch_frame(0, 0b1))
    pad.step(1000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(2000)
    pad.step(5_000_000)

    assert not any(code == tracer_module.BATCH_TIMED_OUT for code, _, _ in tracer.records)


def _device_frames(serial, message_type):
    """The payloads of the frames of one type that the board wrote to the host."""
    data = bytes(serial.written)
    out = []
    i = 0
    while i + wire.FRAME_HEADER_SIZE <= len(data):
        length = data[i + 1] | (data[i + 2] << 8)
        if data[i] == message_type:
            out.append(data[i + wire.FRAME_HEADER_SIZE : i + wire.FRAME_HEADER_SIZE + length])
        i += wire.FRAME_HEADER_SIZE + length
    return out


def test_open_batch_asks_the_host_for_what_it_lacks_after_500_ms():
    """DoD-1 (task 0063): key 2's state never arrives, and after 500 ms with no
    new data the board sends Batch incomplete with the masks that are pending.
    """
    tracer = _FakeTracer()
    pad, _, _, _, hid_device, serial = _build_pad(tracer=tracer)
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b111))
    pad.step(10_000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(20_000)
    hid_device.feed(_key_state_report(1, 0x07E0, 0))
    pad.step(30_000)
    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == []

    pad.step(30_000 + 499_999)
    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == []

    pad.step(30_000 + 500_000)

    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == [
        bytes((0b000, 0b100))
    ]
    incomplete = [r for r in tracer.records if r[0] == tracer_module.BATCH_INCOMPLETE]
    assert incomplete == [(tracer_module.BATCH_INCOMPLETE, 0xFF, 0b100 << 8)]


def test_batch_incomplete_names_the_missing_glyphs_and_key_states_apart():
    pad, _, _, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0b11, 0b11))
    pad.step(10_000)
    serial.feed(_custom_glyph_frame(key_index=0, fill_byte=0xAB))
    pad.step(20_000)
    hid_device.feed(_key_state_report(1, 0x07E0, 0))
    pad.step(30_000)

    pad.step(30_000 + 500_000)

    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == [
        bytes((0b10, 0b01))
    ]


def test_batch_incomplete_repeats_every_500_ms_at_most_three_times():
    tracer = _FakeTracer()
    pad, _, _, _, _, serial = _build_pad(tracer=tracer)
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b1))
    pad.step(10_000)
    for step in range(1, 8):
        pad.step(10_000 + step * 500_000)

    frames = _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE)
    assert len(frames) == 3
    assert set(frames) == {bytes((0, 0b1))}


def test_batch_incomplete_timer_restarts_when_data_arrives():
    pad, _, _, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b11))
    pad.step(10_000)
    pad.step(400_000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(410_000)

    pad.step(410_000 + 499_999)
    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == []

    pad.step(410_000 + 500_000)
    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == [bytes((0, 0b10))]


def test_a_complete_batch_sends_no_batch_incomplete():
    pad, _, _, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_begin_batch_frame(0, 0b1))
    pad.step(1000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(2000)

    pad.step(3_000_000)

    assert _device_frames(serial, wire.MESSAGE_TYPE_BATCH_INCOMPLETE) == []


def test_key_outside_the_masks_draws_at_once_while_a_batch_is_open():
    """DoD-5."""
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0b11, 0b11))
    pad.step(1000)
    hid_device.feed(_key_state_report(4, 0xF800, 0))
    pad.step(2000)
    assert panels.per_key[4].frames[-1] == _solid_frame(0xF800)
    assert len(panels.per_key[4].frames) == 2

    serial.feed(_custom_glyph_frame(key_index=5, fill_byte=0x33))
    pad.step(3000)
    assert panels.per_key[5].frames[-1] == _custom_glyph_pixels(0x33)

    # The named keys still wait.
    hid_device.feed(_key_state_report(0, 0x07E0, 0))
    pad.step(4000)
    assert len(panels.per_key[0].frames) == 1


def _blinking_batch_pad_with_release_at(release_us):
    """Keys 1 and 2 blink in one batch. Key 1's state comes in slot 0, and
    key 2's state comes at `release_us`, which completes the batch.
    """
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_begin_batch_frame(0, 0b110))
    pad.step(1000)
    hid_device.feed(_key_state_report(1, 0x001F, 0, blink=True))
    pad.step(100_000)
    assert len(panels.per_key[1].frames) == 1
    hid_device.feed(_key_state_report(2, 0x001F, 0, blink=True))
    pad.step(release_us)
    return pad, panels


def test_blinking_keys_of_a_batch_draw_the_blink_slot_of_the_release():
    """DoD-6: released in an even slot, both keys draw "on"; released in an odd
    slot, both draw "off", and both flip together at the next slot.
    """
    _, panels = _blinking_batch_pad_with_release_at(200_000)  # slot 0
    assert _last_frame(panels.per_key[1]) == _solid_frame(0x001F)
    assert _last_frame(panels.per_key[2]) == _solid_frame(0x001F)

    pad, panels = _blinking_batch_pad_with_release_at(BLINK_INTERVAL_US + 50_000)  # slot 1
    assert len(panels.per_key[1].frames) == 2
    assert _last_frame(panels.per_key[1]) == _solid_frame(0x0000)
    assert _last_frame(panels.per_key[2]) == _solid_frame(0x0000)

    pad.step(2 * BLINK_INTERVAL_US)  # slot 2
    assert _last_frame(panels.per_key[1]) == _solid_frame(0x001F)
    assert _last_frame(panels.per_key[2]) == _solid_frame(0x001F)


def test_a_new_batch_releases_the_open_one_first():
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b11))
    pad.step(1000)
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(2000)
    assert len(panels.per_key[0].frames) == 1  # key 1 is missing

    serial.feed(_begin_batch_frame(0, 0b1000))
    pad.step(3000)

    assert _last_frame(panels.per_key[0]) == _solid_frame(0xF800)
    assert len(panels.per_key[1].frames) == 1  # nothing arrived for it

    hid_device.feed(_key_state_report(3, 0x07E0, 0))
    pad.step(4000)
    assert _last_frame(panels.per_key[3]) == _solid_frame(0x07E0)


def test_a_batch_withdraws_a_waiting_frame_of_a_named_key():
    """A frame that waits in the queue would draw before the others. It
    leaves the queue, and the key draws again at the release.
    """
    panels = _slow_panels(polls_to_finish=3)
    pad, _, _, _, hid_device, serial = _build_pad(panels=panels)
    pad.step(0)  # key 0 on the wire; keys 1 to 5 wait in the queue
    assert not panels.per_key[3].frames

    serial.feed(_begin_batch_frame(0, 0b1000))
    pad.step(1000)
    _steps(pad, 60, now_us=2000)
    assert panels.per_key[3].frames == []  # held, though it was due

    hid_device.feed(_key_state_report(3, 0xF800, 0))
    pad.step(3000)
    _steps(pad, 60, now_us=3000)

    assert panels.per_key[3].frames == [_solid_frame(0xF800)]
    assert all(len(panels.per_key[key].frames) == 1 for key in (0, 1, 2, 4, 5))


def test_a_key_state_that_arrives_with_the_begin_batch_counts():
    """HID and CDC have no common order. Both arrive in one step, and the
    key state still belongs to the batch.
    """
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_begin_batch_frame(0, 0b11))
    hid_device.feed(_key_state_report(0, 0xF800, 0))
    pad.step(1000)
    assert len(panels.per_key[0].frames) == 1

    hid_device.feed(_key_state_report(1, 0x07E0, 0))
    pad.step(2000)
    assert _frame_counts(panels) == [2, 2, 1, 1, 1, 1]


def test_an_empty_batch_opens_and_closes_at_once():
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_begin_batch_frame(0, 0))
    pad.step(1000)
    hid_device.feed(_key_state_report(2, 0xF800, 0))
    pad.step(2000)

    assert len(panels.per_key[2].frames) == 2


def test_a_batch_of_the_wrong_length_is_dropped():
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    writer = FakeSerial()
    wire.write_frame(writer, wire.MESSAGE_TYPE_BEGIN_BATCH, b"\x01")
    serial.feed(bytes(writer.written))
    pad.step(1000)
    hid_device.feed(_key_state_report(2, 0xF800, 0))
    pad.step(2000)

    assert len(panels.per_key[2].frames) == 2

def test_update_keeps_phase():
    """An update to a blinking key draws the frame of the current slot, with
    the new color, so it never moves the key off the shared phase.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()
    pad.step(0)
    hid_device.feed(_key_state_report(key_index=5, color=0x001F, emoji_id=0, blink=True))
    pad.step(1000)
    assert _last_frame(panels.per_key[5]) == _solid_frame(0x001F)  # slot 0: "on"
    pad.step(BLINK_INTERVAL_US + 1000)
    assert _last_frame(panels.per_key[5]) == _solid_frame(0x0000)  # slot 1: "off"
    shown = len(panels.per_key[5].frames)

    # An update in an "off" slot: the key stays "off", in the new color.
    hid_device.feed(_key_state_report(key_index=5, color=0xF800, emoji_id=0, blink=True))
    pad.step(BLINK_INTERVAL_US + 200_000)
    assert len(panels.per_key[5].frames) == shown + 1
    assert _last_frame(panels.per_key[5]) == _solid_frame(0x0000)

    # No redraw until the slot ends, then "on" in the new color.
    pad.step(2 * BLINK_INTERVAL_US - 1)
    assert len(panels.per_key[5].frames) == shown + 1
    pad.step(2 * BLINK_INTERVAL_US)
    assert len(panels.per_key[5].frames) == shown + 2
    assert _last_frame(panels.per_key[5]) == _solid_frame(0xF800)

    # An update in an "on" slot: the key stays "on".
    hid_device.feed(_key_state_report(key_index=5, color=0x07E0, emoji_id=0, blink=True))
    pad.step(2 * BLINK_INTERVAL_US + 100_000)
    assert _last_frame(panels.per_key[5]) == _solid_frame(0x07E0)
    pad.step(3 * BLINK_INTERVAL_US)
    assert _last_frame(panels.per_key[5]) == _solid_frame(0x0000)


def test_keys_that_start_blinking_apart_show_a_shared_phase():
    """DoD-1: two keys that start to blink 200 ms apart show the same frame
    in every later slot.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()
    pad.step(0)
    hid_device.feed(_key_state_report(key_index=1, color=0x001F, emoji_id=0, blink=True))
    pad.step(100_000)
    hid_device.feed(_key_state_report(key_index=2, color=0x001F, emoji_id=0, blink=True))
    pad.step(300_000)

    for slot in range(1, 7):
        now_us = slot * BLINK_INTERVAL_US + 10_000
        pad.step(now_us)
        expected = _solid_frame(0x001F if slot % 2 == 0 else 0x0000)
        assert _last_frame(panels.per_key[1]) == expected
        assert _last_frame(panels.per_key[2]) == expected


def test_a_key_that_starts_blinking_joins_current_slot():
    """DoD-2: a key that starts to blink in an odd slot shows "off" at once,
    and shows "on" when the next slot starts.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()
    pad.step(0)

    hid_device.feed(_key_state_report(key_index=3, color=0x001F, emoji_id=0, blink=True))
    pad.step(BLINK_INTERVAL_US + 50_000)  # slot 1

    assert len(panels.per_key[3].frames) == 2
    assert _last_frame(panels.per_key[3]) == _solid_frame(0x0000)

    pad.step(2 * BLINK_INTERVAL_US)  # slot 2

    assert _last_frame(panels.per_key[3]) == _solid_frame(0x001F)


def test_a_late_step_jumps_to_phase_with_one_push():
    """DoD-2: after a step that comes slots late, a blinking key shows the
    current phase, drawn once. It does not replay the slots it missed.
    """
    pad, _, panels, _, hid_device, _ = _build_pad()
    hid_device.feed(_key_state_report(key_index=0, color=0x001F, emoji_id=0, blink=True))
    pad.step(0)
    shown = len(panels.per_key[0].frames)

    pad.step(5 * BLINK_INTERVAL_US + 10_000)  # slot 5, five slots late
    assert len(panels.per_key[0].frames) == shown + 1
    assert _last_frame(panels.per_key[0]) == _solid_frame(0x0000)

    pad.step(5 * BLINK_INTERVAL_US + 20_000)  # same slot: nothing to draw
    assert len(panels.per_key[0].frames) == shown + 1

    pad.step(8 * BLINK_INTERVAL_US)  # slot 8, "on"
    assert len(panels.per_key[0].frames) == shown + 2
    assert _last_frame(panels.per_key[0]) == _solid_frame(0x001F)


def test_a_steady_key_is_not_redrawn_by_the_blink_slot():
    pad, _, panels, _, _, _ = _build_pad()
    pad.step(0)

    pad.step(BLINK_INTERVAL_US)
    pad.step(2 * BLINK_INTERVAL_US)

    assert all(len(panel.frames) == 1 for panel in panels.per_key)


class _FakeTracer:
    """Records every `record` call, in call order."""

    def __init__(self):
        self.records = []

    def record(self, code, key, payload, now_us):
        self.records.append((code, key, payload))

    def drain(self, write):
        pass


def _slow_panels(polls_to_finish, wire_log=None):
    """One `FakePanel` per key whose push stays on the wire for
    `polls_to_finish` polls. `wire_log` is shared, so it shows the order of
    every start and end across the panels.
    """
    panels = FakePanels(len(pins.KEYS))
    panels.per_key = [
        FakePanel(polls_to_finish=polls_to_finish, wire_log=wire_log, name=index)
        for index in range(len(pins.KEYS))
    ]
    return panels


def _steps(pad, count, now_us=0):
    for _ in range(count):
        pad.step(now_us)


def test_step_starts_one_push_and_returns_while_it_is_on_the_wire():
    panels = _slow_panels(polls_to_finish=3)
    pad, switches, _, _, _, serial = _build_pad(panels=panels)

    pad.step(0)  # power-on: six keys are dirty

    started = [bool(panel.frames) for panel in panels.per_key]
    assert started == [True, False, False, False, False, False]

    switches[2].value = False  # a press while the push is on the wire
    pad.step(DEBOUNCE_WINDOW_US * 2)

    assert panels.per_key[1].frames == []
    frames = _parse_frames(bytes(serial.written))
    assert any(message_type == wire.MESSAGE_TYPE_EVENT for message_type, _ in frames)


def test_back_frame_not_sent_after_a_color_change_during_a_push():
    """DoD-3: a color change on a key, while a frame of that key is on the
    wire, builds a new frame and leaves the one on the wire whole.
    """
    panels = _slow_panels(polls_to_finish=4)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    pad.step(0)  # key 0's first frame, in the default color, goes on the wire
    assert panels.per_key[0].busy

    hid_device.feed(_key_state_report(key_index=0, color=0xF800, emoji_id=0))
    pad.step(1000)  # composes a new frame for key 0 while the old one is sent
    assert panels.per_key[0].busy

    _steps(pad, 60, now_us=2000)  # let every queued push end

    assert panels.per_key[0].sent[0] == _solid_frame(DEFAULT_COLOR)
    assert panels.per_key[0].sent[0] == panels.per_key[0].frames[0]
    assert panels.per_key[0].sent[1] == _solid_frame(0xF800)


def test_back_frame_not_sent_for_a_cached_blink_frame_that_is_rebuilt():
    panels = _slow_panels(polls_to_finish=4)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    hid_device.feed(_key_state_report(key_index=0, color=0x001F, emoji_id=0, blink=True))
    pad.step(0)
    _steps(pad, 60, now_us=1000)  # every panel has its first frame, key 0 "on"
    pad.step(BLINK_INTERVAL_US)  # a due blink: key 0's "off" frame is on the wire
    assert panels.per_key[0].busy

    hid_device.feed(_key_state_report(key_index=0, color=0x07E0, emoji_id=0, blink=True))
    pad.step(BLINK_INTERVAL_US + 1000)  # rebuilds both cached frames
    _steps(pad, 60, now_us=BLINK_INTERVAL_US + 2000)

    off_frame = panels.per_key[0].sent[1]
    assert off_frame == panels.per_key[0].frames[1]
    assert off_frame == _solid_frame(0x0000)


def test_pushes_do_not_overlap_and_run_in_queue_order():
    """DoD-5: two pushes queued for different panels never overlap, and run
    in queue order.
    """
    wire_log = []
    panels = _slow_panels(polls_to_finish=2, wire_log=wire_log)
    pad, _, _, _, _, _ = _build_pad(panels=panels)

    pad.step(0)
    _steps(pad, 60, now_us=1000)

    assert wire_log == [
        entry for key in range(6) for entry in (("start", key), ("end", key))
    ]


def test_pushes_do_not_overlap_when_keys_update_in_the_middle():
    wire_log = []
    panels = _slow_panels(polls_to_finish=2, wire_log=wire_log)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    pad.step(0)
    hid_device.feed(_key_state_report(key_index=4, color=0xF800, emoji_id=0))
    pad.step(1000)
    hid_device.feed(_key_state_report(key_index=1, color=0x07E0, emoji_id=0))
    _steps(pad, 80, now_us=2000)

    kinds = [kind for kind, _ in wire_log]
    assert kinds == ["start", "end"] * (len(kinds) // 2)
    # Six first paints. An update to a key whose first paint is still queued
    # replaces it, so the two updates add one or two pushes.
    assert 2 * 7 <= len(kinds) <= 2 * 8


def test_a_newer_frame_replaces_one_that_has_not_started_and_keeps_its_place():
    wire_log = []
    panels = _slow_panels(polls_to_finish=2, wire_log=wire_log)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    pad.step(0)  # key 0 on the wire; keys 1 to 5 queued with the default color

    hid_device.feed(_key_state_report(key_index=2, color=0xF800, emoji_id=0))
    pad.step(1000)
    hid_device.feed(_key_state_report(key_index=2, color=0x07E0, emoji_id=0))
    pad.step(2000)
    _steps(pad, 60, now_us=3000)

    assert [name for kind, name in wire_log if kind == "start"] == [0, 1, 2, 3, 4, 5]
    assert panels.per_key[2].frames == [_solid_frame(0x07E0)]


def test_push_started_and_refresh_done_trace_each_push():
    tracer = _FakeTracer()
    panels = _slow_panels(polls_to_finish=2)
    pad, _, _, _, _, _ = _build_pad(panels=panels, tracer=tracer)

    pad.step(0)

    key0 = [code for code, key, _ in tracer.records if key == 0]
    assert key0 == [tracer_module.GLYPH_BUILT, tracer_module.PUSH_STARTED]

    _steps(pad, 2, now_us=1000)

    key0 = [code for code, key, _ in tracer.records if key == 0]
    assert key0 == [
        tracer_module.GLYPH_BUILT,
        tracer_module.PUSH_STARTED,
        tracer_module.REFRESH_DONE,
    ]
    assert tracer_module.PUSH_STARTED == 9


def test_blinking_key_toggles_on_schedule_while_other_pushes_run():
    panels = _slow_panels(polls_to_finish=2)
    pad, _, _, _, hid_device, _ = _build_pad(panels=panels)
    hid_device.feed(_key_state_report(key_index=0, color=0x001F, emoji_id=0, blink=True))
    pad.step(0)
    _steps(pad, 60, now_us=1000)
    first = len(panels.per_key[0].frames)

    pad.step(BLINK_INTERVAL_US - 1)
    _steps(pad, 10, now_us=BLINK_INTERVAL_US - 1)
    assert len(panels.per_key[0].frames) == first
    _steps(pad, 10, now_us=BLINK_INTERVAL_US)

    assert len(panels.per_key[0].frames) == first + 1


def _parallel_pad(tracer=None):
    """A pad of real `st7735.Panel`s, each on its own DIN line of a fake
    `ParallelBus`. Returns the pad, the fake bus, and the HID device.
    """
    bus = FakeBus(key_count=len(pins.KEYS))
    panels = [bus.panel(index, parallel=True) for index in range(len(pins.KEYS))]
    pad, _, _, _, hid_device, _ = _build_pad(
        panels=panels, push_bus=bus.parallel, tracer=tracer
    )
    return pad, bus, hid_device


def test_parallel_push_starts_the_queued_frames_in_groups_of_three():
    """DoD-3: the frames due in one step start in groups, as many panels at a
    time as the bus allows.
    """
    pad, bus, _ = _parallel_pad()

    pad.step(0)  # power-on: six keys are dirty

    assert bus.groups == [[0, 1, 2], [3, 4, 5]]
    pad.step(1000)
    assert all(bus.frame_count(key) == 1 for key in range(6))


def test_parallel_push_builds_each_group_before_any_cs_goes_low():
    """Building the PIO machines can put an edge on SCK, which a panel with
    CS low reads as a clock, so its frame arrives one bit late.
    """
    pad, bus, _ = _parallel_pad()

    pad.step(0)

    assert bus.parallel.prepared == [[0, 1, 2], [3, 4, 5]]


def test_parallel_push_starts_a_group_of_one_for_one_key():
    pad, bus, hid_device = _parallel_pad()
    pad.step(0)
    pad.step(1000)
    pad.step(2000)

    hid_device.feed(_key_state_report(key_index=4, color=0xF800, emoji_id=0))
    pad.step(3000)

    assert bus.groups[2:] == [[4]]


def test_parallel_push_sends_the_windows_before_any_frame_and_no_commands_after():
    pad, bus, hid_device = _parallel_pad()
    for now_us in (0, 1000, 2000):
        pad.step(now_us)
    commands = [len(bus.commands(key)) for key in range(6)]
    assert all(count == 3 for count in commands)  # CASET, RASET, RAMWR

    hid_device.feed(_key_state_report(key_index=2, color=0xF800, emoji_id=0))
    pad.step(3000)
    pad.step(4000)

    assert [len(bus.commands(key)) for key in range(6)] == commands
    assert bus.image(2) == _solid_frame(0xF800)


def test_parallel_push_lowers_cs_on_the_group_only():
    """DoD-3: no panel outside the group sees CS low."""
    pad, bus, hid_device = _parallel_pad()
    for now_us in (0, 1000, 2000):
        pad.step(now_us)
    cs_log = [list(bus.cs[key].log) for key in range(6)]

    hid_device.feed(_key_state_report(key_index=1, color=0xF800, emoji_id=0))
    hid_device.feed(_key_state_report(key_index=4, color=0x07E0, emoji_id=0))
    pad.step(3000)  # one report per step: key 1 starts alone
    pad.step(4000)
    pad.step(5000)

    for key in (0, 2, 3, 5):
        assert bus.cs[key].log == cs_log[key]
    assert bus.groups[2:] == [[1], [4]]
    assert all(bus.cs[key].value is True for key in range(6))


def test_parallel_push_groups_the_keys_that_wait_while_a_group_is_on_the_wire():
    pad, bus, hid_device = _parallel_pad()
    bus.parallel.auto_finish = False
    pad.step(0)  # the first group is on the wire
    hid_device.feed(_key_state_report(key_index=0, color=0xF800, emoji_id=0))
    pad.step(1000)
    hid_device.feed(_key_state_report(key_index=3, color=0x07E0, emoji_id=0))
    pad.step(2000)
    assert bus.groups == [[0, 1, 2]]

    for now_us in (3000, 4000, 5000):
        bus.finish()
        pad.step(now_us)

    # Key 3 was already queued with its power-on frame, so its update replaced
    # that frame, and key 0 waits for a group of its own.
    assert bus.groups == [[0, 1, 2], [3, 4, 5], [0]]
    assert bus.image(3) == _solid_frame(0x07E0)


def test_parallel_push_traces_each_key_of_a_group():
    tracer = _FakeTracer()
    pad, bus, _ = _parallel_pad(tracer=tracer)

    for now_us in (0, 1000, 2000):
        pad.step(now_us)

    started = [key for code, key, _ in tracer.records if code == tracer_module.PUSH_STARTED]
    done = [key for code, key, _ in tracer.records if code == tracer_module.REFRESH_DONE]
    assert started == [0, 1, 2, 3, 4, 5]
    assert sorted(done) == [0, 1, 2, 3, 4, 5]


def test_parallel_push_sends_a_desynced_group_again_with_its_windows():
    pad, bus, _ = _parallel_pad()
    bus.parallel.fail_groups = 1

    pad.step(0)
    pad.step(1000)  # the group desyncs; the same step sends it again

    assert pad.resyncs == 1
    assert bus.groups[:2] == [[0, 1, 2], [0, 1, 2]]
    assert [len(bus.commands(key)) for key in range(3)] == [6] * 3
    for now_us in (2000, 3000):
        pad.step(now_us)
    assert all(bus.frame_count(key) == 1 for key in range(6))


# --- Task 0061: the frame cache --------------------------------------------


def _store_frame_bytes(slot, color, pixels=None):
    """The raw framed bytes of a Store frame message: the glyph goes out as a
    zlib stream, and a frame of the color alone has no stream."""
    writer = FakeSerial()
    payload = bytes((slot, color & 0xFF, color >> 8))
    if pixels is not None:
        payload += zlib.compress(pixels, 9)
    wire.write_frame(writer, wire.MESSAGE_TYPE_STORE_FRAME, payload)
    return bytes(writer.written)


def _show_frames_bytes(slots):
    writer = FakeSerial()
    wire.write_frame(writer, wire.MESSAGE_TYPE_SHOW_FRAMES, bytes(slots))
    return bytes(writer.written)


def _show_misses(serial):
    """The masks of the Show miss messages the board wrote."""
    return [
        payload[0]
        for message_type, payload in _parse_frames(bytes(serial.written))
        if message_type == wire.MESSAGE_TYPE_SHOW_MISS
    ]


UNCHANGED = wire.SLOT_UNCHANGED


def test_the_pool_is_64_buffers_allocated_before_the_loop():
    pad, _, _, _, _, _ = _build_pad()

    # 52 numbered slots and 12 pip buffers (task 0062), 64 in all (task 0063).
    cache = pad._frame_cache
    assert cache.slot_count == 52
    assert cache.pip_buffers == 12
    assert len(cache._views) == 64
    assert wire.SLOT_UNCHANGED == 0xFF
    assert not any(cache.filled(slot) for slot in range(64))


def test_store_frame_draws_nothing_and_show_frames_draws_the_stored_frame():
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    before = _frame_counts(panels)

    serial.feed(_store_frame_bytes(9, 0x07E0))
    pad.step(1000)
    assert _frame_counts(panels) == before

    serial.feed(_show_frames_bytes([UNCHANGED, 9]))
    pad.step(2000)
    pad.step(2001)

    assert len(panels.per_key[1].frames) == before[1] + 1
    assert _last_frame(panels.per_key[1]) == _solid_frame(0x07E0)
    assert len(panels.per_key[0].frames) == before[0]  # key 0 was unchanged


def test_a_stored_glyph_draws_over_its_color_and_a_transparent_pixel_shows_the_color():
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)

    pixels = _rgb565_pixels_with_transparent_corner(0xF800)
    serial.feed(_store_frame_bytes(4, 0x001F, pixels))
    pad.step(1000)
    serial.feed(_show_frames_bytes([UNCHANGED, UNCHANGED, 4]))
    pad.step(2000)
    pad.step(2001)

    frame = _last_frame(panels.per_key[2])
    assert _frame_pixel(frame, 0) == 0x001F  # the transparent corner
    assert _frame_pixel(frame, 1) == 0xF800
    assert _frame_pixel(frame, PIXEL_COUNT - 1) == 0xF800


def test_a_store_into_a_used_slot_replaces_it_for_the_next_show():
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)

    serial.feed(_store_frame_bytes(0, 0x1111))
    pad.step(1000)
    serial.feed(_store_frame_bytes(0, 0x2222))
    pad.step(2000)
    serial.feed(_show_frames_bytes([0]))
    pad.step(3000)
    pad.step(3001)

    assert _last_frame(panels.per_key[0]) == _solid_frame(0x2222)


def test_a_store_allocates_no_bitmap():
    import displayio

    created = []

    class CountingBitmap(displayio.Bitmap):
        def __init__(self, *args, **kwargs):
            created.append(1)
            super().__init__(*args, **kwargs)

    pad, _, _, _, _, serial = _build_pad()
    _paint_power_on(pad)

    original = displayio.Bitmap
    displayio.Bitmap = CountingBitmap
    try:
        for slot in range(3):
            serial.feed(
                _store_frame_bytes(
                    slot, 0xF800, _rgb565_pixels_with_transparent_corner(0x07E0)
                )
            )
            pad.step(1000 + slot)
    finally:
        displayio.Bitmap = original

    assert created == []


def test_a_show_of_six_stored_frames_queues_every_push_in_one_pass_and_traces_it():
    tracer = _FakeTracer()
    pad, bus, _ = _parallel_pad(tracer=tracer)
    serial = pad._serial
    _paint_power_on(pad)
    for key in range(6):
        serial.feed(_store_frame_bytes(10 + key, 0x0100 * (key + 1)))
        pad.step(1000 + key)
    groups_before = len(bus.groups)
    del tracer.records[:]

    serial.feed(_show_frames_bytes([10, 11, 12, 13, 14, 15]))
    pad.step(5000)
    pad.step(5001)

    assert bus.groups[groups_before:] == [[0, 1, 2], [3, 4, 5]]
    shown = [p for code, _, p in tracer.records if code == tracer_module.FRAMES_SHOWN]
    assert shown == [ALL_KEYS]
    assert tracer_module.FRAMES_SHOWN == 11


def test_a_show_that_names_an_empty_slot_draws_the_other_keys_keeps_the_old_image_and_sends_a_miss():
    """DoD-4."""
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_store_frame_bytes(1, 0x07E0))
    pad.step(1000)
    old_key2 = _last_frame(panels.per_key[2])
    counts = _frame_counts(panels)

    # Key 0 names stored slot 1, key 2 names empty slot 50, key 4 names slot 200.
    serial.feed(_show_frames_bytes([1, UNCHANGED, 50, UNCHANGED, 200]))
    pad.step(2000)
    pad.step(2001)

    assert _last_frame(panels.per_key[0]) == _solid_frame(0x07E0)
    assert len(panels.per_key[0].frames) == counts[0] + 1
    assert _frame_counts(panels)[2] == counts[2]
    assert _last_frame(panels.per_key[2]) == old_key2
    assert _frame_counts(panels)[4] == counts[4]
    assert _show_misses(serial) == [0b010100]


def test_a_show_with_no_miss_writes_no_miss_message():
    pad, _, _, _, _, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_store_frame_bytes(0, 0x1234))
    pad.step(1000)
    serial.feed(_show_frames_bytes([0]))
    pad.step(2000)

    assert _show_misses(serial) == []


def test_a_store_into_a_slot_that_is_on_the_wire_waits_for_the_push():
    panels = _slow_panels(polls_to_finish=3)
    pad, _, _, _, _, serial = _build_pad(panels=panels)
    for _ in range(40):
        pad.step(0)  # the power-on paint
    serial.feed(_store_frame_bytes(7, 0x1111))
    pad.step(1000)
    serial.feed(_show_frames_bytes([7]))
    pad.step(2000)
    assert panels.per_key[0].busy

    serial.feed(_store_frame_bytes(7, 0x2222))
    pad.step(3000)

    # The frame on the wire reached the panel whole, in the first color.
    assert panels.per_key[0].sent[-1] == _solid_frame(0x1111)
    serial.feed(_show_frames_bytes([7]))
    for _ in range(10):
        pad.step(4000)
    assert panels.per_key[0].sent[-1] == _solid_frame(0x2222)


def test_a_store_of_a_slot_above_127_is_dropped():
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    counts = _frame_counts(panels)
    serial.feed(_store_frame_bytes(128, 0x1234))
    pad.step(1000)
    serial.feed(_show_frames_bytes([128]))
    pad.step(2000)

    assert _frame_counts(panels) == counts
    assert _show_misses(serial) == [0b1]


def test_a_store_frame_that_does_not_inflate_changes_no_slot():
    pad, _, _, _, _, serial = _build_pad()
    _paint_power_on(pad)
    writer = FakeSerial()
    wire.write_frame(
        writer, wire.MESSAGE_TYPE_STORE_FRAME, bytes((3, 0, 0)) + b"junk junk"
    )
    serial.feed(bytes(writer.written))
    pad.step(1000)

    assert not pad._frame_cache.filled(3)


def test_a_key_state_with_the_same_color_keeps_a_shown_frame_and_a_built_in_emoji_drops_it():
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    pixels = _rgb565_pixels_with_transparent_corner(0xF800)
    serial.feed(_store_frame_bytes(2, 0x001F, pixels))
    pad.step(1000)
    serial.feed(_show_frames_bytes([UNCHANGED, UNCHANGED, 2]))
    pad.step(2000)
    shown = _last_frame(panels.per_key[2])
    calls = len(bitmaptools.calls)

    hid_device.feed(
        _key_state_report(2, 0x001F, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID)
    )
    pad.step(3000)
    assert _last_frame(panels.per_key[2]) == shown
    assert len(bitmaptools.calls) == calls  # nothing composed

    hid_device.feed(_key_state_report(2, 0x001F, 5))  # a built-in Emoji ID
    pad.step(4000)
    pad.step(4001)
    assert _last_frame(panels.per_key[2]) == _solid_frame(0x001F)


def test_a_key_that_blinks_is_not_cached_and_blinks_as_before():
    """DoD-6: a Set custom glyph and a blinking key state use the paths of
    tasks 0044 and 0048. No slot of the pool is written."""
    pad, _, panels, _, hid_device, serial = _build_pad()
    pad.step(0)
    pad.step(1)
    pixels = _rgb565_pixels_with_transparent_corner(0xF800)
    serial.feed(_custom_glyph_frame_with_pixels(key_index=4, pixels=pixels))
    pad.step(2000)
    hid_device.feed(
        _key_state_report(
            4, 0x07E0, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID, blink=True
        )
    )
    pad.step(3000)
    first = _last_frame(panels.per_key[4])
    pad.step(3000 + BLINK_INTERVAL_US)
    second = _last_frame(panels.per_key[4])
    pad.step(3000 + 2 * BLINK_INTERVAL_US)

    assert {_frame_pixel(first, 0), _frame_pixel(second, 0)} == {0x07E0, 0x0000}
    assert _last_frame(panels.per_key[4]) == first
    assert not any(pad._frame_cache.filled(slot) for slot in range(64))


def test_a_shown_frame_that_then_blinks_alternates_with_black():
    pad, _, panels, _, hid_device, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_store_frame_bytes(0, 0x07E0))
    pad.step(1000)
    serial.feed(_show_frames_bytes([0]))
    pad.step(2000)

    hid_device.feed(_key_state_report(0, 0x07E0, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID, blink=True))
    pad.step(3000)
    first = _last_frame(panels.per_key[0])
    pad.step(3000 + BLINK_INTERVAL_US)
    second = _last_frame(panels.per_key[0])

    assert {first, second} == {_solid_frame(0x07E0), _solid_frame(0x0000)}


def test_a_show_releases_an_open_batch_first():
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_begin_batch_frame(0, 0b1))
    pad.step(1000)
    serial.feed(_store_frame_bytes(3, 0x4444))
    pad.step(2000)
    serial.feed(_show_frames_bytes([UNCHANGED, 3]))
    pad.step(3000)
    pad.step(3001)

    assert pad._batch is None
    assert _last_frame(panels.per_key[1]) == _solid_frame(0x4444)


# --- The second core (task 0059) ---------------------------------------------

from fake_glyphcore import FakeGlyphcore  # noqa: E402
import frame_cache as frame_cache_module  # noqa: E402


def _use_core_1(monkeypatch, core1):
    monkeypatch.setattr(wire, "glyphcore", core1)
    monkeypatch.setattr(display_render, "glyphcore", core1)
    monkeypatch.setattr(frame_cache_module, "glyphcore", core1)


def test_a_glyph_inflated_on_core_1_draws_the_same_frame_as_one_inflated_on_core_0(
    monkeypatch,
):
    pixels = _rgb565_pixels_with_transparent_corner(0x07E0)
    stream = zlib.compress(pixels, 9)

    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)
    serial.feed(_compressed_glyph_frame(2, stream))
    pad.step(1000)
    expected = _last_frame(panels.per_key[2])

    _use_core_1(monkeypatch, FakeGlyphcore(polls_before_done=2))
    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)
    serial.feed(_compressed_glyph_frame(2, stream))
    for step in range(1, 8):
        pad.step(step * 1000)

    assert _last_frame(panels.per_key[2]) == expected
    assert _frame_pixel(expected, 0) == 0x0000  # the key's color shows through
    assert _frame_pixel(expected, 1) == 0x07E0
    assert pad.key_states[2].emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID


def test_the_loop_keeps_scanning_while_core_1_inflates(monkeypatch):
    core1 = FakeGlyphcore(polls_before_done=50)
    _use_core_1(monkeypatch, core1)
    pad, switches, _, _, hid_device, serial = _build_pad()
    pad.step(0)

    serial.feed(_compressed_glyph_frame(2, zlib.compress(_rgb565_solid_pixels(0x1234), 9)))
    pad.step(1000)  # submitted; core 1 has not finished
    hid_device.feed(_key_state_report(key_index=4, color=0xF81F, emoji_id=7))
    pad.step(2000)

    assert pad.key_states[2].pixels is None
    assert pad.key_states[4].color == 0xF81F


def test_a_glyph_that_core_1_never_finishes_is_dropped_and_the_key_keeps_its_image(
    monkeypatch,
):
    core1 = FakeGlyphcore(polls_before_done=0)
    _use_core_1(monkeypatch, core1)
    monkeypatch.setattr(wire, "INFLATE_TIMEOUT_NS", 0)
    pad, _, panels, _, hid_device, serial = _build_pad()
    pad.step(0)
    first = _rgb565_solid_pixels(0x1234)
    serial.feed(_compressed_glyph_frame(3, zlib.compress(first, 9)))
    for step in range(1, 4):
        pad.step(step * 1000)
    assert bytes(pad.key_states[3].pixels) == first
    frames_before = len(panels.per_key[3].frames)

    core1.never_answers = True
    serial.feed(_compressed_glyph_frame(3, zlib.compress(_rgb565_solid_pixels(0x5678), 9)))
    for step in range(4, 10):
        pad.step(step * 1000)

    assert bytes(pad.key_states[3].pixels) == first
    assert len(panels.per_key[3].frames) == frames_before
    # The loop goes on: the next key state still draws.
    hid_device.feed(_key_state_report(key_index=0, color=0xF81F, emoji_id=7))
    pad.step(11_000)
    assert _last_frame(panels.per_key[0]) == _solid_frame(0xF81F)


def test_a_store_frame_inflated_on_core_1_fills_the_slot_in_module_memory(monkeypatch):
    core1 = FakeGlyphcore(polls_before_done=1)
    _use_core_1(monkeypatch, core1)
    pad, _, _, _, _, serial = _build_pad()
    pad.step(0)
    pixels = _rgb565_solid_pixels(0x07E0)
    writer = FakeSerial()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_STORE_FRAME,
        bytes((5, 0x1F, 0x00)) + zlib.compress(pixels, 9),
    )
    serial.feed(bytes(writer.written))

    for step in range(1, 6):
        pad.step(step * 1000)

    assert bytes(core1.slots[5]) == pixels


# --- Key state over CDC (task 0059) --------------------------------------------


def _key_state_frame(key_index, color, emoji_id, blink=False):
    writer = FakeSerial()
    wire.write_frame(
        writer,
        wire.MESSAGE_TYPE_KEY_STATE,
        _key_state_report(key_index, color, emoji_id, blink),
    )
    return bytes(writer.written)


def test_a_key_state_frame_on_cdc_updates_one_key_like_a_hid_report():
    pad, _, panels, _, hid_device, serial = _build_pad()
    pad.step(0)

    serial.feed(_key_state_frame(key_index=3, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert pad.key_states[3].color == 0xF81F
    assert pad.key_states[3].emoji_id == 0xA2
    assert _last_frame(panels.per_key[3]) == _solid_frame(0xF81F)
    assert hid_device.queued == []


def test_a_key_state_frame_of_another_version_is_dropped():
    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)
    bad = FakeSerial()
    wire.write_frame(
        bad,
        wire.MESSAGE_TYPE_KEY_STATE,
        _key_state_report(3, 0xF81F, 0xA2, version=wire.PROTOCOL_VERSION + 1),
    )

    serial.feed(bytes(bad.written))
    pad.step(1000)

    assert pad.key_states[3].color == DEFAULT_COLOR
    assert len(panels.per_key[3].frames) == 1


def test_a_key_state_frame_for_an_unknown_key_is_ignored():
    pad, _, _, _, _, serial = _build_pad()
    pad.step(0)

    serial.feed(_key_state_frame(key_index=99, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert all(state.color == DEFAULT_COLOR for state in pad.key_states)


def test_a_key_state_frame_is_traced_like_a_report():
    tracer = tracer_module.Tracer(capacity=32, enabled=True)
    pad, _, _, _, _, serial = _build_pad(tracer=tracer)
    pad.step(0)

    serial.feed(_key_state_frame(key_index=2, color=0x001F, emoji_id=7))
    pad.step(1000)

    records = [
        _decode_trace_record(payload)
        for message_type, payload in _parse_frames(bytes(serial.written))
        if message_type == wire.MESSAGE_TYPE_TRACE
    ]
    assert any(
        code == tracer_module.HOST_MESSAGE_DECODED and key == 2 and trace_payload == 7
        for code, key, trace_payload, _ in records
    )


def test_a_batch_completes_from_key_states_on_cdc_in_order_with_its_glyphs(monkeypatch):
    core1 = FakeGlyphcore(polls_before_done=2)
    _use_core_1(monkeypatch, core1)
    pad, _, panels, _, _, serial = _build_pad()
    pad.step(0)
    pixels = _rgb565_solid_pixels(0x1234)
    begin = FakeSerial()
    wire.write_frame(begin, wire.MESSAGE_TYPE_BEGIN_BATCH, bytes((0b100, 0b100)))
    serial.feed(
        bytes(begin.written)
        + _compressed_glyph_frame(2, zlib.compress(pixels, 9))
        + _key_state_frame(2, 0x0000, wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID)
    )

    drawn_before_release = None
    for step in range(1, 12):
        pad.step(step * 1000)
        if pad._batch is not None:
            drawn_before_release = len(panels.per_key[2].frames)

    assert pad._batch is None
    assert drawn_before_release == 1  # held until both arrived
    assert len(panels.per_key[2].frames) == 2
    assert _frame_pixel(_last_frame(panels.per_key[2]), 0) == 0x1234


# --- The count pip (task 0062) ------------------------------------------------

PIP_COLORS = [0x3AFF, 0x16C9, 0xF800, 0xFFE0, 0xFFFF]


def _pip_sheet_pixels():
    """A sheet with one value for each sprite: disc i is 0x1100 + i, the big
    cell of character index c is 0x2200 + c, and the small 9 and + are 0x3309
    and 0x330A. The top-left pixel of each disc is transparent."""
    sheet = bytearray(32768)

    def fill(x1, y1, x2, y2, value):
        for y in range(y1, y2):
            for x in range(x1, x2):
                offset = (y * 256 + x) * 2
                sheet[offset] = value >> 8
                sheet[offset + 1] = value & 0xFF

    for disc in range(5):
        fill(44 * disc, 0, 44 * (disc + 1), 44, 0x1100 + disc)
        sheet[(44 * disc) * 2 : (44 * disc) * 2 + 2] = b"\x00\x00"
    for index in range(11):
        fill(14 * index, 44, 14 * (index + 1), 64, 0x2200 + index)
    fill(154, 44, 164, 58, 0x3309)
    fill(164, 44, 174, 58, 0x330A)
    return bytes(sheet)


def _pip_sheet_bytes(colors=PIP_COLORS):
    writer = FakeSerial()
    payload = bytes((len(colors),))
    for color in colors:
        payload += bytes((color & 0xFF, color >> 8))
    payload += zlib.compress(_pip_sheet_pixels(), 9)
    wire.write_frame(writer, wire.MESSAGE_TYPE_STORE_PIP_SHEET, payload)
    return bytes(writer.written)


def _show_frames_pips_bytes(slots, counts):
    writer = FakeSerial()
    payload = b"".join(bytes((slot, count)) for slot, count in zip(slots, counts))
    wire.write_frame(writer, wire.MESSAGE_TYPE_SHOW_FRAMES_PIPS, payload)
    return bytes(writer.written)


def _pip_pad(color=0xF800):
    """A pad that holds the pip sheet and one stored frame, in slot 3: a glyph
    of 0x07E0 over `color` with a transparent top-left pixel."""
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_pip_sheet_bytes())
    pad.step(900)
    serial.feed(_store_frame_bytes(3, color, _rgb565_pixels_with_transparent_corner(0x07E0)))
    pad.step(1000)
    return pad, panels, serial


def _show_pips(pad, serial, counts, slot=3, now=2000):
    slots = [slot] + [UNCHANGED] * 5
    serial.feed(_show_frames_pips_bytes(slots, counts + [0] * (6 - len(counts))))
    pad.step(now)
    pad.step(now + 1)


def _panel_pixel(frame, x, y):
    return _frame_pixel(frame, y * 128 + x)


def test_a_show_with_pips_stamps_the_pip_on_a_copy_and_leaves_the_slot_unchanged():
    """DoD-4: counts 1, 42, and 100 differ from the slot only inside the pip box."""
    pad, panels, serial = _pip_pad()
    slot_before = bytes(pad._frame_cache.view(3))

    for step, count in enumerate((1, 42, 100)):
        _show_pips(pad, serial, [count], now=2000 + 10 * step)
        frame = _last_frame(panels.per_key[0])
        differs = 0
        for y in range(128):
            for x in range(128):
                inside = 80 <= x < 124 and 80 <= y < 124
                index = y * 128 + x
                same = frame[2 * index : 2 * index + 2] == slot_before[2 * index : 2 * index + 2]
                if not inside:
                    assert same, "count {} changed ({}, {}) outside the pip box".format(count, x, y)
                differs += not same
        assert differs > 500, "count {} drew too little".format(count)
        assert bytes(pad._frame_cache.view(3)) == slot_before


def test_the_pip_is_the_disc_of_the_slots_color_with_the_digits_of_the_count():
    pad, panels, serial = _pip_pad(color=0xF800)

    _show_pips(pad, serial, [7])
    frame = _last_frame(panels.per_key[0])
    assert _panel_pixel(frame, 0, 0) == 0xF800  # the corner shows the key color
    assert _panel_pixel(frame, 85, 85) == 0x1102  # disc 2 is the one for 0xF800
    assert _panel_pixel(frame, 80, 80) == 0x07E0  # a transparent disc pixel keeps the slot's
    assert _panel_pixel(frame, 95, 92) == 0x2207  # one big cell, centred

    _show_pips(pad, serial, [42], now=3000)
    frame = _last_frame(panels.per_key[0])
    assert _panel_pixel(frame, 88, 92) == 0x2204
    assert _panel_pixel(frame, 102, 92) == 0x2202


def test_a_pip_on_a_color_the_sheet_has_no_disc_for_uses_the_first_disc():
    pad, panels, serial = _pip_pad(color=0x1234)

    _show_pips(pad, serial, [7])

    assert _panel_pixel(_last_frame(panels.per_key[0]), 85, 85) == 0x1100


def test_a_pip_count_above_99_draws_99_plus_and_a_count_of_0_shows_the_slot_itself():
    """DoD-4 and the 100 case of DoD-4's sibling: 99+ in small cells; 0, no pip."""
    pad, panels, serial = _pip_pad()

    _show_pips(pad, serial, [100])
    hundred = _last_frame(panels.per_key[0])
    assert _panel_pixel(hundred, 87, 95) == 0x3309
    assert _panel_pixel(hundred, 97, 95) == 0x3309
    assert _panel_pixel(hundred, 107, 95) == 0x330A

    _show_pips(pad, serial, [255], now=3000)
    assert _last_frame(panels.per_key[0]) == hundred

    _show_pips(pad, serial, [0], now=4000)
    assert pad.key_states[0]._on_frame is pad._frame_cache.view(3)
    assert _panel_pixel(_last_frame(panels.per_key[0]), 85, 85) == 0x07E0


def test_a_pip_without_sheet_draws_the_slot_with_no_pip():
    """DoD-5."""
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_store_frame_bytes(3, 0xF800, _rgb565_pixels_with_transparent_corner(0x07E0)))
    pad.step(1000)

    _show_pips(pad, serial, [12])

    assert pad.key_states[0]._on_frame is pad._frame_cache.view(3)
    assert _panel_pixel(_last_frame(panels.per_key[0]), 85, 85) == 0x07E0
    assert _show_misses(serial) == []


def test_a_pip_on_a_frame_with_no_picture_still_draws_and_marks_the_key_as_a_glyph():
    pad, _, panels, _, _, serial = _build_pad()
    _paint_power_on(pad)
    serial.feed(_pip_sheet_bytes())
    pad.step(900)
    serial.feed(_store_frame_bytes(3, 0xF800))
    pad.step(1000)

    _show_pips(pad, serial, [3])

    assert _panel_pixel(_last_frame(panels.per_key[0]), 85, 85) == 0x1102
    assert pad.key_states[0].emoji_id == wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID


def test_two_pip_counts_in_a_row_use_the_two_buffers_of_the_key_in_turn():
    pad, panels, serial = _pip_pad()
    cache = pad._frame_cache

    _show_pips(pad, serial, [1])
    first = pad.key_states[0]._on_frame
    first_bytes = bytes(first)
    _show_pips(pad, serial, [2], now=3000)
    second = pad.key_states[0]._on_frame

    assert first is not second
    assert first is cache.view(cache.pip_buffer(0, 0))
    assert second is cache.view(cache.pip_buffer(0, 1))
    assert bytes(first) == first_bytes  # the stamp of count 2 did not touch it
    _show_pips(pad, serial, [3], now=4000)
    assert pad.key_states[0]._on_frame is first  # back to the first buffer


def test_a_show_with_pips_of_a_slot_above_the_numbered_ones_is_a_miss():
    pad, panels, serial = _pip_pad()

    serial.feed(_store_frame_bytes(52, 0x1234))  # a pip buffer, not a slot
    pad.step(2000)
    _show_pips(pad, serial, [5], slot=52, now=3000)

    assert not pad._frame_cache.filled(52)
    assert _show_misses(serial) == [0b1]


def test_a_show_with_pips_draws_every_named_key_in_one_pass_and_traces_it():
    tracer = _FakeTracer()
    pad, bus, _ = _parallel_pad(tracer=tracer)
    serial = pad._serial
    _paint_power_on(pad)
    serial.feed(_pip_sheet_bytes())
    pad.step(900)
    for key in range(6):
        serial.feed(_store_frame_bytes(10 + key, 0xF800))
        pad.step(1000 + key)
    groups_before = len(bus.groups)
    del tracer.records[:]

    serial.feed(_show_frames_pips_bytes([10, 11, 12, 13, 14, 15], [1, 2, 3, 4, 5, 6]))
    pad.step(5000)
    pad.step(5001)

    assert bus.groups[groups_before:] == [[0, 1, 2], [3, 4, 5]]
    shown = [p for code, _, p in tracer.records if code == tracer_module.FRAMES_SHOWN]
    assert shown == [ALL_KEYS]
    codes = [code for code, _, _ in tracer.records]
    started = [p for code, _, p in tracer.records if code == tracer_module.SHOW_WITH_PIPS_STARTED]
    assert started == [ALL_KEYS]
    assert codes.index(tracer_module.SHOW_WITH_PIPS_STARTED) < codes.index(tracer_module.FRAMES_SHOWN)
    assert tracer_module.SHOW_WITH_PIPS_STARTED == 13
