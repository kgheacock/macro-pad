---
id: "0012"
title: "Send resolved key events into an owned session"
status: "backlog"
created: "2026-08-04"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0011", "0013", "0016"]
tags: ["driver", "actions", "tmux"]
---

# 0012 — Send resolved key events into an owned session

## Problem

`driver/README.md` says the driver "maps resolved events to host-defined
actions," but no action target exists for a session task 0011 owns. An
owned session cannot yet receive a single keystroke from a key press.

## Goals

- A per-key config binds a resolved event (single press, double press,
  long press) to a literal string sent into that key's owned session.
- Sending to a key with no owned session returns an error, not a silent
  no-op.
- Existing event resolution from the press/release wire message (task
  0002) stays unchanged; this task only adds a new action target.

## Non-goals

- A general API for host code to subscribe to events. Task 0013's plugin
  `signal` broadcast covers that.
- Choosing what to send based on process output or state. This task's
  bindings are fixed at config time.
- Session creation or lifecycle. Task 0011 covers that.

## Approaches considered

### Approach A — Static per-key config, dispatched through tmux send-keys

A config lists, per key and event type, the literal keys to send. A
dispatcher calls `tmux send-keys -t <session> <keys>`.

- Good, because it needs one new function, `SendKeys(key, event, keys)`,
  built on task 0011's registry.
- Good, because fixed strings per event are exactly what "single press
  approves, long press interrupts" needs.
- Bad, because tmux's send-keys syntax mixes literal text and named keys
  (`Escape`, `Enter`) — a wrong config sends the wrong bytes.
- Bad, because it does not scale to logic that depends on process state;
  that need still routes through a plugin subscribed to task 0013's
  `signal` broadcast.

### Approach B — Embed a scripting language for key actions

Add a small scripting engine (Lua, or a Go expression evaluator) so a
key's action can run logic before choosing what to send.

- Good, because one key could vary its output by prior input or process
  state.
- Good, because it needs no future rework to add conditional logic.
- Bad, because a scripting engine is a large new dependency for a
  question this task has not yet answered: whether static bindings fall
  short.
- Bad, because it adds an attack surface (arbitrary script execution) with
  no proven need yet.

### Approach C — Write bytes straight to the pane's pty, skip tmux send-keys

Bypass tmux's command layer and write to the pane's underlying pty device
file.

- Good, because it skips a subprocess spawn per keystroke.
- Good, because it avoids send-keys' text-escaping rules.
- Bad, because tmux does not publish a stable path to a pane's pty — this
  depends on undocumented internals that can break across versions.
- Bad, because it loses tmux's own key-name handling (`Escape`, `Ctrl-C`),
  which then needs a hand-built reimplementation.

## Decision

Chosen: **Approach A — static per-key config through tmux send-keys**.

It proves that a resolved press event can drive an owned session, using
only task 0011's registry. This choice accepts that actions are fixed at
config time; a plugin reacting to task 0013's `signal` broadcast is
where conditional logic belongs.

## Design

`driver/session` gains `SendKeys(key int, keys ...string) error`, a thin
wrapper over `tmux send-keys -t macropad-key-<N> <keys...>`. A new
`driver/action` package loads a per-key YAML config (key, event type,
keys to send) and, on each resolved event from task 0002's press/release
handling, looks up a matching binding and calls `SendKeys`.

Files to change:

- `driver/session/tmux.go` — add `SendKeys`
- `driver/action/config.go` — new, per-key event-to-keys config
- `driver/action/dispatch.go` — new, resolved event to `SendKeys` call
- `driver/README.md` — document the config format, with one example

## Definition of done

- [ ] **DoD-1** — A key configured with `on: single_press, send: ["y",
  "Enter"]` writes `y` and Enter into the owned session's pane. **Proof:**
  `tmux capture-pane -p -t macropad-key-0` after a simulated event, in
  `go test ./driver/action/... -run TestDispatch_SendsKeys`
- [ ] **DoD-2** — An event type with no configured binding sends nothing.
  **Proof:** `go test ./driver/action/... -run TestDispatch_NoBinding`
- [ ] **DoD-3** — Dispatch to a key with no owned session returns an
  error, not a panic. **Proof:** `go test ./driver/action/... -run
  TestDispatch_NoSession`
- [ ] **DoD-4** — Config load rejects an unknown event-type name. **Proof:**
  `go test ./driver/action/... -run TestConfig_UnknownEventType`
- [ ] **DoD-5** — `driver/README.md` documents the config format with one
  worked example. **Proof:** `driver/README.md`
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- tmux send-keys mixes literal text and named keys, so a wrong config
  sends the wrong bytes → DoD-4's validation and the README's worked
  example reduce this.
