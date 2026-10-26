---
id: "0081"
title: "One emoji per repository, on every plugin, and a PR shows the emoji of the Claude session that made it"
status: "ongoing"
created: "2026-10-26"
updated: "2026-10-26"
owner: "kgheacock"
issue: null
issue_url: null
pr: "https://github.com/kgheacock/macro-pad/pull/104"
branch: "claude/project-thread-ik9h13"
related: ["0071", "0072", "0073", "0076"]
tags: ["inbox", "emoji", "github", "ai", "claude-code"]
---

# 0081 — One emoji per repository, and a PR shares its session's emoji

## Problem

Each key of the inbox shows an emoji hashed from its channel (task 0073). The
whole channel feeds the hash, so keys about the same repository disagree:

| Key | Channel | Hashed today |
|---|---|---|
| Claude session (ai) | `github.com.OnePayPal.docreviewonpremnodeweb.08709453` | `channel:github.com.OnePayPal.docreviewonpremnodeweb` + `session:08709453` |
| PR (github) | `OnePayPal.docreviewonpremnodeweb.50` | `channel:OnePayPal.docreviewonpremnodeweb` |
| Subfolder (basic) | `macro-pad.driver` | `channel:macro-pad.driver` |

Every Claude session runs in a folder that is one component, a repository
checkout such as `~/source/macro-pad` or
`~/source/github.com/OnePayPal/docreviewonpremnodeweb`. Keith wants:

1. every key of one component, on every plugin, to show the same first emoji;
2. a PR that a Claude session made to show that session's emoji too, so the
   PR key and the session key match in full.

## Goals

- One **component emoji** per repository, hashed from `repo:NAME`, where NAME
  is the repository's name in lower case. The ai, basic, and github plugins
  and `inbox emoji` all use it as the first emoji.
- A Claude session key keeps its second emoji, the session's.
- A PR key shows the component emoji and, when a Claude session worked on the
  PR's branch, that session's emoji as the second emoji.
- No new command, service, or npm dependency. No network call beyond the one
  the github feed makes already.

## Non-goals

- Two repositories with the same name under two owners share an emoji. The
  set has about 130 emoji, so unrelated names collide anyway.
- Folder layouts other than `~/source/REPO/...` and
  `~/source/HOST/OWNER/REPO/...` (for example `~/source/work/REPO`).
- Sessions on another machine, or cloud sessions: only the hooks of task 0072
  record a session's branch.
- Telling subfolders apart by emoji. The caption still names the subfolder.

## Approaches considered

### Approach A: hash the component, and link a PR to a session by its branch (chosen)

The emoji input becomes the repository name. The Claude Code hook reports the
branch checked out in the session's folder; the store keeps the newest
session per (repository, branch). The github feed asks GitHub for each PR's
head branch and looks the session up.

- Good, because it works for local sessions, which is what the hooks see. A
  session that pushed a branch and opened a PR from it has, by then, run tools
  on that branch.
- Good, because the link needs one more field in the query the feed already
  runs, and no text in the PR.
- Bad, because a branch two sessions worked on goes to the newer one.
- Bad, because the store gains a table and a column (schema 5).

### Approach B: read the session from the PR body or the commit trailers

Claude Code on the web ends its PRs with a `claude.ai/code/session_...` link.
The feed could read it from the body or from a `Claude-Session:` trailer.

- Good, because the link is explicit, not inferred.
- Bad, because that id is a cloud session id. The hooks report the local
  `session_id`, and local sessions write no such link, so nothing matches.
- Bad, because it needs the PR body or commits for every PR, every minute.

### Approach C: make the PR's channel carry the session

The feed names a linked PR `OWNER.REPO.NUMBER.SID`.

- Good, because the renderer needs no extra field.
- Bad, because the channel changes when the link appears, so the PR becomes a
  new key and loses its seen state.

## Decision

Chosen: **Approach A**. It is the only one that links the sessions Keith
actually runs to their PRs. The cost is schema 5 and one heuristic for the
repository name of a folder channel.

## Design

### The component of a channel (`inbox/src/emoji.js`)

`componentOf(folder)` gives the repository name of a folder channel (the
session segment already removed):

- `HOST.TLD.OWNER.REPO...`, when the channel has 4 or more segments and the
  second is a top-level domain (`com`, `org`, `net`, `io`, `dev`, `co`): REPO,
  the fourth segment. `github.com.OnePayPal.docreviewonpremnodeweb` gives
  `docreviewonpremnodeweb`.
- Otherwise the first segment: `macro-pad.driver` gives `macro-pad`.

The github plugin uses the REPO segment of `OWNER.REPO.NUMBER`. The input is
`repo:` and the name in lower case. A dot in a GitHub name is already `-` in
the channel (task 0076); a folder with a dot in its name splits there, which
is a non-goal.

`inbox emoji` hashes the component too, and its JSON names the input
`repo:NAME`. No caller outside this repository reads that field.

### The branch link (store schema 5)

- `messages` gains `session TEXT`: the 8 hex digits of the Claude session a
  message is about, or null. `unseen()` and `recent()` rows gain `session`,
  that of the newest message.
- A new table `branch_sessions (repo TEXT, branch TEXT, session TEXT, at
  INTEGER, PRIMARY KEY (repo, branch))`, shared by every plugin.
- `Store.linkBranch(repo, branch, session, now)` writes the newest session of
  a branch; `Store.branchSession(repo, branch)` reads it, or null.
- `inbox send --branch BRANCH` links BRANCH to the session of the channel
  when the channel is `PROJECT.SID`, under the component of PROJECT. Without a
  session segment it is a usage error.
- The hook `claude-ai-inbox.sh` passes `--branch` with `git branch
  --show-current` of the session's folder, when that prints a branch.

### The github feed and icon

- The query asks for `headRefName` and `headRepository { name }`.
- `sync` looks up the session of (head repository name, head branch). It
  writes a message when the status **or the session** differs from the
  newest message, with the session in its `session` column.
- `prIcon(channel, set, row)` shows the component emoji and, when
  `row.session` is set, the session emoji. The renderer passes the row and
  works out the icon again when the session changes.

### Result for the photo's example

| Key | Emoji |
|---|---|
| session `github.com.OnePayPal.docreviewonpremnodeweb.08709453` | `repo:docreviewonpremnodeweb`, `session:08709453` |
| PR `OnePayPal.docreviewonpremnodeweb.50`, made on a branch of that session | `repo:docreviewonpremnodeweb`, `session:08709453` |
| any other PR of the repository | `repo:docreviewonpremnodeweb` |

Files to change: `inbox/src/emoji.js`, `inbox/src/render.js`,
`inbox/src/store.js`, `inbox/src/cli.js`, `inbox/src/plugins/github.js`,
`inbox/claude-code/claude-ai-inbox.sh`, `inbox/README.md`, and their tests.

## Definition of done

- [ ] **DoD-1**: `componentOf` gives `docreviewonpremnodeweb` for
  `github.com.OnePayPal.docreviewonpremnodeweb` and `macro-pad` for
  `macro-pad.driver`. Proof: `inbox/test/emoji.test.js`.
- [ ] **DoD-2**: the ai key of `github.com.OnePayPal.docreviewonpremnodeweb.08709453`
  and the github key of `OnePayPal.docreviewonpremnodeweb.50` show the same
  first emoji; so do the basic keys `macro-pad` and `macro-pad.driver`. Proof:
  `inbox/test/render.test.js`, `inbox/test/github.test.js`.
- [ ] **DoD-3**: `inbox emoji --folder ~/source/macro-pad/driver` prints the
  emoji of `repo:macro-pad`. Proof: `inbox/test/emoji.test.js`.
- [ ] **DoD-4**: an old store (schema 4) opens and gains the column and the
  table; `send --branch` links the branch, and the newer session wins. Proof:
  `inbox/test/store.test.js`, `inbox/test/cli.test.js`.
- [ ] **DoD-5**: given a link for (`docreviewonpremnodeweb`, `fix-x`), a PR
  whose head is `fix-x` gets the session's emoji as its second emoji, and a
  link made after the PR was first written still reaches its key on the next
  feed run. Proof: `inbox/test/github.test.js`.
- [ ] **DoD-6**: the hook sends `--branch` from the session's folder. Proof:
  `TestHookSendsBranch` in `inbox/test/cli.test.js`.
- [ ] **DoD-7**: on Keith's Mac, a Claude session that opens a PR shows the
  same two emoji on its ai key and on the PR's github key. Proof: Keith checks
  the board.

## Notes

- 2026-10-26: Keith asked for this from a local session's analysis (photo in
  the project thread): the PR channel and the session channel share only
  `OWNER.REPO`.
