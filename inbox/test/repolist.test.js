import assert from 'node:assert/strict';
import { mkdirSync, utimesSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import {
  COUNTS_BATCH,
  CONTRIBUTED_PAGES,
  createCountsState,
  countsEveryMs,
  countsQuery,
  feed,
  fetchContributed,
  fetchFeed,
  parseContributed,
  parseCounts,
  rankCandidates,
  repoKey,
  reposOwner,
  syncFeed,
} from '../src/repolist.js';
import { REPO_MAX_CHILDREN } from '../src/render.js';
import { openStore } from '../src/store.js';
import { askedNames, contributedPage, fakeGitHub, flag, isContributed, isCounts, isGraphql } from '../testlib/github.js';
import { scratch } from '../testlib/helpers.js';

const MIN = 60 * 1000;
const NOW = Date.UTC(2026, 10, 20, 20, 0, 0);

// An execFile stub: answers(args) -> { stdout, error, stderr }. The login and
// the events answer unless `answers` does. Records calls.
function stubGh(answers) {
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push({ file, args, options });
    let got = answers(args, calls.length);
    if (!got) got = args[1] === 'user' ? { stdout: 'kgheacock\n' } : { stdout: '[]' };
    setImmediate(() => callback(got.error ?? null, got.stdout ?? '', got.stderr ?? ''));
  };
  return { calls, execFile };
}

const countCalls = (calls) => calls.filter((c) => isCounts(c.args));

function storeFor(t) {
  const sc = scratch(t);
  const store = openStore(sc.db);
  t.after(() => store.close());
  return { sc, store };
}

// A repository folder under root whose .git/logs/HEAD changed at `at`.
function clone(root, name, at) {
  const dir = path.join(root, name, '.git', 'logs');
  mkdirSync(dir, { recursive: true });
  writeFileSync(path.join(dir, 'HEAD'), '');
  utimesSync(path.join(dir, 'HEAD'), at / 1000, at / 1000);
}

test('TestReposOwnerRejected', async () => {
  for (const owner of ['-x', 'a b', '--json', 'a/b', 'a;b', '-', 'x'.repeat(40)]) {
    const { calls, execFile } = stubGh(() => null);
    await assert.rejects(fetchFeed({ env: { INBOX_REPOS_OWNER: owner }, execFile }), /not a GitHub user or organisation name/, owner);
    assert.equal(calls.length, 0, `a process ran for "${owner}"`);
  }
  assert.equal(reposOwner({}), 'kgheacock');
  assert.equal(reposOwner({ INBOX_REPOS_OWNER: '' }), 'kgheacock');
  assert.equal(reposOwner({ INBOX_REPOS_OWNER: 'Acme-1' }), 'Acme-1');
});

test('TestContributedQuery', async () => {
  // Two pages, then the end; a repository of another owner stays out.
  const { calls, execFile } = stubGh((args) => {
    if (!isContributed(args)) return null;
    const after = flag(args, 'after');
    if (after === undefined) return { stdout: contributedPage(['web', 'other/lib'], { owner: 'ExampleOrg', next: 'c1' }) };
    return { stdout: contributedPage(['example-service'], { owner: 'exampleorg' }) };
  });
  const got = await fetchContributed({ env: { INBOX_GH: '/opt/homebrew/bin/gh' }, owner: 'ExampleOrg', execFile });
  assert.deepEqual(got.map((r) => r.name), ['web', 'example-service']);
  assert.equal(calls.length, 2);
  assert.equal(calls[0].file, '/opt/homebrew/bin/gh');
  assert.deepEqual(calls[0].args.slice(0, 3), ['api', 'graphql', '-f']);
  assert.match(flag(calls[0].args, 'query'), /contributionTypes: \[COMMIT, PULL_REQUEST, PULL_REQUEST_REVIEW, ISSUE\]/);
  assert.match(flag(calls[0].args, 'query'), /includeUserRepositories: true/);
  assert.match(flag(calls[0].args, 'query'), /orderBy: \{ field: PUSHED_AT, direction: DESC \}/);
  assert.equal(flag(calls[1].args, 'after'), 'c1');
  assert.equal(calls[0].options.shell, false);

  // It stops after CONTRIBUTED_PAGES pages, however many GitHub has.
  const endless = stubGh((args) => ({ stdout: contributedPage(['a'], { next: 'more' }) }));
  await fetchContributed({ env: {}, owner: 'kgheacock', execFile: endless.execFile });
  assert.equal(endless.calls.length, CONTRIBUTED_PAGES);

  // An answer in another shape is refused.
  assert.throws(() => parseContributed('not json', { owner: 'x' }), /not JSON/);
  assert.throws(() => parseContributed('{"errors":[{"message":"Bad credentials"}]}', { owner: 'x' }), /GitHub said: Bad credentials/);
  assert.throws(() => parseContributed('{"data":{"viewer":{}}}', { owner: 'x' }), /no repositoriesContributedTo/);
});

test('TestCountsQueryUsesVariables', () => {
  const q = countsQuery(['a', 'b.c']);
  assert.match(q, /^query\(\$o: String!, \$n0: String!, \$n1: String!\)/);
  assert.match(q, /r1: repository\(owner: \$o, name: \$n1\) \{ \.\.\.R \}/);
  assert.match(q, /issues\(states: OPEN\) \{ totalCount \}/);
  assert.match(q, /pullRequests\(states: OPEN\) \{ totalCount \}/);
  // No name is part of the query text.
  assert.ok(!q.includes('b.c'));

  const args = ['api', 'graphql', '-f', `query=${q}`, '-f', 'o=kgheacock', '-f', 'n0=Web', '-f', 'n1=gone'];
  const got = parseCounts(fakeGitHub({ repos: { web: { issues: 2, prs: 3, fork: true } } })(args).stdout, ['Web', 'gone']);
  assert.deepEqual(got.get('Web'), {
    name: 'web',
    key: 'web',
    url: 'https://github.com/kgheacock/web',
    fork: true,
    archived: false,
    pushedAt: '2026-11-16T00:00:00Z',
    issues: 2,
    prs: 3,
  });
  assert.equal(got.get('gone'), null);
  assert.throws(() => parseCounts('{"data":{"r0":{"name":"x"}}}', ['x']), /no issue count for x/);
  assert.throws(() => parseCounts('{"errors":[{"message":"rate limited"}]}', ['x']), /GitHub said: rate limited/);
});

test('TestCountsAtMost128In50s', async (t) => {
  // DoD-4: 200 candidates; counts are asked for the first 128, 50 at a time.
  const { store } = storeFor(t);
  const names = Array.from({ length: 200 }, (_, i) => `r${String(i).padStart(3, '0')}`);
  const github = fakeGitHub({ contributed: names });
  const { calls, execFile } = stubGh((args) => (isGraphql(args) ? github(args) : null));
  const answer = await fetchFeed({ env: {}, store, execFile, now: NOW, counts: createCountsState() });
  const batches = countCalls(calls).map((c) => askedNames(c.args).length);
  assert.equal(REPO_MAX_CHILDREN, 128);
  assert.equal(COUNTS_BATCH, 50);
  assert.deepEqual(batches, [50, 50, 28]);
  assert.equal(new Set(countCalls(calls).flatMap((c) => askedNames(c.args))).size, 128);
  assert.equal(answer.repos.length, 128);
  // The owner goes in as a variable.
  assert.ok(countCalls(calls).every((c) => flag(c.args, 'o') === 'kgheacock'));
  // No call lists the repositories of the owner.
  assert.ok(!calls.some((c) => c.args[0] === 'repo'));
});

test('TestRankByLedgerThenPush', () => {
  // The ledger's score first; a repository with no score by its newest push.
  const contributed = [
    { name: 'old', key: 'old', pushedAt: '2026-01-01T00:00:00Z' },
    { name: 'new', key: 'new', pushedAt: '2026-11-01T00:00:00Z' },
  ];
  const rows = [{ repo: 'keithheacock-com', kind: 'claude', ref: 's1', at: NOW - MIN }];
  const got = rankCandidates({ contributed, rows, known: new Map([['keithheacock-com', 'keithheacock.com']]), now: NOW });
  assert.deepEqual(got.map((c) => [c.name, c.from]), [['keithheacock.com', 'ledger'], ['new', 'contributed'], ['old', 'contributed']]);
  // A ledger key with no known name is asked by the key.
  assert.deepEqual(rankCandidates({ rows, now: NOW }).map((c) => c.name), ['keithheacock-com']);
  assert.equal(rankCandidates({ contributed, max: 1, now: NOW }).length, 1);
});

test('TestCloneOnlyCandidate', async (t) => {
  // DoD-5: a local clone with no contribution is a candidate and is shown.
  const { sc, store } = storeFor(t);
  const root = path.join(sc.root, 'github.com', 'kgheacock');
  clone(root, 'Local.Only', Date.now() - 60 * MIN);
  const github = fakeGitHub({ contributed: [], repos: { 'Local.Only': { issues: 1, prs: 0 } } });
  const { calls, execFile } = stubGh((args) => (isGraphql(args) ? github(args) : null));
  const answer = await fetchFeed({ env: { INBOX_APPS_ROOT: root }, store, execFile, counts: createCountsState() });
  assert.deepEqual(countCalls(calls).map((c) => askedNames(c.args)), [['Local.Only']]);
  assert.deepEqual(answer.repos.map((r) => [r.name, r.key, r.issues]), [['Local.Only', 'local-only', 1]]);
});

test('TestCountsFailureKeepsStored', async (t) => {
  // DoD-6: a failed counts query keeps the stored counts and logs one line.
  const { store } = storeFor(t);
  const kept = { name: 'web', key: 'web', url: 'u', fork: false, archived: false, pushedAt: null, issues: 4, prs: 2 };
  store.replaceRepos('kgheacock', [kept]);
  const { execFile } = stubGh((args) => {
    if (isContributed(args)) return { stdout: contributedPage(['web', 'fresh']) };
    if (isCounts(args)) return { error: Object.assign(new Error('x'), { code: 1 }), stderr: 'gh: HTTP 502\n' };
    return null;
  });
  const logs = [];
  const counts = createCountsState();
  const answer = await fetchFeed({ env: {}, store, execFile, log: (l) => logs.push(l), now: NOW, counts });
  // The stored row stays; a contributed repository with no row shows with no counts.
  assert.deepEqual(answer.repos.map((r) => [r.name, r.issues, r.prs]), [['fresh', 0, 0], ['web', 4, 2]]);
  assert.deepEqual(answer.repos.find((r) => r.name === 'web'), kept);
  assert.deepEqual(logs, ['cannot read the counts of GitHub repositories: gh failed: gh: HTTP 502']);
  syncFeed(store, answer, NOW);
  assert.deepEqual(store.listRepos('kgheacock').find((r) => r.name === 'web'), kept);
  // The same failure is not logged again.
  await fetchFeed({ env: {}, store, execFile, log: (l) => logs.push(l), now: NOW + MIN, counts });
  assert.equal(logs.length, 1);
});

test('TestCountsInterval', async (t) => {
  const { store } = storeFor(t);
  let contributed = ['web', 'api'];
  let issues = 1;
  const { calls, execFile } = stubGh((args) =>
    isGraphql(args) ? fakeGitHub({ contributed, repos: { web: { issues }, api: { issues } } })(args) : null,
  );
  const counts = createCountsState();
  const run = async (now) => {
    const answer = await fetchFeed({ env: {}, store, execFile, now, counts });
    syncFeed(store, answer, now);
    return answer;
  };
  await run(NOW);
  assert.deepEqual(countCalls(calls).map((c) => askedNames(c.args).sort()), [['api', 'web']]);
  // A minute later: every shown repository has a row, so no counts are asked.
  issues = 9;
  let answer = await run(NOW + MIN);
  assert.equal(countCalls(calls).length, 1);
  assert.deepEqual(answer.repos.map((r) => r.issues), [1, 1]);
  // A new candidate is asked alone.
  contributed = ['web', 'api', 'new'];
  answer = await run(NOW + 2 * MIN);
  assert.deepEqual(askedNames(countCalls(calls).at(-1).args), ['new']);
  // After the interval, all of them.
  answer = await run(NOW + 5 * MIN);
  assert.deepEqual(askedNames(countCalls(calls).at(-1).args).sort(), ['api', 'new', 'web']);
  assert.deepEqual(answer.repos.map((r) => [r.name, r.issues]), [['api', 9], ['new', 0], ['web', 9]]);

  assert.equal(countsEveryMs({}), 5 * MIN);
  assert.equal(countsEveryMs({ INBOX_REPOS_COUNTS_EVERY: '600' }), 10 * MIN);
  assert.equal(countsEveryMs({ INBOX_REPOS_COUNTS_EVERY: '1' }), 30 * 1000);
  assert.equal(countsEveryMs({ INBOX_REPOS_COUNTS_EVERY: 'x' }), 5 * MIN);
});

test('TestMissingArchivedAndLeftDropped', async (t) => {
  const { store } = storeFor(t);
  const gone = { name: 'gone', key: 'gone', url: 'u', fork: false, archived: false, pushedAt: null, issues: 0, prs: 0 };
  store.replaceRepos('kgheacock', [gone]);
  // gone: in the ledger, but GitHub no longer has it. old: archived.
  store.noteActivity('gone', 'claude', 's1', Date.now() - MIN);
  const github = fakeGitHub({ contributed: ['web', 'old'], repos: { old: { archived: true } } });
  const { calls, execFile } = stubGh((args) => (isGraphql(args) ? github(args) : null));
  let answer = await fetchFeed({ env: {}, store, execFile, counts: createCountsState() });
  assert.deepEqual(answer.repos.map((r) => r.name), ['web']);
  syncFeed(store, answer);
  assert.deepEqual(store.listRepos('kgheacock').map((r) => r.name), ['web']);
  // A missing repository is not asked again before the next full read.
  const asked = calls.length;
  const counts = createCountsState();
  await fetchFeed({ env: {}, store, execFile, counts, now: NOW });
  const after = calls.length;
  syncFeed(store, await fetchFeed({ env: {}, store, execFile, counts, now: NOW + MIN }));
  assert.equal(calls.slice(after).filter((c) => isCounts(c.args)).length, 0);
  assert.ok(after > asked);
  // INBOX_REPOS_ARCHIVED=1 keeps the archived ones in.
  answer = await fetchFeed({ env: { INBOX_REPOS_ARCHIVED: '1' }, store, execFile, counts: createCountsState() });
  assert.deepEqual(answer.repos.map((r) => [r.name, r.archived]), [['old', true], ['web', false]]);
});

test('TestLedgerKeyAskedByStoredName', async (t) => {
  // A ledger key loses the dot of its name; the stored row gives it back.
  const { store } = storeFor(t);
  const row = { name: 'keithheacock.com', key: 'keithheacock-com', url: 'u', fork: false, archived: false, pushedAt: null, issues: 0, prs: 0 };
  store.replaceRepos('kgheacock', [row]);
  store.noteActivity('keithheacock-com', 'claude', 's1', Date.now() - MIN);
  const github = fakeGitHub({ repos: { 'keithheacock.com': { prs: 2 } } });
  const { calls, execFile } = stubGh((args) => (isGraphql(args) ? github(args) : null));
  const answer = await fetchFeed({ env: {}, store, execFile, counts: createCountsState() });
  assert.deepEqual(askedNames(countCalls(calls)[0].args), ['keithheacock.com']);
  assert.deepEqual(answer.repos.map((r) => [r.name, r.prs]), [['keithheacock.com', 2]]);
});

test('TestRepoKey', () => {
  assert.equal(repoKey('keithheacock.com'), 'keithheacock-com');
  assert.equal(repoKey('Macro-Pad'), 'macro-pad');
});

test('TestReposFeedFails', async () => {
  // A failure of the candidates fails the run.
  const missing = Object.assign(new Error('spawn gh ENOENT'), { code: 'ENOENT' });
  const none = stubGh(() => ({ error: missing }));
  await assert.rejects(fetchFeed({ env: {}, execFile: none.execFile, counts: createCountsState() }), /no gh; install the GitHub CLI/);
  const auth = stubGh((args) => (isContributed(args) ? { error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'gh auth login\nmore' } : null));
  await assert.rejects(fetchFeed({ env: {}, execFile: auth.execFile, counts: createCountsState() }), /gh failed: gh auth login$/);
  assert.equal(feed.source, 'GitHub repositories');
  assert.equal(feed.everyMs, 60_000);
});
