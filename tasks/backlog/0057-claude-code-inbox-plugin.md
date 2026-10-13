---
id: "0057"
title: "Light a key per Claude Code session that needs an answer, and jump to it on press"
status: "backlog"
created: "2026-10-13"
updated: "2026-10-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-53z4fi"
related: ["0013", "0015", "0028", "0054", "0055", "0056"]
tags: ["driver", "plugin", "macos", "claude-code"]
---

# 0057 — Light a key per Claude Code session that needs an answer, and jump to it on press

## Problem

A person runs several Claude Code sessions at once, in different
terminal windows, tabs, and desktops. A session that stops on a
permission prompt, a question, or the end of its turn waits silently
until the person finds it. They find out by cycling through windows.

Task 0013's `claude-status` plugin shows one session on one key. It
cannot tell sessions apart:

- The `Notification` and `Stop` hooks run `macrodriver signal --key 0`.
  The key index is fixed in `settings.json`, so every session that uses
  the same settings lights the same key.
- A `signal` carries only `keyIndex` and `name`. It has no session id,
  no project, and no way back to the terminal the session runs in.
- A press on the key does nothing.

## Dependency

This task builds on task 0055, the plugin host. It starts only after
0055's implementation is on `main`. The inbox is a host plugin: it uses
the host's key ownership, input routing, pages, bus, `keyimg` helpers,
and `macos` helpers. It switches windows the way task 0056's
`window-switcher` does, through `macos.ActivateApp`,
`macos.RaiseWindow`, and `macos.AppleScript`, and it publishes
`window.willHide` before a jump, as 0056 does.

## Goals

- Each running Claude Code session gets its own key, labelled with its
  project folder's name.
- A key shows its session's state by color: working, needs an answer,
  or finished its turn. A session that needs an answer blinks.
- A press on a key brings that session's terminal window, tab, or pane
  to the front, on whatever desktop it is on.
- A long press marks the session seen: the key stops blinking until
  the session changes state again.
- A session keeps its key while it runs, so the person learns where
  each one is.
- More sessions than keys still work: the last key shows how many
  sessions are off the pad, and a press on it jumps to the
  longest-waiting one.
- A session that ends, or whose process dies, frees its key.
- A hook never slows Claude Code down or makes it fail, even when the
  plugin host is not running.

## Non-goals

- Answering a prompt from the pad (approve, deny, send text). A later
  task can add it on top of the session record this task writes.
- Sessions outside a local terminal: the Claude desktop app, claude.ai,
  and cloud sessions. They have no terminal to raise.
- Linux and Windows. The jump is macOS only. The session tracking is
  plain Go and runs anywhere.
- Changes to the daemon, the wire protocol, or the host. A gap in the
  host's API goes under Open questions for task 0055, not patched here.
- Replacing `claude-status`. It stays for a person who wants one key
  for one session, with no host config.

## Approaches considered

### Approach A — Add session fields to `signal` and keep hooks on the WebSocket

Extend `plugin.SignalPayload` with optional `session`, `cwd`, and
`terminal` fields. A new `macrodriver claude-hook` sends them. The
inbox listens with `h.OnSignal`.

- Good, because events reach the plugin at once, with no polling.
- Good, because the virtual pad and non-Go plugins can see the same
  signals.
- Bad, because it changes the wire protocol and the daemon, which tasks
  0055 and this task hold fixed.
- Bad, because a signal sent while the host is down is lost. A host
  restart starts with no sessions until each one fires a hook again.
- Bad, because every hook pays a WebSocket dial. With the daemon down,
  the hook waits on a dial timeout inside Claude Code.

### Approach B — Hooks write one state file per session, and the plugin watches the folder

A new `macrodriver claude-hook` reads the hook's JSON from stdin and
writes `~/.local/state/macropad/claude/<session_id>.json` atomically.
The inbox reads the folder on a short tick.

- Good, because no daemon, protocol, or host change is needed.
- Good, because the state outlives the host. A host restart, or a host
  started after the sessions, shows every live session at once.
- Good, because a hook only writes a small file. It takes a few
  milliseconds and cannot block on the network.
- Good, because the file is easy to test: tests write files into a temp
  folder, with no hook and no daemon.
- Bad, because a tick adds up to its period of delay, 250 ms by
  default.
- Bad, because a session that crashes leaves a file behind. The plugin
  must detect a dead process and remove it.

### Approach C — The plugin opens a local socket, and hooks post to it

The inbox listens on a Unix socket. `macrodriver claude-hook` sends
each event to it.

- Good, because events arrive at once.
- Good, because there is no daemon or protocol change.
- Bad, because the state lives only in the host's memory. A restart
  loses every session.
- Bad, because a hook with the host down must time out or drop the
  event, and the dropped event is gone.
- Bad, because it adds a listener and its own small protocol to a host
  plugin, which the host does not offer as a helper.

## Decision

Chosen: **Approach B — one state file per session, read on a tick**.

The folder is the only option that keeps the daemon and host unchanged
and survives a host restart. A person often starts sessions before the
host, so state that waits for the host matters more than the 250 ms
the tick costs. The task accepts the stale-file cleanup, covered under
Design.

## Design

### Package and command

- `driver/plugins/claudeinbox` — the host plugin, registered in
  `driver/cmd/macropad-plugins/plugins.go` as `claude-inbox`.
- `driver/claudesession` — the session record type, the folder path,
  and atomic read and write. Shared by the plugin and the hook command,
  so both agree on the format.
- `driver/cmd/macrodriver` — a new `claude-hook` subcommand.

### Session record

One JSON file per session, named by the session id from the hook's
input:

```json
{
  "version": 1,
  "sessionId": "8f2c...",
  "cwd": "/Users/keith/src/macro-pad",
  "state": "waiting",
  "reason": "permission_prompt",
  "message": "Claude needs your permission to use Bash",
  "updated": "2026-10-13T23:40:12Z",
  "since": "2026-10-13T23:40:12Z",
  "pid": 41873,
  "terminal": {
    "program": "iTerm.app",
    "itermSession": "w0t2p0:6A1F...",
    "tty": "/dev/ttys004",
    "tmuxPane": ""
  }
}
```

- `state` is one of `working`, `waiting`, or `done`.
- `since` changes only when `state` changes. `updated` changes on every
  write.
- `pid` is the Claude Code process: the nearest ancestor of the hook
  process whose command name contains `claude`, else the hook's parent.
- `terminal` comes from the hook's environment: `TERM_PROGRAM`,
  `ITERM_SESSION_ID`, `TERM_SESSION_ID`, `TMUX_PANE`, and the tty of
  `pid`. Fields it cannot find stay empty.
- `version` lets a later task change the format. The plugin skips a
  file with a version it does not know, and logs it once.

Writes go to a temp file in the same folder, then `rename`, so a reader
never sees half a file.

### `macrodriver claude-hook`

Reads one hook input from stdin and updates that session's file:

| Hook event | New `state` | Notes |
|---|---|---|
| `SessionStart` | `working` | Creates the file. |
| `UserPromptSubmit` | `working` | The person answered. |
| `PostToolUse` | `working` | A permission was granted and the tool ran. |
| `Notification` | `waiting` | `reason` and `message` from the input, when present. |
| `Stop` | `done` | Claude finished its turn. |
| `SessionEnd` | — | Deletes the file. |

An event not in the table is ignored. The command always exits 0 and
prints nothing to stdout, so it never blocks or changes Claude Code's
behavior. An error goes to stderr and to `claude-hook.log` in the
state folder. `--dir` overrides the folder.

The README gives the full `settings.json` block, one entry per event,
each running `macrodriver claude-hook`. It goes in the user's
`~/.claude/settings.json` so every project reports.

### Host config entry

```json
{"id": "inbox", "use": "claude-inbox", "page": "work", "keys": [0, 1, 2, 3, 4],
 "config": {
   "dir": "~/.local/state/macropad/claude",
   "show": "all",
   "staleAfter": "12h",
   "showPageOnWait": false
 }}
```

- `dir` defaults to `~/.local/state/macropad/claude`.
- `show` is `all` (every live session) or `waiting` (only `waiting`
  and `done`). Default `all`.
- `staleAfter` drops a session whose file has not changed for that
  long, even if its process looks alive. Default `12h`.
- `showPageOnWait` calls `h.ShowPage` with the inbox's page when a
  session moves to `waiting` while that page is hidden. Default
  `false`.
- `Start` returns an error for a bad `show`, a bad duration, or a `dir`
  it cannot create. The host disables only this instance.

### Tracking

`h.Every(250 * time.Millisecond, ...)` reads the folder. A file is
read again only when its modification time or size changed. On each
tick, the plugin:

1. drops any session whose `pid` no longer runs, or whose `updated` is
   older than `staleAfter`, and deletes its file;
2. keeps each known session on the key it already has;
3. gives each new session the lowest free key, in order of `since`;
4. repaints only keys whose session or state changed.

With more sessions than owned keys, the last owned key becomes the
overflow key. It shows `+N`, where N counts the sessions not on a key,
and turns amber when any of them is `waiting`. When a key frees up,
the overflow session that has waited longest takes it.

### Key look

Each key is an image from `keyimg.Text`: the project folder's name
from `cwd` on up to two lines, over a background color for the state.

| State | Background | Blink |
|---|---|---|
| `working` | `keyimg.Palette` color for the key, dimmed to 0.25 | no |
| `waiting` | amber, as `SetState("Waiting")` | yes, until seen |
| `done` | green, as `SetState("Done")` | no |
| seen `waiting` | amber, dimmed to 0.5 | no |
| free key | cleared | no |

Two sessions in folders with the same name get a short suffix from the
session id, such as `macro-pad·8f`.

### Press, long press

A trigger is `h.OnPress` for a jump and `h.OnClick(Long)` for seen,
matching task 0056's trigger names. On a press on a session's key, the
plugin:

1. reads `macos.Frontmost()` and publishes `window.willHide` with its
   window id, as task 0056 does; an error here logs and does not stop
   the jump;
2. raises the session's terminal, by the first rule that applies:
   - `itermSession` set → AppleScript selects the iTerm2 session with
     that unique id, its tab, and its window, then activates iTerm2;
   - `program` is `Apple_Terminal` and `tty` set → AppleScript selects
     the Terminal tab with that tty and brings its window to the front;
   - `tmuxPane` set → after raising the terminal by the rules above,
     runs `tmux select-window` and `select-pane` for that pane;
   - else → `macos.ActivateApp` for the app named by `program`, then
     `macos.RaiseWindow(app, base(cwd))`, which matches editors such as
     VS Code whose window titles hold the folder name;
3. marks the session seen.

A press on the overflow key jumps to the longest-waiting overflow
session, or the oldest one when none waits. A failed jump logs one line
with the session id and the rule tried. The key does not change.

macOS moves to the desktop of the raised window when "When switching
to an application, switch to a Space with open windows for the
application" is on, which is the default. The README says so.

Long press marks the session seen without jumping. Seen clears when the
session's `state` or `since` changes.

### Relation to `claude-status`

The inbox does not use `processWaiting` or `processDone`. Those signals
carry no session id. A person can run both: `claude-status` on one key
from `macrodriver signal`, and the inbox on others from
`macrodriver claude-hook`. The README shows the inbox setup first.

Files to change:

- `driver/claudesession/*.go` — record type, folder path, atomic write, read, liveness check
- `driver/cmd/macrodriver/claude_hook.go` — the hook subcommand and its tests
- `driver/plugins/claudeinbox/plugin.go` — config, tracking, slots, key look
- `driver/plugins/claudeinbox/jump.go` — the raise rules over `macos` helpers
- `driver/plugins/claudeinbox/*_test.go` — tests on `hosttest` with a fake `macos` and a temp folder
- `driver/cmd/macropad-plugins/plugins.go` — one registry line
- `driver/README.md` — the hook setup, the plugin config, and Mac test steps, in the "Plugin host" section

## Definition of done

An outside reviewer verifies each item without help from the
implementer. Each item names its proof. The task moves to `complete/`
only when every box is ticked.

- [ ] **DoD-1** — `macrodriver claude-hook` fed each hook event from the
  table writes the matching `state`, keeps `since` when the state does
  not change, and deletes the file on `SessionEnd`. **Proof:**
  `cd driver && go test ./cmd/macrodriver -run TestClaudeHook_States`
- [ ] **DoD-2** — `claude-hook` with bad JSON on stdin, an unwritable
  folder, or an unknown event exits 0 and writes nothing to stdout.
  **Proof:** `go test ./cmd/macrodriver -run TestClaudeHook_NeverFails`
- [ ] **DoD-3** — A reader that runs during 1,000 concurrent writes
  never decodes a partial file. **Proof:**
  `go test ./claudesession -run TestAtomicWrite -race`
- [ ] **DoD-4** — Three session files in the folder light three owned
  keys in order of `since`, and a fourth session added later takes the
  next free key while the first three keep theirs. **Proof:**
  `go test ./plugins/claudeinbox -run TestSlots_Sticky`
- [ ] **DoD-5** — A session whose `pid` is dead, or whose `updated` is
  older than `staleAfter`, frees its key and its file is removed.
  **Proof:** `go test ./plugins/claudeinbox -run TestStaleSessions`
- [ ] **DoD-6** — With seven sessions on five keys, the fifth key shows
  `+3`, turns amber when one of the three waits, and a press on it
  jumps to the one waiting longest. **Proof:**
  `go test ./plugins/claudeinbox -run TestOverflow`
- [ ] **DoD-7** — Each state draws the color and blink in the Key look
  table, and a long press stops a `waiting` key blinking until its
  state changes. **Proof:** `go test ./plugins/claudeinbox -run TestKeyLook`
- [ ] **DoD-8** — A press picks the raise rule from the record (iTerm2,
  Terminal, tmux, fallback) and calls the matching fake `macos` call
  with the right id, tty, or title. `window.willHide` is published
  first. **Proof:** `go test ./plugins/claudeinbox -run TestJump`
- [ ] **DoD-9** — A failed jump logs one line naming the session and
  leaves every key unchanged. **Proof:**
  `go test ./plugins/claudeinbox -run TestJumpFailure`
- [ ] **DoD-10** — With `showPageOnWait: true` and the inbox page
  hidden, a session moving to `waiting` calls `h.ShowPage` once.
  **Proof:** `go test ./plugins/claudeinbox -run TestShowPageOnWait`
- [ ] **DoD-11** — On a Mac with the README's hook setup, three Claude
  Code sessions in iTerm2 on two desktops each get a key named for
  their folder. A permission prompt in one turns its key amber and
  blinking within 1 s, and a press brings that tab to the front on its
  desktop. **Proof:** manual run on the board or `macropadd --emulate`,
  logged in the PR description
- [ ] **DoD-12** — The same run in Apple Terminal, and in VS Code's
  terminal, raises the right window. **Proof:** manual run, logged in
  the PR description, with any app where the raise fails noted
- [ ] **DoD-13** — Quitting a session with `/exit` frees its key, and
  killing one with `kill -9` frees its key within 1 s. **Proof:**
  manual run, logged in the PR description
- [ ] **DoD-14** — Starting the host after three sessions are already
  running lights all three keys on its first tick. **Proof:** manual
  run, logged in the PR description
- [ ] **DoD-15** — `driver/README.md` has the `settings.json` hook
  block, the plugin config, the Space-switching note, and the Mac test
  steps. **Proof:** `driver/README.md`
- [ ] **DoD-16** — `cd driver && go build ./... && go vet ./...` exits 0
  on Linux and on macOS. **Proof:** the command's exit code on each OS
- [ ] **DoD-17** — The PR in the `pr` field links to this spec.
  **Proof:** PR body

## Risks

- Task 0055's final API differs from its spec → this spec's Design
  follows the merged code, and the differences are noted in the
  implementing PR.
- Claude Code renames hook events or input fields → `claude-hook`
  ignores what it does not know, so the inbox goes quiet instead of
  breaking Claude Code. DoD-11 confirms the field names on the
  installed version.
- The `pid` walk finds the wrong process → a live session could be
  dropped, or a dead one kept until `staleAfter`. DoD-13 checks both
  on a Mac; `staleAfter` bounds the second case.
- `Notification` also fires after 60 s idle at the prompt, not only on
  a permission prompt → a `done` session turns `waiting`. Both mean the
  person owes an answer, so the key lights either way; `reason` keeps
  them apart for a later task.
- A terminal the raise rules do not know (Ghostty, WezTerm, Warp) →
  the fallback activates the app and matches a window by folder name,
  which may pick the wrong window. DoD-12 records what happens, and a
  new rule can be added per app.
- Two sessions in one terminal window, in split panes, without tmux →
  the raise selects the window or tab, not the pane.
- A 250 ms tick reading a folder costs a little power on a laptop → the
  plugin stats files and only reads changed ones. A later task can use
  FSEvents.

## Open questions

- [ ] Should `done` light green, or count as `waiting` and blink too,
  since in both cases the session waits on the person? The default is
  green, no blink. — kgheacock
- [ ] Should a press also mark the session seen, or only a long press?
  The default is both. — kgheacock
- [ ] Should the inbox own a page of its own, with the window switcher
  on another page, or share a page with it? The default config shares
  page `work`, with five inbox keys and the pager on key 5. — kgheacock
- [ ] Should `claude-hook` replace `macrodriver signal` in the README's
  hook example, so `claude-status` reads the session files too? The
  default is no; both stay. — kgheacock
