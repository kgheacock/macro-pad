---
id: "0064"
title: "An SQLite inbox CLI with channels from the working directory and piped lines as messages"
status: "backlog"
created: "2026-10-18"
updated: "2026-10-18"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/77"
branch: "claude/project-thread-tl5p4v"
related: ["0057", "0065", "0066", "0067"]
tags: ["inbox", "cli", "sqlite"]
---

# 0064 — An SQLite inbox CLI with channels from the working directory and piped lines as messages

## Problem

Scripts, builds, and agents finish work in many terminals. Nothing collects
what they want to say. A person has to look at each terminal to learn that a
build failed or a job is done.

Task 0057's `macro_streamer` shows one status on one key while a pipe is
open. It keeps no messages. When the pipe closes, nothing records what
happened, and nothing knows what the person has already read.

The pad needs a place to send short messages that outlives the sender, keeps
them per project, and knows which ones are unread. Tasks 0065 to 0067 show
it on the pad. This task builds the store and the command line.

## Goals

- A new top-level folder, `inbox/`, holds all inbox code. It is its own Go
  module in `go.work`.
- One binary, `inbox`, writes and reads one SQLite file.
- Each message belongs to one channel.
- Under the source root (`~/source` by default), the channel comes from the
  working directory: the path below the root, joined by dots.
  `~/source/macro-pad/driver` gives `macro-pad.driver`.
- The channel is the first argument: `inbox send debug "my message"` and
  `my_cmd 2>&1 | inbox pipe debug`. A channel given this way always wins over
  the inferred one.
- Outside the source root, the channel argument is required. A missing
  channel is an error with exit code 2.
- Piped input is a first-class way to send: `inbox pipe` turns each input
  line into one message.
- Messages from a long-running pipe show up while the pipe is still open.
- A person can list, count, and mark messages seen from the command line.
- Several writers and one reader (task 0065's daemon) use the file at once.

## Non-goals

- The daemon and the pad. Tasks 0065, 0066, and 0067 own them.
- Deleting or expiring old messages. A later task can add `inbox prune`.
- Priority, severity, or a status per message. Every message is plain text.
- Sync across machines, or a network API.
- Windows.

## Approaches considered

Three approaches follow. They differ in where messages live.

### Approach A — One SQLite file, written by the CLI

Each `inbox` call opens the file in WAL mode and inserts rows. The daemon in
task 0065 reads the same file.

- Good, because SQLite handles several writers and readers, with locking
  and crash safety built in.
- Good, because "unread per channel" is one indexed query.
- Good, because no process must be running to send a message.
- Bad, because the pure-Go driver adds a large dependency to the new module.
- Bad, because the daemon must poll the file to see new rows.

### Approach B — One append-only JSON lines file per channel

`inbox` appends a line. A seen marker is a second file of ids.

- Good, because it needs no dependency, and `tail` can read it.
- Bad, because two writers that append long lines at once can interleave.
- Bad, because a count of unread messages reads every file.
- Bad, because marking messages seen rewrites or grows a second file, with
  no transaction across both.

### Approach C — The daemon owns the store, and the CLI talks to it

`inbox` sends each message to the daemon over a local socket.

- Good, because the daemon learns of a message at once, with no polling.
- Bad, because nothing can be sent while the daemon is down, which is when
  a person most wants a record.
- Bad, because it needs a protocol, a socket path, and a retry in the CLI.

## Decision

Chosen: **Approach A — one SQLite file, written by the CLI**.

Messages must survive with no daemon running, and unread counts per channel
are the main query. SQLite gives both. The task accepts the driver
dependency, and task 0065 accepts a poll.

## Design

### Layout

- `inbox/go.mod` — module `github.com/kgheacock/macro-pad/inbox`, go 1.22.0.
- `go.work` — gains `use ./inbox`.
- `inbox/cmd/inbox` — the binary. `main` only parses the subcommand.
- `inbox/store` — open, migrate, insert, query, and mark seen.
- `inbox/channel` — channel inference and validation.
- `inbox/README.md` — usage, the schema, and the environment variables.

The SQLite driver is `modernc.org/sqlite`, which is pure Go, so the binary
needs no cgo and cross-builds for macOS and Linux.

### Store

The file is `$INBOX_DB`, or `inbox.db` in the user's data directory
(`~/.local/share/inbox/` by default, from `$XDG_DATA_HOME` when set). The
directory is created with mode 0700 on first write.

```sql
CREATE TABLE messages (
  id         INTEGER PRIMARY KEY AUTOINCREMENT,
  channel    TEXT    NOT NULL,
  body       TEXT    NOT NULL,
  created_at INTEGER NOT NULL,  -- Unix milliseconds
  seen_at    INTEGER,           -- NULL while unseen
  cwd        TEXT    NOT NULL   -- the sender's working directory
);
CREATE INDEX messages_unseen ON messages (channel, id) WHERE seen_at IS NULL;
```

- `PRAGMA user_version` holds the schema version, starting at 1. `Open`
  migrates an older file in one transaction, and refuses a newer one with an
  error that names both versions.
- Every connection sets `journal_mode=WAL` and `busy_timeout=5000`.
- `store.MarkSeen(channel, upToID)` sets `seen_at` on unseen rows of that
  channel with `id <= upToID`, and returns how many rows changed. Task 0067
  calls it with the newest id the pad showed, so a message that lands during
  a press stays unseen.
- `store.Unseen()` returns, per channel with unseen rows: the channel, the
  unseen count, the newest unseen id, and its `created_at`.

### Channels

A channel is one or more segments joined by dots. A segment holds letters,
digits, `-`, and `_`. Anything else is an error that names the bad character.

The source root is `$INBOX_SOURCE_ROOT`, or `~/source`. Inference:

1. Resolve symbolic links in the working directory and in the root.
2. If the working directory is the root, or is not below it, there is no
   inferred channel.
3. Otherwise the channel is the path components below the root, joined by
   dots. `~/source/macro-pad/driver/layers` gives `macro-pad.driver.layers`.
4. A component that is not a valid segment, such as `my.repo` or `a b`, is an
   error that names the component and says to pass the channel as an
   argument.

A channel argument skips inference. With no channel argument and no inferred
channel, a command that needs a channel prints
`inbox: not under <root>; pass the channel, as in: inbox send CHANNEL MESSAGE`
and exits with code 2.

### Commands

| Command | Meaning |
|---|---|
| `inbox send CHANNEL MESSAGE` | Sends one message to `CHANNEL`. |
| `inbox send MESSAGE` | Sends one message to the inferred channel. |
| `inbox pipe [CHANNEL]` | Reads standard input, one message per line, into `CHANNEL` or the inferred channel. |
| `inbox list [CHANNEL \| --all-channels] [--seen] [--json] [-n N]` | Lists unseen messages, oldest first. `--seen` includes seen ones. Default: the inferred channel, else every channel. |
| `inbox count [CHANNEL \| --all-channels]` | Prints the unseen count. |
| `inbox channels [--json]` | Prints each channel with unseen messages, its count, and its newest message time. |
| `inbox seen [CHANNEL \| --all-channels]` | Marks unseen messages seen. Prints how many. |
| `inbox daemon` | Task 0065. |

`inbox send` takes one or two arguments. With two, the first is the channel.
With one, it is the message. With three or more, it exits with code 2 and
says to quote the message, so `inbox send debug build done` is never split
by a guess. `inbox send` does not read standard input; `inbox pipe` does.
`inbox pipe` with a terminal on standard input exits with code 2.

`inbox` with no subcommand prints usage and exits with code 2. `--db PATH` on
any command overrides `$INBOX_DB`.

Exit codes: 0 success, 1 a store error, 2 a usage error (including a missing
channel).

### Piped input

- Each line is one message. A trailing `\r` is dropped. A line that is empty
  or only white space is skipped.
- A line longer than 64 KiB is cut to 64 KiB, and the cut is logged once per
  run to standard error.
- Lines are inserted in a transaction that commits every 200 ms or every 500
  lines, whichever comes first, and at end of input. So `tail -f build.log |
  inbox pipe build` shows each line within 200 ms, and a burst of 10 000 lines
  takes a few transactions, not 10 000.
- SIGINT and SIGTERM commit the lines already read, then exit 0.
- The channel is resolved once, before the first line is read. A missing
  channel exits with code 2 before reading anything.

Files to change:

- `go.work`
- `inbox/` — new module, as above
- `Makefile` — `make inbox` builds `inbox/cmd/inbox`, and `make test` runs
  `go test ./inbox/...`
- `README.md` — one line in "Layout" for `inbox/`

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — `inbox/` is a module in `go.work`, and
  `cd inbox && go build ./... && go vet ./...` exits 0 on go 1.22.0 with
  `CGO_ENABLED=0`. **Proof:** the command's exit code
- [ ] **DoD-2** — Under root `R`, the directory `R/a/b/c` infers `a.b.c`;
  `R` itself and a directory outside `R` infer nothing; a symbolic link into
  `R` infers the target's channel; `R/my.repo` is an error naming
  `my.repo`. **Proof:** `cd inbox && go test ./channel -run TestInfer`
- [ ] **DoD-3** — Outside the root, `inbox send hi` and `echo hi | inbox
  pipe` exit 2, print the root, and write no row. `inbox send debug hi` and
  `echo hi | inbox pipe debug` both store `hi` in `debug`. Under the root,
  `inbox send debug hi` still uses `debug`. **Proof:** `go test ./cmd/inbox -run TestChannelArgument`
- [ ] **DoD-4** — `printf 'a\n\n  \nb\r\nc' | inbox pipe x` stores three
  messages, `a`, `b`, `c`, in order. **Proof:** `go test ./cmd/inbox -run TestPipedLines`
- [ ] **DoD-5** — `inbox send a b c` exits 2, says to quote the message, and
  writes no row. **Proof:** `go test ./cmd/inbox -run TestSendTooManyArgs`
- [ ] **DoD-6** — While a pipe stays open, a line written to it is readable
  by another connection within 500 ms. **Proof:** `go test ./cmd/inbox -run TestPipeCommitsWhileOpen`
- [ ] **DoD-7** — 10 000 piped lines store 10 000 rows in under 2 s on the
  reviewer's machine. **Proof:** `go test ./cmd/inbox -run TestPipeBurst -v` prints the time
- [ ] **DoD-8** — Eight processes that each pipe 1 000 lines at once store
  8 000 rows, with no "database is locked" error. **Proof:** `go test ./store -run TestConcurrentWriters`
- [ ] **DoD-9** — `MarkSeen(ch, id)` marks only rows of `ch` with an id at
  or below `id`; a later row stays unseen. `Unseen()` returns the right count
  and newest id per channel. **Proof:** `go test ./store -run 'TestMarkSeenUpTo|TestUnseen'`
- [ ] **DoD-10** — `list`, `count`, `channels`, and `seen` give the results
  in the table above, with and without a channel, `--all-channels`, and
  `--json`.
  **Proof:** `go test ./cmd/inbox -run TestCommands`
- [ ] **DoD-11** — A file with a newer `user_version` is refused with an
  error naming both versions, and the file is not changed. **Proof:** `go test ./store -run TestNewerSchemaRefused`
- [ ] **DoD-12** — `inbox/README.md` records the commands, the channel
  rules, the schema, `$INBOX_DB`, `$INBOX_SOURCE_ROOT`, and the exit codes.
  **Proof:** `inbox/README.md`
- [ ] **DoD-13** — The PR in the `pr` field links to this spec. **Proof:** PR body

## Risks

- Recent `modernc.org/sqlite` releases may need a newer Go than 1.22.0 →
  check the release's `go` line before `go get`, and pin an older release if
  needed.
- A directory name with a dot, such as `my.repo`, cannot map to a channel
  without making `a.b` ambiguous → it is an error that says to pass the
  channel as an argument.
  A later task can add an escape if this bites.
- The store grows forever → acceptable for text messages at human rates.
  `inbox prune` is a later task.
- A pipe that writes one huge line with no newline holds up to 64 KiB in
  memory → bounded by the cut.

## Open questions

- [ ] Is `~/source` the right default root, or should it be another path? —
  kgheacock
- [ ] Should `inbox list` print seen messages too by default? The default
  here is unseen only. — kgheacock

## Notes

Keith asked for this on 2026-10-18 in the project thread. Task 0057 (in
`tasks/complete/`) is the layer tree the daemon draws on. An earlier draft
also numbered 0057, "Claude Code inbox plugin", was closed unmerged; this
inbox does not build on it.

On 2026-10-18 Keith set the command shape: `my_cmd 2>&1 | inbox pipe debug`
and `inbox send debug my_message`, with the channel as the first argument.
