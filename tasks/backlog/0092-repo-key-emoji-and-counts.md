---
id: "0092"
title: "A repo key shows the repo emoji, a red issue count, a green PR count and a yellow count"
status: "backlog"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: null
related: ["0062", "0073", "0081", "0082", "0087", "0088", "0089", "0090", "0091", "0093"]
tags: ["inbox", "repos", "emoji", "badges"]
---

# 0092 — A repo key shows the repo emoji and three counts

## Problem

The `repos` plugin (0087) draws one child key per repo. Today no code says what such a key shows. The github
plugin draws a caption over a repo emoji and one red pip that counts failing PRs (`prIcon` in
`inbox/src/plugins/github.js`). The person cannot see open issues, open PRs, or their own tracked items at a glance.

## Goals

- A repo key `repo.NAME` shows the emoji of `repo:NAME`, the repo name as caption, and three counts.
- Red counts open issues, green counts open PRs, yellow counts the items of the criteria provider (0093).
- A count of 0 draws no badge. A count above 99 draws "99+".
- The top `repos` key shows its own picture and the totals of the repo keys.

## Non-goals

- Fetching the counts. 0089 writes the `repos` rows, and 0093 writes the yellow answers.
- Ordering the keys (0090) and the shell of the plugin (0087).
- Drawing three badges. 0091 adds the wire fields and the driver code. This spec only fills them.
- Caps on children and nodes (0088), and the item layer under a repo (0095).
- The github plugin's PR keys. 0087 retires them (0076).

## Approaches considered

Three approaches follow. Each one gets the counts from the store to the key in a different way.

### Approach A — The renderer joins the tables when it plans

A renderer in `inbox/src/plugins/repos.js`, like `createAppsRenderer`, reads the `repos` rows, the yellow counts and
the session status on every `reread()`. A pure function in `inbox/src/repo-key.js` turns one repo into the key fields.

- Good, because the key always shows the tables as they are now. No copy of a count exists to go stale.
- Good, because `repoNode` is a pure function. A test needs no store, no daemon and no socket.
- Bad, because the renderer repeats the open-layer rules of `createAppsRenderer`, about 60 lines.
- Bad, because each `reread()` runs a join over all repos. With 100 repos that is 100 rows every 500 ms poll that
  sees a change.

### Approach B — The feed writes one message per repo

The feed writes a message per repo with the counts as JSON in `body`, as `syncPulls` does for PRs. The generic
`createRenderer` in `inbox/src/render.js` draws it through `rules.child` and `rules.icon`.

- Good, because it reuses `createRenderer`, and with it the ordering, `MAX_CHILDREN`, and the stable open layer.
- Good, because no new renderer code exists to test.
- Bad, because every change of a count adds a row to `messages`. 100 repos that change daily add 100 rows a day.
- Bad, because a `Row` has no typed fields. The rules must parse JSON from `body`, and a yellow count that
  changes without a gh change needs a second writer.

### Approach C — The feed writes a finished `repo_keys` table

A feed step folds `repos`, yellow and session status into one `repo_keys` row per repo. The renderer only maps rows
to messages.

- Good, because the renderer is a plain loop over one table.
- Good, because the three counts of one repo change in one transaction. The key never shows a mix of old and new.
- Bad, because the counts now live in three places. Each writer must also update `repo_keys`.
- Bad, because session status changes come from hook processes (0090), not from the feed. A feed-built table lags
  them until the next feed run.

## Decision

Chosen: **Approach A — The renderer joins the tables when it plans**.

The counts come from two feeds with two intervals (0089, 0093). Only a plan-time read shows both without a third
copy. The decision accepts the 60 repeated lines and a join of up to 100 rows per change. The cost is small
at this size. A shared helper may remove the 60 lines later.

## Design

`inbox/src/repo-key.js` (new) exports pure functions:

- `repoNode({ name, issues, prs, yellow, status }, set)` returns the fields of a `setLayerKey`:
  `id: repo.NAME`, `parent: 'repos'`, `iconText: clip(name, MAX_CAPTION)` (from `inbox/src/render.js`),
  `iconEmoji: [emojiFor(componentInput(name), set).iconRef]`, and `status`.
- One emoji only. The child layout then gives the hero 80 px (`emojiLayout` in `driver/layers/emoji.go`).
  The caption is one line of at most 16 runes, and the strip (y 106-128) shows `status`.
- `status` is the worst session status in that repo: `waiting` is `WARN`, `running` is `RUNNING`, `done` is
  `DONE`, none is `''`. 0087 supplies it.
- `badge(n)` is `Math.min(n, 100)`. The driver draws any count above 99 as "99+" (`pipText` in
  `driver/layers/pip.go`). The clamp stops a message when a count moves from 150 to 160.
- The three counts go into `count` (red), `countGreen` and `countYellow`, the fields that task 0091 adds to
  `setLayerKey`. `inbox/src/protocol.js` passes them through. A count of 0 sets no field, so the full upsert resets it and the driver draws no badge.
- `topKey(repos)` returns `iconRef: 'github.png'` (it exists in `assets/`), the sums of the three counts, and the
  worst child status.

Files to change:

- `inbox/src/repo-key.js` — new
- `inbox/src/plugins/repos.js` — the renderer calls `repoNode` and `topKey`
- `inbox/test/repo-key.test.js` — new
- `inbox/README.md` — section "Repo keys"

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task
moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — `repoNode` gives id `repo.NAME`, parent `repos`, and one `iconEmoji` ref equal to
  `emojiFor('repo:NAME').iconRef`. **Proof:** `cd inbox && node --test --test-name-pattern=TestRepoKeyIcon test/repo-key.test.js`
- [ ] **DoD-2** — A name of 20 runes gives a caption of 16 runes that ends in "…". A name of 16 runes stays whole.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestRepoKeyCaptionClips test/repo-key.test.js`
- [ ] **DoD-3** — Issues 7, PRs 3 and yellow 2 give red 7, green 3 and yellow 2 in `count`, `countGreen` and `countYellow`.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestRepoKeyBadges test/repo-key.test.js`
- [ ] **DoD-4** — A count of 0 leaves its field out, and three zeros leave all three out.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestRepoKeyZeroDrawsNothing test/repo-key.test.js`
- [ ] **DoD-5** — Counts 99, 100 and 5000 give 99, 100 and 100. A change from 150 to 160 sends no message.
  **Proof:** `cd inbox && node --test --test-name-pattern='TestRepoKeyCapsAt100|TestReposPlanSendsOnlyChange' test/repo-key.test.js`
- [ ] **DoD-6** — `waiting`, `running`, `done` and none give `WARN`, `RUNNING`, `DONE` and `''`. The worst session wins.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestRepoKeyStatus test/repo-key.test.js`
- [ ] **DoD-7** — The top key has `iconRef: 'github.png'`, the totals of the three counts, and the worst status.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestRepoTopKey test/repo-key.test.js`
- [ ] **DoD-8** — With a store that holds three `repos` rows, the renderer plans three keys with the right
  counts. A change of one `prs` value plans one message. **Proof:**
  `cd inbox && node --test --test-name-pattern=TestRepoKeysFromStore test/repo-key.test.js`
- [ ] **DoD-9** — The whole inbox suite passes. **Proof:** `cd inbox && node --test`
- [ ] **DoD-10** — `inbox/README.md` records the caption, the three colours, the 0 rule and the 99+ cap.
  **Proof:** `grep -n "Repo keys" inbox/README.md`
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- 0089 names the `repos` columns `issues` and `prs` → `repoNode` takes plain fields, so only the renderer's read maps them.
- 0091 draws its three discs at y 89-123, over the hero and the strip of the repo key → the board check of 0091 decides the hero size, and this spec owns any change to it.
- 100 repos need more than the 52 frame slots of the board → the strip colour joins the frame, the counts do not.
  0088 sizes the paging.

## Open questions

- [ ] Should the top key count repos with a non-zero count, not sums? — Keith decides before DoD-7 is built.

## Notes

- Superseded by this spec: the single red pip of `github.js` `rules.top` and `rules.child`.
- Precedent for a plugin's own renderer: `createAppsRenderer` in `inbox/src/plugins/apps.js`.
