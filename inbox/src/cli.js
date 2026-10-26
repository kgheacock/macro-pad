// Subcommands, help text, and error messages. main() takes an io object so
// a test can run the CLI in process; bin/inbox.js passes the real streams.

import path from 'node:path';
import { parseArgs } from 'node:util';
import { inferChannel, sourceRoot, tildify, validateChannel } from './channel.js';
import { DEFAULT_POLL_MS, DEFAULT_PORT, acquirePidFile, startDaemon } from './daemon.js';
import { componentInput, componentOf, emojiFor, sessionInput, splitSessionChannel } from './emoji.js';
import { DaemonRunningError, StoreError, UsageError } from './errors.js';
import { detectHere, resolveHere, validateHere } from './here.js';
import { DEFAULT_PLUGIN, PLUGINS, getPlugin } from './plugins/index.js';
import { defaultDbPath, openStore } from './store.js';

const MAX_LINE_BYTES = 64 * 1024;
const FLUSH_MS = 200;
const FLUSH_LINES = 500;

const CHANNEL_RULES = `How the channel is chosen:
  A CHANNEL argument always wins. Without one, a directory under the source
  root (INBOX_SOURCE_ROOT, default ~/source) gives its channel: the path
  below the root, joined by dots, so ~/source/macro-pad/driver is
  macro-pad.driver. Outside the root, pass the channel.`;

const DB_OPTION = `  --db PATH   use this SQLite file instead of $INBOX_DB`;
const CHANNEL_OPTION = `  --channel C  the CHANNEL, as an option instead of an argument`;
// The --status line names the statuses of the plugin; helpText fills it in.
const STATUS_OPTION = '@STATUS@';
const HERE_OPTION = `  --here H    where a press on the channel key goes: iterm2:UUID, app:BUNDLE_ID, or
              url:URL (default: $INBOX_HERE, else the iTerm session you are in)`;

const HELP = {
  inbox: `inbox - leave short messages per channel, and read them later

Usage: inbox [--plugin NAME] COMMAND [ARGUMENTS] [--db PATH]
       COMMAND | inbox [OPTIONS]            (same as: inbox pipe [OPTIONS])
       COMMAND | inbox --channel debug

Commands:
  send       send one message
  pipe       send each line of standard input as a message
  list       list messages, seen and unseen
  count      print how many messages are not seen yet
  channels   list channels that have unseen messages
  channel    print the channel of the working directory
  seen       mark messages as seen
  here       show or set where a press on a channel key goes
  daemon     watch the inbox and show it on the macro pad
  plugins    list the plugins
  emoji      print the emoji of a folder or a Claude Code session
  help       show help for a command

@PLUGINS@

Examples:
  inbox send debug "build done"
  my_cmd 2>&1 | inbox pipe debug
  echo "hello" | inbox                 (same as: inbox pipe)
  inbox --plugin ai send --status waiting macro-pad "needs a decision"

Run 'inbox COMMAND -h' for the details of one command.
Exit codes: 0 success, 1 store error, 2 usage error, 3 a daemon already runs.`,

  send: `Usage: inbox send [--status STATUS] [--here HERE] [--branch B] [--channel CHANNEL | CHANNEL] MESSAGE

Send one message.

  CHANNEL   where the message goes, such as "debug" or "macro-pad.driver"
  MESSAGE   the text; quote it when it has spaces
${CHANNEL_OPTION}
${STATUS_OPTION}
${HERE_OPTION}
  --branch B  the git branch of a Claude Code session channel PROJECT.SID: a PR
              from branch B of PROJECT's repository then shows the session's
              emoji (task 0081)
${DB_OPTION}

${CHANNEL_RULES}

Examples:
  inbox send debug "build done"
  inbox send --status error debug "build failed"
  inbox send "tests passed"        (in ~/source/macro-pad: channel macro-pad)`,

  pipe: `Usage: inbox pipe [--status STATUS] [--here HERE] [--channel CHANNEL | CHANNEL]

Read standard input and send each line as one message. Empty lines are
skipped. A line longer than 64 KiB is cut. Messages are saved while the pipe
is still open, within 200 ms.

The input is also copied to standard output, as tee does, so a pipe through
inbox still shows the text. Ctrl-C does not stop inbox before the command that
feeds it: inbox reads to the end of the input, then exits 0. Press Ctrl-C a
second time, or send SIGTERM, to save what was read and exit 0 at once.

  CHANNEL   where the messages go
${CHANNEL_OPTION}
${STATUS_OPTION}
${HERE_OPTION}
${DB_OPTION}

${CHANNEL_RULES}

After 2>&1 the two streams share one pipe, and inbox cannot tell them apart.
To keep them apart, give each stream its own pipe and status (bash, zsh):
  my_cmd 2> >(inbox pipe --status error debug) | inbox pipe debug

Examples:
  my_cmd 2>&1 | inbox pipe debug
  tail -f build.log | inbox pipe`,

  list: `Usage: inbox list [CHANNEL | --channel C | --all-channels] [--unseen] [--json] [-n N]

List messages, oldest first. A leading "*" marks the ones not seen yet.
Each line is: time, status, then the channel (with --all-channels), then the
message.

  CHANNEL         list this channel (or --channel C)
  --all-channels  list every channel
  --unseen        list only the messages that are not seen yet
  --json          print a JSON array of {id, channel, body, created_at,
                  seen_at, cwd, status, here}
  -n N            keep only the newest N messages
${DB_OPTION}

Without CHANNEL or --all-channels, list uses the channel of the working
directory, and every channel outside the source root.

Examples:
  inbox list debug
  inbox list --all-channels --json`,

  count: `Usage: inbox count [CHANNEL | --channel C | --all-channels]

Print how many messages are not seen yet.

  CHANNEL         count this channel (or --channel C)
  --all-channels  count every channel
${DB_OPTION}

Without CHANNEL or --all-channels, count uses the channel of the working
directory, and every channel outside the source root.

Example:
  inbox count debug`,

  channels: `Usage: inbox channels [--json]

Print each channel that has unseen messages, how many, and the time of the
newest one.

  --json   print a JSON array of {channel, count, newest_id, newest_at}
${DB_OPTION}

Example:
  inbox channels`,

  channel: `Usage: inbox channel

Print the channel that send and pipe infer from the working directory: the
path below the source root ($INBOX_SOURCE_ROOT, default ~/source) joined by
dots. Outside the source root, or for a folder name that cannot be a channel
segment, it prints an error and exits 2. Scripts use it to name channels the
same way inbox does.

Example:
  cd ~/source/macro-pad/driver && inbox channel   # macro-pad.driver`,

  seen: `Usage: inbox seen [CHANNEL | --channel C | --all-channels]

Mark the unseen messages seen. Print how many changed. A message that lands
after the command reads the inbox stays unseen.

  CHANNEL         mark this channel (or --channel C)
  --all-channels  mark every channel
${DB_OPTION}

${CHANNEL_RULES}

Examples:
  inbox seen debug
  inbox seen --all-channels`,

  here: `Usage: inbox here
       inbox here CHANNEL
       inbox here CHANNEL HERE
       inbox here --clear CHANNEL

A "here" says where a press on a channel key goes. Its kinds are:
  iterm2:UUID       an iTerm session (36 hex digits and dashes)
  app:BUNDLE_ID     a macOS app, such as com.tinyspeck.slackmacgap
  url:URL           a deep link; slack, msteams, ms-outlook, or https

  (no argument)     print the "here" of the iTerm session you are in, or exit 1
  CHANNEL           print the fallback "here" of the channel, or exit 1
  CHANNEL HERE      set the fallback of the channel, used by a message with none
  --clear CHANNEL   remove the fallback of the channel
${DB_OPTION}

send and pipe look for a "here" in this order: --here, $INBOX_HERE, the iTerm
session you are in (not inside tmux). macOS only.

Examples:
  inbox here
  inbox here chat app:com.tinyspeck.slackmacgap
  inbox here --clear chat`,

  emoji: `Usage: inbox emoji [CHANNEL | --channel C | --folder DIR] [--session ID] [--format F]

Print the emoji a key shows for a folder, a session, or both (task 0073). The
same input always gives the same emoji, on every machine. It reads no store,
so a status line can run it often. Every channel of one repository gives the
emoji of that repository (task 0081): macro-pad and macro-pad.driver match,
and so does github.com.me.macro-pad.

  CHANNEL        the emoji of this channel's repository (or --channel C)
  --folder DIR   the emoji of this folder: that of the channel it gives
  --session ID   the emoji of this Claude Code session (its first 8 hex digits)
  --format F     char (default), name, ref (the file under assets/), or json

With no CHANNEL, --folder, or --session, the folder is the current directory.
With a folder and a session, it prints the folder's emoji, then the session's,
as a session key shows them.

${CHANNEL_RULES}

Examples:
  inbox emoji
  inbox emoji --folder ~/source/macro-pad --session "$CLAUDE_CODE_SESSION_ID"
  inbox emoji macro-pad.driver --format name`,

  plugins: `Usage: inbox plugins

Print each plugin and what it shows. Pick one with --plugin NAME on any
command, before or after the command word; without it, the plugin is
${DEFAULT_PLUGIN}. One store holds every plugin, and each command reads and
writes only the messages of its plugin.

Example:
  inbox --plugin ai channels`,

  daemon: `Usage: inbox daemon [--plugin NAME]... [--port N] [--slot [NAME=]N]... [--poll DURATION]

Run until stopped. Watch the store, and send the unseen counts to macropadd
over its WebSocket. It starts with or without macropadd, and sends the full
state again each time it connects. Log lines go to standard error.

One process shows every plugin, each on its own key. --plugin NAME, once or
more (or --plugin A,B), shows only those plugins.

  --plugin NAME     show this plugin (default: every plugin; see 'inbox plugins')
  --port N          macropadd's port on 127.0.0.1 (default: ${DEFAULT_PORT})
  --assets-dir DIR  unused: the daemon sends icon text, not picture files. It is
                    still accepted, so a service that passes it starts.
  --slot NAME=N     put the key of plugin NAME in slot N, 0 to 5; once per plugin
                    (default: the first free slot). With one plugin, --slot N.
  --poll DURATION   how often to look for a change, such as 500ms or 2s
                    (default: ${DEFAULT_POLL_MS}ms)
${DB_OPTION}

Only one daemon runs per store and plugin. A daemon that would show a plugin
another daemon already shows exits with code 3, and shows nothing.
SIGINT and SIGTERM stop it with exit code 0. The keys stay on the pad.
To run it at login, use inbox/scripts/install.sh.

Examples:
  inbox daemon
  inbox daemon --slot basic=0 --slot ai=1
  inbox daemon --plugin ai --slot 2`,
};

// One line per plugin: its name, its summary, and the default marked.
function pluginList() {
  const width = Math.max(...Object.keys(PLUGINS).map((name) => name.length));
  return Object.values(PLUGINS).map(
    (p) => `${p.name.padEnd(width)}  ${p.summary}${p.name === DEFAULT_PLUGIN ? ' (default)' : ''}`,
  );
}

// The help of a command, for the plugin it runs with: its statuses and its
// paragraph.
function helpText(command, plugin) {
  const statuses = plugin.statuses ? plugin.statuses.join(' | ') : 'letters, digits, - and _';
  let text = HELP[command]
    .replace('@STATUS@', `  --status S  the status of the message: ${statuses} (default: ${plugin.defaultStatus})`)
    .replace(
      '@PLUGINS@',
      `Plugins (--plugin NAME, before or after the command):\n${pluginList().map((line) => `  ${line}`).join('\n')}`,
    );
  if (plugin.about && ['inbox', 'send', 'pipe'].includes(command)) {
    text += `\n\nPlugin ${plugin.name}:\n${plugin.about}`;
  }
  return text;
}

const COMMANDS = ['send', 'pipe', 'list', 'count', 'channels', 'channel', 'seen', 'here', 'daemon', 'plugins', 'emoji'];

const SHARED_OPTIONS = { db: { type: 'string' } };
const CHANNEL_OPTION_COMMANDS = ['send', 'pipe', 'list', 'count', 'seen', 'emoji'];
const OPTIONS = {
  send: { status: { type: 'string' }, here: { type: 'string' }, branch: { type: 'string' } },
  pipe: { status: { type: 'string' }, here: { type: 'string' } },
  list: {
    'all-channels': { type: 'boolean' },
    unseen: { type: 'boolean' },
    json: { type: 'boolean' },
    n: { type: 'string', short: 'n' },
  },
  count: { 'all-channels': { type: 'boolean' } },
  channels: { json: { type: 'boolean' } },
  channel: {},
  seen: { 'all-channels': { type: 'boolean' } },
  here: { clear: { type: 'boolean' } },
  plugins: {},
  emoji: { folder: { type: 'string' }, session: { type: 'string' }, format: { type: 'string' } },
  daemon: {
    port: { type: 'string' },
    'assets-dir': { type: 'string' },
    slot: { type: 'string', multiple: true },
    poll: { type: 'string' },
  },
};

function distance(a, b) {
  const row = Array.from({ length: b.length + 1 }, (_, i) => i);
  for (let i = 1; i <= a.length; i++) {
    let diagonal = row[0];
    row[0] = i;
    for (let j = 1; j <= b.length; j++) {
      const above = row[j];
      row[j] = Math.min(row[j] + 1, row[j - 1] + 1, diagonal + (a[i - 1] === b[j - 1] ? 0 : 1));
      diagonal = above;
    }
  }
  return row[b.length];
}

function unknownCommand(name) {
  let best = null;
  for (const command of COMMANDS) {
    const d = distance(name, command);
    if (d <= 2 && (best === null || d < best.d)) best = { command, d };
  }
  const hint = best ? `; did you mean "${best.command}"?` : '.';
  return new UsageError(`unknown command "${name}"${hint} Run 'inbox -h' for the list.`, {
    help: false,
  });
}

function parseOptions(command, args) {
  try {
    return parseArgs({
      args,
      options: {
        ...SHARED_OPTIONS,
        ...(CHANNEL_OPTION_COMMANDS.includes(command) && { channel: { type: 'string' } }),
        ...OPTIONS[command],
      },
      allowPositionals: true,
      strict: true,
    });
  } catch (e) {
    if (e.code === 'ERR_PARSE_ARGS_UNKNOWN_OPTION') {
      const name = /'([^']+)'/.exec(e.message)?.[1] ?? 'option';
      throw new UsageError(`unknown option "${name}" for ${command}`);
    }
    if (e.code === 'ERR_PARSE_ARGS_INVALID_OPTION_VALUE') {
      const name = /'([^']+)'/.exec(e.message)?.[1] ?? 'option';
      throw new UsageError(`option ${name} needs a value`);
    }
    throw new UsageError(e.message);
  }
}

function pad2(n) {
  return String(n).padStart(2, '0');
}

function formatTime(ms) {
  const d = new Date(ms);
  return `${d.getFullYear()}-${pad2(d.getMonth() + 1)}-${pad2(d.getDate())} ${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}

function parseStatus(io, status) {
  const { statuses, defaultStatus } = io.plugin;
  if (status === undefined) return defaultStatus;
  if (statuses !== null && !statuses.includes(status)) {
    throw new UsageError(`bad status "${status}": use ${statuses.join(', ')}`);
  }
  if (!/^[A-Za-z0-9_-]+$/.test(status)) {
    throw new UsageError(
      `bad status "${status}": a status may hold only letters, digits, - and _`,
    );
  }
  return status;
}

// An explicit channel wins; otherwise infer one, or fail with exit code 2.
function requireChannel(io, explicit, example) {
  if (explicit !== undefined) return validateChannel(explicit);
  const root = sourceRoot(io.env);
  const channel = inferChannel(io.cwd(), root);
  if (channel === null) {
    throw new UsageError(
      `no channel: ${tildify(io.cwd())} is not under ${tildify(root)}. Pass it first, as in: ${example}`,
    );
  }
  return channel;
}

// For list and count: an explicit channel, --all-channels, the inferred
// channel, or every channel outside the source root. null means every channel.
function readScope(io, values, positionals) {
  if (positionals.length > 1) {
    throw new UsageError('give one CHANNEL, or --all-channels');
  }
  if (values['all-channels']) {
    if (positionals.length) throw new UsageError('give a CHANNEL or --all-channels, not both');
    return null;
  }
  if (positionals.length) return validateChannel(positionals[0]);
  return inferChannel(io.cwd(), sourceRoot(io.env));
}

function storeFile(io, values) {
  return values.db ?? defaultDbPath(io.env);
}

function withStore(io, values, fn) {
  const store = openStore(storeFile(io, values), io.env, { plugin: io.plugin.name });
  try {
    return fn(store);
  } finally {
    store.close();
  }
}

function out(io, text) {
  if (io.stdoutClosed) return;
  io.stdout.write(`${text}\n`);
}

// When the reader of standard output goes away, as in 'inbox list | head',
// stop writing and finish normally, like a tool that ignores SIGPIPE.
function quietOnClosedStdout(io) {
  if (typeof io.stdout.on !== 'function') return;
  io.stdout.on('error', (e) => {
    if (e.code !== 'EPIPE') throw e;
    io.stdoutClosed = true;
  });
}

function runSend(io, { values, positionals }) {
  if (positionals.length === 0) {
    throw new UsageError('send needs a message, as in: inbox send debug "build done"');
  }
  if (positionals.length > 2) {
    throw new UsageError(
      'send takes CHANNEL and MESSAGE; quote a message with spaces, as in: inbox send debug "build done"',
    );
  }
  const message = positionals[positionals.length - 1];
  if (message.trim() === '') {
    throw new UsageError('send needs a message, as in: inbox send debug "build done"');
  }
  const explicit = positionals.length === 2 ? positionals[0] : undefined;
  const channel = requireChannel(io, explicit, 'inbox send CHANNEL MESSAGE');
  const status = parseStatus(io, values.status);
  const here = resolveHere({ flag: values.here, env: io.env });
  const link = branchLink(channel, values.branch);
  withStore(io, values, (store) => {
    if (link !== null) store.linkBranch(link.repo, link.branch, link.session);
    store.insert(channel, message, io.cwd(), { status, here });
  });
  return 0;
}

// The branch link of `send --branch` (task 0081): { repo, branch, session },
// or null without --branch.
function branchLink(channel, branch) {
  if (branch === undefined) return null;
  if (branch.trim() === '') throw new UsageError('--branch needs a branch name');
  const { folder, session } = splitSessionChannel(channel);
  if (session === null) {
    throw new UsageError(`--branch needs a session channel PROJECT.SID; "${channel}" has no session`);
  }
  return { repo: componentOf(folder), branch: branch.trim(), session };
}

function cutLine(line, warnCut) {
  if (Buffer.byteLength(line) <= MAX_LINE_BYTES) return line;
  warnCut();
  const bytes = Buffer.from(line);
  let end = MAX_LINE_BYTES;
  while (end > 0 && (bytes[end] & 0xc0) === 0x80) end--;
  return bytes.subarray(0, end).toString('utf8');
}

// Reads lines until end of input or a signal. Saves in batches so a long
// pipe shows its lines within FLUSH_MS and a burst costs a few transactions.
function pipeLines(io, store, channel, status, here) {
  const cwd = io.cwd();
  let pending = [];
  let timer = null;
  let tail = '';
  let discarding = false;
  let warned = false;
  let onTimer = () => {};

  // Pass the input on to standard output, as tee does. If the next command
  // in the pipeline goes away, go on saving.
  let forwarding = typeof io.stdout.on === 'function';
  if (forwarding) io.stdout.on('error', () => (forwarding = false));
  const forward = (chunk) => {
    if (forwarding) io.stdout.write(chunk);
  };

  const warnCut = () => {
    if (warned) return;
    warned = true;
    io.stderr.write(`inbox: cut a line longer than ${MAX_LINE_BYTES / 1024} KiB\n`);
  };

  const flush = () => {
    if (timer) clearTimeout(timer);
    timer = null;
    if (pending.length === 0) return;
    const batch = pending;
    pending = [];
    store.insertMany(channel, batch, cwd, { status, here });
  };

  const addLine = (raw) => {
    const line = raw.replace(/\r$/, '');
    if (line.trim() === '') return;
    pending.push(cutLine(line, warnCut));
    if (pending.length >= FLUSH_LINES) flush();
    else if (!timer) timer = setTimeout(() => onTimer(), FLUSH_MS);
  };

  return new Promise((resolve, reject) => {
    let done = false;
    const finish = (error) => {
      if (done) return;
      done = true;
      if (timer) clearTimeout(timer);
      io.process.off('SIGINT', onSignal);
      io.process.off('SIGTERM', onSignal);
      io.stdin.off('data', onData);
      io.stdin.off('end', onEnd);
      io.stdin.off('error', finish);
      if (error) reject(error);
      else resolve(0);
    };
    const guarded = (fn) => (...args) => {
      try {
        fn(...args);
      } catch (e) {
        finish(e);
      }
    };
    onTimer = guarded(flush);
    const onData = guarded((chunk) => {
      forward(chunk);
      const parts = chunk.split('\n');
      for (let i = 0; i < parts.length; i++) {
        if (i > 0) {
          addLine(tail);
          tail = '';
          discarding = false;
        }
        if (!discarding) tail += parts[i];
        if (tail.length > MAX_LINE_BYTES) {
          warnCut();
          tail = tail.slice(0, MAX_LINE_BYTES);
          discarding = true;
        }
      }
    });
    const onEnd = guarded(() => {
      addLine(tail);
      flush();
      finish();
    });
    // The first Ctrl-C reaches the writer as well. Keep reading until it
    // closes the pipe, so its last lines are saved and it does not die of a
    // broken pipe. A second SIGINT, or SIGTERM, saves the lines already read
    // and drops a half-read last line.
    let interrupted = false;
    const onSignal = guarded((signal) => {
      flush();
      if (signal === 'SIGINT' && !interrupted) {
        interrupted = true;
        return;
      }
      io.stdin.destroy();
      finish();
    });
    io.stdin.setEncoding('utf8');
    io.stdin.on('data', onData);
    io.stdin.on('end', onEnd);
    io.stdin.on('error', finish);
    io.process.on('SIGINT', onSignal);
    io.process.on('SIGTERM', onSignal);
  });
}

async function runPipe(io, { values, positionals }) {
  if (positionals.length > 1) {
    throw new UsageError('pipe takes at most one argument, the CHANNEL, as in: my_cmd 2>&1 | inbox pipe debug');
  }
  const channel = requireChannel(io, positionals[0], 'inbox pipe CHANNEL');
  const status = parseStatus(io, values.status);
  // Like the channel, the "here" is fixed before the first line is read.
  const here = resolveHere({ flag: values.here, env: io.env });
  if (io.stdin.isTTY) {
    throw new UsageError(
      'pipe reads lines from a pipe or file, as in: my_cmd 2>&1 | inbox pipe debug',
    );
  }
  const store = openStore(storeFile(io, values), io.env, { plugin: io.plugin.name });
  try {
    return await pipeLines(io, store, channel, status, here);
  } finally {
    store.close();
  }
}

function runList(io, { values, positionals }) {
  let limit = null;
  if (values.n !== undefined) {
    limit = Number(values.n);
    if (!Number.isInteger(limit) || limit < 1) {
      throw new UsageError(`bad -n "${values.n}": give a whole number of 1 or more`);
    }
  }
  const channel = readScope(io, values, positionals);
  const messages = withStore(io, values, (store) =>
    store.list({ channel, includeSeen: !values.unseen, limit }),
  );
  if (values.json) {
    out(io, JSON.stringify(messages));
    return 0;
  }
  const width = Math.max(0, ...messages.map((m) => m.status.length));
  for (const m of messages) {
    const mark = m.seen_at === null ? '* ' : '  ';
    const where = channel === null ? `${m.channel}  ` : '';
    out(io, `${mark}${formatTime(m.created_at)}  ${m.status.padEnd(width)}  ${where}${m.body}`);
  }
  return 0;
}

function runCount(io, { values, positionals }) {
  const channel = readScope(io, values, positionals);
  out(io, String(withStore(io, values, (store) => store.count(channel))));
  return 0;
}

function runChannels(io, { values, positionals }) {
  if (positionals.length) throw new UsageError('channels takes no arguments');
  const rows = withStore(io, values, (store) => store.unseen());
  if (values.json) {
    out(
      io,
      JSON.stringify(
        rows.map((r) => ({
          channel: r.channel,
          count: r.count,
          newest_id: r.newestId,
          newest_at: r.createdAt,
        })),
      ),
    );
    return 0;
  }
  const width = Math.max(0, ...rows.map((r) => r.channel.length));
  for (const r of rows) {
    out(io, `${r.channel.padEnd(width)}  ${String(r.count).padStart(5)}  ${formatTime(r.createdAt)}`);
  }
  return 0;
}

function runSeen(io, { values, positionals }) {
  if (positionals.length > 1) throw new UsageError('give one CHANNEL, or --all-channels');
  let channel = null;
  if (values['all-channels']) {
    if (positionals.length) throw new UsageError('give a CHANNEL or --all-channels, not both');
  } else {
    channel = requireChannel(io, positionals[0], 'inbox seen CHANNEL');
  }
  out(io, String(withStore(io, values, (store) => store.markAllSeen(channel))));
  return 0;
}

function runChannel(io, { positionals }) {
  if (positionals.length) throw new UsageError('channel takes no arguments');
  out(io, requireChannel(io, undefined, 'inbox send CHANNEL MESSAGE'));
  return 0;
}

function runPlugins(io, { positionals }) {
  if (positionals.length) throw new UsageError('plugins takes no arguments');
  for (const line of pluginList()) out(io, line);
  return 0;
}

const EMOJI_FORMATS = ['char', 'name', 'ref', 'json'];

// The channel a folder gives, as send infers it and `inbox channel` prints it
// for the Claude Code hook; outside the source root there is none.
function folderChannel(dir, root) {
  const channel = inferChannel(dir, root);
  if (channel === null) {
    throw new UsageError(`no channel: ${tildify(dir)} is not under ${tildify(root)}. Pass a CHANNEL instead`);
  }
  return channel;
}

function runEmoji(io, { values, positionals }) {
  if (positionals.length > 1) throw new UsageError('emoji takes at most one CHANNEL');
  if (positionals.length && values.folder !== undefined) {
    throw new UsageError('give a CHANNEL or --folder, not both');
  }
  const format = values.format ?? 'char';
  if (!EMOJI_FORMATS.includes(format)) {
    throw new UsageError(`bad format "${format}"; use one of: ${EMOJI_FORMATS.join(', ')}`);
  }
  const inputs = [];
  const root = sourceRoot(io.env);
  const repo = (channel) => componentInput(componentOf(splitSessionChannel(channel).folder));
  if (positionals.length) inputs.push(repo(validateChannel(positionals[0])));
  else if (values.folder !== undefined) inputs.push(repo(folderChannel(path.resolve(io.cwd(), values.folder), root)));
  else if (values.session === undefined) inputs.push(repo(folderChannel(io.cwd(), root)));
  if (values.session !== undefined) inputs.push(sessionInput(values.session));
  const emoji = inputs.map((input) => ({ input, ...emojiFor(input) }));
  if (format === 'json') out(io, JSON.stringify(emoji));
  else out(io, emoji.map((e) => (format === 'char' ? e.char : format === 'name' ? e.name : e.iconRef)).join(' '));
  return 0;
}

function runHere(io, { values, positionals }) {
  if (positionals.length > 2) throw new UsageError('here takes at most CHANNEL and HERE');
  if (values.clear) {
    if (positionals.length !== 1) {
      throw new UsageError('--clear takes one CHANNEL, as in: inbox here --clear chat');
    }
    const channel = validateChannel(positionals[0]);
    withStore(io, values, (store) => store.clearChannelHere(channel));
    return 0;
  }
  if (positionals.length === 0) {
    const here = detectHere(io.env);
    if (here === null) {
      io.stderr.write(
        `inbox: no "here" found for this shell; pass --here or set INBOX_HERE (iterm2:UUID, app:BUNDLE_ID, or url:URL)\n`,
      );
      return 1;
    }
    out(io, here);
    return 0;
  }
  const channel = validateChannel(positionals[0]);
  if (positionals.length === 2) {
    const here = validateHere(positionals[1], 'HERE');
    withStore(io, values, (store) => store.setChannelHere(channel, here));
    return 0;
  }
  const here = withStore(io, values, (store) => store.channelHere(channel));
  if (here === null) {
    io.stderr.write(`inbox: ${channel} has no fallback "here"\n`);
    return 1;
  }
  out(io, here);
  return 0;
}

function parseWhole(flag, text, low, high, hint) {
  if (!/^\d+$/.test(text) || Number(text) < low || Number(text) > high) {
    throw new UsageError(`${flag} ${text} is out of range; ${hint}`, { help: false });
  }
  return Number(text);
}

function parsePoll(text) {
  const match = /^(\d+(?:\.\d+)?)(ms|s)$/.exec(text);
  if (!match) {
    throw new UsageError(`--poll ${text} is not a duration; use a number and a unit, such as 500ms or 2s`, {
      help: false,
    });
  }
  const ms = Number(match[1]) * (match[2] === 's' ? 1000 : 1);
  if (ms < 10) throw new UsageError(`--poll ${text} is too short; use 10ms or more`, { help: false });
  return ms;
}

// --slot N (one plugin) or --slot NAME=N (once per plugin) -> { name: slot }.
function parseSlots(texts = [], plugins) {
  const names = plugins.map((p) => p.name);
  const slots = {};
  const owner = new Map();
  for (const text of texts) {
    const eq = text.indexOf('=');
    let name;
    let value = text;
    if (eq === -1) {
      if (plugins.length > 1) {
        throw new UsageError(
          `--slot ${text} names no plugin, and the daemon runs ${names.join(', ')}; use --slot NAME=N, such as --slot ${names[0]}=${text}`,
          { help: false },
        );
      }
      name = names[0];
    } else {
      name = text.slice(0, eq);
      value = text.slice(eq + 1);
      if (!names.includes(name)) {
        throw new UsageError(`--slot ${text}: the daemon does not run plugin "${name}"; it runs ${names.join(', ')}`, {
          help: false,
        });
      }
    }
    const slot = parseWhole('--slot', value, 0, 5, 'use 0 to 5');
    if (Object.hasOwn(slots, name)) throw new UsageError(`give --slot once for ${name}`, { help: false });
    if (owner.has(slot)) {
      throw new UsageError(`--slot ${slot} is given to both ${owner.get(slot)} and ${name}; give each its own slot`, {
        help: false,
      });
    }
    slots[name] = slot;
    owner.set(slot, name);
  }
  return slots;
}

function daemonSettings(io, values) {
  return {
    file: storeFile(io, values),
    port:
      values.port === undefined ? DEFAULT_PORT : parseWhole('--port', values.port, 1, 65535, 'use 1 to 65535'),
    slots: parseSlots(values.slot, io.plugins),
    pollMs: values.poll === undefined ? DEFAULT_POLL_MS : parsePoll(values.poll),
  };
}

// One process shows every plugin in io.plugins: one loop per plugin, each with
// its own store handle, socket, and pid file (task 0075).
async function runDaemon(io, { values, positionals }) {
  if (positionals.length) throw new UsageError('daemon takes no arguments');
  const { slots, ...settings } = daemonSettings(io, values);
  const log = (text) => io.stderr.write(`inbox: ${text}\n`);
  // With more than one plugin, a loop's lines say which plugin they are from.
  const logOf = (plugin) => (io.plugins.length > 1 ? (text) => log(`[${plugin.name}] ${text}`) : log);
  if (values['assets-dir'] !== undefined) {
    log('--assets-dir is unused: the daemon sends icon text, not picture files');
  }
  // Listen for signals before anything else, so one that arrives right after
  // the start line still ends in a clean stop.
  let onSignal = () => {};
  const signalled = new Promise((resolve) => {
    onSignal = resolve;
  });
  io.process.on('SIGINT', onSignal);
  io.process.on('SIGTERM', onSignal);
  const forget = () => {
    io.process.off('SIGINT', onSignal);
    io.process.off('SIGTERM', onSignal);
  };
  // Take every pid file before any loop starts, so a daemon that already runs
  // one of the plugins stops this one before it draws a key.
  const releases = [];
  const daemons = [];
  const releaseAll = () => {
    for (const release of releases) release();
  };
  try {
    for (const plugin of io.plugins) {
      releases.push(acquirePidFile(settings.file, { plugin: plugin.name, log }));
    }
    for (const plugin of io.plugins) {
      daemons.push(startDaemon({ ...settings, slot: slots[plugin.name], plugin, env: io.env, log: logOf(plugin) }));
    }
  } catch (e) {
    forget();
    await Promise.all(daemons.map((d) => d.stop()));
    releaseAll();
    throw e;
  }
  for (const plugin of io.plugins) {
    log(`${plugin.name} daemon started for ${settings.file} (pid ${process.pid})`);
  }
  await signalled;
  forget();
  await Promise.all(daemons.map((d) => d.stop()));
  releaseAll();
  log('daemon stopped');
  return 0;
}

const RUNNERS = {
  send: runSend,
  pipe: runPipe,
  list: runList,
  count: runCount,
  channels: runChannels,
  channel: runChannel,
  seen: runSeen,
  here: runHere,
  daemon: runDaemon,
  plugins: runPlugins,
  emoji: runEmoji,
};

function wantsHelp(argv) {
  const end = argv.indexOf('--');
  const flags = end === -1 ? argv : argv.slice(0, end);
  return flags.includes('-h') || flags.includes('--help');
}

async function dispatch(argv, io) {
  if (wantsHelp(argv)) {
    const command = argv.find((a) => COMMANDS.includes(a)) ?? 'inbox';
    out(io, helpText(command, io.plugin));
    return 0;
  }
  // Input is piped in and no command is named: read it as a pipe.
  if (!io.stdin.isTTY && (argv.length === 0 || argv[0].startsWith('-'))) {
    return await runCommand('pipe', argv, io);
  }
  if (argv.length === 0) {
    io.stderr.write(`${helpText('inbox', io.plugin)}\n`);
    return 2;
  }
  const [command, ...rest] = argv;
  if (command === 'help') {
    const topic = rest[0];
    if (topic === undefined) out(io, helpText('inbox', io.plugin));
    else if (COMMANDS.includes(topic)) out(io, helpText(topic, io.plugin));
    else throw unknownCommand(topic);
    return 0;
  }
  if (!COMMANDS.includes(command)) {
    if (command.startsWith('-')) {
      throw new UsageError(`unknown option "${command}". Run 'inbox -h' for the list.`, {
        help: false,
      });
    }
    throw unknownCommand(command);
  }
  return await runCommand(command, rest, io);
}

// --channel C is the same as the CHANNEL argument: for send it goes before the
// MESSAGE, for the others it is the only argument.
function mergeChannelOption(command, parsed) {
  const { values, positionals } = parsed;
  if (values.channel === undefined) return parsed;
  const channel = values.channel;
  const limit = command === 'send' ? 1 : 0;
  if (positionals.length > limit) {
    throw new UsageError('give the channel once, as --channel or as an argument');
  }
  return {
    values,
    positionals: command === 'send' && positionals.length === 0 ? [] : [channel, ...positionals],
  };
}

async function runCommand(command, args, io) {
  // pipe has its own rule: it goes on saving when standard output closes.
  if (command !== 'pipe') quietOnClosedStdout(io);
  try {
    return await RUNNERS[command](io, mergeChannelOption(command, parseOptions(command, args)));
  } catch (e) {
    if (e instanceof UsageError) e.command = command;
    throw e;
  }
}

function report(e, io) {
  if (e instanceof UsageError) {
    let text = `inbox: ${e.message}\n`;
    if (e.help && e.command) text += `run 'inbox ${e.command} -h' for help\n`;
    io.stderr.write(text);
    return 2;
  }
  if (e instanceof DaemonRunningError) {
    io.stderr.write(`inbox: ${e.message}\n`);
    return 3;
  }
  if (e instanceof StoreError) {
    io.stderr.write(`inbox: ${e.message}\n`);
    return 1;
  }
  let text = `inbox: internal error: ${e?.message ?? e}\n`;
  if (io.env.INBOX_DEBUG === '1' && e?.stack) text += `${e.stack}\n`;
  io.stderr.write(text);
  return 1;
}

// Takes --plugin NAME (or --plugin=NAME, or --plugin A,B) out of the
// arguments, wherever it is before "--". Returns the plugin names in order,
// without repeats, and the other arguments.
function takePlugins(argv) {
  const rest = [];
  const names = [];
  for (let i = 0; i < argv.length; i++) {
    const arg = argv[i];
    if (arg === '--') {
      rest.push(...argv.slice(i));
      break;
    }
    let value;
    if (arg === '--plugin') {
      value = argv[i + 1];
      if (value === undefined || value.startsWith('-')) {
        throw new UsageError(`--plugin needs a NAME; use one of: ${Object.keys(PLUGINS).join(', ')}`, {
          help: false,
        });
      }
      i++;
    } else if (arg.startsWith('--plugin=')) {
      value = arg.slice('--plugin='.length);
    } else {
      rest.push(arg);
      continue;
    }
    for (const name of value.split(',')) {
      if (!names.includes(name)) names.push(name);
    }
  }
  return { names, argv: rest };
}

// The daemon runs every plugin it is given, and every plugin when given none.
// Every other command runs one plugin, the default when given none.
function choosePlugins({ names, argv }) {
  const plugins = names.map((name) => getPlugin(name));
  if (argv[0] === 'daemon') return plugins.length ? plugins : Object.values(PLUGINS);
  if (plugins.length > 1) {
    throw new UsageError(`give --plugin once; got ${names[0]} and ${names[1]}`, { help: false });
  }
  return plugins.length ? plugins : [getPlugin(DEFAULT_PLUGIN)];
}

// The runners read the plugin from `io`; Object.create keeps the getters of
// the real io.
export async function main(argv, io) {
  const ctx = Object.create(io);
  try {
    const taken = takePlugins(argv);
    ctx.plugins = choosePlugins(taken);
    ctx.plugin = ctx.plugins[0];
    return await dispatch(taken.argv, ctx);
  } catch (e) {
    return report(e, ctx);
  }
}
