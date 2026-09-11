# Native Worker Findings and Review Handoff

Status: supervisor preparation transfer completed for draft PR #193; **stop the
worker product migration at the 0183 decision gate**. Current findings are from
2026-09-11. Six matched lifecycle pairs remain positive, but most of 0182's
636 ms advantage disappears. Do not start GenerationCandidate or broader runtime
parity work from this result. The full development-loop 50% goal remains open.

## What the diff implements

The earlier runtime cut separates application-facing `scenery`, `auth`, `db`,
`durable` and native `runtime` from `runtime/host`. Shared native composition,
service lifecycle, SQL and request context retain their existing owners.
[0182](plans/0182-direct-native-worker-preparation.md) then selects public plus
worker/kernel projection before materializing an explicitly owned workspace.
Validated adapter metadata is shared; ordinary adapter source, composition and
entrypoint are absent from that candidate.

[0183](plans/0183-native-worker-supervisor-lifecycle.md) shares
`devBuildPreparation` with `prepareDevRuntimePlan`. The private
`scenery_native_lifecycle_probe` build of the CLI package calls the actual
watcher, passes `SourceSnapshot`, and keeps one preparation process and workspace
per arm through all edits. Both paths share graph-cache selection, preparation,
metadata analysis, compilation admission and final watcher recapture. A source
change during compilation rejects the candidate without swallowing its pending
edit or replacing the accepted native graph.

Native cache selection is explicit. Its accepted graph is process-owned;
`RefreshNativeExperiment` uses only its selected private workspace and renderer,
creates fresh full verification and reuses preparation hints and process caches.
Ordinary cached refresh rejects native results. No ordinary successful-build state
is loaded into the native owner. An unchanged cache hit is separately proved;
it is not an optimized native unchanged-start path or a startup measurement.

No product CLI flag, environment switch, persistent kernel or new runtime
protocol is introduced. The ordinary product runtime remains the single existing
executable. The experimental kernel admits one supported unary HTTP binding and
never forwards other routes to an old application.

## Six matched lifecycle pairs

The order was fixed as AB, BA, AB, BA, AB, BA before measurement. A is the
ordinary shared preparation with matched full post-build checks; B is the direct
worker. Both arms receive identical semantic source bytes and capture identical
watcher fingerprints in every pair. Two warmups are reported separately; all
12 measured samples succeed and none is discarded.

| Boundary | Control median | Worker median | Difference of medians | Median paired gain |
| --- | ---: | ---: | ---: | ---: |
| Captured edit → checked retained artifact | 3546.048 ms | 3372.035 ms | 174.013 ms / 4.91% | 159.217 ms |
| Captured edit → authenticated SQL response | 4847.965 ms | 4612.995 ms | 234.970 ms / 4.85% | 213.119 ms |
| Authored write → authenticated SQL response | 5018.746 ms | 4790.146 ms | 228.600 ms / 4.55% | 211.040 ms |

Every pair favors the worker at the first two boundaries. The authored-write
paired gains range from 81.018 to 527.331 ms. The preparation owners keep their
PIDs for the complete series; all six worker generations start fresh worker and
kernel processes. No new generation executable runs before its measured proof.

The artifact timestamp is taken after full verification, both input captures,
retention and the shared final watcher check. It excludes diagnostic receipt
bookkeeping. The SQL interval additionally includes the private receipt handoff,
proof, activation and the supported request. Artifact-to-receipt medians are
308.549/350.797 ms, including generated-baseline bookkeeping, pending-path
observation and JSON evidence. These are probe costs, not an established product
runtime cost. The independent artifact boundary prevents attributing them to
build savings.

The measured preparation medians are now 769.414/843.396 ms: the old preparation
advantage does not survive the lifecycle. The joined build/verifier medians are
1762.894/1592.983 ms, so a smaller build advantage remains. Verifier work overlaps
that branch and cannot be added to its saving. Component medians are not
additive. The full six-pair and 12-sample tables are in 0183.

## Correctness and ownership evidence

Independent current dependency captures preserve exactly 166 native packages
and 215 operation identities. Each pair preserves all 509 app/native, embedded
and public-projection input identities **and identical bytes**, without an
intended-edit exception between arms. Complete input counts remain 1628/1631:
50 ordinary private inputs are replaced by 53 selected worker/kernel inputs.
Both arms perform two full input discoveries. A fresh independent kernel-only
capture matches all 687 projected entries, including source and producer hashes.
Complete authored target patterns
and default plus selected ABI verification remain mandatory.

Six distinct worker artifacts and one checksum-verified kernel artifact are
retained. The kernel artifact is reused only after current input/target and full
byte checks, then launched as a new process each time. Native preparation state
contains only version, dependency fingerprint, source stamps and generated-file
hints. Its ordinary latest-build manifest is unchanged. Every preparation and
application process exits successfully and the owned PostgreSQL container is
removed.

Both arms pass source mutation inside the actual compile join, mutation before
the final watcher recapture, and an invalid Go body in an unrelated native
package followed by repair. Failed attempts preserve the accepted receipt and
previous artifacts; pending edits remain observable. Repairs consume the new
source bytes, and a subsequent unchanged request takes the explicit graph-hit
path. A concurrently running previous worker generation returns the same real
authenticated SQL response after each rejected candidate.

The current real PostgreSQL/auth proof passes 56 assertions, retaining all prior
registration, auth rejection, two-tenant, trusted-auth, SQL-error sanitization,
in-flight cancellation, worker-loss and owner-EOF checks. An early timed
injection missed the compile interval; its replacement uses a synchronized trace
gate. A separate PostgreSQL setup attempt stopped before assertions because the
socket-only readiness check observed its initialization server; TCP readiness
fixed that fixture issue. Both attempts remain recorded and neither is a
measurement exclusion. Historical broader toolchain negatives remain in 0182.

## Decision and limits

Stop the product migration. The observed authored-write gain is about 36% of the
prior 636.515 ms result; the paired gain is about one third of its prior value.
This is a comparison of the requested experiment stages, not a causal attribution
of every millisecond across different series. The current 174 ms artifact gain
independently shows the smaller surviving preparation/build benefit.

The gain is repeated, but most of the earlier benefit is gone before paying for
transactional generation integration and missing protocol parity. Under the
requested conservative gate this does not justify GenerationCandidate. Keep the
experiment and reviewable shared preparation; do not begin a generic BuildSession,
DeclarationPlan, GoSession, persistent kernel or broad runtime rewrite.

Only the admitted unary binding is proved. Complete internal/durable behavior,
streaming/backpressure, custom auth, debugger/telemetry parity, resource budgets,
frontends and assistant children remain unqualified for the split runtime. This
is not full-loop acceptance. The fixed full-loop targets remain 3153.377720 ms
for edits and 4017.245375 ms for unchanged starts.

## Review and validation

Review `cmd/scenery/dev_build_preparation.go`, the private alternate entrypoint,
`internal/build/native_experiment_prepare.go` and ordinary refresh rejection
first. The real `dev-process` probe includes watch batching, edits during
compilation and runtime handoff, and passes alongside the current private proofs.

Affected and repository-wide Go tests, focused race tests, normal and tagged
lint, the default verifier, and `native-contract`, `dev-process`, `build-info`
and `assistant-runtime` probes pass. 0183 records exact commands, artifacts and
existing warnings. Compiler/generator source and public schemas did not change;
fixture regeneration and UI validation are not selected. Full release and full
runtime promotion remain unselected. Machine-local ONLV fixtures and raw captures
are outside the PR; committed tables and source are reviewable, but reproducing
these application timings requires that owned fixture.
