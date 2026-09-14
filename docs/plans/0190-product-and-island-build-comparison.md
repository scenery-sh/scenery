# Product and Island Build Comparison

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

Compare the existing AHJ native-island experiment with the actual Scenery ONLV
development loop without presenting different correctness boundaries as the same
builder. The human authorized continuing this comparison on 2026-09-14.
Do not change production build policy or the human-owned target document.

## Progress

- [x] (2026-09-14) Inspected product compilation and existing benchmark owners.
- [x] (2026-09-14) Verified original ONLV HEAD is the experiment's pinned commit;
  unrelated dirty frontend changes must remain untouched.
- [x] (2026-09-14) Provisioned the dedicated fixture with the current producer;
  ordinary runtime and authenticated AHJ endpoint worked.
- [x] (2026-09-14) Measure the same AHJ behavior change through authenticated normal HTTP.
- [x] (2026-09-14) Capture product build phases and compare with the island's
  source-change/build/first-response boundaries, naming non-equivalent work.
- [x] (2026-09-14) Validate identities, restoration, owned shutdown and publish the report.

- [x] (2026-09-14) Retained two rejected warmup attempts, verified exact source
  restoration and stopped the owned runtime; published the incomplete report.
- [x] (2026-09-14) Reproduced the stale source identity in the existing cached
  implementation-edit test, fixed pre-copy identity capture, and passed package,
  full-suite, lint and standard verifier checks.
- [x] (2026-09-14) Accepted 30 product edits and 30 fresh island pairs against
  framework source digest `724ebe661cda1eef291a81053b33ab25de0dbc2064307b0497de4fd2a22c869f`.

## Surprises & Discoveries

Both first warmups served new AHJ behavior but failed current-candidate source
verification. A 15-second retry window did not resolve the second failure.
Restoring original bytes made verification pass. Cached preparation updates
source stamps but retains the previous `SourceFingerprint`; this is consistent
with the runtime rejection. The human authorized fixing this defect and resuming
the comparison on 2026-09-14.

The product already disables VCS stamping, uses selected input-discovery fields
and a guarded digest cache. The island benchmark does not represent that input
preparation. The product has no standalone AHJ-island target; constructing a
synthetic product result would not prove the real pipeline. The existing full
runtime measurement script uses a project-summary edit in another task's root;
reuse its identity/HTTP measurement method, not that root or its mutable state.

## Decision Log

- 2026-09-14, human/Codex: fix cached preparation's source identity using the
  same pre-copy fingerprint capture as full preparation. Preserve all candidate
  checks and verify the repair with a focused test and the real ONLV series.

- 2026-09-14, Codex: keep ONLV commit
  `4f8126a3e3806b7100ab7efaca1b7dd06b894221` and Scenery
  `825f46194bf109887b4856b20c82e261ce565e4c` explicit. Use a new worktree at
  `/Users/petrbrazdil/Repos/onlv-builder-comparison-20260914`, branch
  `feat/builder-comparison-20260914`; do not adopt another task's runtime.
- 2026-09-14, Codex: compare current product preparation/build/activation with
  the real AHJ operation in the complete app. Do not call this an isolated
  same-artifact builder A/B: the experiment links a smaller application and
  omits production checks. If exact same-workload comparison requires a new
  product target or bypass, report that limitation rather than implement it.
- 2026-09-14, Codex: native macOS execution only; Docker is used solely for the
  existing managed database fixture, never to emulate the measured Go runtime.

## Outcomes & Retrospective

Completed on 2026-09-14. The [comparison report](../product-and-island-build-comparison.md)
retains the rejected warmups and publishes two accepted 30-sample series after
the authorized identity fix. Product edit-to-response p50/p95 is 4110.905 /
4383.164 ms; smaller AHJ island is 687.096 / 751.062 ms. Go build alone is
1265.978 / 1358.096 ms versus 399.839 / 432.098 ms. These are differently scoped
stock-Go workflows, not an identical-artifact builder A/B or backend promotion.
Source restoration, exact identity and owned cleanup passed. Linux remains
deferred; first-load platform attribution remains insufficient. The product
fixture is retained stopped. Changes are not committed or pushed by this task.

## Context and Orientation

`internal/build/compile.go` owns stock-Go application compilation;
`cmd/scenery/dev_build_pipeline.go` owns normal preparation and replacement.
`scripts/verify/harness_self_native_reload_attribution.go` owns the private island
series. The report `docs/native-build-first-execution-attribution.md` records its
latest VCS-disabled baseline. ONLV `development/prepare.ts` creates an owned
small fixture and `development/runtime-identity.ts` verifies response identity.

## Milestones

1. Create and prepare an owned fixture with exact framework selection.
2. Add a bounded measurement runner using ONLV's existing identity helpers;
   two excluded warmups and 30 unique AHJ edits, then restore the exact bytes.
3. Analyze phase boundaries and compare same-revision evidence without summing
   overlapping implementation checks or phase quantiles.
4. Stop the owned runtime, retain evidence and document remaining limitations.

## Plan of Work

Use the authored AHJ invalid-query behavior as the semantic marker, matching the
island's validation-before-SQL path. Authenticate normally and request its real
HTTP binding; require the new message, PID and implementation identity. Measure
from source-write start through a complete verified response. Preserve full
runtime checks and frontend/service startup. Polling overhead stays explicit.
Capture supervisor phase events, Go build steps and artifact identity. Record
launcher Developer Tools state and current stock-Go flags. Existing caches stay
in place; do not stop unrelated workloads. If a product contract or fixture
fails, retain the failure and do not bypass it to obtain timings.

## Concrete Steps

The dedicated worktree is created from the exact ONLV commit above. Prepare its
framework using the Scenery worktree-local executable with `framework use
--source /Users/petrbrazdil/Repos/scenery --app-root <owned-root> -o json`, then
run `bun development/prepare.ts` from the owned root. It restores only the new
fixture's coordinated preset. Write the exact measurement command and report
location here once the runner is implemented.

Runner: `.scenery/harness/builder-comparison/measure-product.ts`.
Both `bun .scenery/harness/builder-comparison/measure-product.ts product-825f4619`
and the same command with label `product-825f4619-retry` failed at the mandatory
current-source identity check. The report retains exact hashes and phase evidence.

## Validation and Acceptance

Every accepted sample must have new semantic behavior and a new exact runtime
identity; no warm second launch substitutes for the first accepted generation.
Report p50/p95, sample count, exclusions, source/producer identity and cleanup.
Scope differences must remain visible next to performance comparisons.
Documentation changes require `go run ./scripts/verify --quick --summary --write`
and `git diff --check`. If verifier Go code changes, run affected package tests,
the full Go suite, lint and the changed-area validation union. No
global CLI install, commit or push are authorized by this step. The human has
separately authorized the narrowly scoped production identity fix. Validate it
with `go test ./internal/build`, `go test ./...`, `golangci-lint run ./...` and
`go run ./scripts/verify --summary --write`; the ONLV series supplies runtime
proof without changing an external protocol or bypassing admission checks.

## Idempotence and Recovery

Never reuse an evidence directory or restore over another writer's bytes.
Use the fixture marker and retained exact producer for shutdown. Retain owned
state after a failure; do not prune databases, shared caches or other worktrees.

## Artifacts and Notes

Keep machine-local traces under `.scenery/harness/builder-comparison/` and publish
only the bounded report and methodology. Completed plan 0189 remains immutable.

## Interfaces and Dependencies

Use existing CLI, typed HTTP and identity helpers. No new production runtime,
build mode, environment-variable knob, or subagent workflow is introduced.
