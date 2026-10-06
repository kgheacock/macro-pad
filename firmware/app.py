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

import display_render
import glyph_state
import tracer as tracer_module
import wire
from debounce import Debouncer
from idle_timer import IdleTimer

DEFAULT_COLOR = 0x0000
DEFAULT_EMOJI_ID = 0

# How often a blinking key's visibility toggles. `render_key` toggles
# once per call (task 0006's DoD-3), which assumed render_key was called
# at some human-perceptible cadence; task 0022's main loop instead calls
# it on every `step`, unthrottled, so a blinking key toggled thousands of
# times a second and looked like a flicker-blended solid color instead of
# a blink. Confirmed live during task 0031's key-0 bring-up. 500ms gives
# a 1Hz blink.
BLINK_INTERVAL_US = 500_000

# `step` persists changed keys when no blink is due within this long, so the
# write happens in the idle gap between two rounds of blink pushes. An `nvm`
# write of all six headers took 48 ms to 53 ms on the board; 100 ms leaves
# room for it (task 0044).
PERSIST_BUDGET_US = 100_000

# A change waits for an idle gap at most this long. Then `step` writes
# anyway and freezes the blinking keys for one `nvm` write. Without a limit,
# blinkers that are out of phase can leave no idle gap for ever, and a power
# cut would lose every change made meanwhile.
PERSIST_MAX_WAIT_US = 2_000_000


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


class MacroPad:
    """The whole device: six switches, six panels on one shared SPI bus, six
    backlights.

    Every argument is injected rather than built here, which is what lets
    a test drive `step` one iteration at a time with fakes. `panels` holds
    one `st7735.Panel` for each key, already initialised by `code.py`
    (task 0043). A redraw of one key pushes a frame to that key's panel
    alone — it releases no bus, pulses no reset line, and sends no init
    command, so the other five panels keep their image.
    """

    def __init__(
        self,
        switches,
        panels,
        backlights,
        hid_device,
        serial,
        debounce_window_ms=7.5,
        idle_timer=None,
        tracer=None,
        storage=None,
    ):
        self._switches = switches
        self._panels = panels
        self._backlights = backlights
        self._hid_device = hid_device
        self._serial = serial
        self._tracer = tracer
        self._storage = (
            storage
            if storage is not None
            else glyph_state.NvmStorage(key_count=len(switches))
        )
        self._custom_glyph_reader = wire.CustomGlyphReader()

        self._debouncers = [Debouncer(debounce_window_ms) for _ in switches]
        # The switch's own initial reading, not a sentinel, so the first
        # `step` traces nothing for a switch that has not moved — a trace
        # marks an edge on the pin, not the fact that it was read at all.
        self._last_raw = [not switch.value for switch in switches]
        self._idle_timer = idle_timer if idle_timer is not None else IdleTimer()

        # `_persisted_header` mirrors the header last written for each key,
        # and `_persisted_pixels` the glyph last written. A later state that
        # encodes identically is not written again — see
        # tasks/ongoing/0030-custom-glyph-upload-and-persistence.md's
        # Risks, "Flash wear." Pixels compare by identity, as in
        # display_render.py: a new glyph is a new bytes object.
        self._persisted_header = [None] * len(switches)
        self._persisted_pixels = [None] * len(switches)
        # Keys whose state changed since the last `step` persisted them, and
        # when the oldest of those changes happened. `step` writes them in
        # an idle gap, or once the oldest has waited PERSIST_MAX_WAIT_US.
        self._persist_pending = set()
        self._persist_since_us = None
        self.key_states = [
            self._restore_key_state(key_index) for key_index in range(len(switches))
        ]
        # Next `now_us` at which a blinking key is allowed to toggle
        # visibility again; see BLINK_INTERVAL_US. `None` while the key does
        # not blink, so a key that starts blinking gets a fresh schedule.
        self._next_blink_us = [None] * len(switches)
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

        self._persisted_header[key_index] = glyph_state.encode_header(color, emoji_id, blink)
        self._persisted_pixels[key_index] = pixels
        return display_render.KeyState(
            emoji_id=emoji_id, color=color, blink=blink, pixels=pixels
        )

    def _blink_due_within(self, now_us, span_us):
        """True when a blinking key's next toggle falls within `span_us`."""
        for index, key_state in enumerate(self.key_states):
            next_blink_us = self._next_blink_us[index]
            if key_state.blink and next_blink_us is not None:
                if next_blink_us - now_us <= span_us:
                    return True
        return False

    def _persist_pending_keys(self, now_us):
        """Write every key whose state changed, in one `nvm` write.

        Runs after the redraw, so a write never delays a key's new image.
        It waits while a blink is due within PERSIST_BUDGET_US: the write
        freezes the loop, and a frozen loop is a late blink. A change that
        has waited PERSIST_MAX_WAIT_US is written regardless.

        A write failure (for example the filesystem going read-only for a
        glyph's pixels, or a `make flash` sync landing on the same file at
        the same instant — see `boot.py`'s `storage.remount` comment) is
        dropped rather than raised: the key still keeps its new state in
        RAM and on its display, it just won't survive the next power cycle.
        """
        if not self._persist_pending:
            return
        if self._persist_since_us is None:
            self._persist_since_us = now_us

        overdue = now_us - self._persist_since_us >= PERSIST_MAX_WAIT_US
        if not overdue and self._blink_due_within(now_us, PERSIST_BUDGET_US):
            return

        headers = {}
        pixels = {}
        for key_index in sorted(self._persist_pending):
            key_state = self.key_states[key_index]
            header = glyph_state.encode_header(
                key_state.color, key_state.emoji_id, key_state.blink
            )
            glyph_changed = (
                key_state.pixels is not None
                and key_state.pixels is not self._persisted_pixels[key_index]
            )
            if header != self._persisted_header[key_index] or glyph_changed:
                headers[key_index] = header
            if glyph_changed:
                pixels[key_index] = key_state.pixels

        if headers:
            try:
                written = self._storage.write_many(headers, pixels)
            except OSError:
                written = ()
            for key_index in written:
                self._persisted_header[key_index] = headers[key_index]
                self._persisted_pixels[key_index] = self.key_states[key_index].pixels

        if self._tracer is not None:
            for key_index in sorted(self._persist_pending):
                self._tracer.record(
                    tracer_module.PERSIST_DONE,
                    key_index,
                    0,
                    time.monotonic_ns() // 1000,
                )
        self._persist_pending.clear()
        self._persist_since_us = None

    def step(self, now_us):
        """Run one iteration of the loop."""
        host_message = self._apply_host_report(now_us)
        custom_glyph_message = self._apply_custom_glyph()
        key_event = self._scan_switches(now_us)

        self._render_dirty_keys(now_us)
        self._persist_pending_keys(now_us)

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
        # glyph" in docs/wire-protocol.md. The sentinel names the image
        # already in place, so keep it — this is how a plugin toggles
        # blink on a custom image without resending it.
        if message.emoji_id != wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID:
            key_state.pixels = None
        self._dirty.add(message.key_index)
        self._persist_pending.add(message.key_index)
        return True

    def _apply_custom_glyph(self):
        """Decode one Set custom glyph message from the CDC channel and
        update its key.

        Returns True when a key's state changed. A message of the wrong
        length, or naming a key index this pad does not have, is dropped,
        like `_apply_host_report`.
        """
        try:
            glyph = self._custom_glyph_reader.feed(self._serial)
        except ValueError:
            # A glyph payload of the wrong length. The reader has already
            # consumed the whole frame, so the next message starts clean.
            return False
        if glyph is None:
            return False

        if self._tracer is not None:
            self._tracer.record(
                tracer_module.CUSTOM_GLYPH_DECODED,
                glyph.key_index,
                0,
                time.monotonic_ns() // 1000,
            )

        if glyph.key_index >= len(self.key_states):
            return False

        key_state = self.key_states[glyph.key_index]
        key_state.emoji_id = wire.CUSTOM_GLYPH_SENTINEL_EMOJI_ID
        key_state.pixels = glyph.pixels
        self._dirty.add(glyph.key_index)
        self._persist_pending.add(glyph.key_index)
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

    def _render_dirty_keys(self, now_us):
        """Redraw the keys that changed, plus every blinking key whose
        BLINK_INTERVAL_US has elapsed since it last toggled.

        `render_key` toggles a blinking key's frame only when asked, so
        gating that toggle on elapsed wall-clock time, not on `step`'s own
        iteration rate, is what makes it a human-visible blink instead of
        a flicker. A redraw caused by a state change does not toggle and
        does not move the key's schedule, so an update to a blinking key
        keeps its phase (task 0044). A key that has just started to blink
        gets its first schedule here.
        """
        for index, key_state in enumerate(self.key_states):
            next_blink_us = self._next_blink_us[index]
            due_to_blink = (
                key_state.blink and next_blink_us is not None and now_us >= next_blink_us
            )
            if index not in self._dirty and not due_to_blink:
                continue

            display_render.render_key(
                self._panels[index], key_state, self._tracer, index, toggle=due_to_blink
            )
            if not key_state.blink:
                self._next_blink_us[index] = None
            elif due_to_blink or next_blink_us is None:
                self._next_blink_us[index] = now_us + BLINK_INTERVAL_US

        self._dirty.clear()

    def _apply_backlight(self, now_us):
        duty_cycle = self._idle_timer.duty_cycle(now_us)
        for backlight in self._backlights:
            backlight.duty_cycle = duty_cycle
