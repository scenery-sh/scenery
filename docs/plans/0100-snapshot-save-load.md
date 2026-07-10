# 0100 - Snapshot Save/Load: Portable Full-App Backups of the Postgres Database and Storage Cell

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

After plan 0097, every scenery app has exactly one Postgres database (one schema per service plus the `scenery` schema for auth, durable jobs, and the seed ledger). After plan 0094, every app's storage capability is a plain directory tree under the agent storage cell (`<agent-home>/agent/storage/<cell-id>/objects/<store>/` with `__scenery/metadata/` sidecars). Together those two locations hold *all* durable app data — but today there is no single command that captures or restores them:

* `scenery db snapshot create|restore` covers only the database, only into an unportable app-local directory (`.scenery/db/snapshots/<name>/`), and depends on host-`PATH` `pg_dump`/`pg_restore` that can mismatch the Postgres 18 server.
* Storage has no bulk export at all — only per-object `scenery storage get/put`.

This plan adds one explicit, portable, full-app backup surface:

```text
scenery snapshot save --db --storage --output <file.zip> [--app-root <path>] [--json]
scenery snapshot load --input <file.zip> --db --storage --mode overwrite|merge \
    [--on-conflict fail|skip|overwrite] [--yes] [--dry-run] [--app-root <path>] [--json]
```

The archive is a single zip containing a versioned manifest (`scenery.snapshot.manifest.v1`), a Postgres custom-format dump (`pg_dump -Fc`) of the whole app database, and the verbatim storage-cell object trees (objects plus metadata sidecars) for every configured store. Because `-Fc` dumps do not pin a database name, an archive saved in one worktree or machine loads into another app root's differently-named managed database, making this the app's move/backup/share story.

Selection is explicit by design: `--db` and `--storage` are opt-in flags on both `save` and `load`; passing neither is an error, never a silent default. Load offers two modes: `overwrite` (drop and recreate, destructive, gated on `--yes`) and `merge` (an atomic all-or-nothing attempt that fails cleanly on any conflict).

The old `scenery db snapshot` command is retired in the same change — one save/restore spelling, per the repo rule of a small, singular public surface.

When this plan is done: `scenery snapshot save --db --storage --output app.zip` produces a zip that `scenery snapshot load` restores on a fresh worktree or machine with data intact; `scenery db snapshot` no longer exists in code, help, or docs; the managed-server dump/restore path runs `pg_dump`/`pg_restore` inside the `scenery-postgres` container so no host Postgres client tools are required; and the self-harness proves a full save→mutate→load round-trip live against Docker.

## Progress

* [x] 2026-07-07 - Explored the db CLI (`cmd/scenery/db_cli.go`), storage cell layout (`cmd/scenery/storage_cell.go`, now implemented by `storage/runtime.go`), managed Postgres server state (`cmd/scenery/dev_services_postgres.go`), and prior art (plans 0094, 0097, 0022); settled the four headline decisions with the repo owner; drafted this plan.
* [ ] Milestone 1: `scenery snapshot` command skeleton — dispatcher, explicit flag parsing/validation, help registration, JSON result types, new schemas.
* [ ] Milestone 2: Postgres dump/restore engine — docker-exec streaming for the managed server, host-PATH fallback for external DSNs.
* [ ] Milestone 3: `snapshot save` — manifest, zip writer, db section, storage section.
* [ ] Milestone 4: `snapshot load` — preflights, overwrite and merge modes, storage conflict handling, `--dry-run`.
* [ ] Milestone 5: Retire `scenery db snapshot` — code, tests, help, docs sweep, knowledge index.
* [ ] Milestone 6: Self-harness round-trip probe and final validation.

Update this section at every meaningful stopping point with date, what changed, and whether validation ran.

## Surprises & Discoveries

* 2026-07-07 (pre-implementation survey): `scenery db snapshot` restore uses `pg_restore --clean --if-exists` against the live database URL; it does not drop/recreate the database, so objects added after the snapshot that are absent from the dump survive a "restore". The new `overwrite` mode fixes this by dropping and recreating the database before restoring.
* 2026-07-07: The storage cell is shared across worktrees for the same app (`share: worktree` default, `cfg.StorageCellID()` excludes the worktree path). A `snapshot load --storage` therefore affects *all* worktrees of the app, unlike `--db` which targets only the current worktree's database. The load output must state the cell path it is writing to.
* 2026-07-07: `postgresDockerRunner.Run` (`cmd/scenery/dev_services_postgres.go:52-68`) buffers combined output as a string; dump/restore needs streaming stdio, so the engine adds a streaming variant rather than reusing `Run`.

Add new surprises here with the command, test, or file that exposed them.

## Decision Log

* Decision: The CLI surface is a new top-level `scenery snapshot save|load`, covering the database and the storage cell in one archive.
  Rationale: The feature spans two subsystems, so it does not belong under `scenery db` or `scenery storage`. "save/load" chosen over "export/import" and "backup create/restore" by the repo owner.
  Date/Author: 2026-07-07 / repo owner via Claude.

* Decision: Content selection is explicit and mandatory: `--db` and `--storage` are opt-in flags on both `save` and `load`; providing neither is a usage error. There is no "everything by default" mode.
  Rationale: Repo owner directive ("flags need to be provided, if not nothing is saved. we need to be very explicit"). Backups that silently include or omit data classes are worse than an error.
  Date/Author: 2026-07-07 / repo owner.

* Decision: `save` requires an explicit `--output <path>` ending in `.zip`; `load` requires an explicit `--input <path>`. No default filenames, no positional arguments.
  Rationale: Follows the explicitness directive above; also avoids polluting app roots with generated archive names.
  Date/Author: 2026-07-07 / Claude (consistent with owner's explicitness directive; flag in review if a default name is wanted).

* Decision: The archive format is zip (Go stdlib `archive/zip`): `manifest.json` at the root, `db/<database>.postgres.dump` stored uncompressed (`zip.Store`, since `-Fc` is already compressed), storage trees under `storage/<store>/...` deflated. Zip64 is handled by the stdlib automatically.
  Rationale: Repo owner asked for zip; zip allows reading the manifest without unpacking the archive; no new dependencies.
  Date/Author: 2026-07-07 / repo owner via Claude.

* Decision: The database section is one `pg_dump -Fc` of the entire app database — all service schemas plus the `scenery` schema (auth users, durable jobs, cron schedules, seed ledger). There is no schema/service filtering in v1.
  Rationale: Plan 0097 made "one database per app" precisely so one dump captures the whole app; the seed ledger and auth must travel with the data they describe or a restored app would re-run seeds or lose users.
  Date/Author: 2026-07-07 / Claude.

* Decision: `--mode overwrite` for `--db` drops and recreates the app database (terminating backends, like `postgresdb.DropDatabase`), then restores the dump into the fresh database. It does not use `pg_restore --clean`.
  Rationale: `--clean` leaves behind objects created after the snapshot; drop/recreate guarantees the restored state equals the archived state. Destructive, so gated on `--yes` and refused for external DSN targets (consistent with `scenery db reset`/`drop`, which refuse external DSNs).
  Date/Author: 2026-07-07 / Claude.

* Decision: `--mode merge` for `--db` is an atomic all-or-nothing attempt: `pg_restore --data-only --single-transaction --exit-on-error` into the existing database, with a preflight that every schema in the archive already exists in the target. It either commits fully or rolls back with no change.
  Rationale: Repo owner chose "atomic attempt, else fail". Refinement made here: a full (schema+data) restore without `--clean` would collide on `CREATE SCHEMA scenery` in any database `scenery up` has ever touched, making merge useless in practice. Data-only restore matches the real merge scenario — schema applied via `scenery db setup`, then archived rows merge in or the whole transaction rolls back on any PK/unique/FK conflict. Sequence values (`SEQUENCE SET`) restore as part of data. **Flagged for owner review: this narrows merge to "schema must already exist"; run `scenery db setup` first on fresh targets.**
  Date/Author: 2026-07-07 / Claude (interpreting owner's "merge if it will go through").

* Decision: Row-level conflict resolution (upsert/skip per row) for the database is explicitly out of scope, now and as a follow-up default. Merge means "atomic transaction that either applies or fails".
  Rationale: FK ordering, sequence advancement, and scenery-schema semantics make row merging a project of its own; the owner rejected it.
  Date/Author: 2026-07-07 / repo owner.

* Decision: Against the managed server, `pg_dump`/`pg_restore` run inside the `scenery-postgres` container via `docker exec -i`, streaming stdout/stdin; the in-container connection URL uses `127.0.0.1:5432` with the credentials from the server state file. For external `DATABASE_URL` targets, scenery falls back to host-`PATH` binaries with an error message that names the required client version if they are missing or mismatched.
  Rationale: The `postgres:18` image ships matching client tools, eliminating the "host pg_dump 16 vs server 18" failure that the current `db snapshot` invites. External servers are not scenery-managed, so host tools are the only option there.
  Date/Author: 2026-07-07 / repo owner via Claude.

* Decision: The storage section archives the verbatim on-disk tree of every *configured* store (objects plus `__scenery/metadata/` sidecars) from the app's storage cell. Files on disk under stores that are no longer declared in `.scenery.json` are not archived.
  Rationale: Configuration is the source of truth for what the app owns; archiving stray directories would resurrect deleted stores on load.
  Date/Author: 2026-07-07 / Claude.

* Decision: Storage load semantics per mode: `overwrite` replaces each archived store's directory wholesale via stage-and-rename (build the new tree in a staging directory inside the cell, rename the old store dir aside, rename staging in, delete the old tree). `merge` writes only missing objects, with `--on-conflict fail` (default: preflight all stores, abort before writing anything if any archive key already exists on disk), `skip` (keep existing objects), or `overwrite` (replace conflicting objects via temp+rename; requires `--yes`).
  Rationale: Objects are independently keyed files, so per-object conflict policy is meaningful in a way it is not for relational rows. `fail` as default matches the atomic spirit of db merge; stage-and-rename keeps overwrite crash-safe per store.
  Date/Author: 2026-07-07 / Claude.

* Decision: `snapshot load` refuses to run while a live dev session exists for the app root (same session discovery `scenery down` uses); the error tells the user to `scenery down` first. `snapshot save` is allowed against a live runtime: `pg_dump` is MVCC-consistent, and the storage walk is best-effort (documented).
  Rationale: Loading under a running app would race durable workers and app writes; a dump under load is safe and useful.
  Date/Author: 2026-07-07 / Claude.

* Decision: Load preflight checks (all run before any mutation, also exercised by `--dry-run`): (1) manifest schema version is known; (2) manifest `app.id` equals the current app's ID — mismatch is a hard error with no override flag in v1; (3) with `--db`, every schema listed in the manifest exists in the target's configured service set (plus `scenery`); (4) with `--storage`, every archived store is declared in the current config; (5) zip entry paths are validated against zip-slip (no absolute paths, no `..`, entries only under `db/` and `storage/<declared store>/`); (6) requested sections (`--db`/`--storage`) are present in the archive.
  Rationale: Explicit failure with exact names beats partial restores. A future `--allow-app-mismatch` can be added if moving data between app IDs becomes a real need; not now.
  Date/Author: 2026-07-07 / Claude.

* Decision: `scenery db snapshot` is removed entirely (command, parser, name validation, help entries, docs, tests). Old `.scenery/db/snapshots/` directories become inert files the user may delete; no migration tooling.
  Rationale: Repo owner chose fold-over-keep; two overlapping save/restore spellings invite drift, and repo rules forbid compatibility shims.
  Date/Author: 2026-07-07 / repo owner.

* Decision: New JSON contracts: `scenery.snapshot.manifest.v1` (inside the archive), `scenery.snapshot.save.v1` and `scenery.snapshot.load.v1` (command `--json` output). All three get schema files under `docs/schemas/` and harness schema-validation examples.
  Rationale: Repo-wide rule — machine-readable JSON surfaces are the agent contract and must be schema-backed.
  Date/Author: 2026-07-07 / Claude.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Terms:

* App database: the single Postgres database per app root/worktree, named `<sanitizePG(app_id)>_<shortIdentityHash(appRoot)>` (`internal/postgresdb/name.go`), containing one schema per service plus the `scenery` schema. Resolved for CLI use by `resolvePostgresDatabaseForCLI` (`cmd/scenery/db_cli.go:529`), which returns a `postgresdb.Database{Database, URL, Source, Schemas}` where `Source` is `managed` or `external`.
* Managed Postgres server: the shared Docker container (`scenery-postgres`, `postgres:18`) whose state (container name, volume, loopback port, user, random password) lives in `$SCENERY_AGENT_HOME/postgres/server.json` (`postgresServerState` in `cmd/scenery/dev_services_postgres.go:32`). `ensureSharedPostgresServer` starts it; `postgresDocker` (a `postgresDockerRunner`, line 52) shells out to docker.
* Storage cell: the shared-across-worktrees directory `<agent-home>/agent/storage/<cell-id>/` with per-store object trees under `objects/<store>/` and metadata sidecars under `objects/<store>/__scenery/metadata/<key>.json`. Resolved by `resolveStorageCellPlan` (`cmd/scenery/storage_cell.go:24`); per-store dir via `storageStoreObjectsDir`. The canonical backend (`storage/runtime.go`) writes objects with temp-file+rename and checked fsync.
* Snapshot archive: the zip this plan introduces — `manifest.json`, `db/<database>.postgres.dump`, `storage/<store>/...`.
* Seed ledger: `scenery.seed_runs` table recording applied seeds by `(app_id, path, sha256)`; it lives in the dump and must travel with the data.

Key existing code:

* `cmd/scenery/db_cli.go` — `dbCommand` dispatcher (line 29); `dbSnapshotCommand` (297), `snapshotPostgresDatabase` (599), `restorePostgresDatabase` (619), `parseDBSnapshotArgs` (818), `validDBSnapshotName` (864): all removed or superseded by this plan. `resolvePostgresDatabaseForCLI` (529) and `managedPostgresAdmin` (640) are reused as-is.
* `internal/postgresdb` — `DropDatabase`, `EnsureDatabase`, `ParseURL`, `RedactURL`, `DatabaseNameFor`; the pgx stdlib driver.
* `cmd/scenery/dev_services_postgres.go` — server state, `ensureSharedPostgresServer`, `postgresDockerRunner` interface (52) with the buffered `Run`; this plan adds a streaming sibling.
* `cmd/scenery/storage_cell.go` — cell path resolution.
* `cmd/scenery/main.go` — top-level command switch (~line 40); add `case "snapshot"`.
* `cmd/scenery/help.go` — command help registry; `scenery db snapshot create|restore` appears at lines 105-106 and in the db command entry (263-271).
* `cmd/scenery/inspect.go:407` — `writeInspectJSON`, the shared 2-space-indent JSON writer for `--json` output.
* `cmd/scenery/harness_schema.go` — schema-validation examples; new schemas register examples here.
* Session discovery used by `scenery down`/`scenery ps` (see `cmd/scenery/agent.go`) — reused for the live-session guard.

Docs that name the old command (the Milestone 5 sweep): `docs/local-contract.md` (lines 39, 98, 298, 348, 400), `docs/agent-guide.md` (248), `README.md` (261, 309), `docs/app-development-cookbook.md` (630, 639), `SKILL.md` (237), plus `docs/knowledge.json` freshness metadata for each.

Archive layout (normative):

    manifest.json                                  scenery.snapshot.manifest.v1
    db/<database>.postgres.dump                    pg_dump -Fc of the app database (only with --db)
    storage/<store>/<object key path>              verbatim object files (only with --storage)
    storage/<store>/__scenery/metadata/<key>.json  metadata sidecars, verbatim

Manifest fields (normative): `schema_version`, `created_at` (UTC RFC3339), `scenery_version`, `app` `{name, id}`, `db` (present only when saved) `{database, source, schemas: [{service, schema}], dump_file, dump_format: "pg_custom"}`, `storage` (present only when saved) `{cell_id, stores: [{name, files, bytes}]}` where `files`/`bytes` count archived entries including sidecars.

CLI grammar (normative):

    scenery snapshot save --output <file.zip> [--db] [--storage] [--app-root <path>] [--json]
    scenery snapshot load --input <file.zip> [--db] [--storage] --mode overwrite|merge
        [--on-conflict fail|skip|overwrite] [--yes] [--dry-run] [--app-root <path>] [--json]

Validation rules: at least one of `--db`/`--storage` on both verbs; `--mode` mandatory on load with no default; `--mode overwrite` requires `--yes`; `--on-conflict` valid only with `--mode merge` and `--storage`, defaults to `fail`, and `--on-conflict overwrite` requires `--yes`; `--db --mode overwrite` refuses external-DSN targets; requested sections must exist in the archive; `save --storage` on an app with no configured stores is an error naming the config; unknown flags/arguments error as elsewhere in the CLI.

## Milestones

Each milestone leaves `go test ./...` green.

1. **Command skeleton and contracts.** New files `cmd/scenery/snapshot_cli.go` (dispatcher `snapshotCommand`, option structs `snapshotSaveOptions`/`snapshotLoadOptions`, parsers with the full validation-rule set above) wired into `main.go` and `help.go`. JSON result types for `scenery.snapshot.save.v1` / `scenery.snapshot.load.v1` and the manifest type. Schema files `docs/schemas/scenery.snapshot.manifest.v1.schema.json`, `...save.v1...`, `...load.v1...`; harness schema examples registered in `harness_schema.go`. Parser unit tests covering every validation rule (`snapshot_cli_test.go`).
2. **Postgres dump/restore engine.** New `cmd/scenery/postgres_dumptool.go`: a streaming exec seam (interface with a real implementation and a test fake) that runs `docker exec -i <container> pg_dump -Fc -d <in-container URL>` with stdout streamed to a writer, and `docker exec -i <container> pg_restore <flags> -d <in-container URL>` with stdin streamed from a reader, deriving the in-container URL (`127.0.0.1:5432`, state-file credentials) from `postgresServerState`; plus the host-`PATH` fallback used when `Database.Source == external` (error text names the missing binary and the version-match requirement). Unit tests with the fake runner assert exact argv, URL derivation, and fallback selection. `db_cli.go`'s `snapshotPostgresDatabase`/`restorePostgresDatabase` are not yet touched.
3. **`snapshot save`.** `cmd/scenery/snapshot_save.go`: resolve app + database (`resolvePostgresDatabaseForCLI`, ensuring the managed server is up via the existing lifecycle helper) and storage cell (`resolveStorageCellPlan`); stream the dump into the zip (`zip.Store` for the dump entry), walk each configured store's directory into `storage/<store>/...` (deflate), write `manifest.json` last with real counts; write the zip to `--output` via temp-file+rename in the destination directory. Human and `--json` output report the archive path, database, schemas, stores, files, and bytes. Tests: storage-only round-trip against a temp cell (no Docker needed); manifest golden assertions; db section covered via the fake dump runner.
4. **`snapshot load`.** `cmd/scenery/snapshot_load.go`: open zip, decode manifest, run the full preflight list (app ID match, schema ⊆ services, store ⊆ config, zip-slip, section presence, live-session guard, external-DSN refusal for overwrite); `--dry-run` stops here and reports the plan. Then db: overwrite = terminate/drop (`postgresdb.DropDatabase`) + `EnsureDatabase` + full `pg_restore --exit-on-error`; merge = schema-existence preflight + `pg_restore --data-only --single-transaction --exit-on-error`. Then storage: overwrite = stage-and-rename per store; merge = conflict scan across all stores first, then apply per `--on-conflict`. Output reports per-section actions, counts, skipped/overwritten conflicts, and the storage cell path. Tests: storage modes and conflict policies against temp cells; zip-slip rejection with crafted archives; preflight failures with exact error text; db paths via the fake runner.
5. **Retire `scenery db snapshot`.** Delete `dbSnapshotCommand`, `parseDBSnapshotArgs`, `validDBSnapshotName`, `snapshotPostgresDatabase`, `restorePostgresDatabase`, the `case "snapshot"` in `dbCommand`, and their tests; update the `dbCommand` usage string and `db_cli_test.go:15`. Sweep help (`help.go:105-106`, db entry 263-271; add the `snapshot` command entry) and docs: `docs/local-contract.md` (stability lists at 39/98, prose at 298/400, grammar block at 348 — remove `db snapshot`, add the two `snapshot` lines and the new schema/artifact contracts), `docs/agent-guide.md:248`, `README.md:261/309`, `docs/app-development-cookbook.md:630/639` (the save-point recipe now uses `scenery snapshot save --db`), `SKILL.md:237`. Update `docs/knowledge.json` (this plan's entry, refreshed doc metadata) and `docs/plans/active.md`.
6. **Self-harness proof.** Extend `cmd/scenery/harness_self_postgres.go` (Docker-gated) with a round-trip probe: fixture app with a db service and a storage store → write a row and an object → `snapshot save --db --storage` → mutate both → `load --mode overwrite --yes` restores the saved state → a second `load --mode merge` fails atomically on the now-conflicting row (proving rollback leaves data untouched). Run `scenery harness self --summary --write`.

## Plan of Work

Build additively: the new `snapshot` command lands fully working (Milestones 1-4) before the old `db snapshot` is deleted (Milestone 5), so the repo never lacks a database save/restore path mid-series. The dump/restore engine (Milestone 2) is the only genuinely new infrastructure — everything else composes existing pieces (`resolvePostgresDatabaseForCLI`, `resolveStorageCellPlan`, `writeInspectJSON`, `postgresdb` admin helpers, stdlib `archive/zip`).

Keep the exec seam narrow and test-faked: the streaming runner interface takes argv plus reader/writer, so save/load logic is fully unit-testable without Docker or Postgres; only the harness probe (Milestone 6) exercises the live path. Storage logic operates on plain directory trees and is fully covered by temp-dir tests.

Ordering within load matters and is fixed: all preflights → db restore (atomic on its own) → storage writes. If storage application fails after a successful db restore, the command reports exactly which stores completed; the archive remains the recovery input (re-run load with `--storage` only). This partial-failure contract is documented in the load output and local-contract prose.

## Concrete Steps

All commands run from the repository root.

1. `cmd/scenery/snapshot_cli.go`: `snapshotCommand(args []string) error` dispatching `save`/`load`; parsers enforcing every rule in Context and Orientation; wire `case "snapshot": return snapshotCommand(args[1:])` into `main.go`; add the help entry (`Summary: "Save and load portable app snapshots (Postgres database and storage cell) as zip archives."`, usage lines for both verbs) and the two command spellings to the help index.
2. `cmd/scenery/snapshot_types.go` (or inline in snapshot_cli.go if small): manifest struct + `scenery.snapshot.manifest.v1`; save/load result structs with `schema_version` fields. Write the three schema files under `docs/schemas/`; add examples to `harness_schema.go`.
3. `cmd/scenery/postgres_dumptool.go`: `type pgDumpRunner interface { Dump(ctx, target, out io.Writer) error; Restore(ctx, target, flags []string, in io.Reader) error }` with `target` carrying `postgresdb.Database` + server state; docker-exec implementation (managed) and host-PATH implementation (external); constructor picks by `Source`. In-container URL derivation helper with unit test.
4. `cmd/scenery/snapshot_save.go`: assemble archive as described in Milestone 3. Use a `*zip.Writer` over a temp file in the output directory; `CreateHeader` with `zip.Store` for the dump entry; `filepath.WalkDir` per store writing relative slash paths; fsync + rename the temp file to `--output`.
5. `cmd/scenery/snapshot_load.go`: preflight + apply as described in Milestone 4. Zip-slip guard: every entry name must be `manifest.json`, `db/<manifest dump_file>`, or `storage/<declared store>/<clean relative path>` after `path.Clean`, rejecting `..`, absolute paths, and backslashes. Staging dirs live under `<cellRoot>/objects/` (same filesystem, so rename is atomic); trash dirs removed after successful swap and reported if removal fails.
6. Tests: `snapshot_cli_test.go` (parsers), `snapshot_save_test.go` / `snapshot_load_test.go` (temp-cell round-trips, conflict matrices, zip-slip, preflight errors, fake-runner argv assertions).
7. Milestone 5 sweep exactly as listed there; grep gate: `grep -rn "db snapshot" cmd docs README.md SKILL.md DSL.md` returns nothing outside `docs/plans/`.
8. `cmd/scenery/harness_self_postgres.go`: add the round-trip probe; keep it inside the existing Docker-gated step so `go test ./...` stays Docker-free.
9. Register this plan in `docs/plans/active.md` and `docs/knowledge.json` (done at planning time); on completion update `Outcomes & Retrospective` and `docs/plans/completed.md`.

## Validation and Acceptance

For every milestone:

    go test ./...
    go test ./cmd/scenery

After Milestone 5 (docs sweep):

    grep -rn "db snapshot" cmd docs/local-contract.md docs/agent-guide.md docs/app-development-cookbook.md README.md SKILL.md ; test $? -eq 1

After Milestone 6, substantial-change validation:

    scenery harness self --summary --write

Behavioral acceptance (live, Docker available; fixture app with one db service, one seed, one storage store):

* `scenery snapshot save --db --storage --output /tmp/app.zip --json` emits `scenery.snapshot.save.v1`; the zip contains `manifest.json`, one `db/*.postgres.dump`, and the store tree including `__scenery/metadata/` sidecars; no host `pg_dump` is required (verify with `PATH` stripped of Postgres client tools).
* On a second worktree of the same app (different database name): `scenery snapshot load --input /tmp/app.zip --db --mode merge` after `scenery db setup` merges the data atomically; re-running the same load fails with a rollback and the database is byte-identical to before the attempt (row counts unchanged).
* `scenery snapshot load --input /tmp/app.zip --db --storage --mode overwrite --yes` restores exact saved state after arbitrary mutation; seed ledger rows travel (a subsequent `scenery db seed` reports `skipped`, not re-applied).
* `scenery snapshot save --output x.zip` (no section flags) errors; `load` without `--mode` errors; `load --mode overwrite` without `--yes` errors; `load` while `scenery up` is running errors and names `scenery down`.
* `scenery snapshot load --input /tmp/app.zip --storage --mode merge --dry-run --json` reports planned writes and conflicts without touching the cell.

Do not run `go install ./cmd/scenery`; use the self-harness worktree-local binary per root `AGENTS.md`.

## Idempotence and Recovery

`snapshot save` writes via temp-file+rename, so a killed save leaves at most a temp file in the output directory and never a truncated archive at `--output`; re-running overwrites cleanly. `snapshot load --mode overwrite` for db is drop→create→restore: if restore fails midway the database exists but is partial — re-running the same load is the recovery (drop/create are idempotent). Merge mode is single-transaction and leaves no partial state by construction. Storage overwrite uses stage-and-rename per store; a crash mid-swap leaves either the old or the new tree plus a staging/trash directory under `<cellRoot>/objects/` that the next load run detects and cleans (and reports). Storage merge writes each object via the temp+rename pattern; re-running with `--on-conflict skip` completes an interrupted merge without touching already-written objects. All preflights run before any mutation, so `--dry-run` output is an accurate predictor of what a real run would attempt.

## Artifacts and Notes

* Exploration evidence (2026-07-07): current snapshot mechanics at `cmd/scenery/db_cli.go:297-329, 599-638`; storage cell contract at `cmd/scenery/storage_cell.go` and plan 0094; database-per-worktree naming and `scenery` schema contents per plan 0097's Decision Log.
* Prior art deliberately not reused: plan 0022's `scenery.data.export.v1` is a records-level fixture format for the data platform, not a byte-faithful backup; the cookbook's rclone/restic recipe remains the *continuous offsite replication* story and is complementary — this plan is the *point-in-time portable archive* story. The cookbook gains a sentence distinguishing the two.
* The archive intentionally excludes: `.scenery/gen/` (regenerable cache), session state, Victoria logs/metrics, and the `scenery_symphony` machine database (rebuildable, machine-scoped).

## Interfaces and Dependencies

* New public CLI: `scenery snapshot save|load` with the grammar and validation rules in Context and Orientation. Removed public CLI: `scenery db snapshot create|restore` (and its `.scenery/db/snapshots/` artifact path leaves the contract).
* New JSON contracts: `scenery.snapshot.manifest.v1` (in-archive), `scenery.snapshot.save.v1`, `scenery.snapshot.load.v1` (command output), each with a schema under `docs/schemas/` and a harness example.
* External binaries: `docker` (already required for the managed server); `pg_dump`/`pg_restore` on the host only for external-DSN apps. No new Go dependencies — `archive/zip` is stdlib.
* Unchanged surfaces this plan relies on: `internal/postgresdb` admin helpers, `resolvePostgresDatabaseForCLI`, `resolveStorageCellPlan`, `writeInspectJSON`, the storage local-backend on-disk layout (objects + `__scenery/metadata/` sidecars), and the `scenery.sh/storage` app API (untouched).
* Docs updated together (Milestone 5): `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/app-development-cookbook.md`, `docs/knowledge.json`, `docs/plans/active.md`. No new environment variables.
