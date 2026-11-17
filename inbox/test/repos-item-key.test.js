import assert from 'node:assert/strict';
import { existsSync } from 'node:fs';
import test from 'node:test';
import { componentInput, emojiFor, loadEmojiSet, sessionInput } from '../src/emoji.js';
import { createRenderer } from '../src/render.js';
import { createReposRenderer } from '../src/plugins/repos.js';
import { clipTail, itemKey, statusOf } from '../src/plugins/repos-item-key.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

const set = loadEmojiSet();
const NOW = 1_000_000_000_000;
const REPO = 'macro-pad';
const SID = '3f2a91c0';

const item = (extra = {}) => ({
  id: 'x',
  yellow: null,
  green: null,
  session: null,
  title: 't',
  url: null,
  ci: null,
  agent: null,
  here: null,
  ...extra,
});
const keyOf = (extra) => itemKey(item(extra), { repo: REPO, set });
const repoEmoji = emojiFor(componentInput(REPO), set).iconRef;
const sessionEmoji = (sid) => emojiFor(sessionInput(sid), set).iconRef;

test('TestItemKeyTwoLines', () => {
  const key = keyOf({ yellow: 'PROJ-123', green: 42 });
  assert.deepEqual(key.iconLines, ['PROJ-123', '#42']);
  assert.equal('iconText' in key, false);
  assert.equal('iconRef' in key, false);
  assert.deepEqual(key.iconEmoji, [repoEmoji]);
});

test('TestItemKeyOneLine', () => {
  assert.deepEqual(keyOf({ yellow: 'PROJ-123' }).iconLines, ['PROJ-123']);
  assert.deepEqual(keyOf({ green: 7 }).iconLines, ['#7']);
  // A PR number of 0 is still a number.
  assert.deepEqual(keyOf({ green: 0 }).iconLines, ['#0']);
  assert.throws(() => keyOf({}), /no yellow id, no green id, and no session/);
});

test('TestItemKeySessionOnly', () => {
  const key = keyOf({ session: SID, agent: 'running' });
  assert.deepEqual(key.iconLines, [SID]);
  assert.deepEqual(key.iconEmoji, [sessionEmoji(SID)]);
  assert.notEqual(sessionEmoji(SID), undefined);
});

test('TestItemKeyNoSessionEmoji', () => {
  // Pick a session whose emoji differs from the repository's.
  const sid = ['3f2a91c0', 'cafe0001', 'cafe0002', 'cafe0003', 'cafe0004'].find((s) => sessionEmoji(s) !== repoEmoji);
  assert.ok(sid);
  for (const extra of [{ yellow: 'A-1' }, { green: 3 }, { yellow: 'A-1', green: 3 }]) {
    const key = keyOf({ ...extra, session: sid, agent: 'waiting' });
    assert.deepEqual(key.iconEmoji, [repoEmoji], JSON.stringify(extra));
    assert.equal(key.iconEmoji.includes(sessionEmoji(sid)), false);
    assert.equal(key.iconLines.includes(sid), false);
  }
});

test('TestItemKeyStatus', () => {
  const ci = { failing: 'ERROR', pending: 'RUNNING', passing: 'DONE', none: '' };
  const agent = { waiting: 'WARN', running: 'RUNNING', done: 'DONE' };
  for (const [state, status] of Object.entries(ci)) assert.equal(keyOf({ yellow: 'A-1', green: 1, ci: state }).status, status, `ci ${state}`);
  for (const [state, status] of Object.entries(agent)) assert.equal(keyOf({ session: SID, agent: state }).status, status, `agent ${state}`);
  // A yellow item with no machine state has no colour; nor has a null.
  assert.equal(keyOf({ yellow: 'A-1' }).status, '');
  assert.equal(keyOf({ yellow: 'A-1', ci: null, agent: null }).status, '');

  // The worst one wins, in the order ERROR, WARN, RUNNING, DONE, ''.
  const order = ['ERROR', 'WARN', 'RUNNING', 'DONE', ''];
  const states = [
    ...Object.entries(ci).map(([state, status]) => ({ ci: state, status })),
    { ci: null, status: '' },
  ];
  const agents = [...Object.entries(agent).map(([state, status]) => ({ agent: state, status })), { agent: null, status: '' }];
  for (const c of states) {
    for (const a of agents) {
      const want = order.find((s) => s === c.status || s === a.status);
      assert.equal(statusOf({ ci: c.ci, agent: a.agent }), want, `${c.ci} + ${a.agent}`);
    }
  }
  // A PR that passes with a session that waits shows WARN.
  assert.equal(keyOf({ green: 1, ci: 'passing', session: SID, agent: 'waiting' }).status, 'WARN');
  assert.equal(keyOf({ green: 1, ci: 'failing', session: SID, agent: 'waiting' }).status, 'ERROR');
  assert.equal(keyOf({ green: 1, ci: 'pending', session: SID, agent: 'done' }).status, 'RUNNING');
});

test('TestItemKeyClip', () => {
  const long = 'PLATFORM-INFRA-12345';
  assert.equal([...long].length, 20);
  const clipped = clipTail(long);
  assert.equal([...clipped].length, 16);
  assert.equal(clipped, 'PLATFORM-…-12345');
  assert.ok(clipped.includes('…'));
  assert.ok(clipped.endsWith(long.slice(-5)));
  assert.ok(clipped.startsWith(long.slice(0, 9)));
  // 16 runes or fewer stay as they are.
  assert.equal(clipTail('ABCDEFGHIJKLMNOP'), 'ABCDEFGHIJKLMNOP');
  assert.equal(clipTail('#42'), '#42');
  // Runes, not UTF-16 units.
  assert.equal([...clipTail('😀'.repeat(20))].length, 16);
  assert.deepEqual(keyOf({ yellow: long, green: 9 }).iconLines, [clipped, '#9']);
});

test('TestItemKeyWire', () => {
  const emojiDir = new URL('../../assets/emoji/', import.meta.url);
  const hex = '0123456789abcdef';
  const items = [];
  let n = 7;
  const next = (m) => (n = (n * 1103515245 + 12345) % 2147483648) % m;
  for (let i = 0; i < 30; i++) {
    const sid = Array.from({ length: 8 }, () => hex[next(16)]).join('');
    const yellow = next(3) === 0 ? null : `${'ABCDEFGHIJ'.slice(0, 1 + next(10)).repeat(1 + next(3))}-${next(100000)}`;
    const green = next(2) === 0 ? null : next(5000);
    const hasSession = (yellow === null && green === null) || next(2) === 0;
    items.push(item({
      yellow,
      green,
      session: hasSession ? sid : null,
      ci: ['failing', 'pending', 'passing', 'none', null][next(5)],
      agent: hasSession ? ['waiting', 'running', 'done'][next(3)] : null,
    }));
  }
  for (const it of items) {
    const key = itemKey(it, { repo: REPO, set });
    assert.ok(key.iconLines.length >= 1 && key.iconLines.length <= 2);
    for (const line of key.iconLines) {
      assert.ok([...line].length >= 1 && [...line].length <= 16, line);
      assert.equal(/[\u0000-\u001f\u007f-\u009f]/.test(line), false, line);
    }
    assert.equal(key.iconEmoji.length, 1);
    assert.match(key.iconEmoji[0], /^emoji\/[0-9a-f-]+\.png$/);
    assert.ok(existsSync(new URL(key.iconEmoji[0].slice('emoji/'.length), emojiDir)), key.iconEmoji[0]);
    assert.equal(key.count, 0);
    assert.equal('iconRef' in key, false);
    assert.equal('iconText' in key, false);
    assert.ok(['ERROR', 'WARN', 'RUNNING', 'DONE', ''].includes(key.status));
  }
});

test('TestRenderPassesIconLines', () => {
  // The generic renderer sends whatever fields a plugin's icon() returns.
  const renderer = createRenderer({
    slot: 0,
    emojiSet: set,
    rules: {
      id: 'x',
      top: () => ({}),
      child: () => ({}),
      before: () => 0,
      icon: () => ({ iconLines: ['PROJ-123', '#42'], iconEmoji: [repoEmoji] }),
    },
  });
  const messages = renderer.plan({ unseen: [{ channel: 'a', count: 1, byStatus: {}, newestId: 1, createdAt: NOW }], active: null });
  const child = messages.map((m) => m.setLayerKey).find((k) => k.id === 'a');
  assert.deepEqual(child.iconLines, ['PROJ-123', '#42']);
  assert.equal('iconText' in child, false);
});

test('TestRepoLayerItemKeys', (t) => {
  const sc = scratch(t);
  const store = openStore(sc.db, undefined, { plugin: 'repos' });
  t.after(() => store.close());
  const OWNER = 'kgheacock';
  store.putYellow('macro-pad', {
    items: [
      { id: 'PROJ-123', title: 'Fix login', url: 'https://jira.test/browse/PROJ-123' },
      { id: 'PROJ-9', title: 'Other', url: 'https://jira.test/browse/PROJ-9' },
    ],
    now: NOW,
  });
  store.replacePulls([
    { repo: 'macro-pad', number: 42, title: 'PROJ-123: fix login', url: `https://github.com/${OWNER}/macro-pad/pull/42`, ci: 'failing', headRepo: 'macro-pad', branch: null },
    { repo: 'macro-pad', number: 50, title: 'Plain work', url: `https://github.com/${OWNER}/macro-pad/pull/50`, ci: 'passing', headRepo: 'macro-pad', branch: null },
  ]);
  const rows = [
    {
      channel: `github.com.${OWNER}.macro-pad.${SID}`,
      byStatus: { waiting: 1 },
      count: 1,
      newestId: 1,
      createdAt: NOW,
      status: 'waiting',
      here: null,
    },
  ];
  const r = createReposRenderer({ store, emojiSet: set });
  const fed = { owner: OWNER, repos: [{ name: 'macro-pad', key: 'macro-pad' }], cut: false, offline: null };
  const messages = r.plan({ unseen: rows, active: 'repo.macro-pad', path: ['repos', 'repo.macro-pad'], fed, now: NOW });
  const nodes = messages.filter((m) => m.setLayerKey?.path).map((m) => m.setLayerKey);
  assert.deepEqual(nodes.map((k) => k.id), ['y:PROJ-123', 'y:PROJ-9', 'pr:50', `s:${SID}`]);
  const by = Object.fromEntries(nodes.map((k) => [k.id, k]));
  assert.deepEqual(by['y:PROJ-123'].iconLines, ['PROJ-123', '#42']);
  assert.equal(by['y:PROJ-123'].status, 'ERROR');
  assert.deepEqual(by['y:PROJ-9'].iconLines, ['PROJ-9']);
  assert.equal(by['y:PROJ-9'].status, '');
  assert.deepEqual(by['pr:50'].iconLines, ['#50']);
  assert.equal(by['pr:50'].status, 'DONE');
  assert.deepEqual(by[`s:${SID}`].iconLines, [SID]);
  assert.equal(by[`s:${SID}`].status, 'WARN');
  assert.deepEqual(by[`s:${SID}`].iconEmoji, [sessionEmoji(SID)]);
  for (const k of nodes) {
    assert.deepEqual(k.path, ['repos', 'repo.macro-pad']);
    assert.equal(k.count, 0);
    assert.equal('iconText' in k, false);
    if (k.id !== `s:${SID}`) assert.deepEqual(k.iconEmoji, [repoEmoji]);
  }
});
