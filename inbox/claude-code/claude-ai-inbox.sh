#!/usr/bin/env bash
# Reports this Claude Code session's state to the ai plugin of inbox
# (`inbox --plugin ai`), which shows it on the macro pad: running (blue), waiting for you (yellow), done (green).
# Replaces claude-iterm-badge.sh. Wired from ~/.claude/settings.json hooks.
#
#   $1 = prompt    UserPromptSubmit  you sent a prompt            -> running
#        tool      PostToolUse       a tool finished              -> running (only on a change)
#        waiting   Notification      permission / question        -> waiting
#        done      Stop              the turn ended               -> done
#        end       SessionEnd        the session closed           -> marks it seen, so its key goes
#
# One channel per session: PROJECT.SID. PROJECT is the channel `inbox channel`
# prints in the project folder (the path below ~/source joined by dots); SID is the
# first 8 characters of the session id, from `session_id` in the hook payload,
# else $CLAUDE_CODE_SESSION_ID. Without a session id the channel is PROJECT.
#
# The ai plugin shows the highest status of the UNSEEN messages (waiting > running >
# done), so a state change marks the channel seen first. Otherwise a `waiting`
# from a permission prompt would keep the key yellow after you approved it, and
# `running` would hide `done`. A tool hook sends nothing while the state is
# already running: the last state sent is kept in a file per session.
#
# Never blocks Claude Code: every failure is silent and the exit code is 0.

state="${1:-}"
payload="$(cat)"
inbox="${INBOX_BIN:-$HOME/.local/bin/inbox}"
{ [ -x "$inbox" ] && command -v jq >/dev/null 2>&1; } || exit 0

# The project channel is what `inbox send` would infer in the project folder,
# so both name it the same way. Outside the source root inbox infers nothing,
# and the session is not reported.
proj="${CLAUDE_PROJECT_DIR:-$(jq -r '.cwd // empty' <<<"$payload" 2>/dev/null)}"
project="$(cd "${proj:-$PWD}" 2>/dev/null && "$inbox" channel 2>/dev/null)" || exit 0
[ -n "$project" ] || exit 0

# The full session id; only letters, digits, - and _ are kept.
session="$(jq -r '.session_id // empty' <<<"$payload" 2>/dev/null)"
session="$(printf '%s' "${session:-${CLAUDE_CODE_SESSION_ID:-}}" | tr -cd 'A-Za-z0-9_-')"
channel="$project${session:+.${session:0:8}}"

cache="${CLAUDE_AI_INBOX_CACHE:-${XDG_CACHE_HOME:-$HOME/.cache}/ai-inbox-claude}"
mkdir -p "$cache" 2>/dev/null
file="$cache/${session:-$channel}"
last="$(cat "$file" 2>/dev/null)"

seen() { "$inbox" --plugin ai seen "$channel" >/dev/null 2>&1; }
send() { "$inbox" --plugin ai send --status "$1" -- "$channel" "$2" >/dev/null 2>&1 && printf '%s' "$1" >"$file" 2>/dev/null; }

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
    # The session is gone: its key goes too.
    seen
    rm -f "$file" 2>/dev/null
    ;;
esac
exit 0
