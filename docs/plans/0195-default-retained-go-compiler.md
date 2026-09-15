# Default Retained Go Compiler

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current while the work runs. It
follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Make the retained-domain compiler proven by Plan 0194 the default application
compiler for ordinary `scenery up` development builds. The current supervisor
continues to own compilation, cancellation, candidate verification, activation,
rollback, and cleanup. Stock Go remains the bootstrap and fail-closed
rebootstrap path when no compatible recipe exists or the package graph, files,
imports, directives, module inputs, flags, environment, toolchain, or retained
artifacts change. Production artifact builds remain on stock `go build`.

This is an internal policy with no application configuration, environment
variable, public backend selector, extra daemon, or fallback that publishes an
unverified candidate. The outcome is observable through existing `build.step`
events and a real dev-process replacement.

## Progress

- [x] 2026-09-15: Record Petr's explicit decision to promote the retained
  compiler after reviewing its 35.8 percent same-run accountable-build p50 win.
- [x] 2026-09-15: Read the build architecture, CLI contract, agent guide,
  active native-loop plan, and completed experiment evidence.
- [x] 2026-09-15: Move the reusable recipe/compiler implementation into a
  production-owned internal package and keep benchmark adapters pointing at
  the same code.
- [x] 2026-09-15: Add supervisor-owned bootstrap, retained-build, rebootstrap,
  telemetry, persistence, cancellation, and bounded-reclamation behavior.
- [x] 2026-09-15: Prove the default path, stock rebootstrap, exact identity,
  retained predecessor behavior on failure, and repository validation union.
- [x] 2026-09-15: Update living architecture/contracts, publish outcomes, and
  close the plan.

## Surprises & Discoveries

The benchmark implementation was intentionally below
`scripts/verify/internal/nativebuilddriver`, whose Go `internal` boundary makes
it unavailable to `internal/build`. Promotion therefore requires moving the
single implementation, not copying it or importing verifier code into product
packages.

The retained recipe requires a complete stock-Go action capture. That bootstrap
is substantially more expensive than one warm `go build`; it must be retained
across compatible builds and reported separately from hot compilation. A graph
or configuration change intentionally pays this bootstrap cost again.

The macOS `/tmp` alias resolves to `/private/tmp`. Stock `go list` reports the
canonical package directory while the caller can retain the alias spelling;
recipe admission must therefore compare existing paths after symlink
canonicalization. Without that rule the main package remained named `main` and
the first product recipe appeared incomplete (323 compile actions for 324
packages, including `unsafe`).

The full `dev-process` probe was written around cheap stock fallback for its
many deliberately incompatible edits. With a complete captured bootstrap on
each such edit, its fixed 240-second parent budget expired late in the A/B/A
handoff sequence. A bounded macOS activation smoke therefore proved one
bootstrap, one compatible body edit, one import-triggered rebootstrap, new
process identities, and served typed responses without turning this activation
into a release-gate timeout rewrite.

## Decision Log

- 2026-09-15, Petr: Make the retained compiler the default after the measured
  compiler result was clarified against its same-run stock baseline.
- 2026-09-15, Codex: Limit default selection to development runtime builds.
  Deployable artifact and production-assets builds keep the established stock
  path because Plan 0194 measured development replacement only.
- 2026-09-15, Codex: Use the existing supervisor process as owner. Persist the
  recipe and immutable archives under the workspace-keyed private development
  cache, but keep the loaded recipe process-local and serialize it with the
  existing workspace lock.
- 2026-09-15, Codex: Treat `needs_rebootstrap` as a request for a fresh captured
  stock build. A compiler/tool execution error fails the build and preserves the
  prior served runtime; it does not silently retry through an unmeasured path.

## Outcomes & Retrospective

Completed on 2026-09-15. Ordinary development compilation now selects the
retained compiler without configuration. The existing supervisor performs and
persists complete stock-Go bootstrap recipes, rebuilds compatible body edits
with captured stock compile/link tools, and atomically reboots the recipe for
incompatible inputs. Production, production-assets, and ephemeral builds keep
the prior stock path.

A fresh macOS basic-app activation served `echo:x` from PID 72086 after a
26,137 ms bootstrap. A body edit rebuilt four packages through the retained
backend in 592 ms, completed the build request in 1,112 ms, and served
`retained:x` from PID 72506 with new implementation and build-input identities.
Adding `fmt` forced an incompatible-recipe bootstrap in 25,940 ms and served
`rebootstrap:x` from PID 74090. The runtime was stopped cleanly afterward.

Focused package tests, `go test ./...`, lint, default verifier, documentation
and schema checks passed. The full `dev-process` probe advanced through its
failure/recovery and A/B/A scenarios but its fixed 240-second parent context
expired after repeated intentional bootstraps; it is recorded as failed, not
passing. Per Petr's direction this activation did not run the release gate,
race lane, Linux cohort, or another hour-long performance benchmark.

## Context and Orientation

`cmd/scenery/dev_build_pipeline.go` calls `internal/build.CompileContext` for
every development candidate. `internal/build/compile.go` currently ends in
stock `go build`. The experiment's reusable capture, recipe, compile, and link
logic moves to `internal/nativebuilddriver`; verifier-only wrappers stay under
`scripts/verify`. The Scenery executable exposes a hidden `internal` tool-exec
entrypoint so stock Go can record its actual compile/link actions without
installing another binary.

## Milestones

1. Establish one production-owned retained compiler package.
2. Bootstrap and persist a recipe from the exact development workspace.
3. Select retained compilation by default and rebootstrap fail-closed.
4. Prove runtime replacement and repository contracts, then document the result.

## Plan of Work

Move the experiment core without changing its protocol, and update benchmark
imports. Add a library tool-exec recorder used by both the hidden Scenery
internal command and the existing benchmark binary. Add a retained build owner
inside `internal/build`, keyed by the locked private workspace. On the first
development compile it records a complete stock build and atomically publishes
the validated recipe. Later compatible body edits hash the retained input
domain, snapshot current changed bytes, rebuild the changed packages and
transitive consumers, relink, and publish the requested binary. An incompatible
input removes the old private recipe only after its result is known and runs a
new captured stock bootstrap for the current input.

Emit existing `build.step` records for retained validation, compile, link,
bootstrap/rebootstrap, and artifact publication. Do not change candidate
latest-build contracts.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

```sh
go test ./internal/nativebuilddriver ./internal/build ./cmd/scenery ./scripts/verify
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --probe dev-process --summary --write
```

The Plan 0194 benchmark remains the performance decision evidence. This plan
does not rerun an hour-long benchmark without a separate human request.

## Validation and Acceptance

- `go test ./internal/nativebuilddriver ./internal/build ./cmd/scenery ./scripts/verify`
  passes with bootstrap, retained body edit, forced rebootstrap, cancellation,
  corruption, and exact output identity coverage.
- `go test ./...` and `golangci-lint run ./...` pass.
- `go run ./scripts/verify --summary --write` refreshes
  `.scenery/harness/agent-context.json` and every command in
  `changed_area.recommended_commands` passes.
- A bounded macOS runtime smoke serves new exact behavior and identity through
  the default retained path and proves import-triggered stock rebootstrap. The
  broader `dev-process` probe is attempted and any timeout or failure is
  reported rather than waived as passing evidence.
- `git diff --check` passes; no tracked or untracked `VNEXT.md` change exists.
- `scripts/release-gate.sh`, race mode, Linux execution, and a new performance
  benchmark are unselected because Petr requested default activation, not
  release certification or new measurement. Their absence is reported, not
  called passing evidence.

## Idempotence and Recovery

Recipe publication is atomic. A missing, corrupt, foreign-workspace, or
incompatible recipe cannot publish an executable and triggers a fresh captured
stock bootstrap. Generation staging is private and removed after completion;
each workspace retains only its current recipe. Canceling a
build removes its candidate and leaves both the prior recipe and served runtime
intact. Deleting the private retained state is safe: the next development build
recreates it from current source.

## Artifacts and Notes

Current performance evidence remains under
`.scenery/harness/native-build-compiler/20260914T193915Z-16687730043e95c7/`.
The measured same-run accountable-build p50 was 2,017.491 ms for stock and
1,296.027 ms for the retained compiler. This plan records activation and
correctness evidence separately from that historical timing cohort.

## Interfaces and Dependencies

The implementation uses only the Go standard library, stock `cmd/go`, stock Go
compiler/linker tools, `internal/build` state, and the existing supervisor and
build-step telemetry. Public CLI grammar, application configuration, generated
contracts, runtime identity, environment registry, network protocol, and deploy
artifacts do not change.
