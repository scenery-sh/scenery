#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LOG_DIR="${SCENERY_RELEASE_GATE_LOG_DIR:-"$ROOT/.scenery/release-gate/$(date -u +%Y%m%dT%H%M%SZ)"}"
scenery_bin_was_set="${SCENERY_BIN+x}"
SCENERY_BIN="${SCENERY_BIN:-scenery}"
EXTERNAL_APP_ROOT="${SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT:-}"

mkdir -p "$LOG_DIR"

cleanup_items=()

cleanup() {
  local item
  for item in "${cleanup_items[@]:-}"; do
    eval "$item" || true
  done
}
trap cleanup EXIT

die() {
  printf 'release gate: %s\n' "$*" >&2
  exit 1
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

safe_name() {
  printf '%s' "$1" | tr -cs '[:alnum:]_.-' '-'
}

step() {
  local name="$1"
  shift
  local log="$LOG_DIR/$(safe_name "$name").log"
  printf '\n==> %s\n' "$name"
  if "$@" >"$log" 2>&1; then
    printf 'ok: %s\n' "$name"
    return
  fi
  printf 'failed: %s\nlog: %s\n' "$name" "$log" >&2
  tail -200 "$log" >&2 || true
  exit 1
}

run() {
  printf '+ %s\n' "$*"
  "$@"
}

free_port() {
  python3 - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

wait_for_http() {
  local url="$1"
  local deadline=$((SECONDS + 30))
  local status
  while (( SECONDS < deadline )); do
    status="$(curl -sS -o /dev/null -w '%{http_code}' "$url" || true)"
    if [[ "$status" =~ ^(2|3) ]]; then
      return 0
    fi
    sleep 0.25
  done
  die "timed out waiting for $url"
}

copy_fixture() {
  local name="$1"
  local dst="$2"
  mkdir -p "$dst"
  cp -R "$ROOT/testdata/apps/$name" "$dst"
  python3 - "$dst/$name/go.mod" "$ROOT" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
repo = sys.argv[2]
text = path.read_text()
updated = text.replace("replace scenery.sh => ../../..", f"replace scenery.sh => {repo}")
if updated == text:
    raise SystemExit(f"expected fixture replace directive in {path}")
path.write_text(updated)
PY
}

start_app() {
  local app_root="$1"
  local addr="$2"
  local log="$3"
  local cache
  cache="$(mktemp -d)"
  cleanup_items+=("rm -rf '$cache'")
  SCENERY_DEV_CACHE_DIR="$cache" "$SCENERY_BIN" serve --app-root "$app_root" --listen "$addr" >"$log" 2>&1 &
  local pid=$!
  cleanup_items+=("kill -INT $pid >/dev/null 2>&1 || true; wait $pid >/dev/null 2>&1 || true")
  wait_for_http "http://$addr/service.CallPrivate"
  printf '%s' "$pid"
}

full_go_tests() {
  cd "$ROOT"
  run go test ./...
}

race_tests() {
  cd "$ROOT"
  run go test -race ./...
}

lint_go() {
  cd "$ROOT"
  need golangci-lint
  run golangci-lint run ./...
}

ui_builds() {
  cd "$ROOT/ui"
  need bun
  run bun run typecheck
  run bun run build
}

self_harness() {
  cd "$ROOT"
  run "$SCENERY_BIN" harness self --json --write
}

install_scenery() {
  cd "$ROOT"
  run go install ./cmd/scenery
  if [[ -z "$scenery_bin_was_set" ]]; then
    local gobin
    gobin="$(go env GOBIN)"
    if [[ -z "$gobin" ]]; then
      gobin="$(go env GOPATH)/bin"
    fi
    SCENERY_BIN="$gobin/scenery"
    export SCENERY_BIN
  fi
}

clean_checkout_install() {
  cd "$ROOT"
  need python3
  local tmp
  tmp="$(mktemp -d)"
  cleanup_items+=("rm -rf '$tmp'")
  mkdir -p "$tmp/src"
  git ls-files -z --cached >"$tmp/files.z"
  python3 - "$ROOT" "$tmp/src" "$tmp/files.z" <<'PY'
from pathlib import Path
import shutil
import sys
root = Path(sys.argv[1])
dst = Path(sys.argv[2])
files = Path(sys.argv[3])
for raw in files.read_bytes().split(b"\0"):
    if not raw:
        continue
    rel = Path(raw.decode())
    src = root / rel
    out = dst / rel
    out.parent.mkdir(parents=True, exist_ok=True)
    shutil.copy2(src, out)
PY
  cd "$tmp/src"
  run go install ./cmd/scenery
}

fixture_smoke() {
  local tmp app port addr log
  tmp="$(mktemp -d)"
  cleanup_items+=("rm -rf '$tmp'")
  copy_fixture basic "$tmp"
  app="$tmp/basic"
  port="$(free_port)"
  addr="127.0.0.1:$port"
  log="$LOG_DIR/fixture-smoke-app.log"
  start_app "$app" "$addr" "$log" >/dev/null
  run curl -fsS -H 'X-Echo: hdr' "http://$addr/echo/release?title=Gate" -d '{"body":"ok"}'
  run curl -fsS "http://$addr/service.CallPrivate"
}

external_app_smoke() {
  if [[ -z "$EXTERNAL_APP_ROOT" ]]; then
    printf 'skipping external app smoke; set SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT to enable\n'
    return
  fi
  [[ -d "$EXTERNAL_APP_ROOT" ]] || die "SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT does not exist: $EXTERNAL_APP_ROOT"
  [[ -f "$EXTERNAL_APP_ROOT/.scenery.json" ]] || die "SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT is not a Scenery app: $EXTERNAL_APP_ROOT"
  run "$SCENERY_BIN" inspect app --json --app-root "$EXTERNAL_APP_ROOT"
  run "$SCENERY_BIN" check --json --app-root "$EXTERNAL_APP_ROOT"
}

onlv_two_worktree_smoke() {
  if [[ -z "${SCENERY_ONLV_SMOKE_ROOT:-}" && -z "${SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT:-}" && ! -d "/Users/petrbrazdil/Repos/onlv" ]]; then
    printf 'skipping ONLV two-worktree smoke; set SCENERY_ONLV_SMOKE_ROOT to enable\n'
    return
  fi
  run env SCENERY_BIN="$SCENERY_BIN" SCENERY_ONLV_SMOKE_LOG_DIR="$LOG_DIR/onlv-two-worktree" "$ROOT/scripts/onlv-two-worktree-smoke.sh"
}

router_safety() {
  local tmp app port addr log status
  tmp="$(mktemp -d)"
  cleanup_items+=("rm -rf '$tmp'")
  copy_fixture basic "$tmp"
  app="$tmp/basic"
  port="$(free_port)"
  addr="127.0.0.1:$port"
  log="$LOG_DIR/router-safety-app.log"
  start_app "$app" "$addr" "$log" >/dev/null
  for path in /__scenery/config /platform.Stats /debug/pprof/heap; do
    status="$(curl -sS -o /dev/null -w '%{http_code}' "http://$addr$path")"
    [[ "$status" == "404" ]] || die "$path returned $status, want 404"
  done
}

secrets_gate() {
  local tmp app port addr log output
  tmp="$(mktemp -d)"
  cleanup_items+=("rm -rf '$tmp'")
  copy_fixture secrets "$tmp"
  app="$tmp/secrets"
  rm -f "$app/.env"
  if output="$("$SCENERY_BIN" serve --app-root "$app" --listen "127.0.0.1:$(free_port)" --env production 2>&1)"; then
    printf '%s\n' "$output"
    die "production run succeeded with missing declared secrets"
  fi
  grep -q "missing required secrets for production" <<<"$output" || {
    printf '%s\n' "$output"
    die "missing production secret error did not mention required secrets"
  }

  copy_fixture secrets "$tmp/with-env"
  app="$tmp/with-env/secrets"
  port="$(free_port)"
  addr="127.0.0.1:$port"
  log="$LOG_DIR/secrets-smoke-app.log"
  SCENERY_DEV_CACHE_DIR="$tmp/cache" "$SCENERY_BIN" serve --app-root "$app" --listen "$addr" >"$log" 2>&1 &
  local pid=$!
  cleanup_items+=("kill -INT $pid >/dev/null 2>&1 || true; wait $pid >/dev/null 2>&1 || true")
  wait_for_http "http://$addr/secrets"
  output="$(curl -fsS "http://$addr/secrets")"
  grep -q "service-secret" <<<"$output" || die "service secret was not populated"
  grep -q "helper-secret" <<<"$output" || die "helper secret was not populated"
}

artifact_hygiene() {
  cd "$ROOT"
  local bad
  bad="$(find . \
    \( -path './.git' -o -path './.scenery' -o -path './.codex-tmp' -o -path './ui/node_modules' \) -prune \
    -o \( -name '.DS_Store' -o -name '__MACOSX' \) -print)"
  if [[ -n "$bad" ]]; then
    printf '%s\n' "$bad"
    die "local artifact hygiene failed"
  fi

  local tmp app cache
  tmp="$(mktemp -d)"
  cleanup_items+=("rm -rf '$tmp'")
  copy_fixture basic "$tmp"
  app="$tmp/basic"
  cache="$tmp/cache"
  mkdir -p "$app/.scenery/state" "$app/node_modules" "$app/.git"
  printf 'SHOULD_NOT_COPY=1\n' >"$app/.env"
  printf 'SHOULD_NOT_COPY_LOCAL=1\n' >"$app/.env.local"
  printf 'junk\n' >"$app/.DS_Store"
  SCENERY_DEV_CACHE_DIR="$cache" "$SCENERY_BIN" build --app-root "$app" -o "$tmp/basic-app"
  bad="$(find "$cache" \( -name '.env' -o -name '.env.*' -o -name '.git' -o -name '.scenery' -o -name 'node_modules' -o -name '.DS_Store' -o -name '__MACOSX' \) -print)"
  if [[ -n "$bad" ]]; then
    printf '%s\n' "$bad"
    die "build workspace copied local artifacts"
  fi
}

main() {
  need go
  need git
  need curl
  need python3

  printf 'scenery release gate\nroot: %s\nlogs: %s\n' "$ROOT" "$LOG_DIR"

  step "full go tests" full_go_tests
  step "race tests" race_tests
  step "go lint" lint_go
  step "ui build" ui_builds
  step "install scenery" install_scenery
  step "ONLV two-worktree smoke" onlv_two_worktree_smoke
  step "self harness" self_harness
  step "clean checkout install" clean_checkout_install
  step "fixture smoke" fixture_smoke
  step "external app smoke" external_app_smoke
  step "router safety" router_safety
  step "secrets" secrets_gate
  step "artifact hygiene" artifact_hygiene

  printf '\nrelease gate passed\nlogs: %s\n' "$LOG_DIR"
}

main "$@"
