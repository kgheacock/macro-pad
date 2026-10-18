# inbox

Leave short messages per channel in one SQLite file, and read them later.
Scripts, builds, and agents send; a person (or the daemon in task 0065)
reads. No process must run for a message to be saved.

It is a Node package with no npm dependencies. It uses Node's built-in
`node:sqlite`, so it needs **Node 22.13 or newer**. No `npm install` is
needed.

```
cd inbox && npm test        # or: make test
node bin/inbox.js -h        # or link it: npm link   (gives the `inbox` command)
```

## Commands

| Command | Meaning |
|---|---|
| `inbox send CHANNEL MESSAGE` | Send one message to `CHANNEL`. |
| `inbox send MESSAGE` | Send one message to the channel of the working directory. |
| `inbox pipe [CHANNEL]` | Send each line of standard input as one message. |
| `inbox list [CHANNEL \| --all-channels] [--seen] [--json] [-n N]` | List unseen messages, oldest first. `--seen` adds seen ones (a leading `*` marks the unseen). `-n N` keeps the newest N. Default scope: the inferred channel, else every channel. |
| `inbox count [CHANNEL \| --all-channels]` | Print the unseen count. Same default scope as `list`. |
| `inbox channels [--json]` | Print each channel with unseen messages, its count, and the time of its newest message. |
| `inbox seen [CHANNEL \| --all-channels]` | Mark unseen messages seen. Print how many. With neither argument it needs an inferred channel. |
| `inbox help [COMMAND]` | Same as `-h`. |

`-h` and `--help` work on `inbox` and on every subcommand, before or after
other arguments. `--db PATH` on any command overrides `$INBOX_DB`.
`inbox daemon` is reserved for task 0065.

```
inbox send debug "build done"
my_cmd 2>&1 | inbox pipe debug
tail -f build.log | inbox pipe build
```

`inbox send` takes one or two arguments. With two, the first is the channel.
With three or more it exits with code 2 and says to quote the message.
`inbox send` never reads standard input; `inbox pipe` does, and exits with
code 2 when standard input is a terminal.

### Piped input

- Each line is one message. A trailing `\r` is dropped. A line that is empty
  or only white space is skipped.
- A line longer than 64 KiB is cut to 64 KiB. The cut is logged once per run
  to standard error.
- Lines are saved in a transaction every 200 ms or every 500 lines, whichever
  comes first, and at end of input. A line from a long pipe is readable within
  200 ms.
- `SIGINT` and `SIGTERM` save the lines already read, then exit 0. A last line
  without a newline is dropped on a signal, and kept at end of input.
- The channel is resolved before the first line is read.

## Channels

A channel is one or more segments joined by dots. A segment holds letters,
digits, `-`, and `_`.

The channel argument always wins. Without it, the channel comes from the
working directory under the source root (`$INBOX_SOURCE_ROOT`, default
`~/source`): the path below the root, joined by dots. `~/source/macro-pad/driver`
gives `macro-pad.driver`. Symbolic links are resolved in both paths first.

- The root itself, or a directory outside it, has no inferred channel. A
  command that needs one exits with code 2.
- A directory name that is not a valid segment (`my.repo`, `a b`) is an
  error that says to pass the channel as an argument.

## Store

The file is `$INBOX_DB`, or `inbox.db` in `$XDG_DATA_HOME/inbox/`
(`~/.local/share/inbox/` by default). The directory is created with mode
0700. Every connection uses WAL mode and a 5 s busy timeout, so several
writers and one reader can use the file at once.

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

`PRAGMA user_version` is the schema version (now 1). An older file is
migrated in one transaction. A newer file is refused and left unchanged.

For code that reads the store (`src/store.js`):

- `markSeen(channel, upToId)` marks unseen rows of that channel with
  `id <= upToId` and returns how many changed. A message that lands later
  stays unseen.
- `unseen()` returns, per channel with unseen rows, `{channel, count,
  newestId, createdAt}`.

## Environment

| Variable | Meaning | Default |
|---|---|---|
| `INBOX_DB` | The SQLite file. | `$XDG_DATA_HOME/inbox/inbox.db` |
| `INBOX_SOURCE_ROOT` | Root for channel inference. | `~/source` |
| `XDG_DATA_HOME` | Data directory base. | `~/.local/share` |
| `INBOX_BUSY_TIMEOUT_MS` | Busy timeout. Tests lower it. | `5000` |
| `INBOX_DEBUG` | `1` adds a stack to an internal error. | unset |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | A store error (cannot open, busy, newer schema), or an internal error. |
| 2 | A usage error, including a missing channel. |

Every error is a line on standard error that starts with `inbox:`. A usage
error adds `run 'inbox COMMAND -h' for help`.
