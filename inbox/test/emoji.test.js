import assert from 'node:assert/strict';
import { existsSync, mkdirSync, readFileSync } from 'node:fs';
import test from 'node:test';
import {
  componentOf,
  emojiChar,
  emojiFor,
  loadEmojiSet,
  sessionInput,
  splitSessionChannel,
} from '../src/emoji.js';
import { run, scratch } from '../testlib/helpers.js';

const VECTORS = JSON.parse(readFileSync(new URL('./emoji-vectors.json', import.meta.url), 'utf8'));

test('TestEmojiVectors', () => {
  // A Go copy of the hash (task 0055) must pass this same file.
  assert.equal(VECTORS.length, 20);
  for (const { input, codepoints } of VECTORS) {
    assert.equal(emojiFor(input).codepoints, codepoints, input);
  }
});

test('TestEmojiSetRules', () => {
  // Task 0073's rules: one code point each, none twice, and no skin tone,
  // flag, keycap, ZWJ, face, hand, person, or heart.
  const set = loadEmojiSet();
  assert.ok(set.length >= 128, `${set.length} emoji`);
  assert.equal(new Set(set.map((e) => e.codepoints)).size, set.length, 'an emoji is listed twice');
  const banned = [
    [0x1f3fb, 0x1f3ff], // skin tones
    [0x1f1e6, 0x1f1ff], // regional indicators (flags)
    [0x1f600, 0x1f64f], // faces and gestures
    [0x1f466, 0x1f487], // people
    [0x1f90c, 0x1f91f], // hands
    [0x1f9d0, 0x1f9df], // people
    [0x1f493, 0x1f49f], // hearts
    [0x2764, 0x2764], // heart
  ];
  for (const { codepoints, name } of set) {
    assert.match(codepoints, /^[0-9a-f]{4,5}$/, `${name}: one code point, lower-case hex`);
    const cp = parseInt(codepoints, 16);
    for (const [lo, hi] of banned) {
      assert.ok(cp < lo || cp > hi, `${name} (${codepoints}) is in a banned range`);
    }
    assert.match(name, /^[a-z0-9-]+$/, codepoints);
  }
});

test('TestEmojiInputs', () => {
  assert.equal(sessionInput('3F2A91C0-1111-2222-3333-444455556666'), 'session:3f2a91c0');
  assert.equal(sessionInput('3f2a91c0'), 'session:3f2a91c0');
  assert.throws(() => sessionInput('3f2a91'), /bad session id/);
  assert.throws(() => sessionInput('zz2a91c0'), /bad session id/);
  assert.deepEqual(splitSessionChannel('macro-pad.driver.3f2a91c0'), { folder: 'macro-pad.driver', session: '3f2a91c0' });
  assert.deepEqual(splitSessionChannel('macro-pad.driver'), { folder: 'macro-pad.driver', session: null });
  assert.deepEqual(splitSessionChannel('3f2a91c0'), { folder: '3f2a91c0', session: null });
  assert.equal(emojiChar('2693'), '⚓️');
  // Task 0081: the repository of a folder channel.
  assert.equal(componentOf('github.com.OnePayPal.docreviewonpremnodeweb'), 'docreviewonpremnodeweb');
  assert.equal(componentOf('github.com.kgheacock.Macro-Pad.inbox'), 'macro-pad');
  assert.equal(componentOf('macro-pad.driver'), 'macro-pad');
  assert.equal(componentOf('macro-pad'), 'macro-pad');
  // Three segments are no HOST.TLD.OWNER.REPO.
  assert.equal(componentOf('work.com.notes'), 'work');
  assert.equal(emojiChar('1f98a'), '\u{1f98a}');
});

test('TestFolderIsItsChannel', (t) => {
  // A folder under the source root hashes as the repository of the channel it
  // gives (task 0081), so a folder and its key show the same emoji.
  const sc = scratch(t);
  const dir = `${sc.root}/macro-pad/driver`;
  mkdirSync(dir, { recursive: true });
  const r = run(['emoji', '--folder', dir, '--format', 'ref'], { sc });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout.trim(), emojiFor('repo:macro-pad').iconRef);
  assert.equal(run(['emoji', 'macro-pad.driver', '--format', 'ref'], { sc }).stdout, r.stdout);
  // Outside the root there is no channel, as for `inbox channel`.
  assert.equal(run(['emoji', '--folder', sc.dir], { sc }).status, 2);
});

test('TestEmojiStability', () => {
  // Rendezvous hashing: adding an emoji moves only the inputs that take it,
  // and removing one moves only the inputs that had it.
  const set = loadEmojiSet();
  const grown = [...set, { codepoints: 'ffff0', name: 'new' }];
  const shrunk = set.filter((e) => e.codepoints !== set[0].codepoints);
  let moved = 0;
  const N = 10000;
  for (let i = 0; i < N; i++) {
    const input = `channel:c${i}`;
    const before = emojiFor(input, set).codepoints;
    const after = emojiFor(input, grown).codepoints;
    if (after !== before) {
      moved++;
      assert.equal(after, 'ffff0', `${input} moved to an old emoji`);
    }
    if (before !== set[0].codepoints) assert.equal(emojiFor(input, shrunk).codepoints, before, input);
  }
  assert.ok(moved / N < 0.03, `${moved} of ${N} inputs moved`);
});

test('TestEmojiCommand', (t) => {
  const sc = scratch(t);
  const session = '3F2A91C0-1111-2222-3333-444455556666';
  let r = run(['emoji', '--session', session], { sc });
  assert.equal(r.status, 0, r.stderr);
  assert.equal(r.stdout.trim(), emojiFor('session:3f2a91c0').char);
  // A folder and a session print both, as a session key shows them.
  r = run(['emoji', 'macro-pad', '--session', session, '--format', 'name'], { sc });
  assert.equal(r.stdout.trim(), `${emojiFor('repo:macro-pad').name} ${emojiFor('session:3f2a91c0').name}`);
  r = run(['emoji', '--channel', 'macro-pad', '--format', 'json'], { sc });
  assert.deepEqual(JSON.parse(r.stdout).map((e) => e.input), ['repo:macro-pad']);
  // It needs no store, and makes none.
  assert.equal(run(['emoji', 'x'], { sc }).status, 0);
  assert.ok(!existsSync(sc.db), 'emoji made a store');
  r = run(['emoji', '--session', 'nope'], { sc });
  assert.equal(r.status, 2);
  assert.match(r.stderr, /bad session id/);
  assert.equal(run(['emoji', 'a', '--folder', '.'], { sc }).status, 2);
  assert.equal(run(['emoji', 'a', '--format', 'png'], { sc }).status, 2);
  assert.match(run(['-h'], { sc }).stdout, /emoji +print the emoji/);
  assert.match(run(['emoji', '-h'], { sc }).stdout, /--session ID/);
});
