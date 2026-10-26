import assert from 'node:assert/strict';
import test from 'node:test';
import basic from '../src/plugins/basic.js';
import { emojiFor } from '../src/emoji.js';
import ai from '../src/plugins/ai.js';
import { MAX_CHILDREN, channelIcon, createRenderer } from '../src/render.js';

const row = (channel, count, newestId) => ({ channel, count, newestId, createdAt: newestId });
const renderer = (extra = {}) => createRenderer({ rules: basic.rules, ...extra });

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
    iconEmoji: [emojiFor('repo:b').iconRef],
  });

  // Nothing unseen: the key stays, with count 0 and no status, and no children.
  const empty = renderer().plan({ unseen: [], active: null });
  assert.deepEqual(empty.map((m) => m.setLayerKey), [{ id: 'inbox', status: '', count: 0, iconRef: 'gmail.png' }]);
});

test('TestTarget', () => {
  const r = renderer();
  assert.equal(r.target('b'), null);
  r.plan({ unseen: [row('a', 2, 5), row('b', 1, 9)], active: null });
  assert.deepEqual(r.target('b'), { channel: 'b', shownId: 9, here: null });
  assert.deepEqual(r.target('a'), { channel: 'a', shownId: 5, here: null });
  assert.equal(r.target('c'), null);
  // A channel at zero stays in its place, with nothing to mark.
  r.plan({ unseen: [row('a', 2, 5)], active: 'inbox' });
  assert.deepEqual(r.target('b'), { channel: 'b', shownId: 0, here: null });
  r.reset([]);
  assert.equal(r.target('b'), null);
});

test('TestTargetHere', () => {
  const r = renderer();
  r.plan({ unseen: [{ ...row('a', 1, 5), here: 'app:com.apple.Safari' }, row('b', 1, 9)], active: null });
  assert.equal(r.target('b').here, null);
  assert.equal(r.target('a').here, 'app:com.apple.Safari');
  r.reset([]);
  assert.equal(r.target('a'), null);
});

test('TestPlanSlot', () => {
  const [key] = renderer({ slot: 2 }).plan({ unseen: [], active: null });
  assert.equal(key.setLayerKey.slot, 2);
});

// Task 0078: macropadd pages past five, so seven channels are seven
// children, and the twenty newest show.
test('TestSevenChildren', () => {
  const unseen = [];
  for (let i = 1; i <= 7; i++) unseen.push(row(`c${i}`, i, i * 10));
  const messages = renderer().plan({ unseen, active: null });
  const children = brief(messages).filter((m) => m.startsWith('set inbox/'));
  assert.deepEqual(children, [7, 6, 5, 4, 3, 2, 1].map((i) => `set inbox/c${i} ${i} WARN`));
  assert.equal(messages[0].setLayerKey.count, 28);
});

test('TestTwentyNewest', () => {
  const unseen = [];
  for (let i = 1; i <= 22; i++) unseen.push(row(`c${i}`, 1, i * 10));
  const messages = renderer().plan({ unseen, active: null });
  const children = messages.filter((m) => m.setLayerKey?.parent === 'inbox').map((m) => m.setLayerKey.id);
  assert.equal(children.length, MAX_CHILDREN);
  assert.deepEqual(children, Array.from({ length: 20 }, (_, i) => `c${22 - i}`));
  assert.equal(messages[0].setLayerKey.count, 22);
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
  const full = Array.from({ length: MAX_CHILDREN }, (_, i) => row(`c${i + 1}`, 1, i + 1));
  r.plan({ unseen: full, active: null });
  const open = r.plan({ unseen: [...full, row('late', 1, 99)], active: 'inbox' });
  // No free position: the twenty-first waits, and only the Inbox count changes.
  assert.deepEqual(brief(open), [`set /inbox ${MAX_CHILDREN + 1} WARN`]);
  const closed = r.plan({ unseen: [...full, row('late', 1, 99)], active: null });
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

test('TestChannelIcon', () => {
  const repo = (name) => emojiFor(`repo:${name}`).iconRef;
  // A channel is a folder: its last segment over its repository's emoji.
  assert.deepEqual(channelIcon('debug'), { iconText: 'debug', iconEmoji: [repo('debug')] });
  assert.deepEqual(channelIcon('macro-pad.driver'), {
    iconText: 'driver',
    iconEmoji: [repo('macro-pad')],
  });
  // HOST.TLD.OWNER.REPO: the repository is the fourth segment (task 0081).
  assert.deepEqual(channelIcon('github.com.kgheacock.macro-pad').iconEmoji, [repo('macro-pad')]);
  // Without sessions, a last segment of 8 hex digits is part of the folder.
  assert.deepEqual(channelIcon('3f2a91c0').iconEmoji, [repo('3f2a91c0')]);
  // With sessions, PROJECT.SID is the repository's emoji, then the session's.
  assert.deepEqual(channelIcon('macro-pad.driver.3F2A91C0', { sessions: true }), {
    iconText: 'driver',
    iconEmoji: [repo('macro-pad'), emojiFor('session:3f2a91c0').iconRef],
  });
  // A long name is cut to the 16 runes macropadd takes as a caption.
  assert.equal(channelIcon(`x.${'n'.repeat(40)}`).iconText, `${'n'.repeat(15)}…`);
});

test('TestBasicAndAiShareTheFolderEmoji', () => {
  // A folder sent to the basic inbox and a session in it on the ai inbox show
  // the same folder emoji.
  const basicChild = renderer().plan({ unseen: [row('macro-pad.driver', 1, 1)], active: null })[1].setLayerKey;
  const aiChild = createRenderer({ rules: ai.rules })
    .plan({ unseen: [{ ...row('macro-pad.driver.3f2a91c0', 1, 1), byStatus: { waiting: 1 } }], active: null })[1]
    .setLayerKey;
  assert.equal(aiChild.iconEmoji[0], basicChild.iconEmoji[0]);
  assert.equal(aiChild.iconText, basicChild.iconText);
  assert.equal(aiChild.iconEmoji.length, 2);
});

test('TestOneRepositoryOneEmoji', () => {
  // Task 0081: a subfolder, a folder under HOST/OWNER, and a session in it
  // all show the repository's emoji first.
  const first = (rules, channel) =>
    createRenderer({ rules }).plan({ unseen: [{ ...row(channel, 1, 1), byStatus: { done: 1 } }], active: null })[1]
      .setLayerKey.iconEmoji[0];
  const want = emojiFor('repo:docreviewonpremnodeweb').iconRef;
  assert.equal(first(basic.rules, 'docreviewonpremnodeweb'), want);
  assert.equal(first(basic.rules, 'docreviewonpremnodeweb.src'), want);
  assert.equal(first(ai.rules, 'github.com.OnePayPal.docreviewonpremnodeweb.08709453'), want);
});

test('TestPlanSendsEmojiNotAPicture', () => {
  const messages = renderer().plan({ unseen: [row('macro-pad.driver', 2, 5)], active: null });
  const child = messages[1].setLayerKey;
  assert.equal(child.iconText, 'driver');
  assert.deepEqual(child.iconEmoji, [emojiFor('repo:macro-pad').iconRef]);
  assert.ok(!('iconRef' in child), 'a child sends emoji, and no iconRef');
});
