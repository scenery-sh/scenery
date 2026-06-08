# onlava App Development Cookbook

This cookbook is the practical "how do I build this?" companion to `docs/local-contract.md`. The local contract is the source of truth for exact CLI and JSON contracts; this file gives agents and developers common recipes.

## Minimal App

Create `.onlava.json`:

```json
{"name":"hello"}
```

Create `go.mod`:

```go
module example.com/hello

go 1.26.3

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

Validate:

```sh
onlava check --json
onlava serve
curl http://127.0.0.1:4000/hello/world
```

Common failure: `onlava check` cannot find the app. Run it from the app root or pass `--app-root`.

## Typed Public Endpoint

Typed endpoints accept path parameters, request structs, and return typed JSON responses.

```go
type CreateThingRequest struct {
	Name string `json:"name"`
}

type CreateThingResponse struct {
	ID string `json:"id"`
}

//onlava:api public path=/things method=POST
func CreateThing(ctx context.Context, req *CreateThingRequest) (*CreateThingResponse, error) {
	return &CreateThingResponse{ID: req.Name}, nil
}
```

Validate:

```sh
onlava check --json
curl -X POST http://127.0.0.1:4000/things -d '{"name":"alpha"}'
```

Common failure: missing pointer request or unsupported signature. Check `onlava inspect endpoints --json`.

## Auth Endpoint

Enable standard auth in `.onlava.json`:

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

Use auth-protected APIs:

```go
package service

import (
	"context"

	"github.com/pbrazdil/onlava/auth"
)

type MeResponse struct {
	UserID string `json:"user_id"`
}

//onlava:api auth path=/me method=GET
func Me(ctx context.Context) (*MeResponse, error) {
	uid, _ := auth.UserID()
	return &MeResponse{UserID: string(uid)}, nil
}
```

Validate:

```sh
onlava check --json
onlava serve
curl -X POST http://127.0.0.1:4000/users/dev-bootstrap
```

Common failure: `DatabaseURL` is missing. Put it in process env or an app-root `.env.local` for local development.

Standard auth owns its tenant state in `onlava_auth.tenants`. You do not need an app-local `tenants` service or table to use standard auth; create one only for product-domain tenant APIs or schema.

## Private Endpoint Call

Private endpoints are internal-only and should be called through generated helpers from other onlava endpoints. Do not expose private APIs over external HTTP.

```go
//onlava:api private
func Compute(ctx context.Context) (*ComputeResponse, error) {
	return &ComputeResponse{Value: 42}, nil
}
```

Validate:

```sh
onlava check --json
onlava inspect routes --json
```

Common failure: raw endpoints cannot be called through internal service-to-service helpers in the current contract.

## Service Struct Initialization

Use `//onlava:service` when endpoints are methods on a struct with dependencies.

```go
//onlava:service
type Service struct {
	prefix string
}

func initService() (*Service, error) {
	return &Service{prefix: "hello"}, nil
}

//onlava:api public path=/hello method=GET
func (s *Service) Hello(ctx context.Context) (*HelloResponse, error) {
	return &HelloResponse{Message: s.prefix}, nil
}
```

Validate:

```sh
onlava check --json
go test ./...
```

Common failure: nested services are invalid. Keep one service root per package/service area.

## Middleware

Use `github.com/pbrazdil/onlava/middleware` for app middleware. Start from `testdata/apps/middleware` before writing new patterns.

Validate:

```sh
onlava check --app-root testdata/apps/middleware --json
go test ./internal/parse ./internal/codegen ./runtime
```

Common failure: middleware order or scope is unclear. Inspect the generated app model with `onlava inspect app --json`.

## Request Decoding Tags

Supported request tags:

```text
json
header
query
qs
cookie
onlava:"optional"
```

Example:

```go
type SearchRequest struct {
	Query string `query:"q"`
	Token string `header:"authorization" onlava:"optional"`
}
```

Validate:

```sh
onlava inspect endpoints --json
```

Common failure: forgetting `onlava:"optional"` for values that may be absent.

## HTTP Status Responses

Use `onlava:"httpstatus"` on a response field:

```go
type CreatedResponse struct {
	Status int    `json:"-" onlava:"httpstatus"`
	ID     string `json:"id"`
}
```

Common failure: returning a status field in JSON accidentally. Use `json:"-"` when the field should only control HTTP status.

## Coded Errors

Use `github.com/pbrazdil/onlava/errs` for HTTP-aware coded errors.

```go
return nil, errs.NotFound("thing not found")
```

Validate error mappings with endpoint tests or `curl`.

## Request And Auth Context

Use:

```go
meta := onlava.CurrentRequest()
uid, ok := auth.UserID()
standard, ok := auth.CurrentAuthData()
```

Common failure: relying on globals outside request handling. Pass context or actor values explicitly to lower layers.

## Temporal Workflow Or Activity

Use `github.com/pbrazdil/onlava/temporal` for beta workflow and activity declarations. Packages that call `temporal.NewWorkflow` or `temporal.NewActivity` are imported by generated main so worker processes can register them. Set `temporal.enabled: true` in `.onlava.json` to opt in; Temporal remains off when the field is omitted, even if declarations or TypeScript worker settings are present. Use `onlava up` for local combined API/worker execution, and use `onlava worker` for worker-only processes. Set `ActivityConfig.MaxConcurrency` when a dedicated task queue should cap concurrent activity executions for resource-heavy work, and pass `temporal.WithHeartbeatTimeout(...)` when a workflow activity needs a heartbeat timeout.

## Cron Job

Use `github.com/pbrazdil/onlava/cron` and see `testdata/apps/cron`. When Temporal is enabled, cron jobs run through Temporal Schedules. Set `OverlapPolicy`, `CatchupWindow`, `PauseOnFailure`, `ActivityStartToClose`, and `ActivityRetryPolicy` on `cron.JobConfig` when missed-run, overlap, timeout, or retry behavior must be explicit.

```go
package jobs

import (
	"context"
	"time"

	"github.com/pbrazdil/onlava/cron"
)

var _ = cron.NewJob("nightly-sync", cron.JobConfig{
	Every:                cron.Hour,
	Endpoint:             syncNightly,
	OverlapPolicy:        cron.OverlapSkip,
	CatchupWindow:        10 * time.Minute,
	PauseOnFailure:       true,
	ActivityStartToClose: 15 * time.Minute,
	ActivityRetryPolicy: cron.RetryPolicy{
		InitialInterval: time.Second,
		MaximumAttempts: 3,
	},
})

func syncNightly(ctx context.Context) error {
	return nil
}
```

Validate:

```sh
onlava check --app-root testdata/apps/cron --json
go test ./cron ./internal/parse ./internal/codegen
```

Common failure: relying on wall-clock behavior in unit tests. Keep cron tests deterministic.

## pgxpool Tracing

Use `github.com/pbrazdil/onlava/pgxpool` when you want PostgreSQL operations to appear in onlava local traces.

Validate:

```sh
onlava traces list --json --since 15m
onlava metrics list --json --since 1h
onlava metrics query --json --since 15m --step 5s --promql 'onlava_request_duration_seconds'
```

Common failure: using a raw pool in app code and then expecting DB spans in the dashboard.

## TypeScript Client Generation

Generate a client:

```sh
onlava generate client --lang typescript --output ./src/onlava-client.ts
```

If `.onlava.json` declares `generators.clients`, inspect and run the configured graph:

```sh
onlava inspect generators --json
onlava generate --dry-run --json
onlava generate client
```

Inspect wire support:

```sh
onlava inspect wire --json
```

Common failure: committing generated clients without regenerating after endpoint changes.

## Code Tasks

Use `onlava task` for app-local code tasks that should run from the app root without requiring the app model to parse cleanly.

Code task targets use `<domain>:<name>`, and both segments must match `[A-Za-z0-9_][A-Za-z0-9_-]*`.

Single-file Go tasks live under a domain's `tasks` directory and must start with `//go:build ignore`:

```go
//go:build ignore

package main

import "fmt"

func main() {
	fmt.Println("reconcile")
}
```

```text
billing/tasks/reconcile.task.go
```

Run it:

```sh
onlava task run billing:reconcile -- --dry-run
```

Use a directory for larger Go tasks:

```text
billing/tasks/reconcile/main.go
billing/tasks/reconcile/helpers.go
```

TypeScript tasks use the same namespace:

```text
billing/tasks/reconcile.task.ts
billing/tasks/reconcile/index.ts
```

List and inspect tasks:

```sh
onlava task list --json
onlava task inspect billing:reconcile --json
```

Common failure: putting two single-file Go tasks with `package main` in the same directory without `//go:build ignore`. Normal Go package loading may see both files before onlava can filter anything. Use the build tag for `*.task.go`, or use a per-task directory.

## Validation Profiles

Use `validation` profiles in `.onlava.json` when an app has quality gates beyond the core framework harness:

```json
{
  "tasks": {
    "repo-harness": { "run": "go run ./cmd/repoharness" },
    "web-typecheck": { "cwd": "apps/web", "run": "bun run typecheck" }
  },
  "validation": {
    "default": "quick",
    "profiles": {
      "quick": {
        "description": "Fast handoff gate.",
        "cost": "low",
        "steps": ["harness:core", "task:repo-harness"]
      },
      "frontend": {
        "description": "Frontend validation.",
        "cost": "medium",
        "paths": ["apps/web/**"],
        "steps": ["task:web-typecheck"]
      },
      "full": {
        "description": "Full local quality gate.",
        "cost": "high",
        "steps": ["profile:quick", "profile:frontend"]
      }
    }
  }
}
```

Agents can inspect and run these gates without scraping repo-specific prose:

```sh
onlava inspect validation --json
onlava validate quick --json --write
onlava validate changed --base origin/main --json --write
onlava validate full --dry-run --json
```

## Configured SQLC And DB Lifecycle

Use `onlava generate sqlc` for file generation. It reads `sqlc.yaml`, refreshes convention-matched Atlas schema SQL such as `auth/db/gen/schema.sql` from `auth/db/schema.hcl`, and then runs `sqlc generate`.

SQLC generation does not mutate a database and does not read seed files as inputs.

The DB lifecycle split is:

```text
onlava db apply
onlava db seed
onlava db setup
onlava db neon status --json
onlava db branch status --json
onlava db branch checkout feature/my-branch --json
onlava db branch list --json
onlava db branch expire feature/my-branch --after 24h --json
onlava db branch prune --older-than 336h --json
onlava worktree create feature-my-branch --from main --json
```

`onlava db apply` mutates schema or app-owned database setup only. It does not run SQLC generation or seed files. `onlava db seed` applies initial data such as `SERVICE/db/seed.sql` only, records successful runs in a small internal ledger, skips unchanged seeds, and fails closed if a previously-applied seed changes or if seed SQL contains destructive setup patterns such as `DROP`, `TRUNCATE`, or broad `DELETE`. `onlava db setup` runs apply, then seed.

During `onlava up`, the supervisor runs this DB setup lifecycle before starting the app when `database.apply` or seed files are present. It reuses the session-managed `DatabaseURL` env and skips setup on ordinary rebuilds until the `database.apply` config or seed file hashes change.

`SERVICE/db/seed.sql` is data, not Atlas schema input and not SQLC input. The first seed implementation fails closed when a previously-applied seed changes or destructive seed SQL is detected, rather than offering force or reseed escape hatches.

For Neon configs, the current slice is contract, branch-pin local state, generated dev-cell lifecycle, and ready-lease consumption only: `.onlava.json` can declare `dev.services.postgres.kind: "neon"` with `mode: "self-hosted"` and `isolation: "branch"`, `onlava db neon install --json` writes generated dev-cell state files under the agent home, `onlava db neon start --json` and `stop --json` manage the generated Docker Compose project, `onlava db neon status --json` probes Docker/image/container health and reserved listeners for running components, `onlava db branch checkout <name> --json` writes `.onlava/worktree-db.json`, `onlava db branch list --json` reads Onlava-owned local leases from `branches.json` under the agent home, and `onlava db branch status --json` can report pending, missing, expired, or ready local leases. Branch list also includes lease entries with status, timestamps, and optional redacted endpoint metadata. A ready lease may expose redacted endpoint metadata so `onlava up`, `onlava db psql`, and Electric can synthesize a process-local `DatabaseURL`; pending, missing, expired, or endpoint-less leases fail explicitly. `expire`, `prune`, and `onlava down --db` update only local registry metadata, `onlava down --state` removes the local worktree pin, and `onlava worktree create <name> --json` creates a Git worktree and writes the target pin. Onlava does not yet create backend branches.

## Electric Txid Observation

For Electric-backed writes, call generated TypeScript `WithMeta` methods so the response headers and parsed `txid` are available. Treat the API mutation and Electric observation as separate phases: once the HTTP response is successful and contains `X-Txid`, the mutation committed; a later `awaitTxId` timeout or Electric/Postgres error is a sync observation failure. Wrap the app's observer with generated `observeAPIResponseTxid(response, observer, context)` to get `SyncObservationError` diagnostics that include txid, app/session, API URL, Electric URL or stream context, and the observer error.

## Agent Routes And Frontends

Use `.onlava.json` proxy config:

```json
{
  "name": "myapp",
  "proxy": {
    "workspace": "acme",
    "route_base_domain": "local.dev",
    "frontends": {
      "app": {
        "root": "apps/app"
      }
    }
  }
}
```

Run:

```sh
onlava up
onlava system edge dns install
onlava system edge privileged install
onlava system edge install
onlava system edge trust
```

The session-scoped URLs in `routes` are canonical. Generated routes default to `api.<session>.local.dev`, frontend routes under `<frontend>.<session>.local.dev`, and direct browser API calls should use the generated API route. Configured hosts appear as friendly aliases only for the live session that owns the free alias. Use `onlava up --claim-aliases` only when intentionally transferring live aliases to the current session.

Common failure: trying to bind the agent router or Caddy itself to `127.0.0.1:443` as a normal user. The default-port HTTPS path is managed DNS plus the privileged loopback helper on `127.0.0.1:443`, forwarding raw TCP to user-owned Caddy on a high loopback port, with the agent router kept on its internal loopback upstream. Run `onlava system edge dns install` and `onlava system edge privileged install` once as the normal user, then `onlava system edge install` to prepare user-owned Caddy. Do not run `sudo onlava system edge install`. `onlava system edge trust` trusts the local Caddy CA through a temporary admin-only Caddy process, so it does not require the port-443 edge to already be running. Trusting the local Caddy CA should be a one-time setup unless the CA changes.

The managed edge Caddy config flushes proxied responses immediately so Electric and other SSE streams stay live. Do not disable upstream caching globally; Electric uses cache headers for request collapsing.

## Debugging With Inspect, Logs, Traces, Metrics

Start here:

```sh
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava logs --limit 200
onlava inspect observability --json --session current
onlava logs query --json --session current --since 15m --query 'error OR panic'
onlava traces list --json --since 15m
onlava metrics list --json --since 1h
onlava metrics query --json --session current --since 15m --step 5s --promql 'onlava_request_duration_seconds'
```

For generated paths:

```sh
onlava inspect build --json
onlava inspect paths --json
```

## Harness Workflow

For app changes:

```sh
onlava check --json
go test ./...
onlava harness --json --write
```

For onlava repo changes:

```sh
go test ./...
go test ./cmd/onlava
onlava harness self --summary --write
```

Do not run `go install ./cmd/onlava` unless a human explicitly asks; self-harness
uses a worktree-local `.onlava/harness/bin/onlava` build for binary freshness.

For dashboard/browser validation:

```sh
onlava harness ui --json
```

## Common Mistakes And Fixes

- Missing `.onlava.json`: create it at the app root or pass `--app-root`.
- Stale generated client: rerun `onlava generate client` or configured `onlava generate client`.
- Auth endpoint returns unauthorized: inspect standard auth bootstrap and bearer token.
- `tenants` migration or runtime error: if the relation is `onlava_auth.tenants`, it is framework-owned standard auth state; an unqualified app `tenants` relation is app-domain schema drift.
- Private endpoint exposed over HTTP: change to public/auth only when it should be externally reachable.
- No traces: confirm the app is running under onlava and uses onlava-aware wrappers for DB/client work.
- Proxy upstream unavailable: confirm the child app process is listening on the API URL printed by `onlava up`.
- Browser mutation hangs during local dev: check long-lived SSE streams and prefer local HTTPS/HTTP2 proxy paths when concurrency matters.
