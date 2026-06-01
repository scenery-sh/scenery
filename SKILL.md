---
name: onlava
description: Use when building, running, debugging, inspecting, validating, or generating clients for onlava applications. onlava is a Go-native service runtime and CLI using .onlava.json, //onlava directives, typed endpoints, local dev supervision, MCP, logs, traces, metrics, workers, and TypeScript client generation.
---

# onlava

onlava is a Go-native service runtime and local development platform. It runs app sessions, exposes capabilities for inspection and action, and hides backing substrate details unless you intentionally debug them. Apps are ordinary Go modules with a `.onlava.json` app root and `//onlava:` directives in Go source.

This skill is the portable agent entrypoint. It teaches shared onlava behavior, but it does not replace app-local instructions. Client apps should also keep a small `AGENTS.md` with app root, frontend roots, generated client paths, required environment names, validation commands, and product invariants.

Read next when needed:

- `docs/agent-guide.md` for agent workflow, MCP, capabilities, generated artifacts, and client-app integration.
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
onlava toolchain verify --json
onlava logs --jsonl --limit 200
onlava harness --json --write
```

Prefer JSON output for agent decisions. Prefer `onlava dev` for local development. Use `onlava serve` for headless API execution. Use `onlava run` for app-local operational scripts. Use `onlava worker` for worker-only cron/Temporal execution.

Run `onlava doctor --json` before deep app debugging when local readiness is in doubt. It is read-only and reports host resources, Go version, optional tools, and app-sensitive dependency hints without building or starting services.

## Mental Model

- `.onlava.json` marks the app root.
- Go source is the app model.
- `onlava dev` starts the supervised local platform: app process, rebuild/restart loop, dashboard, API Explorer, MCP endpoint, logs, traces, metrics, managed dev services when configured, and optional frontend/proxy routing.
- `onlava serve` starts a headless API-role server and does not start dashboard, MCP, proxy, or watch mode.
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
onlava dev
onlava dev --json
onlava dev --detach
onlava attach
onlava attach --tui
onlava console
onlava down
```

`onlava dev --json` emits JSONL. `onlava dev --detach` starts an agent-backed app session. `onlava attach` follows session logs. `onlava attach --tui` and `onlava console` open the source-aware terminal console.

## UI Work

Read `docs/ui-agent-contract.md` before dashboard or app UI work. Use onlava-owned primitives and the @onlava registry; add registry components with commands such as `bun run shadcn:add @onlava/button`.

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
onlava harness ui --json
```

## MCP

`onlava dev` exposes MCP as an app-session capability and prints the `MCP SSE URL`. Use MCP for interactive local inspection when a dev session is running. Use CLI JSON and schemas for stable automation.

## Debugging

```sh
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava inspect paths --json
onlava logs --session current --jsonl --limit 200
onlava logs --session current --source api --level error --jsonl --limit 200
onlava inspect traces --json --session current --since 15m --slowest
onlava inspect metrics --json --session current --since 1h
onlava toolchain list --json
onlava toolchain verify --json
```

Onlava-managed tools live under `.onlava/toolchain/` or `ONLAVA_TOOLCHAIN_DIR`. Treat managed Grafana, Victoria, and Temporal CLI details as substrate unless intentionally debugging them. Agents should not rely on system `PATH` binaries for those issues; use `onlava toolchain sync --json` or an explicit per-tool env override.

Do not introduce new onlava-owned production environment variables by default. Prefer `.onlava.json`, explicit CLI flags, or checked-in manifests; when an env variable is truly required, update `docs/environment.registry.json`, `docs/environment.md`, and tests together.

## Generated TypeScript Client

```sh
onlava inspect endpoints --json
onlava inspect wire --json
onlava gen client --lang typescript --output ./src/onlava-client.ts
```

Regenerate committed clients after endpoint, request/response, auth, or wire-capability changes.

When an app configures `generators`, prefer:

```sh
onlava inspect generators --json
onlava generate --dry-run --json
onlava generate
```

Use `onlava db sync` for configured database mutation plus dependent SQLC regeneration; keep `onlava generate` for file generation only.

## Operational Scripts

Use `onlava run` for app-local operational scripts that should run from the app root without requiring the app model to parse cleanly.

```sh
onlava run list --json
onlava run inspect <domain>:<script> --json
onlava run <domain>:<script> [script args...]
```

Single-file Go scripts should live under a domain `scripts` directory and start with `//go:build ignore`, for example:

```text
billing/scripts/reconcile.script.go
billing/scripts/reconcile/main.go
```

## Command Reference

Use `docs/local-contract.md` for the full grammar. Common agent commands:

```text
onlava dev [--app-root <path>] [--session <id>|--new-session] [--json] [--detach]
onlava attach [--app-root <path>] [--session current|<id>] [--jsonl|--json] [--tui]
onlava console [--app-root <path>] [--session current|<id>]
onlava status --json [--app-root <path>] [--session <id>] [--watch]
onlava down [--app-root <path>] [--session <id>]
onlava serve [--app-root <path>] [--env <name>] [--log-format text|json]
onlava worker [--task-queue <name>[,<name>...]]... [--app-root <path>] [--env <name>]
onlava version --json
onlava toolchain list [--json] [--include-source-locks] [--images]
onlava toolchain sync [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images]
onlava toolchain verify [--json] [--all] [--tool <name>] [--platform <goos/goarch>] [--images] [--strict]
onlava toolchain path [--json] --tool <name> [--platform <goos/goarch>]
onlava build [--app-root <path>] [-o <path>]
onlava check [--app-root <path>] --json
onlava generate [--app-root <path>] [--dry-run] [--json]
onlava task list [--app-root <path>] [--json]
onlava task run <name> [--app-root <path>]
onlava task graph --json [--app-root <path>]
onlava run list [--app-root <path>] [--json]
onlava run inspect <domain>:<script> [--app-root <path>] [--lang go|typescript] [--json]
onlava run [--app-root <path>] [--env <name>] [--lang go|typescript] <domain>:<script> [script args...]
onlava harness [--app-root <path>] --json --write
onlava harness self [--repo-root <path>] --json --write
onlava inspect app|routes|services|endpoints|wire|build|paths|generators|temporal|traces|metrics --json [--app-root <path>]
onlava inspect docs --json [--repo-root <path>]
onlava logs [--app-root <path>] [--session current|<id>] [--limit <n>] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]
onlava db psql [--app-root <path>] [psql args...]
onlava db sync [--app-root <path>]
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
