// The activity ledger (task 0090): Keith's own events per repository, a
// score that decays with time, and the order of the repository keys that the
// score gives. The store holds the rows (`activity`, schema 7); this file
// holds the rules, so a change of a weight needs no migration.

import { execFile as nodeExecFile } from 'node:child_process';
import { readdirSync, statSync } from 'node:fs';
import path from 'node:path';
import { GH_TIMEOUT_MS, runGh } from './gh.js';

const DAY_MS = 24 * 60 * 60 * 1000;

// What each kind of event is worth. A kind is a key of WEIGHTS.
export const WEIGHTS = { pr_review: 3, pr_open: 3, push: 3, claude: 2, code: 2, git: 1 };
export const KINDS = Object.keys(WEIGHTS);
// A weight halves every HALF_LIFE_MS; a row older than KEEP_MS is deleted.
export const HALF_LIFE_MS = 3 * DAY_MS;
export const KEEP_MS = 30 * DAY_MS;
// The events API shows 100 events a page. The first run reads this many
// pages; later runs read one.
export const EVENT_PAGE_SIZE = 100;
export const FIRST_RUN_PAGES = 3;

// The key of a repository name, as a channel segment: lower case, a dot
// becoming -, as the github plugin writes the repository of a PR's head.
// repolist.js re-exports it.
export const repoKey = (name) => String(name).replace(/\./g, '-').toLowerCase();

// A repository or session reference the ledger accepts: no space or control
// character, so a value cannot look like a flag or break a log line.
const REF = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$/;
export const validRef = (ref) => REF.test(String(ref));

// The local date of a time, YYYY-MM-DD: the ref of a daily event.
export function dateRef(at) {
  const d = new Date(at);
  const pad = (n) => String(n).padStart(2, '0');
  return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

// rows: [{ repo, kind, at }] -> Map repoKey -> { score, last }. A row counts
// weight * 0.5 ** (age / HALF_LIFE_MS); last is the newest `at`. A row of an
// unknown kind counts nothing. A time in the future counts as now. The score
// is rounded, so the sum does not depend on the order of the rows.
export function scoreRepos(rows, now = Date.now()) {
  const scores = new Map();
  for (const { repo, kind, at } of rows) {
    const weight = WEIGHTS[kind] ?? 0;
    if (weight === 0) continue;
    const got = scores.get(repo) ?? { score: 0, last: 0 };
    got.score += weight * 0.5 ** (Math.max(0, now - at) / HALF_LIFE_MS);
    got.last = Math.max(got.last, at);
    scores.set(repo, got);
  }
  for (const got of scores.values()) got.score = Math.round(got.score * 1e9) / 1e9;
  return scores;
}

// Orders two { score, last, name }: score high first, then last new first,
// then name ascending. It fits `before(a, b)` of render.js. A missing score
// or last counts as 0.
export function compareRepos(a, b) {
  return (
    (b.score ?? 0) - (a.score ?? 0) ||
    (b.last ?? 0) - (a.last ?? 0) ||
    (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)
  );
}

// repos: [{ name, key }] (as parseRepos gives them); scores: the Map of
// scoreRepos. Returns a new list, active repositories first. A repository
// with no event has score 0 and last 0, so it sorts after every active one,
// by name. The result does not depend on the order of `repos`.
export function orderRepos(repos, scores = new Map()) {
  const withScore = repos.map((repo) => {
    const got = scores.get(repo.key ?? repoKey(repo.name));
    return { repo, name: repo.name, score: got?.score ?? 0, last: got?.last ?? 0 };
  });
  return withScore.sort(compareRepos).map((entry) => entry.repo);
}

// The names the keys show. Open (the repos layer, or a layer under it, is
// open): nothing a person sees moves, so `shown` stays and the names of
// `wanted` that are new go at the end, up to `max` in all. Closed: `wanted`,
// at most `max`.
export function nextShown({ shown, wanted, open, max = Infinity }) {
  if (!open) return wanted.slice(0, max);
  const next = [...shown];
  for (const name of wanted) {
    if (next.length >= max) break;
    if (!next.includes(name)) next.push(name);
  }
  return next;
}

// What a GitHub event of the signed-in user is, in ledger terms, or null.
function kindOf(event) {
  switch (event?.type) {
    case 'PushEvent':
      return 'push';
    case 'PullRequestEvent':
      return event.payload?.action === 'opened' ? 'pr_open' : null;
    case 'PullRequestReviewEvent':
      return event.payload?.action === undefined || event.payload.action === 'created' ? 'pr_review' : null;
    default:
      return null;
  }
}

// The events of `users/LOGIN/events` (a JSON array) that the ledger keeps:
// [{ repo, kind, ref, at }], where repo is the repoKey of a repository of
// `owner` (without the owner), ref is the event id, and at is its time in ms.
// Events of other owners, of other types, and with no id or time are left
// out. Throws an Error when the text is not a list of events.
export function parseEvents(text, { owner }) {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error('the answer of gh is not JSON');
  }
  if (!Array.isArray(json)) throw new Error('the answer of gh is not a list of events');
  const events = [];
  for (const event of json) {
    const kind = kindOf(event);
    if (kind === null) continue;
    const full = event.repo?.name;
    const at = Date.parse(event.created_at);
    if (typeof full !== 'string' || event.id === undefined || event.id === null || Number.isNaN(at)) continue;
    const slash = full.indexOf('/');
    if (slash < 1 || full.slice(0, slash).toLowerCase() !== String(owner).toLowerCase()) continue;
    events.push({ repo: repoKey(full.slice(slash + 1)), kind, ref: String(event.id), at });
  }
  return events;
}

// A login GitHub allows; it goes into a URL path.
const LOGIN = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}(\[bot\])?$/;

// The push, PR-open and PR-review events of the signed-in user, in the
// repositories of `owner`. It asks `gh api user` for the login once, then
// `gh api users/LOGIN/events`: GitHub shows the private events of a user
// only to that user. `pages` is how many pages of 100 to read (it stops at
// a short page). execFile is for a test. Rejects when gh does.
export async function fetchEvents({ env = process.env, owner, pages = 1, execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS }) {
  const gh = env.INBOX_GH || 'gh';
  const login = (await runGh(gh, ['api', 'user', '--jq', '.login'], { env, execFile, timeoutMs })).trim();
  if (!LOGIN.test(login)) throw new Error(`gh gave no login: "${login.slice(0, 40)}"`);
  const events = [];
  for (let page = 1; page <= pages; page++) {
    const url = `users/${login}/events?per_page=${EVENT_PAGE_SIZE}${page > 1 ? `&page=${page}` : ''}`;
    const text = await runGh(gh, ['api', url], { env, execFile, timeoutMs });
    events.push(...parseEvents(text, { owner }));
    // A page of fewer than 100 events is the last.
    if ((JSON.parse(text).length ?? 0) < EVENT_PAGE_SIZE) break;
  }
  return events;
}

// The time (ms) a repository folder's .git/logs/HEAD last changed, or 0. A
// commit, a checkout, a pull and a rebase write a line there; `git status`
// rewrites .git/index and does not, so the index time is not used.
export function headLogTime(dir) {
  try {
    return statSync(path.join(dir, '.git', 'logs', 'HEAD')).mtimeMs;
  } catch {
    return 0;
  }
}

// One `git` event per repository folder of `root` whose .git/logs/HEAD
// changed after `since`: [{ repo, kind: 'git', ref: DATE, at }]. A folder
// that is not a repository, or a root that cannot be read, gives none.
export function gitEvents(root, { since = 0 } = {}) {
  let entries;
  try {
    entries = readdirSync(root, { withFileTypes: true });
  } catch {
    return [];
  }
  const events = [];
  for (const entry of entries) {
    if (!entry.isDirectory() && !entry.isSymbolicLink()) continue;
    const at = headLogTime(path.join(root, entry.name));
    if (at > since) events.push({ repo: repoKey(entry.name), kind: 'git', ref: dateRef(at), at: Math.round(at) });
  }
  return events;
}
