---
id: "0052"
title: "An append-only event log reduces to the nodes and state tables that 0051 polls"
status: "backlog"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0051", "0013", "0028"]
tags: ["driver", "sqlite", "state", "event-log"]
---

# 0052 — An append-only event log reduces to the nodes and state tables that 0051 polls

## Problem

Task 0051 reads `nodes` and `state` from SQLite, but nothing defines who writes them. A writer that edits the rows directly leaves no history. A bad write cannot be undone, and the tables cannot be rebuilt.

## Goals

- Every change to the pad is one row in an append-only `events` table.
- `nodes` and `state` are a projection. A rebuild from `events` alone gives the same rows.
- A writer appends an event and updates the projection in one transaction. The poller never sees a half-applied event.
- An event that changes no visible field does not bump `children_rev`.

## Non-goals

- Changes to the poller. Task 0051 owns it, and it stays read-only.
- Key presses that append events. A later task adds them.
- Event compaction, snapshots, and sync between machines.
- A CLI for writers.

## Approaches considered

Three approaches follow. Each one reduces the log in a different place.

### Approach A — SQLite triggers on `events`

An `AFTER INSERT` trigger on `events` updates `nodes` and `state`. Any client can append, including the `sqlite3` command.

- Good, because the append and the projection update share one statement, so no writer can skip the reducer.
- Good, because a writer needs no Go code. A shell script can append an event.
- Bad, because the reduction rules live in SQL. The no-op check and the `children_rev` bump need a trigger for each event type.
- Bad, because a rebuild must replay events through the triggers. The task must add a replay table or a flag to do this.

### Approach B — Go library that appends and reduces in one transaction

A package `driver/statelog` exposes `Append(ev)` and `Rebuild()`. Both call one pure function, `apply(tx, ev)`.

- Good, because `apply` is plain Go. Unit tests cover every event type without a trigger harness.
- Good, because `Rebuild` and `Append` share the same code, so replay and live writes cannot drift apart.
- Bad, because a writer that bypasses the library can break the projection. Only the README guards against this.
- Bad, because every writer must be Go or must call a Go binary.

### Approach C — Separate reducer process that tails `events`

A process reads new rows by `id` and writes `nodes` and `state`. This matches the plugin-process rule in task 0013.

- Good, because writers only append. They need no schema knowledge beyond `events`.
- Good, because the reducer can run beside other readers of the log, such as the recorder from task 0025.
- Bad, because the projection lags the log by one poll. A crash between two reads needs a stored cursor.
- Bad, because there is one more process to start and to monitor.

## Decision

Chosen: **Approach B — Go library that appends and reduces in one transaction**.

The poller must never see a half-applied event, and the reduction rules need unit tests. Plain Go gives both. The task accepts that a writer must use the library.

## Design

Tables, added beside the projection tables from task 0051:

- `events(id INTEGER PRIMARY KEY AUTOINCREMENT, at TEXT, type TEXT, payload TEXT)`. `payload` is JSON with the full new values, not deltas.
- Event types: `node_set` (upsert one node by `id`), `node_delete`, `activate` (set `state.active_node_id`).

Reduction rules in `apply`:

- `node_set` writes the full row. If no visible field changed, it does nothing. Otherwise it bumps `children_rev` on the parent node.
- `node_delete` removes the node. Foreign keys remove its children. It bumps the parent.
- `activate` sets `active_node_id`. It never bumps `children_rev`.
- A failed event rolls back the append too.

Files to change:

- `driver/statelog/statelog.go` — `Append`, `Rebuild`, `apply`
- `driver/statelog/events.sql` — the `events` table
- `driver/statelog/statelog_test.go` — tests
- `driver/README.md` — document events and the "never edit `nodes` or `state`" rule

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — After a `node_set` event, `nodes` holds the new row and the parent's `children_rev` is one higher. **Proof:** `cd driver && go test ./statelog -run TestNodeSetBumpsParent`
- [ ] **DoD-2** — A `node_set` that repeats the stored values leaves `children_rev` unchanged. **Proof:** `go test ./statelog -run TestNoopEventNoBump`
- [ ] **DoD-3** — `Rebuild` after a mixed history gives `nodes` and `state` rows equal to the live rows, including `children_rev`. **Proof:** `go test ./statelog -run TestRebuildEqualsLive`
- [ ] **DoD-4** — An event with an unknown `type` returns an error and adds no row to `events`. **Proof:** `go test ./statelog -run TestUnknownTypeRollsBack`
- [ ] **DoD-5** — With the 0051 poller on the same file, `Append` of a `status` change reaches a fake `Transport` within 1 s. **Proof:** `go test ./statelog -run TestPollerSeesAppend`
- [ ] **DoD-6** — `driver/README.md` records the event types and the rule that writers use `statelog`. **Proof:** `driver/README.md`, section "Event log"
- [ ] **DoD-7** — `cd driver && go build ./... && go vet ./...` exits 0 on go 1.22.0. **Proof:** the command's exit code
- [ ] **DoD-8** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- A writer edits `nodes` directly → the next `Rebuild` erases the edit. The README states that `nodes` and `state` are derived.
- The poller opens the file with `mode=ro` while a writer uses WAL → the reader needs access to the `-shm` file. DoD-5 runs this case.
- `children_rev` is a counter in the projection → `Rebuild` must replay in `id` order so the counts match. DoD-3 checks this.

## Open questions

- [ ] Should `events` store the actor, for example `plugin:name`? The default is no. — kgheacock
- [ ] Does a later task add `press` events, or does the poller stay the only reader of `state`? — kgheacock
