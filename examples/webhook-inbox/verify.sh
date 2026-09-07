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
api_pid=""
worker_pid=""
cleanup() {
  for pid in "$api_pid" "$worker_pid"; do
    if [[ -n "$pid" ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" 2>/dev/null || true; fi
  done
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
"$scenery_cli" build --target development --output "$proof_root/webhook" -o json > "$proof_root/build.json"

# Ordinary PostgreSQL durability settings: no fsync-off test shortcut.
docker run -d --name "$container" -p 127.0.0.1::5432 \
  -e POSTGRES_PASSWORD=local-webhook-proof -e POSTGRES_DB=webhook \
  postgres:18-alpine > "$proof_root/container-id"
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
sql() { docker exec "$container" psql -U postgres -d webhook -v ON_ERROR_STOP=1 -Atc "$1"; }
start_api() {
  SCENERY_ROLE=api SCENERY_LISTEN_ADDR="127.0.0.1:$http_port" "$proof_root/webhook" >> "$proof_root/api.log" 2>&1 &
  api_pid=$!
  for ((attempt=0; attempt<100; attempt++)); do
    kill -0 "$api_pid"
    if [[ "$(curl -s -o /dev/null -w '%{http_code}' "$base_url/events/acceptance-1" || true)" == 401 ]]; then return; fi
    sleep 0.1
  done
  return 1
}
start_api
status="$(curl -sS -o "$proof_root/receipt.json" -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"event_id":"acceptance-1","payload":"hello durable"}' "$base_url/events")"
[[ "$status" == 202 ]]
jq -e '.execution_id | startswith("job_")' "$proof_root/receipt.json" >/dev/null
[[ "$(sql 'SELECT count(*) FROM inbox.processed_events')" == 0 ]]
kill "$api_pid"
wait "$api_pid"
api_pid=""
start_api
[[ "$(sql 'SELECT state FROM scenery.durable_jobs')" == queued ]]
[[ "$(sql 'SELECT count(*) FROM inbox.processed_events')" == 0 ]]
SCENERY_ROLE=worker "$proof_root/webhook" > "$proof_root/worker.log" 2>&1 &
worker_pid=$!
for ((attempt=0; attempt<100; attempt++)); do
  kill -0 "$worker_pid"
  [[ "$(sql 'SELECT state FROM scenery.durable_jobs')" == succeeded ]] && break
  sleep 0.1
done
[[ "$(sql 'SELECT state FROM scenery.durable_jobs')" == succeeded ]]
bun client/verify.ts "$base_url"
[[ "$(sql 'SELECT count(*) FROM scenery.durable_jobs')" == 1 ]]
[[ "$(sql 'SELECT payload FROM inbox.processed_events')" == 'hello durable' ]]
status="$(curl -sS -o "$proof_root/invalid.json" -w '%{http_code}' -H 'Content-Type: application/json' \
  -d '{"event_id":"","payload":"invalid"}' "$base_url/events")"
[[ "$status" == 400 ]]
[[ "$(sql 'SELECT count(*) FROM scenery.durable_jobs')" == 1 ]]
printf 'PASS durable admission, API restart, separate worker, idempotence, validation and typed authenticated status\n'
