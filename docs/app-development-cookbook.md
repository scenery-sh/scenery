# scenery App Development Cookbook

This cookbook is the practical "how do I build this?" companion to `docs/local-contract.md`. The local contract is the source of truth for exact CLI and JSON contracts; this file gives agents and developers common recipes.

## Edition-2027 Native Or Mixed App

Create `scenery.scn` with `language`, `application`, installed `module` blocks, gateways, and declared generation targets. Each local module owns a `scenery.package.scn`. A mixed app keeps an explicit `scenery.migration.scn`; it references the exact legacy app config while that shared file exists and omits `legacy_config` after the file is removed. Remaining bounded legacy services and clients then resolve from the compiled migration snapshot. Every discovered service must appear as `legacy_service`, `shadow_service`, or `native_service`.

Use the compiler and committed-generation loop:

```sh
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery migrate status -o json     # mixed mode
scenery generate -o json
scenery generate --check -o json
go test ./...
scenery harness --json --write
```

Treat `scenerycontract`, `internal/scenerygen`, generated TypeScript roots, and their descriptors as one atomically generated set. Implement the generated Go handler interfaces in ordinary app code; exact HTTP scalar/body/status behavior remains owned by the generated adapter and runtime codec.

For a terminal zero-or-more path, add both extension profiles to
`language.require_profiles` and map the final template segment explicitly:

```hcl
http {
  method        = "GET"
  path          = "/drive/{path...}"
  codec_profile = std.codec.http_json_v1

  path_tail "path" {
    to = operation.download.input.path
  }
}
```

The target must be `string`, `relative_path`, or
`optional(relative_path)`. `/drive` supplies the empty behavior for that type;
`/drive/a/b` supplies `a/b`; a trailing slash, empty segment, traversal,
backslash, encoded separator, NUL, or hazardous double encoding is never
normalized into a value. Generated clients take semantic text, split on `/`,
and percent-encode each segment independently.

Use exact `std.type.unit` for an operation with no input or response body. To prove an implementation rather than only its contract, build a declared target and keep the runtime-bundle sidecar with the binary:

```sh
scenery build --target release -o ./dist/app
jq . ./dist/app.scenery.runtime-bundle.v1.json
```

The descriptor contains the exact Go build-input manifest and resolved tool identities. Do not substitute source globs or an ambient compiler fingerprint. Host CGO records C/C++ tool identities; fixed non-host CGO is deliberately unavailable until it has a typed native-toolchain contract.

An authored CLI binding is invoked directly through its declared command path. Help, completion, typed decoding, output selection, and exit status come from the binding:

```sh
scenery house process-scene scene-42 --mode all --help
scenery completion house process-scene
scenery house process-scene scene-42 --mode all -o json
```

Use lower-kebab command and flag names below a non-reserved first segment. Context mappings are supplied from the runtime-minted local-developer principal and cannot be overridden by caller input.

For typed fixtures, use the same environment name as the deployment. Only matching fixtures are projected, and the existing seed ledger still prevents changed or destructive reapplication:

```sh
scenery db seed --env development --dry-run --json
scenery db seed --env development --json
```

To migrate a service, generate a native candidate, shadow and compare it, activate native ownership with evidence for every reported non-stateless cutover class, verify the service, then retire the legacy candidate. Read `static_contract_complete`, `static_contract_equal`, `behavioral_evidence_complete`, and `operational_evidence_complete` separately. Static equality does not waive advisory behavior: when the activation plan reports `risk_advisory_migration_evidence`, obtain a detached project approval token bound to that exact plan and pass it with `--approval-token`. Handler migration may proceed operation by operation: move the service implementation to the native lifecycle, then remove each operation's `legacy_go_v0` adapter while `migrate status` reports the remaining count. During that mixed phase, the native constructor must still return a pointer assignable to every remaining legacy endpoint receiver; `scenery check` rejects an incompatible split before startup. Keep the activation receipt until retirement because rollback is a new receipt-bound plan, not a runtime toggle. Finish the whole bridge only after all services are native and retired, all adapters/incomplete constructs are gone, v0 CLI and legacy generated-client consumers are cleared, and every stateful retirement has an evidence reference:

```sh
scenery migrate service house --generate --dry-run -o json
scenery migrate service house --shadow --dry-run -o json
scenery migrate compare house -o json
scenery migrate activate house --native --dry-run --out /tmp/house-activation-plan.json --evidence generated_client=artifact://consumer-gate -o json
# Have the project approval service issue a token for plan_id and required_approvals in that exact file.
scenery migrate apply /tmp/house-activation-plan.json --approval-token /path/to/project-issued-token.json -o json
scenery migrate verify house -o json
scenery migrate service house --retire --dry-run -o json
scenery migrate finish --dry-run --evidence v0_cli_consumers=artifact://cli-audit -o json
```

Status output is authoritative for the complete evidence key set; the abbreviated example does not invent evidence for classes the service does not report. Workflow execution and unknown profiles fail compilation rather than degrading to an approximation.

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
scenery up --detach
# discover the base URL with `scenery ps --json`, then:
curl http://localhost:4001/api/hello/world
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
        "kind": "local",
        "access": "auth",
        "tenant_scoped": true,
        "max_object_bytes": 104857600
      }
    }
  }
}
```

The `local` backend is a Scenery-owned directory tree with atomic temp-file+rename
writes, checked fsync on objects and their parent directories, and sidecar
object metadata. It needs no managed process, toolchain artifact, or dev-service
declaration: declaring `storage.stores` is enough, and `scenery up` serves the
stores from the local backend over a session-local proxy.

For a standalone `scenery worker` or an operator-run generated binary, set an explicit
`SCENERY_STORAGE_CONFIG` whose stores use either `kind: "local"` with an absolute
`root`, or `kind: "proxy"` with a `proxy_socket` pointing at an operator-owned
storage runtime. Headless runtimes fail closed when storage is declared but the
config is missing or empty.

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

App code launched by Scenery can import `scenery.sh/storage` and call `storage.Default(ctx)` or `storage.Named(ctx, "app")`. The package reads Scenery-injected capability metadata and talks to the configured proxy socket. App code should not depend on Scenery agent-state paths, proxy sockets, or object directories.

For stores with `tenant_scoped: true`, caller-visible keys stay unchanged while Scenery stores them under a tenant namespace. Authenticated HTTP storage routes derive the tenant from standard auth data. Private/internal calls must pass a standard-auth request context or wrap the context with `storage.WithTenantID(ctx, tenantID)`.

`PutOptions.ContentType` and `PutOptions.Metadata` are returned by `Head`, `Get`, and `List`. Browser/proxy routes carry metadata through `X-Scenery-Storage-Meta-*` headers.

For beta import/export checks, use `put` to import files, `ls`/`stat` to verify object metadata and checksums, `get` to export bytes, and `rm --recursive` to roll back a test prefix. This is a single-object/prefix operational proof, not a production backup system.

`scenery inspect storage --json` and `scenery storage status --json` report the storage-cell path and per-store object counts and total bytes. `scenery storage cleanup --json` reports the shared storage cell without deleting it; add `--yes` to remove the storage-cell directory.

### Single-server production storage with offsite S3 replication

The `local` backend is a plain directory tree, which makes offsite durability an
operator recipe rather than a Scenery subsystem. On a single server, keep the
local store hot (fast, fsync-durable) and replicate the storage-cell object
directories to S3 on a timer. Replicate the **whole** store root so the
`__scenery/metadata/` sidecars travel with their objects.

Find the store root from `scenery inspect storage --json` (`storage.runtime.objects_dir`,
with a per-store subdirectory), or point at an explicit `root` from your headless
`SCENERY_STORAGE_CONFIG`. Then, with `rclone`:

```sh
# One-way mirror of the store root to a bucket/prefix (includes sidecars).
rclone sync /var/lib/files-app/storage/files-app/objects/app \
  s3:my-bucket/files-app/app --transfers 8 --fast-list
```

Drive it from a systemd timer (or cron) every few minutes:

```ini
# /etc/systemd/system/files-app-storage-sync.service
[Service]
Type=oneshot
ExecStart=/usr/bin/rclone sync /var/lib/files-app/storage/files-app/objects/app s3:my-bucket/files-app/app --transfers 8 --fast-list

# /etc/systemd/system/files-app-storage-sync.timer
[Timer]
OnBootSec=2min
OnUnitActiveSec=5min
[Install]
WantedBy=timers.target
```

`restic` is a good alternative when you want deduplicated, encrypted, point-in-time
snapshots instead of a live mirror:

```sh
restic -r s3:s3.amazonaws.com/my-bucket/files-app backup \
  /var/lib/files-app/storage/files-app/objects/app
```

Restore drill: stop the app, restore the mirror/snapshot back into an empty store
root (`rclone sync s3:my-bucket/files-app/app <root>` or `restic restore latest --target <root>`),
then start the app and confirm `scenery storage ls app --json` and a `get` return
the expected objects and metadata. Replication is asynchronous, so objects written
since the last sync can be lost on host loss; size the interval to your tolerated
data-loss window.

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
    "dev_bootstrap": {
      "enabled": true,
      "default_user_email": "owner@example.test",
      "default_tenant_id": "00000000-0000-0000-0000-000000000001"
    }
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
scenery up --detach
# discover the base URL with `scenery ps --json`, then:
curl -X POST http://localhost:4001/api/users/dev-bootstrap
```

When `default_user_email` is configured, the first local dev bootstrap creates
that verified user, the configured default tenant, and an owner membership when
they are missing.

Common failure: `DatabaseURL` is missing. Put it in process env or an app-root `.env.local` for local development.

To enable Google sign-in, opt in explicitly and provide credentials through env:

```json
{
  "auth": {
    "enabled": true,
    "google_oauth": {
      "enabled": true,
      "client_id_env": "GoogleOAuthClientID",
      "client_secret_env": "GoogleOAuthClientSecret",
      "allowed_scopes": ["https://www.googleapis.com/auth/gmail.modify"],
      "token_cipher_key_env": "AuthTokenCipherKey"
    }
  }
}
```

In Google Cloud Console, create a Web application OAuth client and add this redirect URI for each environment:

```text
${APIBaseURL}/auth/google/callback
```

For local development, put the client ID and secret in the app-root `.env` or process environment. The sign-in button should navigate the browser to:

```text
GET /auth/google/start?redirect_path=/
```

When `google_oauth.enabled` is false or absent, `/auth/google/start` and `/auth/google/callback` are not registered, do not appear in `scenery inspect endpoints --json`, and are omitted from generated TypeScript clients. When Google OAuth is enabled but credentials are missing, `scenery check --json` reports an `auth` warning.

ONLV should enable `auth.google_oauth.enabled`, keep `GoogleOAuthClientID` and `GoogleOAuthClientSecret` in its local app-root env, register the redirect URI for its Scenery API base URL, and point its sign-in button at `/auth/google/start?redirect_path=/`. Gmail connection consent reuses the same Google callback URI and dispatches by OAuth state purpose, so apps do not need a second Google redirect URI.

Put a base64-encoded 32-byte `AuthTokenCipherKey` in production env. Local `scenery up` can derive a dev-only key from the local JWT secret when this env is absent.

For long-lived Gmail access, do not leave the Google OAuth consent screen in
Testing publishing status: Google test-user refresh tokens expire after 7 days.
Use an In production external app, or an Internal app under Google Workspace.

The frontend starts consent through the generated auth client:

```ts
const { authorize_url } = await client.auth.GoogleConnectStart({
  scopes: ["https://www.googleapis.com/auth/gmail.modify"],
  redirect_path: "/settings"
})
window.location.href = authorize_url
```

After Google redirects back, `GET /auth/google/connection` returns `status: "active"` with granted scopes. `status: "reauth_required"` means the user should reconnect.

App backend code fetches a short-lived Google access token when it needs Gmail:

```go
token, err := auth.GoogleAccessToken(ctx, "https://www.googleapis.com/auth/gmail.modify")
```

Handle `google_reauth_required` by asking the user to reconnect, and `google_scope_missing` by restarting connect with the missing scope. On a Gmail 401, call `auth.GoogleAccessToken` once more and retry the Gmail request once; use ordinary 429/quota backoff for Gmail rate limits.

For mailbox sync, persist Gmail `historyId`, poll `users.history.list`, and do a full resync if Gmail returns 404 for an expired history cursor. Drafts stay in ONLV and call Gmail `drafts.create` / `drafts.update`. Sending should use a durable outbox: enqueue the send, have a worker call Gmail, and record Gmail `message.id` so retries stay idempotent. Preserve threading by passing Gmail `threadId` when available and setting standard `References` / `In-Reply-To` headers.

Standard auth owns its tenant state in `scenery.scenery_auth_tenants`. You do not need an app-local `tenants` service or table to use standard auth; create one only for product-domain tenant APIs or schema.

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

## Cron Job

Use `scenery.sh/cron` and see `testdata/apps/cron`. Set `OverlapPolicy`, `CatchupWindow`, and `PauseOnFailure` on `cron.JobConfig` when missed-run and overlap behavior must be explicit.

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

## Database Helper

For the default app database, prefer `scenery.sh/db` so services share one `*sql.DB` pinned to the configured service schema:

```go
package api

import (
	"context"
	"database/sql"

	"example.com/app/db/queries"
	scenerydb "scenery.sh/db"
)

type Service struct {
	q *queries.Queries
	db *sql.DB
}

func initService(ctx context.Context) (*Service, error) {
	db, err := scenerydb.Get(ctx)
	if err != nil {
		return nil, err
	}
	return &Service{q: queries.New(db), db: db}, nil
}
```

`scenery.sh/db` is intentionally scoped to configured Scenery database services. It opens Postgres URLs with the pgx database/sql driver and pins each pool to the service schema; pass an explicit service name when the app has more than one database service.

For database services and schemas on the shared dev server:

```json
{
  "database": { "url_env": "DATABASE_URL" },
  "dev": {
    "services": {
      "reports": {},
      "cache": {}
    }
  }
}
```

During `scenery up`, Scenery creates one per-worktree database on the shared local Postgres server, creates `reports`, `cache`, and `scenery` schemas, and injects `DATABASE_URL`, `REPORTS_DATABASE_URL`, `CACHE_DATABASE_URL`, and `SCENERY_DATABASE_JSON`. For production, standalone `scenery worker`, or bring-your-own local Postgres, set `DATABASE_URL` to a `postgres://` or `postgresql://` URL; explicit DSNs always win and Scenery does not manage the server or database in that mode.

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

Common failure: committing generated clients without regenerating after endpoint changes.

Edition-2027 response mappings may rebuild one typed outcome from its body, response headers, and cookies. When outcomes share a status, the client validates each distinct typed mapping and accepts exactly one match; mappings that are not provably disjoint from observable media and structural wire shape are compile errors. Query/header sets are canonical and duplicate-free. Multipart request generation follows each declared part's exact name, kind, media types, byte limit, filename retention, and multiplicity instead of deriving parts from record fields. Optional absent metadata remains absent. Fetch cannot preserve repeated request-header field lines, so use comma encoding for list/set headers only when the scalar codec permits commas to remain unambiguous. A Fetch runtime must expose `Headers.getAll(name)` for repeated response headers and `Headers.getSetCookie()` for response cookies; generated clients return `unsupported_runtime` when the declared wire shape cannot be observed exactly. Declared transport/admission/dispatch failures are typed outcomes and no retry is added implicitly.

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
scenery db list --json
scenery db shell
scenery db server status --json
scenery worktree create feature-my-branch --from main --json
scenery db snapshot create before-refactor --json
```

`scenery db apply` mutates schema or app-owned database setup only. It does not run SQLC generation or seed files. `scenery db seed` applies initial data such as `SERVICE/db/seed.sql` to the matching service schema, records successful runs in `scenery.seed_runs`, skips unchanged seeds, and fails closed if a previously-applied seed changes or if seed SQL contains destructive setup patterns such as `DROP`, `TRUNCATE`, or broad `DELETE`. `scenery db setup` runs apply, then seed.

During `scenery up`, the supervisor runs this DB setup lifecycle before starting the app when `database.apply` or seed files are present. It reuses the runtime-managed service database env values and skips setup on ordinary rebuilds until the `database.apply` config or seed file hashes change.

`SERVICE/db/seed.sql` is data, not Atlas schema input and not SQLC input. The first seed implementation fails closed when a previously-applied seed changes or destructive seed SQL is detected, rather than offering force or reseed escape hatches.

Worktree isolation is the database branching model: each app root/worktree gets a distinct managed database name, and every service is a schema inside that database. `scenery worktree create <name> --json` creates only the Git worktree; the next `scenery up` or DB command ensures its app database. `scenery db reset <service>` drops and recreates only that service schema, leaving other service schemas and the `scenery` schema intact. Durable tasks, cron schedules, auth, and the seed ledger live in the shared `scenery` schema, so they are included in `scenery db snapshot create|restore` with the rest of the app database.

## Agent Routes And Frontends

Use app config frontend settings:

```json
{
  "name": "myapp",
  "dev": {
    "routing": {
      "mode": "path"
    }
  },
  "frontends": {
    "app": {
      "root": "apps/app"
    }
  }
}
```

Run:

```sh
scenery up
scenery ps
```

Default local dev routing is path mode. The app root's live runtime gets one base URL such as `http://localhost:4001`; API routes live under `/api/`, the Scenery dashboard under `/consolenext/`, frontends under `/<frontend>/`, and Scenery runtime surfaces under `/runtime/`. The URLs in `route_manifest.routes` and compatibility `routes` are canonical for the current runtime. Direct browser API calls should use the generated API route.

Use host mode only when you intentionally need default `local.dev` domain-style local routes:

```json
{
  "dev": {
    "routing": {
      "mode": "host"
    }
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

The managed edge Caddy config flushes proxied SSE responses immediately so streams stay live. Do not disable upstream caching globally.

## Serve A Live App On Your Domain

`scenery deploy` is beta and intentionally operator-driven. It serves an enabled live `scenery up` session through the machine public edge; it does not configure your router or DNS provider.

Declare the domain in app config:

```json
{
  "name": "hello",
  "deploy": {
    "domain": "hello.example.com",
    "root": "app"
  }
}
```

Configure the machine once, starting with Let's Encrypt staging:

```sh
scenery deploy setup --acme-ca staging --acme-email ops@example.com
scenery deploy enable --app-root /path/to/app
scenery up --detach --app-root /path/to/app
scenery deploy status --json
```

Use `scenery deploy status --json` as the checklist. It reports the LAN IP for router forwarding, the discovered public IP for DNS, wildcard 80/443 listener state, DNS A/AAAA mismatches, power sleep, macOS firewall, Caddy cert expiry, and whether the enabled app root has a live session. Configure the router to forward public TCP 80/443 to the reported LAN IP, and point the domain's A/AAAA records at the reported public IP. If LAN probes work but public probes fail, check router forwarding and whether the ISP is using CGNAT.

Verification ladder:

```sh
curl -I http://hello.example.com/
curl -kI https://hello.example.com/
curl https://hello.example.com/api/health
scenery deploy status --json
```

After staging works, rerun setup with `--acme-ca production`. `scenery deploy teardown` removes public binding and the resume LaunchAgent while keeping the registry and Caddy certificates. Public deploy never exposes `/runtime`, `/consolenext`, `/__scenery`, or other Scenery control paths.

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

`scenery inspect models --json` and `scenery inspect views --json` expose the beta static IR from `//scenery:model`, `scenery.sh/model`, `//scenery:page`, and `scenery.sh/page`. Model records include source ownership metadata; `model.Table("tasks")` means a generated Scenery-owned table in the service schema, while `model.ExistingTable("legacy", "customers")` binds to an existing physical table, skips generated schema/seed ownership, and allows generated list/get only. View records include each collection page's projection as model/view IR: source row type, projection record type, projected fields, static column display hints, static filters, and static sorts. Use them to check parser-visible model/page shape. `scenery generate data --dry-run --json` writes desired Atlas HCL to `.scenery/gen/db/<service>/schema.hcl`, seed SQL to `.scenery/gen/db/<service>/seed.sql`, and beta frontend model/view packages to `.scenery/gen/web/<frontend>/` when collection pages and configured frontends exist. Generated model DB artifacts use the app-owned `<service>` schema, so seed SQL, generated CRUD SQL, and entity source metadata target the same schema-qualified table instead of `public`; existing-table entities use their explicit schema-qualified table for read-only code and entity source metadata without emitting generated DB ownership artifacts. Those frontend packages include typed storage rows, page projection records in `projections.ts`, entity source definitions, collection descriptors with static filter/sort/display metadata, runtime adapter factories, default page components, route factories, and `registerGeneratedRoutes`; app code still owns the production router, row data source, TanStack DB instance, and layout-kit implementation. Mount a generated read-only page by declaring the entity/page in Go, running `scenery generate data --dry-run --json`, pointing a frontend alias such as `@scenery/generated` at `.scenery/gen/web/<frontend>/index.ts`, importing the generated page or route from that alias, mounting it, and running the host typecheck/render or build command. `scenery db diff --generated --json` compares generated desired schema with the app-owned `SERVICE/db/schema.hcl`; `scenery check --json` reports `model-schema` diagnostics when generated-source schemas drift. Model CRUD actions declared with `model.Generate` appear in `scenery inspect endpoints --json` with `"generated": true`; generated CRUD endpoints default to `auth`, generated CRUD route bases default to `/<service>/<table>`, and generated routes fail check on reserved prefixes (`/runtime`, `/__scenery`, `/api`) or handwritten/generated route collisions. Generated list endpoints default to `limit=100`, accept `limit` up to 500 plus non-negative `offset`, and reject invalid values before querying. Generated create/patch payloads accept both response field names such as `CreatedAt` and DB-column JSON names such as `created_at`, so `time.Time` fields round-trip RFC3339 timestamps or fail decode with a field-scoped error. Generated CRUD stores share one package-level `database/sql` connection for the configured app database URL env, defaulting to `DatabaseURL`, or Scenery's managed database env. Tenant-shaped generated CRUD is scoped to the active standard-auth tenant, with tenant fields limited to `string`, named string types, or `github.com/google/uuid.UUID`.

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
- `tenants` migration or runtime error: if the relation is `scenery.scenery_auth_tenants`, it is framework-owned standard auth state; an unqualified app `tenants` relation is app-domain schema drift.
- Private endpoint exposed over HTTP: change to public/auth only when it should be externally reachable.
- No traces: confirm the app is running under scenery and uses scenery-aware wrappers for DB/client work.
- Proxy upstream unavailable: confirm the child app process is listening on the API URL printed by `scenery up`.
- Browser mutation hangs during local dev: check long-lived SSE streams and prefer local HTTPS/HTTP2 proxy paths when concurrency matters.
