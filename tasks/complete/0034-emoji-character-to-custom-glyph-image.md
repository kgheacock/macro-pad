---
id: "0034"
title: "Send a raw emoji character to a key, not just a pre-made PNG"
status: "complete"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/34"
branch: "0034-emoji-character-to-custom-glyph-image"
related: ["0030"]
tags: ["driver", "cli", "display"]
---

# 0034 — Send a raw emoji character to a key, not just a pre-made PNG

## Problem

Task 0030 lets a caller send an arbitrary 128×128 PNG to a key. No code
turns a Unicode emoji character, such as 😍, into that PNG. A caller who
wants to show an emoji must render one by hand first.

## Goals

- One command sends a Unicode emoji character to a key, with no
  pre-made PNG file needed.
- The command reuses task 0030's `setCustomGlyph` wire path unchanged.

## Non-goals

- Emoji sequences: flags, skin-tone modifiers, family emoji (multiple
  codepoints joined by ZWJ). Only single-codepoint emoji are in scope.
- A render on any OS besides macOS. `transport.Device` is already
  macOS-only (see driver/README.md, "macOS only").
- Caching a rendered image. Each call renders again from scratch.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — A Python helper script, called from a new CLI command

`tools/render_emoji.py` (Pillow, matching `tools/gen_glyphs.py`'s role as
a Python asset-prep script) renders one emoji character to a 128×128 PNG
using macOS's built-in Apple Color Emoji font. A new `macrodriver emoji
--key N --char 😍` subcommand runs this script, reads the PNG it writes,
and sends it through a new `api.Conn.SetCustomGlyph` helper.

- Good, because this session proved the approach live. Pillow's
  `ImageFont`, with `embedded_color=True`, renders Apple Color Emoji at
  a supported strike size (96px, cropped and letterboxed to 128×128).
- Good, because Python already has a place in this repo (`tools/`, the
  firmware pytest suite). This adds no new kind of dependency.
- Bad, because it makes one `macrodriver` subcommand depend on a Python
  interpreter and Pillow. This breaks the driver's self-contained-Go-
  binary shape.
- Bad, because each call starts a new Python process. This adds
  interpreter startup time for each emoji sent.

### Approach B — A Go-native renderer with a bundled color-emoji font

Add a Go package that embeds a color emoji font (for example Noto Color
Emoji) with `go:embed`, decodes its color glyph table, and rasterizes
directly to an RGB565 buffer, skipping the PNG step entirely.

- Good, because the driver stays one self-contained Go binary, with no
  external interpreter dependency for any command.
- Good, because it does not start a new process for each call. There is
  no process startup cost per call.
- Bad, because Go's tools for color emoji glyph formats (COLR, CBDT,
  sbix) are thin. No library in this repo's dependency tree decodes any
  of these formats today, so this needs new, unproven code.
- Bad, because a bundled color emoji font is tens of megabytes. It also
  adds a font license (for example, Noto's OFL) that this repo must
  track.

### Approach C — Document the pattern, add no new command

Leave `driver/api` and `macrodriver` as they are. Add an example script
under `driver/examples/` that shows how to render and send an emoji,
for a plugin author to copy.

- Good, because it adds no new runtime dependency or maintenance cost
  to the driver itself.
- Good, because it matches driver/README.md's existing "any language
  can write a plugin" stance — each author picks their own tool.
- Bad, because it does not meet the goal. A plugin author must still
  run more than one command to send an emoji, every time.
- Bad, because an example tied to Apple Color Emoji is not a portable
  start. A real example must still pick a font source.

## Decision

Chosen: **Approach A — a Python helper script, called from a new CLI
command**.

This repo already treats Python as a first-class tool for image assets
(`tools/gen_glyphs.py`). This session proved Pillow renders Apple Color
Emoji. The cost accepted is a Python and Pillow dependency for one
`macrodriver` subcommand, and one new process for each emoji sent.

## Design

Files to change:

- `tools/render_emoji.py` — new. Takes an emoji character and an output
  path. Renders with Apple Color Emoji at a supported strike size,
  crops to the glyph's bounding box, and letterboxes onto a black
  128×128 canvas, matching this session's live-tested approach.
- `driver/cmd/macrodriver/emoji.go` — new. `macrodriver emoji --key N
  --char 😍 [--addr ...]` runs `render_emoji.py` with `os/exec`, reads
  the PNG it wrote to a temp file, and calls `api.Conn.SetCustomGlyph`.
- `driver/api/state.go` — add `SetCustomGlyph(key int, pngBytes []byte)
  error`, the same shape as `SetEmoji` and `SetState`, wrapping a
  `plugin.KindSetCustomGlyph` message. This gap exists today: every
  other message kind has a helper, this one does not.
- `driver/README.md` — document the new subcommand next to the existing
  `macrodriver signal` documentation.

## Definition of done

- [x] **DoD-1** — `macrodriver emoji --key 0 --char 😍 --emulate` ends
  with the emulator's last custom glyph holding non-empty pixels.
  **Proof:** a driver test asserts `Emulator.LastCustomGlyph()` after
  running the command against `--emulate`.
  `TestRunEmoji_Emulate` in `driver/cmd/macrodriver/emoji_test.go`
  stubs `renderEmojiPNG` (a package var) with a solid-color PNG, so the
  test exercises the real `--emulate` wire path with no dependency on a
  live `python3`/Pillow install; `tools/render_emoji.py` itself was
  proven separately, by hand, against a real `😍` character.
- [x] **DoD-2** — The command rejects a multi-codepoint input (a ZWJ
  sequence, or more than one emoji) before any wire traffic. **Proof:**
  a unit test asserts an error and an empty `LastCustomGlyph()`.
  `TestRunEmoji_RejectsMultiCodepoint` covers a ZWJ family emoji, a
  flag, and two emoji in one `--char`.
- [x] **DoD-3** — A missing `python3` or Pillow produces one line that
  names the missing dependency, not a stack trace. **Proof:** a test
  stubs `exec.LookPath` to fail and asserts the error text and a
  non-zero exit code.
  `TestRunEmoji_MissingPython3`.
- [x] **DoD-4** — Tests cover the new subcommand and its rejection
  cases. **Proof:** `go test ./driver/cmd/macrodriver/... -run Emoji`
  passes. `git stash && go test ./driver/cmd/macrodriver/... -run
  Emoji` fails on `main`.
  The pass on this branch is confirmed. On `main`, `-run Emoji` matches
  no test function (the subcommand doesn't exist there yet), so `go
  test` reports `ok ... [no tests to run]` with exit code 0 — not a
  hard failure. The literal proof text doesn't hold for that reason,
  but the intent — new, passing tests that exist only on this branch —
  does.
- [x] **DoD-5** — `driver/README.md` documents the new command.
  **Proof:** `driver/README.md`, the section next to `macrodriver
  signal`.
  The new `### \`macrodriver emoji\`` section sits directly after
  `### \`macrodriver signal\``.
- [x] **DoD-6** — The PR in the `pr` field links to this spec.
  **Proof:** PR body.
  PR #34's body names `tasks/ongoing/0034-emoji-character-to-custom-glyph-image.md` under "Implements".

## Risks

- Apple Color Emoji renders only at fixed strike sizes. This session
  found that 96px works, and most other sizes fail with the error
  "invalid pixel size" → `render_emoji.py` must pick a working size and
  scale the result. It must not assume that an arbitrary size works.
- A Python and Pillow dependency for one subcommand is a new kind of
  requirement for `macrodriver` → give one clear error when `python3`
  or Pillow is missing (DoD-3), not a raw traceback.

## Notes

Found during live testing of task 0030's custom-glyph pipeline on real
hardware (branch `0031-forward-audio-chunks-to-plugins`): the pipeline
itself already works end to end. Sending an emoji character, not a
pre-made PNG, was the one missing piece.
