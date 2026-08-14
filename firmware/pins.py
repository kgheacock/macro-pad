# Transcribed from hardware/README.md's Pinout table:
# ../hardware/README.md#pinout
from typing import NamedTuple


class KeyPins(NamedTuple):
    switch_pin: str
    display_cs_pin: str
    backlight_pin: str


SPI_SCK = "GP2"
SPI_MOSI = "GP3"
DISPLAY_DC = "GP10"
DISPLAY_RST = "GP11"

MIC_BCLK = "GP19"
MIC_WS = "GP20"
MIC_DATA = "GP21"

KEYS: list[KeyPins] = [
    KeyPins(switch_pin="GP13", display_cs_pin="GP4", backlight_pin="GP0"),
    KeyPins(switch_pin="GP14", display_cs_pin="GP5", backlight_pin="GP1"),
    KeyPins(switch_pin="GP15", display_cs_pin="GP6", backlight_pin="GP22"),
    KeyPins(switch_pin="GP16", display_cs_pin="GP7", backlight_pin="GP26"),
    KeyPins(switch_pin="GP17", display_cs_pin="GP8", backlight_pin="GP27"),
    KeyPins(switch_pin="GP18", display_cs_pin="GP9", backlight_pin="GP28"),
]
