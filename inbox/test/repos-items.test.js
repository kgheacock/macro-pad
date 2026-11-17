import assert from 'node:assert/strict';
import test from 'node:test';
import { startDaemon } from '../src/daemon.js';
import repos, { ACTIVE_MS, createReposRenderer } from '../src/plugins/repos.js';
import { MAX_ITEMS, activeSessions, inRepo, itemsOf, mentions, mergeItems, press } from '../src/plugins/repos-items.js';
import { createPullsFeed, parsePulls } from '../src/plugins/repos-pulls.js';
import { openStore } from '../src/store.js';
import { scratch } from '../testlib/helpers.js';
import { TestServer, waitFor } from '../testlib/wsserver.js';

const OWNER = 'kgheacock';
const NOW = 1_000_000_000_000;
const TAB = 'iterm2:0A1B2C3D-4E5F-6071-8293-A4B5C6D7E8F9';

const yellow = (id, title = `title ${id}`) => ({ id, title, url: `https://jira.test/browse/${id}` });
const pull = (repo, number, extra = {}) => ({
  repo,
  number,
  title: `PR ${number}`,
  url: `https://github.com/${OWNER}/${repo}/pull/${number}`,
  ci: 'none',
  headRepo: repo,
  branch: null,
  ...extra,
});
const sessionRow = (channel, { status = 'running', count = 1, createdAt = NOW, here = null } = {}) => ({
  channel,
  byStatus: count === 0 ? {} : { [status]: count },
  count,
  newestId: 1,
  createdAt,
  status,
  here,
});
const folder = (name, sid, sub = '') => `github.com.${OWNER}.${name}${sub}.${sid}`;

function openRepos(t) {
  const sc = scratch(t);
  const store = openStore(sc.db, undefined, { plugin: 'repos' });
  t.after(() => store.close());
  return { sc, store };
}

const idsOf = (items) => items.map((item) => item.id);

test('TestMergePrIntoYellow', () => {
  const merge = (pulls, ids = ['PROJ-123']) => mergeItems({ yellow: ids.map((id) => yellow(id)), pulls });

  // The title names the id, in any letter case.
  for (const title of ['PROJ-123: fix login', 'proj-123 fix login', 'Fix login (Proj-123)', 'fix PROJ-123']) {
    const items = merge([{ number: 5, title, url: 'https://github.com/x/y/pull/5', ci: 'failing' }]);
    assert.deepEqual(idsOf(items), ['y:PROJ-123'], title);
    assert.deepEqual([items[0].yellow, items[0].green, items[0].ci, items[0].url], ['PROJ-123', 5, 'failing', 'https://github.com/x/y/pull/5']);
  }

  // A longer number and a longer prefix are other ids.
  const other = merge([
    { number: 6, title: 'PROJ-1234: other', url: 'https://github.com/x/y/pull/6', ci: 'none' },
    { number: 7, title: 'XPROJ-123: other', url: 'https://github.com/x/y/pull/7', ci: 'none' },
  ]);
  assert.deepEqual(idsOf(other), ['y:PROJ-123', 'pr:6', 'pr:7']);
  assert.equal(other[0].green, null);

  // Two PRs for one id: the first merges, the second stands alone.
  const two = merge([
    { number: 8, title: 'PROJ-123: first', url: 'https://github.com/x/y/pull/8', ci: 'none' },
    { number: 9, title: 'PROJ-123: second', url: 'https://github.com/x/y/pull/9', ci: 'none' },
  ]);
  assert.deepEqual(idsOf(two), ['y:PROJ-123', 'pr:9']);
  assert.equal(two[0].green, 8);

  assert.ok(mentions('keith/PROJ-123-login', 'PROJ-123'));
  assert.ok(!mentions('PROJ-1234', 'PROJ-123'));
  assert.ok(!mentions('XPROJ-123', 'PROJ-123'));
});

test('TestSessionDedupe', (t) => {
  const { store } = openRepos(t);
  store.putYellow('macro-pad', { items: [yellow('PROJ-5')], now: NOW });
  store.replacePulls([
    pull('macro-pad', 9, { title: 'Fix the thing', branch: 'fix-thing' }),
    pull('macro-pad', 10, { title: 'PROJ-5: add it', branch: 'proj-5-add' }),
  ]);
  // One session on a listed PR's branch, one on a branch that names a yellow
  // id, one on neither.
  store.linkBranch('macro-pad', 'fix-thing', '3f2a91c0');
  store.linkBranch('macro-pad', 'keith/PROJ-5-work', 'aaaa1111');
  store.linkBranch('macro-pad', 'scratch', 'bbbb2222');
  const rows = [
    sessionRow(folder('macro-pad', '3f2a91c0'), { status: 'waiting' }),
    sessionRow(folder('macro-pad', 'aaaa1111')),
    sessionRow(folder('macro-pad', 'bbbb2222')),
  ];
  const { items } = itemsOf({ name: 'macro-pad', owner: OWNER, store, rows, now: NOW });
  assert.deepEqual(idsOf(items), ['y:PROJ-5', 'pr:9', 's:bbbb2222']);
  const [yellowItem, prItem, lone] = items;
  // The PR's session did not make a key; it colours the PR's.
  assert.deepEqual([prItem.session, prItem.agent], ['3f2a91c0', 'waiting']);
  // The session on the yellow id's branch joined the item, which holds a PR too.
  assert.deepEqual([yellowItem.session, yellowItem.agent, yellowItem.green], ['aaaa1111', 'running', 10]);
  assert.deepEqual([lone.session, lone.agent, lone.yellow, lone.green], ['bbbb2222', 'running', null, null]);

  // A second session on a taken item shows as its own key.
  store.linkBranch('macro-pad', 'PROJ-5-again', 'cccc3333');
  rows.push(sessionRow(folder('macro-pad', 'cccc3333')));
  assert.deepEqual(idsOf(itemsOf({ name: 'macro-pad', owner: OWNER, store, rows, now: NOW }).items), [
    'y:PROJ-5',
    'pr:9',
    's:bbbb2222',
    's:cccc3333',
  ]);
});

test('TestActiveSessions', () => {
  const rows = [
    sessionRow(folder('macro-pad', '3f2a91c0')),
    // A subfolder of the repository counts.
    sessionRow(folder('macro-pad', 'cafe0001', '.driver')),
    // Another repository, one whose name starts the same, another owner.
    sessionRow(folder('other', 'cafe0002')),
    sessionRow(folder('macro-pad-x', 'cafe0003')),
    sessionRow(`github.com.someone.macro-pad.cafe0004`),
    // Older than ACTIVE_MS.
    sessionRow(folder('macro-pad', 'cafe0005'), { createdAt: NOW - ACTIVE_MS - 1 }),
    // Ended: `end` marks the rows seen, so nothing is unseen.
    sessionRow(folder('macro-pad', 'cafe0006'), { count: 0, status: 'done' }),
    // No session id in the channel.
    sessionRow('github.com.kgheacock.macro-pad'),
    // In the repository by name and case.
    sessionRow(`GitHub.com.KGheacock.Macro-Pad.cafe0007`, { status: 'waiting' }),
  ];
  const active = activeSessions(rows, { owner: OWNER, name: 'macro-pad', now: NOW });
  assert.deepEqual(active.map((s) => [s.sid, s.agent]), [
    ['cafe0007', 'waiting'],
    ['3f2a91c0', 'running'],
    ['cafe0001', 'running'],
  ]);
  assert.deepEqual(activeSessions(rows, { owner: OWNER, name: 'other', now: NOW }).map((s) => s.sid), ['cafe0002']);
  assert.equal(inRepo('github.com.kgheacock.keithheacock.com.driver', OWNER, 'keithheacock.com'), true);
  assert.equal(inRepo('github.com.kgheacock.keithheacock-com', OWNER, 'keithheacock.com'), false);
  // The edge of the age is in: a message of exactly ACTIVE_MS ago still counts.
  assert.equal(
    activeSessions([sessionRow(folder('macro-pad', 'cafe0008'), { createdAt: NOW - ACTIVE_MS })], { owner: OWNER, name: 'macro-pad', now: NOW }).length,
    1,
  );
});

test('TestItemOrder', (t) => {
  const { store } = openRepos(t);
  store.putYellow('macro-pad', { items: ['A-1', 'A-2', 'A-3', 'A-4', 'A-5'].map((id) => yellow(id)), now: NOW });
  const pulls = [];
  for (let n = 1; n <= 10; n++) pulls.push(pull('macro-pad', n, { title: `PR ${n}`, ci: n === 10 ? 'failing' : n === 9 ? 'pending' : 'passing' }));
  // A-3 gets a failing PR and A-2 a pending one.
  pulls.push(pull('macro-pad', 11, { title: 'A-3: x', ci: 'failing' }), pull('macro-pad', 12, { title: 'A-2: x', ci: 'pending' }));
  store.replacePulls(pulls);
  const rows = [];
  for (let i = 0; i < 8; i++) rows.push(sessionRow(folder('macro-pad', `5e55000${i}`), { status: i === 6 ? 'waiting' : 'running' }));
  const all = itemsOf({ name: 'macro-pad', owner: OWNER, store, rows, now: NOW });
  // 5 yellow + 10 green + 8 sessions = 23 candidates; 20 are sent.
  assert.equal(all.total, 23);
  assert.equal(all.items.length, 20);
  assert.equal(MAX_ITEMS, 20);
  const ids = idsOf(all.items);
  // Yellow first: failing A-3, pending A-2, then the rest by id.
  assert.deepEqual(ids.slice(0, 5), ['y:A-3', 'y:A-2', 'y:A-1', 'y:A-4', 'y:A-5']);
  // Green next: failing pr:10, pending pr:9, then the rest by id.
  assert.deepEqual(ids.slice(5, 8), ['pr:10', 'pr:9', 'pr:1']);
  assert.equal(ids[14], 'pr:8');
  // Sessions last: the waiting one first. Five of the eight fit.
  assert.deepEqual(ids.slice(15), ['s:5e550006', 's:5e550000', 's:5e550001', 's:5e550002', 's:5e550003']);
});

// The fed answer of the repos feed for these tests.
const fedOf = (...names) => ({
  owner: OWNER,
  repos: names.map((name) => ({ name, key: name.replace(/\./g, '-').toLowerCase() })),
  cut: false,
  offline: null,
});
const planOf = (renderer, { rows = [], fed, path = [], now = NOW } = {}) =>
  renderer.plan({ unseen: rows, active: path.at(-1) ?? null, path, fed, now });
const sets = (messages) => messages.filter((m) => m.setLayerKey).map((m) => m.setLayerKey);
const itemNodes = (messages) => sets(messages).filter((k) => k.path);
const removes = (messages) => messages.filter((m) => m.kind === 'removeLayerKey').map((m) => m.removeLayerKey);
const lastOf = (messages, id) => sets(messages).findLast((k) => k.id === id);

test('TestRepoLayerLifecycle', (t) => {
  const { store } = openRepos(t);
  store.putYellow('alpha', { items: [yellow('A-1'), yellow('A-2')], now: NOW });
  store.replacePulls([pull('beta', 4, { title: 'Beta work' })]);
  const r = createReposRenderer({ store });
  const fed = fedOf('alpha', 'beta', 'gamma');
  const rows = [sessionRow(folder('gamma', '3f2a91c0'))];

  // Shut: a repository has `opens` only when it has items, and no item node
  // exists.
  let messages = planOf(r, { fed });
  assert.equal(lastOf(messages, 'repo.alpha').opens, true);
  assert.equal(lastOf(messages, 'repo.beta').opens, true);
  assert.equal(lastOf(messages, 'repo.gamma').opens, undefined);
  assert.deepEqual(itemNodes(messages), []);
  assert.equal(r.owns('repo.alpha'), false);

  // A session makes its repository open.
  messages = planOf(r, { fed, rows });
  assert.equal(lastOf(messages, 'repo.gamma').opens, true);
  assert.deepEqual(itemNodes(messages), []);

  // Open: the items go to the pad, under the repository, at once.
  const path = ['repos', 'repo.alpha'];
  messages = planOf(r, { fed, rows, path });
  const nodes = itemNodes(messages);
  assert.deepEqual(nodes.map((k) => k.id), ['y:A-1', 'y:A-2']);
  for (const node of nodes) {
    assert.deepEqual(node.path, ['repos', 'repo.alpha']);
    assert.equal(node.parent, undefined);
    assert.equal(node.count, 0);
  }
  assert.equal(nodes[0].iconText, 'A-1');
  assert.equal(r.owns('repo.alpha'), true);
  assert.equal(r.owns('repo.beta'), false);
  assert.equal(r.owns('repos'), false);

  // Leave: the items go and nothing else does.
  messages = planOf(r, { fed, rows, path: ['repos'] });
  assert.deepEqual(removes(messages), [
    { id: 'y:A-1', path: ['repos', 'repo.alpha'] },
    { id: 'y:A-2', path: ['repos', 'repo.alpha'] },
  ]);
  assert.deepEqual(itemNodes(messages), []);
  assert.equal(r.owns('repo.alpha'), false);
  // Nothing is left to remove on the next plan, and a new visit sends again.
  assert.deepEqual(removes(planOf(r, { fed, rows, path: ['repos'] })), []);
  assert.deepEqual(itemNodes(planOf(r, { fed, rows, path })).map((k) => k.id), ['y:A-1', 'y:A-2']);

  // The top layer, or another plugin's layer, is a leave too.
  messages = planOf(r, { fed, rows, path: [], now: NOW });
  assert.equal(removes(messages).length, 2);

  // A repository with no items has no `opens` any more.
  store.putYellow('alpha', { items: [], now: NOW });
  assert.equal(lastOf(planOf(r, { fed, rows, path: [] }), 'repo.alpha').opens, undefined);
});

test('TestRepoLayerStable', (t) => {
  const { store } = openRepos(t);
  store.putYellow('alpha', { items: [yellow('A-1'), yellow('A-2'), yellow('A-3')], now: NOW });
  store.replacePulls([pull('beta', 4)]);
  const r = createReposRenderer({ store });
  const fed = fedOf('alpha', 'beta');
  const path = ['repos', 'repo.alpha'];
  assert.deepEqual(itemNodes(planOf(r, { fed, path })).map((k) => k.id), ['y:A-1', 'y:A-2', 'y:A-3']);

  // A feed run drops A-1, keeps A-2, adds A-0 (which would sort first), and
  // a PR names A-3 and turns it failing. Nothing moves or goes; the changed
  // one is sent again; the new one comes last.
  store.putYellow('alpha', { items: [yellow('A-0'), yellow('A-2'), yellow('A-3')], now: NOW });
  store.replacePulls([pull('beta', 4), pull('alpha', 20, { title: 'A-3: go', ci: 'failing' })]);
  const messages = planOf(r, { fed, path });
  assert.deepEqual(removes(messages), []);
  assert.deepEqual(itemNodes(messages).map((k) => [k.id, k.status]), [['y:A-3', 'ERROR'], ['y:A-0', '']]);

  // The same state sends nothing, and a session joins in place.
  assert.deepEqual(planOf(r, { fed, path }), []);
  store.linkBranch('alpha', 'A-2-work', '3f2a91c0');
  const rows = [sessionRow(folder('alpha', '3f2a91c0'), { status: 'waiting' })];
  const joined = planOf(r, { fed, path, rows });
  assert.deepEqual(itemNodes(joined).map((k) => [k.id, k.status]), [['y:A-2', 'WARN']]);
  assert.equal(itemNodes(joined)[0].iconEmoji.length, 2, 'the session emoji follows');

  // Items of one repository at a time.
  const other = planOf(r, { fed, path: ['repos', 'repo.beta'], rows });
  assert.deepEqual(removes(other).map((k) => [k.id, k.path[1]]), [
    ['y:A-1', 'repo.alpha'],
    ['y:A-2', 'repo.alpha'],
    ['y:A-3', 'repo.alpha'],
    ['y:A-0', 'repo.alpha'],
  ]);
  assert.deepEqual(itemNodes(other).map((k) => [k.id, k.path[1]]), [['pr:4', 'repo.beta']]);
});

test('TestRepoLayerStopsAtTwenty', (t) => {
  const { store } = openRepos(t);
  store.putYellow('alpha', { items: Array.from({ length: 23 }, (_, i) => yellow(`A-${String(i).padStart(2, '0')}`)), now: NOW });
  const r = createReposRenderer({ store });
  const path = ['repos', 'repo.alpha'];
  assert.equal(itemNodes(planOf(r, { fed: fedOf('alpha'), path })).length, 20);
  // A later run does not add a 21st.
  store.putYellow('alpha', { items: Array.from({ length: 23 }, (_, i) => yellow(`B-${String(i).padStart(2, '0')}`)), now: NOW });
  assert.equal(itemNodes(planOf(r, { fed: fedOf('alpha'), path })).length, 0);
});

test('TestPressItem', async (t) => {
  const { store } = openRepos(t);
  store.putYellow('macro-pad', { items: [yellow('PROJ-1'), yellow('PROJ-2')], now: NOW });
  store.replacePulls([
    pull('macro-pad', 3, { title: 'PROJ-1: fix' }),
    pull('macro-pad', 4, { title: 'Alone' }),
  ]);
  const rows = [
    sessionRow(folder('macro-pad', '3f2a91c0'), { here: TAB }),
    sessionRow(folder('macro-pad', 'aaaa1111'), { here: null }),
  ];
  for (const row of rows) store.insert(row.channel, 'x', '/x', { status: 'running', here: row.here, now: NOW });
  const fed = fedOf('macro-pad');
  const calls = [];
  const logs = [];
  const go = (child) => press({ parent: 'repo.macro-pad', child, store, fed, focus: async (here) => calls.push(here), log: (l) => logs.push(l), now: NOW });

  // A merged item opens the PR, not the ticket.
  await go('y:PROJ-1');
  assert.deepEqual(calls, ['url:https://github.com/kgheacock/macro-pad/pull/3']);
  // A yellow item with no PR opens the ticket.
  await go('y:PROJ-2');
  assert.equal(calls.length, 2);
  assert.equal(calls[1], 'url:https://jira.test/browse/PROJ-2');
  // A PR alone opens the PR.
  await go('pr:4');
  assert.equal(calls[2], 'url:https://github.com/kgheacock/macro-pad/pull/4');
  // A session alone goes to its tab.
  await go('s:3f2a91c0');
  assert.equal(calls[3], TAB);
  assert.equal(calls.length, 4, 'one call per press');
  assert.deepEqual(logs, []);

  // A session with no "here", and an item that is gone, call nothing.
  await go('s:aaaa1111');
  await go('pr:99');
  assert.equal(calls.length, 4);
  assert.equal(logs.length, 2);
  assert.match(logs[0], /s:aaaa1111.*no valid url or "here"/);
  assert.match(logs[1], /no item pr:99/);

  // A bad url is no target: store it past the validation of the feed.
  store.replacePulls([pull('macro-pad', 5, { title: 'Bad', url: 'javascript:alert(1)' })]);
  await go('pr:5');
  assert.equal(calls.length, 4);
  assert.match(logs[2], /pr:5/);

  // A press for a repository the feed lacks calls nothing.
  await press({ parent: 'repo.nope', child: 'pr:4', store, fed, focus: async (h) => calls.push(h), log: (l) => logs.push(l), now: NOW });
  assert.equal(calls.length, 4);

  // An item that joined a yellow item since it was drawn is still found.
  store.replacePulls([pull('macro-pad', 3, { title: 'PROJ-1: fix' })]);
  await go('pr:3');
  assert.equal(calls.length, 5);
});

test('TestPullsFeed', () => {
  const node = (n, extra = {}) => ({
    number: n,
    title: `T${n}`,
    url: `https://github.com/kgheacock/Macro.Pad/pull/${n}`,
    state: 'OPEN',
    repository: { name: 'Macro.Pad', owner: { login: 'kgheacock' } },
    headRefName: 'fix',
    headRepository: { name: 'Macro.Pad' },
    commits: { nodes: [{ commit: { statusCheckRollup: { state: 'FAILURE' } } }] },
    ...extra,
  });
  const text = JSON.stringify({ data: { search: { issueCount: 3, nodes: [node(1), {}, node(2, { commits: { nodes: [] } })] } } });
  const { total, pulls } = parsePulls(text);
  assert.equal(total, 3);
  assert.deepEqual(pulls.map((p) => [p.repo, p.number, p.ci, p.headRepo, p.branch]), [
    ['macro-pad', 1, 'failing', 'macro-pad', 'fix'],
    ['macro-pad', 2, 'none', 'macro-pad', 'fix'],
  ]);
  assert.throws(() => parsePulls('nope'), /not JSON/);
  assert.throws(() => parsePulls('{"errors":[{"message":"bad"}]}'), /GitHub said: bad/);

  // A cut answer (issueCount 3, two listed) deletes no row, and logs once.
  const feed = createPullsFeed();
  const written = [];
  const store = { replacePulls: (list, options) => (written.push([list.length, options.complete]), 0) };
  const logs = [];
  feed.sync(store, { total: 3, pulls }, { log: (l) => logs.push(l) });
  feed.sync(store, { total: 3, pulls }, { log: (l) => logs.push(l) });
  feed.sync(store, { total: 2, pulls }, { log: (l) => logs.push(l) });
  assert.deepEqual(written, [[2, false], [2, false], [2, true]]);
  assert.equal(logs.length, 1);
});

test('TestDaemonRepoLayer', async (t) => {
  const sc = scratch(t);
  const server = new TestServer();
  server.announceLayer(null);
  await server.listen();
  t.after(() => server.close());
  const writer = openStore(sc.db, undefined, { plugin: 'repos' });
  t.after(() => writer.close());
  const now = Date.now();
  writer.putYellow('macro-pad', { items: [yellow('PROJ-1')], now });
  writer.replacePulls([pull('macro-pad', 7, { title: 'Alone' })]);
  const calls = [];
  const answer = { ...fedOf('macro-pad'), repos: [{ name: 'macro-pad', key: 'macro-pad', url: 'u' }] };
  const plugin = { ...repos, feed: { source: 'stub', everyMs: 10_000, fetch: async () => answer, sync: () => 0 } };
  const daemon = startDaemon({
    file: sc.db,
    port: server.port,
    pollMs: 20,
    feedMs: 10_000,
    log: () => {},
    plugin,
    focus: async (here) => calls.push(here),
  });
  t.after(() => daemon.stop());

  await waitFor(() => server.received.some((m) => m.setLayerKey?.id === 'repo.macro-pad'), 1000, 'the repo key');
  const key = server.received.findLast((m) => m.setLayerKey?.id === 'repo.macro-pad').setLayerKey;
  assert.equal(key.opens, true);
  assert.equal(server.received.some((m) => m.setLayerKey?.path), false, 'no item before the layer opens');

  // The layer of the repo opens: the items follow.
  server.send({ kind: 'layerChanged', layerChanged: { active: 'repo.macro-pad', path: ['repos', 'repo.macro-pad'] } });
  await waitFor(() => server.received.filter((m) => m.setLayerKey?.path).length === 2, 1000, 'the items');
  const items = server.received.filter((m) => m.setLayerKey?.path).map((m) => m.setLayerKey);
  assert.deepEqual(items.map((k) => k.id), ['y:PROJ-1', 'pr:7']);
  assert.deepEqual(items[0].path, ['repos', 'repo.macro-pad']);

  // A press on a PR key reaches `press`.
  server.send({ kind: 'childPressed', childPressed: { keyIndex: 1, parent: 'repo.macro-pad', child: 'pr:7', path: ['repos', 'repo.macro-pad'] } });
  await waitFor(() => calls.length === 1, 1000, 'one focus');
  assert.deepEqual(calls, ['url:https://github.com/kgheacock/macro-pad/pull/7']);

  // Leaving takes them away.
  server.send({ kind: 'layerChanged', layerChanged: { active: 'repos', path: ['repos'] } });
  await waitFor(() => server.received.filter((m) => m.kind === 'removeLayerKey' && m.removeLayerKey.path).length === 2, 1000, 'the removals');
  assert.deepEqual(
    server.received.filter((m) => m.kind === 'removeLayerKey' && m.removeLayerKey.path).map((m) => m.removeLayerKey.id),
    ['y:PROJ-1', 'pr:7'],
  );
});
