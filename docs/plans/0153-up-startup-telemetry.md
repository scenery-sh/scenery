# `scenery up` Startup Telemetry

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

`scenery telemetry` currently records an attached `scenery up` invocation only
when its supervisor exits. That duration is process lifetime, not startup
latency, so startup p50/p95 cannot be measured reliably. This plan makes
startup a distinct timing measurement written as soon as a newly owned runtime
reaches readiness, while keeping the running application under the same
supervisor.

The intended query is:

    scenery telemetry --app <id-or-name> --command up --measurement startup -o json

It reports bounded-sample p50/p95 over actual time-to-ready records. Detached
launcher processes, already-running attachment, and eventual supervisor exit
must not create duplicate startup samples.

## Progress

- [x] (2026-08-12) Traced global CLI timing, attached and detached `up`
  ownership, the supervisor readiness boundary, and telemetry query/schema
  contracts.
- [x] (2026-08-12) Registered ExecPlan 0153 in active plan and knowledge
  indexes.
- [x] (2026-08-12) Write exactly one startup measurement when a newly owned runtime becomes
  ready, including detached-child ownership and failure behavior.
- [x] (2026-08-12) Add measurement filtering/grouping and bounded recent-sample p50/p95.
- [x] (2026-08-12) Update schema, help, local contract, and agent guide.
- [x] (2026-08-12) Run focused, package, repository, self-harness, and real-runtime proof.

## Surprises & Discoveries

- Observation: the existing `runConsole.Banner` call occurs after shared
  startup dependencies, the application listener, and configured frontend
  readiness have succeeded. It is therefore the existing internal readiness
  boundary and does not require another probe framework.
  Evidence: `cmd/scenery/dev_supervisor.go` and `cmd/scenery/watch.go`.

- Observation: `scenery up --detach` uses a short-lived launcher plus a detached
  child that owns the supervisor. Recording both processes would duplicate one
  startup, while an already-running result is acquisition rather than startup.
  Evidence: `cmd/scenery/dev_detach.go`.

- Observation: a real detached fixture startup wrote its owner record at
  2,773 ms, its published route returned HTTP 200, and querying again after
  `scenery down` still returned exactly one startup sample.
  Evidence: temporary app `telemetry-startup-proof-0153` and the filtered
  `scenery telemetry` JSON captured during final acceptance.

## Decision Log

- Decision: Record startup at the existing owner-process readiness callback,
  not at command exit and not by retrying a consumer endpoint.
  Rationale: this measures the lifecycle transition directly and remains valid
  for attached sessions that run indefinitely.
  Date/Author: 2026-08-12 / Codex.

- Decision: Keep completion as the default measurement and mark startup records
  explicitly; historical completion records are not reinterpreted as startup.
  Rationale: old `up` lifetimes must never contaminate startup percentiles.
  Date/Author: 2026-08-12 / Codex.

- Decision: Calculate exact p50/p95 over the latest bounded 10,000 matching
  records and expose the sample count separately from all-history counters.
  Rationale: quantiles become useful without making telemetry query memory grow
  with the append-only store.
  Date/Author: 2026-08-12 / Codex.

## Context and Orientation

`cmd/scenery/main.go` currently measures every CLI invocation until `run`
returns. `cmd/scenery/telemetry.go` owns the private JSONL record and writer.
`cmd/scenery/watch.go` orchestrates the live `up` owner, and
`cmd/scenery/dev_supervisor.go` calls the run-console banner after initial
readiness. `cmd/scenery/dev_detach.go` owns the launcher/child split.

The supported read surface is `cmd/scenery/telemetry_command.go`; its exact JSON
shape is checked by `docs/schemas/scenery.telemetry.schema.json` and the static
payload revision in `cmd/scenery/payload_identity.go`.

## Milestones

1. Separate completion timing from `up` startup timing.
2. Record readiness once from the actual supervisor owner.
3. Expose filtered measurement groups and bounded p50/p95.
4. Prove attached/detached semantics in tests and a real detached app startup.

## Plan of Work

Introduce one per-invocation telemetry object used only by the real CLI entry
path. Ordinary commands write completion on return. `up` writes startup through
an injected readiness callback and suppresses completion; detached launchers
also suppress their own record because the child is the owner. Add an optional
record measurement where omission means completion and `startup` identifies
the new sample. Extend querying with `--measurement`, measurement groups, and a
10,000-record percentile ring used to populate sample count, p50, and p95.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./cmd/scenery -run 'Test(Telemetry|CLITelemetry|UpCommand)'
    go test ./cmd/scenery
    go test ./...
    go build -o .scenery/harness/bin/scenery ./cmd/scenery
    .scenery/harness/bin/scenery harness self --summary --write

For runtime proof, run the worktree-local binary with an isolated telemetry
home against a real app using `up --detach --wait ready`, query
`--measurement startup`, verify one startup record and its p50/p95, then stop
the disposable runtime.

## Validation and Acceptance

Acceptance requires all of the following:

- attached `up` writes startup while the supervisor is still running;
- detached startup produces one owner record, not launcher plus child records;
- already-running acquisition produces no startup record;
- shutdown after readiness produces no second record;
- completion and historical records never enter startup-filtered aggregates;
- p50/p95 use a visibly bounded sample count;
- focused tests, `go test ./cmd/scenery`, `go test ./...`, schema validation,
  knowledge validation, full self-harness, and real runtime proof pass.

## Idempotence and Recovery

Recording is best effort and append-only. The per-process readiness callback is
once-only, so rebuilds and shutdown cannot duplicate startup. Querying is
read-only. A failed telemetry write never changes runtime behavior. The raw
store remains disposable machine-local state.

## Artifacts and Notes

Durable artifacts are the telemetry schema and payload revision, current CLI
contract docs, focused tests, and final harness evidence under `.scenery/`.
Runtime proof may use temporary files outside the repository and must not
commit app state.

## Interfaces and Dependencies

No dependency or environment-variable surface is added. The implementation
uses the Go standard library, the existing `up` readiness boundary, app
identity discovery, JSONL writer, and CLI schema machinery.

## Outcomes & Retrospective

Completed on 2026-08-12. Ordinary commands still record completion, while a
newly owned `scenery up` process now records `measurement: "startup"`
immediately after the existing readiness boundary and never records its later
supervisor lifetime. Detached launchers stay silent so their owner child is the
single sample; already-running acquisition and successful exit without a new
readiness transition do not create startup records.

`scenery telemetry` now accepts repeatable `--measurement
completion|startup`, reports per-measurement groups, and exposes exact p50/p95
over the latest at most 10,000 matching records with an explicit sample count.
Historical completion records are not reclassified, so old `up` lifetimes do
not contaminate startup-filtered results.

Focused lifecycle/query/schema tests, `go test ./cmd/scenery`, `go test ./...`,
and the complete 22-step self-harness passed under the cached-test policy. The
real detached proof returned HTTP 200 and one 2,773 ms startup sample; the
post-shutdown query remained at count one with p50/p95 2,773 ms. No dependency,
environment variable, app process, or frontend lifecycle changed.
