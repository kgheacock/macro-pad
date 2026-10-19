import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { DatabaseSync } from 'node:sqlite';
import { StoreError } from '../src/errors.js';
import { SCHEMA_VERSION, openStore } from '../src/store.js';
import { scratch, start } from '../testlib/helpers.js';

test('TestOpenCreatesSchema', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  store.close();
  const raw = new DatabaseSync(db);
  assert.equal(raw.prepare('PRAGMA user_version').get().user_version, SCHEMA_VERSION);
  assert.equal(raw.prepare('PRAGMA journal_mode').get().journal_mode, 'wal');
  raw.close();
});

test('TestMarkSeenUpTo', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  const a1 = store.insert('a', 'one', '/x');
  const a2 = store.insert('a', 'two', '/x');
  const b1 = store.insert('b', 'other', '/x');
  const a3 = store.insert('a', 'three', '/x');
  assert.equal(store.markSeen('a', a2), 2);
  const rows = store.list({ includeSeen: true });
  const seen = Object.fromEntries(rows.map((r) => [r.id, r.seen_at !== null]));
  assert.deepEqual(seen, { [a1]: true, [a2]: true, [b1]: false, [a3]: false });
  // Marking again changes nothing.
  assert.equal(store.markSeen('a', a2), 0);
  store.close();
});

test('TestUnseen', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  store.insert('a', 'one', '/x', { now: 1000 });
  const a2 = store.insert('a', 'two', '/x', { now: 2000 });
  const b1 = store.insert('b', 'three', '/x', { now: 3000 });
  assert.deepEqual(store.unseen(), [
    { channel: 'a', count: 2, newestId: a2, createdAt: 2000, here: null },
    { channel: 'b', count: 1, newestId: b1, createdAt: 3000, here: null },
  ]);
  assert.equal(store.count(), 3);
  assert.equal(store.count('a'), 2);
  store.markSeen('a', a2);
  assert.deepEqual(
    store.unseen().map((r) => r.channel),
    ['b'],
  );
  assert.equal(store.markAllSeen(), 1);
  assert.deepEqual(store.unseen(), []);
  store.close();
});

test('TestChannels', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  t.after(() => store.close());
  assert.deepEqual(store.channels(), []);
  store.insert('b', 'one', '/x');
  const a = store.insert('a', 'one', '/x');
  store.insert('b', 'two', '/x');
  store.markSeen('a', a);
  // A channel with every message seen is still listed.
  assert.deepEqual(store.channels(), ['a', 'b']);
});

test('TestConcurrentWriters', async (t) => {
  const sc = scratch(t);
  const lines = Array.from({ length: 1000 }, (_, i) => `line ${i}`).join('\n');
  const runs = Array.from({ length: 8 }, (_, i) => {
    const { child, exited } = start(['pipe', `w${i}`], { sc });
    child.stdin.end(lines);
    return exited;
  });
  for (const { status, stderr } of await Promise.all(runs)) {
    assert.equal(status, 0, stderr);
    assert.doesNotMatch(stderr, /locked|busy/i);
  }
  const store = openStore(sc.db);
  assert.equal(store.count(), 8000);
  store.close();
});

test('TestNewerSchemaRefused', (t) => {
  const { db } = scratch(t);
  openStore(db).close();
  const raw = new DatabaseSync(db);
  raw.exec('PRAGMA user_version = 7');
  raw.close();
  const before = createHash('sha256').update(readFileSync(db)).digest('hex');
  assert.throws(
    () => openStore(db),
    (e) =>
      e instanceof StoreError &&
      e.message.includes('schema 7') &&
      e.message.includes(`reads up to ${SCHEMA_VERSION}`),
  );
  const after = createHash('sha256').update(readFileSync(db)).digest('hex');
  assert.equal(after, before);
});

test('TestStatusStoredAndDefaulted', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  store.insert('a', 'plain', '/x');
  store.insert('a', 'bad', '/x', { status: 'error' });
  store.insertMany('a', ['m1', 'm2'], '/x', { status: 'warn' });
  assert.deepEqual(store.list().map((m) => [m.body, m.status]), [
    ['plain', 'info'], ['bad', 'error'], ['m1', 'warn'], ['m2', 'warn'],
  ]);
  store.close();
});

test('TestMigrateV1ToV2', (t) => {
  const { db } = scratch(t);
  mkdirSync(path.dirname(db), { recursive: true });
  const raw = new DatabaseSync(db);
  raw.exec(`CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, channel TEXT NOT NULL, body TEXT NOT NULL,
    created_at INTEGER NOT NULL, seen_at INTEGER, cwd TEXT NOT NULL);
    CREATE INDEX messages_unseen ON messages (channel, id) WHERE seen_at IS NULL;
    INSERT INTO messages (channel, body, created_at, cwd) VALUES ('a', 'old', 1, '/x');
    PRAGMA user_version = 1;`);
  raw.close();
  const store = openStore(db);
  assert.deepEqual(store.list().map((m) => [m.body, m.status]), [['old', 'info']]);
  store.close();
  const check = new DatabaseSync(db);
  assert.equal(check.prepare('PRAGMA user_version').get().user_version, SCHEMA_VERSION);
  check.close();
});

test('TestMigrateToHere', (t) => {
  const { db } = scratch(t);
  mkdirSync(path.dirname(db), { recursive: true });
  const raw = new DatabaseSync(db);
  raw.exec(`CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, channel TEXT NOT NULL, body TEXT NOT NULL,
    created_at INTEGER NOT NULL, seen_at INTEGER, cwd TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'info');
    CREATE INDEX messages_unseen ON messages (channel, id) WHERE seen_at IS NULL;
    INSERT INTO messages (channel, body, created_at, cwd) VALUES ('a', 'old', 1, '/x');
    PRAGMA user_version = 2;`);
  raw.close();
  const store = openStore(db);
  assert.deepEqual(store.list().map((m) => [m.body, m.here]), [['old', null]]);
  assert.equal(store.channelHere('a'), null);
  store.setChannelHere('a', 'app:com.tinyspeck.slackmacgap');
  assert.equal(store.unseen()[0].here, 'app:com.tinyspeck.slackmacgap');
  store.close();
  const check = new DatabaseSync(db);
  assert.equal(check.prepare('PRAGMA user_version').get().user_version, 3);
  check.close();
});

test('TestChannelFallback', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  const mine = 'iterm2:11111111-2222-3333-4444-555555555555';
  const fallback = 'app:com.tinyspeck.slackmacgap';
  store.insert('a', 'none', '/x');
  store.insert('b', 'has one', '/x', { here: mine });
  const hereOf = () => Object.fromEntries(store.unseen().map((r) => [r.channel, r.here]));
  assert.deepEqual(hereOf(), { a: null, b: mine });

  // A fallback fills in a message with no here, and a message here wins.
  store.setChannelHere('a', fallback);
  store.setChannelHere('b', fallback);
  assert.deepEqual(hereOf(), { a: fallback, b: mine });
  assert.equal(store.channelHere('a'), fallback);

  // The newest unseen row decides, not the oldest.
  store.insertMany('a', ['two', 'three'], '/x', { here: mine });
  assert.equal(hereOf().a, mine);
  store.insert('a', 'four', '/x');
  assert.equal(hereOf().a, fallback);

  // Setting again replaces; clearing removes and says whether it did.
  store.setChannelHere('a', 'url:https://example.com/');
  assert.equal(store.channelHere('a'), 'url:https://example.com/');
  assert.equal(store.clearChannelHere('a'), true);
  assert.equal(store.clearChannelHere('a'), false);
  assert.equal(hereOf().a, null);
  store.close();
});
