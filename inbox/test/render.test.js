import assert from 'node:assert/strict';
import { existsSync, readFileSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { channelImage, decodePng, encodePng, writeChannelImage } from '../src/png.js';
import { createRenderer } from '../src/render.js';
import { scratch } from '../testlib/helpers.js';

const row = (channel, count, newestId) => ({ channel, count, newestId, createdAt: newestId });
const drawImage = (_dir, channel) => `.generated/inbox/${channel}.png`;
const renderer = (extra = {}) => createRenderer({ assetsDir: '/unused', drawImage, ...extra });

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
    iconRef: '.generated/inbox/b.png',
  });

  // Nothing unseen: the key stays, with count 0 and no status, and no children.
  const empty = renderer().plan({ unseen: [], active: null });
  assert.deepEqual(empty.map((m) => m.setLayerKey), [{ id: 'inbox', status: '', count: 0, iconRef: 'gmail.png' }]);
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

test('TestPictureFailureStillShowsCount', () => {
  const logs = [];
  const r = renderer({
    log: (line) => logs.push(line),
    drawImage: () => {
      throw new Error('read-only');
    },
  });
  const messages = r.plan({ unseen: [row('a', 2, 5)], active: null });
  assert.equal(messages[1].setLayerKey.iconRef, '');
  assert.equal(messages[1].setLayerKey.count, 2);
  assert.equal(logs.length, 1);
});

const FIXTURE = new URL('./fixtures/channel-macro-pad.driver.png', import.meta.url);

test('TestChannelImage', () => {
  const image = channelImage('macro-pad.driver');
  assert.equal(image.width, 128);
  assert.equal(image.height, 128);
  const golden = decodePng(readFileSync(FIXTURE));
  assert.equal(golden.width, 128);
  assert.equal(golden.height, 128);
  assert.ok(image.rgb.equals(golden.rgb), 'pixels differ from test/fixtures/channel-macro-pad.driver.png');
  // The PNG the encoder writes reads back to the same pixels.
  assert.ok(decodePng(encodePng(image)).rgb.equals(image.rgb));
});

test('TestChannelImageLongNamesAreCut', () => {
  const long = channelImage('a-very-long-parent-segment.and-an-even-longer-last-segment');
  const short = channelImage('ai');
  assert.ok(!long.rgb.equals(short.rgb));
  // The text stays inside the tile: the first and last columns are background.
  for (const y of [14, 40, 60]) {
    for (const x of [0, 127]) {
      const at = (y * 128 + x) * 3;
      assert.deepEqual([...long.rgb.subarray(at, at + 3)], [0x1b, 0x1f, 0x2a]);
    }
  }
});

test('TestChannelImageIsWrittenOnce', (t) => {
  const sc = scratch(t);
  const assets = path.join(sc.dir, 'assets');
  const ref = writeChannelImage(assets, 'demo');
  assert.equal(ref, '.generated/inbox/demo.png');
  const file = path.join(assets, '.generated', 'inbox', 'demo.png');
  assert.ok(existsSync(file));
  // An existing file is kept, even when it differs.
  writeFileSync(file, 'mine');
  writeChannelImage(assets, 'demo');
  assert.equal(readFileSync(file, 'utf8'), 'mine');
  assert.ok(!existsSync(`${file}.${process.pid}.tmp`));
});

test('TestChannelImageNeedsWritableDirectory', (t) => {
  const sc = scratch(t);
  const blocker = path.join(sc.dir, 'file');
  writeFileSync(blocker, '');
  assert.throws(() => writeChannelImage(blocker, 'demo'));
});
