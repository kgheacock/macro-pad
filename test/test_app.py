import board
import displayio

# Imported flat, under the names the board uses. See conftest.py.
import pins
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
    """CDC data endpoint that keeps every byte written to it."""

    def __init__(self):
        self.written = bytearray()

    def write(self, data):
        self.written.extend(data)
        return len(data)


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


def _build_pad(idle_timer=None):
    switches = [make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS]
    displays = [FakeDisplay() for _ in pins.KEYS]
    backlights = [FakeBacklight() for _ in pins.KEYS]
    hid_device = FakeHID()
    serial = FakeSerial()
    emoji_lookup = RecordingEmojiLookup()

    pad = MacroPad(
        switches=switches,
        displays=displays,
        backlights=backlights,
        hid_device=hid_device,
        serial=serial,
        emoji_lookup=emoji_lookup,
        idle_timer=idle_timer,
    )
    return pad, switches, displays, backlights, hid_device, serial, emoji_lookup


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


def test_key_state_applies_to_one_key():
    pad, _, displays, _, hid_device, _, emoji_lookup = _build_pad()

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
    pad, _, displays, _, hid_device, _, _ = _build_pad()

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
    pad, _, _, _, hid_device, _, _ = _build_pad()

    pad.step(0)
    hid_device.feed(_key_state_report(key_index=99, color=0xF81F, emoji_id=0xA2))
    pad.step(1000)

    assert all(state.color == DEFAULT_COLOR for state in pad.key_states)


def test_key_state_accepts_report_with_report_id_prefix():
    pad, _, _, _, hid_device, _, _ = _build_pad()

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
    pad, switches, _, _, _, serial, _ = _build_pad()

    pad.step(0)  # every switch open
    assert serial.written == b""

    switches[0].value = False  # pull-up: closed switch reads low
    pad.step(DEBOUNCE_WINDOW_US * 2)

    assert len(serial.written) == wire.EVENT_SIZE
    assert bytes(serial.written) == wire.encode_event(
        0, wire.PRESS, DEBOUNCE_WINDOW_US * 2
    )


def test_release_writes_event():
    pad, switches, _, _, _, serial, _ = _build_pad()

    pad.step(0)
    switches[2].value = False
    pad.step(DEBOUNCE_WINDOW_US * 2)
    switches[2].value = True
    pad.step(DEBOUNCE_WINDOW_US * 4)

    assert len(serial.written) == wire.EVENT_SIZE * 2
    assert bytes(serial.written[wire.EVENT_SIZE :]) == wire.encode_event(
        2, wire.RELEASE, DEBOUNCE_WINDOW_US * 4
    )


def test_bounce_writes_one_event():
    pad, switches, _, _, _, serial, _ = _build_pad()

    pad.step(0)

    # Five contact bounces, all inside one debounce window.
    for bounce in range(5):
        switches[1].value = bounce % 2 == 1  # low, high, low, high, low
        pad.step(100 + bounce * 100)

    assert len(serial.written) == wire.EVENT_SIZE
    assert bytes(serial.written) == wire.encode_event(1, wire.PRESS, 100)


def test_idle_dims_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, _, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(0)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)

    pad.step(4999)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)


def test_key_event_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, switches, _, backlights, _, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    switches[4].value = False
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def test_host_message_wakes_backlight():
    idle_timer = IdleTimer(idle_window_us=5000)
    pad, _, _, backlights, hid_device, _, _ = _build_pad(idle_timer=idle_timer)

    pad.step(5000)
    assert all(backlight.duty_cycle == 0.1 for backlight in backlights)

    hid_device.feed(_key_state_report(key_index=0, color=0xF800, emoji_id=1))
    pad.step(6000)
    assert all(backlight.duty_cycle == 1.0 for backlight in backlights)


def test_blink_key_redraws_every_step():
    pad, _, displays, _, hid_device, _, _ = _build_pad()

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
