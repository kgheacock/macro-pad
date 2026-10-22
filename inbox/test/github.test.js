import assert from 'node:assert/strict';
import test from 'node:test';
import { channelInput, emojiFor } from '../src/emoji.js';
import github, { ACTIVE_MS, DEFAULT_QUERY, fetchPulls, parseAnswer, syncPulls } from '../src/plugins/github.js';
import { createRenderer } from '../src/render.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';

// One PR as GitHub's search answers it. state null: the commit has no checks.
const pr = (repo, number, state, title = `PR ${number}`) => ({
  number,
  title,
  url: `https://github.com/kgheacock/${repo}/pull/${number}`,
  repository: { name: repo, owner: { login: 'kgheacock' } },
  commits: { nodes: [{ commit: { statusCheckRollup: state === null ? null : { state } } }] },
});
const answer = (nodes, issueCount = nodes.length) => JSON.stringify({ data: { search: { issueCount, nodes } } });

function storeFor(t) {
  const { db } = scratch(t);
  const store = openStore(db, undefined, { plugin: 'github' });
  t.after(() => store.close());
  return store;
}

const keys = (messages) =>
  Object.fromEntries(messages.filter((m) => m.setLayerKey).map((m) => [m.setLayerKey.id, m.setLayerKey]));
const order = (messages) => messages.filter((m) => m.setLayerKey?.parent).map((m) => m.setLayerKey.id);
const rowsOf = (store, now) => store.recent(now - ACTIVE_MS);

test('TestGithubFeedWrites', (t) => {
  const store = storeFor(t);
  const now = 1_000_000;
  const four = parseAnswer(
    answer([
      pr('macro-pad', 97, 'FAILURE', 'One inbox daemon'),
      pr('macro-pad', 98, 'PENDING'),
      pr('dotfiles', 3, 'SUCCESS'),
      pr('site.github.io', 1, null),
    ]),
  );
  assert.equal(syncPulls(store, four, { now }), 4);
  assert.deepEqual(
    store.list().map((m) => [m.channel, m.status, m.body, m.here]),
    [
      ['kgheacock.macro-pad.97', 'failing', 'One inbox daemon', 'url:https://github.com/kgheacock/macro-pad/pull/97'],
      ['kgheacock.macro-pad.98', 'pending', 'PR 98', 'url:https://github.com/kgheacock/macro-pad/pull/98'],
      ['kgheacock.dotfiles.3', 'passing', 'PR 3', 'url:https://github.com/kgheacock/dotfiles/pull/3'],
      // A dot in a name becomes -.
      ['kgheacock.site-github-io.1', 'none', 'PR 1', 'url:https://github.com/kgheacock/site.github.io/pull/1'],
    ],
  );
  // The same answer again writes nothing; a change writes one.
  assert.equal(syncPulls(store, four, { now: now + 1 }), 0);
  const fixed = parseAnswer(
    answer([pr('macro-pad', 97, 'SUCCESS'), pr('macro-pad', 98, 'PENDING'), pr('dotfiles', 3, 'SUCCESS'), pr('site.github.io', 1, null)]),
  );
  assert.equal(syncPulls(store, fixed, { now: now + 2 }), 1);
  assert.equal(store.list().at(-1).status, 'passing');
});

test('TestGithubFeedCloses', (t) => {
  const store = storeFor(t);
  const now = 1_000_000;
  syncPulls(store, parseAnswer(answer([pr('macro-pad', 97, 'FAILURE'), pr('macro-pad', 98, 'SUCCESS')])), { now });

  // A cut answer closes nothing.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS')], 51)), { now: now + 1 }), 0);
  // 97 is gone from a whole answer: a seen closed message, and its key goes.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS')])), { now: now + 2 }), 1);
  const closed = store.list({ includeSeen: true }).at(-1);
  assert.deepEqual([closed.channel, closed.status, closed.seen_at !== null], ['kgheacock.macro-pad.97', 'closed', true]);
  const messages = createRenderer({ rules: github.rules }).plan({ unseen: rowsOf(store, now + 2), active: null });
  assert.deepEqual(order(messages), ['kgheacock.macro-pad.98']);
  // It stays closed: the next answer writes nothing.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS')])), { now: now + 3 }), 0);
  // Reopened, it comes back.
  assert.equal(syncPulls(store, parseAnswer(answer([pr('macro-pad', 97, 'PENDING'), pr('macro-pad', 98, 'SUCCESS')])), { now: now + 4 }), 1);
});

test('TestGithubKeys', (t) => {
  const store = storeFor(t);
  const now = 1_000_000;
  syncPulls(
    store,
    parseAnswer(answer([pr('macro-pad', 98, 'SUCCESS'), pr('macro-pad', 97, 'FAILURE'), pr('a-very-long-repository-name', 1234, 'PENDING')])),
    { now },
  );
  const messages = createRenderer({ rules: github.rules }).plan({ unseen: rowsOf(store, now), active: null });
  const k = keys(messages);
  assert.deepEqual(k['github-inbox'], { id: 'github-inbox', status: 'ERROR', count: 1, iconRef: 'github.png' });
  // Failing first, then pending, then passing.
  assert.deepEqual(order(messages), ['kgheacock.macro-pad.97', 'kgheacock.a-very-long-repository-name.1234', 'kgheacock.macro-pad.98']);
  const red = k['kgheacock.macro-pad.97'];
  assert.deepEqual([red.status, red.count, red.parent], ['ERROR', 1, 'github-inbox']);
  // REPO #NUMBER, over the repository's emoji: both macro-pad PRs share it.
  assert.equal(red.iconText, 'macro-pad #97');
  assert.deepEqual(red.iconEmoji, [emojiFor(channelInput('kgheacock.macro-pad')).iconRef]);
  assert.deepEqual(k['kgheacock.macro-pad.98'].iconEmoji, red.iconEmoji);
  assert.equal(k['kgheacock.macro-pad.98'].status, 'DONE');
  // A long name is cut, and the number stays.
  const long = k['kgheacock.a-very-long-repository-name.1234'];
  assert.equal(long.iconText, 'a-very-lo… #1234');
  assert.equal(long.status, 'RUNNING');

  // Only pending ones: blue, no pip. Seen: the pip goes, the colour stays.
  const pending = storeFor(t);
  syncPulls(pending, parseAnswer(answer([pr('macro-pad', 98, 'PENDING')])), { now });
  pending.markSeen('kgheacock.macro-pad.98', Number.MAX_SAFE_INTEGER, now);
  const p = keys(createRenderer({ rules: github.rules }).plan({ unseen: rowsOf(pending, now), active: null }));
  assert.deepEqual([p['github-inbox'].status, p['github-inbox'].count], ['RUNNING', 0]);
  assert.deepEqual([p['kgheacock.macro-pad.98'].status, p['kgheacock.macro-pad.98'].count], ['RUNNING', 0]);
});

// An execFile stub that answers each call in turn.
function stubGh(results) {
  const calls = [];
  const execFile = (file, args, options, callback) => {
    calls.push({ file, args, options });
    const next = results[Math.min(calls.length - 1, results.length - 1)];
    setImmediate(() => callback(next.error ?? null, next.stdout ?? '', next.stderr ?? ''));
  };
  return { calls, execFile };
}

test('TestGithubFetch', async () => {
  const { calls, execFile } = stubGh([{ stdout: answer([pr('macro-pad', 97, 'ERROR')]) }]);
  const got = await fetchPulls({ env: { INBOX_GH: '/opt/homebrew/bin/gh' }, execFile });
  assert.deepEqual(got.pulls.map((p) => [p.channel, p.status]), [['kgheacock.macro-pad.97', 'failing']]);
  assert.equal(calls[0].file, '/opt/homebrew/bin/gh');
  assert.deepEqual(calls[0].args.slice(0, 2), ['api', 'graphql']);
  assert.ok(calls[0].args.includes(`q=${DEFAULT_QUERY}`), calls[0].args.join(' '));
  assert.equal(calls[0].options.shell, false);
  // The query can be set.
  await fetchPulls({ env: { INBOX_GITHUB_QUERY: 'is:pr is:open review-requested:@me' }, execFile });
  assert.equal(calls[1].file, 'gh');
  assert.ok(calls[1].args.includes('q=is:pr is:open review-requested:@me'));
});

test('TestGithubFeedFails', async () => {
  const missing = Object.assign(new Error('spawn gh ENOENT'), { code: 'ENOENT' });
  const { execFile } = stubGh([{ error: missing }]);
  await assert.rejects(fetchPulls({ env: {}, execFile }), /no gh; install the GitHub CLI or set INBOX_GH/);
  const auth = stubGh([{ error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'gh auth login\nmore' }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: auth.execFile }), /gh failed: gh auth login$/);
  const junk = stubGh([{ stdout: 'not json' }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: junk.execFile }), /not JSON/);
  const said = stubGh([{ stdout: JSON.stringify({ errors: [{ message: 'Bad credentials' }] }) }]);
  await assert.rejects(fetchPulls({ env: {}, execFile: said.execFile }), /GitHub said: Bad credentials/);
});
