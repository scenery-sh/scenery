#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
UI_ROOT="$ROOT/ui"
EMBED_DIST="$ROOT/cmd/scenery/dashboard_static/dist"

command -v bun >/dev/null 2>&1 || {
  printf 'build-dashboard-ui-embed: missing required command: bun\n' >&2
  exit 1
}

cd "$UI_ROOT"
bun install --frozen-lockfile
bun run build

mkdir -p "$EMBED_DIST"
find "$EMBED_DIST" -mindepth 1 ! -name placeholder.txt -exec rm -rf {} +
cp -R "$UI_ROOT/dist/." "$EMBED_DIST/"

test -f "$EMBED_DIST/index.html" || {
  printf 'build-dashboard-ui-embed: expected %s/index.html after build\n' "$EMBED_DIST" >&2
  exit 1
}
