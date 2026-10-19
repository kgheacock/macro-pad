// Subcommands, help text, and error messages. main() takes an io object so
// a test can run the CLI in process; bin/inbox.js passes the real streams.

import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parseArgs } from 'node:util';
import { inferChannel, sourceRoot, tildify, validateChannel } from './channel.js';
import { DEFAULT_POLL_MS, DEFAULT_PORT, acquirePidFile, startDaemon } from './daemon.js';
import { DaemonRunningError, StoreError, UsageError } from './errors.js';
import { detectHere, resolveHere, validateHere } from './here.js';
import { DEFAULT_STATUS, defaultDbPath, openStore } from './store.js';

const MAX_LINE_BYTES = 64 * 1024;
const FLUSH_MS = 200;
const FLUSH_LINES = 500;

const CHANNEL_RULES = `How the channel is chosen:
  A CHANNEL argument always wins. Without one, a directory under the source
  root (INBOX_SOURCE_ROOT, default ~/source) gives its channel: the path
  below the root, joined by dots, so ~/source/macro-pad/driver is
  macro-pad.driver. Outside the root, pass the channel.`;

const DB_OPTION = `  --db PATH   use this SQLite file instead of $INBOX_DB`;
const STATUS_OPTION = `  --status S  the status of the message: letters, digits, - and _ (default: ${DEFAULT_STATUS})`;
const HERE_OPTION = `  --here H    where a press on the channel key goes: iterm2:UUID, app:BUNDLE_ID, or
              url:URL (default: $INBOX_HERE, else the iTerm session you are in)`;

// macropadd run from the repo root reads its pictures from here.
const REPO_ASSETS = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', '..', 'assets');

const HELP = {
  inbox: `inbox - leave short messages per channel, and read them later

Usage: inbox COMMAND [ARGUMENTS] [--db PATH]

Commands:
  send       send one message
  pipe       send each line of standard input as a message
  list       list messages, seen and unseen
  count      print how many messages are not seen yet
  channels   list channels that have unseen messages
  seen       mark messages as seen
  here       show or set where a press on a channel key goes
  daemon     watch the inbox and show it on the macro pad
  help       show help for a command

Examples:
  inbox send debug "build done"
  my_cmd 2>&1 | inbox pipe debug

Run 'inbox COMMAND -h' for the details of one command.
Exit codes: 0 success, 1 store error, 2 usage error, 3 a daemon already runs.`,

  send: `Usage: inbox send [--status STATUS] [--here HERE] [CHANNEL] MESSAGE

Send one message.

  CHANNEL   where the message goes, such as "debug" or "macro-pad.driver"
  MESSAGE   the text; quote it when it has spaces
${STATUS_OPTION}
${HERE_OPTION}
${DB_OPTION}

${CHANNEL_RULES}

Examples:
  inbox send debug "build done"
  inbox send --status error debug "build failed"
  inbox send "tests passed"        (in ~/source/macro-pad: channel macro-pad)`,

  pipe: `Usage: inbox pipe [--status STATUS] [--here HERE] [CHANNEL]

Read standard input and send each line as one message. Empty lines are
skipped. A line longer than 64 KiB is cut. Messages are saved while the pipe
is still open, within 200 ms. SIGINT and SIGTERM save what was read, then
exit 0.

  CHANNEL   where the messages go
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

  list: `Usage: inbox list [CHANNEL | --all-channels] [--unseen] [--json] [-n N]

List messages, oldest first. A leading "*" marks the ones not seen yet.
Each line is: time, status, then the channel (with --all-channels), then the
message.

  CHANNEL         list this channel
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

  count: `Usage: inbox count [CHANNEL | --all-channels]

Print how many messages are not seen yet.

  CHANNEL         count this channel
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

  seen: `Usage: inbox seen [CHANNEL | --all-channels]

Mark the unseen messages seen. Print how many changed. A message that lands
after the command reads the inbox stays unseen.

  CHANNEL         mark this channel
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

  daemon: `Usage: inbox daemon [--port N] [--assets-dir DIR] [--slot N] [--poll DURATION]

Run until stopped. Watch the store, and send the unseen counts to macropadd
over its WebSocket. It starts with or without macropadd, and sends the full
state again each time it connects. Log lines go to standard error.

  --port N          macropadd's port on 127.0.0.1 (default: ${DEFAULT_PORT})
  --assets-dir DIR  the directory macropadd uses for pictures; an absolute
                    path (default: ${REPO_ASSETS})
  --slot N          put the Inbox key in slot N, 0 to 5 (default: the first free slot)
  --poll DURATION   how often to look for a change, such as 500ms or 2s
                    (default: ${DEFAULT_POLL_MS}ms)
${DB_OPTION}

Only one daemon runs per store. A second one exits with code 3.
SIGINT and SIGTERM stop it with exit code 0. The keys stay on the pad.
To run it at login, use inbox/scripts/install.sh.

Example:
  inbox daemon --assets-dir ~/source/macro-pad/assets`,
};

const COMMANDS = ['send', 'pipe', 'list', 'count', 'channels', 'seen', 'here', 'daemon'];

const SHARED_OPTIONS = { db: { type: 'string' } };
const OPTIONS = {
  send: { status: { type: 'string' }, here: { type: 'string' } },
  pipe: { status: { type: 'string' }, here: { type: 'string' } },
  list: {
    'all-channels': { type: 'boolean' },
    unseen: { type: 'boolean' },
    json: { type: 'boolean' },
    n: { type: 'string', short: 'n' },
  },
  count: { 'all-channels': { type: 'boolean' } },
  channels: { json: { type: 'boolean' } },
  seen: { 'all-channels': { type: 'boolean' } },
  here: { clear: { type: 'boolean' } },
  daemon: {
    port: { type: 'string' },
    'assets-dir': { type: 'string' },
    slot: { type: 'string' },
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
      options: { ...SHARED_OPTIONS, ...OPTIONS[command] },
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

function parseStatus(status) {
  if (status === undefined) return DEFAULT_STATUS;
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

function withStore(io, values, fn) {
  const file = values.db ?? defaultDbPath(io.env);
  const store = openStore(file, io.env);
  try {
    return fn(store);
  } finally {
    store.close();
  }
}

function out(io, text) {
  io.stdout.write(`${text}\n`);
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
  const status = parseStatus(values.status);
  const here = resolveHere({ flag: values.here, env: io.env });
  withStore(io, values, (store) => store.insert(channel, message, io.cwd(), { status, here }));
  return 0;
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
    // A signal saves the lines already read and drops a half-read last line.
    const onSignal = guarded(() => {
      flush();
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
  const status = parseStatus(values.status);
  // Like the channel, the "here" is fixed before the first line is read.
  const here = resolveHere({ flag: values.here, env: io.env });
  if (io.stdin.isTTY) {
    throw new UsageError(
      'pipe reads lines from a pipe or file, as in: my_cmd 2>&1 | inbox pipe debug',
    );
  }
  const store = openStore(values.db ?? defaultDbPath(io.env), io.env);
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
        'inbox: no "here" found for this shell; pass --here or set INBOX_HERE (iterm2:UUID, app:BUNDLE_ID, or url:URL)\n',
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

function daemonSettings(io, values) {
  const assetsDir = values['assets-dir'] ?? REPO_ASSETS;
  if (!path.isAbsolute(assetsDir)) {
    throw new UsageError(
      `assets dir "${assetsDir}" is relative; pass an absolute path, as macropadd sees it`,
      { help: false },
    );
  }
  return {
    file: values.db ?? defaultDbPath(io.env),
    port:
      values.port === undefined ? DEFAULT_PORT : parseWhole('--port', values.port, 1, 65535, 'use 1 to 65535'),
    slot: values.slot === undefined ? undefined : parseWhole('--slot', values.slot, 0, 5, 'use 0 to 5'),
    pollMs: values.poll === undefined ? DEFAULT_POLL_MS : parsePoll(values.poll),
    assetsDir,
  };
}

async function runDaemon(io, { values, positionals }) {
  if (positionals.length) throw new UsageError('daemon takes no arguments');
  const settings = daemonSettings(io, values);
  const log = (text) => io.stderr.write(`inbox: ${text}\n`);
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
  let release = () => {};
  let daemon;
  try {
    release = acquirePidFile(settings.file, { log });
    daemon = startDaemon({ ...settings, env: io.env, log });
  } catch (e) {
    forget();
    release();
    throw e;
  }
  log(`daemon started for ${settings.file} (pid ${process.pid})`);
  await signalled;
  forget();
  await daemon.stop();
  release();
  log('daemon stopped');
  return 0;
}

const RUNNERS = {
  send: runSend,
  pipe: runPipe,
  list: runList,
  count: runCount,
  channels: runChannels,
  seen: runSeen,
  here: runHere,
  daemon: runDaemon,
};

function wantsHelp(argv) {
  const end = argv.indexOf('--');
  const flags = end === -1 ? argv : argv.slice(0, end);
  return flags.includes('-h') || flags.includes('--help');
}

async function dispatch(argv, io) {
  if (wantsHelp(argv)) {
    const command = argv.find((a) => COMMANDS.includes(a)) ?? 'inbox';
    out(io, HELP[command]);
    return 0;
  }
  if (argv.length === 0) {
    io.stderr.write(`${HELP.inbox}\n`);
    return 2;
  }
  const [command, ...rest] = argv;
  if (command === 'help') {
    const topic = rest[0];
    if (topic === undefined) out(io, HELP.inbox);
    else if (COMMANDS.includes(topic)) out(io, HELP[topic]);
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
  try {
    return await RUNNERS[command](io, parseOptions(command, rest));
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

export async function main(argv, io) {
  try {
    return await dispatch(argv, io);
  } catch (e) {
    return report(e, io);
  }
}
