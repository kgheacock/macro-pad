# Custom 4-Key Emoji Macro Keyboard — Final Hardware Iteration

*Personal, one-off build. Host controller design deferred — not touched in this pass.*

## Locked Requirements

**Hardware (in hand — birthday gift)**
- SparkFun RP2350 (Pro Micro form factor)
- 4× Waveshare 0.85" ScreenKey Module — ST7735 driver, 128×128, 65K color, SPI, integrated mechanical switch
  - ⚠️ Confirm which SKU you received: the **Module** variant (keycap + switch + PCB) exposes a 9-pin "SPI Control Interface" with a dedicated `KEY` pin; the **LCD-only** variant exposes a 12-pin "LCD Interface" with no switch signal at all. Wiring differs — check before you solder anything.

**Per-key behavior** (unchanged)
- Single press → fires a host-defined action immediately
- Double press → fires the same action, plus a secondary "double" event — resolved host-side from raw timestamped events, not by the MCU
- Long press (hold) → activates the mic while held, streams audio to the host

**Host controller** — unchanged from the prior iteration, out of scope for this pass. (Sends emoji/task id/color/blink, resolves press timing, maps events to actions, decodes audio — in Go, deferred.)

**Microcontroller firmware — updated with hardware ideas borrowed from Stream Deck**
- Renders emoji + background + blink state per key
- Debounces switch input (~5–10ms), emits raw timestamped press/release events — never classifies clicks itself
- **New — idle backlight dimming.** Borrowed from Stream Deck's screensaver behavior: if no host update or key event arrives within a configurable window (e.g. 5 min), ramp the PWM backlight pin down instead of leaving all 4 displays lit indefinitely. Fully self-contained on-device logic — no host involvement needed.
- **New — chunked audio framing with an end-of-stream flag.** Borrowed from Stream Deck's bulk-transfer pattern (fixed-size chunks, final chunk flagged). Add a 1-byte "final" flag to the existing `AUDIO_CHUNK` message so the host can detect end-of-recording without knowing duration up front. Purely a firmware-side framing decision — the host just checks a flag, no new host workflow.
- Captures/buffers mic audio via I2S, streams to host during hold
- Enumerates as a USB-C composite device: HID (primary action) + CDC serial (state, raw events, audio)
- Language: CircuitPython

## Hardware Design Notes

- **Mount modules on header/socket connectors, not solder joints.** Makes any future swap (a different ScreenKey unit, or a separate switch if you ever go the LCD-only route) a plug/replace operation with zero rework.
- **Verify the SKU before wiring** — see the pinout warning above.
- **Enclosure material:** a 3D-printed/laser-cut case is fine (power budget is too low for thermal concerns, as established earlier) — but consider an aluminum panel if you want the more premium, durable feel Stream Deck's own Module chassis goes for. Cosmetic/durability upgrade only, not a requirement.

## Parts List

| Part | Status |
|---|---|
| SparkFun RP2350 (Pro Micro) | In hand (gift) |
| 4× Waveshare 0.85" ScreenKey Module | In hand (gift) — confirm SKU/variant |
| I2S MEMS mic breakout (SPH0645 or ICS-43434) | Still needed |
| Pin headers/sockets, perfboard, hookup wire, USB-C cable | Still needed |
| *(later)* Custom PCB (KiCad → JLCPCB/PCBWay) | Once logic validated on breadboard |
| *(later)* Enclosure — printed/laser-cut, or aluminum panel | Once logic validated |

## Actionable Steps (hardware-focused)

1. **Confirm the exact Waveshare SKU** you received and pull the matching pinout before wiring anything.
2. **Single-key breadboard prototype** — RP2350 + 1 ScreenKey module on header/socket mounts. Validate CircuitPython + `adafruit_st7735r`, render a static emoji + background color.
3. **USB link** — bring up `usb_hid` + `usb_cdc` composite on the device; confirm basic enumeration (your own host controller plugs in here later).
4. **Button handling** — hardware debounce + raw timestamped event emission only. Click-pattern resolution stays out of scope for this pass.
5. **Emoji asset pipeline** — convert a curated emoji set to RGB565 bitmaps sized for 128×128.
6. **Idle dimming** — implement the PWM backlight timeout behavior.
7. **Microphone** — wire the I2S mic via `audiobusio.I2SIn`, capture into a ring buffer during hold, implement the chunked + final-flag framing for streaming to host.
8. **Scale to 4 keys** — replicate wiring on the shared SPI bus (separate CS per module), per-key state loop.
9. **Custom PCB + enclosure** — once the above is validated on breadboard.
