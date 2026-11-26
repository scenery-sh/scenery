#!/usr/bin/env bash
# Native integration proof; intentionally not part of the 100ms Go-test lane.
set -Eeuo pipefail

example_root="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
scenery_root="$(cd "$example_root/../.." && pwd)"
scenery_cli="${1:-$scenery_root/.scenery/harness/bin/scenery}"
for command in docker go bun curl jq; do command -v "$command" >/dev/null; done
test -x "$scenery_cli"
proof_root="$(mktemp -d "${TMPDIR:-/tmp}/scenery-webhook-proof.XXXXXX")"
container="scenery-webhook-proof-$(basename "$proof_root" | tr '[:upper:]' '[:lower:]')"
container_id=""
host_pid=""
cleanup() {
  if [[ -n "$host_pid" ]]; then
    kill "$host_pid" 2>/dev/null || true
    wait "$host_pid" 2>/dev/null || true
  fi
  if [[ "$container_id" =~ ^[0-9a-f]{64}$ ]]; then
    docker rm -fv "$container_id" >/dev/null 2>&1 || true
  fi
  printf 'Proof files retained at %s\n' "$proof_root"
}
trap cleanup EXIT
cp -R "$example_root" "$proof_root/app"
cd "$proof_root/app"
go mod edit "-replace=scenery.sh=$scenery_root"
"$scenery_cli" provider lock -o json > "$proof_root/provider-lock.json"
"$scenery_cli" provider lock --check -o json > "$proof_root/provider-lock-check.json"
"$scenery_cli" generate -o json > "$proof_root/generate.json"
go mod tidy > "$proof_root/go-mod-tidy.log" 2>&1
"$scenery_cli" generate --check -o json > "$proof_root/generate-check.json"
"$scenery_cli" check -o json > "$proof_root/check.json"
go test ./... > "$proof_root/go-test.log"
"$scenery_cli" build --development --output "$proof_root/webhook" -o json > "$proof_root/build.json"
test -x "$proof_root/webhook/scenery-kernel"
test -f "$proof_root/webhook/manifest.json"

# Ordinary PostgreSQL durability settings: no fsync-off test shortcut.
docker run -d --name "$container" -p 127.0.0.1::5432 \
  -e POSTGRES_PASSWORD=local-webhook-proof -e POSTGRES_DB=webhook \
  postgres:19-alpine > "$proof_root/container-id"
read -r container_id < "$proof_root/container-id"
[[ "$container_id" =~ ^[0-9a-f]{64}$ ]]
ready=false
for ((attempt=0; attempt<100; attempt++)); do
  if docker exec "$container" pg_isready -h 127.0.0.1 -U postgres -d webhook >/dev/null 2>&1; then ready=true; break; fi
  sleep 0.1
done
[[ "$ready" == true ]]
db_port="$(docker port "$container" 5432 | sed 's/.*://')"
export DATABASE_URL="postgres://postgres:local-webhook-proof@127.0.0.1:$db_port/webhook?sslmode=disable"
docker exec "$container" psql -U postgres -d webhook -v ON_ERROR_STOP=1 -c 'CREATE SCHEMA inbox' > "$proof_root/schema.log"
go run ./cmd/schema
http_port="$(bun -e 'const s = Bun.serve({hostname:"127.0.0.1", port:0, fetch:()=>new Response()}); console.log(s.port); s.stop(true);')"
base_url="http://127.0.0.1:$http_port"
export WEBHOOK_PROOF_JWT_SECRET="local-webhook-proof-jwt-secret"
cat > "$proof_root/config.json" <<'JSON'
{
  "kind": "scenery.config.snapshot",
  "app_id": "webhook-inbox-example",
  "environment": "local",
  "revision": "sha256:webhook-proof",
  "values": {},
  "secrets": {
    "auth.jwt_secret": {"$env": "WEBHOOK_PROOF_JWT_SECRET"}
  }
}
JSON
auth_token="$(bun -e '
import { createHmac } from "node:crypto";
const b64u = (v: string | Buffer) => Buffer.from(v).toString("base64url");
const now = Math.floor(Date.now() / 1000);
const msg = `${b64u(JSON.stringify({ alg: "HS256", typ: "JWT" }))}.${b64u(JSON.stringify({ sub: "proof-user", tenant_id: "00000000-0000-0000-0000-000000000001", iat: now, exp: now + 900 }))}`;
const sig = createHmac("sha256", process.env.WEBHOOK_PROOF_JWT_SECRET!).update(msg).digest("base64url");
console.log(`${msg}.${sig}`);
')"
sql() { docker exec "$container" psql -U postgres -d webhook -v ON_ERROR_STOP=1 -Atc "$1"; }
start_host() {
  "$proof_root/webhook/scenery-kernel" \
    --listen "127.0.0.1:$http_port" \
    --config "$proof_root/config.json" >> "$proof_root/host.log" 2>&1 &
  host_pid=$!
  for ((attempt=0; attempt<100; attempt++)); do
    kill -0 "$host_pid"
    if [[ "$(curl -s -o /dev/null -w '%{http_code}' "$base_url/events/acceptance-1" || true)" == 401 ]]; then return; fi
    sleep 0.1
  done
  return 1
}
start_host
status="$(curl -sS -o "$proof_root/receipt.json" -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"event_id":"acceptance-1","payload":"hello durable"}' "$base_url/events")"
[[ "$status" == 202 ]]
jq -e '.execution_id | type == "string" and length > 0' "$proof_root/receipt.json" >/dev/null
for ((attempt=0; attempt<600; attempt++)); do
  kill -0 "$host_pid"
  [[ "$(sql 'SELECT state FROM scenery.durable_jobs')" == succeeded ]] && break
  sleep 0.1
done
[[ "$(sql 'SELECT state FROM scenery.durable_jobs')" == succeeded ]]
[[ "$(sql 'SELECT payload FROM inbox.processed_events')" == 'hello durable' ]]
# Restart the Host to confirm persisted durable/event state survives process restarts.
kill "$host_pid"
wait "$host_pid" || true
host_pid=""
start_host
bun client/verify.ts "$base_url" acceptance-1 "$auth_token"
[[ "$(sql 'SELECT count(*) FROM scenery.durable_jobs')" == 1 ]]
[[ "$(sql 'SELECT payload FROM inbox.processed_events')" == 'hello durable' ]]
status="$(curl -sS -o "$proof_root/invalid.json" -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"event_id":"","payload":"invalid"}' "$base_url/events")"
[[ "$status" == 400 ]]
[[ "$(sql 'SELECT count(*) FROM scenery.durable_jobs')" == 1 ]]
printf 'PASS durable admission, host restart, idempotence, validation and typed authenticated status\n'
