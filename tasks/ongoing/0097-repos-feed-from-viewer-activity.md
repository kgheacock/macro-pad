---
id: "0097"
title: "The repos feed lists the repositories the viewer works on, and fetches counts only for the shown ones"
status: "ongoing"
created: "2026-11-20"
updated: "2026-11-21"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-r806g0"
related: ["0085", "0087", "0089", "0090", "0092"]
tags: ["inbox", "repos", "github", "gh", "feed", "graphql"]
---

# 0097 — The repos feed lists the viewer's repositories, with counts for the shown ones

## Problem

The repos feed runs `gh repo list <owner> --limit 1000` with issue and PR
counts every 60 s (`inbox/src/repolist.js`). For a large organisation this
fails. ExampleOrg has over 27,000 repositories. A 1,000-repo list takes 83.5 s,
but the `gh` timeout is 30 s (`GH_TIMEOUT_MS`). Every fetch fails, so the pad
keeps showing the stale stored list.

## Goals

- The feed finishes in seconds for an owner of any size.
- The candidate list grows with the viewer's activity, not with the owner's
  repository count.
- Issue and PR counts are fetched only for the repositories that the pad shows
  (at most `REPO_MAX_CHILDREN`, 128).

## Non-goals

- A change to how the repos key, the repo layer, or the item keys look.
- A change to the activity score (`scoreRepos`) or to the ledger schema.
- Repositories of more than one owner.

## Approaches considered

### Approach A — Raise the timeout and the interval

Keep the full listing. Set the `gh` timeout to 120 s and the interval to 5 min.

- Good, because it changes two constants and no logic.
- Good, because a small owner (kgheacock) behaves as before.
- Bad, because 1,000 repos still take about 84 s per fetch. A 5k limit takes
  about 7 min.
- Bad, because the cut at 1,000 follows the org's pushes, not the viewer's. An
  active repo can fall out of the list.

### Approach B — Candidates from the viewer's activity, counts for the shown repos

A cheap step builds a candidate set from the viewer's activity. An expensive
step fetches counts only for the top 128 candidates.

- Good, because the cheap step is one GraphQL call (cost 1, measured: 46 repos).
- Good, because its cost follows the viewer's work, so org growth does not
  change it.
- Bad, because a repo that the viewer never touched does not appear.
- Bad, because it adds a second GraphQL query and a second interval to the
  feed.

### Approach C — Local clones only

List the repos cloned under `$INBOX_APPS_ROOT`, then fetch counts for them.

- Good, because the list needs no API call. `gitEvents` already scans these
  folders.
- Good, because each repo on the pad has a local folder for the apps actions.
- Bad, because a repo with PR reviews but no clone does not appear.
- Bad, because the list grows with clones. Old clones never leave.

## Decision

Chosen: **Approach B — Candidates from the viewer's activity**.

The goal is a cost that does not grow with the owner. B meets it and keeps
review-only and clone-only repos. It accepts a second query and a second
interval.

## Design

1. Candidates, every `REPOS_EVERY_MS` (60 s). Take the union of these sources.
   Keep only repos of `INBOX_REPOS_OWNER`.
   - `viewer.repositoriesContributedTo(contributionTypes: [COMMIT, PULL_REQUEST,
     PULL_REQUEST_REVIEW, ISSUE], includeUserRepositories: true, orderBy: PUSHED_AT DESC)`,
     paged by 100.
   - Repo folders under `$INBOX_APPS_ROOT` (from `gitEvents`).
   - Repos in the activity ledger (GitHub events, Claude sessions).
2. Rank the candidates with `scoreRepos`. Keep the top `REPO_MAX_CHILDREN`.
3. Counts, every `REPOS_COUNTS_EVERY_MS` (new, default 5 min). Send one aliased
   GraphQL query per 50 repos:
   `rN: repository(owner, name) { url isFork isArchived pushedAt issues(states: OPEN) {
   totalCount } pullRequests(states: OPEN) { totalCount } }`.
   A missing or archived repo is dropped.
4. `syncRepos` stores the shown set. A repo that leaves the set is removed.

Files to change:

- `inbox/src/repolist.js` — replace `fetchRepos` with the candidate and count
  steps.
- `inbox/src/gh.js` — a GraphQL helper, if `runGh` needs one.
- `inbox/src/plugins/repos.js` — update the `about` help text.
- `inbox/test/repolist.test.js` — tests for both steps.
- `inbox/README.md` — document the sources and `INBOX_REPOS_COUNTS_EVERY`.

## Definition of done

- [ ] **DoD-1** — With `INBOX_REPOS_OWNER=ExampleOrg`, a feed run finishes in
  under 10 s. **Proof:** time `fetchFeed` from a one-line `node -e` script.
- [ ] **DoD-2** — The repos layer shows ExampleOrg repos that the viewer works on,
  for example `example-service`. **Proof:** `jq '.keys[1].children[].id'` on
  `~/Library/Application Support/macro-pad/layers.json`.
- [ ] **DoD-3** — No feed run calls `gh repo list`. **Proof:**
  `grep -n "'repo', 'list'" inbox/src` returns nothing.
- [ ] **DoD-4** — A run asks for counts of at most 128 repos, in batches of at
  most 50. **Proof:** a test in `inbox/test/repolist.test.js` with 200 candidates.
- [ ] **DoD-5** — A repo that is only a local clone, with no contribution,
  appears in the candidates. **Proof:** a test in `inbox/test/repolist.test.js`.
- [ ] **DoD-6** — A failed count query keeps the last stored counts and logs one
  line. **Proof:** a test in `inbox/test/repolist.test.js`.
- [ ] **DoD-7** — `make test` passes. **Proof:** CI on the PR.
- [ ] **DoD-8** — `inbox/README.md` names the three candidate sources and the
  counts interval. **Proof:** the README diff.
- [ ] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:** PR body.

## Risks

- `repositoriesContributedTo` covers about one year and skips private-org
  contributions that are hidden on the profile → the clone scan and the ledger
  fill the gap.
- A GraphQL cost spike on a large batch → batches of 50 and a 5 min interval
  keep a run under 10 points.

## Notes

Measured on 2026-11-20 with `gh` 2.x against ExampleOrg:

- `gh repo list --limit 1000` with counts: 83.5 s.
- `organization.repositories(orderBy: PUSHED_AT)` page of 100: 1.7 s, cost 1.
  The newest 100 pushes spanned 17 min, so org order does not follow the viewer.
- `viewer.repositoriesContributedTo`: 46 repos, one call, cost 1.
