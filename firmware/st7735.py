"""Drives the six ST7735R panels over one PIO SPI bus, each initialised once.

This board allows one `displayio` bus at a time, so a key switch used to
release the bus and build a new one. That pulsed the shared RST line and
reran a 763 ms panel init, and the other five panels lost their image. A
`Panel` here owns no bus. It borrows the one `pio_spi.PioBus` under its own
CS line for each transaction, so one key's update never reaches another
panel.

`code.py` calls `init_panels` once at boot: one RST pulse, then one init and one
draw window per panel. The window never changes, and the panel stays in write
mode after it, so its address pointer wraps at the end of each frame. After
boot, a push is the frame alone. It sends no command, no init, and touches no
RST (task 0049).

A push does not wait for the frame. `Panel.start_push` hands the frame to the
bus's DMA and returns. `Panel.poll` releases CS when the frame is on the wire. Until then the panel is `busy`, and no other panel may
use the bus: all six share SCK and MOSI, and `app.MacroPad` sends one push at
a time (task 0045).

See tasks/ongoing/0043-raw-spi-panels-init-once.md,
tasks/complete/0045-double-buffered-dma-panel-push.md and
tasks/ongoing/0049-frame-only-panel-push.md for the design decisions.
"""

import time

# The init sequence of `adafruit_st7735r.ST7735R` v2.0.5, in the same
# `displayio` format: a command byte, an argument count (bit 7 set when a
# delay follows), the arguments, then the delay in ms (0xFF means 500 ms).
# The two last entries are the library's RGB `MADCTL` and, because this
# panel's INVON/INVOFF polarity is the opposite of the library's default,
# `INVON` — confirmed live in task 0006's bring-up with code.py's old
# `invert=True`.
_INIT_SEQUENCE = (
    b"\x01\x80\x96"  # SWRESET and delay 150 ms
    b"\x11\x80\xff"  # SLPOUT and delay 500 ms
    b"\xb1\x03\x01\x2c\x2d"  # FRMCTR1
    b"\xb2\x03\x01\x2c\x2d"  # FRMCTR2
    b"\xb3\x06\x01\x2c\x2d\x01\x2c\x2d"  # FRMCTR3
    b"\xb4\x01\x07"  # INVCTR line inversion
    b"\xc0\x03\xa2\x02\x84"  # PWCTR1 GVDD = 4.7V, 1.0uA
    b"\xc1\x01\xc5"  # PWCTR2 VGH=14.7V, VGL=-7.35V
    b"\xc2\x02\x0a\x00"  # PWCTR3 Opamp current small, Boost frequency
    b"\xc3\x02\x8a\x2a"  # PWCTR4
    b"\xc4\x02\x8a\xee"  # PWCTR5
    b"\xc5\x01\x0e"  # VMCTR1 VCOMH = 4V, VOML = -1.1V
    b"\x20\x00"  # INVOFF
    b"\x36\x01\x18"  # MADCTL bottom to top refresh
    b"\x3a\x01\x05"  # COLMOD 16-bit color
    b"\xe0\x10\x02\x1c\x07\x12\x37\x32\x29\x2d\x29\x25\x2b\x39\x00\x01\x03\x10"  # GMCTRP1
    b"\xe1\x10\x03\x1d\x07\x06\x2e\x2c\x29\x2d\x2e\x2e\x37\x3f\x00\x00\x02\x10"  # GMCTRN1
    b"\x13\x80\x0a"  # NORON and delay 10 ms
    b"\x29\x80\x64"  # DISPON and delay 100 ms
    b"\x36\x01\xc8"  # MADCTL default rotation plus RGB encoding
    b"\x21\x00"  # INVON
)

CASET = 0x2A
RASET = 0x2B
RAMWR = 0x2C

# Held low this long, then high this long, before the panels may be sent
# a command. The ST7735R datasheet asks for at least 10 us low and 120 ms
# before the first command; these leave a wide margin.
_RESET_LOW_S = 0.010
_RESET_RECOVERY_S = 0.120


def pulse_reset(rst, sleep=time.sleep):
    """Pulse the shared RST line once and leave it high.

    `rst` is a `digitalio.DigitalInOut` already set to output. All six
    panels share this line, so any later pulse would reset all of them.
    `init_panels` is the only caller, once at boot.
    """
    rst.value = True
    sleep(_RESET_LOW_S)
    rst.value = False
    sleep(_RESET_LOW_S)
    rst.value = True
    sleep(_RESET_RECOVERY_S)


def init_panels(rst, panels, sleep=time.sleep):
    """Boot sequence: one RST pulse, then one init and one draw window for
    each panel.

    After this returns, nothing in the firmware pulses RST or sends an
    init command again, and a push sends the frame alone.
    """
    pulse_reset(rst, sleep)
    for panel in panels:
        panel.init()
        panel.setup_window()


class Panel:
    """One ST7735R panel, reached through the shared SPI bus and its own
    chip-select line.

    `bus` is a `pio_spi.PioBus`, shared by every panel. `dc` is the shared
    data/command line, and `cs` is this panel's own chip-select line. Both
    are `digitalio.DigitalInOut` outputs. `cs` idles high.

    `busy` is true from `start_push` until `poll` sees the frame on the wire.

    `_needs_window` is true when the panel may not be in write mode with its
    window set: before the first `setup_window`, and after a push that
    raised or did not finish (`abort_push`). The next push then sends the
    window first.
    """

    def __init__(
        self,
        bus,
        dc,
        cs,
        width=128,
        height=128,
        colstart=0,
        rowstart=0,
        sleep=time.sleep,
    ):
        self._bus = bus
        self._dc = dc
        self._cs = cs
        self._width = width
        self._height = height
        self._colstart = colstart
        self._rowstart = rowstart
        self._sleep = sleep
        self.busy = False
        self._needs_window = True
        # The frame on the wire. The panel holds it so that it stays alive and
        # unchanged until `poll` sees it sent.
        self._frame = None
        cs.value = True

    def _begin(self):
        self._cs.value = False

    def _end(self):
        self._cs.value = True

    def _command(self, command, data=None):
        self._dc.value = False
        self._bus.write(bytes((command,)))
        if data:
            self._dc.value = True
            self._bus.write(data)

    def init(self):
        """Run the init sequence on this panel. Boot only."""
        sequence = _INIT_SEQUENCE
        i = 0
        while i < len(sequence):
            command = sequence[i]
            flags = sequence[i + 1]
            count = flags & 0x7F
            data = sequence[i + 2 : i + 2 + count]
            i += 2 + count

            self._begin()
            try:
                self._command(command, data)
            finally:
                self._end()

            if flags & 0x80:
                delay_ms = sequence[i]
                i += 1
                self._sleep((500 if delay_ms == 0xFF else delay_ms) / 1000)

    def setup_window(self):
        """Set the draw window, with this panel's column and row offsets, and
        start a memory write. Boot only, and after a push that failed.

        The window is always the full frame. The panel stays in write mode
        after `RAMWR` and wraps its address pointer at the end of the window,
        so each later frame needs no command.
        """
        x_end = self._colstart + self._width - 1
        y_end = self._rowstart + self._height - 1

        self._begin()
        try:
            self._command(
                CASET,
                bytes((self._colstart >> 8, self._colstart & 0xFF, x_end >> 8, x_end & 0xFF)),
            )
            self._command(
                RASET,
                bytes((self._rowstart >> 8, self._rowstart & 0xFF, y_end >> 8, y_end & 0xFF)),
            )
            self._command(RAMWR)
        finally:
            self._end()
        self._needs_window = False

    def prepare_push(self):
        """Send the window first when the panel may not be in write mode.

        A group of pushes calls this on every panel before `begin_push` on any
        of them, because a command goes out with only its own panel's CS low.
        """
        if self._needs_window:
            self.setup_window()

    def begin_push(self, frame):
        """Set DC high and lower this panel's CS, for `frame` on the wire.

        This starts no DMA: `start_push` does that for one panel, and
        `pio_spi.ParallelBus.start_group` for several. The panel is `busy`
        until `poll` sees the frame sent, or `abort_push` is called. The
        caller must not change `frame` before then.
        """
        if self.busy:
            raise RuntimeError("a push is already on the wire")
        self._dc.value = True
        self._begin()
        self._frame = frame
        self.busy = True

    def start_push(self, frame):
        """Start sending one full frame to the panel, and return.

        `frame` is `width * height` big-endian RGB565 pixels as a buffer of
        bytes. This starts the frame on the bus's DMA, with DC high and this
        panel's CS line low. It sends no command: `setup_window` did that at
        boot. When an earlier push raised, it calls `setup_window` first. The
        caller must not change `frame` before `poll` returns true, and must
        not start another panel's push until then.
        """
        if self.busy:
            raise RuntimeError("a push is already on the wire")

        self.prepare_push()
        self.begin_push(frame)
        try:
            self._bus.start(frame)
        except BaseException:
            self.abort_push()
            raise

    def abort_push(self):
        """Release CS and drop the frame of a push that did not finish.

        The panel may have taken part of the frame, so the next push sends the
        window first.
        """
        self._end()
        self._frame = None
        self.busy = False
        self._needs_window = True

    def poll(self):
        """Release CS once the frame is on the wire. Return True when the
        panel is idle.
        """
        if self.busy and self._bus.done:
            self._end()
            self._frame = None
            self.busy = False
        return not self.busy

    def push(self, frame):
        """Send one full frame and wait until it is on the wire."""
        self.start_push(frame)
        while not self.poll():
            pass
