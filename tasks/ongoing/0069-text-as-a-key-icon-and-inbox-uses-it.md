---
id: "0069"
title: "Text as a key icon (iconText) for inbox, and a smaller icon round (58 px) with the pip and icons moved to match"
status: "ongoing"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/87"
branch: "0069-text-as-a-key-icon-and-inbox-uses-it"
related: ["0041", "0057", "0061", "0062", "0066"]
tags: ["layers", "plugin", "inbox", "driver", "protocol", "firmware", "assets"]
---

# 0069 — Text as a key icon (iconText), and a smaller icon round

The spec has three parts. Part 1 adds `iconText` and changes `inbox`. Part 2
shrinks the round and moves the count pip. Part 3 resizes `assets/*.png`. They
share one PR because Part 1's text box depends on the new pip place. The
Definition of done covers all three.

## Problem

An icon today is a 128x128 PNG file under `assets/`, named by `iconRef`. A
client that wants a word on a key must draw the PNG itself. `inbox` does this:
`inbox/src/png.js` holds a 5x7 bitmap font and a PNG encoder (226 lines) and
writes `assets/.generated/inbox/<channel>.png` once per channel. The file is
never redrawn, the files are never deleted, and the daemon must share an
absolute `--assets-dir` with `macropadd`. The tile is opaque and dark, so the
key status color never shows around it. The driver already has a real font
(`gobold`, in `driver/layers/pip.go`).

Separately, the owner wants the white round smaller: radius 58, not 60
(`iconRoundRadius`). The count pip sits partly on the round's edge, and the
icons were sized for 60, so both must follow.

## Goals

- A key or child can carry `iconText`. The driver draws it as a key icon.
- `inbox` sends `iconText` for each channel and writes no PNG file.
- `iconRef` validation (`ValidateIconRef`) is not weakened.
- `iconRoundRadius` is 58. The pip keeps its overlap with the round's edge.
- Every icon in `assets/` lies inside the new round.

## Non-goals

- Emoji and color glyphs. Tasks 0034 and 0046 cover emoji.
- Choosing the font, color, or size per call. The driver fixes them.
- Text on the top-level `inbox` key. It keeps `iconRef: gmail.png`.
- Text that wraps by itself. The sender puts in the line break.
- A runtime scale of icons. Part 3 is a one-time, committed change.

## Part 1 — Text as an icon

### Approaches considered

Three approaches follow. They differ in who draws the text and how a key names it.

#### Approach A — A new `iconText` field, drawn by the driver

`setLayerKey` and `layers.Node` get a string field `iconText`. `renderGlyph`
draws it with `gobold`, in black and gray on a plain white tile. There is no round.

- Good, because `iconRef` keeps one meaning (a path), so `ValidateIconRef` and its path-traversal tests do not change.
- Good, because the text has its own rules (length, control characters) in `Node.validate`, and an error names the field.
- Good, because one font and one fitting routine serve every client, as the pip does.
- Bad, because it adds a field to `layers.Node`, `plugin.SetLayerKeyPayload`, `view`, and `frameKey`, and to `layers.json`.
- Bad, because a client of an older `macropadd` sends a field that is ignored, and the key shows only its color.

#### Approach B — A `text:` scheme inside `iconRef`

`iconRef: "text:Hello"` means draw "Hello". The driver draws it as in A. No
new field.

- Good, because no struct, payload, or `frameKey` changes. The ref string is already the cache key.
- Good, because an older `macropadd` fails the same way for all refs: it cannot open the file.
- Bad, because `iconRef` now holds a path or a text. `text:a/../b` and a real file named `text:x.png` are both legal paths today, so the rule needs a reserved prefix and a new test set around `ValidateIconRef`.
- Bad, because every reader of `iconRef` (`assetPath`, `cmd/macro_streamer`, the CRUD page) must learn the prefix or it tries to open a file.
- Bad, because the length and control-character limits have no field to attach to.

#### Approach C — The client draws the text into a PNG (the status quo)

Keep `inbox/src/png.js`. Fix its faults: redraw on change, delete old files.
Offer the same helper to other plugins.

- Good, because the driver, the wire protocol, and `layers.json` do not change.
- Good, because it already works and has golden tests (`inbox/test/render.test.js`).
- Bad, because each client re-implements a font and a PNG encoder. The Node font has 95 ASCII glyphs and no kerning.
- Bad, because the plugin and `macropadd` must share one absolute assets path, and the files pile up without a limit.
- Bad, because the tile is dark. The owner wants black and gray text on white, and a client cannot match the driver's look without the driver's font.

### Decision

Chosen: **Approach A — a new `iconText` field**.

It is the only approach that keeps `iconRef` a plain path and removes the font
from every client. The task accepts a new field in four structs and a stale
`macropadd` that ignores it until it restarts.

### Design

`iconText` and `iconRef` are exclusive. A node with both is rejected, as a bad
`iconRef` is today (`setLayerKey id=... dropped`).

**Check** (`Node.validate`): at most 32 runes and at most 2 lines (one `\n`).
Reject control characters other than `\n`, and a text that is empty after trim.
Empty means no text icon.

**Draw** (`renderGlyph`, new `drawIconText` in a new `driver/layers/text.go`):
fill the whole 128x128 tile with white, with no round, and draw the text on it
with `gobold`. The name (the last line) is black `#000000`. The parent (the
first line of a two-line text) is gray `#6B6B6B`, so the name stays the
stronger line. A one-line text is black. The colors are constants.
The text box is x 8..120, y 10..76, with each line centered in it and the
block centered vertically. It ends above the pip sprite, which starts at y 79
(Part 2), so a count never covers text. It does not move with the count.

**Fit.** Each line is sized on its own.

- One line: the largest of 44, 36, 28, 22, 18 px that fits 112 px.
- Two lines: the largest of 28, 22, 18, 14 px for each line.
- A line that is too wide at the smallest size is cut and ends in `…`.
- A rune with no glyph in `gobold` (glyph index 0) draws as `?`. Emoji, CJK,
  and combining marks therefore show `?`. No error.

The tile is fully opaque and the edges of the letters are blended against the
white, so one-bit transparency does not apply and no edge is half transparent.
The key color never shows around a text icon. That is the owner's choice.

**Cache.** `frameKey` becomes `{ref, text, color}`. `hasGlyph` is true for
either. For a text icon the color is left out of the key (zero): the tile is
opaque, so the color cannot change the frame. The count pip stays outside the key (task 0062). Two keys with the same
text share one slot, whatever their colors.

**inbox.** `render.js` sends `iconText` = `<parent>\n<last segment>`, or the
last segment alone. It drops `png.js`, `drawImage`, and the `.generated`
directory. `--assets-dir` is still accepted so an installed service starts. It
is ignored, with one stderr line. `install.sh` and the service templates stop
passing it. The old `.generated/inbox` files stay; the README says to delete them.
The generated PNGs need no resize: they are deleted. (If `png.js` shipped
alone, its full-panel tile ignores the round, and only its name area, which ends
at y 80, would need a 1 px check against the new pip.)

Files to change:

- `driver/layers/doc.go`, `driver/layers/controller.go`, `driver/layers/frames.go` — field, check, view, `frameKey`
- `driver/layers/render.go`, new `driver/layers/text.go` — draw
- `driver/plugin/protocol.go`, `driver/plugin/server.go` — `iconText` in the payload
- `inbox/src/render.js`, `inbox/src/cli.js`, `inbox/src/daemon.js`, `inbox/scripts/*`, `inbox/README.md`, `inbox/completions/_inbox` — use it
- `inbox/src/png.js`, `inbox/test/render.test.js`, `inbox/test/fixtures/channel-macro-pad.driver.png`, `driver/layers/inbox_image_test.go` — remove, replace

## Part 2 — A round of radius 58, and the pip moves with it

### Facts that decide it

- The pip sprite is 44x44. The firmware lands it at `x0 = 128 - PIP_MARGIN - PIP_DISC`
  (`firmware/display_render.py`, `stamp_pip`, `PIP_MARGIN = 4`). It places each
  digit cell at `x0 + (44 - width * count) // 2`. So the host cannot choose
  where the sprite lands. The host sends only the sprite pixels.
  `docs/wire-protocol.md` ("Store pip sheet") fixes the sprite layout, not the
  landing place. The host fallback `DrawPip` uses `pipMargin` in `pip.go`, so
  the two code paths agree today only because both say 4.
- The disc centre is (102, 102). That is 53.7 px from the round's centre
  (64, 64). At radius 60 the centre lies 6.3 px inside the edge. At radius 58
  and margin 4 it lies 4.3 px inside, so the pip drifts out by 2 px.
- Target: keep about 6 px inside. Margin 5 gives centre (101, 101), 52.3 px
  from the centre, 5.7 px inside the edge (margin 6 gives 7.1). So the sprite
  lands at (79, 79) to (123, 123).
- The spike on branch `spike-icon-round-shadow` (uncommitted, `pip.go`) draws
  a 40 px disc in the 44 px sprite (`pipDiam`), with a 2 px margin that holds
  its shadow. The disc centre is still the sprite centre, so the numbers above
  hold. The `pipMargin` comment ("distance from the disc to the corner") is then
  wrong: it is the distance from the sprite. The spike also leaves the shadow
  with almost no room to move inside its sprite.

### Approaches considered

#### Approach A — Host only: bake the offset into the sprite

Keep `PIP_MARGIN = 4`. Draw the disc 1 px up and left inside the sprite, and
draw each digit 1 px up and left inside its cell.

- Good, because no firmware change, no reflash, and no wire change.
- Good, because it ships with the driver.
- Bad, because it works only with the spike's 40 px disc. The committed 44 px
  disc fills its sprite and has no room to move. Any move is at most 2 px.
- Bad, because the landing place is then in two places: firmware says 4, and
  the sprite hides a 1 px offset. `DrawPip` must apply the same offset or the
  two paths differ. The `99+` cells are 10 px wide; a 16 px `9` moved 1 px may clip.

#### Approach B — Firmware constant `PIP_MARGIN = 5`, host `pipMargin = 5`

Change the constant in both places. Document the landing place in
`docs/wire-protocol.md`. No sprite or message change.

- Good, because one number per side and the golden images show the move directly.
- Good, because it works with the 44 px disc and the 40 px disc alike, and for any later move.
- Bad, because every board needs a reflash (`make flash`). Old firmware with a new driver puts the pip 1 px lower-right of the host fallback. Nothing breaks.
- Bad, because it edits `firmware/display_render.py` and `test/test_frame_cache.py`, which names the box 80..124 and pixel (85, 85).

#### Approach C — Leave the pip at margin 4

Change only the radius.

- Good, because it changes the least code and needs no reflash.
- Good, because the pip box and its tests stay.
- Bad, because the centre sits 4.3 px inside the edge, and the disc covers
  more of the round's edge than the owner sees today.
- Bad, because the next radius change meets the same problem.

### Decision

Chosen: **Approach B — `PIP_MARGIN = 5` in firmware and driver**.

The landing place is a firmware fact. Baking it into the sprite hides it in
two places and fails for the committed disc. The task accepts a reflash.
`iconRoundRadius` becomes 58; `pipMargin` becomes 5.

### Design

- `driver/layers/pip.go`: `iconRoundRadius = 58`; `pipMargin = 5`; fix the
  `pipMargin` comment. Rebase on the spike if it lands first.
- `firmware/display_render.py`: `PIP_MARGIN = 5`. `test/test_frame_cache.py`:
  box 79..123, pixel (84, 84).
- `docs/wire-protocol.md` ("Store pip sheet"): add the landing place, "a
  sprite lands with its corner 5 px from the panel corner".
- `driver/README.md`: radius 58.
- Goldens regenerate: `driver/layers/testdata/icon-round.png`, `pips.png`,
  `pip-sheet.png`, and any golden in `layers_test.go` and `pip_test.go`.
  `layers_test.go` line 223 uses `pipDiscRect()` and follows by itself.

## Part 3 — A one-time resize of `assets/*.png`

### Approaches considered

Three approaches follow. They differ in when and where the icon shrinks.

- **A — Resize the files once (chosen).** Good, because the fix is in the
  files, costs nothing per frame, and reverts with `git revert`. Bad, because
  the six icons that task 0062 already shrank (commit 4cb2cca) are resampled twice.
- **B — Scale in `renderGlyph`.** Good, because the files stay as they are.
  Bad, because every frame render resamples, and the golden then depends on
  the filter.
- **C — Keep the files.** Good, because no binary diff. Bad, because 13 icons
  have pixels with alpha at or above 0x80 up to 58.8 px from the centre
  (`dev.png`), past the new edge.

### Decision

Chosen: **Approach A — a throwaway script, committed as one asset-only commit**.

It is the only approach that costs nothing at run time. The task accepts a
second resample on `ai`, `alert`, `deploy`, `docs`, `gmail`, and `lint`.

### Design

- A throwaway Pillow script (not committed). For each of the 13 files:
  convert to premultiplied `RGBa`, resize to 124x124 with `Image.LANCZOS`
  (scale 0.969, near 58/60), convert back to `RGBA`, and paste into a
  transparent 128x128 at (2, 2). The centre stays at (64, 64); size, mode, and
  transparency stay.
- Check: after the resize, every pixel with alpha at or above 0x80 lies within
  57.5 px of the centre (largest now: `dev.png`, 58.8 px, becomes about 57.0).
  `github.png` and `create.png` (the crosshair circle) have an opaque dark
  disc, 56.4 and 56.1 px now and 54.6 and 54.4 px after the resize; the edge
  must leave at least 3 px of white round (disc radius at most 55).
  `test.png` (the check-circle) is a ring that reaches 50.3 px, so it needs no
  special check. All 13 icons measured now: 50.1 (`build.png`) to 58.8
  (`dev.png`, the test tube); after the resize the largest is `dev.png` at 57.0,
  0.5 px inside the 57.5 limit.
- `TestIconRound` (`driver/layers/pip_test.go`) already requires a white round
  wherever an icon is transparent inside radius-2. That holds for any icon
  size, so it does not test the resize. Add a check to it: no pixel with alpha
  at or above 0x80 lies beyond `iconRoundRadius - 0.5`.
- Rollback: the resize is its own commit. `git revert <sha>` restores the
  files. Smaller icons also fit radius 60, so the asset commit can ship first.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — `{"kind":"setLayerKey","setLayerKey":{"id":"t","iconText":"Hello"}}` makes key 0 show "Hello" in black on a plain white tile, with no round. **Proof:** `go test ./layers -run TestIconText` compares with `driver/layers/testdata/icon-text.png`; the PNG shows the text.
- [x] **DoD-2** — A node with both `iconRef` and `iconText`, 33 runes, 3 lines, or a control character is rejected and changes nothing. **Proof:** `go test ./layers -run TestIconTextInvalid`
- [x] **DoD-3** — `ValidateIconRef` and its tests are unchanged. **Proof:** `git diff main...HEAD -- driver/layers/doc.go` shows no change inside `ValidateIconRef`.
- [x] **DoD-4** — A long name, a two-line text, and a text with `é`, `я`, and `😀` draw: long text ends in `…`, `😀` shows `?`, and every pixel of the tile has alpha 255, with the name black and the parent gray. **Proof:** `go test ./layers -run TestIconTextFit`
- [x] **DoD-5** — Two keys with the same text and different colors use one slot; a different text uses another; a count change stores no frame. **Proof:** `go test ./layers -run TestIconTextFrames`
- [x] **DoD-6** — `iconText` survives a `layers.json` save and load, and an old file without it loads. **Proof:** `go test ./layers -run TestIconTextPersist`
- [x] **DoD-7** — `inbox` sends `iconText` for a channel, and creates no file under `<assets>/.generated`. **Proof:** `cd inbox && npm test` (updated `daemon.test.js` and `render.test.js` assert both; `png.js` is deleted)
- [x] **DoD-8** — `inbox daemon --assets-dir /x` starts and prints one line that the flag is unused; `install.sh --dry-run` does not print `--assets-dir`. **Proof:** `inbox/test/cli.test.js`, and the dry-run output.
- [x] **DoD-9** — `iconRoundRadius` is 58 and `pipMargin` is 5 in `driver/layers/pip.go`, and `PIP_MARGIN` is 5 in `firmware/display_render.py`. **Proof:** `grep -n "iconRoundRadius = \|pipMargin  *= \|PIP_MARGIN = " driver/layers/pip.go firmware/display_render.py`
- [x] **DoD-10** — The host and the firmware put the pip at the same place: the sprite spans x and y 79..123. **Proof:** `go test ./layers -run TestPip` (asserts `pipDiscRect()` is (79,79)-(123,123)) and `python -m pytest test/test_frame_cache.py` (the stamp changes only box 79..123).
- [x] **DoD-11** — All 13 `assets/*.png` are still 128x128 RGBA with a transparent corner, and no pixel with alpha at or above 0x80 lies beyond 57.5 px from the centre. **Proof:** `go test ./layers -run TestIconRound` (with the new check); `file assets/*.png`
- [x] **DoD-12** — The resize is one commit that touches only `assets/*.png`, and `github.png` and `create.png` keep at least 3 px of white round around their dark disc. **Proof:** `git show --stat <sha>`; the check in `TestIconRound` for those two files.
- [x] **DoD-13** — The goldens are regenerated and show the round at 58 and the pip at the new place. **Proof:** `git diff main...HEAD --stat -- driver/layers/testdata` lists `icon-round.png` and `pips.png`; `go test ./...` passes without an update flag.
- [x] **DoD-14** — `driver/README.md`, `inbox/README.md`, and `docs/wire-protocol.md` describe `iconText`, the fit rules, the `?` rule, the radius, and the pip landing place. **Proof:** `grep -n "iconText" driver/README.md inbox/README.md`; `grep -n "5 px" docs/wire-protocol.md`
- [ ] **DoD-15** — On the board, after `make flash`, the channel children of `inbox` show their names in black and gray on white, the count pip does not cover a letter, and the other icons sit inside the round. **Proof:** a photo or a note in this spec's Notes, and `go test ./... && (cd inbox && npm test)` passes.
  - Not confirmed: this needs `make flash` (the pip margin is a firmware change) and a look at the panel, which the implementer could not do. `go test ./...`, `python3 -m pytest test` (403 passed), and `cd inbox && npm test` (87 passed) pass. Run `make flash`, restart `macropadd`, start `inbox daemon`, and check the keys.

## Risks

- The shadow spike (a soft shadow under the round and the pip) was dropped, so the goldens change only for this task. The patch is not kept in the repo; the idea can come back as its own task. The pip sprite can then stay 44 px, as the firmware expects.
- A white text tile hides the status color, so a text key shows no running, done, or error color → accepted by the owner; if it is missed, add a key-colored border as a later task. The sizes and gray are constants the owner tunes on the panel.
- A stale `macropadd` ignores `iconText`, and stale firmware puts the pip 1 px off → the README says to flash the firmware and restart `macropadd` before `inbox daemon`.
- The second resample of six icons softens them → compare them on the panel before the commit; revert that commit if they look worse.

## Decisions on the open questions

Settled when the spec was written; the owner can still change a constant after seeing the panel (DoD-15).

- **Text colors (owner):** no round for text. The tile is plain white, the name is black `#000000`, and the parent is gray `#6B6B6B`, as `png.js` keeps a weaker parent line. The old dark tile goes away with `png.js`.
- **Pip margin:** 5. It keeps the pip 5.7 px inside the round's edge against 6.3 px now. Margin 6 would be 7.1 px, a larger change than the radius change asks for.
- **Check-circle icon:** `test.png`. The two icons with a dark disc that need the 3 px check are `github.png` and `create.png`.
