---
id: "0017"
title: "Disposable PoC — wire a long press to an iTerm2 tab switch"
status: "backlog"
created: "2026-08-14"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0011", "0012", "0013", "0014", "0015", "0028"]
tags: ["driver", "poc", "ergonomics", "iterm2"]
---

# 0017 — Disposable PoC — wire a long press to an iTerm2 tab switch

## Problem

Tasks 0011–0015 each build one piece: owned sessions, input actions, task
0013's `signal` broadcast, and a way to attach a key to an iTerm2
session. Once they merge, the glue code for one flow does not yet exist:
a long press on a key selects a specific iTerm2 tab. Nobody knows if the
assembled API reads well for that flow, or if task 0015's session
interface lacks a call it needs.

## Goals

- A disposable PoC writes a plugin that subscribes to task 0028's
  WebSocket API, reacts to a `longPress` `signal` on one key, and
  switches the tab on a bound iTerm2 session. It uses the real APIs from
  tasks 0011–0015 and 0028, assumed complete.
- The PoC drives the key press itself through `driver/transport.Emulator`
  (task 0014, already complete), not a new stand-in.
- The PoC states whether task 0015's session interface already exposes a
  tab-switch call, or names the one method the interface lacks.
- A short write-up records whether the finished plugin reads as a plain
  handful of lines, or whether it needs extra scaffolding. This lets a
  reader judge the API's ergonomics without a need to write the code.

## Non-goals

- A new way to reach iTerm2. Task 0015 already chose its Python API
  bridge. This PoC calls whatever that bridge exposes. It does not add an
  AppleScript path or a keystroke path.
- Changing tasks 0011–0015 or 0028's own scope or definition of done. A
  missing method surfaces here as an open question, not as a rewrite of
  another task's spec.
- A permanent addition to the driver's action library, or a
  `driver/README.md` change. A person deletes the code once the write-up
  is done.

## Approaches considered

### Approach A — A WebSocket plugin, the real-extension-point worked example

This approach adds the PoC as one worked example under
`driver/examples/`. A small Go `main` function dials task 0028's
`plugin.Server` as a client, and on a `signal` message `{keyIndex: 0,
name: "longPress"}` calls the bound session's tab-switch method. This
mirrors the worked-example pattern task 0013 already plans for its
Claude Code hook example.

- Good, because a WebSocket plugin is task 0013's real extension point
  for host-defined code. This checks the actual surface a driver user
  writes against, not a stand-in for it.
- Good, because it needs no new package beyond a `main.go`. It calls
  task 0013's, 0015's, and 0028's finished APIs directly. Any friction
  appears in the real interfaces, not in a stand-in.
- Bad, because it cannot compile and run until tasks 0011–0015 and 0028
  merge. It sits idle in the backlog until then.
- Bad, because a Go worked example only checks ergonomics for a user
  willing to write Go. It says nothing about a user who only edits
  config.

### Approach B — A declarative config entry, the task-0012-style worked example

This approach extends task 0012's static key-binding config with a new
action kind, `select-tab`, instead of Go code. A user names a key, a
session ID, and a tab number in the config. The config needs no handler
code.

- Good, because it checks ergonomics for the config-only user task 0012
  already serves, not only a Go-writing one.
- Good, because a config entry states the binding in the fewest words, if
  the config format can express it at all.
- Bad, because task 0013's design already routes new action kinds through
  a plugin reacting to a `signal` broadcast, not through 0012's static
  config. This approach tests a shape the existing design does not use.
- Bad, because a new config key is itself a small feature, not a
  disposable PoC. It can outlive its own throwaway purpose.

### Approach C — A CLI smoke command, the operator-level worked example

This approach adds a one-time
`macrodriver poc long-press-tab --key 0 --session <id> --tab 2` command.
A person runs it once from a terminal. The command uses the same wiring
as task 0013's `signal` subcommand.

- Good, because it checks the operator path. It shows whether a person
  can point one key at one tab by hand, with no code.
- Good, because it reuses task 0013's own CLI pattern. It costs little
  beyond the command's flag parsing.
- Bad, because it still needs the same plugin-to-session wiring that
  Approach A writes, plus a command layer on top. This is more code for
  the same ergonomics question.
- Bad, because a CLI flag set only shows that an operator can run this
  once. It does not show that a driver user's own code reads well, the
  question task 0013's worked-example pattern already targets.

## Decision

Chosen: **Approach A — a WebSocket plugin, the real-extension-point
worked example**.

Task 0013 already names a WebSocket plugin as the extension point for
new event-driven behavior. Its own worked example follows the same
shape. This choice accepts that the PoC cannot run until tasks 0011–0015
and 0028 merge. It also accepts that it says nothing about the
config-only or operator-only ergonomics that Approaches B and C target
instead.

## Design

`driver/examples/longpress_tab_poc_test.go`, tagged `//go:build poc`. It
creates a `transport.Emulator` (task 0014) and a `plugin.Server` (task
0028) wrapping it. It dials that server as a plugin client and
subscribes to `signal` messages. On `{keyIndex: 0, name: "longPress"}`,
it calls `session.Activate()`. Here, `session` is the iTerm2 session that
task 0015 binds to key 0.

The test injects a press `Event`, then a release `Event`, 600 ms apart,
into the emulator. `plugin.Server`'s own resolver (task 0013) turns that
timing into the `longPress` broadcast. The test then checks that the
plugin's handler ran. If task 0015's session interface
(`driver/session/interface.go`) lacks a method to bring a session's tab
to the front, the PoC adds the smallest one that does. This spec's Notes
section names that method once the PoC runs. The gap becomes an open
question for task 0015, not an edit to that task's own spec.

Files to change:

- `driver/examples/longpress_tab_poc_test.go` — new, disposable, build
  tag `poc`

## Definition of done

- [ ] **DoD-1** — On a branch built on top of tasks 0011–0015 and 0028's
  merged commits, `go build -tags poc ./driver/...` compiles the PoC
  with no new stand-in package. **Proof:** `go build -tags poc
  ./driver/...`
- [ ] **DoD-2** — The emulator's press and release events, 600 ms apart,
  fire the plugin's `longPress` handler exactly once. **Proof:** `go
  test -tags poc ./driver/examples/... -run
  TestPoC_LongPressFiresHandler -v`
- [ ] **DoD-3** — On a live iTerm2 window with the bound session's tab
  not frontmost, the handler brings that tab to the front. **Proof:**
  manual run logged in the commit message, `POC_ITERM=1 go test -tags
  poc ./driver/examples/... -run TestPoC_LongPressFiresHandler -v`
- [ ] **DoD-4** — This spec's Notes section states whether task 0015's
  session interface already exposed the tab-switch call, or names the
  one method the PoC adds. **Proof:** this file, Notes section
- [ ] **DoD-5** — The file is absent from the branch before it merges to
  main, or a person deletes the branch without merging. **Proof:** `git
  log main -- driver/examples/longpress_tab_poc_test.go` returns nothing

## Risks

- The PoC cannot start until tasks 0011–0015 and 0028 merge → tracked in
  the `related` field. This spec stays in `backlog` until then.
- If task 0015's interface needs a new method, that method ships inside
  this throwaway file, not inside task 0015 → DoD-4 turns that gap into
  a recorded open question, not a silent scope change.

## Open questions

- [ ] If the session interface needs a tab-switch method, does task 0015
  adopt it before it merges, or does a later task add it? — repo owner

## Notes

{Fill in once the PoC runs: whether task 0015's session interface already
exposed a tab-switch call, and what the finished plugin's handler line
looked like.}
