# Postgres-Only Managed Branching

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Remove Neon as a Scenery database substrate while preserving the developer
workflow that made the Neon branch model useful:

```text
worktree or session branch
-> isolated PostgreSQL database
-> deterministic DatabaseURL injection
-> DB setup/apply/seed
-> sync, auth, and dev harnesses work
-> deterministic cleanup
```

The product capability is not "Neon". The product capability is a Scenery-owned
managed database branch for each app worktree or session. App code should still
receive `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, and
`SCENERY_MANAGED_DATABASE_NAME`, and app-local database lifecycle commands should
continue to run before the app starts.

This plan supersedes `docs/plans/0073-pg18-default-neon-selfhost.md`. The old
plan tried to preserve the Neon substrate and move it to PostgreSQL 18. Its
discoveries showed that continuing that path keeps Scenery coupled to Neon
storage, compute images, pageserver assumptions, and Neon-specific extensions.
This plan keeps the workflow and removes the Neon substrate.

The target default is PostgreSQL 18. PostgreSQL 19 is a preview/compatibility
lane only while it is beta.

## Progress

- [x] 2026-06-10: Created this ExecPlan as `docs/plans/0074-postgres-only-managed-branching.md`.
- [x] 2026-06-10: Marked `docs/plans/0073-pg18-default-neon-selfhost.md` as superseded by this plan.
- [x] 2026-06-10: Linked this plan from `docs/plans/active.md`.
- [x] 2026-06-10: Indexed this plan in `docs/knowledge.json`.
- [x] 2026-06-10: Compared against `Neon Versus Vanilla PostgreSQL 18 and 19 for scenery` and amended the plan with `cluster_basebackup`, wider cleanup inventory, and stronger validation/monitoring gates.
- [x] 2026-06-10: Audited Neon-named implementation files, schemas, docs, toolchain refs, and harness coverage; current code retains only an intentionally rejected legacy flag test and historical completed-plan references.
- [x] 2026-06-10: Added a provider-dispatched Postgres branch provider for `dev.services.postgres.kind: "postgres"` with explicit branch fields.
- [x] 2026-06-10: Implemented `scenery db postgres install|start|status|logs|stop|restart|uninstall`.
- [x] 2026-06-10: Implemented a Postgres branch registry at `~/.scenery/agent/postgres/branches.json` with provider `postgres` and schema `scenery.db.branch.registry.v2`.
- [x] 2026-06-10: Validated ONLV running on a Postgres 18 branch database with `/healthy` returning HTTP 200.
- [x] 2026-06-10: Replaced the remaining Neon-named branch-provider files/types with provider-neutral branch names and a Postgres-only provider dispatch.
- [x] Implement a local Postgres 18 dev cell and database-per-worktree branch registry.
- [x] 2026-06-10: Recorded `cluster_basebackup` as a future high-fidelity branch strategy; phase-one completion remains `template_database` plus schema-only diff, with full-cluster clone implementation intentionally outside this removal slice.
- [x] Preserve app-facing `DatabaseURL` injection, DB lifecycle, sync, auth, and running ONLV behavior for the Postgres branch path.
- [x] 2026-06-10: Removed Neon lifecycle commands, schemas, selfhost driver/runtime code, image/toolchain refs, active docs guidance, and replaced the default self-harness Neon proof with a Postgres branch lifecycle proof.
- [x] 2026-06-10: Validated in Scenery and ONLV with the commands in `Validation and Acceptance`.

## Surprises & Discoveries

- 2026-06-10: `scenery inspect docs --json` reported `review_due_count: 0` and `stale_count: 0`, so this plan does not need unrelated doc gardening.
- 2026-06-10: `docs/local-contract.md` already documents a regular managed Postgres path with version `18`, `isolation: "database"`, `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, `SCENERY_MANAGED_DATABASE_NAME`, `SCENERY_DEV_POSTGRES_ADMIN_URL`, `SCENERY_DEV_POSTGRES_BIN`, `SCENERY_DEV_POSTGRES_INITDB`, and `SCENERY_DEV_POSTGRES_EXTERNAL`. The implementation should reuse that surface where it fits instead of inventing a second Postgres control plane.
- 2026-06-10: `docs/plans/0073-pg18-default-neon-selfhost.md` records concrete evidence that the self-hosted Neon route is costly: separate storage and compute pins, `PG_VERSION=16` runtime plumbing, a storage image that omitted `/usr/local/v18`, pageserver PG17 assumptions, and Neon-specific preload behavior.
- 2026-06-10: Comparing this plan with `Neon Versus Vanilla PostgreSQL 18 and 19 for scenery` showed one important gap: this plan already covers template databases, dump/restore, PITR, logical replication, and future filesystem snapshots, but it did not explicitly name `pg_basebackup` as the portable full-cluster branch lane. Add it as a distinct strategy for large or high-fidelity branches rather than forcing all branches through database-level cloning.
- 2026-06-10: The same comparison expanded the cleanup and validation surface: `README.md`, all `cmd/scenery/db_neon*.go` files, adjacent dev service/psql/self-harness files, CI integration jobs, backup/restore drills, and replication-slot/WAL monitoring must be inspected during implementation.
- 2026-06-10: The first implementation slice intentionally kept Neon-named structs and files while dispatching by provider. This got ONLV onto Postgres quickly, but a follow-up rename/delete pass is still needed before calling Neon removal complete.
- 2026-06-10: ONLV's `scenery up --json --detach` reached `running` with API, sync, frontends, and TypeScript worker registered. Logs showed `database branch lease ready` from provider `postgres`, and `curl -k https://api.main-dbe32e.onlv.dev/healthy` returned HTTP 200.
- 2026-06-10: The cleanup pass removed `internal/neonselfhost`, `cmd/scenery/db_neon*.go`, Neon status/driver/backend schemas, Neon image refs, and the self-harness Neon proof. Current docs now describe Postgres branch leases under `~/.scenery/agent/postgres/branches.json` with registry schema v2.
- 2026-06-10: Final validation passed `go test ./...`, `scenery inspect docs --json`, `scenery doctor --json`, and `scenery harness self --summary --write` in this repo. ONLV passed `scenery check --json`, `go test ./...`, `just repo-harness`, `just db`, a managed-branch SQL smoke for database `onlv_onlv_runtime_0074`, and a restarted `scenery up --json --detach` session whose `/healthy` endpoint returned `{"status":"ok"}`.

## Decision Log

- Decision: Supersede `0073` and stop pursuing self-hosted Neon on PostgreSQL 18.
  Rationale: The desired developer workflow can be implemented on base Postgres with a smaller substrate. Continuing Neon work keeps Scenery coupled to pageserver, safekeeper, compute-node, timeline metadata, image availability, and Neon extension behavior.
  Date/Author: 2026-06-10 / pbrazdil + Codex

- Decision: Use PostgreSQL 18 as the default managed local database version.
  Rationale: PostgreSQL 18 is the stable target and has the runtime features ONLV wants to prove, including `uuidv7()`. PostgreSQL 19 Beta 1 was announced on 2026-06-04 and remains a preview whose behavior can change during beta.
  Date/Author: 2026-06-10 / pbrazdil + Codex

- Decision: Model worktree/session isolation as database-per-branch on one local Postgres cell in phase one.
  Rationale: It preserves the app-facing branch workflow while using boring Postgres primitives. Template databases make local branch creation fast enough for the default path, and dump/restore is a portable fallback.
  Date/Author: 2026-06-10 / pbrazdil + Codex

- Decision: Add `cluster_basebackup` as an explicit clone strategy, but do not make it the phase-one default.
  Rationale: `pg_basebackup` is the best vanilla Postgres primitive for portable full-cluster writable clones and better matches large or production-like datasets than database-level template cloning. It is still heavier than the common Scenery worktree case because it creates a whole cluster and usually a process/port per branch, so `template_database` stays the default local developer path.
  Date/Author: 2026-06-10 / Codex

- Decision: Do not preserve Neon-named compatibility shims after cleanup.
  Rationale: The repo rules favor a small, current public surface. `scenery db branch ...` remains the generic workflow; provider lifecycle commands should become `scenery db postgres ...`, not hidden Neon aliases.
  Date/Author: 2026-06-10 / pbrazdil + Codex

## Outcomes & Retrospective

Phase-one Postgres branch path is implemented and validated against the running ONLV app.

Update 2026-06-10: The cleanup/removal slice is implemented. The shipped default is local PostgreSQL 18 with database-per-branch `template_database` cloning, provider-neutral branch commands, `scenery db postgres ...` lifecycle commands, schema-only branch diff, Postgres registry v2, updated docs/schemas/toolchain, and a Postgres branch self-harness proof. `cluster_basebackup`, PITR, filesystem snapshots, and deeper data diff remain future strategies, not phase-one completion blockers.

## Context and Orientation

Current branch isolation is exposed through generic commands such as
`scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune`,
and the implementation now routes through provider-neutral branch files plus the
local Postgres provider. For the current implementation, start by reading:

- `cmd/scenery/db_branch_commands.go`
- `cmd/scenery/db_branch_pin.go`
- `cmd/scenery/db_branch_provider.go`
- `cmd/scenery/db_branch_registry.go`
- `cmd/scenery/db_postgres.go`
- `cmd/scenery/db_postgres_branch_provider.go`
- `cmd/scenery/harness_postgres_branch.go`
- `docs/local-contract.md`
- `docs/agent-guide.md`
- `docs/environment.md`
- `docs/environment.registry.json`
- `scenery.toolchain.json`
- `internal/toolchain/manifest_gen.go`
- `docs/schemas/scenery.db.branch.*`
- `testdata/apps/postgres-*`

The current ONLV app configuration uses `.scenery.json`
`dev.services.postgres.kind: "postgres"`, `mode: "local"`,
`isolation: "database"`, `branch_strategy: "template_database"`, project
`onlv`, parent database `onlv_main`, `branch_policy: "worktree"`, a worktree
branch template, and a sibling sync service. The app-facing contract is the
same managed database URL contract as before:

```json
{
  "dev": {
    "services": {
      "postgres": {
        "kind": "postgres",
        "mode": "local",
        "version": "18",
        "isolation": "database",
        "branch_strategy": "template_database",
        "project": "onlv",
        "parent_database": "onlv_main",
        "branch_policy": "worktree",
        "branch_name_template": "{app}/{git_branch}",
        "database": "onlv"
      }
    }
  }
}
```

The app must never need to know whether the branch came from a template
database, dump/restore, full-cluster base backup, PITR restore, or a future
filesystem snapshot. It consumes only the managed database URL contract.

Definitions:

- Postgres dev cell: a Scenery-owned local PostgreSQL 18 cluster, process, or
  container used for managed local databases.
- Branch database: a database in the dev cell that corresponds to one Scenery
  branch pin, worktree, or session.
- Branch cluster: a full PostgreSQL cluster clone that corresponds to one
  Scenery branch pin, used only when the chosen strategy needs cluster-level
  fidelity.
- Parent template: the source database used to create branch databases, treated
  as immutable during cloning.
- Branch registry: Scenery-owned metadata that records which branch maps to which
  database, role, strategy, endpoint, status, and expiration.
- External strategy: an escape hatch where Scenery uses explicit `DatabaseURL`
  and does not manage branch creation.

## Milestones

Milestone 1 renames the capability boundary. Keep the generic branch commands:

```text
scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune
scenery db psql
scenery db setup|apply|seed|reset|drop
```

Remove the Neon lifecycle noun:

```text
scenery db neon install|start|status|logs|stop|restart|uninstall
```

Add the explicit Postgres lifecycle noun:

```text
scenery db postgres install|start|status|logs|stop|restart|uninstall
```

Milestone 2 replaces the Neon provider interface with a provider-neutral
database branch provider. Rename types and files so future code does not route
through Neon-shaped concepts. The provider should expose ensure, inspect,
connection, reset, delete, restore, and diff operations and return branch
endpoint metadata without raw connection URLs in persisted pins or registries.

Milestone 3 implements the local Postgres 18 dev cell. Reuse existing env hooks
where possible:

```text
SCENERY_DEV_POSTGRES_ADMIN_URL
SCENERY_DEV_POSTGRES_BIN
SCENERY_DEV_POSTGRES_INITDB
SCENERY_DEV_POSTGRES_EXTERNAL
```

Remove Neon-specific env hooks unless a short deprecation window is explicitly
approved in this plan and documented:

```text
SCENERY_DEV_NEON_SELFHOST_DRIVER
SCENERY_NEON_SELFHOST_ROOT
SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER
```

The dev cell should own local state under `.scenery/postgres/` or the current
agent-home substrate root selected by existing Scenery managed-Postgres code:

```text
.scenery/postgres/
  cell.json
  postgresql.conf
  pg_hba.conf
  data/
  logs/
  branches.json
  dumps/
  restore/
```

Milestone 4 implements the branch registry. Keep `.scenery/worktree-db.json` in
phase one to reduce churn; the name is generic enough. Replace the global Neon
lease registry with `schema_version: "scenery.db.branch.registry.v2"` and
`provider: "postgres"`. Store endpoint components, not raw URLs.

Milestone 5 implements checkout for `branch_strategy: "template_database"`.
Resolve a branch name from the worktree or session, sanitize database and role
names, ensure the parent template exists, create or rotate the branch role and
password, clone the database from the template, grant ownership, write the
registry entry, and return a ready endpoint.

The SQL shape is:

```sql
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = :'template_db';

ALTER DATABASE :"template_db" WITH ALLOW_CONNECTIONS false;

CREATE DATABASE :"branch_db"
  WITH TEMPLATE :"template_db"
  OWNER :"branch_role"
  STRATEGY FILE_COPY;

ALTER DATABASE :"template_db" WITH ALLOW_CONNECTIONS true;
```

Always recover the template database back to `ALLOW_CONNECTIONS true` if clone
creation fails after it was closed.

Milestone 6 records the non-template clone lanes as future strategy names and
keeps them explicitly unsupported until implemented. Use
`branch_strategy: "dump_restore"` for small databases, schema-only branches,
cross-cluster/cross-version portability, or cases where the parent database must
stay open to connections:

```sh
createdb "$BRANCH_DB"
pg_dump --format=directory --jobs="$JOBS" --no-owner --no-acl --file "$DUMP_DIR" "$PARENT_URL"
pg_restore --jobs="$JOBS" --no-owner --no-acl --dbname "$BRANCH_URL" "$DUMP_DIR"
```

Use `branch_strategy: "cluster_basebackup"` when the branch needs full-cluster
fidelity or production-like scale and the cost of a cluster/process/port per
branch is acceptable:

```sh
pg_basebackup \
  --dbname "$SOURCE_ADMIN_URL" \
  --pgdata "$BRANCH_DATA_DIR" \
  --format plain \
  --wal-method stream \
  --checkpoint fast \
  --progress
```

The base-backup lane should write branch-specific `postgresql.auto.conf`, clear
stale `postmaster.pid`, disable or redirect archiving as appropriate, start the
branch cluster on a stable loopback port, and report endpoint metadata through
the same branch registry shape. It is not implemented in this phase-one
completion slice.

Milestone 7 implements reset, delete, expire, and prune. Reset terminates branch
connections, drops and recreates the branch database from the parent template or
dump, and updates the registry. Delete terminates branch connections, drops the
database, drops owned objects and the role, cleans up sync publications or
replication slots owned by that branch, removes the registry entry, and removes
the current worktree pin only when the current branch is explicitly forced.
Expire and prune become registry-driven operations that actually delete expired
branch databases when allowed.

Milestone 8 implements restore and diff honestly for the completed phase-one
branch strategy, and records deeper restore/data-diff levels as future work.
Restore starts with:

```text
level 1: restore branch from latest parent template
level 2: future restore branch from named local pg_dump archive
level 3: future restore branch from timestamp through PITR temp cluster, then export/import or expose the restored cluster directly when the branch strategy is cluster-level
```

Diff starts with:

```text
level 1: schema-only pg_dump diff
level 2: future row-count and migration-history summary
level 3: future table-specific data diff by explicit opt-in
```

Do not describe this as Neon timeline diff.

Milestone 9 preserves app/dev integration. `scenery up`, `scenery db psql`,
database setup/apply/seed, auth bootstrap, sync startup, workers, and
harnesses must keep consuming the same app-facing managed database URL contract.
sync must receive a deterministic session- or branch-scoped replication
stream identifier so publications and replication slots do not collide.

Milestone 10 removes Neon code and docs after the Postgres path is proven.
Delete or rename/adapt these areas:

```text
README.md
cmd/scenery/db_neon.go
cmd/scenery/db_neon_branch_cli.go
cmd/scenery/db_neon_branch_commands.go
cmd/scenery/db_neon_driver.go
cmd/scenery/db_neon_pin.go
cmd/scenery/db_neon_provider.go
cmd/scenery/db_neon_registry.go
cmd/scenery/db_neon_restore_points.go
cmd/scenery/db_neon_runtime.go
cmd/scenery/db_neon_toolchain.go
cmd/scenery/db_neon_utils.go
cmd/scenery/db_neon_generated.go
cmd/scenery/harness_neon.go
internal/neonselfhost/
docs/schemas/scenery.db.neon.*
testdata/apps/neon-*
Neon entries in scenery.toolchain.json
Neon image refs in generated toolchain manifest
Neon-specific docs/plans as active guidance
```

Also inspect adjacent files that may consume provider kind, branch status, or
session DB wiring, including `cmd/scenery/dev_services.go`,
`cmd/scenery/dev_substrate_manager.go`, `cmd/scenery/dev_supervisor.go`,
`cmd/scenery/db_setup.go`, `cmd/scenery/db_seed.go`, `cmd/scenery/psql.go`, and
other `cmd/scenery/harness*.go` files.

## Plan of Work

Begin with an implementation inventory. Use `rg` to find every Neon-named code,
schema, doc, fixture, toolchain, and harness path. Classify each item as one of:
delete, rename to Postgres, preserve as generic branch behavior, or retire with
an explicit schema/contract update. Add golden tests around the current generic
branch UX before deleting Neon-specific code so checkout, status, list, reset,
restore, diff, delete, worktree create, and session startup behavior can be
proven against both the old implementation and the new provider while the
migration is in flight.

Then introduce the provider-neutral branch boundary. Keep the behavior of
`scenery db branch ...` stable while moving the implementation behind a
`dbBranchProvider` interface and a `postgresBranchProvider` implementation.
Avoid a compatibility layer that simply puts Postgres behind Neon type names.

Next, implement the Postgres dev cell and branch registry. Prefer existing
managed-Postgres startup code for cluster creation, readiness, env injection,
and sync-friendly logical replication settings. Add only the branch-specific
parts that ordinary per-session database isolation does not already provide.

After the basic checkout path is working, add reset/delete/prune safety and the
phase-one restore/diff behavior. Keep unsupported strategies such as
`cluster_basebackup` visible in schema/docs as future strategy names but make
the provider fail explicitly until they are implemented. Preserve current safety posture:
protected parent databases cannot be deleted, destructive operations need
explicit confirmation, and current branch deletion needs `--force`.

Change the self-harness after the Postgres proof exists, not before. Replace
the Docker-backed Neon proof with a Postgres branch proof that creates a branch,
proves isolation, resets or reclones it, runs a backup/restore drill, and checks
logical replication behavior needed by sync.

Once the Postgres path passes focused validation, remove the Neon substrate
surface. Update `README.md`, `docs/local-contract.md`, `docs/agent-guide.md`,
`SKILL.md`, `docs/app-development-cookbook.md`, `docs/environment.md`,
`docs/environment.registry.json`, `docs/knowledge.json`, schemas, fixtures, and
harness docs in the same change that removes or renames behavior.

Finally, prove ONLV still works through the Scenery workflow. The important
acceptance is that app code does not care: it receives `DatabaseURL`, setup
runs, sync starts against the branch database, and `uuidv7()` is available
on PostgreSQL 18.

## Concrete Steps

1. Run orientation commands from the repository root:

   ```sh
   scenery inspect docs --json
   git status --short
   rg -n "neon|Neon|NEON|neonselfhost|db_neon|compute-node|pageserver|safekeeper|storage-broker|SCENERY_DEV_NEON|SCENERY_DEV_LOCAL_POSTGRES_BRANCH_DRIVER"
   ```

2. Inventory all affected files and update this plan's `Surprises & Discoveries`
   with anything that changes scope.

3. Rename the branch provider types. The intended shape is:

   ```go
   type dbBranchProvider interface {
       EnsureBranch(ctx context.Context, pin worktreeDBPin) (dbBranchBackendStatus, error)
       InspectBranch(ctx context.Context, pin worktreeDBPin) dbBranchBackendStatus
       Connection(ctx context.Context, pin worktreeDBPin) (dbBranchConnectionInfo, error)
       ResetBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) error
       DeleteBranch(ctx context.Context, pin worktreeDBPin, branch string, opts dbBranchOptions) error
       RestoreBranch(ctx context.Context, pin worktreeDBPin, opts dbBranchOptions) (dbBranchRestorePoint, error)
       DiffBranch(ctx context.Context, pin worktreeDBPin, target string, opts dbBranchOptions) (string, error)
   }
   ```

4. Implement the Postgres provider:

   ```go
   type postgresBranchProvider struct {
       adminURL string
       strategy postgresBranchStrategy
   }
   ```

   Supported strategies:

   ```text
   template_database
   cluster_basebackup
   dump_restore
   filesystem_snapshot
   external
   ```

   `filesystem_snapshot` may remain future/optional in this plan, but the enum
   should not imply it is implemented until validation proves it.

5. Implement `scenery db postgres install|start|status|logs|stop|restart|uninstall`
   and remove `scenery db neon ...` from help, JSON help, docs, tests, and
   schemas.

6. Implement `checkout` for `template_database`, including cleanup and
   connection termination. Add focused unit tests around identifier sanitization,
   registry writes, protected parent behavior, and template reopen recovery.

7. Implement reset, delete, expire, prune, restore level 1, and schema diff for
   `template_database`. Keep `dump_restore`, `cluster_basebackup`,
   `filesystem_snapshot`, deeper restore levels, and data diff as explicit
   future strategies that fail closed until implemented.

8. Update sync/logical-replication ownership. Ensure local Postgres starts
   with logical replication settings and that per-branch publication and slot
   names are deterministic, unique, and removed during delete/prune/down.

9. Add CI or self-harness coverage for branch creation, branch isolation,
   template reset/restore, schema diff, logical replication smoke behavior,
   replication-slot cleanup, and optional PITR/filesystem snapshot behavior
   when those future strategies are implemented on a runner that supports them.

10. Remove Neon artifacts, image refs, and plans as active guidance. Update
    docs and schemas in one coherent change.

11. Validate focused packages, the whole repo, Scenery runtime smoke, and ONLV.
    Record any skipped command and exact reason in this plan before handing off.

## Validation and Acceptance

Framework validation:

```sh
go test ./cmd/scenery ./internal/app ./internal/toolchain
go test ./...
scenery inspect docs --json
scenery doctor --json
scenery check --json
```

Runtime smoke:

```sh
scenery db postgres install --json
scenery db postgres start --json
scenery db postgres status --json
scenery db branch checkout pg-only-smoke --json
scenery db branch status --json
scenery db branch list --json
scenery db psql -- -Atc "select current_database(), current_setting('server_version_num'), uuidv7() is not null;"
scenery db branch reset --yes --json
scenery db branch diff main --json
scenery db branch delete pg-only-smoke --force --json
scenery up --json
scenery check --json
scenery down --json --db --state
```

Branch-provider integration gates:

```text
branch-create-and-start
branch-isolation-after-source-mutation
branch-reset-or-reclone
backup-and-restore-drill
logical-replication-smoke-for-sync
replication-slot-and-publication-cleanup
PITR-restore-to-timestamp when restore level 3 is implemented
filesystem-snapshot-smoke on a capable self-hosted runner only
```

ONLV validation from the ONLV app root:

```sh
just repo-harness
scenery check --json
go test ./...
just db
just psql
```

The SQL check must report PostgreSQL 18 and `uuidv7() is not null` as true.
`scenery up` must inject `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, and
`SCENERY_MANAGED_DATABASE_NAME` for the branch database. The app process should
not receive Scenery-injected `DATABASE_URL`.

For frontend-touching work, also run the existing app or dashboard frontend
validation commands required by the nearest `AGENTS.md`.

Before this plan is marked complete, run:

```sh
scenery harness self --summary --write
```

If the full self-harness cannot run in the current environment, record the exact
command, failure reason, and replacement focused validation in
`Surprises & Discoveries`.

## Idempotence and Recovery

Postgres dev-cell install/start/status commands must be safe to rerun. A
partially initialized cell should either resume cleanly or fail with a precise
recovery command.

Template-database checkout must always reopen the parent template if it closed
connections before cloning. If clone creation fails after creating a role or
database, the retry path should either reuse the partial resources safely or
delete them before recreating.

Dump/restore must write dumps under a Scenery-owned dump directory and clean up
temporary partial dumps after successful restore. Failed dumps should be kept
only when they help diagnosis and should be referenced in the error output.

Delete and prune must terminate branch connections before dropping databases.
They must not drop protected parent databases. They must not delete foreign
databases, roles, publications, or slots that are not Scenery-owned according to
the registry.

`cluster_basebackup` branches must clean up partially copied data directories,
ports, logs, and process records when startup fails. If a branch cluster starts,
the registry must record enough process and data-dir metadata for deterministic
stop/delete/prune without scanning unrelated Postgres processes.

PITR and backup-backed strategies must expose restore freshness and WAL/archive
health in status or doctor output before they are considered production-grade.
At minimum, monitor archive failures, retained WAL by replication slot, backup
freshness, restore-test freshness, long transactions, and snapshot headroom when
filesystem snapshots are enabled.

`SCENERY_DEV_POSTGRES_EXTERNAL=1` remains the escape hatch for explicit external
database URLs. In external mode, Scenery does not create or delete branch
databases and must make that limitation visible in status JSON.

## Artifacts and Notes

Reference facts checked when this plan was created:

- PostgreSQL 18 release notes list async I/O, retained optimizer statistics
  during `pg_upgrade`, skip scan, `uuidv7()`, virtual generated columns by
  default, OAuth authentication support, and `OLD`/`NEW` values in `RETURNING`.
- PostgreSQL 19 Beta 1 was announced on 2026-06-04 as a preview release whose
  details can change during beta.
- Base Postgres supports `CREATE DATABASE ... TEMPLATE ...` with clone
  strategies such as `WAL_LOG` and `FILE_COPY`.
- `pg_dump` and `pg_restore` provide the portable branch fallback.
- `pg_basebackup` provides the portable full-cluster branch lane for large or
  high-fidelity branches, at the cost of more storage, process, and port
  management than database-per-branch strategies.
- Postgres continuous archiving/PITR and `pg_basebackup` can support future
  branch-from-past workflows through a temporary restored cluster.
- Postgres logical replication supports sync-like publish/subscribe flows,
  but Scenery must own publication and replication-slot lifecycle.

Useful upstream docs:

- `https://www.postgresql.org/docs/current/release-18.html`
- `https://www.postgresql.org/about/news/postgresql-19-beta-1-released-3313/`
- `https://www.postgresql.org/docs/18/sql-createdatabase.html`
- `https://www.postgresql.org/docs/18/app-pgdump.html`
- `https://www.postgresql.org/docs/18/app-pgrestore.html`
- `https://www.postgresql.org/docs/18/continuous-archiving.html`
- `https://www.postgresql.org/docs/18/app-pgbasebackup.html`
- `https://www.postgresql.org/docs/18/logical-replication.html`

Optional future work:

- `branch_strategy: "filesystem_snapshot"` for closer local copy-on-write
  economics through ZFS, Btrfs, LVM, or Docker volume snapshots.
- Read-replica support through native physical streaming replication/hot
  standby for read-heavy local or production-like tests.
- PITR-based restore level 3 for "branch from timestamp" workflows.

## Interfaces and Dependencies

Public Scenery interfaces to preserve:

- `.scenery.json` `dev.services.postgres` as the app-owned managed database
  configuration surface.
- `scenery db branch status|list|checkout|reset|delete|restore|diff|expire|prune`.
- `scenery db psql`.
- `scenery up`, DB setup/apply/seed, auth bootstrap, sync startup, workers,
  and app env injection.
- `.scenery/worktree-db.json` as the worktree branch pin in phase one.
- JSON schemas for branch status/list/restore/diff, updated honestly for
  provider `postgres` and registry v2.

Public Scenery interfaces to remove or replace:

- `scenery db neon ...`
- Neon-specific env vars, unless this plan records a temporary deprecation
  decision before implementation.
- Neon schemas and status documents.
- Neon image/toolchain refs.

Internal dependencies:

- Managed local Postgres substrate startup and readiness.
- App-session env injection.
- Database lifecycle setup/apply/seed.
- sync process/container startup and replication stream naming.
- Worktree creation/removal.
- Self-harness runtime proofs.
- Docs knowledge index and schema validation.

External dependencies:

- PostgreSQL 18 server and client tools.
- Optional Docker for managed local Postgres when local binaries are absent.
- `pg_dump`, `pg_restore`, `createdb`, `dropdb`, and `psql` matching the active
  server major version where practical.
- Filesystem snapshot tooling only if the future optional strategy is enabled.
