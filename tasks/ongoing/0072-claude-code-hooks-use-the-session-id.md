---
id: "0072"
title: "Claude Code hooks: one ai inbox channel per session, named by the session id"
status: "ongoing"
created: "2026-10-19"
updated: "2026-10-19"
owner: "kgheacock"
issue: null
issue_url: null
pr: null
branch: "claude/project-thread-t9q0s6"
related: ["0070", "0071"]
tags: ["inbox", "agents", "claude-code"]
---

# 0072 — Claude Code hooks: one channel per session

## Problem

`inbox/claude-code/claude-ai-inbox.sh` reports a Claude Code session to `inbox --plugin ai` on a channel
named after the project folder only. Every session in one project shares that channel, so:

- one session's `done` marks the channel seen and hides another session that still waits for Keith;
- the cache of the last state is per project, so a `tool` hook of one session can skip a `running` that
  another session needed;
- a `SessionEnd` of one session clears the state of the others.

Claude Code gives every hook the session id: `session_id` in the JSON on stdin, and
`$CLAUDE_CODE_SESSION_ID` in the environment of the commands it runs. Keith wants the hooks keyed by it.

## Goals

- The channel is `PROJECT.SID`: the project channel as today (the path below `~/source` joined by dots,
  else the folder name), then the first 8 characters of the session id. The key shows the project on the
  top line and the short id below it (`render.js` `channelText`).
- The session id comes from `.session_id` of the hook payload, else `$CLAUDE_CODE_SESSION_ID`. Without
  either, the channel is the project alone, as before.
- The last-state cache is one file per session (full id), removed on `SessionEnd`.
- `SessionEnd` always marks the channel seen, so the key of a closed session goes away.
- The script keeps the project path, the project channel, and the full session id in plain variables near
  the top, so a later change (a parallel task: an emoji made from a hash of folder and session id, for the
  key icon) can use them without reworking the script.

## Non-goals

- The emoji icon itself.
- Changes to `inbox`, its plugins, or the daemon.
- Backwards compatibility: old per-project cache files and channels are left to age out.

## Definition of done

- `claude-ai-inbox.sh` and `inbox/claude-code/README.md` describe and use one channel per session.
- Two sessions in one project get two channels; a `done` in one does not mark the other seen.
- A run with a fake `inbox` checks the channel name, the payload and environment fallbacks, and the
  `SessionEnd` cleanup.
