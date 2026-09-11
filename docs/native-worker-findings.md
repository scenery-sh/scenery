# Native Worker Findings and Review Handoff

Status: experimental implementation for review, not a production runtime choice.
The current findings are from 2026-09-11. Read this before the historical decision
in [0180](plans/0180-native-worker-feasibility.md): the measurement audit in
[0181](plans/0181-native-worker-measurement-audit.md) withdrew that architectural
rejection. Its complete sample tables and validation commands are committed.

## What the diff implements

Application-facing `scenery`, `auth`, `db`, `durable` and native `runtime` calls
no longer pull in the framework host. HTTP, policy, scheduling, MCP and host
bootstrap move to `runtime/host`; generated ordinary bootstrap imports that host
explicitly. Native state, callbacks, service lifecycle and composition have small
shared owners under `internal/native*`, `internal/runtimeapp` and
`internal/runtimescope`. Generated fixtures and the semantic generation revision
change with the import boundary. Most of the large runtime diff is file movement.

The private experiment renders a native worker and a framework kernel. It uses
the full prepared target and native verifier, complete consumed-input identities,
content-addressed binary retention, linked first-execution proof and explicit
activation. The kernel's independently verified artifact is reused across edits.
The ordinary product CLI does not select the worker experiment.

## What is established

The real ONLV worker retains all 166 native application packages, registers 215
operations and preserves all 5063 baseline native text symbols. Its dependency
graph has 578 packages; the separate kernel has 331 and no native application
package. Every measured pair retains the same 509 authored native, embedded and
public-projection file identities, with only the intended handler-body edit.
Kernel input projection equals independent discovery of its 687 inputs; complete
worker target identity includes 1681 inputs and is rechecked after compilation.

One real authenticated project-list HTTP binding works through kernel and worker
using actual constructors and PostgreSQL. Observed proof includes two-tenant
isolation, anonymous/invalid-token rejection, SQL failure, in-flight SQL
cancellation, worker loss, linked response identities and owner-channel shutdown.
Arbitrary pointers, contexts and SQL objects are not serialized across processes.

## Performance result and the methodological correction

Each new comparison contains six alternating AB/BA pairs, distinct semantic edits,
fresh worker binaries, first executions and real authenticated SQL responses.
Every worker sample reuses verified kernel bytes but starts a new kernel process.
Warmups are separate and no measured sample is discarded.

| Comparison | Control median | Worker median | Observed difference |
| --- | ---: | ---: | ---: |
| Ordinary build versus corrected worker | 5978.692 ms | 6633.047 ms | +654.355 ms / +10.945% |
| Both with complete post-build input checks | 6414.336 ms | 6532.735 ms | +118.399 ms / +1.846% |

The original 19% slowdown was not a fair architectural comparison: the worker
performed four discovery/hash passes versus one and repeated prepared rendering.
The correction shares one full captured graph and its bytes with the kernel,
retains a complete post-build recapture, and reuses existing pure projections.
The matched control adds that same final check; it is a separate attribution
control, not an optimization or replacement of the ordinary product build.

In the matched series, summed Go-command medians fell from 1813.351 to 1609.017 ms
(204.334 ms), while native checking was 1192.075/1349.354 ms and worker rendering
149.635 ms. These spans overlap: their medians must not be added into a claimed
critical-path saving. Absolute times drifted between series; compare within each
series, not across them. Six pairs establish neither statistical equivalence nor
an inherent 1.8% architectural penalty. They also do not demonstrate a large gain.

## Remaining uncertainty and decision

The candidate still prepares ordinary private composition alongside worker
additions and verifies the resulting full target. That extra preparation is a
prototype cost. A kernel process surviving semantic edits has not been tested;
only its executable is reused. Neither potential saving should be counted yet.
The measured path excludes the complete supervisor, frontend and assistant loop,
so it cannot establish the 0179 edit/start goals of 3153.378/4017.245 ms.

Only the selected unary HTTP binding is admitted. Streaming/backpressure,
custom auth, all internal/durable paths, complete telemetry parity, debugger
behavior and request latency/throughput/resource budgets are not qualified for
the split runtime. BuildSession, DeclarationPlan, GoSession and broad retained
generation/lifecycle migration have not been implemented.

Recommended next decision: authorize one bounded experiment that prepares only
the candidate's private composition while preserving every authored package,
public projection, native input, target/ABI check, freshness check and lifecycle
invariant. Attribute the actual remaining wall-time path, then repeat the same
paired experiment. If the gain still remains in tens or low hundreds of
milliseconds, it does not justify the proposed platform-wide migration. A kernel
that survives edits is a separate, more involved lifecycle experiment and cannot
be used to make unchanged-start measurements appear faster.

## Focus for a cloud reviewer

Check `internal/build/native_experiment*.go`, the worker/kernel renderers and
`runtime/worker` first. Identify a concrete redundant cost or correctness defect
with code evidence; do not infer the complete loop's performance from file moves,
binary size or the count of imported packages. Preserve the distinction between
native callbacks remaining reachable and every protocol being implemented.

Full Go tests, focused race tests, lint, generator fixtures, TypeScript checks,
the default verifier and named native/process/auth/SQL probes passed locally;
0180/0181 record exact commands, stages and remaining warnings. Full release and
unconverted runtime promotion gates were not selected. The ONLV fixture, raw
machine-local captures and process/SQL logs are not part of this repository;
cloud reviewers can inspect the diff and committed result tables but cannot
reproduce those application timings from the Scenery checkout alone.
