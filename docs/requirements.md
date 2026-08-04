# Custom 4-Key Emoji Macro Keyboard — Final Hardware Iteration

*This is a personal, one-off build. This pass does not include the design of the host controller.*

## Locked Requirements

**Hardware (in hand — birthday gift)**
- SparkFun RP2350 (Pro Micro form factor)
- 4× Waveshare 0.85" ScreenKey Module — ST7735 driver, 128×128, 65K color, SPI, integrated mechanical switch
  - ⚠️ **CAUTION:** Before you solder, confirm the SKU that you received. Wiring differs between the two variants.
    - **Module variant** (keycap + switch + PCB): has a 9-pin SPI Control Interface with a `KEY` pin.
    - **LCD-only variant**: has a 12-pin LCD Interface with no switch signal.

**Per-key behavior** (unchanged)
- Single press: fires a host-defined action immediately.
- Double press: fires the same action, then sends a second "double" event. The host resolves this event from raw timestamped events. The MCU does not classify it.
- Long press (hold): activates the mic while held, and streams audio to the host.

**Host controller**

The host controller is unchanged from the prior iteration. This pass does not include it.

It does the following:
- Sends the emoji, task ID, color, and blink state to each key.
- Resolves press timing.
- Maps events to actions.
- Decodes audio.

Language: Go. This work is deferred.

**Microcontroller firmware**

This iteration updates the firmware with hardware ideas borrowed from the Stream Deck.

The firmware does the following:
- Renders the emoji, background, and blink state for each key.
- Debounces the switch input (5 ms to 10 ms).
- Emits raw, timestamped press and release events. It does not classify clicks itself.
- Captures and buffers mic audio through I2S. It streams the audio to the host during a hold.
- Enumerates as a USB-C composite device: HID for the primary action, and CDC serial for state, raw events, and audio.

Language: CircuitPython.

**New: idle backlight dimming.**
This feature is based on the screensaver behavior of the Stream Deck. If no host update or key event arrives within a set time window (for example, 5 minutes), the firmware ramps down the PWM backlight pin. This logic runs only on the device. It needs no input from the host.

**New: chunked audio framing with an end-of-stream flag.**
This feature is based on the bulk-transfer pattern of the Stream Deck: fixed-size chunks, with the final chunk flagged. The firmware adds a 1-byte "final" flag to the existing `AUDIO_CHUNK` message. This flag lets the host detect the end of the recording without prior knowledge of its duration. This is a firmware-side decision only. The host only reads the flag. The host needs no new workflow.

## Hardware Design Notes

- **Mount the modules on header or socket connectors. Do not use solder joints.** This method makes a future swap easy. You can replace a ScreenKey module, or add a separate switch for the LCD-only variant, without rework.
- **Confirm the SKU before you wire the device.** See the pinout warning above.
- **Enclosure material.** A 3D-printed or laser-cut case is enough. The power budget is too low to cause heat problems. An aluminum panel is optional. It gives a more durable finish, similar to the chassis of the Stream Deck module. This is a cosmetic upgrade only. It is not a requirement.

## Parts List

| Part | Status |
|---|---|
| SparkFun RP2350 (Pro Micro) | In hand (gift) |
| 4× Waveshare 0.85" ScreenKey Module | In hand (gift) — confirm SKU/variant |
| I2S MEMS mic breakout (SPH0645 or ICS-43434) | Still needed |
| Pin headers/sockets, perfboard, hookup wire, USB-C cable | Still needed |
| *(later)* Custom PCB (KiCad → JLCPCB/PCBWay) | Once logic confirmed on breadboard |
| *(later)* Enclosure — printed/laser-cut, or aluminum panel | Once logic confirmed |

## Actionable Steps (hardware-focused)

1. Before you wire anything, confirm the exact Waveshare SKU that you received. Then get the matching pinout.
2. **Single-key breadboard prototype.** Connect the RP2350 to one ScreenKey module with header or socket mounts. Confirm that CircuitPython works with `adafruit_st7735r`. Render a static emoji with a background color.
3. **USB link.** Bring up the `usb_hid` and `usb_cdc` composite interface on the device. Confirm basic enumeration. (Your own host controller plugs in here later.)
4. **Button handling.** Add hardware debounce and emit raw, timestamped events only. This pass does not include click-pattern resolution.
5. **Emoji asset pipeline.** Convert a curated set of emoji to RGB565 bitmaps sized for 128×128 pixels.
6. **Idle dimming.** Implement the PWM backlight timeout behavior.
7. **Microphone.** Wire the I2S mic through `audiobusio.I2SIn`. During a hold, capture the audio into a ring buffer. Implement the chunked framing with the final flag to stream audio to the host.
8. **Scale to 4 keys.** Repeat the wiring on the shared SPI bus, with a separate CS pin for each module. Add a per-key state loop.
9. **Custom PCB and enclosure.** After you confirm the design on the breadboard, start this step.
