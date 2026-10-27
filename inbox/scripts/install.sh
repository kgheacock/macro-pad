#!/bin/sh
# Install the inbox command and start the inbox daemon at login. One daemon
# shows every plugin (task 0075).
# macOS: a launchd LaunchAgent. Linux: a systemd user unit.

NAME=install.sh

usage() {
  cat <<USAGE
Usage: install.sh [--plugin NAME]... [--port N] [--slot [NAME=]N]... [--github-query Q]
                  [--apps-root DIR] [--apps-count N] [--apps-run NAME=CMD]...
                  [--no-completions] [--dry-run]

Install the inbox command and start the inbox daemon at login. One daemon,
and one service, shows every plugin, each on its own key. Run it again to
upgrade in place: it rewrites the files and restarts the daemon. It removes
the per-plugin services that older versions made.

  --plugin NAME     show only this plugin; give it once per plugin (default:
                    every plugin, see 'inbox plugins')
  --port N          macropadd's port (default: 8765)
  --slot NAME=N     put the key of plugin NAME in slot N, 0 to 5; once per
                    plugin. With one --plugin, --slot N.
  --github-query Q  the GitHub search of the github plugin, such as
                    'is:pr is:open involves:@me archived:false' (default:
                    \$INBOX_GITHUB_QUERY if set, else the plugin's own)
  --apps-root DIR   the folder of the apps plugin's repositories, such as
                    ~/source/github.com/acme (default: \$INBOX_APPS_ROOT if
                    set; without it the apps plugin shows no keys)
  --apps-count N    how many app keys, 1 to 5 (default: 3)
  --apps-run NAME=CMD  how the app NAME runs, instead of the command its
                    Makefile or package.json gives; once per app
  --no-completions  do not install the zsh completions
  --dry-run         print each action; change nothing
  -h, --help        show this text

It writes:
  \$PREFIX/bin/inbox   a wrapper that runs this checkout with the node found
                      now (PREFIX defaults to \$HOME/.local), and sets
                      INBOX_GH to the gh found now, for the github plugin,
                      and INBOX_GIT to the git found now, for the apps plugin
  _inbox              a link to inbox/completions/_inbox in the completions
                      directory of oh-my-zsh ($ZSH_CACHE_DIR/completions),
                      when oh-my-zsh is installed. Open a new shell to use it.
  macOS   ~/Library/LaunchAgents/com.kgheacock.macropad.inbox.plist
          log: ~/Library/Logs/inbox-daemon.log
  Linux   ~/.config/systemd/user/inbox-daemon.service
          log: journalctl --user -u inbox-daemon.service

It removes, and names, any com.kgheacock.macropad.inbox.NAME.plist (macOS)
or inbox-daemon-NAME.service (Linux) left by an older install.sh.

Keep this checkout where it is. Run install.sh again after you move it or
change the Node version. Restart macropadd after you update it: the
daemon sends icon text, which an old macropadd ignores.
USAGE
}

fail() {
  printf '%s: %s\n' "$NAME" "$1" >&2
  exit 1
}

PORT=8765
SLOTS=
DRY=0
COMPLETIONS=1
PLUGINS_GIVEN=
GITHUB_QUERY=${INBOX_GITHUB_QUERY:-}
APPS_ROOT=${INBOX_APPS_ROOT:-}
APPS_COUNT=${INBOX_APPS_COUNT:-}
APPS_RUN=${INBOX_APPS_RUN:-}
APPS_RUN_GIVEN=0
while [ $# -gt 0 ]; do
  case $1 in
    -h | --help) usage; exit 0 ;;
    --dry-run) DRY=1 ;;
    --no-completions) COMPLETIONS=0 ;;
    --assets-dir | --port | --slot | --plugin | --github-query | --apps-root | --apps-count | --apps-run)
      [ $# -ge 2 ] || { printf '%s: option "%s" needs a value; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2; }
      case $1 in
        --assets-dir) printf '%s: --assets-dir is unused: the daemon sends icon text, not picture files; ignoring it\n' "$NAME" >&2 ;;
        --port) PORT=$2 ;;
        --slot) SLOTS="$SLOTS${SLOTS:+ }$2" ;;
        --plugin) PLUGINS_GIVEN="$PLUGINS_GIVEN${PLUGINS_GIVEN:+ }$2" ;;
        --github-query) GITHUB_QUERY=$2 ;;
        --apps-root) APPS_ROOT=$2 ;;
        --apps-count) APPS_COUNT=$2 ;;
        --apps-run)
          # The --apps-run flags replace $INBOX_APPS_RUN.
          [ "$APPS_RUN_GIVEN" = 1 ] || APPS_RUN=
          APPS_RUN_GIVEN=1
          case $2 in
            ?*=?*) ;;
            *) printf '%s: --apps-run "%s" is not NAME=CMD\n' "$NAME" "$2" >&2; exit 2 ;;
          esac
          APPS_RUN="$APPS_RUN${APPS_RUN:+;}$2" ;;
      esac
      shift ;;
    *) printf '%s: unknown option "%s"; run '"'"'%s -h'"'"' for help\n' "$NAME" "$1" "$NAME" >&2; exit 2 ;;
  esac
  shift
done

HERE=$(cd "$(dirname "$0")" && pwd) || fail "cannot find the folder of this script"
INBOX_DIR=$(dirname "$HERE")
case $PORT in
  '' | *[!0-9]*) fail "--port $PORT is not a port; use 1 to 65535" ;;
esac
{ [ "$PORT" -ge 1 ] && [ "$PORT" -le 65535 ]; } || fail "--port $PORT is not a port; use 1 to 65535"
# Each --slot is N or NAME=N; the daemon checks that the names and slots fit
# together, so a bad pair shows in the log.
for slot in $SLOTS; do
  case $slot in
    *=*) n=${slot#*=} ;;
    *) n=$slot ;;
  esac
  case $n in
    '' | *[!0-9]*) fail "--slot $slot is out of range; use 0 to 5" ;;
  esac
  [ "$n" -le 5 ] || fail "--slot $slot is out of range; use 0 to 5"
done

# The query goes in the wrapper between single quotes.
case $GITHUB_QUERY in
  *[\'\\\}$(printf '\n')]*)
    printf '%s: --github-query may not hold a quote, a backslash, } or a new line\n' "$NAME" >&2
    exit 2 ;;
esac

# The apps settings go in the wrapper between single quotes too.
case $APPS_ROOT in
  *[\'\\\}$(printf '\n')]*)
    printf '%s: --apps-root may not hold a quote, a backslash, } or a new line\n' "$NAME" >&2
    exit 2 ;;
esac
case $APPS_RUN in
  *[\'\\\}$(printf '\n')]*)
    printf '%s: --apps-run may not hold a quote, a backslash, } or a new line\n' "$NAME" >&2
    exit 2 ;;
esac
case $APPS_COUNT in
  '' | [1-5]) ;;
  *) printf '%s: --apps-count %s is out of range; use 1 to 5\n' "$NAME" "$APPS_COUNT" >&2; exit 2 ;;
esac
if [ -n "$APPS_ROOT" ]; then
  case $APPS_ROOT in
    '~') APPS_ROOT=$HOME ;;
    '~/'*) APPS_ROOT=$HOME/${APPS_ROOT#'~/'} ;;
  esac
  [ -d "$APPS_ROOT" ] || fail "--apps-root $APPS_ROOT is not a folder"
  APPS_ROOT=$(cd "$APPS_ROOT" && pwd) || fail "cannot open --apps-root $APPS_ROOT"
fi

for plugin in $PLUGINS_GIVEN; do
  case $plugin in
    *[!A-Za-z0-9_-]*)
      printf '%s: --plugin "%s" is not a plugin name; run '"'"'inbox plugins'"'"' for the list\n' "$NAME" "$plugin" >&2
      exit 2 ;;
  esac
done

# One service for every plugin. Older versions made one per plugin, named
# $LABEL.NAME and inbox-daemon-NAME.service; install removes those.
CMD=inbox
LABEL=com.kgheacock.macropad.inbox
UNIT=inbox-daemon.service
LOG_NAME=inbox-daemon
DESC='Inbox daemon: show every inbox plugin on the macro pad'
DB_VAR=INBOX_DB
DB_OVERRIDE=${INBOX_DB:-}
PREFIX=${PREFIX:-$HOME/.local}
WRAPPER=$PREFIX/bin/$CMD
DB=${DB_OVERRIDE:-${XDG_DATA_HOME:-$HOME/.local/share}/inbox/inbox.db}

# A service file cannot hold these characters safely, so refuse them.
plain() {
  case $2 in
    *[\"\\%\|\&\<\>\'$(printf '\n')]*)
      fail "$1 \"$2\" has a character this script cannot put in a service file (one of \" ' \\ % | & < >); use another path" ;;
  esac
}
plain "the checkout path" "$INBOX_DIR"
plain "PREFIX" "$PREFIX"
[ -z "$DB_OVERRIDE" ] || plain "$DB_VAR" "$DB_OVERRIDE"

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

# gh, by absolute path, for the github plugin's feed (task 0076). Without it
# the github key stays empty and the log says why.
GH=$(command -v gh 2>/dev/null) || GH=
case $GH in
  /*) plain "gh" "$GH" ;;
  *) GH= ;;
esac

# git, by absolute path, for the apps plugin's Pull key (task 0085).
GIT=$(command -v git 2>/dev/null) || GIT=
case $GIT in
  /*) plain "git" "$GIT" ;;
  *) GIT= ;;
esac

# Each plugin must be one this checkout has.
PLUGINS=$("$NODE" "$INBOX_DIR/bin/inbox.js" plugins | awk '{print $1}') || fail "cannot run inbox plugins"
for plugin in $PLUGINS_GIVEN; do
  if ! printf '%s\n' "$PLUGINS" | grep -qx "$plugin"; then
    printf '%s: unknown plugin "%s"; use one of: %s\n' "$NAME" "$plugin" "$(printf '%s' "$PLUGINS" | tr '\n' ' ')" >&2
    exit 2
  fi
done
# A bare --slot N fits only one plugin: two keys cannot share a slot.
if [ -n "$PLUGINS_GIVEN" ]; then COUNT=$(printf '%s\n' "$PLUGINS_GIVEN" | tr ' ' '\n' | wc -l); else COUNT=$(printf '%s\n' "$PLUGINS" | wc -l); fi
for slot in $SLOTS; do
  case $slot in
    *=*) ;;
    *) [ "$COUNT" -eq 1 ] || { printf '%s: --slot %s names no plugin, and the daemon shows more than one; use --slot NAME=N\n' "$NAME" "$slot" >&2; exit 2; } ;;
  esac
done
if [ -n "$PLUGINS_GIVEN" ]; then
  SHOWN=$(printf '%s' "$PLUGINS_GIVEN" | tr ' ' ',')
else
  SHOWN="every plugin ($(printf '%s' "$PLUGINS" | tr '\n' ',' | sed -e 's/,$//' -e 's/,/, /g'))"
fi

# do_run prints an action in a dry run, and runs it otherwise.
do_run() {
  if [ "$DRY" = 1 ]; then
    printf 'would run: %s\n' "$*"
  else
    "$@" || fail "step failed: $*"
  fi
}

# Daemon flags, as words for the wrapper.
DAEMON_ARGS="daemon --port $PORT"
for plugin in $PLUGINS_GIVEN; do DAEMON_ARGS="$DAEMON_ARGS --plugin $plugin"; done
for slot in $SLOTS; do DAEMON_ARGS="$DAEMON_ARGS --slot $slot"; done
[ -z "$DB_OVERRIDE" ] || DAEMON_ARGS="$DAEMON_ARGS --db $DB_OVERRIDE"

write_wrapper() {
  if [ "$DRY" = 1 ]; then
    printf 'would write: %s (runs %s%s%s%s%s%s%s)\n' "$WRAPPER" "$NODE" "${GH:+, INBOX_GH=$GH}" "${GIT:+, INBOX_GIT=$GIT}" \
      "${GITHUB_QUERY:+, INBOX_GITHUB_QUERY=$GITHUB_QUERY}" "${APPS_ROOT:+, INBOX_APPS_ROOT=$APPS_ROOT}" "${APPS_COUNT:+, INBOX_APPS_COUNT=$APPS_COUNT}" "${APPS_RUN:+, INBOX_APPS_RUN=$APPS_RUN}"
    return
  fi
  mkdir -p "$PREFIX/bin" || fail "step failed: mkdir -p $PREFIX/bin"
  {
    printf '#!/bin/sh\n'
    [ -z "$GH" ] || printf "INBOX_GH=\${INBOX_GH:-'%s'}\nexport INBOX_GH\n" "$GH"
    [ -z "$GITHUB_QUERY" ] || printf "INBOX_GITHUB_QUERY=\${INBOX_GITHUB_QUERY:-'%s'}\nexport INBOX_GITHUB_QUERY\n" "$GITHUB_QUERY"
    [ -z "$GIT" ] || printf "INBOX_GIT=\${INBOX_GIT:-'%s'}\nexport INBOX_GIT\n" "$GIT"
    [ -z "$APPS_ROOT" ] || printf "INBOX_APPS_ROOT=\${INBOX_APPS_ROOT:-'%s'}\nexport INBOX_APPS_ROOT\n" "$APPS_ROOT"
    [ -z "$APPS_COUNT" ] || printf "INBOX_APPS_COUNT=\${INBOX_APPS_COUNT:-'%s'}\nexport INBOX_APPS_COUNT\n" "$APPS_COUNT"
    [ -z "$APPS_RUN" ] || printf "INBOX_APPS_RUN=\${INBOX_APPS_RUN:-'%s'}\nexport INBOX_APPS_RUN\n" "$APPS_RUN"
    printf "exec '%s' '%s/bin/%s.js' \"\$@\"\\n" "$NODE" "$INBOX_DIR" "$CMD"
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
      *) printf '%s\n' "$line" | sed -e "s|@LABEL@|$LABEL|g" -e "s|@LOG@|$LOG|g" -e "s|@EXEC@|$EXEC|g" -e "s|@DESC@|$DESC|g" ;;
    esac
  done <"$1"
}

write_service() {
  if [ "$DRY" = 1 ]; then
    printf 'would write: %s (runs: inbox %s)\n' "$SERVICE_FILE" "$DAEMON_ARGS"
    return
  fi
  mkdir -p "$(dirname "$SERVICE_FILE")" || fail "step failed: mkdir -p $(dirname "$SERVICE_FILE")"
  render "$TEMPLATE" >"$SERVICE_FILE" || fail "step failed: write $SERVICE_FILE"
}

# Stop and remove the per-plugin services of older versions. Their logs stay.
remove_old_macos() {
  for old in "$HOME/Library/LaunchAgents/$LABEL".*.plist; do
    [ -e "$old" ] || continue
    old_label=$(basename "$old" .plist)
    if [ "$DRY" = 1 ]; then
      printf 'would run: launchctl bootout gui/%s/%s\n' "$uid" "$old_label"
      printf 'would remove: %s (one daemon now shows every plugin)\n' "$old"
      continue
    fi
    launchctl bootout "gui/$uid/$old_label" >/dev/null 2>&1
    n=0
    while launchctl print "gui/$uid/$old_label" >/dev/null 2>&1 && [ $n -lt 50 ]; do
      sleep 0.1
      n=$((n + 1))
    done
    rm -f "$old" || fail "step failed: rm -f $old"
    printf 'removed:  %s (one daemon now shows every plugin)\n' "$old"
  done
}

remove_old_linux() {
  removed=0
  for old in "$HOME/.config/systemd/user"/inbox-daemon-*.service; do
    [ -e "$old" ] || continue
    old_unit=$(basename "$old")
    if [ "$DRY" = 1 ]; then
      printf 'would run: systemctl --user disable --now %s\n' "$old_unit"
      printf 'would remove: %s (one daemon now shows every plugin)\n' "$old"
      continue
    fi
    systemctl --user disable --now "$old_unit" >/dev/null 2>&1
    rm -f "$old" || fail "step failed: rm -f $old"
    printf 'removed:  %s (one daemon now shows every plugin)\n' "$old"
    removed=1
  done
  [ "$removed" = 0 ] || do_run systemctl --user daemon-reload
}

install_macos() {
  SERVICE_FILE=$HOME/Library/LaunchAgents/$LABEL.plist
  LOG=$HOME/Library/Logs/$LOG_NAME.log
  TEMPLATE=$HERE/com.kgheacock.macropad.inbox.plist.tmpl
  ARGS_TEXT=$(plist_args)
  EXEC=
  uid=$(id -u)
  # Before the new service starts, so the old ai daemon lets go of its pid file.
  remove_old_macos
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
  TEMPLATE=$HERE/inbox-daemon.service.tmpl
  ARGS_TEXT=
  EXEC="$WRAPPER"
  for word in $DAEMON_ARGS; do EXEC="$EXEC \"$word\""; done
  remove_old_linux
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
printf 'plugins:  %s\n' "$SHOWN"
printf 'store:    %s\n' "$DB"
printf 'gh:       %s\n' "${GH:-not found; the github plugin shows no PRs}"
[ -z "$GITHUB_QUERY" ] || printf 'github:   %s\n' "$GITHUB_QUERY"
printf 'apps:     %s\n' "${APPS_ROOT:-no --apps-root; the apps plugin shows no keys}"
printf 'daemon:   %s\n' "$STATE"
[ -z "$COMP_LINE" ] || printf 'zsh:      %s\n' "$COMP_LINE"
case ":$PATH:" in
  *":$PREFIX/bin:"*) ;;
  *) printf 'note: %s is not on your PATH; add it to use the %s command\n' "$PREFIX/bin" "$CMD" ;;
esac
[ "$STATE" = running ] || fail "the daemon is not running; read the log: $LOG"
