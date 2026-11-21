import assert from 'node:assert/strict';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { acquirePidFile, pidPath, startDaemon } from '../src/daemon.js';
import { DaemonRunningError } from '../src/errors.js';
import repos, { createReposRenderer } from '../src/plugins/repos.js';
import { createCountsState, feed as reposFeed, fetchFeed } from '../src/repolist.js';
import { fakeGitHub, isGraphql } from '../testlib/github.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';
import { emojiFor } from '../src/emoji.js';
import { TestServer, waitFor } from '../testlib/wsserver.js';

// Starts a daemon on sc's store against `server`, with a fast poll.
function launch(t, sc, server, extra = {}) {
  const logs = [];
  const daemon = startDaemon({
    file: sc.db,
    port: server.port,
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
  // The channel arrives as a child with its name over its emoji (task 0073),
  // and no file is written.
  const child = server.received.find((m) => m.setLayerKey?.id === 'debug').setLayerKey;
  assert.deepEqual(child, {
    id: 'debug',
    parent: 'inbox',
    status: 'WARN',
    count: 1,
    iconText: 'debug',
    iconEmoji: [emojiFor('repo:debug').iconRef],
  });
  assert.ok(!existsSync(path.join(sc.dir, 'assets')), 'the daemon wrote under the assets directory');
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
    { active: 'inbox', path: ['inbox'] },
  ]);
});

const press = (server, keyIndex) =>
  server.send({ kind: 'event', event: { keyIndex, type: 'press', timestamp: 1 } });
// A press on a child, as macropadd sends it: the event, then the child it
// landed on (task 0078).
const pressChild = (server, parent, child, keyIndex = 0) => {
  press(server, keyIndex);
  server.send({ kind: 'childPressed', childPressed: { keyIndex, parent, child } });
};
const pause = (ms) => new Promise((r) => setTimeout(r, ms));
const counts = (store) => Object.fromEntries(store.unseen().map((row) => [row.channel, row.count]));

// Starts a daemon with a child per channel (the last one inserted is first),
// the Inbox layer open, and a slow poll so only a press can change the pad.
async function openInbox(t, sc, channels, { heres = {}, ...extra } = {}) {
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  for (const channel of [...channels].reverse()) {
    writer.insert(channel, 'one', sc.root, { here: heres[channel] ?? null });
  }
  const { logs } = launch(t, sc, server, { pollMs: 10_000, ...extra });
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === channels[0]), 3000, 'the children');
  server.layerChanged('inbox');
  return { server, writer, logs };
}

test('TestPressMarksChannel', async (t) => {
  const sc = scratch(t);
  const { server, writer } = await openInbox(t, sc, ['a', 'b', 'c']);
  // The up key and keys with no child mark nothing.
  for (const key of [3, 4, 5]) press(server, key);
  await pause(150);
  assert.deepEqual(counts(writer), { a: 1, b: 1, c: 1 });
  pressChild(server, 'inbox', 'a', 0);
  await waitFor(() => counts(writer).a === undefined, 1000, 'a seen');
  pressChild(server, 'inbox', 'b', 1);
  await waitFor(() => counts(writer).b === undefined, 1000, 'b seen');
  pressChild(server, 'inbox', 'c', 2);
  await waitFor(() => counts(writer).c === undefined, 1000, 'c seen');
});

// Task 0078's DoD-7: seven channels are seven children, and a press on a
// channel of page 2 marks that channel, whatever key it is on.
test('TestPressMarksChannelOnPage2', async (t) => {
  const sc = scratch(t);
  const channels = ['a', 'b', 'c', 'd', 'e', 'f', 'g'];
  const { server, writer } = await openInbox(t, sc, channels);
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'g'), 1000, 'the seventh child');
  const children = server.received.filter((m) => m.setLayerKey?.parent === 'inbox').map((m) => m.setLayerKey.id);
  assert.deepEqual(children, channels);
  // Page 2 shows e, f, g on keys 0, 1, 2.
  pressChild(server, 'inbox', 'f', 1);
  await waitFor(() => counts(writer).f === undefined, 1000, 'f seen');
  assert.deepEqual(counts(writer), { a: 1, b: 1, c: 1, d: 1, e: 1, g: 1 });
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
  pressChild(server, 'inbox', 'a');
  await waitFor(() => counts(writer).a === undefined, 1000, 'a seen');
});

test('TestLateMessageStaysUnseen', async (t) => {
  const sc = scratch(t);
  const { server, writer } = await openInbox(t, sc, ['a']);
  writer.insert('a', 'late', sc.root);
  pressChild(server, 'inbox', 'a');
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
  pressChild(server, 'inbox', 'a');
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

test('TestPressFocuses', async (t) => {
  const sc = scratch(t);
  const slack = 'app:com.tinyspeck.slackmacgap';
  const tab = 'iterm2:0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9';
  const calls = [];
  let writer;
  // Records what the store held at the moment of the call: the mark comes first.
  const focus = async (here) => calls.push({ here, unseen: counts(writer) });
  const opened = await openInbox(t, sc, ['a', 'b', 'c'], { heres: { a: slack, b: tab }, focus });
  const { server } = opened;
  writer = opened.writer;

  // The up key, an empty key, and a channel with no here call nothing.
  for (const key of [3, 4, 5]) press(server, key);
  pressChild(server, 'inbox', 'c', 2);
  await waitFor(() => counts(writer).c === undefined, 1000, 'c seen');
  await pause(100);
  assert.deepEqual(calls, []);

  pressChild(server, 'inbox', 'a', 0);
  await waitFor(() => calls.length === 1, 1000, 'one focus');
  assert.equal(calls[0].here, slack);
  assert.equal(calls[0].unseen.a, undefined, 'a was marked before focus');
  assert.equal(calls[0].unseen.b, 1);
  pressChild(server, 'inbox', 'b', 1);
  await waitFor(() => calls.length === 2, 1000, 'two focus');
  assert.equal(calls[1].here, tab);
  await pause(100);
  assert.equal(calls.length, 2);
});

test('TestOpenPressFocusesNothing', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  writer.insert('a', 'one', sc.root, { here: 'app:com.tinyspeck.slackmacgap' });
  const calls = [];
  launch(t, sc, server, { pollMs: 10_000, slot: 0, focus: async (here) => calls.push(here) });
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'a'), 3000, 'the child');
  press(server, 0);
  press(server, 1);
  await pause(150);
  assert.deepEqual(calls, []);
  assert.deepEqual(counts(writer), { a: 1 });
});

test('TestFailedFocusKeepsMark', async (t) => {
  const sc = scratch(t);
  const bad = 'iterm2:0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9';
  const { server, writer, logs } = await openInbox(t, sc, ['a'], {
    heres: { a: bad },
    focus: async () => {
      throw new Error('no iTerm session gone');
    },
  });
  pressChild(server, 'inbox', 'a');
  await waitFor(() => logs.some((l) => l.includes('no iTerm session gone')), 1000, 'the log line');
  assert.ok(logs.some((l) => l.includes('iterm2') && l.includes(' a: ')), logs.join('\n'));
  assert.ok(!logs.some((l) => l.includes(bad)), 'the log names the kind, not the value');
  assert.deepEqual(counts(writer), {});
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
  writeFileSync(pidPath(sc.db, 'basic'), '999999\n');
  const second = acquirePidFile(sc.db, { log: (l) => logs.push(l), alive: () => false });
  assert.ok(logs.some((l) => l.includes('stale')));
  second();
});

test('TestPidFilePerPlugin', (t) => {
  const sc = scratch(t);
  mkdirSync(path.dirname(sc.db), { recursive: true });
  // A basic and a repos daemon share one store, each with its own pid file.
  const basic = acquirePidFile(sc.db, { plugin: 'basic', pid: 4242, alive: () => true });
  const second = acquirePidFile(sc.db, { plugin: 'repos', pid: 4343, alive: () => true });
  assert.ok(existsSync(`${sc.db}.basic.daemon.pid`));
  assert.ok(existsSync(`${sc.db}.repos.daemon.pid`));
  // A second repos daemon is refused.
  assert.throws(
    () => acquirePidFile(sc.db, { plugin: 'repos', pid: 4444, alive: () => true }),
    (e) => e instanceof DaemonRunningError && e.message.includes('repos daemon') && e.message.includes('(pid 4343)'),
  );
  basic();
  second();
});

// The repos plugin with a feed that answers from `answers`, one per run; an
// Error is thrown as a failed read. The answer is { owner, repos }.
function fedRepos(answers) {
  let runs = 0;
  const plugin = {
    ...repos,
    feed: {
      ...repos.feed[0],
      fetch: async () => {
        const next = answers[Math.min(runs, answers.length - 1)];
        runs++;
        if (next instanceof Error) throw next;
        return next;
      },
    },
  };
  return { plugin, runs: () => runs };
}

const oneRepo = (extra = {}) => ({
  owner: 'kgheacock',
  repos: [{ name: 'macro-pad', key: 'macro-pad', url: 'u', fork: false, archived: false, pushedAt: null, issues: 0, prs: 0 }],
  cut: false,
  offline: null,
  ...extra,
});

test('TestDaemonFeed', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const writer = openStore(sc.db, undefined, { plugin: 'repos' });
  t.after(() => writer.close());
  writer.insert('github.com.kgheacock.macro-pad.3f2a91c0', 'needs input', sc.root, { status: 'waiting' });
  const { plugin } = fedRepos([
    oneRepo({ repos: [{ name: 'macro-pad', key: 'macro-pad', url: 'u', fork: false, archived: false, pushedAt: null, issues: 3, prs: 2 }] }),
  ]);
  // A slow poll and a slow feed: only the first run, at start, can draw it.
  const started = Date.now();
  launch(t, sc, server, { plugin, pollMs: 10_000, feedMs: 10_000 });
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'repo.macro-pad'), 1000, 'the repo key');
  assert.ok(Date.now() - started < 1000);
  const top = server.received.findLast((m) => m.setLayerKey?.id === 'repos').setLayerKey;
  // The top key shows the totals of the repo keys (task 0092), not the sessions.
  assert.deepEqual([top.status, top.count, top.countGreen, top.countYellow], ['WARN', 3, 2, undefined]);
  const key = server.received.findLast((m) => m.setLayerKey?.id === 'repo.macro-pad').setLayerKey;
  assert.deepEqual([key.count, key.countGreen, key.countYellow], [3, 2, undefined]);
  // The session is in the layer, so the key opens (task 0095).
  assert.deepEqual([key.parent, key.opens, key.status, key.iconText], ['repos', true, 'WARN', 'macro-pad']);
  // The first connect removes the keys of the retired plugins.
  const removed = server.received.filter((m) => m.kind === 'removeLayerKey').map((m) => m.removeLayerKey.id);
  assert.deepEqual(removed, ['ai-inbox', 'github-inbox', 'repos']);
  // A press on a repository key marks nothing: its layer holds no session.
  server.layerChanged('repos');
  pressChild(server, 'repos', 'repo.macro-pad');
  await pause(150);
  assert.deepEqual(counts(writer), { 'github.com.kgheacock.macro-pad.3f2a91c0': 1 });
});

test('TestDaemonFeedFails', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const down = new Error('no gh; install the GitHub CLI or set INBOX_GH');
  const fed = fedRepos([down, down, down, oneRepo()]);
  const { logs } = launch(t, sc, server, { plugin: fed.plugin, feedMs: 20 });
  await waitFor(() => fed.runs() >= 4, 2000, 'four runs');
  await waitFor(() => logs.includes('GitHub repositories read again'), 1000, 'the recovery');
  const failures = logs.filter((line) => line.startsWith('cannot read GitHub'));
  assert.deepEqual(failures, ['cannot read GitHub repositories: no gh; install the GitHub CLI or set INBOX_GH']);
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'repo.macro-pad'), 1000, 'the repo key');
});

// The repos plugin with the repos feed (tasks 0089 and 0097) and a GitHub
// that answers its GraphQL queries from `results`, one per run: an error, or
// { repos: [ghRow] }. The login and the events always answer.
// `fed` records what the renderer is given.
function reposPlugin(results) {
  const seen = [];
  let runs = 0;
  const counts = createCountsState();
  const fetch = ({ env, store, log }) => {
    const next = results[Math.min(runs, results.length - 1)];
    runs++;
    const github = next.repos
      ? fakeGitHub({ contributed: next.repos.map((r) => r.name), repos: Object.fromEntries(next.repos.map((r) => [r.name, r])) })
      : () => next;
    const execFile = (file, args, options, callback) => {
      let got;
      if (isGraphql(args)) got = github(args);
      else if (args[1] === 'user') got = { stdout: 'kgheacock\n' };
      else got = { stdout: '[]' };
      setImmediate(() => callback(got.error ?? null, got.stdout ?? '', got.stderr ?? ''));
    };
    return fetchFeed({ env, store, log, execFile, counts });
  };
  const plugin = {
    ...repos,
    feed: { ...reposFeed, fetch },
    createRenderer: ({ slot }) => {
      const renderer = createReposRenderer({ slot });
      return { ...renderer, plan: (args) => (seen.push(args.fed), renderer.plan(args)) };
    },
  };
  return { plugin, seen, runs: () => runs };
}

const ghRow = (name, issues, prs) => ({ name, issues, prs });
const exit1 = (stderr) => ({ error: Object.assign(new Error('exit 1'), { code: 1 }), stderr });

test('TestReposFeedOffline', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  const kept = { name: 'macro-pad', key: 'macro-pad', url: 'u', fork: false, archived: false, pushedAt: null, issues: 1, prs: 2 };
  writer.replaceRepos('kgheacock', [kept]);
  const fed = reposPlugin([exit1('HTTP 502'), exit1('HTTP 502'), exit1('HTTP 502')]);
  const { logs } = launch(t, sc, server, { plugin: fed.plugin, feedMs: 20 });
  await waitFor(() => fed.runs() >= 3, 2000, 'three runs');
  // One line for three equal failures, and the rows stay.
  assert.deepEqual(logs.filter((line) => line.startsWith('cannot read')), ['cannot read GitHub repositories: gh failed: HTTP 502']);
  assert.deepEqual(writer.listRepos('kgheacock'), [kept]);
});

test('TestReposColdStart', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const writer = openStore(sc.db);
  t.after(() => writer.close());
  const kept = { name: 'macro-pad', key: 'macro-pad', url: 'u', fork: false, archived: false, pushedAt: null, issues: 1, prs: 2 };
  writer.replaceRepos('kgheacock', [kept]);
  // gh fails at start, then answers.
  const fed = reposPlugin([exit1('no network'), { repos: [ghRow('macro-pad', 0, 5)] }]);
  launch(t, sc, server, { plugin: fed.plugin, feedMs: 150 });
  await waitFor(() => fed.seen.some((answer) => answer?.offline), 2000, 'the cold answer');
  const cold = fed.seen.find((answer) => answer?.offline);
  assert.deepEqual(cold.repos, [kept]);
  assert.equal(cold.owner, 'kgheacock');
  assert.match(cold.offline, /gh failed: no network/);
  // The next good read replaces it.
  await waitFor(() => fed.seen.some((answer) => answer && answer.offline === null), 2000, 'the live answer');
  assert.deepEqual(writer.listRepos('kgheacock').map((r) => [r.name, r.issues, r.prs]), [['macro-pad', 0, 5]]);
});

test('TestDaemonRunsEveryFeed', async (t) => {
  // The first feed draws; the second rejects every run.
  {
    const sc = scratch(t);
    const server = await serve(t);
    let bad = 0;
    const plugin = {
      ...repos,
      feed: [
        { ...reposFeed, fetch: async () => oneRepo(), sync: () => 0 },
        {
          source: 'second feed',
          everyMs: 20,
          fetch: async () => {
            bad++;
            throw new Error('down');
          },
          sync: () => 0,
        },
      ],
    };
    const { logs } = launch(t, sc, server, { plugin, feedMs: 20 });
    await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'repo.macro-pad'), 1000, 'the repo key');
    await waitFor(() => bad >= 3, 1000, 'three failed runs');
    // One line for three equal failures, and the first feed was not logged.
    assert.deepEqual(logs.filter((line) => line.startsWith('cannot read')), ['cannot read second feed: down']);
  }

  // The first feed rejects; the second still runs and its rows draw.
  {
    const sc = scratch(t);
    const server = await serve(t);
    let first = 0;
    const plugin = {
      name: 'basic',
      rules: { id: 'inbox', icon: 'gmail.png' },
      feed: [
        {
          source: 'first feed',
          everyMs: 20,
          fetch: async () => {
            first++;
            throw new Error('down');
          },
          sync: () => 0,
        },
        {
          source: 'second feed',
          everyMs: 20,
          fetch: async ({ store }) => store,
          sync: (store) => {
            if (store.count('news') === 0) store.insert('news', 'from the second feed', sc.root);
            return 1;
          },
        },
      ],
    };
    const real = await import('../src/plugins/basic.js');
    plugin.rules = real.default.rules;
    plugin.createRenderer = real.default.createRenderer;
    launch(t, sc, server, { plugin: { ...real.default, feed: plugin.feed }, feedMs: 20 });
    await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'news'), 1000, 'the key of the second feed');
    assert.ok(first >= 1);
  }
});
