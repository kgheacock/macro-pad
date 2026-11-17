import assert from 'node:assert/strict';
import test from 'node:test';
import { componentInput, emojiFor } from '../src/emoji.js';
import { createReposRenderer } from '../src/plugins/repos.js';
import { BADGE_MAX, badge, padStatus, repoNode, topKey } from '../src/repo-key.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

const OWNER = 'kgheacock';
const COUNT_FIELDS = ['count', 'countGreen', 'countYellow'];

test('TestRepoKeyIcon', () => {
  const node = repoNode({ name: 'macro-pad' });
  assert.equal(node.id, 'repo.macro-pad');
  assert.equal(node.parent, 'repos');
  assert.deepEqual(node.iconEmoji, [emojiFor('repo:macro-pad').iconRef]);
  assert.deepEqual(node.iconEmoji, [emojiFor(componentInput('macro-pad')).iconRef]);
  // A name with a dot: the key has -, the emoji follows the key, the caption keeps the name.
  const dotted = repoNode({ name: 'keithheacock.com' });
  assert.deepEqual([dotted.id, dotted.iconText], ['repo.keithheacock-com', 'keithheacock.com']);
  assert.deepEqual(dotted.iconEmoji, [emojiFor('repo:keithheacock-com').iconRef]);
});

test('TestRepoKeyCaptionClips', () => {
  const long = repoNode({ name: 'abcdefghijklmnopqrst' });
  assert.equal([...long.iconText].length, 16);
  assert.ok(long.iconText.endsWith('…'));
  assert.equal(long.iconText, 'abcdefghijklmno…');
  const exact = repoNode({ name: 'abcdefghijklmnop' });
  assert.equal(exact.iconText, 'abcdefghijklmnop');
  // Runes, not UTF-16 units.
  assert.equal(repoNode({ name: '🚀'.repeat(16) }).iconText, '🚀'.repeat(16));
});

test('TestRepoKeyBadges', () => {
  const node = repoNode({ name: 'macro-pad', issues: 7, prs: 3, yellow: 2 });
  assert.deepEqual([node.count, node.countGreen, node.countYellow], [7, 3, 2]);
});

test('TestRepoKeyZeroDrawsNothing', () => {
  const some = repoNode({ name: 'a', issues: 0, prs: 4, yellow: 0 });
  assert.equal('count' in some, false);
  assert.equal('countYellow' in some, false);
  assert.equal(some.countGreen, 4);
  const none = repoNode({ name: 'a', issues: 0, prs: 0, yellow: 0 });
  for (const field of COUNT_FIELDS) assert.equal(field in none, false, field);
  // Missing and bad counts are zero.
  const bad = repoNode({ name: 'a', issues: NaN, prs: -2 });
  for (const field of COUNT_FIELDS) assert.equal(field in bad, false, field);
});

test('TestRepoKeyCapsAt100', () => {
  assert.deepEqual([99, 100, 5000].map(badge), [99, 100, 100]);
  assert.equal(BADGE_MAX, 100);
  const node = repoNode({ name: 'a', issues: 99, prs: 100, yellow: 5000 });
  assert.deepEqual([node.count, node.countGreen, node.countYellow], [99, 100, 100]);
});

test('TestRepoKeyStatus', () => {
  const status = (s) => repoNode({ name: 'a', status: s }).status;
  assert.deepEqual(['waiting', 'running', 'done', undefined, null].map(status), ['WARN', 'RUNNING', 'DONE', '', '']);
  // The worst session wins.
  assert.equal(status(['done', 'running', 'waiting']), 'WARN');
  assert.equal(status(['done', 'running']), 'RUNNING');
  assert.equal(status([]), '');
  assert.equal(padStatus(['done']), 'DONE');
});

test('TestRepoTopKey', () => {
  const top = topKey([
    { issues: 7, prs: 3, yellow: 2, status: 'running' },
    { issues: 1, prs: 0, yellow: 0, status: 'waiting' },
    { issues: 0, prs: 0, yellow: 0 },
  ]);
  assert.deepEqual(top, { id: 'repos', status: 'WARN', iconRef: 'github.png', count: 8, countGreen: 3, countYellow: 2 });
  // No repositories: no counts, no status.
  assert.deepEqual(topKey([]), { id: 'repos', status: '', iconRef: 'github.png' });
  // The totals are capped like the repository counts.
  const big = topKey([{ issues: 80 }, { issues: 80 }]);
  assert.equal(big.count, 100);
  // A session of a repository that is not in the list still colours the key.
  assert.equal(topKey([{ issues: 1 }], ['running']).status, 'RUNNING');
});

// A store that holds `rows` as the repos rows, and a renderer that reads it.
function storeWith(t, rows) {
  const sc = scratch(t);
  const store = openStore(sc.db, undefined, { plugin: 'repos' });
  t.after(() => store.close());
  const put = (list) =>
    store.replaceRepos(
      OWNER,
      list.map(([name, issues, prs]) => ({
        name,
        key: name.replace(/\./g, '-').toLowerCase(),
        url: 'u',
        fork: false,
        archived: false,
        pushedAt: null,
        issues,
        prs,
      })),
    );
  put(rows);
  const fed = () => ({ owner: OWNER, repos: store.listRepos(OWNER), cut: false, offline: null });
  return { store, put, fed };
}

const plan = (renderer, fed) => renderer.plan({ unseen: [], active: null, path: [], fed });
const keys = (messages) => Object.fromEntries(messages.filter((m) => m.setLayerKey).map((m) => [m.setLayerKey.id, m.setLayerKey]));

test('TestRepoKeysFromStore', (t) => {
  const { store, put, fed } = storeWith(t, [['alpha', 7, 3], ['beta', 0, 2], ['gamma', 1, 0]]);
  store.putYellow('alpha', { items: [{ id: 'P-1', title: 't', url: 'u' }, { id: 'P-2', title: 't', url: 'u' }] });
  const renderer = createReposRenderer({ store });
  const first = plan(renderer, fed());
  const k = keys(first);
  assert.equal(first.filter((m) => m.setLayerKey?.parent === 'repos').length, 3);
  const counts = (id) => [k[id].count, k[id].countGreen, k[id].countYellow];
  assert.deepEqual(counts('repo.alpha'), [7, 3, 2]);
  assert.deepEqual(counts('repo.beta'), [undefined, 2, undefined]);
  assert.deepEqual(counts('repo.gamma'), [1, undefined, undefined]);
  assert.deepEqual(counts('repos'), [8, 5, 2]);
  // Nothing changed: nothing sent.
  assert.deepEqual(plan(renderer, fed()), []);
  // One prs value changes: the repository key moves, and so does the top key
  // whose total follows it. No other key is sent.
  put([['alpha', 7, 3], ['beta', 0, 4], ['gamma', 1, 0]]);
  const next = plan(renderer, fed());
  assert.deepEqual(Object.keys(keys(next)).sort(), ['repo.beta', 'repos']);
  assert.equal(keys(next)['repo.beta'].countGreen, 4);
  assert.equal(keys(next).repos.countGreen, 7);
});

test('TestReposPlanSendsOnlyChange', (t) => {
  const { put, fed, store } = storeWith(t, [['alpha', 0, 150], ['beta', 0, 0]]);
  const renderer = createReposRenderer({ store });
  assert.equal(keys(plan(renderer, fed()))['repo.alpha'].countGreen, 100);
  // 150 to 160 draws the same "99+": no message at all.
  put([['alpha', 0, 160], ['beta', 0, 0]]);
  assert.deepEqual(plan(renderer, fed()), []);
  // 5000 as well.
  put([['alpha', 0, 5000], ['beta', 0, 0]]);
  assert.deepEqual(plan(renderer, fed()), []);
  // Back below the cap sends the key.
  put([['alpha', 0, 99], ['beta', 0, 0]]);
  assert.equal(keys(plan(renderer, fed()))['repo.alpha'].countGreen, 99);
});

test('a repository count that falls to 0 resets its badge', (t) => {
  const { put, fed, store } = storeWith(t, [['alpha', 2, 0]]);
  const renderer = createReposRenderer({ store });
  assert.equal(keys(plan(renderer, fed()))['repo.alpha'].count, 2);
  put([['alpha', 0, 0]]);
  const after = keys(plan(renderer, fed()));
  // The full upsert has no count field: the driver draws no badge.
  assert.equal('count' in after['repo.alpha'], false);
  assert.equal('count' in after.repos, false);
});
