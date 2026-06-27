# scenery App Development Cookbook

This cookbook is the practical "how do I build this?" companion to `docs/local-contract.md`. The local contract is the source of truth for exact CLI and JSON contracts; this file gives agents and developers common recipes.

## Minimal App

Create `.scenery.json`:

```json
{"name":"hello"}
```

`.scenery.json` is preferred. `.config.json` is accepted as an alias when `.scenery.json` is absent.

If the app needs Go build tags or other build-time flags, add them as literal argv entries:

```json
{"name":"hello","build":{"go_flags":["-tags=roofmapnet_native"]}}
```

If the app has a Git-tracked non-runtime tree that should not trigger `scenery up` rebuilds, add a Scenery-only watch ignore:

```json
{"name":"hello","watch":{"ignore":["reference/"]}}
```

Create `go.mod`:

```go
module example.com/hello

go 1.26.3

require scenery.sh v0.0.0
```

Create `service/api.go`:

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

Validate:

```sh
scenery check --json
scenery serve
curl http://127.0.0.1:4000/hello/world
```

Common failure: `scenery check` cannot find the app. Run it from the app root or pass `--app-root`.

## Typed Public Endpoint

Typed endpoints accept path parameters, request structs, and return typed JSON responses.

```go
type CreateThingRequest struct {
	Name string `json:"name"`
}

type CreateThingResponse struct {
	ID string `json:"id"`
}

//scenery:api public path=/things method=POST
func CreateThing(ctx context.Context, req *CreateThingRequest) (*CreateThingResponse, error) {
	return &CreateThingResponse{ID: req.Name}, nil
}
```

Validate:

```sh
scenery check --json
curl -X POST http://127.0.0.1:4000/things -d '{"name":"alpha"}'
```

Common failure: missing pointer request or unsupported signature. Check `scenery inspect endpoints --json`.

## Storage Objects

Declare Scenery-owned storage in app config:

```json
{
  "name": "files-app",
  "storage": {
    "cell_id": "files-app",
    "default": "app",
    "stores": {
      "app": {
        "kind": "zerofs",
        "access": "auth",
        "tenant_scoped": true,
        "max_object_bytes": 104857600
      }
    }
  },
  "dev": {
    "services": {
      "storage": {
        "kind": "zerofs",
        "mode": "local",
        "route": "storage",
        "env": {
          "AWS_REGION": "us-east-1"
        }
      }
    }
  }
}
```

The app-facing storage API is production-supported when headless `scenery serve`
or standalone `scenery worker` receives an explicit operator-provided
`SCENERY_STORAGE_CONFIG` whose stores use `kind: "proxy"` and `proxy_socket`.
Managed ZeroFS remains the beta local-dev path behind `scenery up`; headless
runtimes reject missing or local-root storage config instead of silently creating
local storage.

Inspect and exercise the configured store through Scenery JSON surfaces:

```sh
scenery inspect storage --json
scenery storage status --json
scenery storage put app uploads/example.txt ./example.txt --json
scenery storage ls app --prefix uploads/ --json
scenery storage stat app uploads/example.txt --json
scenery storage get app uploads/example.txt --output /tmp/example.txt --json
scenery storage rm app uploads/example.txt --json
scenery storage rm app uploads/ --recursive --json
scenery storage cleanup --json
```

App code launched by Scenery can import `scenery.sh/storage` and call `storage.Default(ctx)` or `storage.Named(ctx, "app")`. The package reads Scenery-injected capability metadata and talks to the configured proxy socket. In agent-backed dev sessions, that proxy speaks to the managed ZeroFS 9P Unix socket; app code should not depend on Scenery agent-state paths, ZeroFS sockets, proxy sockets, or object directories.

For stores with `tenant_scoped: true`, caller-visible keys stay unchanged while Scenery stores them under a tenant namespace. Authenticated HTTP storage routes derive the tenant from standard auth data. Private/internal calls must pass a standard-auth request context or wrap the context with `storage.WithTenantID(ctx, tenantID)`.

`PutOptions.ContentType` and `PutOptions.Metadata` are returned by `Head`, `Get`, and `List`. Browser/proxy routes carry metadata through `X-Scenery-Storage-Meta-*` headers.

For beta import/export checks, use `put` to import files, `ls`/`stat` to verify object metadata and checksums, `get` to export bytes, and `rm --recursive` to roll back a test prefix. This is a single-object/prefix operational proof, not a production backup system.

When a managed ZeroFS storage cell is attached, `scenery inspect storage --json` and `scenery storage status --json` include runtime lease ownership. `scenery down` releases only the current session's storage lease. `scenery storage cleanup --json` reports the shared storage cell without deleting it; add `--yes` only after live leases are gone.

When storage is configured, the app runtime also exposes auth-protected object routes for browser code:

```text
GET    /__scenery/storage/app?prefix=uploads/&delimiter=/
PUT    /__scenery/storage/app/uploads/example.txt
HEAD   /__scenery/storage/app/uploads/example.txt
GET    /__scenery/storage/app/uploads/example.txt
DELETE /__scenery/storage/app/uploads/example.txt
```

Use the app's normal auth credentials for stores with `access: "auth"`. Stores with `access: "private"` are intentionally unavailable through these external routes; app/runtime code should reach them through `scenery.sh/storage` or Scenery's internal private routing, not browser helpers.

Generated TypeScript clients expose the same auth storage route surface through `client.storage`:

```ts
const appStore = client.storage.store("app")
await appStore.put("uploads/example.txt", file, { contentType: file.type })
const page = await appStore.list({ prefix: "uploads/" })
const text = await appStore.getText("uploads/example.txt")
await appStore.delete("uploads/example.txt")
```

## Auth Endpoint

Enable standard auth in app config:

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

	"scenery.sh/auth"
)

type MeResponse struct {
	UserID string `json:"user_id"`
}

//scenery:api auth path=/me method=GET
func Me(ctx context.Context) (*MeResponse, error) {
	uid, _ := auth.UserID()
	return &MeResponse{UserID: string(uid)}, nil
}
```

Validate:

```sh
scenery check --json
scenery serve
curl -X POST http://127.0.0.1:4000/users/dev-bootstrap
```

Common failure: `DatabaseURL` is missing. Put it in process env or an app-root `.env.local` for local development.

Standard auth owns its tenant state in `scenery_auth.tenants`. You do not need an app-local `tenants` service or table to use standard auth; create one only for product-domain tenant APIs or schema.

## Private Endpoint Call

Private endpoints are internal-only and should be called through generated helpers from other scenery endpoints. Do not expose private APIs over external HTTP.

```go
//scenery:api private
func Compute(ctx context.Context) (*ComputeResponse, error) {
	return &ComputeResponse{Value: 42}, nil
}
```

Validate:

```sh
scenery check --json
scenery inspect routes --json
```

Common failure: raw endpoints cannot be called through internal service-to-service helpers in the current contract.

## Service Struct Initialization

Use `//scenery:service` when endpoints are methods on a struct with dependencies.

```go
//scenery:service
type Service struct {
	prefix string
}

func initService() (*Service, error) {
	return &Service{prefix: "hello"}, nil
}

//scenery:api public path=/hello method=GET
func (s *Service) Hello(ctx context.Context) (*HelloResponse, error) {
	return &HelloResponse{Message: s.prefix}, nil
}
```

Validate:

```sh
scenery check --json
go test ./...
```

Common failure: nested services are invalid. Keep one service root per package/service area.

## Middleware

Use `scenery.sh/middleware` for app middleware. Start from `testdata/apps/middleware` before writing new patterns.

Validate:

```sh
scenery check --app-root testdata/apps/middleware --json
go test ./internal/parse ./internal/codegen ./runtime
```

Common failure: middleware order or scope is unclear. Inspect the generated app model with `scenery inspect app --json`.

## Request Decoding Tags

Supported request tags:

```text
json
header
query
qs
cookie
scenery:"optional"
```

Example:

```go
type SearchRequest struct {
	Query string `query:"q"`
	Token string `header:"authorization" scenery:"optional"`
}
```

Validate:

```sh
scenery inspect endpoints --json
```

Common failure: forgetting `scenery:"optional"` for values that may be absent.

## HTTP Status Responses

Use `scenery:"httpstatus"` on a response field:

```go
type CreatedResponse struct {
	Status int    `json:"-" scenery:"httpstatus"`
	ID     string `json:"id"`
}
```

Common failure: returning a status field in JSON accidentally. Use `json:"-"` when the field should only control HTTP status.

## Coded Errors

Use `scenery.sh/errs` for HTTP-aware coded errors.

```go
return nil, errs.NotFound("thing not found")
```

Validate error mappings with endpoint tests or `curl`.

## Request And Auth Context

Use:

```go
meta := scenery.CurrentRequest()
uid, ok := auth.UserID()
standard, ok := auth.CurrentAuthData()
```

Common failure: relying on globals outside request handling. Pass context or actor values explicitly to lower layers.

## legacy async runtime Workflow Or Activity

Use `scenery.sh/legacy-async-runtime` for beta workflow and activity declarations. Packages that call `legacy-async-runtime.NewWorkflow` or `legacy-async-runtime.NewActivity` are imported by generated main so worker processes can register them. Set `legacy-async-runtime.enabled: true` in app config to opt in; legacy async runtime remains off when the field is omitted, even if declarations or TypeScript worker settings are present. Use `scenery up` for local combined API/worker execution, and use `scenery worker` for worker-only processes. Set `ActivityConfig.MaxConcurrency` when a dedicated task queue should cap concurrent activity executions for resource-heavy work, and pass `legacy-async-runtime.WithHeartbeatTimeout(...)` when a workflow activity needs a heartbeat timeout.

## Cron Job

Use `scenery.sh/cron` and see `testdata/apps/cron`. When legacy async runtime is enabled, cron jobs run through legacy async runtime Schedules. Set `OverlapPolicy`, `CatchupWindow`, `PauseOnFailure`, `ActivityStartToClose`, and `ActivityRetryPolicy` on `cron.JobConfig` when missed-run, overlap, timeout, or retry behavior must be explicit.

```go
package jobs

import (
	"context"
	"time"

	"scenery.sh/cron"
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
scenery check --app-root testdata/apps/cron --json
go test ./cron ./internal/parse ./internal/codegen
```

Common failure: relying on wall-clock behavior in unit tests. Keep cron tests deterministic.

## pgxpool Tracing

Use `scenery.sh/pgxpool` when you want PostgreSQL operations to appear in scenery local traces.

For the default app database, prefer `scenery.sh/db` so services share one traced pool selected by `dev.services.postgres.database_url_env`:

```go
package api

import (
	"context"

	"example.com/app/db/queries"
	scenerydb "scenery.sh/db"
)

type Service struct {
	q *queries.Queries
}

func initService(ctx context.Context) (*Service, error) {
	pool, err := scenerydb.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{q: queries.New(pool)}, nil
}
```

`scenery.sh/db` is intentionally scoped to the configured default Postgres database. Use `scenery.sh/pgxpool` directly for explicit secondary databases.

Validate:

```sh
scenery traces list --json --since 15m
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
```

Common failure: using a raw pool in app code and then expecting DB spans in the dashboard.

## TypeScript Client Generation

Generate a client:

```sh
scenery generate client --lang typescript --output ./src/scenery-client.ts
```

If app config declares `generators.clients`, inspect and run the configured graph:

```sh
scenery inspect generators --json
scenery generate --dry-run --json
scenery generate client
```

Inspect wire support:

```sh
scenery inspect wire --json
```

Common failure: committing generated clients without regenerating after endpoint changes.

## Code Tasks

Use `scenery task` for app-local code tasks that should run from the app root without requiring the app model to parse cleanly.

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
scenery task run billing:reconcile -- --dry-run
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
scenery task list --json
scenery task inspect billing:reconcile --json
```

Common failure: putting two single-file Go tasks with `package main` in the same directory without `//go:build ignore`. Normal Go package loading may see both files before scenery can filter anything. Use the build tag for `*.task.go`, or use a per-task directory.

## Validation Profiles

Use `validation` profiles in app config when an app has quality gates beyond the core framework harness:

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
scenery inspect validation --json
scenery validate quick --json --write
scenery validate changed --base origin/main --json --write
scenery validate full --dry-run --json
```

## Configured SQLC And DB Lifecycle

Use `scenery generate sqlc` for file generation. It reads `sqlc.yaml`, refreshes convention-matched Atlas schema SQL such as `auth/db/gen/schema.sql` from `auth/db/schema.hcl`, and then runs `sqlc generate`.

SQLC generation does not mutate a database and does not read seed files as inputs.

The DB lifecycle split is:

```text
scenery db apply
scenery db seed
scenery db setup
scenery db postgres status --json
scenery db branch status --json
scenery db branch checkout feature/my-branch --json
scenery db branch list --json
scenery db branch expire feature/my-branch --after 24h --json
scenery db branch prune --older-than 336h --json
scenery worktree create feature-my-branch --from main --json
```

`scenery db apply` mutates schema or app-owned database setup only. It does not run SQLC generation or seed files. `scenery db seed` applies initial data such as `SERVICE/db/seed.sql` only, records successful runs in a small internal ledger, skips unchanged seeds, and fails closed if a previously-applied seed changes or if seed SQL contains destructive setup patterns such as `DROP`, `TRUNCATE`, or broad `DELETE`. `scenery db setup` runs apply, then seed.

During `scenery up`, the supervisor runs this DB setup lifecycle before starting the app when `database.apply` or seed files are present. It reuses the runtime-managed `DatabaseURL` env and skips setup on ordinary rebuilds until the `database.apply` config or seed file hashes change.

`SERVICE/db/seed.sql` is data, not Atlas schema input and not SQLC input. The first seed implementation fails closed when a previously-applied seed changes or destructive seed SQL is detected, rather than offering force or reseed escape hatches.

For managed branch configs, app config can declare `dev.services.postgres.kind: "postgres"` with `mode: "local"` and `isolation: "database"`. `scenery db postgres start --json` prepares the shared local Postgres dev cell, `scenery db postgres status --json` inspects it, and `scenery db branch checkout <name> --json` writes `.scenery/worktree-db.json`, ensures the parent template database exists, creates or reuses the branch database through template database cloning, and records redacted endpoint metadata. `scenery db branch list --json` reads Scenery-owned local leases from `branches.json`, and `scenery db branch status --json` can report missing, expired, protected, or ready local leases. A ready lease may expose redacted endpoint metadata so `scenery up`, `scenery db psql`, DB setup, and sync can synthesize a process-local `DatabaseURL`. Missing, expired, protected, or endpoint-less leases fail explicitly. `reset` recreates the branch from the parent template, `delete` drops the branch database and removes the lease, `expire` updates local registry metadata, `prune` removes expired non-current branch databases when the Postgres admin substrate is reachable, `scenery down --state` removes the local worktree pin, and `scenery worktree create <name> --json` creates a Git worktree, writes the target pin, and runs branch-provider ensure. The default `scenery harness self --json --write` path includes the live Postgres branch lifecycle proof; use `--quick` when that live proof is intentionally out of scope.

## sync Txid Observation

For sync-backed writes, call generated TypeScript `WithMeta` methods so the response headers and parsed `txid` are available. Treat the API mutation and sync observation as separate phases: once the HTTP response is successful and contains `X-Txid`, the mutation committed; a later `awaitTxId` timeout or sync/Postgres error is a sync observation failure. Wrap the app's observer with generated `observeAPIResponseTxid(response, observer, context)` to get `SyncObservationError` diagnostics that include txid, app/session, API URL, sync URL or stream context, and the observer error.

## Agent Routes And Frontends

Use app config proxy settings:

```json
{
  "name": "myapp",
  "dev": {
    "routing": {
      "mode": "path"
    }
  },
  "proxy": {
    "workspace": "acme",
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
scenery up
scenery ps
```

Default local dev routing is path mode. The app root's live runtime gets one base URL such as `http://localhost:4001`; API routes live under `/api/`, frontends under `/<frontend>/`, and Scenery runtime surfaces under `/runtime/`. The URLs in `route_manifest.routes` and compatibility `routes` are canonical for the current runtime. Direct browser API calls should use the generated API route.

Use host mode only when you intentionally need domain-style local routes:

```json
{
  "dev": {
    "routing": {
      "mode": "host"
    }
  },
  "proxy": {
    "route_base_domain": "local.dev"
  }
}
```

Then run the edge setup commands:

```sh
scenery system edge dns install
scenery system edge privileged install
scenery system edge install
scenery system edge trust
```

Host-mode configured hosts appear as friendly aliases only for the live app root that owns the free alias. Use `scenery up --claim-aliases` only when intentionally transferring live aliases to the current app root.

Common host-mode failure: trying to bind the agent router or Caddy itself to `127.0.0.1:443` as a normal user. The default-port HTTPS path is managed DNS plus the privileged loopback helper on `127.0.0.1:443`, forwarding raw TCP to user-owned Caddy on a high loopback port, with the agent router kept on its internal loopback upstream. Run `scenery system edge dns install` and `scenery system edge privileged install` once as the normal user, then `scenery system edge install` to prepare user-owned Caddy. Do not run `sudo scenery system edge install`. `scenery system edge trust` trusts the local Caddy CA through a temporary admin-only Caddy process, so it does not require the port-443 edge to already be running. Trusting the local Caddy CA should be a one-time setup unless the CA changes.

The managed edge Caddy config flushes proxied responses immediately so sync and other SSE streams stay live. Do not disable upstream caching globally; sync uses cache headers for request collapsing.

## Debugging With Inspect, Logs, Traces, Metrics

Start here:

```sh
scenery check --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery logs --limit 200
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery traces list --json --since 15m
scenery metrics list --json --since 1h
scenery metrics query --json --since 15m --step 5s --promql 'scenery_request_duration_seconds'
```

`scenery inspect models --json` and `scenery inspect views --json` expose the beta static IR from `//scenery:model`, `scenery.sh/model`, `//scenery:page`, and `scenery.sh/page`. Model records include source ownership metadata; `model.Table("tasks")` means a generated Scenery-owned table in the service schema, while `model.ExistingTable("legacy", "customers")` binds to an existing physical table, skips generated schema/seed ownership, and allows generated list/get only. View records include each collection page's projection as model/view IR: source row type, projection record type, projected fields, static column display hints, static filters, and static sorts. Use them to check parser-visible model/page shape. `scenery generate data --dry-run --json` writes desired Atlas HCL to `.scenery/gen/db/<service>/schema.hcl`, seed SQL to `.scenery/gen/db/<service>/seed.sql`, and beta frontend model/view packages to `.scenery/gen/web/<frontend>/` when collection pages and configured frontends exist. Generated model DB artifacts use the app-owned `<service>` schema, so seed SQL, generated CRUD SQL, and sync shape metadata target the same schema-qualified table instead of `public`; existing-table entities use their explicit schema-qualified table for read-only code and sync shape metadata without emitting generated DB ownership artifacts. Those frontend packages include typed storage rows, page projection records in `projections.ts`, sync shape definitions, collection descriptors with static filter/sort/display metadata, runtime adapter factories, default page components, route factories, and `registerGeneratedRoutes`; app code still owns the production router, sync client, TanStack DB instance, and layout-kit implementation. Mount a generated read-only page by declaring the entity/page in Go, running `scenery generate data --dry-run --json`, pointing a frontend alias such as `@scenery/generated` at `.scenery/gen/web/<frontend>/index.ts`, importing the generated page or route from that alias, mounting it, and running the host typecheck/render or build command. `scenery db diff --generated --json` compares generated desired schema with the app-owned `SERVICE/db/schema.hcl`; `scenery check --json` reports `model-schema` diagnostics when generated-source schemas drift. Model CRUD actions declared with `model.Generate` appear in `scenery inspect endpoints --json` with `"generated": true`; generated CRUD endpoints default to `auth`, generated CRUD route bases default to `/<service>/<table>`, and generated routes fail check on reserved prefixes (`/runtime`, `/__scenery`, `/api`, `/sync`) or handwritten/generated route collisions. Generated list endpoints default to `limit=100`, accept `limit` up to 500 plus non-negative `offset`, and reject invalid values before querying. Generated create/patch payloads accept both response field names such as `CreatedAt` and DB-column JSON names such as `created_at`, so `time.Time` fields round-trip RFC3339 timestamps or fail decode with a field-scoped error. Generated CRUD stores share one package-level pgx pool for the configured app database URL env, defaulting to `DatabaseURL`, or Scenery's managed database env. Tenant-shaped generated CRUD is scoped to the active standard-auth tenant, with tenant fields limited to `string`, named string types, or `github.com/google/uuid.UUID`.

For generated paths:

```sh
scenery inspect build --json
scenery inspect paths --json
```

## Harness Workflow

For app changes:

```sh
scenery check --json
go test ./...
scenery harness --json --write
```

For scenery repo changes:

```sh
go test ./...
go test ./cmd/scenery
scenery harness self --summary --write
```

Do not run `go install ./cmd/scenery` unless a human explicitly asks; self-harness
uses a worktree-local `.scenery/harness/bin/scenery` build for binary freshness.

For dashboard/browser validation:

```sh
scenery harness ui --json
```

## Common Mistakes And Fixes

- Missing app config: create `.scenery.json` or `.config.json` at the app root, or pass `--app-root`.
- Stale generated client: rerun `scenery generate client` or configured `scenery generate client`.
- Auth endpoint returns unauthorized: inspect standard auth bootstrap and bearer token.
- `tenants` migration or runtime error: if the relation is `scenery_auth.tenants`, it is framework-owned standard auth state; an unqualified app `tenants` relation is app-domain schema drift.
- Private endpoint exposed over HTTP: change to public/auth only when it should be externally reachable.
- No traces: confirm the app is running under scenery and uses scenery-aware wrappers for DB/client work.
- Proxy upstream unavailable: confirm the child app process is listening on the API URL printed by `scenery up`.
- Browser mutation hangs during local dev: check long-lived SSE streams and prefer local HTTPS/HTTP2 proxy paths when concurrency matters.
