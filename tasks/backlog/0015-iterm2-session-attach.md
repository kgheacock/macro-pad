---
id: "0015"
title: "Attach a key to an existing iTerm2 session over its WebSocket API"
status: "backlog"
created: "2026-08-06"
updated: "2026-08-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0011", "0013", "0016"]
tags: ["driver", "iterm2", "terminal"]
---

# 0015 — Attach a key to an existing iTerm2 session over its WebSocket API

## Problem

The driver can only reach a session it creates itself (task 0011). A key
cannot yet bind to a terminal tab the user already has open. iTerm2's
native Python API reaches an existing session, sends text, and pushes
shell-integration lifecycle events, with no Accessibility or Automation
permission prompt.

## Goals

- A key binds to a specific, already-running iTerm2 session, addressed by
  iTerm2's session ID, not one the driver spawned.
- Text sent to that key reaches the session through iTerm2's
  `async_send_text`, not tmux.
- iTerm2's shell-integration notifications map to task 0013's and 0016's
  `EventMask`: prompt shown to `PROCESS_WAITING`, command start to
  `PROCESS_RUNNING`, command finish to `PROCESS_DONE` or
  `PROCESS_EXITED`.
- A disabled iTerm2 Python API toggle produces a named error, not a hang.

## Non-goals

- Picking which session to bind automatically, such as "whatever tab is
  frontmost." Binding is by a session ID a human configures.
- Terminal apps other than iTerm2. Each needs its own investigation.
- Replacing task 0011's tmux path. This is an additional, parallel path.

## Approaches considered

### Approach A — A Go-native WebSocket and protobuf client

Speak iTerm2's API protocol directly from Go, with no Python dependency.

- Good, because the driver stays a single binary with no Python runtime
  dependency.
- Good, because it keeps one process, no subprocess bridge to manage.
- Bad, because iTerm2 only publishes an official client for Python — a Go
  client means hand-porting protobuf bindings and auth handling with no
  reference implementation.
- Bad, because a future protocol change in iTerm2 breaks this silently,
  with no maintained Go client tracking it.

### Approach B — A Python helper bridging the official `iterm2` package

Run a small Python script that uses the official `iterm2` package, and
talk to it from Go over stdin/stdout with line-delimited JSON.

- Good, because it reuses the actual maintained client instead of
  reimplementing iTerm2's protobuf protocol by hand.
- Good, because the helper script stays small — a thin translator between
  iTerm2 events and JSON lines.
- Bad, because it adds a Python runtime dependency (`pip install iterm2`)
  alongside the tmux dependency task 0011 already has.
- Bad, because it adds a second IPC hop (Go to Python to iTerm2), one
  more place to lose a message than Approach A.

### Approach C — AppleScript (`tell application "iTerm2"`) instead of the API

Drive iTerm2 through AppleScript, the same mechanism available for
Terminal.app.

- Good, because it needs no new runtime dependency — `osascript` ships
  with macOS.
- Good, because it works even if the user has not enabled iTerm2's Python
  API toggle.
- Bad, because it needs per-app Automation permission, the exact TCC
  friction iTerm2's native API avoids.
- Bad, because it has no event push — AppleScript can only poll
  `contents`, so `PROCESS_WAITING` and `PROCESS_DONE` would need
  pane-scraping instead of the notifications this task wants.

## Decision

Chosen: **Approach B — a Python helper bridging the official `iterm2`
package**.

It reuses the maintained client instead of reimplementing an internal
protocol in Go, and it is the only approach that keeps both guarantees
that made iTerm2 attractive: no TCC permission prompt, and pushed
shell-integration events instead of polling. This choice accepts a new
Python runtime dependency and a second local IPC hop.

## Design

`driver/iterm2bridge/bridge.py` uses the `iterm2` package to connect to
iTerm2, resolve a session by ID, expose `send_text`, and forward
shell-integration notifications as JSON lines over stdout.
`driver/session/iterm2.go` launches and talks to that helper, and
translates its notifications into task 0013's and 0016's `EventMask`
values, dispatched through the same `UseAction` path used by the tmux
path. A shared `driver/session/interface.go` extracts the `Own` /
`SendKeys`-shaped interface so tmux (task 0011) and this backend both
satisfy it.

Files to change:

- `driver/iterm2bridge/bridge.py` — new, the Python helper
- `driver/session/iterm2.go` — new, Go-side process management and JSON
  client
- `driver/session/interface.go` — new, the shared session interface
- `driver/README.md` — document the iTerm2 backend, the Python
  dependency, and the API-enable toggle

## Definition of done

- [ ] **DoD-1** — Binding key 0 to an existing iTerm2 session ID and
  sending `"y\n"` delivers that text into the target tab. **Proof:**
  `go test ./driver/session/... -run TestITerm2_SendText` against a mock
  bridge
- [ ] **DoD-2** — A prompt-shown notification on the bound session raises
  `PROCESS_WAITING`. **Proof:** `go test ./driver/session/... -run
  TestITerm2_PromptRaisesWaiting`
- [ ] **DoD-3** — A command-start notification raises `PROCESS_RUNNING`.
  **Proof:** `go test ./driver/session/... -run
  TestITerm2_CommandStartRaisesRunning`
- [ ] **DoD-4** — A command-finished notification maps to `PROCESS_DONE`
  on exit 0 and `PROCESS_EXITED` on a nonzero exit. **Proof:** `go test
  ./driver/session/... -run TestITerm2_CommandFinishedMapsExitCode`
- [ ] **DoD-5** — With iTerm2's Python API toggle off, binding a key
  returns a named error identifying the missing toggle. **Proof:** `go
  test ./driver/session/... -run TestITerm2_APIDisabled`
- [ ] **DoD-6** — `driver/README.md` documents the iTerm2 backend, the
  Python dependency, and the API-enable toggle. **Proof:**
  `driver/README.md`
- [ ] **DoD-7** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- iTerm2's Python API surface can change across major versions and break
  the bridge script → `driver/README.md` records a minimum iTerm2
  version.
- Python plus the `iterm2` package is a second runtime dependency beyond
  tmux → documented as required for the iTerm2 path only, not the tmux
  path.
