---
id: "0066"
title: "An Inbox key with the unread count, and one child key per channel"
status: "backlog"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/77"
branch: "claude/project-thread-tl5p4v"
related: ["0057", "0062", "0064", "0065", "0067"]
tags: ["inbox", "layers", "driver", "protocol"]
---

# 0066 — An Inbox key with the unread count, and one child key per channel

## Problem

Task 0065's daemon sees the unread counts per channel, but sends nothing the
pad can show. Keith wants one Inbox key on the top layer, and the channels
as its children: a press on Inbox opens a layer with one key per channel.

Task 0057's layer tree fits this: one key with up to five children. Two
things are missing.

- The tree has no way to remove a node. `setLayerKey` only upserts. A
  channel whose messages are all seen would stay on the pad forever.
- A channel has no picture. A key with only a color and a count does not say
  which channel it is.

## Goals

- One top-level key, id `inbox`, shows the total unseen count across every
  channel.
- Each channel with unseen messages is one child of `inbox`, with its own
  unseen count and a picture of its name.
- At most five channels show, the ones with the newest unseen messages.
- A channel with no unseen messages leaves the child layer, but never while
  the child layer is on screen, so keys do not move under a finger.
- A client can remove a key or child from the layer tree.
- The daemon sends only nodes whose values changed.

## Non-goals

- What a press on a channel key does. Task 0067.
- A sixth channel and beyond on the pad, or paging within the child layer.
  They count in the Inbox total, and `inbox channels` lists them.
- Per-channel icons chosen by the user. A later task can map a channel to a
  PNG.
- Firmware changes. The pad receives ordinary layer draws.

## Approaches considered

Three approaches follow. They differ in how a channel leaves the pad.

### Approach A — Add `removeLayerKey` to the protocol

A new client message removes one key or child. The daemon removes a channel
when its count reaches zero, deferred while the child layer is open.

- Good, because the tree holds exactly the channels with news, and
  `layers.json` stays small.
- Good, because other clients, such as `macro_streamer` or the task 0055
  host, gain removal too.
- Bad, because the driver protocol and `driver/README.md` change.

### Approach B — Keep five fixed children and rewrite them

The daemon owns five child ids, `0` to `4`, and rewrites their picture and
count as channels come and go. An empty one gets a blank picture.

- Good, because the protocol does not change.
- Bad, because a blank child is still a child, so `inbox` always has
  children, and its press always opens a layer of blank keys.
- Bad, because a child id no longer names a channel, so task 0067 must keep
  a second map from position to channel.

### Approach C — Re-create the whole `inbox` key

To drop a child, the daemon reserves the slot under a new id and writes the
children again.

- Good, because the protocol does not change.
- Bad, because a key changes id, which closes an open child layer and
  confuses any client that tracks the id.
- Bad, because it redraws every child for one removal.

## Decision

Chosen: **Approach A — add `removeLayerKey`**.

Child ids stay equal to channel names, the press on an empty Inbox does
nothing, and the change is small and useful to every client. The task
accepts one more protocol message.

## Design

### Driver: `removeLayerKey`

```json
{"kind": "removeLayerKey", "removeLayerKey": {"id": "macro-pad.driver", "parent": "inbox"}}
```

- `layers.Controller.RemoveKey(id, parent string) error`. With `parent`, it
  removes that child. Without, it removes the top-level key and its
  children, and frees its slot.
- Removing an unknown id is not an error. It changes nothing and sends
  nothing.
- If the removed node is the open layer's key, or the last child of the open
  layer's key, `active` returns to `null` and `layerChanged` is sent.
- The remaining children keep their order, so they move up one position.
- The server logs and drops a message that fails, as it does for
  `setLayerKey`.

### Inbox key

| Field | Value |
|---|---|
| `id` | `inbox` |
| `slot` | `--slot N` if given; else none, so the key takes the first free slot and the tree keeps it there |
| `iconRef` | `inbox.png`, a new 128x128 asset in `assets/` |
| `count` | total unseen messages in every channel |
| `status` | `WARN` when the count is above 0; empty when 0 |

The Inbox key stays on the pad with a count of 0. It is the place a person
looks for news.

### Channel children

- One child per channel with unseen messages, ordered by newest unseen
  message, newest first. Only the first five are children.
- `id` is the channel name. `count` is its unseen count. `status` is `WARN`.
- `iconRef` is `.generated/inbox/<channel>.png` under `--assets-dir`. The
  daemon draws it: a dark tile with the channel's last segment in large
  letters, and the segment before it in small letters above, cut with `…`
  to fit 128 px. It uses `golang.org/x/image/font/basicfont`, scaled. It
  writes the file with a temporary name and a rename, and only when the
  name has no file yet.
- The layer tree draws the count badge (task 0057, or task 0062's pip once it
  lands).

### Stable keys while the layer is open

The daemon knows the active layer from `layerChanged` (task 0065).

- While `active` is `inbox`, the daemon does not remove or reorder children.
  A channel that reaches zero keeps its key with count 0 and no status. A new
  channel takes a free child position if one is left; otherwise it waits.
- When `active` leaves `inbox`, the daemon reconciles: it removes channels
  at zero, adds waiting ones, and reorders by newest unseen.
- Reorder sends the children again in the new order: remove, then
  `setLayerKey` each one. It happens only while the layer is closed, so no
  one sees it.

### Diffing

The daemon keeps the last values it sent per node, and sends a
`setLayerKey` only when the values differ. On reconnect (task 0065) it
clears this record and sends everything.

Files to change:

- `driver/layers/controller.go` — `RemoveKey`
- `driver/plugin/protocol.go`, `driver/plugin/server.go` — `removeLayerKey`
- `driver/README.md` — "Protocol" and "Layers"
- `assets/inbox.png` — the Inbox icon
- `inbox/render` — the plan from `Unseen()` and the active layer to
  messages, and the channel images
- `inbox/go.mod` — `golang.org/x/image`

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — `RemoveKey` removes a child, removes a top-level key with
  its children, ignores an unknown id, and returns `active` to `null` when
  it empties the open layer. **Proof:** `cd driver && go test ./layers -run TestRemoveKey`
- [ ] **DoD-2** — A `removeLayerKey` message removes the node and redraws only
  the keys that changed. **Proof:** `go test ./plugin -run TestRemoveLayerKey`
- [ ] **DoD-3** — Unseen counts `{a:2, b:1}` give an `inbox` key with count
  3 and status `WARN`, and children `a` and `b` with counts 2 and 1, newest
  first. No unseen messages give an `inbox` key with count 0, no status, and
  no children. **Proof:** `cd inbox && go test ./render -run TestPlan`
- [ ] **DoD-4** — Seven channels with news give five children, the five
  newest; the Inbox count includes all seven. **Proof:** `go test ./render -run TestFiveNewest`
- [ ] **DoD-5** — While `active` is `inbox`, a channel at zero keeps its
  position with count 0, and no child moves. After `layerChanged` to the top,
  the zero channel is removed and the rest reorder. **Proof:** `go test ./render -run TestStableWhileOpen`
- [ ] **DoD-6** — A second plan with no change sends nothing. **Proof:** `go test ./render -run TestNoChangeSendsNothing`
- [ ] **DoD-7** — The channel image for `macro-pad.driver` is 128x128, and
  its pixels match a golden PNG in `inbox/render/testdata`. **Proof:** `go test ./render -run TestChannelImage`
- [ ] **DoD-8** — On the pad, with `macropadd` and `inbox daemon` running,
  `inbox -c demo send hi` shows a count on the Inbox key; a press shows a
  `demo` key with count 1 on key 0, and the shift arrow on key 3. **Proof:**
  Keith's check on the board, recorded in Notes
- [ ] **DoD-9** — `driver/README.md` records `removeLayerKey`. **Proof:** `driver/README.md`, "Protocol"
- [ ] **DoD-10** — `go build ./... && go vet ./...` exits 0 in `driver` and
  in `inbox` on go 1.22.0. **Proof:** the exit codes
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- `golang.org/x/image` may need a newer Go than 1.22.0 → pin a release whose
  `go` line is 1.22 or older.
- Each new channel sends a new image to the board, about 155 ms → the
  frame cache of task 0061 keeps it after the first draw.
- Generated images pile up under `assets/.generated/inbox/` → one small PNG
  per channel ever seen. A later prune can remove them.
- A channel whose last segment is long is hard to read → the cut and the
  small parent segment above it. The golden test fixes the look.

## Open questions

- [ ] Should the Inbox key turn a color other than `WARN` yellow when it
  has news? — kgheacock
- [ ] Should the Inbox key hide when the count is 0, freeing the slot? The
  default is no. — kgheacock

## Notes

Keith asked for "Inbox" as the top-level icon and the channels as its
children on 2026-10-18.
