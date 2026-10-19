import assert from 'node:assert/strict';
import test from 'node:test';
import { channelText, createRenderer } from '../src/render.js';

const row = (channel, count, newestId) => ({ channel, count, newestId, createdAt: newestId });
const renderer = (extra = {}) => createRenderer(extra);

// The setLayerKey and removeLayerKey messages of a plan, as short strings.
function brief(messages) {
  return messages.map((m) => {
    if (m.kind === 'removeLayerKey') return `remove ${m.removeLayerKey.parent ?? ''}/${m.removeLayerKey.id}`;
    const k = m.setLayerKey;
    return `set ${k.parent ?? ''}/${k.id} ${k.count}${k.status ? ' ' + k.status : ''}`;
  });
}

test('TestPlan', () => {
  const r = renderer();
  const messages = r.plan({ unseen: [row('a', 2, 5), row('b', 1, 9)], active: null });
  assert.deepEqual(messages[0], {
    kind: 'setLayerKey',
    setLayerKey: { id: 'inbox', status: 'WARN', count: 3, iconRef: 'gmail.png' },
  });
  // Newest first: b holds the newest unseen message.
  assert.deepEqual(brief(messages), ['set /inbox 3 WARN', 'set inbox/b 1 WARN', 'set inbox/a 2 WARN']);
  assert.deepEqual(messages[1].setLayerKey, {
    id: 'b',
    parent: 'inbox',
    status: 'WARN',
    count: 1,
    iconText: 'b',
  });

  // Nothing unseen: the key stays, with count 0 and no status, and no children.
  const empty = renderer().plan({ unseen: [], active: null });
  assert.deepEqual(empty.map((m) => m.setLayerKey), [{ id: 'inbox', status: '', count: 0, iconRef: 'gmail.png' }]);
});

test('TestTarget', () => {
  const r = renderer();
  assert.equal(r.target(0), null);
  r.plan({ unseen: [row('a', 2, 5), row('b', 1, 9)], active: null });
  assert.deepEqual(r.target(0), { channel: 'b', shownId: 9, here: null });
  assert.deepEqual(r.target(1), { channel: 'a', shownId: 5, here: null });
  assert.equal(r.target(2), null);
  // A channel at zero stays in its place, with nothing to mark.
  r.plan({ unseen: [row('a', 2, 5)], active: 'inbox' });
  assert.deepEqual(r.target(0), { channel: 'b', shownId: 0, here: null });
  r.reset([]);
  assert.equal(r.target(0), null);
});

test('TestTargetHere', () => {
  const r = renderer();
  r.plan({ unseen: [{ ...row('a', 1, 5), here: 'app:com.apple.Safari' }, row('b', 1, 9)], active: null });
  assert.equal(r.target(0).here, null);
  assert.equal(r.target(1).here, 'app:com.apple.Safari');
  r.reset([]);
  assert.equal(r.target(1), null);
});

test('TestPlanSlot', () => {
  const [key] = renderer({ slot: 2 }).plan({ unseen: [], active: null });
  assert.equal(key.setLayerKey.slot, 2);
});

test('TestFiveNewest', () => {
  const unseen = [];
  for (let i = 1; i <= 7; i++) unseen.push(row(`c${i}`, i, i * 10));
  const messages = renderer().plan({ unseen, active: null });
  const children = brief(messages).filter((m) => m.startsWith('set inbox/'));
  assert.deepEqual(children, [
    'set inbox/c7 7 WARN',
    'set inbox/c6 6 WARN',
    'set inbox/c5 5 WARN',
    'set inbox/c4 4 WARN',
    'set inbox/c3 3 WARN',
  ]);
  assert.equal(messages[0].setLayerKey.count, 28);
});

test('TestStableWhileOpen', () => {
  const r = renderer();
  const three = [row('a', 1, 10), row('b', 1, 20), row('c', 1, 30)];
  r.plan({ unseen: three, active: null });

  // The layer opens. Channel b is seen; a new channel d arrives, newest of all.
  const open = r.plan({ unseen: [row('a', 1, 10), row('c', 1, 30), row('d', 4, 40)], active: 'inbox' });
  assert.deepEqual(brief(open), [
    'set /inbox 6 WARN',
    'set inbox/b 0',
    'set inbox/d 4 WARN',
  ]);
  assert.ok(!open.some((m) => m.kind === 'removeLayerKey'));

  // Counts still change in place, and nothing moves.
  const more = r.plan({ unseen: [row('a', 1, 10), row('c', 3, 50), row('d', 4, 40)], active: 'inbox' });
  assert.deepEqual(brief(more), ['set /inbox 8 WARN', 'set inbox/c 3 WARN']);

  // The layer closes: b goes, and the rest reorder, newest first: c, d, a.
  // The pad held c, a, d. Key c stays in front; a and d come back in order.
  const closed = r.plan({ unseen: [row('a', 1, 10), row('c', 3, 50), row('d', 4, 40)], active: null });
  assert.deepEqual(brief(closed), [
    'remove inbox/b',
    'remove inbox/a',
    'remove inbox/d',
    'set inbox/d 4 WARN',
    'set inbox/a 1 WARN',
  ]);
});

test('TestStableWhileOpenFullLayerWaits', () => {
  const r = renderer();
  const five = [1, 2, 3, 4, 5].map((i) => row(`c${i}`, 1, i));
  r.plan({ unseen: five, active: null });
  const open = r.plan({ unseen: [...five, row('late', 1, 99)], active: 'inbox' });
  // No free position: the sixth waits, and only the Inbox count changes.
  assert.deepEqual(brief(open), ['set /inbox 6 WARN']);
  const closed = r.plan({ unseen: [...five, row('late', 1, 99)], active: null });
  assert.ok(brief(closed).includes('set inbox/late 1 WARN'));
  assert.ok(brief(closed).includes('remove inbox/c1'));
});

test('TestNoChangeSendsNothing', () => {
  const r = renderer();
  const unseen = [row('a', 2, 5), row('b', 1, 9)];
  assert.ok(r.plan({ unseen, active: null }).length > 0);
  assert.deepEqual(r.plan({ unseen, active: null }), []);
  assert.deepEqual(r.plan({ unseen: unseen.map((x) => ({ ...x })), active: null }), []);
  // A new message in a makes it the newest, so the two reorder.
  assert.deepEqual(brief(r.plan({ unseen: [row('a', 3, 11), row('b', 1, 9)], active: null })), [
    'set /inbox 4 WARN',
    'remove inbox/b',
    'remove inbox/a',
    'set inbox/a 3 WARN',
    'set inbox/b 1 WARN',
  ]);
  // A count that changes with no reorder sends that channel and the total.
  assert.deepEqual(brief(r.plan({ unseen: [row('a', 3, 11), row('b', 2, 9)], active: null })), [
    'set /inbox 5 WARN',
    'set inbox/b 2 WARN',
  ]);
  // Opening the layer with the same data sends nothing either.
  assert.deepEqual(r.plan({ unseen: [row('a', 3, 11), row('b', 2, 9)], active: 'inbox' }), []);
});

test('TestResetSendsEverythingAndClearsLeftovers', () => {
  const r = renderer();
  const unseen = [row('a', 2, 5)];
  r.plan({ unseen, active: null });
  r.reset(['a', 'gone', 'seen']);
  assert.deepEqual(brief(r.plan({ unseen, active: null })), [
    'set /inbox 2 WARN',
    'remove inbox/a',
    'remove inbox/gone',
    'remove inbox/seen',
    'set inbox/a 2 WARN',
  ]);
});

test('TestChannelText', () => {
  // Only the last segment shows: the ones before the final dot are dropped.
  assert.equal(channelText('debug'), 'debug');
  assert.equal(channelText('macro-pad.driver'), 'driver');
  assert.equal(channelText('github.com.kgheacock.macro-pad'), 'macro-pad');
});

test('TestChannelTextFitsMacropadd', () => {
  // macropadd takes at most 32 runes on one line here, and cuts what is too wide.
  const text = channelText(`p.${'n'.repeat(60)}`);
  assert.equal([...text].length, 32);
  assert.ok(text.endsWith('…'));
  assert.ok(!text.includes('\n'));
  assert.equal(channelText('n'.repeat(32)), 'n'.repeat(32));
  assert.equal(channelText('_'), '_');
});

test('TestPlanSendsTextNotAPicture', () => {
  const messages = renderer().plan({ unseen: [row('macro-pad.driver', 2, 5)], active: null });
  const child = messages[1].setLayerKey;
  assert.equal(child.iconText, 'driver');
  assert.ok(!('iconRef' in child), 'a child sends text, and no iconRef');
});
