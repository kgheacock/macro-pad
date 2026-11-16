---
id: "0089"
title: "A repos feed lists every repository of one owner through gh, with open issue and PR counts"
status: "complete"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/115"
branch: "0089-repo-list-feed-from-gh"
related: ["0075", "0076", "0081", "0085", "0087", "0088", "0090", "0092"]
tags: ["inbox", "repos", "github", "gh", "feed", "store"]
---

# 0089 — A repos feed lists every repository of one owner through gh

## Problem

The inbox knows only the repositories that have a Claude session or an open PR.
It has no list of all repositories. The `repos` plugin (0087) needs one key per
repository, with the open issue count and the open PR count (0092).

## Goals

- `INBOX_REPOS_OWNER` names the owner, a user or an organisation. The default is `kgheacock`.
- The daemon lists all repositories of the owner every 60 s, also when there are more than 100.
- The store keeps, per repository, the open issue count (PRs excluded) and the open PR count.
- When `gh` fails, the last list stays and the daemon logs one line.

## Non-goals

- The key, its emoji, and its badges. 0087 and 0092 own them.
- Sorting. 0090 owns it. This feed returns repositories by name.
- Yellow counts (0093) and the items under a repository (0095).
- Pagination of the pad (0088). Retiring `github.js` is 0087's work.

## Approaches considered

Three approaches follow. They differ in who lists the repositories and who counts.

### Approach A — One `gh repo list` call

Run `gh repo list OWNER --limit 1000 --no-archived --json ...`. The fields
`issues` and `pullRequests` hold open counts.

- Good, because one call returns names, forks, archive flags, and both counts.
  I checked it: `kgheacock` gives 67 rows, and `macro-pad` shows 5 open PRs of 112 in all.
- Good, because gh pages by itself. `gh repo list github --limit 150` returned
  150 rows, and an unknown owner gives one clear error.
- Bad, because the field set is gh's, not ours. A gh release can rename a
  field, so a fixture test must pin the shape.
- Bad, because `--limit 1000` is a hard stop. An owner with more repositories
  loses the rest, so the feed must detect 1000 rows and refuse to delete.

### Approach B — Our own GraphQL query

Run `gh api graphql` with `repositoryOwner(login:) { repositories(first: 100, after:) }`,
as `inbox/src/plugins/github.js` does for PRs. Loop on the cursor.

- Good, because we choose every field and filter, such as `ownerAffiliations`.
- Good, because it matches the existing `GRAPHQL` and `parseAnswer` style.
- Bad, because we write and test the cursor loop. One gh process runs per 100 repositories.
- Bad, because a failure on page 2 leaves a partial list. The feed must discard it.

### Approach C — REST list plus a count per repository

Run `gh api --paginate --slurp users/OWNER/repos`, then count PRs per repository.

- Good, because REST fields are stable and documented.
- Good, because it needs no GraphQL cost budget.
- Bad, because `open_issues_count` includes PRs. The PR split needs one more
  call per repository: 67 calls per minute now.
- Bad, because `users/OWNER/repos` lists public repositories only. Keith has
  53 private ones of 67, so the feed must learn if the owner is a user or an organisation.

## Decision

Chosen: **Approach A — one `gh repo list` call**.

It is the only approach that gives names and both counts in one call, for a
user or an organisation. The decision accepts that gh owns the field names
and that 1000 repositories is the limit. The feed marks a 1000-row answer as
cut and deletes nothing.

## Design

New `inbox/src/gh.js` holds `runGh` (moved from `inbox/src/plugins/github.js`) and
`GH_TIMEOUT_MS`; `github.js` imports them. New `inbox/src/repolist.js` holds:

- `reposOwner(env)`: `INBOX_REPOS_OWNER`, else `kgheacock`. A name that is not
  `/^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/` throws, so no value can look like a flag.
- `repoKey(name)`: lower case, `.` becomes `-`, as `headRepo` does in `github.js`. 0090 uses it.
- `fetchRepos({ env, execFile })`: runs `gh` (`INBOX_GH`) with `--no-archived`,
  unless `INBOX_REPOS_ARCHIVED=1`. Forks stay in.
- `parseRepos(text)` returns `{ repos: [{ name, key, url, fork, archived, pushedAt, issues, prs }], cut }`.
- `feed`: `{ source: 'GitHub repositories', everyMs: 60000, fetch, sync, cold }`.

Schema 6 adds `repos(owner, name, key, url, is_fork, is_archived, pushed_at, issues,
prs, synced_at, PRIMARY KEY (owner, name))`. It has no `plugin` column, as
`branch_sessions` has none. `Store.replaceRepos(owner, repos, { complete })`
upserts in one transaction. It deletes the rows that the answer lacks only when
`complete` is true. `Store.listRepos(owner)` returns rows by name.

`sync` returns the count of changed rows. The answer is
`{ owner, repos, offline: null }`. In `inbox/src/daemon.js`, `runFeed` calls
`feed.cold({ env, store })` once when the first `fetch` fails and `fed` is
null. `cold` returns the stored rows with `offline` set to the error, so a
restart without network still draws.

Files to change:

- `inbox/src/gh.js` (new), `inbox/src/repolist.js` (new)
- `inbox/src/store.js` — migration 5 to 6, `replaceRepos`, `listRepos`
- `inbox/src/daemon.js` — the `cold` call in `runFeed`
- `inbox/scripts/install.sh` — `--repos-owner OWNER`, written to the wrapper
- `inbox/README.md` — Environment table and a "Repositories" section
- `inbox/test/repolist.test.js` (new), `inbox/test/store.test.js`, `inbox/test/daemon.test.js`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — A file at schema 5 opens, gains the `repos` table, and keeps its
  messages. **Proof:** `cd inbox && node --test --test-name-pattern TestReposMigration test/store.test.js`
- [x] **DoD-2** — `fetchRepos` runs `gh repo list OWNER --limit 1000 --no-archived --json`
  with the owner from `INBOX_REPOS_OWNER`, and `kgheacock` when it is unset.
  **Proof:** `cd inbox && node --test --test-name-pattern TestFetchReposArgv test/repolist.test.js`
- [x] **DoD-3** — An owner such as `-x` or `a b` throws before any process starts.
  **Proof:** `cd inbox && node --test --test-name-pattern TestReposOwnerRejected test/repolist.test.js`
- [x] **DoD-4** — `parseRepos` gives the open issue count and open PR count of each
  repository, and `cut: true` for a 1000-row answer.
  **Proof:** `cd inbox && node --test --test-name-pattern TestParseRepos test/repolist.test.js`
- [x] **DoD-5** — A sync with a repository gone deletes its row. A `cut` sync
  deletes nothing. **Proof:** `cd inbox && node --test --test-name-pattern TestReplaceRepos test/store.test.js`
- [x] **DoD-6** — When `gh` exits 1, `runFeed` logs one line and the stored rows
  stay. A second equal failure logs nothing. **Proof:**
  `cd inbox && node --test --test-name-pattern TestReposFeedOffline test/daemon.test.js`
- [x] **DoD-7** — A daemon that starts with `gh` failing and a filled table draws
  from `cold`. **Proof:** `cd inbox && node --test --test-name-pattern TestReposColdStart test/daemon.test.js`
- [x] **DoD-8** — The live call returns more rows than gh's default of 30.
  **Proof:** `gh repo list kgheacock --limit 1000 --no-archived --json name | jq length` prints 30 or more
- [x] **DoD-9** — `install.sh --dry-run --repos-owner acme` prints `INBOX_REPOS_OWNER=acme`.
  **Proof:** run it and read the "would write" line
- [x] **DoD-10** — `inbox/README.md` lists `INBOX_REPOS_OWNER`, `INBOX_REPOS_ARCHIVED`,
  and the 60 s feed. **Proof:** `inbox/README.md`, sections "Environment" and "Repositories"
- [x] **DoD-11** — The PR body links to this spec. **Proof:** the PR in the `pr` field

## Risks

- A gh release renames `issues` or `pullRequests` → `TestParseRepos` uses a fixture from gh 2.92.
- Two daemons write `repos` at once → `replaceRepos` runs in `BEGIN IMMEDIATE`, as the other writers do.
- The 0090 migration collides in number → 0089 takes 5 to 6; 0090 takes 6 to 7 and lands after this task.

## Open questions

- [ ] Do archived repositories stay hidden by default? — Keith. The brief says "all"; this spec hides them.
- [ ] How does 0087 write a key id for `keithheacock.com`? — 0087. This spec stores the raw name and `key`.

## Notes

- `gh repo list --json issues` counts open issues only. `pullRequests` counts open PRs. Both checked on 2026-11-16.
- The 60 s rate cost is one GraphQL call per 100 repositories per minute.
