import assert from 'node:assert/strict';
import test from 'node:test';
import ai from '../src/plugins/ai.js';
import { createRenderer } from '../src/render.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

// A row of Store.unseen(), from the unseen counts per status.
const row = (channel, byStatus, newestId) => ({
  channel,
  byStatus,
  count: Object.values(byStatus).reduce((a, b) => a + b, 0),
  newestId,
  createdAt: newestId,
});

// The setLayerKey fields of a plan, keyed by id.
function keys(messages) {
  return Object.fromEntries(messages.filter((m) => m.setLayerKey).map((m) => [m.setLayerKey.id, m.setLayerKey]));
}
const order = (messages) => messages.filter((m) => m.setLayerKey?.parent).map((m) => m.setLayerKey.id);
const createAiRenderer = () => createRenderer({ rules: ai.rules });
const plan = (unseen, active = null) => createAiRenderer().plan({ unseen, active });

test('TestAiPlan', () => {
  const messages = plan([
    row('a', { waiting: 1, done: 1 }, 5),
    row('b', { running: 1 }, 9),
    row('c', { done: 1 }, 7),
  ]);
  const k = keys(messages);
  assert.deepEqual(k['ai-inbox'], { id: 'ai-inbox', status: 'WARN', count: 1, iconRef: 'ai.png' });
  // The newest message first, whatever the status.
  assert.deepEqual(order(messages), ['b', 'c', 'a']);
  assert.deepEqual([k.a, k.b, k.c].map((x) => [x.status, x.count]), [['WARN', 1], ['RUNNING', 0], ['DONE', 0]]);
  assert.equal(k.a.parent, 'ai-inbox');
});

test('TestAiPlanSeenChannelShows', () => {
  // A channel with all seen (count 0, no statuses) still shows, with no color.
  const seen = { channel: 's', byStatus: {}, count: 0, newestId: 8, createdAt: 8 };
  const messages = plan([row('a', { waiting: 1 }, 5), seen]);
  const k = keys(messages);
  assert.deepEqual(order(messages), ['s', 'a']);
  assert.deepEqual([k.s.status, k.s.count], ['', 0]);
  assert.deepEqual([k['ai-inbox'].status, k['ai-inbox'].count], ['WARN', 1]);
  // Only seen channels: the key has no color.
  assert.equal(keys(plan([seen]))['ai-inbox'].status, '');
});

test('TestAiPlanPipCountsChannels', () => {
  // Three waiting messages in one channel, one in another: two channels wait.
  const k = keys(plan([row('a', { waiting: 3 }, 1), row('b', { waiting: 1 }, 2), row('c', { done: 4 }, 3)]));
  assert.equal(k['ai-inbox'].count, 2);
  assert.equal(k.a.count, 3);
});

test('TestAiPriority', () => {
  const top = (unseen) => keys(plan(unseen))['ai-inbox'];
  assert.equal(top([row('a', { running: 1 }, 1), row('b', { done: 1 }, 2)]).status, 'RUNNING');
  assert.equal(top([row('a', { done: 2 }, 1), row('b', { done: 1 }, 2)]).status, 'DONE');
  assert.equal(top([row('a', { done: 1 }, 1), row('b', { waiting: 1, running: 1 }, 2)]).status, 'WARN');
  // A stored status that is none of the three counts as done.
  assert.equal(top([row('a', { info: 1 }, 1)]).status, 'DONE');
  // Nothing unseen: no status, no pip, no children.
  const messages = plan([]);
  assert.deepEqual(keys(messages)['ai-inbox'], { id: 'ai-inbox', status: '', count: 0, iconRef: 'ai.png' });
  assert.deepEqual(order(messages), []);
});

test('TestAiSeenRecalculates', (t) => {
  const { db } = scratch(t);
  const store = openStore(db, undefined, { plugin: 'ai' });
  t.after(() => store.close());
  const w = store.insert('a', 'needs input', '/x', { status: 'waiting' });
  store.insert('a', 'step 2', '/x', { status: 'done' });
  store.insert('b', 'busy', '/x', { status: 'running' });

  const r = createAiRenderer();
  let k = keys(r.plan({ unseen: store.unseen(), active: null }));
  assert.equal(k['ai-inbox'].status, 'WARN');

  store.markSeen('a', w);
  k = keys(r.plan({ unseen: store.unseen(), active: null }));
  assert.equal(k.a.status, 'DONE');
  assert.equal(k['ai-inbox'].status, 'RUNNING');
  assert.equal(k['ai-inbox'].count, 0);

  store.markAllSeen();
  const messages = r.plan({ unseen: store.unseen(), active: null });
  assert.equal(keys(messages)['ai-inbox'].status, '');
  const removed = messages.filter((m) => m.kind === 'removeLayerKey').map((m) => m.removeLayerKey.id);
  assert.deepEqual(removed.sort(), ['a', 'b']);
});

test('TestAiListWithoutPips', () => {
  const messages = plan([row('a', { done: 1 }, 1), row('b', { running: 2 }, 2)]);
  const k = keys(messages);
  assert.equal(k['ai-inbox'].count, 0);
  assert.deepEqual(order(messages), ['b', 'a']);
});

test('TestAiStableWhileOpen', () => {
  const r = createAiRenderer();
  r.plan({ unseen: [row('a', { waiting: 1 }, 2), row('b', { done: 1 }, 1)], active: null });
  // The layer is open and a is seen: it stays, with no status, and nothing moves.
  const open = r.plan({ unseen: [row('b', { done: 1 }, 1)], active: 'ai-inbox' });
  assert.deepEqual(open.filter((m) => m.removeLayerKey), []);
  const k = keys(open);
  assert.deepEqual([k.a.status, k.a.count], ['', 0]);
  assert.equal(r.topId, 'ai-inbox');
  assert.equal(r.target(0).channel, 'a');
});

test('TestAiRecent', (t) => {
  const { db } = scratch(t);
  const store = openStore(db, undefined, { plugin: 'ai' });
  t.after(() => store.close());
  const hour = ai.rules.activeMs;
  const now = 10 * hour;
  store.insert('old', 'x', '/x', { status: 'waiting', now: now - hour - 1 });
  store.insert('seen', 'x', '/x', { status: 'done', now: now - 1000 });
  const w = store.insert('live', 'x', '/x', { status: 'waiting', now: now - 500 });
  store.insert('live', 'y', '/x', { status: 'running', now: now - 100 });
  store.markSeen('seen', Number.MAX_SAFE_INTEGER, now);

  const rows = store.recent(now - hour);
  // 'old' is out; 'seen' is in, with nothing unseen.
  assert.deepEqual(rows.map((r) => r.channel), ['live', 'seen']);
  const [live, seen] = rows;
  assert.deepEqual([live.count, live.byStatus, live.createdAt], [2, { waiting: 1, running: 1 }, now - 100]);
  assert.deepEqual([seen.count, seen.byStatus], [0, {}]);
  assert.ok(live.newestId > w);
  // The window is inclusive and a message inside it brings the channel back.
  assert.equal(store.recent(now - hour - 1).length, 3);
  assert.deepEqual(plan(rows).filter((m) => m.setLayerKey?.parent).map((m) => m.setLayerKey.id), ['live', 'seen']);
});
