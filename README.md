# macro-pad

Firmware for a custom 4-key emoji macro keyboard. A SparkFun RP2350 (Pro Micro
form factor) drives four Waveshare 0.85" ScreenKey modules, each with its own
128×128 display and mechanical switch, plus an I2S MEMS microphone.

Personal one-off build. The full requirements are in
[`docs/requirements.md`](docs/requirements.md).

## Scope

This repo holds the **microcontroller firmware** (CircuitPython). The host
controller — which maps events to actions, resolves press timing, and decodes
audio — is a separate Go project and is out of scope here.

The firmware:

- Renders an emoji, background color, and blink state per key.
- Debounces switch input (~5–10 ms) and emits raw timestamped press/release
  events. It never classifies single vs. double clicks — the host does that.
- Dims the PWM backlight after a configurable idle window (default 5 min).
- Captures mic audio over I2S while a key is held and streams it to the host in
  fixed-size chunks, with a 1-byte flag marking the final chunk.
- Enumerates as a USB-C composite device: HID for the primary action, CDC
  serial for state, raw events, and audio.

## Hardware

| Part | Status |
|---|---|
| SparkFun RP2350 (Pro Micro) | In hand |
| 4× Waveshare 0.85" ScreenKey Module | In hand — confirm SKU/variant |
| I2S MEMS mic breakout (SPH0645 or ICS-43434) | Needed |
| Pin headers/sockets, perfboard, hookup wire, USB-C cable | Needed |
| Custom PCB (KiCad → JLCPCB/PCBWay) | Later |
| Enclosure — printed, laser-cut, or aluminum panel | Later |

⚠️ **Confirm the ScreenKey SKU before wiring.** The *Module* variant exposes a
9-pin SPI control interface with a dedicated `KEY` pin. The *LCD-only* variant
exposes a 12-pin LCD interface with no switch signal. The wiring differs.

Mount the modules on header/socket connectors rather than soldering them, so a
swap is a plug/replace with no rework.

## Build order

1. Confirm the Waveshare SKU and pull the matching pinout.
2. Single-key breadboard prototype — validate CircuitPython plus
   `adafruit_st7735r`, render a static emoji and background color.
3. USB link — bring up `usb_hid` and `usb_cdc` composite, confirm enumeration.
4. Button handling — debounce and raw timestamped events only.
5. Emoji asset pipeline — convert a curated set to 128×128 RGB565 bitmaps.
6. Idle dimming — PWM backlight timeout.
7. Microphone — `audiobusio.I2SIn` into a ring buffer during hold, chunked
   framing with the final flag.
8. Scale to 4 keys — shared SPI bus with a separate CS per module.
9. Custom PCB and enclosure.

## Layout

```
src/     CircuitPython firmware — contents copy to CIRCUITPY
tools/   Host-side helper scripts (emoji asset pipeline, serial monitor)
assets/  Source emoji art and generated RGB565 bitmaps
docs/    Requirements and hardware notes
```

## Deploying to the board

CircuitPython runs from the `CIRCUITPY` drive the board mounts over USB. To
flash, copy the contents of `src/` to that drive:

```bash
cp -R src/ /Volumes/CIRCUITPY/
```

The board restarts and runs `code.py` on write.
