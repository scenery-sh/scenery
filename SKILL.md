---
name: onlava
description: Use when building, running, debugging, inspecting, or generating clients for onlava applications. onlava is a Go-native service runtime and CLI using .onlava.json, //onlava directives, typed endpoints, local dev supervision, logs, traces, metrics, DB Studio, psql, and TypeScript client generation.
---

# onlava

onlava is a Go-native service runtime and CLI. Applications are ordinary Go modules with a `.onlava.json` file at the app root and `//onlava:` directives in Go source.

This skill is for using onlava in applications. Install it with:

```sh
npx skills add https://github.com/pbrazdil/onlava
```

This installs the agent skill, not the onlava CLI. The `onlava` binary must also be available on `PATH`. If it is missing, install it from the onlava source checkout as described in the repository README.

Read next when you need detail:

- `docs/local-contract.md` for CLI, JSON, generated artifact, and stability contracts.
- `docs/app-development-cookbook.md` for app-building recipes.
- `docs/data-platform.md` and `docs/data-platform-runbook.md` for dynamic data.
- `docs/ui-agent-contract.md` for dashboard/UI work and `@onlava` registry rules.
- `docs/plans/active.md` before substantial changes.

## Mental Model

- `.onlava.json` marks the app root and names the app.
- Go source is the app model. onlava discovers services, APIs, auth handlers, middleware, Pub/Sub handlers, and cron jobs from code.
- `onlava run` builds once and starts one headless local HTTP server.
- `onlava dev` starts the full local development platform: app process, file watching, rebuild/restart supervision, dashboard, API explorer, MCP endpoint, DB Studio, logs, traces, metrics, and optional HTTPS local domains.
- Public and auth endpoints are reachable over external HTTP. Private endpoints are internal-only and called through generated helpers.
- Typed endpoints decode path params, query params, headers, cookies, and JSON bodies into Go values, then encode typed responses.
- Generated internal calls preserve routing, private access, and auth context instead of bypassing the runtime.
- The beta `github.com/pbrazdil/onlava/data` package exposes metadata-defined dynamic objects backed by real PostgreSQL tables, columns, indexes, outbox rows, and live events.
- The local dashboard includes API Explorer, traces, metrics, DB Studio, Pub/Sub/cron surfaces, Data Explorer, and browser-harness validation hooks.

## Minimal App

Create `.onlava.json`:

```json
{"name":"hello"}
```

Create `go.mod`:

```go
module example.com/hello

go 1.26.0

require github.com/pbrazdil/onlava v0.0.0
```

Create `service/api.go`:

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

Validate and run:

```sh
onlava check --json
onlava run
curl http://127.0.0.1:4000/hello/world
```

## Directives

```go
//onlava:api public|auth|private [raw] [path=/...] [method=GET,POST]
//onlava:service
//onlava:authhandler
```

Typed API signature:

```go
func Endpoint(ctx context.Context, pathParam string, req *Request) (*Response, error)
```

Raw API signature:

```go
func Endpoint(w http.ResponseWriter, req *http.Request)
```

Route defaults:

- Default path is `/<service>.<Endpoint>`.
- Typed endpoint default methods are `GET,POST` when no payload exists.
- Typed endpoint default method is `POST` when a payload exists.
- Raw endpoint default method is wildcard.

Struct tags:

- Request decoding: `json`, `header`, `query`, `qs`, `cookie`.
- onlava tags: `onlava:"optional"` and `onlava:"httpstatus"`.

## Public Go Packages

- `github.com/pbrazdil/onlava`: app metadata and `CurrentRequest()`.
- `github.com/pbrazdil/onlava/auth`: request auth helpers such as `UserID()`, `Data()`, `CurrentAuthData()`, and the standard auth module.
- `github.com/pbrazdil/onlava/errs`: coded errors and HTTP status mapping.
- `github.com/pbrazdil/onlava/middleware`: middleware request/response types.
- `github.com/pbrazdil/onlava/pubsub`: local Pub/Sub declarations and runtime integration.
- `github.com/pbrazdil/onlava/cron`: cron job declarations.
- `github.com/pbrazdil/onlava/pgxpool`: pgx pool wrapper with onlava DB tracing.
- `github.com/pbrazdil/onlava/et`: endpoint and service mocking helpers for tests.
- `github.com/pbrazdil/onlava/data`: beta dynamic data platform for metadata-defined objects, fields, records, indexes, relations, saved views, import/export, search, outbox, and SSE live updates.

## Auth

Use `//onlava:authhandler` for request authentication. Auth handlers may be package functions or service methods.

Token-style auth handlers can accept a token string. Structured auth handlers can decode auth params from `header`, `query`, `qs`, and `cookie` tags.

For the built-in standard auth module, enable auth in `.onlava.json`:

```json
{
  "name": "hello",
  "auth": {
    "enabled": true,
    "database_url_env": "DatabaseURL",
    "dev_bootstrap": { "enabled": true }
  }
}
```

Standard auth registers `/auth/*` endpoints plus local `/users/dev-bootstrap`, stores DB-backed state in PostgreSQL schema `onlava_auth`, and returns `*auth.AuthData` from `auth.CurrentAuthData()`.

Inside auth-protected endpoints, use:

```go
import "github.com/pbrazdil/onlava/auth"

userID := auth.UserID()
data := auth.Data()
standardData, ok := auth.CurrentAuthData()
```

Use `//onlava:api auth` for endpoints that require auth. Use `//onlava:api private` for internal-only endpoints.

For data-platform apps, standard auth can scope requests to a data tenant:

```go
store, err := data.Open(ctx, pool, data.Options{
	Permissions: data.StandardAuthPermissions{},
})
tenantKey, err := data.RequireTenantKeyFromContext(ctx)
actor := data.ActorFromContext(ctx)
```

`StandardAuthPermissions` maps standard-auth `tenant_id` to `data.TenantKey` and denies cross-tenant data access by default.

## Services

Use `//onlava:service` when endpoints are methods on a service struct.

```go
//onlava:service
type Service struct {
	// dependencies
}

func initService() (*Service, error) {
	return &Service{}, nil
}
```

onlava initializes service structs and wraps methods so endpoint calls still pass through runtime semantics.

## Data Platform

Use `github.com/pbrazdil/onlava/data` when an app needs dynamic, metadata-defined records backed by PostgreSQL.

```go
store, err := data.Open(ctx, pool, data.Options{})
actor := data.ActorFromContext(ctx)

_, err = store.CreateObject(ctx, actor, data.CreateObjectRequest{
	TenantKey:    "acme",
	NameSingular: "company",
	NamePlural:   "companies",
})

_, err = store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
	TenantKey: "acme",
	Name:      "name",
	Type:      data.FieldText,
})

_, err = store.CreateRecord(ctx, actor, "company", data.CreateRecordRequest{
	TenantKey: "acme",
	Values: data.Record{
		"name": "Acme",
	},
})

page, err := store.QueryRecords(ctx, actor, "company", data.QueryRecordsRequest{
	TenantKey: "acme",
	Query: data.Query{
		Select: []string{"name"},
		Filter: data.EQ("name", "Acme"),
		Sort:   []data.Sort{data.Asc("name")},
		Limit:  50,
	},
})
```

Current beta capabilities include:

- Metadata tenants, objects, fields, field options, and real PostgreSQL schema changes.
- Scalar fields backed by real columns, not a universal JSONB value table.
- Select and multi-select stored as text/text arrays, not PostgreSQL enum types.
- Indexes, one-hop many-to-one relations, many-to-many physical foundations, saved views, keyset cursors, and PostgreSQL-backed search.
- Transactional outbox, optional trigger-backed outbox, SSE live updates, import/export, and typed public errors through `data.CodeOf(err)`.

Inspect data-platform state:

```sh
onlava inspect data --json --database-url "$DATABASE_URL"
onlava inspect data --json --database-url "$DATABASE_URL" --tenant acme --object company
```

Use `docs/data-platform.md` for the overview and `docs/data-platform-runbook.md` for operational workflows and current beta limitations.

## Errors And Responses

Use `github.com/pbrazdil/onlava/errs` for coded errors that map cleanly to HTTP responses.

Use `onlava:"httpstatus"` on a response struct field when the endpoint should return a non-default HTTP status.

Use `onlava.CurrentRequest()` when an endpoint needs request metadata such as method, path, service, endpoint, path params, or payload metadata:

```go
import onlava "github.com/pbrazdil/onlava"
```

## Local Development

Start the local dev platform:

```sh
onlava dev
```

Common modes:

```sh
onlava dev --json
onlava dev --proxy
onlava dev --proxy --trust
onlava dev --port 4000 --listen 127.0.0.1
```

Use `onlava dev --json` for machine-readable JSONL events. Child stdout/stderr are emitted as structured process output events.

`onlava dev` prints the API URL, dashboard URL, MCP SSE URL, and DB Studio URL when enabled. Use `-v` or `--verbose` when you also need Victoria sidecar URLs and sidecar lifecycle output.

Use `onlava dev --proxy` for local HTTPS/frontend domains from `.onlava.json` proxy config:

```json
{
  "name": "myapp",
  "proxy": {
    "workspace": "acme",
    "api_host": "api.acme.localhost",
    "console_host": "console.acme.localhost",
    "mcp_host": "mcp.acme.localhost",
    "frontends": {
      "app": {
        "host": "app.acme.localhost",
        "root": "apps/app"
      }
    }
  }
}
```

Use `onlava dev --proxy --trust` only when installing or updating the local development CA. Once trusted, onlava should not need trust-store permission on every startup.

## Debugging

First checks:

```sh
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava logs --limit 200
onlava logs --jsonl --limit 200
```

Traces and metrics:

```sh
onlava inspect traces --json --since 15m --slowest
onlava inspect traces --json --trace-id <trace-id>
onlava inspect metrics --json --since 1h
```

Harness snapshot:

```sh
onlava harness --json --write
```

Dashboard browser checks:

```sh
onlava harness ui --json
onlava harness ui --json --headed
```

The browser UI harness visits dashboard routes, checks stable DOM markers, captures screenshots, and records console/network diagnostics under `.onlava/harness/ui/`.

If rebuilds do not happen in `onlava dev`, check whether the changed files are under the discovered app root, whether they are ignored/generated paths, and whether the process output shows a compile or restart error.

If local proxy requests hang, check long-lived SSE streams and browser per-host connection limits. Prefer HTTP/2-capable local HTTPS proxy paths for many concurrent streams.

If the proxy says the upstream is unavailable, confirm the app child process is still listening on the API address shown by `onlava dev`, then inspect `onlava logs` and the dashboard process output.

## Observability

`onlava dev` writes local logs and traces. When available, it also runs VictoriaMetrics, VictoriaLogs, and VictoriaTraces sidecars for richer local inspection.

Useful environment flags:

```sh
ONLAVA_DEV_VICTORIA=0
ONLAVA_DEV_VICTORIA_DOWNLOAD=0
```

Victoria sidecars store data under `.onlava/victoria/` by default and stop with the dev supervisor.

## Secrets And Environment

- Process environment wins over local files.
- Local runtime reads `.env` from the app root when a value is not already set.
- `onlava dev` loads `.env` first and `.env.local` second.
- `.env.local` overrides `.env` only for keys not already present in the parent process environment.
- `onlava run --env production` fails before serving if a declared secret is missing from process env or `.env`.

## Database And psql

Use:

```sh
onlava psql [psql args...]
```

Use `onlava inspect app --json` and `onlava inspect paths --json` to understand app configuration and local generated paths before debugging DB access.

DB Studio is available through `onlava dev` when configured.

## Dashboard And Data Explorer

The local dashboard is for development and inspection, not production hosting. Use it to inspect routes, endpoint calls, traces, logs, metrics, DB Studio, Pub/Sub/cron state, and the beta Data Explorer.

Data Explorer is a developer-facing view over `onlava inspect data` semantics: tenants, objects, fields, records, migrations, outbox, triggers, indexes, relations, saved views, and search state. Treat it as a debugging surface; app code should still use `github.com/pbrazdil/onlava/data`.

## UI Development

Agents must compose UI from onlava-owned primitives and layouts. Use the @onlava registry as the only approved shadcn registry input. Do not paste arbitrary shadcn/Tailwind code into dashboard or app screens.

Allowed:

- `ui/src/components/primitives/*`
- `ui/src/components/layouts/*`
- `ui/src/features/*` composed from primitives/layouts
- `@onlava/*` registry items

Forbidden:

- direct imports from `ui/src/components/vendor/shadcn`
- raw `shadcn add` commands
- direct `@radix-ui/*` imports outside primitives
- direct Tailwind class soup in routes/pages

Install approved registry items only through the wrapper:

```sh
cd ui
bun run shadcn:add @onlava/<item>
```

The wrapper rejects non-`@onlava` items, URLs, unsafe flags, and unknown local registry servers. Read `docs/ui-agent-contract.md` before changing UI.

ONLV/Pulse layout migration work should move generic layout source of truth into onlava layouts and registry items while preserving ONLV visuals. Keep ONLV product logic in ONLV.

## TypeScript Client Generation

Generate a TypeScript client:

```sh
onlava gen client --lang typescript --output ./src/onlava-client.ts
```

The generated client understands the app route model and local wire capabilities. Inspect wire support with:

```sh
onlava inspect wire --json
```

## Pub/Sub, Cron, Middleware

onlava discovers Pub/Sub handlers, cron jobs, and middleware from Go source. Treat local Pub/Sub and cron dev/admin UI affordances as beta until lifecycle, retry, scheduling, and clear/delete semantics are frozen for the app you are working on.

When changing these areas, validate with:

```sh
onlava check --json
onlava harness --json --write
```

## Command Reference

```text
onlava dev [--port <n>] [--listen <addr>] [--app-root <path>] [-v|--verbose] [--json] [--proxy] [--trust]
onlava run [--port <n>] [--listen <addr>] [--app-root <path>] [--env <name>] [--log-format text|json]
onlava version [--json]
onlava build [--app-root <path>] [-o <path>] [--db-studio]
onlava check [--app-root <path>] [--json]
onlava harness [--app-root <path>] [--json] [--write]
onlava harness ui [--app-root <path>] [--repo-root <path>] [--json] [--headed]
onlava inspect app|routes|services|endpoints|wire|build|paths|traces|metrics --json [--app-root <path>]
onlava inspect data --json --database-url <url> [--tenant <key>] [--object <name>]
onlava inspect docs --json [--repo-root <path>]
onlava logs [--app-root <path>] [--limit <n>] [--stream all|stdout|stderr] [-f|--follow] [--jsonl|--json]
onlava test [--app-root <path>] [go test flags/packages...]
onlava gen client [<app-id>] --lang typescript --output <path> [--app-root <path>]
onlava psql [--app-root <path>] [psql args...]
```

## Validation Before Finishing App Work

For most app changes:

```sh
onlava check --json
go test ./...
```

For broader app changes:

```sh
onlava harness --json --write
```

For onlava repo changes:

```sh
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

For UI changes in the onlava dashboard:

```sh
cd ui
bun run typecheck
bun run test
bun run build
cd ..
onlava harness self --json --write
```

For data-platform work:

```sh
go test ./data ./internal/objectstore ./internal/datainspect
ONLAVA_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/objectstore ./internal/datainspect
onlava inspect data --json --database-url "$DATABASE_URL"
```

For generated client changes:

```sh
onlava gen client --lang typescript --output <expected-output>
```
