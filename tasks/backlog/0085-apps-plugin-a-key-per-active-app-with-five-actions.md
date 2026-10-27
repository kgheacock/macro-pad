---
id: "0085"
title: "The apps inbox plugin: a key per active app, with five actions under it"
status: "backlog"
created: "2026-10-27"
updated: "2026-10-27"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-2g96mq"
related: ["0068", "0072", "0075", "0076", "0078", "0081", "0082"]
tags: ["inbox", "plugin", "apps", "iterm", "vscode"]
---

# 0085 — The apps inbox plugin

## Problem

Keith's main work is a "loose monorepo": 60 or more apps, each its own GitHub
repository, cloned side by side at `~/source/github.com/<org>/<name>`. On a
given day two or three of them are active. For each active app he does the
same five things, many times a day:

1. view the code (VS Code),
2. open a Claude session,
3. run the app (`npm run dev`, `make dev`, ...; it differs per app),
4. pull from the remote,
5. open a terminal at the repository root.

Each one is a few steps today: find the folder, open the right program, type
the command. The pad can make each one a single press. The inbox daemon
(task 0075) already runs on Keith's Mac, keeps keys on the pad, and hears
presses, so the work fits it as one more plugin.

The repositories hold work code. The name of the work org must not appear in
this repository: the root folder comes from install-time config, and every
test and example uses a placeholder (`acme`).

## Goals

- A plugin `apps` (`inbox --plugin apps`). It shows one **top key per active
  app**, at most `INBOX_APPS_COUNT` (default 3). Each key shows the app's
  name over the emoji of its repository (task 0081), so it matches the app's
  Claude sessions and its PRs on the github key.
- A press on an app key opens its layer of exactly **five children**: Code,
  Claude, Run, Pull, Term. Five children and the up key fit one page (task
  0078).
- **Active** is cheap to work out, without a `git status` over 60
  repositories: the newest of `.git/logs/HEAD` (a checkout or commit),
  `.git/index` (staging), and a Claude session of today in that repository
  (the ai plugin's store rows, tasks 0072 and 0081). An app is active when
  that time is today. A **pin** always wins:
  `inbox --plugin apps pin NAME` and `unpin NAME`.
- **Config at install time**: `install.sh --apps-root DIR` writes
  `INBOX_APPS_ROOT` into the local wrapper, like `--github-query` (task
  0076). The repository stays generic.
- A press runs a fixed action with `execFile`, never a shell on the daemon's
  side.

## Non-goals

- Apps outside one root folder, or nested deeper than `ROOT/NAME`.
- Editors other than VS Code, terminals other than iTerm2.
- "Commits behind" on the Pull key: it needs a `git fetch` per app, which is
  a network call per press or per minute. A pull says how many commits it
  brought instead.
- A second Claude session when one exists. The Claude key focuses the
  newest one (an open question below).
- Showing every line of a running app in the store. The Run key says
  running, stopped or crashed.
- Linux. Pull works there; the other four actions need macOS, like every
  "here".

## Approaches considered

### Approach A — An inbox plugin with its own keys and a press handler (chosen)

`inbox/src/plugins/apps.js` is a plugin like `github`: a feed the daemon runs
every 30 s scans the root, and the store holds pins and the state of each
app's run and pull. Two small hooks in the daemon let a plugin draw more than
one top key and act on a press itself.

- Good, because the one service of task 0075 already runs it: nothing new to
  install or keep alive.
- Good, because the store already gives the CLI a way to talk to the daemon
  (pins, run state), and `inbox list` shows the history for free.
- Bad, because the daemon now draws several top keys for one plugin, and so
  needs a second renderer.

### Approach B — A plugin of the Go host (task 0055)

- Good, because the Go host was meant for plugins with actions.
- Bad, because 0055 is not on `main`, and Keith asked for the inbox daemon.

### Approach C — Keys from a static layers.json, actions as shell scripts

- Good, because it needs no code in the daemon.
- Bad, because the active apps change every day, and a static file cannot
  follow them. Run and Pull would show no state.

## Decision

Chosen: **Approach A**. It is the only one that follows the active apps by
itself and shows the state of a run and a pull. The cost is a second renderer
in `apps.js`, and two optional plugin fields the daemon reads:
`createRenderer` and `press`.

## Design

### Config (environment of the daemon, written into the wrapper by install.sh)

| Variable | Meaning | Default |
|---|---|---|
| `INBOX_APPS_ROOT` | the folder that holds the app repositories, such as `~/source/github.com/acme` | unset: no app keys, and one log line says so |
| `INBOX_APPS_COUNT` | how many app keys, 1 to 5 | 3 |
| `INBOX_APPS_RUN` | Run overrides, `NAME=CMD;NAME=CMD` (CMD split on spaces) | none |
| `INBOX_GIT` | the `git` program | `git` on the `PATH` |

`install.sh --apps-root DIR` (resolved to an absolute path, it must exist),
`--apps-count N` and `--apps-run NAME=CMD` (once per app) write them. A
quote, a backslash, `}` or a new line in a value exits 2, as for
`--github-query`. `install.sh` also writes the absolute path of `git` it
finds, as it does for `gh`.

### Which apps show

An **app** is a folder `ROOT/NAME` that holds a `.git` folder. Its channel
name is `NAME` with a dot turned into `-` (as in task 0076), so
`site.github.io` is `site-github-io`.

The feed runs at start and every 30 s. For each app it reads the mtime of
`.git/logs/HEAD` and `.git/index`, and the newest ai-plugin message of today
whose folder channel is under the root's channel (`github.com.acme.NAME...`,
task 0072). The newest of the three is the app's **activity**. Pressing one
of its keys does not count.

The apps that show, at most `INBOX_APPS_COUNT`:

1. the pinned apps, oldest pin first;
2. then the apps whose activity is today (since local midnight), newest
   first.

A key keeps its position while it shows: a new app takes the slot of the one
that left, and keys do not reorder. While the pad shows an app's layer, that
app's key stays until the layer closes.

### The keys

| Key | id | Shows |
|---|---|---|
| app | `app.NAME` | caption `NAME` (cut to 16 runes) over the repository's emoji; RUNNING while it runs or pulls, ERROR after a crash or a failed pull |
| Code | `app.NAME.code` | caption `Code` over the same emoji |
| Claude | `app.NAME.claude` | caption `Claude` |
| Run | `app.NAME.run` | caption: the command (`make dev`, `pnpm dev`), or `no run`; RUNNING while it runs, ERROR after a crash |
| Pull | `app.NAME.pull` | caption `Pull`; RUNNING while it pulls, DONE after a good pull, ERROR after a failed one |
| Term | `app.NAME.term` | caption `Term` |

`--slot apps=N` is ignored, with a log line: the app keys take the free
slots. Each other plugin takes one slot, so with `basic`, `ai` and `github`
the pad is full at three app keys.

### The actions

All of them run with `execFile`, with an argument list. The commands typed
into an iTerm tab quote each word in single quotes.

1. **Code**: `open -b com.microsoft.VSCode DIR`. `open -b` needs no `PATH`,
   which `launchd` does not give.
2. **Claude**: the newest ai-plugin session of today in this app with an
   `iterm2:` "here" comes to the front. Else the tab this key opened before,
   if it is still open. Else a new iTerm tab runs `cd DIR && claude`, and its
   session id is kept as the "here" of channel `NAME.claude`.
3. **Run**: the command is, first match wins:
   1. the app's `INBOX_APPS_RUN` override;
   2. a `Makefile` (or `makefile`, `GNUmakefile`) target `dev`, `run`,
      `start` or `serve`, in that order: `make TARGET`;
   3. a `package.json` script `dev`, else `start`, with the package manager
      its lockfile names: `pnpm-lock.yaml` pnpm, `yarn.lock` yarn,
      `bun.lock`/`bun.lockb` bun, else npm: `PM run SCRIPT`;
   4. none: the key's caption says `no run`, and a press logs a line.

   It runs in the app's own iTerm tab, opened on the first press and reused
   after (`NAME.run`'s "here"). While it runs, a press brings that tab to the
   front. Otherwise the tab gets
   `cd DIR && inbox --plugin apps run NAME -- CMD...`, which writes
   `running`, runs CMD with the terminal as its input and output, and writes
   `stopped` when it exits 0 or by Ctrl-C (SIGINT), SIGTERM or SIGHUP, or
   `crashed` with its exit code.
4. **Pull**: `git pull --rebase` in DIR, by the daemon, with
   `GIT_TERMINAL_PROMPT=0` and a 60 s limit. It writes `pulling`, then
   `pulled` ("up to date", or "N commits"), or `failed` with git's first
   line. When the rebase stops on a conflict, the daemon runs
   `git rebase --abort`, so the repository is as it was, and says
   "conflict; rebase aborted".
5. **Term**: the tab this key opened before, if it is still open; else a new
   iTerm tab runs `cd DIR`, kept as `NAME.term`'s "here".

### The store

No new table. The plugin's messages:

| Channel | Statuses | Written by |
|---|---|---|
| `NAME.pin` | `pinned`, `unpinned` | `inbox --plugin apps pin`/`unpin` |
| `NAME.run` | `running`, `stopped`, `crashed` | `inbox --plugin apps run` |
| `NAME.pull` | `pulling`, `pulled`, `failed` | the daemon |

The newest message of a channel is its state. The iTerm tabs the daemon
opens are kept as channel "heres" (`NAME.claude`, `NAME.run`, `NAME.term`).
`Store.forPlugin('ai')` reads the ai plugin's rows on the same handle.

### The CLI

```
inbox --plugin apps pin [NAME]       # NAME, or the app of the working directory
inbox --plugin apps unpin [NAME]
inbox --plugin apps run NAME [-- CMD...]   # CMD, or the detected command
```

`pin`, `unpin` and `run` exit 2 with any other plugin. `pin` exits 2 when
`INBOX_APPS_ROOT` is unset, or `ROOT/NAME` holds no `.git`.

### The daemon

- A plugin may have `createRenderer({ slot, env })`, used instead of
  `render.js`. Its renderer adds `owns(parent)`.
- A plugin may have `press({ parent, child, store, file, env, fed, log })`.
  The daemon calls it for a press on a child of a key the renderer owns,
  then reads the store again.
- `plan()` also gets `fed`, the newest feed answer.
- `feed.fetch()` also gets `store`.

Files to change:

- new: `inbox/src/plugins/apps.js`, `inbox/src/apps.js` (run detection, iTerm,
  git), `inbox/test/apps.test.js`
- `inbox/src/plugins/index.js`, `inbox/src/daemon.js`, `inbox/src/store.js`,
  `inbox/src/cli.js`, `inbox/scripts/install.sh`, `inbox/README.md`,
  `inbox/completions/_inbox`, `inbox/testlib/helpers.js`

## Definition of done

An outside reviewer verifies each item without help from the implementer.

- [ ] **DoD-1** — In a fake root (`acme`) of five repositories, two touched
  today, one yesterday, one with an ai session today, one pinned: the keys
  are the pinned one first, then the active ones newest first, three in all;
  an app touched yesterday shows only when pinned. **Proof:**
  `cd inbox && node --test --test-name-pattern=TestAppsChoose test/apps.test.js`
- [ ] **DoD-2** — Run detection: an override wins; a Makefile `dev:` beats a
  `package.json`; `serve:` is found; `dev` beats `start`; a `pnpm-lock.yaml`
  gives `pnpm run dev`; nothing gives null. **Proof:**
  `node --test --test-name-pattern=TestAppsDetectRun test/apps.test.js`
- [ ] **DoD-3** — Each app key has the five children in order, the repo emoji
  of task 0081, a Run caption from detection, and the colours of the store's
  run and pull state; a key that leaves frees its slot without moving the
  others, and the open layer's key stays until it closes. **Proof:**
  `node --test --test-name-pattern=TestAppsKeys test/apps.test.js`
- [ ] **DoD-4** — A press runs the right program with the right arguments:
  `open -b com.microsoft.VSCode DIR`; an existing ai session is focused, else
  an iTerm tab with `claude`; Run writes the quoted `inbox ... run` command
  into a new or the kept tab; Term reuses its tab. **Proof:**
  `node --test --test-name-pattern=TestAppsPress test/apps.test.js`
- [ ] **DoD-5** — Pull on a real scratch repository: one new upstream commit
  gives `pulled` "1 commit"; a conflicting change gives `failed`, and
  `git status` shows no rebase in progress. **Proof:**
  `node --test --test-name-pattern=TestAppsPull test/apps.test.js`
- [ ] **DoD-6** — `inbox --plugin apps run x -- sh -c 'exit 3'` writes
  `running` then `crashed` and exits 3; `exit 0` writes `stopped`; `pin` and
  `unpin` write their messages; `--plugin basic pin x` exits 2. **Proof:**
  `node --test --test-name-pattern=TestAppsCli test/apps.test.js`
- [ ] **DoD-7** — `cd inbox && npm test` passes, and no test opens iTerm, VS
  Code, or the network. **Proof:** the exit code.
- [ ] **DoD-8** — `install.sh --dry-run --apps-root DIR` shows
  `INBOX_APPS_ROOT=DIR` for the wrapper, and a missing DIR exits 1.
  **Proof:** the output.
- [ ] **DoD-9** — On the pad, after `install.sh --apps-root
  ~/source/github.com/<org>`: the keys show the apps Keith works on today,
  and each of the five children does its action. **Proof:** Keith's check on
  the board, recorded in Notes.
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR
  body.

## Risks

- **The pad is full.** Six keys hold `basic`, `ai`, `github` and three apps;
  the meeting or jira key would not fit. → `INBOX_APPS_COUNT` and
  `install.sh --plugin` choose; the README says so.
- **Text typed into a reused tab** lands in whatever runs there now. → Run
  only types when its own run is not running; Term never types into a kept
  tab.
- **SSH under launchd**: `git pull` from the daemon needs the agent.
  `launchd` sets `SSH_AUTH_SOCK` for the user session on macOS, and
  `GIT_TERMINAL_PROMPT=0` makes a missing login fail fast instead of hang.
- **A stale `running`** after a `kill -9` of the run: the next press only
  focuses the tab. Ctrl-C there and a new press runs it again.

## Open questions

- [ ] Should Claude also offer a new session when one exists? Default:
  focus only. — kgheacock
- [ ] Is "today" the right window for active? Default: since local midnight;
  a pin covers the rest. — kgheacock

## Notes
