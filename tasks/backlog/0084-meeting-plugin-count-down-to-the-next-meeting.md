---
id: "0084"
title: "The meeting inbox plugin: a key that counts down to the next meeting, and a press joins it"
status: "backlog"
created: "2026-10-27"
updated: "2026-10-27"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-ez2284"
related: ["0064", "0068", "0069", "0073", "0075", "0076", "0079", "0082"]
tags: ["inbox", "plugin", "calendar", "meeting", "teams"]
---

# 0084 — The meeting inbox plugin

## Problem

Keith's meetings are in Outlook, and the Teams link to join each one is in the
meeting itself. To know how long he has until the next one, or to join it, he
has to switch to Outlook or Teams, find the meeting, and click its link. A
meeting that starts while he is focused on something else is easy to miss by
a few minutes.

Keith already publishes his Outlook calendar as an ICS link (Outlook on the
web, "Shared calendars", "Publish a calendar"). The one inbox daemon (task
0075) already runs plugins that feed themselves on a timer (the `github`
plugin, task 0076). A key that reads that link, counts down to the next
meeting, and joins it on a press fits the same shape.

## Goals

- A plugin `meeting` (`inbox --plugin meeting`). Its key shows the alarm
  clock emoji and a caption with the time to the next meeting and its title,
  such as `14m Standup`.
- The key's colour changes as the meeting gets close: plain, then amber 5
  minutes before, then red 1 minute before and for the first 5 minutes of the
  meeting.
- A press on the key joins the next meeting: Teams opens in the app, not in a
  browser tab. Zoom and Google Meet links open in the browser.
- The calendar is read from Keith's published ICS link every 5 minutes. The
  link is a secret: it is never stored in the repository, in the store, or in a
  log.
- No new command, service, or npm dependency. The one daemon of task 0075
  shows it with the other plugins.

## Non-goals

- A long press, or children that list the rest of today's meetings. Keith left
  these out on 2026-10-27.
- A view during a meeting, such as the minutes left. Five minutes after a
  meeting starts, the key moves on to the next one.
- Teams controls (mute, camera, leave) through the Teams third-party device
  API. That is a later task.
- Microsoft Graph, macOS Calendar (EventKit), or AppleScript as sources. Keith
  has the ICS link, which needs no login and no permission.
- Accepting, declining, or editing meetings. The key only reads.
- Every corner of RFC 5545. The feed reads what Outlook publishes (see "The
  ICS reader"); an event it cannot read is left out and logged.

## Approaches considered

### Approach A — A feed in the daemon writes one message per meeting (chosen)

As for `github` (task 0076), the plugin has a `feed` that the daemon runs
every 5 minutes. It fetches the ICS link, works out the meetings of the next
24 hours, and writes a message when a meeting appears, changes, or is
cancelled. The key draws the next meeting from the store.

- Good, because it is the pattern of task 0076: the one service already runs
  it, and a failure stays in the meeting loop.
- Good, because the store keeps the meetings, so the countdown goes on when
  the network is down or the daemon restarts.
- Good, because `inbox --plugin meeting list` shows what the key knows, which
  helps when a meeting is missing.
- Bad, because a row needs the start time of its meeting, which the store has
  no column for today. This task adds one (`at`, schema 6).

### Approach B — A feed in the daemon keeps the meetings in memory

The feed parses the ICS link and hands the meetings straight to the key,
without the store.

- Good, because no schema change is needed.
- Bad, because a daemon restart while offline shows nothing until the network
  is back.
- Bad, because the rules of a plugin read rows from the store today; the
  key would need a second way to get its data, used by this plugin only.

### Approach C — A script outside the daemon sends the meetings

A script fetches the link and runs `inbox --plugin meeting send` for each
meeting, from cron or a login service.

- Good, because the daemon makes no network call.
- Bad, because it needs a second service or cron, which task 0075 removed, and
  it must remember what it sent between runs.

## Decision

Chosen: **Approach A — a feed in the daemon writes one message per meeting**.
It reuses the feed of task 0076, keeps working offline, and leaves a record to
check. The cost is a new store column (`at`), a hand-written ICS reader (the
inbox has no npm dependencies), and two small additions to the daemon: a
redraw timer and a press on a top key that has no children.

## Design

### The plugin (`inbox/src/plugins/meeting.js`)

| Field | Value |
|---|---|
| `name` | `meeting` |
| `summary` | counts down to the next meeting; a press joins it |
| `statuses` | `upcoming`, `cancelled` |
| `defaultStatus` | `upcoming` |
| top key id | `meeting` |

- **Channel**: one per meeting occurrence, `meet.START.UID8`, where `START` is
  the start in UTC as `YYYYMMDDtHHMMz` and `UID8` is the first 8 hex digits of
  SHA-256 of the event's `UID`. For example `meet.20261027t1630z.3fa9c2d1`.
- **Body**: the meeting title, cut to 200 characters. **At**: the start, in
  ms since 1970. **Here**: the join link (below), or none.
- **Shows**: a row whose status is `upcoming` and whose start is less than 5
  minutes ago. `activeMs` is 36 hours.
- **Children**: none. The top key is the only key.

### What the key shows

The next meeting is the shown row with the earliest start. The key's fields:

| Time to the start | `status` | Caption |
|---|---|---|
| no meeting in the next 24 h | none | `free` |
| more than 60 min | none | the start time, `HH:MM` (24-hour clock), then the title |
| 6 to 60 min | none | `14m`, then the title |
| 2 to 5 min | `WARN` (amber) | `4m`, then the title |
| 1 min or less, or started up to 5 min ago | `ERROR` (red) | `now`, then the title |
| no calendar link set | none | `no calendar` |

- `iconEmoji` is `["emoji/23f0.png"]` (⏰, already in the set), and the caption
  is `iconText`, one line cut to 16 runes, as every caption (task 0073). The
  key colour shows as the strip under the emoji (task 0082).
- The countdown rounds up: 13 min 10 s shows `14m`.
- `count` is 0: the key has no pip.
- The time is the Mac's local time; the ICS reader turns every start into UTC.

### Redraw

The caption changes every minute without a write to the store. A plugin's
rules may set `redrawMs`. The daemon then plans and sends the keys every
`redrawMs` as well as on a store change. The renderer sends a key only when
its fields changed, so a redraw with nothing new sends nothing. `meeting` sets
`redrawMs` to 15 000.

### A press

The top key has no children, so macropadd sends no `childPressed` for it; it
sends the raw `event` of the key (`{"type":"press","keyIndex":N}`). A plugin's
rules may have `press(rows, now)`, which returns the "here" to go to, or null.
The daemon calls it on a press event when its key is at that index: no layer is
open (`active` is null) and `keyIndex` equals the plugin's slot. It then goes
to that "here" as a child press does (task 0068).

- `meeting` returns the "here" of the next meeting, or null when it has none,
  or when it starts more than 15 minutes from now. That stops a stray press
  from joining a meeting that starts hours later.
- The key needs a slot: `install.sh --slot meeting=N`. Without one the daemon
  cannot know which key is its own, so the key draws but a press does nothing.
  The daemon logs that once at start: `meeting: a press needs --slot meeting=N`.
- A press marks nothing seen.

### The join link

The feed looks for a link in this order, and takes the first it finds:

1. `X-MICROSOFT-SKYPETEAMSMEETINGURL`;
2. `URL`;
3. `LOCATION`, then `DESCRIPTION`, scanned for a Teams link
   (`https://teams.microsoft.com/l/meetup-join/…`), a Zoom link
   (`https://*.zoom.us/j/…`), or a Google Meet link
   (`https://meet.google.com/…`).

A Teams link becomes `url:msteams:/l/meetup-join/…`, with the same path and
query, so it opens the Teams app. A `url:https:` here would go to a browser tab
(task 0079). Zoom and Meet links stay `url:https:…`. A link that `parseHere`
refuses is dropped, and the meeting has no "here".

### The feed

The plugin's `feed` is `{ source: 'the calendar', everyMs: 300000, fetch({
env }), sync(store, answer) }`, run by the daemon as in task 0076.

1. **The link.** `$INBOX_CALENDAR_URL` when set. Otherwise, on macOS,
   `security find-generic-password -s macropad-calendar -a ics -w`, run through
   `execFile`. It must be `https:`. No link at all writes nothing, and the key
   shows `no calendar`. That is not logged as a failure.
2. **Fetch.** `fetch` with a 30 s timeout, at most 5 MB. A non-2xx answer, a
   timeout, or a body over the limit is a failure. The text is read as UTF-8.
3. **Read.** The ICS reader (below) gives the occurrences that start from 1
   hour ago to 24 hours ahead.
4. **Sync.** For each occurrence, its channel gets a message when it has none,
   or when the title, the start, or the "here" differs from its newest message.
   For each `upcoming` channel whose start is still ahead and which is not in
   the answer, a `cancelled` message is written, seen at once. A meeting that
   moved gets a new channel, and its old one is cancelled.
5. `sync` returns how many messages it wrote, and the daemon draws when it
   wrote any, as in task 0076.

A failure (no network, a bad answer, not ICS) writes nothing and keeps the
meetings the store already has. It logs one line, `cannot read the calendar:
WHY`, until a run succeeds, which logs `the calendar read again`. No log line,
error, or message holds the link or its query string; a failure names only the
host, as in `calendar.example.com answered 403`.

### The ICS reader (`inbox/src/ics.js`)

A small reader with no dependencies, for what Outlook publishes:

- unfolds lines; reads `VEVENT`s with their properties, parameters, and
  escaped text (`\n`, `\,`, `\;`, `\\`);
- `DTSTART` and `DTEND` in UTC (`Z`), with a `TZID`, or floating (taken as
  local). A `TZID` is an IANA name, or a Windows name such as
  `Pacific Standard Time`, mapped through a table of the zones in
  [CLDR's windowsZones](https://github.com/unicode-org/cldr/blob/main/common/supplemental/windowsZones.xml).
  The `VTIMEZONE` rules are not read. Wall time becomes UTC with
  `Intl.DateTimeFormat`, so daylight saving follows the zone's real rules;
- `RRULE` with `FREQ` `DAILY`, `WEEKLY`, `MONTHLY`, or `YEARLY`; `INTERVAL`,
  `COUNT`, `UNTIL`, `BYDAY` (with an ordinal for `MONTHLY`, such as `2TU` or
  `-1FR`), `BYMONTHDAY`, `BYMONTH`, `BYSETPOS`, and `WKST`. Occurrences are
  made in the event's own zone, so a 09:00 meeting stays 09:00 across a
  daylight saving change;
- `EXDATE` removes occurrences, and an event with a `RECURRENCE-ID` replaces
  the occurrence it names (a moved or renamed instance);
- an event is left out when it is all day (`DTSTART;VALUE=DATE`), cancelled
  (`STATUS:CANCELLED`, or a title starting `Canceled:`), or free
  (`TRANSP:TRANSPARENT`, or `X-MICROSOFT-CDO-BUSYSTATUS:FREE`);
- an event the reader cannot read (an `RRULE` part it does not know, a
  `TZID` it cannot map) is left out, and logged once per `UID` per daemon
  run, with its `UID` but not its title.

### The store (`inbox/src/store.js`)

- Schema 6: `messages` gains `at INTEGER`, the time a message is about, in ms
  since 1970, or NULL. Only `meeting` writes it. Older rows have NULL.
- `Store.recent()` rows gain `at` and `body`, those of the newest message.

### Setup

```
# once: keep the link in the login keychain, not in a file
security add-generic-password -s macropad-calendar -a ics -w 'https://outlook.office365.com/owa/calendar/…/calendar.ics'
inbox/scripts/install.sh --slot meeting=2
inbox --plugin meeting list --all-channels
```

Outlook's publish setting must be **All details**. With "Availability only" or
"Limited details", the ICS link has no titles and no join links: the key then
counts down to `Busy` and a press does nothing.

Files to change:

- new: `inbox/src/plugins/meeting.js`, `inbox/src/ics.js`,
  `inbox/src/windows-zones.js`, `inbox/test/meeting.test.js`,
  `inbox/test/ics.test.js`, `inbox/test/fixtures/*.ics`
- `inbox/src/plugins/index.js`, `inbox/src/daemon.js` (`redrawMs`, `press`),
  `inbox/src/render.js` (the top key may have no children),
  `inbox/src/store.js` (schema 6), `inbox/README.md`
- tests that list the plugins, and `inbox/testlib/helpers.js`, so that
  `INBOX_CALENDAR_URL` points at nothing and no test reads the keychain or the
  network

## Definition of done

An outside reviewer verifies each item without help from the implementer. Each
item names its proof. The task moves to `complete/` only when every box is
ticked.

- [ ] **DoD-1** — The ICS reader gives the right occurrences, in UTC, for a
  fixture that holds a weekly meeting in `Pacific Standard Time` across the
  2026-11-01 daylight saving change, a monthly `BYDAY=2TU` meeting, an
  `EXDATE`, a moved instance (`RECURRENCE-ID`), a cancelled event, an all-day
  event, and a free event. **Proof:**
  `cd inbox && node --test --test-name-pattern=TestIcsOccurrences test/ics.test.js`
- [ ] **DoD-2** — An event with an unknown `RRULE` part, or a `TZID` that maps
  to nothing, is left out, and the other events are read. **Proof:**
  `node --test --test-name-pattern=TestIcsUnreadable test/ics.test.js`
- [ ] **DoD-3** — The join link is found in each of the three places. A Teams
  link becomes `url:msteams:/l/meetup-join/…` and a Zoom link stays
  `url:https:…`. An event with no link has no "here". **Proof:**
  `node --test --test-name-pattern=TestMeetingJoinLink test/meeting.test.js`
- [ ] **DoD-4** — Given a fixture, the feed writes one `upcoming` message per
  occurrence in the next 24 hours, with `at` and the "here". The same answer
  again writes nothing. A meeting missing from the next answer gets a seen
  `cancelled` message. **Proof:**
  `node --test --test-name-pattern=TestMeetingFeedWrites test/meeting.test.js`
- [ ] **DoD-5** — With a fake clock, the key is `free` with no meetings,
  `16:30 Standup` 2 hours before, `14m Standup` with no status at 13 min
  10 s, `4m Standup` with `WARN` at 4 min, and `now Standup` with `ERROR` from
  1 min before to 5 min after. Six minutes after the start it shows the next
  meeting. **Proof:**
  `node --test --test-name-pattern=TestMeetingKey test/meeting.test.js`
- [ ] **DoD-6** — A daemon with `redrawMs` sends the new caption when the
  minute changes, without a store write, and sends nothing when the fields did
  not change. **Proof:**
  `node --test --test-name-pattern=TestDaemonRedraw test/daemon.test.js`
- [ ] **DoD-7** — With slot 2 and no layer open, a press event on key 2 calls
  focus with the next meeting's "here" when it starts within 15 minutes. It
  does not call it 20 minutes before, nor on key 3, nor while a layer is open.
  With no slot, start logs `a press needs --slot meeting=N` once. **Proof:**
  `node --test --test-name-pattern=TestDaemonTopPress test/daemon.test.js`
- [ ] **DoD-8** — A failed fetch (a 403, a timeout, a body that is not ICS)
  writes nothing, keeps the meetings already stored, and logs one line over
  three runs. A later good run logs `the calendar read again`. No log line
  holds the link's path or query. **Proof:**
  `node --test --test-name-pattern=TestMeetingFeedFails test/meeting.test.js`
- [ ] **DoD-9** — A store of schema 5 opens as schema 6 with its rows kept and
  `at` NULL; `recent()` rows carry `at` and `body`. **Proof:**
  `node --test --test-name-pattern=TestStoreMigrates test/store.test.js`
- [ ] **DoD-10** — `cd inbox && npm test` passes, and no test reads the
  network or the keychain. **Proof:** the exit code;
  `grep -n INBOX_CALENDAR_URL testlib/helpers.js`
- [ ] **DoD-11** — `inbox/README.md` documents the plugin, the keychain item,
  the publish setting, and `--slot meeting=N`. **Proof:** the file
- [ ] **DoD-12** — On the pad, with Keith's published link: the key counts down
  to his next Teams meeting, turns amber 5 minutes before, and a press opens
  the meeting in the Teams app. **Proof:** Keith's check on the board,
  recorded in Notes
- [ ] **DoD-13** — The PR in the `pr` field links to this spec. **Proof:** PR
  body

## Risks

- Outlook's published calendar lags the real one, possibly by tens of minutes,
  so a meeting booked at the last minute may not show in time → the README
  says so. Graph would fix it, at the cost of a login (a later task if it
  matters).
- The link leaks → it lives in the keychain, and no log or message holds it
  (DoD-8). Anyone with the link can read the calendar; Outlook can reset it.
- A recurrence the reader gets wrong shows a meeting at the wrong time → the
  fixture tests cover what Outlook writes (DoD-1); a rule part the reader does
  not know leaves the event out rather than guessing (DoD-2).
- A Windows zone name missing from the table → the event is left out and
  logged (DoD-2). The table is CLDR's full list, not a hand-picked few.
- A press joins the wrong meeting → only within 15 minutes of its start, and
  only the next one (DoD-7).

## Open questions

- [ ] A 24-hour clock (`16:30`) or a 12-hour one (`4:30p`) for meetings more
  than an hour away? The default is 24-hour. — kgheacock
- [ ] Should the key hide when there is no meeting in the next 24 hours,
  instead of showing `free`? The default is `free`. — kgheacock
- [ ] Should a meeting Keith declined be left out? Outlook's ICS link does not
  always say; the default shows it. — kgheacock

## Notes

Keith asked on 2026-10-27 for the next-meeting key from the plugin ideas, read
from his published Outlook ICS link, built on the inbox daemon (not the Go
plugin host, task 0055), with the countdown, the colours, and a press that
joins. He left out a long press that lists today's meetings and a view during
a meeting.
