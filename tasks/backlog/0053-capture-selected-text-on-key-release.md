---
id: "0053"
title: "Capture the frontmost app's selected text when a key is released"
status: "backlog"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0013", "0028", "0055"]
tags: ["driver", "host", "macos", "accessibility"]
---

# 0053 — Capture the frontmost app's selected text when a key is released

## Prerequisite

Task 0055 (the plugin host and helper API) must be implemented on `main` before work on this task starts. This spec adds a service to that host.

## Problem

A person who selects text on the Mac has no way to hand it to the pad. A key press only reaches plugins as a raw `event` or a resolved `signal`. Nothing reads what is selected in the frontmost app, so no plugin can act on it.

Selection is not one plugin's job. A switcher, a Claude prompt sender, or a Python plugin may each want the text. If one plugin owns the capture, every other plugin depends on it, and a second plugin that captures on its own fights over the clipboard.

## Goals

- Selection capture is a platform feature of the plugin host, not a plugin.
- Any Go plugin can read the current selection on demand, with one call.
- A release of a key listed in the host config captures the selection and delivers it to every plugin, Go or not.
- The capture works in native apps and in apps with weak accessibility support, such as Chrome, Electron apps, and VS Code.
- The clipboard holds the same contents after a capture as before it.
- Only one capture runs at a time, host-wide.

## Non-goals

- Acting on the text. A later task decides where it goes, for example a Claude Code prompt or a file.
- Images, files, or rich text. Plain text only.
- Linux and Windows. The capture is macOS only; other OSes return an "unsupported" error.
- Changes to the click resolver. A capture key still emits its `singlePress`, `doublePress`, or `longPress` signal as before.
- Capture inside `macropadd`. The daemon stays free of cgo and macOS frameworks, per task 0055's Approach A.

## Approaches considered

### Approach A — Accessibility API only

Read `kAXSelectedTextAttribute` from the system-wide focused element with `AXUIElementCopyAttributeValue`.

- Good, because it reads the selection without touching the clipboard or the app's undo history.
- Good, because it is one synchronous call with no timing guesses.
- Bad, because Chrome, Electron apps, and VS Code often return no value unless `AXEnhancedUserInterface` or `AXManualAccessibility` is set on the app.
- Bad, because it needs Accessibility permission for the plugin host binary.

### Approach B — Simulated Cmd+C only

Save the clipboard, post Cmd+C with `CGEventPost`, wait for `NSPasteboard.changeCount` to move, read the text, then restore the clipboard.

- Good, because almost every app supports copy, so coverage is wide.
- Bad, because it overwrites the clipboard for a moment. A clipboard manager records the captured text as a new entry.
- Bad, because an app that copies slowly, or not at all, needs a timeout. With no selection, nothing changes and the capture waits the full timeout.
- Bad, because it also needs Accessibility permission, to post key events.

### Approach C — Accessibility API first, Cmd+C as a fallback

Try Approach A. If it returns an error or an empty string, run Approach B.

- Good, because native apps take the clean path and other apps still work.
- Good, because both paths need the same single permission grant.
- Bad, because there are two code paths to test, and the fallback keeps Approach B's timeout and clipboard-manager costs.

## Decision

Chosen: **Approach C — Accessibility API first, Cmd+C as a fallback**.

Neither path alone covers the apps a person uses every day. The task accepts the extra code path and the fallback's clipboard side effects.

## Design

### Where it lives

A new package, `driver/host/selection`, holds the capture. The host creates one `selection.Service` at start and shares it with every instance. No plugin package is involved.

The pad is not a USB keyboard, so a key press does not move focus. The selection is still in place when the release arrives.

### Capture

`Service.Capture(ctx) (Selection, error)`, where `Selection{Text, Source string}`:

1. Calls `macos.SelectedText()`. A non-empty result is the capture, with `Source: "ax"`.
2. Otherwise calls `Pasteboard.Save()`, then `macos.Copy()`, then polls `Pasteboard.ChangeCount()` every 10 ms for up to 250 ms. A change means the new string is the capture, with `Source: "clipboard"`. It then calls `Pasteboard.Restore(saved)`, on every path.
3. Returns an empty `Selection` and no error when nothing is selected.

A mutex makes captures run one at a time. A second caller waits for the first to finish.

Without the Accessibility grant, `Capture` returns `macos.ErrNoPermission`. The host logs the missing grant once at start when the config lists capture keys (task 0055 already logs grants).

### Host API

Two additions to task 0055's `Host` interface:

```go
// SelectedText captures the frontmost app's selection now.
// It runs on the caller's queue and blocks for at most ~250 ms.
SelectedText(ctx context.Context) (selection.Selection, error)

// OnSelection runs fn for every capture the host makes on a
// configured key release, on this instance's own queue.
OnSelection(fn func(key int, s selection.Selection))
```

`hosttest` gains `SetSelection(s)` so plugin tests choose what `SelectedText` returns, and `Capture(key, s)` to fire `OnSelection` handlers.

### Capture on release

The host config gains a top-level `selection` block:

```json
{
  "addr": "127.0.0.1:8765",
  "selection": {"captureKeys": [5]},
  "plugins": [ ... ]
}
```

`captureKeys` are physical keys, on every page. A key here may also be owned by a plugin instance; that instance still gets its own press, release, and click events as before. Validation (task 0055) adds: every capture key is in 0..5.

On a release of a capture key, the router hands the capture to the service on its own goroutine, not on any instance queue. When the capture returns non-empty text, the host:

1. calls every instance's `OnSelection` handlers;
2. publishes `selection.Captured{Key, Text, Source}` on `host.TopicSelectionCaptured` (`"selection.captured"`). Task 0055 names the topic only; this task defines the payload type in `driver/host/selection`;
3. sends one `signal` named `selectionCaptured` for that key to the daemon, carrying `text` and `source`, so non-Go WebSocket plugins see it.

A release that arrives while a capture-on-release is running is dropped and logged.

### Protocol

`plugin.SignalPayload` gains two optional fields, `Text string` (`json:"text,omitempty"`) and `Source string` (`json:"source,omitempty"`), and a `SignalSelectionCaptured` constant. Existing signals leave them empty, so their JSON does not change. The daemon already rebroadcasts a client's `signal` verbatim, so it needs no other change.

The host gains `Runner.Signal(key int, p plugin.SignalPayload) error`, an internal way to send a `signal` over its WebSocket connection, built on the runner's existing locked send. Task 0055's `Host` gives plugins no way to send a signal, and this task does not add one for plugins.

### macOS helpers

Task 0055 as built has `macos.SelectedText` but no pasteboard or key-post helpers. This task adds both beside it, each with a stub for other OSes:

- `macos.Copy() error` posts Cmd+C with `CGEventPost`.
- `macos.Pasteboard` with `Save() Saved`, `Restore(Saved)`, and `ChangeCount() int`. `Save` copies every item and type on the general pasteboard; `Restore` writes them back.

`selection.Service` takes the helpers through a small interface, so tests pass fakes.

Files to change:

- `driver/host/selection/selection.go` — `Service`, `Capture`, the AX-then-clipboard logic, the `Captured` payload
- `driver/host/selection/selection_test.go` — tests with fake `macos` helpers
- `driver/host/macos/copy_darwin.go`, `copy_other.go` — `Copy`
- `driver/host/macos/pasteboard_darwin.go`, `pasteboard_other.go` — `Pasteboard`
- `driver/host/*.go` — `SelectedText` and `OnSelection` on `Host`, the `selection` block in `host.Config`, capture on release, `Runner.Signal`
- `driver/host/hosttest/*.go` — `SetSelection`, `Capture`
- `driver/plugin/protocol.go` — `Text` and `Source` on `SignalPayload`, `SignalSelectionCaptured`
- `driver/README.md` — the selection API, the config block, the bus topic, the signal, and the permission step

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — With a fake `SelectedText` that returns `"hello"`, `Capture` returns `{Text: "hello", Source: "ax"}` and never calls `Copy`. **Proof:** `cd driver && go test ./host/selection -run TestCapture_AXFirst`
- [ ] **DoD-2** — With `SelectedText` empty and a fake pasteboard that changes to `"world"` after `Copy`, `Capture` returns `Source: "clipboard"` and `Restore` is called with the saved contents. **Proof:** `go test ./host/selection -run TestCapture_ClipboardFallback`
- [ ] **DoD-3** — With both paths empty, `Capture` returns an empty `Selection`, and `Restore` is still called. **Proof:** `go test ./host/selection -run TestCapture_NothingSelected`
- [ ] **DoD-4** — Two `Capture` calls from two goroutines never overlap. **Proof:** `go test ./host/selection -race -run TestCapture_Serialized`
- [ ] **DoD-5** — A release of a capture key calls `OnSelection` in two instances, publishes on `selection.captured`, and sends one `selectionCaptured` signal with `text` and `source` to the fake daemon. A press, and a release of a key not in `captureKeys`, do none of these. **Proof:** `go test ./host -run TestSelection_CaptureOnRelease`
- [ ] **DoD-6** — A `signal` with no `text` marshals to the same JSON as before this task. **Proof:** `go test ./plugin -run TestSignalPayload_OmitsEmptyText`
- [ ] **DoD-7** — A config with a capture key of 7 fails validation and names it. **Proof:** `go test ./cmd/macropad-plugins -run TestConfig_ReportsEveryProblem`
- [ ] **DoD-8** — On a Mac with Accessibility granted to the host, a release of a capture key with text selected in TextEdit, Safari, and VS Code reaches a Go test subscriber and a WebSocket client such as the virtual pad, and the clipboard holds its prior contents afterwards. **Proof:** manual run, logged in the PR description
- [ ] **DoD-9** — `driver/README.md` documents `SelectedText`, `OnSelection`, the `selection` config block, the `selection.captured` topic, the `selectionCaptured` signal, and the permission step. **Proof:** `driver/README.md`
- [ ] **DoD-10** — `cd driver && go build ./... && go vet ./...` exits 0 on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- The fallback overwrites the clipboard for up to 250 ms → a copy the person makes in that window is lost. The restore runs right after the read to keep the window short.
- A clipboard manager records the captured text → documented in the README. Setting the `org.nspasteboard.TransientType` marker on the restored write may help; DoD-8 checks one manager if one is installed.
- A plugin calls `SelectedText` often, for example on a timer → each call may post Cmd+C and touch the clipboard. The README says to call it only on a person's action.
- The text can hold passwords or other secrets, and the signal reaches every WebSocket client → the server binds to `127.0.0.1` only (task 0028). The README warns against pressing a capture key in password fields.
- Task 0055 changes its names while it is implemented → this spec is updated to match before work starts.

## Open questions

- [ ] Should a capture key also suppress its click-pattern signals? The default is no. — kgheacock
- [ ] Should the host cap the text length? The default is no cap. — kgheacock
- [ ] Should non-Go plugins be able to request a capture on demand, for example with a `captureSelection` message? The default is no; they get captures on configured keys only. — kgheacock
