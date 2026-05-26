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

Validate:

```sh
onlava check --json
onlava run
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
onlava run
curl -X POST http://127.0.0.1:4000/users/dev-bootstrap
```

Common failure: `DatabaseURL` is missing. Put it in process env or an app-root `.env.local` for local development.

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

Use `github.com/pbrazdil/onlava/temporal` for beta workflow and activity declarations. Packages that call `temporal.NewWorkflow` or `temporal.NewActivity` are imported by generated main so worker processes can register them. Enable `temporal.enabled` in `.onlava.json`, use `onlava dev` for local combined API/worker execution, and use `onlava worker` for worker-only processes. Set `ActivityConfig.MaxConcurrency` when a dedicated task queue should cap concurrent activity executions for resource-heavy work.

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
onlava inspect traces --json --since 15m
onlava inspect metrics --json --since 1h
```

Common failure: using a raw pool in app code and then expecting DB spans in the dashboard.

## TypeScript Client Generation

Generate a client:

```sh
onlava gen client --lang typescript --output ./src/onlava-client.ts
```

Inspect wire support:

```sh
onlava inspect wire --json
```

Common failure: committing generated clients without regenerating after endpoint changes.

## Local Proxy And Frontends

Use `.onlava.json` proxy config:

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

Run:

```sh
onlava dev --proxy
onlava dev --proxy --trust
```

Common failure: expecting trust-store prompts every startup. Trusting the local CA should be a one-time operation unless the CA changes.

## Debugging With Inspect, Logs, Traces, Metrics

Start here:

```sh
onlava check --json
onlava inspect app --json
onlava inspect routes --json
onlava inspect endpoints --json
onlava logs --limit 200
onlava inspect traces --json --since 15m
onlava inspect metrics --json --since 1h
```

For generated paths:

```sh
onlava inspect build --json
onlava inspect paths --json
```

For data-platform apps:

```sh
onlava inspect data --json --database-url "$DATABASE_URL"
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
go install ./cmd/onlava
onlava harness self --json --write
```

For dashboard/browser validation:

```sh
onlava harness ui --json
```

## Common Mistakes And Fixes

- Missing `.onlava.json`: create it at the app root or pass `--app-root`.
- Stale generated client: rerun `onlava gen client`.
- Auth endpoint returns unauthorized: inspect standard auth bootstrap and bearer token.
- Private endpoint exposed over HTTP: change to public/auth only when it should be externally reachable.
- No traces: confirm the app is running under onlava and uses onlava-aware wrappers for DB/client work.
- Proxy upstream unavailable: confirm the child app process is listening on the API URL printed by `onlava dev`.
- Browser mutation hangs during local dev: check long-lived SSE streams and prefer local HTTPS/HTTP2 proxy paths when concurrency matters.
