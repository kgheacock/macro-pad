---
id: "0090"
title: "An activity ledger of Keith's own events sorts the repository keys"
status: "complete"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/116"
branch: "0090-activity-ledger-and-repo-ordering"
related: ["0062", "0073", "0081", "0085", "0087", "0088", "0089", "0092"]
tags: ["inbox", "repos", "activity", "ordering", "hooks", "gh"]
---

# 0090 — An activity ledger sorts the repository keys

## Problem

With every repository on the pad (0089), the repository Keith works on may sit
on page 4. Today no file records what Keith did. `apps` reads file times
(`listApps` in `inbox/src/plugins/apps.js`) and ranks only "today".

## Goals

- A ledger records Keith's own events per repository: a Claude prompt, VS Code opened from the apps key, local git use, a push, a PR review, and a PR opened.
- A rule turns the ledger into a score. The repository keys sort by it, newest work first.
- Repositories with no event still show, after the active ones, by name.
- While the repos layer or a layer under it is open, no key moves.

## Non-goals

- The list of repositories and its counts (0089). This task needs `repoKey` from 0089, so it lands second.
- The key drawing (0087, 0092) and the pad paging (0088). They call `orderRepos` and `nextShown`.
- Events of other people. The ledger holds only the signed-in user's events.

## Approaches considered

Three approaches follow. They differ in what the ranking reads.

### Approach A — One `active_at` column on `repos`

Each source runs `UPDATE repos SET active_at = max(active_at, :at)`. The newest event wins.

- Good, because it adds one column and no table or prune job.
- Good, because the sort is one `ORDER BY active_at DESC`.
- Bad, because one Claude prompt outranks five pushes. The kind is lost, so no weight can fix it later.
- Bad, because `replaceRepos` (0089) deletes the row of a repository that leaves the list, and its history goes too.

### Approach B — An `activity` ledger with a decaying score

A table `activity(repo, kind, ref, at)` holds one row per event. A pure function sums weights that halve every 3 days.

- Good, because the kind stays. A weight change re-ranks old events with no new data.
- Good, because `(repo, kind, ref)` is the key, so a re-read of the same gh event is idempotent.
- Bad, because it adds schema 7, a prune step, and a writer in four places.
- Bad, because the weights are a guess. They need a week of use to tune.

### Approach C — Derive the order at read time, no new store

Read `pushed_at` from the 0089 list, `.git/logs/HEAD` times from the apps root, and ai-plugin rows.

- Good, because it needs no hook change, no migration, and no retention.
- Good, because it costs nothing on a prompt.
- Bad, because `pushed_at` counts pushes by other people. It cannot show a PR review or VS Code opened.
- Bad, because 0087 retires the ai plugin, and its rows stop with it. Claude work leaves the order.

## Decision

Chosen: **Approach B — an `activity` ledger with a decaying score**.

The user asked for four kinds of event, and only a table with a kind keeps them
apart. The decision accepts a migration, a node process per Claude prompt, and
guessed weights. The hook is `async`, so Claude Code does not wait.

## Design

New `inbox/src/activity.js` exports:

- `KINDS` and `WEIGHTS`: `pr_review` 3, `pr_open` 3, `push` 3, `claude` 2, `code` 2, `git` 1.
- `HALF_LIFE_MS` (3 days) and `KEEP_MS` (30 days).
- `scoreRepos(rows, now)`: map of `repoKey` to `{ score, last }`. A row counts `weight * 0.5 ** ((now - at) / HALF_LIFE_MS)`.
- `compareRepos(a, b)`: fits `before(a, b)` of `inbox/src/render.js`. Order: score high, `last` new, `name` ascending. No event means score 0 and `last` 0.
- `orderRepos(repos, scores)`: `repos` sorted by `compareRepos`.
- `nextShown({ shown, wanted, open, max })`: when `open`, `shown` plus new names at the end, up to `max`; else `wanted`.
- `parseEvents(text, { owner })`, `fetchEvents`, `headLogTime(dir)`.

Schema 7 adds `activity(repo, kind, ref, at, PRIMARY KEY (repo, kind, ref))`. No
`plugin` column. `Store.noteActivity(repo, kind, ref, at)` keeps the newest `at`.
`Store.activityRows(since)` and `Store.pruneActivity(before)` complete it.

Sources and refs:

- `claude`: `claude-ai-inbox.sh` runs `inbox activity note --repo NAME --kind claude --ref SID` on `prompt` only. NAME is the basename of `git rev-parse --show-toplevel`. SID is 8 hex. One row per session, `at` moves on.
- `code`: `press` in `inbox/src/plugins/apps.js` notes it on action `code`. Ref is the date.
- `git`: the repos feed reads `.git/logs/HEAD` under `INBOX_APPS_ROOT`. It skips `.git/index`, which `git status` rewrites. Ref is the date.
- `push`, `pr_open`, `pr_review`: `fetchEvents` runs `gh api user --jq .login` once, then `gh api "users/LOGIN/events?per_page=100"`. The first run reads 3 pages. Ref is the event id. Events of repositories outside `INBOX_REPOS_OWNER` drop out.

The repos feed (0089) adds `fetchEvents`; its failure is logged and never
fails the list. `sync` writes the ledger, deletes rows older than `KEEP_MS`, and
sets `answer.scores`. The order moves within one feed run, 60 s, of a write.

Files to change: `inbox/src/activity.js` (new), `inbox/src/store.js`,
`inbox/src/cli.js` (`activity note`), `inbox/src/repolist.js`,
`inbox/src/plugins/apps.js`, `inbox/claude-code/claude-ai-inbox.sh`,
`inbox/README.md`, `inbox/claude-code/README.md`, `inbox/completions/_inbox`.

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — A schema 6 file opens, gains `activity`, and keeps `repos` and
  `messages`. **Proof:** `cd inbox && node --test --test-name-pattern TestActivityMigration test/store.test.js`
- [x] **DoD-2** — `noteActivity` twice with one `(repo, kind, ref)` leaves one row with the newer `at`.
  **Proof:** `cd inbox && node --test --test-name-pattern TestNoteActivityUpsert test/activity.test.js`
- [x] **DoD-3** — `inbox activity note --repo macro-pad --kind claude --ref 3f2a91c0` writes one row and exits 0.
  An unknown kind exits 2. **Proof:** `cd inbox && node --test --test-name-pattern TestActivityNoteCli test/cli.test.js`
- [x] **DoD-4** — The hook notes one `claude` row on `prompt` and none on `tool`.
  **Proof:** `cd inbox && node --test --test-name-pattern TestHookNotesActivityOnPrompt test/cli.test.js`
- [x] **DoD-5** — Pressing `code` on an app notes `code`. A `.git/index` time alone notes nothing; a
  `.git/logs/HEAD` time notes `git`. **Proof:** `cd inbox && node --test --test-name-pattern 'TestCodePressNotesActivity|TestGitLogNotesActivity' test/apps.test.js`
- [x] **DoD-6** — `parseEvents` maps `PushEvent`, an opened `PullRequestEvent`, and `PullRequestReviewEvent`.
  It drops other owners and other types. **Proof:** `cd inbox && node --test --test-name-pattern TestParseEvents test/activity.test.js`
- [x] **DoD-7** — `fetchEvents` asks for the events of the signed-in login, not of the owner.
  **Proof:** `cd inbox && node --test --test-name-pattern TestEventsUseSignedInLogin test/activity.test.js`
- [x] **DoD-8** — A push 3 days old scores half a push now. A review outranks a `git` row of the same age.
  **Proof:** `cd inbox && node --test --test-name-pattern TestScoreDecaysByHalfLife test/activity.test.js`
- [x] **DoD-9** — `orderRepos` puts repositories with events first, then the rest by name. The
  result is equal for any input order. **Proof:** `cd inbox && node --test --test-name-pattern TestOrderReposNoActivityLast test/activity.test.js`
- [x] **DoD-10** — `nextShown` with `open: true` keeps `shown` and adds new names at the end.
  **Proof:** `cd inbox && node --test --test-name-pattern TestNextShownFreezesWhileOpen test/activity.test.js`
- [x] **DoD-11** — `pruneActivity` deletes rows older than 30 days, and an events failure leaves the ledger as it was.
  **Proof:** `cd inbox && node --test --test-name-pattern 'TestPruneActivity|TestEventsFailureKeepsLedger' test/activity.test.js`
- [x] **DoD-12** — The READMEs record the kinds, weights, half-life, and retention; the PR links this spec.
  **Proof:** `inbox/README.md` section "Activity"; the PR in the `pr` field

## Risks

- The weights rank wrongly → they are constants in one file, and the ledger keeps the kind, so a change needs no migration.
- The events API lags up to a few minutes, and keeps 300 events or 90 days → pushes can show late; the ledger lasts 30 days.
- A same-named repository of another owner scores the local rows → the join with `repos` (0089) uses the configured owner only.
- Ordering while closed moves many keys → `render.js` resends from the first changed key only.

## Open questions

- [ ] Do the weights fit one week of real use? — Keith, after the first week.
- [ ] Does `claude` count every prompt, or one row per session? — This spec: one row per session.

## Notes

- `users/LOGIN/events` shows private events only to the signed-in user, so the login comes from `gh api user`. An organisation owner has no such events.
- `git push` writes no `.git/logs/HEAD` line, so a local push reaches the ledger through gh.
