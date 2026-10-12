---
id: "0050"
title: "Find why macropadd drops setKeyState messages, and fix it"
status: "complete"
created: "2026-10-10"
updated: "2026-10-11"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/52"
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
  - Not confirmed: `cmd/setkeyrepro` is written and tested against the emulator. The baseline run on the board is not done.
- [ ] **DoD-2** — The daemon logs one line for each `setKeyState` with the
  key, the wait for `sendMu`, and the time of the board write. **Proof:**
  `macropadd` output during the repro run
  - Not confirmed: the log lines exist and are tested. No `macropadd` output from a repro run on the board is recorded.
- [ ] **DoD-3** — The cause is named in Notes with the log lines that show it.
  **Proof:** the Notes section of this spec
  - Not confirmed: the cause is not named. It needs the board run from DoD-1.
- [ ] **DoD-4** — After the fix, three repro runs print `applied 50/50`.
  **Proof:** the three outputs in Notes
  - Not confirmed: no fix was made, because no cause is named.
- [x] **DoD-5** — A message that cannot be applied (bad color, or a device
  error) writes one log line with the reason. **Proof:** `go test ./driver/plugin/...`
- [ ] **DoD-6** — A test reproduces the named cause. It fails on `main` and
  passes on the branch. **Proof:** `go test ./driver/plugin/... ./driver/transport/...`
  - Not confirmed: no test of the named cause exists, because no cause is named.
- [ ] **DoD-7** — `driver/README.md` records the cause and the behavior.
  **Proof:** `driver/README.md`
  - Not confirmed: `driver/README.md` records the log lines and the repro command, but not a cause.
- [x] **DoD-8** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Resolution

Closed as **cannot reproduce** on 2026-10-11. About 300 well-formed
`setKeyState` messages on the real board, at spacings from 100 ms to 5 s, were
all read and applied (see Notes). No cause for the first report is named, so
DoD-3, DoD-4, DoD-6 and DoD-7 are not ticked and are not met. DoD-1 and DoD-2
are met except that the board baseline was run by the implementer, not
recorded in the form the item names.

What the task left behind:

- Logging for each `setKeyState` (read, `sendMu` wait, board write, applied or
  failed), and for a message the server cannot decode or route.
- `cmd/setkeyrepro`, which counts the messages that reach `keys.json`.
- A measured finding for a follow-up: a burst closer than about 200 ms builds
  a queue behind `sendMu`, because each board write then takes about 155 ms.
  The last of 100 messages sent 100 ms apart waited 5 s. A new task can coalesce
  the writes. Reopen this task if a drop shows in the log with a `read` line and
  no `applied` line, or with no `read` line from a client that connected.

## Risks

- The log changes the timing and hides the drop → log after the work, not before.
- The drop does not show on the Emulator → run the repro on the real board.
- The fix is approach B and grows → split it into a new task, and close this one
  with the cause named.

## Open questions

- [x] Does a drop happen with no client reading `keys.json`? — the repro reads
  it every 100 ms and saw no drop; not tested with no reader (see Notes)
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

Board runs on 2026-10-11 (real board, `macropadd` from this branch, key 4):

- `setkeyrepro` with the defaults (50 messages, 5 s apart): `applied 50/50`.
  Each message showed in `keys.json` after about 102 ms. In the daemon log,
  `sendMu wait=0s` and `board write` was 2 to 9 ms for every message.
- `setkeyrepro -interval 1s`: `applied 50/50`.
- `setkeyrepro -interval 2s -count 20`, started while the daemon replayed 6
  saved keys after its start: `applied 20/20`. The first message waited 417 ms
  for `sendMu` and showed after 513 ms. Later messages waited 0 s.
- So the daemon does not lose a well-formed `setKeyState` that arrives on its
  own connection, at idle, at 1 s spacing, or during a replay. The cause is
  not named. DoD-1 has its baseline (`applied 50/50`); DoD-3, DoD-4, DoD-6 and
  DoD-7 stay open.
- The repro measures the daemon only: `keys.json` is written before the board
  write, so it cannot show a board that ignores a write that returned no error.
- Two paths still dropped a message with no log. Both now log (test
  `TestServer_LogsMessagesItCannotDecodeOrRoute`):
  - `ReadJSON` fails on a message it cannot decode (`color` as a string such as
    `"0xF800"`, `color` above 65535, `keyIndex` above 255). `readPump` returns
    and the connection closes, with no log. A long-lived client such as
    `websocat` then reports `I/O failure` on its next send.
  - A message with an unknown `kind` (for example `set_key_state`) matched no
    `case`, so the connection stayed open and nothing happened.
  These match "no echo, no change, no log", but nothing yet shows that the
  failing clients send such messages.
- The repro replaced the saved state of key 4, and `emojiId` 0 deleted
  `glyph-4.bin`. The state directory was restored from a copy afterward.

Burst runs on 2026-10-11 (same board, key 4; counts are from the daemon log,
because the repro's 100 ms poll cannot judge a message at a 100 ms spacing and
printed `applied 5/100` for that reason):

- 100 messages, 100 ms apart: the daemon logged 100 `read`, 100 `applied`, and
  no `failed`, `dropped`, `closing`, or `unknown` line. `keys.json` ended on
  the last color (0x0163). No message was lost.
- Every `board write` took 150 to 160 ms, not 2 to 9 ms. Each message waited on
  `sendMu` about 50 ms longer than the one before it. The last one waited
  5.0 s, and applied about 10 s after the run started, 5 s after the last
  send ended.
- 20 messages at 200 ms, 400 ms, and 700 ms apart: all `read` and `applied`,
  `board write` about 5 to 6 ms, no backlog. The change is between 100 ms and
  200 ms.
- So a burst builds a queue behind `sendMu`: while the board writes take about
  155 ms, each message waits for all the ones before it. A client that reads
  `keys.json`, or watches the board, a few seconds after it sends sees no
  effect yet. That fits "a send within a few seconds of a change usually
  failed" in the first Notes. It is a delay, not a loss.
- Not shown: why a write takes 155 ms after a write that came less than about
  200 ms before it, and 5 ms after one that came more. It may be a minimum
  time that the board needs for each update. The firmware has not been read.
