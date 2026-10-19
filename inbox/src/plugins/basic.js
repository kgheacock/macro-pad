// The basic plugin, the default: messages counted per channel. The top key
// shows the total unseen count, yellow above 0, and the newest channels show
// first.

export default {
  name: 'basic',
  summary: 'unseen messages counted per channel',
  statuses: null,
  defaultStatus: 'info',
  about: null,
  rules: {
    id: 'inbox',
    top(unseen) {
      const total = unseen.reduce((sum, row) => sum + row.count, 0);
      // A picture under macropadd's assets directory.
      return { status: total > 0 ? 'WARN' : '', count: total, iconRef: 'gmail.png' };
    },
    child(row) {
      const count = row?.count ?? 0;
      return { status: count > 0 ? 'WARN' : '', count };
    },
    // Newest unseen message first.
    before: (a, b) => b.newestId - a.newestId,
  },
};
