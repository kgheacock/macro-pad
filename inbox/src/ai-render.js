// The rules of ai-inbox for render.js. A message is waiting (yellow), running
// (blue), or done (green). A channel shows the highest status of its unseen
// messages, and the top key the highest of all channels. A seen message does
// not count, so the status is calculated again on every read of the store.
// The pip counts the channels that wait.

import { createRenderer } from './render.js';

export const AI_INBOX_ID = 'ai-inbox';
// A picture under macropadd's assets directory.
export const AI_ICON = 'ai.png';
export const AI_STATUSES = ['waiting', 'running', 'done'];

// Pad status names (driver/layers/doc.go), by rank. Rank 1 is the lowest.
const PAD_STATUS = [null, 'DONE', 'RUNNING', 'WARN'];

// A stored status that is not one of the three counts as done.
export function rank(row) {
  if (row === undefined) return 0;
  if (row.byStatus.waiting) return 3;
  if (row.byStatus.running) return 2;
  return 1;
}

export const AI_RULES = {
  id: AI_INBOX_ID,
  top(unseen) {
    const highest = Math.max(0, ...unseen.map(rank));
    return {
      status: PAD_STATUS[highest] ?? '',
      count: unseen.filter((row) => rank(row) === 3).length,
      iconRef: AI_ICON,
    };
  },
  child(row) {
    return { status: PAD_STATUS[rank(row)] ?? '', count: row?.byStatus.waiting ?? 0 };
  },
  // The highest status first, then the newest unseen message.
  before: (a, b) => rank(b) - rank(a) || b.newestId - a.newestId,
};

export function createAiRenderer({ slot } = {}) {
  return createRenderer({ slot, rules: AI_RULES });
}
