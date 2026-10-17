---
id: "0062"
title: "Draw the count pip as an antialiased red circle with a real font"
status: "backlog"
created: "2026-10-17"
updated: "2026-10-17"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0057", "0061", "0041"]
tags: ["driver", "layers", "rendering", "ui"]
---

# 0062 — Draw the count pip as an antialiased red circle with a real font

## Problem

The count pip in the bottom-right corner of a key is a dark box with a number drawn from a 3 by 5 pixel font, scaled up five times. The edges are blocky, the digits are hard to read, and a dark box does not look like a notification pip. The code is `drawBadge` and `digitFont` in `driver/layers/render.go`.

## Goals

- A key with a count above 0 shows a red circle with a white number, in the bottom-right corner.
- The number uses a real font, with smooth edges, and stays legible on the 128 by 128 panel.
- The pip is visible on a key of any status color, including red.
- A count of 0 shows no pip, and a count above 99 shows `99+`, as today.

## Non-goals

- A count that changes without a new image. The count stays part of the glyph (task 0061 lists the cost).
- Any change to the wire protocol or the firmware.
- A pip on the shift key, or any other pip position.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Draw the pip on the host with `golang.org/x/image`

`renderGlyph` draws an antialiased circle and the number with an embedded font, the Go Bold font from `golang.org/x/image/font/gofont/gobold`. It uses `font/opentype` and `vector`.

- Good, because the pip is code. Size, color, and font change in one place, and any count renders.
- Good, because nothing outside `driver/layers` changes.
- Bad, because it adds a module dependency and embeds a font in the driver binary. The Go 1.22.0 toolchain here limits the module to v0.24 or older, since v0.30 needs Go 1.23.
- Bad, because the count stays in the image. Each count is a different image, and task 0061's cache misses on each change.

### Approach B — Pre-rendered pip sprites in `assets/`

A script renders 100 small PNGs once, for 1 to 99 and `99+`, with any font. `renderGlyph` composites the right sprite.

- Good, because the driver needs no font and no new module.
- Good, because the look is a design file. A designer can change it with no Go change.
- Bad, because the repository holds 100 generated files, and a change of size or color means a new run of the script.
- Bad, because the sprite edges are antialiased against one fixed color. The driver must still blend them against the real key color.

### Approach C — Draw the pip on the board

The key state carries the count, and the firmware draws the circle and the digits from a small sprite sheet at compose time.

- Good, because a count change needs no new glyph. It removes the cache misses that the count causes in task 0061.
- Good, because the pip costs no bytes on the wire.
- Bad, because it changes the wire protocol, the firmware, and `docs/wire-protocol.md`. It is a larger task than the one asked for.
- Bad, because the firmware needs digit sprites in flash and a compose step for them, and a board that is not flashed shows no pip.

## Decision

Chosen: **Approach A — draw the pip on the host with `golang.org/x/image`**.

The request is a better look, and A changes only the host renderer. The cost accepted: one pinned dependency, a font in the binary, and one image per count. If the cache of task 0061 misses too often, a later task can move the pip to the board (Approach C).

## Design

The panel keeps one bit of transparency (task 0041): a pixel at half opacity or more is opaque, and any other is transparent. An antialiased edge drawn over a transparent corner would turn into a jagged edge. So `renderGlyph` takes the key color, and blends the pip against what lies under it: the icon where the icon is opaque, and the key color elsewhere. Every pixel of the pip is then opaque.

The pip:

- A circle of 44 pixels, filled red `#E02020`, with a white ring of 2 pixels, so it shows on a red key.
- The number is white, in Go Bold, centered in the circle. One or two digits use 26 pixels, and `99+` uses 20 pixels.
- The margin to the corner is 4 pixels, as `badgeMargin` today.

These sizes are a first guess. The owner adjusts them on the board.

Files to change:

- `driver/layers/render.go` — draw the pip, take the key color, and drop `digitFont`.
- `driver/layers/controller.go` — pass the key color to `renderGlyph`.
- `driver/go.mod`, `driver/go.sum` — `golang.org/x/image` at v0.24.0 or older.
- `driver/layers/layers_test.go` and `driver/layers/testdata/` — tests and the golden sheet.
- `driver/README.md` — describe the pip.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The pip is an antialiased red circle with a white ring and a white number, in the bottom-right corner. **Proof:** `cd driver && go test ./layers -run TestPipSheet` passes, and the golden file `driver/layers/testdata/pips.png` shows the counts 1, 9, 10, 42, 99, and 100 over a red key, a white key, and a transparent icon.
- [ ] **DoD-2** — A count of 0 draws no pip, and a count of 100 draws `99+` inside the circle. **Proof:** `cd driver && go test ./layers -run TestPip` passes, with a test for each case.
- [ ] **DoD-3** — After `transport.DecodePNGToRGB565`, no pixel of the pip's disc is transparent, over an icon with a transparent background. **Proof:** `cd driver && go test ./layers -run TestPipOpaque` passes.
- [ ] **DoD-4** — The ring differs from the key color for each of the five status colors. **Proof:** `cd driver && go test ./layers -run TestPipRing` passes.
- [ ] **DoD-5** — `driver/go.mod` pins `golang.org/x/image` at v0.24.0 or older, and the driver builds with Go 1.22.0. **Proof:** `grep x/image driver/go.mod`, and `cd driver && go build ./...` with `go version` printing `go1.22.0`.
- [ ] **DoD-6** — The old `digitFont` and the 3 by 5 drawing code are gone. **Proof:** `git grep -n digitFont driver/` lists nothing.
- [ ] **DoD-7** — On the board, keys with counts 1, 12, and 100 show a red circle and a legible number. **Proof:** a photo of the pad in the PR description.
- [ ] **DoD-8** — `driver/README.md` describes the pip: shape, colors, position, and `99+`. **Proof:** `driver/README.md`, section "Layers".
- [ ] **DoD-9** — Both test suites pass. **Proof:** `.venv/bin/pytest test/ -q` and `cd driver && go test ./...` pass.
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- A newer `golang.org/x/image` needs Go 1.23 or later, and the build fails on this machine → pin v0.24.0 or older, and check `go.mod` of a version before `go get`.
- The number is hard to read at 20 pixels for `99+` → the owner checks the photo of DoD-7, and the sizes are named constants.
- A different image for each count makes a cache miss on each change (task 0061) → only changed keys redraw, and a miss costs about 140 ms.

## Open questions

- [ ] Are 44 pixels, `#E02020`, and a 2 pixel ring the right size and color on the panel? — the owner, from the photo of DoD-7.

## Notes

- The panel treats a pixel at half opacity or more as opaque, and any other as transparent (`transport.DecodePNGToRGB565`). Antialiasing needs an opaque background under the pip.
- The Go fonts in `golang.org/x/image/font/gofont` are in the module, and need no extra file.
- `driver/README.md` says the badge sits in the bottom-right corner. The code agrees.
