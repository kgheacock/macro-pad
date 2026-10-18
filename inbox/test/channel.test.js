import assert from 'node:assert/strict';
import { mkdirSync, symlinkSync } from 'node:fs';
import path from 'node:path';
import test from 'node:test';
import { inferChannel, validateChannel } from '../src/channel.js';
import { UsageError } from '../src/errors.js';
import { scratch } from '../testlib/helpers.js';

test('TestInferNested', (t) => {
  const { root } = scratch(t);
  mkdirSync(path.join(root, 'a', 'b', 'c'), { recursive: true });
  assert.equal(inferChannel(path.join(root, 'a'), root), 'a');
  assert.equal(inferChannel(path.join(root, 'a', 'b', 'c'), root), 'a.b.c');
});

test('TestInferRootAndOutside', (t) => {
  const { dir, root } = scratch(t);
  assert.equal(inferChannel(root, root), null);
  assert.equal(inferChannel(dir, root), null);
  mkdirSync(path.join(dir, 'elsewhere'));
  assert.equal(inferChannel(path.join(dir, 'elsewhere'), root), null);
  // A sibling that shares the root's name as a prefix is not below it.
  mkdirSync(path.join(dir, 'src-other'));
  assert.equal(inferChannel(path.join(dir, 'src-other'), root), null);
});

test('TestInferSymlink', (t) => {
  const { dir, root } = scratch(t);
  mkdirSync(path.join(root, 'a', 'b'), { recursive: true });
  symlinkSync(path.join(root, 'a', 'b'), path.join(dir, 'link'));
  assert.equal(inferChannel(path.join(dir, 'link'), root), 'a.b');
  // A link to the root also resolves, so the root is a link-free comparison.
  symlinkSync(root, path.join(dir, 'rootlink'));
  assert.equal(inferChannel(path.join(root, 'a'), path.join(dir, 'rootlink')), 'a');
});

test('TestInferDottedDirectory', (t) => {
  const { root } = scratch(t);
  mkdirSync(path.join(root, 'github.com', 'me', 'my.repo'), { recursive: true });
  // The dot inside a directory name is kept as an ordinary separator.
  assert.equal(inferChannel(path.join(root, 'github.com'), root), 'github.com');
  assert.equal(inferChannel(path.join(root, 'github.com', 'me', 'my.repo'), root), 'github.com.me.my.repo');
});

test('TestInferBadSegment', (t) => {
  const { root } = scratch(t);
  mkdirSync(path.join(root, 'a b'));
  assert.throws(
    () => inferChannel(path.join(root, 'a b'), root),
    (e) => e instanceof UsageError && e.message.includes('"a b"') && e.message.includes('pass the channel first'),
  );
  for (const name of ['.git', 'a..b', 'trail.']) {
    mkdirSync(path.join(root, name));
    assert.throws(() => inferChannel(path.join(root, name), root), UsageError, name);
  }
});

test('TestValidateChannel', () => {
  for (const ok of ['debug', 'macro-pad.driver', 'a_b.C-9']) {
    assert.equal(validateChannel(ok), ok);
  }
  for (const bad of ['', 'my repo', 'a..b', '.a', 'a.', 'a/b', 'é']) {
    assert.throws(() => validateChannel(bad), UsageError, `channel ${JSON.stringify(bad)}`);
  }
  assert.throws(
    () => validateChannel('my repo'),
    { message: 'bad channel "my repo": a channel segment may hold only letters, digits, - and _' },
  );
});
