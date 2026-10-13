---
id: "0056"
title: "Switch to a desktop, app, or window from a key press"
status: "backlog"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/60"
branch: null
related: ["0011", "0012", "0013", "0015", "0017", "0028"]
tags: ["driver", "plugin", "macos", "windows"]
---

# 0056 — Switch to a desktop, app, or window from a key press

## Problem

The pad cannot move a person between screens. A key press reaches plugins as a raw `event` or a resolved `signal`, but no plugin turns it into a desktop, app, or window switch. Tasks 0011, 0012, 0015, and 0017 each reach one terminal session. None of them switches macOS desktops or brings an arbitrary app or window to the front.

## Goals

- Each of keys 0–5 binds to one target: a desktop number, an app, or an app window whose title contains given text.
- A press on a bound key brings its target to the front.
- With no config, key N switches to desktop N+1.
- Each bound key lights in its own color. The key for the last switch is bright and the rest are dim, so the pad shows where the person is.
- A failed switch logs one line. The plugin keeps running, and the lit key does not change.

## Non-goals

- Thumbnails of hidden desktops or windows. A later task covers them.
- Tracking switches made outside the pad, by trackpad, keyboard, or Mission Control.
- Binding to a specific terminal session. Tasks 0011 and 0015 cover that.
- Linux and Windows. The switch is macOS only. A dry-run mode prints commands on any OS.
- Changes to the daemon or the plugin protocol.

## Approaches considered

### Approach A — Plugin that runs `osascript`

A plugin process reacts to presses and runs AppleScript through `/usr/bin/osascript`. A desktop target sends Ctrl+N through System Events. An app target runs `tell application X to activate`. A window target activates the app, then performs `AXRaise` on the first window whose title contains the text.

- Good, because it uses the plugin API from task 0028 as it is, with no daemon change.
- Good, because it needs no cgo and no new Go dependency, so it builds and tests on Linux.
- Bad, because each switch starts a process. That costs about 100 ms or more per press.
- Bad, because the Ctrl+N "Switch to Desktop N" shortcuts are off by default. The person must turn them on once in System Settings.

### Approach B — cgo bindings to the Accessibility and CGEvent APIs

The plugin posts Ctrl+N with `CGEventPost`, activates apps with `NSRunningApplication`, and raises windows with `AXUIElementPerformAction`.

- Good, because there is no process per switch.
- Good, because errors come back as typed codes, not as AppleScript text.
- Bad, because it adds Objective-C and cgo to the plugin, and tests need a stub file for other OSes.
- Bad, because it still needs the Ctrl+N shortcuts on, as Approach A does.

### Approach C — Private CoreGraphics Services calls

The plugin calls private functions such as `CGSManagedDisplaySetCurrentSpace` to change desktops with no keyboard shortcut.

- Good, because it needs no System Settings change.
- Good, because it can reach any desktop, not only 1–9.
- Bad, because the APIs are private and undocumented, and they break between macOS releases.
- Bad, because recent macOS releases block some of them unless System Integrity Protection is off.

## Decision

Chosen: **Approach A — plugin that runs `osascript`**.

It needs no daemon change and no cgo, and the whole plugin is testable on any OS. The task accepts about 100 ms per switch and a one-time shortcut setup. If the delay is too slow on the board, a later task can move the hot path to Approach B behind the same interface.

## Design

The plugin is a new process, `driver/examples/window-switcher`, per task 0013's rule that reactions live in plugins. It connects to the daemon like `driver/examples/claude-status`.

**Flags.**

| Flag | Default | Meaning |
|---|---|---|
| `--config PATH` | none | JSON bindings. With none, keys 0–5 switch to desktops 1–6. |
| `--addr HOST:PORT` | `127.0.0.1:8765` | The running `macropadd`. |
| `--dry-run` | off | Print each `osascript` command line instead of running it. |

**Config.**

```json
{
  "trigger": "press",
  "keys": [
    {"key": 0, "desktop": 1},
    {"key": 1, "app": "Safari", "color": "#12D84A"},
    {"key": 2, "app": "iTerm2", "window": "build"}
  ]
}
```

- `trigger` is `press` (the raw press event, no delay) or one of the resolved signals `singlePress`, `doublePress`, or `longPress`. Empty means `press`.
- Each key sets exactly one of `desktop` (1–9) or `app`. `window` needs `app`.
- `color` is `#RRGGBB`, converted to RGB565. Empty gives each key a distinct default.
- Unknown fields, a key outside 0–5, a key bound twice, and a bad color or trigger all stop the plugin at startup with one error line.

**Switching.** Each target becomes one `osascript` run:

| Target | AppleScript | macOS permission |
|---|---|---|
| `desktop` | System Events `key code K using control down`, where K is the digit's virtual key code (1→18 … 9→25) | Accessibility, plus the Mission Control shortcut |
| `app` | `tell application (item 1 of argv) to activate` | Automation |
| `app` + `window` | activate, then System Events `perform action "AXRaise"` on the first window whose name contains the text | Accessibility and Automation |

App names and window titles go to `osascript` as run-handler arguments (`on run argv`). They are never spliced into the script source, so a title with a quote in it cannot change what runs.

**Key lights.** On connect, the plugin sends one `setKeyState` per bound key at a quarter of its color. After a switch succeeds on a key that is not already active, it sends two messages: the old key dim and the new key bright. This keeps a switch to two board writes, not six (task 0050 measured about 155 ms per write in a burst).

**Testing seams.** The runner sits behind an interface, `runner`, with `Run(switchScript) error`. The connection sits behind `keyConn`, with `ReadMessage` and `SetKeyState`. Fakes implement both in tests, so the dispatch, the lights, and the script text run under `go test` on any OS. `--dry-run` uses a runner that prints.

Files to change:

- `driver/examples/window-switcher/main.go` — flags, connection, dispatch, key lights
- `driver/examples/window-switcher/config.go` — config parsing, validation, colors
- `driver/examples/window-switcher/macos.go` — AppleScript for each target, the `osascript` runner
- `driver/examples/window-switcher/main_test.go` — tests with a fake runner and a fake connection
- `driver/examples/window-switcher/README.md` — config, permissions, and Mac test steps
- `driver/README.md` — a short section that points to the plugin

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — The default config binds key N to desktop N+1 for keys 0–5. **Proof:** `cd driver && go test ./examples/window-switcher -run TestDefaultConfig`
- [ ] **DoD-2** — Each invalid config listed in Design is rejected at startup. **Proof:** `go test ./examples/window-switcher -run TestParseConfigRejects`
- [ ] **DoD-3** — Desktops 1, 5, 6, and 9 produce key codes 18, 23, 22, and 25 with Control held. **Proof:** `go test ./examples/window-switcher -run TestScriptForDesktop`
- [ ] **DoD-4** — An app name or window title that holds a quote appears only in the `osascript` arguments, never in a script line. **Proof:** `go test ./examples/window-switcher -run TestScriptForPassesNamesAsArguments`
- [ ] **DoD-5** — With `trigger: "press"`, a press on a bound key runs its script once. A release, an unbound key, and a `singlePress` signal run nothing. **Proof:** `go test ./examples/window-switcher -run TestWatchSwitchesOnPress`
- [ ] **DoD-6** — On connect, every bound key is lit dim. A switch to a new key sends the old key dim and the new key bright. A second press on the active key sends no `setKeyState`. **Proof:** same test as DoD-5
- [ ] **DoD-7** — With `trigger: "longPress"`, only a `longPress` signal switches. **Proof:** `go test ./examples/window-switcher -run TestWatchUsesResolvedSignalTrigger`
- [ ] **DoD-8** — A failed switch logs one line and leaves the lit key unchanged. **Proof:** `go test ./examples/window-switcher -run TestWatchKeepsActiveKeyWhenSwitchFails`
- [ ] **DoD-9** — With `macropadd --emulate` and `--dry-run`, an injected press on a bound key prints its `osascript` command line, and the daemon logs the `setKeyState` writes. **Proof:** manual run, logged in the PR description
- [ ] **DoD-10** — On a Mac with six desktops and the Ctrl+1–6 shortcuts on, key 1 moves to desktop 2, and the plugin lights key 1 bright. **Proof:** manual run on the board, logged in the PR description
- [ ] **DoD-11** — On a Mac, an `app` + `window` binding raises the matching iTerm2 window above another iTerm2 window. **Proof:** manual run, logged in the PR description
- [ ] **DoD-12** — The plugin README gives the config format, the permissions, and the Mac test steps. `driver/README.md` links to it. **Proof:** the two files
- [ ] **DoD-13** — `cd driver && go build ./... && go vet ./...` exits 0 on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-14** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- `AXRaise` on a window that sits on a different desktop from the app's front window may raise it without moving there → the README says so and suggests desktop bindings for that case. DoD-11 records what happens.
- Full-screen apps take their own desktop and shift the numbers Ctrl+N reaches → documented in the README.
- The bright key goes stale when the person switches by other means → accepted, as a non-goal. A later task could watch the active desktop.
- Accessibility permission is per binary, so a rebuilt plugin, or a different terminal, may need the grant again → the README says so.
- About 100 ms per `osascript` run → DoD-10 records the felt delay. If it is too slow, a later task moves to Approach B.

## Open questions

- [ ] Should the default trigger be the raw `press`, or `singlePress` so double and long presses stay free for other plugins? `singlePress` adds the 400 ms double-press wait. The default is `press`. — kgheacock
- [ ] Should the window target match the title exactly, by substring, or by pattern? The default is substring. — kgheacock
- [ ] Should a long press on a key bind it to whatever window is in front, with no config edit? The default is no. — kgheacock

## Notes

- A prototype of this design is in commit `d63fa45` on the branch `claude/project-thread-8hsd26`. Its tests passed on Linux, and a dry run against `macropadd --emulate` printed the right commands. It has not run on a Mac.
