// The repos plugin (task 0087): one top key, `repos`, with one child per
// repository of the feed (tasks 0089 and 0090), in the order of the activity
// ledger. It replaces the ai and github plugins. The Claude Code hook still
// reports each session to this plugin (PROJECT.SID channels, task 0072); a
// session colours the key of its repository and the top key: waiting
// (yellow), running (blue), done (green).
//
// The plugin has no key for a session or a PR. Its rows are the sessions only.

import { componentInput, componentOf, emojiFor, splitSessionChannel } from '../emoji.js';
import { orderRepos, nextShown } from '../activity.js';
import { removeLayerKeyMessage, setLayerKeyMessage } from '../protocol.js';
import { feed } from '../repolist.js';
import { yellowFeed } from './repos-yellow.js';
import { MAX_CAPTION, REPO_MAX_CHILDREN, clip } from '../render.js';

export const REPOS_STATUSES = ['waiting', 'running', 'done'];

// Pad status names (driver/layers/doc.go), by rank. Rank 1 is the lowest.
export const PAD_STATUS = [null, 'DONE', 'RUNNING', 'WARN'];

// How long a session stays in the rows after its last message.
export const ACTIVE_MS = 60 * 60 * 1000;

// The top key, the id of a repository's child, and the picture of the top
// key (under macropadd's assets directory).
export const TOP_ID = 'repos';
export const TOP_ICON = 'github.png';
export const repoId = (key) => `repo.${key}`;

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
export function createReposRenderer({ slot, log = () => {}, emojiSet } = {}) {
  const sent = new Map();
  // The keys shown, in the order macropadd holds them.
  let shown = [];
  // key -> repo, for every repository seen, so an open layer can still draw
  // a repository the feed no longer lists.
  const names = new Map();
  let known = false;
  let cutLogged = false;

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

    owns: () => false,

    reset() {
      sent.clear();
      shown = [];
      known = false;
    },

    target: () => null,

    plan({ unseen: rows, active, path, fed }) {
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

      for (const k of shown) {
        const id = repoId(k);
        const name = names.get(k) ?? k;
        set(out, id, {
          id,
          parent: TOP_ID,
          opens: true,
          status: PAD_STATUS[ranks.get(k) ?? 0] ?? '',
          count: 0,
          iconText: clip(name, MAX_CAPTION),
          iconEmoji: [emojiFor(componentInput(k), emojiSet).iconRef],
        });
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
for an hour after its last message.`,
  rules: {
    id: TOP_ID,
    activeMs: ACTIVE_MS,
  },
  createRenderer: ({ slot, log }) => createReposRenderer({ slot, log }),
  // The GitHub feed first: it sets what the renderer reads. The yellow
  // provider (task 0093) writes to the store only.
  feed: [feed, yellowFeed],
};
