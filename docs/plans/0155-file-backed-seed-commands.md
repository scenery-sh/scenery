# File-backed database seed commands

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds. Maintain this file according to `PLANS.md`.

## Purpose / Big Picture

Scenery applications need to bootstrap large, validated reference datasets without duplicating them into generated SQL. After this change, an app can declare a named database seed command with explicit workspace input files. `scenery db seed`, `scenery db setup`, and the `scenery up` setup phase will hash the command definition and every input, execute the command with the selected service database environment, record success, skip unchanged inputs, and rerun safely when an input changes. Ordinary `SERVICE/db/seed.sql` files retain their existing immutable fail-closed behavior.

The motivating acceptance app is ONLV's 28,355-row AHJ catalog. Its canonical 22 MB CSV and atomic Go importer should populate a fresh database automatically without a second 22 MB SQL artifact.

## Progress

- [x] (2026-08-27 22:12Z) Read the Scenery database lifecycle, configuration schema, CLI result schemas, dev setup fingerprinting, reset behavior, and repository instructions.
- [x] (2026-08-27 22:28Z) Implemented and documented file-backed seed-command configuration, deterministic hashing, execution, and current CLI/config schemas.
- [x] (2026-08-27 22:31Z) Added focused coverage for validation, service database routing, unchanged skips, changed-input reruns, JSON output capture, command failures, and service-reset ledger invalidation.
- [x] (2026-08-27 22:32Z) Wired ONLV's AHJ importer through `database.seed.commands` and proved reset/setup reconstruction, the canonical receipt, exact row counts, and unchanged skip against the live managed database.
- [x] (2026-08-27 22:34Z) Passed focused and full Scenery/ONLV suites, source-local check and harness, schema validation, the complete changed-area command union, and completed this plan.

## Surprises & Discoveries

- Observation: SQL seeds are executed through `database/sql`, not `psql`, and changed applied SQL seeds intentionally fail closed.
  Evidence: `cmd/scenery/db_seed.go` executes the complete SQL string in `postgresDatabaseSeedStore.ApplySeed`; `docs/local-contract.md` specifies immutable seed hashes and no force/reseed escape hatch.
- Observation: A service-only `scenery db reset <service>` recreates the service schema but leaves `scenery.seed_runs` intact.
  Evidence: `cmd/scenery/db_cli.go` calls `postgresdb.ResetSchema` only, while the seed ledger lives in the separate `scenery` schema. An unchanged seed would therefore be skipped after reset unless its ledger identity is cleared.
- Observation: The dev supervisor already fingerprints every discovered `dbSeedPlan`.
  Evidence: `buildDevDatabaseSetup` in `cmd/scenery/dev_supervisor.go` hashes each plan path and SHA-256, so command plans can join the existing setup invalidation path without a second watcher mechanism.
- Observation: `scenery.db.setup.result` runtime output can emit apply status `skipped`, but the checked schema did not allow it and embedded an obsolete seed-result schema revision.
  Evidence: `runDBSetupWithHooks` sets `result.Apply.Status = "skipped"` when no apply command exists; focused schema validation failed until the setup schema gained that status and the current nested seed revision.
- Observation: A focused test that accidentally selected the real managed-database path exposed stale machine-local Postgres state.
  Evidence: the local `scenery-postgres` container publishes port 65008 while the current agent state expected 49357. The test was corrected to remain in-process; live acceptance must repair or bypass this unrelated substrate mismatch without changing application source.
- Observation: `db apply -o json` and `db setup -o json` allowed apply-command stdout to precede their JSON envelopes.
  Evidence: the first live ONLV setup emitted `scripts/db-apply.sh` progress followed by the JSON result; after routing apply stdout away from machine-output writers, the complete setup stream parsed directly through `jq` as one document.

## Decision Log

- Decision: Add `database.seed.commands` to `.scenery.json`; each command has a stable `name`, target `service`, shell `command`, one or more workspace-relative regular-file `inputs`, and optional `cwd` and `env`.
  Rationale: Operational database bootstrap belongs beside the existing `database.apply` and `database.seed` configuration. Explicit inputs make rerun behavior deterministic and inspectable without scanning arbitrary source trees.
  Date/Author: 2026-08-27 / Codex.
- Decision: Keep SQL seeds immutable, but allow a successful command to replace its prior ledger hash when declared inputs change.
  Rationale: SQL seeds model one-time initial rows; import commands model versioned reference datasets and are required to be idempotent or atomic. Mixing those change policies would weaken the existing SQL safety contract.
  Date/Author: 2026-08-27 / Codex.
- Decision: Execute file-backed commands after ordinary SQL and typed-fixture seed plans, with `DATABASE_URL` pinned to the declared service DSN while preserving all generated service URL variables.
  Rationale: Importers can depend on schema plus small initial rows, and generic import code can consistently use `DATABASE_URL` without guessing service-specific variable names.
  Date/Author: 2026-08-27 / Codex.
- Decision: Clear the current app's discovered seed identities for a service after `scenery db reset <service>` succeeds.
  Rationale: Recreated service schemas are empty. Retaining a successful seed hash outside that schema makes setup incorrectly skip the data required to reconstruct it.
  Date/Author: 2026-08-27 / Codex.
- Decision: Suppress apply-command stdout only in JSON apply/setup modes while preserving human-mode progress and command stderr.
  Rationale: A machine-output command must emit one valid CLI envelope. Apply scripts already return actionable errors, while their ordinary progress is not part of the stable result schema.
  Date/Author: 2026-08-27 / Codex.

## Outcomes & Retrospective

Scenery now supports strict `database.seed.commands` declarations backed by explicit workspace files. Command definition and input content determine a stable hash; successful commands upsert their ledger record, unchanged commands skip, failures retain the prior record, SQL seed immutability is unchanged, and service reset clears only the selected service's current seed identities. Both seed/setup result schemas identify command records and preserve clean JSON output.

ONLV's AHJ catalog is the production acceptance consumer. Resetting `ahjs` and running normal setup recreated the schema and imported 28,355 rows with 28,355 unique IDs. The importer receipt reported canonical SHA-256 `4ce4a6d8d603df208efcf22b337acb255351ab2238919ef5600fcd92c532fee7` in 738 ms, and the immediate repeat skipped all twelve seeds. No generated AHJ `seed.sql` exists.

Validation passed: focused command/reset/schema tests, `go test ./cmd/scenery`, `go test ./internal/app`, `go test ./...`, every changed-area recommended generation/package command, and the complete Scenery self-harness. ONLV passed focused AHJ tests, `go test ./...`, `just repo-harness`, source-local `scenery check`, and the writable app harness. The implementation did not modify or absorb unrelated dirty work in either repository.

## Context and Orientation

Application config types live in `internal/app/root.go`, and the checked config shape lives in `docs/schemas/scenery.config.schema.json`. `cmd/scenery/db_seed.go` discovers SQL and typed-fixture plans, validates SQL safety, routes plans to service database URLs, consults `scenery.seed_runs`, applies work, and emits `scenery.db.seed.result`. `cmd/scenery/db_setup.go` composes apply then seed. `cmd/scenery/dev_supervisor.go` hashes discovered seed plans and runs the same seed engine during `scenery up`. `cmd/scenery/db_cli.go` owns service reset.

The CLI result schemas are `docs/schemas/scenery.db.seed.result.schema.json` and `docs/schemas/scenery.db.setup.result.schema.json`; their exact revisions are recorded in `cmd/scenery/payload_identity.go`. Practical and normative database lifecycle documentation lives in `docs/app-development-cookbook.md`, `docs/agent-guide.md`, and `docs/local-contract.md`.

## Milestones

Milestone 1 adds the strict config model and deterministic command-plan discovery. Invalid names, duplicate names, unknown services, absolute/traversing paths, missing files, directories, symlinks, and empty input lists fail before any database connection or command execution.

Milestone 2 executes command plans after SQL plans, captures output without corrupting JSON CLI output, updates command ledger hashes only after success, and retains the prior hash after failure. Result records identify SQL versus command seeds.

Milestone 3 makes service reset invalidate all currently discovered seed identities for that service and updates dev setup fingerprinting tests.

Milestone 4 updates schemas, exact revisions, docs, the ONLV integration, and runs real empty-database acceptance.

## Plan of Work

Extend `app.DatabaseSeedConfig` in `internal/app/root.go` with a list of command declarations. Put deterministic input validation and hashing in a focused `cmd/scenery/db_seed_command.go` helper so `db_seed.go` remains readable. Extend `dbSeedPlan` with plan kind and command metadata, merge command plans after SQL/fixture discovery, and execute them through an injectable command runner.

Add a ledger method that records a completed command with an upsert while preserving the current insert-only SQL method. Capture command stdout and stderr; expose trimmed stdout as optional result output and use stderr in failure detail. Pin `DATABASE_URL` to the plan's service DSN and apply command-local environment overrides.

Before a service reset, discover seed plans and retain the identities belonging to that service. After the schema reset succeeds, delete those identities for the current app from `scenery.seed_runs`. Full database reset already removes the ledger with the database.

Update the configuration and CLI JSON schemas, compute their exact current revisions, and update normative and practical docs. Add focused in-process tests in `cmd/scenery` and config decoding coverage. Then wire ONLV's AHJ importer and run the real database sequence.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./cmd/scenery -run 'TestDBSeed|TestDBSetup|TestDBReset'
    go test ./internal/app
    go test ./cmd/scenery
    go test ./...
    .scenery/harness/bin/scenery harness self --summary --write

Refresh `.scenery/harness/agent-context.json` through the repository-supported self-harness workflow and run the exact union in `changed_area.recommended_commands`. No compiler or generator package is intentionally changed, so committed TypeScript fixture regeneration is skipped unless the changed-area report includes the compiler/generator class.

From `/Users/petrbrazdil/Repos/onlv`, using the source-local CLI:

    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery db seed --app-root /Users/petrbrazdil/Repos/onlv --dry-run -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery db reset ahjs --app-root /Users/petrbrazdil/Repos/onlv
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery db setup --app-root /Users/petrbrazdil/Repos/onlv -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery db seed --app-root /Users/petrbrazdil/Repos/onlv -o json

Then query the AHJ service schema through `scenery db shell ahjs` and require exactly 28,355 rows.

## Validation and Acceptance

Focused tests must prove:

- unchanged command inputs produce `skipped` and do not execute twice;
- changed command input content executes again and replaces the prior command hash;
- a changed SQL seed still reports `changed` and fails closed;
- missing, unsafe, symlinked, or duplicate command inputs fail before execution;
- command failure does not advance the ledger;
- the command receives the target service DSN as `DATABASE_URL`;
- JSON output remains one valid CLI envelope even when the command emits stdout;
- service reset clears only that service's current app seed identities.

Real ONLV acceptance requires an empty AHJ schema to be recreated and populated by `db setup`, a count of exactly 28,355 unique catalog rows, a receipt matching the canonical CSV SHA-256, and the immediate repeat `db seed` to skip the unchanged command. The existing malformed-file importer test must remain green, proving a failed validation cannot clear the serving catalog.

The full Scenery Go suite, required self-harness, ONLV `go test ./solar/ahjs/...`, `scenery check -o json`, `go test ./...`, and `just repo-harness` must pass. If concurrent unrelated dirty work causes a failure, record the exact failing package/path and prove the changed packages independently rather than modifying unrelated work.

## Idempotence and Recovery

Command inputs are hashed before execution. A successful unchanged command is skipped. A changed command is safe to retry because its ledger hash changes only after the process exits successfully; the application command contract requires the data mutation itself to be transactional or idempotent. If the command succeeds but ledger recording fails, the next run executes it again, which is safe under that same requirement.

Service reset first recreates the schema and then removes the selected seed identities. If ledger cleanup fails, rerun `scenery db reset <service>` and then `scenery db setup`; no hidden force flag is introduced. Full managed-database reset removes both data and ledger.

## Artifacts and Notes

Current motivating artifact: ONLV `solar/ahjs/data/ahjs.csv`, 28,355 data rows, approximately 22 MB, content SHA-256 `4ce4a6d8d603df208efcf22b337acb255351ab2238919ef5600fcd92c532fee7`.

## Interfaces and Dependencies

The public config interface is `database.seed.commands`. The public command interfaces remain `scenery db seed`, `scenery db setup`, and `scenery up`; no new CLI command or environment knob is introduced. PostgreSQL remains the only database engine. The implementation uses the Go standard library plus Scenery's existing PostgreSQL driver boundary and command lifecycle helpers.
