# Runtime Resource Efficiency

This ExecPlan is a living document. Progress, Surprises & Discoveries, Decision
Log, and Outcomes & Retrospective must be kept up to date as work proceeds.

## Purpose / Big Picture

Reduce Scenery's CPU work, retained memory and latency in the development and
application runtimes. A source review at `a1fd9485356a` found notification
listeners consuming the durable SQL query pool, abandoned job subscriptions,
repeated service-wide recovery, repeated page serialization, disposable gzip
compressors, repeated request-state stack lookups, duplicate source buffers,
history pruning that retains discarded payloads, individual development report
requests, and full table rendering during inline expansion. The human authorized
implementation of all these areas on 2026-09-30, including the report protocol
and UI. Comparable native measurements below establish the benefits and the
remaining allocation tradeoffs for the selected workloads.

## Progress

- [x] 2026-09-30: Reviewed the current source and created an isolated worktree
  at `/Users/petrbrazdil/.codex/worktrees/performance-review/scenery` on
  `perf/runtime-resource-efficiency`.
- [x] Implement shared notifications outside the SQL pool and bounded
  subscription lifetimes, including real PostgreSQL acceptance.
- [x] Reduce idle durable maintenance without delaying retries or recovery.
- [x] Make storage page sizing linear and release pruned history payloads.
- [x] Reuse compression state and avoid repeated logger state lookups.
- [x] Remove redundant captured-input copies while preserving immutability.
- [x] Batch and byte-bound runtime reporting with matching intake and docs.
- [x] Preserve table windowing during inline expansion and verify a served UI.
- [x] Run authorized native before/after benchmarks and retained-heap samples.
- [x] Complete the final changed-area validation union and record outcomes.

## Surprises & Discoveries

The notification change shares a ten-connection SQL pool across service views,
but each service's listener permanently checks out one connection. Ten active
listeners can leave no query capacity. Subscription keys disappear only when a
matching notification arrives; a waiter observing an already finished job
creates a key that may never receive another notification. Both mechanisms are
visible without a load measurement.

The main checkout is clean at the starting revision. The host has approximately
18 GiB free; keep artifacts bounded and reuse Go's cache. Historical plans and
measurements are context, not current performance evidence.

Encoding each discarded history item separately reduced bytes and time but
raised allocations to 11,776 per pruning operation. Encoding the bounded
discarded chunk instead reduces this to 71 allocations, while preserving exact
array, comma and optional-field accounting. The final pruning benchmark uses
the latter implementation.

Browser acceptance exposed a selected row inside overscan but below the visible
viewport after a tall detail. Pixel-based reveal fixes this case. Collapsing a
2,033-pixel detail above the viewport preserves the selected row's screen
position exactly in the served fixture. Ordinary rows retain the existing
44-pixel estimate; this change measures the variable inline detail only.

## Decision Log

- 2026-09-30, Codex: Preserve the single current protocol and resource ownership
  rules. No compatibility decoder, tuning environment variable, dependency or
  global installation is part of this work.
- 2026-09-30, Codex: Work in the isolated worktree, preserving all other
  worktrees and the primary checkout. Ask separately for explicitly restricted
  delegation and benchmark selection; functional implementation continues.
- 2026-09-30, Codex: Select full repository verification because runtime, storage,
  build, UI and report behavior are cumulative validation classes. Do not run a
  quick verifier before the required full run.
- 2026-09-30, Petr: Work without subagents. Run targeted native before/after
  benchmarks. No all-root timing audit or release certification was requested.

The scoped recovery query keeps expiration and admission in one transaction,
without adding a maintenance goroutine or shared timing state. Empty local
acquisitions select the next known retry/lease/timeout deadline, capped at ten
seconds for missed notifications; notification reconnects explicitly wake
observers. Result waiters and error retries retain a one-second fallback.

## Outcomes & Retrospective

Completed on 2026-09-30 in the isolated worktree. All ten reviewed mechanisms
are addressed, targeted native measurements are recorded below, and the
changed-area validation union passes with the existing repository warnings and
the advisory aggregate test-suite timing warning. Changes are uncommitted;
the primary checkout and installed executable were preserved.

Validation evidence:

- `go test ./internal/durable/store ./runtime ./internal/storagefs
  ./internal/devdash ./internal/build ./cmd/scenery ./internal/generate
  ./internal/machine ./internal/devreport ./scripts/verify`: passed through
  focused runs and the final full verifier's repository-wide `go test -json
  ./...`, which satisfies the same package checks for the final source inputs.
- `go test -race ./internal/durable/store ./runtime ./internal/devdash`: passed
  after the final history refinement; logs in `perf-final-race.log`.
- `golangci-lint run ./...`: passed, zero issues; `perf-final-lint.log`.
- Both required `go run ./cmd/scenery generate --target
  typescript_client.public_api --app-root
  internal/compiler/testdata/{native,house} -o json` invocations: passed.
  The owning assistant fixture's generation check also passed.
- `tools/typescript/node_modules/.bin/tsc -p
  internal/generate/testdata/tsconfig.catalog.json`: passed, including the final
  `ui` probe; TypeScript client conformance and typecheck also passed.
- `bun test internal/generate/testdata/query_table_perf.test.tsx
  internal/generate/testdata/query_table_regressions.test.tsx`: 18 passed.
- Generated catalog consumer: `tsc -p tsconfig.consumer.json`, production Vite
  build, generation `--check`, and native Chrome interaction all passed. The
  fixture and screenshot are under `.scenery/harness/perf-ui-app` and
  `.scenery/harness/perf-ui-acceptance.jpg`.
- `go run ./scripts/verify --probe postgres --probe storage --probe
  process-model --probe ui --summary --write`: all selected probes passed;
  `perf-final-probes.json` preserves assertion and cleanup evidence. PostgreSQL
  reports twelve services, one listener, zero query-pool slots occupied, a
  4,998 ms future wake, isolated task recovery to attempt two, and zero listeners
  after close. Storage reports process and resource cleanup passed; the process
  model proves replacement, generation retention and served identity.
- `go run ./scripts/verify --summary --write`: passed with warnings. Contract
  drift, Go suite, vet and schema validation pass. The 37 knowledge freshness
  warnings and 19 source-size warnings remain; no changed hotspot is over the
  source-size warning threshold. The aggregate Go suite took 8.021 s against
  the advisory 5 s budget. Non-isolated concurrent root timings do not establish
  repeated isolated p95; an all-root timing audit was not selected.

The final classes are CLI JSON contract, compiler/generator, Go package,
release-sensitive runtime and UI catalog. The recommended standalone
`scenery logs --limit 500 -o jsonl` is not applicable to the framework checkout,
which has no running target app; the explicit runtime probes capture their own
application output. Full release certification and power-loss simulation were
not requested. Documentation and the current machine identity were updated for
the changed notification, reporting and UI contracts; no instruction layer,
public app model or persisted durable input contract changed.

Native measurements ran on Apple M2 Ultra, macOS 27.0 (26A428), arm64,
Go 1.27.0 and Bun 1.3.14. Baseline is the detached starting revision; candidate
is this worktree. Go uses one CPU, 200 ms per sample and eight samples per
revision, with ordinary tests excluded. Baseline/candidate rounds alternated;
after the bounded-chunk refinement, the final candidate was measured again.
The table reports medians. These are CPU-bound in-process operation times,
not whole-app CPU utilization, RSS or network latency.

| Workload | Before | After | Time change | Allocated bytes before / after | Allocation count before / after |
| --- | ---: | ---: | ---: | ---: | ---: |
| Storage page assembly | 18.925 ms | 0.380 ms | -98.0% | 10,850,897 / 356,662 | 2,084 / 2,644 |
| History budget pruning | 34.842 ms | 9.767 ms | -72.0% | 82,076,245 / 40,486,014 | 40.5 / 71 |
| Candidate snapshot | 0.740 ms | 0.297 ms | -59.8% | 6,606,776 / 1,701,888 | 422 / 131 |
| Buffered gzip response | 300.679 us | 45.193 us | -85.0% | 1,077,224 / 1,224 | 30 / 16 |
| Contextual console logger | 9.437 us | 1.844 us | -80.5% | 7,136 / 6,928 | 13 / 10 |
| Admission and posting of 64 reports | 158.878 us | 120.817 us | -24.0% | 163,336 / 184,222 | 1,472 / 229 |

Storage inputs are 501 descriptors, a 500-row cap and a 64 KiB byte cap; both
implementations return the same 291 objects in 65,318 encoded bytes. The
baseline benchmark extracts the original embedded assembly loop verbatim;
filesystem scanning is excluded. Pruning starts with 4,096 output records with
896-byte payloads, including identical initial encoding and slice-copy cost,
and reduces to 256 KiB. Snapshot inputs have 100 distinct 16 KiB buffers shared
by four overlapping views. Gzip compresses 31,744 bytes of repeated JSON with
the same level and content. Logging uses an explicit enabled request context,
one attribute and a discarded console destination. Report posting uses an
in-process transport that consumes the request body; actual network and
supervisor persistence are excluded. Its HTTP requests fall from 64 to one.

Storage and pruning allocate more small objects but substantially fewer bytes
and take less time. Report encoding allocates 12.8% more bytes because admission
owns immutable encoded records and the outgoing batch; its queue and in-flight
reservations are now bounded to 4 MiB. This tradeoff is retained for bounded
memory, fewer requests and reduced CPU work. No percentage reduction in total
application memory is inferred from B/op.

Eight retained-heap samples after GC keep a one-row tail of 10,001 unique
1 KiB outputs alive. Heap delta falls from 11,444,624 to 1,204,624 bytes; the
remaining slice backing array is retained, but discarded payloads are freed.
After 10,000 completed subscription observations without a later notification,
retained keys fall from 10,000 to zero and heap delta from 1,714,648 to 48 bytes
in this isolated fixture. Small heap deltas are subject to runtime noise.

Twenty-four React Profiler samples per revision use the existing mocked Astryx
table under React test renderer with 10,000 rows and an open first detail.
Median mount falls from 38.091 to 0.450 ms; selection update from 32.662 to
0.261 ms. This isolates React rendering and excludes browser layout and paint.
Independent browser proof uses the actual generated Astryx/StyleX consumer:
33 visible data rows initially, nine with a 2,033-pixel detail, and 25 after
keyboard navigation beyond a 1,233-pixel detail; selection remains visible.
Grouped context and collapse anchoring also pass.

## Context and Orientation

`internal/durable/store` owns PostgreSQL persistence and notification delivery;
`runtime/durable.go` owns acquisition and waiting. Service views share their
base store's database. `internal/storagefs/list.go` owns bounded metadata pages
and opaque, scope-bound cursors. `internal/devdash/store.go` owns the small JSON
session and diagnostic store. `runtime/gzip.go`, `contract_http.go` and
`server.go` compress HTTP responses. Runtime and console log handlers look up
request state through `runtime/current.go`. Captured build input is produced in
`cmd/scenery/watch_*` and consumed by `internal/build`; it must remain immutable
and content verified. `runtime/devreport.go` sends reports to
`cmd/scenery/dashboard.go`; `telemetry_export.go` already batches the subsequent
backend export. `ui/components/DataTable.tsx` and `table-window.ts` own windowed
rendering. The report and UI contracts live in `docs/local-contract.md` and
`docs/ui-agent-contract.md`.

## Milestones

1. Durable notifications retain query capacity with more than ten services and
   subscription memory follows active waiters. Maintenance scans only the acquiring task, rather than rescanning every
   running job in the service from each task loop.
2. Storage, history, compression, logging and snapshot changes remove repeated
   work without weakening byte budgets, identity or lifetime isolation.
3. Current runtime report producers and intake agree on bounded batches;
   overload remains nonblocking and accounted. Expanded tables retain a bounded
   viewport, correct spacing, grouping and keyboard navigation.
4. Required validation passes on the final inputs. Authorized performance
   measurements distinguish CPU, allocation, retained heap and end-to-end time.

## Plan of Work

Use one dedicated notification connection per base store family, listening on
all registered service channels, with cancellation and reconnection on channel
membership changes. Subscribers release their registration on all exit paths;
concurrent subscribers to the same key cannot remove one another. Cover the
state machine in process, then prove actual PostgreSQL query capacity and
notification delivery through the postgres probe.

Scope expired-job recovery to the acquiring task, retaining
deadline-driven progress for delayed retries and expired leases plus a bounded
reconciliation path for missed notifications. Do not weaken atomic SQL
concurrency admission. Count exact encoded page bytes using individually
encoded entries and the cursor. Clear discarded slice slots, avoid repeated
whole-state encoding during pruning, and preserve deferred mutation replay.

Reuse gzip writers while bounding idle retention and resetting destination
references. Resolve logger state once through context before the context-free
fallback. Adopt fresh read buffers where callers do not mutate them, retaining
defensive copies at mutable boundaries and exact content validation.

Bound development reports by bytes before queue admission, drain ready records
into bounded batches, and update intake and its current contract together.
Preserve authentication, record ordering, redaction and overload counts. Extend
table offsets with the measured expanded row height rather than rendering the
whole list; preserve resize observation, grouping and selection behavior.

## Concrete Steps

All commands run from the isolated worktree above. Apply focused changes and
run affected cached tests during implementation. Provision TypeScript tools
with `bun install --frozen-lockfile` from `tools/typescript` before generation
or UI typechecking. Use the worktree-local product executable after verification
builds it, never the installed executable. Keep evidence under ignored
`.scenery/harness/`; use native macOS only.

## Validation and Acceptance

Run affected suites: `go test ./internal/durable/store ./runtime
./internal/storagefs ./internal/devdash ./internal/build ./cmd/scenery`.
Concurrency changes also require `go test -race ./internal/durable/store
./runtime ./internal/devdash`. Ordinary roots remain bounded in-process tests;
real services and toolchains belong in selected probes.

Run `tools/typescript/node_modules/.bin/tsc -p
internal/generate/testdata/tsconfig.catalog.json`, `go test ./internal/generate`,
and regenerate both committed clients with:

```sh
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
```

Run `go run ./scripts/verify --summary --write`, inspect its final
`.scenery/harness/agent-context.json`, and complete the exact union in
`changed_area.recommended_commands`. Its successful repository Go suite
satisfies `go test ./...` for the same final inputs. Run
`golangci-lint run ./...`.

Run the changed external boundaries with `go run ./scripts/verify --probe
postgres --probe storage --probe process-model --probe ui --summary --write`.
Accept each only with its assertion and cleanup evidence. Missing prerequisites
are unverified blockers, not passes. The postgres proof must deliver
notifications across at least twelve service views while a SQL query completes
and leave no owned listener/database resources. Storage proof retains cursor,
budget and persistence behavior. Process-model proof binds served identity and
cleanup. UI proof must show a current served catalog with 10,000 rows and an
inline expansion retaining bounded rendered rows, correct scroll and keyboard
selection across resizing; the consuming fixture must pass its declared checks.

Targeted benchmarks were explicitly requested and are recorded above.
An all-root timing audit was not requested. A functional pass is not a
performance measurement.
Full release certification is not selected by this task.

## Idempotence and Recovery

The worktree can be resumed without touching the primary checkout. Database and
runtime probes allocate only disposable, verified resources and use their own
cleanup. No retained app data is migrated or removed. A failed candidate stays
private until ordinary generation and runtime identity checks accept it.

## Artifacts and Notes

Starting revision: `a1fd9485356a5e7a0d117ce62870a300296bd5ab`. Review findings
describe source behavior at that revision. Record final proof and any rejected
optimization here instead of silently retaining an unmeasured tradeoff.

Ignored native evidence lives in `.scenery/harness/perf-bench/`: `prepare.py`
creates revision-specific Go overlays without changing product sources;
`run.py`, `go-baseline-{1,2}.txt`, `go-candidate-final.txt`, the four
`ui-{baseline,candidate}[-2].json` files and `summarize.py` reproduce
`results.json`. Go invocation is `go test -overlay <overlay.json> -run
'^TestPerf' -bench '^BenchmarkPerf' -benchmem -benchtime=200ms -count=8 -cpu=1`
for the five measured packages (the two baseline rounds use `-count=4`).
`run-ui.py` runs the UI benchmark using the existing profiler preload mocks and
the same locked tooling dependencies for both revisions; it creates and removes
temporary root peer-resolution symlinks. The candidate source DataTable digest
is `796816ed53232e6b5e47317ac7e6365760e72b9a578d5401622f0fabfea7ee69`;
the generated served module includes its ownership header and has digest
`b61191791b521fd02c755530fc1a26b84e3bc8f2794da2d8359cdb2a14d00c78`.
Browser capture: `.scenery/harness/perf-ui-acceptance.jpg`.

## Interfaces and Dependencies

Keep the existing standard-library and pgx dependencies. Subscription lifetime
changes affect the internal store API and all its current consumers together.
The report wire shape changes only with producer/intake/contract updates in the
same series. UI remains Astryx/StyleX and router-neutral. Public app model,
durable persisted input ABI and resource schema do not change.
