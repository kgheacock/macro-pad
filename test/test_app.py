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


def test_custom_glyph_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, _, serial = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    serial.feed(_custom_glyph_frame(key_index=0, fill_byte=0xAB))
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)












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


def test_parallel_push_starts_every_queued_frame_in_one_group():
    """DoD-3: the frames due in one step start in one group."""
    pad, bus, _ = _parallel_pad()

    pad.step(0)  # power-on: six keys are dirty

    assert bus.groups == [[0, 1, 2, 3, 4, 5]]
    pad.step(1000)
    assert all(bus.frame_count(key) == 1 for key in range(6))


def test_parallel_push_sends_the_windows_before_any_frame_and_no_commands_after():
    pad, bus, hid_device = _parallel_pad()
    pad.step(0)
    pad.step(1000)
    commands = [len(bus.commands(key)) for key in range(6)]
    assert all(count == 3 for count in commands)  # CASET, RASET, RAMWR

    hid_device.feed(_key_state_report(key_index=2, color=0xF800, emoji_id=0))
    pad.step(2000)
    pad.step(3000)

    assert [len(bus.commands(key)) for key in range(6)] == commands
    assert bus.image(2) == _solid_frame(0xF800)


def test_parallel_push_lowers_cs_on_the_group_only():
    """DoD-3: no panel outside the group sees CS low."""
    pad, bus, hid_device = _parallel_pad()
    pad.step(0)
    pad.step(1000)
    cs_log = [list(bus.cs[key].log) for key in range(6)]

    hid_device.feed(_key_state_report(key_index=1, color=0xF800, emoji_id=0))
    hid_device.feed(_key_state_report(key_index=4, color=0x07E0, emoji_id=0))
    pad.step(2000)  # one report per step: key 1 starts alone
    pad.step(3000)
    pad.step(4000)

    for key in (0, 2, 3, 5):
        assert bus.cs[key].log == cs_log[key]
    assert bus.groups[1:] == [[1], [4]]
    assert all(bus.cs[key].value is True for key in range(6))


def test_parallel_push_groups_the_keys_that_wait_while_a_group_is_on_the_wire():
    pad, bus, hid_device = _parallel_pad()
    bus.parallel.auto_finish = False
    pad.step(0)  # the first group is on the wire
    hid_device.feed(_key_state_report(key_index=0, color=0xF800, emoji_id=0))
    pad.step(1000)
    hid_device.feed(_key_state_report(key_index=3, color=0x07E0, emoji_id=0))
    pad.step(2000)
    assert bus.groups == [[0, 1, 2, 3, 4, 5]]

    bus.finish()
    pad.step(3000)

    assert bus.groups == [[0, 1, 2, 3, 4, 5], [0, 3]]


def test_parallel_push_traces_each_key_of_a_group():
    tracer = _FakeTracer()
    pad, bus, _ = _parallel_pad(tracer=tracer)

    pad.step(0)
    pad.step(1000)

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
    assert bus.groups == [[0, 1, 2, 3, 4, 5], [0, 1, 2, 3, 4, 5]]
    assert [len(bus.commands(key)) for key in range(6)] == [6] * 6
    pad.step(2000)
    assert all(bus.frame_count(key) == 1 for key in range(6))
