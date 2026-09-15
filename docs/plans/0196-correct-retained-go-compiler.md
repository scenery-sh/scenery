# Correct Retained Go Compiler

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Correct the retained Go compiler promoted by Plan 0195 so it behaves like an
incremental action engine rather than replaying every package changed since its
first bootstrap. A successful development build must atomically advance both
its source baseline and retained package archives. Ordinary package-graph
refreshes must use stock Go's warm cache and preserve compatible retained work
instead of forcing a cold whole-closure rebuild. Direct execution must either
reconstruct every compiler input it uses or reject the complete affected
frontier before starting an incomplete action.

Repair the benchmark at the same time. Each sample must prove the requested
backend actually executed once, measure the enclosing build transaction, and
include the retained-validation-plus-stock-build control needed to distinguish
input discovery savings from direct compiler orchestration. Actual phase
boundaries, not synthesized offsets, provide attribution.

## Progress

- [x] 2026-09-15: Read the revision-bound corrective review, current compiler,
  production owner, benchmark wiring, package instructions, and applicable
  architecture and contract routes.
- [x] 2026-09-15: Confirm every reported defect against current `main` at
  `7f3f7c37` and allocate Plan 0196 without editing completed Plans 0193–0195.
- [x] 2026-09-15: Implement advancing current source/archive state with atomic persistence
  and bounded content-addressed retention.
- [x] 2026-09-15: Implement warm stock graph refresh and complete compiler-input rebinding
  with fail-closed affected-frontier admission.
- [x] 2026-09-15: Cache the validated recipe in the supervisor and deduplicate bootstrap
  tool hashing without weakening live input or artifact validation.
- [x] 2026-09-15: Correct enclosing transaction timing, actual phase timestamps, benchmark
  backend ownership, and the retained-capture-plus-stock control.
- [x] 2026-09-15: Add multi-package A/B/C, embed, native-frontier, graph-refresh, persistence,
  timing, and backend-selection regression coverage.
- [x] 2026-09-15: Run focused tests, the repository validation union, the `dev-process`
  probe, and a short representative macOS comparison; publish current outcomes
  and close the plan only after all acceptance evidence is present.

## Surprises & Discoveries

The production path deletes every warm generation after publishing the binary,
so advancing only the source capture would point future builds at deleted
archives and snapshots. New current state needs durable content-addressed
archive and source storage before its recipe JSON is atomically replaced.

The benchmark controls Go by placing a wrapper first in `PATH`, but the promoted
production path can invoke captured compiler and linker tools before reaching
that wrapper. Backend choice therefore needs an explicit internal build policy
owned by the benchmark lane rather than inference from wrapper output.

Go's shared cache can satisfy a newly selected package action without invoking
`toolexec`. A warm graph refresh must first capture the current closure and then
force only current workspace packages through a semantically neutral,
generation-specific compiler identity. Configured compiler flags use the full
stock bootstrap because a later exact package pattern would otherwise replace
their meaning.

One package can have several archive paths: its compile output and separate
cache aliases recorded by downstream import configurations and the linker.
Advancing only the compile output preserved an old linker alias and served stale
behavior. Successful state advancement now replaces every recorded alias for
the rebuilt import path.

macOS may expose the same temporary workspace as `/tmp/...` and
`/private/tmp/...`. Package ownership and retained paths must canonicalize that
alias before deciding whether a package belongs to the workspace.

## Decision Log

- 2026-09-15, Codex: Keep the captured bootstrap recipe immutable and add an
  explicit current capture plus current archive mapping. This preserves the
  source of recorded actions while allowing successful state to advance.
- 2026-09-15, Codex: Store only newly produced archives and changed source
  snapshots in a recipe-local content-addressed store. Atomically replace the
  recipe manifest, then reclaim unreferenced bytes; never retain generation
  directories merely to keep their contents alive.
- 2026-09-15, Codex: Use a normal-cache stock build with tool recording for
  graph refresh. Merge newly observed actions and link inputs with still-valid
  prior actions; do not use `-a` or a new private `GOCACHE` for ordinary graph
  changes.
- 2026-09-15, Codex: Until non-compile package actions are modeled, reject a
  direct-build frontier containing cgo, assembly, C, C++, Objective-C, Fortran,
  SWIG, or syso inputs before invoking any tool. Existing embed configuration
  is supported by retaining and rebinding its captured input file.
- 2026-09-15, Codex: Capture the current package closure before a warm graph
  refresh and force its workspace packages through `cmd/go` with an identity
  trimpath mapping. This records cache-hidden actions without rebuilding the
  standard-library/dependency closure or inventing compiler arguments.
- 2026-09-15, Codex: Keep the compiler as the human-selected development default
  while reporting that the short corrected control was faster. Three samples
  are observation evidence, not authority to reverse the selected policy or to
  publish a new GO/NO-GO decision.

## Outcomes & Retrospective

Completed on 2026-09-15. The retained compiler now advances its current capture,
source snapshots, and every archive alias after a successful build; failed or
superseded candidates leave a usable committed state. Warm graph changes use
the shared Go cache, record current workspace actions, and fall back to a full
stock bootstrap when the recording or configured compiler flags cannot be
preserved. All regular compiler/linker file arguments are retained and rebound;
unsupported native frontiers fail closed before direct tool execution.

Focused package tests, `go test ./...`, `golangci-lint run ./...`, the full
repository verifier, and the 208.775-second `dev-process` probe passed. The
probe exercised multi-file save, edit-during-compilation supersession, A/B/A,
dependency/import graph changes, embed inputs, native/C fallback, failed
candidate recovery, and owned cleanup. The final architecture report has no
warning in the changed area after splitting build/state execution from
`recipe.go`.

The corrected short ONLV benchmark passed nine measured samples and cleanup.
Accountable-build p50 was 2,399.482 ms for stock, 1,495.849 ms for retained
validation plus stock build, and 1,733.503 ms for the retained compiler.
Accepted-edit p50 was 7,494.986, 6,412.517, and 6,724.223 ms respectively. The
direct compiler beat stock but lost to the retained-stock control, so the
observed benefit is input discovery rather than direct compile/link. The run is
explicitly `short_observation_only`; Linux, race, release gate, and the full
two-cohort benchmark remain unverified.

## Context and Orientation

`internal/build/retained_native.go` selects and owns the development backend,
recipe persistence, binary publication, and build-step telemetry.
`internal/nativebuilddriver/recipe.go` loads stock-Go tool actions, validates a
captured input domain, plans package rebuilds, and directly invokes the stock
compiler and linker. `internal/nativebuilddriver/retained_capture.go` validates
the retained input domain without `go list`. The explicit ONLV benchmark and
its wrapper live in `scripts/verify/harness_self_native_build_driver*.go` and
`scripts/verify/internal/nativebuilddriver/cmd/gowrap`.

The immutable bootstrap describes the originally captured actions. The current
state is the last successfully committed capture and the archives matching it.
A graph refresh is a stock build that records only cache misses, captures the
new closure, retains every archive used by the new link, and merges compatible
old actions. A warm direct build is allowed only when the current closure and
the complete rebuild frontier can be represented by recorded compile/link
actions.

## Milestones

1. Advance successful current package state without accumulating prior edits.
2. Make direct action replay complete for supported inputs and fail closed for
   unsupported frontiers.
3. Replace ordinary cold rebootstrap with a warm stock graph refresh.
4. Make owner loading and telemetry truthful and bounded.
5. Repair benchmark controls and prove behavior in focused and runtime tests.

## Plan of Work

Extend `Recipe` with an explicit current capture. `Recipe.Build` compares live
inputs against current state, records real timing boundaries, rebuilds only the
newly changed packages and consumers, and returns private commit material.
`Recipe.Advance` copies new archives and snapshots into digest-addressed durable
paths, returns a fully validated next recipe, and leaves the prior recipe valid
on failure. The production owner atomically writes that next recipe before
deleting the generation and performs reference-based cleanup afterward.

Capture and retain every regular file argument required by a compiler or linker
action. Rebind source snapshots, import configurations, embed configurations,
symbol ABI files, and other retained support inputs by their recorded argument
index. Compute the rebuild frontier before invoking tools and reject native
action classes that require unmodeled asm, pack, cgo, or host-tool steps.

For package membership or selection changes, run stock `go build -work` with
the recorder and the normal Go cache. Capture the new closure, merge recorded
actions with compatible current actions, copy link/import archives into the
durable store, publish the stock-built candidate, and atomically commit the
refreshed recipe. Reserve complete `-a` capture for a genuinely missing or
identity-incompatible recipe.

Hold the validated recipe in a workspace-keyed supervisor cache, serialized per
workspace, while preserving per-build live source, support, archive, tool, and
configuration validation. Record actual phase start timestamps at the execution
sites and carry an enclosing transaction duration through product and benchmark
results, including final publication.

Give benchmark lanes an explicit backend policy that cannot be bypassed by the
product default. Add a retained-capture-plus-stock execution lane, generation
owner/backend nonce evidence, and a pre-cohort assertion that one fresh result
belongs to the requested edit and lane.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

```sh
go test ./internal/nativebuilddriver ./internal/build ./cmd/scenery ./scripts/verify
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --probe dev-process --summary --write
go run ./scripts/verify --benchmark native-build-compiler --workload-root /Users/petrbrazdil/Repos/onlv --benchmark-short --summary --write
```

The benchmark implementation will expose a bounded short cohort for corrective
iteration and keep the existing full 60-pair decision cohort explicit. The
final command uses the short cohort first; a full hour-long rerun remains
unselected unless Petr explicitly requests it again.

## Validation and Acceptance

- `go test ./internal/nativebuilddriver` proves with deterministic in-process
  package/action fixtures that independent A, then B, then C state advances;
  each later build excludes earlier successful packages from
  `RebuiltPackages` and uses their committed archives.
- The package suite proves retained `-embedcfg` rebinding after bootstrap files
  disappear and rejects a rebuild frontier containing assembly/native inputs
  before any tool executes. The explicit runtime/benchmark evidence supplies
  the real stock-Go process boundary.
- `go test ./internal/build` proves atomic recipe persistence, recovery from a
  failed commit/publication, bounded reclamation, resident recipe reuse, and a
  package-graph refresh that omits `-a`, retains normal `GOCACHE`, and preserves
  compatible archives.
- `go test ./scripts/verify` proves stock, retained compiler, and
  retained-validation-plus-stock lanes each emit exactly one fresh result with
  the requested backend/owner/edit identity. Accountable build equals the
  enclosing monotonic transaction rather than a subphase sum.
- `go test ./...` and `golangci-lint run ./...` pass.
- `go run ./scripts/verify --summary --write` refreshes
  `.scenery/harness/agent-context.json`; every command in the final
  `changed_area.recommended_commands` union passes.
- `go run ./scripts/verify --probe dev-process --summary --write` passes its
  full assertion inventory and owned cleanup within its current timeout after
  ordinary incompatible edits stop triggering cold whole-closure rebuilds.
- A short macOS ONLV comparison records actual previous-stock,
  retained-validation-plus-stock, and corrected-retained transactions with
  backend identity. It reports results without claiming the 300/500 ms target
  unless the measured end-to-end metrics actually meet it.
- `git diff --check` passes and `VNEXT.md` is unchanged.
- `scripts/release-gate.sh`, race mode, Linux execution, and the hour-long full
  benchmark are not selected by this corrective development task; their absence
  is reported as unverified, not passed.

## Idempotence and Recovery

All candidates, refreshed action captures, and state manifests are staged under
private generation directories. A failure before atomic recipe publication
leaves the previous source/archive state valid. Content-addressed copies are
safe to retry; cleanup removes only files absent from the newly committed
manifest. A failed graph refresh serves no candidate and keeps the prior recipe.
Deleting the workspace-keyed retained state remains safe because a later build
performs a complete bootstrap.

Benchmark worktrees, processes, sockets, caches, and evidence roots remain
run-owned and are cleaned through the existing ownership checks. A failed short
cohort records partial evidence and does not mutate the source ONLV checkout.

## Artifacts and Notes

The corrective input is the revision-bound review of commit `7f3f7c37`. Its
isolated reproduction observed A accumulating into a later B edit and a captured
embed configuration surviving only at a deleted bootstrap path. Repository
evidence recorded a 26,137 ms initial bootstrap, a 592 ms compatible body build,
and a 25,940 ms import-triggered cold rebootstrap. Plan 0196 treats those as
problems to correct, not as acceptance baselines.

## Interfaces and Dependencies

No public CLI, app configuration, environment variable, generated contract, or
deploy artifact changes. The implementation uses Go's standard library and
stock `cmd/go`, compiler, and linker. Internal additions are a current recipe
state, graph-refresh merge, explicit private build policy for verifier lanes,
actual timing records, and content-addressed retained artifacts. Production and
deploy builds remain stock `go build`.
