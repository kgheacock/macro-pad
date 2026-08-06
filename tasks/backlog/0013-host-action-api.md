---
id: "0013"
title: "Host action API — setEmoji, setState, useAction"
status: "backlog"
created: "2026-08-04"
updated: "2026-08-06"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0011", "0012", "0015", "0016"]
tags: ["driver", "api", "protocol"]
---

# 0013 — Host action API — setEmoji, setState, useAction

## Problem

Nothing turns an owned session's status (running, waiting on input, done,
failed) into visual feedback on the key. Host-defined code also has no
API to react to an event or set key state directly — task 0012's config
only sends fixed strings.

## Goals

- `SetEmoji(key, id)` and `SetState(key, "Alert", color?)` build and send
  the Key state HID message defined in `docs/wire-protocol.md`.
- `UseAction(key, eventMask, handler)` registers a handler for an event
  mask, such as `LONG_PRESS`, `AUDIO_RECEIVED`, or a new
  `PROCESS_WAITING` / `PROCESS_DONE` / `PROCESS_EXITED` for an owned
  session.
- A `macrodriver signal` subcommand, called from a Claude Code hook,
  raises `PROCESS_WAITING` on a `Notification` hook event and
  `PROCESS_DONE` on a `Stop` hook event.
- A worked example binds those hook-driven events to `SetState` calls
  (amber blink while waiting, solid green when done).

## Non-goals

- Codex CLI hook wiring. Its notify-on-event mechanism needs its own
  confirmation first; a later task adds it. The signal endpoint built
  here is tool-agnostic.
- Reading pane text as a status fallback for tools with no hooks. Later
  task.
- Replacing task 0012's static config. A `UseAction` handler may call
  `SendKeys` itself, but 0012's bindings keep working unchanged.

## Approaches considered

### Approach A — Unix socket server inside the driver, `signal` as its client

The running driver process holds a Unix socket. `macrodriver signal
--key N --event stop` connects and sends one message.

- Good, because the one long-lived driver process holds the registries
  from tasks 0011 and 0012, so a handler can call `SetState` or
  `SendKeys` against the same state.
- Good, because a Unix socket is scoped to the local user's filesystem
  permissions, no network exposure.
- Bad, because it needs a server loop and a wire format for the socket,
  new surface to test.
- Bad, because a signal sent while the driver is not running is dropped,
  with no queue.

### Approach B — File-drop spool, driver polls a directory

A hook writes a small file to a per-key spool directory. The driver polls
the directory for new files.

- Good, because a hook becomes a one-line `touch` or `echo`, no client
  binary to build.
- Good, because a signal written to disk survives a driver restart until
  the driver reads it.
- Bad, because polling ties responsiveness to the poll interval, or a
  fast poll wastes CPU.
- Bad, because it needs its own cleanup story: stale files, ordering, and
  duplicate reads.

### Approach C — Localhost HTTP endpoint instead of a Unix socket

The driver listens on a localhost TCP port. `macrodriver signal` becomes
a plain HTTP call, or a hook can call `curl` directly.

- Good, because any hook mechanism that can shell out to `curl` works,
  with no custom client.
- Good, because it is easy to test by hand and to extend, such as adding
  a status page on the same port.
- Bad, because it opens a TCP port on localhost, a wider blast radius
  than a Unix socket.
- Bad, because it needs port-conflict handling that a fixed-path socket
  avoids.

## Decision

Chosen: **Approach A — Unix socket server, with `signal` as its client**.

It keeps mutable state in the one driver process that already owns tasks
0011 and 0012's registries, and avoids opening a network port for a
local, single-user integration. This choice accepts that a signal fired
while the driver is not running is dropped — acceptable, since a Claude
Code session normally starts after `macrodriver own`.

## Design

`driver/api` defines `SetEmoji(key int, id byte) error` and `SetState(key
int, state string, color *uint16) error`, which translate a named state
such as `"Alert"` into a color, blink, and emoji, then send the Key state
HID message from task 0002. `UseAction(key int, mask EventMask, handler
func(Event))` registers a handler; `EventMask` extends existing event
types with `PROCESS_WAITING`, `PROCESS_DONE`, and `PROCESS_EXITED`.
`driver/signal/server.go` runs the Unix socket server; `macrodriver
signal` is its client. `driver/README.md` gains a worked Claude Code
`settings.json` hook example.

Files to change:

- `driver/api/state.go` — new, `SetEmoji`, `SetState`
- `driver/api/action.go` — new, `UseAction`, `EventMask`
- `driver/signal/server.go` — new, Unix socket server
- `driver/cmd/signal.go` — new, `signal` subcommand
- `driver/README.md` — document the API, the signal endpoint, and the
  hook example

## Definition of done

- [ ] **DoD-1** — `SetState(0, "Alert", nil)` builds a Key state HID
  message matching `docs/wire-protocol.md`'s byte layout for key 0.
  **Proof:** `go test ./driver/api/... -run TestSetState_Alert`
- [ ] **DoD-2** — A `UseAction` handler for `PROCESS_DONE` on key 0 runs
  when `macrodriver signal --key 0 --event stop` reaches a running
  driver. **Proof:** `go test ./driver/signal/... -run
  TestSignal_TriggersHandler`
- [ ] **DoD-3** — A signal for a key with no registered handler does not
  error the driver process. **Proof:** `go test ./driver/signal/... -run
  TestSignal_NoHandler`
- [ ] **DoD-4** — The documented Claude Code hook example, run against a
  real `claude` session owned through tasks 0011 and 0012, raises
  `PROCESS_WAITING` on a permission prompt. **Proof:** manual run, logged
  in the PR description
- [ ] **DoD-5** — `driver/README.md` documents `SetEmoji`, `SetState`,
  `UseAction`, and the event mask values. **Proof:** `driver/README.md`
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- A signal sent while the driver is down is dropped → documented as a
  known v1 limitation, not solved here.
- `SetState`'s named states are a closed set; a new one needs a driver
  code change → acceptable, since the wire protocol's fixed-width
  messages (task 0002) have no room for open-ended state names either.
