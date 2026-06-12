#!/usr/bin/env bash
set -Eeuo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCENERY_BIN="${SCENERY_BIN:-scenery}"
ONLV_ROOT="${SCENERY_ONLV_SMOKE_ROOT:-${SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT:-/Users/petrbrazdil/Repos/onlv}}"
LOG_DIR="${SCENERY_ONLV_SMOKE_LOG_DIR:-$ROOT/.scenery/release-gate/onlv-smoke}"
EDGE_PUBLIC_ADDR="127.0.0.1:443"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    printf 'onlv smoke: missing required command: %s\n' "$1" >&2
    exit 1
  }
}

json_get() {
  python3 - "$1" "$2" <<'PY'
import json
import sys
path, expr = sys.argv[1:]
value = json.loads(open(path).read())
for part in expr.split("."):
    if part:
        value = value[part]
print(value)
PY
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

cleanup_items=()
app_roots=()
AGENT_HOME=""
AGENT_PID=""
EDGE_STARTED=0

cleanup() {
  local status=$?
  local app_root item
  if [[ "$status" != "0" && -n "$AGENT_HOME" && -d "$AGENT_HOME" ]]; then
    rm -rf "$LOG_DIR/agent-home"
    cp -R "$AGENT_HOME" "$LOG_DIR/agent-home" >/dev/null 2>&1 || true
  fi
  for app_root in "${app_roots[@]:-}"; do
    if [[ -n "$AGENT_HOME" ]]; then
      SCENERY_AGENT_HOME="$AGENT_HOME" "$SCENERY_BIN" down --app-root "$app_root" --all >/dev/null 2>&1 || true
    fi
  done
  if [[ -n "$AGENT_PID" ]]; then
    kill -INT "$AGENT_PID" >/dev/null 2>&1 || true
    wait "$AGENT_PID" >/dev/null 2>&1 || true
  fi
  if [[ "$EDGE_STARTED" == "1" && -n "$AGENT_HOME" ]]; then
    SCENERY_AGENT_HOME="$AGENT_HOME" "$SCENERY_BIN" system edge uninstall --json >/dev/null 2>&1 || true
  fi
  if [[ -n "$AGENT_HOME" ]]; then
    pkill -f "$AGENT_HOME" >/dev/null 2>&1 || true
  fi
  for item in "${cleanup_items[@]:-}"; do
    eval "$item" || true
  done
}
trap cleanup EXIT

need git
need python3
need bun

[[ -d "$ONLV_ROOT/.git" ]] || { printf 'onlv smoke: ONLV root not found: %s\n' "$ONLV_ROOT" >&2; exit 1; }
[[ -f "$ONLV_ROOT/.scenery.json" ]] || { printf 'onlv smoke: missing .scenery.json in %s\n' "$ONLV_ROOT" >&2; exit 1; }

export PATH="$ONLV_ROOT/node_modules/.bin:$ONLV_ROOT/apps/pulse/node_modules/.bin:$ONLV_ROOT/apps/blog/node_modules/.bin:$ONLV_ROOT/apps/ui/node_modules/.bin:$ONLV_ROOT/apps/console/node_modules/.bin:$ONLV_ROOT/apps/viewer/node_modules/.bin:$PATH"

mkdir -p "$LOG_DIR"
SMOKE_TMPDIR="${SCENERY_ONLV_SMOKE_TMPDIR:-/tmp}"
mkdir -p "$SMOKE_TMPDIR"
TMP="$(mktemp -d "$SMOKE_TMPDIR/scenery-onlv-smoke.XXXXXX")"
cleanup_items+=("rm -rf '$TMP'")
AGENT_HOME="$TMP/agent"
export SCENERY_AGENT_HOME="$AGENT_HOME"
export SCENERY_AGENT_ROUTER_ADDR="127.0.0.1:$(free_port)"
export SCENERY_LOCAL_PROXY=0

WT_A="$TMP/onlv-a"
WT_B="$TMP/onlv-b"
git -C "$ONLV_ROOT" worktree add --detach "$WT_A" HEAD >/dev/null
git -C "$ONLV_ROOT" worktree add --detach "$WT_B" HEAD >/dev/null
cleanup_items+=("git -C '$ONLV_ROOT' worktree remove --force '$WT_B' >/dev/null 2>&1")
cleanup_items+=("git -C '$ONLV_ROOT' worktree remove --force '$WT_A' >/dev/null 2>&1")

prepare_worktree() {
  local wt="$1"
  python3 - "$wt/go.mod" "$ROOT" <<'PY'
from pathlib import Path
import sys
path = Path(sys.argv[1])
root = sys.argv[2]
text = path.read_text()
lines = []
replaced = False
for line in text.splitlines():
    if line.strip().startswith("replace scenery.sh =>"):
        lines.append(f"replace scenery.sh => {root}")
        replaced = True
    else:
        lines.append(line)
if not replaced:
    lines.append(f"replace scenery.sh => {root}")
path.write_text("\n".join(lines) + "\n")
PY
  for env_file in .env .env.local ".secrets.local.cue"; do
    if [[ -f "$ONLV_ROOT/$env_file" && ! -e "$wt/$env_file" ]]; then
      cp "$ONLV_ROOT/$env_file" "$wt/$env_file"
    fi
  done
}

prepare_worktree "$WT_A"
prepare_worktree "$WT_B"

install_worktree_dependencies() {
  local wt="$1"
  local name="$2"
  local log="$LOG_DIR/$name-bun-install.log"
  if ! (cd "$wt" && bun install >"$log" 2>&1); then
    printf 'onlv smoke: bun install failed for %s\n' "$name" >&2
    tail -200 "$log" >&2 || true
    return 1
  fi
}

install_worktree_dependencies "$WT_A" a
install_worktree_dependencies "$WT_B" b

start_edge() {
  local out="$LOG_DIR/edge-install.json"
  local err="$LOG_DIR/edge-install.stderr"
  if "$SCENERY_BIN" system edge install --json >"$out" 2>"$err"; then
    EDGE_STARTED=1
    return 0
  fi
  cat "$err" >&2 || true
  if [[ -f "$AGENT_HOME/agent/edge/caddy.log" ]]; then
    printf '\nedge log:\n' >&2
    tail -200 "$AGENT_HOME/agent/edge/caddy.log" >&2 || true
  fi
  if [[ -f "$AGENT_HOME/agent/agent.log" ]]; then
    printf '\nagent log:\n' >&2
    tail -200 "$AGENT_HOME/agent/agent.log" >&2 || true
  fi
  return 1
}

start_edge

start_session() {
  local wt="$1"
  local name="$2"
  local out="$LOG_DIR/$name-detach.json"
  SCENERY_AGENT_HOME="$AGENT_HOME" "$SCENERY_BIN" up --app-root "$wt" --detach --json >"$out"
  json_get "$out" "session.session_id"
}

SESSION_A="$(start_session "$WT_A" a)"
app_roots+=("$WT_A")
SESSION_B="$(start_session "$WT_B" b)"
app_roots+=("$WT_B")

STATUS="$LOG_DIR/status.json"
wait_for_sessions_ready() {
  local deadline=$((SECONDS + 180))
  while (( SECONDS < deadline )); do
    SCENERY_AGENT_HOME="$AGENT_HOME" "$SCENERY_BIN" ps --json >"$STATUS"
    if python3 - "$STATUS" "$SESSION_A" "$SESSION_B" <<'PY'
import json
import sys
payload = json.loads(open(sys.argv[1]).read())
sessions = {s.get("session_id"): s for s in payload.get("sessions", [])}
required = ["api", "dashboard", "electric", "grafana", "temporal", "pulse", "blog"]
for sid in sys.argv[2:]:
    session = sessions.get(sid) or {}
    routes = session.get("routes") or {}
    backends = session.get("backends") or {}
    processes = session.get("processes") or {}
    if session.get("status") != "running":
        raise SystemExit(1)
    if not all(routes.get(route) for route in required):
        raise SystemExit(1)
    if (backends.get("api") or {}).get("network") != "unix":
        raise SystemExit(1)
    for process in ("api", "electric", "worker-typescript"):
        if not (processes.get(process) or {}).get("pid"):
            raise SystemExit(1)
print("ready")
PY
    then
      return 0
    fi
    sleep 1
  done
  printf 'timed out waiting for ONLV sessions to register all routes\n' >&2
  SCENERY_AGENT_HOME="$AGENT_HOME" "$SCENERY_BIN" ps --json >&2 || true
  return 1
}

wait_for_sessions_ready

python3 - "$STATUS" "$SESSION_A" "$SESSION_B" "$EDGE_PUBLIC_ADDR" <<'PY'
import json
import re
import sys

status_path, session_a, session_b, edge_public_addr = sys.argv[1:]
edge_on_443 = edge_public_addr.endswith(":443")
payload = json.loads(open(status_path).read())
sessions = {s["session_id"]: s for s in payload.get("sessions", [])}
missing = [sid for sid in (session_a, session_b) if sid not in sessions]
if missing:
    raise SystemExit(f"missing sessions in status: {missing}")
a = sessions[session_a]
b = sessions[session_b]

def fail(msg):
    raise SystemExit(msg)

if a["session_id"] == b["session_id"]:
    fail("session IDs must differ")

api_a = a.get("backends", {}).get("api", {})
api_b = b.get("backends", {}).get("api", {})
if api_a.get("network") != "unix" or api_b.get("network") != "unix" or api_a.get("addr") == api_b.get("addr"):
    fail(f"API Unix socket backends are not isolated: {api_a} {api_b}")

required = {
    "api": "api",
    "electric": "electric",
    "grafana": "grafana",
    "temporal": "temporal",
    "dashboard": "console",
    "pulse": "pulse",
    "blog": "blog",
}
for session in (a, b):
    sid = session["session_id"]
    base_domain = ((session.get("route_namespace") or {}).get("base_domain") or "onlv.dev")
    routes = session.get("routes", {})
    for route, label in required.items():
        url = routes.get(route, "")
        if not url:
            fail(f"{sid} missing route {route}: {routes}")
        if ".scenery.localhost" in url:
            fail(f"{sid} route {route} uses scenery.localhost: {url}")
        if ":9440" in url:
            fail(f"{sid} route {route} kept fallback router port under HTTPS 443: {url}")
        if edge_on_443 and re.search(r":\d+/", url):
            fail(f"{sid} route {route} kept explicit port while edge is on HTTPS 443: {url}")
        if f".{base_domain}" not in url:
            fail(f"{sid} route {route} is not under {base_domain}: {url}")
        if f"{label}.{sid}.{base_domain}" not in url:
            fail(f"{sid} route {route} is not session-scoped with label {label}: {url}")

alias_owner = {}
for session in (a, b):
    for route, url in (session.get("aliases") or {}).items():
        host = re.sub(r"^https?://", "", url).split("/", 1)[0].split(":", 1)[0]
        previous = alias_owner.get(host)
        if previous and previous != session["session_id"]:
            fail(f"alias {host} is owned by both {previous} and {session['session_id']}")
        alias_owner[host] = session["session_id"]

if not alias_owner:
    fail("expected one live session to own friendly aliases")

for session in (a, b):
    for route, conflict in (session.get("alias_conflicts") or {}).items():
        host = conflict.get("host", "")
        owner = conflict.get("session_id", "")
        if host in alias_owner and alias_owner[host] == session["session_id"]:
            fail(f"{session['session_id']} reports conflict for its own alias {host}")
        if owner == session["session_id"]:
            fail(f"{session['session_id']} reports self-owned alias conflict {route}: {conflict}")
PY

python3 - "$STATUS" "$SESSION_A" "$SESSION_B" "$AGENT_HOME" <<'PY'
import json
import os
import re
import subprocess
import sys

status_path, session_a, session_b, agent_home = sys.argv[1:]
payload = json.loads(open(status_path).read())
sessions = {s["session_id"]: s for s in payload.get("sessions", [])}

def process_text_for_pid(pid):
    if not pid:
        return ""
    try:
        return subprocess.check_output(["ps", "-p", str(pid), "-ww", "-o", "command="], text=True, stderr=subprocess.DEVNULL)
    except Exception:
        return ""

def value(text, key):
    match = re.search(rf"(?:^|\\s){re.escape(key)}=([^\\s]+)", text)
    return match.group(1) if match else ""

def branch_pin_for(session):
    path = os.path.join(session["app_root"], ".scenery", "worktree-db.json")
    try:
        with open(path) as f:
            return json.load(f)
    except Exception as exc:
        raise SystemExit(f"{session['session_id']} missing readable branch pin {path}: {exc}")

def logs_contain(text):
    dev_dir = os.path.join(agent_home, "agent", "dev")
    try:
        names = os.listdir(dev_dir)
    except Exception:
        names = []
    for name in names:
        path = os.path.join(dev_dir, name)
        try:
            with open(path, errors="ignore") as f:
                if text in f.read():
                    return True
        except Exception:
            pass
    return False

db_names = []
electric_streams = []
temporal_queues = []
for sid in (session_a, session_b):
    session = sessions[sid]
    processes = session.get("processes") or {}
    pin = branch_pin_for(session)
    db_name = pin.get("database", "")
    if pin.get("session_id") != sid:
        raise SystemExit(f"{sid} branch pin session_id mismatch: {pin}")
    base_app_id = session.get("base_app_id") or ""
    queue = f"scenery.{base_app_id}.{sid}"
    electric_text = process_text_for_pid((processes.get("electric") or {}).get("pid"))
    stream = value(electric_text, "ELECTRIC_REPLICATION_STREAM_ID")
    if not db_name:
        raise SystemExit(f"{sid} missing database name in worktree branch pin: {pin}")
    if db_name not in electric_text:
        raise SystemExit(f"{sid} Electric process command does not reference managed database {db_name}")
    if not logs_contain(queue):
        raise SystemExit(f"{sid} logs do not contain Temporal task queue prefix {queue}")
    if not stream:
        expected = "scenery_" + re.sub(r"[^A-Za-z0-9_]", "_", sid)
        if expected not in electric_text:
            raise SystemExit(f"{sid} missing ELECTRIC_REPLICATION_STREAM_ID in Electric process command/environment")
        stream = expected
    db_names.append(db_name)
    temporal_queues.append(queue)
    electric_streams.append(stream)

if len(set(db_names)) != 2:
    raise SystemExit(f"managed DB names are not distinct: {db_names}")
if len(set(electric_streams)) != 2:
    raise SystemExit(f"Electric stream IDs are not distinct: {electric_streams}")
if len(set(temporal_queues)) != 2:
    raise SystemExit(f"Temporal task queue prefixes are not distinct: {temporal_queues}")
PY

printf 'onlv two-worktree smoke passed: %s %s\n' "$SESSION_A" "$SESSION_B"
