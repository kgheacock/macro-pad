---
id: "0065"
title: "An inbox daemon that watches the store and feeds macropadd, with install and uninstall scripts"
status: "ongoing"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/77"
branch: "0065-inbox-daemon-with-install-and-uninstall"
related: ["0055", "0057", "0064", "0066", "0067"]
tags: ["inbox", "daemon", "launchd", "systemd"]
---

# 0065 — An inbox daemon that watches the store and feeds macropadd, with install and uninstall scripts

## Problem

Task 0064 stores messages, but nothing shows them. A person must run
`inbox list` to learn that anything arrived. The pad should show the unread
counts without anyone asking, and keep doing so across logins and restarts
of `macropadd`.

Something long-lived must watch the store and talk to `macropadd`. Starting
it by hand in a terminal, as plugins are started today, means it is not
running after a reboot. A person needs one command to install it as a login
service and one to remove it.

This task builds that process and its lifecycle. Task 0066 says what it
draws, and task 0067 says what a press does.

## Goals

- `inbox daemon` runs until stopped. It notices a change to the store within
  1 s and sends the new state to `macropadd`.
- It starts with or without `macropadd` running, and survives `macropadd`
  restarts. It reconnects and sends its full state again.
- Only one daemon runs per store.
- `inbox/scripts/install.sh` puts `inbox` on the `PATH` and starts the
  daemon at login. Running it again upgrades in place.
- `inbox/scripts/uninstall.sh` stops the daemon, removes the service and the
  `inbox` command, and keeps the store unless asked.
- macOS uses a launchd LaunchAgent. Linux uses a systemd user unit.

## Non-goals

- The key layout, images, and counts. Task 0066.
- Marking seen on a press. Task 0067.
- Starting `macropadd` or the plugin host at login. This service starts the
  inbox daemon only.
- A Homebrew formula or a package.

## Approaches considered

Three approaches follow. They differ in which process draws the inbox.

### Approach A — A plugin in the task 0055 host

`driver/plugins/inbox` exports a `host.Factory`, and the host's config gives
it a slot.

- Good, because it follows the plugin convention of task 0055, with
  reconnect, routing, and tests from `hosttest`.
- Bad, because task 0055 is still an open PR (#65) and is not on `main`.
  This task would wait on it.
- Bad, because the host is Go and the inbox is Node, so the inbox cannot
  be a host plugin without a rewrite in Go.
- Bad, because the code would live in `driver/plugins/`, not in the new
  `inbox/` folder Keith asked for, and the driver would gain SQLite.
- Bad, because the inbox then runs only while the plugin host runs, and its
  install script would have to install the host.

### Approach B — A standalone daemon in `inbox/`, a WebSocket client of `macropadd`

`inbox daemon` is a subcommand of the task 0064 command. It talks to
`macropadd` with `setLayerKey`, as `macro_streamer` does (task 0057).

- Good, because everything inbox lives in `inbox/` and ships as one package.
- Good, because it works on `main` today, with no wait on task 0055.
- Good, because the layer tree already lets several clients share the keys,
  so it coexists with the plugin host when that lands.
- Bad, because it writes its own dial, reconnect, and press filtering, which
  task 0055's host would give it.
- Bad, because it is one more process to install.

### Approach C — `macropadd` reads the inbox store itself

The daemon opens the inbox file and draws the inbox keys, as task 0051's
poller once read a state database.

- Good, because there is no second process.
- Bad, because task 0057 removed exactly this kind of poller from the
  daemon, to keep its state in one place.
- Bad, because the driver gains SQLite and inbox rules it does not own.

## Decision

Chosen: **Approach B — a standalone daemon in `inbox/`**.

Keith asked for the inbox in its own folder, with its own daemon and
install script. B gives that, and works without task 0055. The task accepts
a small client of its own. When task 0055 lands, the client part can move to
`host` helpers or stay as is; the layer tree makes both work.

## Design

### Command

```
inbox daemon [--db PATH] [--port 8765] [--assets-dir DIR] [--slot N] [--poll 500ms]
```

- `--port` is `macropadd`'s port on 127.0.0.1, `plugin.DefaultPort` by
  default.
- `--assets-dir` must be the same directory `macropadd` uses. Task 0066
  writes generated images under it.
- `--slot` and the key layout are task 0066's.
- The client uses Node's built-in `WebSocket` (Node 22), so it needs no
  npm package. `inbox/src/protocol.js` builds the `setLayerKey` and reads the
  `event` and `layerChanged` messages, as `driver/README.md` "Protocol"
  documents them. A test checks its JSON against fixtures copied from
  `driver/plugin` tests.

### Watching the store

The daemon keeps one read connection open and runs `PRAGMA data_version`
every `--poll`. The value changes when any other connection commits. On a
change it runs `unseen()` and hands the result to the renderer of task
0066. A poll that sees no change does no other query. The daemon also runs
one full read every 30 s, as a guard against a missed change.

### Talking to `macropadd`

- Dial `ws://127.0.0.1:PORT/` with backoff from 250 ms to 5 s. Log once per
  change of state (up, down), not once per try.
- On every connect: read the first `layerChanged`, send the full inbox state
  from task 0066, then send only changes.
- While disconnected, keep reading the store, and keep only the newest
  state. Send it on reconnect.
- Read every message from `macropadd`. Keep the active layer from
  `layerChanged`. Pass press events to task 0067.

### One daemon per store

Node has no `flock`. At start the daemon creates `<db>.daemon.pid` with the
`wx` flag and writes its pid. If the file exists and that pid is alive
(`process.kill(pid, 0)`), it prints
`inbox: a daemon already runs for <db> (pid N)` and exits with code 3. If
that pid is gone, the file is stale: it is replaced, and the daemon logs it.
The daemon removes the file on exit.

### Help and error messages

`inbox daemon -h` and `--help` print the flags with their defaults, and exit
0. Errors follow task 0064's form: one line on standard error that starts
with `inbox:`, names the bad value, and says what to do. Examples:

- `inbox: --slot 7 is out of range; use 0 to 5`
- `inbox: cannot reach macropadd on 127.0.0.1:8765 (connection refused); retrying every 5 s` (logged once per change of state)
- `inbox: assets dir "assets" is relative; pass an absolute path, as macropadd sees it`

### Stopping

SIGINT and SIGTERM close the WebSocket cleanly and exit 0. The daemon does
not clear its keys on exit: `macropadd` keeps them in `layers.json`, and
the next daemon start corrects them.

### Install

`inbox/scripts/install.sh [--assets-dir DIR] [--port N] [--slot N]`:

1. Check that `node` is 22.13 or newer, and record its absolute path, because
   launchd and systemd do not see the shell's `PATH` (nvm, Homebrew). Write
   `$PREFIX/bin/inbox`, with `PREFIX=$HOME/.local` by default: a two-line
   `sh` wrapper that runs that `node` on the repo's `inbox/bin/inbox.js`. The
   service runs the same wrapper.
2. On macOS, write
   `~/Library/LaunchAgents/com.kgheacock.macropad.inbox.plist` with
   `RunAtLoad`, `KeepAlive`, `ThrottleInterval` 5, the daemon flags, and
   logs at `~/Library/Logs/inbox-daemon.log`. Then
   `launchctl bootout` any old copy and `launchctl bootstrap gui/$UID` the new
   one.
3. On Linux, write `~/.config/systemd/user/inbox-daemon.service` with
   `Restart=always` and `RestartSec=5`, then
   `systemctl --user daemon-reload` and `systemctl --user enable --now`.
4. Print where the wrapper, the service file, the log, and the store are, and
   whether the daemon is running.

The script is idempotent. A second run rewrites the wrapper and restarts the
daemon. The repo checkout must stay where it is; the README says to rerun
the script after moving it or after changing Node versions. `--assets-dir` defaults to the repo's `assets/` as an absolute path,
which matches `macropadd` run from the repo root. The script refuses a
relative path, because launchd starts the daemon in `/`.

### Uninstall

`inbox/scripts/uninstall.sh [--purge]`:

1. Stop and remove the LaunchAgent or the systemd unit.
2. Remove the `$PREFIX/bin/inbox` wrapper.
3. With `--purge`, also remove the store and its `-wal` and `-shm` files.
   Without it, print where the store is.

Running it when nothing is installed exits 0 and says so.

Both scripts are POSIX `sh`, pass `shellcheck`, and take `--dry-run` to
print each action without doing it. Both take `-h` and `--help`, which print
their options and what they install or remove, and exit 0. An unknown option
prints `install.sh: unknown option "--foo"; run 'install.sh -h' for help` and
exits 2. A failed step names the step and the command that failed, such as
`install.sh: node 18.19.0 is too old; install Node 22.13 or newer`, and exits 1.

Files to change:

- `inbox/src/cli.js` — the `daemon` subcommand
- `inbox/src/daemon.js` — the watch loop, the client, the pid file
- `inbox/src/protocol.js` — the messages
- `inbox/scripts/install.sh`, `inbox/scripts/uninstall.sh`
- `inbox/scripts/com.kgheacock.macropad.inbox.plist.tmpl`,
  `inbox/scripts/inbox-daemon.service.tmpl`
- `inbox/README.md` — a "Daemon" section

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — With a test WebSocket server in place of `macropadd`, a
  row inserted by another process reaches the server as a message within 1 s.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestChangeReachesPad test/daemon.test.js`
- [ ] **DoD-2** — A poll with no commit runs no query but `data_version`.
  **Proof:** `node --test --test-name-pattern=TestIdlePollIsCheap test/daemon.test.js`
- [ ] **DoD-3** — Started with no server listening, the daemon connects when
  one starts. After the server restarts, it sends its full state again.
  **Proof:** `node --test --test-name-pattern='TestConnectLate|TestResendOnReconnect' test/daemon.test.js`
- [ ] **DoD-4** — A second daemon on the same store exits with code 3 and
  the message above. **Proof:** `node --test --test-name-pattern=TestSecondDaemonRefused test/cli.test.js`
- [ ] **DoD-5** — SIGTERM exits 0 within 1 s. **Proof:** `node --test --test-name-pattern=TestDaemonSignal test/cli.test.js`
- [ ] **DoD-6** — `install.sh --dry-run` and `uninstall.sh --dry-run` print
  the plist path on macOS and the unit path on Linux, and change nothing.
  **Proof:** run both, then `git status` and `ls` the service paths
- [ ] **DoD-7** — On macOS, `install.sh` leaves
  `launchctl print gui/$UID/com.kgheacock.macropad.inbox` showing
  `state = running`. A second run still shows it running.
  `uninstall.sh` leaves `launchctl print` failing, and the store in place.
  **Proof:** the reviewer runs the scripts on Keith's Mac
- [ ] **DoD-8** — `uninstall.sh --purge` also removes the store files.
  **Proof:** `ls` the store path after the run
- [ ] **DoD-9** — `shellcheck inbox/scripts/*.sh` exits 0. **Proof:** the exit code
- [ ] **DoD-10** — `inbox/README.md` "Daemon" records the flags, the
  service paths, the log path, and how to install and uninstall. **Proof:**
  `inbox/README.md`
- [ ] **DoD-11** — `inbox daemon -h`, `install.sh -h`, and `uninstall.sh -h`
  (and `--help`) print usage and exit 0. An unknown option exits 2 with a
  message that names it. **Proof:** `node --test --test-name-pattern=TestDaemonHelp test/cli.test.js`
  and running the scripts
- [ ] **DoD-12** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- The daemon's `--assets-dir` and `macropadd`'s `--assets-dir` differ →
  the pad shows blank child keys. The install script passes the repo's
  `assets/` as an absolute path, and the README says to start `macropadd`
  with the same one.
- launchd starts the daemon before `macropadd` → the dial backs off and
  connects later (DoD-3).
- launchd does not see nvm's or Homebrew's `node` on `PATH` → the wrapper
  names `node` by absolute path, found when the install script runs in the
  user's shell.
- Polling every 500 ms costs a little CPU → `data_version` is a cheap pragma
  on an open connection, and DoD-2 checks no more runs.

## Open questions

- [ ] Should `install.sh` also install `macropadd` as a login service, so the
  pad works after a reboot with no terminal? The default is no; a later task
  can. — kgheacock

## Notes

Keith asked for the daemon and the install and uninstall scripts on
2026-10-18. Task 0055 (open PR #65) is the plugin host; this daemon is a
separate client of `macropadd` and does not need it.
