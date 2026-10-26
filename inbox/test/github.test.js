import assert from 'node:assert/strict';
import test from 'node:test';
import { emojiFor } from '../src/emoji.js';
import github, {
  ACTIVE_MS,
  DEFAULT_QUERY,
  fetchPulls,
  parseAnswer,
  parseNotifications,
  syncPulls,
} from '../src/plugins/github.js';
import ai from '../src/plugins/ai.js';
import { createRenderer } from '../src/render.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

// One PR as GitHub's search answers it. state null: the commit has no checks.
const pr = (repo, number, state, title = `PR ${number}`) => ({
  number,
  title,
  url: `https://github.com/kgheacock/${repo}/pull/${number}`,
  repository: { name: repo, owner: { login: 'kgheacock' } },
  headRefName: `pr-${number}`,
  headRepository: { name: repo },
  commits: { nodes: [{ commit: { statusCheckRollup: state === null ? null : { state } } }] },
});
const answer = (nodes, issueCount = nodes.length) => JSON.stringify({ data: { search: { issueCount, nodes } } });

function storeFor(t) {
  const { db } = scratch(t);
  const store = openStore(db, undefined, { plugin: 'github' });
  t.after(() => store.close());
  return store;
}

const keys = (messages) =>
  Object.fromEntries(messages.filter((m) => m.setLayerKey).map((m) => [m.setLayerKey.id, m.setLayerKey]));
const order = (messages) => messages.filter((m) => m.setLayerKey?.parent).map((m) => m.setLayerKey.id);
const rowsOf = (store, now) => store.recent(now - ACTIVE_MS);

test('TestGithubFeedWrites', (t) => {
  const store = storeFor(t);
  const now = 1_000_000;
  const four = parseAnswer(
    answer([
      pr('macro-pad', 97, 'FAILURE', 'One inbox daemon'),
      pr('macro-pad', 98, 'PENDING'),
      pr('dotfiles', 3, 'SUCCESS'),
      pr('site.github.io', 1, null),
    ]),
  );
  assert.equal(syncPulls(store, four, { now }), 4);
  assert.deepEqual(
    store.list().map((m) => [m.channel, m.status, m.body, m.here]),
    [
      ['kgheacock.macro-pad.97', 'failing', 'One inbox daemon', 'url:https://github.com/kgheacock/macro-pad/pull/97'],
      ['kgheacock.macro-pad.98', 'pending', 'PR 98', 'url:https://github.com/kgheacock/macro-pad/pull/98'],
      ['kgheacock.dotfiles.3', 'passing', 'PR 3', 'url:https://github.com/kgheacock/dotfiles/pull/3'],
      // A dot in a name becomes -.
      ['kgheacock.site-github-io.1', 'none', 'PR 1', 'url:https://github.com/kgheacock/site.github.io/pull/1'],
    ],
  );
  // The same answer again writes nothing; a change writes one.
  assert.equal(syncPulls(store, four, { now: now + 1 }), 0);
  const fixed = parseAnswer(
    answer([pr('macro-pad', 97, 'SUCCESS'), pr('macro-pad', 98, 'PENDING'), pr('dotfiles', 3, 'SUCCESS'), pr('site.github.io', 1, null)]),
  );
  assert.equal(syncPulls(store, fixed, { now: now + 2 }), 1);
  assert.equal(store.list().at(-1).status, 'passing');
});

test('TestGithubFeedCloses', (t) => {
  const store = storeFor(t);
  const now = 1_000_000;
  syncPulls(store, parseAnswer(answer([pr('macro-pad', 97, 'FAILURE'), pr('macro-pad', 98, 'SUCCESS')])), { now });

  // A cut answer closes nothing.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS')], 51)), { now: now + 1 }), 0);
  // 97 is gone from a whole answer: a seen closed message, and its key goes.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS')])), { now: now + 2 }), 1);
  const closed = store.list({ includeSeen: true }).at(-1);
  assert.deepEqual([closed.channel, closed.status, closed.seen_at !== null], ['kgheacock.macro-pad.97', 'closed', true]);
  const messages = createRenderer({ rules: github.rules }).plan({ unseen: rowsOf(store, now + 2), active: null });
  assert.deepEqual(order(messages), ['kgheacock.macro-pad.98']);
  // It stays closed: the next answer writes nothing.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS')])), { now: now + 3 }), 0);
  // Reopened, it comes back.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 97, 'PENDING'), pr('macro-pad', 98, 'SUCCESS')])), { now: now + 4 }), 1);
});

test('TestGithubKeys', (t) => {
  const store = storeFor(t);
  const now = 1_000_000;
  syncPulls(
    store,
    parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS'), pr('macro-pad', 97, 'FAILURE'), pr('a-very-long-repository-name', 1234, 'PENDING')])),
    { now },
  );
  const messages = createRenderer({ rules: github.rules }).plan({ unseen: rowsOf(store, now), active: null });
  const k = keys(messages);
  assert.deepEqual(k['github-inbox'], { id: 'github-inbox', status: 'ERROR', count: 1, iconRef: 'github.png' });
  // Failing first, then pending, then passing.
  assert.deepEqual(order(messages), ['kgheacock.macro-pad.97', 'kgheacock.a-very-long-repository-name.1234', 'kgheacock.macro-pad.98']);
  const red = k['kgheacock.macro-pad.97'];
  assert.deepEqual([red.status, red.count, red.parent], ['ERROR', 1, 'github-inbox']);
  // REPO #NUMBER, over the repository's emoji: both macro-pad PRs share it.
  assert.equal(red.iconText, 'macro-pad #97');
  assert.deepEqual(red.iconEmoji, [emojiFor('repo:macro-pad').iconRef]);
  assert.deepEqual(k['kgheacock.macro-pad.98'].iconEmoji, red.iconEmoji);
  assert.equal(k['kgheacock.macro-pad.98'].status, 'DONE');
  // A long name is cut, and the number stays.
  const long = k['kgheacock.a-very-long-repository-name.1234'];
  assert.equal(long.iconText, 'a-very-lo… #1234');
  assert.equal(long.status, 'RUNNING');

  // Only pending ones: blue, no pip. Seen: the pip goes, the colour stays.
  const pending = storeFor(t);
  syncPulls(pending, parseAnswer(answer([pr('macro-pad', 98, 'PENDING')])), { now });
  pending.markSeen('kgheacock.macro-pad.98', Number.MAX_SAFE_INTEGER, now);
  const p = keys(createRenderer({ rules: github.rules }).plan({ unseen: rowsOf(pending, now), active: null }));
  assert.deepEqual([p['github-inbox'].status, p['github-inbox'].count], ['RUNNING', 0]);
  assert.deepEqual([p['kgheacock.macro-pad.98'].status, p['kgheacock.macro-pad.98'].count], ['RUNNING', 0]);
});

test('TestGithubSessionEmoji', (t) => {
  // Task 0081: a PR shows its repository's emoji, and the emoji of the Claude
  // session that worked on its branch, as the session's own key does.
  const store = storeFor(t);
  const now = 1_000_000;
  const both = parseAnswer(answer([pr('docreviewonpremnodeweb', 50, 'SUCCESS'), pr('docreviewonpremnodeweb', 51, 'SUCCESS')]));
  assert.deepEqual([both.pulls[0].repo, both.pulls[0].branch], ['docreviewonpremnodeweb', 'pr-50']);
  syncPulls(store, both, { now });
  store.markSeen('kgheacock.docreviewonpremnodeweb.50', Number.MAX_SAFE_INTEGER, now);
  const renderer = createRenderer({ rules: github.rules });
  let k = keys(renderer.plan({ unseen: rowsOf(store, now), active: null }));
  const repo = emojiFor('repo:docreviewonpremnodeweb').iconRef;
  assert.deepEqual(k['kgheacock.docreviewonpremnodeweb.50'].iconEmoji, [repo]);

  // The session links the branch after the PR was first written: the next
  // run writes the link, seen, since only the session changed.
  store.linkBranch('docreviewonpremnodeweb', 'pr-50', '08709453', now);
  assert.equal(syncPulls(store, both, { now: now + 1 }), 1);
  assert.equal(syncPulls(store, both, { now: now + 2 }), 0);
  k = keys(renderer.plan({ unseen: rowsOf(store, now + 2), active: null }));
  const pr50 = k['kgheacock.docreviewonpremnodeweb.50'];
  assert.equal(pr50.count, 0);
  assert.deepEqual(pr50.iconEmoji, [repo, emojiFor('session:08709453').iconRef]);
  // The other PR of the repository keeps the repository's emoji alone.
  assert.deepEqual(k['kgheacock.docreviewonpremnodeweb.51'].iconEmoji, [repo]);

  // The session's own key on the ai plugin shows the same two emoji.
  const session = createRenderer({ rules: ai.rules }).plan({
    unseen: [{ channel: 'github.com.OnePayPal.docreviewonpremnodeweb.08709453', count: 1, newestId: 1, createdAt: 1, byStatus: { done: 1 } }],
    active: null,
  })[1].setLayerKey;
  assert.deepEqual(session.iconEmoji, pr50.iconEmoji);
});

// An execFile stub that answers each call in turn.
function stubGh(results) {
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push({ file, args, options });
    const next = results[Math.min(calls.length - 1, results.length - 1)];
    setImmediate(() => callback(next.error ?? null, next.stdout ?? '', next.stderr ?? ''));
  };
  return { calls, execFile };
}

test('TestGithubFetch', async () => {
  const search = { stdout: answer([pr('macro-pad', 97, 'ERROR')]) };
  const { calls, execFile } = stubGh([search, { stdout: '[]' }, search]);
  const got = await fetchPulls({ env: { INBOX_GH: '/opt/homebrew/bin/gh' }, execFile });
  assert.deepEqual(got.pulls.map((p) => [p.channel, p.status]), [['kgheacock.macro-pad.97', 'failing']]);
  assert.equal(calls[0].file, '/opt/homebrew/bin/gh');
  assert.deepEqual(calls[0].args.slice(0, 2), ['api', 'graphql']);
  assert.ok(calls[0].args.includes(`q=${DEFAULT_QUERY}`), calls[0].args.join(' '));
  assert.equal(calls[0].options.shell, false);
  // Then the notifications, for watched PRs; none, so nothing more.
  assert.deepEqual(calls[1].args, ['api', 'notifications?all=true&per_page=50']);
  assert.equal(calls.length, 2);
  // The query can be set, and INBOX_GITHUB_WATCHED=0 leaves out the notifications.
  await fetchPulls({ env: { INBOX_GITHUB_QUERY: 'is:pr is:open review-requested:@me', INBOX_GITHUB_WATCHED: '0' }, execFile });
  assert.equal(calls[2].file, 'gh');
  assert.ok(calls[2].args.includes('q=is:pr is:open review-requested:@me'));
  assert.equal(calls.length, 3);
});

// A notification thread as GET /notifications gives it.
const thread = (repo, number, reason, type = 'PullRequest') => ({
  reason,
  subject: { type, title: 't', url: `https://api.github.com/repos/kgheacock/${repo}/pulls/${number}` },
});

test('TestGithubWatched', async () => {
  // Watched: the repository (subscribed) or the PR (manual). A mention, an
  // issue, and a repeat are not.
  const refs = parseNotifications(
    JSON.stringify([
      thread('macro-pad', 97, 'subscribed'),
      thread('dotfiles', 3, 'manual'),
      thread('dotfiles', 3, 'subscribed'),
      thread('site', 5, 'mention'),
      thread('site', 6, 'subscribed', 'Issue'),
      thread('site', 7, 'author'),
    ]),
  );
  assert.deepEqual(refs, [
    { owner: 'kgheacock', repo: 'macro-pad', number: 97 },
    { owner: 'kgheacock', repo: 'dotfiles', number: 3 },
  ]);

  // The search has 97 already; 3 and 8 are asked for in one query, with
  // their names as variables. 8 is merged, and a deleted repository gives
  // null with an error beside the data.
  const notifications = [thread('macro-pad', 97, 'subscribed'), thread('dotfiles', 3, 'manual'), thread('old', 8, 'manual'), thread('gone', 1, 'manual')];
  const data = {
    data: {
      p0: { pullRequest: { ...pr('dotfiles', 3, 'FAILURE'), state: 'OPEN' } },
      p1: { pullRequest: { ...pr('old', 8, 'SUCCESS'), state: 'MERGED' } },
      p2: null,
    },
    errors: [{ message: "Could not resolve to a Repository with the name 'kgheacock/gone'." }],
  };
  const { calls, execFile } = stubGh([
    { stdout: answer([pr('macro-pad', 97, 'SUCCESS')]) },
    { stdout: JSON.stringify(notifications) },
    { stdout: JSON.stringify(data), error: Object.assign(new Error('exit 1'), { code: 1 }) },
  ]);
  const got = await fetchPulls({ env: {}, execFile });
  assert.deepEqual(got.pulls.map((p) => [p.channel, p.status]), [
    ['kgheacock.macro-pad.97', 'passing'],
    ['kgheacock.dotfiles.3', 'failing'],
  ]);
  assert.equal(got.total, 2, 'a whole answer, so PRs that left can close');
  const asked = calls[2].args;
  assert.ok(asked.includes('o0=kgheacock') && asked.includes('r0=dotfiles') && asked.includes('n0=3'), asked.join(' '));
  assert.equal(asked[asked.indexOf('n0=3') - 1], '-F', 'the number is sent as an Int');
  const query = asked[asked.indexOf('-f') + 1];
  assert.ok(!query.includes('dotfiles'), 'names are variables, not query text');

  // A notifications read that fails says what it was for.
  const failing = stubGh([{ stdout: answer([]) }, { error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'HTTP 403' }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: failing.execFile }), /cannot read watched PRs: gh failed: HTTP 403/);
});

test('TestGithubFeedFails', async () => {
  const missing = Object.assign(new Error('spawn gh ENOENT'), { code: 'ENOENT' });
  const { execFile } = stubGh([{ error: missing }]);
  await assert.rejects(fetchPulls({ env: {}, execFile }), /no gh; install the GitHub CLI or set INBOX_GH/);
  const auth = stubGh([{ error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'gh auth login\nmore' }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: auth.execFile }), /gh failed: gh auth login$/);
  const junk = stubGh([{ stdout: 'not json' }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: junk.execFile }), /not JSON/);
  const said = stubGh([{ stdout: JSON.stringify({ errors: [{ message: 'Bad credentials' }] }) }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: said.execFile }), /GitHub said: Bad credentials/);
});
