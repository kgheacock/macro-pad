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
related: ["0013", "0028"]
tags: ["driver", "plugin", "macos", "accessibility"]
---

# 0053 — Capture the frontmost app's selected text when a key is released

## Problem

A person who selects text on the Mac has no way to hand it to the pad. A key press only reaches plugins as a raw `event` or a resolved `signal`. Nothing reads what is selected in the frontmost app, so no plugin can act on it.

## Goals

- A release of a configured key captures the text selected in the frontmost app.
- The capture works in native apps and in apps with weak accessibility support, such as Chrome, Electron apps, and VS Code.
- The clipboard holds the same contents after a capture as before it.
- The text reaches every connected plugin as one broadcast message.

## Non-goals

- Acting on the text. A later task decides where it goes, for example a Claude Code prompt or a file.
- Images, files, or rich text. Plain text only.
- Linux and Windows. The capture is macOS only.
- Changes to the click resolver. A capture key still emits its `singlePress`, `doublePress`, or `longPress` signal as before.

## Approaches considered

### Approach A — Accessibility API only

Read `kAXSelectedTextAttribute` from the system-wide focused element with `AXUIElementCopyAttributeValue`.

- Good, because it reads the selection without touching the clipboard or the app's undo history.
- Good, because it is one synchronous call with no timing guesses.
- Bad, because Chrome, Electron apps, and VS Code often return no value unless `AXEnhancedUserInterface` or `AXManualAccessibility` is set on the app.
- Bad, because it needs Accessibility permission for the plugin binary.

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

The capture runs in a new plugin process, `driver/examples/selection-capture`, per task 0013's rule that reactions live in plugins, not in the daemon. The pad is not a USB keyboard, so a key press does not move focus. The selection is still in place when the release arrives.

The plugin connects to the daemon like `driver/examples/claude-status`. It takes a `--keys` flag, a comma-separated list of key indexes. On each raw `event` with `type: "release"` for one of those keys, it:

1. Reads `AXSelectedText` from the focused element. A non-empty result is the capture.
2. Otherwise saves every pasteboard item, posts Cmd+C, and polls `changeCount` every 10 ms for up to 250 ms. A change means the new string is the capture. It then writes the saved items back. No change means no capture.
3. Broadcasts a `signal` named `selectionCaptured` with `keyIndex`, `text`, and `source` set to `"ax"` or `"clipboard"`. An empty capture broadcasts nothing.

Captures run one at a time. A release that arrives during a capture is dropped.

Protocol change: `SignalPayload` gains two optional fields, `Text string` (`json:"text,omitempty"`) and `Source string` (`json:"source,omitempty"`). Existing signals leave them empty, so their JSON does not change. The server already rebroadcasts a client's `signal` verbatim, so it needs no other change.

The macOS calls sit behind a small interface, `selector`, with `AXText() (string, error)` and `CopyText() (string, bool, error)`. A cgo file with the `darwin` build tag implements it. A fake implements it in tests, so the capture logic and the broadcast run under `go test` on any OS.

Files to change:

- `driver/plugin/protocol.go` — `Text` and `Source` on `SignalPayload`, the `SignalSelectionCaptured` constant
- `driver/examples/selection-capture/main.go` — flags, connection, release handling, broadcast
- `driver/examples/selection-capture/capture.go` — the AX-then-clipboard logic over `selector`
- `driver/examples/selection-capture/selector_darwin.go` — cgo calls to ApplicationServices and AppKit
- `driver/examples/selection-capture/selector_other.go` — a stub that returns an "unsupported OS" error
- `driver/examples/selection-capture/main_test.go` — tests with a fake `selector`
- `driver/README.md` — the `selectionCaptured` signal and the Accessibility permission step

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — A release on a configured key, with a fake `selector` whose AX path returns `"hello"`, broadcasts `{keyIndex, name: "selectionCaptured", text: "hello", source: "ax"}` and never calls the copy path. **Proof:** `cd driver && go test ./examples/selection-capture -run TestCapture_AXFirst`
- [ ] **DoD-2** — With an AX path that returns an empty string and a copy path that returns `"world"`, the broadcast carries `text: "world"` and `source: "clipboard"`. **Proof:** `go test ./examples/selection-capture -run TestCapture_ClipboardFallback`
- [ ] **DoD-3** — With both paths empty, nothing is broadcast. **Proof:** `go test ./examples/selection-capture -run TestCapture_NothingSelected`
- [ ] **DoD-4** — A release on a key not in `--keys`, and any `press`, trigger no capture. **Proof:** `go test ./examples/selection-capture -run TestCapture_IgnoresOtherEvents`
- [ ] **DoD-5** — A `signal` with no `text` marshals to the same JSON as before this task. **Proof:** `go test ./plugin -run TestSignalPayload_OmitsEmptyText`
- [ ] **DoD-6** — On a Mac with Accessibility granted, a release with text selected in TextEdit, Safari, and VS Code broadcasts that text, and the clipboard holds its prior contents afterwards. **Proof:** manual run, logged in the PR description
- [ ] **DoD-7** — `driver/README.md` documents `selectionCaptured`, its fields, and the Accessibility permission step. **Proof:** `driver/README.md`
- [ ] **DoD-8** — `cd driver && go build ./... && go vet ./...` exits 0 on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- The fallback overwrites the clipboard for up to 250 ms → a copy the person makes in that window is lost. The restore runs right after the read to keep the window short.
- A clipboard manager records the captured text → documented in the README. Setting the `org.nspasteboard.TransientType` marker on the restored write may help; DoD-6 checks one manager if one is installed.
- An app with no selection and no copy response costs the full 250 ms timeout → acceptable for a key release. No proof item covers slow apps.
- Accessibility permission is per binary → a rebuilt plugin may need the grant again. The README says so.
- The text can hold passwords or other secrets, and it reaches every connected plugin → the server binds to `127.0.0.1` only (task 0028). The README warns against binding a capture key in password fields.

## Open questions

- [ ] Should a capture key also suppress its click-pattern signals? The default is no. — kgheacock
- [ ] Should the plugin cap the text length? The default is no cap. — kgheacock
