#!/bin/sh
# Install the inbox command and start the inbox daemon at login.
# macOS: a launchd LaunchAgent. Linux: a systemd user unit.

NAME=install.sh
LABEL=com.kgheacock.macropad.inbox
UNIT=inbox-daemon.service

usage() {
  cat <<USAGE
Usage: install.sh [--assets-dir DIR] [--port N] [--slot N] [--no-completions] [--dry-run]

Install the inbox command and start the inbox daemon at login. Run it again
to upgrade in place: it rewrites the files and restarts the daemon.

  --assets-dir DIR  the directory macropadd uses for pictures; an absolute
                    path (default: the assets/ folder of this checkout)
  --port N          macropadd's port (default: 8765)
  --slot N          put the Inbox key in slot N, 0 to 5
  --no-completions  do not install the zsh completions
  --dry-run         print each action; change nothing
  -h, --help        show this text

It writes:
  \$PREFIX/bin/inbox   a wrapper that runs this checkout with the node found
                      now (PREFIX defaults to \$HOME/.local)
  _inbox              a link to inbox/completions/_inbox in the completions
                      directory of oh-my-zsh ($ZSH_CACHE_DIR/completions),
                      when oh-my-zsh is installed. Open a new shell to use it.
  macOS   ~/Library/LaunchAgents/$LABEL.plist
          log: ~/Library/Logs/inbox-daemon.log
  Linux   ~/.config/systemd/user/$UNIT
          log: journalctl --user -u $UNIT

Keep this checkout where it is. Run install.sh again after you move it or
change the Node version. Start macropadd with the same --assets-dir.
USAGE
}

fail() {
  printf '%s: %s\n' "$NAME" "$1" >&2
  exit 1
}

ASSETS_DIR=
PORT=8765
SLOT=
DRY=0
COMPLETIONS=1
while [ $# -gt 0 ]; do
  case $1 in
    -h | --help) usage; exit 0 ;;
    --dry-run) DRY=1 ;;
    --no-completions) COMPLETIONS=0 ;;
    --assets-dir | --port | --slot)
      [ $# -ge 2 ] || { printf '%s: option "%s" needs a value; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2; }
      case $1 in
        --assets-dir) ASSETS_DIR=$2 ;;
        --port) PORT=$2 ;;
        --slot) SLOT=$2 ;;
      esac
      shift ;;
    *) printf '%s: unknown option "%s"; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2 ;;
  esac
  shift
done

HERE=$(cd "$(dirname "$0")" && pwd) || fail "cannot find the folder of this script"
INBOX_DIR=$(dirname "$HERE")
REPO_DIR=$(dirname "$INBOX_DIR")
[ -n "$ASSETS_DIR" ] || ASSETS_DIR=$REPO_DIR/assets
case $ASSETS_DIR in
  /*) ;;
  *) fail "assets dir \"$ASSETS_DIR\" is relative; pass an absolute path, as macropadd sees it" ;;
esac
case $PORT in
  '' | *[!0-9]*) fail "--port $PORT is not a port; use 1 to 65535" ;;
esac
{ [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ]; } || fail "--port $PORT is not a port; use 1 to 65535"
if [ -n "$SLOT" ]; then
  case $SLOT in
    '' | *[!0-9]*) fail "--slot $SLOT is out of range; use 0 to 5" ;;
  esac
  [ "$SLOT" -le 5 ] || fail "--slot $SLOT is out of range; use 0 to 5"
fi

PREFIX=${PREFIX:-$HOME/.local}
WRAPPER=$PREFIX/bin/inbox
DB=${INBOX_DB:-${XDG_DATA_HOME:-$HOME/.local/share}/inbox/inbox.db}

# A service file cannot hold these characters safely, so refuse them.
plain() {
  case $2 in
    *[\"\\%\|\&\<\>\'$(printf '\n')]*)
      fail "$1 \"$2\" has a character this script cannot put in a service file (one of \" ' \\ % | & < >); use another path" ;;
  esac
}
plain "the checkout path" "$INBOX_DIR"
plain "--assets-dir" "$ASSETS_DIR"
plain "PREFIX" "$PREFIX"
[ -z "${INBOX_DB:-}" ] || plain "INBOX_DB" "$INBOX_DB"

# Step 1: node, by absolute path, because launchd and systemd do not see the
# PATH of a shell (nvm, Homebrew).
NODE=$(command -v node) || fail "node is not on the PATH; install Node 22.13 or newer"
case $NODE in
  /*) ;;
  *) fail "node is \"$NODE\", not a file; put a real node first on the PATH" ;;
esac
plain "node" "$NODE"
NODE_VERSION=$("$NODE" -p 'process.versions.node') || fail "cannot run $NODE --version"
NODE_OK=$("$NODE" -p 'const [a, b] = process.versions.node.split(".").map(Number); a > 22 || (a === 22 && b >= 13)')
[ "$NODE_OK" = true ] || fail "node $NODE_VERSION is too old; install Node 22.13 or newer"

# do_run prints an action in a dry run, and runs it otherwise.
do_run() {
  if [ "$DRY" = 1 ]; then
    printf 'would run: %s\n' "$*"
  else
    "$@" || fail "step failed: $*"
  fi
}

# Daemon flags, as words for the wrapper.
DAEMON_ARGS="daemon --port $PORT --assets-dir $ASSETS_DIR"
[ -z "$SLOT" ] || DAEMON_ARGS="$DAEMON_ARGS --slot $SLOT"
[ -z "${INBOX_DB:-}" ] || DAEMON_ARGS="$DAEMON_ARGS --db $INBOX_DB"

write_wrapper() {
  if [ "$DRY" = 1 ]; then
    printf 'would write: %s (runs %s)\n' "$WRAPPER" "$NODE"
    return
  fi
  mkdir -p "$PREFIX/bin" || fail "step failed: mkdir -p $PREFIX/bin"
  {
    printf '#!/bin/sh\n'
    printf "exec '%s' '%s/bin/inbox.js' \"\$@\"\\n" "$NODE" "$INBOX_DIR"
  } >"$WRAPPER.new" || fail "step failed: write $WRAPPER.new"
  chmod 755 "$WRAPPER.new" || fail "step failed: chmod 755 $WRAPPER.new"
  mv "$WRAPPER.new" "$WRAPPER" || fail "step failed: mv $WRAPPER.new $WRAPPER"
}

# Link the zsh completion into oh-my-zsh's completions directory. A problem
# here is reported but does not stop the install.
install_completions() {
  COMP_LINE=
  [ "$COMPLETIONS" = 1 ] || return 0
  comp_dir=$("$HERE/zsh-completion-dir.sh")
  if [ -z "$comp_dir" ]; then
    COMP_LINE='not installed (oh-my-zsh not found)'
    return 0
  fi
  comp_file=$comp_dir/_inbox
  if [ "$DRY" = 1 ]; then
    printf 'would link: %s -> %s\n' "$comp_file" "$INBOX_DIR/completions/_inbox"
    return 0
  fi
  if mkdir -p "$comp_dir" && ln -sfn "$INBOX_DIR/completions/_inbox" "$comp_file"; then
    COMP_LINE="$comp_file (open a new shell to use it)"
  else
    COMP_LINE="could not link $comp_file"
  fi
}

# The words after the wrapper, one per line, for a plist.
plist_args() {
  printf '    <string>%s</string>\n' "$WRAPPER"
  for word in $DAEMON_ARGS; do printf '    <string>%s</string>\n' "$word"; done
}

# render TEMPLATE: fill in the @...@ marks. $ARGS_TEXT replaces a line that is only @ARGS@.
render() {
  while IFS= read -r line; do
    case $line in
      @ARGS@) printf '%s\n' "$ARGS_TEXT" ;;
      *) printf '%s\n' "$line" | sed -e "s|@LABEL@|$LABEL|g" -e "s|@LOG@|$LOG|g" -e "s|@EXEC@|$EXEC|g" ;;
    esac
  done <"$1"
}

write_service() {
  if [ "$DRY" = 1 ]; then
    printf 'would write: %s\n' "$SERVICE_FILE"
    return
  fi
  mkdir -p "$(dirname "$SERVICE_FILE")" || fail "step failed: mkdir -p $(dirname "$SERVICE_FILE")"
  render "$TEMPLATE" >"$SERVICE_FILE" || fail "step failed: write $SERVICE_FILE"
}

install_macos() {
  SERVICE_FILE=$HOME/Library/LaunchAgents/$LABEL.plist
  LOG=$HOME/Library/Logs/inbox-daemon.log
  TEMPLATE=$HERE/$LABEL.plist.tmpl
  ARGS_TEXT=$(plist_args)
  EXEC=
  uid=$(id -u)
  write_service
  # bootout is a no-op when nothing is loaded; wait until launchd lets go.
  if [ "$DRY" = 1 ]; then
    printf 'would run: launchctl bootout gui/%s/%s\n' "$uid" "$LABEL"
    printf 'would run: launchctl bootstrap gui/%s %s\n' "$uid" "$SERVICE_FILE"
    printf 'would run: launchctl kickstart -k gui/%s/%s\n' "$uid" "$LABEL"
    return
  fi
  launchctl bootout "gui/$uid/$LABEL" >/dev/null 2>&1
  n=0
  while launchctl print "gui/$uid/$LABEL" >/dev/null 2>&1 && [ $n -lt 50 ]; do
    sleep 0.1
    n=$((n + 1))
  done
  do_run launchctl bootstrap "gui/$uid" "$SERVICE_FILE"
  # RunAtLoad alone can leave a job loaded but never started right after a
  # bootstrap, so start it (or restart it, on an upgrade) by name.
  do_run launchctl kickstart -k "gui/$uid/$LABEL"
  STATE=stopped
  n=0
  while [ $n -lt 20 ]; do
    if launchctl print "gui/$uid/$LABEL" 2>/dev/null | grep -q 'state = running'; then
      STATE=running
      break
    fi
    sleep 0.25
    n=$((n + 1))
  done
}

install_linux() {
  SERVICE_FILE=$HOME/.config/systemd/user/$UNIT
  LOG="journalctl --user -u $UNIT"
  TEMPLATE=$HERE/$UNIT.tmpl
  ARGS_TEXT=
  EXEC="$WRAPPER"
  for word in $DAEMON_ARGS; do EXEC="$EXEC \"$word\""; done
  write_service
  do_run systemctl --user daemon-reload
  do_run systemctl --user enable "$UNIT"
  do_run systemctl --user restart "$UNIT"
  [ "$DRY" = 1 ] && return
  STATE=stopped
  n=0
  while [ $n -lt 20 ]; do
    if systemctl --user is-active --quiet "$UNIT"; then
      STATE=running
      break
    fi
    sleep 0.25
    n=$((n + 1))
  done
}

case $(uname -s) in
  Darwin) PLATFORM=macos ;;
  Linux) PLATFORM=linux ;;
  *) fail "$(uname -s) is not supported; use macOS or Linux" ;;
esac

write_wrapper
install_completions
if [ "$PLATFORM" = macos ]; then install_macos; else install_linux; fi

if [ "$DRY" = 1 ]; then
  printf 'dry run: nothing changed\n'
  exit 0
fi
printf 'command:  %s (node %s at %s)\n' "$WRAPPER" "$NODE_VERSION" "$NODE"
printf 'service:  %s\n' "$SERVICE_FILE"
printf 'log:      %s\n' "$LOG"
printf 'store:    %s\n' "$DB"
printf 'daemon:   %s\n' "$STATE"
[ -z "$COMP_LINE" ] || printf 'zsh:      %s\n' "$COMP_LINE"
case ":$PATH:" in
  *":$PREFIX/bin:"*) ;;
  *) printf 'note: %s is not on your PATH; add it to use the inbox command\n' "$PREFIX/bin" ;;
esac
[ "$STATE" = running ] || fail "the daemon is not running; read the log: $LOG"
