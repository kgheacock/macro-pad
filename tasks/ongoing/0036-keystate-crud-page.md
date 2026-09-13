---
id: "0036"
title: "Key-state CRUD page: set, reset, and confirm each key's color, emoji, and blink"
status: "ongoing"
created: "2026-09-13"
updated: "2026-09-13"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "0036-keystate-crud-page"
related: ["0028", "0029"]
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

## Non-goals

- A true state query on load. `driver/plugin` has no snapshot of last-known
  state today, and this task does not add one. A freshly opened or
  reconnected page shows "unknown" until it observes a broadcast, the same
  limit `virtualpad.html` already has.
- Custom glyph upload (`setCustomGlyph`). Task 0030's job.
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

Chosen: **Approach B — a new, dedicated page**.

The ask is a simple HTML UI, and task 0029 already fixed `virtualpad.html`'s
scope at press simulation. Approach C fixes a real gap but changes the
daemon's protocol for a UI-only request; that cost is not justified here.
The cost accepted: two pages, and no true state read on load.

## Design

Files to change:

- `driver/plugin/web/keystate.html` — new page. A table of 6 rows (key
  index, color swatch and picker, emoji ID number input, blink checkbox,
  status: unknown/pending/confirmed, Set button, Reset button). Connects
  to the same WebSocket URL as `virtualpad.html`. Sending Set marks the
  row "pending" and records the exact payload sent; a `setKeyState`
  message received for that key clears "pending" to "confirmed" only when
  every field (`color`, `emojiId`, `blink`) matches. A disconnect while a
  row is "pending" reverts it to "unknown". Reset sends `setKeyState` with
  `emojiId: 0xF1 + keyIndex` (a digit glyph) and `blink: false`.
- `driver/README.md` — "Plugin API" section gains a paragraph on
  `keystate.html`, next to the existing `virtualpad.html` paragraph, and
  states what Reset sends and why (the `0x00` placeholder pitfall).

## Definition of done

- [ ] **DoD-1** — Setting key 0's color, emoji ID, and blink and clicking
  Set sends one `setKeyState` message with those exact fields. **Proof:**
  manual run against `macropadd --emulate`; the message log shows one
  `setKeyState` frame with the entered values.
- [ ] **DoD-2** — Key 0's row shows "pending" right after Set, then
  "confirmed" only once a `setKeyState` broadcast matching every sent
  field arrives. **Proof:** manual run; status changes only after the
  matching broadcast, not on send.
- [ ] **DoD-3** — Clicking Reset for key N sends `setKeyState` with
  `emojiId` equal to `0xF1 + N`, never `0`. **Proof:** `grep -n "0xF1"
  driver/plugin/web/keystate.html` shows the constant; manual run shows a
  digit glyph, not a solid white box.
- [ ] **DoD-4** — Each row updates only from a broadcast naming its own
  `keyIndex`; a broadcast for key 2 leaves key 0's row unchanged. **Proof:**
  manual run; send a `setKeyState` for key 2 from a second client (for
  example `wscat`), confirm only row 2 changes.
- [ ] **DoD-5** — If the connection closes while a row is "pending", that
  row reverts to "unknown", not "confirmed". **Proof:** manual run; Set a
  key, disconnect before the broadcast arrives, confirm the row reads
  "unknown".
- [ ] **DoD-6** — `driver/README.md`'s "Plugin API" section documents
  `keystate.html` and the Reset default. **Proof:** `driver/README.md`,
  "Plugin API" section.
- [ ] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:**
  PR body.

## Risks

- No board is wired yet (task 0010), so every proof above runs against
  `--emulate`. Real-hardware behavior stays unverified until wiring lands,
  the same gap task 0029 already carries.
- Two pages now hand-copy the same WebSocket connect and RGB565 code. A
  protocol change must be applied to both files by hand; nothing enforces
  that today.

## Notes

Precedent: `driver/plugin/web/virtualpad.html` (task 0029) already proved
the rebroadcast-as-confirmation pattern this task reuses, and the fixed
Emoji ID table in `docs/wire-protocol.md` rules out `0x00` as a usable
"reset" or "blank" value — see `0x00`'s "Placeholder" entry.
