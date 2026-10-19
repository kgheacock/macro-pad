// The renderer the daemon uses: one `inbox` key with the total unseen count,
// and one child per channel with unseen messages, at most five. A renderer has
//   plan({ unseen, active }) -> the messages to send macropadd now
//   reset(channels)          -> forget what it sent (called on every connect)
//   target(position)         -> { channel, shownId, here } for the child at that
//                               position (0 to 4), or null
// where `unseen` is Store.unseen(), `active` is the open layer's id or null,
// and `channels` is every channel the store has ever held, so reset can clear
// children an earlier daemon left on the pad.

import { removeLayerKeyMessage, setLayerKeyMessage } from './protocol.js';

// A picture under macropadd's assets directory.
export const INBOX_ICON = 'gmail.png';
export const INBOX_ID = 'inbox';
export const MAX_CHILDREN = 5;

// macropadd takes at most 32 runes of icon text on two lines. A channel is
// letters, digits, - and _ joined by dots, so a long one is cut here to
// leave room for the line break; macropadd cuts what is still too wide.
const MAX_SEGMENT = 15;
const MAX_NAME = 16;

function clip(segment, limit) {
  const runes = [...segment];
  return runes.length <= limit ? segment : `${runes.slice(0, limit - 1).join('')}…`;
}

// The icon text of a channel: its last segment, and the segment before it as
// a line above. `macro-pad.driver` gives "macro-pad\ndriver".
export function channelText(channel) {
  const segments = channel.split('.');
  const name = segments.at(-1);
  if (segments.length === 1) return clip(name, MAX_SEGMENT + 1 + MAX_NAME);
  return `${clip(segments.at(-2), MAX_SEGMENT)}\n${clip(name, MAX_NAME)}`;
}

export function createRenderer({ slot } = {}) {
  // The last values sent per node, as JSON, so only a change is sent again.
  const sent = new Map();
  // The channels macropadd holds as children of `inbox`, in its order.
  let shown = [];
  // False until the children on the pad are known: after a connect, macropadd
  // may hold children this renderer never sent.
  let known = false;
  let leftovers = [];
  // The newest unseen id each child showed when it was last planned. A press
  // marks up to this id, so a message that landed since stays unseen.
  const shownIds = new Map();
  // The "here" of that same message, or null.
  const shownHere = new Map();

  // Adds the node to out when its values differ from the last ones sent.
  const set = (out, key, fields) => {
    const json = JSON.stringify(fields);
    if (sent.get(key) === json) return;
    sent.set(key, json);
    out.push(setLayerKeyMessage(fields));
  };

  const setChild = (out, channel, row) => {
    const count = row?.count ?? 0;
    shownIds.set(channel, row?.newestId ?? 0);
    shownHere.set(channel, row?.here ?? null);
    set(out, channel, {
      id: channel,
      parent: INBOX_ID,
      status: count > 0 ? 'WARN' : '',
      count,
      iconText: channelText(channel),
    });
  };

  const removeChild = (out, channel) => {
    sent.delete(channel);
    out.push(removeLayerKeyMessage(channel, INBOX_ID));
  };

  return {
    reset(channels = []) {
      sent.clear();
      shownIds.clear();
      shownHere.clear();
      shown = [];
      known = false;
      leftovers = channels;
    },

    target(position) {
      const channel = shown[position];
      if (channel === undefined) return null;
      return { channel, shownId: shownIds.get(channel) ?? 0, here: shownHere.get(channel) ?? null };
    },

    plan({ unseen, active }) {
      const out = [];
      const total = unseen.reduce((sum, row) => sum + row.count, 0);
      const key = { id: INBOX_ID, status: total > 0 ? 'WARN' : '', count: total, iconRef: INBOX_ICON };
      if (slot !== undefined) key.slot = slot;
      set(out, INBOX_ID, key);

      const rows = new Map(unseen.map((row) => [row.channel, row]));
      // Newest unseen message first; only the first five show.
      const wanted = [...unseen]
        .sort((a, b) => b.newestId - a.newestId)
        .slice(0, MAX_CHILDREN)
        .map((row) => row.channel);

      if (active === INBOX_ID) {
        // Open: no key moves or goes. A channel at zero stays with count 0.
        for (const channel of shown) setChild(out, channel, rows.get(channel));
        for (const channel of wanted) {
          if (shown.length >= MAX_CHILDREN) break;
          if (shown.includes(channel)) continue;
          shown.push(channel);
          setChild(out, channel, rows.get(channel));
        }
        return out;
      }

      // Closed: nobody sees the children, so remove, reorder, and add.
      if (!known) {
        for (const channel of new Set([...leftovers, ...shown])) removeChild(out, channel);
        shown = [];
        leftovers = [];
        known = true;
      }
      const stay = shown.filter((channel) => wanted.includes(channel));
      for (const channel of shown) {
        if (!stay.includes(channel)) removeChild(out, channel);
      }
      // Children macropadd keeps in a different order than wanted go and come back.
      let same = 0;
      while (same < stay.length && stay[same] === wanted[same]) same++;
      for (const channel of stay.slice(same)) removeChild(out, channel);
      shown = wanted;
      for (const channel of shown) setChild(out, channel, rows.get(channel));
      return out;
    },
  };
}
