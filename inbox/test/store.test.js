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
    { channel: 'a', count: 2, byStatus: { info: 2 }, newestId: a2, createdAt: 2000, here: null },
    { channel: 'b', count: 1, byStatus: { info: 1 }, newestId: b1, createdAt: 3000, here: null },
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

test('TestUnseenByStatus', (t) => {
  const { db } = scratch(t);
  const store = openStore(db);
  t.after(() => store.close());
  const w = store.insert('a', 'one', '/x', { status: 'waiting' });
  store.insert('a', 'two', '/x', { status: 'done' });
  store.insert('a', 'three', '/x', { status: 'done' });
  assert.deepEqual(store.unseen()[0].byStatus, { waiting: 1, done: 2 });
  // A seen message leaves the counts.
  store.markSeen('a', w);
  assert.deepEqual(store.unseen()[0].byStatus, { done: 2 });
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
  assert.equal(check.prepare('PRAGMA user_version').get().user_version, SCHEMA_VERSION);
  check.close();
});

test('TestMigrateToPlugins', (t) => {
  const { db } = scratch(t);
  mkdirSync(path.dirname(db), { recursive: true });
  const raw = new DatabaseSync(db);
  raw.exec(`CREATE TABLE messages (
    id INTEGER PRIMARY KEY AUTOINCREMENT, channel TEXT NOT NULL, body TEXT NOT NULL,
    created_at INTEGER NOT NULL, seen_at INTEGER, cwd TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'info', here TEXT);
    CREATE INDEX messages_unseen ON messages (channel, id) WHERE seen_at IS NULL;
    CREATE TABLE channel_here (channel TEXT PRIMARY KEY, here TEXT NOT NULL);
    INSERT INTO messages (channel, body, created_at, cwd) VALUES ('a', 'old', 1, '/x');
    INSERT INTO channel_here (channel, here) VALUES ('a', 'app:com.tinyspeck.slackmacgap');
    PRAGMA user_version = 3;`);
  raw.close();
  // Rows and fallbacks of a schema 3 file belong to basic.
  const basic = openStore(db);
  assert.deepEqual(basic.list().map((m) => m.body), ['old']);
  assert.equal(basic.channelHere('a'), 'app:com.tinyspeck.slackmacgap');
  basic.close();
  const ai = openStore(db, undefined, { plugin: 'ai' });
  assert.deepEqual(ai.list(), []);
  assert.equal(ai.channelHere('a'), null);
  ai.close();
  const check = new DatabaseSync(db);
  assert.equal(check.prepare('PRAGMA user_version').get().user_version, 4);
  check.close();
});

test('TestPluginScope', (t) => {
  const { db } = scratch(t);
  const basic = openStore(db);
  const ai = openStore(db, undefined, { plugin: 'ai' });
  t.after(() => {
    basic.close();
    ai.close();
  });
  // One channel name in both plugins.
  const b = basic.insert('a', 'mine', '/x');
  ai.insertMany('a', ['agent 1', 'agent 2'], '/x', { status: 'waiting' });
  assert.deepEqual(basic.list().map((m) => m.body), ['mine']);
  assert.deepEqual(ai.list().map((m) => m.body), ['agent 1', 'agent 2']);
  assert.equal(basic.count(), 1);
  assert.equal(ai.count(), 2);
  assert.deepEqual(ai.unseen().map((r) => [r.channel, r.count]), [['a', 2]]);
  assert.deepEqual(basic.channels(), ['a']);

  // A mark in one plugin leaves the other, even past its ids.
  assert.equal(ai.markSeen('a', b + 10), 2);
  assert.equal(basic.count(), 1);
  assert.equal(basic.markAllSeen(), 1);
  assert.equal(ai.count(), 0);

  // A fallback "here" belongs to one plugin.
  ai.setChannelHere('a', 'app:com.tinyspeck.slackmacgap');
  assert.equal(basic.channelHere('a'), null);
  basic.setChannelHere('a', 'url:https://example.com/');
  assert.equal(ai.channelHere('a'), 'app:com.tinyspeck.slackmacgap');
  assert.equal(basic.clearChannelHere('a'), true);
  assert.equal(ai.channelHere('a'), 'app:com.tinyspeck.slackmacgap');
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
