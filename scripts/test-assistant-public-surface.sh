#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
GATE="$ROOT/scripts/check-assistant-public-surface.sh"
FIXTURES="$ROOT/scripts/testdata/assistant-public-surface"
tmp="$(mktemp -d "${TMPDIR:-/tmp}/scenery-assistant-public-surface-test.XXXXXX")"
cleanup() {
  rm -rf "$tmp"
}
trap cleanup EXIT HUP INT TERM

expect_ok() {
  local label="$1"
  shift
  if ! "$GATE" "$@" >"$tmp/$label.out" 2>"$tmp/$label.err"; then
    printf 'expected pass: %s\n' "$label" >&2
    cat "$tmp/$label.err" >&2 || true
    exit 1
  fi
}

expect_fail() {
  local label="$1"
  shift
  if "$GATE" "$@" >"$tmp/$label.out" 2>"$tmp/$label.err"; then
    printf 'expected failure: %s\n' "$label" >&2
    exit 1
  fi
}

expect_ok clean "$FIXTURES/clean"
expect_ok explicit-http --http "$FIXTURES/clean/http"
expect_ok explicit-roots --root "$FIXTURES/clean/typescript" --root "$FIXTURES/clean/browser"

expect_fail exact "$FIXTURES/fail-exact"
expect_fail case "$FIXTURES/fail-case"
expect_fail signature "$FIXTURES/fail-signature"
expect_fail split "$FIXTURES/fail-split"
grep -q 'provider token after normalization' "$tmp/split.err" || {
  printf 'split-chunk failure did not exercise normalized reconstruction\n' >&2
  cat "$tmp/split.err" >&2 || true
  exit 1
}
expect_fail underscore "$FIXTURES/fail-underscore"
expect_fail path "$FIXTURES/fail-path"
expect_fail missing "$tmp/does-not-exist"

# App-root discovery must not scan authored provider-only source next to the
# public capture. Only the allowlisted public directory is read.
app="$tmp/app"
mkdir -p "$app/.scenery/harness/assistant-acceptance/public"
printf '{}' > "$app/.scenery.json"
printf 'Eve' > "$app/agent.ts"
printf '{"type":"event","status":"ok"}\n' \
  > "$app/.scenery/harness/assistant-acceptance/public/response.json"
expect_ok app-discovery "$app"

empty_app="$tmp/empty-app"
mkdir -p "$empty_app"
printf '{}' > "$empty_app/.scenery.json"
expect_fail missing-public-dir "$empty_app"

printf '\000' > "$tmp/binary.bin"
expect_fail binary --root "$tmp/binary.bin"

mkdir -p "$tmp/symlink-root"
ln -s "$FIXTURES/clean" "$tmp/symlink-root/linked"
expect_fail symlink --root "$tmp/symlink-root"

printf 'assistant public-surface tests: ok\n'
