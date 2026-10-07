"""A transmit-only SPI bus on one PIO state machine, sent by DMA.

`busio.SPI.write` keeps the CPU in the call for the whole transfer: 21 ms
for a 128x128 frame at 16 MHz. `rp2pio.StateMachine.background_write` hands
the buffer to a DMA channel and returns at once, so the CPU can compose the
next frame, read the switches, and toggle other blinking keys while a frame
is on the wire.

The PIO owns SCK and MOSI. DC and every CS line stay with the CPU, as in
`st7735.Panel`. A `PioBus` has no lock: the owner makes sure one transfer
ends before the next one starts. `app.MacroPad` does that, because all six
panels share these two lines.

See tasks/ongoing/0045-double-buffered-dma-panel-push.md for the design
decision.
"""

import array

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
