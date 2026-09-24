---
id: "0042"
title: "Instrument the custom-glyph paint pipeline to find the ~1s latency's source"
status: "ongoing"
created: "2026-09-14"
updated: "2026-09-24"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/42"
branch: "0042-instrument-custom-glyph-paint-latency"
related: ["0025", "0030", "0033", "0039", "0041"]
tags: ["firmware", "display", "performance", "instrumentation"]
---

# 0042 — Instrument the custom-glyph paint pipeline to find the ~1s latency's source

## Problem

Setting a key's image takes about 1 second to appear on the panel. Task
0039 moved every glyph — a built-in digit or an uploaded picture — onto
the same custom-glyph pixel path. No measurement yet says which stage of
that path, from the driver's send call to `display.refresh()`, holds the
time.

## Goals

- Each stage of one custom-glyph paint — CDC transfer, decode, flash
  persist, pixel-buffer build, scene update, SPI refresh — has a recorded
  duration from a live hardware capture.
- The recorded durations name the stage, or stages, that account for most
  of the observed ~1s.
- The full `pytest test/` suite still passes with the added instrumentation.

## Non-goals

- Fixing the latency. This task only locates it. A follow-up task acts on
  the finding.
- Instrumenting the built-in key-state (HID) path. Task 0033 already
  measured its `refresh()` cost; this task covers only the custom-glyph
  (CDC) path task 0039 made the sole glyph path.
- Changing `docs/wire-protocol.md`'s Trace record byte layout. Only its
  code registry gains entries.

## Approaches considered

### Approach A — Temporary `time.monotonic_ns()` probes over the debug console

Add throwaway timing calls around each stage in `display_render.py` and
`app.py`, print the deltas, and read them over `make debug`'s console
port — the same method tasks 0031 and 0033 used to measure `refresh()`.

- Good, because it is proven: it already found `refresh()`'s 43-45ms
  full-panel cost and its per-row-overhead cause.
- Good, because a `print()` can sit inside a loop for a moment, giving
  finer detail than a single before/after pair — for example, splitting
  `raw_bitmap_tile_grid`'s transparency scan from its pixel-build pass.
- Bad, because `print()` over CDC is itself slow; placed inside the
  16,384-iteration pixel loops `raw_bitmap_tile_grid` runs, it would add
  more delay than the loop itself, corrupting the exact number sought.
- Bad, because the probes are hand-added and hand-removed, and record
  nothing that survives past one manual session — a later regression
  needs the same edit repeated from scratch.

### Approach B — Extend the Tracer flight recorder (task 0025) with new stage codes

Add trace codes for decode-complete, persist-done, glyph-build-done, and
refresh-done to `firmware/tracer.py`, record them from `app.py` and
`display_render.py`, and drain a capture through `driver/recorder` — the
same JSONL tool that already estimates the device/host clock offset.

- Good, because `Tracer.record` costs no allocation once enabled (its own
  docstring's design goal), so it does not add the GC pause a `print()`
  probe risks.
- Good, because `driver/recorder`'s `line` struct already decodes any
  trace code generically (`code`/`key`/`payload`/`timestamp`) — no Go
  change needed to capture the new codes, and its clock-offset estimator
  lines device-side and host-side timestamps up on one timeline.
- Bad, because `_apply_custom_glyph`'s payload arrives piecemeal across
  many `step()` calls (`CustomGlyphReader.feed`'s docstring), so a
  decode-complete timestamp folds in CDC transfer time and `step()`
  scheduling delay together, not transfer time alone.
- Bad, because it adds four entries to `docs/wire-protocol.md`'s trace
  code registry — a small, additive protocol change, but one other tasks
  now depend on staying stable.

### Approach C — Host-side timing around the driver's `SendCustomGlyph` call

Wrap `driver/transport/device.go`'s `SendCustomGlyph` with `time.Now()`
before the write and after the next key-state change is observed, with no
firmware change at all.

- Good, because it needs no firmware change, so it cannot itself perturb
  the timing it measures.
- Good, because it is the fastest of the three to add — one wrapper in
  one Go file.
- Bad, because "Set custom glyph" carries no device→host acknowledgment
  (`docs/wire-protocol.md`'s Framing type registry lists it host→device
  only), so "done" can only be inferred indirectly, not read off a reply.
- Bad, because it returns one total, not a breakdown — it cannot tell CDC
  transfer time from flash-persist time from pixel-buffer build time,
  which is what "figure out the bottleneck" asks for.

## Decision

Chosen: **Approach B — extend the Tracer flight recorder**. The task
needs a breakdown across stages that live on both sides of the USB link,
and `driver/recorder` already correlates device and host clocks for
exactly that reason. Approach A's finer per-loop detail is not worth its
own probes' overhead skewing the very loops under test, and Approach C
cannot localize past one end-to-end number. The cost accepted: CDC
transfer time and `step()` scheduling delay stay folded together in one
figure, not split further.

## Design

`render_key` gains an optional `tracer` and `key_index`, defaulting to
`None`, so `display_render.py` stays usable with no tracer in tests. When
set, it records a code around `_build_glyph`'s call (glyph-build-done) and
around `display.refresh()` (refresh-done). `MacroPad` passes its own
`self._tracer` and the key index through. `_apply_custom_glyph` and
`_persist_key_state` record decode-done and persist-done directly, since
both already run inside `app.py`.

Files to change:

- `firmware/tracer.py` — add `CUSTOM_GLYPH_DECODED`, `PERSIST_DONE`,
  `GLYPH_BUILT`, `REFRESH_DONE` trace codes.
- `firmware/app.py` — record the four new codes at their call sites; pass
  `self._tracer` and the key index into `render_key`.
- `firmware/display_render.py` — `render_key` accepts and uses the
  optional `tracer`/`key_index`.
- `docs/wire-protocol.md` — add the four codes to the Trace record code
  registry.
- `test/test_display_render.py`, `test/test_app.py` — cover that the new
  codes fire in order, using a fake tracer.
- `firmware/README.md` — record the captured per-stage figures and link
  this spec, alongside the existing "Loop period" and latency sections.

## Definition of done

- [x] **DoD-1** — The four new trace codes fire, in stage order, for one
  custom-glyph paint. **Proof:** a `test/test_app.py` test feeds one Set
  custom glyph message through a fake tracer and asserts the recorded
  code sequence. `test_custom_glyph_paint_trace_order` asserts key 3's
  code sequence is `CUSTOM_GLYPH_DECODED, PERSIST_DONE, GLYPH_BUILT,
  REFRESH_DONE`; `test/test_display_render.py` covers `render_key`'s
  half directly.
- [ ] **DoD-2** — A live capture on real hardware, for one custom-glyph
  Set through `keystate.html`, records a duration in milliseconds for
  each of: CDC transfer + decode, persist, glyph build, refresh.
  **Proof:** the JSONL capture file, or the derived figures, in this
  spec's Notes. **Missing:** no board is attached in this environment
  (no `/Volumes/CIRCUITPY`, no `/dev/cu.usbmodem*`); the instrumentation
  is in place and `firmware/README.md`'s new "Custom-glyph paint
  latency" section gives the capture steps, but no capture has been run.
- [ ] **DoD-3** — This spec names the stage, or stages, that account for
  most of the observed ~1s. **Proof:** a stated conclusion in this spec's
  Notes, backed by DoD-2's figures. **Missing:** blocked on DoD-2.
- [x] **DoD-4** — `docs/wire-protocol.md`'s trace code registry lists the
  four new codes. **Proof:** `docs/wire-protocol.md`, Trace record
  section.
- [ ] **DoD-5** — `firmware/README.md` records the per-stage figures and
  links this spec. **Proof:** `firmware/README.md`. **Missing:** the new
  "Custom-glyph paint latency" section links this spec and gives the
  capture recipe, but its figures are still the "Not yet measured"
  placeholder — blocked on DoD-2.
- [x] **DoD-6** — The full test suite passes. **Proof:** `.venv/bin/pytest
  test/ -q` passes (90 passed).
- [x] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:**
  PR body.

## Risks

- The ring buffer (`Tracer.__init__`'s `capacity`) may be too small to
  hold every record from one 32,768-byte transfer without dropping some →
  check `TRACE_DROPPED` in the capture; raise capacity for the capture
  session if it is nonzero.
- Enabling the tracer for this capture is itself a change to the running
  firmware's behavior → capture with tracing on, but re-confirm the ~1s
  figure with tracing off, so the reported bottleneck is not an artifact
  of tracing itself.

## Open questions

- [ ] Does most of the ~1s sit in `raw_bitmap_tile_grid`'s per-pixel
  Python loops (up to 32,768 iterations across its two passes), or
  somewhere else in the pipeline? — DoD-2's capture answers this.

## Notes

Task 0039 made the custom-glyph path (`wire.py`'s `CustomGlyphReader`,
`display_render.py`'s `raw_bitmap_tile_grid`) the only glyph path; task
0033's Notes measured the unrelated key-state/blink path's `refresh()` at
43-45ms for a full panel, which is far short of the ~1s reported here,
pointing at the custom-glyph decode or build stage as the more likely
cause — DoD-2 and DoD-3 confirm or rule this out.
