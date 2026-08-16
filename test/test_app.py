import board
import displayio

# Imported flat, under the names the board uses. See conftest.py.
import glyph_state
import pins
import tracer as tracer_module
import wire
from app import DEFAULT_COLOR, DEFAULT_EMOJI_ID, Backlight, MacroPad, make_switch
from idle_timer import IdleTimer

DEBOUNCE_WINDOW_US = 7500  # the Debouncer's 7.5 ms default


class FakeDisplay:
    """Records what a real `ST7735R` would have been told to show."""

    def __init__(self):
        self.shown_groups = []

    def show(self, group):
        self.shown_groups.append(group)

    def refresh(self, **kwargs):
        return True


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

    def __call__(self, emoji_id):
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
    displays = [FakeDisplay() for _ in pins.KEYS]
    backlights = [FakeBacklight() for _ in pins.KEYS]
    hid_device = FakeHID()
    serial = FakeSerial()
    emoji_lookup = RecordingEmojiLookup()
    storage = storage if storage is not None else FakeGlyphStorage()

    pad = MacroPad(
        switches=switches,
        displays=displays,
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


def _custom_glyph_frame(key_index, fill_byte):
    """Build the raw framed bytes a host would write to CDC for one Set
    custom glyph message, ready to feed to a `FakeSerial`.
    """
    writer = FakeSerial()
    payload = bytes((key_index,)) + _custom_glyph_pixels(fill_byte)
    wire.write_frame(writer, wire.MESSAGE_TYPE_SET_CUSTOM_GLYPH, payload)
    return bytes(writer.written)


def test_key_state_applies_to_one_key():
    pad, _, displays, _, hid_device, _, emoji_lookup, _ = _build_pad()

    pad.step(0)  # power-on paint of all six keys
    emoji_lookup.requested_ids.clear()

    hid_device.feed(_key_state_report(key_index=3, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert pad.key_states[3].color == 0xF81F
    assert pad.key_states[3].emoji_id == 0xA2
    assert _background_color(displays[3]) == 0xF81F
    assert len(displays[3].shown_groups) == 2
    assert emoji_lookup.requested_ids == [0xA2]

    for index in (0, 1, 2, 4, 5):
        assert pad.key_states[index].color == DEFAULT_COLOR
        assert pad.key_states[index].emoji_id == DEFAULT_EMOJI_ID
        assert len(displays[index].shown_groups) == 1  # not redrawn


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
    assert len(displays[3].shown_groups) == 1  # not redrawn


def test_key_state_ignores_unknown_key_index():
    pad, _, _, _, hid_device, _, _, _ = _build_pad()

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=99, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert all(state.color == DEFAULT_COLOR for state in pad.key_states)


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


def test_blink_key_redraws_every_step():
    pad, _, displays, _, hid_device, _, _, _ = _build_pad()

    pad.step(0)
    hid_device.feed(
        _key_state_report(key_index=5, color=0x001F, emoji_id=7, blink=True)
    )
    pad.step(1000)
    pad.step(2000)
    pad.step(3000)

    assert len(displays[5].shown_groups) == 4
    assert len(displays[0].shown_groups) == 1


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
    assert len(displays[3].shown_groups) == 2

    for index in (0, 1, 2, 4, 5):
        assert pad.key_states[index].pixels is None
        assert len(displays[index].shown_groups) == 1  # not redrawn


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
    assert len(displays[2].shown_groups) == 1  # not yet redrawn

    serial.feed(frame[split:])
    pad.step(2000)
    assert pad.key_states[2].pixels == _custom_glyph_pixels(0xCD)
    assert len(displays[2].shown_groups) == 2


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
    assert len(displays[1].shown_groups) == 1


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
