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
related: ["0011", "0013", "0015", "0017", "0028", "0054", "0055"]
tags: ["driver", "plugin", "macos", "windows"]
---

# 0056 — Switch to a desktop, app, or window from a key press

## Problem

The pad cannot move a person between screens. A key press reaches plugins as a raw `event` or a resolved `signal`, but no plugin turns it into a desktop, app, or window switch. Tasks 0011, 0012, 0015, and 0017 each reach one terminal session. None of them switches macOS desktops or brings an arbitrary app or window to the front.

## Dependency

This task builds on task 0055, the plugin host. It starts only after 0055's implementation is on `main`. The switcher is a host plugin: it uses the host's key ownership, input routing, pages, bus, `keyimg` color helpers, and `macos` switch helpers. It does not dial the daemon, parse flags, or run its own `osascript` runner.

## Goals

- A `window-switcher` host plugin binds each key it owns to one target: a desktop number, an app, or an app window whose title contains given text.
- A press on a bound key brings its target to the front.
- With no `targets` in its config, the plugin's Nth owned key switches to desktop N+1.
- Each bound key lights in its own color. The key for the last switch is bright and the rest are dim, so the pad shows where the person is.
- Just before a switch, the plugin publishes `window.willHide` with the frontmost window, so task 0054's preview plugin can snapshot it.
- A failed switch logs one line. The plugin keeps running, and the lit key does not change.

## Non-goals

- Thumbnails of hidden desktops or windows. Task 0054 covers them.
- Tracking switches made outside the pad, by trackpad, keyboard, or Mission Control.
- Binding to a specific terminal session. Tasks 0011 and 0015 cover that.
- Linux and Windows. The switch is macOS only.
- Changes to the daemon, the plugin protocol, or the host. A gap in the host's API is logged under Open questions for task 0055, not patched here.

## Approaches considered

### Approach A — A host plugin over the host's `macos` helpers

A package `driver/plugins/windowswitcher` implements `host.Plugin`. On a trigger it calls `macos.SwitchDesktop`, `macos.ActivateApp`, or `macos.RaiseWindow` from task 0055, and sets key colors with `h.Key(k)`.

- Good, because the plugin holds only its own logic: config, dispatch, and the active key. Task 0055 already owns the AppleScript, the colors, the reconnect, and the tests' fake host.
- Good, because the host binary holds the one Accessibility grant, so the switcher needs no grant of its own.
- Good, because the in-process bus lets it tell task 0054's previews which window is about to hide, with no race.
- Bad, because it cannot ship until task 0055 lands.
- Bad, because each switch still starts an `osascript` process, about 100 ms or more, if the host's helpers use `osascript`.

### Approach B — A standalone plugin binary now, ported to the host later

Ship the prototype in commit `d63fa45` as `driver/examples/window-switcher`, its own binary over the WebSocket API. Port it when task 0055 lands.

- Good, because it can run on the pad today.
- Bad, because it duplicates helpers that task 0055 moves into the host (`parseHexColor`, `dim`, the `osascript` runner). The port then deletes them again.
- Bad, because it needs its own Accessibility grant, and it cannot call the preview plugin.

### Approach C — Add the switch to the host's built-in `pager` plugin

Extend task 0055's `pager` so a page can map keys to desktops.

- Good, because there is no new plugin.
- Bad, because it mixes two jobs in one plugin: moving between pad pages and moving between screens.
- Bad, because it changes task 0055's scope, which is a non-goal here.

## Decision

Chosen: **Approach A — a host plugin over the host's `macos` helpers**.

Task 0055 was written with this plugin in mind, and moves its helpers into the host. Building the switcher on the host avoids writing those helpers twice and gives it the bus for previews. The task accepts the wait for 0055.

## Design

**Package.** `driver/plugins/windowswitcher`, registered in `driver/cmd/macropad-plugins/plugins.go` as `window-switcher`.

**Host config entry.**

```json
{"id": "desks", "use": "window-switcher", "page": "work", "keys": [0, 1, 2, 3],
 "config": {
   "trigger": "press",
   "targets": [
     {"key": 0, "desktop": 1},
     {"key": 1, "app": "Safari", "color": "#12D84A"},
     {"key": 2, "app": "iTerm2", "window": "build"},
     {"key": 3, "desktop": 2}
   ]
 }}
```

- `trigger` is `press` (`h.OnPress`, no delay) or one of `single`, `double`, `long` (`h.OnClick`). Empty means `press`.
- `targets[].key` is a physical key index and must be in `h.Keys()`, per task 0055's open question on key indexes. If 0055 picks slots instead, `key` becomes a slot.
- Each target sets exactly one of `desktop` (1–9) or `app`. `window` needs `app`.
- `color` is `#RRGGBB`, parsed with `keyimg.Hex`. Empty uses `keyimg.Palette`.
- With no `targets`, the Nth owned key switches to desktop N+1.
- `Start` decodes the config with `h.Config` and returns an error for: a key not owned, a key used twice, both or neither of `desktop` and `app`, a desktop outside 1–9, `window` without `app`, a bad color, or a bad trigger. The host disables only this instance.
- `Start` calls `h.Require(macos.Accessibility)`. Desktop and window targets need it. An app-only config could skip it; see Open questions.

**Switching.** On a trigger for a bound key, the plugin:

1. Reads `macos.Frontmost()` and publishes `window.willHide` with its window id. An error here logs and does not stop the switch.
2. Calls `macos.SwitchDesktop(n)`, `macos.ActivateApp(app)`, or `macos.RaiseWindow(app, title)`.
3. On success, if the key is not already active, sets the old active key to `color.Dim(0.25)` and the new one to `color`. That is two key writes, not six (task 0050 measured about 155 ms per board write in a burst).

**Key lights.** In `Start`, every bound key is set to its dim color. The host caches it and shows it when the page is visible. On `OnVisible(true)`, the plugin does nothing more: the host repaints from its cache.

**Mac setup.** The Ctrl+N "Switch to Desktop N" shortcuts are off by default. The person turns them on once in System Settings > Keyboard > Keyboard Shortcuts > Mission Control. The README says so.

Files to change:

- `driver/plugins/windowswitcher/plugin.go` — config, `Start`, dispatch, key lights
- `driver/plugins/windowswitcher/plugin_test.go` — tests on `hosttest` with a fake `macos`
- `driver/cmd/macropad-plugins/plugins.go` — one registry line
- `driver/README.md` — the plugin's config, the shortcut setup, and Mac test steps, in the "Plugin host" section

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — With no `targets`, an instance owning keys 0, 1, and 2 binds them to desktops 1, 2, and 3. **Proof:** `cd driver && go test ./plugins/windowswitcher -run TestDefaultTargets`
- [ ] **DoD-2** — Each invalid config listed in Design makes `Start` return an error that names the field. **Proof:** `go test ./plugins/windowswitcher -run TestStartRejects`
- [ ] **DoD-3** — With `trigger: "press"`, a press on a bound key calls the matching `macos` switch once. A release and a single click call nothing. **Proof:** `go test ./plugins/windowswitcher -run TestPressSwitches`
- [ ] **DoD-4** — With `trigger: "long"`, only a long click switches. **Proof:** `go test ./plugins/windowswitcher -run TestClickTrigger`
- [ ] **DoD-5** — After `Start`, every bound key is dim. A switch to a new key sets the old key dim and the new key bright. A second press on the active key changes no key. **Proof:** `go test ./plugins/windowswitcher -run TestKeyLights`
- [ ] **DoD-6** — A switch publishes `window.willHide` with the frontmost window's id before it calls the switch. **Proof:** `go test ./plugins/windowswitcher -run TestPublishesWillHide`
- [ ] **DoD-7** — A failed switch logs one line and leaves the lit key unchanged. **Proof:** `go test ./plugins/windowswitcher -run TestFailedSwitchKeepsActiveKey`
- [ ] **DoD-8** — The package has no `osascript` runner, hex parser, or dim function of its own. **Proof:** `grep -rn "osascript\|func dim\|func parseHexColor" driver/plugins/windowswitcher` returns nothing
- [ ] **DoD-9** — On a Mac with six desktops and the Ctrl+1–6 shortcuts on, running `macropad-plugins` with the README's example config, key 1 moves to desktop 2 and lights bright. **Proof:** manual run on the board, logged in the PR description
- [ ] **DoD-10** — On a Mac, an `app` + `window` target raises the matching iTerm2 window above another iTerm2 window. **Proof:** manual run, logged in the PR description
- [ ] **DoD-11** — With task 0054's preview plugin on another page, a switch leaves a fresh snapshot of the window that was in front. **Proof:** manual run, logged in the PR description. Skip if 0054 is not on `main`, and note it.
- [ ] **DoD-12** — `driver/README.md` documents the plugin's config, the shortcut setup, and the Mac test steps. **Proof:** `driver/README.md`
- [ ] **DoD-13** — `cd driver && go build ./... && go vet ./...` exits 0 on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-14** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- Task 0055's final API differs from its spec → this spec's Design follows the merged code, and the differences are noted in the implementing PR.
- `RaiseWindow` on a window that sits on a different desktop from the app's front window may raise it without moving there → the README says so and suggests desktop targets for that case. DoD-10 records what happens.
- Full-screen apps take their own desktop and shift the numbers Ctrl+N reaches → documented in the README.
- The bright key goes stale when the person switches by other means → accepted, as a non-goal. A later task could watch the active desktop.
- About 100 ms per switch if the host's helpers run `osascript` → DoD-9 records the felt delay. A faster path belongs in the host's `macos` package, not here.

## Open questions

- [ ] Should the default trigger be `press`, or `single` so double and long clicks stay free? `single` adds the 400 ms double-click wait. The default is `press`. — kgheacock
- [ ] Should the window target match the title exactly, by substring, or by pattern? The default is substring, matching `macos.RaiseWindow` in task 0055. — kgheacock
- [ ] Should a long press bind its key to whatever window is in front, with no config edit? The default is no. — kgheacock
- [ ] Should an app-only config skip `h.Require(macos.Accessibility)`, since `activate` needs only Automation? The default is to require it always. — kgheacock

## Notes

- A standalone prototype is in commit `d63fa45` on the branch `claude/project-thread-8hsd26`. Its tests passed on Linux, and a dry run against `macropadd --emulate` printed the right commands. It has not run on a Mac. Task 0055 moves its `osascript` runner, `parseHexColor`, and `dim` into the host, so only its config and dispatch logic carry over.
