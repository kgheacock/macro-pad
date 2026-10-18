---
id: "0067"
title: "A press on a channel key marks that channel's messages seen"
status: "backlog"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/77"
branch: "claude/project-thread-tl5p4v"
related: ["0013", "0057", "0064", "0065", "0066"]
tags: ["inbox", "layers", "input"]
---

# 0067 — A press on a channel key marks that channel's messages seen

## Problem

Task 0066 shows a key per channel with its unseen count. Nothing on the pad
lowers a count. A person must type `inbox seen CHANNEL` in a terminal,
which is the work the pad should save.

Keith wants a press on a channel key to mark that channel's messages seen.

## Goals

- In the Inbox child layer, a press on a channel key marks that channel's
  unseen messages seen.
- Only the messages the key showed are marked. A message that arrives during
  the press stays unseen.
- The key's count drops to 0 within 1 s of the press. The Inbox total drops
  by the same amount.
- The press that opens the Inbox layer marks nothing.
- The up key (key 3) still returns to the top layer and marks nothing.

## Non-goals

- Reading the messages on the pad. `inbox list` shows them.
- Marking one message at a time.
- Undo. `seen_at` is kept, so a later `inbox unseen` could restore.
- Double and long press actions. They stay free for later tasks.

## Approaches considered

Three approaches follow. They differ in which input the daemon acts on.

### Approach A — The raw press-down event

The daemon acts on `event` messages of type `press`, with the active layer
it knew when the event arrived.

- Good, because it acts at once, like layer navigation in task 0057.
- Good, because `macropadd` broadcasts the press event before the layer
  tree handles it, so the active layer the daemon holds is the one the
  person saw. The press that opens Inbox arrives while `active` is still
  `null`, and is ignored.
- Bad, because a double or long press on a channel key also marks it seen
  on its first press-down.

### Approach B — The resolved `singlePress` signal

The daemon acts on `singlePress` from the click resolver (task 0013).

- Good, because double and long presses stay free.
- Bad, because the signal comes 400 ms after the press. The press that opens
  Inbox has already changed `active` to `inbox` by then. If Inbox sits on key
  0, its `singlePress` lands on child 0 and marks that channel seen. The
  daemon must remember the layer at each press-down to avoid it.
- Bad, because every mark waits 400 ms.

### Approach C — `macropadd` marks seen

The layer tree calls back into the inbox on a child press.

- Good, because the press and the layer are in one place.
- Bad, because the driver gains inbox rules, which task 0065 kept out of it.

## Decision

Chosen: **Approach A — the raw press-down event**.

It is immediate, and its ordering avoids the open-press bug that B must
work around. The task accepts that double and long press on a channel key
cannot have a different meaning without revisiting this.

## Design

### Press to channel

The daemon holds `active` from `layerChanged` (task 0065), and the children
it last sent, in order (task 0066). On an `event` of type `press` with key
`k`:

1. If `active` is not `inbox`, do nothing.
2. If `k` is 3, do nothing. The layer tree returns to the top layer.
3. Map `k` to a child position: keys 0, 1, 2, 4, 5 are positions 0 to 4.
4. If no child is at that position, do nothing.
5. Otherwise call `store.MarkSeen(channel, shownID)`, where `shownID` is the
   newest unseen id the daemon had when it last sent that child.

A child at count 0 marks nothing new, and is harmless.

### After the mark

`MarkSeen` commits, so the next `data_version` poll (task 0065) sees the
change and task 0066 sends the new counts. To meet the 1 s goal without
waiting for the poll, the daemon runs the read and the send right after the
mark. The child stays in its position at count 0 until the layer closes
(task 0066).

### Errors

A failed `MarkSeen`, such as a locked store past the busy timeout, is
logged with the channel, and the counts stay. The next press tries again.

Files to change:

- `inbox/daemon` — press handling
- `inbox/README.md` — "Daemon": what a press does

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — With `active` `inbox` and children `a`, `b`, `c`, presses
  on keys 0, 1, 2 mark `a`, `b`, `c` seen; keys 4 and 5 with no child mark
  nothing; key 3 marks nothing. **Proof:** `cd inbox && go test ./daemon -run TestPressMarksChannel`
- [ ] **DoD-2** — A press while `active` is `null` marks nothing, including
  the press on the Inbox key that opens the layer, with Inbox on key 0.
  **Proof:** `go test ./daemon -run TestOpenPressMarksNothing`
- [ ] **DoD-3** — A message inserted after the child was sent, and before
  the press, stays unseen, and the child shows count 1 after the press.
  **Proof:** `go test ./daemon -run TestLateMessageStaysUnseen`
- [ ] **DoD-4** — After a press, the server receives the new child count
  and Inbox total within 1 s, with `--poll 10s`. **Proof:** `go test ./daemon -run TestMarkSendsAtOnce`
- [ ] **DoD-5** — On the pad, a press on a channel key with count 2 shows
  count 0, and the Inbox key's count drops by 2. `inbox list` for that
  channel prints nothing. **Proof:** Keith's check on the board, recorded in Notes
- [ ] **DoD-6** — `inbox/README.md` "Daemon" says what a press on a channel
  key does. **Proof:** `inbox/README.md`
- [ ] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- A person presses a channel key to open it, expecting to read, and loses
  the unread marks → `inbox list --seen CHANNEL` still shows the
  messages. The README says a press means "I've seen these".
- `macropadd` changes to handle the layer press before it broadcasts the
  event → DoD-2 then fails, and the daemon must record the layer at each
  press. The test catches it.
- A press during a reconnect, before the first `layerChanged` → `active` is
  unknown, so the daemon treats it as not `inbox` and marks nothing.

## Open questions

- [ ] Should a long press on a channel key do something else, such as open
  a terminal in that project? The default leaves it unused. — kgheacock

## Notes

Keith asked for seen on a channel key press on 2026-10-18. `macropadd`
broadcasts each `event` before it queues the press for the layer tree
(`driver/plugin/server.go`, the read loop), which Approach A relies on.
