// The ai plugin: the state of AI agents. A message is waiting (yellow),
// running (blue), or done (green). A channel shows the highest status of its
// unseen messages, and the top key the highest of all channels. A seen
// message does not count, so the status is calculated again on every read of
// the store. The pip counts the channels that wait.
//
// Which channels show does not depend on what is unseen: a channel with a
// message in the last hour shows, newest first, also when all is seen.

export const AI_STATUSES = ['waiting', 'running', 'done'];

// Pad status names (driver/layers/doc.go), by rank. Rank 1 is the lowest.
const PAD_STATUS = [null, 'DONE', 'RUNNING', 'WARN'];

// How long a channel stays on the pad after its last message.
export const ACTIVE_MS = 60 * 60 * 1000;

// A stored status that is not one of the three counts as done. A row with no
// unseen message has rank 0.
export function rank(row) {
  if (row === undefined || row.count === 0) return 0;
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
waiting, running, done. It stays on the pad for an hour after its last
message, newest first, also when all is seen.
The pip on the key counts the channels that wait.`,
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
    // A channel shows while its newest message is younger than this.
    activeMs: ACTIVE_MS,
    // The newest message first.
    before: (a, b) => b.createdAt - a.createdAt || b.newestId - a.newestId,
    // A Claude Code session reports to PROJECT.SID (task 0072), so its key
    // shows the folder's emoji and the session's (task 0073).
    sessions: true,
  },
};
