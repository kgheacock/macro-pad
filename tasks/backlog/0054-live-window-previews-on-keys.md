---
id: "0054"
title: "Keys show a live thumbnail of their window, and the last snapshot when it is hidden"
status: "backlog"
created: "2026-10-13"
updated: "2026-10-14"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/62"
branch: "claude/project-thread-p5qq77"
related: ["0030", "0042", "0055", "0056"]
tags: ["driver", "plugin", "macos", "screencapturekit"]
---

# 0054 — Keys show a live thumbnail of their window, and the last snapshot when it is hidden

## Problem

A key that stands for a window shows a color, not the window. Each key has a 128×128 screen, and `setCustomGlyph` already sends any PNG to it, but nothing captures windows. A window that is minimized or on another desktop cannot be captured while hidden, so a live capture alone leaves those keys blank.

## Dependency

This task builds on task 0055, the plugin host. It starts only after 0055's implementation is on `main`. The preview is a host plugin: it uses the host's key ownership, pages, bus, `Every` timers, `Require` permission check, `Key.SetImage`, and the `keyimg` image helpers. It adds a window list and window capture to the host's `macos` package (see Design). It does not dial the daemon, parse flags, or encode PNGs itself.

It also takes the `window.willHide` event (`host.TopicWindowWillHide`) from task 0056, the window switcher, which publishes it just before each switch. Its payload is a `macos.Window`. This task needs that `Window` to carry the window's id, which the host in PR #65 does not have yet (see Design, "Host `macos` additions").

## Goals

- A `windowpreview` host plugin binds each key it owns to one window: an app, or an app window whose title contains given text.
- Each bound key shows its window, scaled to 128×128, refreshed every few seconds, even when other windows cover it.
- A window that is minimized, on another desktop, or in a hidden app keeps its last snapshot, marked as stale.
- That snapshot is as fresh as possible: the plugin captures a window when the switcher says it is about to hide it, and when it loses focus by other means.
- While its page is hidden, the plugin runs no timed refresh and no focus poll. It still answers `window.willHide`, because the switcher usually runs on another page, so that event almost always arrives while the previews are hidden.

## Non-goals

- Live video. A key push is 32 KB and waits behind other sends.
- Thumbnails of whole desktops. A desktop target in the switcher gets no picture.
- Platforms other than macOS.
- Changes to the daemon or the plugin protocol. A gap in the host's API is logged under Open questions for task 0055, not patched in the host's core here.

## Approaches considered

### Approach A — A host plugin, with window capture added to the host's `macos` helpers

A package `driver/plugins/windowpreview` implements `host.Plugin`. This task adds a `Screen` interface with `Windows` and `CaptureWindow` to `driver/host/macos`, as task 0055's Design plans for window capture. The plugin scales and marks images with `keyimg.Thumbnail` and `keyimg.Stale`, and sends them with `h.Key(k).SetImage`.

- Good, because the host binary holds the one Screen Recording grant, so the preview needs no grant of its own.
- Good, because the in-process bus delivers the switcher's `window.willHide` before the switch, with no race.
- Good, because the host's pages and cache already handle repaint after a page switch or a daemon restart.
- Good, because the plugin logic is plain Go and tests on `hosttest` with a fake `macos`.
- Bad, because it cannot ship until task 0055 lands.
- Bad, because the Objective-C capture code only builds on macOS with cgo and the macOS 14 SDK, so a Linux build cannot check it.

### Approach B — A standalone plugin binary now, ported to the host later

Ship a `windowpreview` binary over the WebSocket API, as the first draft of this PR did, and port it when 0055 lands.

- Good, because it can run on the pad sooner.
- Bad, because it duplicates helpers 0055 moves into the host: the thumbnail scaler, the stale look, PNG encoding, the dial.
- Bad, because it needs its own Screen Recording grant, and it cannot hear the switcher's `window.willHide`.

### Approach C — Draw previews inside the switcher plugin

Task 0056's switcher captures and draws its own keys.

- Good, because the key that switches to a window also shows it.
- Bad, because it mixes two jobs in one plugin, and the switcher's desktop targets have nothing to show.
- Bad, because it changes task 0056's scope.

## Decision

Chosen: **Approach A — a host plugin, with window capture added to the host's `macos` helpers**.

Task 0055 was written with this plugin in mind and already names its helpers. Building on the host gives one permission grant and the switcher's pre-switch event. The task accepts the wait for 0055.

## Design

**Package.** `driver/plugins/windowpreview`, registered in `driver/cmd/macropad-plugins/plugins.go` as `windowpreview`.

**Host config entry.**

```json
{"id": "thumbs", "use": "windowpreview", "page": "previews", "keys": [0, 1, 2],
 "config": {
   "interval": "5s",
   "focusPoll": "500ms",
   "targets": [
     {"key": 0, "app": "Safari"},
     {"key": 1, "app": "iTerm2", "window": "build"},
     {"key": 2, "app": "Code"}
   ]
 }}
```

- `targets[].key` is a physical key index and must be in `h.Keys()`, per task 0055's open question on key indexes. If 0055 picks slots instead, `key` becomes a slot.
- `app` is required and matches the window's app name, ignoring case. `window`, when set, must appear in the title, ignoring case. These match task 0056's `app` and `window` fields, so one target list can be copied between the two.
- `interval` (default `5s`) and `focusPoll` (default `500ms`) are Go durations.
- `Start` decodes the config with `h.Config` and returns an error for: a key not owned, a key used twice, a target with no `app`, `window` without `app`, or a bad duration. The host disables only this instance.
- `Start` calls `h.Require(macos.ScreenRecording)`. The plugin needs no Accessibility grant: it never calls `macos.Desktop.Frontmost`, which runs `osascript` against System Events (see "Snapshot on leave").
- The plugin holds a `macos.Screen` field, set to `macos.Capture` in its factory. A test sets a fake.

**Host `macos` additions.** These go next to `Desktop`, not inside it, so the switcher's fakes do not grow:

```go
// WindowID is a CGWindowID: kCGWindowNumber in the window list,
// SCWindow.windowID in ScreenCaptureKit.
type WindowID uint32

type Window struct {
	ID       WindowID // new; 0 when unknown
	App      string
	Title    string
	OnScreen bool     // new; set by Screen.Windows only
}

// Screen lists and captures windows. Needs Screen Recording.
type Screen interface {
	// Windows lists normal windows, on screen or not, front to back.
	Windows() ([]Window, error)
	// CaptureWindow returns id's contents, scaled to fit maxW x maxH.
	CaptureWindow(id WindowID, maxW, maxH int) (image.Image, error)
}

// Capture is the real Screen. Off macOS or without cgo, its calls
// return ErrUnsupported.
var Capture Screen
```

- `Window.ID` is the field PR #65's review asks the host to add (its finding 6). If #65 adds it first, this task uses it as merged. `Desktop.Frontmost` should fill it too, so the switcher's `window.willHide` payload carries it.
- `Windows` reads `CGWindowListCopyWindowInfo` with `kCGWindowListOptionAll | kCGWindowListExcludeDesktopElements`: layer 0, alpha above 0, at least 40×40. `OnScreen` is `kCGWindowIsOnscreen`, which is false for a minimized window, a window on another desktop, and every window of a hidden app. Titles read as empty without Screen Recording; `h.Require` stops the plugin before that matters.
- `CaptureWindow` uses ScreenCaptureKit's `SCScreenshotManager` with a desktop-independent window filter, scaled to fit, no cursor, no shadow. It captures a window that other windows cover. It calls `CGMainDisplayID` once first, so a process with no `NSApplication` has a window server connection.
- **Build.** The capture code goes in `screen_cgo_darwin.go` with `//go:build darwin && cgo`, and the stub in `screen_other.go` with `//go:build !darwin || !cgo`, the same pair as `macos_cgo_darwin.go` and `macos_other.go`. The Objective-C sits in the cgo preamble, compiled with `-x objective-c -fobjc-arc`, not in a separate `.m` file, so a `CGO_ENABLED=0` build on macOS sees no Objective-C source. It links `-framework CoreGraphics -framework Foundation -weak_framework ScreenCaptureKit`. `SCScreenshotManager` needs the macOS 14 SDK (Xcode 15 or later) to build. The call is wrapped in `@available(macOS 14.0, *)`, so on an older macOS `CaptureWindow` returns an error and the host still starts.

**Resolving a target.** Each refresh reads `macos.Windows()` once. A key keeps the window it found last while that window still matches, so it does not jump between two windows of one app. Otherwise the frontmost on-screen match wins, then the frontmost match. No match logs one line until a match appears.

**Refresh.** `h.Every(interval)`, skipped while `h.Visible()` is false:

1. An on-screen window is captured at 256×256 fit and passed through `keyimg.Thumbnail`. The result is the key's snapshot, and the key shows it.
2. An off-screen window, or a failed or all-black capture, shows `keyimg.Stale(snapshot)`: half brightness with an amber strip along the bottom. An all-black capture counts as failed, because ScreenCaptureKit can return one for a window in the middle of hiding.
3. A window never seen on screen since `Start` leaves its key alone.
4. The plugin calls `SetImage` only when at least 0.5% of the key's pixels change in RGB565, so a blinking cursor costs no push. It compares against the last image it set on that key.

**Snapshot on leave.** Two paths refresh a window's snapshot just before it hides:

- `h.Subscribe(host.TopicWindowWillHide, …)` from task 0056. The payload is a `macos.Window`. The plugin matches it by `ID`, or by `App` and `Title` when `ID` is 0. If a bound key shows that window, the plugin captures it at once, whether or not its page is visible. The switcher publishes before it switches, so the window is still on screen. A capture while hidden updates the snapshot and the host's cache, and the host sends it when the page shows.
- `h.Every(focusPoll)`, skipped while hidden: the plugin reads `Screen.Windows()` and takes the first on-screen entry as the front window, since the list is front to back. When its id changes, the plugin captures the window that lost focus, if a key shows it. This covers switches made by trackpad, keyboard, or Mission Control. It needs no Accessibility, and it stops while the page is hidden.

**Pages.** On `OnVisible(true)` the plugin refreshes at once. While hidden it runs no refresh and no focus poll; only `window.willHide` captures. The host's cache keeps the last images and repaints them when the page shows again.

**Cost.** Each changed key is one 32 KB glyph. A page switch onto the previews page repaints every key from the host's cache, which task 0055's Risks puts at over 1 s with glyphs. Six windows that all change every tick keep the board link busy, so `interval` is config. The daemon also rewrites `glyph-N.bin` on each push.

Files to change:

- `driver/host/macos/macos.go` — `WindowID`, `Window.ID`, `Window.OnScreen`, the `Screen` interface
- `driver/host/macos/screen_cgo_darwin.go`, `screen_other.go` — `Capture`, with the build tags above
- `driver/host/topics.go` — note that `windowpreview` subscribes to `window.willHide`
- `driver/plugins/windowpreview/plugin.go` — config, `Start`, resolve, refresh, snapshot on leave
- `driver/plugins/windowpreview/plugin_test.go` — tests on `hosttest` with a fake `macos`
- `driver/cmd/macropad-plugins/plugins.go` — one registry line
- `driver/README.md` — the plugin's config, the Screen Recording step, and the cost, in the "Plugin host" section

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — After a refresh, an on-screen window's key has a `hosttest` `KeyState(k).PNG` that decodes to 128×128 with `transport.DecodePNGToRGB565`. **Proof:** `cd driver && go test ./plugins/windowpreview -run TestLivePreview`
- [ ] **DoD-2** — Each invalid config listed in Design makes `Start` return an error that names the field. **Proof:** `go test ./plugins/windowpreview -run TestStartRejects`
- [ ] **DoD-3** — A window whose picture changes by less than 0.5% causes no `SetImage` call. **Proof:** `go test ./plugins/windowpreview -run TestUnchangedNotResent`
- [ ] **DoD-4** — A window that goes off screen shows its last snapshot through `keyimg.Stale`, set once, and is not captured. A failed or all-black capture does the same. A window never on screen leaves its key alone. **Proof:** `go test ./plugins/windowpreview -run 'TestHiddenShowsStale|TestCaptureFailFallsBack|TestNeverSeenLeavesKey'`
- [ ] **DoD-5** — A `window.willHide` delivery for a bound window captures it once, before the next refresh. It matches by `ID`, or by app and title when `ID` is 0. It captures while the page is hidden too, and the new snapshot is in `KeyState(k).PNG`. **Proof:** `go test ./plugins/windowpreview -run TestWillHideSnapshots`
- [ ] **DoD-6** — A change of the first on-screen window in `Screen.Windows()` captures the window that lost focus. The plugin never calls `Desktop.Frontmost` and never requires Accessibility. **Proof:** `go test ./plugins/windowpreview -run TestFocusLeaveSnapshots`
- [ ] **DoD-7** — While the instance's page is hidden, `Tick` runs no refresh and no focus-poll capture; only `window.willHide` captures (DoD-5). `OnVisible(true)` refreshes at once. **Proof:** `go test ./plugins/windowpreview -run TestHiddenPageIdle`
- [ ] **DoD-8** — A key keeps its window while it matches, even when another window of the same app comes to the front. **Proof:** `go test ./plugins/windowpreview -run TestKeepsResolvedWindow`
- [ ] **DoD-9** — The package has no thumbnail scaler, stale look, PNG encoder, or dial of its own. **Proof:** `grep -rn "png.Encode\|func Thumbnail\|func Stale\|websocket" driver/plugins/windowpreview` returns nothing
- [ ] **DoD-10** — On a Mac with Screen Recording granted to `macropad-plugins`, an `app` target shows that window on its key within one interval, and the picture still updates while another window covers it. **Proof:** manual run on the board or the virtual pad, logged in the PR description
- [ ] **DoD-11** — On a Mac, minimizing the window leaves the dimmed snapshot on its key, and restoring it brings the live picture back. **Proof:** same run
- [ ] **DoD-12** — With task 0056's switcher on another page, a switch away from a bound window leaves a snapshot taken just before the switch. **Proof:** manual run, logged in the PR description. Skip if 0056 is not on `main`, and note it.
- [ ] **DoD-13** — `driver/README.md` documents the plugin's config, the Screen Recording step, and the cost. **Proof:** `driver/README.md`
- [ ] **DoD-14** — `cd driver && go build ./... && go vet ./...` exits 0 on Linux, on macOS with cgo, and on macOS with `CGO_ENABLED=0`. **Proof:** the command's exit code on each
- [ ] **DoD-15** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- Task 0055's final API differs from its spec → this spec's Design follows the merged code, and the differences are noted in the implementing PR.
- PR #65 merges without `Window.ID` → this task adds it, and `Desktop.Frontmost` fills it, so `window.willHide` matches by id. Until then the plugin matches by app and title, which can pick the wrong one of two windows with similar titles.
- ScreenCaptureKit in a process with no `NSApplication` → `CaptureWindow` opens a window server connection first. Unverified until DoD-10.
- ScreenCaptureKit may return a blank or stale frame for a window in the middle of hiding → an all-black capture keeps the old snapshot. A blank that is not all black would replace it; DoD-11 records what happens.
- Six changing windows keep the board link busy and delay other keys → the change threshold, and `interval` is config.
- macOS may ask again for the Screen Recording permission, and a rebuilt host may lose it → task 0055's README covers signing the host with a stable identity.

## Open questions

- [ ] Should a switcher key show its own window's preview, instead of previews living on a separate page? Task 0055 lets one instance own a key, so this needs either the switcher to call the preview helpers, or a host feature for two plugins on one key. The default is a separate `previews` page. — kgheacock
- [ ] Should a target also accept a window id, for two windows of one app with similar titles? The default is no; `app` and `window` only, matching task 0056. — kgheacock

## Notes

- A standalone first draft is in commit `515d5f4` on the branch `claude/project-thread-p5qq77`. Its Go logic (resolve, change threshold, stale fallback, focus-leave snapshot) passed tests on Linux with a fake capturer. Its Objective-C capture file has not been compiled. Task 0055 moves its thumbnail and stale helpers into `keyimg`, and its capture code moves into the host's `macos` package, so its plugin logic carries over.
