import assert from 'node:assert/strict';
import test from 'node:test';
import { detectHere, focus, parseHere, resolveHere } from '../src/here.js';

const UUID = '0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9';

// A stand-in for execFile that records the call and ends it as `result` says.
function fake(result = {}) {
  const calls = [];
  const execFile = (file, args, options, done) => {
    calls.push({ file, args, options });
    done(result.error ?? null, '', result.stderr ?? '');
  };
  return { calls, execFile };
}

test('TestFocusIterm', async () => {
  const { calls, execFile } = fake();
  await focus(`iterm2:${UUID}`, { execFile, platform: 'darwin' });
  assert.equal(calls.length, 1);
  const [{ file, args, options }] = calls;
  assert.equal(file, 'osascript');
  // The UUID is the last argument, and appears nowhere in the script text.
  assert.equal(args.at(-1), UUID);
  const script = args.filter((a, i) => args[i - 1] === '-e').join('\n');
  assert.ok(!script.includes(UUID));
  assert.ok(script.includes('unique id of s is target') && script.includes('activate'));
  assert.ok(!options.shell, 'no shell');
  assert.ok(options.timeout > 0 && options.timeout <= 5000);
});

test('TestFocusAppAndUrl', async () => {
  let f = fake();
  await focus('app:com.tinyspeck.slackmacgap', { execFile: f.execFile, platform: 'darwin' });
  assert.deepEqual([f.calls[0].file, f.calls[0].args], ['open', ['-b', 'com.tinyspeck.slackmacgap']]);
  assert.ok(!f.calls[0].options.shell);

  f = fake();
  await focus('url:slack://channel?team=T1&id=C1', { execFile: f.execFile, platform: 'darwin' });
  assert.deepEqual([f.calls[0].file, f.calls[0].args], ['open', ['slack://channel?team=T1&id=C1']]);
  assert.ok(!f.calls[0].options.shell);
});

test('TestFocusRefuses', async () => {
  const { calls, execFile } = fake();
  for (const here of ['sh:ls', 'url:file:///etc/passwd', 'iterm2:x', 'app:-a', '', null]) {
    await assert.rejects(focus(here, { execFile, platform: 'darwin' }), /not a valid "here"/);
  }
  await assert.rejects(focus(`iterm2:${UUID}`, { execFile, platform: 'linux' }), /macOS only/);
  assert.equal(calls.length, 0);
});

test('TestFocusErrors', async () => {
  const closed = fake({ error: new Error('exit 1'), stderr: `no iTerm session ${UUID}\n` });
  await assert.rejects(
    focus(`iterm2:${UUID}`, { execFile: closed.execFile, platform: 'darwin' }),
    (e) => e.message === `no iTerm session ${UUID}`,
  );
  const killed = fake({ error: Object.assign(new Error('timed out'), { killed: true, signal: 'SIGTERM' }) });
  await assert.rejects(
    focus(`iterm2:${UUID}`, { execFile: killed.execFile, platform: 'darwin' }),
    /Automation/,
  );
  await assert.rejects(
    focus('app:com.example.app', { execFile: killed.execFile, platform: 'darwin' }),
    (e) => !e.message.includes('Automation'),
  );
});

test('TestParseHere', () => {
  assert.deepEqual(parseHere(`iterm2:${UUID}`), { kind: 'iterm2', value: UUID });
  assert.deepEqual(parseHere('app:com.apple.Safari'), { kind: 'app', value: 'com.apple.Safari' });
  assert.equal(parseHere('url:HTTPS://example.com')?.kind, 'url');
  for (const bad of [undefined, 42, '', ':', 'iterm2:', 'app:nodots', 'url:https://', 'url:ftp://x', 'url:slack:a b', `iterm2:${UUID}\n`]) {
    assert.equal(parseHere(bad), null, String(bad));
  }
});

test('TestDetectHere', () => {
  const env = { TERM_PROGRAM: 'iTerm.app', ITERM_SESSION_ID: `w3t0p1:${UUID}` };
  assert.equal(detectHere(env), `iterm2:${UUID}`);
  assert.equal(detectHere({ ...env, TMUX: '1' }), null);
  assert.equal(detectHere({ ...env, TERM_PROGRAM: 'vscode' }), null);
  assert.equal(detectHere({ ...env, ITERM_SESSION_ID: 'garbage' }), null);
  assert.equal(detectHere({}), null);
  assert.equal(resolveHere({ env: { ...env, INBOX_HERE: '' } }), `iterm2:${UUID}`);
});
