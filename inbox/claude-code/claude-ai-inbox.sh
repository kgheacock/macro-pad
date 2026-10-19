#!/usr/bin/env bash
# Reports this Claude Code session's state to ai-inbox, which shows it on the
# macro pad: running (blue), waiting for you (yellow), done (green).
# Replaces claude-iterm-badge.sh. Wired from ~/.claude/settings.json hooks.
#
#   $1 = prompt    UserPromptSubmit  you sent a prompt            -> running
#        tool      PostToolUse       a tool finished              -> running (unless it shows already)
#        waiting   Notification      permission / question        -> waiting
#        done      Stop              the turn ended               -> done
#        end       SessionEnd        the session closed           -> clears a stuck running/waiting
#
# One channel per session. It starts with the project: the path below ~/source
# joined by dots, as `ai-inbox send` infers it (else the folder name). Then
# comes the git branch (a worktree has its own), as `macro-pad.my-branch`. If
# a live session holds that channel already, an ordinal follows: `.2`, `.3`.
#
# ai-inbox shows the highest status of the UNSEEN messages (waiting > running >
# done), so a state change marks the channel seen first. Otherwise a `waiting`
# from a permission prompt would keep the key yellow after you approved it, and
# `running` would hide `done`. A tool hook sends nothing while running shows
# already: the last state sent is kept in a file per channel, and the channel
# must still have an unseen message. If you marked it seen, the next tool sends
# running again.
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

# The channel of this session: the project, then the git branch (a worktree
# has its own), then an ordinal if a live session holds that channel already.
# A session keeps its channel until it ends. mkdir is the atomic claim.
sid="$(jq -r '.session_id // empty' <<<"$payload" 2>/dev/null)"
if [ -n "$sid" ]; then
  mkdir -p "$cache/claims" "$cache/sessions" 2>/dev/null
  mine="$(cat "$cache/sessions/$sid" 2>/dev/null)"
  if [ -n "$mine" ]; then
    channel="$mine"
  else
    branch="$(git -C "$proj" symbolic-ref --short -q HEAD 2>/dev/null)"
    base="$(printf '%s' "$channel${branch:+.$branch}" | tr -c 'A-Za-z0-9_.-' '-' | sed -E 's/\.+/./g; s/^\.//; s/\.$//')"
    channel="$base"
    n=1
    until mkdir "$cache/claims/$channel" 2>/dev/null; do
      # A claim that no hook touched for 12 hours belongs to a session that was
      # killed and sent no SessionEnd.
      if [ -n "$(command find "$cache/claims/$channel" -maxdepth 0 -mmin +720 2>/dev/null)" ]; then
        rm -rf "$cache/claims/$channel" 2>/dev/null
        continue
      fi
      [ "$n" -ge 50 ] && { channel="$base"; break; }
      n=$((n + 1))
      channel="$base.$n"
    done
    printf '%s' "$sid" >"$cache/claims/$channel/sid" 2>/dev/null
    printf '%s' "$channel" >"$cache/sessions/$sid" 2>/dev/null
  fi
  touch "$cache/claims/$channel" "$cache/sessions/$sid" 2>/dev/null
fi

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
    [ "$last" = running ] && [ "$("$ai" count "$channel" 2>/dev/null)" -gt 0 ] 2>/dev/null && exit 0
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
    [ -n "$sid" ] && rm -rf "$cache/claims/$channel" "$cache/sessions/$sid" 2>/dev/null
    ;;
esac
exit 0
