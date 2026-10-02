# Observability And Database Query Tracing

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while work proceeds. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Verify local request traces, SQL child spans, structured logs, metrics, export,
querying and optional-backend recovery. Restore automatic SQL instrumentation
through the current database/sql pgx adapter and preserve log context and bound
attributes. An HTTP request that executes SQL must yield a retrievable DB child
span under its request or application span, with query name, redacted SQL,
duration, row count and failure state.

## Progress

- [x] 2026-10-02 Inspected runtime reporting, PostgreSQL opening, OTLP export,
      query interfaces and the current validation catalog.
- [x] 2026-10-02 Confirmed TraceDBQueryStart has no production caller and the
      reporting slog handler ignores context and With attributes.
- [x] 2026-10-02 Restored pgx instrumentation and covered the SDK bridge,
      SQL redaction, child parentage and structured log context/attributes.
- [x] 2026-10-02 Added and passed the real PostgreSQL/Victoria observability
      probe (43.6 s), including owned cleanup.
- [x] 2026-10-02 Passed affected packages, the full verifier, lint, focused
      race checks, real observability round trips and PostgreSQL/Victoria probes.
      Verified assertion inventories and owned cleanup.

## Surprises & Discoveries

- The SQL span implementation survives, but postgresdb.Open uses sql.Open
  without a pgx tracer. Both db.Get and injected SQL use this path.
- PostgreSQL errors can echo bound values even when query text is redacted.
  DB trace errors now retain SQLSTATE and cancellation/deadline classification
  without driver/server message text; the live probe exercises an invalid
  text-to-integer cast with a private argument.
- The default /usr/local/go installation cannot build its own standard library
  (missing-package diagnostics for mldsa and bidi). The existing
  /Users/petrbrazdil/sdk/go1.27.0 toolchain builds the CLI successfully.

## Decision Log

- 2026-10-02, Codex: instrument the shared PostgreSQL connector through the
  existing lightweight appsdk.Host bridge. Do not import the runtime into db
  or postgresdb, wrap sqlc generated code, or add dependencies.
- 2026-10-02, Codex: use isolated probe-owned resources; do not restart an
  installed CLI or another application's runtime.

## Outcomes & Retrospective

Completed locally on 2026-10-02. The pgx database/sql connector now calls the
existing runtime DB hooks through appsdk.Host. SQL child spans preserve the
current request/application parent and sqlc names. SQL text handles PostgreSQL
escaped and dollar-quoted strings, comments, numbers and UTF-8 truncation.
Database errors preserve SQLSTATE without echoing parameter values. Structured
logs retain With attributes, correct group scope and explicit context in both
console and export, including a separate goroutine.

The real final observability journey returned nine spans for trace
`da4d54acf488d838856e9bfb601c180f`, from host PID 37292 / service PID 37264,
build input digest
`sha256:135c2eeafd717bf52f0f2ffc483ee5722ecd36393c1e1685474e91dfd75f4c32`.
It proved named direct/prepared/transaction/error SQL, request/application/DB
parentage, redacted SQL and failing parameters, SQLSTATE 22P02, outgoing HTTP
events, correlated grouped logs, scoped CLI trace lookup and DB duration
metrics. The disposable runtime, Victoria processes and PostgreSQL cluster
were cleaned up. The Victoria lifecycle probe separately proved owner-checked
whole-stack recovery, stale-owner replacement, locking and no restart after
down using its existing process fixtures. The PostgreSQL probe proved full
migration, schema, durable, bootstrap, reset and snapshot journeys with cleanup.

Validation from this worktree, with the existing Go 1.27.0 SDK on PATH:

- `go test ./internal/appsdk ./internal/postgresdb ./db ./runtime ./cmd/scenery ./scripts/verify`: passed; subsequent runtime/verifier edits rechecked by affected tests and the full verifier.
- `go run ./scripts/verify --summary --write`: passed, including the complete `go test ./...`, `go vet ./...`, architecture, contract drift and schema checks. The final source run's cached suite took 4.846 seconds. Existing warnings: 38 overdue documentation reviews and 21 oversized pre-existing files.
- `golangci-lint run ./...`: passed, zero issues.
- `go test -race ./runtime ./internal/postgresdb ./cmd/scenery`: passed.
- `go run ./scripts/verify --probe observability --probe victoria --probe postgres --summary --write`: passed, all probes actually ran and reported cleanup.
- `go run ./scripts/verify --probe observability --summary --write`: passed again after adding the server-error parameter-redaction case (40.6 seconds).
- `git diff --check`: passed.

The refreshed validation union is cli-json-contract, go-package and
release-sensitive-or-runtime. All recommended commands are covered by the
above successful package checks and full verifier. No public schema shape or
generated client changed; compiler/generator regeneration was not applicable.
Benchmarks, an all-root isolated timing audit, release certification, deployment
and another application's UI were not selected. No installed CLI, live user
application, user database or shared system configuration was modified.
Applications must consume this corrected framework and restart to obtain the
fix; unrelated custom database pools remain outside automatic instrumentation.

## Context and Orientation

internal/postgresdb opens database/sql pools used by db.Get, datasource and
framework SQL. runtime/dbtrace.go creates DB reports. runtime/devreport.go
captures logs and outgoing HTTP events. cmd/scenery/dashboard.go accepts
reports and telemetry_export.go batches them into OTLP requests. Victoria
stores the signals; CLI commands expose scoped querying. Existing victoria
probe proves lifecycle using process fixtures, not real signal round trips.

## Milestones

1. Restore SQL tracing without widening the public app API.
2. Correct structured log context/attributes and prove signal round trips.
3. Complete required validation and document explicit limitations.

## Plan of Work

Attach a pgx QueryTracer to the stdlib connector, bridge its callbacks through
appsdk.Host, and use the existing runtime DB report format. Cover successful,
failed, prepared and transaction queries and context parentage. Correct slog
WithAttrs/WithGroup handling and context-first request selection. Inspect
SQL normalization before enabling collection. Exercise real SQL and Victoria
through a named observability probe with explicit assertion and cleanup proof.

## Concrete Steps

Run all commands at the repository root with the working Go 1.27.0 SDK on
PATH. Run affected package tests, then the selected full verifier and lint.
Use the prepared .scenery/harness/bin/scenery for product operations.

## Validation and Acceptance

Expected validation classes are Go packages, runtime and repository verifier.
Run `go test ./internal/appsdk ./internal/postgresdb ./db ./runtime ./cmd/scenery ./scripts/verify`.
Run `go run ./scripts/verify --summary --write`, inspect the refreshed
changed_area recommended_commands and fulfill their union. This includes the
complete repository Go suite and vet. Run `golangci-lint run ./...`.
Run `go run ./scripts/verify --probe observability --probe victoria --probe postgres --summary --write`.
The observability probe must verify actual exported traces, redacted query
events, correlated structured logs, metrics, current serving identity and
owned cleanup. Missing external tools fail the proof, never count as a pass.
Compiler/generator regeneration is not required unless those sources change.
Benchmarks, isolated timing audits and full release certification are not
selected by this functional request.

## Idempotence and Recovery

Probe resources use disposable roots and homes. Always stop the owned runtime
and remove only its owned database resources before removing the temporary
root. Leave unrelated worktrees and running applications untouched. A failed
probe retains diagnostics needed to identify the failed boundary.

## Artifacts and Notes

Validation output is retained beneath .scenery/harness, which is ignored:
observability-full.json/log, observability-probes.json/log,
observability-redaction-probe.json/log, observability-focused-final.log,
observability-lint.log and observability-race.log.
Initial worktree was clean. The installed CLI is not changed by this work.

## Interfaces and Dependencies

Keep public SQL capability types and existing telemetry schemas unchanged.
Use the already pinned pgx v5 and existing appsdk.Host runtime bridge. Keep
real services and processes in the explicit scripts/verify probe catalog.
