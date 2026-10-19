import assert from 'node:assert/strict';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { acquirePidFile, startDaemon } from '../src/daemon.js';
import { DaemonRunningError } from '../src/errors.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';
import { TestServer, waitFor } from '../testlib/wsserver.js';

// Starts a daemon on sc's store against `server`, with a fast poll.
function launch(t, sc, server, extra = {}) {
  const logs = [];
  const daemon = startDaemon({
    file: sc.db,
    port: server.port,
    assetsDir: path.join(sc.dir, 'assets'),
    pollMs: 20,
    log: (line) => logs.push(line),
    ...extra,
  });
  t.after(() => daemon.stop());
  return { daemon, logs };
}

async function serve(t, port) {
  const server = new TestServer();
  server.announceLayer(null);
  await server.listen(port);
  t.after(() => server.close());
  return server;
}

function inboxCounts(server) {
  return server.received.filter((m) => m.setLayerKey?.id === 'inbox').map((m) => m.setLayerKey.count);
}

test('TestChangeReachesPad', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  launch(t, sc, server);
  await waitFor(() => inboxCounts(server).length > 0, 3000, 'the first state');
  assert.deepEqual(inboxCounts(server), [0]);

  // Another connection, as `inbox send` in another process would be.
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  const started = Date.now();
  writer.insert('debug', 'hello', sc.root);
  await waitFor(() => inboxCounts(server).includes(1), 1000, 'the new count');
  assert.ok(Date.now() - started < 1000);
  const last = server.received.findLast((m) => m.setLayerKey?.id === 'inbox').setLayerKey;
  assert.equal(last.status, 'WARN');
  assert.equal(last.iconRef, 'gmail.png');
  // The channel arrives as a child, with a picture drawn under the assets dir.
  const child = server.received.find((m) => m.setLayerKey?.id === 'debug').setLayerKey;
  assert.deepEqual(child, {
    id: 'debug',
    parent: 'inbox',
    status: 'WARN',
    count: 1,
    iconRef: '.generated/inbox/debug.png',
  });
  assert.ok(existsSync(path.join(sc.dir, 'assets', '.generated', 'inbox', 'debug.png')));
});

test('TestChildrenFollowLayer', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  const first = writer.insert('a', 'one', sc.root);
  writer.insert('b', 'two', sc.root);
  launch(t, sc, server);
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'a'), 3000, 'both children');

  // The layer opens; channel a is seen. Nothing is removed while it is open.
  const openedAt = server.received.length;
  server.layerChanged('inbox');
  writer.markSeen('a', first);
  await waitFor(() => inboxCounts(server).at(-1) === 1, 1000, 'the new total');
  await new Promise((r) => setTimeout(r, 100));
  assert.ok(!server.received.slice(openedAt).some((m) => m.kind === 'removeLayerKey'));
  const zero = server.received.findLast((m) => m.setLayerKey?.id === 'a').setLayerKey;
  assert.equal(zero.count, 0);
  assert.equal(zero.status, '');

  // It closes: a is removed.
  const closedAt = server.received.length;
  server.layerChanged(null);
  await waitFor(
    () => server.received.slice(closedAt).some((m) => m.kind === 'removeLayerKey' && m.removeLayerKey.id === 'a'),
    1000,
    'the removal',
  );
});

test('TestIdlePollIsCheap', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const real = openStore(sc.db);
  const calls = [];
  const counted = new Proxy(real, {
    get(target, name) {
      const value = target[name];
      if (typeof value !== 'function') return value;
      return (...args) => {
        calls.push(name);
        return value.apply(target, args);
      };
    },
  });
  launch(t, sc, server, { store: counted, pollMs: 10 });
  await waitFor(() => inboxCounts(server).length > 0, 3000, 'the first state');
  const before = calls.length;
  await new Promise((r) => setTimeout(r, 300));
  const idle = calls.slice(before);
  assert.ok(idle.length >= 5, `expected polls, got ${idle.length}`);
  assert.deepEqual([...new Set(idle)], ['dataVersion']);
  // A commit makes the next poll read the table, once.
  const writer = openStore(sc.db);
  t.after(() => {
    writer.close();
    real.close();
  });
  writer.insert('debug', 'x', sc.root);
  await waitFor(() => calls.slice(before).includes('unseen'), 1000, 'a table read');
});

test('TestConnectLate', async (t) => {
  const sc = scratch(t);
  // Reserve a free port, then leave it closed.
  const probe = await serve(t);
  const port = probe.port;
  await probe.close();
  const writer = openStore(sc.db);
  writer.insert('debug', 'waiting', sc.root);
  t.after(() => writer.close());

  const { logs } = launch(t, sc, { port });
  await new Promise((r) => setTimeout(r, 700));
  assert.equal(logs.filter((l) => l.startsWith('cannot reach macropadd')).length, 1, logs.join('\n'));

  const server = await serve(t, port);
  await waitFor(() => inboxCounts(server).length > 0, 5000, 'the state after a late start');
  assert.deepEqual(inboxCounts(server), [1]);
  assert.ok(logs.some((l) => l.startsWith('connected to macropadd')));
});

test('TestResendOnReconnect', async (t) => {
  const sc = scratch(t);
  const first = await serve(t);
  const port = first.port;
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  writer.insert('debug', 'one', sc.root);
  launch(t, sc, first);
  await waitFor(() => inboxCounts(first).length > 0, 3000, 'the first state');

  // While macropadd is down, the store changes; only the newest state counts.
  await first.close();
  writer.insert('debug', 'two', sc.root);
  writer.insert('debug', 'three', sc.root);
  await new Promise((r) => setTimeout(r, 200));

  const second = await serve(t, port);
  await waitFor(() => inboxCounts(second).length > 0, 5000, 'the state after a restart');
  assert.deepEqual(inboxCounts(second), [3]);
});

test('TestFirstStateWaitsForLayer', async (t) => {
  const sc = scratch(t);
  const server = new TestServer();
  await server.listen();
  t.after(() => server.close());
  launch(t, sc, server);
  await waitFor(() => server.connections === 1, 3000, 'a connection');
  await new Promise((r) => setTimeout(r, 200));
  assert.equal(server.received.length, 0);
  server.layerChanged(null);
  await waitFor(() => server.received.length === 1, 1000, 'the state after layerChanged');
});

test('TestEventsReachHook', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const seen = [];
  launch(t, sc, server, { onEvent: (event, ctx) => seen.push([event, ctx]) });
  await waitFor(() => inboxCounts(server).length > 0, 3000, 'the first state');
  server.layerChanged('inbox');
  server.send({ kind: 'event', event: { keyIndex: 2, type: 'press', timestamp: 7 } });
  await waitFor(() => seen.length === 1, 1000, 'the event');
  assert.deepEqual(seen[0], [
    { kind: 'event', keyIndex: 2, type: 'press', timestamp: 7 },
    { active: 'inbox' },
  ]);
});

const press = (server, keyIndex) =>
  server.send({ kind: 'event', event: { keyIndex, type: 'press', timestamp: 1 } });
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
const counts = (store) => Object.fromEntries(store.unseen().map((row) => [row.channel, row.count]));

// Starts a daemon with a child per channel (the last one inserted is first),
// the Inbox layer open, and a slow poll so only a press can change the pad.
async function openInbox(t, sc, channels) {
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  for (const channel of [...channels].reverse()) writer.insert(channel, 'one', sc.root);
  launch(t, sc, server, { pollMs: 10_000 });
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === channels[0]), 3000, 'the children');
  server.layerChanged('inbox');
  return { server, writer };
}

test('TestPressMarksChannel', async (t) => {
  const sc = scratch(t);
  const { server, writer } = await openInbox(t, sc, ['a', 'b', 'c']);
  // The up key and keys with no child mark nothing.
  for (const key of [3, 4, 5]) press(server, key);
  await pause(150);
  assert.deepEqual(counts(writer), { a: 1, b: 1, c: 1 });
  press(server, 0);
  await waitFor(() => counts(writer).a === undefined, 1000, 'a seen');
  press(server, 1);
  await waitFor(() => counts(writer).b === undefined, 1000, 'b seen');
  press(server, 2);
  await waitFor(() => counts(writer).c === undefined, 1000, 'c seen');
});

test('TestPressMarksChannelKeyMap', async (t) => {
  const sc = scratch(t);
  const { server, writer } = await openInbox(t, sc, ['a', 'b', 'c', 'd', 'e']);
  // Key 3 is the up key, so keys 4 and 5 are children 3 and 4.
  press(server, 4);
  await waitFor(() => counts(writer).d === undefined, 1000, 'd seen');
  press(server, 5);
  await waitFor(() => counts(writer).e === undefined, 1000, 'e seen');
  assert.deepEqual(counts(writer), { a: 1, b: 1, c: 1 });
});

test('TestOpenPressMarksNothing', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  writer.insert('a', 'one', sc.root);
  // Inbox is on key 0. Its press arrives while the top layer is active, and
  // so does a press on any other key.
  launch(t, sc, server, { pollMs: 10_000, slot: 0 });
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'a'), 3000, 'the child');
  press(server, 0);
  press(server, 1);
  await pause(150);
  assert.deepEqual(counts(writer), { a: 1 });
  // Once the layer is open the same key marks it.
  server.layerChanged('inbox');
  press(server, 0);
  await waitFor(() => counts(writer).a === undefined, 1000, 'a seen');
});

test('TestLateMessageStaysUnseen', async (t) => {
  const sc = scratch(t);
  const { server, writer } = await openInbox(t, sc, ['a']);
  writer.insert('a', 'late', sc.root);
  press(server, 0);
  // Only the message the key showed is marked; the late one stays.
  await waitFor(() => counts(writer).a === 1, 1000, 'one message marked');
  await waitFor(() => server.received.findLast((m) => m.setLayerKey?.id === 'a').setLayerKey.count === 1, 1000, 'count 1');
  assert.equal(inboxCounts(server).at(-1), 1);
});

test('TestMarkSendsAtOnce', async (t) => {
  const sc = scratch(t);
  // The poll is 10 s, so only the press can send the new counts.
  const { server } = await openInbox(t, sc, ['a', 'b']);
  const before = server.received.length;
  const started = Date.now();
  press(server, 0);
  await waitFor(
    () => server.received.slice(before).some((m) => m.setLayerKey?.id === 'inbox'),
    1000,
    'the new total',
  );
  assert.ok(Date.now() - started < 1000);
  const sent = server.received.slice(before).map((m) => m.setLayerKey);
  assert.equal(sent.find((k) => k.id === 'inbox').count, 1);
  assert.equal(sent.find((k) => k.id === 'a').count, 0);
});

test('TestPidFile', (t) => {
  const sc = scratch(t);
  mkdirSync(path.dirname(sc.db), { recursive: true });
  const logs = [];
  const release = acquirePidFile(sc.db, { log: (l) => logs.push(l) });
  assert.throws(
    () => acquirePidFile(sc.db, { pid: 4242, alive: () => true }),
    (e) => e instanceof DaemonRunningError && e.message.includes(`(pid ${process.pid})`),
  );
  release();
  // A stale file is replaced.
  writeFileSync(`${sc.db}.daemon.pid`, '999999\n');
  const second = acquirePidFile(sc.db, { log: (l) => logs.push(l), alive: () => false });
  assert.ok(logs.some((l) => l.includes('stale')));
  second();
});
