"""CircuitPython's entry point: it runs this file after `boot.py`.

This file only builds the real hardware objects and hands them to
`app.MacroPad`. The loop itself lives in `app.py`, where it runs under
pytest against the stubs in `test/stubs/` with no board attached.

See tasks/ongoing/0022-firmware-main-loop.md for the design decision.
"""

import board
import busio
import displayio
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

displayio.release_displays()

spi = busio.SPI(
    clock=getattr(board, pins.SPI_SCK),
    MOSI=getattr(board, pins.SPI_MOSI),
)

switches = [make_switch(getattr(board, key.switch_pin)) for key in pins.KEYS]

displays = [
    ST7735R(
        displayio.FourWire(
            spi,
            command=getattr(board, pins.DISPLAY_DC),
            chip_select=getattr(board, key.display_cs_pin),
            reset=getattr(board, pins.DISPLAY_RST),
        ),
        width=DISPLAY_WIDTH,
        height=DISPLAY_HEIGHT,
        auto_refresh=False,
    )
    for key in pins.KEYS
]

backlights = [
    Backlight(pwmio.PWMOut(getattr(board, key.backlight_pin)))
    for key in pins.KEYS
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
