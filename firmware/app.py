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
import tracer as tracer_module
import wire
from debounce import Debouncer
from idle_timer import IdleTimer

DEFAULT_COLOR = 0x0000
DEFAULT_EMOJI_ID = 0

# One blink slot. All blinking keys show "on" in an even slot of the clock
# (`now_us // BLINK_INTERVAL_US`) and "off" in an odd one, so they flash in
# time whatever started each blink (task 0048). `render_key` used to toggle
# on every call, and task 0022's main loop calls `step` unthrottled, so a
# blinking key looked like a flicker-blended solid color (confirmed live
# during task 0031's key-0 bring-up). 500ms gives a 1Hz blink.
BLINK_INTERVAL_US = 500_000


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


class _KeyPort:
    """What `display_render.render_key` hands a frame to: it queues the frame
    for one key's panel in the `MacroPad` that built it.
    """

    def __init__(self, pad, key_index):
        self._pad = pad
        self._key_index = key_index

    def submit(self, frame):
        self._pad._submit_push(self._key_index, frame)


class MacroPad:
    """The whole device: six switches, six panels on one shared SCK, and a
    backlight.

    Every argument is injected rather than built here, which is what lets
    a test drive `step` one iteration at a time with fakes. `panels` holds
    one `st7735.Panel` for each key, already initialised by `code.py`
    (task 0043). A redraw of one key pushes a frame to that key's panel
    alone — it releases no bus, pulses no reset line, and sends no init
    command, so the other five panels keep their image.

    A push goes out by DMA and does not hold the loop (task 0045). `step`
    queues a frame for a key, starts the queued pushes when the bus is free,
    and polls the pushes on the wire. A key has at most one frame in the
    queue: a newer frame replaces an older one that has not started.

    With a `push_bus` (a `pio_spi.ParallelBus`), every key has its own MOSI
    line and all the queued frames start together, as one group (task 0048).
    Without one, the panels share SCK and MOSI and one push is on the wire at
    a time.
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
        push_bus=None,
    ):
        self._switches = switches
        self._panels = panels
        self._backlights = backlights
        self._hid_device = hid_device
        self._serial = serial
        self._tracer = tracer
        self._push_bus = push_bus
        self._custom_glyph_reader = wire.CustomGlyphReader()

        self._debouncers = [Debouncer(debounce_window_ms) for _ in switches]
        # The switch's own initial reading, not a sentinel, so the first
        # `step` traces nothing for a switch that has not moved — a trace
        # marks an edge on the pin, not the fact that it was read at all.
        self._last_raw = [not switch.value for switch in switches]
        self._idle_timer = idle_timer if idle_timer is not None else IdleTimer()

        # Every key starts at the power-on default. The board keeps no state
        # across a power cycle: the host driver replays each key's last state
        # when it reconnects (task 0044).
        self.key_states = [
            display_render.KeyState(emoji_id=DEFAULT_EMOJI_ID, color=DEFAULT_COLOR)
            for _ in switches
        ]
        # Every key is dirty at power-on so the first step paints all six
        # displays, rather than leaving them on whatever the panel powered
        # up showing.
        self._dirty = set(range(len(self.key_states)))

        self._ports = [_KeyPort(self, index) for index in range(len(switches))]
        # The key indexes waiting to push, oldest first, and the frame each
        # waits with. `_active_keys` and `_active_frames` are the group on the
        # wire. `resyncs` counts the groups that desynced and were sent again.
        self._push_queue = []
        self._queued_frame = [None] * len(switches)
        self._active_keys = []
        self._active_frames = []
        self.resyncs = 0

    def step(self, now_us):
        """Run one iteration of the loop."""
        self._service_pushes()
        host_message = self._apply_host_report(now_us)
        custom_glyph_message = self._apply_custom_glyph()
        key_event = self._scan_switches(now_us)

        self._render_dirty_keys(now_us)
        self._service_pushes()

        if host_message or custom_glyph_message or key_event:
            self._idle_timer.touch(now_us)
        self._apply_backlight(now_us)

        if self._tracer is not None:
            self._tracer.drain(self._write_trace_record)

    def _submit_push(self, key_index, frame):
        """Queue `frame` for a key. It replaces a frame of the same key that
        has not started, and keeps that frame's place in the queue.
        """
        if self._queued_frame[key_index] is None:
            self._push_queue.append(key_index)
        self._queued_frame[key_index] = frame

    def _service_pushes(self):
        """End the group on the wire when it is done, then start the next.

        Returns at once when a group is still on the wire. A group is on the
        wire for about 26 ms, so this runs many times for one group.
        """
        while True:
            if self._active_keys:
                if not self._group_done():
                    return

            if not self._push_queue:
                return

            self._start_group()

    def _group_done(self):
        """True when no group is on the wire any more.

        A group that desynced (`pio_spi.ParallelBus.done`) is not done: its
        frames go back to the front of the queue, and the panels send the
        window again before the frames.
        """
        if self._push_bus is not None:
            if not self._push_bus.done:
                return False
            if self._push_bus.desynced:
                self._requeue_active_group()
                return True

        for key_index in self._active_keys:
            if not self._panels[key_index].poll():
                return False

        for key_index in self._active_keys:
            self._record_trace(tracer_module.REFRESH_DONE, key_index)
        self._active_keys = []
        self._active_frames = []
        return True

    def _requeue_active_group(self):
        """Put the frames of a group that failed back in the queue, ahead of
        the others, unless a newer frame is already waiting for that key.
        """
        requeue = []
        for key_index, frame in zip(self._active_keys, self._active_frames):
            self._panels[key_index].abort_push()
            if self._queued_frame[key_index] is None:
                self._queued_frame[key_index] = frame
                requeue.append(key_index)
        self._push_queue[0:0] = requeue
        self._active_keys = []
        self._active_frames = []
        self.resyncs += 1

    def _start_group(self):
        """Start the queued pushes: all of them with a `push_bus`, else the
        oldest one.
        """
        count = len(self._push_queue) if self._push_bus is not None else 1
        keys = self._push_queue[:count]
        del self._push_queue[:count]
        frames = [self._queued_frame[key_index] for key_index in keys]
        for key_index in keys:
            self._queued_frame[key_index] = None

        if self._push_bus is None:
            self._panels[keys[0]].start_push(frames[0])
        else:
            # Every window goes out before any CS goes low: a command
            # reaches the panel whose CS is low, over that panel's DIN alone.
            for key_index in keys:
                self._panels[key_index].prepare_push()
            for key_index, frame in zip(keys, frames):
                self._panels[key_index].begin_push(frame)
            try:
                self._push_bus.start_group(tuple(zip(keys, frames)))
            except BaseException:
                for key_index in keys:
                    self._panels[key_index].abort_push()
                raise

        self._active_keys = keys
        self._active_frames = frames
        for key_index in keys:
            self._record_trace(tracer_module.PUSH_STARTED, key_index)

    def _record_trace(self, code, key_index):
        if self._tracer is not None:
            self._tracer.record(code, key_index, 0, time.monotonic_ns() // 1000)

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
        """Redraw the keys that changed, plus every blinking key that shows
        the wrong frame for the current blink slot.

        The phase is a function of the clock, `blink_on = slot is even`, so
        no key keeps a schedule. A key that starts to blink, an update to a
        blinking key, and a `step` that comes many slots late all draw the
        frame of the current slot, and a blinking key that already shows it
        is not drawn (task 0048).
        """
        blink_on = (now_us // BLINK_INTERVAL_US) % 2 == 0
        for index, key_state in enumerate(self.key_states):
            if index not in self._dirty and not display_render.blink_is_due(
                key_state, blink_on
            ):
                continue

            display_render.render_key(
                self._ports[index], key_state, self._tracer, index, blink_on=blink_on
            )

        self._dirty.clear()

    def _apply_backlight(self, now_us):
        duty_cycle = self._idle_timer.duty_cycle(now_us)
        for backlight in self._backlights:
            backlight.duty_cycle = duty_cycle
