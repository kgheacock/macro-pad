import assert from 'node:assert/strict';
import { mkdirSync, utimesSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import {
  EVENT_PAGE_SIZE,
  HALF_LIFE_MS,
  KEEP_MS,
  KINDS,
  WEIGHTS,
  compareRepos,
  fetchEvents,
  gitEvents,
  nextShown,
  orderRepos,
  parseEvents,
  scoreRepos,
} from '../src/activity.js';
import { fetchFeed, syncFeed } from '../src/repolist.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

const DAY = 24 * 60 * 60 * 1000;
const NOW = Date.UTC(2026, 10, 16, 20, 0, 0);

const event = (id, type, repo, extra = {}) => ({
  id: String(id),
  type,
  repo: { name: repo },
  created_at: new Date(NOW - 1000).toISOString(),
  payload: {},
  ...extra,
});

// An execFile stub: answers(args) -> { stdout } or { error }. Records calls.
function stubGh(answers) {
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push({ file, args, options });
    const next = answers(args, calls.length);
    setImmediate(() => callback(next.error ?? null, next.stdout ?? '', next.stderr ?? ''));
  };
  return { calls, execFile };
}

const repoList = (names) =>
  JSON.stringify(
    names.map((name) => ({
      name,
      url: `https://github.com/kgheacock/${name}`,
      isFork: false,
      isArchived: false,
      pushedAt: '2026-11-16T00:00:00Z',
      issues: { totalCount: 0 },
      pullRequests: { totalCount: 0 },
    })),
  );

test('TestNoteActivityUpsert', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  t.after(() => store.close());
  store.noteActivity('macro-pad', 'claude', '3f2a91c0', 1000);
  store.noteActivity('macro-pad', 'claude', '3f2a91c0', 5000);
  // An older time later does not move `at` back.
  store.noteActivity('macro-pad', 'claude', '3f2a91c0', 2000);
  store.noteActivity('macro-pad', 'claude', 'other', 1000);
  assert.deepEqual(store.activityRows(0), [
    { repo: 'macro-pad', kind: 'claude', ref: '3f2a91c0', at: 5000 },
    { repo: 'macro-pad', kind: 'claude', ref: 'other', at: 1000 },
  ]);
  assert.deepEqual(store.activityRows(2000).map((r) => r.ref), ['3f2a91c0']);
});

test('TestParseEvents', () => {
  const events = [
    event(1, 'PushEvent', 'kgheacock/macro-pad'),
    event(2, 'PullRequestEvent', 'kgheacock/Site.io', { payload: { action: 'opened' } }),
    event(3, 'PullRequestEvent', 'kgheacock/macro-pad', { payload: { action: 'closed' } }),
    event(4, 'PullRequestReviewEvent', 'KGheacock/macro-pad', { payload: { action: 'created' } }),
    event(5, 'PushEvent', 'acme/macro-pad'),
    event(6, 'WatchEvent', 'kgheacock/macro-pad'),
    event(7, 'IssueCommentEvent', 'kgheacock/macro-pad'),
    event(8, 'PushEvent', 'kgheacock/bad', { created_at: 'never' }),
    { type: 'PushEvent', repo: { name: 'kgheacock/noid' }, created_at: new Date(NOW).toISOString() },
  ];
  const got = parseEvents(JSON.stringify(events), { owner: 'kgheacock' });
  assert.deepEqual(
    got.map((e) => [e.repo, e.kind, e.ref]),
    [
      ['macro-pad', 'push', '1'],
      ['site-io', 'pr_open', '2'],
      ['macro-pad', 'pr_review', '4'],
    ],
  );
  assert.equal(got[0].at, NOW - 1000);
  assert.throws(() => parseEvents('nope', { owner: 'x' }), /not JSON/);
  assert.throws(() => parseEvents('{}', { owner: 'x' }), /not a list/);
});

test('TestEventsUseSignedInLogin', async () => {
  const { calls, execFile } = stubGh((args) =>
    args[1] === 'user' ? { stdout: 'keith-h\n' } : { stdout: JSON.stringify([event(1, 'PushEvent', 'acme/web')]) },
  );
  // The owner is acme; the events are of the signed-in login, not of acme.
  const got = await fetchEvents({ env: { INBOX_GH: '/bin/gh' }, owner: 'acme', execFile });
  assert.deepEqual(calls.map((c) => c.args), [
    ['api', 'user', '--jq', '.login'],
    ['api', 'users/keith-h/events?per_page=100'],
  ]);
  assert.equal(calls[0].file, '/bin/gh');
  assert.equal(calls[1].options.shell, false);
  assert.deepEqual(got.map((e) => [e.repo, e.kind]), [['web', 'push']]);
  // No call names the owner as a user.
  assert.ok(!calls.some((c) => c.args.join(' ').includes('users/acme')));

  // A login that is not a login is refused before a second call.
  const bad = stubGh(() => ({ stdout: '../x\n' }));
  await assert.rejects(fetchEvents({ owner: 'acme', execFile: bad.execFile }), /no login/);
  assert.equal(bad.calls.length, 1);
});

test('TestEventsPages', async () => {
  const full = JSON.stringify(Array.from({ length: EVENT_PAGE_SIZE }, (_, i) => event(i, 'PushEvent', 'acme/web')));
  const short = JSON.stringify([event(900, 'PushEvent', 'acme/web')]);
  const { calls, execFile } = stubGh((args) => {
    if (args[1] === 'user') return { stdout: 'me\n' };
    return { stdout: args[1].includes('page=3') ? short : full };
  });
  const got = await fetchEvents({ owner: 'acme', pages: 3, execFile });
  assert.equal(got.length, 2 * EVENT_PAGE_SIZE + 1);
  assert.deepEqual(calls.slice(1).map((c) => c.args[1]), [
    'users/me/events?per_page=100',
    'users/me/events?per_page=100&page=2',
    'users/me/events?per_page=100&page=3',
  ]);
  // A short first page stops the reading.
  const one = stubGh((args) => (args[1] === 'user' ? { stdout: 'me\n' } : { stdout: short }));
  await fetchEvents({ owner: 'acme', pages: 3, execFile: one.execFile });
  assert.equal(one.calls.length, 2);
});

test('TestScoreDecaysByHalfLife', () => {
  const row = (repo, kind, age) => ({ repo, kind, at: NOW - age });
  const scores = scoreRepos(
    [row('now', 'push', 0), row('old', 'push', 3 * DAY), row('rev', 'pr_review', 2 * DAY), row('git', 'git', 2 * DAY)],
    NOW,
  );
  assert.equal(HALF_LIFE_MS, 3 * DAY);
  assert.equal(scores.get('now').score, 3);
  assert.equal(scores.get('old').score, 1.5);
  assert.equal(scores.get('old').score * 2, scores.get('now').score);
  assert.ok(scores.get('rev').score > scores.get('git').score);
  // Two events add, and `last` is the newest time.
  const both = scoreRepos([row('a', 'claude', 0), row('a', 'code', 3 * DAY)], NOW).get('a');
  assert.equal(both.score, 3);
  assert.equal(both.last, NOW);
  // The weights of the spec.
  assert.deepEqual(WEIGHTS, { pr_review: 3, pr_open: 3, push: 3, claude: 2, code: 2, git: 1 });
  assert.deepEqual(KINDS.toSorted(), Object.keys(WEIGHTS).toSorted());
  // A time in the future counts as now; an unknown kind counts nothing.
  assert.equal(scoreRepos([row('f', 'push', -DAY)], NOW).get('f').score, 3);
  assert.equal(scoreRepos([row('u', 'bogus', 0)], NOW).has('u'), false);
});

test('TestOrderReposNoActivityLast', () => {
  const repos = ['zeta', 'alpha', 'quiet', 'Beta.io', 'mid'].map((name) => ({ name, key: name.replace(/\./g, '-').toLowerCase() }));
  const scores = scoreRepos(
    [
      { repo: 'mid', kind: 'git', at: NOW - DAY },
      { repo: 'zeta', kind: 'push', at: NOW - DAY },
      { repo: 'beta-io', kind: 'push', at: NOW - DAY },
      // A repository that is not in the list scores nothing for it.
      { repo: 'gone', kind: 'push', at: NOW },
    ],
    NOW,
  );
  const names = (list) => orderRepos(list, scores).map((r) => r.name);
  // Same score: the newer event first, then the name.
  assert.deepEqual(names(repos), ['Beta.io', 'zeta', 'mid', 'alpha', 'quiet']);
  // The same for any input order.
  const shuffles = [[...repos].reverse(), [repos[2], repos[0], repos[4], repos[1], repos[3]]];
  for (const list of shuffles) assert.deepEqual(names(list), names(repos));
  // No events at all: by name.
  assert.deepEqual(orderRepos(repos).map((r) => r.name), ['Beta.io', 'alpha', 'mid', 'quiet', 'zeta']);
  // The input is not changed.
  assert.equal(repos[0].name, 'zeta');
  // compareRepos fits before(a, b) of render.js, with no event as 0.
  assert.ok(compareRepos({ name: 'a' }, { name: 'b' }) < 0);
  assert.ok(compareRepos({ name: 'a', score: 1 }, { name: 'b' }) < 0);
  assert.ok(compareRepos({ name: 'a', score: 1, last: 1 }, { name: 'b', score: 1, last: 2 }) > 0);
});

test('TestNextShownFreezesWhileOpen', () => {
  const shown = ['b', 'a', 'c'];
  // Open: shown stays in place, a gone name stays, new names go at the end.
  assert.deepEqual(nextShown({ shown, wanted: ['d', 'a', 'e'], open: true, max: 10 }), ['b', 'a', 'c', 'd', 'e']);
  assert.deepEqual(nextShown({ shown, wanted: ['d', 'e'], open: true, max: 4 }), ['b', 'a', 'c', 'd']);
  assert.deepEqual(nextShown({ shown, wanted: ['b'], open: true, max: 2 }), ['b', 'a', 'c']);
  assert.deepEqual(shown, ['b', 'a', 'c']);
  // Closed: the wanted order.
  assert.deepEqual(nextShown({ shown, wanted: ['d', 'a', 'e'], open: false, max: 10 }), ['d', 'a', 'e']);
  assert.deepEqual(nextShown({ shown, wanted: ['d', 'a', 'e'], open: false, max: 2 }), ['d', 'a']);
});

test('TestPruneActivity', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  t.after(() => store.close());
  assert.equal(KEEP_MS, 30 * DAY);
  store.noteActivity('a', 'push', '1', NOW - 31 * DAY);
  store.noteActivity('a', 'push', '2', NOW - 29 * DAY);
  store.noteActivity('b', 'git', '2026-10-01', NOW - 60 * DAY);
  assert.equal(store.pruneActivity(NOW - KEEP_MS), 2);
  assert.deepEqual(store.activityRows(0).map((r) => r.ref), ['2']);
  // syncFeed prunes too, and sets the scores.
  store.noteActivity('a', 'push', '3', NOW - 40 * DAY);
  const answer = { owner: 'kgheacock', repos: [], cut: false, events: [], git: [] };
  syncFeed(store, answer, NOW);
  assert.deepEqual(store.activityRows(0).map((r) => r.ref), ['2']);
  assert.ok(Math.abs(answer.scores.get('a').score - 3 * 0.5 ** (29 / 3)) < 1e-8);
});

test('TestEventsFailureKeepsLedger', async (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  t.after(() => store.close());
  store.noteActivity('macro-pad', 'push', '1', Date.now() - DAY);
  const before = store.activityRows(0);
  const logs = [];
  // gh lists the repositories, and fails on api.
  const { execFile } = stubGh((args) =>
    args[0] === 'repo' ? { stdout: repoList(['macro-pad', 'web']) } : { error: Object.assign(new Error('x'), { code: 1 }), stderr: 'gh: not logged in\n' },
  );
  const answer = await fetchFeed({ env: {}, store, execFile, log: (l) => logs.push(l) });
  assert.deepEqual(answer.repos.map((r) => r.name), ['macro-pad', 'web']);
  assert.deepEqual(answer.events, []);
  assert.equal(logs.length, 1);
  assert.match(logs[0], /cannot read GitHub events: .*not logged in/);
  syncFeed(store, answer);
  assert.deepEqual(store.activityRows(0), before);
  assert.equal(answer.scores.get('macro-pad').score > 0, true);
  // The same failure is not logged again.
  await fetchFeed({ env: {}, store, execFile, log: (l) => logs.push(l) });
  assert.equal(logs.length, 1);
});

test('TestFeedAsksLoginOnce', async (t) => {
  // Task 0090: the feed asks gh for the login once, not every run, and asks
  // again after an events read fails.
  const { db } = scratch(t);
  const store = openStore(db);
  t.after(() => store.close());
  let failEvents = false;
  const { calls, execFile } = stubGh((args) => {
    if (args[0] === 'repo') return { stdout: repoList(['web']) };
    if (args[1] === 'user') return { stdout: 'kgheacock\n' };
    if (failEvents) return { error: Object.assign(new Error('x'), { code: 1 }), stderr: 'gh: HTTP 401\n' };
    return { stdout: '[]' };
  });
  const userCalls = () => calls.filter((c) => c.args[0] === 'api' && c.args[1] === 'user').length;
  await fetchFeed({ env: {}, store, execFile });
  await fetchFeed({ env: {}, store, execFile });
  assert.equal(userCalls(), 1);
  failEvents = true;
  await fetchFeed({ env: {}, store, execFile, log: () => {} });
  failEvents = false;
  await fetchFeed({ env: {}, store, execFile });
  assert.equal(userCalls(), 2);
});

test('TestFeedWritesEventsAndGitLog', async (t) => {
  const sc = scratch(t);
  const store = openStore(sc.db);
  t.after(() => store.close());
  const root = path.join(sc.root, 'github.com', 'kgheacock');
  const at = Date.now() - 2 * 60 * 60 * 1000;
  // web: a commit wrote .git/logs/HEAD. cold: only `git status` touched .git/index.
  for (const [dir, files] of [['web', ['logs/HEAD', 'index']], ['cold', ['index']]]) {
    mkdirSync(path.join(root, dir, '.git', 'logs'), { recursive: true });
    for (const file of files) {
      const p = path.join(root, dir, '.git', file);
      writeFileSync(p, '');
      utimesSync(p, at / 1000, at / 1000);
    }
  }
  const { calls, execFile } = stubGh((args) => {
    if (args[0] === 'repo') return { stdout: repoList(['web', 'cold']) };
    if (args[1] === 'user') return { stdout: 'kgheacock\n' };
    return { stdout: JSON.stringify([event(7, 'PushEvent', 'kgheacock/web', { created_at: new Date().toISOString() })]) };
  });
  const answer = await fetchFeed({ env: { INBOX_APPS_ROOT: root }, store, execFile });
  // The first run, with no gh event in the ledger, reads one page here (a short page ends it).
  assert.equal(calls.filter((c) => c.args[0] === 'api' && c.args[1] !== 'user').length, 1);
  syncFeed(store, answer);
  const rows = store.activityRows(0);
  assert.deepEqual(rows.map((r) => [r.repo, r.kind]).sort(), [['web', 'git'], ['web', 'push']]);
  assert.ok(Math.abs(rows.find((r) => r.kind === 'git').at - at) <= 2);
  assert.ok(answer.scores.get('web').score > (answer.scores.get('cold')?.score ?? 0));
  assert.equal(answer.scores.has('cold'), false);
  // Without a root, no git event.
  assert.deepEqual(gitEvents(path.join(sc.root, 'missing')), []);
});
