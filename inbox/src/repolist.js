// The repos feed (task 0089): every repository of one owner, with its open
// issue count and open PR count, from one `gh repo list` call. The daemon
// runs it every minute and the store keeps the answer in `repos`.

import { execFile as nodeExecFile } from 'node:child_process';
import { KEEP_MS, FIRST_RUN_PAGES, fetchEvents, gitEvents, repoKey, scoreRepos, signedInLogin } from './activity.js';
import { GH_TIMEOUT_MS, runGh } from './gh.js';
import { appsRoot } from './plugins/apps.js';

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

// The key of a repository name, as a channel segment (defined in activity.js).
export { repoKey };

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

// The kinds that only the events of gh give.
const GH_KINDS = ['push', 'pr_open', 'pr_review'];
let lastEventsError = null;
// The signed-in login, asked once per gh program (task 0090 asks `gh api
// user` once, not every minute). Keyed by execFile, so a test stub never
// sees the login of another. A failed events read forgets it, so a new
// `gh auth login` is picked up.
const logins = new WeakMap();

// Asks for the repositories (fetchRepos) and for the activity of the
// signed-in user (task 0090): the events of gh, and the .git/logs/HEAD times
// under INBOX_APPS_ROOT. A failure of the events is logged once and leaves
// answer.events empty; it never fails the list. The first run, with no gh
// event in the ledger, reads 3 pages of events; later runs read one.
export async function fetchFeed({ env = process.env, store, execFile, log = () => {}, now = Date.now() } = {}) {
  const answer = await fetchRepos({ env, execFile });
  answer.events = [];
  answer.git = [];
  const first = !store || !store.activityRows(0).some((row) => GH_KINDS.includes(row.kind));
  const run = execFile ?? nodeExecFile;
  const gh = env.INBOX_GH || 'gh';
  if (!logins.has(run)) logins.set(run, new Map());
  const known = logins.get(run);
  try {
    if (!known.has(gh)) known.set(gh, await signedInLogin({ env, execFile: run }));
    answer.events = await fetchEvents({ env, owner: answer.owner, pages: first ? FIRST_RUN_PAGES : 1, login: known.get(gh), execFile: run });
    lastEventsError = null;
  } catch (e) {
    known.delete(gh);
    if (e.message !== lastEventsError) log(`cannot read GitHub events: ${e.message}`);
    lastEventsError = e.message;
  }
  const root = appsRoot(env);
  if (root !== null) answer.git = gitEvents(root, { since: now - KEEP_MS });
  return answer;
}

// Writes the repositories and the activity: it notes the events, deletes the
// rows older than KEEP_MS, and sets answer.scores (repoKey -> { score, last })
// for the renderer. Returns how many repos rows changed.
export function syncFeed(store, answer, now = Date.now()) {
  const changed = syncRepos(store, answer);
  store.noteActivities([...(answer.events ?? []), ...(answer.git ?? [])]);
  store.pruneActivity(now - KEEP_MS);
  answer.scores = scoreRepos(store.activityRows(now - KEEP_MS), now);
  return changed;
}

// What the daemon draws when the first read fails: the rows the store holds,
// and why they may be old (task 0089), ordered by the ledger it holds.
export function coldRepos({ env = process.env, store, error, now = Date.now() }) {
  const owner = reposOwner(env);
  return {
    owner,
    repos: store.listRepos(owner),
    cut: false,
    offline: error?.message ?? 'offline',
    scores: scoreRepos(store.activityRows(now - KEEP_MS), now),
  };
}

export const feed = {
  source: 'GitHub repositories',
  everyMs: REPOS_EVERY_MS,
  fetch: (options) => fetchFeed(options),
  sync: (store, answer) => syncFeed(store, answer),
  cold: (options) => coldRepos(options),
};
