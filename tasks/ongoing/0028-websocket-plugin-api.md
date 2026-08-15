---
id: "0028"
title: "Add a local WebSocket API for third-party macro-pad plugins"
status: "ongoing"
created: "2026-08-15"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/23"
branch: "0028-websocket-plugin-api"
related: ["0002", "0011", "0012", "0013", "0020", "0021", "0025"]
tags: ["driver", "plugin", "api"]
---

# 0028 — Add a local WebSocket API for third-party macro-pad plugins

## Problem

Nothing stops two processes from opening the same `transport.Device` at
once. Third-party automation has no way to react to a key press. It also
cannot set a key's color and emoji without opening the raw HID and CDC
connection itself.

## Goals

- One long-running daemon holds the only `transport.Device` connection.
  Every other process reaches the board through it.
- A local WebSocket server sends each decoded `Event` to every connected
  client, as JSON.
- A client sets one key's color, emoji, and blink state over the same
  connection.
- A slow or stalled client cannot grow the daemon's memory past a fixed
  bound, and cannot block delivery to other clients.
- The server accepts connections from `localhost` only, and caps the
  number of connected clients.

## Non-goals

- Click-pattern resolution (single, double, long press). Tasks
  0011–0013 own that.
- Audio chunks to a client. If plugins need binary streaming, a later
  task adds it.
- Auth beyond the `localhost` bind and the client cap. Out of scope for
  a single-user desktop tool.
- Replacing task 0013's Unix-socket signal server. That server takes
  hook events into the driver. This task sends device events out.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Local WebSocket server, bounded per-client queues

The daemon runs a WebSocket server bound to `127.0.0.1`. Each client
gets a fixed-size queue. A full queue drops the next message instead of
blocking. Repeated drops disconnect the client.

- Good, because a WebSocket needs no OS-specific client code — Python,
  Node, a shell script, or a browser tab can all connect with a
  standard library.
- Good, because a fixed queue size and a client cap bound total memory,
  no matter how many clients connect or how slowly they read.
- Bad, because it opens a TCP port on `localhost` — the same wider
  blast radius task 0013's Approach C rejected for a similar local
  integration.
- Bad, because it adds a WebSocket library. The daemon needs no
  dependency beyond hidapi and go.bug.st/serial today.

### Approach B — Unix domain socket, matching task 0013's decision

The same JSON messages travel over a Unix domain socket at a fixed
path, instead of a TCP port. This mirrors the mechanism task 0013
already chose for its inbound signal server.

- Good, because it opens no network port. The socket file's filesystem
  permissions control access, for free.
- Good, because it matches a decision this codebase already made
  (task 0013), instead of adding a second kind of local IPC.
- Bad, because a browser tab, or a plain HTTP client, cannot open a
  Unix socket without a bridge process — the exact gap a WebSocket
  closes.
- Bad, because a future Windows port needs named pipes instead. A
  WebSocket needs no such platform fork.

### Approach C — In-process Go handler registration, no IPC

A "plugin" becomes a Go function compiled into and registered with the
daemon itself, instead of a separate process.

- Good, because there is no queue, no serialization, and no
  memory-growth question. The handler runs in the daemon's own
  goroutine.
- Good, because it reuses `driver/api` directly, with no new package or
  dependency.
- Bad, because every plugin needs a driver rebuild and Go knowledge,
  which rules out the any-language goal this task exists for.
- Bad, because one plugin's bug can crash or hang the daemon that owns
  the only device connection, with no process boundary to contain it.

## Decision

Chosen: **Approach A — local WebSocket server**.

The goal is third-party automation in any language, including a
browser-based control panel. Only a WebSocket reaches that with no
bridge process. This choice accepts a `localhost`-bound TCP port and a
new dependency, the same cost task 0013 avoided for its narrower,
Go-only signal use case.

## Design

`driver/cmd/macropadd` opens one `transport.Device` and runs
`plugin.Server`. `driver/plugin/server.go` accepts WebSocket
connections on `127.0.0.1`, up to `maxClients` (16). Each client gets a
buffered channel of `clientQueueSize` (32) JSON messages, sized like
`deviceQueueSize` in `driver/transport`. The dispatch loop's send to a
client channel is non-blocking. A full channel drops the message and
counts the drop. The daemon disconnects a client past `maxDrops` (8).
`driver/plugin/protocol.go` defines two JSON message kinds. `event`
goes from device to client and wraps a decoded `transport.Event`.
`setKeyState` goes from client to device and becomes one
`SendKeyState` call.

Task 0025 landed after this task's design was written, and added a
second consumer of `transport.Device.ReadMessage`:
`driver/recorder.Recorder`. `ReadMessage` supports exactly one caller —
it drains one internal channel — so `plugin.Server` and `Recorder`
running against the same `Device` at once would silently split the
message stream between them. `driver/transport/fanout.go` adds
`Fanout`, the one `ReadMessage` caller `macropadd` now uses; it hands
`plugin.Server` and, when `--trace-file` turns recording on, `Recorder`
each their own subscription — itself a `Transport`, so neither package's
own public API changed. This is the shared demultiplexer task 0020's
Design section already named as `plugin.Server`'s dependency.

Files to change:

- `driver/go.mod` — add a WebSocket library
- `driver/plugin/server.go` — new, connection registry, bounded
  queues, dispatch loop
- `driver/plugin/protocol.go` — new, the `event` and `setKeyState`
  JSON schema
- `driver/plugin/server_test.go` — new, drop, disconnect, and
  `maxClients` tests
- `driver/transport/fanout.go` — new, `Fanout`, so `plugin.Server` and
  task 0025's `Recorder` can each read every device message
- `driver/transport/fanout_test.go` — new, delivery, slow-subscriber,
  and close tests
- `driver/cmd/macropadd/main.go` — new, the daemon entry point; wires
  `Fanout`, `plugin.Server`, and an optional `Recorder` behind
  `--trace-file`
- `driver/README.md` — document the daemon, the protocol, the memory
  bound, and `--trace-file`

## Definition of done

An outside reviewer verifies each item without help from the
implementer.

- [ ] **DoD-1** — A connected client receives a JSON `event` message
      that matches a decoded `Event`. The message arrives within 1 s
      of the device sending it. **Proof:** `go test
      ./driver/plugin/... -run TestServer_DeliversEvent`
- [ ] **DoD-2** — A `setKeyState` JSON message produces one
      `SendKeyState` call with the same key, color, emoji, and blink.
      **Proof:** `go test ./driver/plugin/... -run TestServer_SetKeyState`
- [ ] **DoD-3** — A client that never reads its queue does not block
      delivery to a second, healthy client. **Proof:** `go test
      ./driver/plugin/... -run TestServer_SlowClientDoesNotBlockOthers`
- [ ] **DoD-4** — A client whose queue overflows 8 times loses its
      connection. The daemon keeps running. **Proof:** `go test
      ./driver/plugin/... -run TestServer_DisconnectsStalledClient`
- [ ] **DoD-5** — The server rejects a connection past `maxClients`,
      with a clear close reason, not a crash. **Proof:** `go test
      ./driver/plugin/... -run TestServer_RejectsOverCap`
- [ ] **DoD-6** — `driver/README.md` documents the daemon, the two
      message kinds, and the 16-client, 32-message, 8-drop bounds.
      **Proof:** `driver/README.md`
- [ ] **DoD-7** — The PR body links to this spec. **Proof:** the PR in
      the `pr` field

## Risks

- One stalled client blocks delivery to every other client → the
  dispatch loop's send is non-blocking per client, so one full queue
  affects only that client.
- An unbounded client count grows memory without limit → `maxClients`
  caps it. Total memory stays under `maxClients × clientQueueSize`
  times the size of one message.
- This overlaps task 0013's Unix-socket signal server, so the daemon
  holds two local IPC mechanisms → not resolved here. See the open
  question below.
- Task 0025's `Recorder` and `plugin.Server` both call
  `transport.Device.ReadMessage`, which supports only one caller, so
  running both against `macropadd`'s one `Device` would silently split
  the message stream between them → `transport.Fanout` makes `macropadd`
  the one caller and hands each of them their own subscription instead.
  `MessageTypeTrace` still only reaches the recorder's JSONL file, not a
  plugin — no `signal` broadcasts a trace record today. That gap is
  unresolved; a future task can extend the `signal` vocabulary (see task
  0013) with a trace-derived name if a plugin author needs it.

## Open questions

- [x] Does this WebSocket server merge with task 0013's Unix-socket
      signal server into one process, one IPC mechanism? — Yes,
      decided 2026-08-15. Task 0013's spec now reuses this server: it
      adds a `signal` message kind instead of a separate Unix socket.
      See task 0013's Approach D.
- [ ] Does a plugin need task 0025's device trace records? — deferred,
      no clear demand yet.

## Notes

The design compares to Elgato's Stream Deck plugin SDK. One process
owns the hardware. Plugins speak WebSocket and JSON to it. No plugin
touches the device directly.
