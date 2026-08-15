---
id: "0013"
title: "Host action API — setEmoji, setState, and a signal broadcast"
status: "backlog"
created: "2026-08-04"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0011", "0012", "0015", "0016", "0028"]
tags: ["driver", "api", "protocol"]
---

# 0013 — Host action API — setEmoji, setState, and a signal broadcast

## Problem

Nothing turns an owned session's status (running, waiting on input, done,
failed) into visual feedback on the key. Nothing resolves a raw
press/release pair into a click pattern, either — no single press, double
press, or long press exists yet, only the wire protocol's raw events.

## Goals

- `SetEmoji(key, id)` and `SetState(key, "Alert", color?)` are Go helpers
  that build a `setKeyState` message and send it over a plugin
  connection, for any Go-based plugin to call.
- Raw press/release pairs resolve to `singlePress`, `doublePress`, and
  `longPress`, and broadcast as task 0028's `signal` message.
- A `macrodriver signal` subcommand, called from a Claude Code hook,
  broadcasts `processWaiting` on a `Notification` hook event and
  `processDone` on a `Stop` hook event.
- A worked example plugin reacts to those broadcasts with `SetState`
  calls (amber blink while waiting, solid green when done).

## Non-goals

- An in-process, compiled-into-the-daemon handler registry. Every
  reaction to a `signal` runs in its own plugin process, over task
  0028's WebSocket API — see Approach D and its Decision.
- Codex CLI hook wiring. Its notify-on-event mechanism needs its own
  confirmation first; a later task adds it. The signal broadcast built
  here is tool-agnostic.
- Reading pane text as a status fallback for tools with no hooks. Later
  task.
- Replacing task 0012's static config. A plugin may call `SendKeys`
  itself, but 0012's bindings keep working unchanged.

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

### Approach D — Reuse task 0028's plugin WebSocket server, broadcast a `signal` kind

Task 0028 already runs `plugin.Server`, bound to `127.0.0.1`, with a
tested client cap and bounded per-client queues. `macrodriver signal`
becomes a WebSocket client that sends a new `signal` message kind; the
server broadcasts it to every connected client, exactly like an `event`
from the device — no in-process handler runs inside the daemon.

- Good, because the daemon then runs one local server, not two. This
  matches task 0028's own open question about merging the two.
- Good, because it keeps one extension surface — a plugin process — for
  every kind of driver customization, matching how Stream Deck's own
  plugin model has no in-process equivalent to build against.
- Good, because the client cap, the `localhost` bind, and the bounded
  queues already exist and are tested — `signal` adds no new bounding
  logic.
- Bad, because 0013 cannot start before 0028 reaches `main` — an
  ordering constraint none of the first three approaches carry.
- Bad, because a one-shot `signal` with no plugin currently connected
  and listening has no effect — broadcasting is not a queue.

## Decision

Chosen: **Approach D — reuse task 0028's plugin WebSocket server, broadcast, not dispatch.**

Approach A's Unix socket and task 0028's WebSocket server both solve the
same problem: an external process reaching the one daemon that owns the
device. Running both would leave the daemon with two local IPC
mechanisms to secure and test, for one problem. Approach D retires
Approach A instead, and folds `signal` into 0028's protocol as a
broadcast, not as a call into a handler compiled into the daemon. This
keeps every host-defined reaction — Claude Code hook status, a resolved
click pattern, a future terminal integration — in a plugin process, so
that code stays portable to another plugin host later, not tied to this
daemon's own binary. This choice accepts Approach D's ordering cost, and
that a signal with no plugin listening has no effect. Approach A's
dropped-signal-while-down risk, below, still applies unchanged.

## Design

`driver/plugin/protocol.go` gains a third `MessageKind`, `signal`,
carrying `{keyIndex byte, name string}`. `driver/plugin/server.go`'s
`readPump` broadcasts a `signal` message to every other connected client
the same way `dispatchLoop` broadcasts an `event` — through one shared,
exported `Server.Broadcast(Message)` method, so an in-process observer
(task 0016's OSC 133 scanner, task 0015's iTerm2 bridge) calls it
directly, with no need to dial its own daemon over a socket.

A small resolver inside `driver/plugin/server.go` watches each key's raw
`event` press/release pairs and calls `Broadcast` with a `signal` named
`singlePress`, `doublePress`, or `longPress`, using fixed timing
thresholds. `driver/api/state.go` defines `SetEmoji(key int, id byte)
error` and `SetState(key int, state string, color *uint16) error`: Go
helpers a plugin calls to translate a named state such as `"Alert"` into
a color, blink, and emoji, then send it as `setKeyState`. `macrodriver
signal` opens a short-lived plugin connection, sends one `signal`
message, and exits. `driver/examples/claude-status/main.go` is a
reference plugin: it stays connected, and reacts to `signal` messages
named `processWaiting` and `processDone` by calling `SetState`.
`driver/README.md` gains the signal vocabulary and a worked Claude Code
`settings.json` hook example.

Files to change:

- `driver/plugin/protocol.go` — add the `signal` message kind
- `driver/plugin/server.go` — export `Broadcast`; resolve raw
  press/release pairs into `singlePress` / `doublePress` / `longPress`
  signals
- `driver/api/state.go` — new, `SetEmoji`, `SetState`
- `driver/cmd/signal.go` — new, `signal` subcommand, a WebSocket client
- `driver/examples/claude-status/main.go` — new, the reference plugin
- `driver/README.md` — document the API, the `signal` vocabulary, and
  the hook example

## Definition of done

- [ ] **DoD-1** — `SetState(0, "Alert", nil)` builds a Key state HID
  message matching `docs/wire-protocol.md`'s byte layout for key 0.
  **Proof:** `go test ./driver/api/... -run TestSetState_Alert`
- [ ] **DoD-2** — A press and a release event 600 ms apart on key 0
  broadcast a `signal` named `longPress` for key 0. **Proof:** `go test
  ./driver/plugin/... -run TestServer_ResolvesLongPress`
- [ ] **DoD-3** — `macrodriver signal --key 0 --event stop` reaching a
  running driver with a connected test client broadcasts `{keyIndex: 0,
  name: "processDone"}` to that client. **Proof:** `go test
  ./driver/plugin/... -run TestServer_SignalBroadcast`
- [ ] **DoD-4** — A `signal` broadcast with zero plugins connected does
  not error or panic the driver process. **Proof:** `go test
  ./driver/plugin/... -run TestServer_SignalNoListener`
- [ ] **DoD-5** — The documented Claude Code hook example, run against a
  real `claude` session owned through tasks 0011 and 0012, with
  `driver/examples/claude-status` running, turns the key amber on a
  permission prompt. **Proof:** manual run, logged in the PR description
- [ ] **DoD-6** — `driver/README.md` documents `SetEmoji`, `SetState`,
  the `signal` message, and its recognized names. **Proof:**
  `driver/README.md`
- [ ] **DoD-7** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- A signal sent while the driver is down, or with no plugin listening,
  is dropped → documented as a known v1 limitation, not solved here.
- `SetState`'s named states are a closed set; a new one needs a driver
  code change → acceptable, since the wire protocol's fixed-width
  messages (task 0002) have no room for open-ended state names either.
- Fixed timing thresholds misclassify a click pattern on a slow or fast
  press → no proof item covers this directly; DoD-2 only checks one
  worked timing, not the threshold boundaries.
