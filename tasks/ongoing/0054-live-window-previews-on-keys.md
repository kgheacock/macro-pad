---
id: "0054"
title: "Keys show a live thumbnail of their window, and the last snapshot when it is hidden"
status: "ongoing"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/62"
branch: "claude/project-thread-p5qq77"
related: ["0030", "0042", "0053"]
tags: ["driver", "plugin", "macos", "screencapturekit"]
---

# 0054 — Keys show a live thumbnail of their window, and the last snapshot when it is hidden

## Problem

A key that switches to a window shows a color, not the window. Each key has a 128×128 screen and `setCustomGlyph` already sends any PNG to it, but nothing captures windows. A window that is minimized or on another desktop cannot be captured while hidden, so a live capture alone leaves those keys blank.

## Goals

- Each mapped key shows its window, scaled to 128×128, refreshed every few seconds.
- A hidden window's key keeps the last snapshot, marked as stale.
- The snapshot is taken as late as possible: when the window loses focus.
- The key-to-window mapping is input, so the window switcher (task 0053) can own it.

## Non-goals

- Live video. A key push is 32 KB and waits behind other sends.
- Thumbnails of whole desktops. A desktop binding in the switcher gets no picture yet.
- Platforms other than macOS.

## Approaches considered

### Approach A — Plugin process, cgo calling ScreenCaptureKit

A Go package captures through a small Objective-C file and sends with `api.Conn.SetCustomGlyph`.

- Good, because it is one binary, and the driver already needs cgo.
- Good, because the scheduling and snapshot logic is plain Go and tests run anywhere with a fake capturer.
- Bad, because the Objective-C half only builds on macOS, so CI on Linux cannot compile it.

### Approach B — Plugin process, Swift helper binary

A Swift tool writes PNGs; a Go plugin reads them.

- Good, because Swift async APIs suit ScreenCaptureKit.
- Bad, because it adds a second toolchain and a second binary to install and grant Screen Recording to.

### Approach C — Inside `macropadd`

- Good, because no extra process.
- Bad, because the daemon would need the Screen Recording permission, and a capture stall would sit next to the board link.

## Decision

Chosen: **Approach A**. One binary, one permission grant, testable logic. The cost is that `capture_darwin.m` is only checked by building on a Mac.

## Design

- `driver/preview`: `Capturer` (window list, focused window, capture), `Target`/`Selector`, `Previewer`.
- Window list and focus: `CGWindowListCopyWindowInfo`, layer 0, at least 40×40. `kCGWindowIsOnscreen` is false for minimized windows, other desktops, and hidden apps.
- Capture: `SCScreenshotManager` with a desktop-independent window filter, at 256×256 fit; Go averages down to 128×128.
- Each `Interval` (5 s): on-screen windows are captured; others show `Stale(snapshot)`. A key is sent only when ≥0.5% of its RGB565 pixels change. An all-black capture counts as a failure.
- Each `FocusPoll` (500 ms): when the frontmost window changes, the window that lost it is captured at once.
- `driver/cmd/windowpreview`: flags `--map`, `--list`, `--interval`, `--focus-poll`, `--addr`.

## Definition of done

- [ ] **DoD-1** — An on-screen window is sent to its key as a PNG `transport.DecodePNGToRGB565` accepts. **Proof:** `cd driver && go test ./preview -run TestLivePreviewPushed`
- [ ] **DoD-2** — An unchanged window costs no push. **Proof:** `go test ./preview -run TestUnchangedNotResent`
- [ ] **DoD-3** — A window that goes off screen shows its last snapshot, dimmed, sent once, and is not captured. **Proof:** `go test ./preview -run 'TestHiddenWindowShowsStaleSnapshot|TestNeverSeenWindowSendsNothing|TestCaptureErrorFallsBack|TestBlankCaptureFallsBack'`
- [ ] **DoD-4** — A focus change captures the window that lost focus. **Proof:** `go test ./preview -run TestSnapshotOnLeave`
- [ ] **DoD-5** — `cd driver && go build ./... && go vet ./...` exits 0 on macOS with cgo. **Proof:** the command's exit code on a Mac
- [ ] **DoD-6** — On a Mac with Screen Recording granted, `windowpreview --map 0=<an open app>` shows that window on key 0 within one interval, and a covered window still updates. **Proof:** a board or virtual-pad run noted under Notes
- [ ] **DoD-7** — Minimizing the window leaves the dimmed snapshot on its key. **Proof:** same run
- [ ] **DoD-8** — `driver/README.md` documents the command, the permission, and the cost. **Proof:** section "Window previews"

## Risks

- ScreenCaptureKit from a process with no `NSApplication` → `pv_available` calls `CGMainDisplayID` to open a window server connection. Unverified until DoD-6.
- Six changing windows keep the board link busy → change threshold, and `--interval` is a flag.
- macOS may re-ask for the Screen Recording permission → documented.

## Open questions

- [ ] Should the switcher (0053) run the previewer in its process and call `SnapshotWindow` before each switch? Its `Binding{App, Window}` maps 1:1 to `Selector{App, Title}`. — kgheacock

## Notes
