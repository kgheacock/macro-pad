---
id: "0088"
title: "Raise the child and node caps so every repo shows, and open an empty layer on press"
status: "backlog"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0057", "0061", "0062", "0063", "0078", "0086", "0087", "0095"]
tags: ["driver", "layers", "plugin", "wire-protocol", "paging"]
---

# 0088 — Raise the child and node caps so every repo shows, and open an empty layer on press

## Problem

The owner has 30 to 100 repos, and all must show on the `repos` key (task 0087). `layers.MaxChildren` is 20 and `layers.MaxNodes` is 200, in `driver/layers/doc.go`. The inbox copies the 20 as `MAX_CHILDREN` in `inbox/src/render.js` and drops the rest without a word. The plan also needs item nodes that exist only while a repo layer is open. A press on a node with no children sends `childPressed` and opens nothing (`PressNode` in `driver/layers/controller.go`). So a lazy repo node cannot open its own layer.

## Goals

- A layer holds 128 children, which is 32 pages of four. The tree holds 512 nodes.
- A node marked `opens` opens an empty layer on press, so the plugin fills it after the press.
- A full-size tree still draws every page through the board's frame cache.
- The inbox mirrors the new cap in one named constant. Plugins that want 20 keep 20.

## Non-goals

- A jump or search key for 32 pages. The activity order of task 0090 puts used repos first.
- The repo list and its order (tasks 0089, 0090), the plugin (0087), and the item layer (0095). Those tasks need this one.
- A change to `MaxDepth` (4), `KeyCount`, `PerPage`, the pip, or the firmware.

## Approaches considered

Three approaches follow. They differ in where the per-repo state lives.

### Approach A — Eager tree

Raise both caps far enough for every repo to hold all its items at all times. Nothing is lazy.

- Good, because it adds no driver feature: a press on a repo always opens a real layer.
- Good, because the preload stores item frames while the pad is idle, so an open draws at once.
- Bad, because 128 repos with 30 items each is about 4,000 nodes. Every `SetKey` clones, validates, and compares the whole tree, so one sync costs about 15 million node visits.
- Bad, because `layers.json` grows to about 1 MB and the preload churns the 52 slots with frames nobody sees.

### Approach B — Lean tree, lazy items, `opens` flag

Raise the caps to 128 and 512. A node with `opens: true` and no children opens an empty layer on press. The plugin fills it on `layerChanged`.

- Good, because the idle tree is about 140 nodes (100 repos, 5 apps), so one `SetKey` stays cheap.
- Good, because it keeps the contract of the brief: items exist only while their repo layer is open.
- Bad, because one new field crosses five places: `Node`, `Update`, the plugin payload, the inbox message, and `layers.json`.
- Bad, because the first open shows only the up key until the plugin answers, about one poll away.

### Approach C — Virtual window

Keep the caps small. The plugin sends only the visible page and its neighbours. The driver learns the true child `total` and builds pages from it.

- Good, because the node count and the frame count stay near 20 per layer, for any number of repos.
- Good, because it needs no larger cap and no preload change.
- Bad, because every page turn waits for the plugin to send children. A slow plugin leaves blank keys.
- Bad, because `total` breaks the rule that `PageCount` comes from `len(Children)`. Validation, `settle`, and `neededFrames` all need new cases.

## Decision

Chosen: **Approach B — Lean tree, lazy items, `opens` flag**.

A tree of about 140 idle nodes keeps each update cheap and meets the lazy-item rule of task 0095. The task accepts one new field in five places. It also accepts a cold page cost: 52 slots hold at most 13 pages of four repos, and a miss stores up to four frames at 94 to 109 ms each (task 0062), about 0.4 s.

## Design

Limits, in `driver/layers/doc.go`: `MaxChildren = 128`, `MaxNodes = 512`. The worst case fits: repos 1 + 128 + 128 items, apps 1 + 20 × 6, basic 1 + 20, for 399 nodes. A 129th child fails with `129 children, at most 128`.

`opens`:

- `Node.Opens` and `Update.Opens` carry `opens,omitempty`.
- `PressNode` opens a child when `len(Children) > 0 || Opens`. The top layer does the same.
- `settle` and `Validate` accept an active node that has `Opens` and no children.
- Removing the last child of an `Opens` node leaves its layer open. For any other node the pad still goes up.

The inbox adds `REPO_MAX_CHILDREN = 128` to `inbox/src/render.js`. `MAX_CHILDREN` stays 20, so `basic` and `INBOX_APPS_COUNT` do not change. A test reads `doc.go` and fails when the two numbers differ.

Files to change:

- `driver/layers/doc.go`, `controller.go`, `pages.go` — limits, `Opens`, `settle`
- `driver/plugin/protocol.go`, `server.go` — `opens` in `SetLayerKeyPayload`, copied into `Update`
- `inbox/src/render.js`, `inbox/src/protocol.js`, `inbox/test/fixtures/protocol.json` — constant and field
- `driver/README.md` — limits, "Key layout", protocol table
- `driver/layers/layers_test.go`, `nested_test.go`, `frames_test.go`, `pages_test.go` — tests

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — A layer with 128 children validates. A layer with 129 fails with `129 children, at most 128`. **Proof:** `cd driver && go test ./layers -run 'TestDocValidation|TestChildCap'`
- [ ] **DoD-2** — A tree of 512 nodes validates, and the 513th node is rejected. **Proof:** `cd driver && go test ./layers -run TestMaxNodes`
- [ ] **DoD-3** — The worst-case tree of 399 nodes (repos, apps, basic) loads and draws. **Proof:** `cd driver && go test ./layers -run TestWorstCaseTreeFits`
- [ ] **DoD-4** — 128 children give 32 pages. A walk with the right arrow reaches child 127 on page 32. **Proof:** `cd driver && go test ./layers -run TestPagingOf128Children`
- [ ] **DoD-5** — On a board with a frame cache, a walk over all 32 pages of 128 distinct frames ends every turn in one Show frames. It stores at most 4 frames per turn and never uses the fallback draw. **Proof:** `cd driver && go test ./layers -run TestFrames_AWalkOverAllPagesNeverFallsBack`
- [ ] **DoD-6** — A press on a node with `opens` and no children opens an empty layer, sends `layerChanged` with its path and `pages` 1, and sends no `childPressed`. **Proof:** `cd driver && go test ./layers -run TestOpens_PressOpensAnEmptyLayer`
- [ ] **DoD-7** — Removing the last child of an `opens` layer keeps it open. A node without `opens` still goes up. A saved empty layer reloads open. **Proof:** `cd driver && go test ./layers -run 'TestOpens_(RemoveKeepsLayer|Persists)|TestNestedRemove'`
- [ ] **DoD-8** — `setLayerKey` with `"opens":true` reaches the controller. **Proof:** `cd driver && go test ./plugin -run TestSetLayerKeyOpens`, and `cd inbox && node --test test/protocol.test.js`
- [ ] **DoD-9** — The inbox constant equals `MaxChildren` in `doc.go`, and `INBOX_APPS_COUNT` still stops at 20. **Proof:** `cd inbox && node --test test/limits.test.js`
- [ ] **DoD-10** — One `SetKey` on a full 512-node tree takes under 5 ms. **Proof:** `cd driver && go test ./layers -run TestSetKeyOnAFullTree`
- [ ] **DoD-11** — `driver/README.md` states 128, 512, and `opens`. **Proof:** `grep -n 'at most 128' driver/README.md`
- [ ] **DoD-12** — The PR links to this spec. **Proof:** PR body

## Risks

- A plugin sends more than 128 children → the driver drops the extra one and logs `setLayerKey ... dropped`. The `repos` plugin must log the count it cuts.
- An old `macropadd` ignores `opens` → a repo press sends `childPressed` and opens nothing. Restart `macropadd` after the update.

## Open questions

- [ ] Is 0.4 s on a cold page turn acceptable? — Keith, after the first run on the board.

## Notes

- `SlotCount` is `PoolSize - PipBufferCount`, which is 64 − 12 = 52 (`driver/transport/frames.go`). `neededFrames` stops preloading when the table is full.
