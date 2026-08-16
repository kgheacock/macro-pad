---
id: "0031"
title: "Forward mic audio chunks to plugin clients"
status: "backlog"
created: "2026-08-15"
updated: "2026-08-15"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0002", "0005", "0007", "0013", "0028"]
tags: ["driver", "plugin", "audio"]
---

# 0031 — Forward mic audio chunks to plugin clients

## Problem

`driver/transport` already decodes `MessageTypeAudioChunk` end to end —
`Device`, `Emulator`, and `Fanout` all carry an `AudioChunk` today. But
`plugin.Server`'s `dispatchLoop` (`driver/plugin/server.go:258`) forwards
only `MessageTypeEvent` to clients and silently drops every audio chunk it
reads. `driver/README.md` names this as out of scope for the plugin API.
Once task 0007's mic capture lands, no plugin can read that audio.

## Goals

- A plugin that asks for audio receives each chunk of a recording, in
  order, with the same `StreamID` and `Final` flag the wire protocol
  already assigns.
- A plugin that never asks for audio pays no cost: no queued messages, no
  smaller headroom in its own event queue.
- Forwarding audio does not shrink the memory bound
  `maxClients × clientQueueSize` already gives every other message kind.

## Non-goals

- Firmware mic capture. Task 0007 owns `start()`/`stop()` and turning
  buffered samples into chunks. This task only forwards chunks the driver
  already reads off the wire.
- Click-pattern or hold-duration classification. Tasks 0011–0013 own
  turning press and release timing into a `longPress` or `shortPress`
  signal.
- Deciding what `StreamID` means, for example whether it equals a
  `keyIndex`. Task 0007 is still in backlog and has not fixed that
  convention yet. This task treats `StreamID` as an opaque byte.
- Transcoding or compressing PCM audio. A chunk's bytes reach a plugin
  unchanged from what the wire protocol carries.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — An opt-in binary frame on a separate, bounded queue

A client sends `{"kind": "subscribeAudio"}` to start receiving audio, and
`{"kind": "unsubscribeAudio"}` to stop. Only subscribed clients get a
binary WebSocket frame per chunk (`StreamID` byte, PCM bytes, final-chunk
byte — the same layout `wire.go` already encodes). Each client gets its
own `audioSend` channel, sized independently of `clientQueueSize`.

- Good, because a client that never subscribes never queues an audio
  message, so `maxClients × clientQueueSize` keeps meaning what 0028's
  design promised: a bound sized for small JSON control messages.
- Good, because raw bytes on a binary frame cost nothing extra to encode,
  unlike base64 inside JSON, which adds about 33% to every chunk.
- Bad, because a client's `dispatchLoop` and `writePump` now carry two
  queue-full and disconnect policies instead of one, adding a second code
  path to `server.go` and its tests.
- Bad, because a plugin author's WebSocket library must now branch on
  frame type — text versus binary — not only call `JSON.parse` on every
  message, as 0028's all-JSON design let it do.

### Approach B — Reuse the existing JSON queue, as a new `audioChunk` kind

Audio rides the same `clientQueueSize`-bounded `send` channel every other
kind uses, base64-encoded inside a `Message`, the way task 0030's
`setCustomGlyph` added a kind to the same JSON envelope.

- Good, because it adds no new queue, no new disconnect policy, and no new
  channel type — it repeats the exact pattern `setCustomGlyph` already
  proved.
- Good, because every plugin already parses one JSON shape; one more kind
  is the smallest possible protocol change.
- Bad, because one active recording can fill a 32-message queue shared
  with that client's key events, so audio traffic can drop or delay the
  `event` messages 0028's bound exists to protect.
- Bad, because every connected client pays this cost once any recording
  starts — there is no way to opt out short of not reading the queue,
  which spends that client's `maxDrops` budget instead.

### Approach C — Out-of-band fetch: a local HTTP endpoint, notify over the WebSocket

`macropadd` keeps each stream's recent chunks in a small in-memory ring
keyed by `StreamID`, and serves them over a new `127.0.0.1`-bound HTTP
endpoint. The WebSocket sends only a small JSON notice per chunk —
`{streamId, seq, final}`, no PCM bytes — and a client fetches the bytes
with a separate `GET` request.

- Good, because every WebSocket message stays small JSON, so
  `clientQueueSize` genuinely bounds every client, with no separate
  accounting for audio.
- Good, because any HTTP client — `curl`, a browser `<audio>` tag — can
  fetch a chunk with no WebSocket framing knowledge at all.
- Bad, because it opens a second local server and port, the same wider
  surface 0028's own Approach B rejected when it chose one IPC mechanism
  over two.
- Bad, because the WebSocket notice and the HTTP ring can disagree: a
  slow client's `GET` can arrive after the ring has already evicted that
  chunk, a failure mode a single delivery path never has.

## Decision

Chosen: **Approach A — an opt-in binary frame on a separate, bounded
queue**.

It keeps 0028's existing bound meaningful for every client that never
touches audio, and it adds no second local port, matching 0028's own
choice against a second IPC mechanism. This choice accepts a second
queue-full and disconnect policy inside `server.go`, and it requires a
plugin's WebSocket library to handle a binary frame, not JSON alone.

## Design

`driver/plugin/protocol.go` adds `KindSubscribeAudio` and
`KindUnsubscribeAudio`, both client to server, no payload.
`driver/plugin/server.go`'s `client` struct gains `audioSend chan
[]byte`, sized by a new `audioQueueSize` constant (for example, 8
chunks), and a bool tracking its subscription. `dispatchLoop` widens its
filter past `MessageTypeEvent` to also read `MessageTypeAudioChunk`, and
calls a new `broadcastAudio`, which writes the chunk's raw bytes only to
subscribed clients' `audioSend`, non-blocking, counting drops the same
way `broadcast` does. `writePump` selects over both `send` and
`audioSend`, writing a JSON text frame for the former and a binary frame
for the latter — `wsConn` gains a `WriteMessage(messageType int, data
[]byte) error` method for this.

Files to change:

- `driver/plugin/protocol.go` — add `subscribeAudio`/`unsubscribeAudio`
  kinds
- `driver/plugin/server.go` — `audioSend`, `audioQueueSize`,
  `broadcastAudio`, widened `dispatchLoop` filter, binary write in
  `writePump`
- `driver/plugin/server_test.go` — subscribe, unsubscribe, and
  slow-audio-subscriber tests
- `driver/README.md` — document the two new kinds, the binary frame
  layout, and the `audioQueueSize` bound

## Definition of done

An outside reviewer verifies each item without help from the
implementer.

- [ ] **DoD-1** — After `subscribeAudio`, a client receives one binary
  frame per `transport.MessageTypeAudioChunk` the daemon reads, carrying
  the same `StreamID`, PCM bytes, and final-chunk flag. **Proof:** `go
  test ./driver/plugin/... -run TestServer_DeliversAudioChunkToSubscriber`
- [ ] **DoD-2** — A client that never sends `subscribeAudio` receives no
  audio frames while a recording is in progress. **Proof:** `go test
  ./driver/plugin/... -run TestServer_UnsubscribedClientReceivesNoAudio`
- [ ] **DoD-3** — A subscribed client whose audio queue never drains does
  not block or delay audio or event delivery to a second, healthy client.
  **Proof:** `go test ./driver/plugin/... -run
  TestServer_SlowAudioClientDoesNotBlockOthers`
- [ ] **DoD-4** — The new tests fail on `main`, where `dispatchLoop` drops
  every audio chunk. **Proof:** `git stash && go test
  ./driver/plugin/... -run TestServer_DeliversAudioChunkToSubscriber`
  fails
- [ ] **DoD-5** — `driver/README.md`'s Plugin API section documents
  `subscribeAudio`, `unsubscribeAudio`, the binary frame layout, and
  `audioQueueSize` alongside the existing three bounds. **Proof:**
  `driver/README.md`
- [ ] **DoD-6** — The PR body links to this spec. **Proof:** the PR in
  the `pr` field

## Risks

- Task 0007 has not landed, so this task has no real firmware audio
  producer to test against → tests drive the path with
  `transport.Emulator.InjectAudioChunk`, already available today, the
  same way 0028 tested its own event path before hardware existed.
- `StreamID`'s meaning, for example whether it equals a `keyIndex`, is
  not yet fixed by task 0007 → this task forwards it opaquely; a plugin
  author gets whatever convention task 0007 settles on later.
- A binary frame on the same connection as JSON text frames is untested
  against `driver/plugin/web/virtualpad.html`, which only expects JSON
  today → out of scope here; a later task updates the virtual pad if it
  needs to play audio back.

## Open questions

- [ ] Does `StreamID` become `keyIndex` once task 0007 lands, or does the
  driver need a lookup between them? — task 0007 owner
- [ ] Does `audioQueueSize`'s value need tuning against task 0007's real
  chunk size and I2S sample rate, once hardware (task 0010) exists? —
  repo owner
