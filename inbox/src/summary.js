// The renderer the daemon uses until task 0066 adds the channel keys: one
// `inbox` key that shows the total unseen count, on the Gmail envelope.
// The key has no status, so it is black, and a count of 0 shows no pip. A renderer has
//   plan({ unseen, active }) -> the setLayerKey payloads to send now
//   reset()                  -> forget what it sent (called on every connect)
// where `unseen` is Store.unseen() and `active` is the open layer's id or null.

// A picture under the assets directory (see `--assets-dir`).
export const INBOX_ICON = 'gmail.png';

export function createSummaryRenderer({ slot } = {}) {
  let sent = null;
  return {
    reset() {
      sent = null;
    },
    plan({ unseen }) {
      const count = unseen.reduce((sum, row) => sum + row.count, 0);
      const key = { id: 'inbox', status: '', count, iconRef: INBOX_ICON };
      if (slot !== undefined) key.slot = slot;
      const json = JSON.stringify(key);
      if (json === sent) return [];
      sent = json;
      return [key];
    },
  };
}
