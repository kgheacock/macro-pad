#!/bin/sh
# Stop the inbox daemon and remove its service, the command, and the
# completion. One daemon shows every plugin (task 0075); the per-plugin
# services of older versions go too.

NAME=uninstall.sh

usage() {
  cat <<USAGE
Usage: uninstall.sh [--purge] [--dry-run]

Stop the inbox daemon and remove what install.sh made:
  - its LaunchAgent (macOS) or systemd user unit (Linux), and any per-plugin
    one an older install.sh made
  - the \$PREFIX/bin/inbox wrapper (PREFIX defaults to \$HOME/.local) and
    the zsh completion link

The store (the SQLite file with your messages) stays unless you pass --purge.
If nothing is installed, it says so and exits 0.

  --purge      also remove the store and its -wal, -shm and pid files
  --dry-run    print each action; change nothing
  -h, --help   show this text
USAGE
}

fail() {
  printf '%s: %s\n' "$NAME" "$1" >&2
  exit 1
}

PURGE=0
DRY=0
while [ $# -gt 0 ]; do
  case $1 in
    -h | --help) usage; exit 0 ;;
    --plugin)
      [ $# -ge 2 ] || { printf '%s: option "%s" needs a value; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2; }
      printf '%s: --plugin is unused: one daemon shows every plugin, and this removes it; ignoring --plugin %s\n' "$NAME" "$2" >&2
      shift ;;
    --dry-run) DRY=1 ;;
    --purge) PURGE=1 ;;
    *) printf '%s: unknown option "%s"; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2 ;;
  esac
  shift
done

CMD=inbox
LABEL=com.kgheacock.macropad.inbox
UNIT=inbox-daemon.service
DB_OVERRIDE=${INBOX_DB:-}
PREFIX=${PREFIX:-$HOME/.local}
WRAPPER=$PREFIX/bin/$CMD
DB=${DB_OVERRIDE:-${XDG_DATA_HOME:-$HOME/.local/share}/inbox/inbox.db}

case $(uname -s) in
  Darwin)
    PLATFORM=macos
    SERVICE_DIR=$HOME/Library/LaunchAgents
    SERVICE_FILE=$SERVICE_DIR/$LABEL.plist
    OLD_PATTERN="$LABEL.*.plist" ;;
  Linux)
    PLATFORM=linux
    SERVICE_DIR=$HOME/.config/systemd/user
    SERVICE_FILE=$SERVICE_DIR/$UNIT
    OLD_PATTERN="inbox-daemon-*.service" ;;
  *) fail "$(uname -s) is not supported; use macOS or Linux" ;;
esac

# The service, then the per-plugin services an older install.sh made.
SERVICES=$SERVICE_FILE
for f in "$SERVICE_DIR"/$OLD_PATTERN; do
  [ -e "$f" ] && SERVICES="$SERVICES $f"
done

# do_run prints an action in a dry run, and runs it otherwise.
do_run() {
  if [ "$DRY" = 1 ]; then
    printf 'would run: %s\n' "$*"
  else
    "$@" || fail "step failed: $*"
  fi
}

# Only remove the wrapper if install.sh wrote it.
ours() {
  [ -f "$WRAPPER" ] && grep -q "bin/$CMD.js" "$WRAPPER"
}

# The completion link install.sh made: a link to this checkout's _inbox.
COMP_FILE=$("$(dirname "$0")/zsh-completion-dir.sh")
[ -z "$COMP_FILE" ] || COMP_FILE=$COMP_FILE/_inbox
comp_ours() {
  [ -n "$COMP_FILE" ] && [ -L "$COMP_FILE" ] && case $(readlink "$COMP_FILE") in */inbox/completions/_inbox) true ;; *) false ;; esac
}

FOUND=0
{ [ "$SERVICES" != "$SERVICE_FILE" ] || [ -e "$SERVICE_FILE" ] || ours || comp_ours; } && FOUND=1

# stop_service FILE: stop the job or unit of one service file.
stop_service() {
  if [ "$PLATFORM" = macos ]; then
    label=$(basename "$1" .plist)
    if [ "$DRY" = 1 ]; then
      printf 'would run: launchctl bootout gui/%s/%s\n' "$uid" "$label"
      return
    fi
    launchctl bootout "gui/$uid/$label" >/dev/null 2>&1
    # launchd lets go of the job a moment later; wait so that a check
    # right after this script sees it gone.
    n=0
    while launchctl print "gui/$uid/$label" >/dev/null 2>&1 && [ $n -lt 50 ]; do
      sleep 0.1
      n=$((n + 1))
    done
  elif [ "$DRY" = 1 ]; then
    printf 'would run: systemctl --user disable --now %s\n' "$(basename "$1")"
  else
    systemctl --user disable --now "$(basename "$1")" >/dev/null 2>&1
  fi
}

if [ "$FOUND" = 0 ]; then
  printf 'nothing is installed (no %s, no %s)\n' "$SERVICE_FILE" "$WRAPPER"
else
  uid=$(id -u)
  for f in $SERVICES; do
    stop_service "$f"
    if [ -e "$f" ]; then do_run rm -f "$f"; fi
  done
  [ "$PLATFORM" = linux ] && [ "$DRY" = 0 ] && { systemctl --user daemon-reload >/dev/null 2>&1; }
  if ours; then
    do_run rm -f "$WRAPPER"
  elif [ -e "$WRAPPER" ]; then
    printf 'kept %s: install.sh did not write it\n' "$WRAPPER"
  fi
  if comp_ours; then do_run rm -f "$COMP_FILE"; fi
fi

if [ "$PURGE" = 1 ]; then
  for f in "$DB" "$DB-wal" "$DB-shm" "$DB".*.daemon.pid; do
    if [ -e "$f" ]; then do_run rm -f "$f"; fi
  done
  [ "$DRY" = 1 ] || printf 'removed the store %s\n' "$DB"
elif [ -e "$DB" ]; then
  printf 'kept the store %s; add --purge to remove it\n' "$DB"
fi
[ "$DRY" = 0 ] || printf 'dry run: nothing changed\n'
exit 0
