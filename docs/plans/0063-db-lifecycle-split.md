# Database Lifecycle Split

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

scenery currently has a beta `scenery db sync` command that mixes two different kinds of work: mutating a development database through `database.apply`, then regenerating dependent SQLC source artifacts. That coupling is confusing for agents and target apps because database state and generated source files have different safety rules, retry behavior, and review expectations.

The goal of this plan is to split database lifecycle work into explicit commands:

```text
scenery db apply
scenery db seed
scenery db setup
```

Generated-file work stays under:

```text
scenery generate sqlc
```

Service-local seed files such as `SERVICE/db/seed.sql` are initial data. They are not Atlas schema input, not SQLC input, and not part of generated source artifacts.

## Progress

- [x] 2026-06-02: Accepted the CLI taxonomy with the user in GitHub issue #21.
- [x] 2026-06-02: Created this ExecPlan and linked it from `docs/plans/active.md`.
- [x] 2026-06-02: Implemented service DB artifact discovery through `scenery inspect generators --json`.
- [x] 2026-06-02: Implemented `scenery db apply`.
- [x] 2026-06-02: Implemented `scenery db seed` with a ledgered first-pass safety model.
- [x] 2026-06-02: Implemented `scenery db setup` as apply followed by seed.
- [x] 2026-06-02: Wired `scenery dev` to the setup lifecycle before app startup, with rebuild skipping unless DB setup inputs change.
- [x] 2026-06-02: Added seed safety diagnostics for destructive SQL and kept the no-escape-hatch fail-closed model.
- [x] 2026-07-06: ONLV migrated to the split apply/seed/setup lifecycle as part of the Postgres-only data-platform acceptance in plan 0097.

## Surprises & Discoveries

- 2026-06-02: Existing docs describe `scenery db sync` as a beta mixed command that mutates the configured dev database and then refreshes dependent SQLC artifacts. The new contract should deprecate that mixed shape rather than deepen it.
- 2026-06-02: `scenery inspect generators --json` was the best fit for the first DB artifact manifest because it already reports generated-source inputs and outputs. The new `db_artifacts` array keeps seed files visible without adding them to SQLC generator inputs.

## Decision Log

- Decision: Keep database mutation commands under `scenery db`.
  Rationale: `scenery db` already owns managed Postgres shell, reset, drop, and snapshot behavior. Schema apply, seed data, and setup are database-state operations, not generated-file operations.
  Date/Author: 2026-06-02 / Codex with user approval

- Decision: Define `scenery db apply` as schema/database mutation only.
  Rationale: Applying schema or app-owned database setup should not regenerate source files or apply seed data as a hidden side effect.
  Date/Author: 2026-06-02 / Codex with user approval

- Decision: Define `scenery db seed` as initial-data application only.
  Rationale: Seed data has different safety semantics from schema apply. It should run after schema exists and should be inspectable as data, not as schema or query-generation input.
  Date/Author: 2026-06-02 / Codex with user approval

- Decision: Define `scenery db setup` as `db apply` followed by `db seed`.
  Rationale: Local development needs one ergonomic setup command, but the underlying operations should stay independently callable and testable.
  Date/Author: 2026-06-02 / Codex with user approval

- Decision: Keep `scenery generate sqlc` as the SQLC spelling.
  Rationale: SQLC generation writes source artifacts. It can refresh generated schema SQL from Atlas schema definitions and run `sqlc generate`, but it must not apply schema or seed data to a database.
  Date/Author: 2026-06-02 / Codex with user approval

- Decision: Treat `scenery db sync` as a deprecated beta mixed command.
  Rationale: It exists today and can stay during migration, but new implementation work should not add behavior to the mixed command.
  Date/Author: 2026-06-02 / Codex with user approval

- Decision: Do not add seed safety escape hatches in the first implementation.
  Rationale: The first seed implementation should fail closed with clear diagnostics when a previously-applied seed changes. Explicit reset, drop, and snapshot commands already cover destructive local recovery.
  Date/Author: 2026-06-02 / Codex with user approval

## Outcomes & Retrospective

Completed. Database mutation is split into apply, seed, and setup; SQLC remains source generation only. ONLV acceptance was completed by plan 0097's Postgres migration on 2026-07-06.

As of 2026-06-02, the first two implementation slices are complete: service DB artifact discovery and `scenery db apply`.

Later on 2026-06-02, `scenery db seed` was added. It discovers seed files through the DB artifact graph, stores successful seed runs in `scenery_internal.seed_runs`, skips unchanged seeds, and reports changed previously-applied seeds as failures.

Later on 2026-06-02, `scenery dev` was wired to run DB setup before app startup. The supervisor fingerprints `database.apply` config plus seed file hashes after successful setup, so ordinary rebuilds skip expensive DB work while DB-related changes rerun setup through the existing compile/setup failure path.

Later on 2026-06-02, seed safety diagnostics were added. The seed command now validates seed SQL before opening the database and fails closed with path/line/context diagnostics for destructive setup patterns such as `DROP`, `TRUNCATE`, or broad `DELETE`, while still allowing idempotent inserts and upserts.

## Context and Orientation

Relevant current files:

```text
cmd/scenery/psql.go
cmd/scenery/generate.go
internal/app/root.go
docs/local-contract.md
docs/agent-guide.md
docs/app-development-cookbook.md
SKILL.md
README.md
docs/schemas/scenery.config.v1.schema.json
```

Current command behavior:

- `scenery db sync` runs an explicit configured `database.apply` provider and then refreshes configured SQLC artifacts.
- `scenery generate sqlc` reads `sqlc.yaml`, refreshes convention-matched generated schema SQL from Atlas schema sources, and runs `sqlc generate`.
- `dev.setup` can run app-local shell commands before the app process starts.

The new lifecycle should use service DB artifact discovery as the shared source of truth for schema, query, generated schema SQL, and optional seed files. Seed artifacts must be represented as data artifacts.

## Milestones

Milestone 1: Service DB artifact discovery.

Discover service-local DB artifacts for schema sources, query files, generated schema SQL, and optional seed files. Expose the discovery result through a stable JSON surface or generated manifest.

Milestone 2: `scenery db apply`.

Add a database-only schema/apply command. It can use the existing explicit `database.apply` provider in the first implementation, but it must not run seed files or SQLC generation.

Milestone 3: `scenery db seed`.

Add seed discovery and application for `SERVICE/db/seed.sql`. The command must track applied seed identity and fail closed if a previously-applied seed changes.

Milestone 4: `scenery db setup`.

Add the composed setup command that runs apply, then seed. Its output and JSON result should make both substeps visible.

Milestone 5: Dev and app migration.

Wire `scenery dev` to the setup lifecycle for managed dev databases where configured, then migrate ONLV app-local setup to the split commands.

## Plan of Work

Start by making DB artifact discovery explicit, because both SQLC generation and seed setup need to agree on what is schema, what is query input, what is generated output, and what is initial data. Keep discovery independent from command execution so tests can cover fixtures without Postgres, Atlas, or SQLC installed.

Move database mutation first from `db sync` into `db apply`, preserving the existing explicit provider safety posture. Once `db apply` is stable, add `db seed` with a ledgered idempotence model. Compose them in `db setup`, then update `scenery dev` and ONLV migration docs to use setup.

Do not deepen `db sync`. Keep it only as a beta compatibility surface until target apps are migrated.

## Concrete Steps

1. Add service DB artifact discovery and tests.
2. Add CLI parsing, result shape, text output, and tests for `scenery db apply`.
3. Add seed ledger model, seed execution, diagnostics, and tests for `scenery db seed`.
4. Add `scenery db setup` composition and tests.
5. Update task step vocabulary and docs from `db:sync` toward the split lifecycle.
6. Wire `scenery dev` setup behavior to call the new lifecycle where applicable.
7. Migrate ONLV `Justfile` and `.scenery.json` usage.
8. Remove or clearly quarantine remaining new references to `db sync`.

## Validation and Acceptance

For contract and docs changes:

```sh
git diff --check
```

For implementation changes in this plan:

```sh
go test ./...
go install ./cmd/scenery
```

For substantial lifecycle changes:

```sh
scenery harness self --json --write
```

Feature-specific acceptance:

- `scenery db apply --app-root <path>` mutates schema/app setup only.
- `scenery db seed --app-root <path>` applies seed data only.
- `scenery db setup --app-root <path>` runs apply then seed.
- `scenery generate sqlc --app-root <path>` does not mutate a database and does not read seed files as inputs.
- `SERVICE/db/seed.sql` is represented as data, not schema.

## Idempotence and Recovery

`db apply` should be retryable according to the configured provider's own semantics. `db seed` should be idempotent for an unchanged seed file and should fail closed when a previously-applied seed changes. Recovery from destructive local database state should use existing explicit commands such as `scenery db reset`, `scenery db drop`, and `scenery db snapshot restore`, not hidden seed force flags in the first implementation.

## Artifacts and Notes

GitHub issue #21 records the accepted human decision. Dependent implementation slices are tracked in issues #22 through #30.

No generated cache files should be committed for this plan. Do not commit `.scenery/` state or local database artifacts.

## Interfaces and Dependencies

Affected public surfaces:

```text
scenery db apply [--app-root <path>] [--json]
scenery db seed [--app-root <path>] [--json]
scenery db setup [--app-root <path>] [--json]
scenery generate sqlc [--app-root <path>] [--dry-run] [--json]
```

Affected configuration and artifacts:

```text
.scenery.json
database.apply
generators.sqlc
SERVICE/db/schema.hcl
SERVICE/db/queries.sql
SERVICE/db/gen/schema.sql
SERVICE/db/seed.sql
```

The first implementation should avoid adding new external tool requirements beyond the existing configured provider, Postgres client/runtime availability, Atlas, and SQLC where those paths are already relevant.
