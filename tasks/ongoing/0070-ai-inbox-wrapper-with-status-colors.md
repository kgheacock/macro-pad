---
id: "0070"
title: "ai-inbox: a separate inbox for AI agents, with a running, done, and waiting status on the pad"
status: "ongoing"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0070-ai-inbox-wrapper-with-status-colors"
related: ["0064", "0065", "0066", "0067", "0069"]
tags: ["inbox", "layers", "daemon", "agents"]
---

# 0070 — ai-inbox: a separate inbox for AI agents

## Problem

`inbox` counts unseen messages. Keith runs many AI agents at once. For an agent, the count is the wrong
signal. He needs to know if any agent waits for him, if some still run, or if all are done.

Agent messages also bury his own `inbox` messages when both share one store. Today one pad key and one
store serve both uses.

## Goals

- `ai-inbox` is a second command with its own store and its own key on the pad.
- A message has one of three statuses: `running` (blue), `done` (green), `waiting` (yellow).
- A channel has the highest status of its unseen messages. The key has the highest status of all channels.
  The order is `waiting`, then `running`, then `done`.
- The pip on the key is the number of channels in `waiting` status.
- A seen message does not count. The status is calculated again when messages are seen.
- A press on the key lists every channel with unseen messages, also when the pip is absent.

## Non-goals

- Hooks that make Claude Code or other agents call `ai-inbox`. A later task adds them.
- Changes to the `inbox` command, its store, or its key.
- New driver colors. `RUNNING`, `DONE`, and `WARN` already are blue, green, and yellow.
- A sixth channel on the pad. Channels past five count in the top status, as in task 0066.

## Approaches considered

Three approaches follow. They differ in where the new rules live.

### Approach A — A shell wrapper and a `--rules ai` flag

`$PREFIX/bin/ai-inbox` is a script: `INBOX_DB=…/ai-inbox.db exec inbox "$@"`. Only `inbox daemon` learns a
flag, `--rules ai`, which selects a second renderer.

- Good, because the wrapper is about five lines, and every `inbox` subcommand works unchanged.
- Good, because the CLI parser and its tests do not change.
- Bad, because the store accepts any status. `ai-inbox send --status eror` is saved and shows no color.
- Bad, because help text, errors, and the pid file say `inbox`. The daemon needs the flag in its service file by hand.

### Approach B — A profile inside the `inbox` package

A profile object holds the name, the default store path, the allowed statuses, the key id, and the renderer.
A new `inbox/bin/ai-inbox.js` calls `main(argv, io, profile)`.

- Good, because the store, the daemon loop, and the CLI parser stay one copy of code.
- Good, because the profile rejects a bad status with exit code 2, and help and errors say `ai-inbox`.
- Good, because the daemon takes the key id from the renderer. Two daemons share one `macropadd`.
- Bad, because `cli.js` (786 lines) and `daemon.js` gain a profile argument. Every command must pass it on.
- Bad, because `Store.unseen()` gains a field, which the `inbox` renderer ignores.

### Approach C — A separate `ai-inbox/` package

A new package imports `inbox/src/store.js` and `inbox/src/daemon.js`. It has its own small CLI, its own
installer, and its own tests.

- Good, because `inbox` stays untouched, and the rules are in one place.
- Good, because the CLI lists only the commands that agents need: `send`, `seen`, `channels`, `list`, `daemon`.
- Bad, because `ai-inbox` reaches into `inbox/src`. Each change to the store or daemon API can break it.
- Bad, because it copies the install and uninstall scripts (384 lines), the completion, and the pid logic.

## Decision

Chosen: **Approach B — a profile inside the `inbox` package**.

Keith asked for a wrapper, so the CLI, the store, and the daemon must stay one copy. B keeps one copy and
still rejects a wrong status, which A cannot do. The task accepts a profile argument through `cli.js` and
`daemon.js`.

## Design

**Profile** (`inbox/src/profiles.js`): `inbox` keeps today's behavior. `ai` sets:

| Field | Value |
|---|---|
| name | `ai-inbox` |
| store | `$AI_INBOX_DB`, else `$XDG_DATA_HOME/ai-inbox/ai-inbox.db` (default `~/.local/share/ai-inbox/ai-inbox.db`) |
| statuses | `waiting`, `running`, `done`. Any other `--status` exits with code 2. Default: `done`. |
| key id | `ai-inbox`, with `iconText` `ai\ninbox` |

**Store.** `Store.unseen()` rows gain `byStatus`, an object of unseen counts per status. Seen rows never
appear, so each read calculates the status again.

**Renderer** (`inbox/src/ai-render.js`), same interface as `render.js`. It reuses the stable-while-open
logic.

- Rank: `waiting` 3, `running` 2, `done` 1. A stored status outside the three counts as `done`.
- Channel child: `status` is `WARN`, `RUNNING`, or `DONE` from its highest rank. `count` is its number of
  `waiting` messages. `count` 0 draws no pip.
- Top key: `status` is the highest rank of all channels, empty with none. `count` is the number of channels
  with a `waiting` message.
- Children: the five highest by rank, then by newest unseen message.
- A press on a child marks its shown messages seen, as in task 0067. The next poll calculates again.

**Daemon.** `onPress` compares `active` with `renderer.topId`, not with the constant `INBOX_ID`. The pid file
follows the store, so each daemon has its own.

**Install.** `install.sh --ai` and `uninstall.sh --ai` use the label `com.kgheacock.macropad.ai-inbox`, the unit
`ai-inbox-daemon.service`, the wrapper `$PREFIX/bin/ai-inbox`, and the log `ai-inbox-daemon.log`.

Files to change:

- `inbox/src/profiles.js`, `inbox/src/ai-render.js`, `inbox/bin/ai-inbox.js` — new
- `inbox/src/cli.js`, `inbox/src/daemon.js`, `inbox/src/store.js` — profile, `topId`, `byStatus`
- `inbox/scripts/install.sh`, `inbox/scripts/uninstall.sh`, `inbox/README.md` — `--ai`, a section "ai-inbox"
- `driver/layers/layers_test.go` — one test, no driver code change

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof. The task
moves to `complete/` only when every box is ticked.

- [ ] **DoD-1** — Channel `a` has unseen `waiting` and `done`, `b` has `running`, `c` has `done`. The plan has
  `ai-inbox` with status `WARN` and count 1, and children `a` (`WARN`, 1), `b` (`RUNNING`, 0), `c` (`DONE`, 0), in that
  order. **Proof:** `cd inbox && node --test --test-name-pattern=TestAiPlan test/ai.test.js`
- [ ] **DoD-2** — Only `running` and `done` give `RUNNING`. Only `done` gives `DONE`. No unseen messages give an empty
  status and no children. **Proof:** `node --test --test-name-pattern=TestAiPriority test/ai.test.js`
- [ ] **DoD-3** — After `markSeen` on the `waiting` message of `a`, the next plan gives `a` the status `DONE` and the top
  key `RUNNING` with count 0. **Proof:** `node --test --test-name-pattern=TestAiSeenRecalculates test/ai.test.js`
- [ ] **DoD-4** — With only `done` channels, the top key has count 0 and one child per channel.
  A press on a key with children and count 0 opens the child layer. **Proof:**
  `node --test --test-name-pattern=TestAiListWithoutPips test/ai.test.js` and
  `cd driver && go test ./layers -run TestPressOpensLayerAtZeroCount`
- [ ] **DoD-5** — `ai-inbox send` writes to `$AI_INBOX_DB`. With `INBOX_DB` set to another file, `inbox list
  --all-channels` does not show the message. **Proof:** `node --test --test-name-pattern=TestAiSeparateStore test/cli.test.js`
- [ ] **DoD-6** — `ai-inbox send --status error x y` exits 2, names the three statuses, and saves nothing.
  `ai-inbox send x y` saves status `done`. **Proof:** `node --test --test-name-pattern=TestAiStatus test/cli.test.js`
- [ ] **DoD-7** — A daemon with `topId` `ai-inbox` marks nothing when it gets a press while `active` is `inbox`.
  All other daemon and `inbox` tests pass unchanged. **Proof:** `cd inbox && npm test`
- [ ] **DoD-8** — `inbox/scripts/install.sh --ai --dry-run` names `com.kgheacock.macropad.ai-inbox` and
  `ai-inbox`. Without `--ai`, its output has no `ai-inbox`. Both for `uninstall.sh`. **Proof:** run both commands and
  `grep -c ai-inbox`
- [ ] **DoD-9** — On the pad, with both daemons running: `ai-inbox send --status waiting a x` gives a yellow key with
  pip 1; `ai-inbox seen a` removes the pip and the color; a `done` message gives a green key with no pip, and a
  press lists its channel. **Proof:** Keith's check on the board, recorded in Notes
- [ ] **DoD-10** — `inbox/README.md` has a section "ai-inbox" with the statuses, the order, and the pip rule.
  **Proof:** `inbox/README.md`
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- An old `waiting` message keeps the key yellow until someone sees it, also after the agent went on →
  this is the rule Keith set. A press on the channel marks it seen.
- Two daemons may take the same slot → each takes the first free slot, and `--slot` fixes one. DoD-9 runs both.
- `macropadd` may accept one plugin client only → read `driver/plugin` before the work; DoD-9 proves it.

## Open questions

- [ ] Is the pip on a channel key the number of its `waiting` messages (the default here), or 1? — kgheacock
- [ ] Is `done` the right default status for `ai-inbox send` without `--status`? — kgheacock
