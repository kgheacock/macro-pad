// The repos feed (task 0089): every repository of one owner, with its open
// issue count and open PR count, from one `gh repo list` call. The daemon
// runs it every minute and the store keeps the answer in `repos`.

import { execFile as nodeExecFile } from 'node:child_process';
import { GH_TIMEOUT_MS, runGh } from './gh.js';

export const DEFAULT_OWNER = 'kgheacock';
export const REPOS_EVERY_MS = 60 * 1000;
// gh lists at most this many repositories; an answer of this size may be cut.
export const REPO_LIMIT = 1000;
// The fields of a repository that the store keeps.
const FIELDS = 'name,url,isFork,isArchived,pushedAt,issues,pullRequests';
// A GitHub user or organisation name. It cannot start with - , so no value
// can look like a flag to gh.
const OWNER = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/;

// The owner to list: $INBOX_REPOS_OWNER, else kgheacock. Throws on a name
// that GitHub does not allow.
export function reposOwner(env = process.env) {
  const owner = env.INBOX_REPOS_OWNER || DEFAULT_OWNER;
  if (!OWNER.test(owner)) {
    throw new Error(`INBOX_REPOS_OWNER "${owner}" is not a GitHub user or organisation name`);
  }
  return owner;
}

// The key of a repository name, as a channel segment: lower case, a dot
// becoming -, as the github plugin writes the repository of a PR's head.
export const repoKey = (name) => String(name).replace(/\./g, '-').toLowerCase();

const count = (value, what, name) => {
  const n = value?.totalCount;
  if (!Number.isInteger(n) || n < 0) throw new Error(`the answer has no ${what} count for ${name}`);
  return n;
};

// The repositories of a `gh repo list --json` answer, by name:
// { repos: [{ name, key, url, fork, archived, pushedAt, issues, prs }], cut }.
// cut is true when the answer has REPO_LIMIT rows, so more may exist.
// Throws an Error when the answer is not one.
export function parseRepos(text) {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error('the answer of gh is not JSON');
  }
  if (!Array.isArray(json)) throw new Error('the answer of gh is not a list of repositories');
  const repos = json.map((row) => {
    if (typeof row?.name !== 'string' || row.name === '') throw new Error('a repository in the answer has no name');
    return {
      name: row.name,
      key: repoKey(row.name),
      url: typeof row.url === 'string' ? row.url : '',
      fork: row.isFork === true,
      archived: row.isArchived === true,
      pushedAt: typeof row.pushedAt === 'string' ? row.pushedAt : null,
      issues: count(row.issues, 'issue', row.name),
      prs: count(row.pullRequests, 'PR', row.name),
    };
  });
  repos.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  return { repos, cut: json.length >= REPO_LIMIT };
}

// Asks GitHub for the repositories of the owner. Archived ones stay out
// unless INBOX_REPOS_ARCHIVED=1; forks stay in. env also holds INBOX_GH (the
// gh program). execFile is for a test. Answers { owner, repos, cut, offline }.
export async function fetchRepos({ env = process.env, execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS } = {}) {
  const owner = reposOwner(env);
  const gh = env.INBOX_GH || 'gh';
  const args = ['repo', 'list', owner, '--limit', String(REPO_LIMIT)];
  if (env.INBOX_REPOS_ARCHIVED !== '1') args.push('--no-archived');
  args.push('--json', FIELDS);
  const { repos, cut } = parseRepos(await runGh(gh, args, { env, execFile, timeoutMs }));
  return { owner, repos, cut, offline: null };
}

// Writes the answer to the store. Returns how many rows changed.
export function syncRepos(store, { owner, repos, cut }) {
  return store.replaceRepos(owner, repos, { complete: !cut });
}

// What the daemon draws when the first read fails: the rows the store holds,
// and why they may be old (task 0089).
export function coldRepos({ env = process.env, store, error }) {
  const owner = reposOwner(env);
  return { owner, repos: store.listRepos(owner), cut: false, offline: error?.message ?? 'offline' };
}

export const feed = {
  source: 'GitHub repositories',
  everyMs: REPOS_EVERY_MS,
  fetch: ({ env }) => fetchRepos({ env }),
  sync: (store, answer) => syncRepos(store, answer),
  cold: (options) => coldRepos(options),
};
