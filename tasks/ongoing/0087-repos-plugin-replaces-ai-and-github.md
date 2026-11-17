---
id: "0087"
title: "A repos inbox plugin: one key per repo replaces the ai and github plugins"
status: "ongoing"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0087-repos-plugin-replaces-ai-and-github"
related: ["0071", "0072", "0075", "0076", "0081", "0085", "0086", "0088", "0089", "0090", "0092", "0095"]
tags: ["inbox", "plugin", "repos", "migration"]
---

# 0087 — A repos inbox plugin: one key per repo replaces the ai and github plugins

## Problem

The `ai` plugin draws one key per Claude session (task 0072). The `github` plugin draws one key per open PR (task 0076). Neither answers "what is going on in repo X", and both lists grow without bound. Keith wants one key per repo, under one top key.

## Goals

- A plugin `repos` has one top key, `repos`, and one child per repo: id `repo.NAME`, the name as caption, the emoji of `repo:NAME`.
- Every repo in the feed shows, also a repo with no activity, in the order the feed gives.
- A Claude session still colours its repo key and the top key: waiting, running, done.
- `ai` and `github` leave the code, the CLI, the installer, and the pad. No row or key is left behind.

## Non-goals

- The repo list and its order. Task 0089 (`gh` list) and task 0090 (activity order) come first; this task attaches their feed.
- Counts and badges (tasks 0091, 0092), yellow items (0093), and the item layer (0095).
- The caps and the `opens` flag. Task 0088 comes first.
- PR check colours (failing, pending, passing). They end with the `github` plugin.

## Approaches considered

Three approaches follow. They differ in what happens to the old plugins and their rows.

### Approach A — Add beside, hide the old

Register `repos`. Keep `ai` and `github` in `PLUGINS`, marked hidden so the daemon does not start them. Hooks still send `--plugin ai`. `repos` reads those rows with `store.forPlugin('ai')`, as `apps` does now.

- Good, because it needs no schema change, and the installed hook keeps working untouched.
- Good, because a rollback is one flag.
- Bad, because about 360 lines (`ai.js`, `github.js`) and their two test files stay, and the README keeps documenting plugins nobody runs.
- Bad, because the session store keeps the name `ai` for good, so `--plugin ai` and `--plugin repos` both exist.

### Approach B — Replace, migrate the rows

Delete `ai.js` and `github.js`. `repos` owns the session rows. Schema 8 renames `ai` rows to `repos` and deletes `github` rows. The name `ai` stays only as an alias.

- Good, because one plugin owns sessions and repos, and the dead code goes.
- Good, because today's session rows survive, so `apps` keeps its "active today" data.
- Bad, because schema 8 makes an older checkout refuse the file (`newerSchema` in `inbox/src/store.js`).
- Bad, because 68 lines of `inbox/test/cli.test.js` mention `ai` or `github`, and tests in `daemon.test.js` and `render.test.js`, need a rewrite.

### Approach C — Headless sources, `repos` as a view

Keep `ai` and `github` as plugins that write rows and draw no key. `repos` only reads their rows.

- Good, because hooks, the `gh` feed, and their tests stay as they are.
- Good, because the sources can change without touching the view.
- Bad, because the daemon needs a new "no renderer" mode, one loop per plugin (`inbox/src/cli.js`).
- Bad, because `github` polls `author:@me` PRs beside the repo feed of task 0089. That is two `gh` loops for one answer.

## Decision

Chosen: **Approach B — Replace, migrate the rows**.

Keith is the only client, so a clean cut costs less than two plugin names for one idea. The task accepts a schema bump, a test rewrite, and the loss of the PR check colours. The `ai` alias keeps an installed hook alive, because the hook hides its errors.

## Design

`inbox/src/plugins/repos.js` exports a plugin like `inbox/src/plugins/apps.js`:

- `name: 'repos'`, `statuses: ['waiting','running','done']`, `defaultStatus: 'done'`, `rules: { id: 'repos', activeMs: 3600000 }`.
- `feed`: the object of task 0089, giving `fed.repos` in the order of task 0090.
- `createRenderer({ slot, env, log })` returns `createReposRenderer`, a copy of the pattern of `createAppsRenderer`.

Constants that other specs reuse move from `ai.js` into `repos.js`: `ACTIVE_MS`, `rank`, and the status map. The renderer sends `{ id: 'repos', iconRef: 'github.png', status, count }`. For each repo it sends `{ id: 'repo.NAME', parent: 'repos', opens: true, iconText, iconEmoji, status }`. A session maps to its repo with `componentOf(splitSessionChannel(channel).folder)` from `inbox/src/emoji.js`, compared in lower case with dots as `-`. The repo status is the highest unseen session status, as `rank()` in the old `ai.js`. It sends at most `REPO_MAX_CHILDREN` (task 0088) repos and logs the cut once. While the `repos` layer is open, no key moves. A new repo goes last. On the first connect it sends `removeLayerKey` for `ai-inbox`, `github-inbox`, and `repos`, so no key stays in `layers.json`.

Store, schema 8 (0089 takes 6 and 0090 takes 7, so this task lands after both): `UPDATE messages SET plugin='repos' WHERE plugin='ai'`, the same for `channel_here`, and delete the `github` rows. `branch_sessions` has no plugin and stays. `getPlugin('ai')` returns `repos`.

The hook file keeps its name and its `settings-hooks.json`. Its two `--plugin ai` words become `--plugin repos`.

Files to change:

- `inbox/src/plugins/repos.js` — new; `index.js` — `PLUGINS = { basic, repos, apps }`, alias
- `inbox/src/plugins/ai.js`, `github.js`, `inbox/test/ai.test.js`, `github.test.js` — delete
- `inbox/src/store.js` — schema 8; `inbox/src/plugins/apps.js` — `forPlugin('repos')`
- `inbox/src/daemon.js` — pass `log` to `createRenderer`; `inbox/src/cli.js` — help text
- `inbox/scripts/install.sh` — drop `--github-query`; keep `INBOX_GH` for task 0089
- `inbox/claude-code/claude-ai-inbox.sh`, `README.md`; `inbox/README.md` — new "The repos plugin"
- `inbox/test/repos.test.js` — new; `cli`, `daemon`, `render`, `apps`, `store` tests — update

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task moves to `complete/` only when every box is ticked.

- [x] **DoD-1** — `inbox plugins` lists `basic`, `repos`, `apps` and no other. The four deleted files are gone. **Proof:** `cd inbox && node --test test/cli.test.js --test-name-pattern 'plugins lists'`, then `ls src/plugins`
- [x] **DoD-2** — For a feed of three repos, `plan` sends the top key `repos` and children `repo.NAME` with `parent: 'repos'`, `opens: true`, the name as `iconText`, and the emoji of `repo:name`, in feed order. **Proof:** `cd inbox && node --test test/repos.test.js --test-name-pattern 'one repo child per fed repo'`
- [x] **DoD-3** — A feed of 100 repos with no session draws 100 children. A feed of 130 draws 128 and logs one line. **Proof:** `cd inbox && node --test test/repos.test.js --test-name-pattern 'every repo|cuts a feed'`
- [x] **DoD-4** — A waiting session sets `WARN` on its repo key and the top key. A session of a repo not in the feed draws no key. **Proof:** `cd inbox && node --test test/repos.test.js --test-name-pattern 'waiting session|unknown repo'`
- [x] **DoD-5** — The first `plan` after a connect removes `ai-inbox`, `github-inbox`, and `repos` once. A second `plan` does not. **Proof:** `cd inbox && node --test test/repos.test.js --test-name-pattern 'stale keys'`
- [x] **DoD-6** — While the `repos` layer is open, no repo key moves or goes, and a new repo goes last. **Proof:** `cd inbox && node --test test/repos.test.js --test-name-pattern 'layer is open'`
- [x] **DoD-7** — Opening a version 7 store with `ai` and `github` rows gives `repos` rows for the first, no row for the second, and `user_version` 8. **Proof:** `cd inbox && node --test test/store.test.js --test-name-pattern 'schema 8'`
- [x] **DoD-8** — The hook has no `--plugin ai`. An old hook that sends `--plugin ai` writes a `repos` row. **Proof:** `grep -c -- '--plugin ai' inbox/claude-code/claude-ai-inbox.sh` prints `0`, and `cd inbox && node --test test/cli.test.js --test-name-pattern 'alias'`
- [x] **DoD-9** — `apps` counts a session row of `repos` as activity today. **Proof:** `cd inbox && node --test test/apps.test.js`
- [x] **DoD-10** — `sh inbox/scripts/install.sh --dry-run --github-query x` exits 2 and prints `unknown option`. **Proof:** run it, then `echo $?`
- [x] **DoD-11** — `inbox/README.md` has a section "The repos plugin" and no section "The ai plugin" or "The github plugin". **Proof:** `grep -n '^### The' inbox/README.md`
- [x] **DoD-12** — The suite passes, and the PR in the `pr` field links to this spec. **Proof:** `cd inbox && npm test`, and the PR body

## Risks

- An old hook copy ignores the new name and stops reporting → the `ai` alias keeps it alive until Keith copies the new script.
- A service file made by an old `install.sh` still says `--plugin ai` or `--slot ai=2` → run `install.sh` again. Its text says so.
- A session in a same-named repo of another owner maps to this key, because `componentOf` reads the name only (task 0081) → accepted; the owner scope of task 0089 limits it.

## Notes

- This task replaces the key models of tasks 0072 and 0076. The session id in the hook (0072) stays. Both specs stay in `ongoing/` until Keith closes them.
- `sessionsToday` in `inbox/src/plugins/apps.js` reads `store.forPlugin('ai')`. It must read `repos`.
