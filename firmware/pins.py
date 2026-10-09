# Transcribed from hardware/README.md's Pinout table:
# ../hardware/README.md#pinout
from collections import namedtuple

# collections.namedtuple, not typing.NamedTuple's class syntax: this
# board's CircuitPython build ships no `typing` module.
#
# `din_pin` is the key's own MOSI line (task 0048). It sits on the pin that
# used to carry the key's backlight PWM.
KeyPins = namedtuple("KeyPins", ["switch_pin", "display_cs_pin", "din_pin"])


SPI_SCK = "GP2"

# The six backlight inputs join on this one pin, one shared PWM output.
BACKLIGHT = "GP7"

DISPLAY_DC = "GP10"
DISPLAY_RST = "GP11"

MIC_BCLK = "GP19"
MIC_WS = "GP20"
MIC_DATA = "GP21"

# No `: list[KeyPins]` annotation: this board's CircuitPython core doesn't
# implement `__class_getitem__` on builtins, so subscripting `list` here
# raises `TypeError` at import time, before any hardware object is built.
KEYS = [
    KeyPins(switch_pin="GP13", display_cs_pin="GP4", din_pin="GP0"),
    KeyPins(switch_pin="GP14", display_cs_pin="GP5", din_pin="GP1"),
    KeyPins(switch_pin="GP15", display_cs_pin="GP6", din_pin="GP22"),
    KeyPins(switch_pin="GP16", display_cs_pin="GP3", din_pin="GP26"),
    KeyPins(switch_pin="GP17", display_cs_pin="GP8", din_pin="GP27"),
    KeyPins(switch_pin="GP18", display_cs_pin="GP9", din_pin="GP28"),
]
