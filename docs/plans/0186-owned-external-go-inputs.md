# Owned External Go Inputs

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under `PLANS.md`.

## Purpose / Big Picture

Bind private application compilation to the same immutable generation used for
Go discovery and implementation verification when an application selects local
replacement modules. Today the selected Scenery framework is already rewritten
to an app-owned content-addressed source snapshot, but other `replace ... =>
<local-path>` modules remain live external trees. A compiler can therefore read
temporary B bytes while the source is A before and after compilation. Plan 0183
correctly prevents that executable from entering shared storage; this plan makes
the private candidate itself correspond to owned bytes.

The observable result is that a local replacement is captured into an app-owned,
content-addressed module source generation. The generated workspace rewrites the
replacement to that generation before `go list`, implementation checking and
`go build`. Candidate publication and activation require the live source to
still equal the captured generation. No public syntax, Go API, CLI grammar,
identity meaning or shared-executable reuse domain changes.

## Progress

- [x] 2026-09-13: Branch `feat/owned-external-go-inputs` from clean PR #195
  head `c984d4637391f6c28b502fa90bd059c6c7ba9310`; inspect current framework
  selection, workspace materialization, build-input and activation owners.
- [x] 2026-09-13: Implement a bounded app-owned local-module source generation and rewrite
  workspace replacements before any Go consumer runs.
- [x] 2026-09-13: Reject source/snapshot drift before publication and immediately before
  candidate preparation and retirement; keep shared reuse restricted.
- [x] 2026-09-13: Add deterministic real-Go A/B/A, ignored-Go, empty-embed, native,
  cancellation, corruption and cross-worktree isolation/reuse regressions.
- [x] 2026-09-13: Run cumulative tests, lint, default/race verification and the selected
  production probe union; validate one disposable ONLV build with exact producer.
- [x] 2026-09-13: Record measurements, remaining unowned inputs and the final decision;
  commit the separate branch only when all selected proof passes.

## Surprises & Discoveries

`scenery framework use` already captures framework inputs beneath the application,
builds its CLI from those exact bytes and rewrites only the `scenery.sh` replacement
to that immutable root. Reimplementing framework capture would create two owners.
The missing boundary is therefore ordinary local replacements selected by the
authored root `go.mod`.

The current shared cache restriction remains necessary even after this slice.
Module-cache packages, native toolchain reads, explicit native inputs and custom
flags are not all owned by the new generation. A content-addressed path is useful
identity, not independent authorization to widen executable reuse.

The first dev-process rerun exposed two warm workspace writes instead of one.
Bounded `written_paths` evidence identified `go.mod`: replacement binding had
reset the workspace to authored module bytes and discarded the private tidy
result. Reapplying only the authored replacement set to the existing workspace
module preserved tidy ownership and restored the one-write incremental path.

The same trace exposed an unnecessary 53.571 ms `source.local_modules` interval
with no owned replacements. Its cause was a duplicate framework snapshot hash.
The framework owner already verifies that source before preparation, so the
local-module binder now preserves the authored framework replacement without
re-verifying or canonicalizing its spelling.

## Decision Log

- 2026-09-13, Codex: store local module generations under the application-owned
  `.scenery/build/owned-go-modules/v1/` root. The existing private workspace lock
  serializes preparation/compilation for that app, permits bounded safe cleanup
  and keeps mutable worktree authority separate. Go's package cache remains the
  cross-worktree compilation reuse owner.
- 2026-09-13, Codex: derive identity from complete captured membership, file bytes
  and behavior-relevant modes; preserve empty directories and reject symlinks.
  Never use mtime/change-time as source equivalence.
- 2026-09-13, Codex: retain the 0183 standalone-workspace shared-executable domain
  unchanged. This refactor establishes exact private compilation first.
- 2026-09-13, Codex: preserve private `go mod tidy` requirements while replacing
  only the captured authored replacement set. Tidy and authored dependency
  selection have distinct ownership, and warm implementation edits must not
  rewrite `go.mod` merely to rediscover the same selection.
- 2026-09-13, Codex: persist origin/generation/digest tuples in private build
  state version 10. This lets later current-candidate inspection reject external
  drift after restart without changing the public runtime bundle or identity.

## Outcomes & Retrospective

Completed. Non-framework local replacements are copied into independent,
app-owned content generations before any Go tool reads the build workspace. The
workspace replacement, Go discovery, implementation checking and compilation
all consume those exact bytes. Post-build, pre-candidate, pre-retirement and
later current-candidate checks compare both the retained generation and live
origin by complete content and membership. No timestamp or change-time field is
used as equivalence, no hard links retain the mutable checkout, and shared whole-
executable reuse remains restricted.

The explicit real-Go test series compiled captured A while each live origin held
B, restored A with change-time unavailable, and executed A for a tracked Go file,
a newly enabled ignored file, a temporary embed in an initially empty directory,
and a cgo header. The historical 0184 negative controls still execute B after
restoration and prove that such a live-tree build never enters shared storage.
Cancellation removes its staging tree, corrupt retained generations rebuild,
and two app roots receive distinct mutable authority.

A disposable ONLV worktree at commit `4f8126a3` used the exact prepared producer
with framework source digest `sha256:0960dd9c...87a8`. After the required contract
refresh, `scenery check` and `scenery harness` passed. A real development build
with `github.com/google/uuid` redirected to disposable local source copied that
module to owned digest `sha256:e6ec0430...5031`; the generated workspace and build
state selected the owned path, and `--verify-generation` returned build-input
digest `sha256:7f3a2da8...ec65`. A warm owned-source hit in the attempted runtime
start took 71.269 ms on this checkout. That is attribution, not a universal
latency claim.

The ONLV Go suite had one unrelated missing Git-LFS fixture,
`utilities/data/iou_zipcodes_2024.csv`. Runtime startup was also unavailable
because the disposable checkout lacked frontend `node_modules`/`vite` and that
seed input, so no ONLV endpoint response is claimed. The runtime was stopped,
the disposable worktree was removed, and the local module copy was moved to the
user's Trash.

Final repository proof passed the affected package tests, `go test ./...`,
`golangci-lint run ./...`, quick/default/race verification and the selected
`generation,native-contract,build-info,dev-process,worktree,parallel-runtime,
core-separation` probe union. The union retained one-file warm materialization,
the 20 unique endpoint-verified edit series, native edit and bounded churn
acceptance from Plan 0185. With no local replacements, the final
`source.local_modules` phase measured 0.149 ms. The verifier retained 41 known
knowledge-review and 21 known architecture warnings; no selected check failed.

This closes the local-replacement private-compilation TOCTOU. It does not claim
that arbitrary cgo flags, compiler subprocesses, module-cache packages or custom
tools are snapshot-owned. Those inputs remain outside shared reuse and are the
next boundary if current measurements justify expanding ownership. The rejected
native host/worker experiment and the 300/500 ms development target remain open.

## Context and Orientation

`internal/build/source.go` copies captured application bytes into the generated
workspace and currently rebases local replacements to live absolute paths.
`internal/build/prepare.go` and `internal/build/workspace_cache.go` are the two
fresh/cached materialization paths. Both must call one replacement-binding owner
before dependency/build fingerprints are computed.

`internal/build/build_input.go` asks stock Go for its consumed package inputs;
`internal/build/compile.go` overlaps that discovery/verification with compilation.
Once workspace `go.mod` points at an owned generation, both branches read the
same module root. `cmd/scenery/dev_app_start.go` owns the current pre-candidate
and pre-retirement freshness gates.

The source-generation format is private build state. It is not a module proxy,
public cache contract or new application concept. The selected framework keeps
its existing `internal/build/framework_prepare.go` owner.

## Milestones

1. Capture and validate one complete local Go module source generation without
   hard links or reads through the mutable origin during compilation.
2. Rewrite every local non-framework replacement in the private workspace to its
   exact generation in both fresh and cached preparation paths.
3. Carry origin/snapshot identity through `build.Result`; verify both after Go
   work and at the supervisor's existing freshness barriers.
4. Prove transient selection changes, persistent drift, corruption, cancellation
   and two-worktree isolation with real Go compilation.
5. Update architecture/contracts and finish the selected validation matrix.

## Plan of Work

Parse the authored captured `go.mod`, not a previously rewritten workspace copy.
For each local replacement other than the already-selected framework, canonicalize
the source root and build a deterministic manifest containing every non-excluded
directory plus regular-file path, mode and SHA-256. Preserve empty directories;
include ordinary Go/native/embed sources, dotfiles and other regular files under
the module root while excluding only VCS metadata and Scenery state. Reject
symlinks and special files.

Materialize missing generations through a private staging directory, verify the
staged manifest, atomically rename it to the digest path and make files read-only.
An existing generation is a hit only after complete byte/membership verification.
Quarantine a corrupt exact generation under the same app-owned root and rebuild
it from current source; never accept its bytes. Keep at most 64 generations and
4 GiB, pruning only while the app's workspace lock is held and protecting every
generation referenced by the current preparation.

Rewrite the workspace `go.mod` to the captured roots and then seed/tidy/fingerprint.
Store logical module path, origin root, generation root and digest on `build.Result`.
Before runtime-bundle/build-state publication and at the supervisor's existing
post-build/pre-retirement barriers, recompute origin and generation manifests by
bytes and membership. A mismatch discards the candidate and schedules the normal
fresh reconciliation; no final metadata heuristic may authorize it.

## Concrete Steps

All commands run from `/Users/petrbrazdil/Repos/scenery`; never install a global
Scenery binary.

1. Add the private generation owner and focused pure manifest/materialization tests.
2. Integrate it into fresh and cached workspace preparation, then add final and
   supervisor freshness gates.
3. Add tagged real-Go tests where origin A becomes B only while compilation runs
   and returns to A. Cover an existing file, ignored Go selection, temporary embed
   in an initially empty directory and a cgo/native file. The executable must run
   captured A, not transient B, even with change time unavailable.
4. Add canceled capture, corrupt retained generation and two-app-root isolation
   tests. Preserve existing 0183 publication regressions unchanged.
5. Refresh changed-area selection and run the exact validation below.

## Validation and Acceptance

Expected classes are `go-package` and `release-sensitive-or-runtime`. Run:

```sh
go test ./internal/build
go test ./cmd/scenery
go test ./...
golangci-lint run ./...
go run ./scripts/verify --quick --summary --write
go run ./scripts/verify --summary --write
go run ./scripts/verify --race --summary --write
go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe worktree --probe parallel-runtime --probe core-separation --summary --write
git diff --check
```

After quick verification, read `.scenery/harness/agent-context.json` and add every
cumulative recommended command not already listed. The selected probe union must
retain exact framework/executable/build/implementation identities, current
ownership, native compilation, canceled/superseded candidates and cleanup.

Compiler/generator fixture refreshes are selected only if their sources change.
Auth/PostgreSQL/fixtures production-equivalence probes are unselected because no
execution or capability boundary changes; the existing worktree probe still
covers app/session/SQL isolation. Storage, assistant and deployment probes are
unselected unless their owners change. Full release and performance benchmarks
are unselected because this correctness refactor does not claim a latency win.

Use the exact prepared `.scenery/harness/bin/scenery` and current-source framework
selection for a disposable ONLV `scenery check -o json`, application Go tests,
`scenery harness -o json --write`, development build and one normal endpoint if
that workload is available. Record unavailable services as unperformed.

## Idempotence and Recovery

Generation publication uses staging plus atomic rename. A canceled capture removes
only its task-owned staging directory. Corrupt content is never compiled or used
as a hit. Cleanup targets only validated paths beneath the exact application root;
no broad cache, active executable or other worktree resource is removed. A failed
candidate leaves the previous generation serving. Rerunning preparation either
verifies an exact generation or rebuilds it from current source.

## Artifacts and Notes

Plan 0183/0184 regressions remain the negative shared-publication controls. Plan
0185 records the current safe-path timing and resilience baseline at PR #195 head.
New raw native/process evidence belongs under ignored `.scenery/harness/0186/`.

## Interfaces and Dependencies

The intended private types are equivalent to:

```go
type OwnedGoModuleSource struct {
    ModulePath, OriginRoot, GenerationRoot, Digest string
}

func VerifyOwnedGoModuleSources([]OwnedGoModuleSource) error
```

No new dependency, database, daemon, environment variable, CLI flag or public
application API is introduced. `golang.org/x/mod/modfile` remains the module-file
parser; stock Go remains the only compiler and package cache.
