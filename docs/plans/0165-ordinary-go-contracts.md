# Ordinary Go Contracts Without Generated Git Noise

This ExecPlan is a living document and must be updated as work proceeds. Implementation accepted and completed on 2026-09-07. It supersedes the earlier recommendation to commit generated application Go contracts by default. It does not authorize changes to shared machine state.

Prepared: 2026-09-07. Inspected repository baseline: `c56e3e9614e58914e27a8536b171e4d979f32bbb` in `scenery-sh/scenery`. The original experiment used `86790aea52073f26fdfffc6108a6ac382581f5f5`. Reconcile against the executing agent's actual checkout before editing.

Repository destination: allocate the next unused permanent sequence number under `docs/plans/` according to `PLANS.md`, then register the file in `docs/plans/active.md` and `docs/knowledge.json`. The inspected completed index ends at 0164; this does not reserve 0165 or establish the executing checkout's next number. Do not rewrite completed plans 0163 or 0164.

## Purpose / Big Picture

A Scenery application should use normal Go package resolution without filling its Git history with generated contract changes. After one explicit `scenery generate`, or automatic preparation by a Scenery build/runtime command, ordinary `go doc`, `go mod tidy`, and `go test ./...` must work against application-imported generated packages. These packages live inside the application's existing Go module and are ignored by Git by default. No generated nested module, Scenery-owned root `go.work`, absolute generated-package replacement, or Go-command wrapper supplies their visibility.

The semantic authority remains `.scn`. Generated Go is a reproducible local projection, not authored application semantics. A fresh checkout requires generation before raw Go tooling can resolve those imports. This is an intentional, documented prerequisite, not something to hide with an editor daemon or a Git hook. Go does not automatically run code generation as part of a build. [R7]

This plan implements the Go tooling and generated-artifact cutover, preserves the recently shipped startup fixes, and ends with a separate runtime-ownership decision checkpoint. It does **not** bundle a machine-global agent/PostgreSQL redesign into the Go fix. The recommended subsequent direction remains application/worktree-owned local development, but that requires its own executable design and acceptance proof before implementation. Do not report this plan as having fixed the original managed PostgreSQL isolation failure.

The desired ordinary application layout is:

    app.scn                         authored, tracked
    .scenery.json                   authored, tracked
    go.mod                          authored dependency state, tracked
    go.sum                          dependency integrity state, tracked
    .gitignore                      authored ignore policy, tracked
    library/package.scn             authored, tracked
    library/service.go              authored, tracked
    library/scenerycontract/        generated, ignored by default
    .scenery/                       local evidence/cache/ownership state, ignored

The generated contract directory has no `go.mod`. An application-imported library facade such as `scenerylib_<name>` follows the same rule. Private executable composition and genuinely private build products may remain in the external build cache. The distinction is whether application Go source needs to import the package, not whether a file was generated.

## Progress

- [x] 2026-09-07: Prepared this corrected design from repository reads at the recorded baseline; no source changes or validation executions were performed for this plan.
- [x] 2026-09-07: Verified that external editor modules still exist and that completed plans 0163/0164 describe newer startup/diagnostic fixes. Their recorded test results are prior evidence, not fresh validation for this plan.
- [x] 2026-09-07: M0 — Reconciled clean `main` at `c56e3e9614e58914e27a8536b171e4d979f32bbb`, read the original experiment README and app-local instructions without running it, and inventoried current generation/build/editor consumers below.
- [x] 2026-09-07 13:10 UTC: M1 — Implemented one safe, deterministic in-module projection for all application-imported generated Go packages; ownership, idempotence and interrupted-publication release proof passed.
- [x] 2026-09-07 13:10 UTC: M2 — Integrated preparation/checking with existing commands and removed the synthetic editor-workspace path; external raw-Go, cache preparation, branch drift and user-workspace coexistence passed.
- [x] 2026-09-07 13:12 UTC: M3 — Completed external-checkout Go-tooling proof, regression validation, current documentation, and the ownership-verified cutover procedure. Final full Go/race/lint passed after test-boundary refinements; 92 roots passed 20 repetitions below the timing budget.
- [x] 2026-09-07: M4 — Recorded the separate runtime-ownership decision brief below; implementation remains outside this plan.
- [x] 2026-09-07 12:35 UTC: Implemented public package publication, strict output ownership, shared transaction recovery, Go-only/default/targeted generation routing, editor API removal, cache preparation and generated-presence watch behavior. A complete `go test ./...` passed before the latest documentation/harness refinements; this is not final acceptance.
- [x] 2026-09-07 12:35 UTC: Quick self-harness selected `cli-json-contract`, `compiler-or-generator`, `go-package`, and `release-sensitive-or-runtime`; passed with existing knowledge/architecture warnings. Added current docs and an explicit dry-run-first old-workfile cutover script. External proof is implemented in the existing generation release probe but has not yet run.
- [x] 2026-09-07 12:35 UTC: M4 decision brief recorded below. Runtime ownership implementation and managed lending acceptance remain deliberately outside this plan.

Update timestamps and split partially completed items at every stopping point. Check an implementation item only after its stated evidence exists.

## Surprises & Discoveries

- 2026-09-07, final validation: the first isolated timing pass found repeated
  full compilation/publication in unit fixtures above the 100ms budget. Narrowed
  fixture graphs, exercised revision rehashing at its stable boundary, seeded
  renderer-authenticated bytes for ownership tests, and kept actual durable
  publication/concurrent CLI proof in the release probe. The final 92-root,
  20-repetition run passed with maximum p95 50ms; no TestMain timing exemption,
  skipped test, or production synchronization shortcut was added.
- 2026-09-07, release proof: macOS's `/var` and `/private/var` temporary-path
  aliases caused the parent recovery probe to address the candidate's journal
  through a different lexical app root. The disposable probe now resolves the
  same physical root before starting either process. This is not a migration of
  existing transaction records or a relaxation of path confinement.
- 2026-09-07, webhook proof: the source-only copy required normal `go mod tidy`
  after selecting the current local framework replacement. The proof script
  now records that step; generated imports resolve inside the application
  module, with no synthetic generated-module dependency.
- 2026-09-07, race validation: one parallel release run observed the existing
  router retry test's 50ms deadline under load. Twenty isolated race repetitions
  and subsequent full race suites passed; no router behavior or budget was changed.

- 2026-09-07, implementation: `atomicWriteSet` in `generate_go_artifacts.go`
  has in-process backups but no durable journal, and expected-path descriptors
  can fail verification without blocking overwrite. Reuse the existing
  `workspacetx` lock/journal/recovery protocol for generated publication and
  strengthen descriptor checks rather than adding an independent transaction
  format. Recovery must cover the rename-to-backup interruption window.
- 2026-09-07, implementation: the experiment exists and its README explicitly
  labels ordinary `go test` as compilation-only and managed startup as unproved.
  Its `go.mod` requires `86790aea5207` but deliberately replaces `scenery.sh`
  with this source checkout. That replacement, not the nominal version, selects
  the runtime for a disposable copy. No original app or database was changed.

The reviewed main branch moved after the lending experiment. At `c56e3e9614e58914e27a8536b171e4d979f32bbb`, detached startup already passes a private result pipe, observes the supervisor with `exec.Cmd.Wait`, and preserves structured diagnostics. Optional dotenv behavior and more precise configuration/capability diagnostics are also present. Do not rebuild or replace that work merely because the original report described the old behavior. [R2][R3]

The current editor implementation still creates separate cached modules and manages the root `go.work`. The existing Go renderer and artifact writer can already produce materialized contracts. Reuse the renderer and ownership-verified artifact transaction rather than implementing a second generator. [R4][R5]

Completed plan 0164 explicitly states that matching PostgreSQL port bindings do not prove exclusive ownership. Its early conflict check is a correctness improvement, not full resource isolation. Private agent homes still share globally named PostgreSQL objects in one Docker daemon. [R3]

The local experiment directory was not available to the author of this plan. Its reported HTTP/client tests, ten two-borrower races, validation, and API/PostgreSQL restart persistence are user-supplied evidence. The application had no Go unit tests. Do not call its successful `go test ./...` a unit-test result or claim that standalone-binary success proves managed `up` success.

Append implementation discoveries with a command, diagnostic, exact path, or evidence file. A failed hypothesis is useful evidence; do not turn it into a permanent workaround without revisiting the design.

## Decision Log

- 2026-09-07, implementation: classify exact generated descriptor paths instead of skipping entire managed roots. The lightweight compiler reader is classification only; generation independently authenticates content before writes. Watch stores generated presence separately from authored hashes and only advances that generated baseline after a successful build.
- 2026-09-07, implementation: serialize recovery with a kernel file lock in the existing transaction directory. Without this gate, two stale-owner recoverers could race and remove a new publisher's lock. The journal/owner protocol remains singular; no new public artifact or environment knob is introduced.
- 2026-09-07, implementation: release database cleanup now accepts only the immutable container ID returned by the probe's successful creation. Both relevant probes explicitly create disposable tmpfs containers; they create/delete no named data volume. This bounded harness isolation repair is required before release execution, not a managed-runtime redesign.

- 2026-09-07, developer preference clarified in review; plan author: generated application Go contracts will be real in-module packages and ignored by Git by default. Go visibility and Git tracking are separate choices. The developer accepts a generation prerequisite and does not want generated source flooding ordinary commits.
- 2026-09-07, plan author: keep `.scn` as the only semantic authority and one deterministic Go renderer. Do not replace typed contracts with reflection, handwritten duplicate types, or Go-comment declarations.
- 2026-09-07, plan author: reuse existing commands. Add no `scenery tidy`, `prepare`, editor daemon, Git hook, public generation-mode selector, or environment knob. Remove mechanisms made obsolete by ordinary package resolution.
- 2026-09-07, plan author: `generate` and execution-oriented commands may refresh ignored generated Go; `generate --check`, `check`, and graph/inspection reads do not materialize it in the application tree. Read-only checking may use the existing private generated workspace to verify the current expected ABI. It must not mistake stale local output for current output.
- 2026-09-07, plan author: use existing module dependency and build/provenance information to select and record the generator. Do not create another independent version lock or silently download a matching CLI. A different installed CLI is not automatically the intended generator.
- 2026-09-07, plan author: generated source bytes used by a build remain part of implementation identity; generated files are not authored workspace inputs. Avoid a revision cycle in which a generated descriptor hashes itself.
- 2026-09-07, plan author: publishing a Go module is an explicit distribution decision. It must include required generated source, but that does not require every application development commit to track it. Use the same rendered packages; do not add a second publishing renderer.
- 2026-09-07, plan author: preserve plans 0163/0164 as shipped baseline and keep runtime-ownership redesign outside this implementation scope. Prepare a concrete subsequent decision brief instead of silently expanding into a global-agent rewrite.

## Outcomes & Retrospective

Completed in the designated checkout, without commit or push. Fresh applications
now obtain ordinary ignored in-module contracts and imported library facades
with one `scenery generate --target contracts`; build/test/up preparation uses
the same projection. Default generation publishes Go and selected TypeScript
as one owned transaction. Read-only checks report freshness independently of
native verification, and graph reads create no editor workspace. Synthetic
editor modules, workfile synchronization, old generation flags and editor-only
doctor/report fields were removed. Current contracts and exact fixture ignore
rules were updated; explicit cutover preserves unverified/user-owned workfiles.

External source-only Git, raw Go, offline prewarmed tidy, branch switching,
idempotence, cache preparation, library facades, concurrent publication,
interruption recovery and safe workfile cutover passed. Full Go/race/lint,
TypeScript fixtures/conformance, release self-harness, release gate and the
disposable webhook example passed. Final timing covered 1,840 runs across
92 exact test roots with maximum p95 50ms. Evidence, warnings and deliberate
conditional skips are enumerated below.

The original application, shared resources and installed CLI were not migrated.
M4 recommends app/worktree runtime ownership with durable data identity and
explicit optional machine services; its implementation, data migration and the
original lending application's managed `up` proof remain separate work. This
plan does not fix or claim full managed PostgreSQL/runtime isolation.

## Context and Orientation

The repository is `scenery-sh/scenery`; the developer's checkout is `/Users/petrbrazdil/Repos/scenery`. The original independent application is `/Users/petrbrazdil/Temp/scenery-library-desk.K8zIXY`, with its report in `README.md`. Read that report and app-local instructions if the directory exists. Treat the original app and its existing database as read-only; use a disposable source-only copy for any execution.

A contract projection means generated Go types, constructor/dependency interfaces, outcomes, and application-imported facades derived from a compiled `.scn` graph. A managed root means a generated output directory permitted by `workspace.managed_generated_roots`. A generation receipt means the existing ownership/digest evidence that identifies exactly which files Scenery may replace or remove; it is not evidence that arbitrary files under the directory are disposable.

Read `AGENTS.md`, `PLANS.md`, the applicable child `AGENTS.md` files, `ARCHITECTURE.md`, and the current generation/runtime sections of `docs/local-contract.md` and `docs/agent-guide.md`. Repository artifacts are English; developer updates and handoffs are Czech. Current rules requiring external Go/editor caches are the contracts this plan intentionally changes; update those rules together with the cutover, rather than treating them as a reason to retain two paths. [R1][R4]

Relevant implementation entry points are:

| Area | Paths and symbols to inspect |
| --- | --- |
| Go rendering and safe artifact publication | `internal/generate/generate_go.go`: `GenerateAll`, `GenerateGoContracts`, `GenerateGoContractsFromResult`, `renderExpectedGoContractFiles`, `finishGeneratedFiles`, stale-file and descriptor verification helpers |
| Synthetic editor modules | `internal/generate/editor_workspace.go`, its tests, and `internal/generate/api/editor.go`: `SyncEditorWorkspace`, merge blocks, generated module directories, ownership records, Git exclusion and pruning |
| Command routing and build integration | `cmd/scenery/contract_commands.go`, `contract_command_helpers.go`, `build_generate_hooks.go`; `internal/build/generate_hooks.go`, `prepare.go`; `cmd/scenery/dev_build_pipeline.go`, `watch.go`, worker/test entry points |
| Go ABI and source/build identities | `internal/compiler`, `internal/parse`, `internal/generate`, `internal/build`, `internal/gotarget`; keep `go/packages` inside `internal/parse` |
| Existing cross-process proof | `cmd/scenery/harness_self_generate.go`, especially `runHarnessGenerationCompileProbeCheck`; it already covers materialized contracts, invalid-implementation bootstrap, and editor-workspace/raw-Go behavior |
| Startup fixes to retain | `cmd/scenery/dev_detach.go`, `dev_detach_startup.go`, `cli_diagnostic_error.go`, `appenv.go`, `dev_services_postgres.go`, and their tests |
| Doctor/reporting cleanup | `internal/doctor/checks.go`, `cmd/scenery/doctor.go`, generated-output inspections, CLI schemas/help and harness expectations |
| Disposable native fixture | `testdata/apps/basic`: declared module `example.com/basicapp`, generated root `service/scenerycontract`, development Go target |

Search hits may lag the latest commit; fetch/read the actual working-tree files before choosing edits. Remove all live `SyncEditorWorkspace` callers, not only the `generate` command's caller. The root compiler must remain independent of generation and runtime orchestration.

The original failure has a precise cause: workspace-only synthetic modules are not supplied to `go mod tidy` as ordinary packages in the application's own module. Materializing those packages in-module resolves that mismatch; committing them does not participate in package lookup. [R4][R7]

## Milestones

### M0 — Establish a truthful baseline and bounded scope

Record the checkout SHA and pre-existing changes. Read plans 0163/0164 for previous evidence, inspect the actual implementation, and identify remaining regressions without recreating already completed fixes. Allocate the plan ID from the local tree and update the living indices.

Inventory every generated Go package imported by authored code, including library facades. For each, record its stable import path, containing declared Go module, permitted output root, direct generated dependencies, and whether it currently exists only in an editor/build workspace. Also identify private composition packages that do not need source-tree publication.

Finish M0 with a short before/after command-and-artifact table in this plan. The generator cutover does not depend on resolving the global agent architecture first.

M0 inventory (current implementation reads, not new execution proof):

| Package family | Stable import and module/root mapping | Dependencies and publication |
| --- | --- | --- |
| Package contract | `<go_contract.import_path>/scenerycontract`; `generateModuleContract` writes `<local module source>/scenerycontract` inside its declared `go_module` | Typed values, capabilities and cross-package contracts; currently available through synthetic editor modules, now ordinary declared generated roots. |
| Library facade | `<go_contract.import_path>/scenerylib_<name>`; `generateLibraryArtifacts` writes the sibling managed root | Its package contract, implementation backend and `scenery.sh/library`; publish complete deterministic facade/backend/export files, no nested `go.mod`. |
| Native fixture | `example.com/basicapp/service/scenerycontract`, module `example.com/basicapp`, root `service/scenerycontract` | App-authored service imports it; used by mandatory external-checkout proof. |
| Lending experiment | `example.com/library-desk/library/scenerycontract`, module `example.com/library-desk`, root `library/scenerycontract` | SQL capability types and framework runtime; original source/state remain read-only. |
| Private executable glue | `<app module>/internal/scenerygen/{adapters,composition,assistantassets}` and synthetic executable main | Imported by private composition/build entrypoints, not authored app Go; remains in the existing build cache and uses identical public projection bytes. |

| Surface | Before | Required after |
| --- | --- | --- |
| `generate` / contracts target | TypeScript plus editor modules / explicit `--materialize` export | One declared Go/TypeScript artifact set / narrow Go bootstrap |
| `compile`, check/inspection | CLI compilation and successful check may sync editor workspace | Read-only graph/ABI/freshness evidence, no workfile writes |
| Build preparation/cache | `SyncEditorWorkspace`, private Go overlay; cached refresh omits source-tree preparation | Current in-module Go before all implementation/build/cache consumers; private composition retained |
| Doctor/reporting | `app.editor_workspace`, `data.editor_workspace`, external-module skip rules | Remove editor-specific predicate/field; retain truthful prerequisite scope |
| Provenance | App `go.mod` and replacement, producer identity, build framework/generator fingerprints | Preserve these authorities and record matching candidate/dependency evidence; no new version lock or automatic download |

### M1 — Generate ignored, ordinary Go packages safely

Use one immutable compiler result and the existing renderer to prepare application-imported packages under declared managed roots in their owning Go modules. Never introduce nested generated `go.mod` files. Validate import-path/module-root mapping and reject paths crossing undeclared modules, symlinks, or the app boundary. Do not bypass invalid package ABI diagnostics for different instances of one source package.

Generation must work before application implementation compiles. A missing generated import or a temporarily wrong constructor signature must not prevent a valid `.scn` contract from being generated. Invalid `.scn` must fail without changing the previous complete artifact set. Pure Go-contract generation must not start an agent, Docker, PostgreSQL, Victoria, an application process, or TypeScript tooling.

Reuse the current artifact transaction for staging, validation, publication and recovery. Delete obsolete files only with descriptor/digest-backed ownership proof, including removed services and moved generated roots. A generated marker or `.gitignore` entry alone is not ownership. Fail closed on hand-edited owned output or foreign files at a required output path. Missing cache metadata must not authorize broad deletion.

Publish only byte changes. An unchanged second generation leaves generated bytes and mtimes unchanged. Go implementation-body edits that do not change a contract must not rewrite contract files. Keep timestamps, absolute machine paths and irrelevant build metadata out of generated source. Preserve semantic revision checks without adding a whole-application-implementation revision to each contract file.

Use authored `.gitignore` entries for the exact generated roots. Templates/examples receive these entries in their source changes; existing disposable apps receive them once during the explicit setup/cutover. Generation does not rewrite `.gitignore`, Git's index, or `.git/info/exclude` on every run. Do not ignore a parent containing authored implementation, and do not ignore `go.mod` or `go.sum`. Git absence must not make generation unusable.

### M2 — Integrate the existing workflow and delete the editor-module machinery

Wire automatic Go preparation before implementation analysis/build in `build`, `test`, `up`, and other existing execution paths that need those packages. Refresh from current source even on build-cache hits and after branch changes. During `up`, changes to relevant `.scn`, module/lock/toolchain inputs or missing generated output must trigger preparation. Generated writes must not trigger a self-sustaining rebuild loop.

Make default `generate` publish the ordinary Go projection together with its existing selected TypeScript outputs through one planned artifact-set operation. `generate --target contracts` is the narrow Go-only bootstrap. A targeted TypeScript generation stays targeted; it does not silently regenerate Go as a compiler side effect. Private cached composition uses the same current projection bytes, not a different editor ABI.

Make `check` report missing/stale local Go projection as a failed check predicate and preserve native ABI verification against current expected contracts. It must not report success after quietly repairing the output. `generate --check` also compares without source writes. Both may render privately for comparison; neither writes application generated files, root workfiles, ignores, or module files. A valid graph with an invalid implementation still exposes its graph and exact diagnostics.

Remove editor sync from graph reads and inspection. Eliminate synthetic generated contract modules, exclusive/merge root-workspace management, cache-generation retention used only for that mechanism, editor-workspace-specific doctor readiness and `data.editor_workspace` reporting. Replace the latter only with necessary generated-artifact status using the existing generation result/diagnostics. Update the current schema and all consumers together; do not preserve an alias field or old decoder.

Remove `--merge-editor-workspace`, `--prune-materialized-go`, and the now-redundant `--materialize` switch from the normal generation grammar after auditing callers. Unknown retired switches return the current invalid-request diagnostic with the current replacement command, not compatibility behavior. `--target contracts` remains. Explicit publishing uses the same complete generated package files and the publisher's deliberate Git/release process, not a second generation mode.

Do not delete genuine private build workspaces, user-managed workspaces, target-specific toolchain resolution, or revision-bound source transactions as collateral cleanup.

### M3 — Prove ordinary Go usage and finish the cutover

Extend the existing release generation probe with a disposable Git application outside the Scenery checkout, starting with authored files only. Do not pre-seed generated Go, copy an old `go.work`, or run `--materialize` as a hidden workaround. Use the candidate worktree-local CLI and a fixture dependency on the matching framework source/revision. Record local replacement usage separately from a published-dependency lane.

Prove the acceptance scenarios below, update current docs and fixtures, and run the exact validation matrix. Preserve native detached startup and truthful-scope behavior introduced by 0163/0164.

Provide an ownership-verified one-time procedure for existing Scenery-managed workfiles. Use existing ownership evidence to remove only unchanged, exclusively Scenery-owned bytes, or only an exactly verified managed block from a merged file. Preserve all user-owned bytes and user dependencies. Unverifiable workfiles stop the cutover with a precise manual review action. Never recommend blindly deleting `go.work` or `go.work.sum`. This is an explicit migration procedure, not a permanent fallback decoder/runtime path.

No installed CLI, shared agent or existing app is migrated automatically. Do not recursively delete global editor caches merely because the new runtime no longer needs them.

### M4 — Separate runtime-ownership decision checkpoint

Produce a decision brief alongside this plan's final handoff; no runtime implementation is authorized by this milestone. The brief must recommend one ownership boundary, inventory affected current consumers, name what would be deleted, and identify data migration and acceptance requirements.

The recommended starting direction is one app root/worktree owning its ordinary local runtime and managed capabilities, with machine-wide edge/domain facilities intentional and optional. Distinguish durable capability identity from process/session identity: data must survive CLI upgrades and cannot be keyed solely by CLI/spec revision, session ID or PID. Container, volume, credentials, locks, control endpoints and cleanup authority must share the same ownership boundary. A matching port or name is not proof of ownership.

The brief must address `ps`/logs/discovery, app dashboard dependencies, frontend routing, durable workers, storage sharing contracts, PostgreSQL tools/snapshots, optional Victoria startup, domain/edge publication, and explicitly operator-managed deployment. Do not assume these consumers can simply be disconnected from the global agent without consequences.

Compare application-owned resources with one genuinely machine-global installation requiring coordinated upgrades. Do not introduce automatically version-namespaced databases, multiple compatible protocol decoders, another broker above both owners, or new user environment knobs as a default compromise. If app ownership is selected, the subsequent implementation must remove competing default ownership rather than keep two equal launch paths.

Before that subsequent plan can close, it must prove two incompatible CLI versions running two independent worktrees while a sentinel app remains uninterrupted; one owner for concurrent same-root `up`; ownership-verified restart/endpoint reconciliation; preserved data; degraded non-blocking optional observability; and the lending experiment through **managed `scenery up`**, not a standalone substitute. Measure startup and resource cost before selecting any shared-resource optimization.

M4 is complete when the bounded brief and unresolved choices are recorded. It is not complete runtime redesign and must never be reported as such.

#### M4 decision brief: one application/worktree owner

Recommend one canonical app root/worktree as the default local runtime owner,
with a small app-owned supervisor/control endpoint. Its durable capability
identity must outlive processes, sessions, CLI/spec revisions and checkout
relocation through an explicit relocation/adoption rule. Worktrees get independent
capabilities by default; sharing storage/data requires an explicit operator
contract, not matching ports, database names, or inherited environment state.

Current consumers and required migration boundaries:

| Consumer | Requirement for the subsequent executable design |
| --- | --- |
| `ps`, logs, inspect and discovery | Read app-owned session/control records; a machine index may discover owners but must not become a competing launcher. Preserve diagnostic/report-token evidence when an owner cannot be contacted. |
| App dashboard | Rebind session selection, task execution, approvals and lifecycle to the app owner. Define disconnected/read-only behavior before removing global-agent calls. |
| Frontend routing and workers | One owner launches Go runtime, frontend dev/build processes and durable workers; concurrent same-root `up` attaches or fails deterministically, never creates a second owner. |
| Storage | Durable cell/store identity and sharing rules are explicit; cleanup cannot infer ownership from a root path, PID or port. Relocation and worktree creation must not accidentally adopt/delete another app's files. |
| PostgreSQL and snapshots/tools | Container, volume, credentials, locks, admin endpoint and cleanup authority share the same app identity. Migrate existing databases/volumes using verified ownership plus backup/restore and rollback checkpoints; never version-namespace data by CLI/spec. Snapshots, SQL consoles, schema/reset tools and migrations must resolve the same owner. |
| Optional Victoria | Observability starts as an optional app-owned sidecar with bounded readiness and degraded non-blocking behavior. Preserve logs when observability fails. Measure startup/memory before considering explicit sharing. |
| Edge/domain publication | Machine-wide Caddy/domain facilities may remain explicit optional operator infrastructure; an app publishes a verified route lease to them. They do not own the app's database or default runtime. |
| Operator-managed deployment | SSH/system service deployments keep an explicit operator authority and data lifetime; do not silently apply local worktree cleanup semantics remotely. |

Delete competing default global-agent app/runtime/PostgreSQL launch ownership,
implicit globally named database provisioning, and editor-workspace lifecycle
coupling. Retain one current protocol; do not add cross-version decoders, a broker
above two equal owners, automatically versioned databases or new environment
knobs as the default compromise.

The alternative is one genuinely machine-global installation with coordinated
CLI/agent upgrades and a single supported runtime version. It offers intentional
resource sharing but makes independent worktrees/CLI versions an operational
coordination problem. Given the developer's independent-checkout workflow, app
ownership is the recommended boundary; startup/resource measurements should
precede any later sharing optimization.

Unresolved decisions for the next ExecPlan: durable app identity on relocation
and copying, explicit shared-store authorization, safe adoption/migration of
existing global PostgreSQL data, dashboard discovery UX, and optional
edge lease reconciliation. No data migration is authorized here.

Acceptance for that next plan must run two incompatible CLI versions in two
independent worktrees while a sentinel app remains uninterrupted; prove one
owner for concurrent same-root `up`, fingerprint-verified restart/endpoint
reconciliation, preserved data, degraded optional observability, and the lending
experiment through managed `scenery up`. Standalone binary success and this
plan's Go-tooling proof do not substitute for that acceptance.

## Plan of Work

Start with generation and its current consumers rather than replacing `go.work` with generated `require`/`replace` entries. That substitute would retain fabricated module boundaries and cache-location lifecycle problems.

Keep a single renderer for source-tree contracts and private build verification. A small private preparation function is acceptable if it consolidates existing call sites. It must accept a compiled snapshot and existing target/provenance data, not discover global runtime state. Preserve the `internal/generate/api` leaf and injected `internal/build` hooks; do not make the compiler import the generator or make build link a second copy through a new umbrella dependency.

Audit revision, watch and cache rules together. Generated roots and their ownership evidence remain excluded from authored workspace revision and source-change plans. A declared output root is not permission to hide unrelated authored input. Actual generated Go consumed by a build remains in the content-addressed build-input manifest. The dependency order must be authored inputs plus generator identity, then rendered bytes, then build-input identity; descriptors must not create self-referential hashes.

For provenance, the application's `scenery.sh` dependency and deliberate local replacement are the existing authority for the selected framework source. CI builds/uses the intended CLI at an absolute worktree-local path and records its producer/build/spec identity. Preserve current compatible-agent reuse semantics; do not confuse that with reproducible generator selection. If current metadata cannot establish the generator/runtime match, record that exact gap in M0 and resolve it with existing provenance mechanisms, not a new tool-version subsystem. Never silently choose an older protocol or update an app dependency because a CLI is installed.

Treat publication as delivery of complete source, not a requirement on daily application history. Document that a repository consumed directly as a Go module must include its needed generated packages in the published source revision. An app owner may explicitly track those files for distribution. Do not untrack existing user-owned published sources or change the policy for committed TypeScript/golden test fixtures in this task. [R7]

Update the normative generation sections in `docs/spec/`, root/child `AGENTS.md`, `docs/local-contract.md`, `docs/agent-guide.md`, `SKILL.md`, `README.md`, `docs/app-development-cookbook.md`, `ARCHITECTURE.md`, help/schema output, `.gitignore` examples and `docs/knowledge.json` in the cutover. Remove claims that successful compilation creates `go.work`, that raw Go requires an editor workspace, or that ordinary application contracts must be committed. Historical completed plans remain unchanged.

Do not remove the application harness or change all readiness semantics as an incidental cleanup. In this plan, remove only obsolete editor-workspace assertions and keep already corrected prerequisite/warning/skip reporting. A larger public command reduction belongs to the separately approved runtime/validation design.

## Concrete Steps

All repository commands below run from the agent's designated Scenery implementation checkout. `/Users/petrbrazdil/Repos/scenery` identifies the developer's supplied checkout, not permission to overwrite unrelated work. Use an isolated implementation worktree if required by the developer's operating context. No `git reset --hard`, global clean, shared `go install`, shared restart, commit or push is implied by this plan.

First record the baseline and discover the actual numbered plans:

```sh
pwd -P
git rev-parse HEAD
git status --short
git ls-files 'docs/plans/[0-9][0-9][0-9][0-9]-*.md'
```

Read the experiment without executing it:

```sh
if test -f /Users/petrbrazdil/Temp/scenery-library-desk.K8zIXY/README.md; then
  cat /Users/petrbrazdil/Temp/scenery-library-desk.K8zIXY/README.md
else
  printf '%s\n' 'Original experiment README unavailable; original live proof remains unverified.'
fi
```

Find current consumers and do not assume search results from the original revision are complete:

```sh
rg -n 'SyncEditorWorkspace|EditorWorkOwner|editor_workspace|merge-editor-workspace|prune-materialized-go|materialize' cmd internal docs/schemas
rg -n 'managed_generated_roots|workspace_revision|GOWORK|scenerycontract' internal/build internal/compiler internal/generate cmd/scenery
```

Provision the worktree-local build according to Fresh Worktree Preflight:

```sh
./scripts/build-dashboard-ui-embed.sh
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
```

Read `.scenery/harness/agent-context.json` and run the exact union of `changed_area.recommended_commands` and the explicit commands below. Refresh it after meaningful changes. Store evidence under `.scenery/harness/ordinary-go-contracts/`, not in Git. Record the baseline, binary identity, command cwd, exit code and result rather than only saying a suite was green.

Implement in M1/M2 order; keep the same working tree testable. Extend the existing release generation probe instead of creating a public test wrapper. Within its disposable app, the essential child commands are:

```sh
# Executed by the existing release harness with an absolute candidate CLI path:
"$SCENERY_BIN" generate --target contracts -o json
go doc example.com/basicapp/service/scenerycontract
go list -json example.com/basicapp/service/scenerycontract
go mod tidy
go test ./...
GOWORK=off go doc example.com/basicapp/service/scenerycontract
GOWORK=off go mod tidy
GOWORK=off go test ./...
"$SCENERY_BIN" generate --target contracts --check -o json
"$SCENERY_BIN" check -o json
"$SCENERY_BIN" build --target development -o json
```

Here `SCENERY_BIN` is a harness-local shell variable set to the candidate's absolute path, not a new Scenery configuration knob. `GOWORK=off` is an adversarial test condition proving independence from editor workspace state, not the recommended user workflow. Run the ordinary commands with ambient workspace redirection removed first. Run the no-network variation only after explicitly prewarming real Go dependencies; `GOWORK=off GOPROXY=off go mod tidy` must still succeed without synthesizing a contract-module dependency.

The harness initializes and records the disposable fixture's authored Git baseline. A first tidy may legitimately normalize dependency files: record that separately, check that it adds no generated-module `require`/`replace`, and require the second tidy to be a no-op. Do not conceal legitimate `go.mod` or `go.sum` changes behind a claim that every operation leaves Git clean.

Before any existing release command starts real resources, audit its resource naming and cleanup path. It must use its own disposable state and objects and must not repair the shared `scenery-postgres` container. If that guarantee cannot be established, record the exact blocker and fix the harness isolation before execution. Do not use the private-agent-home setting alone as evidence of database isolation.

## Validation and Acceptance

Expected changed-area classes are `go-package`, `compiler-or-generator`, `cli-json-contract`, and `release-sensitive-or-runtime` because execution preparation changes. Use the actual agent-context spelling/union if it differs. Documentation and child verification requirements are cumulative. Real Go-toolchain, Git subprocess and runtime proof belong in the release integration harness; ordinary Go test roots must remain under the repository's repeated isolated 100ms p95 limit. [R1][R4]

From the repository root run:

```sh
go test ./internal/generate ./internal/compiler ./internal/parse ./internal/build ./internal/doctor ./cmd/scenery
go test ./...
golangci-lint run ./...
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
.scenery/harness/bin/scenery harness self --summary --write
.scenery/harness/bin/scenery harness self --release --summary --write
scripts/release-gate.sh
git diff --check
```

The release run can supersede a separate default run according to the root matrix; record which supersession was used. Quick classification still needs to be refreshed after edits. The optional external-app lane in `scripts/release-gate.sh` may be skipped only when its existing `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` is unset. Report that skip; it does not excuse the mandatory disposable external-checkout Go proof in this plan. Do not set that variable to the original experiment or another live app merely to remove the skip.

If dashboard source/contracts are changed rather than just its embedded build being provisioned, additionally run, from `apps/console`, `bun run lint`, `bun run typecheck`, and `bun run build`, then from the repository root `.scenery/harness/bin/scenery harness ui -o json --write` using the repository's isolated browser fixture. Otherwise record that no dashboard source/contracts changed and this conditional browser acceptance was not selected.

For every new or materially changed exact top-level Go test root, use the repository's isolated test-binary measurement path for 20 repetitions and record p95 below 100ms. Do not hide real process proof in `TestMain` or slow subtests. If infrastructure or toolchain absence blocks a mandatory command, report the exact command and prerequisite and leave its acceptance incomplete; compilation is not a substitute.

Acceptance requires all of the following observable scenarios:

| Scenario | Required evidence |
| --- | --- |
| Fresh source-only checkout | Before generation the generated package is absent; after one `generate --target contracts`, ordinary Go commands succeed. No generated root workfile, nested module or synthetic replacement appears. |
| Normal module resolution | `go list -json` resolves the contract's `Dir` under the app's declared managed root and its owning `Module` is the app module; both ambient-workspace-free and `GOWORK=off` runs pass. |
| Git noise | Generation does not change tracked authored files or add generated source to the index. `git status --short` has no generated paths; `git check-ignore` confirms exact generated roots. Track legitimate dependency normalization separately. |
| Idempotence | A second generation has no changed output, preserves generated mtimes, and a second tidy leaves dependency files unchanged. |
| Bootstrap | Valid `.scn` with missing contracts and an intentionally incorrect Go implementation still permits contract generation. ABI checking then reports the implementation error. |
| Drift | Change one contract input/outcome. Both check-only commands fail without repairing application files; write generation repairs output; check succeeds after implementation is made consistent. |
| Removal and branch switch | Rename/remove an operation or service and exercise an A-to-B-to-A checkout. No obsolete owned symbols remain after regeneration; unknown files and edited outputs are never deleted. |
| Conflicts and recovery | Symlink, traversal, undeclared-root, hand-edited output, missing/corrupt receipt, interrupted publication and two concurrent generators all have deterministic safe outcomes; no permanent partial set is accepted. |
| Workspace coexistence | An existing user `go.work` remains byte-identical and usable. A verified old Scenery workfile has an explicit one-time cutover. Unverified ownership never triggers deletion. |
| Revisions and watch loop | Regeneration alone leaves authored workspace/contract identities unchanged; actual build input changes affect implementation identity; generated writes cause no rebuild loop. |
| Automatic preparation | Starting from missing output, each relevant existing Scenery execution path prepares current contracts before build. A stale build-cache hit cannot reuse an old contract projection. |
| Library and TypeScript coverage | Application-imported `scenerylib_<name>` facades remain resolvable without editor modules; targeted TypeScript generation remains targeted and current committed client fixtures/conformance checks pass. |
| Startup regression preservation | Existing native release probes retain classified early failure, child-exit/result ordering, report-token preservation, optional dotenv behavior, successful detach/reacquisition and safe conflict reporting. No new broad smoke suite is required. |
| Honest scope | Doctor/harness do not claim live application readiness from generated-file or prerequisite checks. Remaining global-resource isolation limits are explicit in the handoff. |

A filesystem transaction can provide coherent Scenery-owned snapshots and crash recovery without making multiple directory replacements globally atomic to arbitrary concurrent raw Go readers. Do not promise that `go test` racing a source-changing generation sees a globally atomic tree. Keep the operation bounded and document the ordinary prerequisite: complete generation before running raw Go commands against changed contracts. Do not solve this with symlinked module trees, a permanent daemon, or a new wrapper.

## Idempotence and Recovery

All generated changes are staged, confined and ownership-verified. A failed render/validation keeps the previous committed artifact set. An interrupted publish uses existing transaction recovery before another Scenery-owned consumer proceeds. Two generators for one app serialize on the same ownership boundary; different worktrees do not share a generated-output lock or output directory.

When current source removes a previously declared output root, cleanup may remove only files authenticated by the previous generation's safe ownership record. Never broaden deletion to everything under an old parent. If proof is absent or content differs, report the exact conflict and preserve bytes.

Do not use `git clean -fdx`, recursive application `.scenery` deletion, or global cache deletion as recovery instructions. `.scenery` contains more than disposable generated Go; deleting it can lose unrelated plans, evidence or runtime state. Use disposable copies for destructive/interruption tests.

Do not change `scenery-postgres`, `scenery-postgres-data`, shared agent state, edge/DNS setup, login services, the installed CLI, the original experiment, or existing applications. Do not call `scenery system agent restart` or global `docker rm` during this plan. Process cleanup may target only exact test-created, fingerprint-verified owners; database cleanup may target only exact test-created, ownership-verified resources. Keep logs and evidence after a failed mandatory probe until the cause is classified.

Rerunning generation after legitimate source changes is normal. It must not overwrite hand edits to generated files silently, automatically untrack files, migrate application dependencies, or select another runtime protocol. Publishing and any later shared-data migration remain explicit operator decisions.

## Artifacts and Notes

### Implementation evidence (2026-09-07)

All repository commands below ran from `/Users/petrbrazdil/Repos/scenery` unless
the command explicitly selects an app root. Evidence is local and ignored under
`.scenery/harness/ordinary-go-contracts/`.

The candidate was `.scenery/harness/bin/scenery`, built from baseline commit
`c56e3e9614e58914e27a8536b171e4d979f32bbb` plus this uncommitted change. Its producer
was `v0.3.7-0.20260907112610-c56e3e9614e5+dirty`, Go `go1.27.0`, current spec
`sha256:ca92e9336c4af2fa74594291ddac6ed1156ca89b4bad876cca06f31a4f35255f`.
The external fixture deliberately replaced `scenery.sh` with this exact source
checkout. This is a local-replacement proof, not a published-dependency proof.

`release-passed.json` records every release step and command, the disposable Git
baseline, all ordinary Go command outputs, resolved package/module directories,
unchanged second-generation mtimes, branch drift, automatic build/test repair,
first-tidy dependency diff and second-tidy stability. The first tidy added real
transitive framework dependencies; it added no generated-module requirements or
replacements. The published-dependency lane was not run.

The interruption probe killed its fingerprint-verified candidate during a
64-package/192-file publication, recovered the complete prior set through the
ordinary compiler, regenerated, and passed two concurrent candidate CLIs.
Seven explicit workfile cutover cases preserved user bytes and `go.work.sum`;
unverified, edited, duplicate-block and wrong-app evidence failed closed.
Library facades passed direct `GOWORK=off go mod tidy`, `go doc` and `go test`.

| Validation | Result and evidence |
| --- | --- |
| `go test ./...` and `go test -race ./...` | Both passed again after all final unit-test refinements. |
| `go test ./internal/generate ./internal/compiler ./internal/parse ./internal/build ./internal/doctor ./cmd/scenery ./internal/evolution ./internal/workspacetx ./internal/generate/api` | Passed after final focused test refinements; API leaf has no test files. |
| `golangci-lint run ./...` | Passed, 0 issues, after final refinements. |
| Three `go run ./cmd/scenery generate --target typescript_client.public_api --app-root <root> -o json` commands for `internal/compiler/testdata/native`, `internal/compiler/testdata/house`, and `testdata/assistant` | Passed; all returned `changed: []`, so no committed client byte diff was needed. |
| `bun test internal/generate/testdata/typescript_client_conformance.test.ts` | Passed, 26 tests / 98 assertions. |
| `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json` and `.../tsconfig.catalog.json` | Both passed, including release repetition. |
| `go build -o .scenery/harness/bin/scenery ./cmd/scenery` and `./scripts/build-dashboard-ui-embed.sh` | Passed; no shared CLI installation. |
| `.scenery/harness/bin/scenery harness self --release --summary --write` | Passed all steps, including full Go/race, process, isolated PostgreSQL, Caddy, source-only Go, library and crash-recovery proof. Recorded 43 knowledge and 28 architecture warnings; no failing step. Supersedes the separate default-mode run. |
| `scripts/release-gate.sh` | Passed via `run-release-gate.sh`; logs `.scenery/release-gate/20260907T125647Z`. The wrapper changes only clean-checkout source enumeration to a private Git index, including new implementation files without modifying the real index. All installs used temporary `GOBIN`. |
| `bash examples/webhook-inbox/verify.sh` | Passed in disposable `scenery-webhook-proof.F18jUV`: durable admission, API restart, separate worker, idempotence, validation and typed authenticated status. Original apps and databases were not used. |
| `apps/console/node_modules/.bin/tsc -p examples/webhook-inbox/client/tsconfig.json` | Passed. |
| Isolated `go test -count=20 -parallel=1 -run '^<exact roots>$' -json <package>` groups | 92 exact top-level roots / 1,840 repetitions passed; maximum p95 0.050s. Full samples and package JSONL are in `test-root-timings.json` and adjacent files; the adapter uses the self-harness's exact-root measurement command. |

Explicit skips: `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` remained unset, so the
optional external-app smoke was skipped. The mandatory disposable external Git
checkout was tested separately. Dashboard source/contracts did not change, so
conditional `harness ui` browser acceptance was not selected; release dashboard
typecheck/build still passed. No Linux/VM or published-module lane was measured.
Managed app/runtime ownership and the original lending app's managed `up` remain
outside this plan and are not claimed as fixed.

Keep this ExecPlan and current documentation tracked; keep generated application Go and all machine-local validation evidence ignored. Existing committed TypeScript clients and renderer golden fixtures are deliberate test fixtures and remain tracked under the repository's validation policy.

Store the implementing agent's baseline, command results, generated path/ownership inventory, before/after Git state, second-generation mtimes, `go list` resolution, tidy diffs, revision comparisons, exact test-root timing and release outputs under `.scenery/harness/ordinary-go-contracts/`. Reuse the self-harness result/evidence format rather than inventing a new public report schema. Summarize those paths and results in this plan.

The final Czech handoff must lead with implemented behavior, explain the explicit fresh-checkout generation prerequisite, list removed mechanisms, report mandatory validation and exact skips, and separately state that full managed runtime isolation has not been implemented here. Include the M4 decision brief with its data-safety boundary and proposed next acceptance proof. Do not inflate earlier lending experiments or prior plan evidence into new test results.

Source references inspected for this draft:

- [R1] Repository operating and planning contracts: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/AGENTS.md and https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/PLANS.md
- [R2] Detached startup baseline: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/cmd/scenery/dev_detach.go and https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/docs/plans/0163-detached-startup-diagnostics.md
- [R3] Safe diagnostics and remaining ownership limits: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/docs/plans/0164-truthful-runtime-diagnostics.md
- [R4] Generation boundary and editor modules: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/internal/generate/AGENTS.md and https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/internal/generate/editor_workspace.go
- [R5] Existing renderer and ownership verification: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/internal/generate/generate_go.go
- [R6] Existing release generation probe and native fixture: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/cmd/scenery/harness_self_generate.go and https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/testdata/apps/basic/app.scn
- [R7] Go module/package and generation semantics: https://go.dev/ref/mod#go-mod-tidy and https://go.dev/blog/generate ; Git ignore semantics: https://git-scm.com/docs/gitignore
- [R8] Current agent workflow and fresh-worktree validation: https://github.com/scenery-sh/scenery/blob/c56e3e9614e58914e27a8536b171e4d979f32bbb/docs/agent-guide.md

## Interfaces and Dependencies

Public behavior after this plan is one current contract:

| Existing surface | Required behavior |
| --- | --- |
| `scenery generate` | Refresh the ordinary in-module Go projection and existing selected generated client outputs; no synthetic editor workspace. |
| `scenery generate --target contracts` | Bootstrap/refresh only generated Go contracts and necessary application-imported facades, without live runtime infrastructure or application ABI success as a prerequisite. |
| `scenery generate --check` and targeted check | Compare expected selected artifacts without application-tree writes; missing/stale output is a failed predicate with actionable current diagnostics. |
| `scenery check` | Check the current model, implementation and generated Go freshness without silently repairing the application tree. |
| `scenery compile`, graph/schema queries and inspections | No generation side effect in the application tree or its root workfile. |
| `scenery build`, `test`, `up`, worker/build-consuming paths | Prepare current required Go projection before compilation/execution; preserve existing capability and lifetime semantics. |
| Ordinary Go commands | Work on the prepared source tree using its normal module; they do not regenerate `.scn` and do not need Scenery's workspace overrides. |
| Module publishing | Supply complete generated packages in the published source; use the same renderer and explicit distribution workflow, not mandatory application commit noise. |

Use the Go standard library and existing `internal/compiler`, `internal/generate`, `internal/generate/api`, `internal/build`, `internal/parse`, `internal/machine` and ownership/transaction machinery. No new production dependency is planned.

Keep exact current machine/schema identities and stable diagnostic codes. Use existing stale-artifact diagnostics where their meaning fits, expected ownership/configuration preconditions for conflicts, and internal diagnostics only for genuine internal failures. Update checked schemas and all current consumers atomically when removing editor-workspace fields. Do not add old decoders, compatibility aliases, automatic dependency upgrades, or a new protocol revision negotiation path.

Plan preparation ends here. Implementation starts only when the developer hands this plan to an implementation agent; shared-machine changes and the M4 runtime redesign require their own explicit authorization and plan.
