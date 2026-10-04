# Measured Performance And Stability, First Pass

This ExecPlan is a living document maintained according to `PLANS.md`. Keep
Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective
current as measurements and validation change. Times below are UTC.

## Purpose / Big Picture

Improve Scenery's performance, allocations, timings and stability while keeping
its contracts and ownership simple. The user authorized at least five hours of
active work on 2026-10-02, beginning at 18:34:05 UTC. This first pass must not be
closed before 23:34:05 UTC. The user subsequently clarified that this means
optimization work, not multi-hour tests. Keep experiments bounded to the decision
they support, including small improvements and adverse controls.

## Progress

- [x] (2026-10-02 18:34 UTC) Confirmed clean worktree at
  `a1fd9485356a5e7a0d117ce62870a300296bd5ab`, native macOS arm64, M2 Ultra,
  24 CPUs and Go 1.27.0. Read owning instructions and existing plans.
- [x] (2026-10-02 19:02 UTC) Profiled graph/context traversal and compression;
  measured immutable baseline/candidate binaries and exact result parity.
- [x] (2026-10-02 19:13 UTC) Reproduced and repaired literal separator escape
  corruption in the exact JSON codec, preserving its existing specification.
- [x] (2026-10-02 19:40 UTC) Stopped excessive runtime/fuzz campaigns on user
  steering. Rejected unbounded compressor retention and selected one idle writer.
- [x] (2026-10-02 20:29 UTC) Improved schema lookup, parser allocation, ASCII
  positions and HTTP negotiation; retained independent correctness oracles.
- [x] (2026-10-02 21:29 UTC) Improved redaction, watch fingerprints, Unicode key
  ordering and bounded gzip stream staging. Fourth full validation checkpoint,
  native-contract/dev-process probes, race checks and lint passed.
- [x] (2026-10-02 21:50 UTC) Removed duplicate schema hashing and canonical
  hash copies. Fifth full verifier, schema/serialization parity and race checks
  passed. Both committed clients regenerated without changes.
- [x] (2026-10-02 22:21 UTC) Measured combined HTTP behavior and two independent
  30-edit activation cohorts. Retained the adverse first-cohort p95. Extended
  disconnect coverage through gzip header, body and trailer failures.
- [x] (2026-10-02 22:38 UTC) Removed graph hash copies, discarded agent snapshot
  clones and full-resource copies during selection. Exact agent parity covers
  5,328 responses; agent race tests and isolated changed roots pass.
- [x] (2026-10-02 23:00 UTC) Reproduced a verifier race: a late completion
  from the preceding build could satisfy the next edit. Bind completion to a
  queued operation after the edit boundary, with a deterministic failing-before
  test. Final dev-process/process-model confirmation subsequently passed.
- [x] (2026-10-02 23:09 UTC) Final CLI and complete stdio comparisons pass
  exact output identity. Full validation, lint, affected race checks and named
  native-contract/dev-process/process-model probes pass. Source review is complete;
  evidence inventory and closure remain below.
- [x] (2026-10-02 23:21 UTC) A final profile-led compiler change preallocates
  existing value indexes without changing ownership. Complete compile-result
  hashes match in all twenty cohorts; allocation falls about 0.3% for both
  fixtures. Affected validation and final scope measurements subsequently passed.
- [x] (2026-10-02 23:37 UTC) Passed the minimum five-hour working interval,
  final complete-source CLI/stdio measurements and all three named runtime probes.
  Independently confirmed all 21 attested probe PIDs absent. Recorded outcomes,
  validation scope and remaining limits, and reconciled the plan indexes.

## Surprises & Discoveries

The original graph/context query rebuilt reference edges for each focus and
scanned all edges for every visited resource. A context allocation profile also
attributed 83.8% to repeatedly materializing the same schema. Query-local shared
work removes those costs without persistent indexes. The agent separately cloned
all retained graph views before checking whether the revision already existed;
94.4% of warm resource-read allocation was discarded snapshot work.

The compression experiment initially used `sync.Pool`. Despite lower CPU and
allocation, a natural-GC sample retained about 34 MiB RSS versus 28 MiB without
reuse. The accepted implementation retains one idle writer and one buffer of at
most 32 KiB. Active responses own their state. The 18-minute bounded comparison
completed 21,904 requests per lane without failures. The proposed four-hour run
was intentionally stopped; it does not establish long-duration stability.

The exact JSON writer blindly replaced encoded U+2028/U+2029 bytes, corrupting
ordinary literal backslash sequences. `TestContractJSONPreservesLiteralSeparatorEscapes`
failed before the repair. Escape-aware writing now preserves both keys and values,
including mixed literal escapes and actual Unicode separators. This conforms to
`docs/spec/http.md` section 12 and `SPEC.md` section 20.4; it is not a new profile.
The old invalid output is excluded from performance claims.

Two test-fixture errors were corrected and their evidence retained. The graph
oracle used an unreachable sentinel inside the permitted depth; disconnected seed
`1000000` now uses a sentinel beyond the transport limit. A 64 KiB gzip failure
fixture reached input EOF before its body-write failure; a 128 KiB fixture really
exercises that boundary. Neither was a product failure. One lint run caught an
unchecked test decoder `Close`; the test cleanup now explicitly handles it. The
first consolidated-plan verifier run also rejected its missing living-document
statement. That documentation error was fixed; `verify-final-confirmed.log`
passes the complete verifier and `lint-final.log` has zero issues.

A native-fixture `check` pilot fails on pre-existing stale generated Go contracts,
including against the baseline source overlay. Successful compile views and the
isolated native-contract probe provide the claimed measurements and boundary
proof. No unrelated fixture cache was repaired to manufacture a timing result.

The final dev-process run reported five matrix builds instead of six, while
native-contract and cleanup passed. The next instrumented run passed unchanged;
the failed supervisor log had already been removed by the existing probe cleanup,
so its exact cause is not proven. Source inspection exposed a concrete verifier
race matching that failure mechanism: the first `build.request` after an edit's
log offset could be the preceding build's late completion. A deterministic test
fails in six boundary cases before the fix. The wait now requires a `build.queue`
and matching operation ID after the offset. Expected build counts and assertions
are unchanged. Its shared callers require both dev-process and process-model
proofs. Evidence: `runtime-probes-final-failed.json`, `dev-process-diagnostic.json`,
`watch-wait-reproduction.log` and `watch-wait-tests.log`.

Keep all adverse controls. An initial absent-Accept result regressed by 0.077 ns;
20 isolated pairs confirmed it. Moving nonempty parsing to a private helper avoids
its stack cost in the default path, and the final matrix improves both empty
controls. Unicode parser, identity compression, escaped-string and small-hash
controls have intervals spanning parity; no speedup is claimed for them. The
first real edit cohort has worse candidate p95 (890 versus 813 ms); the second has
895 versus 1,314 ms. Both consistently improve the paired median about 4.2%, but
neither establishes a stable tail-latency gain or the existing 300/500 ms targets.

## Decision Log

- 2026-10-02, Codex: use native worktree-local executables, cached tests and
  immutable comparison binaries. No global install, dependencies, environment
  knobs, compatibility paths or new persistent caches. Honor the human's
  no-delegation rule.
- 2026-10-02, Codex: the explicit performance/timings request authorizes
  benchmarks and focused root timing. It does not require release certification,
  secret-backed service probes or an all-root timing audit.
- 2026-10-02, Codex: keep graph indexes query-local and point into immutable
  resource slices. A multi-source breadth-first traversal computes the same
  depth-limited union; deterministic output sorting remains at the boundary.
- 2026-10-02, Codex: reuse identities already present in immutable schema indexes
  while continuing to return detached schema maps. Reuse a builder's computed
  revision instead of hashing its result again.
- 2026-10-02, user steering and Codex correction: stop multi-hour tests, retain
  their interruption receipts and continue bounded implementation experiments.
- 2026-10-02, Codex: bound idle compression state with one-entry channels.
  Reset destinations to `io.Discard`; successful callers own `Close`, and failed
  streams receive no valid trailer. Preserve `io.CopyN` length/error semantics.
- 2026-10-02, Codex: keep lossless token byte ownership unchanged. Reserve token
  and trivia capacity, skip unused comment attachment, and use byte columns only
  for wholly ASCII sources. Avoid retaining a whole source through a tiny token.
- 2026-10-02, Codex: use scalar comparisons for UTF-16 key ordering, converting
  only a differing supplementary/BMP pair. Retain independent code-unit oracles,
  malformed-string behavior, escaping and exact revision bytes.
- 2026-10-02, Codex: check existing agent retention before cloning and again under
  the insertion lock. Keep the same 32-snapshot insertion order and aliases.
  Single-resource reads use a reverse scan; multiple reads use a pointer index.
  Request duplicates, sorted errors, empty arrays and returned value copies stay
  unchanged. No cached resource index or public API is added.
- 2026-10-02, Codex: repair the verifier's completion correlation, not its build
  count or timing thresholds. Require a queued operation after the edit offset
  before accepting its completion. Keep the nondeterministic failed probe and
  the passing diagnostic rerun, without claiming the unavailable failed log
  proves causation.
- 2026-10-02, Codex: reserve existing compiler value-map capacity from the
  resource count. Keep value copies and every call site unchanged. Accept the
  small measured allocation reduction without adding a cache or pointer alias.

## Outcomes & Retrospective

Completed the first five-hour optimization pass. Final native/house CLI compile
views are 21–25% faster and complete agent stdio request medians are 39–56% lower,
with exact baseline/candidate output identity. The combined local HTTP workload
at checkpoint five uses 66% less CPU and 94% fewer allocated bytes per request,
with 41% lower p95 latency; its runtime paths did not change afterward. These are
bounded fixture results, not production capacity claims.

Kept small improvements alongside the larger ones, including about 0.3% lower
whole-compile allocation from reserving existing map capacity. Removed repeated
work within existing ownership boundaries, without new dependencies or public
interfaces. Also repaired literal JSON separator escaping and a verifier race
that could attribute a preceding build's completion to the next edit.

Full repository verification, affected race checks, lint, both client
regenerations and native-contract/dev-process/process-model probes pass. Exact
outputs, error paths, concurrency, disconnect behavior and owned cleanup are
covered within the recorded scopes. Existing knowledge/architecture warnings
remain. Real edit activation improves about 4% at the median, but the first
cohort regresses at p95 and the second improves; a stable tail-latency gain and
the existing 300/500 ms targets remain unproven. No universal absence of
regressions or long-duration stability is claimed. Changes remain local and
uncommitted; retained profiles identify the next iteration's work.

## Context and Orientation

The checkout is `/Users/petrbrazdil/.codex/worktrees/b5fe/scenery`. The starting
commit is the immutable whole-CLI/runtime baseline. Individual experiments use
explicit earlier-stage binaries so each incremental effect can be attributed.
Evidence is under `.scenery/harness/performance-0208/` (abbreviated E below),
ignored by Git. Repository artifacts remain English; developer communication is
Czech. No commit, push or global installation is part of this pass.

`internal/graph` owns graph queries, context pages and revision/token hashing.
`internal/contractagent` owns the JSON-RPC read surface and retained immutable
snapshots. `internal/spec` owns schema identities and canonical serialization;
`internal/contract` owns exact application JSON. `internal/scn` owns lossless
source parsing and positions. `internal/redact` and `runtime` own request logging,
response negotiation and compression. `internal/compiler/helpers.go` reserves its existing value index capacity.
`cmd/scenery/watch_compiler_snapshot.go` owns watch input fingerprints. `scripts/verify` owns the corrected build-event
correlation in its existing explicit probes. All changes stay inside these
existing boundaries.

## Milestones

1. Establish immutable baselines, profiles and observable correctness scenarios.
2. Keep only simple measured improvements, with independent boundary checks and
   adverse controls for small, Unicode, missing, concurrent and failure inputs.
3. Validate combined CLI/runtime behavior, exact source/runtime identity and
   cleanup, then record the first-pass limits and next investigation targets.

## Plan of Work

The production changes, source review, final CLI/stdio comparisons and required
validation are complete. Named native-contract, dev-process and process-model
probes confirm their external boundaries and owned cleanup. The final closure
verifier covers the reconciled plan and indexes; `verify-closure.log` and its
saved JSON reports record that result. Future defects require a scoped fix and
the checks affected by it; unchanged passing checks need not be repeated.

The pre-capacity-refinement candidate profiles compile and handle 400 requests per fixture after
warmup, writing JSON to `io.Discard`. They retain an exact workspace revision
throughout. Native allocates about 1.905 GB in total; house about 4.999 GB, including
fresh compilation for every request. The sampled allocation profiles attribute
about 15%/20% to HCL token emission and 17%/18% cumulatively to Scenery's lossless
CST builder (overlapping categories, not additive). Compiler resource indexes
account for about 4%/3%. CPU samples contain substantial Darwin syscall/runtime
frames; do not translate those directly into a proposed CPU saving.
`remaining-native-*` and `remaining-house-*` preserve the candidate-only profiles;
they are future-work attribution, not paired performance claims.

For the next iteration, use the retained profiles to investigate remaining
snapshot construction, canonical JSON normalization and real Go edit latency.
Do not infer that the largest microbenchmark improvement dominates real apps.
The runtime goroutine-ID path needs an ownership/API design before changes;
unsafe runtime internals and new global caches are not justified. Per-line Unicode
indexes and contiguous token arenas were deferred because their retained-memory
and ownership costs need separate evidence.

## Concrete Steps

Run commands from the checkout above. `go test` keeps its normal cache enabled.
For a selected benchmark, build an immutable package binary with
`go test -c -o <candidate.test> ./<package>` and invoke
`<candidate.test> -test.run '^$' -test.bench '<pattern>' -test.benchmem -test.benchtime 200ms`.
The E `compare-benchmarks.py` helper alternates immutable baseline/candidate
binaries for ten pairs and records their SHA-256 identities, raw outputs, medians
and paired bootstrap intervals. Small adverse controls use twenty pairs.
Do not run builds, race suites or other load concurrently with timing cohorts.

`python3 E/compare-cli.py --baseline E/scenery-baseline --candidate E/scenery-complete
--output E/cli-complete-pairs --pairs 20` runs six native/house compile views in
fresh processes, requiring full stdout hashes to match. E is notation: replace
it with `.scenery/harness/performance-0208` when running the command.

`go run ./scripts/verify --benchmark edit-latency --summary --write` owns its
isolated fixture roots and independent cache lanes. Two completed runs are
retained; do not repeat them without a new concern. The HTTP comparison helper
uses five separately launched 20-second cohorts, a three-second quiescence
window, exact bodies/executable identities and stream-close/exit assertions.
No multi-hour campaign is needed to close this pass.

## Validation and Acceptance

Final changed-area classes are `go-package`, `cli-json-contract`,
`compiler-or-generator` and `release-sensitive-or-runtime`. Use full verification, not quick followed by full.
The final selected `--write` run refreshes E-independent
`.scenery/harness/agent-context.json`; inspect `changed_area.validation_classes`
and fulfill the union in `changed_area.recommended_commands`.

| Exact command, from the checkout root | Current evidence / acceptance |
| --- | --- |
| `go test ./internal/compiler ./internal/parse ./internal/generate` | Final capacity-refinement suites pass (`compiler-capacity-tests.log`); compiler race check also passes (`compiler-capacity-race.log`). |
| `go test ./internal/contractagent` | Final agent tests and baseline-overlay tests pass; `agent-selection-tests.log` and `agent-selection-baseline-tests.log`. |
| `go test ./internal/contract ./internal/graph ./internal/redact ./internal/scn ./internal/spec ./runtime ./cmd/scenery` | Affected suites passed through checkpoints; final full verification covers all final source. |
| `go run ./scripts/verify --summary --write` | Full runs pass before and after the verifier fix (`verify-final-confirmed.log`, `verify-after-watch-fix.log`, `verify-complete-source.log`, `verify-closure.log`), including the repository Go suite and vet. Existing 38 knowledge and 21 architecture warnings remain. The final dependency rebuild produced a 6.677 s suite advisory; isolated root proofs remain separate. |
| `golangci-lint run ./...` | Final runs report zero issues, including the verifier fix and final compiler benchmark (`lint-final.log`, `lint-after-watch-fix.log`, `lint-complete-source.log`, `lint-complete-final.log`). |
| `go test -race ./internal/contract ./internal/graph ./internal/redact ./internal/scn ./internal/spec ./runtime ./cmd/scenery ./internal/contractagent` | Owning package race runs pass at their final production changes; final agent run is `agent-final-race.log`. |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json` | Final capacity-refinement regeneration passes with no diagnostics or tracked client changes (`native-regeneration-complete.json`). |
| `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json` | Final capacity-refinement regeneration passes with no diagnostics or tracked client changes (`house-regeneration-complete.json`). |
| `go run ./scripts/verify --probe native-contract --summary --write` | Final production run passes in 8.475 s with current generated behavior and served identity (`runtime-probes-complete.json`). |
| `go run ./scripts/verify --probe dev-process --summary --write` | Final production run passes in 106.572 s, retaining all six matrix builds, 20 unique edits and resource bounds (`runtime-probes-complete.json`). Earlier failure and diagnostic rerun remain recorded. |
| `go run ./scripts/verify --probe process-model --summary --write` | Final production run passes in 29.106 s, including failed builds, exact generations and lost-publication recovery (`runtime-probes-complete.json`). |
| `go test ./scripts/verify` | New completion-correlation test failed before the fix; package suite and race check pass after it (`watch-wait-tests.log`, `watch-wait-race.log`). |

Changed/new focused roots run in twenty separate test processes. Largest measured
root p95 is the existing contract Unicode oracle at 50 ms; spec's random oracle is
21.605 ms, graph fuzz seeds 14.673 ms, and other earlier changed roots below 3.4 ms. The new verifier root is 10 ms p95.
The latest agent selection/retention roots are 0.357/1.770 ms. The unrelated suite
outlier `TestListingsAreBoundedAndEvictedAfterCompleteWalks` is 50.559 ms in
isolation and was left unchanged. Process/package initialization is reported
separately. This is not an all-root timing certification.

Graph fuzzing completed 2,648,315 executions. JSON string/differential campaigns
were intentionally interrupted after 17,566,993/26,416,157 executions; coordinators
reported PASS but outer `go test` exited 1 on signal. They are not normally
completed fuzz campaigns. Ordinary seed coverage remains required in final tests.

Acceptance requires exact results, error precedence, ordering, revision/token
bytes and immutable ownership, except the documented JSON conformance repair.
Require zero checked HTTP failures, balanced streams, successful owned exits and
cleanup, applicable checks, adverse controls retained and at least five hours of
work. No claim of production capacity, indefinite stability or universal absence
of regressions follows from these finite experiments.

Public behavior documentation is intentionally unchanged: implementation-only
optimizations preserve contracts and the JSON repair follows existing normative
text. UI/catalog/typecheck, secret-service probes, emulated Linux, global install
and release certification are unselected because their boundaries are unchanged.
No user-facing UI is changed, so browser acceptance is not applicable.

## Idempotence and Recovery

Keep immutable baseline executables and raw results. Do not reset unrelated work,
remove shared caches/data or signal unverified processes. Probes own isolated
resources and receipts. The two edit-latency roots were independently confirmed
absent, with no remaining Git worktree or owned process (`edit-latency-cleanup.json`).
Final named probes record cleanup, and every attested probe PID is absent in the
independent `final-probe-process-cleanup.json` liveness check. Only this task's rejected prototype
edits may be replaced; do not erase adverse evidence. Storage has stayed sufficient
(approximately 16 GiB free at the latest check).

## Artifacts and Notes

All ratios are candidate/baseline paired medians. Unless specified otherwise,
component results use ten alternating 200 ms pairs and a deterministic 10,000-draw
95% paired bootstrap. Each row names its own stage; ratios must not be multiplied.
Runtime microbenchmarks retain the package's existing `GOMAXPROCS=4`; graph and
standalone runtime processes use 24. Absolute timings across them are incomparable.
Raw evidence remains local and ignored; committed benchmark/test sources retain
repeatable workload definitions.

| Workload / stage | Time ratio | Allocated bytes per operation, before → after | Evidence beneath E |
| --- | --- | --- | --- |
| Graph, 1,024 resources / 8 references | 0.273 | See raw series | `graph-final-pairs` |
| Context, 1,024 resources / 8 focuses | 0.159 | 12,053,453 → 1,784,499 | `graph-final-pairs` |
| Context include deduplication, additional stage | 0.085 | 44,732,872 → 5,717,039 | `graph-includes-pairs` |
| Public record schema identity reuse | 0.087 | 39,037 → 9,049 | `spec-pairs` |
| Schema-index duplicate hash removal | 0.540 | 12,398,999 → 6,956,645 | `schema-index-pairs` |
| Buffered gzip, 4 KiB text, bounded writer reuse | 0.052 | 1,076,945 → 944 | `compression-final-pairs` |
| Streaming gzip, 4 KiB text, bounded writer reuse | 0.054 | 1,080,425 → 4,424 | `compression-final-pairs` |
| Streaming gzip, additional bounded copy buffer | 0.928 | 4,424 → 328 | `stream-copy-sized-serial-pairs` |
| Concurrent gzip, 64 KiB random body | 0.125 | 1,150,546 → 156,836 | `compression-concurrent-pairs` |
| Exact JSON full pass | 0.545 | 297,519 → 185,086 | `json-final-pairs` |
| SCN parse, 1,024 blocks, initial token reservation | 0.907 | 44,536,826 → 34,499,011 | `scn-pairs` |
| Same parser, additional trivia reservation | 0.914 | 34,498,865 → 29,991,815 | `scn-trivia-pairs` |
| ASCII line with 1,024 items, position shortcut | 0.079 | Unchanged | `position-controls-pairs` |
| Accept-Encoding gzip | 0.142 | 216 → 0 | `negotiation-pairs` |
| Accept application/json, one produced type | 0.550 | 304 → 96 | `media-fast-path-pairs` |
| Complete serial request-log handler | 0.333 | 34,506 → 576 | `console-pairs` |
| Watch fingerprint, 4,096 source files | 0.862 | 164,256 → 65,952 | `watch-fingerprint-pairs` |
| Contract map, 1,024 Unicode keys | 0.492 | 924,406 → 415,065 | `object-keys-pairs` |
| Canonical revision map, 1,024 Unicode keys | 0.604 | 1,329,767 → 810,760 | `spec-canonical-pairs` |
| Canonical Unicode strings, additional direct write | 0.731 | 3,824 → 2,892 | `spec-string-pairs` |
| Revision hash copy removal | Inconclusive | About 5% lower for record/catalog | `spec-hash-pairs` |
| Graph hash copy removal | Scope-specific | 3.6–4.6% contract; about 13.3% large generic; about 15% tokens | `graph-hash-pairs` |
| Warm agent resource read, 1,024 resources, including JSON encoding | 0.0008 | 8,890,096 → 2,352 | `agent-final-pairs` |
| Warm agent context read, 1,024 resources, including JSON encoding | 0.0411 | 9,300,690 → 320,623 | `agent-final-pairs` |
| Agent first read, explicit 1,024-resource views | 0.732 | 9,365,903 → 6,799,328 | `agent-final-pairs` |
| Selection of all 1,024 resources, additional stage | 0.417 | 544,985 → 277,840 | `agent-selection-pairs` |

The final compiler-capacity refinement compares complete fresh compilations,
not just its map helper. Native allocation is 3,876,719 → 3,864,640 B/op; house is
10,762,284 → 10,727,451 B/op. Their paired allocation ratios are 0.9971/0.9967,
with intervals below parity; allocation counts fall by about 54/107. Native time
is 0.9559 [0.9443, 0.9808]; house time is inconclusive at 0.9994 [0.9748, 1.0098].
Complete `compiler.Result` JSON hashes match across all twenty cohorts
(`compiler-capacity-pairs`, including `parity.json`). The compiled fixture
benchmarks remain in `internal/compiler/compile_bench_test.go` for later work.

The final agent comparison changes only retention/selection against the previous
agent implementation with the same already-optimized graph dependencies. Warm
1,024-resource lookup goes from 10.83 ms to 8.44 microseconds. It is an in-process
JSON-RPC read measurement, not network latency. The first-read missing-views
control is 1.004, interval [0.993, 1.021], with slightly lower allocation.

The real-fixture agent comparison loads all three captured CLI views (native
22 expanded resources, house 56), and uses explicit expanded-view requests.
Fourteen scenarios cover first/last/eight-resource selection, list, graph,
context and first reads. All 280 checked response hashes match across the
original whole-production-source overlay and final sources. Warm single-read
time ratios are 0.0037–0.0086, list 0.147/0.169, graph 0.117/0.179, context
0.285/0.134, and first reads 0.728/0.692; every interval is below parity.
`real-agent-expanded-pairs` owns the measurements and parity receipt. A pilot
first misapplied canonical expanded-manifest decoding to authored views, then
requested expanded-only addresses from the default effective view; those driver
errors were corrected before any measured cohort. No input sorting or product
behavior was changed to satisfy the driver.

Production `scenery agent serve --stdio` retains one session across requests,
so warm retention reuse is exercised there. It also calls `compiler.Compile`
for every request. Therefore these in-process read speedups are not end-to-end
stdio latency claims; compilation remains part of that production boundary.

The pre-capacity stdio boundary is also measured separately (`agent-stdio-pairs`).
Five alternating baseline/candidate process pairs per fixture execute three warmup
requests and ten rounds of get/list/graph/context, for 860 exact matching responses
and clean process exits. Each request goes through the real `scenery agent serve
--stdio --app-root <fixture>` command and its fresh `compiler.Compile` call.
Per-cohort median round-trip latency ratios are 0.522–0.622 for native and
0.436–0.518 for house. Single-resource reads are 9.398 → 5.407 ms and
23.071 → 10.396 ms; context is 16.647 → 8.745 ms and 29.944 → 12.948 ms.
Whole-process CPU per request, including startup/warmup, has ratios 0.604/0.536.
Maximum RSS has ratios 0.975/0.934; it is a process peak, distinct from quiescent
HTTP RSS. Every paired interval is below parity. No stdio p95 claim is made.

The combined HTTP checkpoint has five separately launched 20-second cohorts,
approximately 52,000 checked requests per lane/cohort and 18 routes including
identity, gzip and HEAD. All body/executable hashes, stream closes and exits pass;
both lanes settle at eight goroutines. `runtime-combined-checkpoint-5/aggregate.json`
records CPU/request 975 → 331 microseconds (ratio 0.3393), allocated bytes/request
922,676 → 55,537 (0.0602), p50 0.660 → 0.409 ms (0.6197), p95 1.310 → 0.770 ms
(0.5914), and quiescent RSS 41,280 → 34,048 KiB (0.8207). All intervals are below
parity. This checkpoint predates the graph-hash and agent changes; it measures a
local synthetic HTTP workload, not production capacity or long-duration stability.

The pre-capacity CLI executable is compared against the original starting-commit binary
in twenty alternating fresh-process pairs for six native/house compile views.
Complete stdout hashes match. Paired median wall ratios are 0.754–0.792,
process CPU ratios 0.736–0.788 and maximum RSS ratios 0.965–0.973; every interval
is below parity. Native views take about 50–51 ms versus 66–67 ms; house views
about 57 ms versus 71–74 ms. `cli-final-pairs` records all 240 invocations and
binary identities. These are warm-filesystem CLI commands, separate from Go edit
activation and request latency.

After the compiler-capacity refinement, the final executable repeats both
boundary comparisons. `cli-complete-pairs` retains 240 byte-identical CLI outputs:
wall ratios 0.752–0.789, CPU 0.735–0.794 and maximum RSS 0.965–0.975, all intervals
below parity. `agent-stdio-complete-pairs` retains another 860 matching responses
and clean exits. Native stdio latency ratios are 0.512–0.607; house is
0.437–0.528, with all intervals below parity. Single-resource read medians are
9.555 → 5.380 ms and 22.989 → 10.630 ms. Context medians are 17.139 → 8.615 ms and
29.581 → 13.313 ms. Process CPU/request ratios are 0.604/0.542, maximum RSS
0.968/0.925. These final cohorts supersede the preceding boundary timing numbers
for the final-source claim; their scope and warmup rules are unchanged.

The isolated copy-buffer HTTP stage improves CPU/request to 0.985 and allocation
to 0.975; p95/RSS intervals span parity. The isolated redaction stage improves
CPU/request to 0.810 and allocation to 0.519; its RSS interval spans parity.
Retain these separate causal measurements alongside the combined result.

Real edit activation is measured by the existing verifier benchmark. In the first
30-pair cohort, p50 is 751.795 → 723.671 ms, p95 812.759 → 890.170 ms, paired median
0.9573 [0.9437, 0.9735]. In the independent confirmation, p50 is 764.733 → 729.245 ms,
p95 1,314.131 → 894.546 ms, paired median 0.9577 [0.9263, 0.9792]. Exact generation
and endpoint behavior pass. The first adverse tail includes a 1,091 ms Go command;
broader host/IO interference is an inference, not a proven cause. Both cohorts and
cleanup are retained in `edit-latency-checkpoint-5.json`, its analysis,
`edit-latency-confirmation.json` and `edit-latency-cleanup.json`.

Independent baseline/candidate output identity:

| Surface | Cases / invariant | Evidence |
| --- | --- | --- |
| Graph/context | 18,177 queries, 9,177 pages, 5,520 expected errors; exact continuation bytes at fixed time | `query-hash-parity.txt` |
| Real native/house graphs | 22/56 resources; eight complete context hashes | Real-graph probe outputs |
| Schema catalog | 153 schemas and 548,407 catalog bytes; original exact revision | `schema-hash-parity.txt` |
| Lossless source | 29 sources, 16,590 tokens, seven diagnostics | Source parity outputs |
| Encoding negotiation | 135,828 cases, 123,236 expected errors; compressed bytes included | Encoding parity outputs |
| Media negotiation | 112,014 cases, 105,013 expected errors | Media parity outputs |
| Unicode codecs | 320 contract and 320 canonical typed/raw mixed-key cases | Object parity outputs |
| Generic graph revisions | 78 cases including unsupported inputs | `graph-revision-parity-baseline.txt`, candidate counterpart |
| Agent reads | 5,328 responses, 3,044 expected errors; views, repeats, aliases, eviction | `agent-read-parity-baseline.txt`, candidate counterpart |

Further adverse controls retained: exact JSON fast path 1.007 [0.995, 1.016],
Unicode parser 1.029 [0.992, 1.060], canonical escaped strings 1.018 [0.974, 1.044],
and isolated graph small hash 0.999 [0.979, 1.009]. Unchanged allocation is not
presented as a CPU gain. `plan-before-consolidation.md` retains chronological
checkpoint details; final summaries and raw cohorts are the metric source of truth.

## Interfaces and Dependencies

No public interface, schema, dependency or compatibility alias is added. Revision
preimages, canonical bytes, CLI/JSON envelopes and generated artifacts remain
stable. The only observable correction is valid preservation of literal JSON
separator escapes already required by the contract. All retained reusable state
is explicitly bounded and owned by existing packages.
