import assert from 'node:assert/strict';
import test from 'node:test';
import { DEFAULT_OWNER, REPO_LIMIT, feed, fetchRepos, parseRepos, repoKey, reposOwner } from '../src/repolist.js';

// A row as `gh repo list --json name,url,isFork,isArchived,pushedAt,issues,pullRequests`
// gives it (gh 2.92).
const row = (name, issues = 0, prs = 0, extra = {}) => ({
  isArchived: false,
  isFork: false,
  issues: { totalCount: issues },
  name,
  pullRequests: { totalCount: prs },
  pushedAt: '2026-11-16T23:46:49Z',
  url: `https://github.com/kgheacock/${name}`,
  ...extra,
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

test('TestFetchReposArgv', async () => {
  const { calls, execFile } = stubGh([{ stdout: JSON.stringify([row('macro-pad', 2, 5)]) }]);
  const got = await fetchRepos({ env: { INBOX_GH: '/opt/homebrew/bin/gh' }, execFile });
  assert.equal(calls[0].file, '/opt/homebrew/bin/gh');
  assert.deepEqual(calls[0].args.slice(0, 6), ['repo', 'list', DEFAULT_OWNER, '--limit', '1000', '--no-archived']);
  assert.equal(calls[0].args[6], '--json');
  assert.equal(calls[0].options.shell, false);
  assert.deepEqual(got.repos.map((r) => [r.name, r.issues, r.prs]), [['macro-pad', 2, 5]]);
  assert.equal(got.owner, 'kgheacock');
  assert.equal(got.offline, null);

  // The owner is set by INBOX_REPOS_OWNER.
  await fetchRepos({ env: { INBOX_REPOS_OWNER: 'acme' }, execFile });
  assert.equal(calls[1].file, 'gh');
  assert.deepEqual(calls[1].args.slice(0, 3), ['repo', 'list', 'acme']);

  // INBOX_REPOS_ARCHIVED=1 keeps the archived ones in.
  await fetchRepos({ env: { INBOX_REPOS_ARCHIVED: '1' }, execFile });
  assert.ok(!calls[2].args.includes('--no-archived'), calls[2].args.join(' '));
  assert.equal(calls.length, 3);
});

test('TestReposOwnerRejected', async () => {
  for (const owner of ['-x', 'a b', '--json', 'a/b', 'a;b', '-', 'x'.repeat(40)]) {
    const { calls, execFile } = stubGh([{ stdout: '[]' }]);
    await assert.rejects(fetchRepos({ env: { INBOX_REPOS_OWNER: owner }, execFile }), /not a GitHub user or organisation name/, owner);
    assert.equal(calls.length, 0, `a process ran for "${owner}"`);
  }
  assert.equal(reposOwner({}), 'kgheacock');
  assert.equal(reposOwner({ INBOX_REPOS_OWNER: '' }), 'kgheacock');
  assert.equal(reposOwner({ INBOX_REPOS_OWNER: 'Acme-1' }), 'Acme-1');
});

test('TestParseRepos', () => {
  const text = JSON.stringify([
    row('meta', 0, 0),
    row('keithheacock.com', 3, 6, { isFork: true }),
    row('macro-pad', 0, 5),
    row('old', 1, 0, { isArchived: true, pushedAt: null }),
  ]);
  const got = parseRepos(text);
  assert.equal(got.cut, false);
  // By name, with the open issue count and the open PR count of each.
  assert.deepEqual(
    got.repos.map((r) => [r.name, r.key, r.issues, r.prs, r.fork, r.archived]),
    [
      ['keithheacock.com', 'keithheacock-com', 3, 6, true, false],
      ['macro-pad', 'macro-pad', 0, 5, false, false],
      ['meta', 'meta', 0, 0, false, false],
      ['old', 'old', 1, 0, false, true],
    ],
  );
  assert.deepEqual(got.repos[1], {
    name: 'macro-pad',
    key: 'macro-pad',
    url: 'https://github.com/kgheacock/macro-pad',
    fork: false,
    archived: false,
    pushedAt: '2026-11-16T23:46:49Z',
    issues: 0,
    prs: 5,
  });
  assert.equal(got.repos[3].pushedAt, null);

  // 1000 rows may be a cut answer.
  const many = Array.from({ length: REPO_LIMIT }, (_, i) => row(`r${i}`));
  assert.equal(parseRepos(JSON.stringify(many)).cut, true);
  assert.equal(parseRepos(JSON.stringify(many.slice(1))).cut, false);

  // An answer in another shape is refused, not read as zero counts.
  assert.throws(() => parseRepos('not json'), /not JSON/);
  assert.throws(() => parseRepos('{}'), /not a list/);
  assert.throws(() => parseRepos(JSON.stringify([{ name: 'x', url: 'u', issues: 3 }])), /no issue count for x/);
  assert.throws(() => parseRepos(JSON.stringify([{ ...row('x'), pullRequests: undefined }])), /no PR count for x/);
  assert.throws(() => parseRepos(JSON.stringify([{ url: 'u' }])), /has no name/);
});

test('TestRepoKey', () => {
  assert.equal(repoKey('keithheacock.com'), 'keithheacock-com');
  assert.equal(repoKey('Macro-Pad'), 'macro-pad');
});

test('TestReposFeedFails', async () => {
  const missing = Object.assign(new Error('spawn gh ENOENT'), { code: 'ENOENT' });
  await assert.rejects(fetchRepos({ env: {}, execFile: stubGh([{ error: missing }]).execFile }), /no gh; install the GitHub CLI/);
  const auth = stubGh([{ error: Object.assign(new Error('exit 1'), { code: 1 }), stderr: 'gh auth login\nmore' }]);
  await assert.rejects(fetchRepos({ env: {}, execFile: auth.execFile }), /gh failed: gh auth login$/);
  assert.equal(feed.source, 'GitHub repositories');
  assert.equal(feed.everyMs, 60_000);
});
