---
id: "0016"
title: "Parse OSC 133 shell-integration sequences for command lifecycle events"
status: "backlog"
created: "2026-08-06"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0011", "0013", "0028"]
tags: ["driver", "protocol", "terminal"]
---

# 0016 — Parse OSC 133 shell-integration sequences for command lifecycle events

## Problem

Task 0013's only status source is Claude Code's own hooks, which needs
that tool's cooperation. Most shells already emit OSC 133 escape
sequences (prompt shown, command started, command finished with exit
code) through shell-integration scripts such as bash-preexec, zsh hooks,
fish, or starship — a tool-agnostic signal the driver does not read yet.

## Goals

- A parser reads a byte stream and recognizes OSC 133 prompt, command-
  start, and command-finished sequences.
- Recognized events extend task 0013's `signal` vocabulary: prompt shown
  broadcasts `processWaiting`, command start broadcasts `processRunning`,
  command finish broadcasts `processDone` (exit 0) or `processExited`
  (nonzero).
- The parser reads task 0011's tmux `pipe-pane` output, so an owned
  session gets status with no per-tool hook config.
- A sequence split across two buffered reads still parses correctly.

## Non-goals

- Reading an iTerm2 session's output. Task 0015 decides whether it reuses
  this parser or iTerm2's own shell-integration notifications.
- Detecting anything inside one command's run, such as a mid-turn
  permission prompt. OSC 133 only marks command boundaries; task 0013's
  hook path still owns that.
- Enabling shell integration on the user's shell. This task assumes it is
  already configured and documents how to check.

## Approaches considered

### Approach A — A byte-stream OSC 133 state machine

Scan raw bytes from `pipe-pane` for the six OSC 133 codes and emit a
structured event per match.

- Good, because it needs no new shell configuration for a user who
  already runs shell integration.
- Good, because it is decoupled from tmux and from any terminal app — the
  same parser could later read an iTerm2 screen stream too.
- Bad, because OSC 133 adoption varies — a shell with no integration
  configured emits nothing, so this produces silent zero events.
- Bad, because a sequence can split across buffered reads at any byte
  boundary, real complexity for what looks like a small parser.

### Approach B — Reuse a full terminal-emulation (VT100/xterm) library

Adopt an existing Go terminal-emulator package instead of writing a
bespoke scanner.

- Good, because a full emulator already handles escape-sequence framing
  correctly, including edge cases this task would otherwise solve itself.
- Good, because it could double as the base for a screen-reading fallback
  later.
- Bad, because full VT100 emulation is a much larger dependency than the
  six codes OSC 133 needs — it builds and maintains virtual screen state
  this task does not use.
- Bad, because these libraries expose matched escapes for rendering, not
  as structured events — this task still bolts an event layer on top.

### Approach C — A shell wrapper that reports status itself, no parsing

Route every command through a wrapper script that writes start/exit
status directly, instead of reading OSC 133 at all.

- Good, because it needs no escape-sequence parsing.
- Good, because it works even without OSC 133 integration configured.
- Bad, because it needs the user to rewrite aliases or `PATH` to route
  every command through the wrapper, a bigger ask than "already has shell
  integration."
- Bad, because it loses the prompt-shown, "waiting on you" event
  entirely — a wrapper only sees commands it wraps, not idle-prompt
  state.

## Decision

Chosen: **Approach A — a byte-stream OSC 133 state machine**.

It needs no new configuration for users who already run shell
integration, and it stays decoupled from any one terminal, matching the
fact that OSC 133 is the one part of this problem with a real
cross-terminal convention. This choice accepts silent zero events for
shells without integration, and accepts the split-sequence handling a
from-scratch scanner needs.

## Design

A new `driver/osc133` package implements `NewScanner(io.Reader) *Scanner`
with `Scan() (Event, bool)`, returning `PromptShown`, `CommandStarted`, or
`CommandFinished{ExitCode int}`. `driver/session/tmux.go` (task 0011)
feeds `pipe-pane` output through the scanner, and maps each scanner event
to a `processWaiting`, `processRunning`, `processDone`, or
`processExited` name, broadcast through `plugin.Server.Broadcast` (task
0013's `signal` message kind). `processRunning` joins task 0013's
recognized names; it needs no code change there, since `signal` carries
an open string, not a fixed Go enum.

Files to change:

- `driver/osc133/scanner.go` — new, the OSC 133 state machine
- `driver/osc133/scanner_test.go` — new
- `driver/session/tmux.go` — wire `pipe-pane` output through the scanner
  and broadcast each event as a `signal`
- `driver/README.md` — document OSC 133 as the tmux path's status
  source, and `processRunning` in the recognized `signal` names

## Definition of done

- [ ] **DoD-1** — Feeding `\x1b]133;A\x07` yields a `PromptShown` event.
  **Proof:** `go test ./driver/osc133/... -run TestScanner_PromptShown`
- [ ] **DoD-2** — Feeding `\x1b]133;D;1\x07` yields
  `CommandFinished{ExitCode: 1}`. **Proof:** `go test ./driver/osc133/...
  -run TestScanner_ExitCode`
- [ ] **DoD-3** — A sequence split across two `Write` calls still parses.
  **Proof:** `go test ./driver/osc133/... -run
  TestScanner_SplitAcrossReads`
- [ ] **DoD-4** — A stream with no OSC 133 sequences produces zero
  events, not an error. **Proof:** `go test ./driver/osc133/... -run
  TestScanner_NoIntegration`
- [ ] **DoD-5** — A command-start sequence on a tmux-owned session (task
  0011) broadcasts a `signal` named `processRunning` for that key, seen
  by a connected test client. **Proof:** `go test ./driver/session/...
  -run TestTmux_OSC133BroadcastsRunning`
- [ ] **DoD-6** — `driver/README.md` documents OSC 133 as the tmux path's
  status source and how to check shell integration is active. **Proof:**
  `driver/README.md`
- [ ] **DoD-7** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- No error surfaces when shell integration is absent → DoD-6 documents
  this as a prerequisite to check by hand.
- Split-sequence handling is the classic bug source for byte scanners →
  DoD-3 exists specifically to catch it.
