---
id: "0075"
title: "One inbox daemon and one login service for every plugin"
status: "backlog"
created: "2026-10-21"
updated: "2026-10-21"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-w1w7ar"
related: ["0065", "0066", "0070", "0071"]
tags: ["inbox", "daemon", "install"]
---

# 0075 — One inbox daemon and one login service for every plugin

## Problem

Since task 0071, each inbox plugin has its own daemon and its own login
service. Keith runs two: `basic`
(`com.kgheacock.macropad.inbox`) and `ai`
(`com.kgheacock.macropad.inbox.ai`). That means two `node` processes, two
logs, two pid files, and `install.sh` and `uninstall.sh` run once per plugin.
An upgrade has to restart both, and it is easy to restart one and forget the
other.

The two daemons already share the store, the `inbox` command, and the
`macropadd` they talk to. Only the loop is duplicated. `macropadd` stays a
separate process; this task does not touch it.

## Goals

- `inbox daemon` with no `--plugin` shows every plugin in one process.
- `--plugin NAME`, given once or more, limits the daemon to those plugins.
  `inbox daemon --plugin ai` behaves as it does today.
- `install.sh` installs one service, `com.kgheacock.macropad.inbox` on macOS
  and `inbox-daemon.service` on Linux, which runs every plugin, with one log.
- `install.sh` removes the per-plugin services that task 0071 made
  (`com.kgheacock.macropad.inbox.ai`, `inbox-daemon-ai.service`, one per
  plugin other than `basic`), so an upgrade leaves exactly one service.
- `uninstall.sh` removes that one service, and any per-plugin service left.
- What each key shows, and what a press does, does not change.

## Non-goals

- Merging `macropadd` and the inbox, or starting `macropadd` at login.
- A change to the store, the schema, or any command other than `daemon`.
- A change to what macropadd receives: each plugin still owns its top key and
  reads presses only in its own layer.

## Approaches considered

### Approach A — One process, one loop per plugin

`runDaemon` calls `startDaemon` once per plugin, in one process. Each loop
keeps what it has now: its own store handle, its own WebSocket to
`macropadd`, its own pid file. On a signal, the process stops every loop.

- Good, because `startDaemon` does not change; the work is in `cli.js`
  (`runDaemon`) and the two scripts. The daemon tests keep passing as they
  are.
- Good, because a loop that fails to read the store or loses its socket does
  not affect the other.
- Good, because the per-plugin pid files still refuse an old per-plugin daemon
  that runs at the same time (exit 3), so an upgrade cannot draw a key twice.
- Bad, because `macropadd` still sees two WebSocket clients, and the store is
  polled twice every 500 ms. Both are cheap: one pragma per poll, and
  `macropadd` already serves several clients.

### Approach B — One process, one socket, one poll

One loop polls `data_version` once and renders every plugin; one WebSocket
carries every plugin's keys, and an event goes to the plugin whose layer it
came from.

- Good, because it is the least work at run time: one socket and one poll.
- Bad, because `startDaemon` has to be split into a shared connection and
  per-plugin renderers. The reconnect, the wait for `layerChanged`, and the
  full resend on connect all become shared state, and most daemon tests
  change.
- Bad, because one plugin's error path (a failed read, a bad row) now sits in
  the loop of the other.

### Approach C — Keep two processes under one service

The service runs a small shell script that starts `inbox daemon --plugin
basic` and `inbox daemon --plugin ai` and waits for both.

- Good, because no JavaScript changes.
- Bad, because it is still two daemons; only the plist is shared.
- Bad, because launchd's `KeepAlive` restarts the script, not a child that
  died, so the script must supervise its children itself.

## Decision

Chosen: **Approach A — one process, one loop per plugin** (pending Keith's
review).

It gives one process, one service, and one log with the least change to code
that works. The cost is a second WebSocket and a second poll inside the one
process, which are cheap. If they ever matter, Approach B can follow without
changing the command or the service.

## Design

### Command

```
inbox daemon [--plugin NAME]... [--db PATH] [--port 8765] [--slot [NAME=]N]... [--poll 500ms]
```

- No `--plugin`: every plugin that `inbox plugins` lists, in that order.
- `--plugin NAME` once or more (or `--plugin basic,ai`): those plugins only.
  An unknown name exits 2 and names it, as today. A name given twice runs once.
- `--slot N` with one plugin: as today. `--slot NAME=N`, once per plugin, sets
  the slot of that plugin. A bare `--slot N` with more than one plugin exits 2:
  two top keys cannot share a slot. Two plugins given the same slot exit 2.
- `--port`, `--poll`, and `--db` apply to every plugin.
- One start line per plugin, as today
  (`inbox: basic daemon started for <db> (pid N)`), so the log reads the same.
  Log lines from a plugin's loop start with `inbox: [ai]` when the process runs
  more than one plugin; with one plugin they stay as they are.

### One daemon per store and plugin

The rule of task 0065 stays. The process takes `<db>.<plugin>.daemon.pid` for
each plugin before it starts any loop. If one is held by a live process, it
releases the ones it took and exits 3 with today's message, so a merged daemon
and an old `--plugin ai` daemon never both draw the `ai-inbox` key.

### Stopping

`SIGINT` and `SIGTERM` stop every loop, release every pid file, and exit 0
within 1 s, as today.

### Install

`install.sh` writes one service, with the label and unit of `basic` today
(`com.kgheacock.macropad.inbox`, `inbox-daemon.service`, log
`~/Library/Logs/inbox-daemon.log`). Its arguments are `daemon --port N`, plus
`--plugin NAME` and `--slot NAME=N` when given.

- `--plugin NAME`, once or more, limits the service to those plugins. Without
  it, the service runs every plugin, so a plugin added later shows after
  `install.sh` runs again, or after the service restarts.
- `--slot N` or `--slot NAME=N`, as for the command.
- Before it starts the new service, it boots out and deletes every
  `com.kgheacock.macropad.inbox.*` plist (macOS) and every
  `inbox-daemon-*.service` unit (Linux) and names each one it removed. Their
  logs stay.
- `--dry-run` prints those removals too.

### Uninstall

`uninstall.sh` removes the one service and any per-plugin service left, then
the command and the completion. `--plugin` is no longer needed: given, it is
accepted with one line on standard error and ignored. `--purge` is no longer
refused for another plugin's service, since there is one service.

### Help and README

`inbox daemon -h`, `install.sh -h`, `uninstall.sh -h`, and `inbox/README.md`
("Daemon", "Install at login", "Plugins") say that one daemon shows every
plugin. `inbox/claude-code/README.md` drops `install.sh --plugin ai`.

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — `inbox daemon` with no `--plugin`, against a test WebSocket
  server, draws both the `inbox` and the `ai-inbox` key, and a row sent to
  either plugin reaches the server within 1 s.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestOneDaemonEveryPlugin test/cli.test.js`
- [ ] **DoD-2** — `inbox daemon --plugin ai` draws only `ai-inbox`, as before.
  **Proof:** `node --test --test-name-pattern=TestDaemonOnePlugin test/cli.test.js`
- [ ] **DoD-3** — While a `--plugin ai` daemon runs, `inbox daemon` exits 3,
  names the `ai` daemon, and leaves no `basic` pid file behind.
  **Proof:** `node --test --test-name-pattern=TestMergedDaemonRefused test/cli.test.js`
- [ ] **DoD-4** — A bare `--slot N` with two plugins exits 2; `--slot basic=1
  --slot ai=2` puts each top key in its slot; two plugins on one slot exit 2.
  **Proof:** `node --test --test-name-pattern=TestDaemonSlots test/cli.test.js`
- [ ] **DoD-5** — `SIGTERM` stops every loop, removes every pid file, and
  exits 0 within 1 s. **Proof:** `node --test --test-name-pattern=TestDaemonSignal test/cli.test.js`
- [ ] **DoD-6** — With a fake `HOME` that holds both plists of task 0071,
  `install.sh --dry-run` prints the removal of
  `com.kgheacock.macropad.inbox.ai.plist` and the write of one plist with no
  `--plugin`. **Proof:** running the script, with stub `launchctl` and `uname`
- [ ] **DoD-7** — On Keith's Mac, after `install.sh`,
  `launchctl print gui/$UID/com.kgheacock.macropad.inbox` shows
  `state = running`, `launchctl print gui/$UID/com.kgheacock.macropad.inbox.ai`
  fails, one `node` process runs `inbox daemon`, and both keys show on the pad.
  After `uninstall.sh`, both `launchctl print` calls fail.
  **Proof:** the reviewer runs the scripts on Keith's Mac
- [ ] **DoD-8** — `shellcheck inbox/scripts/*.sh` exits 0. **Proof:** the exit code
- [ ] **DoD-9** — `cd inbox && npm test` passes. **Proof:** the exit code
- [ ] **DoD-10** — `inbox/README.md`, `inbox/claude-code/README.md`, and the
  `-h` text of `daemon`, `install.sh`, and `uninstall.sh` describe one daemon
  for every plugin. **Proof:** the files and the output
- [ ] **DoD-11** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- An upgrade leaves the old `ai` service running → `install.sh` removes it
  before it starts the new one, and the pid files refuse a double start
  (DoD-3, DoD-6).
- A slot set with `install.sh --plugin ai --slot 3` today is lost when the
  service is rewritten → `install.sh` prints the slot each plugin gets, and
  `--slot ai=3` restores it.
- A crash in one plugin's loop takes the other key's daemon down too →
  launchd's `KeepAlive` restarts the process within 5 s; each loop already
  catches its own read and socket errors.

## Open questions

- [ ] Should a plain `install.sh` keep running every plugin, or only the ones
  Keith names? The default here is every plugin. — kgheacock

## Notes

Keith asked on 2026-10-21 whether the two inbox daemons (`basic` and `ai`)
can be one; `macropadd` stays separate.
