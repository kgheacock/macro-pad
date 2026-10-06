"""CircuitPython's entry point: it runs this file after `boot.py`.

This file only builds the real hardware objects, initialises the panels
once, and hands everything to `app.MacroPad`. The loop itself lives in
`app.py`, where it runs under pytest against the stubs in `test/stubs/`
with no board attached.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import board
import busio
import digitalio
import displayio
import pwmio
import usb_cdc
import usb_hid

import pins
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
# targets 16MHz (a frame push takes 21ms, against 80ms at 4MHz), but no
# panel has been checked at that rate yet — see that task's DoD-7. Raise
# this to 16_000_000 once a wired key renders correctly at it.
DISPLAY_BAUDRATE = 4_000_000

# `displayio` claims the display pins at boot. Release them so the raw SPI
# driver can use them.
displayio.release_displays()

spi = busio.SPI(
    clock=getattr(board, pins.SPI_SCK),
    MOSI=getattr(board, pins.SPI_MOSI),
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
        spi,
        dc,
        _output(key.display_cs_pin, True),
        DISPLAY_BAUDRATE,
        width=DISPLAY_WIDTH,
        height=DISPLAY_HEIGHT,
        colstart=DISPLAY_COLSTART,
        rowstart=DISPLAY_ROWSTART,
    )
    for key in pins.KEYS
]

# The one RST pulse and the one init of each panel. Nothing after this
# pulses RST or sends an init command (task 0043).
st7735.init_panels(rst, panels)

switches = [make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS]

backlights = [
    Backlight(pwmio.PWMOut(getattr(board, key.backlight_pin))) for key in pins.KEYS
]

macro_pad = MacroPad(
    switches=switches,
    panels=panels,
    backlights=backlights,
    hid_device=usb_hid.devices[0],
    serial=usb_cdc.data,
)

macro_pad.run()
