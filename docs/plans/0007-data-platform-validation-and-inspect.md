# Data Platform Validation and Inspect

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The first onlava data-platform slice exists, but its most important PostgreSQL-backed integration tests need to run continuously, not as an optional manual path. Before adding product surface, dashboard UI, reporting, indexing, or trigger-backed outbox, make the data platform trustworthy infrastructure.

This plan closes the biggest confidence gap by adding PostgreSQL validation in CI and a narrow inspection surface:

```text
real PostgreSQL in CI
        |
        v
testcontainers-backed PostgreSQL integration tests
        |
        v
idempotence and cleanup guarantees
        |
        v
onlava inspect data --json
        |
        v
stable docs/schema for agents and humans
```

The inspection command should report metadata, physical schema state, migration state, and outbox state. It must not dump user records. The goal is to answer debugging questions such as: did metadata bootstrap, did object creation create the expected table, did field creation create expected columns, are migrations stuck, what is the latest outbox sequence, and is `schema_version` where expected.

## Progress

- [x] (2026-05-08 21:08Z) Created this ExecPlan and assigned historical ID 0007.
- [x] (2026-05-08 21:08Z) Based scope on follow-up guidance after completing the first data-platform vertical slice in `0005-onlava-data-platform.md`.
- [x] (2026-05-08 21:42Z) Add CI DB-backed test coverage.
- [x] (2026-05-08 21:42Z) Make data-platform integration tests idempotent and safe to rerun against the same database.
- [x] (2026-05-08 21:42Z) Add `onlava inspect data --json` with explicit database URL support.
- [x] (2026-05-08 21:42Z) Add schema/docs for the inspect output and local DB test setup.
- [x] (2026-05-08 22:05Z) Validate and update Outcomes & Retrospective.
- [x] (2026-05-08 22:20Z) Replace service-container-only DB validation with `testcontainers-go` integration tests.

## Surprises & Discoveries

- Go module boundaries mean `go test ./testdata/...` from the repository root matches no packages because `testdata/apps/data-platform` is its own module. The CI job now runs the DB-backed objectstore package tests directly and separately runs `onlava check --app-root testdata/apps/data-platform --json` to validate the fixture app.
- `onlava inspect data` needs to be safe against empty databases. It returns a valid JSON document with warnings when the onlava data schemas/tables are absent instead of bootstrapping or failing.
- Real PostgreSQL validation caught two issues the skipped test path had hidden: test cleanup was registered after the pool close, and the SSE test subscribed from seq 0 so replayed old metadata outbox rows before the `ready` event. Cleanup now keeps the pool alive, and the SSE test uses the record query watermark it means to subscribe from.
- Empty inspect collections initially rendered as `null`; they now render as stable empty arrays.

## Decision Log

- Decision: Prioritize CI-backed PostgreSQL validation before any additional product features.
  Rationale: The first slice's DB integration tests are present but skipped by default. Until they run in CI, real DDL, transactions, outbox rows, and SSE replay remain promising but under-proven.
  Date/Author: 2026-05-08 / Codex

- Decision: Add data inspection before dashboard/UI.
  Rationale: onlava's local development value depends on stable, machine-readable inspectability. A CLI JSON command gives humans and agents a reliable debugging surface without building UI prematurely.
  Date/Author: 2026-05-08 / Codex

- Decision: Prefer `onlava inspect data --json --database-url <url>` as the first inspect path.
  Rationale: The CLI cannot always discover a user app's runtime database connection reliably from app config. An explicit database URL keeps the command simple and testable. App-root discovery can be added later.
  Date/Author: 2026-05-08 / Codex

- Decision: Inspection output must not include user records.
  Rationale: The command is infrastructure introspection, not a data export. Avoiding records reduces privacy risk and keeps output bounded.
  Date/Author: 2026-05-08 / Codex

- Decision: Validate the fixture app with `onlava check` instead of root-level `go test ./testdata/...`.
  Rationale: The data-platform fixture is an independent example module. Root-level Go package patterns intentionally do not cross module boundaries, so `onlava check` is the stable repository-level validation for that app.
  Date/Author: 2026-05-08 / Codex

- Decision: Use `testcontainers-go` for PostgreSQL-backed data-platform tests.
  Rationale: The tests now own their PostgreSQL lifecycle, so local and CI `go test` run the same DB-backed path without a separate GitHub Actions service container or silent skip. `ONLAVA_TEST_DATABASE_URL` remains as an override for developers who want to reuse an existing database.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed on 2026-05-08. Revised on 2026-05-08 22:20Z to use `testcontainers-go` for PostgreSQL integration tests.

Shipped:

- Added `testcontainers-go` PostgreSQL startup for DB-backed objectstore and data-inspect tests, with `ONLAVA_TEST_DATABASE_URL` retained as an override.
- Folded data-platform validation back into the normal Go CI job instead of a separate PostgreSQL service job.
- Made PostgreSQL integration cleanup safe for reruns against the same database by using unique tenant keys and test-owned table cleanup before closing the pool.
- Added `internal/datainspect` and `onlava inspect data --json --database-url <postgres-url> [--tenant <key>] [--object <name>]`.
- Added `docs/schemas/onlava.inspect.data.v1.schema.json`, self-harness schema tracking, local-contract docs, docs index entry, and the data-platform fixture README.
- Verified the fixture app with `onlava check --app-root testdata/apps/data-platform --json`.

Validation run:

```sh
go test ./internal/datainspect ./cmd/onlava ./internal/objectstore
go test ./...
go run ./cmd/onlava check --app-root testdata/apps/data-platform --json
go test ./internal/objectstore -count=1
go test ./internal/objectstore -count=1
go install ./cmd/onlava
onlava harness self --json --write
```

PostgreSQL validation now uses temporary `postgres:17-alpine` testcontainers and removes them after the run.

Follow-up remains `0008-data-platform-migration-and-live-hardening.md`.

## Context and Orientation

This plan continues the completed first data-platform slice in `docs/plans/0005-onlava-data-platform.md`.

Relevant files in the onlava repository:

- `internal/objectstore/*`: metadata bootstrap, migrations, query compiler, mutation layer, outbox, live routing, and SSE.
- `data/data.go`: public facade currently exposing aliases around `internal/objectstore`.
- `internal/objectstore/objectstore_integration_test.go`: PostgreSQL integration test backed by `testcontainers-go`, with `ONLAVA_TEST_DATABASE_URL` override support.
- `testdata/apps/data-platform`: fixture app proving ordinary onlava services can use the data platform.
- `.github/workflows/ci.yml`: current CI workflow. It runs `go test ./...`, data-platform fixture checks, UI checks, and self-harness.
- `internal/inspect/inspect.go`: existing inspect command response types.
- `cmd/onlava`: CLI command wiring for inspect subcommands.
- `docs/schemas`: JSON schema contracts for inspect and harness output.
- `docs/local-contract.md`: stable/beta local command contract.
- `PLANS.md`: ExecPlan requirements and validation expectations.

The existing CI workflow uses GitHub Actions on Ubuntu with Go and Bun. DB-backed data-platform tests now run inside the regular Go job through `testcontainers-go`.

Terminology:

- `onlava_data`: metadata/outbox schema from the first slice.
- `onlava_data_records`: physical dynamic object table schema from the first slice.
- Data tenant: runtime data isolation unit. Do not call it a workspace in new code.
- Outbox sequence: monotonically increasing `seq` from `onlava_data.outbox_events`.
- Migration state: rows in `onlava_data.schema_migrations`, grouped by status.

## Milestones

Milestone 1: CI PostgreSQL integration.

Run PostgreSQL coverage in CI through `testcontainers-go`. `ONLAVA_TEST_DATABASE_URL` remains available as an override for developers or CI experiments that want an existing database. Run a focused command such as:

```sh
go test ./internal/objectstore -count=1
go run ./cmd/onlava check --app-root testdata/apps/data-platform --json
```

This milestone is complete when the job passes on CI and fails if the integration test fails.

Milestone 2: Idempotent DB-backed tests.

Make the data-platform integration test safe to run twice against the same database. Use unique tenant/object names per run and cleanup that does not hide original failures. Add an explicit test path that calls bootstrap/object/field creation repeatedly and verifies no duplicate schema or outbox corruption.

This milestone is complete when the CI job can run the focused DB command twice in the same job against the same PostgreSQL database.

Milestone 3: `onlava inspect data --json`.

Add a narrow inspect command. Initial shape:

```sh
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL"
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL" --tenant <tenant-key>
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL" --tenant <tenant-key> --object <object>
```

The output must include metadata schema names, tenants, objects, fields with physical columns, migration counts/latest rows, and outbox summary. It must not include user records.

Milestone 4: Schema and docs.

Add `docs/schemas/onlava.inspect.data.v1.schema.json`, tests for JSON shape, and documentation in `docs/local-contract.md`. Add `testdata/apps/data-platform/README.md` with local PostgreSQL setup, fixture run commands, and sample curl calls.

Milestone 5: Final validation.

Run the acceptance commands, update this ExecPlan, and leave `0008-data-platform-migration-and-live-hardening.md` as the next data-platform follow-up.

## Plan of Work

Start with CI, not the inspect command. The inspect command is useful only if the data it inspects is already proven by real PostgreSQL tests.

Update `.github/workflows/ci.yml` so the regular Go job runs DB-backed data-platform tests through `testcontainers-go`. Avoid a separate service-container job unless the containerized test path proves unreliable in CI.

Then update the integration tests. The current test should be examined for fixed tenant keys or object names. Replace fixed names with unique values based on timestamp or random suffix. Cleanup should target only those unique tenants and physical tables. If cleanup fails after a test failure, report both errors without masking the original failure.

Make the DB-backed test run twice in CI. A simple CI sequence is:

```sh
go test ./internal/objectstore -count=1
go test ./internal/objectstore -count=1
go run ./cmd/onlava check --app-root testdata/apps/data-platform --json
```

After that, add inspect. Prefer a small internal package for data inspect rendering so it can be unit tested without CLI plumbing. The CLI command should open a pgx pool using `--database-url`, read metadata and PostgreSQL catalogs, and render JSON. If the data schemas do not exist, the command should return valid JSON with empty summaries and a clear status, not panic.

The inspect output should be intentionally boring. Suggested shape:

```json
{
  "schema_version": "onlava.inspect.data.v1",
  "schemas": {
    "metadata": "onlava_data",
    "records": "onlava_data_records"
  },
  "tenants": [
    {
      "id": "...",
      "key": "fixture-test-...",
      "objects": 1,
      "latest_outbox_seq": 42
    }
  ],
  "objects": [
    {
      "id": "...",
      "tenant_key": "fixture-test-...",
      "name": "company",
      "physical_table": "...",
      "schema_version": 7,
      "fields": [
        {
          "name": "name",
          "type": "text",
          "columns": ["name"]
        }
      ]
    }
  ],
  "migrations": {
    "pending": 0,
    "failed": 0,
    "latest": []
  },
  "outbox": {
    "latest_seq": 42,
    "unpublished": 0
  }
}
```

Do not add dashboard UI in this plan. Do not add trigger-backed outbox or index metadata. This plan is about proof and inspectability.

## Concrete Steps

1. Read `.github/workflows/ci.yml`, `internal/objectstore/objectstore_integration_test.go`, `internal/objectstore/metadata.go`, `internal/objectstore/migrate.go`, `internal/objectstore/outbox.go`, `internal/inspect/inspect.go`, and `cmd/onlava` inspect command wiring.
2. Add CI PostgreSQL coverage through `testcontainers-go`, retaining `ONLAVA_TEST_DATABASE_URL` as an override.
3. In that job, run the focused DB integration command twice against the same database.
4. Update integration tests to use unique tenant/object keys and reliable cleanup.
5. Add local documentation for `ONLAVA_TEST_DATABASE_URL`.
6. Add an internal inspect-data renderer and tests.
7. Add CLI wiring for `onlava inspect data --json --database-url ...`.
8. Add `--tenant` and `--object` filters.
9. Add `docs/schemas/onlava.inspect.data.v1.schema.json`.
10. Update docs and fixture README.
11. Run final validation commands and update this plan.

## Validation and Acceptance

Required local validation:

```sh
go test ./...
go test ./internal/objectstore -count=1
go test ./internal/objectstore -count=1
go run ./cmd/onlava check --app-root testdata/apps/data-platform --json
go install ./cmd/onlava
onlava harness self --json --write
```

Inspect validation:

```sh
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL"
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL" --tenant fixture-test-...
onlava inspect data --json --database-url "$ONLAVA_TEST_DATABASE_URL" --tenant fixture-test-... --object company
```

CI acceptance:

- CI runs DB-backed data-platform tests through `testcontainers-go`.
- The command fails on real integration failures and does not silently skip due to an unset database URL.

Feature acceptance:

- `onlava inspect data --json` reports tenants, objects, fields, migrations, and outbox state.
- Inspect output includes physical table/column names for debugging but does not dump user records.
- A JSON schema exists under `docs/schemas`.
- `docs/local-contract.md` documents the command after the shape is stable.
- `testdata/apps/data-platform/README.md` includes local setup and sample calls.

## Idempotence and Recovery

The testcontainer path should use a fresh database by default. Tests must still be idempotent because developers can set `ONLAVA_TEST_DATABASE_URL` to a shared local database. Unique tenant keys and physical names prevent accidental collision with previous failed runs.

Cleanup should be best-effort and narrowly scoped to the generated tenant/object names. Do not drop entire shared schemas in local tests unless the test created a dedicated temporary database. If cleanup fails, report it with `t.Cleanup` or joined errors while preserving the original test failure.

If `onlava inspect data` is pointed at a database without onlava data schemas, return a valid empty inspect document with a warning field if needed. Do not bootstrap schemas just because inspect was called.

If CI PostgreSQL startup flakes, prefer adjusting the testcontainer wait strategy before adding sleeps or restoring a dedicated service-container job.

## Artifacts and Notes

Expected changed files:

- `.github/workflows/ci.yml`
- `internal/objectstore/objectstore_integration_test.go`
- `internal/inspect` or a new `internal/datainspect` package
- `cmd/onlava` inspect command wiring
- `docs/schemas/onlava.inspect.data.v1.schema.json`
- `docs/local-contract.md`
- `testdata/apps/data-platform/README.md`
- Tests for inspect output and DB idempotence

Do not add:

- Dashboard UI
- Reporting
- Dynamic GraphQL
- Trigger-backed outbox
- Index metadata
- New data directives

## Interfaces and Dependencies

CLI interface:

```sh
onlava inspect data --json --database-url <postgres-url>
onlava inspect data --json --database-url <postgres-url> --tenant <tenant-key>
onlava inspect data --json --database-url <postgres-url> --tenant <tenant-key> --object <object-name>
```

JSON schema:

```text
docs/schemas/onlava.inspect.data.v1.schema.json
```

Environment variable:

```text
ONLAVA_TEST_DATABASE_URL
```

The only added dependency is `testcontainers-go` for integration tests. It is not imported by production binaries. PostgreSQL access still uses existing pgx/pgxpool.
