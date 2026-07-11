---
name: scenery
description: Use when building, running, debugging, inspecting, validating, or generating clients for scenery applications. scenery is a Go-native service runtime and CLI using .scenery.json/.config.json app config, //scenery directives, typed endpoints, local dev supervision, logs, traces, metrics, workers, and TypeScript client generation.
---

# scenery

scenery is a Go-native service runtime and local development platform. It runs app-root dev runtimes, exposes capabilities for inspection and action, and hides backing substrate details unless you intentionally debug them. Apps are ordinary Go modules with a `.scenery.json` app config, or `.config.json` alias, at the app root and `//scenery:` directives in Go source.

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
scenery inspect durable --json
scenery inspect storage --json
scenery system toolchain verify --json
scenery logs --jsonl --limit 200
scenery inspect observability --json
scenery logs query --json --since 15m --query 'error OR panic'
scenery harness --json --write
scenery validate quick --json --write
scenery deploy status --json
```

Prefer JSON output for agent decisions. Prefer `scenery up` for local development. Use `scenery task` for configured and code tasks. Use `scenery validate` for app-owned quality gates. Use `scenery worker` for worker-only durable and cron execution.

In edition-2027 apps, inspect stable diagnostic metadata with `scenery schema scenery.diagnostics.2027.v1 -o json` or agent `schema.get`; branch on `SCNxxxx`, not message text. Resolve opaque source IDs through the returned source map. Source columns are zero-based Unicode-scalar positions and are paired with UTF-8 byte offsets.

Run `scenery doctor --json` before deep app debugging when local readiness is in doubt. It is read-only and reports host resources, Go version, Docker engine reachability/details, optional tools, and app-sensitive dependency hints without building or starting services.

`scenery inspect docs --json` exposes `summary.review_due_count`, document-level `review_due` and `stale` fields, discovered `AGENTS.md` scopes, and Child Agent Index drift. For scenery repo changes, `scenery harness self --summary --write` surfaces those docs knowledge signals in compact validation summaries and leaves full evidence in `.scenery/harness/` artifacts. When docs and behavior disagree, the same PR must either fix the affected docs or open/update an ExecPlan that records the drift.

## Mental Model

- `.scenery.json` marks the app root; `.config.json` is accepted as an alias when `.scenery.json` is absent.
- App-required Go build tags or build-time flags belong in app config as `build.go_flags`, for example `["-tags=roofmapnet_native"]`; Scenery applies them to app builds and generated-workspace tests.
- App-owned non-runtime trees that should remain Git-tracked but stay out of `scenery up` rebuilds belong in app config `watch.ignore`, for example `["reference/"]`.
- Public deploy is beta. `deploy.domain` claims one public FQDN for `scenery deploy enable`; `deploy.root` optionally names the frontend/service that owns `/`. `scenery deploy setup` configures the macOS privileged helper with sudo for public 80/443, while `scenery deploy status --json` reports DNS, reachability, sleep, firewall, helper, and certificate diagnostics.
- Go source is the app model.
- `scenery up` starts the supervised local platform: app process, rebuild/restart loop, dashboard, API Explorer, logs, traces, metrics, managed dev services when configured, and optional frontend routing through the local agent. Default local routing is path mode: one app root/worktree gets one base URL such as `http://localhost:4001`, and dashboard/API/frontends live under `/consolenext/`, `/api/`, `/<frontend>/`, and `/runtime/`; use `scenery ps --json` to discover the base URL and route manifest.
- Storage is a Scenery-owned app capability when app config declares `storage`. Stores use `kind: "local"` (empty defaults to `local`): a Scenery-owned directory tree with atomic temp-file+rename writes, checked fsync, and sidecar object metadata. Declaring `storage.stores` is enough; there is no managed storage process, toolchain artifact, or `dev.services` storage entry. `scenery up` serves the stores from the local backend over a session-local proxy socket; app code uses `scenery.sh/storage` and should not inspect proxy sockets or object directories. A standalone `scenery worker` requires an explicit `SCENERY_STORAGE_CONFIG` and fails closed when storage is declared but the config is missing or empty; each store uses `kind: "local"` with an absolute `root` or `kind: "proxy"` with a `proxy_socket`. Private stores are internal-only: external storage routes deny them, while app/runtime helpers and Scenery's private route table may use them. Tenant-scoped stores keep caller-visible keys unchanged, derive tenants from standard auth on external routes, and require `storage.WithTenantID(ctx, tenantID)` or standard-auth context for private/internal calls. `PutOptions.ContentType` and `PutOptions.Metadata` are returned by `Head`, `Get`, and `List`; browser/proxy routes carry metadata through `X-Scenery-Storage-Meta-*` headers. `scenery inspect storage --json` and `scenery storage status --json` report the storage-cell path and per-store object counts/bytes; `scenery storage cleanup --json` reports the cell and removes it only with `--yes`. Offsite durability is an operator concern — replicate the storage-cell object directories (objects plus `__scenery/metadata/` sidecars) to S3 with `rclone`/`restic`.
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
scenery up --detach
# discover the base URL with `scenery ps --json`, then:
curl http://localhost:4001/api/hello/world
```

## Directives

```go
//scenery:api public|auth|private [raw] [path=/...] [method=GET,POST]
//scenery:service
//scenery:authhandler
```

Standard auth can be enabled from app config without app-local wrapper endpoints. Its tenant tables are framework-owned Postgres tables in the app database's `scenery` schema; app-local `tenants` services or tables are product-domain concerns only. When `auth.google_oauth.enabled` is true, Google sign-in and Google connection endpoints are generated; app code can call `auth.GoogleAccessToken(ctx, scopes...)` or `auth.GoogleAccessTokenForUser(ctx, userID, scopes...)`, and clients should treat `google_reauth_required` as a reconnect prompt.

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
- `scenery.sh/durable` for typed task declarations, startup DB reconciliation, queued job starts, interval schedules, retrying local Go handler execution, durable step/signal helpers, durable worker lease/heartbeat/complete/fail HTTP endpoints, durable job admin, and durable inspect metadata
- `scenery.sh/cron`
- `scenery.sh/storage`
- `scenery.sh/db`
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

`scenery up --json` emits JSONL. `scenery up --detach` starts the app root's agent-backed dev runtime. `scenery logs --follow` follows that runtime's logs. `scenery console` opens the source-aware terminal console. Agent `routes` are canonical; configured friendly hosts appear separately in `aliases` only for the live app root that owns the free alias. Use `scenery up --claim-aliases` only for intentional live alias transfer.

Use `scenery system edge dns install`, `scenery system edge privileged install`, `scenery system edge install`, then `scenery system edge trust` when a browser needs trusted wildcard local HTTPS on `127.0.0.1:443`. The DNS command owns wildcard `local.dev` resolution through managed dnsmasq; the privileged helper owns only the default HTTPS loopback listener and forwards raw TCP to user-owned Caddy on an unprivileged loopback port. Do not run Caddy, the agent router, or `scenery system edge install` as root. `scenery system edge` uses managed dnsmasq and Caddy from the toolchain. `scenery system edge trust` uses a temporary admin-only Caddy process and does not require the port-443 edge to already be running.

For service databases, app processes, setup commands, DB setup, and workers receive Postgres URLs for the app database. An app-level `DATABASE_URL` wins and is treated as external; otherwise `scenery up` uses the shared managed Postgres dev server and creates one database per app root/worktree with one schema per service plus `scenery`. Scenery injects `DATABASE_URL`, `<SERVICE>_DATABASE_URL`, and `SCENERY_DATABASE_JSON`. A standalone `scenery worker` requires an explicit Postgres `DATABASE_URL`; it does not start the managed dev server.

Generated TypeScript `WithMeta` methods expose response headers, status, and the raw `Response` alongside decoded data.

## UI Work

For dashboard work, follow `apps/consolenext/AGENTS.md`. For reusable `@scenery registry` work, read `docs/ui-agent-contract.md`; add registry components with commands such as `bun run shadcn:add @scenery/button` from `ui/`.
The browser UI harness is implemented; use it for dashboard route validation when UI behavior changes. Prefer `--write` when debugging so screenshots, DOM snapshots, console JSONL, and network JSONL are available under `.scenery/harness/ui/`.

```sh
cd apps/consolenext
bun run lint
bun run typecheck
bun run build
cd ../..
scenery harness ui --json --write
```

For `ui/` registry changes, run `bun run typecheck` and `bun run test` from `ui/`.

## Debugging

```sh
scenery check --json
scenery inspect app --json
scenery inspect routes --json
scenery inspect endpoints --json
scenery inspect paths --json
scenery inspect durable --json
scenery inspect storage --json
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

Scenery-managed tools live under `.scenery/toolchain/`, `~/.scenery/toolchain/` for machine-level edge tools, or `SCENERY_TOOLCHAIN_DIR`. Treat managed dnsmasq, Caddy, and Victoria details as substrate unless intentionally debugging them. Agents should not rely on system `PATH` binaries for those issues; use `scenery system toolchain sync --json` for app-root tools, `scenery system edge dns install` for wildcard local DNS, or `scenery system edge install` for Caddy edge. Use `scenery upgrade` to replace a prebuilt local Scenery binary with the latest verified release; it then syncs managed toolchain entries already present locally, and `--toolchain all` pulls every frozen tool/image from the upgraded manifest. Shared substrate failures appear in `scenery ps --json` under `substrates` with `last_exit`, `component_exits`, and stdout/stderr log paths. Dead registered runtime children such as managed frontend processes appear as session `degraded` with `status_reason`; managed Vite/Astro frontend processes are restarted by `scenery up` when their dev-server process exits unexpectedly.

Do not introduce new scenery-owned production environment variables by default. Prefer app config, explicit CLI flags, or checked-in manifests; when an env variable is truly required, update `docs/environment.registry.json`, `docs/environment.md`, and tests together.

## Generated TypeScript Client

Edition-2027 apps use generated targets declared in `scenery.scn`. Read root/package declarations and `scenery.migration.scn`, then use:

```sh
scenery fmt --check -o json
scenery check -o json
scenery compile --view expanded -o json
scenery migrate status -o json
scenery generate --check -o json
```

Edit `.scn` declarations, never generated `scenerycontract`, `internal/scenerygen`, or TypeScript files. Keep local module sources inside the non-symlink workspace and TypeScript outputs beneath declared managed generated roots; top-level generation commits all families together. Use `scenery list|get|explain|graph ... -o json` and `scenery diff --semantic` for graph and compatibility facts; source preserves authored expressions, effective resolves inputs, applies defaults, then patches, and expanded adds generators, with RFC 6901 pointers into each resource explaining every value. Mixed manifests use exact normalized `legacy_config`, canonical `./...` package roots, declared namespaces/targets, and an explicit default legacy gateway; omit the config field only after removing the shared config. Remaining bounded legacy clients then derive identity/options from structured canonical resources in the compiled migration snapshot, never source-symbol heuristics. Native ownership switches the whole service, including routes, lifecycle, durable/schedule, schema/event, external identity, and generated-client surfaces. Candidate validation preserves all other active owners. Static comparison, behavioral evidence, and operational evidence are separate; advisory behavior requires explicit activation approval. Cutover classes union both candidates, so removing a legacy stateful resource does not remove its evidence obligation. A `native_service` package cannot hide undeclared legacy models, pages, or references to package-init builder symbols; non-registering durable/cron APIs remain allowed. The service implementation owns one lifecycle adapter while operations independently keep or remove `legacy_go_v0`; inspect `lifecycle_adapter`, `remaining_operation_bridge_count`, and `adapter_retirement_ready` in migration status. A native constructor result must be statically assignable to every remaining legacy receiver. An explicit bridge adapter may remain only while migration status reports it. Model durable tasks, schedules, and other runtime identities in `.scn` first. Preserve an old durable task name with `external_name`; increment `revision` and drain or migrate active rows when its persisted input ABI changes. Activation/rollback/retirement are receipt-producing plans and stateful classes require `--evidence class=reference`; once committed as `native_service`, the retired service stays ready without machine-local activation receipts. App-wide `migrate finish` also requires v0 CLI/client-consumer clearance and closed rollback ownership. Semantic changes and deployments likewise use immutable revision-bound plan/apply; planning retains the exact canonical issued plan and apply rejects caller-recomputed plans before trusting approvals, edits, or provider actions. Operations are normalized before hashing and renames carry digest-checked, revision-bound evidence across migration references and containing-module descendants that later diffs load from applied receipts or accept through `--rename-receipts`. For `required_approvals`, pass a project-issued detached token with `--approval-token`; Scenery verifies it against uncommitted public keys in `.scenery/approval-trust.json`, and agents must never generate or store the private signing key. Legacy-only apps continue using the stable generator workflow below.

For an approval-bearing migration transition, retain the planning result with `--out <plan>` and apply that exact file through `scenery migrate apply <plan>`. Rerunning the transition issues a different expiry-bound plan ID and cannot consume the earlier token.

For semantic creation, read agent `resource_create_kinds` and the target `schema.get` result first. The schema is recursive and distinguishes attributes from labeled/repeated blocks, including transport-specific label patterns for HTTP headers, queries, cookies, and multipart parts; unadvertised kinds are intentionally `capability_unavailable` rather than guessed. Declarative extension syntax is known but `unsupported_profile` until its profile is implemented.

`scenery compile` intentionally reports no `implementation_revision`; run `scenery build --target <go-target>` for the exact content-addressed Go input/toolchain identity and runtime-bundle sidecar. Fixed non-host CGO targets are rejected until a native-toolchain schema exists. Use exact `std.type.unit` for no-input operations. Authored CLI bindings execute as their declared non-reserved lower-kebab `scenery <command...>` path with generated help/completion, typed arguments/flags, runtime-trusted context, outcome output, and exit codes. Use `scenery db seed --env <environment>` so local fixture selection matches deployment.

Go `service.config` keys are dynamic lower-snake generated field names; each references a typed package input, whose name may differ and whose type, phase, constraints, and sensitivity define the generated constructor config.

Edition-2027 TypeScript clients reconstruct typed outcomes split across response body, headers, and cookies. Same-status business outcomes are selected by their typed wire mappings and exactly one mapping must decode; ambiguous observable wire shapes are compile errors. Query/header sets use canonical semantic order and reject duplicates; multipart requests use only the declared part names, kinds, media types, filename policy, multiplicity, and byte limits. Fetch cannot preserve repeated request-header field lines, so TypeScript targets reject repeated list/set request headers; use comma encoding only for compatible scalar codecs. Repeated response headers need Fetch `Headers.getAll`; response cookies need `Headers.getSetCookie`; absence of either required extension fails as `unsupported_runtime`. Declared transport/admission/dispatch failures are returned as typed outcomes and generated clients do not add retries.

```sh
scenery inspect endpoints --json
scenery inspect models --json
scenery inspect views --json
scenery generate client --lang typescript --output ./src/scenery-client.ts
```

Regenerate committed clients after endpoint, request/response, or auth changes.
Generated model CRUD endpoints are beta and appear in `scenery inspect endpoints --json`
with `"generated": true`; generated stores use the configured app database URL
env, defaulting to `DatabaseURL`, or Scenery's managed database env and target
the app-owned service schema rather than `public`. Generated CRUD endpoints default to `auth` for every
action. Generated CRUD route bases default to `/<service>/<table>`
and `scenery check` reports collisions with reserved route prefixes
(`/runtime`, `/__scenery`, `/api`) or handwritten/generated app routes.
Use `model.ExistingTable(schema, table)` for read-only generated pages/endpoints
over an existing physical table; inspect models exposes source metadata, schema/seed
generation skips that table, and generated mutations or `model.Seed(...)` rows are rejected.
Generated Atlas HCL uses schema-qualified resource labels such as
`table "<service>" "<table>"` so app-owned schemas can coexist with handwritten
multi-schema HCL.
`scenery generate data --dry-run --json`
 also writes beta generated frontend packages under `.scenery/gen/web/<frontend>/`
 for configured frontends with static collection pages, including runtime adapter
factories, page projection records in `projections.ts`, static page filter/sort/display metadata, default page components, and route registration helpers for app-owned data/TanStack/layout-kit wiring;
generated entity source metadata uses the same schema-qualified table as the DB artifacts.

To mount a generated page, declare the entity/page in Go, run `scenery generate data --dry-run --json`, point a frontend alias such as `@scenery/generated` at `.scenery/gen/web/<frontend>/index.ts`, import the generated page or route from that alias, mount it, and run the host typecheck/render or build command.

When an app configures `generators`, prefer:

```sh
scenery inspect generators --json
scenery generate --dry-run --json
scenery generate
```

Keep `scenery generate` for file generation only. `scenery generate sqlc` may refresh generated schema SQL and run `sqlc generate`, but it must not apply database schema or seed data.

Use `scenery db apply` for schema/app database mutation only. Use `scenery db seed` for initial data such as `SERVICE/db/seed.sql` and generated model seed files under `.scenery/gen/db/<service>/seed.sql`; seeds apply to the matching service database, and changed previously-applied seeds or destructive seed SQL fail closed with path/line diagnostics. Use `scenery db setup` for apply then seed. `scenery up` runs this setup lifecycle before app startup when DB setup inputs exist, then skips it on ordinary rebuilds until `database.apply` config or seed file hashes change.

Generated model CRUD endpoints default to auth-only. If a generated entity has a convention tenant field (`TenantID` or `tenant_id`), Scenery derives it from standard-auth tenant data, keeps it out of create/patch payloads, and currently supports `string`, named string types, or `github.com/google/uuid.UUID` tenant fields.
Generated list endpoints are bounded: default `limit=100`, maximum `limit=500`, and non-negative `offset`.
Generated create/patch payloads accept response field names such as `CreatedAt` as well as DB-column JSON names such as `created_at`; `time.Time` values should be RFC3339 JSON timestamps and malformed values fail decoding.

Use `scenery db list --json` and `scenery db shell` for configured Postgres service schemas; use `scenery db server status --json` only when debugging the shared managed Postgres server. Worktree isolation is automatic through per-worktree database names. `scenery db reset <service>` drops and recreates only that service schema; full-database save/restore uses `scenery db snapshot create|restore`. The default `scenery harness self --json --write` path includes the Docker-gated Postgres probe for one app database, service schemas, durable state, auth bootstrap, worktree isolation, and service-schema reset; use `--quick` for the smaller self-harness mode.

## Tasks

Use `scenery task` for configured repo tasks and app-local code tasks. Configured tasks use plain names from app config. Code tasks use `<domain>:<name>` and run from the app root without requiring the app model to parse cleanly.

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
scenery worker [--app-root <path>] [--env <name>]
scenery worker durable --endpoint <url> --token <token> [--service <name>]... [--app-root <path>] [--env <name>]
scenery worker durable jobs list|inspect|cancel|retry [job-id] --service <name> [--app-root <path>] --json
scenery worker durable token create --service <name> [--name <name>] [--id <id>] [--app-root <path>] --json
scenery version --json
scenery upgrade [--version latest|vX.Y.Z] [--target <path>] [--toolchain installed|all|none] [--force] [--dry-run] [--json]
scenery deploy enable [--app-root <path>] [--json]
scenery deploy disable [--app-root <path>] [--json]
scenery deploy status [--json]
scenery deploy setup [--acme-email <email>] [--acme-ca production|staging] [--json]
scenery deploy resume [--json]
scenery deploy teardown [--json]
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
scenery storage status [--app-root <path>] --json
scenery storage webui [--app-root <path>] --json
scenery storage ls <store> [--prefix <prefix>] [--cursor <cursor>] [--limit <n>] [--app-root <path>] --json
scenery storage stat <store> <key> [--app-root <path>] --json
scenery storage put <store> <key> <file> [--app-root <path>] --json
scenery storage get <store> <key> --output <file> [--app-root <path>] --json
scenery storage rm <store> <key> [--recursive] [--app-root <path>] --json
scenery storage cleanup [--yes] [--app-root <path>] --json
scenery inspect app|routes|services|endpoints|models|views|build|paths|generators|durable|storage|observability --json [--app-root <path>]
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
scenery db list [--app-root <path>] [--json]
scenery db shell [--app-root <path>] [--service <name>] [psql args...]
scenery db apply [--app-root <path>] [--json]
scenery db seed [--app-root <path>] [--env <name>] [--dry-run] [--json]
scenery db setup [--app-root <path>] [--json]
scenery db reset|drop|snapshot [--app-root <path>] [--yes]
scenery db server status|start|stop|logs [--json] [--yes]
scenery worktree create <name> [--from <branch>] [--app-root <path>] [--json]
scenery worktree list [--app-root <path>] [--json]
scenery worktree remove <name> [--app-root <path>] [--db] [--json]
```

Self-harness Go test steps use the Go test result cache by default. Pass
`--fresh-tests` when a fresh `-count=1` run is intentionally required. The Go
timing step uses the locally measured package parallelism `-p 8`.
Treat the seven-second timing value as an optimization target. Cached and fresh
budgets are advisory, release timing is enforced, and package/test warnings are
actionable only after the timing artifact records isolated confirmation.

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
