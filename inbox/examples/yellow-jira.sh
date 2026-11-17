#!/bin/sh
# An example yellow criteria provider (task 0093): the open JIRA tickets that
# carry the label of a repository. The inbox runs it once per repository:
#
#   yellow-jira.sh REPO-NAME
#
# and reads a JSON array of { id, title, url } from its standard output. Point
# the inbox at it with `install.sh --yellow-cmd /path/to/yellow-jira.sh`.
#
# It needs curl and jq. Put two plain settings, no credential, in
# ~/.config/inbox/yellow-jira.env (launchd and systemd do not pass your shell
# environment to the daemon):
#   JIRA_URL=https://example.atlassian.net
#   JIRA_USER=you@example.com
# The credential is read from the macOS Keychain, service "inbox-jira":
#   security add-generic-password -a "$USER" -s inbox-jira -w
# On Linux, read it from a file of mode 600 instead.
#
# Fewer JIRA calls: this runs one query per repository. Cache the answer in a
# file of your own if you have many repositories. Print nothing to stderr
# that you would not want in the inbox's log: the first line of it is stored.

set -eu

conf=${XDG_CONFIG_HOME:-$HOME/.config}/inbox/yellow-jira.env
# shellcheck disable=SC1090
[ ! -f "$conf" ] || . "$conf"

repo=${1:-}
case $repo in
  '' | -* | *[!A-Za-z0-9._-]*) echo "bad repository name" >&2; exit 2 ;;
esac
: "${JIRA_URL:?set JIRA_URL}"
: "${JIRA_USER:?set JIRA_USER}"

secret=$(security find-generic-password -s inbox-jira -w 2>/dev/null) || {
  echo "no Keychain item inbox-jira" >&2
  exit 1
}

jql="labels = \"$repo\" AND statusCategory != Done ORDER BY updated DESC"

# A quoted value of a curl config takes \\ and \" as escapes, so a token
# that holds a backslash or a quote must be escaped to reach curl whole.
quote() { printf '%s' "$1" | sed 's/[\\"]/\\&/g'; }

# The credential goes to curl on a pipe, not on its command line.
printf 'user = "%s:%s"\n' "$(quote "$JIRA_USER")" "$(quote "$secret")" |
  curl -sS --fail --max-time 15 --config - -G "$JIRA_URL/rest/api/3/search/jql" \
    --data-urlencode "jql=$jql" --data-urlencode "fields=summary" --data-urlencode "maxResults=200" |
  jq --arg base "$JIRA_URL" '[.issues[] | { id: .key, title: .fields.summary, url: ($base + "/browse/" + .key) }]'
