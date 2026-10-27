// The apps plugin (task 0085): a top key for each app Keith works on today,
// among the repositories in one root folder ($INBOX_APPS_ROOT, such as
// ~/source/github.com/acme). A press on an app key opens five children:
// Code, Claude, Run, Pull, Term.
//
// The feed scans the root every 30 s. An app's activity is the newest of its
// .git/logs/HEAD and .git/index mtimes and of an ai-plugin session today in
// its folder. The keys show the pinned apps, then the apps active today,
// newest first, at most $INBOX_APPS_COUNT. The store holds pins (NAME.pin),
// runs (NAME.run) and pulls (NAME.pull); the newest message of each is its
// state.

import { readdirSync, statSync } from 'node:fs';
import os from 'node:os';
import path from 'node:path';
import { detectRun, newTab, openCode, parseRunOverrides, pull, runLine, shellWord, typeInTab } from '../apps.js';
import { inferChannel, sourceRoot } from '../channel.js';
import { componentInput, componentOf, emojiFor, splitSessionChannel } from '../emoji.js';
import { focus as focusHere } from '../here.js';
import { removeLayerKeyMessage, setLayerKeyMessage } from '../protocol.js';
import { MAX_CAPTION, clip } from '../render.js';

export const APPS_STATUSES = ['pinned', 'unpinned', 'running', 'stopped', 'crashed', 'pulling', 'pulled', 'failed'];
export const DEFAULT_COUNT = 3;
export const MAX_COUNT = 5;
export const FEED_EVERY_MS = 30 * 1000;
// Pins and run state are kept for good; the feed decides what is active.
export const ACTIVE_MS = 10 * 365 * 24 * 60 * 60 * 1000;

// The children of an app key, in order: [action, caption].
export const ACTIONS = [
  ['code', 'Code'],
  ['claude', 'Claude'],
  ['run', 'Run'],
  ['pull', 'Pull'],
  ['term', 'Term'],
];

const RUN_PAD = { running: 'RUNNING', crashed: 'ERROR' };
const PULL_PAD = { pulling: 'RUNNING', pulled: 'DONE', failed: 'ERROR' };

// The channel name of an app folder: a dot becomes -, as in the github
// plugin. null when the name holds anything else a channel cannot.
export function appName(dirName) {
  const name = dirName.replace(/\./g, '-');
  return /^[A-Za-z0-9_-]+$/.test(name) ? name : null;
}

export const topId = (name) => `app.${name}`;

function expandHome(p) {
  return p === '~' || p.startsWith('~/') ? path.join(os.homedir(), p.slice(1)) : p;
}

// $INBOX_APPS_ROOT as an absolute path, or null.
export function appsRoot(env = process.env) {
  return env.INBOX_APPS_ROOT ? path.resolve(expandHome(env.INBOX_APPS_ROOT)) : null;
}

export function appsCount(env = process.env) {
  const n = Number(env.INBOX_APPS_COUNT);
  return Number.isInteger(n) && n >= 1 && n <= MAX_COUNT ? n : DEFAULT_COUNT;
}

export function startOfDay(now = Date.now()) {
  const d = new Date(now);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
}

function mtime(file) {
  try {
    return statSync(file).mtimeMs;
  } catch {
    return 0;
  }
}

// The apps of the root: [{ name, dirName, dir, activity }], by name. An app
// is a folder that holds a .git folder.
export function listApps(root) {
  const apps = [];
  for (const entry of readdirSync(root, { withFileTypes: true })) {
    if (!entry.isDirectory() && !entry.isSymbolicLink()) continue;
    const name = appName(entry.name);
    const dir = path.join(root, entry.name);
    if (name === null) continue;
    try {
      if (!statSync(path.join(dir, '.git')).isDirectory()) continue;
    } catch {
      continue;
    }
    const git = path.join(dir, '.git');
    apps.push({ name, dirName: entry.name, dir, activity: Math.max(mtime(path.join(git, 'logs', 'HEAD')), mtime(path.join(git, 'index'))) });
  }
  return apps.sort((a, b) => a.name.localeCompare(b.name));
}

// The channel prefix of a folder under the root, as the ai hook names it
// (task 0072): ~/source/github.com/acme -> github.com.acme. null when the
// root is not under the source root.
function rootChannel(root, env) {
  try {
    return inferChannel(root, sourceRoot(env));
  } catch {
    return null;
  }
}

// A matcher (aiRow) -> the app it is about, or undefined. A session channel
// is FOLDER.SID, and FOLDER is the root's channel, the app's folder, and
// maybe a subfolder. Outside the source root, the repository name of task
// 0081 has to do.
export function sessionMatcher(apps, root, env = process.env) {
  const prefix = rootChannel(root, env);
  return (row) => {
    const { folder } = splitSessionChannel(row.channel);
    if (prefix === null) {
      const repo = componentOf(folder);
      return apps.find((app) => app.name.toLowerCase() === repo || app.dirName.toLowerCase() === repo);
    }
    if (!folder.startsWith(`${prefix}.`)) return undefined;
    const rest = folder.slice(prefix.length + 1);
    return apps.find((app) => rest === app.dirName || rest.startsWith(`${app.dirName}.`));
  };
}

// The newest ai session today of each app: Map name -> row.
export function sessionsToday(store, apps, root, { env = process.env, now = Date.now() } = {}) {
  const match = sessionMatcher(apps, root, env);
  const newest = new Map();
  for (const row of store.forPlugin('ai').recent(startOfDay(now))) {
    const app = match(row);
    if (!app) continue;
    const had = newest.get(app.name);
    if (!had || row.createdAt > had.createdAt || (row.createdAt === had.createdAt && row.newestId > had.newestId)) {
      newest.set(app.name, row);
    }
  }
  return newest;
}

// The pinned apps in the rows of this plugin, oldest pin first.
export function pinsOf(rows) {
  return rows
    .filter((row) => row.channel.endsWith('.pin') && row.status === 'pinned')
    .sort((a, b) => a.createdAt - b.createdAt || a.newestId - b.newestId)
    .map((row) => row.channel.slice(0, -'.pin'.length));
}

// The names of the apps that show: pins first, then the apps active since
// `since`, newest first; at most count.
export function chooseApps({ apps, since }, rows, count) {
  const byName = new Map(apps.map((app) => [app.name, app]));
  const chosen = pinsOf(rows).filter((name) => byName.has(name));
  const active = apps
    .filter((app) => app.activity >= since && !chosen.includes(app.name))
    .sort((a, b) => b.activity - a.activity || a.name.localeCompare(b.name));
  for (const app of active) chosen.push(app.name);
  return chosen.slice(0, count);
}

// The feed's answer: { root, since, apps: [{ name, dirName, dir, activity,
// run }] }, where run is the label of the app's run command or null, for
// the apps that may show (active today or pinned).
export function scan({ env = process.env, store, now = Date.now() }) {
  const root = appsRoot(env);
  if (root === null) throw new Error('no INBOX_APPS_ROOT; run install.sh --apps-root DIR');
  let apps;
  try {
    apps = listApps(root);
  } catch (e) {
    throw new Error(`cannot read ${root}: ${e.message}`);
  }
  const since = startOfDay(now);
  if (store) {
    for (const [name, row] of sessionsToday(store, apps, root, { env, now })) {
      const app = apps.find((a) => a.name === name);
      app.activity = Math.max(app.activity, row.createdAt);
    }
  }
  const pins = new Set(store ? pinsOf(store.recent(now - ACTIVE_MS)) : []);
  const overrides = parseRunOverrides(env.INBOX_APPS_RUN);
  for (const app of apps) {
    if (app.activity < since && !pins.has(app.name)) continue;
    app.run = detectRun(app.dir, overrides.get(app.name) ?? overrides.get(app.dirName))?.label ?? null;
  }
  return { root, since, apps };
}

// The renderer of the apps plugin: a top key per app, five children each.
// Unlike render.js it draws several top keys, so it says which keys are its
// own (owns). A key keeps its slot while it shows; one that leaves frees its
// slot for the next. The key of the open layer stays until the layer closes.
export function createAppsRenderer({ env = process.env, emojiSet } = {}) {
  const sent = new Map();
  let shown = [];
  let known = false;
  let leftovers = [];
  const emojiOf = (name) => emojiFor(componentInput(name), emojiSet).iconRef;

  const set = (out, key, fields) => {
    const json = JSON.stringify(fields);
    if (sent.get(key) === json) return;
    sent.set(key, json);
    out.push(setLayerKeyMessage(fields));
  };

  const remove = (out, name) => {
    const id = topId(name);
    for (const key of [...sent.keys()]) if (key === id || key.startsWith(`${id}.`)) sent.delete(key);
    out.push(removeLayerKeyMessage(id));
  };

  const draw = (out, name, app, rows) => {
    const id = topId(name);
    const emoji = [emojiOf(name)];
    const run = RUN_PAD[rows.get(`${name}.run`)?.status] ?? '';
    const pulled = PULL_PAD[rows.get(`${name}.pull`)?.status] ?? '';
    const both = [run, pulled];
    const status = both.includes('ERROR') ? 'ERROR' : both.includes('RUNNING') ? 'RUNNING' : '';
    set(out, id, { id, status, count: 0, iconText: clip(name, MAX_CAPTION), iconEmoji: emoji });
    for (const [action, caption] of ACTIONS) {
      const child = { id: `${id}.${action}`, parent: id, status: '', count: 0, iconText: caption, iconEmoji: emoji };
      if (action === 'run') {
        child.status = run;
        if (app?.run !== undefined) child.iconText = clip(app.run ?? 'no run', MAX_CAPTION);
      } else if (action === 'pull') {
        child.status = pulled;
      }
      set(out, child.id, child);
    }
  };

  return {
    topId: null,

    owns: (parent) => shown.some((name) => topId(name) === parent),

    reset(channels = []) {
      sent.clear();
      leftovers = [...new Set([...shown, ...channels.map((c) => c.split('.')[0])])];
      shown = [];
      known = false;
    },

    target: () => null,

    plan({ unseen: rows, active, fed }) {
      // The apps are not known before the first scan.
      if (!fed) return [];
      const out = [];
      const byChannel = new Map(rows.map((row) => [row.channel, row]));
      const byName = new Map(fed.apps.map((app) => [app.name, app]));
      const wanted = chooseApps(fed, rows, appsCount(env));
      if (!known) {
        // An earlier daemon may have left app keys: remove every app key
        // that should not show.
        for (const name of new Set([...leftovers, ...byName.keys()])) {
          if (!wanted.includes(name)) remove(out, name);
        }
        leftovers = [];
        known = true;
      }
      const open = shown.find((name) => topId(name) === active);
      for (const name of shown) {
        if (!wanted.includes(name) && name !== open) remove(out, name);
      }
      const next = shown.filter((name) => wanted.includes(name) || name === open);
      for (const name of wanted) if (!next.includes(name)) next.push(name);
      shown = next;
      for (const name of shown) draw(out, name, byName.get(name), byChannel);
      return out;
    },
  };
}

// The newest message of one channel, or undefined.
const newest = (store, channel) => store.list({ channel, includeSeen: true, limit: 1 })[0];

// Brings a kept tab to the front; false when there is none, or it is gone.
async function focusKept(store, channel, focus) {
  const here = store.channelHere(channel);
  if (here === null) return false;
  try {
    await focus(here);
    return true;
  } catch {
    return false;
  }
}

async function keepNewTab(store, channel, line, deps) {
  store.setChannelHere(channel, await newTab(line, deps));
}

// The folders a pull runs in now: one pull per app at a time.
const pulling = new Set();

// Runs the action of a pressed child. deps holds execFile, platform, node
// and bin, for a test.
export async function press({ parent, child, store, file, env = process.env, fed, focus = focusHere, changed = () => {}, log = () => {}, now = Date.now(), deps = {} }) {
  const name = parent.slice('app.'.length);
  const app = fed?.apps.find((a) => a.name === name);
  if (!app) {
    log(`no app ${name} under ${fed?.root ?? 'INBOX_APPS_ROOT'}`);
    return;
  }
  const action = child.slice(parent.length + 1);
  const { dir } = app;
  const cd = `cd ${shellWord(dir)}`;
  const focusWith = (here) => focus(here, deps);

  if (action === 'code') {
    await openCode(dir, deps);
  } else if (action === 'claude') {
    // The newest session today, else the tab this key opened, else a new one.
    const session = sessionsToday(store, fed.apps, fed.root, { env, now }).get(name);
    if (session?.here?.startsWith('iterm2:')) {
      try {
        await focusWith(session.here);
        return;
      } catch {
        // Its tab is closed.
      }
    }
    if (await focusKept(store, `${name}.claude`, focusWith)) return;
    await keepNewTab(store, `${name}.claude`, `${cd} && claude`, deps);
  } else if (action === 'run') {
    const overrides = parseRunOverrides(env.INBOX_APPS_RUN);
    const run = detectRun(dir, overrides.get(name) ?? overrides.get(app.dirName));
    if (run === null) {
      log(`no run command for ${name}: no Makefile target dev, run, start or serve, no package.json script dev or start, and no INBOX_APPS_RUN entry`);
      return;
    }
    const channel = `${name}.run`;
    if (newest(store, channel)?.status === 'running' && (await focusKept(store, channel, focusWith))) return;
    const line = runLine({ dir, name, argv: run.argv, file, node: deps.node, bin: deps.bin });
    const kept = store.channelHere(channel);
    if (kept !== null) {
      try {
        await typeInTab(kept, line, deps);
        return;
      } catch {
        // Its tab is closed.
      }
    }
    await keepNewTab(store, channel, line, deps);
  } else if (action === 'pull') {
    const channel = `${name}.pull`;
    if (pulling.has(dir)) return;
    pulling.add(dir);
    try {
      store.insert(channel, 'pulling', dir, { status: 'pulling' });
      changed();
      const result = await pull(dir, { env, execFile: deps.execFile });
      store.insert(channel, result.body, dir, { status: result.ok ? 'pulled' : 'failed' });
      if (!result.ok) log(`pull of ${name} failed: ${result.body}`);
    } finally {
      pulling.delete(dir);
    }
  } else if (action === 'term') {
    if (await focusKept(store, `${name}.term`, focusWith)) return;
    await keepNewTab(store, `${name}.term`, cd, deps);
  }
}

export default {
  name: 'apps',
  summary: 'a key per app you work on today: code, Claude, run, pull, terminal',
  statuses: APPS_STATUSES,
  defaultStatus: 'stopped',
  about: `A top key per app in $INBOX_APPS_ROOT (install.sh --apps-root DIR) that
you worked on today, or pinned, at most $INBOX_APPS_COUNT (default ${DEFAULT_COUNT}).
Its five children open VS Code, focus or open a Claude session, run the
app in its own iTerm tab, git pull --rebase, and open a terminal.
Pin an app with 'inbox --plugin apps pin NAME'.`,
  rules: {
    id: 'apps',
    activeMs: ACTIVE_MS,
  },
  createRenderer: ({ env }) => createAppsRenderer({ env }),
  press,
  feed: {
    source: 'apps',
    everyMs: FEED_EVERY_MS,
    fetch: ({ env, store }) => scan({ env, store }),
    // The feed writes nothing; the renderer reads its answer.
    sync: () => 0,
  },
};
