// The picture of a repository key (task 0092): the emoji of the repository,
// its name as the caption, and three counts in the badges of task 0091. Pure
// functions: the renderer of the repos plugin reads the tables and calls them.

import { repoKey } from './activity.js';
import { componentInput, emojiFor } from './emoji.js';
import { MAX_CAPTION, clip } from './render.js';

// The picture of the top key (under macropadd's assets directory).
export const TOP_ICON = 'github.png';

// The pad status names (driver/layers/doc.go), by rank. Rank 1 is the lowest.
export const PAD_STATUS = [null, 'DONE', 'RUNNING', 'WARN'];

// The session status of a rank, for the renderer.
export const RANK_STATUS = [null, 'done', 'running', 'waiting'];

// The driver draws a count above 99 as "99+". Any number from 100 up draws
// the same, so the key sends 100 and a count that moves from 150 to 160
// sends nothing.
export const BADGE_MAX = 100;

// The number to send for a count: at most BADGE_MAX. A count that is not a
// positive number is 0.
export function badge(n) {
  return Number.isFinite(n) && n > 0 ? Math.min(Math.floor(n), BADGE_MAX) : 0;
}

// The rank of one session status: waiting is 3, running is 2, and any other
// stored status counts as done (1). No status is 0.
function statusRank(status) {
  if (status === 'waiting') return 3;
  if (status === 'running') return 2;
  return status ? 1 : 0;
}

// The pad status of the worst of some session statuses (a name, or a list of
// names, or none): waiting is WARN, running is RUNNING, done is DONE, none
// is ''.
export function padStatus(status) {
  const list = Array.isArray(status) ? status : [status];
  return PAD_STATUS[Math.max(0, ...list.map(statusRank))] ?? '';
}

// The three counts as the fields of a setLayerKey. A count of 0 sets no
// field, so a full upsert resets it and the driver draws no badge.
function countFields({ issues = 0, prs = 0, yellow = 0 }) {
  const out = {};
  if (badge(issues) > 0) out.count = badge(issues);
  if (badge(prs) > 0) out.countGreen = badge(prs);
  if (badge(yellow) > 0) out.countYellow = badge(yellow);
  return out;
}

// The fields of the key of one repository. `key` is the repository key, the
// name in lower case with a dot as `-` (task 0089); it is made from `name`
// when it is missing. `issues` is the red count, `prs` the green, `yellow`
// the yellow one. `status` is the session status of the repository, or a list
// of them: the worst one wins.
export function repoNode({ name, key = repoKey(name), issues = 0, prs = 0, yellow = 0, status }, set) {
  return {
    id: `repo.${key}`,
    parent: 'repos',
    status: padStatus(status),
    iconText: clip(name, MAX_CAPTION),
    iconEmoji: [emojiFor(componentInput(key), set).iconRef],
    ...countFields({ issues, prs, yellow }),
  };
}

// The fields of the top key from the repositories it holds, each
// { issues, prs, yellow, status }: the sums of the three counts, and the
// worst status. `outside` lists the statuses of sessions that belong to no
// repository of the list; they colour the key too.
export function topKey(repos, outside = []) {
  const sum = (field) => repos.reduce((total, repo) => total + (repo[field] > 0 ? repo[field] : 0), 0);
  return {
    id: 'repos',
    status: padStatus([...repos.map((repo) => repo.status), ...outside].flat()),
    iconRef: TOP_ICON,
    ...countFields({ issues: sum('issues'), prs: sum('prs'), yellow: sum('yellow') }),
  };
}
