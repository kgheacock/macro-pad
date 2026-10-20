---
id: "0074"
title: "Draw emoji refs from Apple Color Emoji at run time, with the Fluent PNGs as the fallback"
status: "backlog"
created: "2026-10-20"
updated: "2026-10-20"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-ctk4fk"
related: ["0034", "0073"]
tags: ["assets", "emoji", "driver", "layers"]
---

# 0074 — Draw emoji from Apple Color Emoji at run time

## Problem

Task 0073 gave every folder and Claude Code session an emoji, drawn from
`assets/emoji/<cp>.png`: Microsoft Fluent Emoji (flat), committed because its
licence (MIT) allows it. The owner wants the board to show Apple Color Emoji,
the pictures the Mac shows everywhere else, instead.

Apple's pictures may not be copied into this public repo, so they cannot be
committed as PNGs the way Fluent was (0073, Problem). The owner's direction,
2026-10-20: do not generate files; render on the host at run time.

## Goals

- On a Mac, every emoji ref (`iconEmoji`, or an `iconRef` under `emoji/`) shows
  the Apple Color Emoji picture of its code point, fitted to the round as the
  Fluent pictures are (farthest solid pixel 50 px from the centre).
- Nothing of Apple's is written to disk or committed.
- `set.txt`, the refs, the inbox hash, and the frame cache do not change: a
  folder keeps its emoji, only the picture changes.
- A host with no font (Linux, CI), or a code point the font lacks, draws the
  Fluent PNG, so tests and goldens are unchanged.

## Non-goals

- Emoji sequences (ZWJ, skin tones, flags): they need the font's shaping
  tables. `set.txt` has none (0073's rules), so they draw Fluent.
- Typed emoji in `iconText` (0046, 0069).
- Removing the Fluent PNGs: they stay as the fallback and for the tests.

## Facts that decide it

- Apple Color Emoji is `/System/Library/Fonts/Apple Color Emoji.ttc`, a
  collection of about 180 MB. Its pictures are PNGs in the `sbix` table, one
  set per strike size (20 to 160 px); Pillow, through FreeType, which reads
  only PNG strikes, drew it at 96 px in task 0034.
- `golang.org/x/image/font/sfnt` cannot read color tables (CBDT, COLR, sbix).
- The sbix layout is small: a strike is a ppem, a ppi, and one offset per glyph;
  a glyph record is an origin, a 4-byte type (`png `, `dupe`, …), and the data.
  The code point to glyph map is the format 12 `cmap`.
- `loadIcon` is the one place both `iconEmoji` and an `iconRef` read a PNG,
  once the `iconRef` branch of `renderIcon` calls it.
- The frame cache keys on the ref, so a picture fixed for the life of the
  process needs no cache change; a new font shows after a `macropadd` restart.

## Approaches considered

### Approach A — Read the sbix PNGs in Go, in the driver (chosen)

`driver/layers/emojifont.go` opens the font, reads the table directory, the
`cmap` (format 12), `maxp`, and the `sbix` strike offsets with `ReadAt`, and on
the first draw of a ref decodes that glyph's PNG from the largest strike that
has one, fits it, and keeps it in memory.

- Good, because nothing is written to disk and nothing new is installed: no
  Python, no Pillow, no cgo.
- Good, because the font is read lazily: a few KB per emoji, not 180 MB.
- Good, because the fit is the same rule as `tools/emoji`, in the same package
  as the round it must fit.
- Bad, because it is about 330 lines of font parsing the repo owns.
- Bad, because a strike in Apple's own compressed format (`emjc`, seen in iOS
  fonts) is passed over for a smaller PNG strike, or the Fluent file.

### Approach B — Shell out to `tools/render_emoji.py` per emoji

Reuse 0034's Pillow script: run it on the first draw of a ref, read the PNG.

- Good, because the script exists and Pillow draws the font today.
- Bad, because it needs Python and Pillow on the host, and a temp file per
  emoji, which is generated files again.
- Bad, because a process per emoji on the draw path adds about 200 ms each.

### Approach C — CoreText through cgo

Draw the emoji with macOS's own text stack into a bitmap.

- Good, because it handles every emoji, sequences included, exactly as macOS.
- Bad, because the driver gains cgo and a darwin-only build, and the tests
  cannot run on Linux.

## Decision

Chosen: **Approach A**. It renders on the host at run time as the owner asked,
with no new runtime dependency, and leaves every committed asset, ref, and hash
alone. It accepts the font parser and the single-code-point limit, which
`set.txt` already obeys.

## Design

- `layers.AppleColorEmoji` is the font's path. `layers.UseEmojiFont(path)` opens
  it (or records why not, `layers.EmojiFontErr()`); `""` turns it off. Tests
  never call it, so goldens stay Fluent.
- `fontIcon(ref)`: for a ref under `emoji/` whose name is one code point,
  optionally `-fe0f`, look the code point up in the cmap, read its picture from
  the largest strike whose record is `png ` (following up to 4 `dupe`
  records), and fit it: the box of pixels of alpha 0x80 or more, its centre,
  the farthest of them from it, then a `CatmullRom` affine transform onto
  128x128 that puts that pixel 50 px from the centre. Each ref is drawn once
  and kept, `nil` included.
- `loadIcon` validates the ref, then returns `fontIcon(ref)` when it is not nil,
  and the PNG under `assets/` otherwise. `renderIcon`'s `iconRef` branch now
  calls `loadIcon`.
- `macropadd --emoji-font FILE`, default `layers.AppleColorEmoji`; `''` turns it
  off. A missing file is silent (Linux); any other error is logged once, and the
  Fluent PNGs draw.

Files to change:

- new `driver/layers/emojifont.go`, `driver/layers/emojifont_test.go`
- `driver/layers/emoji.go` (`loadIcon`), `driver/layers/render.go` (`renderIcon`)
- `driver/cmd/macropadd/main.go` (`--emoji-font`)
- `driver/README.md`, `assets/emoji/README.md`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — The parser reads a font built to the sbix spec, plain and as a collection: the cmap, the largest PNG strike, and `dupe` records. **Proof:** `go test ./layers -run 'TestEmojiFont$|TestEmojiFontStrikes'` (its fixture also parses with fontTools, as noted in the PR).
- [ ] **DoD-2** — With a font, an emoji ref draws the font's picture fitted to radius 50 and centred, in `iconRef` and both `iconEmoji` slots; a code point the font lacks draws the Fluent PNG; `github.png` is unchanged. **Proof:** `go test ./layers -run TestEmojiFont$`
- [ ] **DoD-3** — With no font, or a file that is not a font, every emoji ref draws the Fluent PNG byte for byte. **Proof:** `go test ./layers -run TestEmojiFontMissing`
- [ ] **DoD-4** — Without `UseEmojiFont`, the 0073 goldens and the round check pass unchanged. **Proof:** `go test ./layers -run 'TestIconEmoji|TestIconRound'`
- [ ] **DoD-5** — `macropadd --help` lists `--emoji-font`, and `driver/README.md` describes it and the fallback. **Proof:** `go run ./cmd/macropadd --help 2>&1 | grep emoji-font`; `grep -n "Apple Color Emoji" driver/README.md`
- [ ] **DoD-6** — On the owner's Mac, after a `macropadd` restart, the inbox's channel keys show Apple Color Emoji inside the round, with the same emoji per folder as before, and nothing new under `assets/`. **Proof:** the owner's note in this spec's Notes, and `git status --porcelain assets` printing nothing.

## Risks

- The Mac's font stores a strike in a format other than PNG (`emjc`) → that
  strike is passed over for the next largest PNG one; 0034 proved 96 px is PNG.
  If none is, the key shows Fluent, and DoD-6 catches it.
- A macOS update moves or renames the font → `--emoji-font` names the new path;
  until then the keys show Fluent, never nothing.
- Apple's picture of an emoji differs from Fluent's enough that two of 0073's
  "no near twin" rules fail (two emoji look alike) → change `set.txt`, as 0073
  allows; the folders on the changed line move.
- Apple pictures carry their own margins and shadows → the fit measures solid
  pixels (alpha 0x80 or more), as for Fluent, so faint shadows may pass the
  50 px line but stay inside the round of 58.

## Notes

- Spike, 2026-10-20 (not committed): a Pillow script fitted all 149 emoji of
  `set.txt` from Noto Color Emoji on Linux to radius 50 (worst 50.5 px). The
  owner then chose run-time rendering over generated files, so the spike's
  build step was dropped.
