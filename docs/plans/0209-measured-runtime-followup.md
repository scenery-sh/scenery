# Measured Runtime Followup

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

Follow the completed resource-efficiency series with measured improvements to
report admission under overload, storage metadata enumeration, history
persistence and grouped table scrolling. Attribute CPU and memory in a real
ONLV runtime, then compare the first series against this followup using the
same owned fixture. The human authorized this work, native targeted benchmarks
and isolated workspaces, and explicitly declined subagents. No global install,
commit, push, release certification or all-root timing audit is requested.

## Progress

- [x] Capture the first series in an immutable before-followup worktree.
- [x] Observe the existing ONLV session read-only and prepare an owned small
  ONLV fixture through its official worktree workflow.
- [x] Capture comparable native baseline operation measurements and profiles.
- [x] Implement and measure cheap overload report refusal.
- [x] Improve storage enumeration without masking ownership or corruption.
- [x] Measure history persistence and screen a separate-disk-layout design.
- [x] Improve grouped table scrolling and measure actual browser geometry.
- [x] Compare the owned application's CPU, RSS, GC and request latency.
- [x] Complete the cumulative validation union and record final evidence.

## Surprises & Discoveries

The first ten-second observation of 48 existing ONLV processes attributes
14.77% of one core and 422.1 MiB RSS to the supervisor. Total RSS is 1,101.5 MiB
and includes shared pages; this is observed activity, not an isolated idle or
before/after comparison. The routed pprof endpoint belongs to the agents
service (PID 24342), identified by the response header, not the API host.

Storage enumeration rechecks every namespace parent for each reference read.
An anchored shard root removes repeated filesystem checks while still
opening, checking and strictly decoding every reference. This preserves the
bounded-memory scan and fail-closed behavior without a retained metadata cache.

The native storage benchmark now uses one owned namespace with 5,000 current
metadata references. It seeds a real Put descriptor once, derives the remaining
metadata-only references outside the timer, and repeats List on the same
namespace. This measures native metadata enumeration without repeatedly
synchronizing 40,000 unrelated payload publications. The interrupted initial
publication-seeded run is retained separately and excluded from comparisons.

Chrome confirms ordinary rows are 49 px and group headers 37 px in the neutral
theme, whereas the previous window assumed 44 px for both. Measured geometry
and an index that scales with group count address this mismatch.

The combined native probes passed PostgreSQL, storage and process replacement,
but the UI conformance test for cancellation during a response body timed out
once at 5 seconds. Its focused rerun passed in 31.6 ms and the complete UI probe
then passed. No client cancellation implementation or timeout was changed;
this remains an observed intermittent test failure, not an established cause.

## Decision Log

- 2026-09-30, Petr: Continue the five followup areas, without subagents, with
  targeted native before/after measurement.
- 2026-09-30, Codex: Preserve the existing running ONLV session and its dirty
  go.mod. Only the official owned fixture may be stopped, repinned or mutated.
- 2026-09-30, Codex: Keep one current report and history format. Prefer measured
  implementation improvements over a speculative persistent index or journal
  requiring an unrelated retained-state migration.

A warmed bounded history save takes about 4.2 ms and allocates about 2.1 MB in
the synthetic workload initially populated with 3,000 outputs and 3,000 events,
then normalized under the current retention rules before timing. The owned runtime CPU profile
attributes its recurring cost to watch membership traversal and GC, while its
retained heap is dominated by syntax trees and type/compiler data. Trace/report
histories are already exported to Victoria rather than duplicated in devdash
JSON. A second history disk format is therefore a NO-GO for this series: it
would add migration, crash recovery and writer coordination without addressing
the observed supervisor hotspot. The whole-file bounded metadata/history write
remains; no incremental persistence saving is claimed.

The observed watcher path does justify a small compiler change: wildcard
matching now walks UTF-8 boundaries without allocating long-filename rune
slices. Existing independent exhaustive/randomized equivalence tests pass.

## Outcomes & Retrospective

The implementation, measured acceptance and cumulative validation are complete.
The followup establishes
local operation improvements, correct grouped geometry and an explicit NO-GO
for a second history format. It does not establish lower whole-application CPU,
retained heap or browser frame/paint time.

Eight native samples per lane on Apple M2 Ultra, darwin/arm64, Go 1.27.0 use
the first series as baseline and this followup as candidate. Values below are
medians; allocated bytes are cumulative per operation, not retained RSS.

| Workload | Before | After | Interpretation |
|---|---:|---:|---|
| Full report queue, 8 KiB payload | 8,639.5 ns; 9,760 B; 3 allocations | 9.391 ns; 0 B; 0 allocations | Immediate lossy refusal avoids serialization. |
| Byte-full report queue | 8,458 ns; 9,760 B; 3 allocations | 9.4875 ns; 0 B; 0 allocations | The exact byte admission check remains authoritative. |
| Accepted report | 8,534 ns; 9,760 B; 3 allocations | 8,615 ns; 9,760 B; 3 allocations | No material accepted-path improvement. |
| Native List, 5,000 references, page 100 | 1,869.38 ms; 77.11 MB; 679,978 allocations | 428.40 ms; 61.29 MB; 412,110 allocations | 77.08% less time and 20.51% fewer allocated bytes; still a full O(N) scan. |
| Six watcher glob comparisons, including a long name | 2,668.5 ns; 912 B; 7 allocations | 2,062 ns; 336 B; 6 allocations | 22.73% less time without long-name rune buffers. |
| Group-context lookup, long group | 8,876.73 ns | 21.0 ns | 10,000 lookups per sample; one 2.81 ms index build excluded. Numeric index storage is 8 B for this single-group fixture, plus array overhead. |
| Unchanged history save, warmed negative control | 4.215 ms; 2,098,867 B; 17 allocations | 4.200 ms; 2,098,867.5 B; 17 allocations | Same persistence code; no saving claimed. |

The original cold/pruning history samples are excluded. Their apparent timing
and allocation difference disappeared after warming and normalizing both
lanes, illustrating why unchanged controls are necessary. The final storage
result includes both pre-open and post-open shard identity/ownership checks;
the earlier faster candidate result is not used.

The real owned ONLV fixture runs 48 processes. One paired observation uses
about 12 seconds of quiescent profiling and 400 read-only platform Stats calls
at 40 requests/second, with exact framework/executable identity and no process
replacement or failed requests. Supervisor CPU is 5.38% -> 6.28% of one core,
RSS 330.8 -> 325.6 MiB, retained heap after forced GC 153.0 -> 153.3 MiB,
and idle allocation 49.05 -> 47.97 MiB with one GC per lane. API p50 is
2.667 -> 2.739 ms and p95 4.977 -> 5.132 ms. These short observations do not
prove an application-wide improvement or statistically characterize small
regressions. Summed process RSS includes shared pages and variable frontend
residency, so its decrease is not credited to this patch.

The baseline supervisor CPU profile attributes 52.6% cumulative sampled CPU to
watch membership traversal, with substantial syscall and GC work. Its retained
heap is dominated by CSTs, imported Go types, maps, cloned resources and source
buffers. The focused glob improvement does not remove the periodic workspace
walk or the compiler's retained models; broader changes need their own measured
design rather than a speculative cache in this series.

Chrome renders the generated catalog against 10,000 rows with two groups and
49/37 px measured row/header heights. Deep selection retains the correct group
and only 32 ordinary DOM rows. Expanding a 1,233 px measured detail, resizing it,
selecting row 8500 and collapsing it preserves the visible anchor at exactly
the same top coordinate (0 px shift). Native scroll tracing retains all events
without data loss. Frame p95 is 14.1 -> 14.0 ms, Layout 25.9 -> 27.9 ms and
Paint 36.2 -> 37.2 ms: no whole-frame or paint improvement is claimed.

## Context and Orientation

Candidate: `/Users/petrbrazdil/.codex/worktrees/performance-review/scenery`,
branch `perf/runtime-resource-efficiency`. Before-followup:
`/Users/petrbrazdil/.codex/worktrees/performance-followup-baseline/scenery`,
52 copied files with digest
`e24ff7918157e7a94ece32f233f9631888acedfea987166e4232ea922962d8b6`.
The primary repository remains at `a1fd9485356a` and is not edited.

The owned app is `/Users/petrbrazdil/Repos/onlv-scenery-perf-followup`, fixture
`e2f9fd30-b2c2-40eb-a506-3b5c95d45e36`, prepared by
`just worktree scenery-perf-followup`. Its data and runtime are independent of
the main application's data and session.

## Milestones

1. Attribute real costs and establish operation and application baselines.
2. Implement focused improvements with ownership, retention and UI proof.
3. Compare final code and complete repository and native-boundary validation.

## Plan of Work

Use ignored Go overlays for expensive filesystem benchmarks and process
profiling. Measure report enqueue with full and available queues, storage List
with thousands of references, and history saves with representative bounded
payloads. Use the generated UI catalog in a served browser fixture to measure
scrolling and verify group context, selection and expansion. Pin the owned ONLV
fixture to exact snapshot producers before each application measurement.

Keep changes simple and bounded. Reject a larger persistence/index architecture
if measured costs do not justify its RAM or crash-recovery complexity; record
the remaining scan/write work rather than claiming it was removed.

## Concrete Steps

Run targeted native benchmarks serially with `-benchmem`, repeated samples,
and the same Go toolchain. Save profiles and raw measurements under
`.scenery/harness/perf-followup/`. Run affected package tests and meaningful
race tests. Regenerate both committed clients, typecheck the UI catalog, run
the full verifier, correctness linter and selected storage/process/UI probes.

## Validation and Acceptance

Acceptance requires comparable before/after operation samples, real application
producer attribution, and explicit distinction between observed activity and
controlled workloads. Successful reports retain the same wire format and
counters; rejected full queues avoid serialization. Storage ownership,
corruption, cancellation and generation invalidation remain fail-closed.
History reload/replay and retention remain correct. Served tables keep bounded
DOM rows, group context and correct measured offsets during scrolling.

The final changed-area snapshot selects the cumulative validation union.
Existing warnings are reported separately. No ordinary Go test launches real
processes, services, toolchains or expensive native benchmarks.

Executed checks and results:

- `go test ./internal/compiler ./internal/parse ./runtime ./internal/storagefs ./internal/generate ./internal/devdash`: pass.
- `go test ./runtime` after the test-only tagged-switch correction: pass.
- `go test -race ./runtime ./internal/storagefs ./internal/compiler`: pass.
- `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`: pass.
- The same generation command for `internal/compiler/testdata/house` and `testdata/assistant`: pass; assistant output unchanged.
- Generation and `generate --check` for the ignored served catalog consumer: pass.
- `tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json` and `tsconfig.generated-clients.json`: pass.
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts internal/generate/testdata/dev_runtime_client.test.ts`: pass, including the complete UI probe rerun described above.
- `bun test internal/generate/testdata/query_table_perf.test.tsx internal/generate/testdata/query_table_regressions.test.tsx`: 19 pass, 0 fail, 92 assertions.
- `tools/typescript/node_modules/.bin/tsc -p .scenery/harness/perf-ui-app/tsconfig.consumer.json` and `node .scenery/harness/perf-ui-app/serve.mjs --build`: pass.
- `golangci-lint run ./...`: pass after correcting QF1003 in the new test; no suppression.
- `go run ./scripts/verify --summary --write`: pass with existing knowledge/architecture and advisory suite-duration warnings; includes `go test ./...`, vet, contract drift and schema checks.
- `go run ./scripts/verify --probe postgres --probe storage --probe process-model --probe ui --summary --write`: PostgreSQL, storage and process-model pass with cleanup evidence; UI initially failed its intermittent download-abort test.
- `go run ./scripts/verify --probe ui --summary --write`: pass with warnings after the focused cancellation rerun; no boundary code changed between probe runs.
- Final `go run ./scripts/verify --summary --write` after the test correction and evidence update: pass with 37 knowledge and 19 architecture warnings; the cached repository suite took 3.714 seconds and raised no duration warning.
- The stopped owned ONLV fixture's normal-candidate generation and `scenery check -o json`: pass; `scenery ps -o json` confirms it remains stopped.

The changed-area classes are `cli-json-contract`, `compiler-or-generator`,
`go-package`, `release-sensitive-or-runtime` and `ui-catalog`. The full verifier's
repository Go suite covers every recommended package command, including
`cmd/scenery`, `internal/devreport`, `internal/durable/store`, `internal/machine`
and `scripts/verify`. The `scenery logs --limit 500 -o jsonl` recommendation has
no application session in the framework checkout; owned ONLV session logs and
native probe evidence provide the runtime boundary evidence. The private UI
fixture has no app-specific lint command. Production dataset/domain API load,
release certification, all-root isolated 100 ms p95 audits, global installation,
commit and push were not performed or claimed. The initial interrupted storage
benchmark is excluded. No target-app domain source was changed.

The current report wire revision and storage format are unchanged by this
followup. The affected report overload and UI geometry contracts were updated;
instruction documents were intentionally unchanged because ownership and
workflows did not change.

## Idempotence and Recovery

The before-followup snapshot is immutable. Repeating benchmarks only replaces
task-owned ignored artifacts. Stop the owned ONLV fixture using its recorded
runtime producer, preserve its data, and restore no unrelated selection. Remove
only task-owned browser tabs and temporary Tailscale Serve mappings. Keep
existing mappings and the main application running.

## Artifacts and Notes

Baseline manifest, live observation, routed service profile, supervisor native
sample and ONLV preparation log are in `.scenery/harness/perf-followup/`.
The completed first-series plan 0208 remains immutable historical evidence.

Raw before/after Go and group-index samples, the warmed history control and
`benchmark-summary.json` are retained there. `baseline-app.json` and
`candidate-app.json` bind profiles to actual executable SHA-256 and framework
snapshots. Profiling uses one identical temporary hook in separate source
snapshots, selected through the owned app's official framework-use workflow;
it is absent from the product patch. `baseline-browser.json`,
`candidate-browser.json`, both complete trace JSON files,
`ui-candidate-identity.json` and `ui-acceptance.png` bind the browser proof to
the generated catalog. Logs retain failures as well as successful reruns.

The owned ONLV fixture is stopped with data retained, and repinned/generated
against a normal candidate framework snapshot without the profiling hook.
Its worktree remains available. The task browser tab, UI server, closed profiler
sockets and only the task's HTTPS 8445 Serve mapping have been removed; the
existing 8443/8444 mappings were independently verified unchanged. Main ONLV
still runs as session `main-dbe32e`, owner PID 22727, with its pre-existing dirty
go.mod preserved. The primary Scenery checkout remains clean at
`a1fd9485356a5e7a0d117ce62870a300296bd5ab`. Changes remain uncommitted in the
candidate branch.

## Interfaces and Dependencies

Use Go's standard library and the existing React/StyleX/Astryx catalog. Add no
dependency, environment knob, compatibility decoder or public tuning surface.
Application source snapshots, generated clients and executable identity are
part of the measurement record. Existing report budgets and storage leases
remain authoritative.
