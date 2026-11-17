// The items of a repository layer (task 0095): the yellow items of the
// criteria provider (task 0093), the open PRs of the repository, and the
// active Claude sessions in its folders, merged so that one piece of work is
// one key. A PR whose title names a yellow id joins that item; a session
// whose branch is a listed PR's, or names a listed yellow id, joins that item.
//
// An item: { id, yellow, green, session, title, url, ci, agent, here }.
//   id       the id of its node: y:ID, pr:N, or s:SID
//   yellow   the yellow id, or null
//   green    the PR number, or null
//   session  the 8-digit session id, or null
//   url      the PR's url, else the yellow item's, else null
//   ci       failing, pending, passing, none, or null (no PR)
//   agent    waiting, running, done, or null (no session)
//   here     the "here" of the session, or null
// Task 0096 draws them; this file only decides which items exist.

import { repoKey } from '../activity.js';
import { componentOf, splitSessionChannel } from '../emoji.js';
import { focus as focusHere, parseHere } from '../here.js';
import { ACTIVE_MS, SESSION_PLUGIN, rank, repoId } from './repos.js';

// At most this many items show; the driver pages them (task 0078).
export const MAX_ITEMS = 20;

const AGENTS = [null, 'done', 'running', 'waiting'];

// Pad status names (driver/layers/doc.go) of an item.
export function itemStatus(item) {
  if (item.ci === 'failing') return 'ERROR';
  if (item.agent === 'waiting') return 'WARN';
  if (item.ci === 'pending' || item.agent === 'running') return 'RUNNING';
  if (item.ci === 'passing' || item.agent === 'done') return 'DONE';
  return '';
}

// Whether `text` holds `id` between characters that are not letters or
// digits, in any letter case: PROJ-123 is in "PROJ-123: fix" and in
// "fix/proj-123-login", not in PROJ-1234 or XPROJ-123.
export function mentions(text, id) {
  if (typeof text !== 'string' || id === '') return false;
  const haystack = text.toLowerCase();
  const needle = id.toLowerCase();
  const alnum = /[a-z0-9]/;
  for (let at = haystack.indexOf(needle); at !== -1; at = haystack.indexOf(needle, at + 1)) {
    const before = haystack[at - 1];
    const after = haystack[at + needle.length];
    if ((before === undefined || !alnum.test(before)) && (after === undefined || !alnum.test(after))) return true;
  }
  return false;
}

// Whether the folder channel `folder` (HOST.TLD.OWNER.REPO..., task 0072) is
// inside the repository OWNER/NAME: it holds `.OWNER.NAME` with a segment
// edge on both sides, so subfolders count and macro-pad-x is not macro-pad.
export function inRepo(folder, owner, name) {
  return `.${folder}.`.toLowerCase().includes(`.${owner}.${name}.`.toLowerCase());
}

// The active Claude sessions of one repository, from Store.recent() rows:
// [{ sid, agent, rank, here, link }], newest word first by rank. A session is
// active when it is of this repository, its newest message is younger than
// ACTIVE_MS, and it has an unseen message (`end` marks the rows seen and
// sends nothing). link is the repository key its branches are stored under
// (task 0081).
export function activeSessions(rows, { owner, name, now = Date.now() }) {
  const out = [];
  for (const row of rows ?? []) {
    const { folder, session } = splitSessionChannel(row.channel);
    if (session === null || !inRepo(folder, owner, name)) continue;
    if (row.createdAt < now - ACTIVE_MS || !(row.count > 0)) continue;
    const r = rank(row);
    out.push({ sid: session, agent: AGENTS[r], rank: r, here: row.here ?? null, link: componentOf(folder) });
  }
  return out.sort((a, b) => b.rank - a.rank || (a.sid < b.sid ? -1 : a.sid > b.sid ? 1 : 0));
}

// How urgent an item is inside its group: lower first.
function urgency(item) {
  if (item.ci === 'failing') return 0;
  if (item.agent === 'waiting') return 1;
  if (item.ci === 'pending') return 2;
  if (item.agent === 'running') return 3;
  return 4;
}

const group = (item) => (item.yellow !== null ? 0 : item.green !== null ? 1 : 2);

// Orders two items: yellow, green, session; then failing, waiting, pending,
// running, the rest; then id.
export function compareItems(a, b) {
  return group(a) - group(b) || urgency(a) - urgency(b) || (a.id < b.id ? -1 : a.id > b.id ? 1 : 0);
}

// Merges the three lists into items, in order, all of them (the caller cuts
// to MAX_ITEMS).
//   yellow           [{ id, title, url }]
//   pulls            [{ number, title, url, ci }], first one wins a yellow id
//   sessions         activeSessions()
//   sessionOfPull(pr) -> the session id on the PR's branch, or null
//   branchesOf(session) -> the branches the session ran on
export function mergeItems({ yellow = [], pulls = [], sessions = [], sessionOfPull = () => null, branchesOf = () => [] }) {
  const items = yellow.map((y) => ({
    id: `y:${y.id}`,
    yellow: y.id,
    green: null,
    session: null,
    title: y.title,
    url: y.url ?? null,
    ci: null,
    agent: null,
    here: null,
  }));
  const yellowItems = [...items];

  // PR number -> its item.
  const byPull = new Map();
  for (const pr of pulls) {
    const target = yellowItems.find((item) => item.green === null && mentions(pr.title, item.yellow));
    if (target) {
      target.green = pr.number;
      target.url = pr.url;
      target.ci = pr.ci;
      byPull.set(pr.number, target);
      continue;
    }
    const item = {
      id: `pr:${pr.number}`,
      yellow: null,
      green: pr.number,
      session: null,
      title: pr.title,
      url: pr.url,
      ci: pr.ci,
      agent: null,
      here: null,
    };
    items.push(item);
    byPull.set(pr.number, item);
  }

  // A session joins the item of its branch, once. Else it is an item.
  const join = (item, s) => {
    item.session = s.sid;
    item.agent = s.agent;
    item.here = s.here;
  };
  // The session on each PR's branch, asked once, and only when one is active.
  const onPull = new Map(sessions.length > 0 ? pulls.map((p) => [p.number, sessionOfPull(p)]) : []);
  for (const s of sessions) {
    const pr = pulls.find((p) => onPull.get(p.number) === s.sid && byPull.get(p.number).session === null);
    let target = pr ? byPull.get(pr.number) : null;
    if (!target) {
      const branches = branchesOf(s);
      target = yellowItems.find((item) => item.session === null && branches.some((b) => mentions(b, item.yellow))) ?? null;
    }
    if (target) {
      join(target, s);
      continue;
    }
    const item = { id: `s:${s.sid}`, yellow: null, green: null, session: null, title: s.sid, url: null, ci: null, agent: null, here: null };
    join(item, s);
    items.push(item);
  }
  return items.sort(compareItems);
}

// The names under which the branch link of a PR's head may be stored: the
// head's repoKey, the repository's, and its first dotted part (the key that
// componentOf gives a folder).
const linkKeys = (name, pr) => [...new Set([pr.headRepo, repoKey(name), name.split('.')[0].toLowerCase()].filter(Boolean))];

// The items of repository `name` of `owner`, at most MAX_ITEMS:
// { items, total }. rows are Store.recent() rows of the repos plugin; without
// them the function reads them. now is for a test.
export function itemsOf({ name, owner, store, rows, now = Date.now() }) {
  const yellow = store.yellowItems(name, now);
  const pulls = store.listPulls(repoKey(name));
  const sessions = activeSessions(rows ?? store.forPlugin(SESSION_PLUGIN).recent(now - ACTIVE_MS), { owner, name, now });
  const sessionOfPull = (pr) => {
    if (pr.branch === null) return null;
    for (const key of linkKeys(name, pr)) {
      const sid = store.branchSession(key, pr.branch);
      if (sid !== null) return sid;
    }
    return null;
  };
  const items = mergeItems({
    yellow,
    pulls,
    sessions,
    sessionOfPull,
    branchesOf: (s) => store.sessionBranches(s.link, s.sid),
  });
  return { items: items.slice(0, MAX_ITEMS), total: items.length };
}

// The item a press named: by id, else by the PR or session it holds, since a
// PR that was alone may have joined a yellow item since the key was drawn.
function findItem(items, id) {
  const exact = items.find((item) => item.id === id);
  if (exact) return exact;
  if (id.startsWith('pr:')) return items.find((item) => item.green === Number(id.slice(3)));
  if (id.startsWith('s:')) return items.find((item) => item.session === id.slice(2));
  return undefined;
}

// The "here" a press goes to: the PR's url, else the yellow item's url, else
// the session's. Null when there is none, or it is not a valid "here".
export function targetOf(item) {
  const target = item.url ? `url:${item.url}` : item.here;
  return target && parseHere(target) !== null ? target : null;
}

// Acts on a press on an item key: `parent` is repo.KEY, `child` the item id.
// A bad target is logged, and nothing runs.
export async function press({ parent, child, store, fed, focus = focusHere, log = () => {}, now = Date.now() }) {
  const repo = (fed?.repos ?? []).find((r) => repoId(r.key) === parent);
  if (!repo) {
    log(`no repository ${parent} in the feed`);
    return;
  }
  const { items } = itemsOf({ name: repo.name, owner: fed.owner, store, now });
  const item = findItem(items, child);
  if (!item) {
    log(`no item ${child} in ${repo.name} any more`);
    return;
  }
  const target = targetOf(item);
  if (target === null) {
    log(`cannot open ${child} of ${repo.name}: it has no valid url or "here"`);
    return;
  }
  await focus(target);
}
