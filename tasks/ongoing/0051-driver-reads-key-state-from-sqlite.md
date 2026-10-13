---
id: "0051"
title: "macropadd polls a SQLite page tree and shows the active page on the keys"
status: "ongoing"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0051-driver-reads-key-state-from-sqlite"
related: ["0013", "0028", "0038", "0050", "0052"]
tags: ["driver", "daemon", "sqlite", "state"]
---

# 0051 — macropadd polls a SQLite page tree and shows the active page on the keys

## Problem

`macropadd` learns what the keys show only from `setKeyState` messages on the plugin WebSocket. It keeps a flat list of six keys in `keys.json`. A writer must hold a connection to change the pad, and there are no pages. A writer that is not running leaves no record that another process can read.

## Goals

- `macropadd --state-db PATH` opens a SQLite file and shows the children of the active node on keys 0–5. A child's `position` is its key index.
- The poller compares only `(active_node_id, children_rev)`. It reads the children only when the pair changes.
- A write that changes no visible field sends no message to the board.

## Non-goals

- Drawing the `channel` icon and the `message`. The poller reads and compares them. The icons are ready in `assets/`. Drawing them waits for a later task.
- A writer tool, and key presses that change `active_node_id`.
- A migration of `keys.json` into the database.

## Approaches considered

Three approaches follow. Each one solves the problem in a different way.

### Approach A — Poller inside the daemon, linked SQLite library

A new package `driver/statedb` holds one connection. It calls `transport.Transport.SendKeyState` directly.

- Good, because `PRAGMA data_version` works on a connection that stays open, so an idle poll costs almost nothing.
- Good, because `github.com/mattn/go-sqlite3 v1.14.52` needs go 1.21 and the driver already needs cgo for `go-hid`, so no new build requirement.
- Bad, because it adds one dependency, and the first build compiles the SQLite C source.
- Bad, because the poller bypasses `plugin.Server`, so the virtual pad does not see database-driven keys.

### Approach B — Poller as a separate plugin process

A new binary reads the database and sends each change through `api.Conn.SetState` over the WebSocket. The daemon does not change. This follows task 0013's rule that every extension is a plugin process.

- Good, because the daemon gains no dependency and no new failure mode.
- Good, because the virtual pad and `keystate.html` see every change through the server's rebroadcast.
- Bad, because the pad shows nothing from the database until a second process runs.
- Bad, because a page switch sends six messages through the server's one `sendMu` queue, and the WebSocket adds a hop to each. Task 0050 measured a delay in bursts, not a loss.

### Approach C — Poller inside the daemon, `sqlite3` command

The daemon runs `/usr/bin/sqlite3 -json` on each poll. macOS ships version 3.51.0.

- Good, because the module gains no Go dependency and no go 1.22 pin risk.
- Good, because it needs no cgo code for SQLite.
- Bad, because each poll starts a process, and `data_version` is per connection, so no idle gate exists.
- Bad, because the daemon needs `sqlite3` on `PATH`, and `-json` needs SQLite 3.33 or later.

## Decision

Chosen: **Approach A — Poller inside the daemon, linked SQLite library**.

The pad must keep its state with no plugin running, and an idle poll must cost almost nothing. Only a persistent connection gives both. The task accepts one new dependency and a slower first build.

## Data contract

`nodes` and `state` are a projection. Task 0052 derives them from an append-only event log. The poller reads them and never writes them.

- `nodes(id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES nodes(id) ON DELETE CASCADE, position INTEGER, channel TEXT CHECK (channel IN ('LIVE','DEV','MESSAGES','GITHUB','AI','CREATE')), message TEXT, status TEXT CHECK (status IN ('RUNNING','DONE','ERROR','WARN')), children_rev INTEGER NOT NULL DEFAULT 0, UNIQUE(parent_id, position))`
- `state(id INTEGER PRIMARY KEY CHECK (id = 1), active_node_id INTEGER REFERENCES nodes(id))`
- `children_rev` counts changes to the visible fields of a node's children. The writer bumps it. If a write changes no visible field, it does not bump it.
- `status` is SQL NULL for the white state. The four names are the only other values.
- `status` gives the key color. These RGB565 values were picked by rendering every icon on every color. Pure blue hid the dark icons, so RUNNING is lifted. Check them on the board before release:

  | status | color | RGB565 |
  |---|---|---|
  | RUNNING | blue `#3D5CFF` | `0x3AFF` |
  | DONE | green `#12D84A` | `0x16C9` |
  | ERROR | red | `0xF800` |
  | WARN | yellow | `0xFFE0` |
  | NULL | white | `0xFFFF` |

- `channel` gives the icon. `assets/*.png` are 128×128 RGBA PNGs that `transport.DecodePNGToRGB565` accepts:

  | channel | icon |
  |---|---|
  | LIVE | 🎼 `assets/live.png` |
  | DEV | 🧪 `assets/dev.png` |
  | MESSAGES | `assets/gmail.png` |
  | GITHUB | `assets/github.png` |
  | AI | `assets/ai.png` |
  | CREATE | ⊕ `assets/create.png` (black) |

- The poller query is `SELECT s.active_node_id, n.children_rev FROM state s JOIN nodes n ON n.id = s.active_node_id`.

## Design

`driver/statedb` exposes `Run(ctx, path, interval, dev transport.Transport, logf)`.

- Open with `?_pragma=foreign_keys(1)&mode=ro`. Create the schema in a test helper only; the poller never writes.
- Each tick, read `PRAGMA data_version`. If it equals the last value, stop.
- Otherwise run the poller query. If the pair equals the last pair, stop.
- Otherwise read `WHERE parent_id = ? ORDER BY position`. Send one `KeyState` for each of keys 0–5. A key with no child gets color 0, emoji 0, no blink.
- `status` maps through the table in "Data contract". NULL gives white. An unknown name gives color 0 and one log line. No status blinks; a later task may add it.

Files to change:

- `driver/statedb/statedb.go` — the poller and the status color table
- `driver/statedb/schema.sql` — the "Data contract" schema, used by tests and by task 0052
- `driver/cmd/macropadd/main.go` — flags `--state-db` and `--state-db-poll` (default `250ms`)
- `driver/go.mod` — add `github.com/mattn/go-sqlite3 v1.14.52`
- `driver/README.md` — document the schema and flags

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — After a write that changes a child's `status`, the poller sends that key's new `KeyState` to a fake `Transport` within 1 s. **Proof:** `cd driver && go test ./statedb -run TestStatusChangeSent`
- [ ] **DoD-2** — `UPDATE nodes SET message = message` sends no message. **Proof:** `go test ./statedb -run TestNoopWriteSendsNothing`
- [ ] **DoD-3** — Setting `state.active_node_id` to another node sends six `KeyState` messages for that node's children. **Proof:** `go test ./statedb -run TestPageSwitch`
- [ ] **DoD-4** — With no change, the poller runs the children query 0 times in 20 ticks. **Proof:** `go test ./statedb -run TestIdleDoesNotQueryChildren`
- [ ] **DoD-5** — A key with no child at its position gets color 0 and emoji 0. A NULL `status` gets color `0xFFFF`. An unknown `status` logs one line and gets color 0. **Proof:** `go test ./statedb -run 'TestMissingChild|TestNullStatus|TestUnknownStatus'`
- [ ] **DoD-6** — Deleting the active node's parent with `foreign_keys` on removes its children. **Proof:** `go test ./statedb -run TestCascadeDelete`
- [ ] **DoD-7** — `macropadd --emulate --state-db FILE` shows the child's color on the emulator within 1 s. **Proof:** `go test ./cmd/macropadd -run TestStateDBFlag`
- [ ] **DoD-8** — `driver/README.md` records the schema, `--state-db`, and `--state-db-poll`. **Proof:** `driver/README.md`, section "State database"
- [ ] **DoD-9** — `cd driver && go build ./... && go vet ./...` exits 0 on go 1.22.0. **Proof:** the command's exit code
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body
- [ ] **DoD-11** — RUNNING, DONE, ERROR, WARN, and NULL send `0x3AFF`, `0x16C9`, `0xF800`, `0xFFE0`, and `0xFFFF`, with no blink. **Proof:** `go test ./statedb -run TestStatusColors`
- [ ] **DoD-12** — Each of the six `assets/*.png` files is 128×128 and decodes with `transport.DecodePNGToRGB565`. **Proof:** `go test ./statedb -run TestChannelAssets`

## Risks

- A plugin and the database both set one key → the last write wins, and the poller sends only on a change. Note this in the README.
- A page switch sends six `KeyState` messages at once. Task 0050 measured about 155 ms per board write in a burst closer than 200 ms, so a switch may take about 1 s on the board → the DoD times use a fake `Transport`, and a board run records the real time in Notes. A later task may give each key one writer, as 0050's Resolution says.
- The database is on a stale or network mount → read errors log and retry on the next tick, and the board keeps its last state.

## Open questions

- [ ] Should version 1 draw the `channel` icon with `SendCustomGlyph`? The default is no; the icons stay ready in `assets/`. — kgheacock
- [ ] Should any status blink, for example ERROR as `Alert` does today? The default is no. — kgheacock
- [x] Which process creates the database and writes `state`? Answer: the `statelog` library from task 0052 reduces events into `nodes` and `state`. Until 0052 lands, tests write the rows directly. — kgheacock
