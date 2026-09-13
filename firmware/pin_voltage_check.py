"""
Per-pin GPIO voltage check, for verifying solder joints on header pins.

Not imported by code.py. Run from the CircuitPython REPL (connect over
serial, Ctrl-C to interrupt code.py if it's running, then paste one line
per pin):

    import pin_voltage_check
    pin_voltage_check.check_pin("GP13")

Put the multimeter's black lead on any GND pin and the red lead on the
pin under test, in DC voltage mode, then watch it track the console
print. Ctrl-C stops the loop; move the probe and call check_pin() again
for the next pin. pin_voltage_check.HEADER_PINS lists every pin
hardware/README.md's Pinout table assigns, if you want a reminder of
what to walk.

Interpreting the multimeter reading against the console print:
  - Alternates ~3.3V / ~0V in step with HIGH/LOW -> joint is good.
  - Stays ~0V or floats/noisy the whole time -> open joint on THIS pin
    (cold solder, missing bridge, or probing the wrong pin).
  - Tracks a NEIGHBOR pin's HIGH/LOW instead of the one being driven ->
    solder bridge shorting this pin to that neighbor.
  - Sits at a steady voltage that is neither ~0V nor ~3.3V -> a
    high-resistance joint, or a short to a rail through another part.
"""
import time

import board
import digitalio

# Transcribed from hardware/README.md's Pinout table, not imported from
# pins.py: pins.py does `from typing import NamedTuple`, a module
# CircuitPython 10.2.1 does not ship, so importing it crashes the REPL.
HEADER_PINS = [
    "GP2", "GP3",  # SPI SCK / MOSI
    "GP4", "GP5", "GP6", "GP7", "GP8", "GP9",  # 6x display CS
    "GP10",  # shared DC
    "GP11",  # shared RST
    "GP13", "GP14", "GP15", "GP16", "GP17", "GP18",  # 6x KEY inputs
    "GP19", "GP20", "GP21",  # I2S mic BCLK / WS / DATA
    "GP0", "GP1", "GP22", "GP26", "GP27", "GP28",  # 6x backlight PWM
]


def check_pin(pin_name, cycle_seconds=2):
    pin = digitalio.DigitalInOut(getattr(board, pin_name))
    pin.direction = digitalio.Direction.OUTPUT
    print("Probe {} now (red lead on {}, black on GND). Ctrl-C to stop.".format(pin_name, pin_name))
    try:
        while True:
            pin.value = True
            print("{}: HIGH (expect ~3.3V)".format(pin_name))
            time.sleep(cycle_seconds)
            pin.value = False
            print("{}: LOW (expect ~0V)".format(pin_name))
            time.sleep(cycle_seconds)
    finally:
        pin.deinit()
