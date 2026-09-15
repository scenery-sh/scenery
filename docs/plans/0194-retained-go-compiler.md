# Retained Go Compiler Experiment

## Purpose / Big Picture

Determine whether a retained, fail-closed compiler driver can remove repeated
Go package discovery from the complete ONLV build transaction while preserving
the exact stock-Go compiler, linker, generated application scope, runtime
identity, and authenticated response behavior. This is repository-only
experimental tooling. It does not select or add a production build backend.

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the experiment runs.

## Progress

- [x] 2026-09-14: Record the user-authorized continuation after the Plan 0193
  retained-driver NO-GO result and allocate a separate protocol comparison.
- [x] 2026-09-14: Implement complete retained-domain hashing, directory
  membership validation, changed-source snapshots, and explicit compiler mode.
- [x] 2026-09-14: Complete the unsupported-change and corruption matrix.
- [x] 2026-09-14: Run two 30-pair full-ONLV cohorts and the 50-edit churn cohort.
- [x] 2026-09-14: Publish the decision, run the validation union, and clean
  owned roots.

## Surprises & Discoveries

Plan 0193 showed that direct compiler/linker invocation did not help when both
lanes repeated complete `go list` discovery and capture. Across 60 samples per
backend, stock accountable build p50/p95 was 1,874.625/2,341.557 ms while the
retained driver was 1,897.614/2,683.187 ms. Complete capture alone was about
738 ms p50. The next experiment must remove that repeated discovery without
weakening the input domain; optimizing the roughly 140 ms changed-package
compile cannot provide the required payoff by itself.

An initial complete serial-hash compiler run reduced discovery to about 257 ms
and passed both cohort medians, but one cohort's build p95 was 5.12 percent above
stock due to two linker outliers. A bounded parallel-read attempt increased
input hashing to 320-390 ms on APFS and was stopped as invalid diagnostic
evidence. Ctime-backed digest reuse for unchanged external inputs reduced the
complete validation p50/p95 to 106.939/124.203 ms while retaining mandatory
hashing for all workspace inputs.

## Decision Log

- 2026-09-14, Petr: Authorized implementation of the compiler experiment after
  reviewing the equal-scope retained-driver NO-GO result.
- 2026-09-14, Codex: Keep stock Go 1.27 compile/link tools and the captured
  620-package recipe. Do not write a Go parser, type checker, code generator, or
  linker and do not fork the Go toolchain.
- 2026-09-14, Codex: Register `native-build-compiler` separately from frozen
  `native-build-driver`. The candidate hashes every one of the 5,114 known
  inputs and package-directory membership on each transaction, snapshots only
  changed workspace bytes, and returns `needs_rebootstrap` for graph or
  configuration changes.
- 2026-09-14, Codex: Use the same two cohorts, two warmups, 30 alternating pairs,
  backend/root swap, 50-edit churn, full generated ONLV main, and 25 percent plus
  100 ms median payoff gate as Plan 0193.
- 2026-09-14, Codex: Reject parallel small-file hashing after measured APFS
  contention. Reuse an external digest only with a nonzero unchanged ctime and
  identical complete file stamp; hash every workspace input and every external
  input whose stamp changed or lacks ctime.
- 2026-09-14, Codex: Final run
  `20260914T193915Z-16687730043e95c7` passed both cohort gates and returned
  `go_for_next_experiment`. This authorizes production-boundary design, not
  product backend activation.

## Outcomes & Retrospective

The final run completed 60 stock and 60 compiler full-ONLV samples plus 50
compiler churn edits. Aggregate accountable-build p50 fell from 2,017.491 to
1,296.027 ms (35.8 percent), first-verified-response p50 from 5,146.984 to
4,312.820 ms (16.2 percent), and accepted-edit p50 from 7,047.562 to 6,234.328
ms (11.5 percent). Both cohort p95 gates passed, every typed response and
identity check passed, four generations remained, the original target digest
was unchanged, all owned worktrees/processes were removed, and the benchmark
exited zero. Artifact work itself was 1.1 percent slower at p50; the gain is
entirely retained discovery/validation. The stock linker remains the next
measured bottleneck at 1,001.768 ms compiler-lane p50.

## Context and Orientation

`scripts/verify/internal/nativebuilddriver` owns stock-Go recipe capture and
direct compile/link execution. `retained_capture.go` adds the no-`go list`
transaction. `scripts/verify/harness_self_native_build_driver.go` owns both
equal-scope benchmark protocols and disposable ONLV runtimes. The workload is
ONLV commit `4f8126a3e3806b7100ab7efaca1b7dd06b894221`; the measured target is the
complete generated `./scenery_internal_main`, not an AHJ island.

## Milestones

1. Freeze and validate the retained input domain.
2. Prove body edits, restoration, and fail-closed unsupported changes.
3. Execute two comparable full-ONLV cohorts and bounded churn.
4. Publish GO/NO-GO evidence, validate the repository, and remove owned state.

## Plan of Work

Bootstrap the candidate lane with a stock `-toolexec` recording and immutable
archive copies. For each hot transaction, compare raw build configuration and
tool identities, hash every captured input, compare directory membership, and
parse imports/directives only for changed Go files. Reuse bootstrap snapshots
for unchanged consumer sources, snapshot changed bytes, compile the changed
package and transitive consumers, relink, hash, and atomically publish only the
newest owner sequence.

The stock lane performs complete package discovery and capture before ordinary
`go build`, matching the production safety boundary. Both lanes use isolated
caches and the same source bytes, build flags, generated contracts, target,
request, readiness, and candidate identity verification.

## Concrete Steps

From the Scenery repository root run:

```sh
go run ./scripts/verify --benchmark native-build-compiler --workload-root /Users/petrbrazdil/Repos/onlv --summary --write
```

Evidence is bounded beneath `.scenery/harness/native-build-compiler/<run-id>/`.
The source ONLV checkout remains read only; all edits occur in owned detached
worktrees at the pinned workload commit.

## Validation and Acceptance

- Run `go test ./scripts/verify/internal/nativebuilddriver/...`.
- Run `go test ./scripts/verify` and `go test ./...`.
- Run `golangci-lint run ./...` and `git diff --check`.
- Run `go run ./scripts/verify --quick --summary --write`, read
  `.scenery/harness/agent-context.json`, and execute its cumulative
  `changed_area.recommended_commands` union.
- Run the exact benchmark command above. Require two complete 30-pair cohorts,
  all authenticated behavior and identity checks, the negative matrix, 50 churn
  edits, four-or-fewer retained generations, and owned cleanup.
- Require each cohort to improve accountable-build p50 by at least 25 percent
  and 100 ms, keep candidate p95 within five percent of stock, improve accepted
  edit p50, and keep accepted edit p95 within five percent of stock.
- Do not run release or unrelated benchmarks. This changes no product external
  boundary, so no named release probe is selected.

## Idempotence and Recovery

Each run uses a random owned directory, marker, Unix socket, session identity,
and four detached worktrees. Unsupported inputs never fall back to stock build.
Cleanup signals only verified owned processes, restores owned source bytes,
removes registered worktrees, and preserves incomplete evidence as invalid.
The original checkout may receive unrelated human edits during a run; the
benchmark verifies its target source digest rather than claiming ownership of
the complete checkout status.

## Artifacts and Notes

The benchmark report records raw per-sample discovery, compile, link,
first-response and accepted-edit timing, exact artifact and runtime identities,
commands, churn, cleanup, and the decision gate. Plan 0193 remains the
same-capture baseline and is not rewritten as evidence for this protocol.

## Interfaces and Dependencies

The experiment uses Go 1.27 compiler/linker tools, `-toolexec`, Git worktrees,
the existing Scenery supervisor and typed ONLV AHJ endpoint, Unix sockets, and
standard-library filesystem/hash/parser packages. Production packages do not
import the experimental helper. No public API, environment knob, daemon,
fallback, plugin, JIT, interpreter, or generated contract is added.
