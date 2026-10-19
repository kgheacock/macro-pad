---
id: "0068"
title: "Messages carry a 'here', and a press on a channel key goes there"
status: "ongoing"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "spec-0068-inbox-here"
related: ["0064", "0065", "0066", "0067"]
tags: ["inbox", "input", "macos", "iterm"]
---

# 0068 — Messages carry a "here", and a press on a channel key goes there

## Problem

A message from `inbox pipe` comes from one iTerm session. Task 0067 lets a
press mark the channel seen. It cannot take the person to the session that
sent it. The person must find that tab by hand. The same holds for a message
that should lead to Slack, Teams, or Outlook.

## Goals

- `inbox send` and `inbox pipe` store a "here" with each message. In an iTerm
  session they find it without help.
- Where no "here" can be found, the person sets it by hand: on one command,
  or as a fallback for a channel.
- A press on a channel key in the Inbox layer brings that "here" to the front.
  It still marks the channel seen.
- One scheme names an iTerm session, a macOS app, or a deep link into one.

## Non-goals

- Other terminals, tmux panes, and remote hosts. They use a manual "here".
- Running a command. A "here" is never run as a shell command.
- Choosing between several "here" values in one channel. The newest message wins.
- Linux. The first task covers macOS only.

## Approaches considered

Three approaches follow. They differ in where the "here" comes from.

### Approach A — Capture a "here" per message

The sender stores a "here" string on each message. A per-channel fallback
covers a sender with none. The daemon reads it with the unseen counts.

- Good, because the "here" is exact for the run that sent the message.
- Good, because the `unseen()` query already joins the newest unseen row, so
  the daemon needs no second query.
- Bad, because it adds a schema version (3), one column, and one table.
- Bad, because a sender in tmux, ssh, or cron cannot find its "here" and
  needs a manual value.

### Approach B — One "here" per channel

Each send writes the sender's "here" over a `channel_here` row. The newest
sender wins.

- Good, because it adds one table and no column on `messages`.
- Good, because the daemon reads one row per channel.
- Bad, because two runs in one channel send the person to the later one,
  even if the earlier one sent the unseen message.
- Bad, because every send writes the channel row, so a sender with no "here"
  must clear or keep the old value, and both choices are wrong for someone.

### Approach C — Find the session when the key is pressed

Store nothing. On a press, the daemon asks iTerm for the session whose
working directory equals the message's `cwd`, which the store already holds.

- Good, because it changes no schema and no sender.
- Good, because old messages work too.
- Bad, because iTerm reports a working directory only with shell
  integration, and two tabs in one directory cannot be told apart.
- Bad, because Slack, Teams, and Outlook have no working directory, so this
  cannot meet the app goal.

## Decision

Chosen: **Approach A — a "here" per message, with a per-channel fallback**.

It is the only approach that names the exact session and also covers apps.
The task accepts a schema bump, and a manual value where detection fails.

## Design

### The scheme

A "here" is `KIND:VALUE`. The reader accepts only these kinds:

| Value | Meaning | Action |
|---|---|---|
| `iterm2:UUID` | An iTerm session. `UUID` is 36 hex digits and dashes. | `osascript` selects the window, tab, and session whose `unique id` is `UUID`, then activates iTerm. |
| `app:BUNDLE_ID` | A macOS app, such as `com.tinyspeck.slackmacgap`. | `open -b BUNDLE_ID` |
| `url:URL` | A deep link. The scheme must be `slack`, `msteams`, `ms-outlook`, or `https`. | `open URL` |

Every value is checked by pattern when stored and again when the daemon
reads it, because any process can write the store. The daemon runs
`execFile` with an argument list, never a shell. For `iterm2`, the UUID is a
script argument, not part of the script text.

### Capture

For `send` and `pipe`, the first source that gives a value wins:

1. `--here VALUE`
2. `$INBOX_HERE`
3. `$TERM_PROGRAM` is `iTerm.app`, `$TMUX` is unset, and `$ITERM_SESSION_ID`
   is `w0t0p0:UUID`. The value is `iterm2:UUID`.
4. None. The column stays NULL.

`pipe` resolves the value once, before the first line, as it does for the
channel. A bad value exits with code 2.

### Manual values

- `inbox here` prints the value that step 3 finds, or exits 1 and says to
  pass `--here`.
- `inbox here CHANNEL VALUE` sets the channel's fallback.
  `inbox here --clear CHANNEL` removes it. `inbox here CHANNEL` prints it.

### Store

Schema 3 adds `messages.here TEXT` (NULL by default) and
`channel_here(channel TEXT PRIMARY KEY, here TEXT NOT NULL)`. `unseen()`
returns `here` of the newest unseen row, else the channel's fallback.
`list --json` shows `here`.

### Daemon

`render.js` `target()` also returns `here`. After a press marks the channel
(task 0067), the daemon calls `focus(here)` when `here` is set. The
mark and the new counts go first. A failed focus is logged with the channel
and the kind. It never undoes the mark. `startDaemon` takes `focus` as an
option, so tests use a stub.

Files to change:

- `inbox/src/store.js` — schema 3, `insert*` take `here`, `unseen()`, fallback calls
- `inbox/src/here.js` — new: parse, detect, `focus`
- `inbox/src/cli.js` — `--here`, `inbox here`
- `inbox/src/render.js`, `inbox/src/daemon.js` — carry `here`, call `focus`
- `inbox/completions/_inbox` — `here` and `--here`
- `inbox/README.md` — "Here"

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — A schema 2 file opens as schema 3. Its old rows have a NULL
  `here`, and `channel_here` exists. **Proof:** `cd inbox && node --test --test-name-pattern=TestMigrateToHere test/store.test.js`
- [ ] **DoD-2** — The precedence is `--here`, then `$INBOX_HERE`, then iTerm
  detection. With `$TMUX` set, detection gives nothing. **Proof:** `node --test --test-name-pattern=TestHerePrecedence test/cli.test.js`
- [ ] **DoD-3** — `inbox pipe` stores the same `iterm2:UUID` on every line of a
  run. `list --json` shows it. **Proof:** `node --test --test-name-pattern=TestPipeStoresHere test/cli.test.js`
- [ ] **DoD-4** — `--here` rejects `iterm2:xyz`, `url:file:///etc/passwd`,
  `url:javascript:alert(1)`, and `sh:ls` with exit code 2. **Proof:** `node --test --test-name-pattern=TestHereRejects test/cli.test.js`
- [ ] **DoD-5** — `inbox here CHANNEL VALUE` sets a fallback, `--clear`
  removes it, and a message with no `here` takes the fallback. A message
  `here` wins over it. **Proof:** `node --test --test-name-pattern=TestChannelFallback test/cli.test.js test/store.test.js`
- [ ] **DoD-6** — A press on a channel key calls `focus` once with that
  channel's `here`, after the mark. A key with no `here` marks and calls
  nothing. The open press and key 3 call nothing. **Proof:** `node --test --test-name-pattern=TestPressFocuses test/daemon.test.js`
- [ ] **DoD-7** — `focus` runs `osascript` for `iterm2`, with the UUID as an
  argument. It runs `open -b` for `app` and `open` for `url`. It uses no
  shell. **Proof:** `node --test test/here.test.js`
- [ ] **DoD-8** — On the board, a press on a channel key fed by `inbox pipe`
  from an iTerm tab selects that tab and brings iTerm to the front. A
  `--here app:com.tinyspeck.slackmacgap` message brings Slack to the front.
  **Proof:** Keith's check, recorded in Notes
- [ ] **DoD-9** — `inbox/README.md` "Here" lists the scheme, the capture order,
  and the Automation prompt. **Proof:** `inbox/README.md`
- [ ] **DoD-10** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- The first `osascript` call needs the macOS Automation permission for iTerm.
  The spike hung until it was allowed, and the permission belongs to the
  `node` that runs the daemon, so a new Node path may ask again → the daemon
  times out after about 5 s and logs where to allow it, and the README names
  the prompt.
- `$ITERM_SESSION_ID` in tmux can name the session that started tmux, not
  the pane → detection is off when `$TMUX` is set.
- The bundle IDs and URL schemes for Slack, Teams, and Outlook are not
  verified here → DoD-8 checks Slack, and a wrong ID shows as an `open` error.
- The iTerm session is closed when the key is pressed → `osascript` finds no
  match, the daemon logs it, and the mark still stands.
- A process that can write the store sets a `here` → only the three kinds
  pass, and none runs a shell.

## Open questions

- [ ] Should the scheme list be an env setting, for apps that are not listed?
  — kgheacock
- [ ] When the session is closed, should the daemon still bring iTerm to the
  front? The spike script does not. — kgheacock
- [ ] Should a long press focus and a plain press only mark seen? This needs
  task 0013's click resolver. — kgheacock

## Notes

Keith asked for this on 2026-10-18, after the board check of task 0067.
Spike, 2026-10-18, on macOS with iTerm 3.7.1:

- `$ITERM_SESSION_ID` is `w0t0p0:UUID`, and `UUID` equals iTerm's `unique id`
  of the session. Five sessions were listed, and this one matched.
- The script in the Design section selected a different tab, from a shell
  and from a launchd job (`launchctl bootstrap`, then `kickstart -k`). The
  job exited 0 and printed `focused`. An unknown UUID exits 1 with
  `no iTerm session <uuid>`.
- The script is: loop over `windows`, `tabs of w`, and `sessions of t`; when
  `unique id of s` equals `item 1 of argv`, run `select w`, `select t`,
  `select s`, and `activate`; else `error "no iTerm session " & target`.
- The first launchd run hung with no output until the Automation prompt was
  allowed. So the daemon must run `osascript` with a timeout of about 5 s,
  and log where to allow it (System Settings, Privacy & Security, Automation).
- `launchctl submit` and `RunAtLoad` alone never started the job. Use
  `kickstart`, as `install.sh` does.
- A closed session: with the `/inbox` tab exited, the launchd job exited 1
  and wrote `no iTerm session <uuid>` to standard error. Focus stayed where it
  was, and iTerm was not brought forward, because the script fails before
  `activate`. The daemon logs that line and the mark stands.
- Not tried: `app:` and `url:` actions, Teams and Outlook IDs, and a Space
  other than the current one.
