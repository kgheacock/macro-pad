// The ai plugin: the state of AI agents. A message is waiting (yellow),
// running (blue), or done (green). A channel shows the highest status of its
// unseen messages, and the top key the highest of all channels. A seen
// message does not count, so the status is calculated again on every read of
// the store. The pip counts the channels that wait.

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

export default {
  name: 'ai',
  summary: 'the state of AI agents: waiting, running, done',
  statuses: AI_STATUSES,
  defaultStatus: 'done',
  about: `A channel shows the highest status of its unseen messages, in the order
waiting, running, done. The pip on the key counts the channels that wait.`,
  rules: {
    // The key id of the pad's layers.json; it predates the plugin name.
    id: 'ai-inbox',
    top(unseen) {
      const highest = Math.max(0, ...unseen.map(rank));
      return {
        status: PAD_STATUS[highest] ?? '',
        count: unseen.filter((row) => rank(row) === 3).length,
        // A picture under macropadd's assets directory.
        iconRef: 'ai.png',
      };
    },
    child(row) {
      return { status: PAD_STATUS[rank(row)] ?? '', count: row?.byStatus.waiting ?? 0 };
    },
    // The highest status first, then the newest unseen message.
    before: (a, b) => rank(b) - rank(a) || b.newestId - a.newestId,
  },
};
