# inbox

Leave short messages per channel in one SQLite file, and read them later.
Scripts, builds, and agents send; a person (or the daemon, see "Daemon")
reads. No process must run for a message to be saved.

A plugin decides what the messages mean and how they show on the pad. The
default, `basic`, counts unseen messages; `repos` shows one key per
repository, coloured by your Claude sessions; `apps`
opens the apps you work on today, each with five actions. Pick one with `--plugin NAME` on any command (see "Plugins").

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
| `inbox --plugin apps pin [NAME]`, `unpin [NAME]` | Always show an app's key, or stop. See "The apps plugin". |
| `inbox --plugin apps run NAME [-- CMD...]` | Run an app here and report its state to its Run key. See "The apps plugin". |
| `inbox activity note --repo NAME --kind KIND [--ref REF]` | Note one event of yours in a repository, for the order of the repository keys. See "Activity". |
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
and `_`, and the default is `info`; the `repos` plugin takes three (see
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
| `url:URL` | A deep link. The scheme is `slack`, `msteams`, `ms-outlook`, or `https`. | `open URL`. For `https`, first a tab that already shows it (see below). |

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

### A tab that already shows the URL

A press on an `https:` "here", such as a link to a PR, first
looks for a browser tab that already shows that URL (task 0079). A tab counts
when its URL is the URL, or the URL followed by `/`, `?`, or `#`, so a PR on
its `/files` page counts. The first one found is selected, its window raised,
and its browser activated; only when there is none does `open URL` run.

It looks in Google Chrome, Brave, Microsoft Edge, Chromium, and Safari, in
that order, and only in the ones that run: none is started to look. Firefox
and Arc cannot be searched, so a press there opens a new tab. The search is
one `osascript -l JavaScript` call with a 3 s limit. Each browser asks once
for the **Automation** permission; until you allow it, or when the search
fails or runs out of time, a new tab opens as before.

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

`PRAGMA user_version` is the schema version (now 10; version 2 added `status`,
version 3 added `here` and `channel_here`, version 4 added `plugin`, version 5
added `messages.session` and `branch_sessions`, version 6 added `repos`, see
"Repositories", version 7 added `activity`, see "Activity", version 8 moved
the `ai` rows to `repos`, version 9 added `yellow_answers`, see "Yellow
criteria", version 10 added `repo_pulls`, see "Repo layer"). An older
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
  the unseen count and can be 0. The `repos` daemon reads this.

## Environment

| Variable | Meaning | Default |
|---|---|---|
| `INBOX_DB` | The SQLite file. | `$XDG_DATA_HOME/inbox/inbox.db` |
| `INBOX_SOURCE_ROOT` | Root for channel inference. | `~/source` |
| `XDG_DATA_HOME` | Data directory base. | `~/.local/share` |
| `INBOX_BUSY_TIMEOUT_MS` | Busy timeout. Tests lower it. | `5000` |
| `INBOX_DEBUG` | `1` adds a stack to an internal error. | unset |
| `INBOX_GH` | The `gh` program the repos feed runs. `install.sh` sets it in the wrapper. | `gh` on the `PATH` |
| `INBOX_REPOS_OWNER` | The GitHub user or organisation whose repositories the repos feed lists (see "Repositories"). `install.sh --repos-owner OWNER` sets it in the wrapper. | `kgheacock` |
| `INBOX_REPOS_YELLOW_CMD` | The program that gives the yellow items of a repository (see "Yellow criteria"). `install.sh --yellow-cmd PATH` sets it in the wrapper. Also `INBOX_REPOS_YELLOW_EVERY` (seconds, default 300) and `INBOX_REPOS_YELLOW_TIMEOUT` (seconds, default 20). | unset |
| `INBOX_REPOS_ARCHIVED` | `1` keeps archived repositories in the repos feed. | unset (archived ones stay out) |
| `INBOX_REPOS_COUNTS_EVERY` | Seconds between two counts queries for all shown repositories (see "Repositories"); at least 30. | `300` |
| `INBOX_APPS_ROOT` | The folder of the `apps` plugin's repositories. `install.sh --apps-root DIR` sets it in the wrapper. | unset (no app keys) |
| `INBOX_APPS_COUNT` | How many apps the `apps` key shows, 1 to 20. `install.sh --apps-count N`. | `5` |
| `INBOX_APPS_RUN` | Run commands that win over detection, `NAME=CMD;NAME=CMD`. `install.sh --apps-run NAME=CMD`. | unset |
| `INBOX_GIT` | The `git` the `apps` plugin's Pull key runs. `install.sh` sets it in the wrapper. | `git` on the `PATH` |

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
what the `repos` plugin shows instead. It is a client of `macropadd`: it sends `setLayerKey` messages over the
plugin WebSocket (see `driver/README.md` "Protocol"), and reads `layerChanged`,
`childPressed`, and `event` from it. It draws one `inbox` key with the total unseen count,
and one child key per channel with unseen messages. A press on a child key
marks that channel's messages seen, then goes to its "here" (see "Here").

```
inbox daemon [--plugin NAME]... [--db PATH] [--port 8765] [--slot [NAME=]N]... [--poll 500ms]
```

| Flag | Meaning |
|---|---|
| `--plugin NAME` | Show this plugin. Give it once per plugin, or as `--plugin basic,repos`. Default: every plugin `inbox plugins` lists. |
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
  adds the waiting ones, and orders the keys by newest unseen message. A
  layer under the key's own, from a child with children of its own (task
  0086), counts as open too: the daemon reads the open layer's `path` from
  `layerChanged`.
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
  a Claude Code session of the repos plugin the session's emoji beside it (see
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
inbox/scripts/install.sh [--plugin NAME]... [--port N] [--slot [NAME=]N]... [--repos-owner OWNER]
                         [--yellow-cmd PATH] [--apps-root DIR] [--apps-count N] [--apps-run NAME=CMD]...
                         [--no-completions] [--dry-run]
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
write the plugin's own messages (`repos` asks GitHub). The plugins live in
`src/plugins/`; to add one, write `src/plugins/NAME.js` and list it in
`src/plugins/index.js`. The `ping` plugin (task 0098) has its own section at the end.

```
inbox plugins
inbox --plugin repos send --status waiting macro-pad.driver "needs a decision"
inbox --plugin repos send --status running macro-pad.driver "running the tests"
inbox send --plugin repos macro-pad.driver "tests passed"     # status done
inbox --plugin repos seen macro-pad.driver
```

| | `basic` (default) | `repos` | `apps` |
|---|---|---|---|
| `--status` | any word; default `info` | `waiting`, `running`, or `done`; default `done`. Any other value exits with code 2 and saves nothing. | `pinned`, `running`, `stopped`, and others (see "The apps plugin"); default `stopped` |
| Key on the pad | `inbox`, a Gmail envelope (`assets/gmail.png`) | `repos`, `assets/github.png` | `apps`, `assets/vscode.png` |
| Colors | yellow when a message is unseen | blue, green, or yellow, by status | by the state of a run or a pull |
| Pip | the unseen messages | the sessions that wait | none |
| Keys shown | the channels with an unseen message | one per repository of the feed | the apps of today |
| Key order | newest unseen message first | the activity ledger (see "Activity") | pins, then newest activity |

The names `ai` and `github` are gone (task 0087). `--plugin ai` still works as
an alias of `repos`, so an old hook keeps reporting; the `github` plugin and
its PR keys have no replacement.

### The repos plugin

One top key, `repos`, with one child per repository of the repos feed (see
"Repositories"), in the order of the activity ledger (see "Activity"). A
child has the id `repo.KEY` (`KEY` is the repository key: the name in lower
case, a dot becoming `-`, so `keithheacock.com` is `repo.keithheacock-com`),
the repository's name as its caption, and the emoji of `repo:KEY`. A press
on a key that has items opens its layer (see "Repo layer"). Every repository of the feed shows, also one with no activity, up to
macropadd's 128 children; a longer feed is cut, and the daemon logs one line.

Claude Code sessions report to this plugin through the hooks (see "Claude
Code"). A session is a channel `PROJECT.SID`, and its repository is the name
of its folder (see "Emoji"). A session of a repository that the feed lacks
draws no key, but it counts for the top key.

| Status | Color | Pad status | Meaning |
|---|---|---|---|
| `waiting` | yellow | `WARN` | The agent waits for a person. |
| `running` | blue | `RUNNING` | The agent works. |
| `done` | green | `DONE` | The agent finished. |

- **A repository key** shows the highest status of the unseen messages of its
  sessions: waiting, then running, then done. A repository with no unseen
  message has no color. A stored status outside the three counts as `done`.
- **The `repos` key** shows the highest status of all sessions, and the totals
  of the counts of its repository keys (see "Repo keys"). A session stays in
  the rows for an hour after its last message. The key has no color when every
  message is seen.
- **While the repos layer is open** no repository key moves or goes; a new
  repository goes last. When the layer is closed the keys follow the order.
- **On every connect** the daemon removes the keys `ai-inbox`,
  `github-inbox`, and `repos`, so a key of the retired plugins does not stay
  in the pad's `layers.json`, and draws `repos` again.
- **No key** is drawn before the first answer of the feed. A start without a
  network draws the stored repositories.
- **An old `waiting`** keeps its repository yellow until it is seen. The hook
  sees it on the next state change, or run `inbox --plugin repos seen CHANNEL`.

Schema 8 (task 0087) renames the `ai` rows to `repos` and deletes the
`github` rows. A checkout older than this change refuses a file of schema 8.
An installed service from before this task, with `--plugin ai` or `--slot
ai=N` in its arguments, still starts: run `install.sh` again to rewrite it.

`inbox daemon` and `install.sh` show it beside `basic` and `apps`, in one
process. Give the keys fixed places with `--slot basic=N --slot repos=M`;
otherwise each takes the first free slot. To show only the repos plugin:

```
inbox daemon --plugin repos [--db PATH] [--port 8765] [--slot N] [--poll 500ms]
```

### Emoji

Each channel key shows its folder's name over one or two emoji from
`assets/emoji/` (task 0073): the repository's on the left and, for a Claude
Code session, the session's on the right. The same repository or session
always gets the same emoji, on every machine.

- The folder of a channel is the channel itself. For the repos plugin, a channel
  `PROJECT.SID` from the Claude Code hooks, whose last segment is 8 hex digits,
  is the folder `PROJECT` and the session `SID`.
- Every key of one repository shows the same first emoji, on every plugin
  (task 0081). It hashes `repo:` and the repository's name in lower case. The
  name is the fourth segment of `HOST.TLD.OWNER.REPO...` (from
  `~/source/github.com/OWNER/REPO`), else the first segment (from
  `~/source/REPO/...`). So `macro-pad`, `macro-pad.driver`, and
  `github.com.kgheacock.macro-pad.3f2a91c0` all match, and so does the key
  `repo.macro-pad`.
- A session hashes `session:` and the first 8 hex digits of its id, in lower
  case. The hooks send the session's git branch (`send --branch`); the store
  keeps it for the keys of task 0096.
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
the repos plugin: running while it works, waiting when it needs you, done when the
turn ends. See `inbox/claude-code/README.md` for the events, the install
steps, and the limits.

### Repositories

The repos feed (tasks 0089 and 0097) lists the repositories of one owner that
you work on, with the open issue count (pull requests not counted) and the open
PR count of each. It is the data for the `repos` plugin (task 0087). The owner
is `$INBOX_REPOS_OWNER` (default `kgheacock`; `install.sh --repos-owner OWNER`
writes it into the wrapper). A name that GitHub does not allow, such as `-x`,
is refused before `gh` runs. No step lists every repository of the owner, so an
organisation with thousands of repositories costs the same as a user with ten.

Every minute the feed builds the **candidates**, the union of three sources,
kept to the repositories of the owner:

1. **Your contributions.** One GraphQL query (`gh api graphql`, cost 1), paged
   by 100, at most 5 pages: `viewer.repositoriesContributedTo` with commits,
   PRs, PR reviews and issues, your own repositories included, newest push
   first. GitHub counts about the last year.
2. **Your clones.** Each folder under `$INBOX_APPS_ROOT` whose `.git/logs/HEAD`
   changed in the last 30 days (the same scan that feeds the `git` kind of the
   activity ledger).
3. **The activity ledger.** Each repository with a row in the ledger (see
   "Activity"): GitHub events, Claude sessions, `Code` presses.

It ranks the candidates by the ledger's score (a repository with no score by
its newest push) and keeps the first 128 (`REPO_MAX_CHILDREN`). Only these get
**counts**: one aliased GraphQL query per 50 repositories, each a
`repository(owner, name) { url isFork isArchived pushedAt issues(states: OPEN)
{ totalCount } pullRequests(states: OPEN) { totalCount } }`. The counts of all
shown repositories are asked every `$INBOX_REPOS_COUNTS_EVERY` seconds (default
300, at least 30); in between, only a new candidate with no stored row is
asked. A repository GitHub does not have is dropped. Archived repositories
stay out unless `INBOX_REPOS_ARCHIVED=1`; forks stay in. A repository that you
never touched does not show.

The store keeps the answer in the `repos` table (schema 6), one row per
`(owner, name)`: `key` (the name in lower case, a dot becoming `-`), `url`,
`is_fork`, `is_archived`, `pushed_at`, `issues`, `prs`, `synced_at`. Every
plugin reads the same table, through `listRepos(owner)` and
`replaceRepos(owner, repos, { complete })` in `src/store.js`. The table holds
the shown set: a repository that leaves it loses its row.

- **A failure** (no `gh`, not logged in, no network) keeps the stored rows and
  logs one line, `inbox: cannot read GitHub repositories: WHY`, until a read
  succeeds.
- **A failed counts query** keeps the stored counts and logs one line,
  `inbox: cannot read the counts of GitHub repositories: WHY`, until a counts
  query succeeds. A new candidate shows with no counts until then.
- **A start without a network** draws the stored rows: the daemon calls the
  feed's `cold` hook once when the first read fails.

### Activity

The activity ledger (task 0090) records your own events per repository, and a
score of them sorts the repository keys, newest work first. The `repos`
plugin (task 0087) draws the keys in this order. The
store keeps one row per event in `activity(repo, kind, ref, at)` (schema 7),
with `(repo, kind, ref)` as the key, so a second read of one event changes
nothing but may move `at` forward. `repo` is the repository key (the name in
lower case, a dot becoming `-`). Every plugin shares the table.

| Kind | Weight | Where it comes from | `ref` |
|---|---|---|---|
| `pr_review` | 3 | a `PullRequestReviewEvent` of your GitHub events | the event id |
| `pr_open` | 3 | a `PullRequestEvent` that opened a PR | the event id |
| `push` | 3 | a `PushEvent` | the event id |
| `claude` | 2 | the Claude Code hook, on each prompt | the 8 first characters of the session id (one row per session) |
| `code` | 2 | a press on `Code` under an app of the apps plugin | the date |
| `git` | 1 | the time of `.git/logs/HEAD` of each folder under `INBOX_APPS_ROOT` | the date |

A row scores `weight * 0.5 ** (age / 3 days)`: its weight halves every 3 days.
The score of a repository is the sum of its rows. The keys sort by score,
then by the time of the newest event, then by name; a repository with no event
shows after the active ones, by name. A row older than 30 days is deleted each
time the repos feed runs. The weights, the half-life, and the retention are
constants in `src/activity.js` (`WEIGHTS`, `HALF_LIFE_MS`, `KEEP_MS`); the
ledger keeps the kind, so a change needs no migration.

- **Your events on GitHub.** The repos feed runs `gh api user --jq .login`
  once (again only after an events read fails), then `gh api "users/LOGIN/events?per_page=100"`: GitHub shows the private
  events of a user only to that user, so it asks for the signed-in login, not
  for `INBOX_REPOS_OWNER`. The first run, with no such event in the ledger,
  reads 3 pages; later runs read one. Events of repositories outside
  `INBOX_REPOS_OWNER` are left out, and an owner that is an organisation has no
  such events. The API shows an event up to a few minutes late, and keeps 300
  events or 90 days. A failure is logged once as `inbox: cannot read GitHub
  events: WHY`; the list and the ledger stay as they were.
- **Local git use.** The feed reads the time of `.git/logs/HEAD` of each repository
  folder under `INBOX_APPS_ROOT` (a commit, a checkout, or a pull writes there).
  It does not read `.git/index`, which `git status` rewrites. `git push`
  writes no line to that file, so a push reaches the ledger through GitHub.
- **Claude and Code.** The Claude Code hook (see `claude-code/README.md`) and
  the `Code` key call `inbox activity note --repo NAME --kind KIND --ref REF`.
  An unknown kind exits 2.

`orderRepos(repos, scores)` and `nextShown({ shown, wanted, open, max })` in
`src/activity.js` give the order. While the repos layer, or a layer under it,
is open, `nextShown` keeps the keys where they are and puts a new name at the
end, so no key moves under your finger; when it is closed, the keys follow the
order. The order moves within one feed run (60 s) of a write.

### Yellow criteria

The yellow items of a repository (task 0093) come from a program that you
write, because only you know your criteria: for example the open JIRA tickets of
the repository. The count on the repository key (task 0092) and the items under it
(task 0095) read what this feed stores. Green stays "open PRs".

**The contract.** Point `INBOX_REPOS_YELLOW_CMD` at an executable file (`install.sh
--yellow-cmd PATH` writes the path into the wrapper). The repos feed runs it
once per repository, without a shell, with the repository name (as GitHub
spells it) as `argv[1]` and as `$INBOX_REPO`. It must print a JSON array on
standard output and exit 0:

```
[{ "id": "PROJ-12", "title": "Fix the login", "url": "https://example.test/browse/PROJ-12" }]
```

The yellow count of the repository is the length of the list. The feed drops an
item whose `id` is not `[A-Za-z0-9][A-Za-z0-9._-]{0,39}` or whose `url` is not
`https://`, drops a repeated `id`, keeps the first 200 items, and cuts a title
to 200 characters. Output that is not a JSON array, an exit other than 0, or
more than 1 MiB of output fails the run. Without `INBOX_REPOS_YELLOW_CMD` the
feed does nothing and logs nothing, and every count is 0.

**Settings.**

| Variable | Meaning | Default |
|---|---|---|
| `INBOX_REPOS_YELLOW_CMD` | The program to run. | unset (no yellow items) |
| `INBOX_REPOS_YELLOW_EVERY` | Seconds between two runs for one repository; at least 30. | `300` |
| `INBOX_REPOS_YELLOW_TIMEOUT` | Seconds before a run is killed. | `20` |

**Failure.** A slow or broken program never blanks a count: a failed run (exit
1, a timeout, bad JSON) keeps the stored items, stores the error, and the
repository waits one interval. The log gets one line per distinct error, for
example `inbox: cannot read the yellow items of macro-pad: ... did not answer
within 20 s`, not one per run. An answer older than 24 hours counts 0 and lists
nothing. The feed runs apart from the GitHub feed, so a failure of one does not
stop the other. Four runs go at once, and the daemon never waits for the program
longer than its timeout. A script that starts other programs should `exec` the
last one, so the kill reaches it.

**Cost.** The program starts once per repository per interval: 100 repositories
at 300 s make one start every 3 s. A script that must make fewer JIRA calls caches
them in a file of its own.

**Where credentials live.** Not in the repository, not in the wrapper (it has
mode 755), and not in the store. The program reads its own from the macOS
Keychain (`security find-generic-password -s NAME -w`), or from a file of mode
600. `install.sh` has no option for a credential. The store keeps the first line
of the standard error of a failed run, cut to 120 characters, and the log
shows it: a program must not print a credential to standard error.
`examples/yellow-jira.sh` is a complete example for JIRA, with the credential
in the Keychain; the repository ships no JIRA client.

The store keeps the answer in `yellow_answers(repo, items, checked_at, tried_at,
error)` (schema 9), one row per repository name; `items` is the JSON list.
`yellowCounts(now)` gives `{ repo: count }` and `yellowItems(repo, now)` the
list, both ignoring an answer older than 24 hours.

### Repo keys

A repository key (task 0092) shows the emoji of `repo:KEY` as one picture, the
repository name as the caption, and up to three count badges (task 0091).
`src/repo-key.js` makes the fields; the renderer of the repos plugin reads the
tables each time it plans, so the badges always show the tables as they are.

- **Caption.** The repository name, at most 16 runes. A longer name is cut to
  15 runes and "…".
- **Red badge.** The open issues of the repository (`repos.issues`, task 0089),
  in the field `count`.
- **Green badge.** The open PRs (`repos.prs`), in `countGreen`.
- **Yellow badge.** The items of the criteria provider (`yellowCounts`, task
  0093; see "Yellow criteria"), in `countYellow`. The count is by repository
  name, and an answer older than 24 hours counts 0.
- **A count of 0** sets no field. Each message is a full upsert, so the driver
  draws no badge for a field that is missing.
- **A count above 99** draws as "99+". The inbox sends at most 100, so a
  change from 150 to 160 sends no message.
- **Strip.** The strip under the picture shows the worst session status of the
  repository: `waiting` is `WARN`, `running` is `RUNNING`, `done` is `DONE`, and
  none is no color.
- **The `repos` key** keeps its picture (`github.png`), shows the sum of each
  of the three counts over the repository keys on the pad (also capped at
  100), and the worst status of all sessions.

### Repo layer

A press on a repository key (task 0095) opens the layer `repos` / `repo.KEY`.
It holds one key for each piece of work in that repository: the yellow items of
the criteria provider (see "Yellow criteria"), the open PRs, and the active
Claude sessions. One piece of work is one key, because a ticket, the PR whose
title names it, and the session on the PR's branch are one thing.

**The merge.**

1. Each yellow item is an item, with the id `y:ID`.
2. Each open PR whose title holds a yellow id joins the first yellow item that has no PR yet.
   The id must stand between characters that are not letters or digits, in any
   letter case: `PROJ-123` is in `proj-123: fix login` and `fix (PROJ-123)`,
   and not in `PROJ-1234` or `XPROJ-123`. A second PR for the same id, and a PR
   that names no yellow id, is its own item, `pr:N`. The joined item takes the
   PR's url and checks.
3. A Claude session joins the item whose PR is on the session's branch
   (`branch_sessions`, task 0081), or else the first yellow item whose id is in one of
   the session's branches (for example `keith/PROJ-12-login`). One item holds one session;
   any other session is its own item, `s:SID`.

An item has `{ id, yellow, green, session, title, url }` and also `ci`
(`failing`, `pending`, `passing`, `none`, or null) and `agent` (`waiting`,
`running`, `done`, or null). Task 0096 draws them (see "Item keys").

**Item keys.** `itemKey(item, { repo, set })` in `src/plugins/repos-item-key.js`
gives the picture of one item key. The repo layer sends it as `status`,
`count` (always 0), `iconLines`, and `iconEmoji`. It never sends `iconText`:
the driver takes `iconLines` for a child key (task 0094).

- *Caption.* Line 1 is the yellow id, for example `PROJ-123`. Line 2 is the
  green id, for example `#42`. An item with one id shows that id alone. An
  item with neither shows the 8-hex session id. An item with no id and no
  session is a bug, and `itemKey` throws.
- *Long ids.* A line is cut to 16 runes and keeps its end, because the number
  tells ids apart: 9 head runes, `…`, 6 tail runes. `PLATFORM-INFRA-12345`
  shows as `PLATFORM-…-12345`.
- *Emoji.* An item with a yellow or a green id shows one emoji: the emoji of
  its repository, the same as on the repository key. It never shows the
  session emoji, also when the item has a session. An item with neither id
  shows one emoji: the emoji of `session:<8hex>`.
- *Strip colour.* The worst of the checks and the agent wins, in the order
  `ERROR`, `WARN`, `RUNNING`, `DONE`, none.

| Source | Value | Strip |
|---|---|---|
| PR checks (`ci`) | failing | `ERROR` (red) |
| PR checks | pending | `RUNNING` (blue) |
| PR checks | passing | `DONE` (green) |
| PR checks | none, or no PR | none |
| Claude session (`agent`) | waiting | `WARN` (yellow) |
| Claude session | running | `RUNNING` (blue) |
| Claude session | done | `DONE` (green) |
| Yellow item alone | no machine state | none |

A PR that passes, with a session that waits, shows `WARN`. A key with two
lines is a frame on the board: each distinct caption, emoji, and colour takes
one frame slot, and one layer holds at most 20 keys.

**Active session.** A session is in a repository when its folder channel holds
`.OWNER.NAME` with a segment edge on both sides, so `github.com.me.app.driver`
is in `app` and `github.com.me.app-x` is not. It is active when its newest
message is younger than one hour, and it has an unseen message (`end`
marks the rows seen, so a closed session has none). A press never marks a session
seen.

**Order and size.** Yellow items first, then PRs, then sessions. Inside a group:
failing checks, a waiting agent, pending checks, a running agent, the rest, then
by id. At most 20 items show; macropadd pages them (task 0078).

**When items exist.** A repository key has `opens` only when it has at least
one item, so a press on a repository with nothing does nothing. No item node
exists while the layer is shut. When the layer opens, the daemon sends the items,
so the layer shows only its up key for one round trip. While it is open, no item
moves or goes: a changed item updates in place, and a new one goes last (at most
20 in all). A PR that joins a yellow item after its own key was drawn can show
twice until you leave. When the layer closes, the items go. Item nodes exist
for one repository at a time.

**A press on an item** opens, once, the PR (`url:`), else the yellow item
(`url:`), else the tab of the session (`iterm2:`). A target that is not a
valid "here" is logged and nothing runs.

**The PR feed.** Every 60 s the daemon asks `gh api graphql` for
`is:pr is:open user:OWNER archived:false` (`INBOX_REPOS_OWNER`), the first 50
PRs. It stores them in `repo_pulls(repo, number, title, url, ci, head_repo,
branch, synced_at)` (schema 10, `repo` is a repository key). A search of more
than 50 PRs is cut: the feed logs it once, keeps the rows it has, and deletes
none. `listPulls(key)` gives the PRs of one repository, newest number first.
The feed runs apart from the other two, so one that fails does not stop them.

### The apps plugin

One key, `apps`, opens a key for each app you work on today, and each app
opens five actions (task 0085, on the nested layers of task 0086). An app is a folder of
`$INBOX_APPS_ROOT` that holds a `.git` folder, such as
`~/source/github.com/acme/web`. Set the root once:

```
inbox/scripts/install.sh --apps-root ~/source/github.com/acme
```

Every 30 s the daemon reads, for each app, the times of `.git/logs/HEAD` (a
checkout or commit) and `.git/index` (staging), and the newest Claude session
of today in its folder (the `repos` plugin's channels). An app whose newest time
is today is active. The keys show the pinned apps first, oldest pin first,
then the active apps, newest first, at most `$INBOX_APPS_COUNT` (default 5,
one page). While the apps layer is open, no app moves or goes, and a new
one goes at the end. Each app shows its name over its repository's emoji,
the same emoji as its Claude sessions and its PRs. The `apps` key shows the
VS Code logo (`assets/vscode.png`), and is blue
while an app runs or pulls, and red after a crash or a failed pull.

A press on an app opens five children. Each shows its own emoji (from
`assets/emoji/extra.txt`, which no folder or session hashes to), with the
app's emoji as the sticker at its bottom left:

| Key | Emoji | A press |
|---|---|---|
| Code | 💻 | `open -b com.microsoft.VSCode DIR` |
| Claude | 🤖 | brings the app's newest Claude session of today to the front; else the tab this key opened; else a new iTerm tab runs `claude` in DIR |
| Run | ▶️ | runs the app in its own iTerm tab, opened once and reused; while it runs, a press brings the tab to the front. Blue while it runs, red after a crash. |
| Pull | 📥 | `git pull --rebase` in DIR. Blue while it pulls, green after, red when it fails. A conflict aborts the rebase, so the repository is as it was. |
| Term | ⌨️ | the iTerm tab this key opened at DIR, else a new one |

The Run key's caption is the command it runs, the first that fits:

1. the app's entry in `$INBOX_APPS_RUN` (`install.sh --apps-run 'web=make serve PORT=8080'`);
2. a Makefile target `dev`, `run`, `start` or `serve`: `make dev`;
3. a `package.json` script `dev`, else `start`, with the package manager of the
   lockfile (`pnpm-lock.yaml`, `yarn.lock`, `bun.lock`, else npm): `pnpm dev`;
4. else `no run`, and a press does nothing but log a line.

The Run tab types `cd DIR && inbox --plugin apps run NAME -- CMD`, which you
can type yourself too. It runs CMD with the terminal, writes `running`, and
writes `stopped` when CMD exits 0 or you stop it (Ctrl-C), or `crashed`.

```
inbox --plugin apps pin web          # always show web; or pin with no NAME inside it
inbox --plugin apps unpin web
inbox --plugin apps list --all-channels
```

| Channel | Statuses |
|---|---|
| `NAME.pin` | `pinned`, `unpinned` |
| `NAME.run` | `running`, `stopped`, `crashed` |
| `NAME.pull` | `pulling`, `pulled`, `failed` |

The `apps` key takes one slot, like every plugin; `--slot apps=N` places it.
Without `$INBOX_APPS_ROOT` the plugin shows no
key and logs `cannot read apps: no INBOX_APPS_ROOT` once.

### The ping plugin

One key, `ping`, with one child, `Ping` (task 0098). A press on the child runs
the Ping intent of
[macro-spotlight-intents](https://github.com/kgheacock/macro-spotlight-intents),
so one key press proves the path from the pad to an App Intent.

The plugin runs `shortcuts run NAME` with `execFile`, with no shell, its
stdin closed, and a 20 s timeout. `NAME` is a saved shortcut that holds the Ping action. Make it once:

1. Install the macro-spotlight-intents app in `/Applications` and open it once.
2. In the Shortcuts app, click **+**, search the actions for `Ping`, and add the
   one under Macro Spotlight Intents.
3. Name the shortcut `Ping`.

The child shows `RUNNING` while the command runs, then `DONE` when it exits with
code 0 and `ERROR` when it does not. The daemon logs one line for an error, with
the exit code. A press while the shortcut still runs does nothing.

| Variable | Meaning | Default |
|---|---|---|
| `INBOX_PING_SHORTCUT` | the name of the saved shortcut | `Ping` |

A name must start with a letter or a digit and have at most 128 characters on one
line, so that `shortcuts` never reads it as an option. Any other name is refused,
the child shows `ERROR`, and the daemon logs `refused INBOX_PING_SHORTCUT`. The
variable is read from the environment of the daemon. For a run in a terminal:

```
INBOX_PING_SHORTCUT=Ping inbox daemon --plugin ping
```

The plugin works on macOS only.

To check the whole chain, press the key, or press it on the virtual pad
(`driver/plugin/web/virtualpad.html`). Then read the log of the intents app. The
subsystem is the bundle ID, that is `<BUNDLE_ID_PREFIX>.MacroSpotlightIntents`,
where the prefix comes from `Config/Signing.local.xcconfig` in that repository:

```
/usr/bin/log show --last 1m --predicate 'subsystem == "<prefix>.MacroSpotlightIntents"'
```

The output holds `PingIntent started` and `PingIntent finished in N ms`. Use the
full path `/usr/bin/log`: in zsh, plain `log` is a shell builtin.

| Channel | Statuses |
|---|---|
| `ping.run` | `running`, `done`, `failed` |

The `ping` key takes one slot, like every plugin; `--slot ping=N` places it.
