---
id: "0050"
title: "Find why macropadd drops setKeyState messages, and fix it"
status: "ongoing"
created: "2026-10-10"
updated: "2026-10-10"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0050-daemon-drops-setkeystate-messages"
related: ["0028", "0030", "0036"]
tags: ["driver", "plugin", "daemon", "reliability"]
---

# 0050 — Find why macropadd drops setKeyState messages, and fix it

## Problem

A client of the plugin WebSocket (`ws://127.0.0.1:8765`) sends `setKeyState`.
About half of the messages have no effect. The server sends no echo, does not
change `keys.json`, logs nothing, and keeps the connection open. A client
cannot tell a drop from a success. This hurts every plugin and the key-state
page (task 0036).

## Goals

- Every `setKeyState` that a client sends is applied, or it fails with a
  logged reason.
- The cause of the drops is known, and the notes in this spec record it.

## Non-goals

- A new message type or a change to the wire protocol.
- Speed of the board. A glyph still takes about 3 s to cross.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Measure first, then fix the named cause

Add one log line for each message the server reads and each device send, with
the time it took. Add a repro command. Run it on the board. Fix the cause that
the log names.

- Good, because the cause is unknown, and this approach never fixes a guess.
- Good, because the log stays in the daemon and finds the next drop too.
- Bad, because it needs a board run before any fix, so it takes two steps.
- Bad, because the fix is not known now, so the size of the task is not known.

### Approach B — Split the client from the board

`readPump` stores the state, echoes it, and queues the board write. One writer
goroutine sends to the board and keeps only the newest state for each key.
`Reconnecting` no longer holds `sendMu` while it writes to the board.

- Good, because a client message never waits for a board write.
- Good, because a burst of changes to one key sends only the last one.
- Bad, because it changes `Reconnecting` and `Server`, and the tests of both.
- Bad, because it fixes a guess: if the cause is elsewhere, the drops stay.

### Approach C — Make the client confirm and retry

The tools that send (`websocat` loops, the key-state page) wait for the echo
and send again after a timeout. No daemon change.

- Good, because it needs no change to `driver/plugin` or `driver/transport`.
- Good, because it works with any daemon version.
- Bad, because a retry can apply an old state after a new one.
- Bad, because every plugin author must write the retry, and the cause stays.

## Decision

Chosen: **Approach A — Measure first, then fix the named cause**.

The cause is unknown, and B and C are each a guess. A costs one extra board
run. The task may end as B if the log shows that `sendMu` is the cause.

## Design

Facts from the code that guide the first look:

- `Server.readPump` ignores the error of `SendKeyState`. It skips a bad
  message with no log line (`driver/plugin/server.go`).
- `Reconnecting.SendKeyState` holds `sendMu` for the whole board write. So does
  `connect`, which replays each glyph (about 3 s each) after a reconnect.
- `Device.SendKeyState` calls `hid.Write` with no timeout
  (`driver/transport/device.go`).

Files to change:

- `driver/plugin/server.go` — log each `setKeyState`: read, applied, error.
- `driver/transport/reconnect.go` — log how long a send waits for `sendMu`.
- `driver/plugin/server_test.go` — a test for the named cause.
- `driver/README.md` — record the cause and the behavior.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — A repro command sends 50 `setKeyState` messages, one for each
  connection, 5 s apart, and prints `applied N/50` from `keys.json`. The
  baseline run on the board is in Notes. **Proof:** the command, and the Notes
  line
- [ ] **DoD-2** — The daemon logs one line for each `setKeyState` with the
  key, the wait for `sendMu`, and the time of the board write. **Proof:**
  `macropadd` output during the repro run
- [ ] **DoD-3** — The cause is named in Notes with the log lines that show it.
  **Proof:** the Notes section of this spec
- [ ] **DoD-4** — After the fix, three repro runs print `applied 50/50`.
  **Proof:** the three outputs in Notes
- [ ] **DoD-5** — A message that cannot be applied (bad color, or a device
  error) writes one log line with the reason. **Proof:** `go test ./driver/plugin/...`
- [ ] **DoD-6** — A test reproduces the named cause. It fails on `main` and
  passes on the branch. **Proof:** `go test ./driver/plugin/... ./driver/transport/...`
- [ ] **DoD-7** — `driver/README.md` records the cause and the behavior.
  **Proof:** `driver/README.md`
- [ ] **DoD-8** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- The log changes the timing and hides the drop → log after the work, not before.
- The drop does not show on the Emulator → run the repro on the real board.
- The fix is approach B and grows → split it into a new task, and close this one
  with the cause named.

## Open questions

- [ ] Does a drop happen with no client reading `keys.json`? — owner, in the
  baseline run
- [ ] Does the board stall `hid.Write` while it pushes frames or saves flash?
  — owner, from the write times in the log

## Notes

Seen on 2026-10-10 with the real board:

- A new connection gets the replay of the saved states. A `setKeyState` sent
  2 to 3 s later was often not applied.
- A send 8 to 15 s after the last change worked more often. A send within a few
  seconds of a change usually failed. `websocat` once reported `I/O failure`.
- `emojiId` 0 deleted `glyph-N.bin`, as `driver/README.md` says. That is not a
  drop.
