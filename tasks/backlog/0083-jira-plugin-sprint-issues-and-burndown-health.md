---
id: "0083"
title: "The jira inbox plugin: a key per open issue of mine in the sprint, and the sprint's health on the top key"
status: "backlog"
created: "2026-10-27"
updated: "2026-10-27"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/106"
branch: "claude/project-thread-n6s0ey"
related: ["0068", "0071", "0073", "0075", "0076", "0078"]
tags: ["inbox", "plugin", "jira", "sprint"]
---

# 0083 — The jira inbox plugin

## Problem

Keith's team runs Scrum on Jira Cloud. To see which of his stories are still
open, and whether the sprint is on track, he opens the board and does the sum
in his head. The pad already shows his open PRs (the `github` plugin, task
0076) from a feed inside the one inbox daemon (task 0075). His sprint fits the
same shape: one key per open issue, and a top key that says whether the
points he committed to are getting done.

## Goals

- A plugin `jira` (`inbox --plugin jira`). Its top key shows **my sprint
  health**: the story points completed over the points committed, coloured
  green, amber, or red against how far the sprint has run, with a pip that
  counts my blocked issues.
- One child per open issue assigned to me in the active sprint, coloured by
  where it is: blocked, in review, in progress, to do. Blocked shows first.
  Past five children, the paging of task 0078 applies.
- One child **Team**, first, with the same health sum for the whole sprint.
- A press on an issue opens it in the browser (a `url:` "here", task 0068); a
  press on Team opens the board.
- A done issue, or one that leaves the sprint, leaves the pad by itself.
- The Jira API token lives in the macOS login Keychain, never in a file
  (see [Where the API token is stored](#where-the-api-token-is-stored)).
- No new command, store, service, or npm dependency. Plain `fetch` (Node 22).

## Non-goals

- Jira Server or Data Center, and Kanban boards. Cloud and Scrum only.
- A drawn burndown chart on the key. The key shows numbers and a colour; a
  picture needs a PNG writer, which is its own task.
- Hours or issue counts as the measure. Story points only.
- Changing issues from the pad (moving a status, logging work).
- More than one board.

## Approaches considered

### Approach A — A feed inside the daemon, like github (chosen)

The plugin has a `feed` (task 0076). Every 2 minutes it asks the Jira REST
API for the active sprint and its issues, works out the health, and writes a
message when an issue's state or a health line changes.

- Good, because the one service already runs feeds: nothing more to install.
- Good, because the code shape, tests, and failure logging copy github's.
- Bad, because unlike `gh`, nothing holds the login for us, so the plugin
  needs a token and a safe place to keep it.

### Approach B — Use a Jira CLI (`jira` by ankitpokhrel, or `acli`)

Run a CLI the way github runs `gh`, and let it hold the login.

- Good, because the CLI stores the token.
- Bad, because neither gives sprint-report numbers (committed points at the
  start), so we would call the REST API anyway.
- Bad, because it is one more program to install and keep on the `PATH`.

### Approach C — Jira's sprint report endpoint

`/rest/greenhopper/1.0/rapid/charts/sprintreport` returns committed and
completed points directly.

- Good, because the sums are Jira's own.
- Bad, because it is a private, undocumented endpoint that Atlassian can
  change or drop without notice.

## Decision

Chosen: **Approach A — a feed inside the daemon, with plain `fetch`**. Keith
asked for plain `fetch`. It uses only the public REST APIs, and the plugin
stays in one file. The cost is working out "committed" from each issue's
changelog, once per issue per daemon run.

## Design

### The plugin (`inbox/src/plugins/jira.js`)

| Field | Value |
|---|---|
| `name` | `jira` |
| `summary` | my open issues in the active sprint, and the sprint's health |
| `statuses` | `blocked`, `review`, `progress`, `todo`, `done`, `green`, `amber`, `red`, `unknown` |
| `defaultStatus` | `todo` |
| top key id, icon | `jira-inbox`, the emoji 🏃 with the health caption |

The issue statuses, from the issue's fields:

| Status | Rule, first match wins | Pad status |
|---|---|---|
| `done` | `status.statusCategory.key` is `done` | the key goes |
| `blocked` | flagged (the Flagged field holds Impediment), or the status name matches `/block/i` | `ERROR` (red) |
| `review` | the status name matches `/review\|qa\|test/i` | `WARN` (amber) |
| `progress` | `statusCategory.key` is `indeterminate` | `RUNNING` (blue) |
| `todo` | anything else | none |

- **Channels**: an issue is `issue.KEY` (`issue.PROJ-123`). The two health
  lines are `sprint.mine` and `sprint.team`.
- **Issue child**: caption `PROJ-123` over the emoji of `channel:PROJ`, so
  one project's issues share an emoji (task 0073). Body: the summary. Here:
  `url:https://SITE/browse/PROJ-123`. A pip with its unseen messages.
- **Team child**: caption from its newest body, for example `62% · day 6/10`,
  over 👥, coloured by health. Here: `url:` the board.
- **Top key**: the caption and colour of `sprint.mine`, the emoji 🏃, and a
  pip with the number of my `blocked` issues.
- **Order**: Team, then blocked, review, progress, todo; newest change first
  within each.
- `shows(row)` hides `done` rows and `sprint.mine` (it is on the top key).
- `Store.recent()` rows gain `body`, the body of the newest message, so the
  health caption can be read back.

### Sprint health

For the active sprint (`startDate` to `endDate`):

- **Committed**: the story points of the issues that were in the sprint at
  `startDate`, at the value they had then. These are the same numbers as
  Jira's sprint report, so the pad and the board agree.
- **Completed**: the current story points of the issues in the sprint whose
  status category is `done`, added mid-sprint or not, as Jira's sprint report
  counts them. This can go over 100%.
- **Health** = completed ÷ committed, against **elapsed** = the share of the
  sprint's time that has passed (1 after `endDate`). With `gap = elapsed −
  health`:

| Gap | Status | Pad status |
|---|---|---|
| gap ≤ 0.10 | `green` | `DONE` (green) |
| 0.10 < gap ≤ 0.25 | `amber` | `WARN` (amber) |
| gap > 0.25 | `red` | `ERROR` (red) |
| committed is 0, or no active sprint | `unknown` | none |

- **Caption**: `PCT% · day D/N`, `PCT` the rounded health, `D` and `N` the
  sprint's calendar days. `no sprint` or `no points` when unknown.
- **Mine** sums only the issues assigned to me now; **Team** sums them all.

A message is written on `sprint.mine` or `sprint.team` only when its status
or caption changes, so a quiet sprint writes about one message a day.

Working out committed: the first time the feed sees an issue in this daemon
run, it reads the issue's changelog. The issue counts as committed when no
change to the Sprint field added this sprint after `startDate`. Its committed
points are the points field's value at `startDate`, found by walking the
changelog back from now. The result is kept in memory by sprint and issue, so
each issue's changelog is read once per daemon run.

### The feed

`{ source: 'Jira', everyMs: 120000, fetch({ env }), sync(store, answer) }`.
Each run:

1. Reads the token (below). None → fails with `no Jira token (see
   inbox --plugin jira --help)`.
2. `GET /rest/agile/1.0/board/$INBOX_JIRA_BOARD/configuration` once per run of
   the daemon, for the board's estimation field (its story points field) and
   board URL.
3. `GET /rest/agile/1.0/board/$INBOX_JIRA_BOARD/sprint?state=active`. None →
   both health lines `unknown`, every issue channel `done`.
4. `GET /rest/agile/1.0/sprint/ID/issue?fields=summary,status,assignee,flagged,POINTS`
   paged by `startAt` until `total`. With `assignee.accountId` set against
   `GET /rest/api/3/myself` (read once per run of the daemon).
5. The changelog of each issue not seen yet (`GET
   /rest/api/3/issue/KEY/changelog`, paged).
6. `sync` writes a message for each of my issues whose status changed, a seen
   `done` for each issue channel no longer in the answer, and the two health
   lines when they changed. It returns how many it wrote.

Every request is `https://$INBOX_JIRA_SITE` with Basic auth of
`$INBOX_JIRA_EMAIL:TOKEN`, a 30 s timeout (`AbortSignal.timeout`), and one run
at a time. A 429 waits out `Retry-After` on the next run. A failure writes
nothing and logs one line, `cannot read Jira: WHY`, once until a run succeeds
(`Jira read again`), as github does. A 401 says `Jira refused the token for
EMAIL`. No log line, error, or message body holds the token or the
`Authorization` header.

### Settings

None of these is a secret, so `install.sh` writes them into the
`$PREFIX/bin/inbox` wrapper, as it does for `INBOX_GITHUB_QUERY`:

| Variable | `install.sh` flag | Meaning |
|---|---|---|
| `INBOX_JIRA_SITE` | `--jira-site` | `yourcompany.atlassian.net` |
| `INBOX_JIRA_EMAIL` | `--jira-email` | the Atlassian account email |
| `INBOX_JIRA_BOARD` | `--jira-board` | the Scrum board's id (the number in its URL) |
| `INBOX_JIRA_POINTS_FIELD` | `--jira-points-field` | optional; overrides the board's estimation field, for example `customfield_10016` |

The plugin shows nothing, and logs `Jira is not set up` once, until site,
email, and board are all set. A value with a quote, backslash, `}`, or new
line makes `install.sh` exit 2, as `--github-query` does.

### Where the API token is stored

The token is a secret. It is stored **only** in the macOS login Keychain, as
a generic password:

| Keychain field | Value |
|---|---|
| service (`-s`) | `com.kgheacock.macropad.jira` |
| account (`-a`) | `$INBOX_JIRA_EMAIL` |
| password (`-w`) | the API token |

It is **never** put in `plugins.json`, the `inbox` wrapper, the launchd plist,
a shell profile, the SQLite store, a log, or the repository.

To set it up once:

1. Make a token at https://id.atlassian.com/manage-profile/security/api-tokens
   (**Create API token**, the classic kind, not the one with scopes). Name it
   `macro-pad`. Set an expiry; a year is the most Atlassian allows.
2. Store it, typing or pasting the token at the prompt so it stays out of the
   shell history:

   ```
   inbox/scripts/jira-token.sh you@company.com
   ```

   which runs

   ```
   security add-generic-password -U -s com.kgheacock.macropad.jira \
     -a you@company.com -T /usr/bin/security -w
   ```

   `-U` replaces an old token; `-T /usr/bin/security` lets the daemon read it
   back with no Keychain dialog; `-w` given last prompts for the value.
3. Check it: `inbox/scripts/jira-token.sh --check you@company.com` reads the
   token back and calls `GET /rest/api/3/myself`, printing the display name
   or why it failed. It never prints the token.

When the token expires or is revoked, the key goes plain and the log says
`Jira refused the token`; make a new one and run step 2 again. To remove it:
`security delete-generic-password -s com.kgheacock.macropad.jira -a
you@company.com`, and revoke it on the Atlassian page.

The feed reads it on each run with `security find-generic-password -s
com.kgheacock.macropad.jira -a EMAIL -w` through `execFile`, and keeps it
only for that run. Reading it each time means a new token works without a
daemon restart.

On Linux (the daemon runs under systemd in tests and CI), the same script and
feed use libsecret instead: `secret-tool store --label='macro-pad Jira'
service com.kgheacock.macropad.jira account EMAIL`, read back with
`secret-tool lookup`. For tests only, `INBOX_JIRA_TOKEN` in the environment
wins over both; `install.sh` never writes it.

### Use

```
inbox/scripts/jira-token.sh you@company.com
inbox/scripts/install.sh --jira-site acme.atlassian.net \
  --jira-email you@company.com --jira-board 42
inbox --plugin jira list --all-channels
```

Files to change:

- new: `inbox/src/plugins/jira.js`, `inbox/src/jira-token.js` (the Keychain
  and libsecret reads), `inbox/scripts/jira-token.sh`, `inbox/test/jira.test.js`
- `inbox/src/plugins/index.js`, `inbox/src/store.js` (`body` in
  `recent()`), `inbox/scripts/install.sh`, `inbox/README.md`
- tests that list the plugins, and `inbox/testlib/helpers.js` (no test calls
  Jira, `security`, or `secret-tool`)

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — Given a sprint answer with a flagged, an in-review, an
  in-progress, a to-do, and a done issue of mine, the feed writes four issue
  messages with those statuses, summaries, and `url:` browse links, and none
  for the done one; the same answer again writes nothing. **Proof:**
  `cd inbox && node --test --test-name-pattern=TestJiraFeedWrites test/jira.test.js`
- [ ] **DoD-2** — An issue missing from the next answer, or now done, gets a
  seen `done` message and leaves the children. **Proof:**
  `node --test --test-name-pattern=TestJiraFeedCloses test/jira.test.js`
- [ ] **DoD-3** — From fixture changelogs: an issue added after `startDate` is
  not committed; an issue re-estimated from 3 to 5 after the start counts 3
  committed and, when done, 5 completed; mine and team sum separately.
  **Proof:** `node --test --test-name-pattern=TestJiraCommitted test/jira.test.js`
- [ ] **DoD-4** — Health is `green` at 50% done half way, `amber` at 35%,
  `red` at 20%, and `unknown` with no points; the caption is `50% · day 5/10`.
  **Proof:** `node --test --test-name-pattern=TestJiraHealth test/jira.test.js`
- [ ] **DoD-5** — The top key is `jira-inbox` with 🏃, mine's caption and
  colour, and a pip of 1 for one blocked issue; Team is the first child; the
  blocked issue comes next and red. **Proof:**
  `node --test --test-name-pattern=TestJiraKeys test/jira.test.js`
- [ ] **DoD-6** — The token is read from `INBOX_JIRA_TOKEN`, else `security
  find-generic-password` (macOS) or `secret-tool lookup` (Linux) with service
  `com.kgheacock.macropad.jira` and the email; a missing token, a 401, a 429,
  and bad JSON each give a reason, logged once over three runs, and no log
  line or error holds the token. **Proof:**
  `node --test --test-name-pattern='TestJiraToken|TestJiraFails' test/jira.test.js`
- [ ] **DoD-7** — `jira-token.sh EMAIL` calls `security add-generic-password
  -U -s com.kgheacock.macropad.jira -a EMAIL -T /usr/bin/security -w` (a stub
  `security` records the arguments), and never takes the token as an
  argument. **Proof:** `node --test --test-name-pattern=TestJiraTokenScript test/cli.test.js`
- [ ] **DoD-8** — `install.sh --dry-run --jira-site S --jira-email E
  --jira-board 42` shows the three variables for the wrapper; a quote in one
  exits 2; nothing it writes holds a token. **Proof:**
  `node --test --test-name-pattern=TestInstallJira test/cli.test.js`
- [ ] **DoD-9** — `inbox plugins` lists `jira`; `inbox --plugin jira --help`
  says where the token goes. `cd inbox && npm test` passes and no test calls
  Jira or a real Keychain. **Proof:** the exit code; `grep -n
  'INBOX_JIRA' testlib/helpers.js`
- [ ] **DoD-10** — On the pad, after storing the token and running
  `install.sh`: my sprint issues show, the top key's numbers match the
  board's sprint report, and a press opens the issue. **Proof:** Keith's check
  on the board, recorded in Notes
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR
  body

## Risks

- An issue removed from the sprint mid-way is not returned by the sprint's
  issue list, so it drops out of committed, and the pad can read higher than
  Jira's sprint report → noted in `--help`; the board check (DoD-10) shows how
  far off it is in practice.
- The first run of a daemon reads every issue's changelog (one call each, ~30
  for a sprint) → kept in memory after that, and far under Jira's limits.
- A team that moves issues to Done without a `done` status category → the
  statuses come from Jira's categories, which every workflow has.
- The token leaks into a log → requests never log headers; DoD-6 checks the
  log and the errors.
- The Keychain item was made by hand without `-T /usr/bin/security`, so macOS
  shows a dialog the daemon cannot answer → `jira-token.sh --check` reads it
  the same way the daemon does, and `-U` with the script fixes it.

## Open questions

- [ ] Are the thresholds (10 and 25 points behind) right, and should a sprint
  that is ahead show differently? — kgheacock
- [ ] Should "mine" be the issues assigned to me at the start of the sprint,
  rather than now, so a hand-off does not move my numbers? — kgheacock
- [ ] Atlassian's scoped API tokens use a different base URL
  (`api.atlassian.com/ex/jira/CLOUD_ID`). Support them later, so the token
  can be read-only? — kgheacock

## Notes
