// The picture of one item key of a repository layer (task 0096). An item
// (see repos-items.js) has a yellow id, a green id (the PR number), and a
// session; the key shows the ids as one or two lines over the repository's
// emoji, and the session's emoji only when it has neither id. Pure: the
// renderer of the repos plugin spreads the result into its setLayerKey.

import { componentInput, emojiFor, sessionInput } from '../emoji.js';
import { MAX_CAPTION } from '../render.js';

// The pad status of each state, as driver/layers/doc.go names them.
export const STATUS = {
  ci: { failing: 'ERROR', pending: 'RUNNING', passing: 'DONE', none: '' },
  agent: { waiting: 'WARN', running: 'RUNNING', done: 'DONE' },
};

// From the worst to the mildest. '' is no state.
const WORST_FIRST = ['ERROR', 'WARN', 'RUNNING', 'DONE', ''];

// The strip colour of an item: the worst of its checks and its agent.
export function statusOf({ ci = null, agent = null }) {
  const states = [STATUS.ci[ci] ?? '', STATUS.agent[agent] ?? ''];
  return WORST_FIRST.find((status) => states.includes(status)) ?? '';
}

// Cuts `text` to `limit` runes and keeps its end, because the number tells
// ids apart: 9 head runes, `…`, and the rest as tail (6 runes for 16).
// PLATFORM-INFRA-12345 gives PLATFORM-…-12345.
export function clipTail(text, limit = MAX_CAPTION) {
  const runes = [...text];
  if (runes.length <= limit) return text;
  const head = Math.ceil((limit - 1) * 0.6);
  const tail = limit - 1 - head;
  return `${runes.slice(0, head).join('')}…${runes.slice(runes.length - tail).join('')}`;
}

// The lines of an item: the yellow id, then the green id (#N), with the
// absent ones left out; the session id when it has neither.
export function linesOf(item) {
  const lines = [];
  if (item.yellow) lines.push(String(item.yellow));
  if (item.green !== null && item.green !== undefined) lines.push(`#${item.green}`);
  if (lines.length === 0 && item.session) lines.push(String(item.session));
  if (lines.length === 0) throw new Error(`item ${item.id ?? '?'} has no yellow id, no green id, and no session`);
  return lines;
}

// The only place that knows the wire field of a caption (task 0094): one or
// two lines, each of at most MAX_CAPTION runes. It never sets `iconText`.
export function captionOf(lines) {
  return { iconLines: lines.map((line) => clipTail(line)) };
}

// The fields of the key of `item`, besides `id` and `path`:
// { status, count, iconLines, iconEmoji }. `repo` is the repository key the
// repository key (task 0092) draws its emoji from; `set` is the emoji set.
export function itemKey(item, { repo, set }) {
  const lines = linesOf(item);
  const named = Boolean(item.yellow) || (item.green !== null && item.green !== undefined);
  const input = named ? componentInput(repo) : sessionInput(item.session);
  return {
    status: statusOf(item),
    count: 0,
    ...captionOf(lines),
    iconEmoji: [emojiFor(input, set).iconRef],
  };
}
