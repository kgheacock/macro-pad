#!/bin/sh
# Stop the inbox daemon of one plugin and remove its service. The command and
# the completion go with the last plugin's service.

NAME=uninstall.sh

usage() {
  cat <<USAGE
Usage: uninstall.sh [--plugin NAME] [--purge] [--dry-run]

Stop the inbox daemon of one plugin and remove what install.sh made:
  - its LaunchAgent (macOS) or systemd user unit (Linux)
  - the \$PREFIX/bin/inbox wrapper (PREFIX defaults to \$HOME/.local) and
    the zsh completion link, when no other plugin's service is left

The store (the SQLite file with your messages) stays unless you pass --purge.
If nothing is installed, it says so and exits 0.

  --plugin NAME  the plugin whose daemon to remove (default: basic)
  --purge      also remove the store and its -wal, -shm and pid files. The
               store holds every plugin, so this is refused while another
               plugin's service is installed.
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
PLUGIN=basic
while [ $# -gt 0 ]; do
  case $1 in
    -h | --help) usage; exit 0 ;;
    --plugin)
      [ $# -ge 2 ] || { printf '%s: option "%s" needs a value; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2; }
      PLUGIN=$2
      shift ;;
    --dry-run) DRY=1 ;;
    --purge) PURGE=1 ;;
    *) printf '%s: unknown option "%s"; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2 ;;
  esac
  shift
done

case $PLUGIN in
  '' | *[!A-Za-z0-9_-]*)
    printf '%s: --plugin "%s" is not a plugin name\n' "$NAME" "$PLUGIN" >&2
    exit 2 ;;
esac

CMD=inbox
if [ "$PLUGIN" = basic ]; then
  LABEL=com.kgheacock.macropad.inbox
  UNIT=inbox-daemon.service
else
  LABEL=com.kgheacock.macropad.inbox.$PLUGIN
  UNIT=inbox-daemon-$PLUGIN.service
fi
DB_OVERRIDE=${INBOX_DB:-}
PREFIX=${PREFIX:-$HOME/.local}
WRAPPER=$PREFIX/bin/$CMD
DB=${DB_OVERRIDE:-${XDG_DATA_HOME:-$HOME/.local/share}/inbox/inbox.db}

case $(uname -s) in
  Darwin)
    PLATFORM=macos
    SERVICE_DIR=$HOME/Library/LaunchAgents
    SERVICE_FILE=$SERVICE_DIR/$LABEL.plist
    ALL_SERVICES="com.kgheacock.macropad.inbox.plist com.kgheacock.macropad.inbox.*.plist" ;;
  Linux)
    PLATFORM=linux
    SERVICE_DIR=$HOME/.config/systemd/user
    SERVICE_FILE=$SERVICE_DIR/$UNIT
    ALL_SERVICES="inbox-daemon.service inbox-daemon-*.service" ;;
  *) fail "$(uname -s) is not supported; use macOS or Linux" ;;
esac

# The service files of the other plugins, one per line. While one is left,
# the wrapper, the completion, and the store are still in use.
OTHERS=
for pattern in $ALL_SERVICES; do
  for f in "$SERVICE_DIR"/$pattern; do
    [ -e "$f" ] && [ "$f" != "$SERVICE_FILE" ] && OTHERS="$OTHERS${OTHERS:+ }$(basename "$f")"
  done
done
if [ "$PURGE" = 1 ] && [ -n "$OTHERS" ]; then
  fail "--purge removes the store of every plugin, and $OTHERS still uses it; uninstall that first, or drop --purge"
fi

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
{ [ -e "$SERVICE_FILE" ] || ours || comp_ours; } && FOUND=1

if [ "$FOUND" = 0 ]; then
  printf 'nothing is installed (no %s, no %s)\n' "$SERVICE_FILE" "$WRAPPER"
else
  if [ "$PLATFORM" = macos ]; then
    uid=$(id -u)
    if [ "$DRY" = 1 ]; then
      printf 'would run: launchctl bootout gui/%s/%s\n' "$uid" "$LABEL"
    else
      launchctl bootout "gui/$uid/$LABEL" >/dev/null 2>&1
      # launchd lets go of the job a moment later; wait so that a check
      # right after this script sees it gone.
      n=0
      while launchctl print "gui/$uid/$LABEL" >/dev/null 2>&1 && [ $n -lt 50 ]; do
        sleep 0.1
        n=$((n + 1))
      done
    fi
  elif [ "$DRY" = 1 ]; then
    printf 'would run: systemctl --user disable --now %s\n' "$UNIT"
  else
    systemctl --user disable --now "$UNIT" >/dev/null 2>&1
  fi
  if [ -e "$SERVICE_FILE" ]; then do_run rm -f "$SERVICE_FILE"; fi
  [ "$PLATFORM" = linux ] && [ "$DRY" = 0 ] && { systemctl --user daemon-reload >/dev/null 2>&1; }
  if [ -n "$OTHERS" ]; then
    printf 'kept %s and the completion: %s still uses them\n' "$WRAPPER" "$OTHERS"
  else
    if ours; then
      do_run rm -f "$WRAPPER"
    elif [ -e "$WRAPPER" ]; then
      printf 'kept %s: install.sh did not write it\n' "$WRAPPER"
    fi
    if comp_ours; then do_run rm -f "$COMP_FILE"; fi
  fi
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
