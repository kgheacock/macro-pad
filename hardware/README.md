# hardware

Wiring notes, PCB design files, and enclosure design files for the macro
pad.

## Scope

**Parts:**

| Part | Status |
|---|---|
| Pimoroni Pico Plus 2 (RP2350) | In hand, connected |
| 6× Waveshare 0.85" ScreenKey Module — ST7735 driver, 128×128, 65K color, SPI, integrated mechanical switch | In hand, SKU confirmed |
| I2S MEMS mic breakout (SPH0645 or ICS-43434) | Needed |
| MX1.25 9-pin cable(s) (ships with each module, breaks out to Dupont leads), perfboard, 2.54mm male header pins (for the perfboard side of each cable's Dupont leads), hookup wire, USB-C cable | Needed |
| 1× 10µF aluminum electrolytic capacitor, 16V+, radial through-hole — bulk cap at the 3V3/GND rail entry point | Needed |
| Custom PCB (KiCad → JLCPCB/PCBWay) | Later — once the design is confirmed on breadboard |
| Enclosure — printed, laser-cut, or aluminum panel | Later — once the design is confirmed on breadboard |

**ScreenKey SKU confirmed: the *Module* variant.** It has a 9-pin SPI
Control Interface with a dedicated `KEY` pin, not the *LCD-only* variant's
12-pin LCD Interface with no switch signal. The two need different wiring.
See `docs/0.85inch_ScreenKey_Module.pdf` for the confirmed part's
datasheet, and `docs/ppico_plus_2_pinout_diagram.pdf` for the Pico Plus
2's.

**Each module's interface is a factory MX1.25 9-pin socket, not a 2.54mm
pin header — standard Dupont jumpers and 2.54mm headers/sockets do not
mate with it directly.** The included MX1.25 cable breaks out into 9
individual Dupont-terminated leads on its free end, already sized for
standard 2.54mm pins — don't cut those off. Solder a row of male 2.54mm
header pins into the perfboard at each module's assigned positions (see
[Pinout](#pinout)) and plug the cable's Dupont leads onto them directly.
This needs no stripping or soldering of the cable's own (fine-gauge)
wire, and keeps both ends serviceable: MX1.25 unplugs at the module,
Dupont unplugs at the perfboard header.

**Enclosure material.** A 3D-printed or laser-cut case is enough — the
power budget is too low to cause heat problems. An aluminum panel is a
cosmetic and durability upgrade only, not a requirement.

## Pinout

Confirmed GPIO assignments on the Pimoroni Pico Plus 2, assuming the
ScreenKey *Module* variant (dedicated `KEY` pin per module). Transcribed
into [`firmware/pins.py`](../firmware/pins.py). See
[`breadboard-diagram.html`](breadboard-diagram.html) for all 6 keys'
pin connections in one diagram.

| Function | Pin(s) |
|---|---|
| Shared SCK (clock for all 6 panels) | GP2 |
| 6× DIN (per-key MOSI, driven by PIO) | GP12, GP1, GP22, GP26, GP27, GP28 (keys 0 to 5) |
| 6× CS (plain GPIO, software-toggled) | GP3, GP4, GP5, GP6, GP8, GP9 |
| Shared DC | GP10 |
| Shared RST | GP11 |
| 6× KEY inputs | GP13–GP18 |
| I2S mic: BCLK / WS / DATA | GP19 / GP20 / GP21 |
| Shared BL (the six backlight inputs joined on one PWM pin) | GP7 |

**Each key has its own DIN line (task 0048).** The six panels share SCK, DC and
RST, and each panel's DIN is its own PIO output, so up to three frames go out
at once. Keys 1 to 5 use the pin that carried the key's backlight PWM before.
Key 0's DIN is on GP12, not GP0: GP0's breadboard row never carried a signal
to its module on the first rewire, so GP0 is unused. The six backlight inputs
now join on one pin, GP7, which was the shared MOSI before. A backlight is on
or off for all six keys together. CS stays a plain GPIO for each key.

**Decoupling:** one 10µF aluminum electrolytic cap across the 3V3/GND
rail rows, right where the Pico's 3V3 and GND wires land on the
breadboard. This is a bulk reservoir for the shared rail feeding all 6
modules over comparatively long breadboard jumpers — each ScreenKey
module already has its own onboard decoupling for the LCD driver and
its backlight boost converter (see `docs/0.85inch_ScreenKey_Module.pdf`),
so no per-module cap is needed here. CLK and DIN are signal lines and
don't need decoupling either.

Electrolytic, not ceramic: a bulk reservoir cap doesn't need ceramic's
low ESR or lack of drift, and 10µF X7R/X5R ceramics are essentially an
SMD-only part — through-hole ones are rare and expensive. Electrolytic
is the normal choice for this role and is genuinely through-hole.

**This part is polarized** — the banded/shorter lead is negative and
must go to GND, the long lead to 3V3. Reversed, it can bulge or vent.

CLK and DIN are signal lines and don't need decoupling.

## Out of scope

- Firmware and driver code (see [`firmware/`](../firmware/) and
  [`driver/`](../driver/)).
