---
id: "0036"
title: "Key-state CRUD page: set, reset, and confirm each key's color, emoji, and blink"
status: "ongoing"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/35"
branch: "0036-keystate-crud-page"
related: ["0028", "0029", "0030", "0034", "0037"]
tags: ["driver", "plugin", "ui"]
---

# 0036 — Key-state CRUD page: set, reset, and confirm each key's color, emoji, and blink

## Problem

`driver/plugin` exposes `setKeyState` over WebSocket, but no page lets a
person set every key's color, emoji, and blink, and see the daemon confirm
each change. `virtualpad.html` (task 0029) edits one key at a time and
exists to simulate a press, not to administer state across all six keys.

## Goals

- A person sets any of the 6 keys' color, emoji ID, and blink from a
  browser page, against `macropadd --emulate` or a real board.
- Each key's row shows "pending" after a send, then "confirmed" once the
  daemon's own `setKeyState` rebroadcast for that exact state arrives.
- A person resets a key to a defined default in one click, using a digit
  glyph (`0xF1`-`0xF6`), never the `0x00` placeholder.
- A person sets a key's glyph to an actual Unicode emoji character —
  typed directly, not looked up as a numeric Emoji ID — with the same
  pending/confirmed feedback the color/emoji-ID/blink editor already
  gives.
- A client that connects after the daemon has already confirmed a key's
  state — this page, `virtualpad.html`, or any future plugin — sees that
  key's last known state right away, instead of reading "unknown" until
  it happens to observe the next broadcast.

## Non-goals

- A full historical or on-demand state query. `driver/plugin`'s cache (see
  Design) holds only the single most recently confirmed message per key,
  and replays it exactly once, automatically, right when a client
  connects. It does not answer a client asking again later, and it does
  not keep any history older than the latest message. A page open before
  the daemon last confirmed a key's state, or one that reconnects and
  somehow misses the replay, still reads "unknown" until the next
  broadcast — the same limit `virtualpad.html` already has, just narrowed.
- Persistence across a daemon restart. The cache lives in `plugin.Server`
  only; a freshly started `macropadd` begins with an empty cache, same as
  today.
- An arbitrary image file picker for `setCustomGlyph`. Task 0030's job.
  This task's one narrow use of `setCustomGlyph` — sending a single typed
  emoji character, rendered client-side — is not a general upload UI; the
  cache itself does treat any `setCustomGlyph` message as key state
  regardless of source, since leaving it out would make a key's replayed
  state wrong after a custom glyph was the last thing set for it.
- Plain letters or other non-emoji text as a key's glyph. The browser's
  own font can render a letter as easily as an emoji, but this task
  restricts the new input to characters that test as emoji (see Design),
  matching `macrodriver emoji` (task 0034)'s same scope limit. Rendering
  arbitrary text — a letter included — is task 0037's job
  (`tools/render_html.py`), not this page's.
- Click-pattern or signal handling. Task 0013's job.

## Approaches considered

### Approach A — Extend `virtualpad.html` in place

Add a state table, a reset button, and a pending/confirmed status column to
the existing page, next to its press-simulation tiles.

- Good, because it needs no new file, and reuses the page's working
  WebSocket, log, and RGB565 helper code.
- Good, because a person testing presses and state at once uses one tab,
  one connection.
- Bad, because it mixes two jobs — "simulate a press" and "administer
  state" — on one page, against task 0029's stated scope for this file.
- Bad, because the tile grid and a CRUD table serve different reading
  styles; fitting both crowds the page's layout.

### Approach B — A new, dedicated page

Add `driver/plugin/web/keystate.html`: a table of the 6 keys, each row
with a color, emoji ID, and blink editor, a Set button, a Reset button, and
a status column. It reuses `virtualpad.html`'s connect and RGB565 logic,
copied rather than shared, since the project has no JS build step.

- Good, because `virtualpad.html` keeps its stated single job: simulate a
  press. This page's job is administering state.
- Good, because a table of 6 rows reads clearly as CRUD; the tile grid
  does not have to grow columns for a status field.
- Bad, because a person driving both presses and state opens two tabs
  against the same daemon.
- Bad, because the WebSocket connect boilerplate and RGB565 helpers exist
  twice, with no shared file to update once.

### Approach C — Add a state-snapshot query to `driver/plugin`

Give `plugin.Server` a `[6]*SetKeyStatePayload` cache, updated on every
`setKeyState` it handles. Add a `queryState` message, or send the cache to
each client on connect, so a fresh page shows real current state, not
"unknown".

- Good, because it fixes the same gap for every future plugin author, not
  only this page: today, nothing at all can learn a key's state without
  having watched every broadcast since the daemon started.
- Good, because "confirmed" becomes reliable across a reconnect, too — a
  client that joins mid-session sees the last confirmed state right away.
- Bad, because it changes `driver/plugin/server.go` and `protocol.go`, the
  daemon's most stable and most-documented surface, for a UI request.
- Bad, because the cache must track `setCustomGlyph`'s `0xFE` sentinel
  too, so the query answers the same way a live broadcast would — this
  grows past "add a UI" into a protocol change.

## Decision

Chosen: **Approach B, extended with Approach C's cache**.

The ask started as a simple HTML UI, and task 0029 already fixed
`virtualpad.html`'s scope at press simulation, so a new dedicated page is
still right. But a page cannot show real key state at all without the
daemon remembering it somewhere, so Approach C's core — a per-key cache in
`plugin.Server` — folds into this task instead of staying deferred.

This task's version of C is narrower than the "Approaches considered"
entry above: no new `queryState` message kind, and no cache of
`setCustomGlyph`'s `0xFE` sentinel as a special case. `Server` instead
remembers the exact `Message` (whichever of `setKeyState` or
`setCustomGlyph` came last) it broadcast for each key index, and replays
each stored message, verbatim, to a client right after it connects, before
that client's own send matters. That keeps the wire protocol at its
current message kinds — a client sees the replay exactly the way it
already handles a live broadcast, no new code path to write, and the
sentinel question above answers itself: whatever was actually broadcast
last is what gets replayed. The cost accepted: `driver/plugin/server.go`
changes for what began as a UI-only task, and two pages (`keystate.html`,
`virtualpad.html`) still hand-copy the same WebSocket connect and RGB565
code, per Approach B's own accepted cost.

`keystate.html` also gains a way to type an emoji character directly,
rather than only a raw numeric Emoji ID. `macrodriver emoji` (task 0034)
already proved the wire-level shape — render a character to a 128×128
PNG, send it as `setCustomGlyph` — but that command shells out to
`tools/render_emoji.py`, a Python process a browser page cannot invoke.
A browser already has its own emoji-capable font and a `<canvas>` element
that can rasterize text to a PNG with no server round trip at all, so
`keystate.html` renders the character itself: fill a 128×128 canvas
black (matching `render_emoji.py`'s background), draw the character
centered with a large emoji-covering font stack, and export it with
`canvas.toDataURL`. This needs no change to `driver/plugin` or
`driver/api` — `setCustomGlyph` already accepts any correctly-sized PNG,
from any source. The input is restricted to a single codepoint that
tests positive against JavaScript's `\p{Extended_Pictographic}` Unicode
property — the same "exactly one codepoint" restriction
`validateSingleCodepoint` enforces in `emoji.go`, checked here in the
browser instead of in Go. A plain letter fails that test and is
rejected with a log line, not silently rendered as a blank box the way
`tools/render_emoji.py` renders one today (see Non-goals) — 0037 covers
turning arbitrary text into a glyph, this page does not.

## Design

Files to change:

- `driver/plugin/server.go` — `Server` gains `stateCache map[byte]Message`
  guarded by its own mutex, populated in `readPump`'s `KindSetKeyState` and
  `KindSetCustomGlyph` cases with the exact `Message` just broadcast,
  keyed by that message's `keyIndex`. `addClient` calls a new
  `snapshotTo(c *client)` right after registering `c` and before starting
  its `readPump`/`writePump` goroutines: it sends `c` every cached
  message, in ascending key-index order, over `c.send` — a non-blocking
  send with the same drop-rather-than-block rule `broadcast` already
  follows, so an implausibly large cache (an adversarial or buggy client
  naming hundreds of distinct key indices) cannot stall `addClient`. A key
  never named by a `setKeyState` or `setCustomGlyph` message this server
  has handled is simply absent from the cache and from the replay — a
  client still reads it as unknown until a broadcast names it.
- `driver/plugin/web/keystate.html` — new page. A table of 6 rows (key
  index, color swatch and picker, emoji ID number input, blink checkbox,
  status: unknown/pending/confirmed, Set button, Reset button). Connects
  to the same WebSocket URL as `virtualpad.html`. Sending Set marks the
  row "pending" and records the exact payload sent; a `setKeyState`
  message received for that key clears "pending" to "confirmed" only when
  every field (`color`, `emojiId`, `blink`) matches. A `setKeyState`
  message received for a row that is not "pending" — the connect-time
  replay above, or a change made from another client — instead sets that
  row straight to "confirmed" with the received fields, since it names a
  state the daemon has already confirmed, just not one this page asked
  for. A disconnect while a row is "pending" reverts it to "unknown".
  Reset sends `setKeyState` with `emojiId: 0xF1 + keyIndex` (a digit
  glyph) and `blink: false`.

  Each row also gets an "Emoji char" text input and its own "Set emoji"
  button, independent of the numeric Emoji ID input and the main Set
  button. Clicking "Set emoji" validates the field (exactly one code
  point — `Array.from(value).length === 1` — that matches
  `/\p{Extended_Pictographic}/u`); on failure it logs an error and sends
  nothing. On success it rasterizes the character onto an offscreen
  128×128 canvas and sends the result as one `setCustomGlyph` message,
  marking the row "pending" with the kind and payload it is waiting on —
  the same status column now tracks whichever of `setKeyState` or
  `setCustomGlyph` this row's most recent action was. A `setCustomGlyph`
  broadcast confirms a row exactly like a `setKeyState` broadcast already
  does: an exact match against the awaited payload while "pending"
  clears it to "confirmed"; any `setCustomGlyph` message arriving on a
  row that is not "pending" (the connect-time replay, or another
  client's change) sets that row straight to "confirmed" too, since it
  is not this page's job to distinguish where a confirmed state came
  from. A row confirmed by a custom glyph does not touch that row's
  color/emoji-ID/blink inputs — `setCustomGlyph` carries no such fields
  to show.
- `driver/README.md` — "Plugin API" section's "Protocol" subsection gains
  a paragraph on the connect-time replay, next to the `setKeyState`
  bullet it extends; a new paragraph on `keystate.html` follows the
  existing `virtualpad.html` paragraph and states what Reset sends and
  why (the `0x00` placeholder pitfall), and what the emoji-character
  input sends and why it rejects a plain letter.

## Definition of done

- [ ] **DoD-1** — Setting key 0's color, emoji ID, and blink and clicking
  Set sends one `setKeyState` message with those exact fields. **Proof:**
  manual run against `macropadd --emulate`; the message log shows one
  `setKeyState` frame with the entered values.
  **Not confirmed:** the implementing sandbox kills any process that
  opens a listening TCP socket, so `macropadd` cannot stay up long enough
  here to drive `keystate.html` against it. `sendSet` in
  `driver/plugin/web/keystate.html` builds and sends exactly one
  `setKeyState` message from the row's current field values by
  inspection; a person with a normal shell should run the manual proof
  before trusting this box.
- [ ] **DoD-2** — Key 0's row shows "pending" right after Set, then
  "confirmed" only once a `setKeyState` broadcast matching every sent
  field arrives. **Proof:** manual run; status changes only after the
  matching broadcast, not on send.
  **Not confirmed:** same sandbox limit as DoD-1. `onSetKeyState`'s
  pending/confirmed logic is exercised indirectly by
  `TestServer_SnapshotReplaysEachKeyOnceInAscendingOrder` and friends at
  the server layer, but the client-side state machine itself needs the
  manual run.
- [ ] **DoD-3** — Clicking Reset for key N sends `setKeyState` with
  `emojiId` equal to `0xF1 + N`, never `0`. **Proof:** `grep -n "0xF1"
  driver/plugin/web/keystate.html` shows the constant; manual run shows a
  digit glyph, not a solid white box.
  **Partly confirmed:** the grep half passes — `RESET_EMOJI_BASE = 0xF1`
  at `driver/plugin/web/keystate.html:171`, added to `row.keyIndex` in
  `sendReset`. The manual-run half is blocked by the same sandbox limit.
- [ ] **DoD-4** — Each row updates only from a broadcast naming its own
  `keyIndex`; a broadcast for key 2 leaves key 0's row unchanged. **Proof:**
  manual run; send a `setKeyState` for key 2 from a second client (for
  example `wscat`), confirm only row 2 changes.
  **Not confirmed:** same sandbox limit as DoD-1. `onSetKeyState` indexes
  `rows[payload.keyIndex]` and returns early if that row doesn't exist,
  so by inspection only the named row's DOM is ever touched — but this
  needs the manual run to confirm against a live daemon.
- [ ] **DoD-5** — If the connection closes while a row is "pending", that
  row reverts to "unknown", not "confirmed". **Proof:** manual run; Set a
  key, disconnect before the broadcast arrives, confirm the row reads
  "unknown".
  **Not confirmed:** same sandbox limit as DoD-1. The `close` listener in
  `driver/plugin/web/keystate.html` reverts every "pending" row to
  "unknown" by inspection, but needs the manual run to confirm.
- [x] **DoD-6** — `driver/README.md`'s "Plugin API" section documents
  `keystate.html` and the Reset default. **Proof:** `driver/README.md`,
  "Plugin API" section.
- [ ] **DoD-7** — A client that connects after the daemon has already
  broadcast a `setKeyState` (or `setCustomGlyph`) for key K immediately
  receives that exact message, unprompted, before sending anything
  itself; a key the daemon has never confirmed is left out of the replay
  entirely. **Proof:** `go test ./driver/plugin/...` covers both cases;
  manual run confirms a `keystate.html` page opened after another client
  already set key 0 shows key 0 as "confirmed" immediately, with no Set
  click on this page.
  **Partly confirmed:** `go test ./driver/plugin/...` passes, including
  `TestServer_SnapshotsSetKeyStateToNewClient`,
  `TestServer_SnapshotsSetCustomGlyphToNewClient`, and
  `TestServer_NewClientGetsNoSnapshotForUnknownKey`, which cover the
  server side of both cases named above (`TestServer_SlowClientDoesNotBlockOthers`
  and `TestServer_SlowAudioClientDoesNotBlockOthers` flake under load on
  the unmodified base commit too — pre-existing, unrelated to this task).
  The `keystate.html` half of the proof needs the manual run, blocked by
  the same sandbox limit as DoD-1.
- [x] **DoD-8** — `driver/README.md`'s "Protocol" subsection documents the
  connect-time replay next to the `setKeyState` bullet it extends.
  **Proof:** `driver/README.md`, "Protocol" subsection.
- [x] **DoD-9** — The PR in the `pr` field links to this spec. **Proof:**
  PR body.
- [ ] **DoD-10** — Typing a single emoji character into key 0's "Emoji
  char" field and clicking "Set emoji" sends one `setCustomGlyph` message
  for key 0 whose image decodes as a 128×128 PNG. **Proof:** manual run
  against `macropadd --emulate`; the message log shows one
  `setCustomGlyph` frame for key 0.
- [ ] **DoD-11** — Typing a plain letter into the same field and clicking
  "Set emoji" sends nothing. **Proof:** manual run; the message log gains
  no new frame, and an error line names the rejected input.
- [ ] **DoD-12** — Key 0's row shows "pending" right after "Set emoji",
  then "confirmed" only once a `setCustomGlyph` broadcast for key 0
  arrives matching the exact image sent. **Proof:** manual run; status
  changes only after the matching broadcast, not on send.

## Risks

- No board is wired yet (task 0010), so every proof above runs against
  `--emulate`. Real-hardware behavior stays unverified until wiring lands,
  the same gap task 0029 already carries.
- Two pages now hand-copy the same WebSocket connect and RGB565 code. A
  protocol change must be applied to both files by hand; nothing enforces
  that today.
- The cache is keyed by whatever `keyIndex` a client sends, unbounded by
  the board's real 6-key count. A client naming out-of-range indices grows
  `stateCache` without limit; `snapshotTo`'s drop-rather-than-block guard
  keeps that from stalling `addClient`, but the cache map itself still
  grows. No cap is added here — narrowing this is future work if it ever
  matters in practice.

## Notes

Precedent: `driver/plugin/web/virtualpad.html` (task 0029) already proved
the rebroadcast-as-confirmation pattern this task reuses, and the fixed
Emoji ID table in `docs/wire-protocol.md` rules out `0x00` as a usable
"reset" or "blank" value — see `0x00`'s "Placeholder" entry.

`tools/render_emoji.py` (task 0034) renders a plain letter as a blank
black box, since Apple Color Emoji has no non-emoji glyphs — confirmed by
running it against `"A"` while scoping this addition. That gap stays
unticketed here: `tasks/backlog/0037-render-static-html-on-a-key.md`
already covers turning arbitrary text, letters included, into a glyph via
`tools/render_html.py`, so a person who needs a letter on a key has a
named task to pick up instead of a second, overlapping one.
