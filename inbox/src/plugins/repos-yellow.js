// The criteria provider for the yellow items of a repository (task 0093).
//
// The person points the inbox at one program, $INBOX_REPOS_YELLOW_CMD. For
// each repository the feed runs it with the repository name as argv[1] (and
// as $INBOX_REPO), and it prints a JSON array on stdout:
//
//   [{ "id": "PROJ-12", "title": "Fix the login", "url": "https://..." }]
//
// The yellow count of the repository is the length of that list. A slow or
// broken program never blanks a count: a failure keeps the last good items.
// The program reads its own token (Keychain, a mode 600 file); this file
// never sees one.

import { execFile as nodeExecFile } from 'node:child_process';
import { reposOwner } from '../repolist.js';

export const YELLOW_EVERY_MS = 30 * 1000;
// Per-repository interval in seconds: default, and lowest allowed.
export const DEFAULT_EVERY_S = 300;
export const MIN_EVERY_S = 30;
export const DEFAULT_TIMEOUT_S = 20;
export const MAX_OUTPUT = 1024 * 1024;
export const MAX_ITEMS = 200;
export const MAX_TITLE = 200;
export const CONCURRENCY = 4;
const MAX_ERROR = 120;
// Added to the timeout before the feed stops waiting for a program whose
// child still holds the output pipe open after the kill.
const GRACE_MS = 500;

const ID = /^[A-Za-z0-9][A-Za-z0-9._-]{0,39}$/;

const positive = (text) => {
  const n = Number(text);
  return text !== undefined && text !== '' && Number.isFinite(n) && n > 0 ? n : null;
};

// How long a repository waits between runs, in ms: $INBOX_REPOS_YELLOW_EVERY
// seconds, default 300, at least 30.
export function yellowEveryMs(env = process.env) {
  return Math.max(MIN_EVERY_S, positive(env.INBOX_REPOS_YELLOW_EVERY) ?? DEFAULT_EVERY_S) * 1000;
}

// The time limit of one run, in ms: $INBOX_REPOS_YELLOW_TIMEOUT seconds,
// default 20.
export function yellowTimeoutMs(env = process.env) {
  return (positive(env.INBOX_REPOS_YELLOW_TIMEOUT) ?? DEFAULT_TIMEOUT_S) * 1000;
}

// The items of the text a provider printed: [{ id, title, url }]. Throws an
// Error when the text is not a JSON array. It drops an item with a bad id or
// an url that is not https://, drops a repeated id, keeps MAX_ITEMS, and cuts
// a title to MAX_TITLE characters.
export function parseItems(text) {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error('the output is not JSON');
  }
  if (!Array.isArray(json)) throw new Error('the output is not a JSON array');
  const items = [];
  const seen = new Set();
  for (const row of json) {
    if (items.length >= MAX_ITEMS) break;
    if (row === null || typeof row !== 'object') continue;
    const { id, title, url } = row;
    if (typeof id !== 'string' || !ID.test(id) || seen.has(id)) continue;
    if (typeof url !== 'string' || !url.startsWith('https://')) continue;
    try {
      if (new URL(url).protocol !== 'https:') continue;
    } catch {
      continue;
    }
    seen.add(id);
    const text = typeof title === 'string' ? title.replace(/\s+/g, ' ').trim() : '';
    items.push({ id, title: Array.from(text || id).slice(0, MAX_TITLE).join(''), url });
  }
  return items;
}

// Runs the provider for one repository. Resolves its stdout; rejects with an
// Error that says why. The stored error is the first line of stderr, cut to
// MAX_ERROR characters, so a program that prints a token there leaks at most
// that line (see the README). execFile is node's, or a stub in a test.
export function runYellow(cmd, repo, { env, execFile = nodeExecFile, timeoutMs = DEFAULT_TIMEOUT_S * 1000 }) {
  return new Promise((resolve, reject) => {
    let settled = false;
    const done = (fn, value) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      fn(value);
    };
    const timeout = () => new Error(`${cmd} did not answer within ${timeoutMs / 1000} s`);
    // execFile kills the program, but a child of a shell script may keep the
    // pipe open; stop waiting then.
    const timer = setTimeout(() => done(reject, timeout()), timeoutMs + GRACE_MS);
    try {
      execFile(
        cmd,
        [repo],
        { env: { ...env, INBOX_REPO: repo }, timeout: timeoutMs, killSignal: 'SIGKILL', maxBuffer: MAX_OUTPUT, shell: false },
        (error, stdout, stderr) => {
          if (!error) return done(resolve, String(stdout));
          if (error.code === 'ENOENT') return done(reject, new Error(`no ${cmd}; check INBOX_REPOS_YELLOW_CMD`));
          if (error.killed || error.signal === 'SIGTERM' || error.signal === 'SIGKILL') return done(reject, timeout());
          if (error.code === 'ERR_CHILD_PROCESS_STDIO_MAXBUFFER') {
            return done(reject, new Error(`${cmd} printed more than ${MAX_OUTPUT} bytes`));
          }
          const line = String(stderr ?? '').trim().split('\n')[0] || error.message;
          return done(reject, new Error(`${cmd} failed: ${Array.from(line).slice(0, MAX_ERROR).join('')}`));
        },
      );
    } catch (e) {
      done(reject, new Error(`cannot run ${cmd}: ${e.message}`));
    }
  });
}

// Runs fn over items, `limit` at a time.
async function pool(items, limit, fn) {
  let next = 0;
  const worker = async () => {
    while (next < items.length) await fn(items[next++]);
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
}

// A feed in the shape of repolist.js's: { source, everyMs, fetch, sync }. It
// holds the errors it logged, so each distinct one is logged once; make one
// per daemon (yellowFeed) or per test.
export function createYellowFeed() {
  // repo -> the error of its last run, for the repositories that now fail.
  const failing = new Map();

  return {
    source: 'yellow provider',
    everyMs: YELLOW_EVERY_MS,

    // Asks the provider about each repository that is due. It resolves
    // { results: [{ repo, items, error }] } and never rejects for one
    // repository's failure. execFile, now and timeoutMs are for a test.
    async fetch({ env = process.env, store, execFile, now = Date.now(), timeoutMs } = {}) {
      const cmd = env.INBOX_REPOS_YELLOW_CMD;
      if (!cmd || !store) return { results: [] };
      const every = yellowEveryMs(env);
      const limit = timeoutMs ?? yellowTimeoutMs(env);
      const tried = store.yellowTried();
      const due = store
        .listRepos(reposOwner(env))
        .map((repo) => repo.name)
        .filter((name) => tried[name] === undefined || now - tried[name] >= every);
      const results = [];
      await pool(due, CONCURRENCY, async (repo) => {
        try {
          const items = parseItems(await runYellow(cmd, repo, { env, execFile, timeoutMs: limit }));
          results.push({ repo, items, error: null });
        } catch (e) {
          results.push({ repo, items: null, error: e.message });
        }
      });
      return { results, at: now };
    },

    // Writes the results. A failure keeps the stored items. Logs a failure
    // once while the same error lasts. Returns how many repositories changed
    // their items.
    sync(store, answer, { log = () => {} } = {}) {
      let changed = 0;
      for (const { repo, items, error } of answer.results ?? []) {
        const now = answer.at ?? Date.now();
        if (items !== null) {
          if (store.putYellow(repo, { items, now })) changed++;
          failing.delete(repo);
          continue;
        }
        store.putYellow(repo, { error, now });
        const known = [...failing.values()].includes(error);
        failing.set(repo, error);
        if (!known) log(`cannot read the yellow items of ${repo}: ${error}`);
      }
      return changed;
    },
  };
}

export const yellowFeed = createYellowFeed();
