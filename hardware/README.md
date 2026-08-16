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
| Pin headers/sockets, perfboard, hookup wire, USB-C cable | Needed |
| Custom PCB (KiCad → JLCPCB/PCBWay) | Later — once the design is confirmed on breadboard |
| Enclosure — printed, laser-cut, or aluminum panel | Later — once the design is confirmed on breadboard |

**ScreenKey SKU confirmed: the *Module* variant.** It has a 9-pin SPI
Control Interface with a dedicated `KEY` pin, not the *LCD-only* variant's
12-pin LCD Interface with no switch signal. The two need different wiring.
See `docs/0.85inch_ScreenKey_Module.pdf` for the confirmed part's
datasheet, and `docs/ppico_plus_2_pinout_diagram.pdf` for the Pico Plus
2's.

**Mount the modules on header or socket connectors. Do not use solder
joints.** This makes a future swap a plug/replace operation, with no
rework.

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
| Shared SPI (hardware SPI0): SCK / MOSI | GP2 / GP3 |
| 6× CS (plain GPIO, software-toggled) | GP4–GP9 |
| Shared DC | GP10 |
| Shared RST | GP11 |
| 6× KEY inputs | GP13–GP18 |
| I2S mic: BCLK / WS / DATA | GP19 / GP20 / GP21 |
| 6× BL (per-key backlight PWM) | GP0, GP1, GP22, GP26, GP27, GP28 |

## Out of scope

- Firmware and driver code (see [`firmware/`](../firmware/) and
  [`driver/`](../driver/)).
