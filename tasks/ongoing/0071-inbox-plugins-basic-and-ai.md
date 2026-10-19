---
id: "0071"
title: "One inbox with plugins: --plugin NAME picks the rules, basic by default, and ai-inbox becomes the ai plugin"
status: "ongoing"
created: "2026-10-19"
updated: "2026-10-19"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/inbox-plugins-d6tc63"
related: ["0064", "0065", "0066", "0067", "0068", "0069", "0070"]
tags: ["inbox", "daemon", "agents", "plugin"]
---

# 0071 — One inbox with plugins

## Problem

Task 0070 added `ai-inbox` beside `inbox`. Both run one copy of the code through a "profile", but the
profile also made `ai-inbox` a second program: a second command (`bin/ai-inbox.js`), a second store
(`$AI_INBOX_DB`, `~/.local/share/ai-inbox/ai-inbox.db`), a second error prefix, a second install flag
(`install.sh --ai`), and help text made by rewriting the `inbox` help with regular expressions.

Keith wants one inbox. Each kind of inbox (the AI one now, others later) is a plugin of it, picked with an
argument, and the plain inbox is the default. Backwards compatibility is not required: Keith is the only
client and always runs the latest.

## Review of the two implementations

What the two share today, line for line: the CLI parser and every subcommand (`cli.js`), the store
(`store.js`), "here" (`here.js`), the daemon loop and its press handling (`daemon.js`), the renderer that
keeps keys still while the layer is open (`render.js`), the protocol, and the install scripts.

What differs, and whether it is a legitimate difference:

| Difference | Where today | Legitimate? |
|---|---|---|
| Allowed statuses (`waiting`, `running`, `done` vs any word) and the default status (`done` vs `info`) | `profiles.js` | Yes: it is what a message means to that inbox. |
| Top key id and icon (`ai-inbox` + `ai.png` vs `inbox` + `gmail.png`) | `ai-render.js`, `render.js` | Yes: each plugin owns one key on the pad. |
| Key color and pip (highest status, waiting channels vs a count) | `AI_RULES`, `INBOX_RULES` | Yes: the reason the AI inbox exists. |
| Order of the children (highest status first vs newest first) | `before` in the rules | Yes. |
| One help paragraph on the statuses | regex in `helpText` | Yes, as text the plugin supplies. |
| Command name, error prefix | `profiles.js`, `report()` | No: one command, `inbox`. |
| Store file and its variable (`AI_INBOX_DB`) | `profiles.js`, install scripts | No: one store; the plugin is a column. |
| Install flag, service label, unit, wrapper | `install.sh --ai` | Partly: one service per plugin daemon is real, a second command is not. |
| Help text rewritten by regex | `helpText` | No: the plugin supplies its lines. |

## Goals

- One command, `inbox`. `--plugin NAME` picks the plugin on every command, before or after the command
  word. The default is `basic`, today's inbox.
- A plugin is one file in `inbox/src/plugins/` that holds only the legitimate differences above. Adding a
  plugin is adding a file and one line in `src/plugins/index.js`.
- One store. Each message belongs to one plugin; every command reads and writes only the messages of its
  plugin, so AI messages do not bury Keith's own (the reason 0070 split the stores).
- `ai-inbox` is gone: `inbox --plugin ai` replaces it everywhere (command, daemon, install, Claude Code hooks).

## Non-goals

- Plugins outside this repository, or loaded from a path at run time.
- One daemon that serves several plugins. One daemon still runs one plugin, so each plugin keeps its own key
  and the press logic stays as it is.
- Keeping `ai-inbox`, `AI_INBOX_DB`, `install.sh --ai`, or the old `ai-inbox.db` data.
- New statuses, colors, or driver changes.

## Approaches considered

### Approach A — One store with a `plugin` column, and a plugin file per kind (chosen)

Schema 4 adds `messages.plugin` (default `basic`) and keys `channel_here` by `(plugin, channel)`. `openStore`
takes the plugin name and every query filters on it. A plugin file exports the statuses, the default
status, the key rules, and help lines.

- Good, because there is one store, one `INBOX_DB`, one command, one install, and one completion.
- Good, because the same channel name (`macro-pad.driver`) can live in both plugins without mixing.
- Bad, because every store query gains a `plugin = ?` term, and the migration rebuilds `channel_here`.

### Approach B — A store file per plugin

Keep the store as it is. The plugin name picks the file: `$XDG_DATA_HOME/inbox/<plugin>.db`.

- Good, because `store.js` does not change.
- Bad, because `INBOX_DB` or `--db` then points every plugin at the same file, and they mix. It needs one
  variable per plugin again, which is the second-program shape this task removes.

### Approach C — No plugins: one inbox that colors by status

Teach the basic inbox the three AI statuses, and drop the AI key.

- Good, because it is the least code.
- Bad, because AI messages bury Keith's own again, and the pip cannot both count messages and count waiting
  channels. Keith asked for plugins.

## Decision

Chosen: **Approach A**. It is the only one where the plugin holds every legitimate difference and nothing
else, with one store and one command. The cost is a plugin filter on each store query and a schema
migration, both covered by the existing store tests.

## Design

**Plugin** (`inbox/src/plugins/<name>.js`, listed in `inbox/src/plugins/index.js`):

| Field | basic | ai |
|---|---|---|
| `name` | `basic` | `ai` |
| `summary` (one line, for `inbox plugins` and help) | messages counted per channel | the state of AI agents: waiting, running, done |
| `statuses` (null: any word of letters, digits, `-`, `_`) | null | `waiting`, `running`, `done` |
| `defaultStatus` | `info` | `done` |
| `about` (a paragraph added to `inbox -h` and `inbox send -h`) | none | the order and pip rule |
| `rules` (the renderer rules: `id`, `top`, `child`, `before`) | today's `INBOX_RULES`, key `inbox` | today's `AI_RULES`, key `ai-inbox` |

The key ids stay `inbox` and `ai-inbox`, so the keys `macropadd` keeps in `layers.json` are reused.
`render.js` keeps only the shared renderer and takes the rules as a required argument.

**CLI.** `--plugin NAME` and `--plugin=NAME` are taken out of the arguments before the command is parsed,
anywhere before `--`. An unknown name, a missing value, or two different names exit 2 and list the plugins.
A new command, `inbox plugins`, prints each plugin and its summary, the default marked. Every message,
error, and help text says `inbox`. Help is built from the plugin: the `--status` line names its statuses and
default, and `about` is appended. `profiles.js`, `ai-render.js`, and `bin/ai-inbox.js` are removed.

**Store.** Schema 4:

```sql
ALTER TABLE messages ADD COLUMN plugin TEXT NOT NULL DEFAULT 'basic';
DROP INDEX messages_unseen;
CREATE INDEX messages_unseen ON messages (plugin, channel, id) WHERE seen_at IS NULL;
-- channel_here is rebuilt with PRIMARY KEY (plugin, channel); its rows go to basic.
```

`openStore(file, env, { plugin })` returns a store bound to one plugin (default `basic`). Every read, write,
mark, and fallback "here" uses only that plugin's rows. Rows in a schema 3 file become `basic`.

**Daemon.** `startDaemon({ plugin })` opens the store for the plugin and renders with its rules. The pid
file is `<db>.<plugin>.daemon.pid`, so a basic and an ai daemon run beside each other on one store. A
second daemon of the same plugin exits 3.

**Install.** `install.sh [--plugin NAME]` and `uninstall.sh [--plugin NAME]`. The name is checked with
`inbox plugins`. `basic` keeps `com.kgheacock.macropad.inbox` and `inbox-daemon.service`; another plugin
gets `com.kgheacock.macropad.inbox.NAME` and `inbox-daemon-NAME.service`, and its daemon runs with
`--plugin NAME`. Both write the one `$PREFIX/bin/inbox` wrapper and the completion. `uninstall.sh` removes
the wrapper and the completion only when no other plugin's service is left, and `--purge` (the store holds
every plugin) is refused while another plugin's service is installed.

**Claude Code hooks.** `inbox/claude-code/claude-ai-inbox.sh` runs `inbox --plugin ai` (variable
`INBOX_BIN`, default `~/.local/bin/inbox`).

**Completion.** `_inbox` offers `--plugin` with the names from `inbox plugins`, and passes it on when it
lists channels.

Files to change:

- new: `inbox/src/plugins/index.js`, `inbox/src/plugins/basic.js`, `inbox/src/plugins/ai.js`
- removed: `inbox/src/profiles.js`, `inbox/src/ai-render.js`, `inbox/bin/ai-inbox.js`
- `inbox/src/cli.js`, `inbox/src/store.js`, `inbox/src/daemon.js`, `inbox/src/render.js`, `inbox/src/entry.js`,
  `inbox/bin/inbox.js`, `inbox/package.json`
- `inbox/scripts/install.sh`, `inbox/scripts/uninstall.sh`, `inbox/completions/_inbox`
- `inbox/claude-code/*`, `inbox/README.md`, tests under `inbox/test/` and `inbox/testlib/`

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each item names its proof.

- [ ] **DoD-1** — `inbox --plugin ai send --status waiting agent x` and `inbox send mine y` write one store.
  `inbox list --all-channels` shows only `y`; `inbox --plugin ai list --all-channels` shows only `x`;
  `inbox --plugin ai seen agent` leaves the basic count at 1. **Proof:**
  `cd inbox && node --test --test-name-pattern=TestPluginsShareOneStore test/cli.test.js`
- [ ] **DoD-2** — `--plugin` works before and after the command word and as `--plugin=ai`. An unknown name
  exits 2 and names `basic` and `ai`. `inbox plugins` lists both, `basic` marked as the default. **Proof:**
  `node --test --test-name-pattern=TestPluginFlag test/cli.test.js`
- [ ] **DoD-3** — With `--plugin ai`, `--status error` exits 2, names the three statuses, and saves nothing;
  no status saves `done`. Basic still takes any word, default `info`. **Proof:**
  `node --test --test-name-pattern=TestAiStatus test/cli.test.js`
- [ ] **DoD-4** — `inbox --plugin ai -h` and `inbox --plugin ai send -h` say `inbox`, name the three statuses
  and the pip rule; `inbox -h` lists `--plugin` and both plugins. No help or error text says `ai-inbox`.
  **Proof:** `node --test --test-name-pattern=TestPluginHelp test/cli.test.js`
- [ ] **DoD-5** — A schema 3 file opens as schema 4; its messages and channel fallbacks belong to `basic`, and
  `ai` sees none of them. A fallback "here" set for `ai` does not show for `basic`. **Proof:**
  `node --test --test-name-pattern='TestMigrate|TestPluginScope' test/store.test.js`
- [ ] **DoD-6** — A basic and an ai daemon run on one store; each takes its own pid file, and a second ai
  daemon exits 3. The ai daemon shows only ai channels and marks nothing on a press in the `inbox` layer.
  **Proof:** `node --test --test-name-pattern='TestAiDaemon|TestPidFilePerPlugin' test/daemon.test.js`
- [ ] **DoD-7** — The renderer tests of 0070 pass against `src/plugins/ai.js`, and every other test passes.
  **Proof:** `cd inbox && npm test`
- [ ] **DoD-8** — `install.sh --plugin ai --dry-run` names `com.kgheacock.macropad.inbox.ai` (macOS) or
  `inbox-daemon-ai.service` (Linux) and `--plugin ai`, and writes `$PREFIX/bin/inbox`. Without `--plugin`,
  its output is unchanged from before this task except for the store path. `--plugin nope` exits 2.
  **Proof:** run the commands; `grep -c ai`
- [ ] **DoD-9** — Nothing in `inbox/` (code, scripts, completion, README, hooks) names `ai-inbox` as a command,
  `AI_INBOX_DB`, or `--ai`. **Proof:** `grep -rnE 'AI_INBOX_DB|--ai\b|bin/ai-inbox' inbox/` prints nothing
  - The implementer ran DoD-1 to DoD-9 (all 102 tests pass; script dry runs on Linux). The boxes are left
    for a reviewer to tick.
- [ ] **DoD-10** — On the pad, both daemons running from `install.sh` and `install.sh --plugin ai`: the Claude
  Code hooks turn the ai key yellow and green as in 0070, and `inbox send` still changes only the inbox key.
  **Proof:** Keith's check on the board, recorded in Notes

## Risks

- The old `ai-inbox` service keeps running from a deleted `bin/ai-inbox.js` and restarts in a loop → before
  pulling, run the old `inbox/scripts/uninstall.sh --ai --purge`; the PR body says so.
- The live Claude Code hook in `~/.claude/hooks/` still calls `~/.local/bin/ai-inbox` → it exits 0 silently
  when the command is missing, so nothing breaks; copy the new script (README "Install").
- A store query that forgets the plugin leaks one plugin's messages into another → DoD-5 and DoD-1 cover
  each command.

## Open questions

- [ ] Should `inbox list --all-plugins` exist, to see every plugin's messages at once? Not in this task. — kgheacock

## Notes

Upgrade steps for Keith (once, before pulling this change):

```
inbox/scripts/uninstall.sh --ai --purge    # the old ai-inbox service, wrapper and store
git pull
inbox/scripts/install.sh                   # basic, rewrites the inbox wrapper
inbox/scripts/install.sh --plugin ai
install -m 755 inbox/claude-code/claude-ai-inbox.sh ~/.claude/hooks/
```
