---
id: "0061"
title: "Cache composed glyph frames on the board, so a layer switch sends slot numbers"
status: "backlog"
created: "2026-10-17"
updated: "2026-10-17"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0057", "0058", "0059", "0060", "0048", "0045", "0044"]
tags: ["firmware", "driver", "wire-protocol", "layers", "performance", "memory"]
---

# 0061 — Cache composed glyph frames on the board, so a layer switch sends slot numbers

## Problem

A layer switch sends every image again, though the user goes in and out of the same layers all day. Six keys take 0.8 s (task 0058). Each key costs about 140 ms: 10 ms to receive, 45 to 65 ms to inflate, and 70 to 80 ms to compose. The HID reports add a floor of 0.75 s, because they go out 150 ms apart (`minReportGap`).

## Goals

- A layer switch whose images are on the board takes at most 0.15 s, and sends no image.
- After a background preload at start-up, the first visit to a layer is as fast as a revisit.
- The board keeps the cache in RAM only, in a fixed pool of 128 slots that it allocates at start-up, and never writes flash for it.
- The host writes each distinct glyph file once, and `keys.json` only when a key state changes.

## Non-goals

- Board-resident layer navigation. The host still decides what each key shows.
- Key state over CDC as a general feature. Only the new "Show frames" message carries a color.
- The second core (task 0059). If this task lands, task 0059 is not needed: inflate then runs once per image, in the background.
- Caching a key that blinks. It keeps the paths of tasks 0044 and 0048.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — No cache: key state on CDC and glyphs composed on the host

The host blends the key color into each glyph, so the board skips the fill and blit. Key states move onto CDC, in one frame with the glyphs. This removes the 0.75 s HID floor.

- Good, because it needs no state on the board and no miss handling.
- Good, because it is the smallest change: `driver/layers/render.go`, `firmware/display_render.py`, and a frame type.
- Bad, because every switch still sends and inflates six images. The estimate is about 0.5 s for six keys, not 0.15 s.
- Bad, because a change of key color needs a new image, and the blink of an opaque image hides the whole image (task 0041).

### Approach B — Cache the decoded glyphs

The board keeps each inflated glyph (32 KB) in a slot, keyed by the hash of its pixels. A switch names the slots, and the board composes each frame.

- Good, because the key color stays a message field. A color change needs no new image.
- Good, because the cache holds one entry per image, not one per image and color.
- Bad, because the board still composes each key on a switch, 70 to 80 ms each. The estimate is about 0.45 s for six keys.
- Bad, because compose still allocates two bitmaps per call.

### Approach C — Cache the composed frames

The board keeps each composed frame in a slot of a fixed pool. A frame is one image over one color. A "Show frames" message names a slot for each key, and the board queues all the pushes together.

- Good, because a switch does no inflate and no compose. It costs two push groups, about 55 ms, and one message.
- Good, because one message is atomic. The panels change together, with no "Begin batch" (task 0060).
- Bad, because the cache holds one slot per image and color. A new status color or count badge is a miss that costs about 140 ms for that key.
- Bad, because the host must track which slot holds which frame, and must repair that after a board reset.

## Decision

Chosen: **Approach C — cache the composed frames**.

The goal is 0.15 s, and only a switch with no inflate and no compose reaches it. The cost accepted: 4 MB of the 7 MB free heap for the whole run, a longer start-up, two new host-to-device frame types and one reply, and a slot table that the host owns.

## Design

Measure first. A probe on the board records the speed of a 32 KB copy, the time to allocate 128 slots, and the free heap after the pool is allocated. The pool is fixed at 128 slots of 32 KB, 4 MB. If the probe shows that 128 slots leave less than 2 MB free, stop and ask the owner. The firmware does not shrink the pool by itself.

The messages, as frame types after those of task 0060:

- "Store frame", host to device: a slot number, a color, and a zlib stream of the glyph. The board inflates it and composes it into the slot. A glyph with no image is only the color.
- "Show frames", host to device: for each key, a slot number or "unchanged". The board queues the pushes of every named key in one pass.
- "Show miss", device to host: a mask of the keys that named an empty slot. The board draws the others.

Rules:

- The host owns the slot numbers. When all 128 slots are in use, it overwrites the least recently used one. The board is only an array of slots.
- The board allocates all 128 slots at start-up, before the loop, and never frees one. Slot numbers are 0 to 127, and the host never names a number above 127. Compose writes into the slot's own bitmap, and uses one scratch bitmap.
- The pool has no growth and no shrink. Memory use is the same for the whole run, so the heap does not fragment.
- After a reconnect, the host clears its table and stores the frames again. `transport.Reconnecting` already knows the connect event.
- At start-up the host stores the frames of every node of the layer tree, while the pad is idle. A press stops the preload until the switch is done.
- The host names each glyph file by its hash and writes it once. `keys.json` changes only with a key state.

Files to change:

- `docs/wire-protocol.md` — the three messages.
- `firmware/display_render.py`, `firmware/wire.py`, `firmware/app.py` — slots, store, show, miss.
- `driver/transport/` — `StoreFrame` and `ShowFrames` on `Transport`, and the slot table in `Reconnecting`.
- `driver/transport/statestore.go` — glyph files named by hash.
- `driver/layers/controller.go` — store, show, and preload.
- `driver/cmd/blinksend/main.go` — a `cached` scenario.
- `test/`, `driver/layers/`, `driver/transport/` — tests.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The board allocates 128 slots at start-up, and at least 2 MB of heap stays free. The probe also records the allocation time and the time of a 32 KB copy. **Proof:** the three figures in `firmware/README.md`.
- [ ] **DoD-2** — On the board, a Show of six stored frames takes at most 150 ms, and the six `PUSH_STARTED` records are at most 40 ms apart. **Proof:** the lines `six keys in N ms` and `push span N ms` of `blinksend --scenario=cached`, in three runs.
- [ ] **DoD-3** — After the preload, a layer switch sends a Show and no Store. **Proof:** `cd driver && go test ./layers -run Preload` passes.
- [ ] **DoD-4** — A Show that names an empty slot draws the other keys, keeps the old image on that key, and sends a Show miss. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test for it.
- [ ] **DoD-5** — After a reconnect, the host stores every frame again, and the next switch draws. **Proof:** `cd driver && go test ./transport -run Reconnect` passes, with a test for it.
- [ ] **DoD-6** — A key that blinks is not cached, and blinks as before. **Proof:** `.venv/bin/pytest test/test_app.py` passes, with a test for it.
- [ ] **DoD-7** — The firmware writes no file and no `nvm` byte at run time. **Proof:** `git grep -nE "open\(|nvm|storage\." firmware/*.py` lists no write.
- [ ] **DoD-8** — Six layer switches write no glyph file after the first, and write `keys.json` only when a key state changed. **Proof:** `cd driver && go test ./transport -run StateWrites` passes, with a counting store.
- [ ] **DoD-9** — `docs/wire-protocol.md`, `firmware/README.md`, and `driver/README.md` record the messages, the pool, and the state files. **Proof:** those three files.
- [ ] **DoD-10** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass.
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- A count badge or a status color changes often, so the cache misses and the switch is as slow as today for that key → only changed keys redraw, and a miss costs about 140 ms. DoD-2 measures the hit case only.
- The host table and the board disagree, for example after a code reload that keeps USB up → the board answers a Show of an empty slot with a miss, and the host stores the frame again.
- A preload store blocks the board for about 140 ms, and delays a press → the host stops the preload on a press.
- The pool takes memory that other code needs, and start-up is longer → DoD-1 records the free heap and the start-up time. The pool size is one constant, `SLOT_COUNT = 128`, and it is the cap.

## Open questions

- [ ] Does composing into a reused bitmap cost less than the 75 ms of today? — the probe answers it.

## Notes

Measured by task 0058 on 2026-10-17, on a Pico Plus 2 with CircuitPython 10.2.1:

- Six keys with `icons`: 0.80 s to 0.82 s. One key takes about 140 ms.
- A 32 KB `zlib.decompress` took 32 to 65 ms. `bytes(32768)` alone took about 11 ms.
- `HOST_MSG` gaps were 130 to 160 ms, because `minReportGap` holds HID reports 150 ms apart.
- A push group holds at most three panels (`pio_spi.MAX_GROUP`). Six keys are two groups of about 26 ms each.
- Flash has a limited number of erase cycles, and task 0044 removed the board's persistence. This cache must not persist.
- `Reconnecting.SendCustomGlyph` writes `glyph-N.bin` and `keys.json` on every send. A six-key switch writes about 200 KB to the host.
