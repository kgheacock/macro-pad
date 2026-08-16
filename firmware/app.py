"""The main loop that wires the firmware's modules into one device.

`MacroPad` takes every hardware object as a constructor argument, so the
whole loop runs under pytest against the stubs in `test/stubs/` with no
board attached. `code.py` builds the real objects and calls `run()`.

One `step(now_us)` call is one iteration, and every event it emits
carries that same timestamp — `docs/wire-protocol.md` defines the event
timestamp as monotonic microseconds, and a single reading per iteration
keeps the events of one scan consistent with each other.

Imports here are flat (`import wire`, not `from firmware import wire`)
because `firmware/`'s contents are copied to the root of the `CIRCUITPY`
drive, where there is no `firmware` package. `test/conftest.py` puts the
folder on `sys.path` so tests resolve the same names.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import time

import digitalio
import displayio

import display_render
import glyph_state
import tracer as tracer_module
import wire
from debounce import Debouncer
from idle_timer import IdleTimer

DEFAULT_COLOR = 0x0000
DEFAULT_EMOJI_ID = 0


class Backlight:
    """One key's PWM backlight, addressed as a 0.0 to 1.0 fraction.

    `IdleTimer` speaks in fractions, while `pwmio.PWMOut.duty_cycle` is a
    16-bit integer. This converts between the two and remembers the
    fraction, so a caller can read back the level it set.
    """

    _FULL_SCALE = 0xFFFF

    def __init__(self, pwm):
        self._pwm = pwm
        self._duty_cycle = 0.0
        self.duty_cycle = 1.0

    @property
    def duty_cycle(self):
        return self._duty_cycle

    @duty_cycle.setter
    def duty_cycle(self, fraction):
        self._duty_cycle = fraction
        self._pwm.duty_cycle = int(fraction * self._FULL_SCALE)


def make_switch(pin):
    """Build one switch input with its pull-up enabled.

    This lives here, not in `code.py`, so that file stays pure object
    construction, and so the pull-up choice sits beside the inverted read
    in `MacroPad._scan_switches` that depends on it.
    """
    switch = digitalio.DigitalInOut(pin)
    switch.switch_to_input(pull=digitalio.Pull.UP)
    return switch


def blank_glyph(emoji_id):
    """Stand-in emoji lookup used until task 0023 generates the real one.

    It returns an empty 1x1 tile so a board with no glyph table still
    renders each key's background color instead of failing at import.
    """
    palette = displayio.Palette(1)
    palette[0] = DEFAULT_COLOR
    return displayio.TileGrid(displayio.Bitmap(1, 1, 1), pixel_shader=palette)


class MacroPad:
    """The whole device: six switches, six displays, six backlights.

    Every argument is injected rather than built here, which is what lets
    a test drive `step` one iteration at a time with fakes.
    """

    def __init__(
        self,
        switches,
        displays,
        backlights,
        hid_device,
        serial,
        emoji_lookup,
        debounce_window_ms=7.5,
        idle_timer=None,
        tracer=None,
        storage=None,
    ):
        self._switches = switches
        self._displays = displays
        self._backlights = backlights
        self._hid_device = hid_device
        self._serial = serial
        self._emoji_lookup = emoji_lookup
        self._tracer = tracer
        self._storage = storage if storage is not None else glyph_state.FilesystemStorage()
        self._custom_glyph_reader = wire.CustomGlyphReader()

        self._debouncers = [Debouncer(debounce_window_ms) for _ in switches]
        # The switch's own initial reading, not a sentinel, so the first
        # `step` traces nothing for a switch that has not moved — a trace
        # marks an edge on the pin, not the fact that it was read at all.
        self._last_raw = [not switch.value for switch in switches]
        self._idle_timer = idle_timer if idle_timer is not None else IdleTimer()

        # `_persisted` mirrors the bytes last written for each key, so a
        # later state that encodes identically is not written again — see
        # tasks/ongoing/0030-custom-glyph-upload-and-persistence.md's
        # Risks, "Flash wear."
        self._persisted = [None] * len(displays)
        self.key_states = [
            self._restore_key_state(key_index) for key_index in range(len(displays))
        ]
        # Every key is dirty at power-on so the first step paints all six
        # displays, rather than leaving them on whatever the panel powered
        # up showing.
        self._dirty = set(range(len(self.key_states)))

    def _restore_key_state(self, key_index):
        """Build one key's starting `KeyState`: its last persisted state,
        or the power-on default when none was saved, or the saved record
        is corrupt.
        """
        saved = self._storage.read(key_index)
        if saved is None:
            return display_render.KeyState(emoji_id=DEFAULT_EMOJI_ID, color=DEFAULT_COLOR)

        try:
            color, emoji_id, blink, pixels = glyph_state.decode(saved)
        except ValueError:
            return display_render.KeyState(emoji_id=DEFAULT_EMOJI_ID, color=DEFAULT_COLOR)

        self._persisted[key_index] = saved
        return display_render.KeyState(
            emoji_id=emoji_id, color=color, blink=blink, pixels=pixels
        )

    def _persist_key_state(self, key_index):
        """Write `key_index`'s current state to storage, unless it
        already matches what was last written there.
        """
        key_state = self.key_states[key_index]
        data = glyph_state.encode(
            key_state.color, key_state.emoji_id, key_state.blink, key_state.pixels
        )
        if data == self._persisted[key_index]:
            return
        self._storage.write(key_index, data)
        self._persisted[key_index] = data

    def step(self, now_us):
        """Run one iteration of the loop."""
        host_message = self._apply_host_report(now_us)
        custom_glyph_message = self._apply_custom_glyph()
        key_event = self._scan_switches(now_us)

        self._render_dirty_keys()

        if host_message or custom_glyph_message or key_event:
            self._idle_timer.touch(now_us)
        self._apply_backlight(now_us)

        if self._tracer is not None:
            self._tracer.drain(self._write_trace_record)

    def _write_trace_record(self, record_bytes):
        wire.write_frame(self._serial, wire.MESSAGE_TYPE_TRACE, record_bytes)

    def run(self):
        """Loop forever. `code.py` calls this and never returns."""
        while True:
            self.step(time.monotonic_ns() // 1000)

    def _apply_host_report(self, now_us):
        """Decode one HID output report and update the key it names.

        Returns True when a key's state changed. A report that cannot be
        decoded — a short message, or one built against another protocol
        version — is dropped, and every key keeps its previous state.
        """
        report = self._hid_device.get_last_received_report()
        if not report:
            return False

        try:
            message = wire.decode_key_state(self._strip_report_id(report))
        except ValueError:
            return False

        if self._tracer is not None:
            self._tracer.record(
                tracer_module.HOST_MESSAGE_DECODED,
                message.key_index,
                message.emoji_id,
                now_us,
            )

        if message.key_index >= len(self.key_states):
            return False

        key_state = self.key_states[message.key_index]
        key_state.color = message.color
        key_state.emoji_id = message.emoji_id
        key_state.blink = message.blink
        # A built-in Emoji ID switches the key back to the glyph table,
        # replacing any custom image it showed before — see "Set custom
        # glyph" in docs/wire-protocol.md.
        key_state.pixels = None
        self._dirty.add(message.key_index)
        self._persist_key_state(message.key_index)
        return True

    def _apply_custom_glyph(self):
        """Decode one Set custom glyph message from the CDC channel and
        update its key.

        Returns True when a key's state changed. A message naming a key
        index this pad does not have is dropped, like
        `_apply_host_report`.
        """
        glyph = self._custom_glyph_reader.feed(self._serial)
        if glyph is None:
            return False
        if glyph.key_index >= len(self.key_states):
            return False

        key_state = self.key_states[glyph.key_index]
        key_state.emoji_id = wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
        key_state.pixels = glyph.pixels
        self._dirty.add(glyph.key_index)
        self._persist_key_state(glyph.key_index)
        return True

    @staticmethod
    def _strip_report_id(report):
        """Drop the leading report ID byte when CircuitPython includes it.

        `boot.py` declares report ID 1, and whether the core hands that
        byte back with the report is this task's open question. Accepting
        both lengths means the first board run answers it without needing
        a firmware change.
        """
        if len(report) == wire.KEY_STATE_SIZE + 1:
            return report[1:]
        return report

    def _scan_switches(self, now_us):
        """Debounce every switch and write an event for each transition.

        Returns True when at least one event was written.
        """
        wrote_event = False

        for index, switch in enumerate(self._switches):
            # The switches are wired to ground with the pull-up enabled,
            # so a closed switch reads low.
            pressed = not switch.value
            edge = pressed != self._last_raw[index]
            self._last_raw[index] = pressed

            transition = self._debouncers[index].feed(pressed, now_us)

            if self._tracer is not None and edge:
                self._tracer.record(
                    tracer_module.SWITCH_READ, index, int(pressed), now_us
                )
                if transition == "press":
                    verdict = wire.PRESS
                elif transition == "release":
                    verdict = wire.RELEASE
                else:
                    verdict = 0xFF  # rejected as a bounce
                self._tracer.record(
                    tracer_module.DEBOUNCE_VERDICT, index, verdict, now_us
                )

            if transition is None:
                continue

            event_type = wire.PRESS if transition == "press" else wire.RELEASE
            payload = wire.encode_event(index, event_type, now_us)
            wire.write_frame(self._serial, wire.MESSAGE_TYPE_EVENT, payload)
            if self._tracer is not None:
                self._tracer.record(
                    tracer_module.EVENT_WRITTEN, index, event_type, now_us
                )
            wrote_event = True

        return wrote_event

    def _render_dirty_keys(self):
        """Redraw the keys that changed, plus every key that blinks.

        A blinking key is redrawn each iteration because `render_key`
        toggles its visibility once per call.
        """
        for index, key_state in enumerate(self.key_states):
            if index not in self._dirty and not key_state.blink:
                continue
            display_render.render_key(
                self._displays[index], key_state, self._emoji_lookup
            )

        self._dirty.clear()

    def _apply_backlight(self, now_us):
        duty_cycle = self._idle_timer.duty_cycle(now_us)
        for backlight in self._backlights:
            backlight.duty_cycle = duty_cycle
