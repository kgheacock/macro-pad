#!/bin/sh
# Stop the inbox daemon, and remove its service and the inbox command.

NAME=uninstall.sh
LABEL=com.kgheacock.macropad.inbox
UNIT=inbox-daemon.service

usage() {
  cat <<USAGE
Usage: uninstall.sh [--purge] [--dry-run]

Stop the inbox daemon and remove what install.sh made:
  - the LaunchAgent (macOS) or the systemd user unit (Linux)
  - the \$PREFIX/bin/inbox wrapper (PREFIX defaults to \$HOME/.local)

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
    --dry-run) DRY=1 ;;
    --purge) PURGE=1 ;;
    *) printf '%s: unknown option "%s"; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2 ;;
  esac
  shift
done

PREFIX=${PREFIX:-$HOME/.local}
WRAPPER=$PREFIX/bin/inbox
DB=${INBOX_DB:-${XDG_DATA_HOME:-$HOME/.local/share}/inbox/inbox.db}

case $(uname -s) in
  Darwin) PLATFORM=macos; SERVICE_FILE=$HOME/Library/LaunchAgents/$LABEL.plist ;;
  Linux) PLATFORM=linux; SERVICE_FILE=$HOME/.config/systemd/user/$UNIT ;;
  *) fail "$(uname -s) is not supported; use macOS or Linux" ;;
esac

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
  [ -f "$WRAPPER" ] && grep -q 'bin/inbox.js' "$WRAPPER"
}

FOUND=0
{ [ -e "$SERVICE_FILE" ] || ours; } && FOUND=1

if [ "$FOUND" = 0 ]; then
  printf 'nothing is installed (no %s, no %s)\n' "$SERVICE_FILE" "$WRAPPER"
else
  if [ "$PLATFORM" = macos ]; then
    uid=$(id -u)
    if [ "$DRY" = 1 ]; then
      printf 'would run: launchctl bootout gui/%s/%s\n' "$uid" "$LABEL"
    else
      launchctl bootout "gui/$uid/$LABEL" >/dev/null 2>&1
    fi
  elif [ "$DRY" = 1 ]; then
    printf 'would run: systemctl --user disable --now %s\n' "$UNIT"
  else
    systemctl --user disable --now "$UNIT" >/dev/null 2>&1
  fi
  if [ -e "$SERVICE_FILE" ]; then do_run rm -f "$SERVICE_FILE"; fi
  [ "$PLATFORM" = linux ] && [ "$DRY" = 0 ] && { systemctl --user daemon-reload >/dev/null 2>&1; }
  if ours; then
    do_run rm -f "$WRAPPER"
  elif [ -e "$WRAPPER" ]; then
    printf 'kept %s: install.sh did not write it\n' "$WRAPPER"
  fi
fi

if [ "$PURGE" = 1 ]; then
  for f in "$DB" "$DB-wal" "$DB-shm" "$DB.daemon.pid"; do
    if [ -e "$f" ]; then do_run rm -f "$f"; fi
  done
  [ "$DRY" = 1 ] || printf 'removed the store %s\n' "$DB"
elif [ -e "$DB" ]; then
  printf 'kept the store %s; add --purge to remove it\n' "$DB"
fi
[ "$DRY" = 0 ] || printf 'dry run: nothing changed\n'
exit 0
