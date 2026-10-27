---
id: "0080"
title: "A printed body for the one-piece 110 × 100 mm panel"
status: "ongoing"
created: "2026-10-24"
updated: "2026-10-24"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/102"
branch: "claude/project-thread-mknkbk"
related: []
tags: ["hardware", "enclosure"]
---

# 0080 — A printed body for the one-piece 110 × 100 mm panel

## Problem

Keith ordered a one-piece 110 × 100 × 3 mm panel (`screenkey_panel_3x2.step`). It replaces
the faceplate and the mounting plate of `build_case.py`. The body in the repo is 99.7 × 91.1 mm,
so it does not fit the panel, and its corner inserts do not line up with the panel's holes.

## Goals

- A printable body STL whose outline matches the panel: 110 × 100 mm with 4 mm corner radii.
- A JLC3DP Type 5 (M3\*4\*5) threaded insert under each of the panel's 4 countersunk holes,
  at (±50, ±45) mm: a 3.6 mm hole 7 mm deep with at least 3 mm of wall (Keith's ask, 2026-10-25).
- The same wall, floor and depth as `body.stl`.
- A 22 mm round hole for the Adafruit 4218 panel-mount USB-C jack, in place of the old slot
  (Keith's ask, 2026-10-25).

## Non-goals

- Changing the panel. It is already ordered.
- A custom PCB, or other changes to the wiring inside the body.

## Approaches considered

### Approach A — Parametrise build_case.py for both layouts

- Good, because there is one script.
- Bad, because the old layout has two plates and a different key pitch, so most constants would
  need a switch.

### Approach B — A second script that reuses build_case.py's helpers

- Good, because the old case stays byte-for-byte the same.
- Good, because the panel numbers are in one short block.
- Bad, because there are two scripts to keep in step.

### Approach C — Edit body.stl by hand in a CAD tool

- Good, because it's quick once.
- Bad, because the result is not reproducible from the repo.

## Decision

Approach B. `build_panel_body.py` imports the shapes and tolerances from `build_case.py` and
writes `panel_body.stl`. The cost is a second entry point. The panel's numbers were measured from
the STEP file, which is committed next to the script.

## Definition of done

- [ ] `python hardware/enclosure/build_panel_body.py` writes a watertight `panel_body.stl`,
      110 × 100 × 78.6 mm.
- [ ] Each insert pocket is centred under a panel hole. Checked by placing the STEP panel on the
      STL.
- [ ] A test print takes the panel with 4× M3 × 8 flat-head screws into the JLC3DP inserts.
- [ ] The six modules fit in the body with the panel fitted.
