# 0088 - SQLite Service Databases and Postgres Removal

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery currently treats Postgres as the built-in managed database capability. This plan replaces that model with SQLite as the default managed local database model, using the invariant:

```text
one scenery service = one SQLite database file
one scenery app can own multiple SQLite database files
```

The result must remove active Postgres code paths from Scenery. After this plan is complete, Scenery should not start Postgres, require Postgres Docker images, expose `scenery db postgres`, call `psql` or `pg_dump`, depend on `pgx` or `lib/pq`, generate Postgres auth SQL, or require a `dev.services.postgres` entry for database access.

The new model is service-oriented. A `.scenery.json` app can declare multiple SQLite database services under `dev.services`, for example:

```json
{
  "name": "example",
  "dev": {
    "services": {
      "auth": {
        "kind": "sqlite",
        "database_url_env": "AUTH_DATABASE_URL"
      },
      "billing": {
        "kind": "sqlite",
        "database_url_env": "BILLING_DATABASE_URL"
      }
    }
  }
}
```

At runtime, Scenery creates or resolves one file per SQLite service and injects one database URL per service. For example:

```text
AUTH_DATABASE_URL=sqlite:///absolute/path/.scenery/sessions/<session_id>/sqlite/auth.sqlite
AUTH_DATABASE_PATH=/absolute/path/.scenery/sessions/<session_id>/sqlite/auth.sqlite
BILLING_DATABASE_URL=sqlite:///absolute/path/.scenery/sessions/<session_id>/sqlite/billing.sqlite
BILLING_DATABASE_PATH=/absolute/path/.scenery/sessions/<session_id>/sqlite/billing.sqlite
SCENERY_SQLITE_DATABASES_JSON=[{"service":"auth","path":"...","url":"..."},{"service":"billing","path":"...","url":"..."}]
```

A legacy `DatabaseURL` compatibility alias may be injected only when exactly one SQLite service exists. When multiple SQLite services exist, `DatabaseURL` is ambiguous and must not be injected unless the app explicitly configured that env name for one service.

This change should preserve Scenery's agent-friendly guarantees: database state is inspectable, deterministic, file-backed, safe to reset, safe to branch, and safe to snapshot. SQLite files are substrate details; users and agents interact with service names and Scenery CLI commands.

## Progress

* [x] 2026-06-27 - Initial ExecPlan drafted from current code survey.
* [x] 2026-06-27 - Added this plan as `docs/plans/0088-sqlite-service-databases.md`, linked it from `docs/plans/active.md`, and indexed it in `docs/knowledge.json`.
* [x] 2026-06-27 - Re-ran repository inventory for Postgres references before editing. Validation: `git grep -n -i ... -- .` and `go list -deps ./... | grep -E 'pgx|pq|postgres' || true`.
* [x] 2026-06-27 - Implemented SQLite service discovery and config schema changes. Validation: `go test ./internal/app ./internal/sqlitedb` passed.
* [x] 2026-06-27 - Implemented SQLite file resolver, connection helper, and env injection, including `SCENERY_SQLITE_DATABASES_JSON`. Validation: `go test ./internal/sqlitedb ./cmd/scenery ./db` passed.
* [x] 2026-06-27 - Rewrote database CLI commands around SQLite service names, files, shell/apply/seed/setup/reset/drop/snapshot/diff/branch flows. Validation: `go test ./cmd/scenery` passed.
* [x] 2026-06-27 - Rewrote database branch lifecycle around SQLite files and fixed forced current-branch delete to clear the stale worktree pin. Validation: `go run ./cmd/scenery harness self --summary --write` passed the `sqlite-branch-lifecycle` step with `leases_after: 1`.
* [x] 2026-06-27 - Ported standard auth from pgx/Postgres to SQLite and `database/sql`. Validation: `go test ./auth ./auth/db/gen` passed through `go test ./...`.
* [x] 2026-06-27 - Removed managed Postgres substrate, Postgres dependencies, Postgres toolchain image, public `pgxpool`, internal Postgres test helper, Atlas Postgres defaults, and the Postgres fixture. Validation: final active-surface grep and `go list -deps ./... | rg 'pgx|pq|postgres'` returned no matches.
* [x] 2026-06-27 - Updated docs, schemas, harness expectations, knowledge indexes, environment registry, and dashboard DB explorer copy. Validation: `scenery inspect docs --json` passed with 65 documents, 0 missing, 0 review-due, 0 stale.
* [x] 2026-06-27 - Ran final validation and recorded exact outcomes. Validation: `go test ./...`, `go test ./cmd/scenery`, `cd ui && bun run typecheck && bun run test && bun run build`, `./scripts/build-dashboard-ui-embed.sh`, final dependency/source inventories, and `go run ./cmd/scenery harness self --summary --write` passed.

Update this section at every meaningful stopping point. Every update must include the date, what changed, and whether validation was run.

## Surprises & Discoveries

* Current `go.mod` has direct Postgres dependencies: `github.com/jackc/pgx/v5` and `github.com/lib/pq`.
* Current `db/db.go` is not database-neutral. It returns a shared pgx pool and requires a managed Postgres service.
* Current `pgxpool/` is a Postgres-specific tracing wrapper. The tracing concept is reusable, but the package is not.
* Current standard auth is pgx/sqlc/Postgres-specific and must be rewritten, not shimmed.
* Current managed Electric code is coupled to Postgres concepts such as `DatabaseURL`, replication stream IDs, and Postgres lock cleanup. For this migration, managed Electric should either be removed from active code or disabled with an explicit "not available after Postgres removal" diagnostic until a SQLite-compatible sync design exists.
* SQLite snapshots and branch copies must not naïvely copy only the `.sqlite` file while a WAL file may contain committed data. Use a safe backup method such as SQLite backup API or `VACUUM INTO` into a temporary file, then atomic rename.
* The final grep must distinguish active code from historical plan prose. Historical completed plans may mention Postgres; active code and current user-facing docs should not.
* 2026-06-27 - Fresh inventory confirmed active Postgres code in `db`, `pgxpool`, `auth`, `cmd/scenery` DB/dev/branch/dashboard paths, `internal/testpostgres`, `internal/agent/types.go`, `scenery.toolchain.json`, config/docs, and generated auth sqlc. `go list -deps ./...` still includes `github.com/jackc/pgx/v5`, `github.com/lib/pq`, `scenery.sh/pgxpool`, and `scenery.sh/internal/testpostgres`.

Add new surprises here with the command, test, or file that exposed them.

## Decision Log

* 2026-06-27 - Use service names, not a single app-wide database identity. Rationale: the user requirement is "1 service = 1 SQLite database," and a single `DatabaseURL` cannot represent multiple databases without ambiguity. Author: initial ExecPlan.
* 2026-06-27 - Use `database/sql` for Scenery's public DB helper instead of exposing a SQLite-driver-specific connection type. Rationale: Scenery should keep the app-facing API small and avoid leaking driver internals. Author: initial ExecPlan.
* 2026-06-27 - Prefer a pure-Go SQLite driver for the first implementation unless module compatibility or correctness forces a change. Rationale: removing Postgres and Docker should reduce external local-dev dependencies; requiring CGO would reintroduce host setup friction. Record the final driver choice here after `go test ./...` passes. Author: initial ExecPlan.
* 2026-06-27 - Do not preserve `scenery db postgres` or `scenery db psql` as compatibility aliases. Rationale: the requested migration removes Postgres-related Scenery code; keeping aliases would keep stale mental models and tests alive. Author: initial ExecPlan.
* 2026-06-27 - Keep `runtime/dbtrace.go` conceptually, but remove the pgx-specific tracing wrapper. Rationale: DB traces are useful and generic; pgx instrumentation is the obsolete piece. Author: initial ExecPlan.
* 2026-06-27 - Remove or explicitly disable managed Electric in this migration unless it is fully decoupled from Postgres in the same change. Rationale: current managed Electric is Postgres-backed; leaving it active while removing Postgres would create broken runtime paths. Author: initial ExecPlan.
* 2026-06-27 - Standard auth must be SQLite-native before Postgres dependencies are removed. Rationale: auth currently imports pgx and generated pgx sqlc code; removing dependencies first would break compilation. Author: initial ExecPlan.
* 2026-06-27 - Use `modernc.org/sqlite` as the SQLite driver. Rationale: `go get modernc.org/sqlite@latest` succeeded and keeps SQLite pure-Go without CGO host setup. Author: implementation.

When implementation chooses exact driver, CLI grammar, schema shape, or auth storage strategy, append new decision entries with date, rationale, and author.

## Outcomes & Retrospective

Outcome:

- App authors now declare managed local databases as `dev.services.<name>.kind: "sqlite"`. Each service resolves to one SQLite file and receives its configured URL env plus a service-specific path env. `DatabaseURL` remains only as the configured/default single-service app alias and is not injected when multiple SQLite services make it ambiguous.
- Agents now inspect, snapshot, reset, branch, diff, and shell into managed service databases through SQLite-backed `scenery db ...` commands. Branch isolation is file-backed, self-harness-proven, and leaves no stale current-branch pin after forced deletion.
- Standard auth, generated model stores, public `scenery.sh/db`, DB setup/seed flows, branch lifecycle, docs, schemas, toolchain metadata, fixtures, and dashboard DB copy now use SQLite/database/sql.
- Removed active Postgres surfaces: managed Postgres dev substrate, Postgres DB CLI/provider files, public `pgxpool`, internal Postgres test helper, Postgres fixture app, Atlas Postgres auth generation defaults, Postgres toolchain image artifact, direct `github.com/jackc/pgx/v5` and `github.com/lib/pq` dependencies, and active `psql`/`pg_dump` command paths.
- Intentional compatibility that remains: current historical ExecPlans and docs index history may mention the removed Postgres system; config/CLI rejection paths still reject removed old spellings with clear errors; `DatabaseURL` remains a conventional app env name only when explicitly configured or unambiguous.

Validation:

- `go test ./cmd/scenery ./internal/app ./internal/sqlitedb ./db ./runtime ./internal/toolchain ./internal/envpolicy ./internal/codegen` passed.
- `go test ./cmd/scenery ./internal/envpolicy` passed after harness/env registry fixes.
- `go test ./cmd/scenery` passed after branch delete and fixture cleanup.
- `go test ./...` passed after all implementation and final cleanup.
- `cd ui && bun run typecheck && bun run test && bun run build` passed; Vite emitted existing Lightning CSS unknown at-rule warnings but exited 0, with 8 UI test files and 27 tests passing.
- `./scripts/build-dashboard-ui-embed.sh` passed; it also emitted the same Lightning CSS at-rule warnings and exited 0.
- `scenery inspect docs --json` passed with 65 documents, 0 missing, 0 review-due, 0 stale.
- `go list -deps ./... | rg 'pgx|pq|postgres'` returned no matches.
- Active-surface `git grep -n -i -e postgres -e postgresql -e 'github.com/jackc/pgx' -e 'github.com/lib/pq' -e pgxpool -e psql -e pg_dump -e pg_database -e pg_class -e pg_namespace -e pg_indexes -e SCENERY_DEV_POSTGRES -e SubstratePostgres -- .` with historical/deleted path exclusions returned no matches.
- `go run ./cmd/scenery harness self --summary --write` passed with status `pass_with_warnings`: 0 errors; warnings were existing architecture large-file warnings and slow-test timing warnings. Key passing steps included knowledge contract, inspect docs, architecture checks with no errors, contract drift checks, UI static architecture, Go tests, parallel worktree runtimes, SQLite branch lifecycle, dashboard UI typecheck/build/freshness, fixture matrix 9/9, storage fixture probe, and schema validation.

Follow-up:

- No migration of old local Postgres state is attempted; users with old state should delete stale state or recreate SQLite branches through the documented commands.
- The self-harness still reports existing large-file and slow-test warnings. They are not blockers for this migration and should be handled by the existing harness/test-suite quality plans rather than expanding this database migration.

## Context and Orientation

Read these files before editing:

```text
AGENTS.md
PLANS.md
docs/plans/active.md
docs/local-contract.md
docs/agent-guide.md
docs/environment.md
docs/environment.registry.json
docs/schemas/scenery.config.v1.schema.json
internal/app/root.go
db/db.go
pgxpool/pgxpool.go
cmd/scenery/dev_services.go
cmd/scenery/psql.go
cmd/scenery/db_postgres.go
cmd/scenery/db_postgres_branch_provider.go
cmd/scenery/db_branch_commands.go
cmd/scenery/db_branch_pin.go
cmd/scenery/db_branch_types.go
cmd/scenery/db_branch_utils.go
cmd/scenery/doctor.go
internal/agent/types.go
auth/standard.go
auth/standard_service.go
auth/db/gen/db.go
auth/db/gen/schema.sql
scripts/gen-auth-sqlc.sh
atlas.hcl
scenery.toolchain.json
internal/testpostgres/postgres.go
```

The repo's root `AGENTS.md` is authoritative for agent behavior. It says not to spawn subagents or background agent tasks. Do all implementation, exploration, and validation in the main session. Before changing non-trivial code, read the root instructions and any narrower `AGENTS.md` files for the directories being edited. At the time this plan was written, the root file said there were no child `AGENTS.md` files indexed, but re-check before editing.

Current Postgres hotspots:

```text
go.mod
  Direct Postgres dependencies: github.com/jackc/pgx/v5, github.com/lib/pq.

pgxpool/
  Scenery wrapper around pgxpool with DB trace instrumentation.

db/db.go
  Shared default database helper. Resolves DatabaseURL and SCENERY_MANAGED_DATABASE_URL.
  Requires dev.services.postgres. Returns *pgxpool.Pool.

cmd/scenery/dev_services.go
  Managed Postgres plan/server/substrate.
  Docker postgres image startup.
  Admin URL, database create/reset/drop helpers.
  Managed Electric integration that assumes a Postgres database URL and Postgres replication/lock behavior.

cmd/scenery/db_postgres.go
  CLI group for scenery db postgres install/start/status/logs/stop/restart/uninstall.

cmd/scenery/psql.go
  db psql/apply/seed/setup/reset/drop/snapshot/diff/branch/postgres routing.
  pg_dump snapshot and psql restore.
  Managed Postgres lifecycle env and current-session database plan.

cmd/scenery/db_postgres_branch_provider.go
  Postgres branch provider.
  Uses CREATE DATABASE WITH TEMPLATE, pg_database, pg_class, pg_namespace, pg_indexes, pg_terminate_backend.

cmd/scenery/db_branch_*.go
  Generic-ish branch command shell, but hardwired to postgresBranchProvider and postgres constants.

auth/
  Standard auth uses pgxpool, pgx.Tx, pgtype.UUID, pgconn errors, generated pgx sqlc package.
  Generated schema is Postgres SQL.

internal/testpostgres/
  Test helper starts/caches Docker Postgres.

docs/schemas/scenery.config.v1.schema.json
  dev.services.kind allows postgres/electric/zerofs and documents Postgres-specific fields.

scenery.toolchain.json
  Contains postgres:18 image artifact.

internal/agent/types.go
  Contains SubstratePostgres constant.
```

Target SQLite mental model:

```text
Service database:
  A named SQLite database file owned by one dev service entry.

SQLite service:
  A dev.services map entry with kind "sqlite". The map key is the service/database identity.

SQLite service URL:
  A Scenery-normalized URL, sqlite:///absolute/path/to/file.sqlite.
  This is app-facing configuration, not a driver-specific DSN.

SQLite service path:
  The absolute file path to the database. Also injected separately as <SERVICE>_DATABASE_PATH.

SQLite database registry:
  A machine-readable JSON env value and inspect result listing all SQLite service databases.

SQLite branch:
  A set of SQLite files, one per service, stored under one branch directory and referenced by a worktree pin.
```

## Milestones

### Milestone 0 - Plan, inventory, and safety rails

Add this ExecPlan, link it from `docs/plans/active.md`, and collect a fresh Postgres inventory. Do not edit implementation before the inventory is captured in `Surprises & Discoveries`.

Successful milestone result:

```text
docs/plans/0088-sqlite-service-databases.md exists.
docs/plans/active.md links it.
git grep inventory has been recorded.
No implementation files changed yet except plan/index files.
```

### Milestone 1 - Config model: `sqlite` services replace `postgres` services

Update app config parsing and JSON schema so `dev.services.*.kind` accepts `sqlite` and no longer exposes Postgres-only service semantics in current contract docs. Add helper functions in `internal/app` or a small database config package to discover SQLite services.

Successful milestone result:

```text
A config with two sqlite services parses.
Config schema accepts kind sqlite.
Config schema no longer advertises Postgres as a current managed service kind.
Unit tests cover service env defaults and duplicate/invalid service names.
```

### Milestone 2 - SQLite resolver and file lifecycle package

Add a package that resolves SQLite service definitions into absolute file paths, app-facing URLs, and env values. This package must create parent directories idempotently, initialize database files safely, and expose helpers for copy/snapshot/reset.

Successful milestone result:

```text
Given app root + cfg + optional session, code returns deterministic service DB records.
Files are created under .scenery/... paths.
Foreign keys, busy timeout, and WAL behavior are configured for opened DBs.
Unit tests cover path layout, URL parsing, and idempotent creation.
```

### Milestone 3 - Replace `db` and remove `pgxpool`

Rewrite `db/db.go` to return SQLite `database/sql` handles by service name. Delete the `pgxpool/` package after callers are migrated.

Successful milestone result:

```text
db.Get(ctx, "auth") returns *sql.DB or a Scenery wrapper around *sql.DB.
db.Get(ctx) works only when exactly one SQLite service exists.
No code imports scenery.sh/pgxpool.
No code imports github.com/jackc/pgx/v5 through the db package.
```

### Milestone 4 - Dev runtime env injection

Replace `managedPostgresEnv` and Postgres database lifecycle env with SQLite service env injection. `scenery up` should create and inject one database per SQLite service.

Successful milestone result:

```text
scenery up injects AUTH_DATABASE_URL and BILLING_DATABASE_URL for the fixture app.
scenery up emits dev events for sqlite service databases.
SCENERY_SQLITE_DATABASES_JSON is present and parseable.
No Postgres admin URL or Postgres substrate is needed.
```

### Milestone 5 - SQLite CLI replaces Postgres CLI

Replace `scenery db psql` and `scenery db postgres` with SQLite-oriented CLI commands. Update parser, help, tests, docs, and local contract.

Successful milestone result:

```text
scenery db shell <service> opens sqlite3 when sqlite3 is installed.
scenery db path <service> prints the database path.
scenery db list --json prints all SQLite service DBs.
scenery db reset/drop/snapshot/restore operate on SQLite service DB files.
scenery db postgres returns unknown command or no longer exists.
scenery db psql returns unknown command or no longer exists.
```

### Milestone 6 - SQLite branch lifecycle

Replace Postgres branch provider with SQLite file-backed branch provider. The branch provider operates on all SQLite service files as one logical branch set.

Successful milestone result:

```text
scenery db branch checkout feature/a creates one branch DB file per service.
feature/a and feature/b are isolated.
reset restores branch DB files from parent.
delete removes branch DB files and lease records.
diff compares SQLite schemas, not Postgres catalogs.
Existing branch command JSON schemas are updated or replaced.
```

### Milestone 7 - Standard auth port

Port standard auth to SQLite and remove pgx/sqlc generated Postgres code. Auth must continue to support signup, login, refresh, logout, password reset, organizations, and impersonation behavior.

Successful milestone result:

```text
auth package compiles without pgx, pgtype, pgconn, auth/db/gen pgx DBTX, or scenery.sh/pgxpool.
Auth schema is SQLite SQL.
Auth tests pass against temp SQLite files.
Duplicate email and duplicate token behavior maps to existing Scenery errors.
```

### Milestone 8 - Test infrastructure replacement

Delete `internal/testpostgres` and replace all test usage with `internal/testsqlite` or per-test temp DB helpers.

Successful milestone result:

```text
No test starts Docker Postgres.
No test requires SCENERY_TEST_DATABASE_URL.
SQLite tests create temp files and clean up automatically.
go test ./... does not require Docker for database tests.
```

### Milestone 9 - Toolchain, doctor, agent substrate, and Electric cleanup

Remove Postgres image artifact, Postgres substrate constants, Postgres storage-size checks, and managed Electric Postgres coupling. Managed Electric should be removed from current service startup or explicitly disabled until a separate SQLite sync design exists.

Successful milestone result:

```text
scenery.toolchain.json has no postgres artifact.
internal/agent/types.go has no SubstratePostgres.
doctor does not report Postgres database storage.
managed Electric no longer imports or calls Postgres helpers.
```

### Milestone 10 - Docs, schemas, skill, knowledge, and harness

Update all current docs and machine-readable schemas affected by the database model. Historical completed plans may remain historical, but current docs must describe SQLite service databases.

Successful milestone result:

```text
docs/local-contract.md describes SQLite DB CLI and JSON contracts.
docs/agent-guide.md describes agent workflow for per-service SQLite DBs.
SKILL.md describes app-facing SQLite database use.
docs/environment.md and docs/environment.registry.json include new envs.
docs/schemas/scenery.config.v1.schema.json accepts sqlite service definitions.
docs/knowledge.json references updated docs/plans.
scenery harness self --summary --write passes or failures are recorded.
```

### Milestone 11 - Postgres removal sweep and final validation

Remove all active Postgres references, run full validation, and update this ExecPlan with outcome.

Successful milestone result:

```text
git grep active-code inventory has no Postgres implementation references.
go test ./... passes.
go test ./cmd/scenery ./auth ./db ./runtime passes.
scenery harness self --summary --write passes when practical.
This ExecPlan has completed progress and retrospective.
```

## Plan of Work

The migration should be staged so the repo remains understandable at every point. Do not start by deleting Postgres files. First introduce the SQLite model, then migrate callers, then delete Postgres-specific files once nothing imports them.

Work in this order:

1. Record the current Postgres inventory.
2. Add SQLite service discovery and schema support.
3. Add SQLite resolver/file lifecycle package.
4. Rewrite `db` package around SQLite services.
5. Migrate dev runtime env injection.
6. Rewrite CLI surfaces.
7. Rewrite branch lifecycle.
8. Port auth.
9. Replace tests.
10. Remove Postgres substrate/toolchain/docs.
11. Run final grep and validation.

The implementation should avoid broad compatibility shims. Scenery should expose one current model: SQLite service databases. Avoid keeping dead aliases such as `postgres`, `psql`, `pg_dump`, `SCENERY_DEV_POSTGRES_ADMIN_URL`, or `SubstratePostgres`.

The only compatibility exception is `DatabaseURL`, and only under this rule:

```text
Inject DatabaseURL only when there is exactly one SQLite service and that service did not configure another explicit env name that conflicts with DatabaseURL.
```

When multiple SQLite services exist, require service-specific envs and service-specific CLI selection.

### New config behavior

A SQLite service entry uses the existing `DevServiceConfig` shape where possible:

```go
type DevServiceConfig struct {
    Kind           string            `json:"kind"`
    DatabaseURLEnv string           `json:"database_url_env"`
    Database       string           `json:"database"`
    Env            map[string]string `json:"env"`
    // Existing fields that only made sense for Postgres should be removed
    // from current docs/schema or ignored with validation errors.
}
```

The service map key is the canonical service name. For:

```json
"services": {
  "auth": { "kind": "sqlite" }
}
```

defaults are:

```text
service name: auth
database file: auth.sqlite
database URL env: AUTH_DATABASE_URL
database path env: AUTH_DATABASE_PATH
```

If `database_url_env` is set, use it. If `database` is set, use it as a file label after sanitization, not as a server database name.

Reject or warn on Postgres-only fields for SQLite services:

```text
mode
version
isolation
project
parent_branch
parent_database
branch_policy
branch_name_template
ttl
role
image
route
```

Some of these may still be used by non-database services; remove them from current SQLite examples and validate where safe.

### SQLite path layout

Use deterministic app-local paths. For a dev session:

```text
<app-root>/.scenery/sessions/<session_id>/sqlite/<service>.sqlite
```

For no session but local dev helper use:

```text
<app-root>/.scenery/sqlite/local/<service>.sqlite
```

For branches:

```text
<app-root>/.scenery/db/branches/<branch_id>/<service>.sqlite
```

For branch parent template:

```text
<app-root>/.scenery/db/branches/main/<service>.sqlite
```

For snapshots:

```text
<app-root>/.scenery/db/snapshots/<snapshot_name>/<service>.sqlite
```

Use atomic writes for registry files. For database file replacement, write or backup to a temporary file in the same directory and then atomic rename.

### SQLite URL format

Use app-facing URLs:

```text
sqlite:///absolute/path/to/auth.sqlite
```

Also inject paths separately:

```text
AUTH_DATABASE_PATH=/absolute/path/to/auth.sqlite
```

The internal SQLite package may convert URLs to driver DSNs. Do not expose driver-specific DSNs as the stable public contract unless there is a clear reason and it is documented.

### SQLite connection rules

Every opened DB connection should apply or verify:

```sql
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
PRAGMA journal_mode = WAL;
```

Do not assume pragmas persist for every connection. Ensure they apply when `db.Get` opens a new handle and in tests.

For snapshot, branch clone, and reset, prefer one of these safe mechanisms:

1. SQLite backup API if available through the chosen driver.
2. `VACUUM INTO '<tmpfile>'` from a read connection, then atomic rename.
3. File copy only after a deliberate checkpoint and only when the source is not being concurrently written.

Record the final mechanism in `Decision Log`.

### Auth storage strategy

Use one of two strategies:

```text
Preferred first cut:
  Handwritten database/sql store with a small authStore interface.

Alternative:
  Regenerate sqlc for SQLite if it produces clean database/sql code and reduces manual query risk.
```

The preferred first cut is handwritten because current generated code is tightly coupled to pgx types and Postgres SQL. The migration should define a small interface that the existing auth service calls, then implement it with SQLite. This reduces leakage of SQLite driver details into service logic.

Replace Postgres schema namespace with table prefixes:

```text
scenery_auth.users                  -> scenery_auth_users
scenery_auth.tenants                -> scenery_auth_tenants
scenery_auth.auth_identities        -> scenery_auth_auth_identities
scenery_auth.refresh_sessions       -> scenery_auth_refresh_sessions
scenery_auth.one_time_tokens        -> scenery_auth_one_time_tokens
scenery_auth.organization_memberships -> scenery_auth_organization_memberships
...
```

Represent UUIDs as text. Represent timestamps as UTC RFC3339 text or integer Unix time. Choose one, record it in `Decision Log`, and use it consistently. JSON metadata can be stored as text; validate JSON at the application boundary or use SQLite JSON functions only if the chosen driver/environment reliably supports them.

### Electric strategy

Managed Electric is not part of the first SQLite database cut. Remove its Postgres-specific managed DB coupling, or disable managed Electric with a clear diagnostic:

```text
managed Electric is unavailable in this SQLite-only Scenery build; SQLite-compatible sync is not implemented yet
```

Do not keep code that silently expects a Postgres `DatabaseURL`.

## Concrete Steps

### Step 0.1 - Add the ExecPlan

Create:

```text
docs/plans/0088-sqlite-service-databases.md
```

Paste this plan into that file.

Update:

```text
docs/plans/active.md
```

Add a link to this plan. Keep the ordering consistent with current active plan conventions.

Run:

```sh
git diff -- docs/plans/0088-sqlite-service-databases.md docs/plans/active.md
```

Record the result in `Progress`.

### Step 0.2 - Fresh inventory

Run from repo root:

```sh
git grep -n -i \
  -e postgres \
  -e postgresql \
  -e 'github.com/jackc/pgx' \
  -e 'github.com/lib/pq' \
  -e pgxpool \
  -e psql \
  -e pg_dump \
  -e pg_database \
  -e pg_class \
  -e pg_namespace \
  -e pg_indexes \
  -e SCENERY_DEV_POSTGRES \
  -e SubstratePostgres \
  -- .
```

Also run:

```sh
go list -deps ./... | grep -E 'pgx|pq|postgres' || true
```

Append a short summary to `Surprises & Discoveries`. Do not paste huge output into the plan; mention the command and the key files.

### Step 1.1 - Add SQLite service discovery

Edit:

```text
internal/app/root.go
```

Add functions near existing config helpers:

```go
func (c Config) SQLiteServices() []SQLiteServiceConfig
func (c Config) SQLiteService(name string) (SQLiteServiceConfig, bool)
```

or place them in a new package if it avoids bloating `internal/app`.

Suggested types:

```go
type SQLiteServiceConfig struct {
    Name           string
    FileLabel      string
    DatabaseURLEnv string
    DatabasePathEnv string
    Raw            DevServiceConfig
}
```

Rules:

```text
kind must be sqlite
service name is the dev.services map key
file label defaults to service name
database_url_env defaults to upper snake service name + _DATABASE_URL
database path env defaults to upper snake service name + _DATABASE_PATH
```

Add tests for:

```text
one sqlite service
two sqlite services
database_url_env override
database filename override
empty service map
legacy postgres service rejected or ignored according to current validation decision
```

### Step 1.2 - Update config schema

Edit:

```text
docs/schemas/scenery.config.v1.schema.json
```

Change `devService.kind` to allow `sqlite` and remove `postgres` from current active schema. Keep `electric` only if still actively supported after this migration. If managed Electric is disabled or removed, remove it from current service kinds too.

Update descriptions for database fields. Do not describe Postgres branching in the current config schema.

Add SQLite-specific optional fields only if needed. Prefer reusing:

```text
database_url_env
database
env
```

Avoid adding env-var knobs unless this plan records why they are needed.

### Step 1.3 - Update config validation tests

Search for config schema tests:

```sh
git grep -n "scenery.config.v1.schema" .
git grep -n "dev.services" cmd internal docs | head -100
```

Update tests that expect `postgres` as a valid service kind. Add tests that expect `sqlite` as valid.

### Step 2.1 - Add SQLite resolver package

Create a new package, for example:

```text
internal/sqlitedb/
```

Files:

```text
internal/sqlitedb/service.go
internal/sqlitedb/path.go
internal/sqlitedb/url.go
internal/sqlitedb/env.go
internal/sqlitedb/copy.go
internal/sqlitedb/schema.go
internal/sqlitedb/service_test.go
internal/sqlitedb/path_test.go
internal/sqlitedb/url_test.go
internal/sqlitedb/copy_test.go
```

Suggested public internal API:

```go
type Service struct {
    Name           string `json:"name"`
    FileLabel      string `json:"file_label"`
    Path           string `json:"path"`
    URL            string `json:"url"`
    DatabaseURLEnv string `json:"database_url_env"`
    DatabasePathEnv string `json:"database_path_env"`
}

type ResolveRequest struct {
    AppRoot   string
    Config    app.Config
    SessionID string
    BranchID  string
    Mode      Mode
}

type Mode string

const (
    ModeLocal   Mode = "local"
    ModeSession Mode = "session"
    ModeBranch  Mode = "branch"
)

func ResolveServices(req ResolveRequest) ([]Service, error)
func ResolveService(req ResolveRequest, name string) (Service, error)
func EnsureFiles(ctx context.Context, services []Service) error
func Env(services []Service, includeDatabaseURLAlias bool) []string
func ParseURL(raw string) (path string, err error)
func URLForPath(path string) string
func Backup(ctx context.Context, sourcePath, targetPath string) error
func Snapshot(ctx context.Context, services []Service, dir string) error
func DumpSchema(ctx context.Context, path string) (string, error)
```

Sanitization:

```text
service names: lower-case letters, digits, dash, underscore, dot after sanitization
file label: same, with .sqlite appended
reject names that sanitize to empty
reject path traversal
```

Path creation:

```go
os.MkdirAll(filepath.Dir(path), 0o755)
```

File creation:

```go
sql.Open(driverName, dsn)
PRAGMA foreign_keys = ON
PRAGMA busy_timeout = 5000
PRAGMA journal_mode = WAL
CREATE TABLE IF NOT EXISTS scenery_sqlite_metadata(...)
```

The metadata table is optional but useful. If added, use:

```sql
CREATE TABLE IF NOT EXISTS scenery_sqlite_metadata (
  key TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
```

Store:

```text
service name
created by scenery
schema version
```

Do not add application tables here.

### Step 2.2 - Choose SQLite driver

In `go.mod`, add one SQLite driver. Preferred first attempt:

```sh
go get modernc.org/sqlite@latest
```

If this creates unacceptable module issues, record the reason in `Decision Log` and switch to another driver. Do not leave two SQLite drivers in `go.mod`.

Create a single internal constant for the driver name:

```go
const DriverName = "sqlite"
```

or whatever the selected driver registers. Keep driver imports isolated in the SQLite package:

```go
import _ "modernc.org/sqlite"
```

Do not scatter blank imports across the repo.

### Step 2.3 - Safe backup/copy implementation

Implement database backup so branch clone, snapshot, and reset are safe with WAL.

Preferred implementation shape:

```go
func Backup(ctx context.Context, sourcePath, targetPath string) error {
    // Open source read-only.
    // Ensure target parent dir.
    // Write to targetPath + ".tmp".
    // Use SQLite backup API or VACUUM INTO tmp.
    // fsync/close where practical.
    // Rename tmp to targetPath.
}
```

If using `VACUUM INTO`, quote file paths safely. Do not concatenate unescaped user input into SQL. Add a helper:

```go
func quoteSQLiteString(s string) string {
    return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
```

Tests:

```text
backup creates target
backup replaces existing target atomically
backup preserves a table and inserted row
backup works when source has WAL mode enabled
failed backup does not leave corrupt target
```

### Step 3.1 - Rewrite `db/db.go`

Replace pgx-based API with SQLite service DB API.

Current external API likely has users calling:

```go
db.Get(ctx)
db.MustGet(ctx)
```

Use a varargs compatibility shape:

```go
func Get(ctx context.Context, service ...string) (*sql.DB, error)
func MustGet(ctx context.Context, service ...string) *sql.DB
func Close(service ...string) error
func CloseAll() error
```

Rules:

```text
Get(ctx, "auth") resolves auth service.
Get(ctx) works if exactly one sqlite service exists.
Get(ctx) errors if zero services exist.
Get(ctx) errors if more than one service exists.
Get(ctx) honors explicit configured env if present.
Get(ctx) falls back to Scenery-managed local file if config has sqlite service and env is absent.
```

State cache:

```go
type cachedDB struct {
    service string
    dsn     string
    db      *sql.DB
}
```

Cache per service+path. If the resolved path changes, close the old handle.

Error messages must name the service and app root:

```text
scenery db: sqlite service "auth" database URL is not configured and no managed file could be resolved for app root ...
scenery db: multiple sqlite services configured; call db.Get(ctx, "auth") instead of db.Get(ctx)
```

### Step 3.2 - Remove `pgxpool/` callers

Search:

```sh
git grep -n "scenery.sh/pgxpool\|pgxpool\." .
```

Migrate each caller to `database/sql` or the new `db` package. Delete:

```text
pgxpool/pgxpool.go
```

after no callers remain.

### Step 3.3 - Preserve DB tracing where practical

`runtime/dbtrace.go` can remain. Add optional tracing in the new `db` package if useful.

Minimum acceptable behavior:

```text
Postgres tracing wrapper is removed.
runtime DB trace helpers still compile and are available for future database instrumentation.
No Postgres-specific trace code remains.
```

Better behavior:

```go
type DB struct {
    service string
    inner   *sql.DB
}

func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
    traceCtx := runtime.TraceDBQueryStart(ctx, query, len(args))
    res, err := d.inner.ExecContext(ctx, query, args...)
    runtime.TraceDBQueryEnd(traceCtx, sqliteCommandTag(query), rowsAffected(res), err)
    return res, err
}
```

If returning `*sql.DB` directly is more important for compatibility, do not block the migration on wrapper tracing. Record the decision.

### Step 4.1 - Replace managed Postgres env injection

Edit:

```text
cmd/scenery/dev_services.go
cmd/scenery/dev_supervisor.go
cmd/scenery/psql.go
```

Remove or replace:

```text
managedPostgresDeclared
resolveManagedPostgresPlan
managedPostgresEnv
envWithManagedPostgresAdminURL
ensureLocalManagedPostgresSubstrate
startLocalManagedPostgres
startLocalManagedPostgresContainer
managedPostgresAdminReachable
postgresAdminVersionMatches
ensureManagedPostgresDatabase
resetManagedPostgresDatabase
dropManagedPostgresDatabase
postgresDatabaseURL
managedPostgresDatabaseName
postgresIdentifierPart
```

Add:

```go
func managedSQLiteEnv(ctx context.Context, appRoot string, cfg app.Config, session *localagent.Session, baseEnv []string) ([]string, []sqlitedb.Service, error)
```

Behavior:

```text
If no sqlite services exist, return nil.
If sqlite services exist, resolve session/local paths.
Create database files.
Return env for every service.
If exactly one service and no explicit DatabaseURL conflict, include DatabaseURL alias.
Emit dev events with service, path, and source=sqlite.
```

Update `devSupervisor.managedAppEnv` to call SQLite env injection instead of Postgres env injection.

### Step 4.2 - Remove Postgres substrate from agent state

Edit:

```text
internal/agent/types.go
cmd/scenery/dev_services.go
cmd/scenery/dashboard.go
cmd/scenery/agent.go
```

Remove:

```go
SubstratePostgres = "postgres"
```

Remove code that gets, upserts, deletes, monitors, or displays Postgres substrates.

If UI or dashboard expects substrate rows generically, ensure removing Postgres does not break layout. It should simply no longer show a Postgres substrate.

### Step 4.3 - Managed Electric decision

Search:

```sh
git grep -n -i "electric.*postgres\|postgres.*electric\|ELECTRIC_REPLICATION\|cleanupManagedElectricPostgres\|managedElectricDatabaseURL" cmd internal
```

If managed Electric cannot be fully decoupled in this plan, disable it:

```go
func (s *devSupervisor) ensureManagedElectric(ctx context.Context) error {
    if _, _, ok := managedElectricDeclared(s.cfg); !ok {
        return nil
    }
    return fmt.Errorf("dev.services.electric is unavailable after the SQLite database migration; SQLite-compatible sync is not implemented yet")
}
```

Then remove Postgres lock/replication cleanup helpers.

If product requires keeping Electric in this same change, create a separate sub-milestone in this ExecPlan with a SQLite-specific sync design before editing. Do not leave partially decoupled Electric code.

### Step 5.1 - Rewrite DB command routing

Edit:

```text
cmd/scenery/psql.go
cmd/scenery/help.go
cmd/scenery/cli_contract_test.go
```

Rename `psql.go` if helpful:

```text
cmd/scenery/db.go
cmd/scenery/db_shell.go
cmd/scenery/db_lifecycle.go
```

Update `dbCommand` usage from:

```text
scenery db psql|apply|seed|setup|reset|drop|snapshot|diff|branch|postgres
```

to:

```text
scenery db list|path|shell|apply|seed|setup|reset|drop|snapshot|restore|diff|branch
```

Do not include `postgres` or `psql`.

### Step 5.2 - Implement `scenery db list`

Command:

```sh
scenery db list [--app-root <path>] [--json]
```

Text output:

```text
auth     /abs/path/.scenery/sessions/.../sqlite/auth.sqlite
billing  /abs/path/.scenery/sessions/.../sqlite/billing.sqlite
```

JSON output schema:

```json
{
  "schema_version": "scenery.db.sqlite.list.v1",
  "ok": true,
  "app": {
    "name": "example",
    "id": "example",
    "root": "/abs/app",
    "config_path": "/abs/app/.scenery.json"
  },
  "databases": [
    {
      "service": "auth",
      "path": "/abs/app/.scenery/...",
      "url_env": "AUTH_DATABASE_URL",
      "path_env": "AUTH_DATABASE_PATH",
      "url": "sqlite:///abs/app/.scenery/..."
    }
  ]
}
```

### Step 5.3 - Implement `scenery db path`

Command:

```sh
scenery db path <service> [--app-root <path>] [--json]
```

Errors:

```text
missing service name
unknown sqlite service "..."
multiple sqlite services configured; choose one
```

JSON schema:

```json
{
  "schema_version": "scenery.db.sqlite.path.v1",
  "ok": true,
  "service": "auth",
  "path": "/abs/path/auth.sqlite",
  "url": "sqlite:///abs/path/auth.sqlite"
}
```

### Step 5.4 - Implement `scenery db shell`

Command:

```sh
scenery db shell <service> [--app-root <path>] [-- sqlite3 args...]
```

Behavior:

```text
Find sqlite3 in PATH.
Resolve service path.
Run sqlite3 <path> plus user args.
If sqlite3 is missing, print a clear error with the resolved path.
```

No fallback to `psql`.

### Step 5.5 - Rewrite reset/drop/snapshot/restore

Commands:

```sh
scenery db reset [--service <name>]... [--app-root <path>] [--yes]
scenery db drop [--service <name>]... [--app-root <path>] [--yes]
scenery db snapshot create <name> [--service <name>]... [--app-root <path>]
scenery db snapshot restore <name> [--service <name>]... [--app-root <path>] [--yes]
```

Rules:

```text
No --service means all SQLite services.
If multiple services exist and command is destructive, require --yes.
reset empties/recreates selected database files or restores from branch parent if currently branched.
drop removes selected database files and WAL/SHM siblings.
snapshot create uses safe backup for each selected service.
snapshot restore atomically replaces selected service files from snapshot.
```

When removing a database, remove common SQLite sidecars:

```text
<path>
<path>-wal
<path>-shm
<path>-journal
```

Use best-effort sidecar cleanup and report failures.

### Step 5.6 - Database apply/setup/seed env

Update database lifecycle commands so they receive all SQLite service envs.

For `scenery db apply`:

```text
Run configured database.apply.command once with all SQLite service env vars.
If no database.apply.command is configured, return same style of error as before, but do not mention Postgres.
```

For `scenery db seed`:

```text
If existing seed semantics target one DatabaseURL, require --service when multiple SQLite services exist.
If seed files are command-based, pass all env vars.
If seed files are SQL-file based, add --service and apply to that SQLite DB.
```

Inspect existing `cmd/scenery/db_seed.go` before implementing. Preserve existing useful behavior where possible, but remove Postgres assumptions.

### Step 6.1 - Replace branch provider types

Edit:

```text
cmd/scenery/db_branch_types.go
cmd/scenery/db_branch_utils.go
cmd/scenery/db_branch_pin.go
cmd/scenery/db_branch_commands.go
```

Provider constants:

```go
const sqliteBranchProviderName = "sqlite"
```

Worktree pin shape should include per-service databases. Either revise `worktreeDBPin` or add a new versioned schema.

Suggested new pin schema:

```go
const dbBranchPinSchemaVersion = "scenery.db.branch.v2"

type worktreeDBPin struct {
    SchemaVersion string                       `json:"schema_version"`
    Provider      string                       `json:"provider"`
    Project       string                       `json:"project"`
    ParentBranch  string                       `json:"parent_branch"`
    Branch        string                       `json:"branch"`
    BranchID      string                       `json:"branch_id"`
    Databases     map[string]worktreeSQLiteDB `json:"databases"`
    SessionID     string                       `json:"session_id,omitempty"`
    WorktreeRoot  string                       `json:"worktree_root,omitempty"`
    CreatedBy     string                       `json:"created_by"`
    TTL           string                       `json:"ttl,omitempty"`
}

type worktreeSQLiteDB struct {
    Service string `json:"service"`
    Path    string `json:"path"`
    URL     string `json:"url"`
}
```

If maintaining the old schema reader is useful for migration, make it fail with an actionable error:

```text
old Postgres db branch pin found at .scenery/worktree-db.json; remove it and run scenery db branch checkout <name> to create SQLite branch files
```

Do not silently interpret old Postgres pins.

### Step 6.2 - Implement SQLite branch provider

Create:

```text
cmd/scenery/db_sqlite_branch_provider.go
```

Implement methods matching the old provider responsibilities:

```go
type sqliteBranchProvider struct {
    cfg     app.Config
    appRoot string
}

func (p sqliteBranchProvider) EnsureBranch(ctx context.Context, pin worktreeDBPin) (dbBranchBackendStatus, error)
func (p sqliteBranchProvider) InspectBranch(ctx context.Context, pin worktreeDBPin) dbBranchBackendStatus
func (p sqliteBranchProvider) Connection(ctx context.Context, pin worktreeDBPin, service string) (dbBranchConnectionInfo, error)
func (p sqliteBranchProvider) ResetBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) error
func (p sqliteBranchProvider) DeleteBranch(ctx context.Context, pin worktreeDBPin, branch string, opts dbBranchOptions) error
func (p sqliteBranchProvider) RestoreBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) (dbBranchRestorePoint, error)
func (p sqliteBranchProvider) DiffBranch(ctx context.Context, pin worktreeDBPin, target string, opts dbBranchOptions) (string, error)
```

Use service file backups for branch creation:

```text
if branch DB file missing:
  if parent DB file exists, backup parent into branch file
  else create empty branch DB file
```

For reset:

```text
for each service:
  backup parent into branch temp
  atomic replace branch file
```

For delete:

```text
remove branch dir
remove registry lease
if deleting current branch, remove worktree pin
```

For diff:

```text
for each service:
  dump SQLite schema for current branch
  dump SQLite schema for target branch
  produce unified text diff with service labels
```

SQLite schema dump should include:

```sql
SELECT type, name, tbl_name, sql
FROM sqlite_schema
WHERE name NOT LIKE 'sqlite_%'
ORDER BY type, name;
```

Also include pragma-derived metadata where useful:

```text
PRAGMA table_info(<table>)
PRAGMA index_list(<table>)
PRAGMA foreign_key_list(<table>)
```

### Step 6.3 - Branch registry

Keep a registry file, but make it SQLite provider-specific.

Path suggestion:

```text
<app-root>/.scenery/db/sqlite-branches.json
```

or keep the existing registry location if it is already generic. The registry must include provider `"sqlite"` and lease entries.

Schema version suggestion:

```text
scenery.db.branch.registry.v3
```

Update docs schemas:

```text
docs/schemas/scenery.db.branch.v1.schema.json
docs/schemas/scenery.db.branch.status.v1.schema.json
docs/schemas/scenery.db.branch.list.v1.schema.json
docs/schemas/scenery.db.branch.registry.v2.schema.json
```

Either replace these with v2/v3 schemas or update them if no external compatibility is required. Keep schema names honest; do not publish a schema that says Postgres endpoint when it now means SQLite file path.

### Step 6.4 - Branch harness

Replace:

```text
cmd/scenery/harness_postgres_branch.go
```

with:

```text
cmd/scenery/harness_sqlite_branch.go
```

Harness should create a temp app:

```json
{
  "name": "sqlite-branch-harness",
  "id": "sqlite-branch-harness",
  "dev": {
    "services": {
      "auth": { "kind": "sqlite", "database_url_env": "AUTH_DATABASE_URL" },
      "billing": { "kind": "sqlite", "database_url_env": "BILLING_DATABASE_URL" }
    }
  }
}
```

It should verify:

```text
checkout feature/a creates both auth and billing DB files
checkout feature/b creates isolated DB files
insert marker into feature/a auth does not appear in feature/b auth
reset removes marker from feature/a after copying parent
diff emits schema_version for SQLite branch diff
delete feature/b leaves one lease
```

Use SQLite SQL markers:

```sql
CREATE TABLE IF NOT EXISTS scenery_branch_marker(value TEXT PRIMARY KEY);
INSERT OR IGNORE INTO scenery_branch_marker(value) VALUES (?);
SELECT EXISTS(SELECT 1 FROM scenery_branch_marker);
```

No `$1` placeholders.

### Step 7.1 - Auth schema rewrite

Create:

```text
auth/db/sqlite_schema.sql
```

or:

```text
auth/schema/sqlite.sql
```

Convert current schema to SQLite.

Mapping:

```text
CREATE SCHEMA scenery_auth;          -> remove
"scenery_auth"."users"               -> scenery_auth_users
uuid                                 -> TEXT
timestamptz                          -> TEXT
jsonb                                -> TEXT
boolean                              -> INTEGER or BOOLEAN accepted by SQLite
now()                                -> app-supplied timestamp or CURRENT_TIMESTAMP
COMMENT ON TABLE                     -> remove
ARRAY[...] checks                    -> CHECK (provider IN ('email','google'))
partial unique index casts           -> SQLite-compatible partial unique indexes
```

Example:

```sql
CREATE TABLE IF NOT EXISTS scenery_auth_users (
  id TEXT PRIMARY KEY,
  display_name TEXT NOT NULL DEFAULT '',
  avatar_url TEXT NOT NULL DEFAULT '',
  primary_email TEXT NOT NULL DEFAULT '',
  normalized_primary_email TEXT NOT NULL DEFAULT '',
  email_verified_at TEXT,
  disabled_at TEXT,
  can_impersonate_users INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS scenery_auth_users_normalized_primary_email_key
ON scenery_auth_users(normalized_primary_email)
WHERE normalized_primary_email <> '';
```

Apply schema idempotently during auth bootstrap.

### Step 7.2 - Auth store interface

Add:

```text
auth/store.go
auth/store_sqlite.go
auth/store_test.go
```

Define a storage interface around the behavior the service needs. Avoid passing pgx rows or pgtype structs through service logic.

Example shape:

```go
type authStore interface {
    WithTx(ctx context.Context, fn func(authStore) error) error

    FindUserByNormalizedEmail(ctx context.Context, email string) (authUser, error)
    CreateUser(ctx context.Context, user authUser) (authUser, error)
    CreateIdentity(ctx context.Context, identity authIdentity) error
    CreateTenant(ctx context.Context, tenant authTenant) (authTenant, error)
    ListUserMemberships(ctx context.Context, userID string) ([]authMembership, error)
    CreateOrganizationMembership(ctx context.Context, membership authMembership) error
    CreateRefreshSession(ctx context.Context, session authRefreshSession) (authRefreshSession, error)
    FindRefreshSessionByTokenHash(ctx context.Context, tokenHash string) (authRefreshSession, error)
    UpdateRefreshSessionRotation(ctx context.Context, params ...) error
    InsertAuthEvent(ctx context.Context, event authEvent) error
    ...
}
```

The exact methods should be derived from the current `authdb.Queries` calls. Do not create one giant generic `Exec` interface. Use domain methods so the rest of auth does not depend on SQL details.

### Step 7.3 - Replace pgx types in auth

Edit:

```text
auth/standard.go
auth/standard_service.go
auth/standard_tokens.go
auth/standard_sessions.go
auth/standard_organizations.go
auth/standard_impersonation.go
auth/standard_google.go
auth/standard_dev.go
```

Remove imports:

```go
"github.com/jackc/pgx/v5"
"github.com/jackc/pgx/v5/pgconn"
"github.com/jackc/pgx/v5/pgtype"
"github.com/jackc/pgx/v5/pgxpool"
authdb "scenery.sh/auth/db/gen"
scenerypgxpool "scenery.sh/pgxpool"
```

Replace:

```text
pgtype.UUID -> string
pgx.ErrNoRows -> sql.ErrNoRows
pgconn unique violation -> SQLite constraint helper
pgx.Tx -> authStore.WithTx
```

Keep API response JSON unchanged where possible. UUID strings should remain strings in JSON.

### Step 7.4 - Standard auth DB resolution

Update `standardAuthService(ctx)`:

```text
Load dotenv.
Normalize config.
Find database URL from configured auth env.
If missing and app has sqlite service named auth, resolve it.
Open SQLite DB.
Apply auth schema if AutoBootstrapDatabase is true.
Create service with SQLite store.
Register cleanup with runtime.MarkServiceInitialized.
```

Default auth DB env:

```text
AUTH_DATABASE_URL when dev.services.auth.kind == sqlite
configured auth.database_url_env if provided
DatabaseURL only when exactly one sqlite service exists and no explicit auth env is configured
```

Record final default in `Decision Log`.

### Step 7.5 - Auth tests

Update or add tests for:

```text
schema bootstrap is idempotent
email signup creates user, identity, tenant, membership, refresh session
duplicate signup maps to AlreadyExists or current expected error
login succeeds
refresh rotates session
logout revokes session
password reset token flow works
dev bootstrap works
organization membership list works
impersonation permissions work
```

Use temp SQLite files. Do not require Docker.

### Step 8.1 - Delete testpostgres

Delete:

```text
internal/testpostgres/postgres.go
```

Create:

```text
internal/testsqlite/sqlite.go
```

Suggested API:

```go
package testsqlite

type Database struct {
    Path string
    URL  string
}

func Start(t testing.TB, name string) *Database
func Open(t testing.TB, name string) (*sql.DB, *Database)
```

Behavior:

```text
t.TempDir()
create <name>.sqlite
open with Scenery SQLite helper
register t.Cleanup close/remove
```

Search and replace test imports:

```sh
git grep -n "internal/testpostgres\|testpostgres" .
```

Migrate each test to `testsqlite`.

### Step 8.2 - Remove Docker Postgres test assumptions

Search:

```sh
git grep -n "SCENERY_TEST_DATABASE_URL\|postgres:17\|postgres:18\|Docker Postgres\|PostgreSQL docker" .
```

Remove or rewrite.

### Step 9.1 - Remove Postgres toolchain image

Edit:

```text
scenery.toolchain.json
```

Remove artifact:

```json
{
  "name": "postgres",
  "kind": "image",
  "version": "18",
  ...
}
```

Run any toolchain manifest tests. Search:

```sh
git grep -n '"postgres"' scenery.toolchain.json internal cmd docs
```

Remove code expecting a Postgres artifact.

### Step 9.2 - Doctor cleanup

Edit:

```text
cmd/scenery/doctor.go
cmd/scenery/doctor_test.go
```

Remove:

```text
Postgres database storage size
Docker requirement for managed Postgres
Postgres-specific suggestions
```

Add SQLite checks if useful:

```text
Scenery SQLite storage size under .scenery/sqlite or .scenery/db
sqlite3 CLI optional check only when db shell is relevant
```

Do not make `sqlite3` CLI required. Scenery can manage SQLite files through Go. The CLI is only needed for interactive shell.

### Step 9.3 - Remove Atlas/Postgres auth generation

Delete or replace:

```text
atlas.hcl
scripts/gen-auth-sqlc.sh
auth/db/schema.hcl
auth/db/gen/
```

If `auth/db/schema.hcl` still serves another purpose, replace its Postgres-specific content with SQLite schema docs or delete it.

If using handwritten auth store, remove sqlc generation for auth. If using SQLite sqlc, create a new script that does not mention Postgres or Docker.

### Step 9.4 - Update docs and knowledge

Update current docs:

```text
README.md
SKILL.md
docs/local-contract.md
docs/agent-guide.md
docs/app-development-cookbook.md
docs/environment.md
docs/environment.registry.json
docs/schemas/scenery.config.v1.schema.json
docs/schemas/scenery.db.*.schema.json
docs/knowledge.json
docs/plans/active.md
```

Current docs should teach:

```text
dev.services.<name>.kind = sqlite
one service = one DB file
service-specific env vars
scenery db list/path/shell
snapshot/reset/drop behavior
branch checkout/reset/delete/diff behavior
standard auth uses SQLite service named auth by default
```

Remove current docs that tell users to configure `dev.services.postgres`, use `scenery db psql`, run Postgres Docker, or use managed Postgres branches.

Historical plans under `docs/plans/completed.md` may mention Postgres as history. Do not rewrite historical records unless they are currently active docs.

### Step 10.1 - Remove Postgres implementation files

After compile errors have been resolved, delete:

```text
pgxpool/
cmd/scenery/db_postgres.go
cmd/scenery/db_postgres_branch_provider.go
cmd/scenery/harness_postgres_branch.go
internal/testpostgres/
```

Then run:

```sh
git grep -n "postgresBranchProvider\|postgresBranchProviderName\|managedPostgres\|Postgres" cmd internal auth db runtime -- .
```

Resolve remaining active-code references.

### Step 10.2 - Update `go.mod` and `go.sum`

Run:

```sh
go mod tidy
```

Then verify removed dependencies:

```sh
grep -E 'github.com/jackc/pgx|github.com/lib/pq|pgpassfile|pgservicefile|puddle' go.mod go.sum || true
```

If pgx remains because another dependency uses it indirectly, record why in `Surprises & Discoveries`. The goal is no direct Scenery Postgres implementation dependency.

### Step 10.3 - Compile early and often

Run after each major package migration:

```sh
go test ./db ./runtime
go test ./auth
go test ./cmd/scenery
```

Then:

```sh
go test ./...
```

Record any skipped command and why.

### Step 10.4 - Final Postgres grep

Run:

```sh
git grep -n -i \
  -e postgres \
  -e postgresql \
  -e 'github.com/jackc/pgx' \
  -e 'github.com/lib/pq' \
  -e pgxpool \
  -e psql \
  -e pg_dump \
  -e pg_database \
  -e pg_class \
  -e pg_namespace \
  -e pg_indexes \
  -e SCENERY_DEV_POSTGRES \
  -e SubstratePostgres \
  -- .
```

Classify any remaining matches:

```text
allowed:
  historical completed plans, if clearly historical
  this ExecPlan references Postgres as the removed system
  migration tests that assert old Postgres pins produce clear errors, if kept temporarily

not allowed:
  active Go code
  current README/SKILL/local-contract/agent-guide instructions
  current JSON schemas
  toolchain manifest
  environment registry
```

If active-code matches remain, continue removing them before marking this plan complete.

## Validation and Acceptance

### Required validation commands

Run from repo root:

```sh
go test ./...
go test ./cmd/scenery ./auth ./db ./runtime
scenery harness self --summary --write
```

If `scenery harness self --summary --write` cannot be run in the current environment, record exactly why in `Outcomes & Retrospective`.

Do not run `go install ./cmd/scenery` unless the human explicitly asks. Use the worktree-local harness build where possible.

### SQLite service fixture validation

Create a temporary fixture app outside the repo or under a test temp directory:

```json
{
  "name": "sqlite-fixture",
  "id": "sqlite-fixture",
  "dev": {
    "services": {
      "auth": {
        "kind": "sqlite",
        "database_url_env": "AUTH_DATABASE_URL"
      },
      "billing": {
        "kind": "sqlite",
        "database_url_env": "BILLING_DATABASE_URL"
      }
    }
  }
}
```

Run:

```sh
scenery db list --app-root <fixture> --json
scenery db path auth --app-root <fixture> --json
scenery db path billing --app-root <fixture> --json
scenery db snapshot create smoke --app-root <fixture>
scenery db reset --app-root <fixture> --yes
scenery db snapshot restore smoke --app-root <fixture> --yes
```

Acceptance:

```text
list returns exactly auth and billing
auth and billing paths are different files
both files exist after list/path resolution
snapshot creates two sqlite files
reset and restore do not corrupt files
```

### Branch lifecycle validation

Run:

```sh
scenery db branch checkout feature/a --app-root <fixture> --json
scenery db branch status --app-root <fixture> --json
scenery db branch checkout feature/b --app-root <fixture> --json
scenery db branch list --app-root <fixture> --json
scenery db branch diff feature/a --app-root <fixture> --json
scenery db branch delete feature/b --app-root <fixture> --force --json
```

Acceptance:

```text
status provider is sqlite
branch pin lists both auth and billing databases
feature/a and feature/b database files are distinct
diff emits SQLite branch diff schema version
delete removes feature/b lease/files
```

### Auth validation

Run auth package tests:

```sh
go test ./auth -run TestStandard
go test ./auth
```

Acceptance:

```text
auth package has no pgx imports
standard auth schema bootstraps into SQLite
signup/login/refresh/logout tests pass
organization and impersonation tests pass
duplicate constraints map to existing error semantics
```

### CLI contract validation

Run:

```sh
go test ./cmd/scenery -run 'Test.*DB|Test.*CLI|Test.*Doctor|Test.*Harness'
```

Acceptance:

```text
help text does not advertise psql or postgres
db command parser rejects postgres and psql
db list/path/shell/reset/drop/snapshot tests pass
doctor no longer reports Postgres database storage
harness no longer runs Postgres branch lifecycle
```

### Dependency acceptance

Run:

```sh
go list -deps ./... | grep -E 'pgx|pq|postgres' || true
```

Acceptance:

```text
No direct active Scenery dependency on pgx or lib/pq.
If a transitive dependency includes a word match, document it and prove active Scenery code does not import it.
```

### Grep acceptance

Run final grep from Step 10.4.

Acceptance:

```text
No active Go code mentions Postgres.
No current docs tell app authors to use Postgres.
No schema or environment registry exposes Postgres as a current capability.
Only historical completed plans or this migration plan may mention Postgres.
```

## Idempotence and Recovery

All file lifecycle operations must be retry-safe.

### Database creation

Creating SQLite service files must be idempotent:

```text
If directory exists, continue.
If file exists, open and apply pragmas.
If metadata table exists, update only Scenery-owned metadata keys.
```

### Atomic replacement

For reset, restore, and branch clone:

```text
write target.tmp in same directory
verify target.tmp opens as SQLite
rename target.tmp to target
remove stale sidecars for target
```

On failure:

```text
leave existing target untouched
remove target.tmp if possible
return actionable error naming service and path
```

### Snapshot safety

Do not use naïve file copy against a live WAL database. Use backup API or `VACUUM INTO`. If implementation falls back to file copy, it must first ensure no live writer and checkpoint WAL; record the limitation in `Decision Log`.

### Branch registry safety

Use existing atomic JSON write helper if available. Registry mutation must be lock-protected. If an operation fails after creating DB files but before writing registry, rerunning checkout should discover or overwrite the files safely.

### Recovery commands

Document these in user-facing docs:

```sh
scenery db list --json
scenery db path <service>
scenery db reset --service <service> --yes
scenery db branch status --json
scenery db branch reset --yes
```

For corrupted SQLite files, error messages should name the file and suggest snapshot restore or reset.

### Old Postgres state

Do not silently migrate old Postgres state. If old files or pins are detected:

```text
.scenery/worktree-db.json with provider postgres
agent substrate kind postgres
.scenery/agent/postgres
```

Return a diagnostic that tells the user to remove stale state or run a documented cleanup command. Do not attempt to connect to Postgres.

If adding cleanup support, use a SQLite/current name such as:

```sh
scenery down --state
```

or:

```sh
scenery db cleanup-stale --yes
```

Do not add `scenery db postgres cleanup`.

## Artifacts and Notes

Files expected to be added:

```text
docs/plans/0088-sqlite-service-databases.md
internal/sqlitedb/service.go
internal/sqlitedb/path.go
internal/sqlitedb/url.go
internal/sqlitedb/env.go
internal/sqlitedb/copy.go
internal/sqlitedb/schema.go
internal/sqlitedb/*_test.go
internal/testsqlite/sqlite.go
cmd/scenery/db_sqlite.go
cmd/scenery/db_shell.go
cmd/scenery/db_sqlite_branch_provider.go
cmd/scenery/harness_sqlite_branch.go
auth/store.go
auth/store_sqlite.go
auth/db/sqlite_schema.sql
```

Files expected to be heavily edited:

```text
go.mod
go.sum
internal/app/root.go
db/db.go
cmd/scenery/dev_services.go
cmd/scenery/dev_supervisor.go
cmd/scenery/psql.go
cmd/scenery/db_branch_commands.go
cmd/scenery/db_branch_pin.go
cmd/scenery/db_branch_types.go
cmd/scenery/db_branch_utils.go
cmd/scenery/help.go
cmd/scenery/doctor.go
cmd/scenery/*_test.go
auth/standard.go
auth/standard_service.go
auth/standard_*.go
docs/schemas/scenery.config.v1.schema.json
docs/local-contract.md
docs/agent-guide.md
docs/environment.md
docs/environment.registry.json
docs/app-development-cookbook.md
SKILL.md
README.md
docs/knowledge.json
docs/plans/active.md
scenery.toolchain.json
```

Files expected to be deleted:

```text
pgxpool/pgxpool.go
cmd/scenery/db_postgres.go
cmd/scenery/db_postgres_branch_provider.go
cmd/scenery/harness_postgres_branch.go
internal/testpostgres/postgres.go
auth/db/gen/db.go
auth/db/gen/*.go
auth/db/gen/schema.sql
scripts/gen-auth-sqlc.sh
atlas.hcl
```

Deletion of `auth/db/gen` depends on the final auth implementation choice. If using SQLite sqlc, replace `auth/db/gen` with SQLite-generated code instead of deleting generated storage entirely.

Generated or local artifacts not to commit:

```text
.scenery/
*.sqlite
*.sqlite-wal
*.sqlite-shm
coverage files
local env files
```

Add or verify `.gitignore` coverage for SQLite local DB files under `.scenery/`.

## Interfaces and Dependencies

### App config interface

Current target:

```json
{
  "dev": {
    "services": {
      "<service>": {
        "kind": "sqlite",
        "database_url_env": "<OPTIONAL_ENV_NAME>",
        "database": "<optional-file-label>"
      }
    }
  }
}
```

Rules:

```text
service map key is required and stable
kind must be sqlite
database_url_env optional
database optional and means file label, not server database
```

### Runtime env interface

For each service:

```text
<SERVICE>_DATABASE_URL
<SERVICE>_DATABASE_PATH
```

Global registry:

```text
SCENERY_SQLITE_DATABASES_JSON
```

Compatibility:

```text
DatabaseURL only when exactly one SQLite service exists and no explicit conflict exists.
```

### Go app helper interface

Package:

```go
import "scenery.sh/db"
```

Target API:

```go
func Get(ctx context.Context, service ...string) (*sql.DB, error)
func MustGet(ctx context.Context, service ...string) *sql.DB
func Close(service ...string) error
func CloseAll() error
```

Examples:

```go
authDB := db.MustGet(ctx, "auth")
billingDB := db.MustGet(ctx, "billing")
```

Ambiguous call:

```go
db.Get(ctx)
```

must fail when more than one SQLite service exists.

### CLI interface

Target current commands:

```text
scenery db list [--app-root <path>] [--json]
scenery db path <service> [--app-root <path>] [--json]
scenery db shell <service> [--app-root <path>] [-- sqlite3 args...]
scenery db apply [--app-root <path>] [--json]
scenery db seed [--service <name>] [--app-root <path>] [--json]
scenery db setup [--app-root <path>] [--json]
scenery db reset [--service <name>]... [--app-root <path>] --yes
scenery db drop [--service <name>]... [--app-root <path>] --yes
scenery db snapshot create <name> [--service <name>]... [--app-root <path>]
scenery db snapshot restore <name> [--service <name>]... [--app-root <path>] --yes
scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune ...
```

Removed commands:

```text
scenery db psql
scenery db postgres ...
```

### Branch JSON interface

Target status JSON should expose SQLite file paths instead of host/port/database/role endpoint objects.

Suggested status shape:

```json
{
  "schema_version": "scenery.db.branch.status.v2",
  "ok": true,
  "provider": "sqlite",
  "status": "pinned",
  "backend_status": "ready",
  "pin": {
    "schema_version": "scenery.db.branch.v2",
    "provider": "sqlite",
    "branch": "feature-a",
    "branch_id": "br-local-...",
    "databases": {
      "auth": {
        "service": "auth",
        "path": "/abs/path/auth.sqlite",
        "url": "sqlite:///abs/path/auth.sqlite"
      }
    }
  }
}
```

Update schemas and docs to match.

### Auth interface

External HTTP routes and response JSON should remain unchanged where possible. Internal auth storage changes from pgx/Postgres to SQLite.

Standard auth config:

```json
{
  "auth": {
    "enabled": true,
    "database_url_env": "AUTH_DATABASE_URL",
    "auto_bootstrap_database": true
  },
  "dev": {
    "services": {
      "auth": {
        "kind": "sqlite",
        "database_url_env": "AUTH_DATABASE_URL"
      }
    }
  }
}
```

### Dependency policy

Remove:

```text
github.com/jackc/pgx/v5
github.com/lib/pq
scenery.sh/pgxpool
Postgres Docker image artifact
Atlas Postgres dev URL
pg_dump/psql runtime dependence
```

Add exactly one SQLite driver. Keep it isolated behind Scenery's SQLite package.

### Documentation contract

Update current public docs in the same PR. The root agent instructions require docs and machine-readable knowledge to stay synchronized when behavior changes. Do not leave current docs teaching Postgres after this migration.

Final statement for docs:

```text
Scenery local database capability is SQLite-based. Each sqlite dev service owns one database file. Apps with multiple data services declare multiple sqlite services and receive one env var per service.
```
