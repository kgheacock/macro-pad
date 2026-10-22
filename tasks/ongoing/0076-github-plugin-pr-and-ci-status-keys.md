---
id: "0076"
title: "The github inbox plugin: one key per open PR, coloured by its checks, and a press opens it"
status: "ongoing"
created: "2026-10-22"
updated: "2026-10-22"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/99"
branch: "claude/project-thread-bwnpuf"
related: ["0064", "0066", "0068", "0071", "0073", "0075"]
tags: ["inbox", "plugin", "github", "ci"]
---

# 0076 — The github inbox plugin

## Problem

Keith keeps several pull requests open at once, in this repository and
others. To know which one has red checks, he has to open GitHub and look. The
pad already shows the state of AI agents (the `ai` plugin, task 0071), and the
one inbox daemon shows every plugin (task 0075). The state of his PRs fits the
same shape: one key per PR, coloured by its checks, and a press that goes to
it.

## Goals

- A plugin `github` (`inbox --plugin github`). Its top key shows the GitHub
  picture (`github.png`), the worst state of the open PRs, and a pip that
  counts the PRs whose checks fail.
- One child per open PR, at most five: red when its checks fail, blue while
  they run, green when they pass, plain when it has none. The failing ones
  show first.
- A press on a child opens the PR in the browser (a `url:` "here", task 0068)
  and marks it seen.
- A closed or merged PR leaves the pad by itself.
- No new command, store, service, or npm dependency. The one daemon of task
  0075 shows it with the other plugins.

## Non-goals

- PRs of other people, review requests, or issues. The query can be set
  (below), but the colours mean checks.
- The state of each check. A key shows the roll-up GitHub gives the head
  commit.
- Webhooks. The pad is on Keith's desk, not on the internet.
- Linux desktop opening of the URL. A press opens it on macOS only, as every
  "here" does.

## Approaches considered

### Approach A — The plugin feeds itself, inside the daemon (chosen)

A plugin may have a `feed`: a function the daemon calls every 60 s with the
plugin's store. The github feed runs `gh api graphql` once (one search query
returns every open PR with its check roll-up), and writes a message only when
a PR's state changed.

- Good, because the one service of task 0075 already runs it: nothing more to
  install or keep alive, and the key shows after `install.sh` runs again.
- Good, because `gh` holds Keith's GitHub login; the inbox stores no token.
- Good, because one GraphQL call a minute is far under the rate limit (5000 an
  hour).
- Bad, because the daemon now runs a network program. A failure must stay in
  the github loop: it is logged once, and the other plugins do not notice.

### Approach B — A feed script outside the daemon

`inbox/github/github-inbox.js` asks GitHub and runs `inbox --plugin github
send` for each change, like the Claude Code hooks feed the `ai` plugin.

- Good, because the daemon stays free of network calls.
- Bad, because nothing runs it: it needs a second login service (launchd and
  systemd) or cron, which is what task 0075 removed.
- Bad, because it must remember the last state of each PR between runs, or
  read it back through `inbox list` and parse it.

### Approach C — GitHub Actions write the state

A workflow step in each repository posts the check state somewhere the pad
can read.

- Good, because it is pushed, not polled.
- Bad, because the pad listens on 127.0.0.1, so the workflow cannot reach it
  without a tunnel, and every repository needs the step.

## Decision

Chosen: **Approach A — the plugin feeds itself, inside the daemon**. It is the
only one that needs nothing new to run, and it keeps the plugin in one file.
The cost is a `feed` field on a plugin and a timer in the daemon loop, and a
network call that must fail quietly.

## Design

### The plugin (`inbox/src/plugins/github.js`)

| Field | Value |
|---|---|
| `name` | `github` |
| `summary` | open pull requests, coloured by their checks |
| `statuses` | `failing`, `pending`, `passing`, `none`, `closed` |
| `defaultStatus` | `none` |
| top key id, icon | `github-inbox`, `github.png` |

The status of a PR is the status of its newest message, seen or not. A press
marks it seen, which clears the pip on its child; it does not change the
colour.

| Status | From the check roll-up | Pad status |
|---|---|---|
| `failing` | `FAILURE`, `ERROR` | `ERROR` (red) |
| `pending` | `PENDING`, `EXPECTED` | `RUNNING` (blue) |
| `passing` | `SUCCESS` | `DONE` (green) |
| `none` | no checks | none |
| `closed` | the PR is no longer open | the key goes |

- **Top key**: the worst status of the open PRs (failing, pending, passing),
  and a pip with the number of failing PRs.
- **Child**: its status, and a pip with its unseen messages, so a PR whose
  state changed since Keith last pressed it shows a 1.
- **Order**: failing, pending, passing, none; newest change first within each.
- **Channel**: `OWNER.REPO.NUMBER`, a dot in a name becoming `-`, for example
  `kgheacock.macro-pad.97`.
- **Icon**: the caption `REPO #NUMBER` (cut to 16 runes, as every caption),
  over the emoji of `channel:OWNER.REPO`, so the PRs of one repository share
  an emoji.
- **Body**: the PR title. **Here**: `url:` the PR's URL.
- A channel shows while its newest message is younger than 365 days
  (`activeMs`), and its status is not `closed`.

### The feed

The plugin's `feed` is `{ source: 'GitHub', everyMs: 60000, fetch({ env }),
sync(store, answer) }`. At start and every `everyMs`, the daemon awaits
`fetch` and then calls `sync`, which together do:

1. `gh api graphql` (from `$INBOX_GH`, default `gh` on the `PATH`) with
   `search(query: Q, type: ISSUE, first: 50)`, where `Q` is
   `$INBOX_GITHUB_QUERY`, default `is:pr is:open author:@me archived:false`.
   For each PR: owner, repository, number, title, URL, and the
   `statusCheckRollup.state` of its last commit. A 30 s timeout.
2. For each PR whose status differs from its channel's newest message, or that
   has no channel yet, inserts one message with that status, its title, and
   its URL as the "here".
3. For each channel the store shows whose PR is not in the answer, inserts a
   `closed` message, seen at once. Skipped when the answer has more PRs than
   it returned (more than 50 open), so a PR is never closed by a cut list.
4. `sync` returns how many messages it wrote. When more than 0, the daemon
   reads the store again and draws (the daemon's own writes do not move
   `data_version`).

A failure (no `gh`, not logged in, no network, bad JSON) writes nothing and
logs one line, `cannot read GitHub: WHY`; the same failure again logs nothing
until a run succeeds, which logs `GitHub read again`. A run never overlaps the
one before it. `stop()` stops the timer and does not wait for a `fetch` in
flight; its answer is dropped.

`launchd` runs the daemon without the shell's `PATH`, so `install.sh` writes
the absolute path of `gh`, when it finds one, as `INBOX_GH` into the
`$PREFIX/bin/inbox` wrapper the service runs, as it does for `node`. A value
already in the environment wins.

### The daemon and the renderer

- `startDaemon` runs `plugin.feed` as above when the plugin has one
  (`feedMs` overrides `everyMs`, for a test). A test passes a plugin with a
  fake `fetch`.
- `Store.recent()` rows gain `status`, the status of the newest message.
- A plugin's rules may have `shows(row)`: rows it returns false for are left
  out before the top key and the children are worked out. `github` hides
  `closed`.
- A plugin's rules may have `icon(channel)`, used instead of the default
  caption and emoji (task 0073).

### Use

```
inbox/scripts/install.sh                 # the one service now shows github too
inbox --plugin github list --all-channels
INBOX_GITHUB_QUERY='is:pr is:open review-requested:@me' inbox daemon --plugin github
```

Files to change:

- new: `inbox/src/plugins/github.js`, `inbox/test/github.test.js`
- `inbox/src/plugins/index.js`, `inbox/src/daemon.js`, `inbox/src/render.js`,
  `inbox/src/store.js`, `inbox/scripts/install.sh`, `inbox/README.md`,
  `inbox/test/daemon.test.js`
- tests that list the plugins, and `inbox/testlib/helpers.js` (`INBOX_GH`
  points at nothing, so no test calls GitHub)

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — Given a GraphQL answer with a failing, a pending, a passing,
  and a no-check PR, the feed writes four messages with those statuses, the
  titles, and `url:` the PR URLs; the same answer again writes nothing.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestGithubFeedWrites test/github.test.js`
- [ ] **DoD-2** — A PR missing from the next answer gets a seen `closed`
  message and leaves the children; with `issueCount` above the PRs returned,
  nothing is closed. **Proof:** `node --test --test-name-pattern=TestGithubFeedCloses test/github.test.js`
- [ ] **DoD-3** — The top key is `github-inbox` with `github.png`, status
  `ERROR` and pip 1 for one failing PR, `RUNNING` with only pending ones; the
  failing child shows first and red, and its caption is `macro-pad #97`.
  **Proof:** `node --test --test-name-pattern=TestGithubKeys test/github.test.js`
- [ ] **DoD-4** — A missing `gh`, a `gh` that exits 1, and an answer that is
  not JSON each give a reason; a failing feed logs one line over three runs,
  and a later good run logs `GitHub read again` and draws the PR. **Proof:**
  `node --test --test-name-pattern='TestGithubFeedFails|TestGithubFetch' test/github.test.js`
  and `node --test --test-name-pattern=TestDaemonFeedFails test/daemon.test.js`
- [ ] **DoD-5** — A daemon whose plugin has a feed draws the PR the feed wrote
  within 1 s, without another process writing the store, and a press on its
  child calls focus with its `url:` here. **Proof:**
  `node --test --test-name-pattern=TestDaemonFeed test/daemon.test.js`
- [ ] **DoD-6** — `inbox plugins` lists `github`; `inbox --plugin github send
  --status red x y` exits 2 and names the five statuses. **Proof:**
  `node --test --test-name-pattern='TestPluginFlag|TestGithubStatus' test/cli.test.js`
- [ ] **DoD-7** — `cd inbox && npm test` passes, and no test runs a real `gh`.
  **Proof:** the exit code; `grep -n INBOX_GH testlib/helpers.js`
- [ ] **DoD-8** — With `gh` on the `PATH`, `install.sh --dry-run` shows
  `INBOX_GH=` that `gh` for the wrapper, and the written wrapper exports it.
  **Proof:** the output; `cat ~/.local/bin/inbox`
- [ ] **DoD-9** — On the pad, after `install.sh`: a key per open PR of Keith's,
  red for a failing one, and a press opens it in the browser. **Proof:**
  Keith's check on the board, recorded in Notes
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR
  body

## Risks

- `gh` is not logged in, so the key stays empty → one log line says why
  (DoD-4); `gh auth status` fixes it.
- A PR is closed when the search answer is cut → the feed closes nothing when
  the answer says more PRs exist (DoD-2).
- The feed blocks the daemon loop → `gh` runs through `execFile`, async, with
  a 30 s timeout, and one run at a time.
- Keith does not want the key → `install.sh --plugin basic --plugin ai`
  leaves it out.

## Open questions

- [ ] Should a PR waiting on Keith's review, rather than his own, have its own
  colour? Not in this task. — kgheacock

## Notes
