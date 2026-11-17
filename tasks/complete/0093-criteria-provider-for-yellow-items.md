---
id: "0093"
title: "A client writes its own yellow criteria as a command that the feed runs per repo"
status: "complete"
created: "2026-11-16"
updated: "2026-11-16"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/118"
branch: "0093-criteria-provider-for-yellow-items"
related: ["0064", "0075", "0076", "0087", "0089", "0090", "0091", "0092", "0095"]
tags: ["inbox", "repos", "provider", "jira", "secrets"]
---

# 0093 — A criteria provider for the yellow items

## Problem

The yellow count on a repo key (0092) and the yellow items under it (0095) come from criteria that the person
defines, for example the JIRA tickets that are open for a repo. No such source exists. The inbox has no config
file, only env vars that `inbox/scripts/install.sh` writes into a wrapper script. That wrapper has mode 755, so a
JIRA token must never go in it.

## Goals

- The person points the inbox at one program. For each repo it returns the yellow items as `[{ id, title, url }]`.
- The yellow count of a repo is the length of that list.
- A slow or broken provider never blanks a count, stops the gh feed, or hangs the daemon.
- A token stays outside the repo, the wrapper, and the store.

## Non-goals

- The `repos` rows and the gh feed (0089), the badge on the key (0091, 0092), and the item layer and de-dupe (0095).
- A built-in JIRA client. The repo ships one example script only.
- A second provider for the green criteria. Green stays open PRs.

## Approaches considered

Three approaches follow. All keep the fixed answer shape `[{ id, title, url }]`.

### Approach A — An external command named by an env var

`INBOX_REPOS_YELLOW_CMD` is the path of a program. The feed runs it once per repo with the repo name as `argv[1]`.
It prints a JSON array on stdout. This is the way `runGh` runs `INBOX_GH` in `inbox/src/plugins/github.js`.

- Good, because the provider can be a shell, Python, or `curl` script. A bug or a hang kills one process, not the daemon.
- Good, because the program reads its own token from the Keychain or a mode 600 file. The daemon never holds it.
- Bad, because it spawns one process per repo per interval. 100 repos make 100 spawns and, without a cache in the
  script, 100 JIRA calls.
- Bad, because the person must write and test a script. The feed can only check the output shape.

### Approach B — A JS module loaded in the daemon

`INBOX_REPOS_YELLOW_MODULE` is a path. The feed calls `import()` once and then `default(repo, ctx)` for each repo.

- Good, because one logged-in JIRA client serves all repos, with no spawn and no JSON parse.
- Good, because a test calls the function with no process.
- Bad, because the code runs in the daemon next to the store and the pad socket. A loop that never yields
  freezes the 500 ms `poll` of `inbox/src/daemon.js`.
- Bad, because a timeout stops the wait, not the work. A changed module needs a daemon restart, because
  `import()` caches it.

### Approach C — A declarative rule over gh data

`INBOX_REPOS_YELLOW_RULE` holds a regex, such as `title:\b[A-Z]+-\d+\b`. The feed applies it to the PR titles,
branch names or issue titles that 0089 already holds.

- Good, because it needs no code, no network call, and no secret.
- Good, because the count moves as soon as the gh feed moves.
- Bad, because it finds only a ticket that already has a PR, a branch or an issue. A ticket with none, which is
  what yellow is for, never counts.
- Bad, because a regex with a quote breaks the `printf` in `write_wrapper` of `install.sh`.

## Decision

Chosen: **Approach A — An external command named by an env var**.

Only a separate process can be killed on a hang, and only it keeps a token away from the daemon. The decision
accepts one spawn per repo per interval: with concurrency 4 and 300 s, 100 repos make one spawn per 3 s. A script
that needs fewer JIRA calls must cache them itself.

## Design

`inbox/src/plugins/repos-yellow.js` (new) exports `yellowFeed`, an object in the shape of the feed of `github.js`:
`{ source: 'yellow provider', everyMs: 30000, fetch, sync }`.

- With no `INBOX_REPOS_YELLOW_CMD`, `fetch` returns nothing and logs nothing.
- `fetch({ env, store })` picks repos whose `tried_at` is older than `INBOX_REPOS_YELLOW_EVERY` seconds
  (default 300, at least 30). It runs the command through `execFile` with `shell: false`, four at a time.
- The command gets the repo name as `argv[1]` and as `INBOX_REPO`. The timeout is `INBOX_REPOS_YELLOW_TIMEOUT`
  seconds, default 20. The output limit is 1 MiB.
- `parseItems(text)` accepts a JSON array. It drops an item whose `id` is not `^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$`
  or whose `url` is not `https://`. It drops a repeated `id` and keeps 200 items. A title is cut to 200 characters.
- `sync(store, answer, { log })` writes the table below. On a failure it keeps `items`, sets `tried_at` and `error`,
  and logs the line once per distinct `error`. The repo waits one interval.

The store gets table `yellow_answers(repo TEXT PRIMARY KEY, items TEXT NOT NULL, checked_at INTEGER NOT NULL,
tried_at INTEGER NOT NULL, error TEXT)` in schema 9 of `inbox/src/store.js`. Specs 0089, 0090 and 0087 take schemas 6, 7 and 8, so this task lands after them. `items` is JSON.
`yellowCounts(now)` and `yellowItems(repo, now)` ignore an answer older than 24 h. 0092 and 0095 read them.

`inbox/src/daemon.js` accepts `plugin.feed` as one object or a list. Each feed gets its own `feeding` flag, timer
and failure text, so the gh feed and this feed fail apart. Task 0089's `cold` hook stays per feed object, so each feed in the list may define its own. It passes `{ log }` as the third argument of `sync`.

Files to change:

- `inbox/src/plugins/repos-yellow.js` — new
- `inbox/src/store.js` — migration and three methods
- `inbox/src/daemon.js` — a list of feeds, `log` for `sync`
- `inbox/scripts/install.sh` — flag `--yellow-cmd PATH`, no token flag
- `inbox/examples/yellow-jira.sh` — new, reads a token from the Keychain
- `inbox/test/yellow.test.js` — new
- `inbox/README.md` — section "Yellow criteria"

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task
moves to `complete/` only when every box is ticked.

- [x] **DoD-1** — With three repos and a stub `execFile`, the command runs once per repo with the repo name as
  `argv[1]`. A repo with two items has count 2.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestYellowRunsCommandPerRepo test/yellow.test.js`
- [x] **DoD-2** — Output that is not an array fails the run. Items with a bad `id`, an `http://` url or a repeated
  `id` are dropped, and 201 items give 200.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestYellowRejectsBadItems test/yellow.test.js`
- [x] **DoD-3** — After exit 1, a timeout, and bad JSON, the stored `items` stay and `error` is set. The log gets
  one line per distinct error, not one per run.
  **Proof:** `cd inbox && node --test --test-name-pattern='TestYellowKeepsLastGoodOnFailure|TestYellowLogsOncePerError' test/yellow.test.js`
- [x] **DoD-4** — A real script that sleeps 5 s, run with a 100 ms timeout, fails with "did not answer within".
  The default timeout is 20 s.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestYellowTimeoutKills test/yellow.test.js`
- [x] **DoD-5** — A repo tried 100 s ago is skipped at a 300 s interval. One tried 300 s ago, or one that failed 300
  s ago, runs. **Proof:** `cd inbox && node --test --test-name-pattern=TestYellowSkipsFreshRepos test/yellow.test.js`
- [x] **DoD-6** — An answer 23 h old counts, and one 25 h old counts 0 and lists nothing.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestYellowStaleAnswerCountsZero test/yellow.test.js`
- [x] **DoD-7** — With `INBOX_REPOS_YELLOW_CMD` unset, no process runs, no line is logged, and every count is 0.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestYellowNoCommandRunsNothing test/yellow.test.js`
- [x] **DoD-8** — A daemon with two feeds runs both. When one rejects, the other still runs and draws.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestDaemonRunsEveryFeed test/daemon.test.js`
- [x] **DoD-9** — A store file at schema 8 migrates to 9 and gets `yellow_answers`.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestStoreMigratesToYellow test/store.test.js`
- [x] **DoD-10** — The installer takes `--yellow-cmd PATH` and has no option for a token. **Proof:**
  `sh inbox/scripts/install.sh --dry-run --yellow-cmd /bin/true | grep -c INBOX_REPOS_YELLOW_CMD` prints 1, and
  `sh inbox/scripts/install.sh -h | grep -ci token` prints 0.
- [x] **DoD-11** — `inbox/README.md` records the contract, the three env vars, and where tokens live.
  `inbox/examples/yellow-jira.sh` reads its token from the Keychain. **Proof:**
  `grep -n "Yellow criteria" inbox/README.md` and `grep -c "security find-generic-password" inbox/examples/yellow-jira.sh`
- [x] **DoD-12** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- A script prints its token to stderr and `error` stores it → the stored line is the first line of stderr,
  cut to 120 characters, and the README warns about it.
- 0089 names its repo read differently → only the `fetch` read changes.

## Open questions

- [ ] Should one call return every repo, for a large JIRA? — Keith decides after a week of use.

## Notes

- Precedent: `runGh`, `GH_TIMEOUT_MS` and `INBOX_GH` in `inbox/src/plugins/github.js`; the feed loop in
  `inbox/src/daemon.js` (`feeding`, `feedFailure`).
