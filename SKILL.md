---
name: scenery
description: Use when building, running, debugging, inspecting, validating, or generating clients for scenery applications. scenery is a Go-native service runtime and CLI using .scenery.json, //scenery directives, typed endpoints, local dev supervision, logs, traces, metrics, workers, and TypeScript client generation.
---

# scenery

scenery is a Go-native service runtime and local development platform. It runs app-root dev runtimes, exposes capabilities for inspection and action, and hides backing substrate details unless you intentionally debug them. Apps are ordinary Go modules with a `.scenery.json` app root and `//scenery:` directives in Go source.

This skill is the portable agent entrypoint. It teaches shared scenery behavior, but it does not replace app-local instructions. Client apps should also keep a small `AGENTS.md` with app root, frontend roots, generated client paths, required environment names, validation commands, and product invariants. In target apps, read the root `AGENTS.md` and every child `AGENTS.md` on the path to files you expect to touch before editing non-trivial changes.

Read next when needed:

- `docs/agent-guide.md` for agent workflow, capabilities, generated artifacts, and client-app integration.
- `docs/local-contract.md` for exact CLI grammar, JSON schemas, artifact paths, and stability labels.
- `docs/app-development-cookbook.md` for app recipes.
- `docs/ui-agent-contract.md` before UI work.

## Agent Fast Path

```sh
scenery check --json
scenery doctor --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery inspect wire --json
scenery system toolchain verify --json
scenery logs --jsonl --limit 200
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery harness --json --write
scenery validate quick --json --write
```

Prefer JSON output for agent decisions. Prefer `scenery up` for local development. Use `scenery serve` for headless API execution. Use `scenery task` for configured and code tasks. Use `scenery validate` for app-owned quality gates. Use `scenery worker` for worker-only cron/Temporal execution.

Run `scenery doctor --json` before deep app debugging when local readiness is in doubt. It is read-only and reports host resources, Go version, Docker engine reachability/details, optional tools, and app-sensitive dependency hints without building or starting services.

`scenery inspect docs --json` exposes `summary.review_due_count`, document-level `review_due` and `stale` fields, discovered `AGENTS.md` scopes, and Child Agent Index drift. For scenery repo changes, `scenery harness self --summary --write` surfaces those docs knowledge signals in compact validation summaries and leaves full evidence in `.scenery/harness/` artifacts. When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Mental Model

- `.scenery.json` marks the app root.
- App-required Go build tags or build-time flags belong in `.scenery.json` as `build.go_flags`, for example `["-tags=roofmapnet_native"]`; Scenery applies them to app builds and generated-workspace tests.
- Go source is the app model.
- `scenery up` starts the supervised local platform: app process, rebuild/restart loop, dashboard, API Explorer, logs, traces, metrics, managed dev services when configured, and optional frontend routing through the local agent.
- `scenery serve` starts a headless API-role server and does not start dashboard, proxy, or watch mode.
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

require scenery.sh v0.0.0
```

```go
package service

import "context"

type HelloResponse struct {
	Message string `json:"message"`
}

//scenery:api public path=/hello/:name method=GET
func Hello(ctx context.Context, name string) (*HelloResponse, error) {
	return &HelloResponse{Message: "hello " + name}, nil
}
```

```sh
scenery check --json
scenery serve
curl http://127.0.0.1:4000/hello/world
```

## Directives

```go
//scenery:api public|auth|private [raw] [path=/...] [method=GET,POST]
//scenery:service
//scenery:authhandler
```

Standard auth can be enabled from `.scenery.json` without app-local wrapper endpoints. Its tenant tables are framework-owned in PostgreSQL schema `scenery_auth` including `scenery_auth.tenants`; app-local `tenants` services or tables are product-domain concerns only.

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
- scenery tags: `scenery:"optional"`, `scenery:"httpstatus"`

## Public Go Packages

- `scenery.sh`
- `scenery.sh/auth`
- `scenery.sh/errs`
- `scenery.sh/middleware`
- `scenery.sh/temporal`
- `scenery.sh/cron`
- `scenery.sh/pgxpool`
- `scenery.sh/et`

## Local Development

```sh
scenery up
scenery up --json
scenery up --detach
scenery logs --follow
scenery console
scenery down
```

`scenery up --json` emits JSONL. `scenery up --detach` starts the app root's agent-backed dev runtime. `scenery logs --follow` follows that runtime's logs. `scenery console` opens the source-aware terminal console. Agent `routes` are canonical; configured friendly hosts appear separately in `aliases` only for the live app root that owns the free alias. If an app explicitly configures `proxy.route_base_domain`, `scenery up` requires the local edge to be ready and fails loudly with DNS, privileged listener, Caddy, and router diagnostics instead of publishing internal `:9440` router URLs as user-facing routes. Use `scenery up --claim-aliases` only for intentional live alias transfer.

Use `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, then `scenery system edge trust` when a browser needs trusted wildcard local HTTPS on `127.0.0.1:443`. The DNS command owns wildcard `local.dev` resolution through managed dnsmasq; the privileged helper owns only the default HTTPS loopback listener and forwards raw TCP to user-owned Caddy on an unprivileged loopback port. Do not run Caddy, the agent router, or `scenery system edge install` as root. `scenery system edge` uses managed dnsmasq and Caddy from the toolchain. `scenery system edge trust` uses a temporary admin-only Caddy process and does not require the port-443 edge to already be running.

For managed Postgres, app processes, setup commands, DB setup, and workers receive `DatabaseURL` as the app database authority. Scenery does not inject `DATABASE_URL` into those app-facing environments; treat `SCENERY_MANAGED_DATABASE_URL` as tooling/debug metadata. The shared Postgres substrate records only physical-server metadata; the runtime database URL/name is a runtime env lease, not a global substrate key. To use an explicit external DB with declared managed Postgres, set `SCENERY_DEV_POSTGRES_EXTERNAL=1` and provide `DatabaseURL`; `DATABASE_URL` is ignored.

For Electric-backed frontend writes, generated TypeScript `WithMeta` methods include parsed `txid` metadata. Use `observeAPIResponseTxid` around the app's Electric/TanStack observer so a post-commit sync timeout is reported as `SyncObservationError` instead of an API mutation failure.

## UI Work

Read `docs/ui-agent-contract.md` before dashboard or app UI work. Use scenery-owned primitives and the @scenery registry; add registry components with commands such as `bun run shadcn:add @scenery/button`.
The browser UI harness is implemented; use it for dashboard route validation when UI behavior changes. Prefer `--write` when debugging so screenshots, DOM snapshots, console JSONL, and network JSONL are available under `.scenery/harness/ui/`.

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
scenery harness ui --json --write
```

## Debugging

```sh
scenery check --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect paths --json
scenery logs --jsonl --limit 200
scenery logs --source api --level error --jsonl --limit 200
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery traces list --json --since 15m --slowest
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
scenery system toolchain list --json
scenery system toolchain verify --json
```

Scenery-managed tools live under `.scenery/toolchain/`, `~/.scenery/toolchain/` for machine-level edge tools, or `SCENERY_TOOLCHAIN_DIR`. Treat managed dnsmasq, Caddy, Grafana, Victoria, and Temporal CLI details as substrate unless intentionally debugging them. Agents should not rely on system `PATH` binaries for those issues; use `scenery system toolchain sync --json` for app-root tools, `scenery system edge dns install` for wildcard local DNS, or `scenery system edge install` for Caddy edge. Shared substrate failures appear in `scenery ps --json` under `substrates` with `last_exit`, `component_exits`, and stdout/stderr log paths.

Do not introduce new scenery-owned production environment variables by default. Prefer `.scenery.json`, explicit CLI flags, or checked-in manifests; when an env variable is truly required, update `docs/environment.registry.json`, `docs/environment.md`, and tests together.

## Generated TypeScript Client

```sh
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery inspect wire --json
scenery generate client --lang typescript --output ./src/scenery-client.ts
```

Regenerate committed clients after endpoint, request/response, auth, or wire-capability changes.
Generated model CRUD endpoints are beta and appear in `scenery inspect endpoints --json`
with `"generated": true`; generated stores use the app database selected by
`DatabaseURL` or Scenery's managed database env and target the app-owned service
schema rather than `public`. Generated CRUD route bases default to `/<service>/<table>`
and `scenery check` reports collisions with handwritten or generated app routes.
Generated Atlas HCL uses schema-qualified resource labels such as
`table "<service>" "<table>"` so app-owned schemas can coexist with handwritten
multi-schema HCL.
`scenery generate data --dry-run --json`
also writes beta generated frontend packages under `.scenery/gen/web/<frontend>/`
for configured frontends with static collection pages, including runtime adapter
factories and route registration helpers for app-owned Electric/TanStack/layout-kit wiring;
generated Electric shape metadata uses the same schema-qualified table as the DB artifacts.

When an app configures `generators`, prefer:

```sh
scenery inspect generators --json
scenery generate --dry-run --json
scenery generate
```

Keep `scenery generate` for file generation only. `scenery generate sqlc` may refresh generated schema SQL and run `sqlc generate`, but it must not apply database schema or seed data.

Use `scenery db apply` for schema/app database mutation only. Use `scenery db seed` for initial data such as `SERVICE/db/seed.sql` and generated model seed files under `.scenery/gen/db/<service>/seed.sql`; changed previously-applied seeds and destructive seed SQL fail closed with path/line diagnostics. Use `scenery db setup` for apply then seed. `scenery up` runs this setup lifecycle before app startup when DB setup inputs exist, then skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.

For managed Postgres branch work, use `scenery db postgres status --json` to inspect the shared local Postgres dev cell, `scenery db postgres start --json`/`stop --json` to manage it, `scenery db branch status --json` to inspect `.scenery/worktree-db.json`, and `scenery db branch list --json` to inspect Scenery-owned local branch leases in `branches.json` under the agent Postgres state root. The phase-one provider supports `dev.services.postgres.kind: "postgres"`, `mode: "local"`, `isolation: "database"`, and `branch_strategy: "template_database"`. `checkout` creates or reuses a branch database from the protected parent template database and records a ready endpoint without persisting raw connection URLs. `reset` recreates the branch from the parent template, `delete` drops the branch database and removes its lease, `expire` updates lease metadata, `prune` removes expired non-current branch databases when the Postgres admin substrate is reachable, and `restore` currently maps to template reset. `scenery up`, `scenery db psql`, DB setup, and Electric consume ready branch endpoints and fail explicitly when the lease is missing, expired, protected, or endpoint-less. The default `scenery harness self --json --write` path includes the live Postgres branch lifecycle proof; use `--quick` for the smaller self-harness mode.

## Tasks

Use `scenery task` for configured repo tasks and app-local code tasks. Configured tasks use plain names from `.scenery.json`. Code tasks use `<domain>:<name>` and run from the app root without requiring the app model to parse cleanly.

```sh
scenery task list --json
scenery task inspect <target> --json
scenery task run <name>
scenery task run <domain>:<name> -- [task args...]
```

Single-file Go code tasks should live under a domain `tasks` directory and start with `//go:build ignore`, for example:

```text
billing/tasks/reconcile.task.go
billing/tasks/reconcile/main.go
```

## Command Reference

Use `docs/local-contract.md` for the full grammar. Common agent commands:

```text
scenery up [--app-root <path>] [--claim-aliases] [--json] [--detach]
scenery logs --follow [--app-root <path>] [--jsonl|--json]
scenery console [--app-root <path>]
scenery system edge install|trust|status|restart|uninstall|dns|privileged [--json]
scenery help <command>|all|--json
scenery ps [--json] [--app-root <path>] [--watch]
scenery down [--app-root <path>] [--json]
scenery serve [--app-root <path>] [--env <name>] [--log-format text|json]
scenery worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>]
scenery version --json
scenery system toolchain list [--json] [--include-source-locks] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
scenery system toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
scenery system toolchain path [--json] --tool <name> [--platform <goos/goarch>]
scenery build [--app-root <path>] [-o <path>]
scenery check [--app-root <path>] --json
scenery generate [--app-root <path>] [--dry-run] [--json]
scenery task list [--app-root <path>] [--json]
scenery task inspect <target> [--app-root <path>] [--lang go|typescript] [--json]
scenery task run <name> [--app-root <path>]
scenery task run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<name> [-- task args...]
scenery task graph --json [--app-root <path>]
scenery validate [<profile>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery validate list [--app-root <path>] [--json]
scenery validate inspect <profile> [--app-root <path>] [--json]
scenery validate graph [<profile>] [--app-root <path>] --json
scenery validate changed [--base <ref>] [--app-root <path>] [--json] [--write] [--dry-run]
scenery harness [--app-root <path>] --json --write
scenery harness self [--repo-root <path>] --summary --write [--fresh-tests]
scenery inspect app|routes|services|endpoints|models|views|wire|build|paths|generators|temporal|observability --json [--app-root <path>]
scenery inspect docs --json [--repo-root <path>]
scenery traces list --json [--app-root <path>]
scenery metrics list --json [--app-root <path>]
scenery logs query [--app-root <path>] --query <logsql> [--json]
scenery logs tail [--app-root <path>] --query <logsql> [--since <duration>] [--jsonl]
scenery metrics query --json [--app-root <path>] --promql <query>
scenery metrics labels --json [--app-root <path>] [--match <selector>]
scenery metrics series --json [--app-root <path>] --match <selector>
scenery traces clear --json [--app-root <path>]
scenery logs [--app-root <path>] [--limit <n>] [--jsonl|--json]
scenery test [--app-root <path>] [go test flags/packages...]
scenery generate client [<app-id>] --lang typescript --output <path> [--app-root <path>]
scenery db psql [--app-root <path>] [psql args...]
scenery db apply [--app-root <path>] [--json]
scenery db seed [--app-root <path>] [--dry-run] [--json]
scenery db setup [--app-root <path>] [--json]
scenery db reset|drop|snapshot [--app-root <path>]
scenery db branch status|list [--app-root <path>] [--json]
scenery db branch checkout <name> [--app-root <path>] [--json]
scenery db branch reset [--app-root <path>] [--yes]
scenery db branch delete <name> [--app-root <path>] [--force]
scenery db branch restore --at <timestamp-or-lsn> [--app-root <path>] [--yes]
scenery db branch diff <branch> [--app-root <path>] [--json]
scenery db branch expire [<name>] --after <duration> [--app-root <path>] [--json]
scenery db branch prune [--older-than <duration>] [--app-root <path>] [--json]
scenery db postgres install|start|status|logs|stop|restart|uninstall [--json]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [--json]
scenery worktree list [--app-root <path>] [--json]
scenery worktree remove <name> [--app-root <path>] [--db] [--json]
```

Self-harness Go test steps use the Go test result cache by default. Pass
`--fresh-tests` when a fresh `-count=1` run is intentionally required.

## Validation Before Finishing

For app changes:

```sh
scenery check --json
go test ./...
scenery harness --json --write
scenery validate quick --json --write
```

For scenery repo changes:

```sh
go test ./...
go test ./cmd/scenery
scenery harness self --summary --write
```

Do not run `go install ./cmd/scenery` unless the human explicitly asks. Multiple
worktrees can share one installed binary; self-harness builds a worktree-local
`.scenery/harness/bin/scenery` for freshness checks.
