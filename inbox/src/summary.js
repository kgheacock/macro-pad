// The renderer the daemon uses until task 0066 adds the channel keys: one
// `inbox` key that shows the total unseen count. A renderer has
//   plan({ unseen, active }) -> the setLayerKey payloads to send now
//   reset()                  -> forget what it sent (called on every connect)
// where `unseen` is Store.unseen() and `active` is the open layer's id or null.

export function createSummaryRenderer({ slot } = {}) {
  let sent = null;
  return {
    reset() {
      sent = null;
    },
    plan({ unseen }) {
      const count = unseen.reduce((sum, row) => sum + row.count, 0);
      const key = { id: 'inbox', status: count > 0 ? 'WARN' : '', count };
      if (slot !== undefined) key.slot = slot;
      const json = JSON.stringify(key);
      if (json === sent) return [];
      sent = json;
      return [key];
    },
  };
}
