#!/bin/sh
# Keep the Go build cache under a size budget.
#
# The go command only expires cache entries unused for five days; agent waves
# building many worktrees, test variants and go run executables reach tens of
# gigabytes within days. This script removes least-recently-used entries (the
# go command bumps an entry's mtime when it reuses it, at most hourly) until
# the cache is back under 80% of the budget. Entries used within the last two
# hours are never removed, so a running build cannot lose an input it just
# reused; a missing entry is an ordinary cache miss for the go command.
#
# Usage: go-build-cache-trim.sh [budget]   budget such as 12G or 500M; default 12G
#
# Hourly launchd installation on macOS:
#   cp scripts/go-build-cache-trim.sh ~/.local/bin/go-build-cache-trim
#   chmod +x ~/.local/bin/go-build-cache-trim
#   write ~/Library/LaunchAgents/local.go-build-cache-trim.plist running that
#   path with StartInterval 3600 and PATH including the Go toolchain, then
#   launchctl bootstrap gui/$(id -u) ~/Library/LaunchAgents/local.go-build-cache-trim.plist
set -eu

budget=${1:-12G}
case "$budget" in
*G) budget_kb=$(( ${budget%G} * 1024 * 1024 )) ;;
*M) budget_kb=$(( ${budget%M} * 1024 )) ;;
*) echo "budget must end in G or M: $budget" >&2; exit 2 ;;
esac

cache=${GOCACHE:-}
if [ -z "$cache" ] && command -v go >/dev/null 2>&1; then
	cache=$(go env GOCACHE)
fi
if [ -z "$cache" ]; then
	cache="$HOME/Library/Caches/go-build"
fi
[ -d "$cache" ] || exit 0

stamp() { date '+%Y-%m-%dT%H:%M:%S%z'; }
size_kb=$(du -sk "$cache" | cut -f1)
if [ "$size_kb" -le "$budget_kb" ]; then
	echo "$(stamp) go-build cache $((size_kb / 1024)) MB within $budget budget"
	exit 0
fi

target_bytes=$(( budget_kb * 1024 * 8 / 10 ))
excess_bytes=$(( size_kb * 1024 - target_bytes ))
list=$(mktemp)
trap 'rm -f "$list"' EXIT
# "<mtime> <size> <path>" for every cache entry not used in the last two hours,
# oldest first, cut off once enough bytes are selected.
if stat -f '%m' / >/dev/null 2>&1; then
	set -- stat -f '%m %z %N'
else
	set -- stat -c '%Y %s %n'
fi
find "$cache" -mindepth 2 -type f \( -name '*-a' -o -name '*-d' \) -mmin +120 -print0 |
	xargs -0 "$@" |
	sort -n |
	awk -v excess="$excess_bytes" 'freed < excess { freed += $2; print substr($0, length($1) + length($2) + 3) }' >"$list"
count=$(wc -l <"$list" | tr -d ' ')
tr '\n' '\0' <"$list" | xargs -0 rm -f --
after_kb=$(du -sk "$cache" | cut -f1)
echo "$(stamp) go-build cache trimmed $count entries: $((size_kb / 1024)) MB -> $((after_kb / 1024)) MB (budget $budget)"
