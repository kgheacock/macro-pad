---
id: "0095"
title: "A press on a repo key opens its items: yellow items, open PRs, and Claude sessions, with no duplicates"
status: "backlog"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0068", "0076", "0078", "0081", "0085", "0086", "0087", "0088", "0089", "0092", "0093", "0096"]
tags: ["inbox", "repos", "layers", "claude-code", "github"]
---

# 0095 — A press on a repo key opens its items, with no duplicates

## Problem

The `repos` key (task 0087) shows one key per repo. A press shows nothing, so Keith cannot see the tickets, PRs, and sessions behind the counts. One piece of work often appears three times: as a ticket, as a PR whose title names the ticket, and as a session on the PR's branch.

## Goals

- A press on a repo opens the layer `['repos', 'repo.NAME']`. It holds yellow items (task 0093), open PRs of the repo, and active Claude sessions in the repo.
- One piece of work is one key. A PR merges into the yellow item that its title names. A session is dropped when its branch names a listed PR or a listed yellow id.
- A press on an item goes "here" (task 0068). More than five items page (task 0078).

## Non-goals

- What an item key draws. Task 0096 owns that.
- The yellow provider (0093), the repo counts (0089, 0092), the limits and the `opens` flag (0088), and the nested layer (0086). This task needs them.
- Marking a session seen.

## Approaches considered

One fact shapes the options. `PressNode` in `driver/layers/controller.go` opens a layer only when the node has children. So items that exist only while the layer is open need a way to open an empty layer first.

### Approach A — The `opens` flag of task 0088, items sent on `layerChanged`

A repo that has items carries `opens: true`. A press opens its empty layer. On `layerChanged` with the repo in the path, the renderer sends the items.

- Good, because closed repos cost no nodes, and no placeholder key shows.
- Good, because removing the items on leave is safe: an `opens` layer stays open when its last child goes.
- Bad, because this task waits for 0088's new field, which crosses five places in the driver and the inbox.
- Bad, because the layer shows only the up key for one `plan` round trip after the press.

### Approach B — A placeholder child, replaced on `layerChanged`

A repo with items holds one placeholder child, so a press opens a real layer. The renderer sends the items, then removes the placeholder.

- Good, because it needs no driver change and does not wait for task 0088.
- Bad, because 100 repos with items hold 100 extra nodes, which every tree clone copies.
- Bad, because the order is strict: items first, then the placeholder out. A mistake pops the pad up one level.

### Approach C — Every item node always exists

The renderer keeps the items of every repo under its key.

- Good, because the items are on the pad before the press, so nothing flashes.
- Good, because it needs no lifecycle code and no driver change.
- Bad, because 100 repos with 3 items add 300 nodes, and each feed run rewrites nodes that nobody sees.
- Bad, because the preload spends frame slots (52) on frames nobody sees. Task 0088 rejects this shape.

## Decision

Chosen: **Approach A — the `opens` flag, items sent on `layerChanged`**.

It keeps the idle tree small and the lazy rule of the brief. The task accepts a dependency on 0088's field, and a short empty layer after the press.

## Design

Files to change:

- `inbox/src/plugins/repos-items.js` — new: `mergeItems`, `activeSessions`, `itemsOf`, `press`
- `inbox/src/plugins/repos-pulls.js` — new feed of open PRs; takes `pullOf` and `PR_FIELDS` from `inbox/src/plugins/github.js` by copy, because task 0087 deletes that file; query `is:pr is:open user:OWNER archived:false`
- `inbox/src/plugins/repos.js` (task 0087) — repo node gets `opens`; `plan({ path })` calls `itemsOf`; `owns(parent)` is true for the open `repo.NAME`
- `inbox/src/store.js` — schema 10 (after 0089, 0090, 0087 and 0093), table `repo_pulls`; `replacePulls`, `listPulls(key)`, `sessionBranches(repo, session)`
- `inbox/test/repos-items.test.js` — new
- `inbox/README.md` — section "Repo layer"

**Item.** The brief's `{ id, yellow, green, session, title, url }`, plus `ci` (`failing|pending|passing|none|null`) and `agent` (`waiting|running|done|null`) for task 0096. The wire id is `y:ID`, `pr:N`, or `s:SID`, with `path` `['repos', 'repo.NAME']`.

**Inputs.** Yellow: `store.yellowItems(name, now)` (0093). PRs: `store.listPulls(repoKey(name))`, with `repoKey` from 0089. The feed is cut at 50 PRs, as in `github.js`. Sessions: `store.forPlugin(SESSION_PLUGIN).recent(now - ACTIVE_MS)`. `SESSION_PLUGIN` is `repos`, and `ACTIVE_MS` comes from `inbox/src/plugins/repos.js`. Task 0087 moves both there from `ai.js`.

**Active session.** A row of this repo, newer than `ACTIVE_MS`, and not ended. The hook `inbox/claude-code/claude-ai-inbox.sh` marks seen, then sends, so a live row has `count` of 1 or more. `end` marks seen and sends nothing, so `done` with `count` 0 means ended. `agent` is `rank(row)` from `ai.js`. Channel `PROJECT.SID` is in a repo when its folder holds `.OWNER.NAME` at a segment edge, so subfolders count.

**Merge.**

1. Each yellow entry is an item.
2. Take PRs by number. A PR whose title holds a yellow id, case-insensitive and between non-alphanumerics, sets `green`, `ci`, and `url` on the first yellow item with no green. Else it is a green item.
3. A session S joins the item whose PR has `branchSession(repo, branch) === S`. Else it joins the first yellow item whose id is in a branch from `sessionBranches`. It sets `agent` and `session`. Else it is a session-only item.

**Order.** Yellow, green, session. Inside a group: `failing`, `waiting`, `pending`, `running`, the rest, then id. At most 20 show; the driver pages them.

**Lifecycle.** While `path` is `['repos', 'repo.NAME']`, no item moves or goes, a new one appends, and a changed one updates in place, as `createAppsRenderer` does. When `path` leaves, the renderer removes the items. A repo with no items has no `opens`.

**Press.** `press({ parent, child, store, fed, focus })` recomputes `itemsOf` and finds the item. The target is the PR `url:`, else the yellow `url:`, else the session's `iterm2:` here. `parseHere` in `inbox/src/here.js` checks it. A bad target is logged, and nothing runs.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — A PR titled `PROJ-123: fix login` merges into yellow `PROJ-123` in any letter case. `PROJ-1234` and `XPROJ-123` do not. Two PRs for one id give one merged and one green item. **Proof:** `cd inbox && node --test --test-name-pattern=TestMergePrIntoYellow test/repos-items.test.js`
- [ ] **DoD-2** — A session mapped to a listed PR by `branch_sessions`, or on a branch with a listed yellow id, makes no item. An unmatched session is one `s:SID` item. **Proof:** `node --test --test-name-pattern=TestSessionDedupe test/repos-items.test.js`
- [ ] **DoD-3** — A session is active only in its repo, within `ACTIVE_MS`, and not ended. **Proof:** `node --test --test-name-pattern=TestActiveSessions test/repos-items.test.js`
- [ ] **DoD-4** — The order is yellow, green, session, with failing and waiting first. 23 candidates send 20 nodes. **Proof:** `node --test --test-name-pattern=TestItemOrder test/repos-items.test.js`
- [ ] **DoD-5** — A repo node has `opens` only when it has items. With the layer shut, no item node exists. Opened, the renderer sends the items. On leave, it removes them. **Proof:** `node --test --test-name-pattern=TestRepoLayerLifecycle test/repos-items.test.js`
- [ ] **DoD-6** — While open, a feed change updates items in place and removes none; a new item appends. Item nodes exist for one repo at a time. **Proof:** `node --test --test-name-pattern=TestRepoLayerStable test/repos-items.test.js`
- [ ] **DoD-7** — `listPulls` returns the PRs of one repo after a cut answer, and `replacePulls` with `complete` false deletes no row. **Proof:** `node --test --test-name-pattern=TestRepoPulls test/store.test.js`
- [ ] **DoD-8** — A press calls `focus` once: PR `url:`, else yellow `url:`, else `iterm2:`. A bad target calls nothing. **Proof:** `node --test --test-name-pattern=TestPressItem test/repos-items.test.js`
- [ ] **DoD-9** — Over a test WebSocket, `layerChanged` with the repo path draws the items, and `childPressed` reaches `press`. **Proof:** `node --test --test-name-pattern=TestDaemonRepoLayer test/repos-items.test.js`
- [ ] **DoD-10** — On the board, a press on a repo opens its items, a sixth item adds a page, and a press on a PR key opens it. **Proof:** Keith's check, in Notes
- [ ] **DoD-11** — `inbox/README.md` "Repo layer" lists the rules, and the PR body links to this spec. **Proof:** `inbox/README.md`; the PR in the `pr` field

## Risks

- `branch_sessions` keeps one session per branch, so a second session on it shows as its own key → the key shows its own `agent`.
- `componentOf` in `inbox/src/emoji.js` reads `foo.js` as `foo` → the session match above uses the folder; the branch link keeps the old key.
- Hooks write to plugin `ai` today, and task 0087 renames it to `repos` → `SESSION_PLUGIN` is one constant.
- A search cut at 50 PRs hides some, and the repo count (0089) shows them → the cut is logged.

## Open questions

- [ ] Should a merged item open the ticket, not the PR? The default is the PR. — kgheacock

## Notes

Board check: not done yet.
