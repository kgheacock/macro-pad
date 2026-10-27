import assert from 'node:assert/strict';
import { execFileSync, spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, utimesSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { detectRun, parseRunOverrides, runLine, shellWord } from '../src/apps.js';
import { startDaemon } from '../src/daemon.js';
import { emojiFor } from '../src/emoji.js';
import apps, { appName, chooseApps, createAppsRenderer, press, scan, startOfDay } from '../src/plugins/apps.js';
import { openStore } from '../src/store.js';
import { run, scratch } from '../testlib/helpers.js';
import { TestServer, waitFor } from '../testlib/wsserver.js';

const HOUR = 60 * 60 * 1000;
// Noon today, so "today" and "yesterday" are hours apart whatever the clock.
const NOON = startOfDay(Date.now()) + 12 * HOUR;

// A fake app root under the source root: ~/source/github.com/acme.
function fakeRoot(t) {
  const sc = scratch(t);
  const root = path.join(sc.root, 'github.com', 'acme');
  mkdirSync(root, { recursive: true });
  const env = { INBOX_SOURCE_ROOT: sc.root, INBOX_APPS_ROOT: root };
  return { sc, root, env };
}

// An app folder whose .git was last touched at `at`.
function fakeApp(root, dirName, at, files = {}) {
  const dir = path.join(root, dirName);
  mkdirSync(path.join(dir, '.git', 'logs'), { recursive: true });
  for (const file of ['logs/HEAD', 'index']) {
    const p = path.join(dir, '.git', file);
    writeFileSync(p, '');
    utimesSync(p, at / 1000, at / 1000);
  }
  for (const [name, text] of Object.entries(files)) writeFileSync(path.join(dir, name), text);
  return dir;
}

function storeFor(t, sc, plugin = 'apps') {
  const store = openStore(sc.db, undefined, { plugin });
  t.after(() => store.close());
  return store;
}

const rowsOf = (store) => store.recent(0);
const sets = (messages) => messages.filter((m) => m.setLayerKey).map((m) => m.setLayerKey);
const removed = (messages) => messages.filter((m) => m.removeLayerKey).map((m) => m.removeLayerKey.id);

test('TestAppsChoose', (t) => {
  const { sc, root, env } = fakeRoot(t);
  fakeApp(root, 'alpha', NOON - 2 * HOUR);
  fakeApp(root, 'beta', NOON - HOUR);
  fakeApp(root, 'gamma', NOON - 24 * HOUR);
  fakeApp(root, 'delta', NOON - 24 * HOUR);
  fakeApp(root, 'eps', NOON - 48 * HOUR);
  fakeApp(root, 'site.github.io', NOON - 24 * HOUR);
  mkdirSync(path.join(root, 'not-a-repo'));
  const store = storeFor(t, sc);
  // A Claude session today in a subfolder of delta, at 09:00.
  storeFor(t, sc, 'ai').insert('github.com.acme.delta.web.3f2a91c0', 'working', root, { status: 'running', now: NOON - 3 * HOUR });
  // A session in another org's delta does not count.
  storeFor(t, sc, 'ai').insert('github.com.other.gamma.3f2a91c1', 'working', root, { status: 'running', now: NOON });
  store.insert('eps.pin', 'pinned', root, { status: 'pinned', now: NOON - 5 * HOUR });

  const fed = scan({ env, store, now: NOON });
  assert.deepEqual(fed.apps.map((a) => a.name), ['alpha', 'beta', 'delta', 'eps', 'gamma', 'site-github-io']);
  assert.equal(fed.apps.find((a) => a.name === 'delta').activity, NOON - 3 * HOUR);
  // The pin first, then today's apps, newest first.
  assert.deepEqual(chooseApps(fed, rowsOf(store), 3), ['eps', 'beta', 'alpha']);
  assert.deepEqual(chooseApps(fed, rowsOf(store), 5), ['eps', 'beta', 'alpha', 'delta']);
  // Unpinned, eps was touched two days ago and goes.
  store.insert('eps.pin', 'unpinned', root, { status: 'unpinned', now: NOON - HOUR });
  assert.deepEqual(chooseApps(fed, rowsOf(store), 5), ['beta', 'alpha', 'delta']);
  // Pinned again later than gamma: oldest pin first.
  store.insert('gamma.pin', 'pinned', root, { status: 'pinned', now: NOON - 30 * 60 * 1000 });
  store.insert('eps.pin', 'pinned', root, { status: 'pinned', now: NOON - 10 * 60 * 1000 });
  assert.deepEqual(chooseApps(fed, rowsOf(store), 3), ['gamma', 'eps', 'beta']);

  // No root: the feed says why.
  assert.throws(() => scan({ env: {}, store, now: NOON }), /no INBOX_APPS_ROOT/);
  assert.equal(appName('site.github.io'), 'site-github-io');
  assert.equal(appName('bad name'), null);
});

test('TestAppsDetectRun', (t) => {
  const { root } = fakeRoot(t);
  const pkg = (scripts) => JSON.stringify({ scripts });
  const make = fakeApp(root, 'make', 0, { Makefile: 'VAR := 1\nbuild:\n\techo\ndev: build\n\techo dev\n', 'package.json': pkg({ dev: 'vite' }) });
  assert.deepEqual(detectRun(make), { argv: ['make', 'dev'], label: 'make dev' });
  // An override wins.
  const overrides = parseRunOverrides('make=make serve PORT=1;other=x');
  assert.deepEqual(detectRun(make, overrides.get('make')), { argv: ['make', 'serve', 'PORT=1'], label: 'make serve PORT=1' });
  const serve = fakeApp(root, 'serve', 0, { makefile: 'serve::\n\tpython -m http.server\nstart := no\n' });
  assert.equal(detectRun(serve).label, 'make serve');
  // A Makefile with no run target falls through to package.json.
  const both = fakeApp(root, 'both', 0, { Makefile: 'test:\n\ttrue\n', 'package.json': pkg({ start: 'node .', dev: 'vite' }), 'pnpm-lock.yaml': '' });
  assert.deepEqual(detectRun(both), { argv: ['pnpm', 'run', 'dev'], label: 'pnpm dev' });
  const start = fakeApp(root, 'start', 0, { 'package.json': pkg({ start: 'node .' }), 'yarn.lock': '' });
  assert.deepEqual(detectRun(start).argv, ['yarn', 'run', 'start']);
  assert.equal(detectRun(fakeApp(root, 'npm', 0, { 'package.json': pkg({ dev: 'next dev' }) })).label, 'npm dev');
  assert.equal(detectRun(fakeApp(root, 'none', 0, { 'package.json': pkg({ test: 'x' }) })), null);
  assert.equal(detectRun(fakeApp(root, 'broken', 0, { 'package.json': '{' })), null);
});

// A feed answer for the renderer: apps active at the given hours of today.
const fedOf = (root, list) => ({
  root,
  since: startOfDay(NOON),
  apps: list.map(([name, hoursAgo, runLabel = null]) => ({ name, dirName: name, dir: path.join(root, name), activity: NOON - hoursAgo * HOUR, run: runLabel })),
});

// The app children of the apps key, and the actions of one app.
const appsOf = (keys) => keys.filter((k) => k.parent === 'apps').map((k) => k.id);
const actionsOf = (keys, id) => keys.filter((k) => k.path?.[1] === id);

test('TestAppsKeys', (t) => {
  const { sc, root } = fakeRoot(t);
  const store = storeFor(t, sc);
  const renderer = createAppsRenderer({ slot: 4, env: { INBOX_APPS_COUNT: '2' } });
  // Nothing before the first scan.
  assert.deepEqual(renderer.plan({ unseen: [], active: null, path: [], fed: null }), []);

  let fed = fedOf(root, [['web', 1, 'pnpm dev'], ['api', 2, null], ['old', 30]]);
  renderer.reset(['leftover.pin']);
  let messages = renderer.plan({ unseen: rowsOf(store), active: null, path: [], fed });
  // The first plan after a connect clears what an earlier daemon left.
  assert.deepEqual(messages[0], { kind: 'removeLayerKey', removeLayerKey: { id: 'apps' } });
  const keys = sets(messages);
  assert.deepEqual(keys[0], { id: 'apps', status: '', count: 0, iconText: 'Apps', slot: 4 });
  assert.deepEqual(appsOf(keys), ['app.web', 'app.api']);
  const web = emojiFor('repo:web').iconRef;
  assert.deepEqual(keys.find((k) => k.id === 'app.web'), { id: 'app.web', parent: 'apps', status: '', count: 0, iconText: 'web', iconEmoji: [web] });
  const children = actionsOf(keys, 'app.web');
  assert.deepEqual(children.map((k) => k.id), ['code', 'claude', 'run', 'pull', 'term']);
  assert.deepEqual(children[0].path, ['apps', 'app.web']);
  assert.deepEqual(children.map((k) => k.iconText), ['Code', 'Claude', 'pnpm dev', 'Pull', 'Term']);
  assert.ok(children.every((k) => k.iconEmoji[0] === web));
  assert.equal(actionsOf(keys, 'app.api').find((k) => k.id === 'run').iconText, 'no run');
  assert.ok(renderer.owns('app.web') && !renderer.owns('app.old') && !renderer.owns('inbox'));

  // A run and a pull colour the keys; nothing else is sent again.
  store.insert('web.run', 'pnpm run dev', root, { status: 'running' });
  store.insert('api.pull', 'conflict; rebase aborted', root, { status: 'failed' });
  messages = renderer.plan({ unseen: rowsOf(store), active: null, path: [], fed });
  const changed = sets(messages).map((k) => [k.path?.[1] ?? k.id, k.id, k.status]);
  assert.deepEqual(changed, [
    ['apps', 'apps', 'ERROR'],
    ['app.web', 'app.web', 'RUNNING'],
    ['app.web', 'run', 'RUNNING'],
    ['app.api', 'app.api', 'ERROR'],
    ['app.api', 'pull', 'ERROR'],
  ]);

  // web's layer is open, under apps, while new comes in: nothing goes, and
  // new goes at the end.
  fed = fedOf(root, [['new', 0.5], ['web', 1, 'pnpm dev'], ['api', 2]]);
  messages = renderer.plan({ unseen: rowsOf(store), active: 'app.web', path: ['apps', 'app.web'], fed });
  assert.deepEqual(removed(messages), []);
  assert.deepEqual(appsOf(sets(messages)), ['app.new']);
  // Closed: the apps follow the choice, in order.
  messages = renderer.plan({ unseen: rowsOf(store), active: null, path: [], fed });
  assert.deepEqual(removed(messages), ['app.api', 'app.web', 'app.new']);
  assert.deepEqual(appsOf(sets(messages)), ['app.new', 'app.web']);
  assert.deepEqual(messages.find((m) => m.removeLayerKey).removeLayerKey, { id: 'app.api', parent: 'apps' });
  // The apps are already in order: nothing moves.
  messages = renderer.plan({ unseen: rowsOf(store), active: null, path: [], fed });
  assert.deepEqual(messages, []);
});

// A stand-in for execFile: records each call and answers from `answers`
// (file -> stdout, or an Error).
function fakeExec(answers = {}) {
  const calls = [];
  const execFile = (file, args, options, cb) => {
    calls.push({ file, args, options });
    const answer = typeof answers[file] === 'function' ? answers[file](args) : answers[file];
    if (answer instanceof Error) cb(Object.assign(answer, { code: 1 }), '', answer.message);
    else cb(null, answer ?? '', '');
  };
  return { calls, execFile };
}

// The argument the AppleScript of an osascript call got last: the line it types.
const typed = (call) => call.args.at(-1);
const scriptHas = (call, text) => call.args.includes(text);

test('TestAppsPress', async (t) => {
  const { sc, root, env } = fakeRoot(t);
  const web = fakeApp(root, 'web', NOON, { Makefile: 'dev:\n\ttrue\n' });
  const store = storeFor(t, sc);
  const fed = scan({ env, store, now: NOON });
  const focused = [];
  let tabs = 0;
  const { calls, execFile } = fakeExec({ osascript: (args) => (scriptHas({ args }, 'return unique id of s') ? `UUID-${++tabs}\n` : 'typed\n') });
  const deps = { execFile, platform: 'darwin', node: '/n/node', bin: '/i/inbox.js' };
  const focusOk = async (here) => focused.push(here);
  const go = (child, focus = focusOk) =>
    press({ parent: 'app.web', child, store, file: '/d/inbox.db', env, fed, focus, now: NOON, deps });

  await go('code');
  assert.deepEqual([calls[0].file, calls[0].args], ['open', ['-b', 'com.microsoft.VSCode', web]]);

  // Claude: a session of today in web comes to the front.
  const ai = storeFor(t, sc, 'ai');
  ai.insert('github.com.acme.web.3f2a91c0', 'done', web, { status: 'done', here: 'iterm2:AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA', now: NOON - HOUR });
  await go('claude');
  assert.deepEqual(focused, ['iterm2:AAAAAAAA-AAAA-AAAA-AAAA-AAAAAAAAAAAA']);
  // Its tab is closed: a new tab runs claude, and the next press focuses it.
  calls.length = 0;
  const gone = async () => {
    throw new Error('no iTerm session');
  };
  await go('claude', gone);
  assert.equal(calls.length, 1);
  assert.equal(typed(calls[0]), `cd ${web} && claude`);
  assert.equal(store.channelHere('web.claude'), 'iterm2:UUID-1');

  // Run: a new tab types the inbox run line, quoted.
  calls.length = 0;
  await go('run');
  assert.equal(typed(calls[0]), `cd ${web} && /n/node /i/inbox.js --plugin apps --db /d/inbox.db run web -- make dev`);
  assert.equal(store.channelHere('web.run'), 'iterm2:UUID-2');
  // Not running: the next press types it into the same tab.
  calls.length = 0;
  await go('run');
  assert.deepEqual(calls[0].args.slice(-2), ['UUID-2', typed(calls[0])]);
  assert.ok(typed(calls[0]).endsWith('run web -- make dev'));
  // Running: a press only brings the tab to the front.
  store.insert('web.run', 'make dev', web, { status: 'running' });
  calls.length = 0;
  focused.length = 0;
  await go('run');
  assert.deepEqual([calls.length, focused], [0, ['iterm2:UUID-2']]);

  // Term: a new tab at the root, then the same tab.
  calls.length = 0;
  focused.length = 0;
  await go('term', gone);
  assert.equal(typed(calls[0]), `cd ${web}`);
  await go('term');
  assert.deepEqual(focused, ['iterm2:UUID-3']);

  // No run command: a log line, and nothing runs.
  const logs = [];
  const bare = fakeApp(root, 'bare', NOON);
  calls.length = 0;
  await press({ parent: 'app.bare', child: 'run', store, env, fed: scan({ env, store, now: NOON }), log: (l) => logs.push(l), deps });
  assert.equal(calls.length, 0);
  assert.match(logs[0], /^no run command for bare/);
  assert.ok(existsSync(bare));

  // Words with spaces and quotes are quoted for the shell.
  assert.equal(shellWord("it's here"), `'it'\\''s here'`);
  assert.equal(runLine({ dir: '/a b', name: 'x', argv: ['make', 'dev'], node: 'node', bin: 'b' }), `cd '/a b' && node b --plugin apps run x -- make dev`);
});

// git with a fixed identity, in dir.
const git = (dir, ...args) =>
  execFileSync('git', args, {
    cwd: dir,
    encoding: 'utf8',
    env: { ...process.env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@example.com', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@example.com' },
    stdio: ['ignore', 'pipe', 'pipe'],
  });

test('TestAppsPull', async (t) => {
  const { sc, root, env } = fakeRoot(t);
  const upstream = path.join(sc.dir, 'upstream.git');
  git(sc.dir, 'init', '-q', '--bare', '-b', 'main', upstream);
  const other = path.join(sc.dir, 'other');
  git(sc.dir, 'clone', '-q', upstream, other);
  git(other, 'checkout', '-q', '-b', 'main');
  writeFileSync(path.join(other, 'f'), 'one\n');
  git(other, 'add', 'f');
  git(other, 'commit', '-q', '-m', 'one');
  git(other, 'push', '-q', 'origin', 'main');
  const app = path.join(root, 'web');
  git(root, 'clone', '-q', upstream, app);

  const store = storeFor(t, sc);
  const gitEnv = { ...process.env, ...env, GIT_AUTHOR_NAME: 't', GIT_AUTHOR_EMAIL: 't@example.com', GIT_COMMITTER_NAME: 't', GIT_COMMITTER_EMAIL: 't@example.com' };
  const fed = scan({ env: gitEnv, store });
  const changes = [];
  const go = () => press({ parent: 'app.web', child: 'pull', store, env: gitEnv, fed, changed: () => changes.push(rowsOf(store).find((r) => r.channel === 'web.pull').status) });
  const last = () => store.list({ channel: 'web.pull', includeSeen: true }).at(-1);

  await go();
  assert.deepEqual([last().status, last().body], ['pulled', 'up to date']);
  assert.deepEqual(changes, ['pulling']);

  writeFileSync(path.join(other, 'g'), 'two\n');
  git(other, 'add', 'g');
  git(other, 'commit', '-q', '-m', 'two');
  git(other, 'push', '-q', 'origin', 'main');
  await go();
  assert.deepEqual([last().status, last().body], ['pulled', '1 commit']);

  // A conflict: the rebase is aborted, and the local commit stays.
  writeFileSync(path.join(other, 'f'), 'theirs\n');
  git(other, 'commit', '-q', '-am', 'theirs');
  git(other, 'push', '-q', 'origin', 'main');
  writeFileSync(path.join(app, 'f'), 'mine\n');
  git(app, 'commit', '-q', '-am', 'mine');
  await go();
  assert.deepEqual([last().status, last().body], ['failed', 'conflict; rebase aborted']);
  assert.equal(existsSync(path.join(app, '.git', 'rebase-merge')) || existsSync(path.join(app, '.git', 'rebase-apply')), false);
  assert.equal(git(app, 'log', '-1', '--format=%s').trim(), 'mine');
  assert.equal(git(app, 'status', '--porcelain'), '');

  // Local changes: git's own reason.
  writeFileSync(path.join(app, 'g'), 'dirty\n');
  await go();
  assert.equal(last().status, 'failed');
  assert.match(last().body, /^error: /);
});

test('TestAppsCli', (t) => {
  const { sc, root, env } = fakeRoot(t);
  const web = fakeApp(root, 'web', NOON);
  const list = () =>
    JSON.parse(run(['--plugin', 'apps', 'list', '--all-channels', '--json'], { sc, env }).stdout).map((m) => [m.channel, m.status, m.body]);

  let r = run(['--plugin', 'apps', 'run', 'web', '--', 'sh', '-c', 'exit 3'], { sc, env });
  assert.equal(r.status, 3);
  r = run(['--plugin', 'apps', 'run', 'web', '--', 'sh', '-c', 'exit 0'], { sc, env });
  assert.equal(r.status, 0);
  assert.deepEqual(list(), [
    ['web.run', 'running', 'sh -c exit 3'],
    ['web.run', 'crashed', 'exited with 3'],
    ['web.run', 'running', 'sh -c exit 0'],
    ['web.run', 'stopped', 'exited 0'],
  ]);
  // Without a command, the detected one; with none, exit 2.
  r = run(['--plugin', 'apps', 'run', 'web'], { sc, env, cwd: web });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /no run command in/);

  // pin by name, and from inside the app's folder; unpin.
  assert.equal(run(['--plugin', 'apps', 'pin', 'web'], { sc, env }).stdout, 'pinned web\n');
  mkdirSync(path.join(web, 'src'));
  assert.equal(run(['--plugin', 'apps', 'unpin'], { sc, env, cwd: path.join(web, 'src') }).stdout, 'unpinned web\n');
  assert.deepEqual(list().slice(-2), [
    ['web.pin', 'pinned', 'pinned'],
    ['web.pin', 'unpinned', 'unpinned'],
  ]);
  r = run(['--plugin', 'apps', 'pin', 'nope'], { sc, env });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /no app "nope"/);
  r = run(['--plugin', 'apps', 'pin', 'web'], { sc });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /needs INBOX_APPS_ROOT/);
  r = run(['pin', 'web'], { sc, env });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /use: inbox --plugin apps pin/);
});

test('TestDaemonApps', async (t) => {
  const { sc, root } = fakeRoot(t);
  const server = new TestServer();
  server.announceLayer(null);
  await server.listen();
  t.after(() => server.close());
  const presses = [];
  const plugin = {
    ...apps,
    feed: { ...apps.feed, fetch: async () => fedOf(root, [['web', 1, 'make dev']]) },
    press: async ({ parent, child, fed }) => presses.push([parent, child, fed.apps.length]),
  };
  const logs = [];
  const daemon = startDaemon({ file: sc.db, port: server.port, pollMs: 20, plugin, slot: 2, env: { INBOX_APPS_COUNT: '3' }, log: (l) => logs.push(l) });
  t.after(() => daemon.stop());
  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'term'), 1000, 'the app key');
  assert.equal(server.received.find((m) => m.setLayerKey?.id === 'apps').setLayerKey.slot, 2);
  // A press on another plugin's key is not ours.
  server.send({ kind: 'childPressed', childPressed: { keyIndex: 0, parent: 'inbox', child: 'x', path: ['inbox'] } });
  server.send({ kind: 'childPressed', childPressed: { keyIndex: 0, parent: 'app.web', child: 'code', path: ['apps', 'app.web'] } });
  await waitFor(() => presses.length === 1, 1000, 'the press');
  assert.deepEqual(presses, [['app.web', 'code', 1]]);
  assert.deepEqual(logs.filter((l) => !l.startsWith('connected')), []);
});

test('TestAppsInstall', (t) => {
  const { sc, root } = fakeRoot(t);
  const script = path.join(import.meta.dirname, '..', 'scripts', 'install.sh');
  // A systemctl that does nothing, so the install runs to the end on Linux.
  const stubs = path.join(sc.dir, 'stubs');
  mkdirSync(stubs);
  writeFileSync(path.join(stubs, 'systemctl'), '#!/bin/sh\nexit 0\n', { mode: 0o755 });
  writeFileSync(path.join(stubs, 'uname'), '#!/bin/sh\necho Linux\n', { mode: 0o755 });
  const prefix = path.join(sc.dir, 'prefix');
  const env = { ...process.env, HOME: sc.dir, PREFIX: prefix, PATH: `${stubs}:${process.env.PATH}`, INBOX_APPS_ROOT: '' };
  const install = (...args) => spawnSync('sh', [script, '--no-completions', ...args], { env, encoding: 'utf8' });

  let r = install('--dry-run', '--apps-root', root, '--apps-count', '2');
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.includes(`, INBOX_APPS_ROOT=${root}, INBOX_APPS_COUNT=2)`), r.stdout);
  r = install('--dry-run', '--apps-root', path.join(root, 'nope'));
  assert.equal(r.status, 1);
  assert.equal(r.stderr, `install.sh: --apps-root ${path.join(root, 'nope')} is not a folder\n`);
  for (const bad of [['--apps-count', '21'], ['--apps-run', 'web'], ['--apps-run', "web=it's"]]) {
    assert.equal(install('--dry-run', ...bad).status, 2, bad.join(' '));
  }

  r = install('--apps-root', root, '--apps-run', 'web=make serve', '--apps-run', 'api=npm start');
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.includes(`apps:     ${root}\n`), r.stdout);
  const wrapper = readFileSync(path.join(prefix, 'bin', 'inbox'), 'utf8');
  assert.ok(wrapper.includes(`INBOX_APPS_ROOT=\${INBOX_APPS_ROOT:-'${root}'}\nexport INBOX_APPS_ROOT\n`), wrapper);
  assert.ok(wrapper.includes(`INBOX_APPS_RUN=\${INBOX_APPS_RUN:-'web=make serve;api=npm start'}\n`), wrapper);
  // The wrapper's environment reaches the CLI: pin finds the root.
  fakeApp(root, 'web', NOON);
  const pinned = spawnSync(path.join(prefix, 'bin', 'inbox'), ['--plugin', 'apps', 'pin', 'web'], {
    env: { PATH: process.env.PATH, HOME: sc.dir, INBOX_DB: sc.db },
    encoding: 'utf8',
  });
  assert.equal(pinned.stdout, 'pinned web\n', pinned.stderr);
});
