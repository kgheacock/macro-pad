import assert from 'node:assert/strict';
import { chmodSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { createYellowFeed, parseItems, yellowEveryMs, yellowTimeoutMs } from '../src/plugins/repos-yellow.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

const HOUR = 60 * 60 * 1000;
const repoRow = (name) => ({
  name,
  key: name,
  url: `https://github.com/kgheacock/${name}`,
  fork: false,
  archived: false,
  pushedAt: null,
  issues: 0,
  prs: 0,
});

// A store holding the named repositories of the default owner.
function storeWith(t, names) {
  const sc = scratch(t);
  const store = openStore(sc.db);
  t.after(() => store.close());
  store.replaceRepos('kgheacock', names.map(repoRow));
  return { sc, store };
}

const item = (id) => ({ id, title: `Ticket ${id}`, url: `https://jira.test/browse/${id}` });
const env = (extra = {}) => ({ INBOX_REPOS_YELLOW_CMD: '/opt/yellow', ...extra });

// An execFile that records its calls and answers from `answer(repo)`: a
// string is stdout; an object is { error, stderr, stdout }.
function stubExec(answer) {
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push({ file, args, options });
    const next = answer(args[0]);
    setImmediate(() => {
      if (typeof next === 'string') callback(null, next, '');
      else callback(next.error ?? null, next.stdout ?? '', next.stderr ?? '');
    });
  };
  return { execFile, calls };
}

// One run: fetch then sync, as the daemon does.
async function run(feed, store, options, logs = []) {
  const answer = await feed.fetch({ store, ...options });
  const changed = feed.sync(store, answer, { log: (line) => logs.push(line) });
  return { answer, changed, logs };
}

test('TestYellowRunsCommandPerRepo', async (t) => {
  const { store } = storeWith(t, ['alpha', 'beta', 'gamma']);
  const { execFile, calls } = stubExec((repo) => JSON.stringify(repo === 'beta' ? [item('B-1'), item('B-2')] : []));
  const now = 1_000_000;
  const { changed } = await run(createYellowFeed(), store, { env: env(), execFile, now });
  assert.deepEqual(calls.map((c) => c.args).sort(), [['alpha'], ['beta'], ['gamma']]);
  for (const call of calls) {
    assert.equal(call.file, '/opt/yellow');
    assert.equal(call.options.shell, false);
    assert.equal(call.options.env.INBOX_REPO, call.args[0]);
    assert.equal(call.options.maxBuffer, 1024 * 1024);
    assert.equal(call.options.timeout, 20_000);
  }
  assert.deepEqual(store.yellowCounts(now), { alpha: 0, beta: 2, gamma: 0 });
  assert.deepEqual(store.yellowItems('beta', now).map((i) => i.id), ['B-1', 'B-2']);
  // Only beta changed its items from nothing.
  assert.equal(changed, 3);
});

test('TestYellowRejectsBadItems', async (t) => {
  assert.throws(() => parseItems('{"id":"A-1"}'), /not a JSON array/);
  assert.throws(() => parseItems('nope'), /not JSON/);
  const kept = parseItems(
    JSON.stringify([
      item('OK-1'),
      { id: 'bad id', title: 'x', url: 'https://x.test/1' },
      { id: '-lead', title: 'x', url: 'https://x.test/1' },
      { id: 'A'.repeat(41), title: 'x', url: 'https://x.test/1' },
      { id: 'HTTP-1', title: 'x', url: 'http://x.test/1' },
      { id: 'NOURL-1', title: 'x' },
      { id: 'OK-1', title: 'again', url: 'https://x.test/2' },
      null,
      'text',
      { id: 'LONG-1', title: 'y'.repeat(300), url: 'https://x.test/3' },
    ]),
  );
  assert.deepEqual(kept.map((i) => i.id), ['OK-1', 'LONG-1']);
  assert.equal(kept[1].title.length, 200);
  const many = Array.from({ length: 201 }, (_, i) => item(`T-${i}`));
  assert.equal(parseItems(JSON.stringify(many)).length, 200);

  // Output that is not an array fails the run and keeps the old items.
  const { store } = storeWith(t, ['alpha']);
  const feed = createYellowFeed();
  const logs = [];
  await run(feed, store, { env: env(), execFile: stubExec(() => '{"a":1}').execFile, now: 1000 }, logs);
  assert.equal(logs.length, 1);
  assert.match(logs[0], /alpha: the output is not a JSON array/);
  assert.deepEqual(store.yellowCounts(1000), {});
});

test('TestYellowKeepsLastGoodOnFailure', async (t) => {
  const { store } = storeWith(t, ['alpha']);
  const feed = createYellowFeed();
  const every = 300_000;
  let now = 1_000_000;
  await run(feed, store, { env: env(), execFile: stubExec(() => JSON.stringify([item('A-1')])).execFile, now });
  assert.deepEqual(store.yellowCounts(now), { alpha: 1 });

  const failures = [
    { error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'boom\nsecond line' },
    { error: Object.assign(new Error('killed'), { killed: true, signal: 'SIGTERM' }) },
    '[not json',
  ];
  const expected = [/failed: boom$/, /did not answer within 20 s/, /not JSON/];
  for (const [i, failure] of failures.entries()) {
    now += every;
    await run(feed, store, { env: env(), execFile: stubExec(() => failure).execFile, now });
    assert.deepEqual(store.yellowItems('alpha', now).map((x) => x.id), ['A-1'], `failure ${i}`);
    assert.match(store.yellowError('alpha'), expected[i]);
  }
});

test('TestYellowLogsOncePerError', async (t) => {
  const { store } = storeWith(t, ['alpha', 'beta']);
  const feed = createYellowFeed();
  const logs = [];
  const exit1 = { error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'HTTP 502' };
  let now = 1_000_000;
  // Two repositories and three runs with the same error: one line.
  for (let i = 0; i < 3; i++) {
    await run(feed, store, { env: env(), execFile: stubExec(() => exit1).execFile, now }, logs);
    now += 300_000;
  }
  assert.equal(logs.length, 1);
  assert.match(logs[0], /HTTP 502/);
  // A new error logs again.
  const down = { error: Object.assign(new Error('x'), { code: 'ENOENT' }) };
  await run(feed, store, { env: env(), execFile: stubExec(() => down).execFile, now }, logs);
  assert.equal(logs.length, 2);
  assert.match(logs[1], /no \/opt\/yellow/);
  // A stderr line is cut to 120 characters.
  const long = { error: Object.assign(new Error('x'), { code: 1 }), stderr: `${'z'.repeat(500)}\nmore` };
  now += 300_000;
  await run(createYellowFeed(), store, { env: env(), execFile: stubExec(() => long).execFile, now }, logs);
  assert.ok(store.yellowError('alpha').length < 200);
  assert.ok(!store.yellowError('alpha').includes('z'.repeat(121)));
});

test('TestYellowTimeoutKills', async (t) => {
  const { sc, store } = storeWith(t, ['alpha']);
  const script = path.join(sc.dir, 'slow.sh');
  writeFileSync(script, '#!/bin/sh\nsleep 5\n');
  chmodSync(script, 0o755);
  const logs = [];
  const started = Date.now();
  await run(createYellowFeed(), store, { env: { INBOX_REPOS_YELLOW_CMD: script }, timeoutMs: 100 }, logs);
  assert.ok(Date.now() - started < 3000, 'waited for the script');
  assert.equal(logs.length, 1);
  assert.match(logs[0], /did not answer within 0\.1 s/);
  assert.match(store.yellowError('alpha'), /did not answer within/);
  // The default is 20 s, and the env variable sets it in seconds.
  assert.equal(yellowTimeoutMs({}), 20_000);
  assert.equal(yellowTimeoutMs({ INBOX_REPOS_YELLOW_TIMEOUT: '5' }), 5000);
  assert.equal(yellowTimeoutMs({ INBOX_REPOS_YELLOW_TIMEOUT: 'x' }), 20_000);
});

test('TestYellowSkipsFreshRepos', async (t) => {
  const { store } = storeWith(t, ['fresh', 'due', 'failed', 'never']);
  const now = 10_000_000;
  store.putYellow('fresh', { items: [], now: now - 100_000 });
  store.putYellow('due', { items: [], now: now - 300_000 });
  store.putYellow('failed', { error: 'broke', now: now - 300_000 });
  const feed = createYellowFeed();
  const { execFile, calls } = stubExec(() => '[]');
  await run(feed, store, { env: env(), execFile, now });
  assert.deepEqual(calls.map((c) => c.args[0]).sort(), ['due', 'failed', 'never']);
  // The interval is a setting, at least 30 s.
  assert.equal(yellowEveryMs({}), 300_000);
  assert.equal(yellowEveryMs({ INBOX_REPOS_YELLOW_EVERY: '60' }), 60_000);
  assert.equal(yellowEveryMs({ INBOX_REPOS_YELLOW_EVERY: '5' }), 30_000);
  const second = stubExec(() => '[]');
  await run(feed, store, { env: env({ INBOX_REPOS_YELLOW_EVERY: '60' }), execFile: second.execFile, now: now + 50_000 });
  assert.deepEqual(second.calls.map((c) => c.args[0]).sort(), ['fresh']);
});

test('TestYellowStaleAnswerCountsZero', (t) => {
  const { store } = storeWith(t, ['alpha']);
  const now = 100 * HOUR;
  store.putYellow('alpha', { items: [item('A-1'), item('A-2')], now: now - 23 * HOUR });
  assert.deepEqual(store.yellowCounts(now), { alpha: 2 });
  assert.equal(store.yellowItems('alpha', now).length, 2);
  assert.deepEqual(store.yellowCounts(now + 2 * HOUR), {});
  assert.deepEqual(store.yellowItems('alpha', now + 2 * HOUR), []);
  assert.deepEqual(store.yellowItems('nobody', now), []);
});

test('TestYellowNoCommandRunsNothing', async (t) => {
  const { store } = storeWith(t, ['alpha', 'beta']);
  const { execFile, calls } = stubExec(() => '[]');
  const logs = [];
  const { changed } = await run(createYellowFeed(), store, { env: {}, execFile }, logs);
  assert.equal(calls.length, 0);
  assert.deepEqual(logs, []);
  assert.equal(changed, 0);
  assert.deepEqual(store.yellowCounts(), {});
});
