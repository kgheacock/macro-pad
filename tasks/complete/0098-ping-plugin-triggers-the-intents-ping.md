---
id: "0098"
title: "A ping inbox plugin: one key press runs the Ping intent of macro-spotlight-intents"
status: "complete"
created: "2026-11-23"
updated: "2026-11-23"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/128"
branch: "0098-ping-plugin-triggers-the-intents-ping"
related: ["0029", "0075", "0085"]
tags: ["inbox", "plugin", "intents", "macos"]
---

# 0098 — A ping inbox plugin

## Problem

The repository [macro-spotlight-intents](https://github.com/kgheacock/macro-spotlight-intents)
holds App Intents that this pad is meant to trigger. It has one intent, `Ping`,
and it logs each run (its task 0002). Today a person starts `Ping` by hand, in
Spotlight or in the Shortcuts app. No key on the pad starts it. Until a key press
runs an intent, the link between the pad and its intents is not proved.

## Goals

- A plugin `ping` (`inbox --plugin ping`) draws one key, `ping`. A press on its
  one child, `Ping`, runs the `Ping` intent.
- The child shows `RUNNING` while the run lasts, then `DONE` or `ERROR`.
- One command proves the chain: the intents app logs `PingIntent started` after a press.

## Non-goals

- Other intents, or one plugin for any intent. The second intent decides that shape.
- Opening Spotlight with keystrokes (Approach B).
- The missing dialog in Spotlight. That belongs to macro-spotlight-intents.
- An `install.sh` flag. An environment variable is enough for one plugin.
- Linux. The action needs macOS.

## Approaches considered

Three approaches follow. Each one reaches the intent in a different way.

### Approach A — Run a saved shortcut with `shortcuts run`

The plugin calls `execFile('shortcuts', ['run', NAME])`. A saved shortcut named
`Ping` holds the `Ping` action. The exit code gives the result.

- Good, because `shortcuts` is Apple's own command and needs no new permission.
- Good, because `execFile` uses no shell, and the repository changes only here.
- Bad, because a person must make one saved shortcut by hand for each intent.
- Bad, because the path is Shortcuts and not Spotlight, so Spotlight stays unproved.

### Approach B — Type into Spotlight with `osascript`

The plugin sends Cmd+Space, types `Ping`, and presses Return through System Events.

- Good, because it repeats exactly what Keith does by hand.
- Good, because it needs no saved shortcut.
- Bad, because the daemon needs the Accessibility permission, and the keys go to the focused app if Spotlight does not open.
- Bad, because Spotlight lists two `Ping` results (a shortcut and the app), and the first one wins by rank, not by choice.

### Approach C — A URL scheme in the intents app, opened with `open -g`

The intents app handles `macro-spotlight-intents://run/ping` and calls the intent. The plugin runs `open -g URL`.

- Good, because it needs no saved shortcut and no Spotlight ranking.
- Good, because a URL carries an intent name, so more intents need no new plugin code.
- Bad, because it needs a new task in macro-spotlight-intents, in a second repository.
- Bad, because it skips the system's intent registration. A registration fault, the thing that repository's task 0001 checks, would not show.

## Decision

Chosen: **Approach A — `shortcuts run`**.

The goal is the shortest proof, from a key press to `PingIntent started`, through
the system's own path from Shortcuts to the app. The cost is a saved shortcut that
a person makes by hand, and Spotlight itself is not tested. If Keith wants the
Spotlight path, Approach B is the next task.

## Design

`inbox/src/plugins/ping.js` follows `apps.js`: it sets `name`, `summary`,
`statuses` (`RUNNING`, `DONE`, `ERROR`), `createRenderer`, and `press`. The top
key `ping` opens a layer with one child, `Ping`.

A press on the child:

1. Sets the child to `RUNNING`.
2. Runs `shortcuts run NAME` with `execFile`, with its stdin closed and a 20 s timeout.
3. Sets `DONE` on exit code 0. Otherwise it sets `ERROR` and logs the exit code and the first line of stderr.

`NAME` comes from `INBOX_PING_SHORTCUT`, default `Ping`. A name that does not
start with a letter or digit, or that is longer than 128 characters, is refused
and logged. This stops `shortcuts` from reading it as an option, as `here.js` does for bundle IDs.

Files to change:

- `inbox/src/plugins/ping.js` — new
- `inbox/src/plugins/index.js` — list the plugin in `PLUGINS`
- `inbox/src/apps.js` — export `run`, the `execFile` helper that `ping.js` reuses
- `inbox/test/ping.test.js` — new, with a fake `execFile`
- `inbox/test/cli.test.js` — the lists of plugins now include `ping`, because one daemon runs every plugin and gives `ping` a key slot (task 0075)
- `inbox/README.md` — new section "The ping plugin", beside "The apps plugin"

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [x] **DoD-1** — `inbox plugins` lists `ping`. **Proof:** `node inbox/bin/inbox.js plugins` prints a `ping` line
- [x] **DoD-2** — A press on the `Ping` child runs `shortcuts` with exactly `run Ping`, with no shell. **Proof:** `cd inbox && node --test test/ping.test.js` passes the test `runs shortcuts run Ping with execFile`
- [x] **DoD-3** — Exit code 0 sets `DONE`. A non-zero exit sets `ERROR` and writes one log line with the exit code. **Proof:** the tests `sets DONE` and `sets ERROR and logs the exit code` pass
- [x] **DoD-4** — A run longer than 20 s sets `ERROR`. **Proof:** the test `times out after 20 s` passes
- [x] **DoD-5** — `INBOX_PING_SHORTCUT=-x` runs nothing and logs a refusal. **Proof:** the test `refuses a name that starts with a dash` passes
- [x] **DoD-6** — The whole inbox suite still passes. **Proof:** `cd inbox && npm test` exits 0
- [x] **DoD-7** — On Keith's Mac, a key press writes `PingIntent started` and `PingIntent finished` in the intents app log. **Proof:** with the saved shortcut `Ping` and the app installed, press the `Ping` key (or press it on `driver/plugin/web/virtualpad.html`), then run `/usr/bin/log show --last 1m --predicate 'subsystem == "<prefix>.MacroSpotlightIntents"'`. `<prefix>` is `BUNDLE_ID_PREFIX` in macro-spotlight-intents `Config/Signing.local.xcconfig`
- [x] **DoD-8** — `inbox/README.md` records the plugin, the variable, and the saved shortcut it needs. **Proof:** `inbox/README.md`, section "The ping plugin"
- [x] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:** PR body
- [x] **DoD-10** — The plugin closes the stdin of `shortcuts`. Left open, `shortcuts run` waits for input and never ends. **Proof:** the test `closes stdin so shortcuts does not wait for input` passes, and DoD-7 shows `PingIntent started` after a press that goes through the daemon

## Risks

- A daemon started by `launchd` may lack the right environment for `shortcuts` → DoD-7 runs it through the installed service, not only from a terminal.
- The saved shortcut is missing → `shortcuts run` exits non-zero, the key shows `ERROR`, and the log line names the exit code.

## Open questions

- [ ] Does `shortcuts run Ping` show the dialog, or only run the intent? — Keith, during DoD-7
- [ ] Write the plugin in `.js` like the rest of `inbox/src`, or add TypeScript? the workspace `CLAUDE.md` says scripts are TypeScript, and `inbox` has no TypeScript build — Keith

## Notes

- This plugin is not the `pingpong` command (`driver/cmd/pingpong`) or `firmware/ping_pong.py`. Those check the USB link to the board.
- Measured on 2026-11-23: `shortcuts run Ping` takes 5.5 to 6.9 s here, though the intent takes 0 ms. A 10 s timeout left too little room, so the limit is 20 s.
- The first version left stdin open, and the daemon logged `shortcuts did not answer within 10 s`. A Node test with a fake `execFile` could not show it. Only a run through the daemon did (DoD-10).
- Depends on macro-spotlight-intents tasks 0001 and 0002 (the `Ping` intent and its log lines).

Evidence, 2026-11-23:

- DoD-1: `node inbox/bin/inbox.js plugins` prints `ping`.
- DoD-2 to DoD-5, DoD-10: `node --test test/ping.test.js` passes 14 tests. Without the fix to `ping.js`, the stdin test and the 20 s test fail.
- DoD-6: `cd inbox && npm test` exits 0, with 237 tests passing.
- DoD-7, through the virtual-pad path: `macropadd --emulate` on a private port, the inbox daemon with `--plugin ping`, and an injected press on the `ping` key, then on its `Ping` child. macropadd sent `childPressed {parent: "ping", child: "run"}`. The app log then showed `PingIntent started` and `PingIntent finished in 0 ms` 0.2 s after the press.
- A press on the physical board was not confirmed. During the checks the board sent no key events: a raw read of its serial port gave 0 bytes while keys were pressed. That fault is below this plugin and is not part of this task.
