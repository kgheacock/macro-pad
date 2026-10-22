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
export const FEED_EVERY_MS = 60 * 1000;
export const GH_TIMEOUT_MS = 30 * 1000;
// The cwd of a message the feed writes: it was sent from no folder.
const NO_FOLDER = '';
// One answer holds at most this many PRs.
const PAGE = 50;

const GRAPHQL = `query($q: String!) {
  search(query: $q, type: ISSUE, first: ${PAGE}) {
    issueCount
    nodes {
      ... on PullRequest {
        number
        title
        url
        repository { name owner { login } }
        commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }
      }
    }
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
  const pulls = [];
  for (const node of search.nodes) {
    if (!node || typeof node.number !== 'number' || typeof node.url !== 'string') continue;
    const channel = channelOf(node.repository?.owner?.login, node.repository?.name, node.number);
    if (channel === null || !node.url.startsWith('https://')) continue;
    const state = node.commits?.nodes?.[0]?.commit?.statusCheckRollup?.state;
    pulls.push({
      channel,
      title: String(node.title ?? '').trim() || `#${node.number}`,
      url: node.url,
      status: ROLLUP[state] ?? 'none',
    });
  }
  return { total: typeof search.issueCount === 'number' ? search.issueCount : pulls.length, pulls };
}

// Asks GitHub for the open PRs. env holds INBOX_GH (the gh program) and
// INBOX_GITHUB_QUERY (the search). execFile is for a test.
export function fetchPulls({ env = process.env, execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS } = {}) {
  const gh = env.INBOX_GH || 'gh';
  const query = env.INBOX_GITHUB_QUERY || DEFAULT_QUERY;
  const args = ['api', 'graphql', '-f', `query=${GRAPHQL}`, '-f', `q=${query}`];
  return new Promise((resolve, reject) => {
    execFile(gh, args, { env, timeout: timeoutMs, maxBuffer: 16 * 1024 * 1024, shell: false }, (error, stdout, stderr) => {
      if (!error) {
        try {
          resolve(parseAnswer(String(stdout)));
        } catch (e) {
          reject(e);
        }
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
$INBOX_GITHUB_QUERY (default "${DEFAULT_QUERY}"), one channel
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
