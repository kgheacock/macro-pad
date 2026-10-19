// A stable emoji for a folder or a Claude Code session (task 0073). The
// emoji come from assets/emoji/set.txt, which tools/emoji/ draws as PNGs under
// macropadd's assets directory. An input always gets the same emoji: each
// emoji of the set gets a score from SHA-256 of the input and the emoji, and
// the highest score wins (rendezvous hashing). Adding an emoji to the set moves
// only the inputs that now score it highest, about 1 in N.
//
// The inputs are a kind and a value:
//   channel:NAME   a channel; a folder hashes as the channel it gives
//   session:SID    the first 8 characters of a session id, in lower case

import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { UsageError } from './errors.js';

export const SET_FILE = new URL('../../assets/emoji/set.txt', import.meta.url);

// A Claude Code session channel (task 0072) ends in the first 8 characters of
// the session id.
const SESSION_SEGMENT = /^[0-9A-Fa-f]{8}$/;
// A session id as given: at least 8 hex digits, then hex digits and dashes.
const SESSION_ID = /^[0-9A-Fa-f]{8}[0-9A-Fa-f-]*$/;

// Reads the set: [{ codepoints, name }] in the order of the file.
export function parseEmojiSet(text) {
  const set = [];
  for (const raw of text.split('\n')) {
    const line = raw.trim();
    if (line === '' || line.startsWith('#')) continue;
    const [codepoints, name] = line.split(/\s+/);
    set.push({ codepoints, name });
  }
  return set;
}

let cached = null;

export function loadEmojiSet(file = SET_FILE) {
  if (file === SET_FILE && cached) return cached;
  const set = parseEmojiSet(readFileSync(file, 'utf8'));
  if (file === SET_FILE) cached = set;
  return set;
}

export function channelInput(channel) {
  return `channel:${channel}`;
}

// The session input of a session id: its first 8 characters, in lower case.
export function sessionInput(id) {
  if (typeof id !== 'string' || !SESSION_ID.test(id)) {
    throw new UsageError(`bad session id "${id}": give a Claude Code session id, 8 or more hex digits`);
  }
  return `session:${id.slice(0, 8).toLowerCase()}`;
}

// Splits a channel into its folder and session parts. A channel whose last
// segment is 8 hex digits is PROJECT.SID; any other is a folder alone.
export function splitSessionChannel(channel) {
  const dot = channel.lastIndexOf('.');
  const last = channel.slice(dot + 1);
  if (dot > 0 && SESSION_SEGMENT.test(last)) {
    return { folder: channel.slice(0, dot), session: last.toLowerCase() };
  }
  return { folder: channel, session: null };
}

// The text of an emoji. One code point below U+1F000, such as the anchor
// U+2693, gets U+FE0F so that a terminal draws it as an emoji, not as text.
export function emojiChar(codepoints) {
  const points = codepoints.split('-').map((hex) => parseInt(hex, 16));
  if (points.length === 1 && points[0] < 0x1f000) points.push(0xfe0f);
  return String.fromCodePoint(...points);
}

function score(input, codepoints) {
  return createHash('sha256').update(`${input}\n${codepoints}`).digest().readBigUInt64BE(0);
}

// The emoji of an input: { codepoints, name, char, iconRef }. Ties go to the
// earlier line of the set.
export function emojiFor(input, set = loadEmojiSet()) {
  let best = null;
  let bestScore = -1n;
  for (const emoji of set) {
    const s = score(input, emoji.codepoints);
    if (s > bestScore) {
      best = emoji;
      bestScore = s;
    }
  }
  if (best === null) throw new Error('the emoji set is empty');
  return {
    codepoints: best.codepoints,
    name: best.name,
    char: emojiChar(best.codepoints),
    iconRef: `emoji/${best.codepoints}.png`,
  };
}
