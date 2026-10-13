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
tags: ["driver", "plugin", "macos", "accessibility"]
---

# 0053 — Capture the frontmost app's selected text when a key is released

## Prerequisite

Task 0055 (the plugin host and helper API) must be implemented on `main` before work on this task starts. This spec uses its `Host` handle, its `macos` helpers, and its bus.

## Problem

A person who selects text on the Mac has no way to hand it to the pad. A key press only reaches plugins as a raw `event` or a resolved `signal`. Nothing reads what is selected in the frontmost app, so no plugin can act on it.

## Goals

- A release of a configured key captures the text selected in the frontmost app.
- The capture works in native apps and in apps with weak accessibility support, such as Chrome, Electron apps, and VS Code.
- The clipboard holds the same contents after a capture as before it.
- The text reaches every other plugin in the plugin host as one message on the host bus.

## Non-goals

- Acting on the text. A later task decides where it goes, for example a Claude Code prompt or a file.
- Images, files, or rich text. Plain text only.
- Linux and Windows. The capture is macOS only.
- Changes to the click resolver. A capture key still emits its `singlePress`, `doublePress`, or `longPress` signal as before.
- Changes to the daemon or the wire protocol. The plugin runs in the task 0055 plugin host, which is a WebSocket client like any other.

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

The capture is a plugin package, `driver/plugins/selectioncapture`, that runs in the task 0055 plugin host. It writes no `main`, no flag parsing, and no dial. It is listed in `driver/cmd/macropad-plugins/plugins.go` as `selection-capture`. A config entry gives it its keys:

```json
{"id": "grab", "use": "selection-capture", "page": "work", "keys": [5]}
```

The pad is not a USB keyboard, so a key press does not move focus. The selection is still in place when the release arrives.

`Start(ctx, h)`:

1. Calls `h.Require(macos.Accessibility)`. A missing grant disables only this instance. The grant is the host binary's, so it covers every plugin in the host.
2. Registers `h.OnRelease`. The host routes only releases of this instance's own keys on the active page, so the plugin needs no key filter.

On each release, the handler runs on the instance's own queue (task 0055), so a 250 ms fallback does not delay other plugins:

1. Calls `macos.SelectedText()`. A non-empty result is the capture.
2. Otherwise calls `Pasteboard.Save()`, then `macos.Copy()`, then polls `Pasteboard.ChangeCount()` every 10 ms for up to 250 ms. A change means the new string is the capture. It then calls `Pasteboard.Restore(saved)`. No change means no capture.
3. Publishes a `selection.Captured{Key int, Text string, Source string}` on the bus topic `selection.captured`, with `Source` set to `"ax"` or `"clipboard"`. An empty capture publishes nothing.

Because the handler queue runs calls in order, a release during a capture waits for it instead of running at the same time.

Additions to the 0055 helpers:

- `macos.Copy() error` posts Cmd+C with `CGEventPost`. Task 0055 lists `SelectedText` and `Pasteboard` but no key-post helper. This task adds it beside them, with a stub for other OSes.
- `driver/host/topics.go` gains the `selection.Captured` payload type for the `selection.captured` topic that 0055 already names.

The plugin takes the `macos` helpers through a small interface (`SelectedText`, `Copy`, `Pasteboard`), so tests pass fakes. Tests drive it with `hosttest.New(t, 5)` and read the bus with `Subscribe`.

Files to change:

- `driver/plugins/selectioncapture/selectioncapture.go` — `Start`, the release handler, the AX-then-clipboard logic
- `driver/plugins/selectioncapture/selectioncapture_test.go` — tests on `hosttest` with fake `macos` helpers
- `driver/host/macos/copy_darwin.go`, `copy_other.go` — `Copy`
- `driver/host/topics.go` — the `selection.Captured` payload
- `driver/cmd/macropad-plugins/plugins.go` — register the plugin
- `driver/README.md` — the plugin, its config entry, and the `selection.captured` topic

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — A release on an owned key, with a fake `SelectedText` that returns `"hello"`, publishes `{Key, Text: "hello", Source: "ax"}` on `selection.captured` and never calls `Copy`. **Proof:** `cd driver && go test ./plugins/selectioncapture -run TestCapture_AXFirst`
- [ ] **DoD-2** — With `SelectedText` empty and a fake pasteboard that changes to `"world"` after `Copy`, the message carries `Text: "world"` and `Source: "clipboard"`, and `Restore` is called with the saved contents. **Proof:** `go test ./plugins/selectioncapture -run TestCapture_ClipboardFallback`
- [ ] **DoD-3** — With both paths empty, nothing is published, and `Restore` is still called. **Proof:** `go test ./plugins/selectioncapture -run TestCapture_NothingSelected`
- [ ] **DoD-4** — A press on an owned key triggers no capture. **Proof:** `go test ./plugins/selectioncapture -run TestCapture_IgnoresPress`
- [ ] **DoD-5** — With `h.Require(macos.Accessibility)` failing in the fake host, `Start` returns an error and registers no handler. **Proof:** `go test ./plugins/selectioncapture -run TestStart_RequiresAccessibility`
- [ ] **DoD-6** — `macropad-plugins --list` shows `selection-capture`. **Proof:** the command's output, pasted in the PR description
- [ ] **DoD-7** — On a Mac with Accessibility granted to the host, a release with text selected in TextEdit, Safari, and VS Code publishes that text, and the clipboard holds its prior contents afterwards. **Proof:** manual run with a test subscriber that logs the topic, logged in the PR description
- [ ] **DoD-8** — `driver/README.md` documents the plugin, its config entry, and the `selection.captured` payload. **Proof:** `driver/README.md`
- [ ] **DoD-9** — `cd driver && go build ./... && go vet ./...` exits 0 on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- The fallback overwrites the clipboard for up to 250 ms → a copy the person makes in that window is lost. The restore runs right after the read to keep the window short.
- A clipboard manager records the captured text → documented in the README. Setting the `org.nspasteboard.TransientType` marker on the restored write may help; DoD-7 checks one manager if one is installed.
- An app with no selection and no copy response costs the full 250 ms timeout → the delay stays on this instance's queue; other plugins are not held up.
- The text can hold passwords or other secrets → it stays inside the host process on the bus and never goes over the WebSocket. The README warns against pressing a capture key in password fields.
- Task 0055 changes its helper names while it is implemented → this spec is updated to match before work starts.

key release. No proof item covers slow apps.
- Accessibility permission is per binary → a rebuilt plugin may need the grant again. The README says so.
- The text can hold passwords or other secrets, and it reaches every connected plugin → the server binds to `127.0.0.1` only (task 0028). The README warns against binding a capture key in password fields.

## Open questions

- [ ] Should a capture key also suppress its click-pattern signals? The default is no. — kgheacock
- [ ] Should the plugin cap the text length? The default is no cap. — kgheacock
- [ ] Should the capture also reach non-Go WebSocket plugins, as a `signal` with a `text` field? Task 0055 gives a host plugin no way to send a signal, so the default is bus only. — kgheacock
