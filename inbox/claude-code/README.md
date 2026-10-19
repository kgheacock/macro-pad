# Claude Code hooks for ai-inbox

Reference copy of the Claude Code setup that reports each session to
`ai-inbox` (see "ai-inbox" in `../README.md`). The live files are
`~/.claude/hooks/claude-ai-inbox.sh` and the `hooks` key of
`~/.claude/settings.json`. Task 0070 put them there, in place of the iTerm
badge hooks (`claude-iterm-badge.sh`, `claude-iterm-reset.sh`).

| Claude Code event | Argument | What the pad shows |
|---|---|---|
| `UserPromptSubmit` | `prompt` | running (blue) |
| `PostToolUse` | `tool` | running, unless it shows already (the channel has an unseen message) |
| `Notification` | `waiting` | waiting (yellow); `idle_prompt` and `auth_success` are ignored |
| `Stop` | `done` | done (green) |
| `SessionEnd` | `end` | clears a running or waiting state that would stay stuck |

One channel per session. It starts with the project: the path below `~/source`
joined by dots, as `ai-inbox send` infers it, else the folder name. The git
branch follows (a worktree has its own), as `macro-pad.my-branch`. If a live
session holds that channel already, an ordinal follows: `.2`, `.3`. A session
keeps its channel until it ends, also when the branch changes. Claims live in
`~/.cache/ai-inbox-claude`; a claim that no hook touched for 12 hours (a session
that was killed) is free again. The script marks the channel seen on a state change, because
`ai-inbox` shows the highest status of the *unseen* messages: without it, a
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

This replaces every hook in `settings.json`. Needs `jq` and `ai-inbox` in
`~/.local/bin` (`inbox/scripts/install.sh --ai`).

## Limits

- A turn that you stop with Esc sends no `Stop`, so the key stays blue until
  your next prompt or the end of the session.
- After you approve a permission prompt, the key stays yellow until the tool
  finishes (`PostToolUse`).
- Settings are read when a session starts; run `/hooks` or restart `claude`
  to load a changed file.
