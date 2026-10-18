import assert from 'node:assert/strict';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
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
  store.insert('a', 'one', '/x', 1000);
  const a2 = store.insert('a', 'two', '/x', 2000);
  const b1 = store.insert('b', 'three', '/x', 3000);
  assert.deepEqual(store.unseen(), [
    { channel: 'a', count: 2, newestId: a2, createdAt: 2000 },
    { channel: 'b', count: 1, newestId: b1, createdAt: 3000 },
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
