---
id: "0078"
title: "Page a child layer past five children, with arrow keys"
status: "backlog"
created: "2026-10-23"
updated: "2026-10-23"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-lc87yi"
related: ["0055", "0056", "0057", "0061", "0066", "0067", "0076"]
tags: ["layers", "driver", "protocol", "inbox"]
---

# 0078 — Page a child layer past five children, with arrow keys

## Problem

A key holds at most five children (task 0057, `layers.MaxChildren`). A
`setLayerKey` for a sixth child is rejected, so every source of child keys
cuts its own list at five. The inbox does it in `inbox/src/render.js`
(`MAX_CHILDREN`), and so does every inbox plugin through it, the github
plugin of task 0076 included. A sixth channel, or a sixth open PR, cannot be
reached from the pad. The window switcher of task 0056 will hit the same wall
with a sixth window.

Keith asked on 2026-10-23: "When children crosses 5, key 6 will be a right
arrow and when clicked show page 2. Page 2 will have a left arrow at key 4
and a right (as needed) at 6." He wants it "across all apps".

## Key numbers

Keith counts keys from 1. The code counts from 0. This spec uses Keith's
numbers, with the code's index after a slash: key 4/3 is the bottom-left key,
the up key today, and key 6/5 is the bottom-right key.

| Keith | Code | Child layer today |
|---|---|---|
| keys 1, 2, 3 | 0, 1, 2 | children 1 to 3 |
| key 4 | 3 | up (shift arrow), back to the top layer |
| keys 5, 6 | 4, 5 | children 4 and 5 |

## Goals

- A key holds more than five children. The cap rises to 20.
- With five children or fewer, a child layer looks and acts as it does
  today.
- With six or more, the layer has pages. Page 1 keeps the up key at key
  4/3 and shows a right arrow at key 6/5. Every later page shows a left
  arrow at key 4/3. Every page but the last shows a right arrow at key 6/5.
- A press on the right arrow shows the next page. A press on the left arrow
  shows the page before. The up key on page 1 returns to the top layer.
- Paging lives in `macropadd`'s layer controller, so every client that
  sends children gets it with no paging code of its own.
- A client learns which child was pressed by its id, not by key index, so
  no client needs to know the page layout.
- The page survives a `macropadd` restart, as `active` does.

## Non-goals

- Paging the top layer: more than six top-level keys. Task 0055's `pager`
  plugin (PR #65, not on main) covers that inside the plugin host. See
  "The pager of task 0055" below.
- Grandchildren. A child still has no children.
- A page number on the pad, such as "2/3" on an arrow. See Open questions.
- Swipes, long presses, or double presses to jump pages. One press moves
  one page.
- Firmware changes. Arrows are ordinary glyphs.

## Layout

Six keys hold either a child or a control. On page 1 the up key and the
right arrow take two, so page 1 holds four children. A middle page has the
left and the right arrow, so it holds four. The last page has only the left
arrow, so it holds five.

| Page | 1/0 | 2/1 | 3/2 | 4/3 | 5/4 | 6/5 |
|---|---|---|---|---|---|---|
| only page (≤ 5 children) | child | child | child | up | child | child |
| first of several | child | child | child | up | child | right |
| middle | child | child | child | left | child | right |
| last | child | child | child | left | child | child |

Children fill each page in the order keys 1, 2, 3, 5, then 6 on the last
page, the order a child layer uses today. A key with no child on the last
page is blank.

| Children | Pages | Children per page |
|---|---|---|
| 1 to 5 | 1 | as today |
| 6 to 9 | 2 | 4, then 2 to 5 |
| 10 to 13 | 3 | 4, 4, then 2 to 5 |
| 14 to 17 | 4 | 4, 4, 4, then 2 to 5 |
| 18 to 20 | 5 | 4, 4, 4, 4, then 2 to 4 |

The sixth child does not push one child to page 2 alone: with six children,
page 1 shows four and page 2 shows two. Page 1 has no room for a fifth
child once key 6/5 is the arrow.

## Approaches considered

### Approach A — Pages in the layer controller

`macropadd` keeps the page beside `active`, draws the arrows, and handles
their presses. A press event on a child names the child's id.

- Pro: one implementation; inbox, github, and any later client get it free.
- Pro: the arrows and the page are drawn from the same state as the
  children, so they never disagree.
- Con: a protocol change. Clients that map key index to child must switch
  to the child id. Today that is only the inbox daemon.

### Approach B — Each client pages its own children

A client keeps a page number, sends five children for the page it wants,
and draws its own arrow children.

- Pro: no driver change.
- Con: every client writes the same paging code, and it is not "across all
  apps" until each one does.
- Con: the arrows are children, so they count in the cap and the client
  cannot tell an arrow press from a child press without its own table.
- Con: a page flip is a full rewrite of five children over the socket,
  slower than a local redraw.

### Approach C — A paging plugin in the plugin host (task 0055)

A host plugin owns the arrows and asks its sibling plugins for the next
page.

- Pro: fits the plugin host's model of one plugin per job.
- Con: the inbox is a Node client, not a host plugin, so it gets nothing.
- Con: waits on task 0055, which is not on main.

## Decision

**Approach A.** It is the only one that pages every client without each
one writing it, and it keeps the arrows in the same object as the
children. The cost is one protocol addition (`child` on a press event) and
one client change (the inbox daemon), both small.

## Design

### Layer object

- `MaxChildren` rises from 5 to 20. `Validate` keeps the cap and its error.
- `Doc` gains `Page int` (`json:"page"`), zero-based, the shown page of the
  active layer. It is 0 when `active` is null. `Validate` rejects a page
  outside the active layer's page count. A file without `page` reads as 0.
- `pageCount(n)` gives 1 for n ≤ 5, else `ceil((n - 1) / 4)`: p pages hold
  4(p - 1) + 5 children. One function, `pageLayout(n, page)`, returns which
  child shows on each key and which keys are controls; `desired`, `Press`,
  and the preload all use it.

### Presses

| Press | Result |
|---|---|
| top layer, key with children | opens its child layer on page 1 |
| page 1, key 4/3 (up) | returns to the top layer, page reset |
| later page, key 4/3 (left) | shows the page before |
| any page but the last, key 6/5 (right) | shows the next page |
| a child | nothing in the layer; clients see the press with `child` |
| a blank key | nothing |

Opening a layer always starts on page 1. Closing it resets `page` to 0.

### Arrows

`assets/left.png` and `assets/right.png`, drawn like `assets/shift.png`: a
black arrow on a white round, on the same dark key color (`ColorUp`). A new
view kind, `kindArrow`, draws them. They are frames in the cache like the
shift icon.

### Removing and adding children while a layer is open

- A removal that leaves the shown page past the last page moves to the new
  last page. A removal that leaves five children or fewer shows the single
  page with no arrows.
- An added child goes at the end, as today, so it never moves a child the
  person sees. It may turn the last page's key 6/5 from a child into a right
  arrow, and push that child to a new last page. That is the one case where
  a visible key changes under a finger; the inbox avoids it by not adding
  children while its layer is open (task 0066's stable rule).

### Protocol

- A press `event` gains `child` and `parent` when the key showed a child:
  `{"kind":"event","type":"press","keyIndex":4,"child":"macro-pad.driver","parent":"inbox"}`.
  The daemon fills them from the layout before it handles the press, so they
  name what the person saw. A press on an arrow or the up key carries
  neither. `keyIndex` stays, so clients that only want raw keys are
  unaffected.
- `layerChanged` gains `page` and `pages`, for clients that want to show or
  log them. No client needs them.

### Inbox

- `MAX_CHILDREN` in `inbox/src/render.js` follows the driver's cap, 20.
  Task 0066's "five newest" becomes "twenty newest"; its stable-while-open
  rule is unchanged.
- `onPress` in `inbox/src/daemon.js` uses `message.child` and drops
  `CHILD_KEYS` and `UP_KEY`. `renderer.target(position)` becomes
  `renderer.target(channel)`.
- Every inbox plugin (basic, ai, github) gets pages through these two
  changes, with no plugin change.

### Frame cache

The table holds 52 frames (task 0061). Today the preload stores every node,
at most 6 + 30 frames. With 20 children per key it can be 126, more than fit.
The preload stores in this order: the top layer, page 1 of each key, then the
later pages, and stops when the table is full, as it does now (it evicts
nothing). A later page that did not fit sends its images on the first flip,
about 155 ms per key, then stays cached.

### The pager of task 0055

The `pager` plugin in PR #65 flips the plugin host's own pages: which
plugins own the six top-level keys. It works on the top layer and never
sees a child layer, so it does not overlap with this task. Two notes for
when 0055 lands:

- The 0055 spec's Non-goals still say "Pages, a page switcher" while PR #65
  ships `pager`. One of them should change.
- A host plugin that sends children gets pages from this task, so the host
  needs no paging helper.

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — `pageCount` gives 1, 1, 2, 2, 3, 5 for 1, 5, 6, 9, 10, 20
  children, and `pageLayout` matches the Layout table for 6, 9, 10, and 20
  children. **Proof:** `cd driver && go test ./layers -run TestPageLayout`
- [ ] **DoD-2** — With five children, a child layer draws and acts exactly
  as before: the existing layer tests pass unchanged. **Proof:** `go test ./layers`
- [ ] **DoD-3** — With seven children: opening shows children 1 to 4 on keys
  1, 2, 3, 5/0, 1, 2, 4, up on key 4/3, right on key 6/5. Right shows children
  5 to 7 on keys 1, 2, 3/0, 1, 2 with left on key 4/3 and key 5/4 and 6/5
  blank. Left returns to page 1. Up returns to the top layer. **Proof:** `go test ./layers -run TestPaging`
- [ ] **DoD-4** — Removing children while on page 3 of 3 moves to page 2
  when page 3 empties, and to the single page with no arrows at five
  children. **Proof:** `go test ./layers -run TestPagingRemove`
- [ ] **DoD-5** — `page` is saved and read back; a file with a page outside
  the active layer's pages is moved to `.bad`; a file without `page` reads
  as page 0. **Proof:** `go test ./layers -run TestPageSaved`
- [ ] **DoD-6** — A press on a child carries `child` and `parent` naming the
  child shown on that page; a press on an arrow or up carries neither.
  **Proof:** `go test ./plugin -run TestPressNamesChild`
- [ ] **DoD-7** — Seven channels with news give seven `inbox` children, and
  a press on the channel shown on page 2 marks that channel seen. **Proof:** `cd inbox && node --test`
- [ ] **DoD-8** — The preload stores the top layer and every page 1 before
  any later page. **Proof:** `go test ./layers -run TestPreloadOrder`
- [ ] **DoD-9** — `driver/README.md` "Layers" and "Key layout" describe
  pages, the arrows, and the press fields. **Proof:** the README diff
- [ ] **DoD-10** — `cd driver && go build ./... && go vet ./... && go test ./...`
  and `cd inbox && npm test` pass. **Proof:** the exit codes
- [ ] **DoD-11** — On the board, with seven inbox channels, the Inbox layer
  shows a right arrow on key 6, page 2 shows the other three channels with a
  left arrow on key 4, and a press on a page 2 channel goes where it was sent.
  **Proof:** Keith's check on the board, recorded in Notes

## Risks

- A client that maps key index to child, other than the inbox, picks the
  wrong child on page 2 → only the inbox does it on main today; the README
  says to use `child`.
- An added child flips the last page's key 6/5 from a child to an arrow
  under a finger → the inbox does not add children while open; other
  clients are warned in the README.
- More frames than the cache holds → later pages send images on their
  first flip, about 155 ms per key.
- A layers.json written by this version is rejected by an older
  `macropadd` (unknown cap, `page` ignored) → Keith runs the latest; no
  backward compatibility, as in task 0071.

## Open questions

- [x] Is key 4 the up key's slot? This spec reads Keith's "key 4" as the
  bottom-left key, where the up key sits today, so page 1 holds four
  children, not five, once there is a right arrow. — kgheacock
  - Yes (Keith, 2026-10-23): exactly 5 children show on one page with no
    arrows; at 6, page 1 holds 4 and the last page holds 2.
- [ ] Should an arrow take the status color of the worst child on the pages
  it leads to, so an ERROR on page 3 shows from page 1? The default is no: a
  plain arrow. — kgheacock
- [ ] Is 20 the right cap? The default is 20, five pages. — kgheacock
- [ ] Should an arrow show the page, such as "2/3"? The default is no. —
  kgheacock

## Notes

Keith asked for paging "across all apps" on 2026-10-23. Not started; the
spec waits for Keith's okay.

Keith confirmed the layout on 2026-10-23: "If child length is exactly 5 the
first page should show 5 (no pagination needed). If it increases to 6, the
first page will be 4 children (full) and the last page will be 2." This is
the Layout table as written.
