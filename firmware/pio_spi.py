"""Transmit-only SPI buses on PIO state machines, sent by DMA.

`busio.SPI.write` keeps the CPU in the call for the whole transfer: 21 ms
for a 128x128 frame at 16 MHz. `rp2pio.StateMachine.background_write` hands
the buffer to a DMA channel and returns at once, so the CPU can compose the
next frame, read the switches, and toggle other blinking keys while a frame
is on the wire.

The PIO owns SCK and MOSI. DC and every CS line stay with the CPU, as in
`st7735.Panel`. A `PioBus` has no lock: the owner makes sure one transfer
ends before the next one starts. `app.MacroPad` does that, because all six
panels share these two lines.

`PioBus` is one SPI bus: one SCK and one MOSI. `ParallelBus` (task 0048) gives
each panel its own MOSI line and one shared SCK, so the six panels take a frame
at the same time instead of one after another.

See tasks/complete/0045-double-buffered-dma-panel-push.md and
tasks/ongoing/0048-shared-blink-clock.md for the design decisions.
"""

import array
import time

import rp2pio

# SPI mode 0, most significant bit first, two PIO clocks per bit. SCK is half
# the state machine clock. The program is assembled on the host, because
# `adafruit_pioasm` is not on the board. Source:
#
#     .side_set 1
#     .wrap_target
#         out pins, 1  side 0
#         nop          side 1
#     .wrap
#
# MOSI changes while SCK is low and holds while it is high.
_PROGRAM = array.array("H", (0x6001, 0xB042))
_CLOCKS_PER_BIT = 2

# The PIO divides the 150 MHz system clock. A whole divider keeps the SCK
# edges evenly spaced, so choose `baudrate` as 150 MHz / (2 * n), for example
# 15 MHz (n = 5).
SYSTEM_CLOCK_HZ = 150_000_000


class PioBus:
    """The shared SPI bus the six panels borrow, one transfer at a time.

    `sck` and `mosi` are `microcontroller.Pin`s. `baudrate` is the SCK
    frequency in Hz.
    """

    def __init__(self, sck, mosi, baudrate):
        self.baudrate = baudrate
        self._sm = rp2pio.StateMachine(
            _PROGRAM,
            frequency=baudrate * _CLOCKS_PER_BIT,
            first_out_pin=mosi,
            first_sideset_pin=sck,
            auto_pull=True,
            pull_threshold=8,
            out_shift_right=False,
        )

    def start(self, data):
        """Start sending `data` by DMA and return at once.

        `data` is a buffer of bytes. A caller must keep it unchanged until
        `done` is true.
        """
        self._sm.background_write(data)

    @property
    def done(self):
        """True when no transfer is on the wire.

        The DMA is finished and the state machine has used every byte. The DMA
        ends when it has put the last byte in the FIFO, so up to five bytes
        (2.7 us at 15 MHz) are still to shift out. The state machine stalls
        when it has nothing left to shift, so this clears the stall flag once
        the DMA is done, and reads it. The flag is set at once when the state
        machine is already idle, because an idle state machine is stalled. A
        `False` here means the last bytes are on their way; read it again.

        The flag cannot start the wait: it is set while the state machine is
        idle, before the DMA delivers the first byte. It only means "drained"
        after the DMA ends.
        """
        if self._sm.writing:
            return False
        self._sm.clear_txstall()
        return self._sm.txstall

    def write(self, data):
        """Send `data` and wait until it is on the wire. For short writes."""
        self.start(data)
        while not self.done:
            pass

    def deinit(self):
        """Free the state machine, its DMA channel, and its pins."""
        self._sm.deinit()


# The parallel push. The leader makes SCK: 10 PIO cycles low, then 5 high, so
# a bit takes 15 cycles and SCK is 150 MHz / 15 = 10 MHz. It also shifts the
# frame's bits out to nowhere (`out null`), which is what paces it. Source:
#
#     .side_set 1
#     .wrap_target
#         out null, 1 side 0 [9]
#         nop         side 1 [4]
#     .wrap
_LEADER_PROGRAM = array.array("H", (0x6961, 0xB442))

# Each follower watches SCK, with SCK as its first input pin, and puts the next
# bit of its own frame on its own DIN line while SCK is low. A follower sees
# SCK about 3 cycles late, so SCK needs the long low phase above. Source:
#
#     .wrap_target
#         wait 0 pin 0
#         out pins, 1
#         wait 1 pin 0
#     .wrap
_FOLLOWER_PROGRAM = array.array("H", (0x2020, 0x6001, 0x20A0))

# 10 MHz is the fastest SCK that held in spike 2 (2026-10-06): no desync in 300
# pushes. At 15 MHz a follower lost sync in 5 of 150 pushes.
PARALLEL_BAUDRATE = 10_000_000
_PARALLEL_CLOCK_HZ = SYSTEM_CLOCK_HZ

# A command or window goes through one `PioBus` on one panel's DIN line at this
# rate, never through a follower: commands sent through a follower were flaky
# in spike 1.
COMMAND_BAUDRATE = 15_000_000

# A frame takes 26 ms at 10 MHz. A push that has not ended after this long is
# treated as a desync.
PUSH_TIMEOUT_NS = 200_000_000


class _PanelBus:
    """What one `st7735.Panel` borrows: commands for its panel alone, and a
    frame push as a group of one.
    """

    def __init__(self, parent, key):
        self._parent = parent
        self._key = key

    def write(self, data):
        self._parent.write(self._key, data)

    def start(self, data):
        self._parent.start_group(((self._key, data),))

    @property
    def done(self):
        return self._parent.done


class ParallelBus:
    """Six panels on one shared SCK and six DIN lines, one frame DMA each.

    `sck` is a `microcontroller.Pin` and `dins` holds the DIN pin of each
    panel. One leader state machine makes SCK, and one follower for each panel
    shifts that panel's frame onto its DIN line, so `start_group` sends the
    frames of any panels at the same time. That is 7 of the 12 state machines
    and 7 of the 16 DMA channels.

    The bus is in one of two modes. In parallel mode the seven machines exist.
    In command mode one `PioBus` exists on a single panel's DIN line, and
    `write` sends a command to that panel alone. `write` and `start_group`
    switch the mode when they need to, and refuse to while a group is on the
    wire. A switch deinitialises the machines of the other mode and builds the
    new ones.

    A follower that misses a clock edge shifts its frame out of step, and a
    frame push has no resync. `done` tells a follower that did not finish with
    the leader from one that did: it tears all seven machines down, sets
    `desynced`, and returns true. The caller then sends the window and the
    frames again (`app.MacroPad` does).
    """

    def __init__(self, sck, dins):
        self._sck = sck
        self._dins = list(dins)
        self.baudrate = PARALLEL_BAUDRATE
        self._leader = None
        self._followers = None
        self._single = None
        self._single_key = None
        self._group = []
        self._deadline_ns = 0
        self._cleared = False
        self.desynced = False

    def panel_bus(self, key):
        """The bus object that panel `key` borrows."""
        return _PanelBus(self, key)

    def write(self, key, data):
        """Send a command or its data to panel `key` alone, and wait."""
        self._enter_command_mode(key)
        self._single.write(data)

    def start_group(self, items):
        """Start sending a frame to each panel in `items`, a sequence of
        `(key, frame)`, and return at once.

        Every frame is a buffer of the same length, which the caller keeps
        unchanged until `done` is true. The caller has already lowered CS on
        these panels only.
        """
        if self._group:
            raise RuntimeError("a group is already on the wire")
        self._enter_parallel_mode()
        self.desynced = False
        self._cleared = False
        self._deadline_ns = time.monotonic_ns() + PUSH_TIMEOUT_NS
        keys = []
        for key, frame in items:
            self._followers[key].background_write(frame)
            keys.append(key)
        # The leader's bytes go to `out null`, so any buffer of the right
        # length paces it.
        self._leader.background_write(items[0][1])
        self._group = keys

    @property
    def done(self):
        """True when no group is on the wire: every frame is sent, or the
        group desynced or timed out (then `desynced` is true).

        Never blocks. The leader's DMA ends a few bytes before its last bit,
        so this clears the leader's and the followers' stall flags once the
        leader's DMA is done, and reads them on a later call, as
        `PioBus.done` does. A state machine that is out of bytes stalls on its
        `out`. A follower in step is stalled by then, because it has shifted
        its last bit. A follower that is not is still writing, or is waiting
        for a clock edge that will not come.
        """
        if not self._group:
            return True
        if self._leader.writing:
            return self._wait_or_desync()
        if not self._cleared:
            self._leader.clear_txstall()
            for key in self._group:
                self._followers[key].clear_txstall()
            self._cleared = True
            return self._wait_or_desync()
        if not self._leader.txstall:
            return self._wait_or_desync()
        for key in self._group:
            follower = self._followers[key]
            if follower.writing or not follower.txstall:
                self._desync()
                return True
        self._group = []
        return True

    def _wait_or_desync(self):
        if time.monotonic_ns() < self._deadline_ns:
            return False
        self._desync()
        return True

    def _desync(self):
        self._teardown()
        self.desynced = True

    def _enter_command_mode(self, key):
        if self._group:
            raise RuntimeError("a group is on the wire")
        if self._single_key == key:
            return
        self._teardown()
        self._single = PioBus(self._sck, self._dins[key], COMMAND_BAUDRATE)
        self._single_key = key

    def _enter_parallel_mode(self):
        if self._leader is not None:
            return
        self._teardown()
        # Followers first: the leader claims SCK as its side-set pin, and the
        # followers only watch it (`exclusive_pin_use=False`).
        followers = []
        try:
            for din in self._dins:
                followers.append(
                    rp2pio.StateMachine(
                        _FOLLOWER_PROGRAM,
                        frequency=_PARALLEL_CLOCK_HZ,
                        first_out_pin=din,
                        first_in_pin=self._sck,
                        auto_pull=True,
                        pull_threshold=8,
                        out_shift_right=False,
                        exclusive_pin_use=False,
                    )
                )
            leader = rp2pio.StateMachine(
                _LEADER_PROGRAM,
                frequency=_PARALLEL_CLOCK_HZ,
                first_sideset_pin=self._sck,
                auto_pull=True,
                pull_threshold=8,
                out_shift_right=False,
            )
        except BaseException:
            for follower in followers:
                follower.deinit()
            raise
        self._followers = followers
        self._leader = leader

    def _teardown(self):
        """Free every state machine of either mode. Safe to call twice."""
        self._group = []
        machines = []
        if self._leader is not None:
            machines.append(self._leader)
        if self._followers is not None:
            machines.extend(self._followers)
        self._leader = None
        self._followers = None
        for machine in machines:
            machine.stop_background_write()
            machine.deinit()
        if self._single is not None:
            single = self._single
            self._single = None
            self._single_key = None
            single.deinit()
