#!/usr/bin/env bash
# Reports this Claude Code session's state to ai-inbox, which shows it on the
# macro pad: running (blue), waiting for you (yellow), done (green).
# Replaces claude-iterm-badge.sh. Wired from ~/.claude/settings.json hooks.
#
#   $1 = prompt    UserPromptSubmit  you sent a prompt            -> running
#        tool      PostToolUse       a tool finished              -> running (only on a change)
#        waiting   Notification      permission / question        -> waiting
#        done      Stop              the turn ended               -> done
#        end       SessionEnd        the session closed           -> clears a stuck running/waiting
#
# One channel per project: the path below ~/source joined by dots, as
# `ai-inbox send` infers it (else the folder name). Sessions in one project
# share a channel.
#
# ai-inbox shows the highest status of the UNSEEN messages (waiting > running >
# done), so a state change marks the channel seen first. Otherwise a `waiting`
# from a permission prompt would keep the key yellow after you approved it, and
# `running` would hide `done`. A tool hook sends nothing while the state is
# already running: the last state sent is kept in a file per channel.
#
# Never blocks Claude Code: every failure is silent and the exit code is 0.

state="${1:-}"
payload="$(cat)"
ai="${AI_INBOX_BIN:-$HOME/.local/bin/ai-inbox}"
{ [ -x "$ai" ] && command -v jq >/dev/null 2>&1; } || exit 0

proj="${CLAUDE_PROJECT_DIR:-$(jq -r '.cwd // empty' <<<"$payload" 2>/dev/null)}"
proj="$(cd "${proj:-$PWD}" 2>/dev/null && pwd -P)" || exit 0
root="$(cd "${INBOX_SOURCE_ROOT:-$HOME/source}" 2>/dev/null && pwd -P)"
case "$proj" in
  "$root"/*) rel="${proj#"$root"/}"; channel="${rel//\//.}" ;;
  *) channel="$(basename "$proj")" ;;
esac
# A channel holds letters, digits, - and _ between dots.
channel="$(printf '%s' "$channel" | tr -c 'A-Za-z0-9_.-' '-' | sed -E 's/\.+/./g; s/^\.//; s/\.$//')"
[ -n "$channel" ] || exit 0

cache="${CLAUDE_AI_INBOX_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/ai-inbox-claude}"
mkdir -p "$cache" 2>/dev/null
file="$cache/$channel"
last="$(cat "$file" 2>/dev/null)"

seen() { "$ai" seen "$channel" >/dev/null 2>&1; }
send() { "$ai" send --status "$1" -- "$channel" "$2" >/dev/null 2>&1 && printf '%s' "$1" >"$file" 2>/dev/null; }

case "$state" in
  prompt)
    seen
    send running "working"
    ;;
  tool)
    [ "$last" = running ] && exit 0
    seen
    send running "working"
    ;;
  waiting)
    # An idle prompt is Claude waiting for your next prompt, not for a decision.
    type="$(jq -r '.notification_type // empty' <<<"$payload" 2>/dev/null)"
    case "$type" in idle_prompt | auth_success) exit 0 ;; esac
    [ "$last" = waiting ] && exit 0
    msg="$(jq -r '.message // empty' <<<"$payload" 2>/dev/null)"
    send waiting "${msg:-needs you}"
    ;;
  done)
    seen
    send done "finished"
    ;;
  end)
    [ "$last" = done ] || seen
    rm -f "$file" 2>/dev/null
    ;;
esac
exit 0
