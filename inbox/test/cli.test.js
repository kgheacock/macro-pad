import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { existsSync, mkdirSync, readdirSync, symlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { DatabaseSync } from 'node:sqlite';
import { SCHEMA_VERSION, openStore } from '../src/store.js';
import { fileURLToPath } from 'node:url';
import { BIN, envFor, run, runAi, scratch, start } from '../testlib/helpers.js';
import { TestServer, waitFor } from '../testlib/wsserver.js';

// Every row of the store, of every plugin, oldest first.
function rows(sc) {
  if (!existsSync(sc.db)) return [];
  const db = new DatabaseSync(sc.db);
  const all = db.prepare('SELECT * FROM messages ORDER BY id').all().map((row) => ({ ...row }));
  db.close();
  return all;
}

function subdir(sc, ...parts) {
  const dir = path.join(sc.root, ...parts);
  mkdirSync(dir, { recursive: true });
  return dir;
}

test('TestChannelCommand', (t) => {
  const sc = scratch(t);
  let r = run(['channel'], { sc, cwd: subdir(sc, 'github.com', 'me', 'app') });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout, 'github.com.me.app\n');
  r = run(['channel'], { sc, cwd: sc.dir });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes(sc.root), r.stderr);
  r = run(['channel'], { sc, cwd: subdir(sc, 'bad name') });
  assert.equal(r.status, 2);
  assert.equal(r.stdout, '');
  assert.equal(run(['channel', 'x'], { sc, cwd: sc.dir }).status, 2);
});

test('TestChannelArgument', (t) => {
  const sc = scratch(t);
  const outside = sc.dir;

  let r = run(['send', 'hi'], { sc, cwd: outside });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes(sc.root), r.stderr);
  r = run(['pipe'], { sc, cwd: outside, input: 'hi\n' });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes(sc.root), r.stderr);
  assert.equal(rows(sc).length, 0);

  assert.equal(run(['send', 'debug', 'hi'], { sc, cwd: outside }).status, 0);
  assert.equal(run(['pipe', 'debug'], { sc, cwd: outside, input: 'hi\n' }).status, 0);
  assert.deepEqual(rows(sc).map((m) => [m.channel, m.body]), [['debug', 'hi'], ['debug', 'hi']]);

  // Under the root, an argument still beats the inferred channel.
  const inside = subdir(sc, 'proj', 'sub');
  assert.equal(run(['send', 'debug', 'again'], { sc, cwd: inside }).status, 0);
  assert.equal(run(['send', 'inferred'], { sc, cwd: inside }).status, 0);
  assert.deepEqual(
    rows(sc).slice(2).map((m) => [m.channel, m.body]),
    [['debug', 'again'], ['proj.sub', 'inferred']],
  );
});

test('TestPipedLines', (t) => {
  const sc = scratch(t);
  const r = run(['pipe', 'x'], { sc, input: 'a\n\n  \nb\r\nc' });
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(rows(sc).map((m) => m.body), ['a', 'b', 'c']);
});

test('TestPipeCutsLongLines', (t) => {
  const sc = scratch(t);
  const r = run(['pipe', 'x'], { sc, input: `${'é'.repeat(100000)}\nshort\n${'z'.repeat(70000)}\n` });
  assert.equal(r.status, 0, r.stderr);
  const bodies = rows(sc).map((m) => m.body);
  assert.equal(bodies.length, 3);
  assert.ok(Buffer.byteLength(bodies[0]) <= 65536);
  assert.ok(!bodies[0].includes('�'));
  assert.equal(bodies[1], 'short');
  assert.equal(bodies[2].length, 65536);
  assert.equal(r.stderr.match(/cut a line/g).length, 1);
});

test('TestSendTooManyArgs', (t) => {
  const sc = scratch(t);
  const r = run(['send', 'a', 'b', 'c'], { sc });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /quote/);
  assert.equal(rows(sc).length, 0);
});

test('TestPipeCommitsWhileOpen', async (t) => {
  const sc = scratch(t);
  const { child, exited } = start(['pipe', 'live'], { sc });
  // Wait until the process has opened the store.
  child.stdin.write('first\n');
  const reader = () => {
    try {
      const store = openStore(sc.db);
      const n = store.count('live');
      store.close();
      return n;
    } catch {
      return 0;
    }
  };
  const waitFor = async (n) => {
    const started = Date.now();
    while (reader() < n) {
      assert.ok(Date.now() - started < 5000, `row ${n} never appeared`);
      await new Promise((r) => setTimeout(r, 20));
    }
  };
  await waitFor(1);
  const wrote = Date.now();
  child.stdin.write('second\n');
  await waitFor(2);
  const latency = Date.now() - wrote;
  t.diagnostic(`line visible after ${latency} ms`);
  assert.ok(latency < 500, `took ${latency} ms`);
  child.stdin.end();
  assert.equal((await exited).status, 0);
});

test('TestPipeSignalSavesAndExitsZero', async (t) => {
  const sc = scratch(t);
  const { child, exited } = start(['pipe', 'sig'], { sc });
  child.stdin.write('kept\npartial');
  await new Promise((r) => setTimeout(r, 600));
  child.kill('SIGTERM');
  assert.equal((await exited).status, 0);
  assert.deepEqual(rows(sc).map((m) => m.body), ['kept']);
});

test('TestPipeForwardsStandardOutput', (t) => {
  const sc = scratch(t);
  const input = 'a\n\n  \nb\r\nc';
  const r = run(['pipe', 'x'], { sc, input });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout, input);
  assert.deepEqual(rows(sc).map((m) => m.body), ['a', 'b', 'c']);
});

test('TestPipeSigintReadsToTheEnd', async (t) => {
  const sc = scratch(t);
  const { child, exited } = start(['pipe', 'int'], { sc });
  let out = '';
  child.stdout.setEncoding('utf8').on('data', (d) => (out += d));
  child.stdin.write('one\n');
  await new Promise((r) => setTimeout(r, 600));
  child.kill('SIGINT');
  await new Promise((r) => setTimeout(r, 300));
  // Still running: the writer may have more to say after its own Ctrl-C.
  assert.equal(child.exitCode, null);
  child.stdin.end('last\npartial');
  assert.equal((await exited).status, 0);
  assert.deepEqual(rows(sc).map((m) => m.body), ['one', 'last', 'partial']);
  assert.equal(out, 'one\nlast\npartial');
});

test('TestPipeSecondSigintExits', async (t) => {
  const sc = scratch(t);
  const { child, exited } = start(['pipe', 'int2'], { sc });
  child.stdin.write('kept\npartial');
  await new Promise((r) => setTimeout(r, 600));
  child.kill('SIGINT');
  await new Promise((r) => setTimeout(r, 200));
  child.kill('SIGINT');
  assert.equal((await exited).status, 0);
  assert.deepEqual(rows(sc).map((m) => m.body), ['kept']);
});

test('TestPipeKeepsSavingWhenStandardOutputCloses', async (t) => {
  const sc = scratch(t);
  const { child, exited } = start(['pipe', 'gone'], { sc });
  child.stdout.destroy();
  child.stdin.write('a\n');
  await new Promise((r) => setTimeout(r, 300));
  child.stdin.end('b\n');
  assert.equal((await exited).status, 0);
  assert.deepEqual(rows(sc).map((m) => m.body), ['a', 'b']);
});

test('TestPipeBurst', (t) => {
  const sc = scratch(t);
  const input = Array.from({ length: 10000 }, (_, i) => `line ${i}`).join('\n');
  const started = Date.now();
  const r = run(['pipe', 'burst'], { sc, input });
  const ms = Date.now() - started;
  t.diagnostic(`10000 lines stored in ${ms} ms`);
  assert.equal(r.status, 0, r.stderr);
  assert.equal(rows(sc).length, 10000);
  assert.ok(ms < 2000, `took ${ms} ms`);
});

test('TestCommands', (t) => {
  const sc = scratch(t);
  const store = openStore(sc.db);
  store.insert('a', 'a one', '/x', { now: Date.UTC(2026, 0, 1, 12) });
  store.insert('a', 'a two', '/x', { now: Date.UTC(2026, 0, 1, 13) });
  store.insert('b', 'b one', '/x', { now: Date.UTC(2026, 0, 1, 14) });
  const b2 = store.insert('proj.sub', 'p one', '/x', { now: Date.UTC(2026, 0, 1, 15) });
  store.markSeen('proj.sub', b2);
  store.insert('proj.sub', 'p two', '/x', { now: Date.UTC(2026, 0, 1, 16) });
  store.close();
  const inside = subdir(sc, 'proj', 'sub');
  const bodies = (out) =>
    out.trimEnd().split('\n').map((l) => l.replace(/^..\d{4}-\d\d-\d\d \d\d:\d\d:\d\d {2}\S+ +(?:\S+ {2})?/, '').trim());

  // count
  assert.equal(run(['count', 'a'], { sc }).stdout, '2\n');
  assert.equal(run(['count', '--all-channels'], { sc }).stdout, '4\n');
  assert.equal(run(['count'], { sc, cwd: inside }).stdout, '1\n');
  assert.equal(run(['count'], { sc, cwd: sc.dir }).stdout, '4\n');

  // list: seen and unseen by default, --unseen, scope, -n, --json
  assert.deepEqual(bodies(run(['list', 'a'], { sc }).stdout), ['a one', 'a two']);
  assert.deepEqual(bodies(run(['list', '--all-channels'], { sc }).stdout), [
    'a one', 'a two', 'b one', 'p one', 'p two',
  ]);
  assert.match(run(['list', '--all-channels'], { sc }).stdout, /info {2}a {2}a one/);
  assert.deepEqual(bodies(run(['list'], { sc, cwd: inside }).stdout), ['p one', 'p two']);
  assert.equal(bodies(run(['list'], { sc, cwd: sc.dir }).stdout).length, 5);
  const lines = run(['list', 'proj.sub'], { sc }).stdout.trimEnd().split('\n');
  assert.equal(lines.length, 2);
  assert.match(lines[0], /^ {2}\d{4}-\S+ \S+ {2}info {2}p one$/);
  assert.match(lines[1], /^\* \d{4}-\S+ \S+ {2}info {2}p two$/);
  assert.deepEqual(bodies(run(['list', '--unseen', 'proj.sub'], { sc }).stdout), ['p two']);
  assert.deepEqual(bodies(run(['list', '--unseen', '--all-channels'], { sc }).stdout), [
    'a one', 'a two', 'b one', 'p two',
  ]);
  assert.deepEqual(bodies(run(['list', '-n', '1', 'a'], { sc }).stdout), ['a two']);
  const json = JSON.parse(run(['list', '--json', '--all-channels'], { sc }).stdout);
  assert.deepEqual(json.map((m) => [m.channel, m.body, m.seen_at !== null]), [
    ['a', 'a one', false], ['a', 'a two', false], ['b', 'b one', false],
    ['proj.sub', 'p one', true], ['proj.sub', 'p two', false],
  ]);
  assert.deepEqual(Object.keys(json[0]), ['id', 'channel', 'body', 'created_at', 'seen_at', 'cwd', 'status', 'here']);
  assert.equal(json[0].created_at, Date.UTC(2026, 0, 1, 12));
  assert.equal(json[0].seen_at, null);
  const unseenJson = JSON.parse(run(['list', '--json', '--unseen', '--all-channels'], { sc }).stdout);
  assert.equal(unseenJson.length, 4);

  // channels
  const channels = run(['channels'], { sc }).stdout.trim().split('\n');
  assert.equal(channels.length, 3);
  assert.match(channels[0], /^a\s+2\s+\d{4}-/);
  assert.match(channels[2], /^proj\.sub\s+1\s+\d{4}-/);
  assert.deepEqual(
    JSON.parse(run(['channels', '--json'], { sc }).stdout).map((c) => [c.channel, c.count, c.newest_at]),
    [['a', 2, Date.UTC(2026, 0, 1, 13)], ['b', 1, Date.UTC(2026, 0, 1, 14)], ['proj.sub', 1, Date.UTC(2026, 0, 1, 16)]],
  );

  // seen
  assert.equal(run(['seen', 'a'], { sc }).stdout, '2\n');
  assert.equal(run(['count', 'a'], { sc }).stdout, '0\n');
  assert.equal(run(['seen'], { sc, cwd: inside }).stdout, '1\n');
  assert.equal(run(['seen', '--all-channels'], { sc }).stdout, '1\n');
  assert.equal(run(['count', '--all-channels'], { sc }).stdout, '0\n');
  assert.equal(run(['channels'], { sc }).stdout, '');
  // seen needs a channel outside the root.
  assert.equal(run(['seen'], { sc, cwd: sc.dir }).status, 2);
});

test('TestHelpFlags', (t) => {
  const sc = scratch(t);
  const lines = [
    ['-h'],
    ['--help'],
    ['--help', 'send'],
    ['help'],
    ['help', 'pipe'],
    ['send', '-h'],
    ['send', 'a', 'b', 'c', '--help'],
    ['pipe', '--help'],
    ['list', '--bogus', '-h'],
    ['count', 'a', 'b', '-h'],
    ['channels', '-h'],
    ['seen', '--all-channels', 'x', '--help'],
    ['sned', '-h'],
  ];
  for (const args of lines) {
    const r = run(args, { sc });
    assert.equal(r.status, 0, `${args.join(' ')}: ${r.stderr}`);
    assert.equal(r.stderr, '', args.join(' '));
    assert.match(r.stdout, /^(Usage: |inbox - )/, args.join(' '));
  }
  const top = run(['-h'], { sc }).stdout;
  for (const word of ['send', 'pipe', 'list', 'count', 'channels', 'seen']) {
    assert.match(top, new RegExp(`^  ${word} `, 'm'));
  }
  assert.ok(top.includes('inbox send debug "build done"'));
  assert.ok(top.includes('my_cmd 2>&1 | inbox pipe debug'));
  assert.equal(run(['help', 'send'], { sc }).stdout, run(['send', '-h'], { sc }).stdout);
  assert.equal(run([], { sc }).status, 2);
});

test('TestPipeIsTheDefaultForPipedInput', (t) => {
  const sc = scratch(t);
  const outside = sc.dir;

  // No command and standard input is a pipe: same as 'inbox pipe'.
  let r = run([], { sc, cwd: subdir(sc, 'proj'), input: 'hello\nworld\n' });
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(rows(sc).map((m) => [m.channel, m.body]), [['proj', 'hello'], ['proj', 'world']]);

  // Options before any command are pipe options. A channel needs the word 'pipe'.
  r = run(['--status', 'warn'], { sc, cwd: subdir(sc, 'proj2'), input: 'w\n' });
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(rows(sc).filter((m) => m.channel === 'proj2').map((m) => m.status), ['warn']);

  // Outside the source root the channel is still required.
  r = run([], { sc, cwd: outside, input: 'hi\n' });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes('no channel'), r.stderr);
  assert.match(r.stderr, /run 'inbox pipe -h' for help/);

  // Help still works, and an unknown word is still an unknown command.
  assert.equal(run(['-h'], { sc, input: 'x\n' }).status, 0);
  assert.equal(run(['lst'], { sc, input: 'x\n' }).status, 2);
});

test('TestNoCommandOnATerminalShowsHelp', async (t) => {
  const sc = scratch(t);
  const { main } = await import('../src/cli.js');
  let err = '';
  const code = await main([], {
    stdin: { isTTY: true },
    stdout: { write() {} },
    stderr: { write: (s) => (err += s) },
    env: { INBOX_DB: sc.db, INBOX_SOURCE_ROOT: sc.root },
    cwd: () => sc.root,
    process,
  });
  assert.equal(code, 2);
  assert.ok(err.startsWith('inbox - leave short messages'), err);
});

test('TestErrorMessages', async (t) => {
  const sc = scratch(t);
  const first = (r) => r.stderr.split('\n')[0];
  const expect = (r, status, line) => {
    assert.equal(r.status, status, r.stderr);
    assert.match(first(r), line);
    assert.match(first(r), /^inbox: /);
  };

  expect(run(['sned', 'x'], { sc }), 2,
    /^inbox: unknown command "sned"; did you mean "send"\? Run 'inbox -h' for the list\.$/);
  expect(run(['zzzzzz'], { sc }), 2, /^inbox: unknown command "zzzzzz"\. Run 'inbox -h' for the list\.$/);

  const outside = run(['send', 'hi'], { sc, cwd: sc.dir });
  expect(outside, 2,
    new RegExp(`^inbox: no channel: ${sc.dir} is not under ${sc.root}\\. Pass it first, as in: inbox send CHANNEL MESSAGE$`));
  assert.match(outside.stderr, /\nrun 'inbox send -h' for help\n$/);

  expect(run(['send', 'my repo', 'hi'], { sc }), 2,
    /^inbox: bad channel "my repo": a channel segment may hold only letters, digits, - and _$/);

  const dotted = subdir(sc, 'my repo');
  expect(run(['send', 'hi'], { sc, cwd: dotted }), 2,
    new RegExp(`^inbox: cannot use directory "my repo" in ${dotted} as a channel segment; pass the channel first$`));

  expect(run(['send', 'a', 'b', 'c'], { sc }), 2,
    /^inbox: send takes CHANNEL and MESSAGE; quote a message with spaces, as in: inbox send debug "build done"$/);
  expect(run(['send'], { sc }), 2,
    /^inbox: send needs a message, as in: inbox send debug "build done"$/);

  // Standard input is a terminal: a pseudo-terminal is the only way to get
  // one, so check the message through the in-process entry point.
  const { main } = await import('../src/cli.js');
  let err = '';
  const code = await main(['pipe', 'debug'], {
    stdin: { isTTY: true },
    stdout: { write() {} },
    stderr: { write: (s) => (err += s) },
    env: { INBOX_DB: sc.db, INBOX_SOURCE_ROOT: sc.root },
    cwd: () => sc.root,
    process,
  });
  assert.equal(code, 2);
  assert.equal(err.split('\n')[0],
    'inbox: pipe reads lines from a pipe or file, as in: my_cmd 2>&1 | inbox pipe debug');

  const blocker = path.join(sc.dir, 'file');
  writeFileSync(blocker, 'x');
  expect(run(['count', '--all-channels', '--db', path.join(blocker, 'sub', 'inbox.db')], { sc }), 1,
    /^inbox: cannot open inbox at .*inbox\.db: .+\. Set INBOX_DB or pass --db to use another file\.$/);

  // A writer holds the file: the second writer waits, then gives up.
  openStore(sc.db).close();
  const holder = new DatabaseSync(sc.db);
  holder.exec('BEGIN IMMEDIATE');
  const busy = run(['send', 'a', 'hi'], { sc, env: { INBOX_BUSY_TIMEOUT_MS: '300' } });
  holder.exec('ROLLBACK');
  holder.close();
  expect(busy, 1,
    new RegExp(`^inbox: inbox at ${sc.db} is busy \\(another writer held it for 0\\.3 s\\); try again$`));

  const raw = new DatabaseSync(sc.db);
  raw.exec('PRAGMA user_version = 9');
  raw.close();
  expect(run(['count', '--all-channels'], { sc }), 1,
    new RegExp(`^inbox: inbox at ${sc.db} has schema 9; this inbox reads up to ${SCHEMA_VERSION}\\. Update the inbox checkout\\.$`));
});

test('TestSymlinkedWorkingDirectory', (t) => {
  const sc = scratch(t);
  const target = subdir(sc, 'real', 'dir');
  symlinkSync(target, path.join(sc.dir, 'link'));
  assert.equal(run(['send', 'hi'], { sc, cwd: path.join(sc.dir, 'link') }).status, 0);
  assert.equal(rows(sc)[0].channel, 'real.dir');
});

test('TestStatus', (t) => {
  const sc = scratch(t);
  assert.equal(run(['send', 'debug', 'plain'], { sc }).status, 0);
  assert.equal(run(['send', '--status', 'error', 'debug', 'bad'], { sc }).status, 0);
  assert.equal(run(['pipe', '--status', 'warn', 'debug'], { sc, input: 'w1\nw2\n' }).status, 0);
  assert.deepEqual(rows(sc).map((m) => [m.body, m.status]), [
    ['plain', 'info'], ['bad', 'error'], ['w1', 'warn'], ['w2', 'warn'],
  ]);
  const lines = run(['list', 'debug'], { sc }).stdout.trimEnd().split('\n');
  assert.match(lines[0], /^\* \S+ \S+ {2}info {3}plain$/);
  assert.match(lines[1], /^\* \S+ \S+ {2}error {2}bad$/);
  const json = JSON.parse(run(['list', '--json', 'debug'], { sc }).stdout);
  assert.deepEqual(json.map((m) => m.status), ['info', 'error', 'warn', 'warn']);

  const bad = run(['send', '--status', 'not ok', 'debug', 'x'], { sc });
  assert.equal(bad.status, 2);
  assert.equal(bad.stderr.split('\n')[0], 'inbox: bad status "not ok": a status may hold only letters, digits, - and _');
  assert.equal(rows(sc).length, 4);
});

test('TestDottedDirectoryChannel', (t) => {
  const sc = scratch(t);
  const dir = subdir(sc, 'github.com', 'me', 'app');
  assert.equal(run(['send', 'hi'], { sc, cwd: dir }).status, 0);
  assert.equal(rows(sc)[0].channel, 'github.com.me.app');
});

// Waits until the child writes `text` to standard error.
function stderrHas(child, text, ms = 5000) {
  return new Promise((resolve, reject) => {
    let seen = '';
    const timer = setTimeout(() => reject(new Error(`no "${text}" in: ${seen}`)), ms);
    child.stderr.on('data', (d) => {
      seen += d;
      if (seen.includes(text)) {
        clearTimeout(timer);
        resolve();
      }
    });
  });
}

test('TestSecondDaemonRefused', async (t) => {
  const sc = scratch(t);
  const first = start(['daemon', '--port', '1'], { sc });
  t.after(() => first.child.kill('SIGKILL'));
  await stderrHas(first.child, 'daemon started');

  const second = run(['daemon', '--port', '1'], { sc });
  assert.equal(second.status, 3);
  assert.equal(
    second.stderr,
    `inbox: a basic daemon already runs for ${sc.db} (pid ${first.child.pid})\n`,
  );

  // After the first one stops, a new daemon can start.
  first.child.kill('SIGTERM');
  assert.equal((await first.exited).status, 0);
  const third = start(['daemon', '--port', '1'], { sc });
  t.after(() => third.child.kill('SIGKILL'));
  await stderrHas(third.child, 'daemon started');
  third.child.kill('SIGTERM');
  assert.equal((await third.exited).status, 0);
});

test('TestDaemonAssetsDirIsUnused', async (t) => {
  const sc = scratch(t);
  const d = start(['daemon', '--port', '1', '--assets-dir', '/x'], { sc });
  t.after(() => d.child.kill('SIGKILL'));
  let seen = '';
  d.child.stderr.on('data', (chunk) => {
    seen += chunk;
  });
  await stderrHas(d.child, 'daemon started');
  assert.equal(seen.split('--assets-dir is unused').length - 1, 1, seen);
  d.child.kill('SIGTERM');
  assert.equal((await d.exited).status, 0);

  // Without the flag there is no such line.
  const quiet = start(['daemon', '--port', '1'], { sc });
  t.after(() => quiet.child.kill('SIGKILL'));
  let quietSeen = '';
  quiet.child.stderr.on('data', (chunk) => {
    quietSeen += chunk;
  });
  await stderrHas(quiet.child, 'daemon started');
  assert.ok(!quietSeen.includes('unused'), quietSeen);
  quiet.child.kill('SIGTERM');
  assert.equal((await quiet.exited).status, 0);
});

test('TestInstallDryRunHasNoAssetsDir', (t) => {
  const sc = scratch(t);
  const script = path.join(import.meta.dirname, '..', 'scripts', 'install.sh');
  for (const extra of [[], ['--assets-dir', '/x']]) {
    const r = spawnSync('sh', [script, '--dry-run', '--no-completions', ...extra], {
      env: { ...process.env, HOME: sc.dir, PREFIX: path.join(sc.dir, 'prefix') },
      encoding: 'utf8',
    });
    assert.equal(r.status, 0, r.stderr);
    assert.ok(r.stdout.includes('dry run: nothing changed'), r.stdout);
    assert.ok(!r.stdout.includes('--assets-dir'), r.stdout);
    assert.ok(!existsSync(path.join(sc.dir, 'prefix')), 'a dry run wrote files');
  }
});

// A HOME with the two per-plugin LaunchAgents of task 0071, and stubs that
// make the scripts think they run on macOS.
function oldMacInstall(sc) {
  const agents = path.join(sc.dir, 'Library', 'LaunchAgents');
  mkdirSync(agents, { recursive: true });
  for (const label of ['com.kgheacock.macropad.inbox', 'com.kgheacock.macropad.inbox.ai']) {
    writeFileSync(path.join(agents, `${label}.plist`), '<plist/>\n');
  }
  const stubs = path.join(sc.dir, 'stubs');
  mkdirSync(stubs);
  writeFileSync(path.join(stubs, 'uname'), '#!/bin/sh\necho Darwin\n', { mode: 0o755 });
  writeFileSync(path.join(stubs, 'launchctl'), '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  return {
    agents,
    env: { ...process.env, HOME: sc.dir, PREFIX: path.join(sc.dir, 'prefix'), PATH: `${stubs}:${process.env.PATH}` },
  };
}

test('TestInstallRemovesPerPluginServices', (t) => {
  const sc = scratch(t);
  const { agents, env } = oldMacInstall(sc);
  const script = path.join(import.meta.dirname, '..', 'scripts', 'install.sh');
  const old = path.join(agents, 'com.kgheacock.macropad.inbox.ai.plist');
  const one = path.join(agents, 'com.kgheacock.macropad.inbox.plist');

  let r = spawnSync('sh', [script, '--dry-run', '--no-completions'], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.includes(`would run: launchctl bootout gui/${process.getuid()}/com.kgheacock.macropad.inbox.ai\n`), r.stdout);
  assert.ok(r.stdout.includes(`would remove: ${old}`), r.stdout);
  // One service, which runs every plugin: no --plugin on its command line.
  assert.ok(r.stdout.includes(`would write: ${one} (runs: inbox daemon --port 8765)\n`), r.stdout);
  assert.ok(existsSync(old), 'a dry run removed a file');

  r = spawnSync('sh', [script, '--dry-run', '--no-completions', '--plugin', 'ai', '--slot', '2'], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.includes('(runs: inbox daemon --port 8765 --plugin ai --slot 2)'), r.stdout);
  r = spawnSync('sh', [script, '--dry-run', '--no-completions', '--slot', 'basic=0', '--slot', 'ai=1'], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.includes('(runs: inbox daemon --port 8765 --slot basic=0 --slot ai=1)'), r.stdout);
  r = spawnSync('sh', [script, '--dry-run', '--slot', 'ai=9'], { env, encoding: 'utf8' });
  assert.equal(r.status, 1);
  assert.equal(r.stderr, 'install.sh: --slot ai=9 is out of range; use 0 to 5\n');
  r = spawnSync('sh', [script, '--dry-run', '--slot', '2'], { env, encoding: 'utf8' });
  assert.equal(r.status, 2);
  assert.equal(r.stderr, 'install.sh: --slot 2 names no plugin, and the daemon shows more than one; use --slot NAME=N\n');
  r = spawnSync('sh', [script, '--dry-run', '--plugin', 'nope'], { env, encoding: 'utf8' });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes('unknown plugin "nope"'), r.stderr);

  // The github plugin's search goes in the wrapper; a quote cannot.
  const query = 'is:pr is:open involves:@me archived:false';
  r = spawnSync('sh', [script, '--dry-run', '--no-completions', '--github-query', query], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stdout.includes(`INBOX_GITHUB_QUERY=${query})`), r.stdout);
  r = spawnSync('sh', [script, '--dry-run', '--github-query', "it's"], { env, encoding: 'utf8' });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes('--github-query may not hold a quote'), r.stderr);
});

test('TestUninstallRemovesEveryService', (t) => {
  const sc = scratch(t);
  const { agents, env } = oldMacInstall(sc);
  const script = path.join(import.meta.dirname, '..', 'scripts', 'uninstall.sh');
  let r = spawnSync('sh', [script, '--dry-run'], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  for (const label of ['com.kgheacock.macropad.inbox', 'com.kgheacock.macropad.inbox.ai']) {
    assert.ok(r.stdout.includes(`would run: launchctl bootout gui/${process.getuid()}/${label}\n`), r.stdout);
    assert.ok(r.stdout.includes(`would run: rm -f ${path.join(agents, `${label}.plist`)}\n`), r.stdout);
  }
  // --plugin is accepted, and ignored.
  r = spawnSync('sh', [script, '--dry-run', '--plugin', 'ai'], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.ok(r.stderr.includes('--plugin is unused'), r.stderr);
  r = spawnSync('sh', [script], { env, encoding: 'utf8' });
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(readdirSync(agents), []);
});

test('TestStalePidFileIsReplaced', async (t) => {
  const sc = scratch(t);
  mkdirSync(path.dirname(sc.db), { recursive: true });
  writeFileSync(`${sc.db}.basic.daemon.pid`, '2147483646\n');
  const d = start(['daemon', '--port', '1'], { sc });
  t.after(() => d.child.kill('SIGKILL'));
  await stderrHas(d.child, 'daemon started');
  d.child.kill('SIGTERM');
  const { status, stderr } = await d.exited;
  assert.equal(status, 0);
  assert.ok(stderr.includes('stale pid file'), stderr);
});

for (const signal of ['SIGTERM', 'SIGINT']) {
  test(`TestDaemonSignal ${signal}`, async (t) => {
    const sc = scratch(t);
    const d = start(['daemon', '--port', '1'], { sc });
    t.after(() => d.child.kill('SIGKILL'));
    await stderrHas(d.child, 'daemon started');
    const sent = Date.now();
    d.child.kill(signal);
    const { status } = await d.exited;
    assert.equal(status, 0);
    assert.ok(Date.now() - sent < 1000, `took ${Date.now() - sent} ms`);
    assert.equal(existsSync(`${sc.db}.basic.daemon.pid`), false);
  });
}

test('TestDaemonHelp', () => {
  const sc = scratch();
  for (const flag of ['-h', '--help']) {
    const r = run(['daemon', flag], { sc });
    assert.equal(r.status, 0);
    assert.ok(r.stdout.startsWith('Usage: inbox daemon '), r.stdout);
    for (const word of ['--port', '--assets-dir', '--slot', '--poll', '--db', '8765', '500ms']) {
      assert.ok(r.stdout.includes(word), `${flag} lacks ${word}`);
    }
  }
});

test('TestDaemonBadFlags', () => {
  const sc = scratch();
  const cases = [
    [['--plugin', 'basic', '--slot', '7'], 'inbox: --slot 7 is out of range; use 0 to 5\n'],
    [['--slot', 'ai=7'], 'inbox: --slot 7 is out of range; use 0 to 5\n'],
    [['--port', 'x'], 'inbox: --port x is out of range; use 1 to 65535\n'],
    [['--poll', '5'], 'inbox: --poll 5 is not a duration; use a number and a unit, such as 500ms or 2s\n'],
  ];
  for (const [flags, message] of cases) {
    const r = run(['daemon', ...flags], { sc });
    assert.equal(r.status, 2, flags.join(' '));
    assert.equal(r.stderr, message);
  }
  const extra = run(['daemon', 'now'], { sc });
  assert.equal(extra.status, 2);
});

// A test server in place of macropadd, closed after the test.
async function serve(t) {
  const server = new TestServer();
  server.announceLayer(null);
  await server.listen();
  t.after(() => server.close());
  return server;
}

// The setLayerKey messages the server got for key id, oldest first.
const keysOf = (server, id) => server.received.filter((m) => m.setLayerKey?.id === id).map((m) => m.setLayerKey);

// Starts a daemon and stops it at the end of the test.
function daemon(t, sc, args) {
  const d = start(['daemon', '--poll', '20ms', ...args], { sc });
  t.after(() => d.child.kill('SIGKILL'));
  return d;
}

test('TestOneDaemonEveryPlugin', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const d = daemon(t, sc, ['--port', String(server.port)]);
  await stderrHas(d.child, 'ai daemon started');
  // One process, one connection per plugin, and a top key for each.
  await waitFor(
    () => keysOf(server, 'inbox').length && keysOf(server, 'ai-inbox').length && keysOf(server, 'github-inbox').length,
    3000,
    'every top key',
  );
  assert.equal(server.connections, 3);

  // A message of either plugin reaches the pad within 1 s.
  let sent = Date.now();
  assert.equal(run(['send', 'one', 'hello'], { sc }).status, 0);
  await waitFor(() => keysOf(server, 'inbox').some((k) => k.count === 1), 1000, 'the basic count');
  assert.ok(Date.now() - sent < 1000);
  sent = Date.now();
  assert.equal(runAi(['send', '--status', 'waiting', 'two', 'needs you'], { sc }).status, 0);
  await waitFor(() => keysOf(server, 'ai-inbox').some((k) => k.status === 'WARN'), 1000, 'the ai status');
  assert.ok(Date.now() - sent < 1000);
  // Each channel goes under its own plugin's key.
  assert.equal(keysOf(server, 'one')[0].parent, 'inbox');
  assert.equal(keysOf(server, 'two')[0].parent, 'ai-inbox');

  d.child.kill('SIGTERM');
  const { status, stderr } = await d.exited;
  assert.equal(status, 0);
  assert.ok(stderr.includes(`inbox: basic daemon started for ${sc.db} (pid ${d.child.pid})\n`), stderr);
  for (const plugin of ['basic', 'ai', 'github']) assert.equal(existsSync(`${sc.db}.${plugin}.daemon.pid`), false, plugin);
  // The github loop finds no gh in a test, and says so once.
  assert.equal(stderr.split('\n').filter((line) => line.startsWith('inbox: [github] cannot read GitHub')).length, 1, stderr);
});

test('TestDaemonOnePlugin', async (t) => {
  const sc = scratch(t);
  const server = await serve(t);
  const d = daemon(t, sc, ['--plugin', 'ai', '--port', String(server.port)]);
  await stderrHas(d.child, 'ai daemon started');
  await waitFor(() => keysOf(server, 'ai-inbox').length, 3000, 'the ai key');
  assert.equal(run(['send', 'one', 'hello'], { sc }).status, 0);
  await new Promise((r) => setTimeout(r, 200));
  assert.deepEqual(keysOf(server, 'inbox'), []);
  assert.equal(server.connections, 1);
  assert.equal(existsSync(`${sc.db}.basic.daemon.pid`), false);
  d.child.kill('SIGTERM');
  const { status, stderr } = await d.exited;
  assert.equal(status, 0);
  // With one plugin, the lines have no [plugin] prefix, as before task 0075.
  assert.ok(!stderr.includes('[ai]'), stderr);
});

test('TestMergedDaemonRefused', async (t) => {
  const sc = scratch(t);
  const ai = daemon(t, sc, ['--plugin', 'ai', '--port', '1']);
  await stderrHas(ai.child, 'daemon started');
  const merged = run(['daemon', '--port', '1'], { sc });
  assert.equal(merged.status, 3);
  assert.equal(merged.stderr, `inbox: a ai daemon already runs for ${sc.db} (pid ${ai.child.pid})\n`);
  // It took the basic pid file first, and gave it back.
  assert.equal(existsSync(`${sc.db}.basic.daemon.pid`), false);
  ai.child.kill('SIGTERM');
  assert.equal((await ai.exited).status, 0);
});

test('TestDaemonSlots', async (t) => {
  const sc = scratch(t);
  const usage = [
    [['--slot', '1'], 'inbox: --slot 1 names no plugin, and the daemon runs basic, ai, github; use --slot NAME=N, such as --slot basic=1\n'],
    [['--slot', 'basic=1', '--slot', 'ai=1'], 'inbox: --slot 1 is given to both basic and ai; give each its own slot\n'],
    [['--slot', 'basic=1', '--slot', 'basic=2'], 'inbox: give --slot once for basic\n'],
    [['--plugin', 'ai', '--slot', 'basic=1'], 'inbox: --slot basic=1: the daemon does not run plugin "basic"; it runs ai\n'],
    [['--slot', 'nope=1'], 'inbox: --slot nope=1: the daemon does not run plugin "nope"; it runs basic, ai, github\n'],
  ];
  for (const [flags, message] of usage) {
    const r = run(['daemon', '--port', '1', ...flags], { sc });
    assert.equal(r.status, 2, flags.join(' '));
    assert.equal(r.stderr, message);
  }

  const server = await serve(t);
  const d = daemon(t, sc, ['--port', String(server.port), '--slot', 'basic=1', '--slot', 'ai=4']);
  await waitFor(() => keysOf(server, 'inbox').length && keysOf(server, 'ai-inbox').length, 3000, 'both top keys');
  assert.equal(keysOf(server, 'inbox')[0].slot, 1);
  assert.equal(keysOf(server, 'ai-inbox')[0].slot, 4);
  d.child.kill('SIGTERM');
  assert.equal((await d.exited).status, 0);

  // With one plugin, a bare --slot N still works.
  const one = daemon(t, sc, ['--plugin', 'ai', '--port', String(server.port), '--slot', '2']);
  await waitFor(() => keysOf(server, 'ai-inbox').some((k) => k.slot === 2), 3000, 'the ai key in slot 2');
  one.child.kill('SIGTERM');
  assert.equal((await one.exited).status, 0);
});

test('TestPluginListOnlyForDaemon', () => {
  const sc = scratch();
  const r = run(['--plugin', 'basic,ai', 'list'], { sc });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes('give --plugin once; got basic and ai'), r.stderr);
  const bad = run(['daemon', '--plugin', 'basic,nope'], { sc });
  assert.equal(bad.status, 2);
  assert.equal(bad.stderr, 'inbox: unknown plugin "nope"; use one of: basic, ai, github\n');
});

const SESSION = '0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9';
const ITERM = { TERM_PROGRAM: 'iTerm.app', ITERM_SESSION_ID: `w0t1p2:${SESSION}` };
const hereOf = (sc) => rows(sc).map((m) => m.here);

test('TestHerePrecedence', (t) => {
  const sc = scratch(t);
  const flag = 'app:com.tinyspeck.slackmacgap';
  const fromEnv = 'url:https://example.com/a';

  // --here beats $INBOX_HERE, which beats iTerm detection.
  assert.equal(run(['send', '--here', flag, 'x', 'one'], { sc, env: { ...ITERM, INBOX_HERE: fromEnv } }).status, 0);
  assert.equal(run(['send', 'x', 'two'], { sc, env: { ...ITERM, INBOX_HERE: fromEnv } }).status, 0);
  assert.equal(run(['send', 'x', 'three'], { sc, env: ITERM }).status, 0);
  // With $TMUX set, or another terminal, detection gives nothing.
  assert.equal(run(['send', 'x', 'four'], { sc, env: { ...ITERM, TMUX: '/tmp/tmux-1/default,1,0' } }).status, 0);
  assert.equal(run(['send', 'x', 'five'], { sc, env: { ...ITERM, TERM_PROGRAM: 'Apple_Terminal' } }).status, 0);
  assert.equal(run(['send', 'x', 'six'], { sc }).status, 0);
  assert.deepEqual(hereOf(sc), [flag, fromEnv, `iterm2:${SESSION}`, null, null, null]);

  // `inbox here` prints what detection finds, or exits 1.
  let r = run(['here'], { sc, env: ITERM });
  assert.deepEqual([r.status, r.stdout], [0, `iterm2:${SESSION}\n`]);
  r = run(['here'], { sc, env: { ...ITERM, TMUX: 'x' } });
  assert.equal(r.status, 1);
  assert.ok(r.stderr.includes('--here'), r.stderr);
});

test('TestPipeStoresHere', (t) => {
  const sc = scratch(t);
  const r = run(['pipe', 'x'], { sc, env: ITERM, input: 'a\nb\nc\n' });
  assert.equal(r.status, 0, r.stderr);
  assert.deepEqual(hereOf(sc), Array(3).fill(`iterm2:${SESSION}`));
  const json = JSON.parse(run(['list', 'x', '--json'], { sc }).stdout);
  assert.deepEqual(json.map((m) => m.here), Array(3).fill(`iterm2:${SESSION}`));

  // A bad value stops the run before any line is stored.
  const bad = run(['pipe', 'x', '--here', 'sh:ls'], { sc, input: 'd\n' });
  assert.equal(bad.status, 2);
  assert.equal(rows(sc).length, 3);
});

test('TestHereRejects', (t) => {
  const sc = scratch(t);
  const bad = [
    'iterm2:xyz',
    'url:file:///etc/passwd',
    'url:javascript:alert(1)',
    'sh:ls',
    'app:-a',
    'app:com.example.app; rm -rf /',
    'url:https://example.com/ a',
    'url:',
    'nonsense',
  ];
  for (const value of bad) {
    for (const args of [['send', 'x', 'm'], ['pipe', 'x']]) {
      const r = run([args[0], '--here', value, ...args.slice(1)], { sc, input: 'm\n' });
      assert.equal(r.status, 2, `${value}: ${r.stderr}`);
      assert.match(r.stderr.split('\n')[0], /^inbox: bad --here /);
    }
    assert.equal(run(['here', 'x', value], { sc }).status, 2, value);
  }
  assert.equal(run(['send', 'x', 'm'], { sc, env: { INBOX_HERE: 'sh:ls' } }).status, 2);
  assert.equal(rows(sc).length, 0);
  // The good kinds pass.
  for (const value of [
    `iterm2:${SESSION}`,
    'app:com.apple.Safari',
    'url:slack://channel?team=T1&id=C1',
    'url:msteams://teams.microsoft.com/l/chat',
    'url:ms-outlook://emails',
    'url:https://example.com/',
  ]) {
    assert.equal(run(['send', '--here', value, 'x', 'm'], { sc }).status, 0, value);
  }
});

test('TestChannelFallback', (t) => {
  const sc = scratch(t);
  const fallback = 'app:com.tinyspeck.slackmacgap';
  const mine = `iterm2:${SESSION}`;
  assert.equal(run(['here', 'chat', fallback], { sc }).status, 0);
  let r = run(['here', 'chat'], { sc });
  assert.deepEqual([r.status, r.stdout], [0, `${fallback}\n`]);

  assert.equal(run(['send', 'chat', 'no here'], { sc }).status, 0);
  const unseenHere = () => {
    const store = openStore(sc.db);
    const found = store.unseen().map((u) => u.here);
    store.close();
    return found;
  };
  assert.deepEqual(unseenHere(), [fallback]);
  // A message here wins over the fallback.
  assert.equal(run(['send', '--here', mine, 'chat', 'has here'], { sc }).status, 0);
  assert.deepEqual(unseenHere(), [mine]);

  assert.equal(run(['here', '--clear', 'chat'], { sc }).status, 0);
  r = run(['here', 'chat'], { sc });
  assert.equal(r.status, 1);
  assert.deepEqual(unseenHere(), [mine]);
  // Usage errors.
  assert.equal(run(['here', '--clear'], { sc }).status, 2);
  assert.equal(run(['here', 'bad channel!', fallback], { sc }).status, 2);
});

test('TestChannelOption', (t) => {
  const sc = scratch(t);
  const outside = sc.dir;
  const channels = () => rows(sc).map((m) => `${m.channel}:${m.body}`);

  assert.equal(run(['send', '--channel', 'a', 'one'], { sc, cwd: outside }).status, 0);
  assert.equal(run(['send', 'one', '--channel=b'], { sc, cwd: outside }).status, 0);
  assert.equal(run(['pipe', '--channel', 'c'], { sc, cwd: outside, input: 'two\n' }).status, 0);
  // Piped input with no command takes it too.
  assert.equal(run(['--channel', 'd'], { sc, cwd: outside, input: 'three\n' }).status, 0);
  assert.deepEqual(channels(), ['a:one', 'b:one', 'c:two', 'd:three']);

  assert.equal(run(['count', '--channel', 'a'], { sc, cwd: outside }).stdout, '1\n');
  assert.ok(run(['list', '--channel', 'c'], { sc, cwd: outside }).stdout.includes('two'));
  assert.equal(run(['seen', '--channel', 'a'], { sc, cwd: outside }).stdout, '1\n');
  assert.equal(run(['count', '--channel', 'a'], { sc, cwd: outside }).stdout, '0\n');

  // The channel twice, or with --all-channels, is a usage error.
  for (const args of [
    ['send', '--channel', 'a', 'b', 'msg'],
    ['pipe', '--channel', 'a', 'b'],
    ['count', '--channel', 'a', 'b'],
    ['count', '--channel', 'a', '--all-channels'],
  ]) {
    const r = run(args, { sc, cwd: outside, input: 'x\n' });
    assert.equal(r.status, 2, args.join(' '));
  }
  assert.equal(run(['send', '--channel', 'a'], { sc, cwd: outside }).status, 2);
  assert.equal(run(['send', '--channel', 'bad channel!', 'm'], { sc, cwd: outside }).status, 2);
  assert.equal(run(['channels', '--channel', 'a'], { sc }).status, 2);
});

test('TestListSurvivesAClosedStdout', async (t) => {
  const sc = scratch(t);
  const input = Array.from({ length: 20000 }, (_, i) => `line ${i}`).join('\n');
  assert.equal(run(['pipe', 'big'], { sc, input }).status, 0);
  const { child, exited } = start(['list', 'big'], { sc });
  child.stdout.destroy();
  const { status, stderr } = await exited;
  assert.equal(stderr, '');
  assert.equal(status, 0);
});

test('TestPluginsShareOneStore', (t) => {
  const sc = scratch(t);
  assert.equal(runAi(['send', 'agent', 'needs input', '--status', 'waiting'], { sc }).status, 0);
  assert.equal(run(['send', 'mine', 'hello'], { sc }).status, 0);

  // One file holds both, each row with its plugin.
  assert.deepEqual(rows(sc).map((m) => [m.plugin, m.channel, m.status]), [
    ['ai', 'agent', 'waiting'],
    ['basic', 'mine', 'info'],
  ]);
  // Each plugin reads only its own.
  assert.equal(run(['list', '--all-channels'], { sc }).stdout.includes('needs input'), false);
  assert.equal(runAi(['list', '--all-channels'], { sc }).stdout.includes('hello'), false);
  assert.equal(runAi(['count', '--all-channels'], { sc }).stdout, '1\n');
  assert.equal(runAi(['channels'], { sc }).stdout.startsWith('agent '), true);

  // Seen in ai does not touch basic.
  assert.equal(runAi(['seen', 'agent'], { sc }).stdout, '1\n');
  assert.equal(run(['count', '--all-channels'], { sc }).stdout, '1\n');
});

test('TestPluginFlag', (t) => {
  const sc = scratch(t);
  // Before the command, after it, and with =.
  assert.equal(run(['--plugin', 'ai', 'send', 'a', 'one'], { sc }).status, 0);
  assert.equal(run(['send', '--plugin', 'ai', 'a', 'two'], { sc }).status, 0);
  assert.equal(run(['send', 'a', 'three', '--plugin=ai'], { sc }).status, 0);
  assert.equal(run(['--plugin', 'basic', 'send', 'a', 'four'], { sc }).status, 0);
  assert.equal(runAi(['count', 'a'], { sc }).stdout, '3\n');
  assert.equal(run(['count', 'a'], { sc }).stdout, '1\n');
  // A pipe with no command word.
  assert.equal(run(['--plugin', 'ai', '--channel', 'a'], { sc, input: 'five\n' }).status, 0);
  assert.equal(runAi(['list', '--all-channels', '--json'], { sc }).stdout.includes('five'), true);
  // After --, the words are the message.
  assert.equal(run(['send', '--', 'b', '--plugin'], { sc }).status, 0);
  assert.equal(run(['list', 'b'], { sc }).stdout.includes('--plugin'), true);

  let r = run(['--plugin', 'nope', 'list'], { sc });
  assert.equal(r.status, 2);
  assert.equal(r.stderr, 'inbox: unknown plugin "nope"; use one of: basic, ai, github\n');
  r = run(['list', '--plugin'], { sc });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes('--plugin needs a NAME'), r.stderr);
  r = run(['--plugin', 'ai', 'list', '--plugin', 'basic'], { sc });
  assert.equal(r.status, 2);
  assert.ok(r.stderr.includes('give --plugin once'), r.stderr);

  r = run(['plugins'], { sc });
  assert.equal(r.status, 0);
  const lines = r.stdout.trimEnd().split('\n');
  assert.equal(lines.length, 3);
  assert.ok(lines[0].startsWith('basic ') && lines[0].endsWith('(default)'), lines[0]);
  assert.ok(lines[1].startsWith('ai ') && !lines[1].endsWith('(default)'), lines[1]);
  assert.ok(lines[2].startsWith('github ') && !lines[2].endsWith('(default)'), lines[2]);
});

test('TestAiStatus', (t) => {
  const sc = scratch(t);
  let r = runAi(['send', '--status', 'error', 'x', 'y'], { sc });
  assert.equal(r.status, 2);
  for (const word of ['waiting', 'running', 'done']) assert.ok(r.stderr.includes(word), r.stderr);
  assert.ok(r.stderr.startsWith('inbox: '), r.stderr);
  r = runAi(['pipe', '--status', 'info', 'x'], { sc, input: 'a\n' });
  assert.equal(r.status, 2);
  assert.equal(existsSync(sc.db), false, 'a rejected send must not create the store');

  // No status: done. The three names are accepted.
  assert.equal(runAi(['send', 'x', 'plain'], { sc }).status, 0);
  for (const status of ['waiting', 'running', 'done']) {
    assert.equal(runAi(['send', '--status', status, 'x', status], { sc }).status, 0);
  }
  // basic still takes any status, and defaults to info.
  assert.equal(run(['send', '--status', 'error', 'x', 'y'], { sc }).status, 0);
  assert.equal(run(['send', 'x', 'z'], { sc }).status, 0);
  assert.deepEqual(rows(sc).map((m) => [m.plugin, m.status]), [
    ['ai', 'done'], ['ai', 'waiting'], ['ai', 'running'], ['ai', 'done'], ['basic', 'error'], ['basic', 'info'],
  ]);
});

test('TestGithubStatus', (t) => {
  const sc = scratch(t);
  let r = run(['--plugin', 'github', 'send', '--status', 'red', 'x', 'y'], { sc });
  assert.equal(r.status, 2);
  for (const word of ['failing', 'pending', 'passing', 'none', 'closed']) assert.ok(r.stderr.includes(word), r.stderr);
  assert.equal(existsSync(sc.db), false, 'a rejected send must not create the store');
  assert.equal(run(['--plugin', 'github', 'send', 'x', 'y'], { sc }).status, 0);
  assert.deepEqual(rows(sc).map((m) => [m.plugin, m.status]), [['github', 'none']]);
  r = run(['--plugin', 'github', 'send', '-h'], { sc });
  assert.ok(r.stdout.includes('counts the failing PRs'), r.stdout);
});

test('TestPluginHelp', (t) => {
  const sc = scratch(t);
  const top = runAi(['-h'], { sc });
  assert.equal(top.status, 0);
  assert.ok(top.stdout.startsWith('inbox - '), top.stdout);
  assert.ok(top.stdout.includes('waiting, running, done'));
  const send = runAi(['send', '-h'], { sc }).stdout;
  assert.ok(send.includes('Usage: inbox send'), send);
  assert.ok(send.includes('waiting | running | done (default: done)'), send);
  assert.ok(send.includes('counts the channels that wait'), send);

  const basicTop = run(['-h'], { sc }).stdout;
  for (const word of ['--plugin NAME', 'basic', 'ai ', 'plugins']) assert.ok(basicTop.includes(word), word);
  assert.ok(!basicTop.includes('counts the channels that wait'));
  assert.ok(run(['send', '-h'], { sc }).stdout.includes('letters, digits, - and _ (default: info)'));
  assert.ok(runAi(['bogus'], { sc }).stderr.includes("Run 'inbox -h'"));
  for (const r of [top, runAi(['send', '-h'], { sc }), runAi(['bogus'], { sc }), run(['plugins', '-h'], { sc })]) {
    assert.ok(!`${r.stdout}${r.stderr}`.includes('ai-inbox'), r.stdout + r.stderr);
  }
});

test('TestSendBranch', (t) => {
  // Task 0081: --branch links the branch of a session's repository to it.
  const sc = scratch(t);
  let r = runAi(['send', '--branch', 'fix-x', 'github.com.OnePayPal.docreviewonpremnodeweb.08709453', 'working'], { sc });
  assert.equal(r.status, 0, r.stderr);
  const store = openStore(sc.db);
  t.after(() => store.close());
  assert.equal(store.branchSession('docreviewonpremnodeweb', 'fix-x'), '08709453');
  // A channel without a session has nothing to link.
  r = runAi(['send', '--branch', 'fix-x', 'macro-pad', 'working'], { sc });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /--branch needs a session channel/);
  assert.match(run(['send', '-h'], { sc }).stdout, /--branch B/);
});

test('TestHookSendsBranch', (t) => {
  // The Claude Code hook names the branch of the session's folder.
  const sc = scratch(t);
  const repo = path.join(sc.root, 'macro-pad');
  mkdirSync(repo);
  const git = (...args) => spawnSync('git', ['-C', repo, ...args], { encoding: 'utf8' });
  if (git('init', '-q', '-b', 'claude/fix-x').status !== 0) return t.skip('no git');
  const hook = fileURLToPath(new URL('../claude-code/claude-ai-inbox.sh', import.meta.url));
  const r = spawnSync('bash', [hook, 'prompt'], {
    input: JSON.stringify({ session_id: '3f2a91c0-1111-2222-3333-444455556666', cwd: repo }),
    env: envFor(sc, { INBOX_BIN: BIN, CLAUDE_AI_INBOX_CACHE: path.join(sc.dir, 'cache') }),
    encoding: 'utf8',
  });
  if (r.error) return t.skip('no bash');
  assert.equal(r.status, 0, r.stderr);
  const store = openStore(sc.db, undefined, { plugin: 'ai' });
  t.after(() => store.close());
  assert.deepEqual(store.list().map((m) => [m.channel, m.status]), [['macro-pad.3f2a91c0', 'running']]);
  assert.equal(store.branchSession('macro-pad', 'claude/fix-x'), '3f2a91c0');
});
