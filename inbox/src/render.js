// The renderer the daemon uses: one top key, and one child per channel with
// unseen messages, at most MAX_CHILDREN; macropadd pages past five (task
// 0078). What the keys show comes from the rules of a
// plugin (see plugins/index.js). A renderer has
//   topId                    -> the id of the top key
//   plan({ unseen, active }) -> the messages to send macropadd now
//   reset(channels)          -> forget what it sent (called on every connect)
//   target(channel)          -> { channel, shownId, here } for that child, if
//                               the pad shows it, or null
// where `unseen` is Store.unseen(), `active` is the open layer's id or null,
// and `channels` is every channel the store has ever held, so reset can clear
// children an earlier daemon left on the pad.

import { componentInput, componentOf, emojiFor, loadEmojiSet, splitSessionChannel } from './emoji.js';
import { removeLayerKeyMessage, setLayerKeyMessage } from './protocol.js';

// macropadd's layers.MaxChildren.
export const MAX_CHILDREN = 20;

// macropadd takes a caption of at most 16 runes over emoji, and cuts what is
// still too wide.
export const MAX_CAPTION = 16;

export function clip(segment, limit) {
  const runes = [...segment];
  return runes.length <= limit ? segment : `${runes.slice(0, limit - 1).join('')}…`;
}

// The icon of a channel (task 0073): the last segment of its folder as a
// caption, over the emoji of the folder's repository (task 0081) and, for a
// session channel, the session's emoji. With `sessions`, a channel
// PROJECT.SID (task 0072) is the folder PROJECT and the session SID;
// otherwise the whole channel is the folder. `macro-pad.driver.3f2a91c0`
// gives "driver" over the emoji of repo:macro-pad and session:3f2a91c0.
export function channelIcon(channel, { sessions = false, set = loadEmojiSet() } = {}) {
  const { folder, session } = sessions ? splitSessionChannel(channel) : { folder: channel, session: null };
  const emoji = [emojiFor(componentInput(componentOf(folder)), set).iconRef];
  if (session !== null) emoji.push(emojiFor(`session:${session}`, set).iconRef);
  return { iconText: clip(folder.split('.').at(-1), MAX_CAPTION), iconEmoji: emoji };
}

// `rules` is a plugin's rules: what the top key and the children show.
// `unseen` is Store.unseen().
//   id               the id of the top key
//   top(unseen)      the fields of the top key, besides id and slot
//   child(row)       the fields of a child, besides id and parent; row is
//                    undefined for a channel that has no unseen message now
//   before(a, b)     orders two rows; the first MAX_CHILDREN show
//   sessions         true when a channel may be PROJECT.SID (see channelIcon)
//   shows(row)       optional; a row it returns false for is left out of the
//                    top key and the children
//   icon(channel, set, row) optional; { iconText, iconEmoji } instead of
//                    channelIcon. row may be undefined; its `session` is
//                    the session of the newest message (task 0081)
// `emojiSet` is the emoji set, for a test; by default assets/emoji/set.txt.
export function createRenderer({ slot, rules, emojiSet }) {
  // The icon of each channel and session, worked out once.
  const icons = new Map();
  const iconOf = (channel, row) => {
    const key = `${channel}\n${row?.session ?? ''}`;
    if (!icons.has(key)) {
      icons.set(
        key,
        rules.icon
          ? rules.icon(channel, emojiSet, row)
          : channelIcon(channel, { sessions: rules.sessions === true, set: emojiSet }),
      );
    }
    return icons.get(key);
  };
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
    shownIds.set(channel, row?.newestId ?? 0);
    shownHere.set(channel, row?.here ?? null);
    set(out, channel, {
      id: channel,
      parent: rules.id,
      ...rules.child(row),
      ...iconOf(channel, row),
    });
  };

  const removeChild = (out, channel) => {
    sent.delete(channel);
    out.push(removeLayerKeyMessage(channel, rules.id));
  };

  return {
    topId: rules.id,

    reset(channels = []) {
      sent.clear();
      shownIds.clear();
      shownHere.clear();
      shown = [];
      known = false;
      leftovers = channels;
    },

    target(channel) {
      if (!shown.includes(channel)) return null;
      return { channel, shownId: shownIds.get(channel) ?? 0, here: shownHere.get(channel) ?? null };
    },

    plan({ unseen: all, active }) {
      const unseen = rules.shows ? all.filter(rules.shows) : all;
      const out = [];
      const key = { id: rules.id, ...rules.top(unseen) };
      if (slot !== undefined) key.slot = slot;
      set(out, rules.id, key);

      const rows = new Map(unseen.map((row) => [row.channel, row]));
      // Only the first MAX_CHILDREN show.
      const wanted = [...unseen]
        .sort(rules.before)
        .slice(0, MAX_CHILDREN)
        .map((row) => row.channel);

      if (active === rules.id) {
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
