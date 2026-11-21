// The repos feed (tasks 0089 and 0097): the repositories of one owner that
// the signed-in user works on, with the open issue count and the open PR
// count of each. A cheap step every minute builds the candidates from the
// user's activity: the repositories GitHub says they contributed to, the
// clones under INBOX_APPS_ROOT, and the activity ledger. The candidates are
// ranked by the ledger's score, and only the first REPO_MAX_CHILDREN get
// counts, in aliased GraphQL batches, every INBOX_REPOS_COUNTS_EVERY seconds.
// No step lists every repository of the owner, so an organisation of any
// size costs the same. The store keeps the shown set in `repos`.

import { execFile as nodeExecFile } from 'node:child_process';
import { KEEP_MS, FIRST_RUN_PAGES, fetchEvents, gitEvents, repoKey, scoreRepos, signedInLogin, orderRepos } from './activity.js';
import { GH_TIMEOUT_MS, runGh } from './gh.js';
import { appsRoot } from './plugins/apps.js';
import { REPO_MAX_CHILDREN } from './render.js';

export const DEFAULT_OWNER = 'kgheacock';
export const REPOS_EVERY_MS = 60 * 1000;
// The counts are asked this often by default ($INBOX_REPOS_COUNTS_EVERY
// seconds), and no more often than MIN_COUNTS_EVERY_S.
export const DEFAULT_COUNTS_EVERY_S = 300;
const MIN_COUNTS_EVERY_S = 30;
export const REPOS_COUNTS_EVERY_MS = DEFAULT_COUNTS_EVERY_S * 1000;
// Repositories per counts query.
export const COUNTS_BATCH = 50;
// repositoriesContributedTo pages of 100; the feed reads at most this many.
export const CONTRIBUTED_PAGE_SIZE = 100;
export const CONTRIBUTED_PAGES = 5;
// A GitHub user or organisation name. It cannot start with - , so no value
// can look like a flag to gh.
const OWNER = /^[A-Za-z0-9][A-Za-z0-9-]{0,38}$/;
// A repository name GitHub allows.
const NAME = /^[A-Za-z0-9._-]{1,100}$/;

// The owner to list: $INBOX_REPOS_OWNER, else kgheacock. Throws on a name
// that GitHub does not allow.
export function reposOwner(env = process.env) {
  const owner = env.INBOX_REPOS_OWNER || DEFAULT_OWNER;
  if (!OWNER.test(owner)) {
    throw new Error(`INBOX_REPOS_OWNER "${owner}" is not a GitHub user or organisation name`);
  }
  return owner;
}

// How often the counts are asked, in ms: $INBOX_REPOS_COUNTS_EVERY seconds,
// default 300, at least 30.
export function countsEveryMs(env = process.env) {
  const n = Number(env.INBOX_REPOS_COUNTS_EVERY);
  const s = env.INBOX_REPOS_COUNTS_EVERY && Number.isFinite(n) && n > 0 ? n : DEFAULT_COUNTS_EVERY_S;
  return Math.max(MIN_COUNTS_EVERY_S, s) * 1000;
}

// The key of a repository name, as a channel segment (defined in activity.js).
export { repoKey };

const count = (value, what, name) => {
  const n = value?.totalCount;
  if (!Number.isInteger(n) || n < 0) throw new Error(`the answer has no ${what} count for ${name}`);
  return n;
};

// A `gh api graphql` answer as JSON, with its data. Throws an Error that says
// why when it has none.
function graphqlData(text) {
  let json;
  try {
    json = JSON.parse(text);
  } catch {
    throw new Error('the answer of gh is not JSON');
  }
  if (json === null || typeof json.data !== 'object' || json.data === null) {
    const why = json?.errors?.[0]?.message;
    throw new Error(why ? `GitHub said: ${why}` : 'the answer of gh has no data');
  }
  return json.data;
}

const CONTRIBUTED = `query($after: String) {
  viewer {
    repositoriesContributedTo(first: ${CONTRIBUTED_PAGE_SIZE}, after: $after,
        contributionTypes: [COMMIT, PULL_REQUEST, PULL_REQUEST_REVIEW, ISSUE],
        includeUserRepositories: true, orderBy: { field: PUSHED_AT, direction: DESC }) {
      nodes { name owner { login } url isFork isArchived pushedAt }
      pageInfo { hasNextPage endCursor }
    }
  }
}`;

// One page of the contributed-to answer: { repos, next }, where repos are the
// ones of `owner` as { name, key, url, fork, archived, pushedAt } and next is
// the cursor of the next page, or null. Throws an Error when the text is not
// an answer.
export function parseContributed(text, { owner }) {
  const list = graphqlData(text).viewer?.repositoriesContributedTo;
  if (!list || !Array.isArray(list.nodes)) throw new Error('the answer has no repositoriesContributedTo');
  const repos = [];
  for (const node of list.nodes) {
    if (typeof node?.name !== 'string' || !NAME.test(node.name)) continue;
    if (String(node.owner?.login ?? '').toLowerCase() !== owner.toLowerCase()) continue;
    repos.push({
      name: node.name,
      key: repoKey(node.name),
      url: typeof node.url === 'string' ? node.url : '',
      fork: node.isFork === true,
      archived: node.isArchived === true,
      pushedAt: typeof node.pushedAt === 'string' ? node.pushedAt : null,
    });
  }
  const page = list.pageInfo ?? {};
  return { repos, next: page.hasNextPage === true && typeof page.endCursor === 'string' ? page.endCursor : null };
}

// The repositories of the owner that the signed-in user contributed to (a
// commit, a PR, a review, or an issue, in about the last year), or owns, newest
// push first. Reads at most CONTRIBUTED_PAGES pages. Rejects when gh does.
export async function fetchContributed({ env = process.env, owner = reposOwner(env), execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS } = {}) {
  const gh = env.INBOX_GH || 'gh';
  const repos = [];
  let after = null;
  for (let page = 0; page < CONTRIBUTED_PAGES; page++) {
    const args = ['api', 'graphql', '-f', `query=${CONTRIBUTED}`];
    if (after !== null) args.push('-f', `after=${after}`);
    const got = parseContributed(await runGh(gh, args, { env, execFile, timeoutMs }), { owner });
    repos.push(...got.repos);
    if (got.next === null) break;
    after = got.next;
  }
  return repos;
}

// The counts query of `names`: one aliased `repository` per name, rN for the
// Nth. The owner and the names go in as variables, so no name is ever part of
// the query text.
export function countsQuery(names) {
  const vars = names.map((_, i) => `$n${i}: String!`).join(', ');
  const fields = names.map((_, i) => `  r${i}: repository(owner: $o, name: $n${i}) { ...R }`).join('\n');
  return `query($o: String!, ${vars}) {
${fields}
}
fragment R on Repository {
  name url isFork isArchived pushedAt
  issues(states: OPEN) { totalCount }
  pullRequests(states: OPEN) { totalCount }
}`;
}

// The answer of one counts query for `names`: a Map of the asked name to its
// row ({ name, key, url, fork, archived, pushedAt, issues, prs }), or to null
// when GitHub has no such repository. Throws an Error when the answer has no
// data, or a row has no counts.
export function parseCounts(text, names) {
  const data = graphqlData(text);
  const got = new Map();
  names.forEach((asked, i) => {
    const node = data[`r${i}`];
    if (node === null || node === undefined) {
      got.set(asked, null);
      return;
    }
    const name = typeof node.name === 'string' && NAME.test(node.name) ? node.name : asked;
    got.set(asked, {
      name,
      key: repoKey(name),
      url: typeof node.url === 'string' ? node.url : '',
      fork: node.isFork === true,
      archived: node.isArchived === true,
      pushedAt: typeof node.pushedAt === 'string' ? node.pushedAt : null,
      issues: count(node.issues, 'issue', name),
      prs: count(node.pullRequests, 'PR', name),
    });
  });
  return got;
}

// Asks GitHub for the counts of the repositories `names` of `owner`, one
// query per COUNTS_BATCH names. Answers the Map of parseCounts for all.
// Rejects when a query fails.
export async function fetchCounts({ env = process.env, owner, names, execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS }) {
  const gh = env.INBOX_GH || 'gh';
  const got = new Map();
  for (let i = 0; i < names.length; i += COUNTS_BATCH) {
    const batch = names.slice(i, i + COUNTS_BATCH);
    const args = ['api', 'graphql', '-f', `query=${countsQuery(batch)}`, '-f', `o=${owner}`];
    batch.forEach((name, j) => args.push('-f', `n${j}=${name}`));
    // A missing repository is an error beside the data; gh exits 1 for it.
    const text = await runGh(gh, args, { env, execFile, timeoutMs, partial: true });
    for (const [name, row] of parseCounts(text, batch)) got.set(name, row);
  }
  return got;
}

// The candidates, ranked: [{ name, key, from }], the first REPO_MAX_CHILDREN
// of the union of `contributed` (rows of fetchContributed), `clones` (git
// events, with the folder name) and the ledger keys of `rows`. A ledger key
// takes its name from `known` (key -> name) when it has one. They are ordered
// by the score of `rows`; a repository with no score by its newest push.
export function rankCandidates({ contributed = [], clones = [], rows = [], known = new Map(), now = Date.now(), max = REPO_MAX_CHILDREN }) {
  const byKey = new Map();
  const add = (name, from) => {
    if (typeof name !== 'string' || !NAME.test(name)) return;
    const key = repoKey(name);
    if (!byKey.has(key)) byKey.set(key, { name, key, from });
  };
  for (const repo of contributed) add(repo.name, 'contributed');
  for (const event of clones) add(event.name ?? known.get(event.repo) ?? event.repo, 'clone');
  for (const row of rows) add(known.get(row.repo) ?? row.repo, 'ledger');
  // A row of the ledger once per (repo, kind, ref): a new event may also be
  // in the store.
  const seen = new Set();
  const unique = rows.filter((row) => {
    const id = `${row.repo}\0${row.kind}\0${row.ref}`;
    if (seen.has(id)) return false;
    seen.add(id);
    return true;
  });
  const scores = scoreRepos(unique, now);
  for (const repo of contributed) {
    if (scores.has(repo.key)) continue;
    const pushed = Date.parse(repo.pushedAt ?? '');
    scores.set(repo.key, { score: 0, last: Number.isNaN(pushed) ? 0 : pushed });
  }
  return orderRepos([...byKey.values()], scores).slice(0, max);
}

// Writes the shown set to the store; a repository that left it loses its
// row. Returns how many rows changed.
export function syncRepos(store, { owner, repos }) {
  return store.replaceRepos(owner, repos, { complete: true });
}

// The kinds that only the events of gh give.
const GH_KINDS = ['push', 'pr_open', 'pr_review'];
let lastEventsError = null;
// The signed-in login, asked once per gh program (task 0090 asks `gh api
// user` once, not every minute). Keyed by execFile, so a test stub never
// sees the login of another. A failed events read forgets it, so a new
// `gh auth login` is picked up.
const logins = new WeakMap();

// When the counts were last read for all shown repositories, and the last
// failure logged. A test passes its own.
// `missing` holds the names GitHub did not have, or has archived, so they are
// not asked again before the next full read.
export const createCountsState = () => ({ owner: null, at: -Infinity, error: null, missing: new Set() });
const countsState = createCountsState();

// One run of the feed: the activity of the signed-in user (task 0090: the
// events of gh, and the .git/logs/HEAD times under INBOX_APPS_ROOT), the
// candidates, and the counts of the shown ones (task 0097). A failure of the
// events is logged once and leaves answer.events empty. A failure of the
// candidates fails the run. A failure of the counts is logged once and
// keeps the stored counts. The counts of all shown repositories are asked
// every INBOX_REPOS_COUNTS_EVERY seconds; in between only a repository with
// no stored row is asked. Answers { owner, repos, cut, offline, events, git }.
export async function fetchFeed({ env = process.env, store, execFile, log = () => {}, now = Date.now(), counts = countsState } = {}) {
  const owner = reposOwner(env);
  const answer = { owner, repos: [], cut: false, offline: null, events: [], git: [] };
  const ledger = store ? store.activityRows(now - KEEP_MS) : [];
  const first = !store || !store.activityRows(0).some((row) => GH_KINDS.includes(row.kind));
  const run = execFile ?? nodeExecFile;
  const gh = env.INBOX_GH || 'gh';
  if (!logins.has(run)) logins.set(run, new Map());
  const known = logins.get(run);
  try {
    if (!known.has(gh)) known.set(gh, await signedInLogin({ env, execFile: run }));
    answer.events = await fetchEvents({ env, owner, pages: first ? FIRST_RUN_PAGES : 1, login: known.get(gh), execFile: run });
    lastEventsError = null;
  } catch (e) {
    known.delete(gh);
    if (e.message !== lastEventsError) log(`cannot read GitHub events: ${e.message}`);
    lastEventsError = e.message;
  }
  const root = appsRoot(env);
  if (root !== null) answer.git = gitEvents(root, { since: now - KEEP_MS });

  const contributed = await fetchContributed({ env, owner, execFile: run });
  const stored = new Map((store ? store.listRepos(owner) : []).map((row) => [row.key, row]));
  const names = new Map([...stored.values()].map((row) => [row.key, row.name]));
  const shown = rankCandidates({
    contributed,
    clones: answer.git,
    rows: [...ledger, ...answer.events, ...answer.git],
    known: names,
    now,
  });

  if (counts.owner !== owner) Object.assign(counts, createCountsState(), { owner });
  const due = now - counts.at >= countsEveryMs(env);
  const ask = due ? shown : shown.filter((c) => !stored.has(c.key) && !counts.missing.has(c.name));
  let fresh = new Map();
  if (ask.length > 0) {
    try {
      fresh = await fetchCounts({ env, owner, names: ask.map((c) => c.name), execFile: run });
      if (due) {
        counts.at = now;
        counts.missing.clear();
      }
      for (const [name, row] of fresh) if (row === null) counts.missing.add(name);
      counts.error = null;
    } catch (e) {
      if (e.message !== counts.error) log(`cannot read the counts of GitHub repositories: ${e.message}`);
      counts.error = e.message;
    }
  }

  const fromContributed = new Map(contributed.map((repo) => [repo.key, repo]));
  const keepArchived = env.INBOX_REPOS_ARCHIVED === '1';
  const repos = [];
  const keys = new Set();
  for (const c of shown) {
    let row;
    if (fresh.has(c.name)) row = fresh.get(c.name);
    else if (counts.missing.has(c.name)) row = null;
    else row = stored.get(c.key) ?? (fromContributed.has(c.key) ? { ...fromContributed.get(c.key), issues: 0, prs: 0 } : null);
    // GitHub has no such repository, or it is not known yet.
    if (row === null || row === undefined) continue;
    if (row.archived && !keepArchived) {
      counts.missing.add(c.name);
      continue;
    }
    if (keys.has(row.key)) continue;
    keys.add(row.key);
    repos.push(row);
  }
  repos.sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0));
  answer.repos = repos;
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
