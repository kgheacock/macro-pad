# Claude Code hooks for the ai plugin

Reference copy of the Claude Code setup that reports each session to
`inbox --plugin ai` (see "The ai plugin" in `../README.md`). The live files are
`~/.claude/hooks/claude-ai-inbox.sh` and the `hooks` key of
`~/.claude/settings.json`. Task 0070 put them there, in place of the iTerm
badge hooks (`claude-iterm-badge.sh`, `claude-iterm-reset.sh`).

| Claude Code event | Argument | What the pad shows |
|---|---|---|
| `UserPromptSubmit` | `prompt` | running (blue) |
| `PostToolUse` | `tool` | running, only if the state was not running already |
| `Notification` | `waiting` | waiting (yellow); `idle_prompt` and `auth_success` are ignored |
| `Stop` | `done` | done (green) |
| `SessionEnd` | `end` | marks the session seen, so its key goes away |

One channel per session, `PROJECT.SID`: `PROJECT` is what `inbox channel`
prints in the folder the session started in, the same channel `inbox send`
infers there (`~/source/macro-pad/driver` gives `macro-pad.driver`). A session
outside `~/source`, or in a folder whose name cannot be a channel, is not
reported. `SID` is the first 8 characters of the session id, from `session_id` in the
hook payload, else `$CLAUDE_CODE_SESSION_ID`. The key shows the project above
the short id, so two sessions in one project get two keys, and one session's
`done` cannot hide another that waits. Without a session id the channel is
`PROJECT`. `SessionEnd` marks the channel seen, so the key of a closed session
goes away. The script marks the channel seen on a state change, because
the ai plugin shows the highest status of the *unseen* messages: without it, a
`waiting` from a permission prompt would stay yellow after you approve, and a
`running` would hide the `done`. A press on the channel key goes back to the
iTerm session that sent the message (the "here").

## Install

```
install -m 755 inbox/claude-code/claude-ai-inbox.sh ~/.claude/hooks/
cp ~/.claude/settings.json ~/.claude/settings.json.bak
jq --slurpfile h inbox/claude-code/settings-hooks.json '.hooks = $h[0].hooks' \
  ~/.claude/settings.json.bak > ~/.claude/settings.json
```

This replaces every hook in `settings.json`. Needs `jq` and `inbox` in
`~/.local/bin` (set `INBOX_BIN` for another path), and the inbox daemon,
which shows the ai plugin (`inbox/scripts/install.sh`).

## Limits

- A turn that you stop with Esc sends no `Stop`, so the key stays blue until
  your next prompt or the end of the session.
- After you approve a permission prompt, the key stays yellow until the tool
  finishes (`PostToolUse`).
- Settings are read when a session starts; run `/hooks` or restart `claude`
  to load a changed file.
