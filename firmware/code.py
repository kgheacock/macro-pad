"""CircuitPython's entry point: it runs this file after `boot.py`.

This file only builds the real hardware objects and hands them to
`app.MacroPad`. The loop itself lives in `app.py`, where it runs under
pytest against the stubs in `test/stubs/` with no board attached.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import board
import busio
import displayio
import fourwire
import pwmio
import usb_cdc
import usb_hid

import pins
from app import Backlight, MacroPad, blank_glyph, make_switch

try:
    from adafruit_st7735r import ST7735R
except ImportError as error:
    raise ImportError(
        "adafruit_st7735r is missing. Copy it into CIRCUITPY/lib from the "
        "Adafruit CircuitPython bundle, then reset the board."
    ) from error

DISPLAY_WIDTH = 128
DISPLAY_HEIGHT = 128

# Confirmed live: a thin strip of stale RAM bled in along the bottom and
# right edges — this panel's visible glass sits a couple pixels into the
# ST7735R's addressable RAM, so the undersized draw window undershot it
# on those edges. colstart=2 confirmed live (right edge bleed resolved);
# rowstart raised from 1 after the bottom edge still showed a strip.
DISPLAY_COLSTART = 2
DISPLAY_ROWSTART = 2

# Temporary: this board's CircuitPython build allows only 1 concurrent
# display bus (confirmed live: a 2nd fourwire.FourWire raises "Too many
# display busses"), so building all 6 of pins.KEYS's displays at once
# crashes. Only key 0 is wired for task 0010's single-key bring-up, so
# scope every hardware list to it until the 6-key bus-sharing redesign
# lands. See tasks/backlog/ for that follow-up task.
BRING_UP_KEYS = pins.KEYS[:1]

displayio.release_displays()

spi = busio.SPI(
    clock=getattr(board, pins.SPI_SCK),
    MOSI=getattr(board, pins.SPI_MOSI),
)

switches = [make_switch(getattr(board, key.switch_pin)) for key in BRING_UP_KEYS]

displays = [
    ST7735R(
        fourwire.FourWire(
            spi,
            command=getattr(board, pins.DISPLAY_DC),
            chip_select=getattr(board, key.display_cs_pin),
            reset=getattr(board, pins.DISPLAY_RST),
        ),
        width=DISPLAY_WIDTH,
        height=DISPLAY_HEIGHT,
        colstart=DISPLAY_COLSTART,
        rowstart=DISPLAY_ROWSTART,
        auto_refresh=False,
        # Confirmed live: magenta (0xFF00FF) rendered as its exact
        # complement, green (0x00FF00) — this panel's INVON/INVOFF
        # polarity is the opposite of the driver's default.
        invert=True,
    )
    for key in BRING_UP_KEYS
]

backlights = [
    Backlight(pwmio.PWMOut(getattr(board, key.backlight_pin)))
    for key in BRING_UP_KEYS
]

macro_pad = MacroPad(
    switches=switches,
    displays=displays,
    backlights=backlights,
    hid_device=usb_hid.devices[0],
    serial=usb_cdc.data,
    emoji_lookup=blank_glyph,
)

macro_pad.run()
