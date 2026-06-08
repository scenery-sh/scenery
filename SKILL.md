---
name: onlava
description: Use when building, running, debugging, inspecting, validating, or generating clients for onlava applications. onlava is a Go-native service runtime and CLI using .onlava.json, //onlava directives, typed endpoints, local dev supervision, logs, traces, metrics, workers, and TypeScript client generation.
---

# onlava

onlava is a Go-native service runtime and local development platform. It runs app sessions, exposes capabilities for inspection and action, and hides backing substrate details unless you intentionally debug them. Apps are ordinary Go modules with a `.onlava.json` app root and `//onlava:` directives in Go source.

This skill is the portable agent entrypoint. It teaches shared onlava behavior, but it does not replace app-local instructions. Client apps should also keep a small `AGENTS.md` with app root, frontend roots, generated client paths, required environment names, validation commands, and product invariants.

Read next when needed:

- `docs/agent-guide.md` for agent workflow, capabilities, generated artifacts, and client-app integration.
- `docs/local-contract.md` for exact CLI grammar, JSON schemas, artifact paths, and stability labels.
- `docs/app-development-cookbook.md` for app recipes.
- `docs/ui-agent-contract.md` before UI work.

## Agent Fast Path

```sh
onlava check --json
onlava doctor --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect wire --json
onlava system toolchain verify --json
onlava logs --jsonl --limit 200
onlava inspect observability --json --session current
onlava logs query --json --session current --since 15m --query 'error OR panic'
onlava harness --json --write
```

Prefer JSON output for agent decisions. Prefer `onlava up` for local development. Use `onlava serve` for headless API execution. Use `onlava task` for configured and code tasks. Use `onlava worker` for worker-only cron/Temporal execution.

Run `onlava doctor --json` before deep app debugging when local readiness is in doubt. It is read-only and reports host resources, Go version, optional tools, and app-sensitive dependency hints without building or starting services.

`onlava inspect docs --json` exposes `summary.review_due_count` plus document-level `review_due` and `stale` fields. For onlava repo changes, `onlava harness self --json --write` surfaces those docs knowledge signals in validation summaries. When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Mental Model

- `.onlava.json` marks the app root.
- Go source is the app model.
- `onlava up` starts the supervised local platform: app process, rebuild/restart loop, dashboard, API Explorer, logs, traces, metrics, managed dev services when configured, and optional frontend routing through the local agent.
- `onlava serve` starts a headless API-role server and does not start dashboard, proxy, or watch mode.
- Public and auth endpoints are externally reachable. Private endpoints are internal-only and called through generated helpers.
- Typed endpoints decode path, query, header, cookie, and JSON body inputs into Go values.
- Generated internal calls preserve routing, private access, auth context, tracing, and error semantics.

## Minimal App

```json
{"name":"hello"}
```

```go
module example.com/hello

go 1.26.3

require github.com/pbrazdil/onlava v0.0.0
```

```go
package service

import "context"

type HelloResponse struct {
	Message string `json:"message"`
}

//onlava:api public path=/hello/:name method=GET
func Hello(ctx context.Context, name string) (*HelloResponse, error) {
	return &HelloResponse{Message: "hello " + name}, nil
}
```

```sh
onlava check --json
onlava serve
curl http://127.0.0.1:4000/hello/world
```

## Directives

```go
//onlava:api public|auth|private [raw] [path=/...] [method=GET,POST]
//onlava:service
//onlava:authhandler
```

Standard auth can be enabled from `.onlava.json` without app-local wrapper endpoints. Its tenant tables are framework-owned in PostgreSQL schema `onlava_auth` including `onlava_auth.tenants`; app-local `tenants` services or tables are product-domain concerns only.

Typed endpoint shape:

```go
func Endpoint(ctx context.Context, pathParam string, req *Request) (*Response, error)
```

Raw endpoint shape:

```go
func Endpoint(w http.ResponseWriter, req *http.Request)
```

Struct tags:

- request decoding: `json`, `header`, `query`, `qs`, `cookie`
- onlava tags: `onlava:"optional"`, `onlava:"httpstatus"`

## Public Go Packages

- `github.com/pbrazdil/onlava`
- `github.com/pbrazdil/onlava/auth`
- `github.com/pbrazdil/onlava/errs`
- `github.com/pbrazdil/onlava/middleware`
- `github.com/pbrazdil/onlava/temporal`
- `github.com/pbrazdil/onlava/cron`
- `github.com/pbrazdil/onlava/pgxpool`
- `github.com/pbrazdil/onlava/et`

## Local Development

```sh
onlava up
onlava up --json
onlava up --detach
onlava logs --follow
onlava console
onlava down
```

`onlava up --json` emits JSONL. `onlava up --detach` starts an agent-backed app session. `onlava logs --follow` follows session logs. `onlava console` opens the source-aware terminal console. Agent session `routes` are canonical; configured friendly hosts appear separately in `aliases` only for the live session that owns the free alias. Use `onlava up --claim-aliases` only for intentional live alias transfer.

Use `onlava system edge dns install`, `onlava system edge privileged install`, `onlava system edge install`, then `onlava system edge trust` when a browser needs trusted wildcard local HTTPS on `127.0.0.1:443`. The DNS command owns wildcard `local.dev` resolution through managed dnsmasq; the privileged helper owns only the default HTTPS loopback listener and forwards raw TCP to user-owned Caddy on an unprivileged loopback port. Do not run Caddy, the agent router, or `onlava system edge install` as root. `onlava system edge` uses managed dnsmasq and Caddy from the toolchain. `onlava system edge trust` uses a temporary admin-only Caddy process and does not require the port-443 edge to already be running.

For managed Postgres, app processes, setup commands, DB setup, and workers receive `DatabaseURL` as the app database authority. Onlava does not inject `DATABASE_URL` into those app-facing environments; treat `ONLAVA_MANAGED_DATABASE_URL` as tooling/debug metadata. The shared Postgres substrate records only physical-server metadata; the session database URL/name is a session env lease, not a global substrate key. To use an explicit external DB with declared managed Postgres, set `ONLAVA_DEV_POSTGRES_EXTERNAL=1` and provide `DatabaseURL`; `DATABASE_URL` is ignored.

For Electric-backed frontend writes, generated TypeScript `WithMeta` methods include parsed `txid` metadata. Use `observeAPIResponseTxid` around the app's Electric/TanStack observer so a post-commit sync timeout is reported as `SyncObservationError` instead of an API mutation failure.

## UI Work

Read `docs/ui-agent-contract.md` before dashboard or app UI work. Use onlava-owned primitives and the @onlava registry; add registry components with commands such as `bun run shadcn:add @onlava/button`.
The browser UI harness is implemented; use it for dashboard route validation when UI behavior changes. Prefer `--write` when debugging so screenshots, DOM snapshots, console JSONL, and network JSONL are available under `.onlava/harness/ui/`.

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
onlava harness ui --json --write
```

## Debugging

```sh
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect paths --json
onlava logs --session current --jsonl --limit 200
onlava logs --session current --source api --level error --jsonl --limit 200
onlava inspect observability --json --session current
onlava logs query --json --session current --since 15m --query 'error OR panic'
onlava traces list --json --session current --since 15m --slowest
onlava metrics list --json --session current --since 1h
onlava metrics query --json --session current --since 15m --step 5s --promql 'onlava_request_duration_seconds'
onlava system toolchain list --json
onlava system toolchain verify --json
```

Onlava-managed tools live under `.onlava/toolchain/`, `~/.onlava/toolchain/` for machine-level edge tools, or `ONLAVA_TOOLCHAIN_DIR`. Treat managed dnsmasq, Caddy, Grafana, Victoria, and Temporal CLI details as substrate unless intentionally debugging them. Agents should not rely on system `PATH` binaries for those issues; use `onlava system toolchain sync --json` for app-root tools, `onlava system edge dns install` for wildcard local DNS, or `onlava system edge install` for Caddy edge. Shared substrate failures appear in `onlava ps --json` under `substrates` with `last_exit`, `component_exits`, and stdout/stderr log paths.

Do not introduce new onlava-owned production environment variables by default. Prefer `.onlava.json`, explicit CLI flags, or checked-in manifests; when an env variable is truly required, update `docs/environment.registry.json`, `docs/environment.md`, and tests together.

## Generated TypeScript Client

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava generate client --lang typescript --output ./src/onlava-client.ts
```

Regenerate committed clients after endpoint, request/response, auth, or wire-capability changes.

When an app configures `generators`, prefer:

```sh
onlava inspect generators --json
onlava generate --dry-run --json
onlava generate
```

Keep `onlava generate` for file generation only. `onlava generate sqlc` may refresh generated schema SQL and run `sqlc generate`, but it must not apply database schema or seed data.

Use `onlava db apply` for schema/app database mutation only. Use `onlava db seed` for initial data such as `SERVICE/db/seed.sql`; changed previously-applied seeds and destructive seed SQL fail closed with path/line diagnostics. Use `onlava db setup` for apply then seed. `onlava up` runs this setup lifecycle before app startup when DB setup inputs exist, then skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.

## Tasks

Use `onlava task` for configured repo tasks and app-local code tasks. Configured tasks use plain names from `.onlava.json`. Code tasks use `<domain>:<name>` and run from the app root without requiring the app model to parse cleanly.

```sh
onlava task list --json
onlava task inspect <target> --json
onlava task run <name>
onlava task run <domain>:<name> -- [task args...]
```

Single-file Go code tasks should live under a domain `tasks` directory and start with `//go:build ignore`, for example:

```text
billing/tasks/reconcile.task.go
billing/tasks/reconcile/main.go
```

## Command Reference

Use `docs/local-contract.md` for the full grammar. Common agent commands:

```text
onlava up [--app-root <path>] [--session <id>|--new-session] [--claim-aliases] [--json] [--detach]
onlava logs --follow [--app-root <path>] [--session current|<id>] [--jsonl|--json]
onlava console [--app-root <path>] [--session current|<id>]
onlava system edge install|trust|status|restart|uninstall|dns|privileged [--json]
onlava ps --json [--app-root <path>] [--session <id>] [--watch]
onlava down [--app-root <path>] [--session <id>]
onlava serve [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>]
onlava version --json
onlava system toolchain list [--json] [--include-source-locks] [--images]
onlava system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
onlava system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
onlava system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
onlava build [--app-root <path>] [-o <path>]
onlava check [--app-root <path>] --json
onlava generate [--app-root <path>] [--dry-run] [--json]
onlava task list [--app-root <path>] [--json]
onlava task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
onlava task run <name> [--app-root <path>]
onlava task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
onlava task graph --json [--app-root <path>]
onlava harness [--app-root <path>] --json --write
onlava harness self [--repo-root <path>] --json --write
onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|observability --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava traces list --json [--app-root <path>]
onlava metrics list --json [--app-root <path>]
onlava logs query [--app-root <path>] [--session current|<id>] --query <logsql> [--json]
onlava logs tail [--app-root <path>] [--session current|<id>] --query <logsql> [--since <duration>] [--jsonl]
onlava metrics query --json [--app-root <path>] [--session current|<id>] --promql <query>
onlava metrics labels --json [--app-root <path>] [--session current|<id>] [--match <selector>]
onlava metrics series --json [--app-root <path>] [--session current|<id>] --match <selector>
onlava traces clear --json [--app-root <path>]
onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
onlava db psql [--app-root <path>] [psql args...]
onlava db apply [--app-root <path>] [--json]
onlava db seed [--app-root <path>] [--dry-run] [--json]
onlava db setup [--app-root <path>] [--json]
onlava db reset|drop|snapshot [--app-root <path>]
```

## Validation Before Finishing

For app changes:

```sh
onlava check --json
go test ./...
onlava harness --json --write
```

For onlava repo changes:

```sh
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```
