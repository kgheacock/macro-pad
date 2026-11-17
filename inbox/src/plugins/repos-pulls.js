// The open PRs of the repositories of one owner (task 0095): one GraphQL
// search per run, written to the store (`repo_pulls`, schema 10). The repo
// layer reads them; the repos feed of task 0089 does not. PR_FIELDS and
// pullOf are a copy of the retired github plugin's (task 0087 deleted it).

import { execFile as nodeExecFile } from 'node:child_process';
import { repoKey } from '../activity.js';
import { GH_TIMEOUT_MS, runGh } from '../gh.js';
import { reposOwner } from '../repolist.js';

export const PULLS_EVERY_MS = 60 * 1000;
// One answer holds at most this many PRs. A longer search is cut, and the
// feed logs it once.
export const PULLS_PAGE = 50;

// The check roll-up of a commit, as GitHub names it, to a ci word.
const ROLLUP = {
  FAILURE: 'failing',
  ERROR: 'failing',
  PENDING: 'pending',
  EXPECTED: 'pending',
  SUCCESS: 'passing',
};

// The fields of a PR that an item needs.
export const PR_FIELDS =
  'number title url state repository { name owner { login } } headRefName headRepository { name } ' +
  'commits(last: 1) { nodes { commit { statusCheckRollup { state } } } }';

const GRAPHQL = `query($q: String!) {
  search(query: $q, type: ISSUE, first: ${PULLS_PAGE}) {
    issueCount
    nodes { ... on PullRequest { ${PR_FIELDS} } }
  }
}`;

// The search for the open PRs of an owner's repositories. The owner is a
// checked GitHub name (reposOwner), so it cannot add a qualifier.
export const pullsQuery = (owner) => `is:pr is:open user:${owner} archived:false`;

// One PR node of an answer as { repo, number, title, url, ci, headRepo,
// branch }, or null. repo and headRepo are repoKeys.
export function pullOf(node) {
  if (!node || !Number.isInteger(node.number) || typeof node.url !== 'string') return null;
  const name = node.repository?.name;
  if (typeof name !== 'string' || name === '' || !node.url.startsWith('https://')) return null;
  const state = node.commits?.nodes?.[0]?.commit?.statusCheckRollup?.state;
  return {
    repo: repoKey(name),
    number: node.number,
    title: String(node.title ?? '').replace(/\s+/g, ' ').trim() || `#${node.number}`,
    url: node.url,
    ci: ROLLUP[state] ?? 'none',
    headRepo: typeof node.headRepository?.name === 'string' ? repoKey(node.headRepository.name) : null,
    branch: typeof node.headRefName === 'string' && node.headRefName !== '' ? node.headRefName : null,
  };
}

// The PRs of a `gh api graphql` answer: { total, pulls }. Throws an Error
// when the answer is not one.
export function parsePulls(text) {
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

// Asks GitHub for the open PRs of the owner. env holds INBOX_GH (the gh
// program) and INBOX_REPOS_OWNER. execFile is for a test.
export async function fetchPulls({ env = process.env, execFile = nodeExecFile, timeoutMs = GH_TIMEOUT_MS } = {}) {
  const owner = reposOwner(env);
  const gh = env.INBOX_GH || 'gh';
  const text = await runGh(gh, ['api', 'graphql', '-f', `query=${GRAPHQL}`, '-f', `q=${pullsQuery(owner)}`], {
    env,
    execFile,
    timeoutMs,
  });
  return parsePulls(text);
}

// A feed in the shape of repolist.js's. Make one per daemon (pullsFeed) or
// per test: it holds whether it logged a cut answer.
export function createPullsFeed() {
  let cutLogged = false;
  return {
    source: 'open pull requests',
    everyMs: PULLS_EVERY_MS,
    fetch: ({ env, execFile } = {}) => fetchPulls({ env, execFile }),
    // A cut answer (more PRs than it lists) deletes no row. Returns how many
    // rows changed.
    sync(store, { total, pulls }, { log = () => {} } = {}) {
      const complete = total <= pulls.length;
      if (!complete && !cutLogged) {
        cutLogged = true;
        log(`GitHub has ${total} open pull requests; the feed keeps the first ${pulls.length}`);
      }
      if (complete) cutLogged = false;
      return store.replacePulls(pulls, { complete });
    },
  };
}

export const pullsFeed = createPullsFeed();
