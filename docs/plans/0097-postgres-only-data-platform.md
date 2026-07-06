# 0097 - Postgres-Only Data Platform: One Database Per App/Worktree, One Schema Per Service

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery's managed database model today is dual-engine: SQLite is the default (`dev.services.<name>.kind: "sqlite"`, one file per service, plans 0088/0089), and Postgres is opt-in (`kind: "postgres"`, one database per app/worktree/service on a shared Docker server, plan 0093). Auth, durable execution, cron schedules, the seed ledger, SQLite branching, the dashboard DB explorer, and the Symphony store are all SQLite-native.

This plan removes SQLite entirely and makes Postgres 18 the only database engine, with a simpler and stronger shape than plan 0093's per-service databases:

```text
one machine              = one Scenery-managed shared Postgres 18 server (Docker, plan-0093 substrate, unchanged)
one app root + worktree  = exactly ONE Postgres database: <sanitized app_id>_<short identity hash of app root>
one service              = one schema inside that database (service "projects" -> schema "projects")
scenery-native tables    = the "scenery" schema inside that same database (auth, durable, seed ledger, metadata)
durable execution        = ONE shared job store in the "scenery" schema with a service column — no per-service durable stores
external DSN             = one app-level DATABASE_URL always wins; Scenery then manages no server and no database
```

Why this shape:

* One database per app/worktree keeps worktree isolation (different identity hash → disjoint database on the same shared server) while making the whole app inspectable with one `psql` connection and one `pg_dump`.
* One schema per service preserves service-level namespacing (the property per-service SQLite files provided) without multiplying connections, databases, or DSNs. Postgres schemas are the native tool for exactly this.
* Scenery-native state (auth tables, durable jobs, seed ledger) moves into a clearly-owned `scenery` schema in the app's own database. App schemas never collide with scenery's tables, and scenery's tables ride along with every backup, branch-by-worktree, and external DSN automatically.
* A single durable store keyed by `service` replaces N per-service `<service>.durable.sqlite` files. Postgres's `FOR UPDATE SKIP LOCKED` is the natural leasing primitive and removes SQLite's single-writer contention.

When this plan is done: `internal/sqlitedb` and the `modernc.org/sqlite` dependency are gone; `.scenery.json` declares database services without any `kind`; `scenery up` ensures one database with one schema per service plus `scenery`; `scenery.sh/db` returns per-service `*sql.DB` pools pinned to the service schema via `search_path`; auth and durable run on Postgres in the `scenery` schema; the DB CLI (`list`, `shell`, `apply`, `seed`, `setup`, `reset`, `drop`, `snapshot`, `server`) is Postgres-only; `db path` and `db branch` are removed; docs/schemas/harness cover the new contract; and the onlv app runs on it end to end.

## Progress

* [x] 2026-07-06 - Surveyed the current dual-engine model (plans 0088/0089/0093), sqlite importer inventory, durable store schema, auth/sqlc wiring, symphony store, dashboard explorer, and the onlv app's SQLite state; drafted this plan.
* [x] 2026-07-06 - Registered this plan in `docs/plans/active.md` and `docs/knowledge.json`.
* [x] Milestone 1: Config surface — kind-less database services, app-level `database.url_env`, schema updates.
* [x] Milestone 2: `internal/postgresdb` extensions — app database naming, schema ensure, per-service URL derivation, registry v2.
* [x] Milestone 3: Dev runtime — one-database env injection, `scenery down --db`, sqlite injection removal.
* [x] Milestone 4: `scenery.sh/db` — Postgres-only dispatch, per-service search_path pools.
* [x] Milestone 5: Durable store rewrite on Postgres — single store in `scenery` schema, SKIP LOCKED leasing. Focused validation on 2026-07-06: `go build ./...` exited 0 with a sandbox-denied Go module stat-cache warning; `go test ./internal/durable/... ./durable ./db ./internal/postgresdb` passed; `go test ./runtime -run 'Test.*Durable|TestStartDurableRuntime'` passed; `SCENERY_TEST_DATABASE_URL=postgres://test:test@127.0.0.1:5433/test go test ./internal/durable/...` passed live; `go test ./cmd/scenery` was blocked by sandbox loopback bind denial in unrelated console tests, while the durable/worker/inspect subset passed live.
* [x] Milestone 6: Auth on Postgres — sqlc engine postgresql, tables in `scenery` schema, bootstrap.
* [x] Milestone 7: Seed ledger and apply/seed/setup lifecycle on Postgres.
* [x] Milestone 8: DB CLI — Postgres-only list/shell/reset/drop/snapshot; remove path/branch; `scenery.db.list.v3`.
* [x] Milestone 9: Symphony store and dashboard DB explorer on Postgres.
* [x] Milestone 10: Removed `internal/sqlitedb`, removed the sqlite driver dependency from `go.mod`/`go.sum`, removed sqlite spellings from current code/config/schema/docs surfaces, and reran the grep gates. The current-surface gates for `sqlite` and removed database registry env names exited with no matches; historical references remain only in plan prose.
* [x] Milestone 11: Made generator engine validation Postgres-only, replaced the mixed `postgres-basic` fixture with `db-basic`, refreshed tests, and extended `harness_self_postgres.go` to cover one app database, `scenery` plus service schemas, distinct worktree databases, durable round-trip, auth bootstrap, and service-only reset behavior.
* [x] Milestone 12: Swept docs, schemas, env registry, and knowledge metadata for the Postgres-only contract. Validation on 2026-07-06: `go build ./...` passed with a sandbox-denied Go module stat-cache warning; `SCENERY_TEST_DATABASE_URL=postgres://test:test@127.0.0.1:5433/test go test ./internal/durable/... ./auth/... ./internal/symphony/...` passed; `go test ./...` was blocked by sandbox loopback listener denials in packages using `httptest.NewServer`; JSON validation for `docs/knowledge.json` and `docs/environment.registry.json` passed; `go mod tidy` was blocked by the sandbox's read-only module-cache lock path; `go run ./cmd/scenery harness self --summary --write` built the worktree-local harness binary and passed UI typecheck/build, but failed on sandbox loopback listener denials, cache writes under `~/Library/Caches/scenery`, and Docker socket permission denial, with an explicit Postgres probe skip recorded for unavailable Docker.
* [x] 2026-07-06 - Post-wave verification and harness hardening on the host (outside the codex sandbox): fixed a wrong `internal/localagent` import alias, restored worktree name sanitization (`sanitizeWorktreeName` recovered from the deleted branch-pin file), removed the dead branch-lock tiers from `dev_named_lock.go` and ported its tests to the substrate lock, fixed Postgres SQL bugs the sandbox could not reach live (`symphony_app_counters` ambiguous `next_value` upsert, `attachLatestRuns`-adjacent `$1` reuse in the task-by-id query, `?` placeholders in a symphony test, an auth test asserting the search_path-relative `to_regclass` rendering), aligned `scenery.worktree.remove.v1` with the removed `db_pin_removed` field, gave `DevServiceConfig` legacy fields `omitempty` so `inspect app` output conforms to the tightened config schema, routed `inspect.go`/`worker.go` env access through `envpolicy`, and documented the registered `HOME` variable. Validation: `go build ./...`, full `go test ./...`, live `SCENERY_TEST_DATABASE_URL` runs for auth/durable/symphony, and `go run ./cmd/scenery harness self --summary --write` → `pass_with_warnings`, `can_proceed: true`, with the Postgres probe running live against Docker.
* [x] 2026-07-06 - Independent review round (gpt-5.5 via codex, read-only) found and we fixed: generated `//scenery:model` CRUD still emitting `?` placeholders and service-less `db.Get(ctx)` (blocker; now `$N` with runtime-ordinal update builder and per-service pool helper), unfenced durable job UPDATEs after lease verification (now `FOR UPDATE` row locks + state predicates + RowsAffected assertions), hard-coded 60s lease ignoring `durable_tasks.default_lease_ms` (now task-driven with 60s fallback), sanitized schema-name collisions accepted in config validation (now rejected), unsanitized snapshot names allowing path escape (now slug-restricted), and stale SQLite prose in DSL.md. One follow-up host fix: the new lease join made `ORDER BY created_at` ambiguous — qualified to `j.*`. Validation: `go build ./...`, full `go test ./...`, live `SCENERY_TEST_DATABASE_URL` runs for durable (including new lease-duration and stale-worker-cancel fencing tests), auth, symphony, runtime — all green; `go install ./cmd/scenery` refreshed for client testing.
* [x] 2026-07-06 - Milestone 13: onlv migrated and validated end to end. Client repo work (uncommitted in `/Users/petrbrazdil/Repos/onlv`): kind-less `.scenery.json` with 34 database services, all 34 `*/db/schema.sql` + 33 query files + 7 seeds converted to Postgres 18 (uuidv7 ids, timestamptz, `$N`), sqlc regenerated as postgresql, `scripts/db-apply.sh` rewritten (per-schema psql apply, non-destructive when a service schema already has tables), the composite `solar/db/seed.sql` split into per-service seeds with UUID ids, and `modernc.org/sqlite` removed. Scenery-side fix discovered by this milestone: DB artifact discovery only scanned top-level `<service>/db` dirs, so nested services (`solar/projects/db`) had invisible seeds — `discoverDBArtifactServiceDirs` in `cmd/scenery/generate.go` now scans one nested level. Acceptance evidence: `scenery db setup` applies 34 schemas + 11 seeds with zero failures; `scenery up --detach --wait ready` serves all frontends and `/healthy` returns 200; `scenery db list --json` emits `scenery.db.list.v3` with one managed database and 34 schemas; auth bootstrap creates 9 `scenery.scenery_auth_*` tables on first use and rejects bad logins; 8 `scenery.durable_*` tables exist; seed data survives a full `scenery down`/`up` restart; `go test ./...` green in both repos.

Update this section at every meaningful stopping point with date, what changed, and whether validation ran.

2026-07-06 milestone 6-9 implementation: auth sqlc was regenerated with `sqlc v1.30.0`; auth now opens only Postgres URLs through `postgresdb.Open` with `search_path=scenery`, and bootstrap applies idempotent scenery-schema auth DDL under an advisory lock. Seed lifecycle now uses `scenery.seed_runs` and executes service seeds through service-schema search paths. The DB CLI is Postgres-only (`list` v3, `shell`, `reset`, `drop`, `snapshot`); `db path`, `db branch`, branch providers, branch schemas, and branch harness wiring were removed. Symphony now opens the managed server's `scenery_symphony` database, and the dashboard DB explorer uses Postgres catalog queries. Validation attempted with a writable Go build cache; broad Go validation was blocked because the sandbox cannot download the missing `github.com/kr/pretty@v0.3.1` module and cannot write the default module stat cache.

## Surprises & Discoveries

* 2026-07-06: The only packages importing the sqlite driver are `internal/sqlitedb` and `internal/durable/store`; `internal/devdash` persists JSON, not SQLite, so the control plane needs no migration. The Symphony store (`internal/symphony/store.go`) and dashboard explorer (`cmd/scenery/dashboard_sqlite.go`) go through `internal/sqlitedb`.
* 2026-07-06: `auth/standard.go` hard-rejects non-`sqlite://` URLs ("standard auth database URL must be sqlite:///absolute/path", `auth/standard.go:189`), and root `sqlc.yaml` generates `auth/db/gen` with `engine: sqlite` — auth is the deepest sqlite coupling outside the durable store.
* 2026-07-06: The onlv app was Postgres-native with one schema per service until commit `469f330e` (2026-06-27, "Migrate app database to Scenery SQLite", 370 files); its pre-migration Atlas HCL, `$N` queries, and a 348 MB Postgres dump (`var/backup/database/onlv-main-20260622T081453Z.sql`) survive in git/worktree — the client migration is largely a guided revert.

* 2026-07-06: `scenery harness self` exposed a managed-server identity flaw: the `scenery-postgres` container name and volume were machine-global constants while port/password state is per-agent-home, so the parallel-worktree harness step (isolated agent home) created the container and the postgres probe (another isolated home) then dialed its own freshly allocated port and got connection refused. Fixed by making the server state file own the container/volume names, seeding harness steps with `scenery-postgres-harness-<label>` containers that are removed after the step, and adding a published-port mismatch diagnosis to `waitForPostgresServer` for the real-world stale-state case.

Add new surprises here with the command, test, or file that exposed them.

## Decision Log

* Decision: Postgres 18 is the only engine. `kind: "sqlite"` (and `kind` entirely) is removed from config; a config carrying any `dev.services.<name>.kind` fails validation with an error naming this plan. No compatibility shim, no dual-engine transition window in scenery itself.
  Rationale: User directive ("postgres 18 only, no more sqlite"); root engineering rule says remove obsolete spellings instead of carrying shims.
  Date/Author: 2026-07-06 / repo owner via Claude.

* Decision: One database per app root/worktree named `<sanitizePG(app_id)>_<shortIdentityHash(appRoot)>` (63-byte cap, hash always survives truncation). The plan-0093 per-service database naming (`<app>_<service>_<hash>`) is retired.
  Rationale: User directive ("one database per app/worktree"); one DSN per app matches production posture; schemas provide the per-service namespace.
  Date/Author: 2026-07-06 / repo owner via Claude.

* Decision: Each database service gets a schema named exactly after the service (sanitized to a valid Postgres identifier); scenery-native tables live in the `scenery` schema of the same database. The plan-0093 `scenery_internal` seed-ledger schema is renamed to `scenery`.
  Rationale: User directive ("native scenery tables will be in scenery schema. Each service has its own schema"). One spelling for scenery-owned state.
  Date/Author: 2026-07-06 / repo owner via Claude.

* Decision: "Durable workers in 1 table" is implemented as ONE shared durable store in the `scenery` schema — a single `scenery.durable_jobs` table (with lease columns inline, keyed by `service`) as the source of job existence, plus its minimal satellite tables (`durable_tasks`, `durable_job_events`, `durable_job_steps`, `durable_job_signals`, `durable_schedules`, `durable_worker_tokens`) which are per-row histories, not per-service duplicates. The plan-0089 per-service `<service>.durable.sqlite` model and the separate `leases` table are removed.
  Rationale: The intent is "no durable store per service", not literally collapsing events/steps into the jobs table. Lease state folds into `durable_jobs` columns (`lease_id`, `lease_owner`, `lease_until`) because `FOR UPDATE SKIP LOCKED` leasing operates on the job row itself.
  Date/Author: 2026-07-06 / Claude (interpreting user directive; flag in review if the user wants stricter collapse).

* Decision: Durable leasing uses `SELECT ... FOR UPDATE SKIP LOCKED` inside a transaction to claim queued jobs; long-poll HTTP endpoints for remote workers keep their plan-0089 paths, auth, and fencing semantics unchanged.
  Rationale: SKIP LOCKED is the standard Postgres queue primitive; the remote worker HTTP contract is public surface and does not need to change because storage changed.
  Date/Author: 2026-07-06 / Claude.

* Decision: External DSN is app-level: `database.url_env` (default `DATABASE_URL`). When set to a `postgres://`/`postgresql://` URL, Scenery manages no server and no database, and derives per-service URLs from it by pinning `search_path`. Per-service `database_url_env` overrides are removed.
  Rationale: One database per app means one DSN; per-service DSN overrides made sense only for per-service databases. Env-present-means-external stays the one rule (plan 0093).
  Date/Author: 2026-07-06 / repo owner via Claude.

* Decision: Injected env: `DATABASE_URL` (the app database), `<SERVICE>_DATABASE_URL` (same database, `search_path=<schema>`), and `SCENERY_DATABASE_JSON` (one object: database, url, source, schemas per service). `SCENERY_SQLITE_DATABASES_JSON` and `SCENERY_POSTGRES_DATABASES_JSON` are removed.
  Rationale: The registry shape changes from "list of databases" to "one database, many schemas", so a new singular name is honest; keeping the old plural names would be a shim.
  Date/Author: 2026-07-06 / Claude.

* Decision: `scenery.sh/db` public API is unchanged (`Get(ctx, service...)`, `MustGet`, `Close`, returns `*sql.DB`). `db.Get(ctx, "projects")` returns a pool whose connections set `search_path = projects, scenery`; `db.Get(ctx)` with no service works when the app has exactly one database service (unchanged rule) — pools are cached per resolved URL.
  Rationale: Apps keep writing unqualified table names. Including `scenery` second in search_path lets generated helpers and auth-adjacent queries resolve scenery tables without qualification while service tables always win.
  Date/Author: 2026-07-06 / Claude.

* Decision: Auth moves to Postgres tables in the `scenery` schema, generated by root `sqlc.yaml` with `engine: postgresql`. `auth.database_url_env` keeps working but now expects the app database URL; `auto_bootstrap_database` creates the `scenery` schema and auth tables idempotently.
  Rationale: Auth is scenery-native state; the `scenery` schema is exactly where the user asked for it.
  Date/Author: 2026-07-06 / repo owner via Claude.

* Decision: Auth keeps Postgres-native `uuid` columns for IDs and relies on the existing sqlc UUID override; a tiny alias file preserves the previous generated Go type names after schema-qualified sqlc generation.
  Rationale: Native UUID columns are the smallest faithful Postgres conversion, and aliases avoid churn in hand-written auth code while leaving generated code untouched.
  Date/Author: 2026-07-06 / Codex.

* Decision: `scenery db branch` and `scenery db path` are removed entirely (commands, providers, harness checks, schemas, docs). Worktree isolation via per-worktree databases is the branching story; snapshots (`pg_dump -Fc`/`pg_restore`) are the save/restore story.
  Rationale: SQLite file branching has no Postgres analog worth building now (plan 0093 already refused it); keep the public surface small and singular.
  Date/Author: 2026-07-06 / repo owner via Claude.

* Decision: The Symphony store moves from `symphony.sqlite` to the shared managed Postgres server in a machine-scoped `scenery_symphony` database (it is machine state, not app state). No data migration; symphony state is rebuildable cache.
  Rationale: "No more sqlite" includes internal stores; Symphony already runs only inside `scenery up`/dashboard sessions where the managed server is available.
  Date/Author: 2026-07-06 / Claude (alternative considered: JSON file like devdash — rejected because symphony has concurrent writers).

* Decision: Symphony uses the `public` schema inside the machine-scoped `scenery_symphony` database.
  Rationale: The database is dedicated to Symphony, so a second schema adds no useful isolation and would only make operational inspection noisier.
  Date/Author: 2026-07-06 / Codex.

* Decision: `go test ./...` still must not require Docker. Store/auth/CLI logic is unit-tested against fakes; live-Postgres integration tests gate on a reachable server (start via harness or an explicit `SCENERY_TEST_DATABASE_URL`-style hook decided at implementation time, registered in the env registry as a test-only input if added) and skip with a recorded reason otherwise. `scenery harness self` runs the full live probe suite (schema-per-service, worktree isolation, durable roundtrip, auth bootstrap, seed ledger) when Docker is reachable and records an explicit skip when not.
  Rationale: Preserves the plan-0088/0093 fast-and-portable default validation loop while the harness provides real proof.
  Date/Author: 2026-07-06 / Claude.

* Decision: `scenery down --db` drops this worktree's managed database; `scenery db server` subcommands, the pinned `postgres:18` toolchain image, server state file, substrate lease model, and doctor checks from plan 0093 carry over unchanged apart from naming.
  Rationale: The plan-0093 server substrate is exactly right; only the database granularity above it changes.
  Date/Author: 2026-07-06 / Claude.

When implementation chooses exact SQL, pool parameters, schema shapes, or error texts, append new entries here.

* Decision: Per-service Postgres URLs use the pgx URL runtime parameter `search_path=<service_schema>,scenery` in the query string, preserving any existing query parameters. The parameter is percent-encoded by Go's `net/url` as `search_path=reports%2Cscenery`, which pgx v5 decodes into startup runtime parameters.
  Rationale: pgx v5 accepts URL query parameters as runtime parameters, so this avoids connection hooks or per-query `SET search_path` code while keeping each pool pinned by DSN.
  Date/Author: 2026-07-06 / Codex.

* Decision: Milestones 1-4 left later-wave SQLite internals in place behind narrow compatibility seams. The public app config and `scenery.sh/db` path are Postgres-only, while standard auth, Symphony store, SQLite branch CLI, and dashboard SQLite explorer remain compiling for their later milestones; Milestone 5 superseded the durable store exception.
  Rationale: The scoped implementation request explicitly deferred Milestones 5-12 and asked for minimal mechanical adjustments to keep the build green without rewriting those features.
  Date/Author: 2026-07-06 / Codex.

* Decision: Durable store bootstrap runs inside a transaction guarded by `pg_advisory_xact_lock(hashtextextended('scenery.durable.store', 0))`, then applies `CREATE SCHEMA IF NOT EXISTS scenery`, `CREATE TABLE IF NOT EXISTS ...`, and inserts the `durable_schema_migrations` row with `ON CONFLICT DO NOTHING`.
  Rationale: The advisory lock keeps concurrent generated role startup from racing DDL while preserving idempotent re-open behavior.
  Date/Author: 2026-07-06 / Codex.

* Decision: Durable Postgres DDL stores structured task/job metadata (`requirements_json`, `labels_json`, `memo_json`, token scopes) as `jsonb`, while handler inputs/results/errors/events remain `bytea` plus codec columns.
  Rationale: Queryable store-owned metadata benefits from Postgres types; app payloads stay byte-preserving to keep the existing durable envelope contract.
  Date/Author: 2026-07-06 / Codex.

* Decision: Live durable storage tests gate on `SCENERY_TEST_DATABASE_URL` and create a random per-test database, then terminate connections and drop that database in cleanup.
  Rationale: `go test ./...` remains Docker-free by default, and live runs do not share mutable durable state between tests.
  Date/Author: 2026-07-06 / Codex.

* Decision: Generator database engine validation is now Postgres-only. Root `sqlc.yaml` may omit an engine because the root contract is already Postgres, but generated or app-local config that asks for another engine fails with an error naming this plan.
  Rationale: After SQLite removal there is no supported alternate engine, and the failure needs to point maintainers at the contract that made the surface singular.
  Date/Author: 2026-07-06 / Codex.

* Decision: The long-lived database fixture is named `testdata/apps/db-basic` rather than `postgres-basic`.
  Rationale: Postgres is now the only database engine, so the fixture name should describe the capability rather than distinguish it from a removed SQLite sibling.
  Date/Author: 2026-07-06 / Codex.

* Decision: The self-harness Postgres probe proves behavior through the current public surfaces instead of adding Docker-gated unit tests: managed env/database ensure, schema existence, second-root isolation, durable store round-trip, auth bootstrap DDL, and `db reset <service>` schema isolation.
  Rationale: `go test ./...` must stay Docker-free while the harness remains the correct place for live Docker acceptance.
  Date/Author: 2026-07-06 / Codex.

* Decision: Durable lease expiration uses each task row's `default_lease_ms` for both initial lease and heartbeat extension, with 60000 ms only as the fallback when the task row is absent or the value is non-positive.
  Rationale: Lease duration is task configuration, not a store constant; the fallback preserves safe behavior for partially reconciled rows.
  Date/Author: 2026-07-06 / Codex.

* Decision: Durable worker finish/fail/retry mutations are fenced by row locks, running-state predicates, lease owner/id predicates when present, and `RowsAffected == 1` checks.
  Rationale: A stale worker must not overwrite a concurrent cancel or admin transition after its lease verification read.
  Date/Author: 2026-07-06 / Codex.

## Outcomes & Retrospective

Completed 2026-07-06.

Outcome:
- Postgres 18 is scenery's only database engine. SQLite (engine, config kind, branching, snapshots-by-file, per-service durable files, dashboard explorer, symphony store) is fully removed; `modernc.org/sqlite` is gone from `go.mod`.
- One managed database per app root/worktree (`<app_id>_<hash>` on the shared plan-0093 Docker server), one schema per service, scenery-native tables (auth, durable store, seed ledger) in the `scenery` schema, external `DATABASE_URL` always wins. Injected env: `DATABASE_URL`, `<SERVICE>_DATABASE_URL` (search_path-pinned), `SCENERY_DATABASE_JSON`.
- Durable execution runs on one shared store (`scenery.durable_jobs` + satellites) with `FOR UPDATE SKIP LOCKED` leasing, task-driven lease durations, and lease/state-fenced transitions; the plan-0089 public API and remote worker protocol are unchanged.
- The DB CLI is Postgres-only (`scenery.db.list.v3`, psql shell, schema-scoped reset, pg_dump snapshots); `db path` and `db branch` are removed.
- The onlv app runs on the new platform end to end (Milestone 13 entry above records the acceptance evidence).

Validation: recorded per milestone in Progress; final state was full `go test ./...` (Docker-free) green, live `SCENERY_TEST_DATABASE_URL` suites green, `scenery harness self --summary --write` `pass_with_warnings`/`can_proceed: true` with the live Postgres probe, and the onlv acceptance run.

Retrospective notes:
- Codex implementation waves were effective but sandbox limits (no loopback bind/connect, no Docker) meant live-SQL bugs (ambiguous columns, placeholder reuse, `?` placeholders in tests) consistently surfaced only in host-side verification — budget a host verification pass after every wave.
- The machine-global container name vs per-agent-home state mismatch was the one real architecture flaw found post-implementation; state-owned container/volume names fixed it.
- An independent review pass (gpt-5.5) caught a genuine blocker (generated model CRUD still sqlite-shaped) that all wave-level testing missed because no fixture exercised generated CRUD against live Postgres.

Follow-up candidates: post-plan fixes resolved the live-runtime dependency for `scenery down --db` and made dev-bootstrap create the configured default auth user/tenant on first local bootstrap when missing. A template-database branching story remains explicitly out of scope.

## Context and Orientation

Terms:

* **App database**: the one Postgres database for an (app root, worktree) pair on the managed shared server, or the database behind the external `DATABASE_URL`.
* **Service schema**: the Postgres schema named after a database service (a `dev.services` entry). Service `projects` → schema `projects`.
* **`scenery` schema**: the schema inside the app database owning scenery-native tables: auth tables, the durable store (`durable_*`), the seed ledger (`seed_runs`), and metadata.
* **Managed shared server**: the single `scenery-postgres` Docker container per machine from plan 0093 (pinned `postgres:18` toolchain image, agent-home credentials, substrate leases). Unchanged by this plan.
* **External DSN**: a `postgres://` URL in the app-level `database.url_env` (default `DATABASE_URL`); makes the whole app external — Scenery manages nothing.

Read before editing:

```text
AGENTS.md
docs/plans/0093-postgres-service-databases.md   (the substrate this plan builds on)
docs/plans/0089-sqlite-durable-execution-runtime.md  (durable semantics to preserve)
docs/local-contract.md
internal/app/root.go                (DevServiceConfig, SQLiteServices, PostgresServices, validateDevServices)
internal/postgresdb/                (Open, DatabaseNameFor, EnsureDatabase, Service, Env)
internal/sqlitedb/sqlitedb.go       (everything being deleted; auth/cli callers)
db/db.go                            (engine dispatch to replace)
internal/durable/store/             (store.go ~1163 lines, schema.go, path.go — the rewrite target)
auth/standard.go                    (sqlite-only open path ~line 187), sqlc.yaml, auth/db/
cmd/scenery/dev_services.go, dev_services_postgres.go, dev_supervisor.go
cmd/scenery/db_cli.go, db_seed.go, db_branch_commands.go, db_sqlite_branch_provider.go, harness_sqlite_branch.go
cmd/scenery/dashboard_sqlite.go, internal/symphony/store.go
cmd/scenery/harness_self_postgres.go, harness_arch.go
docs/schemas/scenery.config.v1.schema.json, scenery.db.list.v2.schema.json, scenery.db.server.status.v1.schema.json
docs/environment.md, docs/environment.registry.json
```

Key semantics to preserve:

* `scenery.sh/db` public API signatures; `scenery.sh/durable` public API (`NewTask`, `Start`, `Schedule`, `Step`, `Signal`) and job/state vocabulary; remote durable worker HTTP endpoints, token model, and lease fencing; `scenery worker durable ...` CLI; dev event shapes (engine label changes only); `scenery db server` behavior; plan-0093 fail-closed posture for `scenery serve`/`worker` without a DSN.
* Managed-vs-external precedence: env present means external, always.
* Destructive CLI operations refuse to touch external databases.

## Milestones

Each milestone leaves `go test ./...` green (Docker-free).

1. **Milestone 1 — Config surface.** `dev.services.<name>` entries declare database services with NO `kind`: accepted fields are `env` only for now (service schema name = sanitized service name; a `schema` override field is intentionally not added). Any `kind`, `database`, `database_url_env`, `mode`, or other legacy field fails validation naming this plan. Add app-level `database.url_env` (default `DATABASE_URL`) next to the existing `database.apply` config. Replace `SQLiteServices()`/`PostgresServices()` with `DatabaseServices() []DatabaseServiceConfig` (`Name`, `Schema`, `Raw`). Update `docs/schemas/scenery.config.v1.schema.json` and `internal/app/root_test.go`.

2. **Milestone 2 — `internal/postgresdb` extensions.** `DatabaseNameFor(appID, appRoot)` drops the service segment. Add `SchemaNameFor(service)` (sanitize, reserve `scenery`, `public`, `pg_*`, `information_schema` as invalid service names at config validation). Add `EnsureSchema(ctx, db, schema)`, `ResetSchema` (drop cascade + recreate), `ServiceURL(baseURL, schema)` (sets `search_path=<schema>,scenery` as a URL runtime parameter). Replace the `Service` registry with a `Database` envelope (`Database`, `URL`, `Source`, `Schemas []{Service, Schema, URL}`) and `RegistryEnv = "SCENERY_DATABASE_JSON"`. Unit tests: naming stability/truncation, reserved schema rejection, URL derivation, registry round-trip — no live server.

3. **Milestone 3 — Dev runtime.** Rewrite `managedPostgresEnv` → `managedDatabaseEnv`: external `DATABASE_URL` present → source external, derive per-service URLs, manage nothing; otherwise ensure server (unchanged), ensure the one app database, ensure `scenery` + per-service schemas, inject `DATABASE_URL`, `<SERVICE>_DATABASE_URL`, `SCENERY_DATABASE_JSON`. Delete `managedSQLiteEnv` and all sqlite env injection. `scenery down --db` drops the managed app database. Dev events report `engine=postgres`, database, schemas. Headless `scenery serve`/`worker`: fail closed without `DATABASE_URL` (message updated to app-level wording).

4. **Milestone 4 — `scenery.sh/db`.** `db/db.go` resolves services from config + `SCENERY_DATABASE_JSON` (+ `DATABASE_URL`/`<SERVICE>_DATABASE_URL` fallbacks), accepts only `postgres://`/`postgresql://`, dispatches to `postgresdb.Open`, caches pools per resolved URL. Remove the sqlite path. Errors name the service, the schema, and `DATABASE_URL`.

5. **Milestone 5 — Durable store on Postgres.** Rewrite `internal/durable/store` against the app database's `scenery` schema: single `durable_jobs` table with `service` column and inline lease columns; satellite tables `durable_tasks`, `durable_job_events`, `durable_job_steps`, `durable_job_signals`, `durable_schedules`, `durable_worker_tokens`; idempotent bootstrap (`CREATE SCHEMA IF NOT EXISTS scenery` + `CREATE TABLE IF NOT EXISTS` + a `durable_schema_migrations` version row). Leasing via `FOR UPDATE SKIP LOCKED`; heartbeats/complete/fail fenced by `worker_id`+`lease_id` as today. The store opens via the same resolved app-database URL (worker role: `DATABASE_URL`); `SCENERY_DURABLE_STATE_ROOT` and `<service>.durable.sqlite` paths are removed. Runtime wiring (`runtime/`, generated worker main) passes one store handle shared by all services. `scenery inspect durable --json` reports the database/schema instead of file paths (schema version bump). `scenery worker durable jobs ...` admin CLI reads the same store.

6. **Milestone 6 — Auth on Postgres.** Root `sqlc.yaml` → `engine: postgresql`; rewrite `auth/db/gen/schema.sql` DDL to Postgres in the `scenery` schema; regenerate `auth/db/gen`; `auth/standard.go` opens via `postgresdb.Open` with `search_path=scenery`, rejects non-postgres URLs, and `auto_bootstrap_database` applies the auth DDL idempotently. Standard-auth dev bootstrap (default user/tenant) unchanged in behavior.

7. **Milestone 7 — Seed lifecycle.** `scenery db apply/seed/setup` run against the app database with per-service env; the seed ledger becomes `scenery.seed_runs`; destructive-SQL validation unchanged; service-local `SERVICE/db/seed.sql` routes to the service schema (execute with `search_path=<schema>,scenery`).

8. **Milestone 8 — DB CLI.** `db list` → `scenery.db.list.v3` (one database entry: name, redacted URL, source, size, schemas with per-service row counts optional); `db shell [service]` → `psql` (service arg sets `search_path` via options); `db reset [service]` → schema drop/recreate for one service, full database recreate with `--yes` for all; `db drop` → drop the managed app database; `db snapshot create|restore` → `pg_dump -Fc`/`pg_restore` of the app database into the existing snapshot layout. Delete `db path`, `db branch`, the sqlite branch provider, and `harness_sqlite_branch.go`. External-DSN destructive refusal carries over.

9. **Milestone 9 — Internal stores.** Symphony store → `scenery_symphony` database on the managed server (bootstrap on first dashboard use; degrade with a clear error when Docker is unavailable). Dashboard DB explorer (`dashboard_sqlite.go` → renamed) lists schemas/tables/rows of the app database via `information_schema`.

10. **Milestone 10 — SQLite removal.** Delete `internal/sqlitedb`; drop `modernc.org/sqlite` from `go.mod`; remove sqlite spellings from config schema, env registry, dev events, docs, fixtures. Grep gates:

        git grep -ni "sqlite" -- cmd internal db auth durable runtime scenery.go go.mod   # only historical plan/doc references remain outside docs/plans
        git grep -n "SCENERY_SQLITE_DATABASES_JSON\|SCENERY_POSTGRES_DATABASES_JSON"      # nothing

11. **Milestone 11 — Generators, fixtures, harness.** `scenery generate sqlc`/`generate data`/`db diff --generated` are Postgres-only (engine validation errors mention this plan); `//scenery:model` HCL stays schema-qualified and now maps to the service schema. Fixtures: replace `testdata/apps/postgres-basic` mixed fixture with `testdata/apps/db-basic` (two services → two schemas); update `standard-auth`, `cron`, durable fixtures. Harness: extend `harness_self_postgres.go` probe — one database, `scenery` + service schemas exist, worktree hash isolation, durable job roundtrip through `scenery.sh/durable`, auth bootstrap, `db reset <service>` empties only that schema; explicit recorded skip without Docker.

12. **Milestone 12 — Docs sweep.** `docs/local-contract.md` (engine, naming, schemas, env, CLI grammar, `scenery.db.list.v3`, removed commands, stability: postgres-only data platform **beta**), `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/app-development-cookbook.md`, `docs/environment.md` + `docs/environment.registry.json` (add `DATABASE_URL` input + injected set, `SCENERY_DATABASE_JSON`; remove sqlite entries), `docs/schemas/` (config v1, list v3, inspect durable), `docs/knowledge.json`, `docs/plans/active.md`. Close plans 0089 and 0093 references with "current contract lives in 0097" notes where they describe superseded behavior.

13. **Milestone 13 — onlv migration (client repo `/Users/petrbrazdil/Repos/onlv`).** Update `.scenery.json` (kind-less `dev.services`, app-level DSN env), convert 32 `*/db/schema.sql` + 7 seed files to Postgres dialect (git history at `469f330e~1` has the Postgres originals), `sqlc.yaml` engine → postgresql, regenerate `*/db/gen`, rewrite `scripts/db-apply.sh` for Postgres (or switch to `scenery db setup`), update onlv `AGENTS.md`. Acceptance: `scenery up` on onlv reaches ready; auth bootstrap works; `scenery db list/seed/reset` behave; app endpoints round-trip.

## Plan of Work

Work bottom-up so each phase compiles and tests without Docker. Milestones 1–4 are one coherent phase (config → resolver → runtime → db package). Milestone 5 (durable) is the largest single rewrite and depends only on Milestone 2's helpers. Milestones 6–9 are parallelizable surfaces on top (auth, seeds, CLI, internal stores). Milestones 10–12 are removal + proof + docs and must land after everything stops referencing sqlite. Milestone 13 happens in the onlv repo against an installed scenery build.

Keep every Docker interaction behind the existing fake-runner seams from plan 0093 so all orchestration logic is unit-testable.

## Concrete Steps

All commands run from the repository root.

1. Milestone 1: edit `internal/app/root.go` + tests + config schema. 2. Milestone 2: extend `internal/postgresdb` + tests. 3. Milestone 3: rewrite `cmd/scenery/dev_services_postgres.go`, delete sqlite injection from `dev_services.go`, wire `dev_supervisor.go`, update `down`/serve/worker fail-closed paths. 4. Milestone 4: rewrite `db/db.go` + tests. 5. Milestone 5: rewrite `internal/durable/store` (new `schema.go` DDL, `store.go` SQL to `$N` placeholders, SKIP LOCKED lease query), update `runtime/` wiring, `inspect durable`, worker CLI + tests. 6. Milestone 6: `sqlc.yaml`, `auth/db/*`, regenerate, `auth/standard.go` open path + bootstrap + tests. 7. Milestone 7: `db_seed.go` ledger + routing. 8. Milestone 8: `db_cli.go` rewrite, delete branch/path files, new list schema. 9. Milestone 9: `internal/symphony/store.go`, dashboard explorer rename/rewrite. 10. Milestone 10: deletions + `go mod tidy` + grep gates. 11. Milestone 11: generators + fixtures + `harness_self_postgres.go`. 12. Milestone 12: docs sweep. 13. Milestone 13: onlv repo work per its own AGENTS.md.

## Validation and Acceptance

Per milestone: `go test ./...` and `go test ./cmd/scenery` (both Docker-free). After Milestone 5: `go test ./internal/durable/... ./db ./internal/postgresdb ./internal/app`. After Milestone 12: `scenery harness self --summary --write` via the worktree-local harness build. Behavioral acceptance with Docker: harness probe green (database/schemas/durable/auth/reset/worktree isolation) plus the Milestone 13 onlv checks. `go test ./...` passes with Docker stopped. Installation for client testing requires explicit human authorization in that milestone; do not run `go install ./cmd/scenery` during the scoped Milestones 10-12 pass.

## Idempotence and Recovery

All code steps are Git-tracked edits. Runtime idempotence: database/schema ensure paths use existence checks + `IF NOT EXISTS` inside retry loops; durable bootstrap is versioned DDL safe to re-run; `scenery down --db` then `scenery up` recreates an empty database and `scenery db setup` rebuilds it; a deleted container/volume recreates on next `up` (plan-0093 recovery unchanged). If a codex/agent phase lands partial work, `go test ./...` gates and this plan's Progress record where to resume.

## Artifacts and Notes

* Naming example: app `onlvnext-o5o2`, worktree `/Users/petrbrazdil/Repos/onlv` → database `onlvnext_o5o2_<hash>`; service `contacts` → schema `contacts`; auth/durable/seed ledger → schema `scenery`.
* SQLite importer inventory (2026-07-06): driver imports only in `internal/sqlitedb` and `internal/durable/store`; `internal/sqlitedb` imported by `db/db.go`, `auth/standard.go`, `cmd/scenery/{dev_services,db_cli,db_seed,harness_sqlite_branch,db_sqlite_branch_provider,dashboard_sqlite}.go`, `internal/symphony/store.go`.
* The durable rewrite preserves plan-0089 job-state vocabulary (`queued|running|succeeded|failed|canceled`) and the remote worker protocol; only storage moves.
* Cron/schedules ride on the durable store (`durable_schedules`); no separate cron storage exists.

## Interfaces and Dependencies

* `scenery.sh/db` and `scenery.sh/durable` public APIs unchanged. Accepted URL schemes: `postgres://`, `postgresql://` only.
* Config: `dev.services.<name>: {}` (database service, no kind), `database.url_env` (default `DATABASE_URL`), auth config unchanged in shape.
* Injected env: `DATABASE_URL`, `<SERVICE>_DATABASE_URL`, `SCENERY_DATABASE_JSON`. Removed: `SCENERY_SQLITE_DATABASES_JSON`, `SCENERY_POSTGRES_DATABASES_JSON`, `<SERVICE>_DATABASE_PATH`.
* JSON surfaces: `scenery.db.list.v3` (replaces v2), `scenery.inspect.durable.v2`, `scenery.db.server.status.v1` unchanged.
* Dependencies: `github.com/jackc/pgx/v5` stays (now also used by `internal/durable/store` and `auth`); `modernc.org/sqlite` removed.
* Toolchain: pinned `postgres:18` image artifact unchanged.
* External host tools: `docker` (managed server), `psql`, `pg_dump`/`pg_restore` (CLI shell/snapshots), `atlas`, `sqlc` (generators) — optional, doctor-warned, never required by `go test`.
