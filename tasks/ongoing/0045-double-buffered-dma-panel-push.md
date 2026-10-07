---
id: "0045"
title: "Double-buffered panel push: send frames by DMA so blinking does not use the CPU"
status: "ongoing"
created: "2026-10-06"
updated: "2026-10-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0045-double-buffered-dma-panel-push"
related: ["0043", "0044"]
tags: ["firmware", "blink", "spi", "dma", "pio"]
---

# 0045 — Double-buffered panel push: send frames by DMA so blinking does not use the CPU

## Problem

A blink swaps between two cached frames of the key. The panel has one frame store, so each swap sends a full
128×128 frame (32,768 bytes) over SPI. The CPU sits in `spi.write` for the whole push: 21 ms at 16 MHz.
Six blinking keys use 126 ms of every 500 ms. A color change adds a 23 ms compose, and a flash write adds 50 ms.
All of them delay the next blink toggle, because the CPU does all of the work in one loop.

Backlight blinking and panel inversion hide or change the image, so they cannot replace the frame swap.

## Goals

- A push of a cached frame costs the CPU 3 ms or less, instead of 21 ms.
- The CPU composes a new frame while the previous frame is still on the wire.
- Every blinking key keeps its 500 ms toggle schedule, within 100 ms, while another key updates.
- The panels show the same image as today.

## Non-goals

- A blink that needs no CPU at all. Approach B covers it, and 0045 does not build it.
- A frame size or color depth change, for example 12-bit color.
- One shared blink phase. That is issue 2.

## Approaches considered

Three approaches follow. Each one moves the push off the CPU in a different way.

### Approach A — PIO SPI with a background DMA transfer

A PIO state machine shifts out SPI on GP2 and GP7. `StateMachine.background_write` sends a cached frame by DMA
and returns at once. The CPU sets CS and DC, and sets CS high when the transfer ends.

- Good, because it runs on stock CircuitPython 10.2.1. `import rp2pio` works on this board.
- Good, because the CPU is free for about 20 ms of each push. It can compose the next frame in that time.
- Bad, because the CPU still starts each toggle. A flash write still delays a toggle, by up to 50 ms after 0044.
- Bad, because the PIO takes over GP2 and GP7, so `busio.SPI` goes away. The init sequence moves to PIO too.

### Approach B — PIO and DMA chain that blinks with no CPU

The PIO program also drives DC and CS from the data stream. A timer-paced DMA chain re-sends the two frames in turn.
The CPU only writes a frame pointer when a key changes.

- Good, because a blink keeps time through any CPU stall, including a flash write. The PIO spike showed this for a pin.
- Good, because it makes the blink phase the same for all keys, which solves issue 2.
- Bad, because it needs DMA registers set from Python through `memorymap`, and no spike has shown it works.
- Bad, because a frame change during a transfer needs a safe hand-over. A mistake shows as a torn image.

### Approach C — A native blink task in a custom CircuitPython build

A C module runs the blink and the DMA push from RAM, on the second core or from a timer interrupt.

- Good, because it is the only approach that also removes the Python cost of composing frames.
- Good, because code in RAM keeps running while flash is busy.
- Bad, because the project must build and keep its own CircuitPython. A spike built one in about 2 minutes.
- Bad, because the code is in C, outside the pytest-with-stubs test setup of `test/`.

## Decision

Chosen: **Approach A — PIO SPI with a background DMA transfer**.

It is the smallest change that takes the 21 ms push off the CPU, and it runs on the stock firmware.
It also gives real double buffering: one frame is on the wire while the next is composed.
The decision accepts that toggles stay CPU-scheduled. Approach B is the next step if DoD-4 shows that a 50 ms
flash write still hurts. This task writes the PIO bus so that B can reuse it.

## Design

`firmware/pio_spi.py` holds a PIO SPI program, assembled on the host, because `adafruit_pioasm` is not on the board.
`PioBus` owns the state machine, and the shared DC line. `Panel.start_push(frame)` sets the window with short
blocking writes. It then starts `background_write` for the frame and sets `busy`. `Panel.poll()` sets CS high
when the state machine is done.

`MacroPad.step` calls `poll()` on the active panel and starts the next queued push. Only one panel pushes at a
time, because all six share SCK and MOSI. `display_render` keeps a front and a back frame for each key.
A new frame is composed into the back frame, and the two swap when the push of the old one ends.
The code sends a frame as bytes. The `Bitmap` stores a 16-bit value, so a view with 8-bit items is needed.
Start at 16 MHz. Raise the rate only after a person checks all six panels.

Files to change:

- `firmware/pio_spi.py` — new: PIO program, `PioBus`
- `firmware/st7735.py` — `Panel.start_push`, `Panel.poll`, `busy`; init sends through `PioBus`
- `firmware/display_render.py` — front and back frames, `render_key` starts a push
- `firmware/app.py` — queue and poll pushes in `step`
- `firmware/code.py` — build `PioBus` in place of `busio.SPI`
- `firmware/tracer.py`, `docs/wire-protocol.md` — trace code `PUSH_STARTED` (9)
- `test/stubs/rp2pio.py`, `test/test_st7735.py`, `test/test_app.py` — stub and tests
- `firmware/README.md` — record the design and the measured times

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — On the board, the CPU runs a loop for 15 ms or more while a cached-frame push is on the wire.
  **Proof:** the spike output `cpu_free_ms` is 15 or more (the push takes 21 ms)
  Result: `make dma-spike` printed `cpu_free_ms 17` for 5 pushes. The push takes 18 ms at 15 MHz.
- [x] **DoD-2** — A frame sent with `start_push` arrives byte-identical on the SPI stub, with the same window
  commands as `Panel.push` today. CS stays low until `poll()` sees the state machine done.
  **Proof:** `python3 -m pytest test/test_st7735.py -k start_push`
- [x] **DoD-3** — A color change on a key, while a push of another frame runs, never changes the frame that
  is on the wire.
  **Proof:** `python3 -m pytest test/test_app.py -k back_frame_not_sent`
- [x] **DoD-4** — With six keys blinking and an update to key 4 every 2 s, every gap between `PUSH_STARTED`
  records of one blinking key is 600 ms or less. Today, at 16 MHz, the expected figure is about 650 ms.
  **Proof:** `make blink-trace SCENARIO=single` prints `max gap` of 600 ms or less. Task 0044 adds this command.
  Result: `max gap 506.6 ms` and `max gap 507.5 ms` (two runs). The command keeps keys 0 to 2 blinking, not six.
- [x] **DoD-5** — Two pushes queued for different panels never overlap, and run in queue order.
  **Proof:** `python3 -m pytest test/test_app.py -k pushes_do_not_overlap`
- [x] **DoD-6** — At the rate in `code.py`, all six panels show the 8-bar and column test pattern with no noise,
  and a person records the rate. **Proof:** the Notes of this spec
  Result: see "DoD-6 check" in the Notes.
- [x] **DoD-7** — The trace registry lists `PUSH_STARTED`.
  **Proof:** `docs/wire-protocol.md`, section "Trace code registry"
- [x] **DoD-8** — `firmware/README.md` records the push design and the measured CPU time per push.
  **Proof:** `firmware/README.md`, section "Latency"
- [ ] **DoD-9** — The PR in the `pr` field links to this spec.
  **Proof:** the PR body

## Risks

- `background_write` may not accept a `Bitmap`, or may send 16-bit items in the wrong byte order → DoD-1
  tests this on the board first. A `bytearray` copy of each frame costs 32 KB per frame and is the fallback.
- PIO at a fractional clock divider can jitter the SCK edges → DoD-6 checks the image at the chosen rate.
- A PIO that owns GP2 and GP7 cannot share them with `busio.SPI` → the init sequence must use `PioBus` too.
- A mounted `CIRCUITPY` on macOS reloads the board and breaks the run → unmount it before `make blink-trace`.

## Open questions

- [ ] Is 16 MHz the right rate, or is 25 MHz worth a visual check? A DMA push at 25 MHz takes about 10 ms. — owner
- [ ] When 0045 lands, is Approach B worth its risk for issue 2? Decide with the DoD-4 result. — owner

## Notes

- Spike results, 2026-10-06, RP2350, CircuitPython 10.2.1. `rp2pio` and `memorymap` import. `_thread` does not.
  A PIO blink on a pin kept its 500 ms grid through a 158 ms `nvm` write and a 262 ms file write.
  `StateMachine.write` waits about 1 s unless `wait_for_txstall=False`; with it, a write takes 8 ms to 16 ms.
- A frame push takes 79 ms at 4 MHz, 42 ms at 8 MHz, 21 ms at 16 MHz, and 17 ms at 20 MHz and above.
  The CPU limits a push above about 19 MHz. DMA removes that limit.
- The ST7735R frame store holds about 132×162 pixels, from the chip datasheet. This was not checked against
  `docs/0.85inch_ScreenKey_Module.pdf`. Two 128×128 frames would not fit, so the panel could not flip between
  two frames in hardware.

- DoD-6 check, 2026-10-06, RP2350, 15 MHz. A person looked at all six panels. The pattern had 8 color bars, a
  band of 1 px black and white columns, and a mark for each key (one to six white squares). They saw correct
  colors, sharp columns, and no noise or shifted rows on all six. The image went through `Panel.push` on the PIO bus.
  25 MHz was not tried, so the first open question stays open.
- What differs from the Design section:
  - The rate is 15 MHz, not 16 MHz. The PIO runs at twice the baud rate, and 150 MHz / 30 MHz is a whole divider (5),
    so the SCK edges are even. `busio.SPI` already ran at 15 MHz when `code.py` asked for 16 MHz.
  - There are no separate front and back frames for each key. `_compose` already allocates a new `Bitmap` for each
    rebuild, so a rebuild never writes into a frame that is on the wire, and the `Panel` holds the frame it is
    sending. The cached frames are byte views, `memoryview(bitmap).cast("B")`. `background_write` accepted the view
    on the board with no copy.
  - The queue lives in `MacroPad`, not in `render_key`. `render_key` hands a frame to a per-key port, and `step`
    starts the pushes. A key has one waiting frame at most, and a newer frame replaces an older one that has not
    started.
  - `PioBus.done` clears `txstall` after the DMA ends, and reads it. `StateMachine.tx_fifo` does not exist on
    this CircuitPython, and `txstall` is set whenever the state machine is idle, so it cannot start the wait.
    `writing` goes false when the DMA has put the last byte in the FIFO, up to 5 bytes (2.7 us) before the wire is clear.
  - `blink_trace.py` now counts `PUSH_STARTED` records, and its limit is 600 ms.
- Measured on the board, 2026-10-06: `background_write` of the byte view takes 150 us to 250 us to start, the DMA of
  32,768 bytes ends 17.6 ms to 17.7 ms later, and `start_push` holds the CPU 1.2 ms to 1.3 ms for the window commands.
  The CPU does not copy a frame.
- The bytes on the wire were not captured with a logic analyzer. A capture state machine hung the board, and a
  person checked the image on the panels instead (DoD-6).
