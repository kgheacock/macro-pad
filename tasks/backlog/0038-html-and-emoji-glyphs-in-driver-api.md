---
id: "0038"
title: "Send HTML and emoji glyphs through driver/api, not just the CLI"
status: "backlog"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0030", "0034", "0037"]
tags: ["driver", "api", "cli"]
---

# 0038 — Send HTML and emoji glyphs through driver/api, not just the CLI

## Problem

Tasks 0034 and 0037 render an emoji character or an HTML file to a PNG
and send it to a key. This only happens inside `macrodriver`'s CLI
code. A Go-based plugin cannot call this directly — it can only shell
out to `macrodriver` as a subprocess, or duplicate the render logic
itself.

## Goals

- A Go plugin that imports `driver/api` can render and send an emoji or
  an HTML file in one call, with no dependency on `macrodriver`.
- Rendering no longer depends on the caller's current working
  directory. Today's `--script` flag defaults to a `tools/` path that
  assumes the caller runs from the repo root.
- `macrodriver emoji` and `macrodriver html` keep working, as thin
  wrappers over the new `driver/api` methods, not a second copy of the
  render logic.

## Non-goals

- Replacing the Python/WeasyPrint/Pillow render step with a pure-Go
  renderer. Tasks 0034 and 0037 already ruled this out for lack of a
  mature Go HTML/CSS or color-emoji-font library.
- A render cache or a persistent render process. Each call still
  renders from scratch, matching 0034's and 0037's accepted cost.
- A new glyph source beyond emoji and HTML — for example SVG or an
  animated image.
- Changing `docs/wire-protocol.md`'s `setCustomGlyph` message. It
  already carries arbitrary PNG bytes.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Embed the two scripts in `driver/api`, render per call

Move `tools/render_emoji.py` and `tools/render_html.py` under
`driver/api` (`go:embed` cannot reach outside its own package
directory). Add `Conn.SetEmojiGlyph(key int, char string) error` and
`Conn.SetHTMLGlyph(key int, htmlPath string) error`. Each writes its
embedded script to a temp file, shells out to `python3` once, and
calls `SetCustomGlyph` with the result.

- Good, because the caller's working directory no longer matters — the
  script travels inside the binary, not on a relative path.
- Good, because it keeps the exact render mechanism 0034 and 0037
  already proved works, with the smallest possible change around it.
- Bad, because every `driver/api` consumer's binary now carries both
  scripts' text, whether or not it ever calls these methods.
- Bad, because each call still pays a fresh `python3` process start and
  a WeasyPrint or Pillow import, same as today.

### Approach B — A persistent local render process, reached over a socket

Add a small long-lived render helper process, started on first use,
that keeps `python3` and WeasyPrint warm. `SetEmojiGlyph` and
`SetHTMLGlyph` become client calls over a Unix domain socket to a tiny
render protocol. The process renders the input and returns PNG bytes.

- Good, because it removes the interpreter and WeasyPrint import cost
  from every call after the first, the main cost Approach A keeps.
- Good, because a plugin that renders many glyphs in one run — a
  dashboard cycling several keys — pays the startup cost once.
- Bad, because it adds a second local IPC surface next to the existing
  plugin WebSocket protocol, with its own start, health-check, and
  restart logic.
- Bad, because a crashed or stuck render process now needs recovery
  logic that a stateless per-call subprocess never needed.

### Approach C — Pre-render emoji at build time; keep HTML live

Run `tools/render_emoji.py` once, offline, over a fixed emoji set, and
`go:embed` the resulting PNGs. `SetEmojiGlyph` becomes a Go map lookup
with no Python dependency for any emoji in that set. `SetHTMLGlyph`
keeps Approach A's live render — HTML's input space cannot be
pre-rendered.

- Good, because a call for a pre-rendered emoji has no process-start
  cost and no Python dependency at all.
- Good, because it removes the most common case's runtime dependency
  on Cairo, Pango, and PyMuPDF entirely.
- Bad, because it solves only the emoji half of this task — `SetHTMLGlyph`
  still needs Approach A's mechanism, so the codebase carries two
  different render paths.
- Bad, because the pre-rendered set is a build-time decision — an
  emoji outside it needs a fallback path or fails, and the binary grows
  with every emoji added.

## Decision

Chosen: **Approach A — embed the two scripts in `driver/api`, render
per call**.

The goal is a Go plugin calling one method, no `macrodriver` and no
repo-root assumption — Approach A gets there with the fewest moving
parts. The cost accepted: every call still pays a fresh `python3`
start, the same cost 0034 and 0037 already accepted.

## Design

Files to change:

- `driver/api/render_emoji.go`, `driver/api/render_html.go` — new.
  `go:embed` the two scripts (moved from `tools/`), write each to a
  temp file per call, shell out to `python3`, matching
  `renderEmojiPNG`/`renderHTMLPNG`'s existing shape as package-var
  render steps a test can stub.
- `driver/api/state.go` — add `SetEmojiGlyph` and `SetHTMLGlyph`,
  each calling its render step then the existing `SetCustomGlyph`.
- `driver/cmd/macrodriver/emoji.go`, `html.go` — drop the private
  render steps and the `--script` flag; call the new `api.Conn`
  methods instead.
- `driver/README.md` — document `SetEmojiGlyph`/`SetHTMLGlyph` in the
  Plugin API section; describe `macrodriver emoji`/`html` as thin
  wrappers over them.

## Definition of done

- [ ] **DoD-1** — `api.Conn.SetEmojiGlyph` and `api.Conn.SetHTMLGlyph`
  send a `setCustomGlyph` message with non-empty PNG bytes. **Proof:**
  `TestSetEmojiGlyph` and `TestSetHTMLGlyph` in
  `driver/api/state_test.go`, stubbing the render step, pass.
- [ ] **DoD-2** — Rendering does not depend on the caller's working
  directory. **Proof:** a test `os.Chdir`s to `t.TempDir()` before
  calling the real, unstubbed render step for each of emoji and HTML.
  When python3, WeasyPrint, or Pillow are not importable, the test is
  skipped.
- [ ] **DoD-3** — `macrodriver emoji` and `macrodriver html` still send
  a glyph end to end, now through the new `api.Conn` methods. **Proof:**
  `TestRunEmoji_Emulate` and `TestRunHtml_Emulate`, updated to stub
  `api.Conn`'s render step, pass.
- [ ] **DoD-4** — The two scripts embed from inside `driver/api`, not
  `tools/`. **Proof:** `go build ./...` exits 0. `tools/render_emoji.py`
  and `tools/render_html.py` no longer exist.
- [ ] **DoD-5** — `driver/README.md` documents the new methods and the
  CLI's changed shape. **Proof:** `driver/README.md`'s Plugin API
  section.
- [ ] **DoD-6** — The PR in the `pr` field links to this spec. **Proof:**
  PR body.

## Risks

- `go:embed` cannot use `..` to reach outside its package directory →
  the scripts move under `driver/api`, not merely get referenced from
  there; every doc pointing at `tools/render_*.py` needs updating.
- Dropping `--script` removes today's override hook → tests stub the
  render step's package var instead, the same pattern 0034 and 0037
  already use.
- WeasyPrint and Pillow still need Cairo, Pango, and PyMuPDF on the
  host → already documented in `driver/README.md`. This task does not
  change that.

## Notes

Follows a conversation reviewing task 0037: the driver stays a thin
wire-protocol client for callers who never render glyphs, since
`os/exec`'s cost is lazy — a `driver/api` consumer that never calls
`SetEmojiGlyph` or `SetHTMLGlyph` pays nothing for either script.
