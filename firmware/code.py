"""CircuitPython's entry point: it runs this file after `boot.py`.

This file only builds the real hardware objects, initialises the panels
once, and hands everything to `app.MacroPad`. The loop itself lives in
`app.py`, where it runs under pytest against the stubs in `test/stubs/`
with no board attached.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import board
import digitalio
import displayio
import pwmio
import usb_cdc
import usb_hid

import pins
import pio_spi
import st7735
from app import Backlight, MacroPad, make_switch

DISPLAY_WIDTH = 128
DISPLAY_HEIGHT = 128

# Confirmed live: a thin strip of stale RAM bled in along the bottom and
# right edges — this panel's visible glass sits a couple pixels into the
# ST7735R's addressable RAM, so the undersized draw window undershot it
# on those edges. colstart=2 confirmed live (right edge bleed resolved).
# rowstart raised from 1 to 2, then to 3, after a 1px bottom-edge strip
# remained at 2 (confirmed live rendering a custom glyph image).
DISPLAY_COLSTART = 2
DISPLAY_ROWSTART = 3

# 4MHz confirmed live as reliable on this board's breadboard wiring, for
# the old `displayio` driver. 24MHz (its default) wiped visibly top to
# bottom, and 32MHz showed no content at all. Task 0043's raw SPI driver
# runs at 16MHz: a frame push takes 21ms, against 79ms at 4MHz. Confirmed
# live on all six wired panels (2026-10-06): a labeled test pattern of
# color bars and 1px columns rendered cleanly at 4, 8, 12, 16, and 20MHz.
# The RP2350 steps its SPI clock down from 150MHz, so 16MHz ran at an
# actual 15MHz. Above about 19MHz a push stayed near 17ms, because the CPU
# feeding the FIFO was the limit, not the wire.
# Task 0045 sends frames by DMA from a PIO state machine, whose clock is the
# 150MHz system clock over a divider. 15MHz is a whole divider (5), so the
# SCK edges are evenly spaced, and it is the rate the panels already ran at.
# A frame push takes 22ms and holds the CPU for under 3ms of it.
# Task 0048 gives each panel its own DIN line, so six frames go out at once.
# A follower state machine cannot slow the leader's clock, and at 15MHz it
# lost sync in 5 of 150 pushes (2026-10-06), so the parallel push runs at the
# 10MHz of `pio_spi.PARALLEL_BAUDRATE`: a push takes 26ms, for one panel or
# six. Boot-time commands go through one panel's DIN at 15MHz.

# `displayio` claims the display pins at boot. Release them so the raw SPI
# driver can use them.
displayio.release_displays()

# A reload leaves the last run's state machines enabled, and a stale leader
# holds SCK low. Nothing of this run exists yet, so stop them all first.
pio_spi.stop_stale_state_machines()

bus = pio_spi.ParallelBus(
    sck=getattr(board, pins.SPI_SCK),
    dins=[getattr(board, key.din_pin) for key in pins.KEYS],
)


def _output(pin_name, value):
    pin = digitalio.DigitalInOut(getattr(board, pin_name))
    pin.switch_to_output(value=value)
    return pin


# DC and RST are shared by all six panels. Each panel has its own CS.
dc = _output(pins.DISPLAY_DC, False)
rst = _output(pins.DISPLAY_RST, True)

panels = [
    st7735.Panel(
        bus.panel_bus(index),
        dc,
        _output(key.display_cs_pin, True),
        width=DISPLAY_WIDTH,
        height=DISPLAY_HEIGHT,
        colstart=DISPLAY_COLSTART,
        rowstart=DISPLAY_ROWSTART,
    )
    for index, key in enumerate(pins.KEYS)
]

# The one RST pulse and the one init of each panel. Nothing after this
# pulses RST or sends an init command (task 0043).
st7735.init_panels(rst, panels)

switches = [make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS]

# The six backlight inputs join on one pin, so there is one backlight.
backlights = [Backlight(pwmio.PWMOut(getattr(board, pins.BACKLIGHT)))]

macro_pad = MacroPad(
    switches=switches,
    panels=panels,
    backlights=backlights,
    hid_device=usb_hid.devices[0],
    serial=usb_cdc.data,
    push_bus=bus,
)

# No `try/finally` that frees the bus here: a reload that freed the machines
# while their DMA was running left a DMA channel stuck, and the next run hung on
# its first panel command (2026-10-09). `stop_stale_state_machines` above is the
# cleanup.
macro_pad.run()
