---
id: "0011"
title: "Own an external process as a tmux-backed session"
status: "backlog"
created: "2026-08-04"
updated: "2026-08-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0012", "0013", "0015", "0016"]
tags: ["driver", "tmux", "cli"]
---

# 0011 — Own an external process as a tmux-backed session

## Problem

The driver has no way to start a long-running host process, such as
`claude` or `codex`, and hold a handle to it that a key can bind to.
Without an owned, addressable session, tasks 0012 and 0013 have no target
to send input to or read status from.

## Goals

- `macrodriver own <command> --key N` starts `command` in a session the
  driver can address later by key index.
- A user can run `tmux attach -t macropad-key-N` and see the same session
  live, in any terminal app.
- A registry maps key index to session name, for tasks 0012 and 0013 to
  read.
- `IsAlive(key)` reports whether the owned session still exists.

## Non-goals

- Sending key-press input into the session. Task 0012 covers that.
- Turning process status into key visual state. Task 0013 covers that.
- Attaching to a terminal tab the driver did not create. Task 0015 covers
  attaching to an existing iTerm2 session.

## Approaches considered

### Approach A — Shell out to the tmux CLI

Run `tmux new-session`, `has-session`, and `kill-session` through
`os/exec`, and treat tmux as an external dependency.

- Good, because tmux already solves pty allocation, detach, and
  reattach — the driver needs no pty code.
- Good, because a session survives a driver crash and stays inspectable
  from any terminal the user opens.
- Bad, because it adds a hard runtime dependency on `tmux` being
  installed.
- Bad, because each call spawns a process and parses text output, slower
  and less structured than a library call.

### Approach B — tmux control mode (`tmux -CC`)

Hold one persistent connection to tmux's control-mode protocol instead of
one-off CLI calls, and receive pane and window events as they happen.

- Good, because one connection pushes events, instead of the driver
  polling for state.
- Good, because it avoids the repeated process-spawn cost of the CLI.
- Bad, because the control-mode line protocol needs a real parser
  (`%begin`/`%end`-framed blocks), more code for a first cut.
- Bad, because each session needs its own connection lifecycle managed by
  the driver.

### Approach C — A driver-owned pty, no tmux

Allocate a pty in Go (`creack/pty`) and manage the child process directly,
with no external dependency.

- Good, because it removes the tmux runtime dependency.
- Good, because the pty's lifecycle ties exactly to the driver process.
- Bad, because it loses detach and reattach — a driver restart kills the
  session and its scrollback, unless the driver rebuilds that itself.
- Bad, because a user can no longer open a plain terminal and attach to
  the live session, the exact gap tmux fills for free.

## Decision

Chosen: **Approach A — shell out to the tmux CLI**.

It gives detach, reattach, and pty handling with no new code, so the user
can watch and type into an owned session from any terminal. This choice
accepts a hard dependency on tmux and per-call CLI overhead; control mode
(Approach B) is the upgrade path if that overhead matters later.

## Design

A new `driver/session` package wraps tmux CLI calls: `Own(key int,
command string) error` runs `tmux new-session -d -s macropad-key-<N>
<command>`. An in-memory registry maps key index to session name.
`IsAlive(key int) bool` calls `tmux has-session`. A new `own` subcommand
exposes this on the command line.

Files to change:

- `driver/go.mod` — new, initializes the Go module
- `driver/session/tmux.go` — new, wraps tmux CLI calls
- `driver/session/registry.go` — new, key-to-session-name map
- `driver/cmd/own.go` — new, the `own` subcommand
- `driver/README.md` — document the `own` subcommand and the tmux
  dependency

## Definition of done

- [ ] **DoD-1** — `macrodriver own "sleep 60" --key 0` creates a session
  reachable by `tmux attach -t macropad-key-0`. **Proof:** manual run,
  then `tmux has-session -t macropad-key-0` exits `0`.
- [ ] **DoD-2** — Calling `own` twice for the same key returns an error
  instead of a second session. **Proof:** `go test ./driver/session/...
  -run TestOwn_DuplicateKey`
- [ ] **DoD-3** — With `tmux` absent from `$PATH`, `own` returns a named
  error, not a panic. **Proof:** `go test ./driver/session/... -run
  TestOwn_TmuxMissing`
- [ ] **DoD-4** — `IsAlive` returns `false` once the session is killed.
  **Proof:** `go test ./driver/session/... -run TestIsAlive_SessionGone`
- [ ] **DoD-5** — `driver/README.md` documents the `own` subcommand and
  the tmux dependency. **Proof:** `driver/README.md`
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- A host without tmux blocks the whole feature → DoD-3 fails loud and
  early; `driver/README.md` lists tmux as a prerequisite.
- Per-call CLI overhead may not scale under heavy use → acceptable for
  v1; Approach B is the documented escape hatch.
