// The ping plugin (task 0098): one top key, `ping`, and one child, `Ping`.
// A press on the child runs the saved shortcut named by $INBOX_PING_SHORTCUT
// (default `Ping`) with `shortcuts run`. That shortcut holds the Ping action
// of macro-spotlight-intents, so a press on the pad reaches the intent through
// the system's own path from Shortcuts to the app.
//
// The store holds one channel, `ping.run`. Its newest message is the state of
// the key: running, done, or failed.

import { execFile as nodeExecFile } from 'node:child_process';
import { run } from '../apps.js';
import { setLayerKeyMessage } from '../protocol.js';
import { ACTIVE_MS } from './apps.js';

export const PING_STATUSES = ['running', 'done', 'failed'];
export const TOP_ID = 'ping';
export const CHILD_ID = 'run';
export const CHANNEL = 'ping.run';
export const DEFAULT_SHORTCUT = 'Ping';
export const MAX_NAME = 128;
export const SHORTCUT_TIMEOUT_MS = 10 * 1000;

// The pad status of each state. Done shows on the child only.
const PAD = { running: 'RUNNING', done: 'DONE', failed: 'ERROR' };
// A table tennis paddle and ball, under macropadd's assets directory.
const EMOJI = 'emoji/1f3d3.png';

// The name of the shortcut to run, or null when it is not safe to pass on:
// `shortcuts run` would read a name that starts with a dash as an option.
export function shortcutName(env = process.env) {
  const name = env.INBOX_PING_SHORTCUT || DEFAULT_SHORTCUT;
  if (!/^[A-Za-z0-9]/.test(name) || name.length > MAX_NAME || /[\r\n]/.test(name)) return null;
  return name;
}

export function createPingRenderer({ slot } = {}) {
  const sent = new Map();

  const set = (out, key, fields) => {
    const json = JSON.stringify(fields);
    if (sent.get(key) === json) return;
    sent.set(key, json);
    out.push(setLayerKeyMessage(fields));
  };

  return {
    topId: TOP_ID,

    owns: (parent) => parent === TOP_ID,

    reset: () => sent.clear(),

    target: () => null,

    plan({ unseen: rows }) {
      const pad = PAD[rows.find((row) => row.channel === CHANNEL)?.status] ?? '';
      const out = [];
      const top = { id: TOP_ID, status: pad === 'DONE' ? '' : pad, count: 0, iconText: 'Ping', iconEmoji: [EMOJI] };
      if (slot !== undefined) top.slot = slot;
      set(out, TOP_ID, top);
      set(out, `${TOP_ID}/${CHILD_ID}`, { id: CHILD_ID, parent: TOP_ID, status: pad, count: 0, iconText: 'Ping', iconEmoji: [EMOJI] });
      return out;
    },
  };
}

// One run at a time: a second press while the shortcut runs does nothing.
let running = false;

// Runs the action of a pressed child. deps holds execFile, platform and
// timeoutMs, for a test.
export async function press({ parent, child, store, env = process.env, changed = () => {}, log = () => {}, deps = {} }) {
  if (parent !== TOP_ID || child !== CHILD_ID) {
    log(`no action ${child} on ${parent}`);
    return;
  }
  const { execFile = nodeExecFile, platform = process.platform, timeoutMs = SHORTCUT_TIMEOUT_MS } = deps;
  const cwd = process.cwd();
  const name = shortcutName(env);
  if (name === null) {
    log(`refused INBOX_PING_SHORTCUT: it must start with a letter or digit, and be at most ${MAX_NAME} characters on one line`);
    store.insert(CHANNEL, 'refused the shortcut name', cwd, { status: 'failed' });
    return;
  }
  if (running) return;
  running = true;
  try {
    store.insert(CHANNEL, `shortcuts run ${name}`, cwd, { status: 'running' });
    changed();
    try {
      if (platform !== 'darwin') throw new Error('this key works on macOS only');
      await run(execFile, 'shortcuts', ['run', name], { timeout: timeoutMs });
      store.insert(CHANNEL, `ran ${name}`, cwd, { status: 'done' });
    } catch (e) {
      const code = e.code === undefined ? '' : ` (exit code ${e.code})`;
      log(`shortcuts run ${name} failed${code}: ${e.message}`);
      store.insert(CHANNEL, e.message, cwd, { status: 'failed' });
    }
  } finally {
    running = false;
  }
}

export default {
  name: 'ping',
  summary: 'a key that runs the Ping intent through the Shortcuts app',
  statuses: PING_STATUSES,
  defaultStatus: 'done',
  about: `The ping key opens one child, Ping. A press runs 'shortcuts run NAME', where NAME
is $INBOX_PING_SHORTCUT (default ${DEFAULT_SHORTCUT}), a saved shortcut that holds the Ping action of
macro-spotlight-intents. The child shows RUNNING, then DONE or ERROR. macOS only.`,
  rules: {
    id: TOP_ID,
    activeMs: ACTIVE_MS,
  },
  createRenderer: ({ slot }) => createPingRenderer({ slot }),
  press,
};
