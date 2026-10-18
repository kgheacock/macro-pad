import assert from 'node:assert/strict';
import { existsSync, mkdirSync, symlinkSync, writeFileSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { DatabaseSync } from 'node:sqlite';
import { openStore } from '../src/store.js';
import { run, scratch, start } from '../testlib/helpers.js';

function rows(sc) {
  const store = openStore(sc.db);
  const all = store.list({ includeSeen: true });
  store.close();
  return all;
}

function subdir(sc, ...parts) {
  const dir = path.join(sc.root, ...parts);
  mkdirSync(dir, { recursive: true });
  return dir;
}

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
  assert.deepEqual(Object.keys(json[0]), ['id', 'channel', 'body', 'created_at', 'seen_at', 'cwd', 'status']);
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
    new RegExp(`^inbox: inbox at ${sc.db} has schema 9; this inbox reads up to 2\\. Update the inbox checkout\\.$`));
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
    `inbox: a daemon already runs for ${sc.db} (pid ${first.child.pid})\n`,
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

test('TestStalePidFileIsReplaced', async (t) => {
  const sc = scratch(t);
  mkdirSync(path.dirname(sc.db), { recursive: true });
  writeFileSync(`${sc.db}.daemon.pid`, '2147483646\n');
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
    assert.equal(existsSync(`${sc.db}.daemon.pid`), false);
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
    [['--slot', '7'], 'inbox: --slot 7 is out of range; use 0 to 5\n'],
    [['--port', 'x'], 'inbox: --port x is out of range; use 1 to 65535\n'],
    [['--poll', '5'], 'inbox: --poll 5 is not a duration; use a number and a unit, such as 500ms or 2s\n'],
    [
      ['--assets-dir', 'assets'],
      'inbox: assets dir "assets" is relative; pass an absolute path, as macropadd sees it\n',
    ],
  ];
  for (const [flags, message] of cases) {
    const r = run(['daemon', ...flags], { sc });
    assert.equal(r.status, 2, flags.join(' '));
    assert.equal(r.stderr, message);
  }
  const extra = run(['daemon', 'now'], { sc });
  assert.equal(extra.status, 2);
});
