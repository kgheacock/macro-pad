import assert from 'node:assert/strict';
import test from 'node:test';
import { getPlugin } from '../src/plugins/index.js';
import ping, { CHANNEL, createPingRenderer, press, shortcutName } from '../src/plugins/ping.js';
import { openStore } from '../src/store.js';
import { run, scratch } from '../testlib/helpers.js';

function storeFor(t) {
  const sc = scratch(t);
  const store = openStore(sc.db, undefined, { plugin: 'ping' });
  t.after(() => store.close());
  return { sc, store };
}

// An execFile that records its calls and answers with `answer`:
// null for success, or an error object for a failure.
function fakeExec(answer = null) {
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push({ file, args, options });
    if (answer === null) callback(null, '', '');
    else callback(answer, '', answer.stderr ?? '');
  };
  return { calls, execFile };
}

const latest = (store) => store.recent(0).find((row) => row.channel === CHANNEL);
const childOf = (store) => {
  const out = createPingRenderer({}).plan({ unseen: store.recent(0) });
  return out.map((m) => m.setLayerKey).find((k) => k.id === 'run');
};
const base = { parent: 'ping', child: 'run', deps: { platform: 'darwin' } };

test('the ping plugin is listed', (t) => {
  assert.equal(getPlugin('ping'), ping);
  const { sc } = storeFor(t);
  const result = run(['plugins'], { sc });
  assert.equal(result.status, 0);
  assert.match(result.stdout, /^ping\b/m);
});

test('runs shortcuts run Ping with execFile', async (t) => {
  const { store } = storeFor(t);
  const { calls, execFile } = fakeExec();
  await press({ ...base, store, deps: { ...base.deps, execFile } });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].file, 'shortcuts');
  assert.deepEqual(calls[0].args, ['run', 'Ping']);
  assert.equal(calls[0].options.shell, false);
});

test('closes stdin so shortcuts does not wait for input', async (t) => {
  const { store } = storeFor(t);
  let ended = 0;
  const execFile = (file, args, options, callback) => {
    callback(null, '', '');
    return { stdin: { end: () => ended++ } };
  };
  await press({ ...base, store, deps: { ...base.deps, execFile } });
  assert.equal(ended, 1);
});

test('INBOX_PING_SHORTCUT names the shortcut', async (t) => {
  const { store } = storeFor(t);
  const { calls, execFile } = fakeExec();
  await press({ ...base, store, env: { INBOX_PING_SHORTCUT: 'Ping Two' }, deps: { ...base.deps, execFile } });
  assert.deepEqual(calls[0].args, ['run', 'Ping Two']);
});

test('sets DONE', async (t) => {
  const { store } = storeFor(t);
  const { execFile } = fakeExec();
  await press({ ...base, store, deps: { ...base.deps, execFile } });
  assert.equal(latest(store).status, 'done');
  assert.equal(childOf(store).status, 'DONE');
});

test('sets RUNNING while the shortcut runs', async (t) => {
  const { store } = storeFor(t);
  let seen;
  const execFile = (file, args, options, callback) => {
    seen = childOf(store).status;
    callback(null, '', '');
  };
  await press({ ...base, store, deps: { ...base.deps, execFile } });
  assert.equal(seen, 'RUNNING');
});

test('sets ERROR and logs the exit code', async (t) => {
  const { store } = storeFor(t);
  const lines = [];
  const { execFile } = fakeExec(Object.assign(new Error('boom'), { code: 1, stderr: 'Error: Shortcut not found\n' }));
  await press({ ...base, store, log: (line) => lines.push(line), deps: { ...base.deps, execFile } });
  assert.equal(latest(store).status, 'failed');
  assert.equal(childOf(store).status, 'ERROR');
  assert.equal(lines.length, 1);
  assert.match(lines[0], /exit code 1/);
  assert.match(lines[0], /Shortcut not found/);
});

test('times out after 20 s', async (t) => {
  const { store } = storeFor(t);
  const lines = [];
  const { calls, execFile } = fakeExec(Object.assign(new Error('killed'), { killed: true, signal: 'SIGTERM' }));
  await press({ ...base, store, log: (line) => lines.push(line), deps: { ...base.deps, execFile } });
  assert.equal(calls[0].options.timeout, 20000);
  assert.equal(latest(store).status, 'failed');
  assert.match(lines[0], /did not answer within 20 s/);
});

test('refuses a name that starts with a dash', async (t) => {
  const { store } = storeFor(t);
  const lines = [];
  const { calls, execFile } = fakeExec();
  await press({ ...base, store, env: { INBOX_PING_SHORTCUT: '-x' }, log: (line) => lines.push(line), deps: { ...base.deps, execFile } });
  assert.equal(calls.length, 0);
  assert.match(lines[0], /refused INBOX_PING_SHORTCUT/);
  assert.equal(latest(store).status, 'failed');
});

test('a shortcut name is a line that starts with a letter or digit', () => {
  assert.equal(shortcutName({}), 'Ping');
  assert.equal(shortcutName({ INBOX_PING_SHORTCUT: '' }), 'Ping');
  assert.equal(shortcutName({ INBOX_PING_SHORTCUT: '9 lives' }), '9 lives');
  for (const bad of ['-x', ' Ping', 'a\nb', 'a'.repeat(129)]) assert.equal(shortcutName({ INBOX_PING_SHORTCUT: bad }), null, bad);
  assert.equal(shortcutName({ INBOX_PING_SHORTCUT: 'a'.repeat(128) }), 'a'.repeat(128));
});

test('runs nothing off macOS and says so', async (t) => {
  const { store } = storeFor(t);
  const lines = [];
  const { calls, execFile } = fakeExec();
  await press({ ...base, store, log: (line) => lines.push(line), deps: { execFile, platform: 'linux' } });
  assert.equal(calls.length, 0);
  assert.match(lines[0], /macOS only/);
  assert.equal(latest(store).status, 'failed');
});

test('a press on another child runs nothing', async (t) => {
  const { store } = storeFor(t);
  const { calls, execFile } = fakeExec();
  await press({ ...base, child: 'other', store, deps: { ...base.deps, execFile } });
  assert.equal(calls.length, 0);
});

test('a second press while the shortcut runs does nothing', async (t) => {
  const { store } = storeFor(t);
  let release;
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push(args);
    release = () => callback(null, '', '');
  };
  const first = press({ ...base, store, deps: { ...base.deps, execFile } });
  await press({ ...base, store, deps: { ...base.deps, execFile } });
  release();
  await first;
  assert.equal(calls.length, 1);
});

test('the renderer draws the key and its child, once, in a slot', () => {
  const renderer = createPingRenderer({ slot: 2 });
  assert.equal(renderer.owns('ping'), true);
  assert.equal(renderer.owns('apps'), false);
  const first = renderer.plan({ unseen: [] }).map((m) => m.setLayerKey);
  assert.deepEqual(first.map((k) => k.id), ['ping', 'run']);
  assert.equal(first[0].slot, 2);
  assert.equal(first[1].parent, 'ping');
  assert.equal(first[1].status, '');
  assert.deepEqual(renderer.plan({ unseen: [] }), []);
  renderer.reset();
  assert.equal(renderer.plan({ unseen: [] }).length, 2);
});
