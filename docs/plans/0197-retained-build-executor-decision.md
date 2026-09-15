# Retained Build Executor Decision

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

The development builder keeps one immutable input model, avoids repeated graph
discovery and whole-recipe work after a compatible edit, and never compiles live
embedded bytes under a captured identity. Normal product development builds use
the retained direct compiler/linker executor. Stock `go build` execution is not
a product fallback or selectable app policy; it exists in this work only as an
isolated, build-tagged benchmark control. Bootstrap and graph reconciliation may
still invoke `cmd/go` to observe the tool actions and dependency selection that
the direct executor will retain. Scenery does not invent those rules.

These report names are permanent:

- `GO-BUILD ONLY`
- `FULL-PREP + GO-BUILD`
- `FULL-PREP + GO-TOOLS`
- `RETAINED-PREP + GO-BUILD`
- `RETAINED-PREP + GO-TOOLS`

## Progress

- [x] (2026-09-15) Read the revision-bound review, current retained recipe,
  product owner, benchmark owner, current contracts, and Plan 0196 outcome.
- [x] (2026-09-15) Reused validated state metadata during advancement and
  exposed exact state-commit hashing/reuse counters.
- [x] (2026-09-15) Rebound `-embedcfg` paths to generation-owned captured bytes
  and proved isolation through a real compiler invocation.
- [x] (2026-09-15) Restricted graph refresh to the changed package frontier and
  added a post-refresh captured-stamp TOCTOU check.
- [x] (2026-09-15) Reused the retained package graph for compatible body edits,
  avoided the second `go list` unless directory membership changed, reduced
  watched-tree rescans, and collapsed candidate copy/hash handling.
- [x] (2026-09-15) Completed the corrected short full-ONLV comparison, including
  bare and retained stock controls, product-path measurement, absolute phase
  waterfalls, outlier marking, and retained evidence per lane.
- [x] (2026-09-15) Kept the direct executor as the product development default
  by explicit human decision; stock execution is benchmark-control-only.

## Surprises & Discoveries

- State advancement previously validated retained files and then hashed every
  retained archive and support file again. Unchanged validated metadata can be
  carried into the next manifest without that second byte pass.
- Retaining the embed JSON file was insufficient because its `Files` values
  still named live workspace paths. Those values now resolve to captured bytes.
- Full preparation was a material confounder. In the final short run,
  `FULL-PREP + GO-BUILD` had a 3,650.096 ms accountable-build p50 versus
  2,599.317 ms for `GO-BUILD ONLY`, a 1,050.779 ms preparation penalty.
- On equal retained preparation, direct tools improved accountable-build p50 by
  only 99.746 ms (3.40 percent) over the stock control. Accepted-edit p50 was
  effectively tied: 7,126.610 ms versus 7,133.020 ms. The short sample is
  observational, not a statistical release decision.
- The direct path's current dominant build cost is linking: 2,175.229 ms p50,
  versus 196.261 ms p50 compiling in the final short run.
- One `FULL-PREP + GO-BUILD` `implementation.check` observation was 2,108.546
  ms, 93.7 percent above its lane median, and is explicitly flagged as an
  outlier rather than silently influencing interpretation.
- ONLV's user-owned checkout changed concurrently during the benchmark, so the
  report correctly records `source_status_unchanged: false`. All measured lanes
  used owned detached worktrees pinned to one commit, cleanup passed, and the
  selected AHJ target file was unchanged.

## Decision Log

- Decision: retain integrity validation but reuse metadata for unchanged state
  during commit. Rationale: integrity remains fail-closed while commit work is
  proportional to changed state. Date: 2026-09-15. Author: Codex.
- Decision: rewrite embedded-file mappings from the current capture rather than
  trust paths in retained configuration. Rationale: the compiler must consume
  the bytes named by the captured identity. Date: 2026-09-15. Author: Codex.
- Decision: derive graph-refresh forcing from changed packages and transitive
  consumers, then validate captured stamps again before publishing the recipe.
  Rationale: avoid unrelated compilation and reject refresh-time input races.
  Date: 2026-09-15. Author: Codex.
- Decision: use the retained package graph as `BuildInput` discovery evidence
  while directory membership and graph-affecting source identity remain exact;
  relist after membership, import, directive, module, configuration, or tool
  changes. Rationale: compatible body edits do not need repeated package
  loading, while graph changes remain delegated to Go. Date: 2026-09-15.
  Author: Codex.
- Decision: keep `RETAINED-PREP + GO-TOOLS` as the normal product development
  executor even though the short equal-input speed advantage is small. Stock
  execution remains an isolated build-tagged benchmark control and cannot act
  as a runtime fallback. Rationale: explicit human product direction; benchmark
  evidence describes the cost but does not override that decision. Date:
  2026-09-15. Author: Petr and Codex.
- Decision: do not add `-w` to development links. Direct-link measurement was
  745.819 ms p50 and 52,007,602 bytes without `-w`, versus 538.757 ms p50 and
  40,482,226 bytes with it: 207.062 ms (27.8 percent) faster and 11,525,376
  bytes (22.2 percent) smaller. Rationale: `-w` removes DWARF information and
  conflicts with debugger and runtime-attribution evidence. Date: 2026-09-15.
  Author: Codex.

## Outcomes & Retrospective

The final short macOS report is
`.scenery/harness/native-build-compiler/20260915T132011Z-f22d32d363813289/report.json`.
It used one fixed full-ONLV cohort, one excluded warmup and three measured edits
per lane. Correctness and owned cleanup passed. Every measured lane retains its
final executable and `result.json` under the evidence root.

| Path | Accountable build p50 / p95 | First verified response p50 / p95 | Accepted edit p50 / p95 |
|---|---:|---:|---:|
| `GO-BUILD ONLY` | 2,599.317 / 2,640.803 ms | 4,864.583 / 4,973.170 ms | 6,778.089 / 6,898.230 ms |
| `FULL-PREP + GO-BUILD` | 3,650.096 / 4,180.654 ms | 5,879.699 / 6,411.113 ms | 7,776.102 / 8,332.989 ms |
| `RETAINED-PREP + GO-BUILD` | 2,933.540 / 2,971.419 ms | 5,241.790 / 5,368.037 ms | 7,133.020 / 7,341.488 ms |
| `RETAINED-PREP + GO-TOOLS` | 2,833.794 / 3,136.587 ms | 4,918.439 / 5,270.801 ms | 7,126.610 / 7,531.394 ms |

`FULL-PREP + GO-TOOLS` is a permanent matrix name but was not rerun in the final
compiler comparison because the decision boundary was equal retained inputs.
Its historical measurements remain in earlier reports.

`first_verified_response` is the user-visible response boundary. `accepted_edit`
also includes the later repository-side candidate verification, which measured
roughly 1.9 to 2.3 seconds in this run; it must not be presented as hidden build
or runtime startup cost. The final comparison therefore separates preparation,
artifact construction, state commit, scheduler delay, launch/attestation,
runtime activation, response observation, candidate verification, and the
concurrent `implementation.check` sample.

The result does not meet the retained compiler's 200 ms accountable-build
target. It identifies the link step, not package loading, as the next executor
bottleneck under retained preparation. Linux comparison and release
certification remain outside this macOS-only plan.

## Context and Orientation

`internal/nativebuilddriver` owns captured Go compiler/linker actions, retained
input validation, direct execution, and immutable recipe state.
`internal/build/retained_native.go` owns product development policy, durable
publication, graph reconciliation, telemetry, and rollback. `scripts/verify`
owns the explicit full-ONLV comparison and real-tool correctness evidence.

Before Plan 0195 the production development executor was bare `go build`, not a
full-capture-plus-build lane. Plans 0195 and 0196 introduced retained direct
execution and corrected its cumulative state. The earlier Plan 0196 run did not
measure the final product path or its current graph refresh and included about
two seconds of post-response candidate verification in “accepted edit”; this
plan makes those boundaries explicit.

## Milestones

Milestone 1 made state advancement preserve validated metadata and report new
hashing separately from reuse. Milestone 2 rebound embedded bytes and narrowed
graph reconciliation with a final stamp check. Milestone 3 made the retained
graph serve compatible `BuildInput` discovery, removed redundant rescans and
digest passes, and exposed the full timing waterfall. Milestone 4 ran the
corrected full-ONLV short comparison and applied the human-selected direct-only
development policy.

## Plan of Work

Change the retained input and recipe owners first, then consume that state from
the product build path. Keep stock execution confined to a tagged verifier
producer. Extend the benchmark only after product telemetry exposes the exact
same lifecycle, and update current architecture, contract, workflow and decision
documents from the final evidence.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`, update `internal/nativebuilddriver`,
`internal/build`, `cmd/scenery`, and `scripts/verify`; run focused tests while
iterating; run the short full-ONLV benchmark once; apply the direct-executor
decision; then complete the validation commands below and publish the result.

## Validation and Acceptance

Acceptance requires the focused package tests, `go test ./...`,
`golangci-lint run ./...`, the full verifier, the `dev-process` probe, the short
native-build benchmark above, and `git diff --check`. The final executor must
pass fresh bootstrap, sequential body edits, import graph refresh, embed
isolation, candidate preflight, activation, and rollback. `VNEXT.md` must remain
unchanged. Full release certification, Linux measurements, and the all-root
timing audit are not selected by this plan.

The benchmark launcher ancestry was recorded from the ChatGPT application. The
human enabled ChatGPT in macOS per-application Developer Tools controls, while
`DevToolsSecurity -status` still reported global developer mode disabled. The
per-application grant has no stable CLI readback here, so both observations are
recorded separately and neither is inferred from the other.

## Idempotence and Recovery

Tests use temporary roots. The benchmark owns detached ONLV worktrees, caches,
processes and sockets; rerunning with a new run ID is safe. Any failed retained
state commit leaves the prior recipe and binary published. Embed or graph
reconciliation failures stop before candidate activation. Deleting the private
retained development cache is safe and causes the next build to capture a new
complete recipe.

Do not remove user worktrees or data. Do not edit `VNEXT.md`. If validation is
interrupted, inspect Git status, ONLV source status, running benchmark processes,
and the latest report before resuming.

## Artifacts and Notes

Benchmark reports and retained lane evidence live under the ignored
`.scenery/harness/native-build-compiler/` tree. The final report path and all
material measurement limits are recorded in Outcomes & Retrospective above.
Earlier completed ExecPlans remain immutable historical evidence.

## Interfaces and Dependencies

No dependency, public CLI, app configuration, or environment selector is added.
Normal development uses the retained direct compiler/linker path. The isolated
stock controls are available only to the repository benchmark through the
`scenery_benchmark_stock` build tag. Recipe bootstrap and graph reconciliation
use the installed `cmd/go` only to capture Go's selected actions; ordinary app
body edits execute the retained compiler and linker commands directly.
