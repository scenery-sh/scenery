# Fast Iteration and Explicit External Proof

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current.

Prepared and approved on 2026-09-08 from `fc9f4cf5fb8c6abc8063a5133c16dec5e0967b77`.
The developer approved separating the fast development loop, selected runtime
proof, explicit release certification, and explicitly requested benchmarks.

## Purpose / Big Picture

Ordinary repository changes must not automatically start managed applications,
databases, or the complete release suite. Keep cached correctness and inexpensive
contract checks as the default. Give each existing external probe an explicit
selector, retain the full functional release set, and remove resource-cost
measurement from that release set into its own explicit command.

The absolute 100 ms test-root contract and its existing 20-process confirmation
algorithm are unchanged. This work changes invocation policy and composition,
not assertions, timing thresholds, test execution, or application behavior.

## Progress

- [x] (2026-09-08) Read the owning instructions and current mode composition;
  verified the clean baseline and the measured source of the long loop.
- [x] (2026-09-08) Implement explicit probe selection and a standalone worktree-cost benchmark.
- [x] (2026-09-08) Make default/race verification service-free and release functional-only.
- [x] (2026-09-08) Align current instructions, command recommendations, schemas and docs.
- [x] (2026-09-08) Validate selection, cached correctness and focused external boundaries;
  record actual times and skipped unselected work.

## Surprises & Discoveries

- The previous worktree probe took 932.290 seconds. Its A18 resource benchmark
  consumed 710.278 seconds; it ran three repetitions of 1/5/10 worktrees with
  cold/warm startup, idle/load sampling and real Victoria/PostgreSQL processes.
- Default verification also ran parallel runtimes, PostgreSQL and storage
  probes. Avoiding only A18 would not make the ordinary loop service-free.
- Current agent instructions and `known_release_loop` recommended a standalone
  release invocation followed by a shell gate that ran the same release set
  again. The shell already delegates once; remove the duplicated outer recipe.
- Plan 0169's 19 timing findings remain deferred. This plan does not repair,
  suppress, or reinterpret their failing evidence. Plan 0145's confirmation
  selection and test-binary attribution remain unchanged.

## Decision Log

- 2026-09-08, developer: ordinary iteration uses fast checks and Go's result
  cache; relevant runtime changes use selected integration scenarios; complete
  release proof runs explicitly before release, once; benchmarks and all-root
  audits run only when explicitly requested.
- 2026-09-08, agent: keep one concrete probe catalog in `scripts/verify` for
  both `--probe <id>` and the full release composition. Repeated `--probe`
  selects a union with duplicates rejected. Unknown IDs and conflicting modes
  fail before builds or provisioning. Do not add a second runner or environment
  knobs. Selected modes report `probe` or `benchmark`, never `release`.
- 2026-09-08, agent: `--benchmark worktree-cost` retains the original A18
  algorithm, input sizes, assertions and owned cleanup. Its preparation shares
  the existing worktree fixture and actual Victoria tool synchronization.
- 2026-09-08, agent: the release recipe is only `scripts/release-gate.sh`, which
  invokes the release verifier once. Do not add trust-based reuse of old reports,
  skip flags, or a second release receipt protocol.

## Outcomes & Retrospective

Implemented and validated. The single 32-entry catalog selects focused probes
and preserves the complete functional release inventory. Default/quick/race do
not invoke it; A18 is a separate explicit benchmark. Failed probe reruns select
only their own boundary, while actual subprocess argv/cwd remain in evidence.
Schemas and static payload revisions include the distinct probe/benchmark modes.

Validation from the repository root:

- `go test ./scripts/verify ./cmd/scenery ./internal/machine`: passed.
- `go test ./...`: passed, using the native result cache.
- `golangci-lint run ./...`: passed, zero issues.
- `go run ./scripts/verify --quick --summary --write`: passed with existing
  documentation/architecture warnings; whole-command wall time 7.13 seconds.
- `go run ./scripts/verify --summary --write`: passed with the same warnings;
  whole-command wall time 11.04 seconds, Go test step 4.371 seconds. Its 11-step
  inventory contains no runtime, database, UI or fixture integration.
- `go run ./scripts/verify --probe auth --summary --write`: passed all 15
  cases and 111 assertions with `cleanup_ok=true`; whole-command wall time
  18.07 seconds, auth step 11.757 seconds.
- `go run ./scripts/verify --probe worktree --summary --write`: passed all
  17 functional cases, no A18, and verified owned-cluster cleanup without a
  retained probe root; whole-command wall time 226.99 seconds, probe step
  221.372 seconds.
- `bash -n scripts/release-gate.sh` and `git diff --check`: passed.

These are local observed runs, not universal performance guarantees. The
existing 42 documentation review warnings and 23 architecture warnings were
not expanded into unrelated cleanup. The first validation attempts caught an
instruction word-budget overrun and stale payload schema revisions; both were
fixed and the final package/repository suites passed.

Full release/shell execution, fresh/all-root timing audit and the resource
benchmark were intentionally not selected under the approved policy. Selection
and rejection are covered in-process; the A18 measurement algorithm is
unchanged, but its standalone resource execution is not claimed as tested.
Fixture regeneration is unselected because no production compiler/generator
source or committed client changed. Plan 0169's 19 deferred timing failures
remain open; this plan does not reinterpret them as passing.

The checkout acquired intermediate commit `8519aa7c` during implementation;
it was preserved. Follow-up corrections were validated in the working tree.
This task did not install, push, or create that intermediate commit.

## Context and Orientation

`scripts/verify/harness_self.go` parses options, composes modes and writes the
existing self-harness envelope/artifacts. `harness_drift.go` contains toolchain,
fixture and effect metadata. `harness_agent_context.go` produces recommended
agent workflows. `internal/repoinfo` owns the read-only changed-path table.

`harness_self_worktree_runtime.go` owns a disposable fixture lifecycle and A1–A18.
`harness_self_worktree_cost.go` owns A18, while `harness_self_worktree_victoria.go`
prepares the actual Victoria binaries it needs. Keep the functional A1–A17 set
and the benchmark's resource ownership intact, but invoke them separately.

The current contract lives in root/subtree `AGENTS.md`, `PLANS.md`,
`docs/agent-guide.md`, `docs/harness-engineering.md`, `docs/local-contract.md`,
the self report/summary schemas and their knowledge entries. Completed plans
are historical evidence and remain immutable.

## Milestones

### M0 — Explicit composition

Add `--probe <id>` and `--benchmark worktree-cost`. Default runs the full cached
Go suite and vet after common local checks; quick runs affected packages; race
adds the existing race shortlist. All live runtime, database, UI, generator
fixture and tool probes are explicit or part of release. Release retains all
functional checks and the full race suite but never invokes A18.

### M1 — One worktree fixture owner

Separate A18 dispatch before functional scenario execution, with a fresh
authored fixture and synchronized real Victoria binaries. Reuse the existing
verified resource cleanup. Functional completion reports 17 acceptance rows;
benchmark output reports its actual measurement separately. Preparation or
cleanup failures remain failures, not skipped or synthetic evidence.

### M2 — Contracts and focused proof

Update all living invocation guidance and the report mode enums together.
Tests pin valid/invalid selections, the service-free default and release's
functional inventory. Exercise real default and selected probes locally.

## Plan of Work

First change the composition while retaining the existing probe functions.
Then separate benchmark preparation and dispatch. Update instructions and
schemas before running the final command union, so validation uses the newly
approved policy instead of recursively re-running the old exhaustive recipe.

## Concrete Steps

All commands run from the repository root:

```sh
go test ./scripts/verify ./cmd/scenery ./internal/machine
go test ./...
golangci-lint run ./...
go run ./scripts/verify --quick --summary --write
go run ./scripts/verify --summary --write
go run ./scripts/verify --probe auth --summary --write
go run ./scripts/verify --probe worktree --summary --write
bash -n scripts/release-gate.sh
git diff --check
```

The expected changed-area classes are `cli-json-contract`, `go-package`, and
`release-sensitive-or-runtime`. Run the refreshed `recommended_commands` union.
The selected auth probe proves an independent catalog entry; worktree proves
the changed functional/benchmark boundary and verified cleanup.

## Validation and Acceptance

Default/quick/race must select no external probe. Release must select every
functional probe exactly once and no benchmark. Explicit selection must run
only the named entries and retain failures and accurate reproduction commands.
Invalid selection must fail before any work. Probe/benchmark report modes must
validate against the current schemas and remain distinguishable in summaries.

The real default run must contain no runtime/DB/UI/fixture probe steps. The
selected auth run must pass all 15 cases; the worktree run must pass A1–A17,
contain no A18 and verify owned cleanup. Report whole-command wall time and
individual check durations separately, without claiming a universal duration.

Do not run the full release gate for ordinary implementation of this plan: the
developer explicitly selected focused validation instead. It is reserved for an
explicit release request. Do not run the 1/5/10-worktree cost benchmark or the
all-root 20-process audit: no new performance measurement was requested. Their
dispatch is covered in process and the cost algorithm remains unchanged. Record
these as unselected, not passing. Compiler/generator production sources and
committed client fixtures are not changed, so fixture regeneration is not selected.

## Idempotence and Recovery

Preserve unrelated work and use the worktree-local CLI. Each selected probe owns
and cleans its existing disposable resources. Never restart a user application,
install a shared CLI, prune shared resources, or rewrite failed evidence to pass.
Unknown selectors must not start a fixture. No commit, push or installation is
implied by this implementation request.

## Artifacts and Notes

Store this change's local evidence under `.scenery/harness/fast-verification/`.
Preserve full mode-specific reports before later runs replace `self-latest.json`.
Use the previous SQL/auth reports only for historical timing attribution, not as
proof of this changed verifier.

## Interfaces and Dependencies

The repository verifier gains explicit probe and benchmark selectors, plus
matching current report mode values. Existing product CLI grammar, runtime APIs,
test-root accounting, timing budgets, dependencies and environment variables are
unchanged. No generic scheduling, receipt cache, dependency-routing framework,
or alternate execution engine is introduced.
