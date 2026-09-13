# External Input Selection Regression Proof

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under `PLANS.md`.

## Purpose / Big Picture

Complete the two real-Go reproductions requested by the re-review of PR #195:
an ignored external Go file becomes eligible only during compilation, and a
temporary embedded file appears in a previously empty external directory.
Both compiled candidates must execute B after the source has returned to A,
without making B reusable under A's input identity.

The local Plan 0183 repair already excludes mutable external source before
shared lookup, subscriber joining and publication. Keep that structural rule;
do not add metadata heuristics, an immutable-source materializer or a runtime
mode. Private external compilation remains supported, not newly hermetic.
Completed plans 0182 and 0183 remain immutable. This follow-up records additional
regression evidence, not a new performance or runtime-architecture result.

## Progress

- [x] 2026-09-13: Inspect HEAD `3ab90b51492ade089c24e850cd27a1c9f202e1d6`,
  the existing uncommitted 0183 repair, the supplied re-review and owning docs.
  Preserve all existing changes; no subagents or remote writes.
- [x] 2026-09-13: Trace the publication boundary and its legitimate private-build
  and owned-workspace controls; allocate 0184 with bounded validation.
- [x] 2026-09-13: Implement both native selection interleavings and bounded probe evidence.
- [x] 2026-09-13: Demonstrate that both tests fail with the pre-0183 publication owner and
  pass with the current owner; retain the tracked-file restoration control.
- [x] 2026-09-13: Run cumulative validation and the owning dev-process probe, inspect the
  final diff, record results and close this follow-up only.

## Surprises & Discoveries

The current Go listing still does not observe ignored Go files or every empty
embed-search directory. That is not shared-cache admission authority: an
external local replacement is excluded regardless of those paths. The existing
native restored-file test covers a tracked file, not these selection changes.

Both new cases passed with the current admission boundary. With only the
publication owner replaced by its `3ab90b51` source through the disposable Go
overlay, both failed specifically with `external native candidate was published:
hit=true`. Before reaching that assertion they had executed B after restoring
A and compared the complete consumed-input entries and observed metadata.
This is a causal red/green control, not a timing benchmark or a reconstruction
of the entire old checkout. The first full tagged union passed in 9.577 seconds.

The final separate review found that the shared test helper waited for compiler
entry without selecting early errors/context completion. It now reports early
failure and joins the compiler before restoring the runner or deleting temporary
inputs. This is test cleanup hardening, not another process supervisor.

## Decision Log

- 2026-09-13, Codex: extend the existing tagged native input-mutation proof rather
  than duplicate product compilation or add real tools to ordinary Go tests.
- 2026-09-13, Codex: absence of shared output and actual A recompilation are the
  external-input acceptance criteria. Requiring an external-input cache hit
  would contradict the explicitly chosen 0183 repair. The supported private
  standalone-module control still proves a legitimate shared hit with live checks.
- 2026-09-13, Codex: perform the security skill's independent investigation and
  final bypass review as separate local passes; project rules prohibit agents.
- 2026-09-13, Codex: test the old publication owner through a disposable Go
  overlay, not by reverting the dirty checkout. Do not claim an entire historical
  checkout was tested: only its cache orchestration is substituted.

## Outcomes & Retrospective

Completed both requested native selection regressions and their standard probe
integration. Each compiles and executes B, restores A before the real publication
guard, compares exact input entries and every observed stamp without change time,
executes the still-B candidate after restoration, proves no shared artifact and
compiles A twice under the original exact cache key. The external ignored file
and empty embed directory are deliberately absent from the initial observation
map. No broader observation heuristic was introduced.

Both tests fail on actual B publication with the historical cache owner supplied
through a disposable Go overlay, and pass with 0183's reuse-domain restriction.
The original tracked-file, persistent-membership, cancellation, corruption,
cleanup and legitimate owned-workspace reuse controls remain. Tagged race checks,
repository tests, both lint commands, quick/default/race verification and the
standard dev-process probe passed. The probe report includes both new assertions
and proves the normal edited response, exclusive writer and predecessor recovery.

The follow-up changes only the tagged test helper/fixtures, bounded dev-process
assertion summary, verifier guidance and plan/discovery docs. No additional
production boundary change was needed. The security skill's separate local
review prompted a bounded early-failure wait and explicit test-compiler join.
No test runner, ordinary root, compiler/generator implementation, application
contract, deployment, public identity or runtime mode changed. No benchmark,
release, extra six-probe union rerun or ONLV operation was selected here; 0183's
earlier proof remains separately attributed, and 0180/0181 remain open.
All changes remain local and uncommitted; no remote Git writes were made.

## Context and Orientation

`internal/build/shared_binary_cache.go` rejects unsupported reuse before any
cache lookup or shared producer. `shared_binary_inputs.go` and `build_input.go`
derive current admission and retain best-effort freshness checks. None needs a
new observation heuristic for this follow-up.

`internal/build/shared_binary_cache_integration_test.go` is compiled only with
`scenery_build_cache_integration`. The existing verifier `dev-process` probe
runs every `TestSharedBinaryCrossProcess` root. It owns the external proof and
bounded assertion summary in `scripts/verify/harness_self_dev_process.go`.
`shared_binary_domain_test.go` retains fast in-process input-domain controls.

## Milestones

1. Extend the native mutation test's fixture variants while retaining its one
   actual Go build, post-build guard and private retry path.
2. Prove B execution, restored A entries and observed metadata, no shared output,
   and fresh A execution for both temporary-input cases without change time.
3. Record the new assertions in current verifier guidance and complete validation.

## Plan of Work

Create the excluded Go file and the empty embed directory before initial real
Go discovery. Assert the changing path is absent from the observation map.
Use the existing Go-runner wrapper to compile and execute B, then restore A
before returning to the production publication guard. Compare complete initial
and final consumed-file entries and observation maps; never substitute injected
Go listing data. Execute the retained candidate again after restoration.

Require no shared cache entry and two actual A recompilations. Preserve the
tracked-file mutation/restoration controls and the persistent-new-file rejection.
Reuse the existing temporary-root cleanup and bounded process context. Document
the exact additional proof without rewriting completed historical evidence.

## Concrete Steps

All commands run from `/Users/petrbrazdil/Repos/scenery`. Fresh Worktree Preflight
does not need UI provisioning: no dashboard sources changed, and each verifier
prepares the worktree-local product itself. Never install a global binary.

1. Run the two new tagged roots against a disposable overlay containing
   `git show 3ab90b51492ade089c24e850cd27a1c9f202e1d6:internal/build/shared_binary_cache.go`.
   Retain the overlay and failing output under ignored `.scenery/harness/0184-review/`.
2. Run `go test -tags=scenery_build_cache_integration ./internal/build -run=^TestSharedBinaryCrossProcess -count=1`.
3. Run `go test ./internal/build` and `go test ./scripts/verify`, then refresh
   selection with `go run ./scripts/verify --quick --summary --write`.
   Read `.scenery/harness/agent-context.json` and execute its exact cumulative commands.
4. Complete the validation below and record commands, results, producer identity
   and report paths before closing the plan.

## Validation and Acceptance

The cumulative dirty patch selects Go package, CLI JSON and runtime classes.
After affected-package tests, run:

```sh
go test ./cmd/scenery
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --race --summary --write
go run ./scripts/verify --probe dev-process --summary --write
git diff --check
```

The dev-process report must explicitly confirm both temporary-input regressions
and existing ownership, cancellation and crash-recovery assertions. All created
processes and fixture roots must be cleaned up by their existing owners.
No new ordinary test root or production build behavior is introduced in this
follow-up; retain ordinary in-process controls and the absolute 100 ms contract.
Do not run benchmarks or all-root timing audits: this request is correctness
proof, not a new measurement request.

The other six probes in 0183's full union need a rerun only if this follow-up
changes production build/runtime/generator/ownership behavior, rather than tagged
test fixtures and the dev-process summary. Record that condition and the diff.
Compiler/generator consumer refreshes are unselected unless those sources change.
Release certification, ONLV measurements and runtime-split promotion are not
selected; do not report them as passed.

## Idempotence and Recovery

Tests allocate fresh private roots and isolated cache directories with `t.TempDir`;
failed candidates do not enter shared storage. The old-owner overlay is optional
negative evidence and never modifies checked-in production files. Rerun a failed
command after fixing its actual cause. Preserve earlier failures and uncommitted
0183 changes. No commit, push, merge, original ONLV mutation or shared-cache
clearing is part of this follow-up.

## Artifacts and Notes

Starting tree: existing 0183 changes, including its untracked completed plan and
domain/probe tests. Additional evidence belongs under ignored
`.scenery/harness/0184-review/`, not in default product output or Git.

Completed validation, all from `/Users/petrbrazdil/Repos/scenery`, Go 1.27.0
darwin/arm64:

| Command | Result |
| --- | --- |
| `go test ./internal/build ./scripts/verify` | pass; build package cached |
| `go test ./internal/build` | pass, cached |
| `go test ./scripts/verify` | pass |
| `go test ./cmd/scenery` | pass |
| `go test ./...` | pass |
| `golangci-lint run ./...` | pass, zero issues |
| `golangci-lint run --build-tags=scenery_build_cache_integration ./internal/build` | pass, zero issues |
| `go test -tags=scenery_build_cache_integration ./internal/build -run='^TestSharedBinaryCrossProcess(TemporarilyEnabledIgnoredGoInput\|TemporaryEmbedInEmptyDirectory\|RestoredExternalInputsWithoutChangeTime\|RejectsChangedNativeInputs)$' -v` | all four pass |
| `go test -tags=scenery_build_cache_integration ./internal/build -run=^TestSharedBinaryCrossProcess -count=1` | final tagged union passes; `native-final.txt` |
| `go test -race -tags=scenery_build_cache_integration ./internal/build -run='^TestSharedBinaryCrossProcess(TemporarilyEnabledIgnoredGoInput\|TemporaryEmbedInEmptyDirectory\|RestoredExternalInputsWithoutChangeTime)$'` | pass |
| `go run ./scripts/verify --quick --summary --write` | pass with existing knowledge/architecture warnings; cumulative oracle read |
| `go run ./scripts/verify --summary --write` | pass with warnings; `default.json` |
| `go run ./scripts/verify --race --summary --write` | pass with existing knowledge/architecture warnings; `race.json` |
| `go run ./scripts/verify --probe dev-process --summary --write` | pass; both new assertions true, existing ownership/cleanup/recovery proof retained; `dev-process.json` |
| `git diff --check` | pass |

The oracle selects `cli-json-contract`, `go-package` and
`release-sensitive-or-runtime`. Its exact commands are default verification,
`go test ./...`, `go test ./cmd/scenery`, `go test ./internal/build` and
`go test ./scripts/verify`; all have executed directly or in the affected-package
invocation above. Default verification reported 41 existing knowledge warnings,
21 architecture warnings and an advisory 5.491-second whole-suite wall time
against 5 seconds. Race verification's ordinary suite and race shortlist passed.
These are not new exact-root timing measurements; no ordinary test root changed.

The final negative command is
`go test -overlay=.scenery/harness/0184-review/pre-0183-overlay.json -tags=scenery_build_cache_integration ./internal/build -run='^TestSharedBinaryCrossProcess(TemporarilyEnabledIgnoredGoInput|TemporaryEmbedInEmptyDirectory)$' -v`.
It exits 1 with both expected `hit=true` failures. Raw failures are retained in
`red-old-publication.txt` and `red-final.txt`; neither is a failed current-code
acceptance run. The substituted historical file has SHA-256
`7b883f7daad8a15580ba90c5e6704549c9108e14f67297ee63534e1879f5780c`.

The prepared worktree-local CLI is
`/Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery`, SHA-256
`d07e2b5ec38cfb9d4ad011842f30a110c8093f29cefa712d7d2bc0d1221afc03`.
Its selected framework source digest is
`sha256:bce7dbb0e0a5143f131412d51d99b217d1262899d345f97aab450082948b7535`.

`dev-process.json` SHA-256 is
`4805fba30e29789591b149aa1669be533072aa686253252666341ec9deaebf7e`.
Its `shared_build_processes` summary reports
`temporarily_enabled_ignored_go_input_not_cached: true` and
`temporary_embed_in_empty_directory_not_cached: true`, with the prior restoration,
subscriber, link-slot, workspace and publication-recovery assertions still true.
Native test temporary-root cleanup passed; probe-managed children were reaped
through the unchanged process owner and fixture cleanup path.

The normal endpoint served implementation
`sha256:d43203c6e75a1d1aa89a6c54c071e23fb213c7ee90ef1e20d54ce84d3b35a02c`
with build inputs
`sha256:00143949697d8ba7022e6391e6eddb2f6406f614ed5c7934ce5175edc3d2167c`.
Its selected snapshot CLI had executable digest
`sha256:42e45ff3d988d895dd6977e865b03be3765468929825a67f2728ee3ba016c395`
and the same framework source digest recorded above. The correlated build
`build-18d4f688cda9c8f8-4` performed one private native build, current input checks
and bounded link admission, with shared publication false. Graph and Go/TypeScript
projection reuse and one changed workspace file remained. These observations are
functional probe evidence, not a latency comparison or acceptance measurement.

## Interfaces and Dependencies

No public interfaces, configuration, dependency, identity semantics or runtime
ownership change. Test-only fixture variants use the stock Go toolchain and the
existing production Go-runner seam. Current verification guidance and knowledge
discovery are updated together; stable architecture and application contracts
remain as documented by 0183.
