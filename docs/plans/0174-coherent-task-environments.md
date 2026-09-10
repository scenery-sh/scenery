# Version-Coherent, Data-Safe Task Environments

This ExecPlan is a living document maintained under `PLANS.md`. It owns the
Scenery mechanisms and cross-repository acceptance for the ONLV development
milestone. The companion ONLV plan is
`docs/agent/exec-plans/active/coherent-task-environments.md` in `pbrazdil/onlv`.

## Purpose / Big Picture

A fresh agent must be able to create an isolated, populated ONLV worktree,
change an application field, and verify real persisted behavior without first
repairing its development environment. The selected Scenery executable and
application framework must be one coherent session choice. Application schemas
must evolve without treating reset as the normal development loop. NextNext is
the normal frontend scope, test-only changes do not restart the backend, and
top-level checks accurately include the primary frontend.

The user selected the three substantial deliverables and their adjacent cheap
wins from the September 10 source review of Scenery `849a5208` and ONLV
`66386c29`. That review is a hypothesis and acceptance source, not runtime proof.
The requirements below preserve its full selected outcome. Later roadmap work
(resource orchestration, production promotion, a retained-authority revision
policy redesign, and operator credential/history remediation) is not silently
substituted for or added to this milestone.

## Progress

- [x] (2026-09-10 13:07Z) Read the complete supplied review, current architecture,
  operating contracts and both repository plan/validation rules. Verified clean
  starting checkouts at the reviewed commits and the original ONLV live owner.
- [ ] Implement and prove coherent normal and explicit co-development selection.
- [x] (2026-09-10) Implemented `framework use|inspect`, content-addressed source
  and CLI preparation, authored-module preservation, producer stamping in the
  repository verifier, per-build agreement checks and framework/CLI entries in
  runtime build-input manifests. In-process build/CLI/machine/verifier tests
  pass. Native preparation, ONLV adoption and the source-edit session proof
  remain outstanding; this is not yet R1/R2 completion.
- [x] (2026-09-10) Native `--probe dev-process` now prepares a producer through
  public `framework use`, edits a separate owned co-development origin after
  startup, and proves subsequent application rebuilds retain the selected
  source and executable digests in the actual runtime bundle. The extended
  process probe passed in 28.891 seconds; ONLV adoption remains outstanding.
- [x] (2026-09-10) Implemented and proved safe candidate preflight/recovery
  without concurrent writers. The extended `--probe dev-process` passed:
  rejected preflight retained PID 73653; failed-start rollback served from
  PID 73778; a subsequent source edit served its changed response from PID
  73838. The fixture holds an exclusive OS writer lock for each generation.
- [x] (2026-09-10) Implemented the read-only generated runtime handshake and
  stop/start/rollback state machine, retaining exact launch environment and
  served metadata. Focused in-process runtime, codegen and CLI tests pass;
  real-process failure/recovery acceptance remains outstanding.
- [ ] Implement transactional application initialization and schema migration.
- [x] (2026-09-10) Implemented per-binding app-authored numbered SQL,
  non-allocating status, schema-local checksum/owner ledger and atomic pending
  chains. Native `--probe postgres` passed all eight initialization, unknown
  state, SQL failure, checksum change, killed-process, retry and retained-row
  assertions in two private clusters with ownership-verified cleanup. ONLV
  adoption and known populated-baseline reconciliation remain outstanding.
- [ ] Adopt the migration path in ONLV and prove a populated-field evolution.
- [ ] Provision a curated ONLV worktree with complete object/native assets and
  its own stable origin using existing ownership and snapshot mechanisms.
- [ ] Wire authoritative validation profiles and a real authenticated mutation,
  object, restart-persistence and cross-tenant denial journey.
- [ ] Eliminate test-only/content-identical backend restarts and make NextNext
  the default runtime scope; repair the broad check ergonomics.
- [x] (2026-09-10) Native process proof confirmed test-only, documentation-only
  and identical-content edits retained the existing backend PID. NextNext
  default scope and the remaining validation-profile adoption are still open.
- [ ] Complete the requirement-by-requirement audit, selected native probes,
  repository validation and current documentation in both repositories.
- [x] (2026-09-10 16:12Z) ONLV fixture adoption now proves the populated
  `projects.summary` evolution, authenticated project/object attachment,
  byte readback, restart persistence and cross-tenant denial. The curated
  coordinated archive contains one project, one complete scene and six objects;
  fresh published-module worktree acceptance and preserving main cutover remain.
- [x] (2026-09-10 16:12Z) Native PostgreSQL acceptance passed the full migration
  chain and all seven malformed predicate result cases. Initial verification
  accepts exactly one boolean column in one row, without SQL Scan coercion.
  Build-info, dev-process, storage, worktree-git, snapshot-backup and
  validation-git probes also passed. The final updated worktree probe passed
  A1-A17 in 273.931 seconds, and PostgreSQL passed in 62.382 seconds with
  ownership-verified cleanup. Default self-harness passed the complete Go suite,
  vet and schemas; lint reports zero issues. Only the existing 41 documentation
  freshness and 23 architecture warnings remain. The installable skill validator
  and both committed compiler fixture refresh commands also passed.

## Surprises & Discoveries

- The first truly fresh ONLV worktree selected the published module correctly
  but failed app compilation: Go reports a downloaded module's `GoMod` under
  `cache/download/.../@v/<version>.mod`, not inside its source directory. The
  build-input collector had inferred source from that metadata path. It now
  requires Go's actual module `Dir`, with an in-process published-layout test;
  fresh public workflow acceptance is repeated against the corrected release.

- The historical/current coexistence fixture built its current CLI on the host
  without the new producer stamp. It now compiles at the real `/candidate`
  source path inside its disposable Linux daemon. A subsequent run correctly
  caught `go mod download all` adding unused test checksums to candidate
  `go.sum` after the source fingerprint; candidate compilation now uses
  `-mod=readonly` and does not run download-all. The immutable historical
  baseline keeps its separate preparation path, not a compatibility fallback.
- `database/sql` boolean scanning accepts text/numeric values and QueryRow
  ignores further rows. Baseline adoption now reads the actual driver value,
  rejects additional rows/results/columns and validates one query before SQL.
  The native probe proves every malformed result leaves the ledger absent.

- The first public migration failure probe found two JSON values on stdout:
  a structured lifecycle result followed by the generic top-level error. The
  migration/setup path now emits one failed envelope with result evidence,
  diagnostics and an already-rendered exit error; a focused CLI test covers it.
- Published Go modules contain the dashboard source and placeholder, not an
  ignored developer-built dashboard. Producer source identity now includes the
  dashboard source/lockfile/build script, while executable identity binds its
  derived assets. Preparation builds the dashboard only in the private source
  snapshot before compiling the selected CLI.

- The initial `just context go.mod .scenery.json scripts/db-apply.sh Justfile`
  confirms that ONLV independently resolves mutable `../scenery` source and a
  PATH executable. The live session advertises all five frontend mounts.
- `scripts/db-apply.sh` skips an entire service when any table exists, and its
  schema apply is not a transaction. This confirms the reviewed initialization
  gap from current source; no failure was induced against personal data.
- The worktree-local inspection binary was built at `758cbf79+dirty`, while the
  current Scenery HEAD is the documentation-only `849a5208`. Its output remains
  discovery evidence only; final validation will build the exact changed tree.
- Build workspace copying silently rewrites `scenery.sh` requirements to the
  CLI's mutable compile-time checkout. Pinning ONLV's authored requirement alone
  cannot establish producer coherence; selection must also govern parsing and
  generated-workspace dependency resolution.
- The old build pipeline published candidate metadata before activating that
  candidate, and compile errors cleared the live session PID. Both now preserve
  the current generation's serving identity while reporting the failed build.
- The extended native handoff probe initially used the wrong fixture route
  (`/service/echo` instead of `/echo`) and an unnecessary process-environment
  read in its source string. Corrected the route and used the owned app working
  directory after the HTTP probe and repository contract scanner rejected them.
- Real failed-start acceptance then found a product defect: the retained app
  executable was a symlink into a build cache that had already removed its
  previous target. Session binaries now retain independent bytes under their
  actual executable SHA-256, surviving cache deletion or same-path replacement.
  Successful transitions release the known unused generation; uncertain live
  processes keep their executable and ownership.
- Test-only and content-identical watch invalidation fixes passed focused and
  complete CLI package tests. The quick self-harness passed with existing
  knowledge-review and architecture warnings. ONLV's broad lint recipe now
  invokes NextNext lint/typecheck; `just check-app nextnext` and
  `just check-harness` passed (46 existing NextNext lint warnings, zero errors).

## Decision Log

- Decision: preserve the existing ONLV canonical root, database/object authority
  and browser origin `http://localhost:4920`. Execute migration failures and
  mutation acceptance only in explicitly test-owned environments.
  Rationale: the successful prior data migration is not permission to reset,
  duplicate all personal records, or modify another active task's runtime.
  Date/Author: 2026-09-10 / Codex.
- Decision: extend existing build manifests, runtime bundles, SQL endpoint and
  worktree ownership, snapshot materialization and validation profiles.
  Rationale: the milestone composes existing capabilities; it does not need
  another snapshot system, arbitrary service pruning, SQL-diff engine or agent
  orchestrator. App-authored migrations and fixture semantics remain in ONLV.
  Date/Author: 2026-09-10 / Codex.
- Decision: keep execution single-agent and shared installation outside
  validation. After successful validation, commit and push exactly this task's
  changes in both repositories. After proving the isolated environment, the
  original ONLV may be briefly stopped and restarted while preserving its
  database, objects and `localhost:4920` origin. No deployment or shared
  `go install` is authorized or required.
  Rationale: the developer explicitly approved publication and the preserving
  main-runtime transition in the September 10 follow-up.
  Date/Author: 2026-09-10 / Petr / Codex.

## Outcomes & Retrospective

Not yet completed. Source review, selected-framework native isolation,
preflight/failed-start recovery and content-based watch acceptance are proven.
ONLV producer adoption, schema evolution, fixture provisioning and integrated
validation remain in progress.

## Context and Orientation

`cmd/scenery/watch.go`, `watch_inputs.go` and `dev_supervisor.go` own source
change dispatch and backend replacement. `internal/watchignore` owns shared
input policy. `internal/build` already records dependency, source, framework,
generator and build fingerprints, a Go build-input manifest and runtime bundles.
Those are the starting points for producer coherence, not an independent lock
file hierarchy. Compiler generation and framework source selection must agree
with the actual app build inputs even for a dirty co-development source tree.

`internal/app/root.go` owns named environment frontend selection, database hooks
and existing validation profile configuration. `cmd/scenery/db_setup.go`,
`db_seed*.go` and `worktree_database_*.go` compose compiled SQL requirements,
verified allocation, lifecycle exclusion and setup. `internal/postgresdb` owns
database IO; `internal/app` must remain free of the driver layer.

`cmd/scenery/worktree.go` creates Git worktrees. Existing worktree records own
stable resources and ports. `internal/snapshotarchive` plus the snapshot CLI
already provide checksums and independent clone/copy materialization. House's
ONLV `var/storage` native paths require explicit asset coverage in addition to
Drive's logical object store. `internal/validation` owns profile planning,
changed-file selection and execution; ONLV must adopt it rather than duplicate
its check catalog in another script.

## Milestones

M1 establishes producer selection, content-bound co-development input and safe
candidate replacement. M2 establishes transactional SQL evolution and its ONLV
consumer. M3 makes a curated ONLV worktree reproducible and its scenario real.
M4 connects scoped validation, runtime selection and cheap edit-loop fixes.
M5 audits all requirements against current source and runtime evidence. Small
independent fixes may land earlier without changing these completion gates.

## Plan of Work

First trace the existing framework fingerprints, generated workspaces, runtime
metadata and candidate startup ordering. Make the selected framework source and
executable immutable within a session, with explicit source-digest provenance
for co-development. The normal ONLV dependency must be pinned, and its wrapper
must resolve a matching checkout-local executable. Reject incompatible candidate
contracts before stopping a healthy backend and retain a safe recovery path if
startup later fails. Never overlap write-producing runtime generations.

Add explicit migration files and one Scenery-owned transaction/status path using
the existing service endpoint and operation ownership. Initialization must be
atomic; populated unversioned schemas cannot be assumed complete from table
count. Migration identity/checksum and successful SQL application must commit
together. Failed/interrupted work remains pending or failed, never complete.
Reset remains an explicitly destructive operation, separate from evolution.

Build ONLV's small, versioned fixture preset and dependency preparation around
existing worktree/startup and snapshot materialization. Check required inputs
before starting a partial environment. Supply a complete scene and its declared
House-native files, not dangling rows. New roots receive independent writable
state and stable distinct origins without tracked port edits. Share only
immutable assets/caches. Wire the same preset into real smoke validation.

Make ONLV validation configuration the executable source of check selection;
compose it into `just context` and existing recipes. Make NextNext the default
runtime frontend, with deliberate optional frontend/process selection, and
ensure broad `just lint` includes its lint/typecheck. Use content identity for
runtime invalidation and exclude test-only Go inputs from backend restarts.

## Concrete Steps

Run Scenery commands from its exact implementation checkout. Before editing an
owned subtree, read its `AGENTS.md` and use one
`scenery inspect docs --for-path <path> -o json` query per surface. Record exact
new command grammar and artifact fields here as decisions become implemented.

For ONLV, start with `just context <changed-paths...>`, use the companion plan,
and keep the canonical main environment separate from the disposable fixture
root. A fixture worktree must be made by the implemented public workflow, not
by manual copying that bypasses the acceptance requirement.

## Validation and Acceptance

The following inventory is the completion contract, not a list of currently
passing checks. Final evidence must identify exact checkout/producer/digests,
runtime origin and selected fixture identity.

| ID | Required behavior and authoritative proof |
| --- | --- |
| R1 | Normal ONLV uses a pinned framework and matching worktree-local CLI; inspect dependency, executable identity and actual build-input manifest. |
| R2 | Explicit co-development binds dirty source content and supervisor/app to one snapshot; mutate the other source checkout and prove the existing session cannot silently switch. |
| R3 | Incompatible candidate and failed candidate startup preserve or recover the healthy application; prove no simultaneous durable/scheduled writers. |
| R4 | Empty schema initialization is all-or-nothing; populated unknown/partial initialization is diagnosed instead of skipped as complete. |
| R5 | Known schema revisions migrate with checksum/status evidence; failed SQL and interrupted execution cannot produce a completed ledger entry; retry is safe. |
| R6 | ONLV adds a field to a populated service, regenerates queries/contracts, exercises the handler/API and preserves prior rows. |
| R7 | The public worktree/preset workflow provisions a small complete fixture, dependencies, object bytes and declared native assets, with missing inputs rejected before partial startup. |
| R8 | The fresh ONLV worktree reaches a useful page and complete scene in Chrome, has its own retained origin/writable state, and does not disturb the original root or origin. |
| R9 | `onlv-smoke` authenticates fixture users, creates a record, attaches/reads an object, survives restart and denies another tenant through real application boundaries. |
| R10 | `validate changed --base main --dry-run -o json` selects focused checks from one authoritative profile mapping; `harness --with-validation=onlv-smoke -o json --write` executes R9. |
| R11 | Test-only, documentation-only and content-identical edits do not restart a backend; an actual runtime edit does and serves the correct revision. |
| R12 | NextNext is the ordinary runtime scope, other frontends are explicitly selected, and top-level lint/check/context commands state and execute their real coverage, including NextNext lint/typecheck. |

Mandatory Scenery commands, from the Scenery checkout, are:

    go test ./internal/app ./internal/build ./internal/watchignore ./internal/agent ./internal/postgresdb ./internal/validation ./cmd/scenery ./scripts/verify
    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write
    go run ./scripts/verify --probe build-info --probe dev-process --probe worktree --probe worktree-git --probe postgres --probe storage --probe snapshot-backup --probe validation-git --summary --write

Add each newly created affected Go package to the focused command before
running the repository suite. Run the union of
`changed_area.recommended_commands` from the refreshed self-harness report.
If compiler/generator code changes, also run both committed fixture commands:

    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json

Those two commands may be skipped only if the final diff contains no compiler
or generator change; record that path evidence. Dashboard checks may be skipped
only if no dashboard source/build contract changes; otherwise run its exact
root-matrix lint/typecheck/build and UI harness commands. ONLV's companion plan
owns its concrete Go, frontend and live scenario commands. Named probes must
report their assertion inventory and ownership-verified cleanup. Full release,
all-root timing audits and benchmarks are not selected by this request; none
may be reported as passed. The existing absolute 100 ms test contract remains.

## Idempotence and Recovery

Leave original data, backups, live owner and browser origin intact. Refuse
unknown or incompatible ownership rather than deleting metadata. Fixture
creation uses an identified disposable root; cleanup must prove ownership and
may remove only resources created for this acceptance run. Record its handles
before starting it. A timeout is not process completion; resume the same handle.

Preserve the previous working backend until candidate preflight succeeds, and
do not launch two write-producing generations. Migration transactions and their
ledger are inseparable. Interrupted provisioning must either safely resume the
same input identity or report the precise incomplete phase, not start again on
another root or replace personal data.

## Artifacts and Notes

Starting Scenery: `849a5208b44c882a539bd723f9f3abe756c8115b`.
Starting ONLV: `66386c29b32bd347f21bfeb76d94d2a1e6682d08`.
The original ONLV origin is `http://localhost:4920`; its source-linked framework
and executable were separately reported by `just context` on September 10.
Machine-local reports, fixtures materialization, credentials and snapshots are
not source artifacts. Record bounded evidence paths and hashes, never secrets.

## Interfaces and Dependencies

Use Go's standard library and existing PostgreSQL driver, compiler/build
manifests, runtime bundle metadata, worktree ownership, snapshot and validation
packages. Keep app-specific migrations, fixture recipes and domain assertions
in ONLV. New CLI/config/JSON fields require their current schemas, diagnostics,
runbooks, agent workflow and fixture updates in the same change. No deprecated
aliases, alternate runtime decoder, new database engine or implicit reset.
