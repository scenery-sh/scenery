# Cache-Only Generated Go Artifacts

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current throughout
implementation.

## Purpose / Big Picture

Today every Scenery application checkout accumulates generated Go source that
must be committed next to authored code: one `<package>/scenerycontract/`
directory per local Scenery package (holding `contract.gen.go`,
`types.gen.go`, and a `scenery.package-generated.json` descriptor), plus
`internal/scenerygen/` (per-service adapters and the application composition
package) with its `scenery.generated.json` descriptor. In an app with four
packages that is five generated directories and their descriptors polluting
`git status`, code review, and the authored mental model. The repository's own
doctrine already says generated files under `.scenery/gen/` are cache, not API,
so the current layout contradicts the intended model.

After this plan is complete:

- An application checkout contains only authored files. `git status
  --porcelain` is empty after every normal Scenery command.
- Generated Go contracts, adapters, composition, and the runtime `main` are
  rendered in memory and injected into Scenery's existing external build
  workspace (under the OS user cache). `scenery check`, `scenery test`,
  `scenery build`, and `scenery up` never require materialized generated files
  in the source tree and never write them there.
- Editors (`gopls`) and raw `go test ./...` resolve contract imports through
  external generated contract modules in the OS user cache, bridged by a
  Scenery-owned, machine-local, git-ignored root `go.work`.
- Go import identities such as `your.module/health/scenerycontract` remain
  byte-for-byte stable. Go type identity and the package-contract ABI revision
  do not change; only physical storage moves.
- Source materialization becomes an explicit export/vendor mode for published
  modules, not the default for every application.

## Progress

- [x] Slice A: build/check independence from materialized generated Go files.
- [x] Slice B: editor contract modules, Scenery-owned root `go.work`, mandatory
      refresh, ownership protocol, source-input exclusion.
- [x] Slice C: verified pruning of legacy materialized artifacts, default flip,
      workspace-revision recomposition, plan invalidation and receipt rebind.
- [x] Slice D: TypeScript client `materialization = "cache" | "source"`.
- [x] 2026-07-14 - Migrated ONLV: authenticated and removed 43 generated Go
      trees, refreshed its current provider lock and source-materialized
      TypeScript SDK, and proved check/raw Go/Scenery test/build/detached up.
- [x] 2026-07-14 - Final validation: `go test ./...` passed in 46.30s using
      the ordinary cache policy; TypeScript conformance (16 tests) and generated
      client typecheck passed; self-harness functional lanes passed.
- [x] 2026-07-14 - Design review completed against the current implementation
      (generator, check, overlay verification, build workspace sync, revision
      composition, receipt structure). ExecPlan authored. Implementation not
      started.

## Surprises & Discoveries

- The Go toolchain resolves explicit imports of packages under dot-prefixed
  directories. Experiment on `go1.26.0 darwin/arm64`: a module importing
  `example.com/app/.scenery/gen/contracts/health` builds with both
  `go build .` and `go build ./...`; the dot-directory package is excluded only
  from package-pattern enumeration. This does not rescue in-checkout dotted
  import paths as a design (see Decision Log), but corrects the assumption that
  they cannot compile.
- `internal/build/source.go:232` treats `go.work` and `go.work.sum` as authored
  application source. A generated root `go.work` with machine-specific absolute
  paths would flow into source snapshots, fingerprints, and the build
  workspace unless explicitly excluded. This makes the ownership split between
  user-owned and Scenery-owned workfiles load-bearing, not defensive polish.
- `TestWorkspaceRevisionIncludesOnlyDescriptorOwnedGeneratedFiles`
  (`internal/compiler/compiler_test.go:267`) confirms `workspace_revision`
  currently hashes descriptor-owned generated files, so generating derived
  bytes changes the revision of the source they were derived from.
- `RenameReceipt` (`internal/evolution/compatibility.go:35`) binds only base
  and target contract revisions; `ChangeReceipt`
  (`internal/evolution/changes.go:81`) binds workspace and contract revisions.
  `ContractRevision` folds the entire global `spec.CurrentRevision()` into its
  hash (`internal/compiler/compiler.go:443`), so a required spec-revision bump
  invalidates rename receipts indirectly even when the contract projection is
  byte-identical.
- Overlay-based native verification already exists and is regression-tested:
  `internal/generate/generated_go_overlay.go` renders expected artifacts into a
  `go/packages` overlay, and `internal/generate/module_nested_test.go` deletes
  materialized `scenerycontract` / `internal/scenerygen` directories and
  verifies implementations without recreating them. The architecture this plan
  ships is mostly an extension of machinery that already works.
- `internal/parse/analysis.go` already sets `GOWORK=off` in its hermetic Go
  environment, providing the precedent for making that invariant explicit in
  build, test, and harness child processes.
- ONLV exposed that the external test workspace must contain the app config and
  ordinary Go `testdata` trees, not only Go/C/embed inputs. Without them,
  package initialization could not discover `.scenery.json` and non-embedded
  golden fixtures disappeared. The source sync now includes both categories.

## Decision Log

- Decision: Keep generated Go import identities exactly stable
  (`your.module/<pkg>/scenerycontract`, `your.module/internal/scenerygen/...`)
  and move only physical storage. Reject relocating imports to
  `.scenery/gen/...` paths even though explicit dot-directory imports compile.
  Rationale: relocation would churn canonical Go type identity and the
  package-contract ABI projection (`internal/generate/generate_go.go:468,543`),
  drop the packages from pattern enumeration, depend on under-specified
  toolchain behavior, and require an otherwise unnecessary physical/logical
  identity indirection layer. Stability makes the ABI invariant hold by
  construction.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).
- Decision: Bridge editors and raw Go commands with a Scenery-owned,
  machine-local root `go.work` whose `use` directives point at external
  generated contract modules in the OS user cache, instead of `GOWORK`
  environment plumbing or in-checkout hidden modules.
  Rationale: the Go command discovers `go.work` upward from the working
  directory and `gopls` honors it with zero per-editor configuration; raw
  `go test ./...` works after one Scenery compilation. External storage keeps
  every generated byte out of the checkout and sidesteps `gopls`
  dot-directory scanning risk.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).
- Decision: Scenery-owned `go.work`/`go.work.sum` are machine-local editor
  metadata with fail-closed digest ownership (sidecar record under
  `.scenery/editor/`), excluded from source snapshots, fingerprints, revision
  inputs, build-workspace sync, watcher inputs, and semantic workspace clones.
  User-owned workfiles keep their current authored-input treatment. Scenery
  never mutates a workfile it cannot prove it owns.
  Rationale: `internal/build/source.go:232` classifies workfiles as source;
  without the split, machine-specific absolute paths would leak into
  fingerprints and make implementation revisions machine-dependent.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).
- Decision: Editor-cache refresh is mandatory, not optional: every successful
  top-level application compilation (`scenery compile`, `scenery check`,
  `scenery up` initial and watcher recompilations, agent mutation applies)
  synchronizes the editor contract cache and managed `go.work` before
  reporting success. `compiler.Compile` itself stays side-effect-free;
  synchronization is command-level orchestration, because the compiler also
  runs against staged and temporary workspaces during mutation planning.
  Rationale: stale editor contracts after a `.scn` edit would be a recurring
  trust failure; speculative compilations must not write editor state.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).
- Decision: Stage the work as four independently shippable slices (A: hermetic
  build/check; B: editor workspace; C: prune, default flip, revision
  migration; D: TypeScript materialization mode), with Slice A purely additive
  so legacy materialized layouts keep working until Slice C.
  Rationale: Slice A proves the architecture with the existing toolchain before
  any migration or revision-policy change; each later slice is verified by a
  toolchain that no longer depends on materialized files.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).
- Decision: The revision migration invalidates outstanding unapplied
  change/deployment plans explicitly (dedicated `revision_scheme_changed`
  diagnostic), never rewrites applied receipts, and preserves rename evidence
  through an explicit spec-only revision-rebind proof permitted only when the
  canonical contract projection is byte-unchanged. Modeled on the durable
  artifact rebind in `internal/agent/artifact.go` (commit `f0e2a8db`).
  Rationale: `ChangeReceipt` binds workspace revisions directly and
  `ContractRevision` folds the global spec revision, so both direct and
  indirect invalidation paths are real; silently orphaning or mutating signed
  evidence is unacceptable.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).
- Decision: Decoupling `contract_revision` from the entire global spec catalog
  (a dedicated contract-projection identity) is recorded as a follow-up in
  this plan's Artifacts and Notes, not a blocking dependency of any slice.
  Rationale: it is a broader identity correction; the rebind mechanism above
  handles the one bump this plan requires.
  Date/Author: 2026-07-14 / Petr Brazdil with design review (Codex, Claude).

## Outcomes & Retrospective

Completed on 2026-07-14. Ordinary Scenery compilation no longer reads or
writes materialized generated Go. Build/test/up inject one deterministic render
into the external workspace, while editor contract modules retain original Go
import identity through an ownership-verified root `go.work`. Exclusive and
explicit tagged-merge ownership both fail closed on user divergence, and all
hermetic Go children ignore ambient workspaces.

The one-time migration is safe and observable: current and final legacy
descriptors are accepted only with normalized owned paths, generated markers,
and a matching aggregate digest. Source materialization is now an explicit
`--target contracts --materialize` export. Workspace revisions exclude derived
roots, pending old-scheme plans report `revision_scheme_changed`, and immutable
rename receipts can be supplemented only by projection-matching rebinds.

ONLV acceptance removed 43 generated directories without touching authored
files. `scenery check`, raw `go test ./...`, `scenery test ./...`, and
`scenery build --target development` passed; detached `up --wait ready`
returned ready and the application frontend returned HTTP 200. Repeating check
did not change Git status and no generated Go directory reappeared.

## Context and Orientation

Terms used in this plan:

- *Materialized generated files*: generated Go source written into the
  authored application checkout (`<pkg>/scenerycontract/`,
  `internal/scenerygen/`) and tracked by generated descriptors
  (`scenery.package-generated.json`, `scenery.generated.json`).
- *Build workspace*: the external synchronized copy of the application that
  Scenery builds and tests in, located under `os.UserCacheDir()`
  (`internal/build/state.go:60`) and populated by `syncGeneratedFiles`
  (`internal/build/workspace_cache.go:271`) from `build.Prepare`
  (`internal/build/prepare.go:17`). Today its only generated content is
  `scenery_internal_main/main.go`.
- *Editor contract modules*: this plan's new external Go modules, one per
  local Scenery package, each declaring `module
  your.module/<pkg>/scenerycontract` and holding that package's rendered
  contract files, stored in the OS user cache keyed by canonical app root
  (sibling to the build workspace, e.g. `<UserCacheDir>/scenery/editor/
  <app-key>/contracts/<pkg>/`).
- *Managed root `go.work`*: a Scenery-generated, git-ignored workspace file at
  the app root whose `use` directives list `.` plus the absolute paths of the
  editor contract modules.

Where the current behavior lives:

- Contract generation: `generateModuleContract`
  (`internal/generate/generate_go.go:409`) hardcodes
  `filepath.Join(dir, "scenerycontract")`; the ABI projection includes
  `importPath + "/scenerycontract"` (`generate_go.go:468,543`). Application
  adapters and composition: `internal/generate/generate_application.go`
  (hardcodes `internal/scenerygen` around line 173).
- Check semantics: `generate.Check` (`internal/generate/check.go`) calls
  `generateGoContractsFromResult(result, true)`, which compares expected bytes
  against the materialized checkout and reports missing/stale files as errors.
- Overlay verification: `internal/generate/generated_go_overlay.go`,
  `verify_go.go`, `verify_go_native.go`; regression coverage in
  `internal/generate/module_nested_test.go`.
- Source classification: `internal/build/source.go:232` lists `go.mod`,
  `go.sum`, `go.work`, `go.work.sum` as authored source files.
- Revisions: `computeWorkspaceRevision`
  (`internal/compiler/compiler_workspace_revision.go:17`) hashes authored
  source plus descriptor-owned generated files; `ContractRevision` composition
  folds `spec.CurrentRevision()` (`internal/compiler/compiler.go:354,443`).
- Evolution evidence: `ChangeReceipt` (`internal/evolution/changes.go:81`),
  `RenameReceipt` (`internal/evolution/compatibility.go:35`), receipt loading
  in `LoadAppliedRenameReceipts` (`internal/evolution/`).
- Rebind precedent: `internal/agent/artifact.go` (commit `f0e2a8db`,
  "Rebind unchanged durable artifacts to the current spec").
- Hermetic Go env precedent: `internal/parse/analysis.go` sets `GOWORK=off`.

Verified Go toolchain behavior this plan relies on (Go 1.26.0 local
experiments; re-pin on the declared toolchain and selected `gopls` in Slice B
tests): `go.work` is discovered upward from the working directory; `use`
directives accept absolute paths; a module whose module path is nested under
another module's path (`example.com/app/health/scenerycontract` beside
`example.com/app`) resolves correctly in workspace mode; `go test -overlay` is
NOT sufficient for packages that exist only virtually (vet attempts to chdir
into the absent directory), which is why editor resolution uses real external
modules rather than overlays.

## Milestones

1. **Slice A — hermetic build and check.** `scenery check`, `scenery test`,
   `scenery build`, and `scenery up` succeed with all materialized generated
   Go directories deleted, without recreating them. Legacy materialized
   layouts still work unchanged.
2. **Slice B — editor and raw-Go workspace.** After one successful Scenery
   compilation, raw `go test ./...` and `gopls` resolve contract imports from
   the authored checkout via the managed root `go.work`, with fail-closed
   ownership, source-input exclusion, and pinned toolchain/`gopls` tests.
3. **Slice C — migration and default flip.** Verified pruning removes legacy
   materialized artifacts; Go generated roots leave
   `managed_generated_roots`; source materialization becomes explicit
   export/vendor mode; `workspace_revision` stops hashing derived cache;
   pending plans invalidate explicitly and applied rename evidence survives
   via revision rebind.
4. **Slice D — TypeScript materialization mode.** `typescript_client` gains
   `materialization = "cache" | "source"`; `cache` generates into
   `.scenery/gen/typescript/` for first-party consumption, `source` keeps the
   current `output_root` behavior for committed/published SDKs.

## Plan of Work

**Slice A.** Add a pure renderer in `internal/generate` that returns every
expected generated Go artifact (contracts, adapters, composition, descriptors)
as workspace-relative paths mapped to bytes, with no filesystem writes or
staleness inspection of the checkout — factored from the existing render path
used by `generatedGoVerificationOverlay`. In `build.Prepare`, merge the
rendered map into the `codegen.Output` before `syncGeneratedFiles`, failing on
path collisions. Pass the rendered path set to source synchronization as a
skip set; otherwise legacy checked-in generated files would be copied into the
workspace as authored source first and overwritten as generated output second,
masking staleness. Rework `generate.Check` to validate rendered bytes and run
the existing overlay verification without requiring source materialization; a
missing or stale cache is no longer contract invalidity. Keep materialized
files supported (still rendered to the same relative paths) but irrelevant to
build correctness.

**Slice B.** Add editor workspace synchronization: render editor contract
modules (each with its own `go.mod` declaring the existing import identity)
into a fresh content-addressed generation directory under the OS user cache,
then atomically commit and update the managed root `go.work`. Ownership is
fail-closed via a sidecar record `.scenery/editor/go-work-owner.json` holding
the workfile path and content digest: create when absent, replace only on
digest match, stop rewriting when the digest diverges (now user-owned), never
touch tracked or pre-existing user workfiles, and offer explicit merge mode
that adds/removes only Scenery-tagged `use` entries. While Scenery owns the
files, add `/go.work` and `/go.work.sum` to the repository's local exclude
file (`.git/info/exclude`), never to the committed `.gitignore`. Exclude
Scenery-owned workfiles from source snapshots, fingerprints, revision inputs,
build-workspace sync, watcher inputs, and semantic workspace clones
(`internal/build/source.go` classification gains the ownership check).
Set `GOWORK=off` explicitly in every Scenery-spawned hermetic Go process
(build, test, harness, artifact verification), mirroring
`internal/parse/analysis.go`. Wire mandatory refresh into command-level
orchestration after every successful top-level compile; on failed compilation
keep the last-known-good cache and report through diagnostics/`scenery doctor`
that editor contracts correspond to the previous valid revision. `doctor`
also detects workfile ownership conflicts and both directions of parent
`go.work` shadowing (app nested in a monorepo whose root has or gains its own
workspace file).

**Slice C.** Add verified pruning of legacy materialized artifacts reusing the
existing descriptor-retirement safety mechanism (descriptor identity, owned
paths, generated-file markers; recognize both `.v1.json` and current
descriptor names), removing only authenticated generated files and then empty
directories. Stop requiring Go generated roots in `managed_generated_roots`
(they become implicit and framework-owned; the setting remains for
intentionally exported artifacts). Make source materialization explicit
(`scenery generate --target contracts --materialize`) for published modules,
per the Go implementation spec's release-packaging requirement. Recompose
revisions: `workspace_revision` hashes authored source and explicit
non-derived inputs only; contract revision continues to hash the canonical
contract graph; artifact revisions bind contract revision, generator identity,
target, and output digest. Ship the migration contract: pending unapplied
change/deployment plans fail with `revision_scheme_changed`; applied receipts
are preserved unmodified; a `RevisionRebind` artifact (from/to spec and
contract revisions, contract projection hash, reason, digest) attaches as
additional evidence and is accepted only when the canonical contract
projection is unchanged, modeled on `internal/agent/artifact.go`.

**Slice D.** Add the `materialization` attribute to `typescript_client`,
defaulting to current behavior until the flip is decided, generating `cache`
output under `.scenery/gen/typescript/` and exposing it to Scenery-managed
frontend tasks.

## Concrete Steps

Work happens in this repository (`~/Repos/scenery`), one slice per PR-sized
change set, each keeping `go test ./...` green.

1. Slice A: add `RenderGoWorkspaceFiles` (see Interfaces) plus unit tests
   asserting exact path sets for the `internal/generate` and
   `internal/compiler` fixture apps (`internal/compiler/testdata/native`,
   `internal/generate/module_nested_test.go` fixtures). Extend
   `internal/build/prepare.go` and `internal/build/workspace_cache.go`
   (collision check, source skip set). Rework `internal/generate/check.go`.
   Extend `module_nested_test.go` so the deleted-directories fixture also
   passes `scenery build` and `scenery test` paths, not only verification.
2. Slice B: new `internal/generate` (or dedicated package) editor-workspace
   sync; ownership sidecar under `.scenery/editor/`; source-classification
   change in `internal/build/source.go`; `GOWORK=off` audit across
   `internal/build`, `internal/testsuite`, harness runners; `scenery doctor`
   checks; pinned integration test that runs `go test ./...` in a fixture app
   with the managed workspace on the declared toolchain, plus a `gopls`
   resolution smoke test where the environment allows.
3. Slice C: prune command; `managed_generated_roots` policy change in
   `internal/graph`/`internal/spec` as applicable; `--materialize` mode;
   revision recomposition in `internal/compiler/compiler_workspace_revision.go`
   with a spec-revision bump in `internal/spec`; `revision_scheme_changed`
   diagnostic registered in the current diagnostic catalog; `RevisionRebind`
   in `internal/evolution` with digest-preserving evidence attachment and
   tests covering pending-plan invalidation and rename-evidence survival.
4. Slice D: source-schema attribute in `internal/spec`/`internal/scn`,
   generation routing in `internal/generate`, docs.
5. Every slice: update `docs/local-contract.md`, `docs/agent-guide.md`,
   `SKILL.md`, `docs/spec/go-implementation.md` (and related spec companions),
   affected `AGENTS.md` files (`internal/generate`, `internal/compiler`,
   `internal/evolution`, `internal/graph`, `internal/spec`), and
   `docs/knowledge.json` in the same change.

## Validation and Acceptance

Standard repo gates for every slice:

    go test ./...
    scenery harness self --summary --write

(Do not run `go install ./cmd/scenery`; use the worktree-local
`.scenery/harness/bin/scenery` build per root `AGENTS.md`.)

Slice A acceptance, in a fixture or scratch client app:

    rm -rf */scenerycontract internal/scenerygen
    scenery check -o json
    scenery test ./...
    scenery build -o json

All pass; none recreates the deleted directories; the build workspace contains
contracts, adapters, composition, descriptors, and generated main.

Slice B acceptance: after one `scenery check` in the fixture app,
`go test ./...` passes from the authored checkout with no per-shell
configuration, and `git status --porcelain` is empty (workfiles locally
excluded).

Slice C acceptance: pruning a legacy app leaves authored files intact and
`git status` shows only the one-time deletions; subsequent Scenery commands
produce no Git changes; a pending pre-migration plan fails with
`revision_scheme_changed`; a pre-migration rename receipt still satisfies
`scenery diff --semantic` through its rebind evidence.

Full regression matrix to cover across slices: clean checkout without
materialized generation through `check`/`test`/`build`/`up`; raw `go test`
after sync; `gopls` on the declared toolchain; paths with spaces and
non-ASCII; Windows absolute `use` paths; checkout relocation followed by
automatic `go.work` repair; deleted OS cache regenerating deterministically;
user-owned and tracked workfile protection; app nested in a monorepo with a
parent `go.work` (both shadowing directions surfaced by `doctor`); no
machine-specific absolute path in any workspace or implementation revision;
cross-contract generated imports; cache GC while `gopls` holds the previous
generation; descriptor pruning never deleting unmarked files; empty
`git status --porcelain` after every normal command.

## Idempotence and Recovery

Editor-workspace sync writes each generation into a fresh content-addressed
directory and commits by atomically rewriting `go.work`; a crash mid-sync
leaves the previous generation live. Re-running any Scenery command re-renders
and converges. Keep the previous complete generation until the new one is
committed (and retain one prior generation for `gopls` continuity; GC sweeps
older generations during sync). Identical content must not be rewritten or
have mtimes churned. Serialize sync per application root.

The managed `go.work` is recoverable by deletion: removing it and its sidecar,
or removing the OS cache, is always safe; the next successful compile
regenerates both. If the ownership digest diverges, Scenery stops writing and
reports — resolution is manual and explicit, never silent.

Pruning (Slice C) deletes only descriptor-owned, generated-marker files and is
safe to re-run; a partial prune leaves a smaller set for the next run.

## Artifacts and Notes

Dot-directory experiment (2026-07-14, `go1.26.0 darwin/arm64`): module
`example.com/app` with package `.scenery/gen/contracts/health`, explicit
import from `main.go`; `go build ./...` and `go build .` both exit 0. Recorded
because it corrects the design-phase assumption that such imports cannot
compile; the design still rejects them for identity-churn reasons.

Overlay limitation (design-phase experiment, Go 1.23.2): `go test -overlay`
fails for packages that exist only virtually because vet attempts to chdir
into the absent directory. Editor resolution therefore uses real external
modules, not overlays.

Follow-up (recorded, not blocking): decouple `contract_revision` from the
global `spec.CurrentRevision()` by introducing a dedicated contract-projection
identity, so unrelated generator/diagnostic changes stop rekeying contract
identity. Today that folding is why a spec bump indirectly invalidates rename
receipts.

Note on `go.work.sum`: filesystem `use` modules produce no sum entries; the
sum-file ownership policy will mostly be a no-op. Stated so nobody "fixes" it.

## Interfaces and Dependencies

New pure renderer (Slice A), `internal/generate`:

    // RenderGoWorkspaceFiles returns every expected generated Go artifact
    // (contracts, adapters, composition, descriptors) as workspace-relative
    // paths mapped to file bytes. It performs no filesystem writes and does
    // not inspect the checkout for stale materialized artifacts.
    func RenderGoWorkspaceFiles(result *compiler.Result) (map[string][]byte, error)

Editor sync entry point (Slice B), called by command orchestration after every
successful top-level compile:

    func SyncEditorWorkspace(result *compiler.Result) error

Ownership sidecar (Slice B), `.scenery/editor/go-work-owner.json`:

    { "path": "go.work", "digest": "sha256:...",
      "application": "...", "generator": "scenery.editor-workspace" }

Rebind evidence (Slice C), `internal/evolution`:

    type RevisionRebind struct {
        FromSpecRevision       string
        ToSpecRevision         string
        FromContractRevision   string
        ToContractRevision     string
        ContractProjectionHash string
        Reason                 string
        Digest                 string
    }

CLI surface changes: `scenery generate --target contracts --materialize`;
`scenery generate --prune-materialized-go` (name final at implementation);
new `scenery doctor` checks (workfile ownership conflict, parent `go.work`
shadowing, editor cache behind last valid revision); no new environment
variables (`GOWORK` is Go's own variable, set only on Scenery-spawned child
processes).

Path layout constants must come from one internal layout resolver (one
function mapping a module resource to its workspace-relative generated
directory); no other code concatenates `"scenerycontract"` or
`"internal/scenerygen"` directly.

Dependencies: no new Go module dependencies expected. Pinned-toolchain tests
depend on the repository's declared Go toolchain and, where available, the
selected `gopls` version; if `gopls` is unavailable in the environment, say so
and skip only that check.
