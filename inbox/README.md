# inbox

Leave short messages per channel in one SQLite file, and read them later.
Scripts, builds, and agents send; a person (or the daemon, see "Daemon")
reads. No process must run for a message to be saved.

A plugin decides what the messages mean and how they show on the pad. The
default, `basic`, counts unseen messages; `ai` shows the state of AI agents;
`github` shows your open pull requests, coloured by their checks. Pick one with `--plugin NAME` on any command (see "Plugins").

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
| `inbox send [--status S] [--here H] CHANNEL MESSAGE` | Send one message to `CHANNEL`. |
| `inbox send [--status S] [--here H] MESSAGE` | Send one message to the channel of the working directory. |
| `inbox pipe [--status S] [--here H] [CHANNEL]` | Send each line of standard input as one message. |
| `inbox list [CHANNEL \| --all-channels] [--unseen] [--json] [-n N]` | List messages, oldest first, seen and unseen (a leading `*` marks the unseen). `--unseen` lists only the unseen. `-n N` keeps the newest N. Each line is: time, status, the channel (with `--all-channels` or outside the root), then the message. Default scope: the inferred channel, else every channel. |
| `inbox count [CHANNEL \| --all-channels]` | Print the unseen count. Same default scope as `list`. |
| `inbox channels [--json]` | Print each channel with unseen messages, its count, and the time of its newest message. |
| `inbox channel` | Print the channel inferred from the working directory (see Channels). |
| `inbox seen [CHANNEL \| --all-channels]` | Mark unseen messages seen. Print how many. With neither argument it needs an inferred channel. |
| `inbox here [CHANNEL [HERE]] [--clear CHANNEL]` | Show or set where a press goes. See "Here". |
| `inbox plugins` | Print each plugin and what it shows. See "Plugins". |
| `inbox emoji [CHANNEL \| --folder DIR] [--session ID] [--format F]` | Print the emoji a key shows for a folder, a session, or both. See "Emoji". |
| `inbox help [COMMAND]` | Same as `-h`. |

`send`, `pipe`, `list`, `count`, `seen` and `emoji` also take `--channel C` in place of
the `CHANNEL` argument. Give the channel only once: both together exit with
code 2.

`-h` and `--help` work on `inbox` and on every subcommand, before or after
other arguments. `--db PATH` on any command overrides `$INBOX_DB`.
`--plugin NAME` on any command, before or after the command word, picks the
plugin; the default is `basic`.
`inbox daemon` is described under "Daemon", below.

```
inbox send debug "build done"
my_cmd 2>&1 | inbox pipe debug
tail -f build.log | inbox pipe build
```

### Status

Every message has a status. In the `basic` plugin it is letters, digits, `-`
and `_`, and the default is `info`; the `ai` plugin takes three (see
"Plugins"). `--status S` on `send` and `pipe` sets it, and `list` shows it as
a column (and as `status` in `--json`).

### Standard output and standard error

After `2>&1`, both streams share one pipe, so `inbox` cannot tell a standard
error line from a standard output line. To keep them apart, give each stream
its own `inbox pipe` and a status (bash and zsh):

```
my_cmd 2> >(inbox pipe --status error debug) | inbox pipe debug
```

The two pipes run as separate processes, so their lines are saved in the order
each process commits them (within 200 ms), not in the order `my_cmd` wrote them.
Use `2>&1` when the order matters more than the stream.

`inbox send` takes one or two arguments. With two, the first is the channel.
With three or more it exits with code 2 and says to quote the message.
`inbox send` never reads standard input; `inbox pipe` does, and exits with
code 2 when standard input is a terminal.

When standard input is a pipe or file and you name no command, `inbox` acts as
`inbox pipe`. Options may follow, but a channel needs the word `pipe`:

```
echo "hello" | inbox
make 2>&1 | inbox --status error
make 2>&1 | inbox --channel build
```

On a terminal, `inbox` with no command shows help.

### Piped input

- Each line is one message. A trailing `\r` is dropped. A line that is empty
  or only white space is skipped.
- A line longer than 64 KiB is cut to 64 KiB. The cut is logged once per run
  to standard error.
- Lines are saved in a transaction every 200 ms or every 500 lines, whichever
  comes first, and at end of input. A line from a long pipe is readable within
  200 ms.
- The input is copied to standard output as it arrives, as `tee` does, so
  `cmd | inbox` still shows the text of `cmd`. If standard output closes,
  `inbox` goes on saving.
- The first `SIGINT` (Ctrl-C) does not stop `inbox`. The terminal sends it to
  `cmd` too, and `inbox` reads on until `cmd` closes the pipe, so the last
  lines of `cmd` are saved and `cmd` does not die of a broken pipe. Then it
  exits 0. A second `SIGINT`, or `SIGTERM`, saves the lines already read and
  exits 0 at once. A last line without a newline is dropped on that second
  signal, and kept at end of input.
- The channel is resolved before the first line is read.

## Here

A message can carry a "here": where a press on its channel key in the Inbox
layer takes you (see "Daemon"). The press still marks the channel seen. A
"here" is `KIND:VALUE`, and only these kinds are accepted:

| Value | Meaning | What the daemon runs |
|---|---|---|
| `iterm2:UUID` | An iTerm session. `UUID` is the session's `unique id`. | `osascript` selects the window, tab, and session, then activates iTerm. |
| `app:BUNDLE_ID` | A macOS app, such as `com.tinyspeck.slackmacgap`. | `open -b BUNDLE_ID` |
| `url:URL` | A deep link. The scheme is `slack`, `msteams`, `ms-outlook`, or `https`. | `open URL` |

`send` and `pipe` take the first of these that gives a value:

1. `--here VALUE`
2. `$INBOX_HERE`
3. The iTerm session you are in: `$TERM_PROGRAM` is `iTerm.app`, `$TMUX` is
   unset, and `$ITERM_SESSION_ID` is `w0t0p0:UUID`. The value is `iterm2:UUID`.
4. None. The message has no "here", and a press only marks it seen.

`pipe` finds the value once, before the first line, so every line of a run
carries the same one. A bad value exits with code 2. Where detection cannot
work (tmux, ssh, cron), pass `--here` or set `INBOX_HERE`.

```
inbox here                                   # print the value that step 3 finds
inbox send --here app:com.tinyspeck.slackmacgap chat "reply to Sam"
inbox here chat app:com.tinyspeck.slackmacgap   # a fallback for a channel
inbox here chat                              # print it
inbox here --clear chat                      # remove it
```

A channel's fallback is used for a message with no "here". A "here" on the
message wins over it. A key goes to the "here" of the channel's newest unseen
message. Nothing is run as a shell command: the daemon checks the value again
when it reads it, and runs `osascript` or `open` with an argument list. macOS
only.

The first `osascript` call needs the macOS **Automation** permission for
iTerm. The permission belongs to the `node` that runs the daemon, so a new
Node path may ask again. Allow it in System Settings, Privacy & Security,
Automation. Until you do, the call waits for about 5 s, then the daemon logs
where to allow it. A press on a key whose iTerm session is closed logs
`no iTerm session UUID`; the channel is still marked seen, and iTerm is not
brought forward.

## Channels

A channel is one or more segments joined by dots. A segment holds letters,
digits, `-`, and `_`.

The channel argument always wins. Without it, the channel comes from the
working directory under the source root (`$INBOX_SOURCE_ROOT`, default
`~/source`): the path below the root, joined by dots. `~/source/macro-pad/driver`
gives `macro-pad.driver`. Symbolic links are resolved in both paths first.

- The root itself, or a directory outside it, has no inferred channel. A
  command that needs one exits with code 2.
- A dot inside a directory name is kept, so `~/source/github.com/me/app`
  gives `github.com.me.app`. (`a.b` can then mean the directory `a.b` or
  `a/b`; that is accepted.) Set `INBOX_SOURCE_ROOT=~/source/github.com/me` for
  the short name `app`.
- A directory name with any other character (`a b`, or a name that starts or
  ends with a dot) is an error that says to pass the channel as an argument.

`inbox channel` prints the inferred channel of the working directory, or exits
2 where there is none, so a script can name a channel the way inbox does.

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
  cwd        TEXT    NOT NULL,  -- the sender's working directory
  status     TEXT    NOT NULL DEFAULT 'info', -- added in schema 2
  here       TEXT,                             -- added in schema 3; NULL for none
  plugin     TEXT    NOT NULL DEFAULT 'basic' -- added in schema 4
);
CREATE INDEX messages_unseen ON messages (plugin, channel, id) WHERE seen_at IS NULL;
CREATE TABLE channel_here (  -- added in schema 3, keyed by plugin in schema 4
  plugin  TEXT NOT NULL,
  channel TEXT NOT NULL,
  here    TEXT NOT NULL,
  PRIMARY KEY (plugin, channel)
);
```

`PRAGMA user_version` is the schema version (now 4; version 2 added `status`,
version 3 added `here` and `channel_here`, version 4 added `plugin`). An older
file is migrated in one transaction; its rows belong to `basic`. A newer file
is refused and left unchanged.

One file holds every plugin. Each row belongs to one plugin, and every command
reads and writes only the rows of the plugin it runs with, so the same channel
name can live in two plugins without mixing.

For code that reads the store (`src/store.js`), `openStore(file, env,
{ plugin })` returns a store bound to one plugin (default `basic`):

- `markSeen(channel, upToId)` marks unseen rows of that channel with
  `id <= upToId` and returns how many changed. A message that lands later
  stays unseen.
- `unseen()` returns, per channel with unseen rows, `{channel, count,
  newestId, createdAt, here}`. `here` is that of the newest unseen row, else
  the channel's fallback, else `null`.
- `recent(since)` returns the same fields, plus `byStatus`, per channel with
  a row created at or after `since` (ms), seen or not. `newestId`,
  `createdAt`, and `here` are those of the newest row of any kind; `count` is
  the unseen count and can be 0. The `ai` daemon reads this.

## Environment

| Variable | Meaning | Default |
|---|---|---|
| `INBOX_DB` | The SQLite file. | `$XDG_DATA_HOME/inbox/inbox.db` |
| `INBOX_SOURCE_ROOT` | Root for channel inference. | `~/source` |
| `XDG_DATA_HOME` | Data directory base. | `~/.local/share` |
| `INBOX_BUSY_TIMEOUT_MS` | Busy timeout. Tests lower it. | `5000` |
| `INBOX_DEBUG` | `1` adds a stack to an internal error. | unset |
| `INBOX_GH` | The `gh` program the `github` plugin runs. `install.sh` sets it in the wrapper. | `gh` on the `PATH` |
| `INBOX_GITHUB_QUERY` | The GitHub search of the `github` plugin. | `is:pr is:open author:@me archived:false` |

## Exit codes

| Code | Meaning |
|---|---|
| 0 | Success. |
| 1 | A store error (cannot open, busy, newer schema), or an internal error. |
| 2 | A usage error, including a missing channel. |

Every error is a line on standard error that starts with `inbox:`. A usage
error adds `run 'inbox COMMAND -h' for help`.

## Daemon

`inbox daemon` watches the store and shows the unseen messages of every
plugin on the macro pad, each on its own key, in one process (task 0075).
`--plugin NAME`, once or more, shows only those plugins. This section
describes the `basic` plugin; "Plugins" says
what the `ai` plugin shows instead. It is a client of `macropadd`: it sends `setLayerKey` messages over the
plugin WebSocket (see `driver/README.md` "Protocol"), and reads `layerChanged`,
`childPressed`, and `event` from it. It draws one `inbox` key with the total unseen count,
and one child key per channel with unseen messages. A press on a child key
marks that channel's messages seen, then goes to its "here" (see "Here").

```
inbox daemon [--plugin NAME]... [--db PATH] [--port 8765] [--slot [NAME=]N]... [--poll 500ms]
```

| Flag | Meaning |
|---|---|
| `--plugin NAME` | Show this plugin. Give it once per plugin, or as `--plugin basic,ai`. Default: every plugin `inbox plugins` lists. |
| `--port N` | `macropadd`'s port on 127.0.0.1. Default 8765. |
| `--assets-dir DIR` | Unused. The daemon sends icon text, not picture files, so it needs no assets directory. The flag is still accepted, with one line on standard error, so a service that passes it starts. |
| `--slot NAME=N` | Put the key of plugin `NAME` in slot N, 0 to 5, once per plugin. With one plugin, `--slot N` is enough; with more, a bare `--slot N` exits 2, and so do two plugins on one slot. Default: the first free slot. |
| `--poll DURATION` | How often to look for a change, such as `500ms` or `2s`. Default `500ms`. |
| `--db PATH` | The store, as for every command. |

The keys:

- The `inbox` key shows the total unseen count in every channel, on the Gmail
  envelope. It is yellow (`WARN`) when the count is above 0, and black when it
  is 0. It stays on the pad at 0.
- A press on `inbox` opens a layer with one key per channel that has unseen
  messages, each with its own count. The key shows the channel's last name
  segment in black letters on a plain white tile, and the segment before it in
  gray letters above (`macro-pad.driver` shows `macro-pad` over `driver`).
  There are at most 20, the channels with the newest unseen messages; past
  five, `macropadd` shows them on pages with arrows (task 0078). A channel
  past the 20th counts in the `inbox` total, and `inbox channels` lists it.
- A channel with no unseen messages leaves the layer, but not while the layer
  is open, so keys do not move under a finger. It stays with count 0 until you
  leave the layer. A new channel takes a free position if one is left, and
  otherwise waits. When the layer closes, the daemon removes channels at zero,
  adds the waiting ones, and orders the keys by newest unseen message.
- A press on a channel key marks that channel's unseen messages seen. It
  marks the messages the key showed, so one that lands during the press stays
  unseen. The count on the key, and the total on `inbox`, drop within 1 s. The
  key stays at 0 until you leave the layer. The daemon finds the channel from
  the `childPressed` message `macropadd` sends, so a channel on page 2 works
  the same. The press that opens the layer, the up key, and the arrows mark
  nothing. A press means "I have seen these":
  `inbox list CHANNEL` still shows the messages, and a failed mark is logged
  and tried again on the next press. A double or long press on a channel key
  marks it too, on its first press-down.
- After the mark, and after the new counts go out, a press on a channel key
  brings the "here" of its newest unseen message to the front. A channel with
  no "here" only marks. A "here" that fails (a closed iTerm session, a wrong
  bundle ID, a missing Automation permission) is logged with the channel and
  the kind, and the mark stands.
- `macropadd` draws the icon. The daemon sends each channel as the last segment
  of its folder in `iconText`, over the folder's emoji in `iconEmoji`, and for
  a Claude Code session of the ai plugin the session's emoji beside it (see
  "Emoji"). It writes no file. A name of more than 16 characters is cut with
  `…`. Older versions wrote pictures to `<assets-dir>/.generated/inbox/`;
  those files are no longer used, and you can delete that directory.
- The daemon sends a key only when its values changed. On every connect it
  removes every channel it knows from the `inbox` layer and sends the full
  state, so a child that an earlier daemon left on the pad does not stay.

How it behaves:

- It looks for a change every `--poll` with `PRAGMA data_version`, and reads
  the table only when that value changes (and once every 30 s as a guard). A
  new message reaches the pad within about one poll.
- It starts with or without `macropadd`. It dials with backoff from 250 ms to
  5 s, and logs once when the link goes down and once when it comes back. On
  every connect it waits for the first `layerChanged` (at most 1 s), then sends
  the full state, then only changes.
- While `macropadd` is down it keeps reading the store, and sends only the
  newest state on reconnect.
- Each plugin runs its own loop in the process: its own store handle, its
  own WebSocket to `macropadd`, and its own poll. Each draws its own key and
  reads a press only in the layer of its own key, and a failed read or a lost
  link in one loop does not stop the other.
- One daemon runs per store and plugin. The process makes
  `<db>.<plugin>.daemon.pid` for each plugin it shows, all before it draws a
  key. If another daemon shows one of them, it exits with code 3 and `inbox: a
  basic daemon already runs for <db> (pid N)`, and draws nothing. A pid file
  whose process is gone is replaced.
- `SIGINT` and `SIGTERM` close every WebSocket and exit 0. The keys stay on the
  pad: `macropadd` keeps them in `layers.json`, and the next daemon start
  corrects them.
- Log lines go to standard error, each starting with `inbox:`. It logs one
  `NAME daemon started` line per plugin. With more than one plugin, the lines of
  one plugin's loop start with `inbox: [NAME]`.

Start `macropadd` first, for example from the repo root. Use a `macropadd`
and firmware from this checkout: an older `macropadd` ignores `iconEmoji` (and
`iconText` before task 0069), and the key then shows less.

```
go run ./driver/cmd/macropadd --vendor-id=0x2E8A --product-id=0x10A3 --assets-dir "$PWD/assets"
inbox daemon
```

### Install at login

```
inbox/scripts/install.sh [--plugin NAME]... [--port N] [--slot [NAME=]N]... [--no-completions] [--dry-run]
inbox/scripts/uninstall.sh [--purge] [--dry-run]
```

`install.sh` installs one service that runs `inbox daemon`, so one process
shows every plugin. `--plugin NAME`, once or more, limits it to those plugins;
`install.sh` checks each name with `inbox plugins`. Without `--plugin`, a
plugin added later shows once the service restarts. `--slot` takes the same
values as the daemon's.

`install.sh` needs Node 22.13 or newer. It records the absolute path of the
`node` it finds, because launchd and systemd do not see your shell's `PATH`
(nvm, Homebrew). It then:

1. writes `$PREFIX/bin/inbox` (`PREFIX` defaults to `~/.local`), a wrapper that
   runs `inbox/bin/inbox.js` of this checkout with that `node`;
2. links the zsh completion, if oh-my-zsh is installed (see "Completions");
3. stops and removes the per-plugin services an older `install.sh` made
   (`com.kgheacock.macropad.inbox.NAME.plist`, `inbox-daemon-NAME.service`),
   and names each one; their logs stay;
4. writes the service and starts it;
5. prints where the command, the service file, the log, and the store are,
   the plugins it shows, whether the daemon runs, and where the completion
   link is.

| | macOS | Linux |
|---|---|---|
| Service file | `~/Library/LaunchAgents/com.kgheacock.macropad.inbox.plist` | `~/.config/systemd/user/inbox-daemon.service` |
| Log | `~/Library/Logs/inbox-daemon.log` | `journalctl --user -u inbox-daemon` |
| Restart | `KeepAlive`, 5 s throttle | `Restart=always`, `RestartSec=5` |

Run `install.sh` again to upgrade: it rewrites the files and restarts the
daemon. Keep the checkout where it is; run the script again after you move it
or change the Node version. `install.sh` does not start `macropadd`.

`uninstall.sh` stops the daemon and removes its service, any per-plugin
service an older `install.sh` left, the wrapper, and the completion link. It
keeps the store. `--purge` also removes the store, its `-wal` and `-shm` files,
and the pid files. `--plugin NAME` is no longer needed; it is accepted with one
line on standard error and ignored. With nothing installed it says so and
exits 0. Both
scripts take `--dry-run` (print each step, change nothing) and `-h`.

### Completions

With oh-my-zsh, `install.sh` links `inbox/completions/_inbox` into
`$ZSH_CACHE_DIR/completions`, a directory oh-my-zsh puts on `fpath` before it
runs `compinit`. It finds the directory by asking `zsh -ic`, so a
`ZSH_CACHE_DIR` set in `.zshrc` counts, and otherwise uses
`~/.oh-my-zsh/cache/completions`. Without oh-my-zsh it installs nothing and
says so. `--no-completions` skips the step.

Open a new shell (`exec zsh`) to use it. If the completion does not appear, delete
`~/.zcompdump*` and open a new shell. `Tab` completes the commands, each
command's options, and channel names. The channel names come from
`inbox channels`, so only channels with unseen messages are offered. It is a
link into this checkout, so an edit to `_inbox` applies at once, and moving
the checkout needs `install.sh` again.

Exit codes of `inbox daemon`: 0 after a signal, 1 store error, 2 usage error,
3 a daemon already runs.

## Plugins

A plugin holds only what one kind of inbox does differently: the statuses
`--status` accepts and its default, the key on the pad (id and icon), what
the key and the channel keys show, the order of the channel keys, and a
paragraph of help. The store, the commands, "here", the daemon, and the
install are shared. A plugin may also have a feed, which the daemon runs to
write the plugin's own messages (`github` asks GitHub). The plugins live in
`src/plugins/`; to add one, write `src/plugins/NAME.js` and list it in
`src/plugins/index.js`.

```
inbox plugins
inbox --plugin ai send --status waiting macro-pad.driver "needs a decision"
inbox --plugin ai send --status running macro-pad.driver "running the tests"
inbox send --plugin ai macro-pad.driver "tests passed"        # status done
inbox --plugin ai seen macro-pad.driver
```

| | `basic` (default) | `ai` | `github` |
|---|---|---|---|
| `--status` | any word; default `info` | `waiting`, `running`, or `done`; default `done`. Any other value exits with code 2 and saves nothing. | `failing`, `pending`, `passing`, `none`, or `closed`; default `none` |
| Key on the pad | `inbox`, a Gmail envelope (`assets/gmail.png`) | `ai-inbox`, `assets/ai.png` | `github-inbox`, `assets/github.png` |
| Colors | yellow when a message is unseen | blue, green, or yellow, by status | red, blue, or green, by the newest message |
| Pip | the unseen messages | the channels that wait | the failing PRs |
| Channels shown | those with an unseen message | those with a message in the last hour, seen or not | the open PRs |
| Channel order | newest unseen message first | newest message first | failing, pending, passing, none |

### The ai plugin

The inbox for AI agents. Agents send; a person looks at the pad.

| Status | Color | Pad status | Meaning |
|---|---|---|---|
| `waiting` | yellow | `WARN` | The agent waits for a person. |
| `running` | blue | `RUNNING` | The agent works. |
| `done` | green | `DONE` | The agent finished. |

A seen message does not count for the colors and the pip. The daemon reads the
store on every change, so they are calculated again each time a message is
seen. Which channels show does not depend on what is seen. A message with a stored status outside the three counts as `done`.

- **A channel** shows the highest status of its unseen messages, in the order
  `waiting`, then `running`, then `done`. A channel with no unseen message has
  no color, but it stays on the pad while its newest message is less than an
  hour old, and leaves within about 30 s after that hour ends.
- **The `ai-inbox` key** shows the highest status of the channels on the pad.
  A channel with no message in the last hour does not count. The key has no
  color when every message is seen, and it stays on the pad.
- **The pip** on the `ai-inbox` key is the number of channels that have an
  unseen `waiting` message. The pip on a channel key is the number of its
  unseen `waiting` messages. A count of 0 draws no pip.
- **A press** on the `ai-inbox` key opens the list of channels with a message
  in the last hour, also when there is no pip. At most 20 show, on pages past
  five, the channel with the newest message first, whatever its status. A
  channel past the 20th counts in the key's status and pip.
- **A press on a channel key** marks the messages it showed seen, then goes
  to the "here" of its newest message (seen or not), as for `basic`. The keys do not move while the layer is
  open; a seen channel keeps its place, with no color, until you leave.

An old `waiting` message keeps the key yellow until it is seen, also after the
agent went on. Press the channel, or run `inbox --plugin ai seen CHANNEL`.

`inbox daemon` and `install.sh` show it beside `basic`, in one process. Give
the two keys fixed places with `--slot basic=N --slot ai=M`; otherwise each
takes the first free slot. To show only the ai inbox:

```
inbox daemon --plugin ai [--db PATH] [--port 8765] [--slot N] [--poll 500ms]
```

### Emoji

Each channel key shows its folder's name over one or two emoji from
`assets/emoji/` (task 0073): the folder's on the left and, for a Claude Code
session, the session's on the right. The same folder or session always gets
the same emoji, on every machine.

- The folder of a channel is the channel itself. For the ai plugin, a channel
  `PROJECT.SID` from the Claude Code hooks, whose last segment is 8 hex digits,
  is the folder `PROJECT` and the session `SID`. So a folder shows the same
  emoji in the basic inbox and in the ai inbox.
- The emoji of a folder is the emoji of `channel:` and its channel; a session
  hashes `session:` and the first 8 hex digits of its id, in lower case.
- Every emoji of `assets/emoji/set.txt` gets a score, the first 8 bytes of
  SHA-256 of the input, a line break, and the emoji's code points; the highest
  wins (rendezvous hashing). Adding an emoji to the set moves only the inputs
  that take it, about 1 in the size of the set.
- Two keys on the pad share an emoji about 1 time in 13; their names tell them apart.
- `inbox emoji` prints the same emoji, so a status line can show them:

```
inbox emoji                                     # the current folder
inbox emoji --folder "$PWD" --session "$CLAUDE_CODE_SESSION_ID"
inbox emoji macro-pad.driver --format name      # char, name, ref, or json
```

### Claude Code

`inbox/claude-code/` holds the hooks that report each Claude Code session to
the ai plugin: running while it works, waiting when it needs you, done when the
turn ends. See `inbox/claude-code/README.md` for the events, the install
steps, and the limits.

### The github plugin

One key per open pull request, coloured by the checks of its last commit
(task 0076). Nobody sends to it: the daemon asks GitHub once a minute, through
`gh api graphql`, for the PRs of `$INBOX_GITHUB_QUERY` (default
`is:pr is:open author:@me archived:false`, at most 50), and writes a message
only when a PR's state changes. `gh` holds the login; run `gh auth login` once.

| Status | Check roll-up | Color | Pad status |
|---|---|---|---|
| `failing` | `FAILURE`, `ERROR` | red | `ERROR` |
| `pending` | `PENDING`, `EXPECTED` | blue | `RUNNING` |
| `passing` | `SUCCESS` | green | `DONE` |
| `none` | no checks | none | none |
| `closed` | the PR is no longer open | the key goes | |

- **A channel** is `OWNER.REPO.NUMBER` (a dot in a name becomes `-`), such as
  `kgheacock.macro-pad.97`. Its key shows `REPO #NUMBER` over the emoji of
  `OWNER.REPO`, so the PRs of one repository share an emoji. The body is the
  PR title, and the "here" is the PR's URL.
- **The colour** is the status of the newest message, seen or not. The pip on
  a PR's key counts its unseen changes; a press marks them seen and opens the
  PR in the browser.
- **The `github-inbox` key** shows the worst status of the open PRs, and its
  pip counts the failing ones.
- **A PR that is no longer open** gets a seen `closed` message and leaves the
  pad. When the search has more than 50 PRs, nothing is closed.
- **A failure** (no `gh`, not logged in, no network) changes nothing and logs
  one line, `inbox: [github] cannot read GitHub: WHY`, until a read succeeds.
  `launchd` and `systemd` do not see the shell's `PATH`, so `install.sh` writes
  the `gh` it finds into the wrapper as `INBOX_GH`. To leave the key out, run
  `install.sh --plugin basic --plugin ai`.

```
inbox --plugin github list --all-channels
INBOX_GITHUB_QUERY='is:pr is:open review-requested:@me' inbox daemon --plugin github
```
