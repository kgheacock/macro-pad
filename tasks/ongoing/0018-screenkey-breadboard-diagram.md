---
id: "0018"
title: "Add a breadboard wiring diagram for the ScreenKey Module"
status: "ongoing"
created: "2026-08-14"
updated: "2026-08-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0018-screenkey-breadboard-diagram"
related: ["0009", "0010"]
tags: ["hardware", "docs"]
---

# 0018 — Add a breadboard wiring diagram for the ScreenKey Module

## Problem

`hardware/README.md`'s Pinout table and `firmware/pins.py` give the GPIO
mapping as text. No diagram shows all 6 ScreenKey Module connectors wired
to the Pico Plus 2, the way Waveshare's own reference diagram does for
their ESP32 dev kit.

## Goals

- One static HTML file shows all 6 keys' pin connections at once, not
  just one key.
- Each connection renders as a colored, labeled square — no drawn wire
  lines.
- Squares that land on the same physical pin (the shared SPI, DC, RST,
  GND, and VCC lines) stack horizontally at that pin, one square per key.
- Every label and color matches `firmware/pins.py` and the confirmed
  Pinout table in `hardware/README.md` exactly.

## Non-goals

- Drawing routed wire paths between the connector and the board. Squares
  at each pin replace wires entirely.
- Picking a diagramming library or tool. The file stays a static,
  dependency-free artifact.
- Validating the mapping against real hardware. Task 0010 covers that.

## Approaches considered

### Approach A — Hand-authored single-file HTML/CSS grid

Author a static HTML file with plain CSS (no SVG routing needed): a
legend of the 9 signals, and one square per pin connection, colored and
labeled, stacked horizontally where a pin is shared across keys.

- Good, because it needs no build step — open the file in a browser and
  it renders.
- Good, because a grid of squares, with no wire geometry to route, is
  simple enough to hand-write and check against `firmware/pins.py` by
  eye, pin by pin.
- Bad, because it is hand-authored — nothing catches drift if
  `firmware/pins.py` changes after this file is written.
- Bad, because adding a 7th key means manually adding a row of squares,
  not a config change.

### Approach B — Script-generated grid from `firmware/pins.py`

Write a small script that reads `firmware/pins.py` and emits the HTML,
so the grid regenerates from the source of truth instead of being
typed by hand.

- Good, because the diagram cannot drift from `firmware/pins.py` — a
  regenerate run always matches it.
- Good, because adding a key needs no manual diagram edits, only a
  rerun of the generator.
- Bad, because it adds a generator script and a run step for one
  documentation file, more tooling than a static grid needs.
- Bad, because it adds a second thing to maintain — the generator —
  alongside the pin config it reads.

### Approach C — Use an existing wiring-diagram tool (for example, WireViz)

Define the connector-to-pin mapping in the tool's format and let it
render the page, instead of hand-authored HTML/CSS.

- Good, because a purpose-built tool already draws connector legends
  and pinout tables without custom markup.
- Good, because it removes the CSS grid layout work from this task.
- Bad, because these tools solve wire routing, and this design has no
  wires to route — most of the tool's value goes unused.
- Bad, because it adds an external tool to install and learn for a
  document that plain HTML/CSS can render on its own.

## Decision

Chosen: **Approach A — Hand-authored single-file HTML/CSS grid**.

Removing wire routing from the design removes Approach A's earlier
weakness — a hand-drawn diagram of 6 keys' wires would have been
unreadable, but a grid of labeled squares is not. This choice accepts
that the file needs a manual edit if `firmware/pins.py`'s mapping
changes, the same cost 0009 already accepted for `hardware/README.md`'s
Pinout table.

## Design

Create `hardware/breadboard-diagram.html`, a self-contained HTML/CSS
file — no SVG wires, no external assets.

**Legend** — one row per signal, each showing its fill color and a
two-letter code:

| Signal | Color  | Code |
|---|---|---|
| KEY  | blue   | Ke |
| DC   | yellow | Dc |
| CS   | orange | Cs |
| CLK  | teal   | Cl |
| DIN  | gray   | Di |
| GND  | black  | Gn |
| VCC  | red    | Vc |
| PWM  | purple | Pw |
| RST  | brown  | Rs |

Signal names and colors come from `docs/0.85inch_ScreenKey_Module.pdf`'s
`H2` connector schematic and Waveshare's reference wiring photo.

**Pin grid** — one entry per GPIO pin used, from `firmware/pins.py`:

- Shared pins (`SPI_SCK`, `SPI_MOSI`, `DISPLAY_DC`, `DISPLAY_RST`, plus
  the GND and 3V3 rails) each show 6 squares stacked horizontally, one
  per key, labeled `1Cl 2Cl 3Cl 4Cl 5Cl 6Cl` and so on.
- Per-key pins (`switch_pin`, `display_cs_pin`, `backlight_pin`) each
  show 1 square, labeled `{key number}{code}` — for example, key 1's
  `switch_pin` (GP13) shows `1Ke`.

Each square's fill color matches its signal's legend color; the code
inside states the signal without depending on that color.

Files to change:

- `hardware/breadboard-diagram.html` — new
- `hardware/README.md` — link to the new diagram from the Pinout section

## Definition of done

- [ ] **DoD-1** — `hardware/breadboard-diagram.html` opens in a browser
  and renders with no network request. **Proof:** view-source shows no
  `<script src=`, `<link href=`, or `<img src=` pointing outside the file
- [ ] **DoD-2** — The legend lists all 9 signals with the colors and
  codes in the Design table above. **Proof:** the legend block in
  `hardware/breadboard-diagram.html`
- [ ] **DoD-3** — `SPI_SCK`, `SPI_MOSI`, `DISPLAY_DC`, `DISPLAY_RST`,
  GND, and 3V3 each show 6 stacked squares, one per key. **Proof:**
  count the squares at each of those pins in
  `hardware/breadboard-diagram.html`
- [ ] **DoD-4** — Each key's `switch_pin`, `display_cs_pin`, and
  `backlight_pin` shows exactly 1 square, labeled and colored to match
  that key's row in `firmware/pins.py`'s `KEYS` list. **Proof:** compare
  `hardware/breadboard-diagram.html` against `firmware/pins.py`
- [ ] **DoD-5** — `hardware/README.md` links to the new diagram.
  **Proof:** `hardware/README.md`, Pinout section
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- The diagram assumes the ScreenKey *Module* variant's 9-pin connector,
  transcribed from `docs/0.85inch_ScreenKey_Module.pdf` → if the SKU in
  hand turns out to be the LCD-only variant, this diagram is wrong the
  same way `hardware/README.md`'s Pinout table would be.
- The hand-authored grid does not track `firmware/pins.py` automatically
  → a future pin change needs a matching manual edit here, or the two
  drift apart.

## Notes

An earlier version of this spec used single-letter color codes (for
example, "B" for blue). Blue (KEY), black (GND), and brown (RST) all
start with "B", so a label read without its fill color could not tell
them apart. Two-letter signal codes (Ke, Dc, Cs, Cl, Di, Gn, Vc, Pw, Rs)
replace them — each label is unique on its own, independent of color.
