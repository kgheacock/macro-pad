---
id: "0053"
title: "Window switcher plugin: each key brings a desktop, app, or window forward"
status: "ongoing"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/60"
branch: "claude/project-thread-8hsd26"
related: ["0011", "0012", "0013", "0015", "0017", "0028"]
tags: ["driver", "plugin", "macos", "windows"]
---

# 0053 — Window switcher plugin: each key brings a desktop, app, or window forward

## Problem

The pad has no way to move between screens. Tasks 0011, 0012, 0015, and 0017
each reach one terminal session, but none switches macOS desktops or brings an
arbitrary app or window to the front on a key press.

## Goals

- A plugin binds keys 0–5 to a desktop number, an app, or an app window whose
  title contains given text.
- A press brings the target forward. With no config, key N switches to
  desktop N+1.
- The bound keys light in their own color: bright for the last switch, dim
  for the rest.
- A failed switch is logged and does not stop the plugin.

## Non-goals

- Thumbnails of hidden desktops or windows.
- Tracking switches made outside the pad.
- Binding to a specific terminal session. Tasks 0011 and 0015 cover that.

## Approaches considered

### Approach A — WebSocket plugin that runs `osascript`

A Go plugin under `driver/examples/` reacts to presses and runs AppleScript:
Ctrl+N through System Events for desktops, `activate` for apps, and `AXRaise`
for windows.

- Good, because it uses the plugin API (task 0028) as is, with no daemon
  change.
- Good, because `osascript` needs no cgo and no new Go dependency.
- Bad, because each switch spawns a process, about 100 ms or more.
- Bad, because the Ctrl+N shortcuts must be turned on by hand.

### Approach B — cgo bindings to the Accessibility and CGEvent APIs

- Good, because there is no process per switch.
- Bad, because it adds Objective-C and cgo to an example, and a build that
  only works on macOS.

### Approach C — Private CGS calls to change Spaces directly

- Good, because it needs no keyboard shortcut.
- Bad, because the APIs are private and break between macOS releases.

## Decision

Chosen: **Approach A**. It costs a process per switch and a one-time manual
shortcut setup, in return for no daemon change and no cgo.

## Definition of done

- [ ] `go test ./driver/examples/window-switcher/` passes.
- [ ] With `macropadd --emulate` and `--dry-run`, a press on a bound key
      prints its `osascript` command and lights the key bright.
- [ ] On a Mac with six desktops and Ctrl+1..6 on, key 1 moves to desktop 2.
- [ ] An `app` + `window` binding raises the matching window above the app's
      other windows.
- [ ] A binding to an app that is not installed logs one line and the
      plugin keeps running.

## Risks

- `AXRaise` on a window on a different desktop from the app's front window
  may not move to that desktop.
- Fullscreen apps take their own desktop and shift the Ctrl+N numbers.

## Notes

How to test on a Mac: `driver/examples/window-switcher/README.md`.
