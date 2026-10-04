# Performance Engineering First Session

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log and Outcomes & Retrospective current throughout the session.

## Purpose / Big Picture

Begin Petr's multi-week efficiency effort with at least five hours of active
work from 2026-09-30 21:57:08 UTC through no earlier than 2026-10-01 02:57:08 UTC.
Improve CPU cost, allocations, stability and latency without weakening current
identity, freshness, ownership or public behavior. Keep useful small gains when
they simplify the implementation; do not add a speculative cache, tuning knob
or persistent format to obtain a benchmark result. The human authorizes local
implementation, isolated workspaces and targeted native before/after benchmarks
and explicitly requires work without subagents.

## Progress

- [x] 2026-09-30 22:06 UTC: Verify the clean primary checkout and preserve both
  completed performance increments in a before-session worktree.
- [x] 2026-09-30 22:59 UTC: Establish native watcher, filesystem metadata and workspace hashing
  baselines with retained raw samples and profiles.
- [x] Improve the measured paths in independently reviewable increments;
  prove exact-output equivalence and retain meaningful stability coverage.
- [x] 2026-09-30 22:54 UTC: Validate the first hash/glob/metadata/generated-path
  increment with full verification, affected race tests, lint and native
  `dev-process` plus `build-info` probes.
- [x] 2026-09-30 23:40 UTC: Implement and test ignore-path temporary storage,
  physically current parsed-rule reuse, canonical-container validation and
  allocation-free Unicode ordering, plus content-only embed-pattern caching.
- [x] 2026-09-30 23:55 UTC: Validate the ignore/canonical/embed increment with
  both generated clients, full verification, lint, affected races and native
  `dev-process` plus `build-info` probes. Preserve advisory warning details.
- [x] 2026-10-01 00:18 UTC: Measure exact CST token capacity: preserve nine
  complete CST/diagnostic manifests and pass affected source/compiler/evolution
  and CLI tests. Measure integrated compilation and final acceptance next.
- [x] 2026-10-01 00:24 UTC: Pre-size address indexes and reuse the assistant
  validator's existing index throughout its immutable input traversal. Retain
  paired benchmarks and identical empty/multiple/colliding-route diagnostics.
- [x] 2026-10-01 00:40 UTC: Validate the CST/index increment with regenerated clients, full checks,
  affected races and native build/watch probes.
- [x] 2026-10-01 00:30 UTC: Preserve the failed native proof and successful
  unchanged rerun. Synchronize handoff link-evidence reading with its already
  required terminal build event; rerun the exact probe after this change.
- [x] 2026-10-01 00:53 UTC: Prove 18 affected roots below 100 ms p95 with 20
  serial fresh-process samples per exact root (maximum 42 ms). Fix the retry
  test's scheduler-dependent clock using existing test seams; its mutation and
  safe-method roots pass race and 20-process p95 at 3 and 10 ms. Preserve the
  initial failure and 20 unchanged isolated passing reproductions.
- [x] 2026-10-01 01:10 UTC: Reuse the HTTP validator's address index for stream
  outcome checks and construct it through the pre-sized helper. Four complete
  diagnostic manifests match; eight-stream helper samples improve without a
  new cache. Whole compilation saves another 1.64 MB; its timing is uncertain.
  Both fixture generations, full verification, affected race tests, lint and
  the current native build/watch probes pass at 01:29 UTC.
- [x] 2026-10-01 02:40 UTC: Complete five source-bound 30-minute runtime
  observations, including the identical-observer before-session/final HTTP pair.
- [x] 2026-10-01 03:07 UTC: Complete the 10-minute after-candidate control,
  preserve inconclusive raw CPU/latency comparisons, and pass normal shipping
  producer acceptance, app checks and Go suite. Restore the current selection
  and stop only the owned runtime, retaining its data.
- [x] Complete changed-area validation for unchanged final product source
  `sha256:6b03ea329b608d7ee68ec2ac78a4a232886811056f669316cbb855dcafb1fac6`;
  record remaining costs and raw runtime risks. Closure refreshes the current
  classifier and documentation evidence once more.
- [x] 2026-10-01 02:57 UTC: Fulfill the minimum five-hour working interval.
- [x] Publish the first-session outcome below with native raw artifacts and
  measured next-session leads. The primary checkout and main ONLV owner are
  preserved; no commit, push, install or release was requested.

## Surprises & Discoveries

The preceding owned ONLV profile attributed 52.6% cumulative sampled CPU to
watch membership traversal. The paired application observation did not prove
lower total CPU or retained heap. Current source still reads file status-change
timestamps through reflection in the watcher, directory listings and build
input hashing. Glob path splitting and workspace-revision length framing also
allocate in repeated work. These are candidates, not established improvements.

The host has approximately 17 GiB free at preflight. Keep artifacts bounded and
reuse existing native toolchain/cache provisioning; do not delete unrelated
workspaces, applications or data to increase benchmark headroom.

The 833-directory, 576-watched-file, 677-revision-file ONLV snapshot costs a
median 54.72 ms and 7,740,664 bytes / 80,107 allocations before this session.
After canonical length framing, local glob storage, native metadata access and
delayed generated-path joins it costs 54.52 ms and 6,082,424 bytes / 61,328
allocations. The allocation reduction is established; the timing difference is
too small to claim an improvement. CPU samples are dominated by filesystem
calls, not the length-framing helper. At 5,000 inputs, canonical hashing falls
from 10,006 to 5 allocations with exact golden digests unchanged.

The ignore-rule cache previously deliberately accepted same-size rewrites with
restored mtime. Its existing test documented that stale result. The current
increment strengthens it to full physical identity and status-change time,
with before/after metadata confirmation when parsed rules are stored. Local
path storage and parent-key lookup remove per-decision split/join allocations
without enlarging the memoized parent map or retaining complete entry paths.

Canonical validation of the table-page schema falls from 1,360 allocations /
21,760 bytes to 43 / 688 by avoiding reflection for ordinary JSON containers.
Typed containers, struct fields, pointers, byte slices and invalid UTF-8 remain
covered. The writer still normalizes through the same standard JSON codec and
uses the same number/string encoding. Unicode ordering compares decoded runes
and leading surrogates against an independent UTF-16-unit oracle. Its final
ASCII-prefix path and generic key sorter are being measured; no digest or
semantic revision is intentionally changed.

The embed-pattern cache used only `stamp.hash` but retained `stamp.data` and all
other stamp fields. It now retains the hash and immutable patterns alone.
A 512-file experiment releases all caller-owned 64 KiB source buffers before
GC: old retained heap is about 33.7 MB, new about 110 KB. Fixture creation and
its different GC work make that experiment's wall time unsuitable as a cache
operation comparison; separately measure hits/stores with source still held by
a snapshot. This does not claim that an unchanged live snapshot releases its
own necessary source buffers.

The full ONLV compile allocation profile attributes 17.1% of sampled flat
allocation bytes to lossless CST construction. Geometric token-slice growth
copies large records and retains spare records. An exact count of lexer tokens
and omitted trivia gaps reduces a 23,000-token source parse from 35,022,536.5
to 24,521,061 allocated bytes and from 20.87 to 18.17 ms (eight native samples).
Token byte ownership remains unchanged; an empty tree retains its nil slice.
Complete serialized CST and diagnostic hashes match the baseline for nine
valid/recovered/Unicode/CRLF/invalid-byte cases. Whole-compile impact remains to
be measured separately. HCL's exported API does not expose the parser's lexer
tokens; removing its repeated lexing would require a fork/private API and is
outside this increment.

The CST increment reduces complete ONLV compilation from 503,816,108 to
447,704,940 allocated bytes relative to a freshly repeated phase-two control.
Medians of eight whole-compile samples are 564.61 and 562.12 ms: this small
timing difference does not establish faster whole compilation. The original
before-session compile allocates 511,565,280 bytes / 3,522,843.5 objects.

Address-index construction accounts for another 5.6% of sampled allocation
bytes. Pre-sizing its known input length reduces a 5,000-resource index from
1,476,664 to 1,258,480 bytes and 863.21 to 653.66 microseconds. The assistant
path validator previously rebuilt the whole index for each assistant, although
it already needed the same index for route collision checks. Reusing that
single local index (not retaining a new cache) reduces an eight-assistant,
500-other-resource synthetic case from 974.37 to 95.13 microseconds and
1,446,604 to 139,524 bytes. These are scoped helper measurements; full compile
and runtime effects are measured independently.

The phase-three `dev-process` probe first failed with no completed build
operation in its log excerpt, then passed unchanged. The newly served response
can precede the terminal `build.request` event; the probe inspected link steps
immediately after that response. It now explicitly waits for that operation's
successful completion before checking the same current-operation private-link
assertions. This preserves all existing conditions and the original measured
edit-to-response boundary, without sleeps or enlarged timeout budgets.

The final source review found that the watcher metadata test skipped its
synthetic cases when native change-time was unavailable. It now supplies a
synthetic previous identity in that case, keeping conservative missing-field
and replacement checks active on other platforms. Native platform performance
is still measured only on macOS; cross-compilation is not runtime proof.

A later full CLI package run failed a pre-existing HTTP retry test at 70 ms
with a synthetic 50 ms wall-clock budget; 20 unchanged isolated repetitions
passed at no more than 10 ms. The real loopback failure's scheduler delay could
consume the test's budget before its first retry. The test now uses the existing
invocation-local clock/wait seams, advancing time only on retry waits. The
actual dropped connection, exact mutation/safe request counts and budget value
remain asserted; production clocks, timeouts and retry behavior are unchanged.

The HTTP validator had its own unsized index and reconstructed another index
for each streaming result response. Its immutable validation input already
provides the needed lookup. Constructing one pre-sized map and passing it to
the stream validator reduces an eight-stream, 500-other-resource case from
722.68 to 143.99 microseconds, 1,265,773 to 166,828 bytes and 5,324 to 1,184
allocations. Full diagnostic hashes match for valid, invalid-codec,
duplicate-route and empty inputs. The existing stream ABI tests retain their
assertions with the explicit lookup argument. This is an additional compiler
increment; the first `final` runtime observation predates it and must not be
reported as its producer.

The first three 30-minute runtime observations retain 18,000 successful
identity-attested requests and 48 unchanged process identities each, with
supervisor descriptor deltas of 0, -1 and +1. Supervisor allocation rates fall
from 5.73 MiB/s before this session to about 4.55 MiB/s in both candidates;
post-GC heap stays near 151–154 MiB rather than growing without bound during
these intervals. This is scoped stability evidence, not a general leak proof.

The pre-HTTP final observation has substantially worse request tails during
its first 20 minutes but returns to roughly 6 ms p95 in its last 10 minutes
without a producer change. Host load reaches 58.46, versus 19.83 in the first
baseline. Known-task exclusion alone does not remove this uncertainty. Keep
all original samples, avoid attributing the delay to the implementation, and
repeat the before-session and final HTTP sources with an identical observer
that also times DNS and records the host's top CPU processes. Do not interfere
with unrelated workloads or label them an established cause.

The repeated before-session control completes another 18,000 requests with
zero errors, 48 stable processes and no descriptor delta. Its allocation rate
is 5.73140 MiB/s and 74 GC cycles, essentially identical to the original
5.73138 MiB/s and 74 cycles. Raw request p50/p95 is 2.805/5.741 ms. DNS timing
accounts for 119.48 ms of the worst 125.81 ms request in this repeat; the
non-DNS remainder's worst request is 29.31 ms. This establishes an observed
DNS tail in the new baseline, not the cause of old uninstrumented candidates.
All samples and five-minute distributions are retained. The matching final
HTTP-source observation is next.

A task-only framework-switch script initially treated the doctor's exit 3
as an unexpected code even though its JSON contained only the same two
pre-existing production-token findings. Correct the accepted code, retain the
failed helper invocation and continue from the prepared source without
repeating the already completed stop/pin/generation steps. This is an
orchestration mistake rather than a product or doctor regression.

The matched final HTTP cohort completes 18,000 successful requests with
48 stable process identities. Allocation rate falls from 5.73140 to
4.56490 MiB/s (20.35%), allocations/s from 46,682.6 to 31,843.9 (31.79%),
and GC from 74 to 59 cycles. Post-GC heap moves 150.92 to 151.55 MiB;
descriptor delta is -2. Its raw HTTP p50/p95 is 3.554/6.372 ms versus
2.805/5.741 ms, and known-task-free supervisor CPU median is 8.016% versus
7.099% of one core. Record the worse raw values rather than claiming no
performance regression. A short same-observer baseline after the candidate
will check temporal host drift; normal shipping-producer app check and harness
already pass. Exact producer/source equivalence and contract revision remain
asserted. This final allocation benefit does not by itself prove lower CPU.

## Decision Log

- 2026-09-30, Petr: Choose useful performance, allocation, stability and timing
  work autonomously for at least five hours as the first multi-week increment;
  prevent regressions and avoid trading small improvements for technical debt.
- 2026-09-30, Codex: Continue in the existing candidate worktree and snapshot its
  exact current source before editing. Preserve completed plans 0208 and 0209.
- 2026-09-30, Codex: Start with the previously observed watcher cost and its
  repeated metadata/glob/hash helpers. Use current repository contracts rather
  than obsolete command spellings in the historical read-only audit notes.
- 2026-09-30, Codex: Leave directory-entry caching unchanged: eliminating its
  interface boxes by retaining larger entries would increase held memory.
  First remove temporary path allocations with no retained-memory tradeoff.
- 2026-09-30, Codex: Add paired, 30-minute native runtime observations after a
  five-minute warmup, using 10 read-only platform.Stats requests per second.
  Record cumulative allocations/GC, process identities, CPU and request tails;
  report longer-duration stability separately from microbenchmark timings.
- 2026-09-30, Codex: Optimize canonical validation and ordering without
  replacing standard JSON normalization or adding a second serializer. Retain
  typed/Unicode/UTF-8 equivalence coverage and all current revision identities.
- 2026-09-30, Codex: Remove source ownership from the embed-pattern cache rather
  than introduce a pool or cache lifecycle mechanism to mask retained buffers.
- 2026-10-01, Codex: Allocate exactly the lossless CST's existing token count;
  preserve independent token bytes and complete output. Do not trade larger
  retained capacity or a parser fork for timing improvements.
- 2026-10-01, Codex: Pre-size temporary address indexes and reuse only the
  assistant validator's already-required local index. Its source resource
  collection is unchanged throughout validation; no cross-phase cache or new
  ownership authority is introduced.
- 2026-10-01, Codex: Fix the observed native evidence-read race by waiting for
  completion through the existing bounded helper. Do not suppress the failed
  proof, weaken private-link assertions or expand timeouts.
- 2026-10-01, Codex: Isolate retry policy testing from host scheduling with the
  existing fake clock. Keep actual connection-failure assertions and bounded
  simulated time. Keep portable synthetic metadata coverage active.
- 2026-10-01, Codex: Reuse the already-owned HTTP validation index for stream
  outcomes. Preserve immutable resource traversal, all validation branches and
  diagnostic ordering; do not add a retained graph index.

## Outcomes & Retrospective

Completed the first session after more than five hours from 21:57:08 UTC to
03:07 UTC. The code has measured gains without a new dependency, runtime knob,
persistent format, parser fork or retained graph cache. Complete ONLV
compilation improves 7.48% by median paired ratio (eight pairs, candidate wins
all eight), with 13.73% fewer allocated bytes and 10.53% fewer allocations.
Complete watcher snapshots allocate 31.95% fewer bytes and about 37.65% fewer
objects; their time gain is not established. Exact CST capacity, canonical
container/order paths, content-only embed caching and local validation-index
reuse have separately recorded equivalent outputs and scoped gains.

The matched 30-minute runtime observations establish 20.35% fewer supervisor
allocated bytes/s, 31.79% fewer allocations/s and 59 rather than 74 GC cycles.
Each of the five long cohorts has 18,000 successful identity-attested requests,
48 stable process identities and bounded descriptor/after-GC heap observations.
The after-candidate 10-minute baseline adds 6,000 successful requests; its
allocation rate returns to 5.73433 MiB/s. Its CPU median rises to 7.744% of one
core from the prior baseline's 7.099%, versus the candidate's 8.016%. Raw
candidate HTTP p50/p95 is 3.554/6.372 ms, prior baseline 2.805/5.741 and later
short baseline 2.830/5.583. These values do not prove lower whole-application
CPU or HTTP latency and do not resolve every possible performance regression.
All observations, including the worse pre-HTTP cohort, remain in the report.
Short idle CPU profiles are dominated by filesystem calls; their hundreds of
milliseconds of CPU samples cannot establish a causal explanation.

The current non-profiler shipping producer passes app check, app harness,
`go test ./...`, and 3,000 HTTP requests over five minutes: no errors, all 48
start identities stable, descriptor delta zero, observed p50/p95 2.800/5.735 ms.
Restoring the same source/executable digest adds 12 successful responses before
stopping only the owned runtime. The primary Scenery checkout stays clean at
the original commit; main ONLV keeps PID 22727 and its Sep-30 11:55:58 start,
with only its pre-existing dirty go.mod. Data and worktrees are retained.
No general leak, CPU, reload or UI frame-time claim is inferred from these
scoped proofs. Continue the next session from the recorded allocation leaders.

Implementation-only changes intentionally leave instruction documents,
CLI/JSON schemas and public generation contracts unchanged. Architecture
records the small stat helper boundary, and the local contract records fresh
ignore-rule identity. Earlier UI/report/PostgreSQL/storage changes are
preserved rather than edited again. This session fixes two observed test/probe
scheduling races without larger budgets or weaker assertions. Required product
checks and reused byte-identical boundary evidence are listed below; closure
verification writes the final current classifier and receipt.

## Context and Orientation

Candidate: `/Users/petrbrazdil/.codex/worktrees/performance-review/scenery`,
branch `perf/runtime-resource-efficiency`. The primary Scenery checkout stays
clean at `a1fd9485356a5e7a0d117ce62870a300296bd5ab`. Before-session:
`/Users/petrbrazdil/.codex/worktrees/performance-session1-baseline/scenery`,
56 copied changed files with digest
`eec75c1344b357746b6ebd34d4c785d4c819a6fd1e657653f8cf3ad06848468c`.

The CLI owns repeated watch snapshots. `internal/dirlisting` owns bounded,
freshness-checked directory membership. `internal/compiler` owns declared
workspace membership and canonical workspace hashing. `internal/build` owns
exact captured source and build-input identity. A helper optimization must not
grant new reuse authority or replace current content-bound admission.

Use the existing owned ONLV fixture at
`/Users/petrbrazdil/Repos/onlv-scenery-perf-followup`, fixture
`e2f9fd30-b2c2-40eb-a506-3b5c95d45e36`. Its task-owned profiler runtime is
is now stopped with its data retained and the normal current framework
selection restored. The completed final HTTP snapshot's selected source is
`sha256:da9f252a00b57cbc97ece6b21c9726b8c8ec9bbe8e41bfd00e02ab9622df4520`,
executable `sha256:4e55828fa57497435b63276d369fce6b2c9e924145acd9a7c084de023162d6fa`.
All 1,076 source build-input entries match the immutable profiler worktree;
the desired origin and the materialized runtime source are distinguished in
`final-http-framework-attestation.json`. The identical observer is
`8219b3c8701d084bdae8a84498bf69d66f1662389575da33f7f6aee0c7404e4a`.
Original baseline, phase-two, pre-HTTP final, repeated baseline and final HTTP
observations are complete and retained with exact producer manifests. The
normal shipping producer has passed app check/harness, `go test ./...` and
3,000 HTTP requests over five minutes without a hook, identity change, error
or descriptor growth. Its observed p50/p95 is 2.800/5.735 ms; this acceptance
interval is shorter and includes initial app checks, so it is not a replacement
for the paired 30-minute profile comparison.
Main ONLV is unrelated running work with a pre-existing dirty go.mod; do not
edit, restart or repin it.

## Milestones

1. Establish per-operation and whole-scan costs from the exact before-session
   source; screen simple changes against correctness and allocation profiles.
2. Implement measured improvements and strengthen demonstrated stability gaps
   without changing public formats or adding independent runtime mechanisms.
3. Validate real captured-input/build behavior and controlled runtime samples,
   then record the five-hour session's achieved and unmeasured outcomes.

## Plan of Work

Measure before editing. Use ignored benchmark overlays for expensive native
filesystem workloads, with setup and normalization outside the timer. Repeat
samples serially under the same native macOS Go toolchain; record medians,
allocation counts, raw ranges and exact source identities. Compare unchanged
controls when filesystem or runtime variability could explain a small gain.

Keep structural changes proportional to measured costs. Shared metadata access
may remove duplicate reflection only if it preserves unknown-platform and
missing-change-time conservatism. Matcher/hash changes require independent
equivalence proof. Do not reduce watch coverage, skip file freshness checks,
weaken generated ownership or retain unbounded caches for better timings.

## Concrete Steps

Commands below run in the candidate root unless another root is explicit.
Before-session operation measurements run the same overlay from the baseline
root. Save raw evidence under `.scenery/harness/perf-session1/`.

Run targeted native `go test -overlay <recorded-overlay.json> -run '^$'
-bench <recorded-benchmark-expression> -benchmem -benchtime=300ms -count=8
-cpu=1 ./cmd/scenery` and the corresponding compiler/build helper package
commands. Choose exact expressions before each comparison and record them in
the artifact manifest. These explicitly requested benchmarks are not ordinary
tests. Do not run an all-root timing audit or release gate by inference.

For runtime observation, use the owned app's `just framework-use --source
<exact-source-root>`, `./scripts/scenery generate --target
typescript_client.public_api -o json`, `./scripts/scenery doctor -o json`,
`./scripts/scenery up --detach --wait ready -o json`, and fresh
`./scripts/scenery ps -o json`. Verify the served producer and every observed
PID's start/executable identity before comparing read-only workload samples.
Use `./scripts/scenery down -o json` to stop only that owned session.

The longer runtime sample uses an isolated profiler source snapshot with one
task-owned Unix-socket hook, absent from the shipping patch. Preserve the
source manifest and hook digest independently. Save minute-level observations
and request samples; force GC only before/after the measured interval. Record
other task builds/tests and host load so contaminated CPU/tail intervals can
be distinguished. A stable longer run is scoped evidence, not proof of every
possible leak or workload.

## Validation and Acceptance

Expected classes start with `go-package`, `compiler-or-generator` and
`release-sensitive-or-runtime`, in addition to the existing candidate's CLI
JSON and UI classes. Select full directly: `go run ./scripts/verify --summary
--write`, inspect its final `changed_area.validation_classes` and fulfill the
union of `changed_area.recommended_commands`. Its successful repository Go
suite satisfies `go test ./...` and the recommended package roots for the same
source inputs. Run `golangci-lint run ./...`.

For compiler changes run `go test ./internal/compiler ./internal/parse`, then
both committed client generations:

    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json

Run affected CLI/build/directory tests with `go test ./cmd/scenery
./internal/build ./internal/dirlisting`, and focused race coverage with
`go test -race ./cmd/scenery ./internal/build ./internal/dirlisting
./internal/compiler`. Exact-output tests must cover path/rune/glob edge cases,
canonical framing, changed file identity and missing metadata. Native process
and expensive filesystem proof stays in explicit probes/benchmark overlays.

Watch/build changes select `go run ./scripts/verify --probe dev-process --probe
build-info --summary --write`; assert exact served generations, captured-input
invalidation and cleanup in its report. Additional external boundaries are
added to this plan before their probes are executed. Reuse the preceding UI,
PostgreSQL and storage evidence only while their source/boundary inputs are
unchanged; no pass is inferred for newly changed code.

If no UI/catalog/client implementation changes in this session, retain the
preceding catalog/client typecheck and browser acceptance identified in plan
0209 and record that source hashes are unchanged. If that observable condition
fails, run both catalog/client tsc configurations, the current Bun client/table
tests and `--probe ui`, and verify the newly generated catalog in native Chrome.
No source change authorizes a global install, commit, push or release gate.

## Idempotence and Recovery

Keep the before-session source immutable. Every overlay and temporary profiler
hook is task-owned and absent from the shipping patch. Stop only verified owned
processes; preserve their data and worktrees. Interrupted samples remain
excluded rather than relabeled as results. Failed correctness checks are fixed
before a performance candidate is accepted. If an experiment fails its measured
or safety criteria, remove only that experiment's edits and retain earlier
authorized increments.

## Artifacts and Notes

`baseline-manifest.json` and `baseline.patch` bind this session to the completed
first series and followup. Keep benchmark scripts, sample logs, profiles,
validation reports and runtime producer records under the ignored session
artifact directory. Update this plan at each meaningful stopping point and
retain an explicit remaining-work list until the time and acceptance conditions
are both fulfilled.

### Recorded benchmark summary

Eight native serial samples per row use identical overlays and fixed fixture
inputs. Time is a median, and bytes/objects are allocated per operation. The
seeded resampling intervals in `benchmark-comparison.json` describe sample
variation only; they do not remove sequential-cohort host confounding.

| Operation | Before time | After time | Before bytes | After bytes |
|---|---:|---:|---:|---:|
| Complete ONLV watch snapshot | 54.72 ms | 53.60 ms | 7,740,664 | 5,267,120 |
| Complete ONLV compilation | 583.29 ms | 537.97 ms | 511,565,280 | 441,310,084 |
| 23,000-token lossless source parse | 20.87 ms | 18.17 ms | 35,022,536.5 | 24,521,061 |
| 5,000-input canonical revision | 1.605 ms | 1.357 ms | 162,288 | 82,160 |
| Table-schema canonical JSON | 564.49 us | 484.89 us | 239,442 | 211,284 |
| Catalog canonical JSON | 7.209 ms | 6.668 ms | 3,404,369.5 | 3,124,629.5 |
| Table-schema input validation | 63.81 us | 24.42 us | 21,760 | 688 |
| Eight-assistant path validation | 974.37 us | 95.13 us | 1,446,604 | 139,524 |
| Eight-stream HTTP validation | 722.68 us | 143.99 us | 1,265,773 | 166,828 |

The whole watcher time interval includes no improvement. Whole compilation
has fewer allocations in every phase; its incremental matched controls show
small uncertain timing changes. Preserve these distinctions when reporting
results. The embed cache retention experiment separately drops approximately
33.7 MB of retained caller-owned buffers to 0.11 MB after GC; its fixture wall
time is not a cache-operation timing result.

The final eight-pair whole-compile control randomizes each pair's lane order
(seed 210), uses one fresh native test process per sample, and verifies the
unchanged fixture go.mod and identical benchmark source. Before/after medians
are 596.33/550.30 ms, 511,575,996/441,317,256 bytes and
3,522,938.5/3,151,813 allocations. Candidate wins all eight time pairs; the
median paired time ratio is 0.92524 (7.48% lower), bytes 0.86266 and allocations
0.89466. This reproduces the cumulative compile gain at the fixed complete
ONLV scope. It does not establish a reload, HTTP latency or total application
CPU improvement. The small incremental HTTP timing remains uncertain.

### Validation ledger so far

- `go run ./scripts/verify --summary --write`: PASS after each implementation
  increment; `full-6.json` includes repository `go test -json ./...`, vet,
  current schemas and drift checks. It has 37 knowledge and 19 architecture
  advisories, plus a 6.636-second aggregate cached-suite advisory. No failed
  step is converted into a pass.
- `go test ./internal/compiler ./internal/parse` and affected source,
  evolution, build, directory, watcher and CLI packages: PASS. The final full
  suite satisfies the classifier's package-root commands for current inputs.
- Both exact fixture-generation commands above: PASS after the final HTTP
  change (`native-generate-4.json`, `house-generate-4.json`). Committed client
  contents still match the before-session source.
- `golangci-lint run ./...`: PASS, zero issues (`lint-6.log`).
- `go test -race ./internal/scn ./internal/compiler ./internal/evolution
  ./cmd/scenery ./internal/build ./internal/dirlisting ./internal/spec
  ./internal/watchignore`: PASS for the CST/index increment. Subsequent CLI
  clock correction, verifier evidence synchronization and HTTP change have
  their own passing current `-race` runs (`router-retry-stability-race-fixed.log`,
  `verify-race-fix-race.log`, `stream-race.log`). Original failed attempts remain retained.
- `go run ./scripts/verify --probe dev-process --probe build-info --summary
  --write`: PASS for final product source (`build-watch-probes-5.json`);
  current-operation link assertions, unique edits, captured input identity,
  cgo boundary and process cleanup remain asserted.
- Eighteen affected exact roots: 20 serial isolated fresh-process samples each,
  maximum p95 42 ms. Three corrected stability roots: p95 2, 3 and 10 ms,
  20 samples each. Use the existing repository confirmation engine; Go JSON
  rounds sub-millisecond measurements rather than proving zero time.
- `scenery logs --limit 500 -o jsonl`: current profiler session yields 501
  valid identity-bearing envelopes including its terminal envelope; retained
  as `final-soak/runtime-logs.jsonl`.
- Catalog/client TypeScript, UI, PostgreSQL and storage boundary acceptance
  from plan 0209 is reused only for byte-identical inputs, independently
  checked in `unchanged-boundaries.json`. This goal does not introduce a UI
  change or a new browser acceptance claim.
- Linux amd64/386 and FreeBSD amd64 metadata helper compilation: PASS. These
  are portability checks, not native performance or runtime measurements.
- `git diff --check`: PASS for current source. No global installation,
  commit/push, release certification or all-root timing audit is requested
  or performed.

### Next session priorities

Use the exact pre-HTTP compiler allocation profile as a starting observation,
then refresh attribution for the source chosen in the next session. It records
19.25% flat bytes in HCL token emission, 7.23% in CST construction, 5.64% in
`maps.clone`, 5.35% in address indexes and 5.11% in resource value cloning.
The profile includes setup and five timed compilations; it is attribution
rather than a normal benchmark wall-time comparison.

1. Review index ownership and repeated resource cloning within immutable
   compiler phases. Prefer another local reuse/removal with exact diagnostic
   equivalence over a retained graph cache or mutable shared authority.
2. Examine token/byte ownership and parser work through exported HCL APIs.
   Keep lossless CST, diagnostics and independently owned bytes; a fork or
   hidden lexer integration needs a separate justified decision.
3. Refresh steady-state watcher CPU attribution. Filesystem calls dominate
   complete scans. Any membership optimization must preserve current physical
   identity, freshness, traversal coverage and captured input admission.

These are measured leads for continuing the multi-week effort, not promised
performance gains or unvalidated changes in this session.

## Interfaces and Dependencies

Use the Go standard library and existing project dependencies. Keep one current
compiler/runtime/protocol. Public CLI, JSON, source and storage formats remain
authoritative; implementation-only changes require no instruction/contract
rewrite. If a changed package boundary or public behavior requires one, update
its owning living architecture/contract document in the same increment.
