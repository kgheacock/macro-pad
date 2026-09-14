---
id: "0037"
title: "Render static HTML to a key's 128x128 image"
status: "ongoing"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0037-render-static-html-on-a-key"
related: ["0030", "0034"]
tags: ["driver", "cli", "display"]
---

# 0037 — Render static HTML to a key's 128x128 image

## Problem

A plugin can send a pre-made 128×128 PNG (task 0030) or a single emoji
character (task 0034) to a key. Nothing turns a snippet of static HTML —
text, layout, color — into that image. A plugin author who wants a text
label or a styled status card must hand-produce a PNG first.

## Goals

- One command turns a static HTML file into a 128×128 image and sends it
  to a key, through task 0030's `setCustomGlyph` wire path, unchanged.
- The renderer supports plain text, block and inline layout, and
  background and text color — the subset a 128×128 button plausibly
  needs.
- The render never runs the page's own JavaScript. Input is static
  markup, not a live page.

## Non-goals

- Pixel-exact parity with a real browser, or full CSS support: flexbox,
  grid, animation, or web fonts fetched at render time.
- A live HTML view on the device. Rendering happens host-side, as it
  does for 0030 and 0034. Once sent, the image is as static as any other
  custom glyph.
- Fetching remote images or stylesheets by URL. The render must not
  depend on network access, and must not hang if the HTML references
  one.
- Dynamic data binding or templating. Plain static markup in, one PNG
  out.

## Approaches considered

### Approach A — A Python helper using a static (no-JS) HTML/CSS renderer

`tools/render_html.py` (WeasyPrint, pip-installable, renders HTML+CSS to
a raster image and executes no JavaScript by design) renders one HTML
file to a 128×128 PNG. A new `macrodriver html --key N --file page.html`
subcommand runs it and calls `api.Conn.SetCustomGlyph`, mirroring
`macrodriver emoji`'s shape from task 0034.

- Good, because WeasyPrint's own no-JS design meets this task's "no
  script execution" goal for free, with no sandboxing code to write.
- Good, because this repo already treats Python as its asset-prep tool
  (`tools/gen_glyphs.py`, `tools/render_emoji.py`), so the new script
  fits an established shape.
- Bad, because WeasyPrint depends on system libraries (Cairo, Pango),
  not a pure `pip install` — a heavier install step than Pillow's, which
  task 0034 did not need.
- Bad, because a new Python process starts on every render, adding
  interpreter and layout-engine startup time to what should be a quick
  key update.

### Approach B — A Go-native HTML/CSS rasterizer bundled into the driver

Add a Go package that parses a small, hand-picked subset of HTML and CSS
(text, block/inline boxes, colors) and rasterizes it straight to an
RGB565 buffer, in-process.

- Good, because the driver stays one self-contained Go binary, with no
  external interpreter or renderer dependency for this command.
- Good, because no per-call process start cost exists; render runs
  in-process, next to the `image/png` decode task 0030 already added.
- Bad, because Go has no mature HTML/CSS layout engine to import — this
  task would hand-write parsing and box-model layout from scratch, the
  same gap task 0034's Approach B hit for color-emoji glyph formats.
- Bad, because "static HTML" then quietly means only the hand-rolled
  subset this task implements; any other tag or CSS property an author
  writes fails silently or renders wrong, with no clear error.

### Approach C — Document the pattern, add no renderer

Leave `driver/api` and `macrodriver` as they are. Add an example under
`driver/examples/` showing how to render HTML to a PNG with any tool and
send it through the existing `SetCustomGlyph`.

- Good, because it adds no new dependency or maintenance cost to the
  driver itself, and matches driver/README.md's "any language can write
  a plugin" stance.
- Good, because a plugin author is already free to pick a renderer they
  trust — Puppeteer, wkhtmltoimage, WeasyPrint — and nothing here blocks
  that today.
- Bad, because it does not meet the goal: a person must still write and
  maintain their own render step, in a language of their choosing, every
  time.
- Bad, because unlike emoji (task 0034 left `tools/render_emoji.py` as a
  working example), no starting script exists for HTML today.

## Decision

Chosen: **Approach A — a Python helper using a static HTML/CSS
renderer**.

The "no JavaScript" goal is easiest to guarantee with a renderer that
has no JS engine at all, not by sandboxing one that does. WeasyPrint
meets that for free. The cost accepted: a new Python dependency
(WeasyPrint, plus Cairo and Pango) heavier than Pillow's, and a
per-call process start, the same trade task 0034 already accepted for
`macrodriver emoji`.

## Design

**Update, during implementation:** current WeasyPrint (the version `pip
install weasyprint` gets today) dropped `HTML.write_png()` — since
v53 it renders to PDF only. `tools/render_html.py` renders to a
128×128-sized one-page PDF with WeasyPrint, then rasterizes that page
to a PNG with PyMuPDF (`pip install pymupdf`), a second new dependency
this section did not originally name. PyMuPDF's wheel bundles its own
PDF engine, so it needs no extra system libraries beyond what
WeasyPrint itself needs. Network-blocking moved from `base_url=None`
alone to `weasyprint.urls.URLFetcher(allowed_protocols=[])`, WeasyPrint's
own mechanism for the same guarantee — `base_url=None` alone does not
stop an absolute remote URL from resolving.

Files to change:

- `tools/render_html.py` — new. Takes an HTML file path and an output
  path. Renders with WeasyPrint at a fixed 128×128 size, with
  `base_url=None` so relative or remote resource references do not
  resolve, and any `<script>` tag is inert by WeasyPrint's own design.
- `driver/cmd/macrodriver/html.go` — new. `macrodriver html --key N
  --file page.html [--addr ...] [--emulate]`, structured like
  `emoji.go`: a `lookPath`-guarded, package-var render step; the shared
  `newEmulatedConn` helper; a call to `api.Conn.SetCustomGlyph`.
  `driver/api.Conn.SetCustomGlyph` needs no change.
- `driver/README.md` — document the new subcommand next to `macrodriver
  emoji`, naming the supported HTML/CSS subset and the WeasyPrint
  install step.

## Definition of done

- [x] **DoD-1** — `macrodriver html --key 0 --file testdata/sample.html
  --emulate` ends with the emulator's last custom glyph holding
  non-empty pixels. **Proof:** a driver test stubs `renderHTMLPNG` and
  asserts `Emulator.LastCustomGlyph()`, matching
  `TestRunEmoji_Emulate`'s shape.
  `TestRunHtml_Emulate` in `driver/cmd/macrodriver/html_test.go`; passes.
- [x] **DoD-2** — A `<script>` tag in the input HTML has no effect on
  the rendered image. **Proof:** render one HTML file whose script
  would change the background color if it ran, and one without that
  script; assert the two renders are pixel-identical.
  `TestRunHtml_ScriptInert`, run against the real `tools/render_html.py`
  pipeline (python3 + WeasyPrint + PyMuPDF); passes.
- [x] **DoD-3** — A remote `<img src="http://...">` or `@font-face` URL
  does not make the render hang. **Proof:** render HTML naming an
  unreachable URL and assert the call returns within a fixed timeout in
  a test.
  `TestRunHtml_RemoteURLDoesNotHang`, real pipeline, 10s deadline;
  returns in well under a second because `render_html.py`'s
  `URLFetcher(allowed_protocols=[])` rejects the URL before any socket
  opens.
- [x] **DoD-4** — A missing `python3` or WeasyPrint produces one line
  naming the missing dependency, not a stack trace. **Proof:** a test
  stubs `exec.LookPath` (or the import check) to fail and asserts the
  error text and a non-zero exit code, matching
  `TestRunEmoji_MissingPython3`.
  `TestRunHtml_MissingPython3`; passes.
- [x] **DoD-5** — Tests cover the new subcommand and its script- and
  network-inertness cases. **Proof:** `go test
  ./driver/cmd/macrodriver/... -run Html` passes; the same command on
  `main` reports no matching tests.
  Confirmed both halves directly.
- [x] **DoD-6** — `driver/README.md` documents the new command and the
  supported HTML/CSS subset. **Proof:** `driver/README.md`, the section
  next to `macrodriver emoji`.
  New "`macrodriver html`" section added right after "`macrodriver
  emoji`".
- [ ] **DoD-7** — The PR in the `pr` field links to this spec.
  **Proof:** PR body.

## Risks

- WeasyPrint's CSS support is not full-browser parity (limited or no
  flexbox/grid depending on version, no remote web fonts by design) →
  document the supported subset in `driver/README.md`; treat a mismatch
  against a plugin author's expectation as this task's known limit, not
  a bug.
- WeasyPrint needs Cairo and Pango installed on the host, beyond `pip
  install` → document the install step next to the existing Pillow note
  in `driver/README.md`.
- A large or deeply nested HTML file could make one render slow on the
  host → host-side only, no device-side cost; not mitigated here, same
  as task 0034's "renders again from scratch, every call" acceptance.

## Notes

Precedent: task 0034 chose the same shape — a Python helper script,
called from a new `macrodriver` subcommand, reusing
`api.Conn.SetCustomGlyph` unchanged — for turning a Unicode emoji
character into a custom glyph. This task follows it for HTML input.
