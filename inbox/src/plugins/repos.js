// The repos plugin (task 0087): one top key, `repos`, with one child per
// repository of the feed (tasks 0089 and 0090), in the order of the activity
// ledger. It replaces the ai and github plugins. The Claude Code hook still
// reports each session to this plugin (PROJECT.SID channels, task 0072); a
// session colours the key of its repository and the top key: waiting
// (yellow), running (blue), done (green).
//
// A press on a repository key opens its layer (task 0095): the yellow items
// of the criteria provider, the open PRs, and the active Claude sessions of
// the repository, merged so that one piece of work is one key (see
// repos-items.js). The items exist only while that layer is open.

import { componentInput, componentOf, emojiFor, splitSessionChannel } from '../emoji.js';
import { orderRepos, nextShown } from '../activity.js';
import { removeLayerKeyMessage, setLayerKeyMessage } from '../protocol.js';
import { feed } from '../repolist.js';
import { yellowFeed } from './repos-yellow.js';
import { pullsFeed } from './repos-pulls.js';
import { MAX_ITEMS, activeSessions, itemStatus, itemsOf, press } from './repos-items.js';
import { MAX_CAPTION, REPO_MAX_CHILDREN, clip } from '../render.js';

export const REPOS_STATUSES = ['waiting', 'running', 'done'];

// Pad status names (driver/layers/doc.go), by rank. Rank 1 is the lowest.
export const PAD_STATUS = [null, 'DONE', 'RUNNING', 'WARN'];

// How long a session stays in the rows after its last message.
export const ACTIVE_MS = 60 * 60 * 1000;

// The plugin the Claude Code hook reports sessions to.
export const SESSION_PLUGIN = 'repos';

// The top key, the id of a repository's child, and the picture of the top
// key (under macropadd's assets directory).
export const TOP_ID = 'repos';
export const TOP_ICON = 'github.png';
const REPO_PREFIX = 'repo.';
export const repoId = (key) => `${REPO_PREFIX}${key}`;
// The key under which `sent` holds an item node of one repository.
const itemNodeKey = (repoKey, id) => `${repoId(repoKey)}/${id}`;

// The keys of the plugins this one replaces. The first connect removes them,
// so no key stays in the pad's layers.json.
export const STALE_KEYS = ['ai-inbox', 'github-inbox', TOP_ID];

// A stored status that is not one of the three counts as done. A row with no
// unseen message has rank 0.
export function rank(row) {
  if (row === undefined || row.count === 0) return 0;
  if (row.byStatus.waiting) return 3;
  if (row.byStatus.running) return 2;
  return 1;
}

// The repository key a session channel belongs to (task 0081): the name of
// its folder's repository, in lower case, a dot becoming -.
export const sessionRepo = (channel) => componentOf(splitSessionChannel(channel).folder).replace(/\./g, '-');

// The renderer of the repos plugin. It is a copy of the pattern of the apps
// renderer: `plan` draws nothing before the first answer of the feed; the
// first plan after a connect removes the keys of the retired plugins and the
// `repos` key itself, with whatever an earlier daemon left under it.
//
// With a `store` the renderer also reads the yellow counts, the open PRs, and
// the PR and branch tables: a repository key has `opens` only when it has
// items, and while its layer is open the renderer sends them (task 0095).
export function createReposRenderer({ slot, log = () => {}, emojiSet, store = null } = {}) {
  const sent = new Map();
  // The keys shown, in the order macropadd holds them.
  let shown = [];
  // key -> repo, for every repository seen, so an open layer can still draw
  // a repository the feed no longer lists.
  const names = new Map();
  let known = false;
  let cutLogged = false;
  let readLogged = false;
  // The repository whose items are on the pad, with the ids in the order
  // sent and the last item sent under each id. Null when no layer is open.
  let itemsKey = null;
  let itemIds = [];
  let itemData = new Map();

  const set = (out, key, fields) => {
    const json = JSON.stringify(fields);
    if (sent.get(key) === json) return;
    sent.set(key, json);
    out.push(setLayerKeyMessage(fields));
  };

  const remove = (out, key) => {
    sent.delete(repoId(key));
    out.push(removeLayerKeyMessage(repoId(key), TOP_ID));
  };

  return {
    topId: TOP_ID,

    // A press on an item reaches the plugin; the item layer is the only one
    // that has presses (a repository key opens its layer).
    owns: (parent) => itemsKey !== null && parent === repoId(itemsKey),

    reset() {
      sent.clear();
      shown = [];
      known = false;
      itemsKey = null;
      itemIds = [];
      itemData = new Map();
    },

    target: () => null,

    plan({ unseen: rows, active, path, fed, now = Date.now() }) {
      // The repositories are not known before the first answer.
      if (!fed) return [];
      const out = [];
      if (!known) {
        for (const id of STALE_KEYS) out.push(removeLayerKeyMessage(id));
        sent.clear();
        known = true;
      }

      // The highest rank of the sessions of each repository, and of all.
      const ranks = new Map();
      let top = 0;
      let waiting = 0;
      for (const row of rows) {
        const r = rank(row);
        top = Math.max(top, r);
        if (r === 3) waiting++;
        const key = sessionRepo(row.channel);
        ranks.set(key, Math.max(ranks.get(key) ?? 0, r));
      }

      // Every repository of the feed, in the order of the ledger, once each.
      const wanted = [];
      for (const repo of orderRepos(fed.repos ?? [], fed.scores)) {
        if (wanted.includes(repo.key)) continue;
        wanted.push(repo.key);
        names.set(repo.key, repo.name);
      }
      if (wanted.length > REPO_MAX_CHILDREN && !cutLogged) {
        cutLogged = true;
        log(`the feed has ${wanted.length} repositories; the pad shows the first ${REPO_MAX_CHILDREN}`);
      }

      const key = { id: TOP_ID, status: PAD_STATUS[top] ?? '', count: waiting, iconRef: TOP_ICON };
      if (slot !== undefined) key.slot = slot;
      set(out, TOP_ID, key);

      const open = path ? path[0] === TOP_ID : active === TOP_ID;

      // The repository whose layer is open.
      const wantKey = path?.[0] === TOP_ID && path[1]?.startsWith(REPO_PREFIX) ? path[1].slice(REPO_PREFIX.length) : null;
      // Leaving a repository layer: its items go, before its key can.
      if (itemsKey !== null && itemsKey !== wantKey) {
        for (const id of itemIds) {
          sent.delete(itemNodeKey(itemsKey, id));
          out.push(removeLayerKeyMessage(id, [TOP_ID, repoId(itemsKey)]));
        }
        itemsKey = null;
        itemIds = [];
        itemData = new Map();
      }

      if (open) {
        // Nothing a person sees moves: a new repository goes last.
        shown = nextShown({ shown, wanted, open: true, max: REPO_MAX_CHILDREN });
      } else {
        const next = nextShown({ shown, wanted, open: false, max: REPO_MAX_CHILDREN });
        for (const k of shown) if (!next.includes(k)) remove(out, k);
        const stay = shown.filter((k) => next.includes(k));
        // Keys that sit in another order than wanted go and come back.
        let same = 0;
        while (same < stay.length && stay[same] === next[same]) same++;
        for (const k of stay.slice(same)) remove(out, k);
        shown = next;
      }

      // Its items go to the pad only when the pad shows its key.
      const openKey = wantKey !== null && shown.includes(wantKey) ? wantKey : null;

      // Which repositories have items: a yellow count, an open PR, or an
      // active session. The layer that is open keeps `opens`, so it stays
      // open if its last item goes.
      let yellow = {};
      let pulls = {};
      if (store) {
        try {
          yellow = store.yellowCounts(now);
          pulls = store.pullCounts();
          readLogged = false;
        } catch (e) {
          if (!readLogged) log(`cannot read the items of the repositories: ${e.message}`);
          readLogged = true;
        }
      }
      const owner = fed.owner ?? '';
      const hasItems = (k) => {
        const name = names.get(k) ?? k;
        return (
          (yellow[name] ?? 0) > 0 ||
          (pulls[k] ?? 0) > 0 ||
          activeSessions(rows, { owner, name, now }).length > 0
        );
      };

      for (const k of shown) {
        const id = repoId(k);
        const name = names.get(k) ?? k;
        const fields = {
          id,
          parent: TOP_ID,
          status: PAD_STATUS[ranks.get(k) ?? 0] ?? '',
          count: 0,
          iconText: clip(name, MAX_CAPTION),
          iconEmoji: [emojiFor(componentInput(k), emojiSet).iconRef],
        };
        if (hasItems(k) || (k === openKey && sent.get(id)?.includes('"opens":true'))) fields.opens = true;
        set(out, id, fields);
      }

      // The items of the open repository: a new one goes last, a known one
      // changes in place, and none moves or goes while the layer is open.
      if (openKey !== null && store) {
        if (itemsKey === null) itemsKey = openKey;
        try {
          const { items } = itemsOf({ name: names.get(openKey) ?? openKey, owner, store, rows, now });
          for (const item of items) {
            if (!itemData.has(item.id) && itemIds.length >= MAX_ITEMS) continue;
            if (!itemData.has(item.id)) itemIds.push(item.id);
            itemData.set(item.id, item);
          }
        } catch (e) {
          if (!readLogged) log(`cannot read the items of ${names.get(openKey) ?? openKey}: ${e.message}`);
          readLogged = true;
        }
        const emoji = emojiFor(componentInput(openKey), emojiSet).iconRef;
        for (const id of itemIds) {
          const item = itemData.get(id);
          const label = item.yellow ?? (item.green !== null ? `#${item.green}` : item.session);
          set(out, itemNodeKey(openKey, id), {
            id,
            path: [TOP_ID, repoId(openKey)],
            status: itemStatus(item),
            count: 0,
            iconText: clip(String(label), MAX_CAPTION),
            iconEmoji: item.session ? [emoji, emojiFor(`session:${item.session}`, emojiSet).iconRef] : [emoji],
          });
        }
      }
      return out;
    },
  };
}

export default {
  name: 'repos',
  summary: 'a key per repository, coloured by Claude sessions: waiting, running, done',
  statuses: REPOS_STATUSES,
  defaultStatus: 'done',
  about: `The repos key opens one key per repository of the GitHub owner
(\$INBOX_REPOS_OWNER, default kgheacock), the ones you worked on first. A
Claude Code session colours the key of its repository: waiting (yellow),
running (blue), done (green). The top key shows the highest status of all
sessions, and its pip counts the sessions that wait. A session stays on
for an hour after its last message. A press on a repository key opens its
items: the yellow items of $INBOX_REPOS_YELLOW_CMD, its open PRs, and its
Claude sessions, one key for one piece of work. A press on an item opens its
PR, else its yellow item, else its session's tab.`,
  rules: {
    id: TOP_ID,
    activeMs: ACTIVE_MS,
  },
  createRenderer: ({ slot, log, store }) => createReposRenderer({ slot, log, store }),
  press,
  // The GitHub feed first: it sets what the renderer reads. The yellow
  // provider (task 0093) and the PR feed (task 0095) write to the store only.
  feed: [feed, yellowFeed, pullsFeed],
};
