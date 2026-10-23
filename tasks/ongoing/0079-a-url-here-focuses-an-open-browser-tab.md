---
id: "0079"
title: "A url here focuses the browser tab that already shows it, before it opens a new one"
status: "ongoing"
created: "2026-10-23"
updated: "2026-10-23"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-bwnpuf"
related: ["0068", "0076"]
tags: ["inbox", "here", "github", "browser"]
---

# 0079 — A url here focuses an open browser tab

## Problem

A press on a PR key of the `github` plugin (task 0076) goes to its "here",
`url:https://github.com/OWNER/REPO/pull/N`. `focus` (task 0068) runs
`open URL`, and the browser opens a new tab every time. After a few presses
Keith has the same PR in several tabs. He wants a press to bring the tab that
already shows the PR to the front, and open a new tab only when there is none.

## Goals

- A press on a key whose "here" is an `https:` URL brings a browser tab that
  shows that URL to the front: it selects the tab, raises its window, and
  activates the browser.
- A tab counts when its URL is the URL, or the URL followed by `/`, `?`, or
  `#`. So a PR's tab still counts on `/files`, `/checks`, or at a comment.
- With no such tab, the press runs `open URL`, as today.
- Google Chrome, Brave, Microsoft Edge, Chromium, and Safari. A browser that is
  not running is not started to look in it.
- Any other "here" (`iterm2:`, `app:`, `slack:` and the other URL schemes) does
  as it does today.

## Non-goals

- Firefox and Arc. Firefox has no scripting of tabs, and Arc's tabs live in
  spaces with another dictionary. A press there opens a new tab, as today.
- Reloading the tab that was found.
- Linux. A "here" works on macOS only.

## Approaches considered

### Approach A — One JavaScript for Automation script over every browser (chosen)

`osascript -l JavaScript` runs one script that, for each supported browser
that runs, reads the URLs of every tab of every window (one Apple Event per
window) and selects the first match. It prints `focused` or `none`. On
`none`, or on any failure, `focus` runs `open URL`.

- Good, because one process looks in every browser; `Application(name)` is
  resolved when the script runs, so a browser that is not installed is
  skipped, not a compile error.
- Good, because `running()` does not start a browser.
- Bad, because macOS asks once, per browser, to let the inbox control it
  (Automation). Until Keith allows it, the script fails and a new tab opens.

### Approach B — One AppleScript per browser

Run an AppleScript `tell application "Google Chrome"` block, then one for
Safari, and so on.

- Good, because AppleScript is what `here.js` uses for iTerm.
- Bad, because a script that names a browser that is not installed does not
  compile, so it needs one `osascript` per browser: up to five processes and
  about a second per press.

### Approach C — A browser extension

An extension that the daemon tells which URL to show.

- Good, because it works in every browser that takes the extension,
  Firefox too.
- Bad, because it is a second program to build, install, and keep up to date
  in each browser, for one action.

## Decision

Chosen: **Approach A — one JavaScript for Automation script**. It covers the
browsers that can be scripted in one process, starts none, and falls back to
today's behaviour on any failure. The cost is the Automation prompt, once per
browser.

## Design

`inbox/src/here.js`:

- `focus('url:https://…')` first runs
  `osascript -l JavaScript -e SCRIPT URL` (the URL is an argument, never part
  of the script text), with a timeout of 3 s.
- The script's `run(argv)` tries, in order, Google Chrome, Brave Browser,
  Microsoft Edge, Chromium, then Safari. For each one that `running()`:
  for each window, `w.tabs.url()`; on the first match, it selects the tab
  (`activeTabIndex` in the Chrome family, `currentTab` in Safari), sets the
  window's `index` to 1, calls `activate()`, and prints `focused`. A browser
  that throws (not installed, no permission) is skipped. It prints `none` at
  the end.
- `focused` on standard output: `focus` resolves. Anything else (`none`, an
  error, a timeout): `focus` runs `open URL` as today, and resolves or rejects
  as `open` does.
- Other kinds and other URL schemes are unchanged.

`inbox/README.md` ("Here") says what a press on an `https:` URL does, which
browsers, and the Automation prompt.

## Definition of done

An outside reviewer verifies each item without help from the implementer.
Each item names its proof. The task moves to `complete/` only when every box
is ticked.

- [ ] **DoD-1** — `focus('url:https://…')` runs `osascript -l JavaScript`
  with the URL as the last argument and nowhere in the script text, no shell,
  and a timeout of at most 3 s; when it prints `focused`, nothing else runs.
  **Proof:** `cd inbox && node --test --test-name-pattern=TestFocusTab test/here.test.js`
- [ ] **DoD-2** — When the script prints `none`, fails, or times out, `focus`
  runs `open URL` once and resolves. **Proof:**
  `node --test --test-name-pattern=TestFocusTabFallsBack test/here.test.js`
- [ ] **DoD-3** — The script, run under Node with stub `Application` objects
  for Chrome and Safari, selects the tab whose URL is the URL plus `/files`,
  skips a browser that is not running, matches neither `…/pull/9` nor
  `…/pull/97x` for `…/pull/97`, and prints `none` when nothing matches.
  **Proof:** `node --test --test-name-pattern=TestTabScript test/here.test.js`
- [ ] **DoD-4** — `url:slack://…` and `app:` still run one `open`. **Proof:**
  `node --test --test-name-pattern=TestFocusAppAndUrl test/here.test.js`
- [ ] **DoD-5** — `cd inbox && npm test` passes. **Proof:** the exit code
- [ ] **DoD-6** — On Keith's Mac, with a PR open in a Chrome tab (also on its
  `/files` page), a press on its key brings that tab to the front and opens
  no new tab; with the tab closed, the press opens one. **Proof:** Keith's
  check on the board, recorded in Notes
- [ ] **DoD-7** — The PR in the `pr` field links to this spec. **Proof:** PR
  body

## Risks

- Keith declines the Automation prompt → the script fails and the press opens
  a new tab, as before; System Settings, Privacy & Security, Automation turns
  it on later. The README says so.
- A browser with many windows is slow to answer → the 3 s timeout ends the
  search and a new tab opens.
- A PR URL that redirects (a renamed repository) does not match its tab → a
  new tab opens, as today.

## Open questions

- [ ] Should a URL that is not a PR, such as one sent with `inbox send --here`,
  also reuse its tab? This task does it for every `https:` "here". — kgheacock

## Notes

Keith asked on 2026-10-23: "ideally it would re-use tabs meaning it would
focus a tab if it already exists".
