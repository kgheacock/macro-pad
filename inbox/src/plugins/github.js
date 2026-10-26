// The github plugin (task 0076): one key per open pull request, coloured by
// the checks of its last commit. The plugin feeds itself: the daemon asks
// GitHub through `gh` every minute and writes a message when a PR's state
// changes. A channel shows the status of its newest message, seen or not; a
// press opens the PR and clears the pip on its key. The top key shows the
// worst state, and the pip counts the PRs whose checks fail.

import { execFile as nodeExecFile } from 'node:child_process';
import { validateChannel } from '../channel.js';
import { channelInput, emojiFor } from '../emoji.js';
import { MAX_CAPTION, clip } from '../render.js';

export const GITHUB_STATUSES = ['failing', 'pending', 'passing', 'none', 'closed'];

// The check roll-up of a commit, as GitHub names it, to a status.
const ROLLUP = {
  FAILURE: 'failing',
  ERROR: 'failing',
  PENDING: 'pending',
  EXPECTED: 'pending',
  SUCCESS: 'passing',
};

// Pad status names (driver/layers/doc.go).
const PAD_STATUS = { failing: 'ERROR', pending: 'RUNNING', passing: 'DONE' };

// The order of the children: failing first.
const ORDER = ['failing', 'pending', 'passing', 'none'];

// A PR shows while its newest message is younger than this. The feed writes
// only on a change, so it is long.
export const ACTIVE_MS = 365 * 24 * 60 * 60 * 1000;

export const DEFAULT_QUERY = 'is:pr is:open author:@me archived:false';
// The notification reasons of a watched PR: you watch its repository
// (subscribed) or the PR itself (manual).
const WATCH_REASONS = ['subscribed', 'manual'];
// The newest notification threads read for watched PRs.
const NOTIFICATION_PAGE = 50;
export const FEED_EVERY_MS = 60 * 1000;
export const GH_TIMEOUT_MS = 30 * 1000;
// The cwd of a message the feed writes: it was sent from no folder.
const NO_FOLDER = '';
// One answer holds at most this many PRs.
const PAGE = 50;

// The fields of a PR that a key needs.
const PR_FIELDS =
  'number title url state repository { name owner { login } } ' +
  'commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }';

const GRAPHQL = `query($q: String!) {
  search(query: $q, type: ISSUE, first: ${PAGE}) {
    issueCount
    nodes { ... on PullRequest { ${PR_FIELDS} } }
  }
}`;

// A GitHub name as one channel segment: a dot becomes -.
const segment = (name) => String(name).replace(/\./g, '-');

// The channel of a PR, OWNER.REPO.NUMBER, or null when a name cannot be one.
export function channelOf(owner, repo, number) {
  try {
    return validateChannel(`${segment(owner)}.${segment(repo)}.${number}`);
  } catch {
    return null;
  }
}

// The caption REPO #NUMBER, over the emoji of the repository, so the PRs of
// one repository share an emoji.
export function prIcon(channel, set) {
  const parts = channel.split('.');
  const number = parts.at(-1);
  const repo = parts.length > 1 ? parts.slice(0, -1).join('.') : channel;
  const name = parts.length > 1 ? parts.at(-2) : channel;
  const room = Math.max(1, MAX_CAPTION - number.length - 2);
  return {
    iconText: parts.length > 1 ? `${clip(name, room)} #${number}` : clip(channel, MAX_CAPTION),
    iconEmoji: [emojiFor(channelInput(repo), set).iconRef],
  };
}

// The PRs of a `gh api graphql` answer: { total, pulls: [{ channel, title,
// url, status }] }. Throws an Error when the answer is not one.
export function parseAnswer(text) {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error('gh printed something that is not JSON');
  }
  const search = json?.data?.search;
  if (!search || !Array.isArray(search.nodes)) {
    const why = json?.errors?.[0]?.message;
    throw new Error(why ? `GitHub said: ${why}` : 'the answer has no search results');
  }
  const pulls = search.nodes.map(pullOf).filter((pr) => pr !== null);
  return { total: typeof search.issueCount === 'number' ? search.issueCount : pulls.length, pulls };
}

// One PR node of an answer as { channel, title, url, status }, or null.
function pullOf(node) {
  if (!node || typeof node.number !== 'number' || typeof node.url !== 'string') return null;
  const channel = channelOf(node.repository?.owner?.login, node.repository?.name, node.number);
  if (channel === null || !node.url.startsWith('https://')) return null;
  const state = node.commits?.nodes?.[0]?.commit?.statusCheckRollup?.state;
  return {
    channel,
    title: String(node.title ?? '').trim() || `#${node.number}`,
    url: node.url,
    status: ROLLUP[state] ?? 'none',
  };
}

// The PRs you watch, from a `gh api notifications` answer: the threads whose
// reason is subscribed or manual, as [{ owner, repo, number }], each once.
export function parseNotifications(text) {
  let threads;
  try {
    threads = JSON.parse(text);
  } catch {
    throw new Error('gh printed notifications that are not JSON');
  }
  if (!Array.isArray(threads)) throw new Error('the notifications answer is not a list');
  const seen = new Set();
  const refs = [];
  for (const thread of threads) {
    if (thread?.subject?.type !== 'PullRequest' || !WATCH_REASONS.includes(thread.reason)) continue;
    const match = /\/repos\/([^/]+)\/([^/]+)\/pulls\/(\d+)$/.exec(String(thread.subject.url ?? ''));
    if (match === null) continue;
    const [, owner, repo, number] = match;
    const key = `${owner}/${repo}#${number}`.toLowerCase();
    if (seen.has(key)) continue;
    seen.add(key);
    refs.push({ owner, repo, number: Number(number) });
  }
  return refs;
}

// One GraphQL query for the given PRs: an alias per PR, its names passed as
// variables, never in the query text.
export function watchedQuery(refs) {
  const vars = refs.map((_, i) => `$o${i}: String!, $r${i}: String!, $n${i}: Int!`).join(', ');
  const fields = refs
    .map((_, i) => `p${i}: repository(owner: $o${i}, name: $r${i}) { pullRequest(number: $n${i}) { ${PR_FIELDS} } }`)
    .join('\n  ');
  const args = refs.flatMap((ref, i) => ['-f', `o${i}=${ref.owner}`, '-f', `r${i}=${ref.repo}`, '-F', `n${i}=${ref.number}`]);
  return { query: `query(${vars}) {\n  ${fields}\n}`, args };
}

// The open PRs of a watchedQuery answer. A PR GitHub could not give (a
// deleted repository, no access) is left out.
export function parseWatched(text) {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error('gh printed something that is not JSON');
  }
  if (!json?.data) {
    const why = json?.errors?.[0]?.message;
    throw new Error(why ? `GitHub said: ${why}` : 'the answer has no pull requests');
  }
  return Object.values(json.data)
    .map((repository) => repository?.pullRequest)
    .filter((node) => node?.state === 'OPEN')
    .map(pullOf)
    .filter((pr) => pr !== null);
}

// Runs gh with args. Resolves its standard output; rejects with an Error
// that says why. With partial, a GraphQL answer that holds data is resolved
// even when gh exits 1 for the errors beside it.
function runGh(gh, args, { env, execFile, timeoutMs, partial = false }) {
  return new Promise((resolve, reject) => {
    execFile(gh, args, { env, timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024, shell: false }, (error, stdout, stderr) => {
      if (!error || (partial && error.code === 1 && /"data"\s*:\s*\{/.test(String(stdout)))) {
        resolve(String(stdout));
        return;
      }
      if (error.code === 'ENOENT') {
        reject(new Error(`no ${gh}; install the GitHub CLI or set INBOX_GH`));
      } else if (error.killed || error.signal === 'SIGTERM') {
        reject(new Error(`${gh} did not answer within ${timeoutMs / 1000} s`));
      } else {
        const detail = String(stderr ?? '').trim().split('\n')[0] || error.message;
        reject(new Error(`${gh} failed: ${detail}`));
      }
    });
  });
}

// Asks GitHub for the open PRs: those of the search, then, unless
// INBOX_GITHUB_WATCHED is 0, the ones you watch that have a notification
// among your newest 50. env holds INBOX_GH (the gh program) and
// INBOX_GITHUB_QUERY (the search). execFile is for a test.
export async function fetchPulls({ env = process.env, execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS } = {}) {
  const gh = env.INBOX_GH || 'gh';
  const run = (args, options = {}) => runGh(gh, args, { env, execFile, timeoutMs, ...options });
  const query = env.INBOX_GITHUB_QUERY || DEFAULT_QUERY;
  const searched = parseAnswer(await run(['api', 'graphql', '-f', `query=${GRAPHQL}`, '-f', `q=${query}`]));
  if (env.INBOX_GITHUB_WATCHED === '0') return searched;

  let refs;
  try {
    refs = parseNotifications(await run(['api', `notifications?all=true&per_page=${NOTIFICATION_PAGE}`]));
  } catch (e) {
    throw new Error(`cannot read watched PRs: ${e.message}`);
  }
  const have = new Set(searched.pulls.map((pr) => pr.channel));
  refs = refs.filter((ref) => !have.has(channelOf(ref.owner, ref.repo, ref.number)));
  if (refs.length === 0) return searched;
  const { query: watched, args } = watchedQuery(refs);
  const extra = parseWatched(await run(['api', 'graphql', '-f', `query=${watched}`, ...args], { partial: true }));
  // A cut search still says so: total keeps what the search left out.
  return { total: searched.total + extra.length, pulls: [...searched.pulls, ...extra] };
}

// Writes what changed since the store's last word on each PR, and closes the
// PRs that are no longer open. Returns how many messages it wrote.
export function syncPulls(store, { total, pulls }, { now = Date.now() } = {}) {
  const known = new Map(store.recent(now - ACTIVE_MS).map((row) => [row.channel, row]));
  let wrote = 0;
  const open = new Set();
  for (const pr of pulls) {
    open.add(pr.channel);
    if (known.get(pr.channel)?.status === pr.status) continue;
    store.insert(pr.channel, pr.title, NO_FOLDER, { status: pr.status, here: `url:${pr.url}`, now });
    wrote++;
  }
  // A cut answer does not say which PRs closed.
  if (total > pulls.length) return wrote;
  for (const [channel, row] of known) {
    if (open.has(channel) || row.status === 'closed') continue;
    const id = store.insert(channel, 'closed', NO_FOLDER, { status: 'closed', here: row.here, now });
    store.markSeen(channel, id, now);
    wrote++;
  }
  return wrote;
}

const rankOf = (row) => {
  const i = ORDER.indexOf(row.status);
  return i === -1 ? ORDER.length : i;
};

export default {
  name: 'github',
  summary: 'open pull requests, coloured by their checks',
  statuses: GITHUB_STATUSES,
  defaultStatus: 'none',
  about: `The daemon asks GitHub through gh every minute for the open PRs of
$INBOX_GITHUB_QUERY (default "${DEFAULT_QUERY}"), and the open PRs you
watch (set INBOX_GITHUB_WATCHED=0 to leave them out), one channel
OWNER.REPO.NUMBER each. A PR is failing (red), pending (blue), passing
(green), or none; the newest message says which. A press opens the PR.
The pip on the key counts the failing PRs.`,
  rules: {
    id: 'github-inbox',
    top(rows) {
      const statuses = new Set(rows.map((row) => row.status));
      const worst = ['failing', 'pending', 'passing'].find((s) => statuses.has(s));
      return {
        status: PAD_STATUS[worst] ?? '',
        count: rows.filter((row) => row.status === 'failing').length,
        // A picture under macropadd's assets directory.
        iconRef: 'github.png',
      };
    },
    child(row) {
      return { status: PAD_STATUS[row?.status] ?? '', count: row?.count ?? 0 };
    },
    activeMs: ACTIVE_MS,
    shows: (row) => row.status !== 'closed',
    before: (a, b) => rankOf(a) - rankOf(b) || b.createdAt - a.createdAt || b.newestId - a.newestId,
    icon: prIcon,
  },
  feed: {
    source: 'GitHub',
    everyMs: FEED_EVERY_MS,
    fetch: ({ env }) => fetchPulls({ env }),
    sync: (store, answer) => syncPulls(store, answer),
  },
};
