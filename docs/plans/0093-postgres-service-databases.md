# 0093 - First-Class Postgres Service Databases on a Shared Dev Server

> Current contract note (2026-07-06): 0097 supersedes this plan's per-service database and mixed-engine model. Postgres is now the only engine, with one database per app/worktree and one schema per service; the current contract lives in `0097-postgres-only-data-platform.md` and `docs/local-contract.md`.

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Scenery's managed database model today is SQLite-only: `dev.services.<name>.kind: "sqlite"` maps one service to one SQLite file (plan 0088 removed the previous built-in Postgres substrate entirely). SQLite stays the default and is already first-class. This plan adds Postgres back as a *first-class service kind* — but in a deliberately different shape than the pre-0088 model:

```text
one machine        = at most one Scenery-managed shared Postgres server (Docker)
one app + worktree = one Postgres database per postgres service on that server
external DSN       = always wins; Scenery then manages no server at all
```

The two supported connection modes, in fixed precedence order:

1. **External DSN.** If the service's configured `database_url_env` (default `<SERVICE>_DATABASE_URL`) is already set in the environment to a `postgres://` URL, Scenery uses it verbatim and manages nothing. This is the production posture and the "bring your own server" dev posture.
2. **Managed shared Docker server.** Otherwise, under `scenery up`, Scenery ensures a single machine-wide Postgres server container (pinned image, agent-home-owned credentials and state), creates one database per app/worktree/service, and injects the resulting URL.

Worktree isolation comes from database naming, not from separate servers: the managed database name is derived from the app ID, the service name, and a short hash of the absolute app root, so two Git worktrees of the same app get disjoint databases on the same shared server. This mirrors the shared-substrate philosophy of plan 0079 (Victoria): one efficient machine-wide substrate, per-consumer isolation, visible ownership.

Schema and query tooling stays Atlas + sqlc (both already integrated as optional host tools; both support the Postgres dialect natively). This plan explicitly does **not** adopt entgo or any ORM: Scenery's app-facing DB API remains `*sql.DB`, and `//scenery:model` remains the model IR.

When this plan is done: an app can declare `dev.services.reports.kind: "postgres"`, `scenery up` injects `REPORTS_DATABASE_URL` pointing at an isolated database on the shared server (or passes through an external DSN), `scenery.sh/db` returns a working `*sql.DB` for it, the DB CLI (`list`, `shell`, `reset`, `drop`, `snapshot`, `seed`) understands postgres services, `scenery serve`/`worker` fail closed without an explicit DSN, and docs/schemas/harness cover the new contract.

## Progress

* [x] 2026-07-02 - Surveyed current SQLite-only model, plan 0088 history, toolchain image support, doctor Docker checks, substrate machinery; drafted this plan.
* [x] 2026-07-02 - Registered this plan in `docs/plans/active.md` and `docs/knowledge.json`; added a forward-pointer note to plan 0088.
* [x] Milestone 0: Inventory and 0088 closure note.
* [x] Milestone 1: Config surface — accept `kind: "postgres"`, `PostgresServices()` helpers, JSON schema update.
* [x] Milestone 2: Driver and resolver — `internal/postgresdb` package, `jackc/pgx/v5` stdlib driver, database-name derivation, admin operations.
* [x] Milestone 3: Managed shared server — Docker container lifecycle, agent-home credentials/state, toolchain image pin, substrate registration, doctor check, `scenery db server` CLI.
* [x] Milestone 4: Dev runtime env injection — per-worktree database ensure + env injection under `scenery up`, external-DSN precedence, `SCENERY_POSTGRES_DATABASES_JSON`, dev events.
* [x] Milestone 5: `scenery.sh/db` engine dispatch — `db.Get` serves both engines by URL scheme.
* [x] Milestone 6: Headless contract — `scenery serve`/`worker` require explicit DSNs for postgres services; fail-closed errors.
* [x] Milestone 7: DB CLI — `list`/`shell`/`reset`/`drop`/`snapshot`/`seed` postgres awareness; `path` and `branch` fail with clear guidance.
* [x] Milestone 8: Atlas/sqlc plumbing — postgres dialect through `scenery generate sqlc`, `generate data`, `scenery db diff --generated`.
* [x] Milestone 9: Fixture, tests, and harness probe (Docker-gated, skips cleanly).
* [x] Milestone 10: Docs, schemas, env registry, knowledge sync, final validation.
* [x] 2026-07-02 - Implemented Postgres service support end-to-end in this branch: config/runtime/env/db helper/CLI/server/doctor/harness/docs. Focused validation passed with `go test ./cmd/scenery ./internal/app ./internal/postgresdb ./db ./internal/toolchain`; final full-suite validation is recorded below.

Update this section at every meaningful stopping point with date, what changed, and whether validation ran.

## Surprises & Discoveries

* 2026-07-02: Plan 0088's implementation landed (commit `2c23508d` "Replace Postgres service databases with SQLite") but its `Progress` and `Outcomes & Retrospective` were never updated; it is still listed active in `docs/plans/active.md`. Milestone 0 of this plan records that closure.
* 2026-07-02: `internal/app/root.go` `validateDevServices` spells the rejected kind as `"post" + "gres"` so plan 0088's final grep gate passes. This plan replaces that rejection with real support, which also retires the string-splitting trick.
* 2026-07-02: The toolchain manifest already supports `kind: "image"` artifacts (`internal/toolchain/manifest.go:154`) and the store already shells out to `docker image inspect` (`internal/toolchain/store.go:34`), so pinning a Postgres image needs no new toolchain machinery.
* 2026-07-02: Doctor already has `docker.context` and `docker.engine` checks (`cmd/scenery/doctor.go:848`), and already special-cases `docker://` Atlas dev URLs. The managed-server path can build on these.
* 2026-07-02: A bare `dev.services.postgres: {}` now becomes a Postgres service named `postgres`; this removes the old string-split rejection while keeping explicit service maps compact.
* 2026-07-02: Service-local seed routing was still defaulting all seeds into one database. The implementation now resolves each `SERVICE/db/seed.sql` and generated `.scenery/gen/db/<service>/seed.sql` against the matching service database, with a single-service or conventional `db` fallback for old simple apps.
* 2026-07-02: Generated model HCL was already Postgres-shaped and schema-qualified. The generator work needed here was SQLC engine validation/metadata: schemas for configured Postgres services must use a Postgres SQLC engine, and `inspect generators` now carries optional artifact `engine`.

Add new surprises here with the command, test, or file that exposed them.

## Decision Log

* Decision: SQLite remains the default; Postgres is opt-in per service via `dev.services.<name>.kind: "postgres"`.
  Rationale: SQLite's zero-setup, file-backed model is the right default for agents and local dev; Postgres is for apps that need its semantics (concurrency, extensions, production parity).
  Date/Author: 2026-07-02 / repo owner.

* Decision: Keep Atlas + sqlc as the schema/query toolchain; do not adopt entgo.
  Rationale: Atlas HCL is already the `//scenery:model` schema IR and sqlc is already a beta generator lifecycle; both are permissively-licensed external CLI tools, not runtime deps. entgo would be a large runtime dependency, a competing app model against `//scenery:model` and the `*sql.DB`-shaped `scenery.sh/db` API, and it uses Atlas for migrations anyway.
  Date/Author: 2026-07-02 / repo owner.

* Decision: One shared machine-wide managed Postgres server; isolation by database name (`<app_id>_<service>_<worktree-hash>`), not by per-app servers.
  Rationale: User requirement ("pg could be shared, each app/worktree would have different database"); matches the plan-0079 shared-substrate philosophy; one container is cheap to supervise and inspect, N containers are not.
  Date/Author: 2026-07-02 / repo owner.

* Decision: External DSN via the service's `database_url_env` always takes precedence over the managed server; no `mode` config field.
  Rationale: Env-present-means-external is one rule with no new config surface. Production (`scenery serve`/`worker`) is *only* external DSN — the managed server is a dev substrate.
  Date/Author: 2026-07-02 / this plan.

* Decision: Manage the server through the `docker` CLI (no Docker SDK dependency); pin the image as a toolchain `image` artifact.
  Rationale: `internal/toolchain` already shells out to Docker; adding a Docker SDK violates the stdlib-first rule; pinning via `scenery.toolchain.json` follows plan 0059.
  Date/Author: 2026-07-02 / this plan.

* Decision: Server data lives in a named Docker volume (`scenery-postgres-data`); credentials and port live under the agent home.
  Rationale: A named volume is the boring Docker-native choice and survives container recreation; bind mounts into the agent home have ownership/permission friction (Linux container UID vs host UID) for no inspectability gain — the inspection surface is SQL, not files.
  Date/Author: 2026-07-02 / this plan.

* Decision: Out of scope for this plan: standard auth on Postgres, durable/cron store on Postgres, `scenery db branch` for postgres services, dashboard DB explorer for Postgres, any sync/replication subsystem.
  Rationale: Auth and the durable store are SQLite-native since 0088/0089 and work fine alongside postgres app services. Per-worktree databases already deliver the isolation `db branch` provides for SQLite; a template-database branch provider is a follow-up plan if demand appears. Keeping the first cut small keeps it shippable.
  Date/Author: 2026-07-02 / repo owner + this plan.

* Decision: `go test ./...` must not require Docker. Postgres integration proof lives in unit tests against fakes plus a self-harness probe that skips (recorded, not silent) when Docker is unreachable.
  Rationale: Preserves the plan-0088 test invariant and keeps the default validation loop fast and portable.
  Date/Author: 2026-07-02 / this plan.

* Decision: No new environment-variable knobs. New env names are runtime-*injected* outputs only (`<SERVICE>_DATABASE_URL` for postgres services, `SCENERY_POSTGRES_DATABASES_JSON`), registered in `docs/environment.registry.json` with `direction: "injected"`.
  Rationale: Root `AGENTS.md` env policy. The external-DSN input path reuses the app-configured `database_url_env` contract that already exists for SQLite.
  Date/Author: 2026-07-02 / this plan.

* Decision: `scenery down` never stops the shared Postgres server; stopping is explicit via `scenery db server stop`.
  Rationale: The server is machine-shared across apps and worktrees; one app's teardown must not break another's session. Leases make usage visible instead.
  Date/Author: 2026-07-02 / this plan.

* Decision: Destructive CLI operations (`reset`, `drop`) refuse to operate on external-DSN postgres services; they only manage databases Scenery created on the managed server.
  Rationale: Scenery must not drop databases on servers it does not own. Fail closed with a message naming the env var that made the service external.
  Date/Author: 2026-07-02 / this plan.

* Decision: `dev.services.postgres: {}` is accepted as a Postgres service named `postgres`.
  Rationale: It keeps the common single-service shorthand compact, and validation still rejects old provider/isolation fields for Postgres services.
  Date/Author: 2026-07-02 / implementation.

* Decision: `scenery generate sqlc` validates service-owned SQLC engines but does not invent a new Scenery-only `atlas_dev_url` indirection.
  Rationale: SQLC's own `engine: postgresql` is the app-owned source of truth. Atlas already accepts `postgres://`, `postgresql://`, and `docker://` dev URLs, so Scenery should pass those through rather than add hidden magic.
  Date/Author: 2026-07-02 / implementation.

When implementation chooses exact SQL, container flags, schema shapes, or error texts, append new entries here.

## Outcomes & Retrospective

Outcome:
- Apps can declare Postgres service databases with `dev.services.<name>.kind: "postgres"`.
- Existing explicit service DSNs win and are marked `source: "external"`; otherwise `scenery up` ensures a shared local Docker Postgres server and creates one database per app root/worktree/service.
- SQLite remains the default and keeps file paths, snapshots, branches, worktree branch creation, and single-service alias behavior. The `DatabaseURL` alias rule now counts all database service engines.
- `scenery.sh/db` opens SQLite and Postgres URLs behind the same `*sql.DB` API.
- `scenery db list`, `shell`, `reset`, `drop`, `snapshot`, `seed`, `path`, `branch`, and `server` now have explicit Postgres behavior. External Postgres destructive operations fail closed.
- `scenery serve` and `scenery worker` require explicit Postgres DSNs and never start the managed dev server.
- Docs, schemas, env registry, toolchain manifest, fixture config, and self-harness probe were updated. Plan 0088 was closed as the old-substrate-removal baseline that this plan does not undo.

Validation:
- `go test ./cmd/scenery ./internal/app ./internal/postgresdb ./db ./internal/toolchain` passed during implementation.
- `go test ./...` passed.
- `go test ./cmd/scenery` passed.
- `go run ./cmd/scenery harness self --summary --write` passed with warnings and `can_proceed: true`. Warning classes were existing large-file/timing warnings plus the Postgres service probe skipping live Docker proof because the local Docker/OrbStack engine socket was unavailable.

Follow-up:
- Dashboard DB explorer remains SQLite-only.
- Standard auth, durable execution, cron storage, and SQLite branch templates stay SQLite-native.
- A future plan can add richer Postgres database diffing or branch/template database strategies if demand appears.

## Context and Orientation

Terms:

* **Postgres service**: a `dev.services` map entry with `kind: "postgres"`. The map key is the service name, exactly like SQLite services.
* **Managed shared server**: the one Scenery-owned Postgres container per machine, named `scenery-postgres`, registered as an agent substrate (like Victoria) with per-app-root leases.
* **Service database**: one Postgres database on that server, owned by one (app root, service) pair. Name: `sanitizePG(<app_id>)_<service>_<shortIdentityHash(appRoot)>`, truncated to 63 bytes (Postgres identifier limit).
* **External DSN**: a `postgres://` or `postgresql://` URL provided by the user through the service's `database_url_env`; makes the service external — Scenery manages neither server nor database.
* **Agent home**: the machine-wide Scenery state directory used by `internal/localagent` (where Victoria substrate state already lives).

Read before editing (the plan-0088 file is the map of everything that was deleted and must *not* be blindly revived):

```text
AGENTS.md
docs/plans/0088-sqlite-service-databases.md
docs/plans/0079-victoria-shared-substrate-visibility.md
docs/local-contract.md
internal/app/root.go            (DevServiceConfig, validateDevServices ~line 493, SQLiteServices ~line 143)
internal/sqlitedb/sqlitedb.go   (the shape internal/postgresdb should rhyme with)
db/db.go                        (current SQLite-only resolution)
cmd/scenery/dev_services.go     (managedSQLiteEnv, shortIdentityHash, verifySubstrateOwner)
cmd/scenery/dev_supervisor.go   (supervisor phases; storage-cell precedent from plan 0094)
cmd/scenery/db_cli.go           (db command routing, scenery.db.sqlite.list.v1)
cmd/scenery/db_seed.go          (seed_runs ledger)
cmd/scenery/doctor.go           (docker.context / docker.engine, optional-tool checks)
internal/toolchain/manifest.go, store.go   (image artifacts, docker CLI probing)
internal/agent/types.go         (Substrate, SubstrateLease, SubstrateVictoria)
internal/envpolicy/             (env access policy; register new injected names)
docs/schemas/scenery.config.v1.schema.json
docs/environment.registry.json
```

Key existing behavior to preserve unchanged:

* SQLite services: config shape, path layout, env injection, `SCENERY_SQLITE_DATABASES_JSON`, snapshots, branches, and the `DatabaseURL` single-service alias all keep working exactly as today. An app may declare both sqlite and postgres services.
* `scenery.sh/db` public API: `Get(ctx, service...)`, `MustGet`, `Close` signatures unchanged; still returns `*sql.DB`.
* The plan-0088 invariant that `go test ./...` needs no Docker.

## Milestones

Each milestone leaves `go test ./...` green.

1. **Milestone 0 — Inventory and 0088 closure.** Run a fresh grep inventory for leftover postgres rejection points (`git grep -n -i "postgres\|post\" + \"gres\|psql\|pg_dump" -- cmd internal db docs/schemas`). Update plan 0088: check off its completed Progress items against commit `2c23508d`, fill `Outcomes & Retrospective` honestly (implementation landed, doc closure happened here), move it out of `docs/plans/active.md` into `docs/plans/completed.md`, and keep the note (added with this plan's registration) that Postgres *support* returns in 0093 with a different architecture while 0088's *removal of the old built-in substrate* stands.

2. **Milestone 1 — Config surface.** `dev.services.<name>.kind` accepts `"postgres"`. Add `Config.PostgresServices() []PostgresServiceConfig` and `Config.PostgresService(name)` in `internal/app/root.go`, mirroring the SQLite helpers: `Name`, `DatabaseLabel` (from `database`, default service name), `DatabaseURLEnv` (from `database_url_env`, default `<UPPER_SNAKE>_DATABASE_URL`), `Raw`. Validation: postgres services accept only `kind`, `database_url_env`, `database`, `env`; reject the legacy pre-0088 fields (`mode`, `version`, `isolation`, `project`, `parent_branch`, `parent_database`, `branch_policy`, `branch_name_template`, `ttl`, `role`, `image`, `route`) with errors naming this plan. Update `docs/schemas/scenery.config.v1.schema.json` (`kind` enum gains `postgres`) and `root_test.go`.

3. **Milestone 2 — Driver and resolver.** Add `github.com/jackc/pgx/v5` (stdlib adapter `pgx/v5/stdlib`; driver name `"pgx"`) — the only new Go dependency; justify it in `harness_arch.go`'s dependency allowlist. Create `internal/postgresdb/` rhyming with `internal/sqlitedb`:

        internal/postgresdb/postgresdb.go       Open (pool defaults), ParseURL, RedactURL
        internal/postgresdb/name.go             DatabaseNameFor(appID, service, appRoot) — sanitize + hash + 63-byte cap
        internal/postgresdb/admin.go            EnsureDatabase, DropDatabase, ResetDatabase (terminate backends, drop, recreate), ListSceneryDatabases
        internal/postgresdb/service.go          Service{Name, Database, URL, DatabaseURLEnv, Source("external"|"managed")}, Env(services), registry JSON encode/decode
        internal/postgresdb/*_test.go

   Admin SQL uses quoted identifiers built by a `quoteIdent` helper (Postgres has no placeholder support in DDL); never interpolate unsanitized input. Unit tests cover name derivation (stability, collision resistance across worktrees, 63-byte truncation keeping the hash suffix), URL parse/redact, and env encoding — all without a live server.

4. **Milestone 3 — Managed shared server.** New file `cmd/scenery/dev_services_postgres.go` (plus test):
   * Pin `postgres:18` as a toolchain `image` artifact (digest-pinned) in `scenery.toolchain.json` and `internal/toolchain/scenery.toolchain.json`; license `PostgreSQL`.
   * Server state file `<agent-home>/agent/postgres/server.json` (0600): `{schema_version: "scenery.dev.postgres.server.v1", container: "scenery-postgres", image, port, user: "scenery", password, created_at}`. Password generated once with `crypto/rand`; port allocated once from the free-port helper and persisted.
   * `ensureSharedPostgresServer(ctx)`: `docker container inspect scenery-postgres`; if absent, `docker run -d --name scenery-postgres -p 127.0.0.1:<port>:5432 -v scenery-postgres-data:/var/lib/postgresql/data -e POSTGRES_USER=scenery -e POSTGRES_PASSWORD=... <pinned image>`; if stopped, `docker start`. Readiness: retry `SELECT 1` on `postgres://scenery:...@127.0.0.1:<port>/postgres` with backoff (~30s cap), then upsert an agent substrate (`SubstratePostgres = "postgres"` re-added to `internal/agent/types.go`) with a lease per app root, following `verifySubstrateOwner` conventions.
   * `scenery db server status|start|stop|logs [--json]` in `db_cli.go` routing: status reports container state, port, image, database count/bytes (`pg_database_size`), leases; stop refuses while other live leases exist unless `--yes`.
   * Doctor: `db.postgres_server` check (info when no postgres services configured; error with remediation when configured but Docker unreachable). Reuse `docker.engine`. Add optional-tool warnings for `psql`/`pg_dump` only when postgres services exist.

5. **Milestone 4 — Dev env injection.** In `cmd/scenery/dev_services_postgres.go`, `managedPostgresEnv(ctx, appRoot, cfg, session, baseEnv)` mirroring `managedSQLiteEnv` (`dev_services.go:25`): for each configured postgres service, if the service's `database_url_env` is already set in the base env → mark `Source: "external"`, pass through, manage nothing; otherwise ensure the shared server, `EnsureDatabase(DatabaseNameFor(...))`, and inject `<SERVICE>_DATABASE_URL`. Always inject `SCENERY_POSTGRES_DATABASES_JSON` (`[{service, database, url, url_env, source}]`) when any postgres service exists. Wire into `devSupervisor.managedAppEnv` after SQLite injection; emit dev events (`service`, `database`, `source=postgres`). The single-service `DatabaseURL` alias rule: injected only when the app has exactly one database service *of any engine* and no explicit `database_url_env` — update the existing alias condition to count postgres services too.

6. **Milestone 5 — `scenery.sh/db` engine dispatch.** In `db/db.go`: resolution first checks configured postgres services (config + `SCENERY_POSTGRES_DATABASES_JSON` fallback, mirroring the SQLite discovery path), then sqlite. Dispatch on the resolved URL scheme: `sqlite:` → `sqlitedb.Open`; `postgres:`/`postgresql:` → `postgresdb.Open`. Pool cache stays keyed by DSN. Errors name the service, engine, and the env var to set. `db.Get(ctx)` with no name works when exactly one database service exists across both engines.

7. **Milestone 6 — Headless contract.** `scenery serve`/`worker` with a declared postgres service and no `<SERVICE>_DATABASE_URL` in the environment fails closed at startup with: the service name, the env var to set, and a pointer that the managed server is a `scenery up` dev substrate only. This parallels the plan-0094 storage fail-closed posture. Document in `docs/local-contract.md`.

8. **Milestone 7 — DB CLI.** In `db_cli.go` and `db_seed.go`:
   * `db list`: include postgres services; new schema `scenery.db.list.v2` with `engine` per entry (`sqlite` entries keep their fields; postgres entries carry `database`, redacted `url`, `source`). Keep emitting `scenery.db.sqlite.list.v1` semantics for sqlite-only apps is NOT required — migrate the schema version once, update `docs/schemas/`, tests, and any harness expectations together.
   * `db path <service>`: errors for postgres services ("postgres services have no file path; use `scenery db list --json` or `scenery db shell`").
   * `db shell <service>`: for postgres, exec `psql <dsn>`; clear error naming the resolved database when `psql` is missing.
   * `db reset`/`db drop`: managed postgres services → `ResetDatabase`/`DropDatabase` (terminate backends first, require `--yes` when multiple services); external services → refuse (see Decision Log).
   * `db snapshot create|restore <name>`: postgres via `pg_dump -Fc` / `pg_restore --clean --if-exists` into the existing snapshot directory layout; fail with an explicit message when the tools are missing. Restore requires `--yes`.
   * `db seed`: apply service-local `SERVICE/db/seed.sql` and generated seeds to postgres services through the same fail-closed ledger, recorded in a `scenery_internal.seed_runs` table (schema `scenery_internal`) inside the target database; destructive-SQL validation applies unchanged.
   * `db branch`: any attempt to include a postgres service errors with "postgres services are not branchable; worktree isolation is automatic via per-worktree databases (plan 0093)". SQLite branch behavior untouched.

9. **Milestone 8 — Atlas/sqlc plumbing.** Postgres dialect flows through the existing beta generator lifecycle: `generators.sqlc` schemas whose service is a postgres service use Atlas/sqlc postgres engines; `atlas_dev_url` may be a `postgres://` URL (including a scratch database on the managed server — `scenery generate sqlc` may `EnsureDatabase("scenery_atlas_dev_<hash>")` when the configured dev URL points at the managed server) or the already-supported `docker://` form. `scenery db diff --generated` introspects postgres schemas for postgres services. Keep the existing rule: explicit `atlas_source` refresh still requires an explicit `dev_url`. `//scenery:model` generated HCL is already schema-qualified, which maps directly onto Postgres schemas — cover one model-on-postgres case in tests or explicitly record deferral in the Decision Log.

10. **Milestone 9 — Fixture, tests, harness.** Fixture app `testdata/apps/postgres-basic/.scenery.json` with one `postgres` and one `sqlite` service. Unit tests: config parsing/validation, name derivation, env injection with a fake ensure function (no Docker), `db list --json` shape, fail-closed headless error, external-DSN passthrough. Harness: `runHarnessPostgresProbe` in a new `cmd/scenery/harness_self_postgres.go` — when Docker is reachable: ensure server, ensure database, round-trip a marker row through `scenery.sh/db`, verify a second app-root hash gets a distinct database, verify `scenery db reset` empties it, release lease; when Docker is unreachable: record an explicit skip in harness output (never silent).

11. **Milestone 10 — Docs and final validation.** Update in one change: `docs/local-contract.md` (service kinds, precedence rule, database naming, headless contract, CLI grammar, `scenery.db.list.v2`, stability: postgres services **beta**), `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/app-development-cookbook.md` (recipe: "Postgres service on the shared dev server" + "external DSN for production"), `docs/environment.md` + `docs/environment.registry.json` (injected: `SCENERY_POSTGRES_DATABASES_JSON`, the per-service URL pattern), `docs/schemas/` (config v1, db list v2, server status schema), `docs/knowledge.json`, `docs/plans/active.md`/`completed.md`. Run the full validation matrix below.

## Plan of Work

Work bottom-up so each commit compiles and no milestone depends on Docker to test:

Milestones 1–2 are pure Go with unit tests (config + resolver). Milestone 3 introduces the only process-management code; keep every Docker interaction behind a small interface (`postgresServerRunner` with a real docker-CLI implementation and a test fake) so supervisor and CLI logic is unit-testable without Docker — the same pattern the ZeroFS supervisor used before its removal, minus the evidence machinery. Milestone 4 wires injection into the dev supervisor and is testable with the fake. Milestones 5–7 are engine dispatch and CLI, testable against config + env fixtures (plus live checks in the harness probe). Milestone 8 touches only generator plumbing. Milestones 9–10 close with proof and docs.

Coordination note: plan 0094 also rewrote `dev_supervisor.go`/`dev_services.go` for ZeroFS removal, so this branch was rebased after that work before completion.

Interplay with plan 0088: 0088's outcome ("Scenery does not ship a built-in Postgres substrate coupled to auth/branching/Electric") remains true. What returns here is narrower: an opt-in service kind, an isolated shared dev server, and DSN passthrough. Auth, durable execution, and branching stay SQLite-native.

## Concrete Steps

All commands run from the repository root. Compile-check order matches milestone order.

1. **0088 closure.** Edit `docs/plans/0088-sqlite-service-databases.md` (Progress checkboxes, Outcomes & Retrospective citing commit `2c23508d`), move its entry from `docs/plans/active.md` to `docs/plans/completed.md`, refresh its `docs/knowledge.json` entry (`status: "completed"`, summary noting partial supersession by 0093).

2. **Config.** `internal/app/root.go`: extend the `validateDevServices` kind switch to `case "", "sqlite", "postgres":`; delete the `removedDatabaseKind` string-splitting special case (a bare `dev.services.postgres` entry with empty kind now means a postgres service named `postgres` — verify this is acceptable or require explicit kind; record the choice). Add `PostgresServiceConfig`, `PostgresServices()`, `PostgresService(name)` next to the SQLite helpers. Reject legacy fields for postgres kind. Update `docs/schemas/scenery.config.v1.schema.json` and `internal/app/root_test.go`.

3. **Resolver.** `go get github.com/jackc/pgx/v5@latest`; create `internal/postgresdb/` per Milestone 2. Keep the blank driver import isolated in this package. Name derivation:

        base = sanitizePG(appID) + "_" + sanitizePG(service) + "_" + shortIdentityHash(absAppRoot)
        sanitizePG: lowercase, [a-z0-9_], collapse repeats, trim; hash suffix always survives truncation to 63 bytes

   Move/share `shortIdentityHash` from `cmd/scenery/dev_services.go:120` rather than duplicating it (export from an internal package both can import).

4. **Server.** `cmd/scenery/dev_services_postgres.go` + `dev_services_postgres_test.go` per Milestone 3. Toolchain manifests gain the digest-pinned `postgres` image artifact. `internal/agent/types.go` re-adds `SubstratePostgres = "postgres"`. `doctor.go` gains `db.postgres_server` and conditional `tool.psql`/`tool.pg_dump` warnings. `db_cli.go` routes `scenery db server ...`; add JSON schema `docs/schemas/scenery.db.server.status.v1.schema.json`.

5. **Injection.** `managedPostgresEnv` + supervisor wiring per Milestone 4; update the `DatabaseURL` alias condition in `managedSQLiteEnv`'s caller so it counts services of both engines; dev events; update `dev_supervisor_sqlite_test.go` neighbors with postgres cases using the fake runner.

6. **db package.** `db/db.go` engine dispatch per Milestone 5; `db/db_test.go` covers: postgres service with env set (external), postgres service resolved from `SCENERY_POSTGRES_DATABASES_JSON`, mixed-engine app requiring explicit service names, unknown scheme error.

7. **Headless.** Startup validation in the serve/worker path (locate where SQLite env resolution happens for headless runs and add the postgres fail-closed check); tests assert the exact error text.

8. **CLI.** `db_cli.go`, `db_seed.go`, `db_branch_cli.go` guard, snapshot integration per Milestone 7; migrate `scenery.db.sqlite.list.v1` → `scenery.db.list.v2` (schema file, `db_cli_test.go`, `cli_contract_test.go`, harness expectations, `docs/local-contract.md` in the same commit).

9. **Generators.** `generate.go`/`validate.go`/`db_cli.go` diff path per Milestone 8.

10. **Fixture + harness.** `testdata/apps/postgres-basic/`; `cmd/scenery/harness_self_postgres.go` probe with explicit skip reporting.

11. **Docs sweep** per Milestone 10. Env registry entries: `SCENERY_POSTGRES_DATABASES_JSON` (`direction: "injected"`, `secret: true`, category `app.services.database`) and a pattern entry for `*_DATABASE_URL` if the registry requires one beyond the existing SQLite pattern (check how `AUTH_DATABASE_URL`-style names are registered and mirror it).

12. **Final gates.**

        git grep -n '"post" + "gres"' -- internal cmd db        # returns nothing
        git grep -ni "not supported.*postgres" -- internal cmd   # only intentional legacy-field validation remains

## Validation and Acceptance

For every milestone:

    go test ./...
    go test ./cmd/scenery

After Milestone 5:

    go test ./db ./internal/postgresdb ./internal/app

After Milestone 10, substantial-change validation:

    scenery harness self --summary --write

Do not run `go install ./cmd/scenery`; use the self-harness worktree-local `.scenery/harness/bin/scenery` build per root `AGENTS.md`.

Behavioral acceptance (requires Docker; run manually or via the harness probe):

* `scenery up` on `testdata/apps/postgres-basic` starts the shared server (first run), creates the service database, injects `<SERVICE>_DATABASE_URL`, and `scenery.sh/db` round-trips a row.
* The same app opened from a second Git worktree gets a *different* database name on the *same* server; rows do not leak between worktrees.
* With `<SERVICE>_DATABASE_URL` pre-set to an external server, `scenery up` touches Docker not at all (verify: no `scenery-postgres` container created on a clean machine).
* `scenery serve` with a declared postgres service and no DSN exits non-zero with the documented error.
* `scenery db list --json` emits `scenery.db.list.v2` with both engines; `scenery db reset --yes` empties only this worktree's database; `scenery db server status --json` reports leases.
* `go test ./...` passes on a machine with Docker stopped.

If a command cannot be run in the current environment, record exactly which and why in `Outcomes & Retrospective`.

## Idempotence and Recovery

All steps are Git-tracked code/doc edits; re-running overwrites the same files. Runtime idempotence requirements: `ensureSharedPostgresServer` must be safe to call concurrently from two `scenery up` sessions (take a file lock on `server.json` around create/start, mirroring the db-branch lock helpers `db_branch_lock_unix.go`); `EnsureDatabase` uses `CREATE DATABASE` guarded by a catalog existence check inside a retry loop (Postgres has no `CREATE DATABASE IF NOT EXISTS`); credential/port state is written atomically (temp file + rename). If the container is deleted out-of-band, the next `scenery up` recreates it and the named volume preserves data; if the volume is deleted, databases are recreated empty and `scenery db apply`/`seed` rebuild schema — document this as the recovery path. A failed harness probe leaves at worst a `scenery_harness_*` database; the probe drops it on entry as well as exit.

## Artifacts and Notes

* Survey evidence (2026-07-02): postgres kind rejected via `"post" + "gres"` at `internal/app/root.go:494`; SQLite env injection at `cmd/scenery/dev_services.go:25`; toolchain image support at `internal/toolchain/manifest.go:154` and `store.go:34`; doctor Docker checks at `cmd/scenery/doctor.go:848`; plan-0088 implementation commit `2c23508d`; pre-0088 shared-pool helper existed briefly (commit `5bc0fffe`) and was removed — do not resurrect its API.
* Naming example: app `onlv`, service `reports`, worktree `/Users/x/repos/onlv-wt2` → database `onlv_reports_a1b2c3d4e5f6`.
* The managed server is dev-substrate-only and documented **beta**; production is external DSN. A future plan may add: postgres branch provider (template databases), auth-on-postgres, managed-server upgrades across major versions (`pg_upgrade` or dump/restore), and dashboard explorer support.

## Interfaces and Dependencies

* `scenery.sh/db` public API unchanged: `Get(ctx, service...) (*sql.DB, error)`, `MustGet`, `Close`. New accepted URL schemes: `postgres://`, `postgresql://`.
* Config contract: `dev.services.<name>.kind: "postgres"` with optional `database_url_env`, `database`, `env`. JSON schema updated in `docs/schemas/scenery.config.v1.schema.json`.
* New injected env: `<SERVICE>_DATABASE_URL` (postgres DSN, dev-managed or passthrough), `SCENERY_POSTGRES_DATABASES_JSON`. Registered in `docs/environment.registry.json`; no new input knobs.
* New JSON surfaces: `scenery.db.list.v2` (replaces `scenery.db.sqlite.list.v1`), `scenery.db.server.status.v1`.
* New Go dependency: `github.com/jackc/pgx/v5` (stdlib adapter only in `internal/postgresdb`; justified in `harness_arch.go`).
* New toolchain artifact: `postgres` (`kind: "image"`, version 18, digest-pinned, license PostgreSQL) in both manifests.
* External host tools (optional, doctor-warned, never required for `go test`): `docker` (required only for the managed server), `psql`, `pg_dump`/`pg_restore`, `atlas`, `sqlc`.
* Substrate: `SubstratePostgres` re-added to `internal/agent/types.go`; leases per app root; `scenery db server status` is the visibility surface (plan-0079 pattern).
