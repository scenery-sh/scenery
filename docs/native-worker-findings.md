# Native Worker Findings and Review Handoff

Status: bounded experiment completed for draft PR #193; no production runtime
promotion. Current findings are from 2026-09-11. The historical measurement
correction in [0181](plans/0181-native-worker-measurement-audit.md) withdrew the
architectural rejection based on the original 19% slowdown. The direct preparation
follow-up in [0182](plans/0182-direct-native-worker-preparation.md) now measures a
repeatable gain within its six-pair API probe. The complete-loop 50% goal remains
unproven.

## What the diff implements

Application-facing `scenery`, `auth`, `db`, `durable` and native `runtime` calls
no longer pull in the framework host. HTTP, policy, scheduling, MCP and host
bootstrap live in `runtime/host`; generated ordinary bootstrap imports that host
explicitly. Native state, callbacks, service lifecycle and composition have small
shared owners under `internal/native*`, `internal/runtimeapp` and
`internal/runtimescope`. Most of this earlier runtime diff is file movement.

The private experiment now prepares the worker and kernel directly. It selects
shared public projection plus the selected private output before materializing
one owned workspace. Ordinary composition, ordinary adapters and the ordinary
entrypoint are never generated there. Adapter metadata and common validation are
separate from ordinary `Source`; the worker renderer does not invoke that source
renderer even on a cold cache. Compilation consumes the prepared set without
adding or repairing files afterward.

Compiler/public Go publication, TypeScript, source ownership, complete authored
target patterns, default plus selected ABI checks and post-build recapture remain
mandatory. Only the internally forced unused ordinary entrypoint disappears.
An explicitly authored pattern requesting it still fails rather than being
rewritten. The ordinary product CLI continues to use its existing executable.

## What is established for the direct candidate

Independent dependency captures preserve exactly the same 166 native packages
and 215 operation identities. All six pairs preserve exactly 509 app/native,
embedded and public-projection input identities and bytes, except the intended
project-list body edit. The full target manifest changes from 1628 control inputs
to 1631 candidate inputs: 50 ordinary private inputs disappear (47 adapters,
composition, empty assistant assets and entrypoint), and 53 selected inputs enter
(47 worker adapters, two entrypoints and four worker runtime source files).
There is no unexplained change to common inputs.

The kernel's 687-input projection matches independent discovery. Both arms
perform two full discovery passes. Each candidate has a distinct retained worker
executable; all six reuse one checksum-verified kernel executable while starting
six new kernel processes. Native preparation cache hints contain no ordinary
successful binary or graph identity. Failure proofs preserve the actual ordinary
latest-build manifest, previous receipt and retained binary bytes.

Ten negative toolchain proofs cover an unrelated native constructor signature,
unrelated invalid body, distinct selected-target tags, explicit ordinary target
pattern, projection preparation failure, generated-file tampering, imports,
tags, embedded membership and canceled final recapture. The initial target-test
attempt stopped on stale TypeScript; the corrected proof refreshed the authored
public projection and then reached the intended Go-target failure. That earlier
attempt remains recorded rather than counted as ABI coverage.

Real PostgreSQL/auth proof passes 53 assertions, including exact operation
registration, anonymous/invalid-token rejection, two tenants, trusted auth data
over caller tenant paths, SQL table failure with sanitized HTTP errors, in-flight
SQL cancellation, worker loss and both owner-channel shutdowns. Earlier 0181
symbol evidence remains historical; this follow-up specifically repeats exact
package/operation/input membership and the real behavior proof.

## Six matched pairs

Each sample makes a distinct semantic edit and measures full preparation through
first proof, activation and the real authenticated SQL response. Two warmups are
separate. Every measured sample is retained; every candidate is faster within its
pair. A positive gain below means control minus worker.

| Metric | Result |
| --- | ---: |
| Control median | 5471.196 ms |
| Direct worker median | 4834.681 ms |
| Difference of arm medians | 636.515 ms / 11.634% gain |
| Median within-pair gain | 631.524 ms |
| Within-pair gain range | 520.523–1035.094 ms |

The full sample and timeline tables are in 0182. Median preparation is
1471.587/1223.858 ms (control/worker). The actual joined build/verifier interval is
1886.204/1725.063 ms; the build branch dominates every pair. Its median paired
saving is 165.301 ms. Summed Go-command time is not that branch's complete wall
time, and verifier savings cannot be added to build savings. Final recapture and
retention remain approximately 565 ms in both arms. Component medians are not
additive.

Observed exposed kernel startup, from process spawn through compiled proof and
private setup to first TCP acceptance, has a 44.720 ms median and a
44.147–53.543 ms range. The polling resolution is 10 ms. Startup depends on an
already activated worker; authenticated SQL response time is separate. This is
only an observed upper bound on removable restart cost in these samples, not a
credit for an unimplemented surviving kernel.

## Decision and actual development-path limits

Keep the direct candidate in draft for review. The approximately 0.63-second
paired gain is useful evidence for this bounded cut. It does not demonstrate
that a broad runtime migration will halve the complete development loop, and no
such rewrite or production promotion follows from this result.

The source maps preparation and the joined build to the real edit path in
`cmd/scenery/dev_build_pipeline.go`. That path first attempts cached refresh,
passes a source snapshot, and continues through metadata, service/runtime setup,
activation and frontend/assistant work. Cached refresh also renders ordinary
projection via `internal/build/workspace_cache.go`, but the experiment uses a
fresh driver and nil snapshot. Neither its preparation saving nor its complete
11.6% result is a measured saving in the full supervisor loop. Do not subtract
636 ms from the older 4.495-second edit sample or compare absolute times across
0180/0181/0182 series.

Only one unary HTTP binding is admitted. Streams/backpressure, custom auth,
all internal/durable paths, telemetry parity, debugger behavior and complete
resource/throughput budgets are not qualified for the split runtime.
BuildSession, DeclarationPlan, GoSession and persistent-kernel generation rebinding
remain unimplemented. The measured kernel restart interval is too small to
supply the missing 50% evidence by assumption.

## Review and validation

Review `internal/build/native_experiment*.go`, shared preparation and the split
metadata/worker renderers first. Ordinary source selection must remain separate
from shared validation; complete captured inputs and retained bytes remain the
admission boundary. Native registration reachability is not full protocol parity.

Affected Go tests, focused race tests, lint, generator fixture refreshes,
TypeScript checks, the default verifier and the named `native-contract`,
`dev-process`, `build-info` and `assistant-runtime` probes pass. 0182 records exact
commands, output artifacts and existing warnings. Full release and runtime
promotion gates were not selected. The private ONLV fixture and machine-local
captures remain outside the PR; cloud reviewers can inspect source and committed
tables, but cannot reproduce those application timings from this checkout alone.
