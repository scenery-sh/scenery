# Shared Build Input Ownership and Recovery

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under `PLANS.md`.

## Purpose / Big Picture

Repair the six findings in the review of PR #195 at
`c17644f2ec4e66c8f5bfa4a19556edbb1e6d7935`. A reusable executable must match its
current action inputs, and cancellation must not release a workspace still read
by its producer. Empty captured files, semantic manifest corruption, abandoned
publication bytes, and lost executable permissions must recover correctly.

This is production build-path repair, not another native execution experiment.
Plan 0180 already changed production preparation and executable caching. Plan
0181 did not promote its experimental host; that narrower statement does not
establish safety of the whole PR. Preserve its five-sample NO-GO evidence and
all unmet latency/contract gates without rerunning or reinterpreting them.

## Progress

- [x] 2026-09-13: Read the supplied review, trace the production call chain,
  inspect ownership/verification helpers and tests, and confirm a clean checkout
  on `feat/incremental-worktree-native-loop` at the reviewed commit.
- [x] 2026-09-13: Allocate 0182 and record the proof and command selection before
  implementation. No subagents or remote mutations were used for this review.
- [x] 2026-09-13: Reproduce and repair action publication and cancellation
  ownership; retain the input workspace until producer and cleanup join.
- [x] 2026-09-13: Reproduce and repair all four recovery cases.
- [x] 2026-09-13: Extend the existing real-process cache probe, update current
  guidance, run cumulative validation and the full selected probe union, and
  record final results and unperformed proof below. Complete locally; publication
  is tracked by Git history and PR #195, not release certification.

## Surprises & Discoveries

`produceSharedBinary` publishes before `CompileContext` rechecks framework and
workspace identity. Its detached producer borrows `Result.Dir`, but cancellation
returns before it joins. Existing cancellation tests write constant output and
therefore cannot establish continued ownership of input bytes.

Restore checks semantic action metadata whereas publication only checks a
generic valid manifest. Publication temporary directories have no lease and
are excluded by pruning. Three capture clones turn a present empty slice into
nil, which later means unavailable. Destination reuse ignores executable mode.

Pre-fix repository-native tests reproduced all six defects: framework/local
replacement/workspace mutation returned nil; a canceled producer released the
lock to the next writer; empty capture became nil; semantic corruption caused
"shared binary disappeared after successful publication"; mode remained 0644;
and abandoned sparse publication survived pruning. The cancellation test uses
`testing/synctest` to settle the cancellation branch before probing its actual
workspace lock, removing a race in the first draft of that regression.

Post-fix `go test ./internal/build`, `go test ./cmd/scenery`, focused empty/cached
capture tests, and `go test -tags=scenery_build_cache_integration ./internal/build
-run '^TestSharedBinaryCrossProcess' -count=1` passed. The tagged test runs a
real Go-built executable containing B, rejects it before caching, then rebuilds
A and proves a subsequent hit. Separate helper processes prove a blocked next
materializer and hard-killed publication cleanup. Cumulative verification is
still pending; these results alone do not close the plan.

The first quick verifier passed with existing knowledge/architecture warnings
(41 and 21 respectively). Its exact cumulative recommendation is:
`go run ./scripts/verify --summary --write`, `go test ./...`,
`go test ./cmd/scenery`, `go test ./internal/build`, and
`go test ./scripts/verify`; classes are CLI JSON contract, Go package, and
release-sensitive/runtime. Initial lint found one capitalized error string in
the new input guard; corrected without suppressing the rule.

The separate local post-patch review found that end-of-action hashes alone
would miss A to B to A within the build. Added run-local file and external
package-directory observations alongside complete content/membership checks,
with a restored-input regression. These observations are never serialized or
used as the action identity. The same review strengthened the empty-file test
to require actual graph reuse; its initial test baseline mistakenly included
implementation files and fell back to a fresh compile. Corrected the test's
baseline instead of weakening the product or its new reuse assertion. That
intermediate quick/full-suite failure is superseded only by later final runs.

An existing cached-graph test still invoked real Go discovery and reached
160 ms after the guard was added. A 20-process confirmation showed that mocking
discovery alone still left real native checking in this publication test. It now
injects those two external boundaries, retains full hashing/publication and
workspace/target assertions, and asserts two discoveries and one native check.
Real input discovery remains proved by the tagged mutation/rejection/retry test;
native checking remains in the required native-contract probe. No test was
gated or assertion removed. Initial raw confirmation (including the 160 ms
failure) is retained under `.scenery/harness/0182-confirmation/`; the corrected
root's separate rerun is under `.scenery/harness/0182-confirmation-cached-graph/`.

## Decision Log

- 2026-09-13, Codex: retain the original caller's workspace lease until the
  producer has finished, even when its subscriber leaves. Other subscribers
  keep the action alive; waiting subscribers still cancel independently. This
  avoids adding a second workspace materializer or a new supervisor.
- 2026-09-13, Codex: put current workspace/framework and complete Go input
  validation inside the shared action before reusable publication. Keep live
  verification and outer publication checks; a native cache entry is not a
  saved application-validation or readiness verdict.
- 2026-09-13, Codex: use the established cache lease/cleanup protocol for
  publication staging and keep retained executable copies independent.
- 2026-09-13, Codex: move the private shared-binary cache to v2. Pre-guard v1
  entries may already contain rejected outputs and v1 publishers lack the new
  lease protocol. Never reuse them or sweep potentially active old-version
  directories. The old version's retained data is left untouched, not migrated.
- 2026-09-13, Codex: the guard reruns complete Go input discovery; this may add
  build cost and intentionally makes no latency-improvement claim. Native
  correctness takes precedence over the previous measurements.
- 2026-09-13, Codex: retain change-time/inode observations only for the current
  action to detect restored consumed inputs. Continue exact content hashing and
  current membership discovery; metadata never substitutes for either check.

## Outcomes & Retrospective

All six reviewed defects are repaired and verified. Native output cannot enter
the current shared cache before the action input guard passes. A canceled
producing caller retains its workspace through producer completion. Empty
captures, semantic metadata corruption, abandoned leased publication stages,
and missing execute permissions have regression coverage. The independent
retained-generation model and current runtime/SDK contracts are unchanged.

The explicit probe union passed all seven selected boundaries. Worktree cases
A1–A17 passed, including current/native protocol coexistence, managed SQL and
cleanup. The report states `verified owned worktree clusters removed`; the
disposable worktree root and test helpers were also absent after completion.
The dev-process proof verified a changed normal-endpoint response and its exact
generation, failure recovery, cache round-trip, input invalidation, and the new
shared action/process assertions. These are functional results, not a new
30-edit ONLV performance comparison or promotion of a host/worker split.

One correctness-probe fixture response took 1,736.095 ms; its native build span
was 510.638 ms, candidate preflight 547.079 ms, and the new shared input guard
134.246 ms. Spans can overlap. This is a single diagnostic observation, not
ONLV latency acceptance or an interleaved baseline comparison. The guard adds
work to establish correctness; no speed improvement is claimed.

Plans 0180/0181 remain open with their original unmet latency and execution-model
gates. No full release, new worktree-cost benchmark, new ONLV measurement, or
alternative execution technology was selected. Old v1 cache data was not
automatically reclaimed because its active publishers lack the v2 lease
protocol. No original ONLV source, services or data was changed.

## Context and Orientation

`internal/build/compile.go` holds the private workspace lock while checking and
building. `verification.go` joins the native checker and compiler. `prepare.go`
uses the same lock for materialization. `shared_binary_cache.go` deduplicates
an action by complete metadata, with leased subscribers and bounded link slots.
`build_input.go` uses Go's actual package/module/native input projection;
`verification_workspace.go` verifies private membership and bytes.

`cmd/scenery/watch_snapshot.go` captures authored bytes,
`watch_compiler_snapshot.go` converts them to `build.SourceSnapshot`, and
`compiler_snapshot.go` / `source.go` consume that capture. Nil bytes mean absent
capture, not a legitimate zero-length file. `scripts/verify` owns explicit
process proof; ordinary tests must retain the exact-root 100 ms p95 contract.

## Milestones

1. Action integrity: deterministic source mutation/rejection/retry and retained
   lease tests, with repairs at the shared action boundary.
2. Recovery: valid empty captures, semantic corruption, leased publication
   cleanup, and executable-mode repair.
3. Verification and handoff: real-process/worktree probe union, service-free
   checks, current architecture/docs, and exact review feedback.

## Plan of Work

Add barrier-controlled regressions before changing production code. The first
build must consume changed source and be rejected before shared publication;
restoring the original inputs must build again, not restore rejected output.
Test framework inputs and a local replacement dependency. Cancellation tests
must read the actual workspace after cancellation and prove a second writer
cannot change it until compilation ends. Also test the last subscriber.

Use current complete input discovery for the publication guard, not just the
linked executable metadata. A cache hit still runs live input and application
verification. Reclaim only leased, abandoned publication stages; protect active
ones and preserve independently owned destinations. Keep test injection at the
existing native tool boundary, not as a production validation escape hatch.

## Concrete Steps

All commands below run from `/Users/petrbrazdil/Repos/scenery`. First capture
`git status --short`, `git rev-parse HEAD`, and `git diff --check`. Add focused
regressions, record their pre-fix failures, apply the shared-boundary repairs,
then execute the validation sequence below. The root and scoped instructions
apply; read `scripts/verify/AGENTS.md` before changing tagged probe coverage.

## Validation and Acceptance

| Finding / boundary | Required proof |
| --- | --- |
| Wrong input identity publication | Barrier-controlled A to B during build, rejection with no reusable entry, A retry recompiles; framework and local replacement inputs covered |
| Canceled producer borrows workspace | Original bytes read after cancellation while next writer is blocked; producer and cleanup joined before releasing lease; last-subscriber stop |
| Empty capture | Empty watched ignore file and embedded asset survive fresh/cached preparation; nil, wrong size/hash still rejected |
| Semantic corruption | Wrong contract/build/implementation metadata rebuilt and replaced; subsequent hit avoids a new build |
| Publication crash | Active lease survives pruning; hard-killed publisher's partial/sparse bytes reclaimed; unrelated paths retained |
| Execute mode | Correct bytes with lost execute permission restored as executable, with no compilation |

Expected classes are Go source, runtime/build, command orchestration, and
documentation; tagged tests also select the verifier class. Run in this order:

```sh
go test ./internal/build
go test ./cmd/scenery
go test ./scripts/verify
go run ./scripts/verify --quick --summary --write
```

Read `.scenery/harness/agent-context.json` after these edits and execute its exact
cumulative `changed_area.recommended_commands`. Then finish with:

```sh
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --race --summary --write
go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe worktree --probe parallel-runtime --probe core-separation --summary --write
```

The explicit `dev-process` lane must include the extended tagged cache proof.
The `worktree` and `parallel-runtime` lanes must report exact owned resources,
current executable identity, isolation, and cleanup. Missing prerequisites or
skipped commands remain unperformed proof. A fixture command using the prepared
binary is `.scenery/harness/bin/scenery check --app-root testdata/apps/worktree-postgres -o json`;
the selected worktree probe owns disposable runtime/SQL execution and cleanup.

No compiler/generator implementation or committed consumer projection is
intended to change. If either does change, run both root fixture regenerations
and every child generation command before the suite. No storage/assistant/
deployment/coordinator protocol changes are selected. No runtime split is
promoted, so its auth/SQL/stream promotion proofs remain unperformed, not passed.
No new benchmark, all-root timing audit, ONLV service mutation, global install,
or full release gate is selected by this corrective plan.

### Final command results

All commands ran from `/Users/petrbrazdil/Repos/scenery` on Go 1.27.0,
darwin/arm64 (Mac14,14, 64 GiB RAM). The worktree-local product was
`/Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery`, SHA-256
`388caaa702b193820cd83c01397b1f409843d5a1bded78b7438440c98edbdf06` for
the recorded probe run. Framework source was not edited during that run.

| Exact command | Final result |
| --- | --- |
| `git diff --check` | Pass |
| `go test ./internal/build ./cmd/scenery ./scripts/verify` | Pass; affected packages ran before the full suite |
| `go test ./internal/build` and `go test ./cmd/scenery` | Pass in focused iterations |
| `go test ./...` | Pass |
| `golangci-lint run ./...` | Pass, zero issues |
| `go run ./scripts/verify --quick --summary --write` | Pass with 41 knowledge and 21 architecture warnings; selection refreshed and exact cumulative commands executed |
| `go run ./scripts/verify --summary --write` | Pass with those warnings and an advisory 5.026 s / 5 s full-suite warning |
| `go run ./scripts/verify --race --summary --write` | Pass with knowledge/architecture warnings |
| `go test -race ./internal/build` | Pass; explicit because the normal race shortlist does not include this package |
| `go test -tags=scenery_build_cache_integration ./internal/build -run '^TestSharedBinaryCrossProcess' -count=1` | Pass; real Go input mutation/retry, separate-process input lease and publication crash proof |
| `go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe worktree --probe parallel-runtime --probe core-separation --summary --write` | Pass with knowledge/architecture warnings; all selected steps and A1–A17 passed |

The source/ABI generator did not change, so committed client regeneration and
its child TypeScript commands were not selected. The fixture example above is
operator guidance; actual disposable fixture/native execution was covered by
the selected generation, native-contract, dev-process and worktree probes.
Standalone storage, assistant, deployment and shared-agent protocol probes were
not selected because those boundaries did not change. No unselected lane is
reported as passed.

Eleven affected exact roots were confirmed in 20 fresh serial processes each,
with one linked binary per package per confirmation run. The command shape was
`go test -c -o <temporary-binary> ./<package>`, then from that package's directory
`go tool test2json -t -p scenery.sh/<package> <temporary-binary> -test.v=test2json -test.count=1 -test.parallel=1 -test.run=^<ExactRoot>$`.
Final p95 values were 80–90 ms for the eight cache/publication roots, 20 ms for
empty compiler preparation, and below the Go report's 10 ms granularity for
orphan pruning and empty watch capture. Maximum final p95 was 90 ms; one
semantic-corruption sample rounded to 100 ms but its p95 was 90 ms. The original
cached-graph 160 ms p95 failure is retained separately from its corrected 90 ms
rerun. This was a targeted 11-root confirmation, not an all-root timing audit.
Load averages observed just after confirmation were 8.17 / 7.48 / 7.48; unrelated
user services were left running.

## Idempotence and Recovery

Use temporary roots and owned helper processes in tests. Never clear shared Go
caches or delete a developer workspace. Failed shared actions must leave no
reusable output, and retry must rebuild under the current complete key. Leases
are OS ownership evidence; PIDs and names alone do not authorize reclamation.
Retained generation executables stay independently copied and rollback-safe
with respect to cache eviction. The user's existing request to commit to the
existing PR governs handoff; do not create a new PR, force-push, or merge.

## Artifacts and Notes

The supplied review concerns the entire PR, not just commit c17644f2's
experimental slice. It reproduced four recovery bugs in extracted code and
identified two P1 defects by inspection; this plan must provide repository-native
regressions. Keep bounded command evidence under ignored `.scenery/harness/`;
record results here without committing caches or machine-local state.

Final quick/default/race/probe reports are retained under
`.scenery/harness/0182-review/`. `probes.json` SHA-256 is
`18c7c9c573e146778d8b15779e2a783d410026b267f0a8af2ae2b687bc33188a`.
The initial timing summary SHA-256 is
`6065ab18493db4285f71fba7dce534b5cf15244fb6d647ab51012c7b0bc37065`;
the corrected cached-graph summary is
`f2a3e86d4fc0b2aadffdbafc02c871e943adfb1382f3a7bdf1e7d0b2ed1fe8f1`.
Each summary's neighboring JSON files retain all sample events. The repository
tests and command shapes above reproduce the assertions without requiring those
machine-local artifacts. The draft reply to the reviewer is retained as
`.scenery/harness/0182-review/reviewer-feedback.md` and is not sent automatically.

## Interfaces and Dependencies

No public SDK, `.scn`, CLI grammar, environment knob, machine envelope, or runtime
identity meaning changes. Keep one compiler/runtime path. Shared artifact
publication and subscriber lifetime remain private `internal/build` concerns.
The semantic execution ABI, database/streaming promotion gates, and NO-GO
decision remain owned by Plans 0180/0181.
