import assert from 'node:assert/strict';
import test from 'node:test';
import { componentInput, emojiFor } from '../src/emoji.js';
import repos, { ACTIVE_MS, STALE_KEYS, createReposRenderer, rank } from '../src/plugins/repos.js';
import { REPO_MAX_CHILDREN } from '../src/render.js';
import { getPlugin } from '../src/plugins/index.js';

const repo = (name) => ({ name, key: name.replace(/\./g, '-').toLowerCase() });
const feedOf = (...names) => ({ owner: 'kgheacock', repos: names.map(repo), cut: false, offline: null });

// A row of Store.recent(): the unseen counts per status.
const session = (channel, byStatus = {}, newestId = 1) => ({
  channel,
  byStatus,
  count: Object.values(byStatus).reduce((a, b) => a + b, 0),
  newestId,
  createdAt: newestId,
});

const plan = (renderer, { unseen = [], fed, active = null, path } = {}) =>
  renderer.plan({ unseen, active, path: path ?? (active ? [active] : []), fed });
// The setLayerKey fields of a plan, keyed by id.
const keys = (messages) => Object.fromEntries(messages.filter((m) => m.setLayerKey).map((m) => [m.setLayerKey.id, m.setLayerKey]));
const children = (messages) => messages.filter((m) => m.setLayerKey?.parent).map((m) => m.setLayerKey.id);
const removed = (messages) => messages.filter((m) => m.kind === 'removeLayerKey').map((m) => m.removeLayerKey.id);

test('the repos plugin has the rules of a plugin with a feed', () => {
  assert.equal(repos.name, 'repos');
  assert.deepEqual(repos.statuses, ['waiting', 'running', 'done']);
  assert.equal(repos.defaultStatus, 'done');
  assert.deepEqual(repos.rules, { id: 'repos', activeMs: 3600000 });
  assert.equal(ACTIVE_MS, 3600000);
  assert.equal(typeof repos.feed.fetch, 'function');
  // The old name is an alias.
  assert.equal(getPlugin('ai'), repos);
  assert.equal(rank(undefined), 0);
  assert.equal(rank(session('a', { info: 1 })), 1);
  assert.equal(rank(session('a', { running: 1, done: 1 })), 2);
  assert.equal(rank(session('a', { waiting: 1, running: 1 })), 3);
});

test('one repo child per fed repo', () => {
  const r = createReposRenderer({ slot: 2 });
  const fed = {
    ...feedOf('macro-pad', 'Zed', 'keithheacock.com'),
    scores: new Map([['zed', { score: 9, last: 9 }], ['keithheacock-com', { score: 5, last: 5 }]]),
  };
  const messages = plan(r, { fed });
  const k = keys(messages);
  assert.deepEqual(k.repos, { id: 'repos', status: '', count: 0, iconRef: 'github.png', slot: 2 });
  // The order of the ledger. The key of a name with a dot has the dot as -
  // (task 0089's open question); the caption keeps the name.
  assert.deepEqual(children(messages), ['repo.zed', 'repo.keithheacock-com', 'repo.macro-pad']);
  for (const [name, key] of [['macro-pad', 'macro-pad'], ['keithheacock.com', 'keithheacock-com'], ['Zed', 'zed']]) {
    assert.deepEqual(k[`repo.${key}`], {
      id: `repo.${key}`,
      parent: 'repos',
      opens: true,
      status: '',
      count: 0,
      iconText: name,
      iconEmoji: [emojiFor(componentInput(key)).iconRef],
    });
  }
});

test('the children follow the order the feed gives', () => {
  const r = createReposRenderer();
  const fed = {
    ...feedOf('a', 'b', 'c'),
    scores: new Map([['c', { score: 5, last: 9 }], ['b', { score: 1, last: 3 }]]),
  };
  assert.deepEqual(children(plan(r, { fed })), ['repo.c', 'repo.b', 'repo.a']);
});

test('draws nothing before the feed answers', () => {
  const r = createReposRenderer();
  assert.deepEqual(plan(r, { fed: null }), []);
  assert.deepEqual(plan(r, { fed: undefined }), []);
});

test('every repo shows, also with no session', () => {
  const names = Array.from({ length: 100 }, (_, i) => `r${String(i).padStart(3, '0')}`);
  const messages = plan(createReposRenderer(), { fed: feedOf(...names) });
  assert.deepEqual(children(messages), names.map((n) => `repo.${n}`));
});

test('cuts a feed of 130 to the pad limit and logs one line', () => {
  const names = Array.from({ length: 130 }, (_, i) => `r${String(i).padStart(3, '0')}`);
  const logs = [];
  const r = createReposRenderer({ log: (line) => logs.push(line) });
  const fed = feedOf(...names);
  assert.equal(children(plan(r, { fed })).length, 128);
  assert.equal(REPO_MAX_CHILDREN, 128);
  plan(r, { fed });
  plan(r, { fed });
  assert.equal(logs.length, 1, logs.join('\n'));
  assert.match(logs[0], /130 repositories.*first 128/);
});

test('a waiting session colours its repo key and the top key', () => {
  const r = createReposRenderer();
  const fed = feedOf('macro-pad', 'other');
  const unseen = [
    session('macro-pad.3f2a91c0', { waiting: 1 }, 1),
    session('github.com.kgheacock.macro-pad.aaaaaaaa', { done: 1 }, 2),
    session('other.bbbbbbbb', { running: 1 }, 3),
  ];
  const k = keys(plan(r, { fed, unseen }));
  assert.deepEqual([k['repo.macro-pad'].status, k['repo.other'].status], ['WARN', 'RUNNING']);
  assert.deepEqual([k.repos.status, k.repos.count], ['WARN', 1]);
  // Seen: no colour on the repo, the top key, or the pip.
  const seen = [session('macro-pad.3f2a91c0', {}, 4)];
  const after = keys(plan(r, { fed, unseen: seen }));
  assert.deepEqual([after['repo.macro-pad'].status, after['repo.other'].status, after.repos.status, after.repos.count], ['', '', '', 0]);
  // Done is green.
  const done = keys(plan(r, { fed, unseen: [session('other.bbbbbbbb', { done: 2 }, 5)] }));
  assert.deepEqual([done['repo.other'].status, done.repos.status], ['DONE', 'DONE']);
});

test('a session of an unknown repo draws no key', () => {
  const r = createReposRenderer();
  const messages = plan(r, { fed: feedOf('macro-pad'), unseen: [session('stranger.3f2a91c0', { waiting: 1 })] });
  assert.deepEqual(children(messages), ['repo.macro-pad']);
  assert.equal(keys(messages)['repo.stranger'], undefined);
  assert.equal(keys(messages)['repo.macro-pad'].status, '');
  // Its colour still shows on the top key.
  assert.equal(keys(messages).repos.status, 'WARN');
});

test('a session of a dotted repo name maps to its key', () => {
  const r = createReposRenderer();
  const k = keys(plan(r, { fed: feedOf('keithheacock.com'), unseen: [session('github.com.kgheacock.keithheacock-com.3f2a91c0', { running: 1 })] }));
  assert.equal(k['repo.keithheacock-com'].status, 'RUNNING');
});

test('stale keys of ai, github, and repos go once per connect', () => {
  const r = createReposRenderer();
  const fed = feedOf('macro-pad');
  const first = plan(r, { fed });
  assert.deepEqual(removed(first), ['ai-inbox', 'github-inbox', 'repos']);
  assert.deepEqual(STALE_KEYS, ['ai-inbox', 'github-inbox', 'repos']);
  // The removals come before the top key is drawn again.
  assert.deepEqual(first.slice(0, 3).map((m) => m.kind), ['removeLayerKey', 'removeLayerKey', 'removeLayerKey']);
  assert.deepEqual(removed(plan(r, { fed })), []);
  // A new connect: the renderer is reset, and removes them once more, and
  // sends everything again.
  r.reset();
  const again = plan(r, { fed });
  assert.deepEqual(removed(again), ['ai-inbox', 'github-inbox', 'repos']);
  assert.deepEqual(children(again), ['repo.macro-pad']);
  assert.deepEqual(removed(plan(r, { fed })), []);
});

test('sends only what changed', () => {
  const r = createReposRenderer();
  const fed = feedOf('a', 'b');
  plan(r, { fed });
  assert.deepEqual(plan(r, { fed }), []);
  const k = keys(plan(r, { fed, unseen: [session('b.3f2a91c0', { running: 1 })] }));
  assert.deepEqual(Object.keys(k).sort(), ['repo.b', 'repos']);
});

test('while the repos layer is open no repo key moves or goes', () => {
  const r = createReposRenderer();
  plan(r, { fed: feedOf('a', 'b', 'c') });
  const fed = {
    ...feedOf('a', 'b', 'c', 'd'),
    // d is the newest work and would go first; a is second.
    scores: new Map([['d', { score: 9, last: 9 }], ['a', { score: 5, last: 5 }]]),
  };
  // The layer of repos is open, or a layer under it.
  let messages = plan(r, { fed, active: 'repos' });
  assert.deepEqual(removed(messages), []);
  // d is new and goes last; the three that were shown send nothing again.
  assert.deepEqual(children(messages), ['repo.d']);
  messages = plan(r, { fed, active: 'repo.a', path: ['repos', 'repo.a'] });
  assert.deepEqual(messages, []);

  // A repo that left the feed stays while the layer is open.
  assert.deepEqual(plan(r, { fed: feedOf('a', 'd'), active: 'repos' }), []);

  // Closed: the keys follow the order, and those that left go.
  const closed = plan(r, { fed: { ...feedOf('a', 'd'), scores: fed.scores } });
  assert.deepEqual(removed(closed), ['repo.b', 'repo.c', 'repo.a', 'repo.d']);
  assert.deepEqual(children(closed), ['repo.d', 'repo.a']);
});

test('a new repo past the cap stays out while the layer is open', () => {
  const r = createReposRenderer();
  const names = Array.from({ length: 128 }, (_, i) => `r${String(i).padStart(3, '0')}`);
  plan(r, { fed: feedOf(...names) });
  assert.deepEqual(plan(r, { fed: feedOf('new', ...names), active: 'repos' }), []);
});
