# One Pure SQL Endpoint Selection

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as implementation proceeds.

Prepared and approved: 2026-09-08. Baseline:
`72a28c78c87c5e62e3d7bb312d79f47bdebaab47` on the initially clean Scenery
checkout. This is the narrowly approved follow-up to completed plan 0168, not
a reopening of that immutable plan or an approval of the separate timing
exception cleanup proposal.

Scope extension approved by the developer on 2026-09-08: repair the existing
live auth test boundaries that block the required all-root timing acceptance.
Preserve their substantive assertions in mandatory release probes, retain
focused fast assertions, and then finish the full audit and release gates.

## Purpose / Big Picture

Remove the duplicate choice between an explicit per-binding PostgreSQL URL
and a schema-specific URL derived from an application base URL. Put that pure
choice in the existing `internal/postgresdb` owner and make the generated
runtime and explicitly named standalone `db` path consume it.

The observable contract must remain unchanged. This is not a unified SQL
context: compiled requirements, environment supply, and retained allocations
have different authority and lifetime. No CLI provisioning, allocation,
snapshot/recovery, environment ABI, compiler, or public API changes are planned.

## Progress

- [x] (2026-09-08 14:14Z) Recorded the clean baseline, read the owning contracts,
  and confirmed that the user approved only SQL endpoint selection plus a full
  20-process-per-root timing audit. No subagents, shared installation, live
  application restart, or external database mutation are authorized.
- [x] (2026-09-08 14:30Z) M0: both consumer characterization tables passed
  before the production change; captured the pre-implementation package and
  root listing after adding those two characterization roots. Nested-module
  and Go-tool/source reconciliation are part of the full audit below.
- [x] (2026-09-08 14:30Z) M1: `ResolveServiceEndpoint` now owns override/base
  selection; runtime and standalone `db` call it. The same characterization
  tables, all three affected packages, full `go test ./...`, and lint pass.
- [ ] M2 acceptance: all requested proof has been executed, but timing failures
  outside the approved SQL/auth scope still block closure. The developer
  deferred those repairs and authorized committing, pushing and installing
  the completed SQL/auth change with the failures recorded.
- [x] (2026-09-08) Developer approved the auth test/release-probe extension
  after the existing OAuth root measured 510 ms isolated p95.
- [x] (2026-09-08) M1a: move the 15 live auth journeys to a mandatory public-boundary
  release fixture, map all existing assertions, and retain focused in-process
  coverage under the original test-root identities.
  The new public-boundary release fixture has passed all 15 cases with 111
  named assertions and verified owned cleanup. All original root identities
  now execute focused SQL transaction, scope, cipher, PKCE, callback, privilege
  and lifecycle decisions without a live database or network. The JWKS cache
  test also uses an in-process HTTP transport. Focused auth tests pass.
- [x] (2026-09-08) All 45 auth roots passed 20 isolated processes each; highest
  p95 was 20 ms. The partial all-root run retained identical before/after source
  digests and was stopped to supply the remaining durable-test prerequisites,
  not because auth failed. Evidence: `final/all-roots-664711090/`.
- [x] (2026-09-08) Completed the final seven-module, 108-package inventory:
  1,852 roots and 37,040 valid isolated process samples. All roots passed
  functionally; 16 exceeded the unchanged 100 ms p95 limit. The final 45 auth
  roots all passed, with maximum p95 14 ms. Four missing-prerequisite roots
  were completed separately with matching source and test-binary hashes.
- [x] (2026-09-08) Executed release/fresh and the full shell gate. Every mandatory
  external release probe passed, including auth and owned cleanup. Release/fresh
  failed three additional timing confirmations; the cached release verifier and
  shell gate passed. No passing retry erases the earlier failing evidence.
- [x] (2026-09-08) Recorded the final outcomes and scope boundary; post-update
  quick verification passed with the existing 42 knowledge and 23 architecture
  warnings. `bash -n scripts/release-gate.sh` and `git diff --check` passed.

## Surprises & Discoveries

- `runtime` constructs a registry, whereas `db` first consumes an already
  supplied registry. Registry precedence cannot be moved into one generic
  resolver without changing one of those contracts.
- Runtime ignores `SCENERY_DATABASE_URL` for the framework binding. A
  standalone `db.Get("scenery")` without a registry rejects the reserved schema.
  Those differences must remain at the consumer boundaries.
- Runtime validates the selected endpoint before publishing it. Standalone
  `db` selection leaves validation of an explicit override to `Get`; base-URL
  derivation validates immediately. The common selector must not silently
  change validation order, fallback behavior, or diagnostics.
- A whitespace-only base URL retained in registry metadata is not the same
  input as an absent environment URL. Preserve caller normalization rather
  than adding a new blanket trim inside the shared selector.
- The initial complete inventory contained 1,851 native exact roots: 1,849 in the root
  module and two in `internal/compiler/testdata/native`. Five other authored
  modules have no authored or Go-selected test files. Their package metadata
  is retained using `go list -e -json`, including missing generated dependency
  diagnostics; this establishes zero test roots, not successful fixture builds.
  Those builds and external behavior remain mandatory release-probe work.
  The auth release-owner fast test adds one root: the final inventory is
  1,852 roots, including the same two nested-module roots.
- Initial nested-module inventory failed on the unmaterialized webhook
  example. The retry reconciled Go test listings against source ASTs for both
  modules with tests, and independently checked tracked/untracked authored
  files and Go-selected file metadata for every zero-test module. No test root
  was omitted. Inventory-only reports explicitly make no timing claim.
- The first post-edit quick verifier found the missing required living-document
  statement in this new plan. All selected package and schema checks passed;
  the failed report remains in `final/quick.log`. The statement is now present.
  The subsequent quick run passed.
- The all-root attempt found 15 auth roots that skip without
  `SCENERY_TEST_DATABASE_URL`. The unchanged confirmation engine correctly
  rejected those samples. The run was interrupted after this independent
  blocker was established; cancellation diagnostics and unexecuted roots are
  not test regressions and are not acceptance evidence.
- Supplying an owned disposable database through the existing
  `writePostgresHarnessConfig`, `provisionHarnessDatabase`, and
  `cleanupHarnessWorktreePostgres` workflow did not remove the timing blocker.
  `TestGoogleOAuthBrowserFlowWithFakeGoogle` passed all 20 fresh native test
  processes but measured 393–520 ms, with nearest-rank p95 510 ms. PostgreSQL
  was container-backed; these are native macOS test-process measurements,
  not native Linux evidence. Owned fixture cleanup succeeded.
- At that first blocker, the auth test and its database helper were unchanged
  from the pinned base. No baseline performance comparison is claimed. Their live database creation,
  OAuth HTTP flow and database cleanup cross the external boundaries that the
  repository rules assign to release probes. Fixing that test architecture
  is distinct from extracting SQL endpoint selection.
- Running all 15 old live scenarios with an owned database established that
  seven Google connection/token scenarios fail before their intended assertions:
  direct calls pass an auth context but do not establish the runtime's current
  request identity. Their error is `endpoint requires auth`. The replacement
  release fixture must use real authenticated HTTP for those endpoints, not
  duplicate the invalid private-call setup. The other eight scenarios passed;
  owned fixture cleanup passed. Raw baseline evidence is in
  `final/auth-prerequisite-310371176/baseline.jsonl`.
- The other live test helpers still read the registered
  `SCENERY_TEST_DATABASE_URL`: nine durable-store roots, five durable-runtime
  roots and one worker-CLI root. The restarted all-root audit supplies this
  existing variable from an owned disposable worktree database, while keeping
  ordinary `DATABASE_URL` absent. These test bodies are not modified by the
  auth scope extension. Any functional/timing failures remain explicit.
- Four otherwise-skipped roots required explicit existing inputs: the native
  compiler fixture for `PERF_PROBE_ROOT`; the managed native TypeScript checker
  plus an owned React dependency tree containing the test's required router;
  unique owned helper-process marker paths; and a real host c-shared library
  built through public Scenery generation/build commands from an owned native
  fixture copy. Preparation failures remain archived. No placeholder dependency,
  invented ABI artifact, skipped root, or changed test body substitutes for proof.
- The combined audit retains 36,960 valid samples from the main run and 80 from
  the four prerequisite completions. The four original skipped attempts are
  excluded, not counted as passes. All four supplemented test binaries match
  their main-run SHA-256, and both runs have identical before/after authored
  source digest `08d87f9c920dde84f9c0c17e3f05db3869093985f72ed2530c83a27df2fa5a11`.
- The complete audit exposed 15 live durable roots at 140–283 ms p95 and the
  real TypeScript-checker root at 194 ms. Release/fresh separately measured
  three unchanged roots at 113–121 ms that had measured 70–90 ms in the audit.
  These are actual failing observations, not proof of a SQL-extraction regression
  or a controlled baseline performance comparison. Their causes and repairs are
  outside the approved auth extension.
- The earlier progress entry's 121 auth assertion count was an arithmetic error.
  Both the successful standalone fixture report and the integrated release
  reports contain 111 named assertions across the same 15 cases.

## Decision Log

- 2026-09-08, developer: do not extend this work to the 19 additional timing
  findings now; commit and push the current SQL/auth change and install Scenery.
  This delivery decision does not waive the 100 ms contract or turn the failed
  timing evidence into a pass. No live application restart was requested.
- 2026-09-08, developer: approve fixing these auth proof boundaries; retain
  the 100 ms policy, all-root measurement, and mandatory release acceptance.
- 2026-09-08, agent: use a repository-owned native release fixture program
  built from `scripts/verify/testdata/authprobe`, invoking public auth APIs,
  actual standard-auth HTTP routes and authored database-state assertions.
  Use an owned loopback fake Google server and transport rewrite restricted
  to Google's four known OAuth endpoints. Do not add test-only product APIs,
  build-tagged slow tests, private-source overlays or a second timing lane.
  A fresh fixture process isolates auth registration/configuration per journey.
- 2026-09-08, developer: implement only the runtime/standalone-`db` SQL
  workstream; leave timing exceptions unchanged. Require 20 fresh isolated
  processes for every exact current top-level Go test root, not just changed
  roots or observed timing candidates.
- 2026-09-08, agent: use `internal/postgresdb` and an explicit value-only
  result for the common override/base selection. Keep registry selection,
  defaults, framework handling, remote durable filtering, endpoint validation,
  and managed provenance in their existing consumers.
- 2026-09-08, agent: use the unchanged verifier confirmation implementation
  through a disposable local driver for the all-root audit. Do not add a
  product flag, permanent second timing engine, scheduler, cache, or exception.

## Outcomes & Retrospective

SQL implementation is complete but acceptance is not. Two independent
override/base implementations now have one pure owner and two concrete
consumers. Production accounting is +20 lines in `internal/postgresdb`, +1
net line in `db`, and -1 in `runtime`: +20 production lines overall, not a
net code-size reduction. Characterization/helper proof adds 322 test lines.
No registry ABI, allocation authority, timing policy, exception inventory,
compiler, or lifecycle implementation changed.

The approved auth proof-boundary repair is also complete. All 15 original root
identities remain as focused in-process tests; real HTTP/database journeys now
have a mandatory release owner. Auth production code is unchanged. All 45 auth
roots pass the final isolated audit, and both integrated release runs pass all
15 journeys and 111 named assertions with verified cleanup.

Validation to date:

| Command / proof | Result |
| --- | --- |
| `go test ./runtime ./db ./internal/postgresdb` before and after extraction | Passed; identical consumer characterization tables |
| `go test ./auth ./scripts/verify ./db ./internal/postgresdb ./runtime` | Passed after auth migration |
| `go test ./...` | Passed after auth migration; ordinary success alone does not certify optional external prerequisites |
| `go test ./cmd/scenery` | Passed |
| `golangci-lint run ./...` | Passed, zero issues |
| `go build ./scripts/verify/testdata/authprobe` and `golangci-lint run ./scripts/verify/testdata/authprobe` | Passed; fixture lint reports zero issues |
| `go run ./scripts/verify --quick --summary --write` | Passed after the result-only documentation update; existing 42 knowledge and 23 architecture warnings, no errors |
| Complete package/root inventory | Seven modules, 108 packages, 1,852 exact native roots; no nested test roots omitted |
| Disposable all-root confirmation driver plus explicit-prerequisite supplement | 37,040 valid samples, all functional passes, 16 timing failures; source and supplemented binary hashes match |
| `go run ./scripts/verify --release --fresh-tests --summary --write` | Failed only three additional isolated timing confirmations; every mandatory external probe, full race suite, vet, fixture matrix, dashboard/client/catalog checks and schema validation passed |
| `go run ./scripts/verify --summary --write` | Not separately run; all default checks executed in both release runs, but the failed fresh acceptance is not claimed as superseded |
| `go run ./scripts/verify --release --summary --write` inside shell gate | Passed, including standard auth lifecycle and worktree/PostgreSQL cleanup; cached correctness does not replace failed fresh/all-root timing proof |
| `SCENERY_BIN="$PWD/.scenery/harness/bin/scenery" scripts/release-gate.sh` | Passed: lint, dashboard embed, repository verification, source snapshot build, HTTP fixture, router safety and artifact hygiene |
| Optional external-app smoke | Skipped because `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` was unset; no live client application was selected |
| `bash -n scripts/release-gate.sh` and `git diff --check` | Passed |
| Committed client fixture regeneration / manual UI work | Not independently selected: compiler, generator and dashboard source are unchanged; their release checks passed |

Final evidence is under `.scenery/harness/sql-endpoint-resolution/final/`:
`all-roots-4040544660/` contains the final inventory and complete main run;
`prerequisites-3697411916/` contains the four valid completions;
`all-roots-combined.json` checks full sample accounting and binary/source
identity. `validation-summary.json`, `release-fresh-report.json`,
`release-gate-report.json`, and `release-gate.log` preserve distinct outcomes.
The owned audit database cleanup is recorded in
`all-roots-owned-database-1079734812/summary.json`. Earlier interrupted,
skipped and failed preparation attempts remain in their own directories.

The unresolved timing scope is explicit below. Every listed root has 20
successful functional samples in each cited measurement. No exceptions,
threshold relaxation or assertion deletion was used. The plan stays active;
repairing these additional roots requires a separately approved follow-up. The
developer subsequently authorized commit, push and shared CLI installation
while deferring these repairs. No live app restart is part of that delivery.

| Unchanged root (package relative to `scenery.sh/`) | Audit p95 ms | Release/fresh p95 ms when additionally failing |
| --- | ---: | ---: |
| `cmd/scenery.TestWorkerDurableTokenCreate` | 223 | — |
| `internal/durable/store.TestFailJobRetriesUntilMaxAttempts` | 160 | — |
| `internal/durable/store.TestJobAdminListEventsCancelAndRetry` | 150 | — |
| `internal/durable/store.TestLeaseAndHeartbeatUseTaskLeaseDuration` | 150 | — |
| `internal/durable/store.TestLeaseCompleteAndFailJobs` | 150 | — |
| `internal/durable/store.TestOpenCreatesPostgresSchemaWithExpectedTables` | 140 | — |
| `internal/durable/store.TestReconcileTasksAndStartAreIdempotentByDedupeKey` | 140 | — |
| `internal/durable/store.TestStaleWorkerCannotResurrectCanceledJob` | 140 | — |
| `internal/durable/store.TestStepsSignalsAndSchedules` | 150 | — |
| `internal/durable/store.TestWorkerTokenAndLeasedJobFencing` | 145 | — |
| `runtime.TestDurableLocalWorkerExecutesQueuedJob` | 172 | — |
| `runtime.TestDurableRemoteWorkerExecutesJobOverHTTP` | 170 | — |
| `runtime.TestDurableScheduleEnqueuesAndRuns` | 283 | — |
| `runtime.TestDurableWorkerHTTPLeaseHeartbeatAndComplete` | 150 | — |
| `runtime.TestStartDurableRuntimeReconcilesTasks` | 144 | — |
| `internal/generate.TestGeneratedDetailPageCompilesWithManagedTypeScriptChecker` | 194 | — |
| `cmd/scenery.TestDeployResumeStartsMissingTargetsAndSkipsLiveSessions` | 70 | 121 |
| `internal/build.TestRefreshCachedWorkspaceFallsBackWhenFrameworkChanges` | 70 | 113 |
| `internal/evolution.TestChangePlanDoesNotWriteAndApplyIsRevisionBound` | 90 | 115 |

The final documentation-only result update happens after the frozen-source
measurements and release proof. It changes no implementation or test input;
the quick verifier refresh validates that handoff separately.

## Context and Orientation

`runtime/sql_bindings.go` receives compiled `SQLBinding` names and schemas,
loads explicit supply, filters remote-only durable bindings, publishes the
existing environment registry, and preserves managed provenance only when
the supplied base and selected bindings match exactly.

`db/db.go` selects one supplied binding, excluding framework `scenery` from
an otherwise unambiguous default. Only an explicitly named caller with no
supplied schemas uses standalone endpoint selection. Unknown bindings in a
nonempty registry fail rather than falling back to arbitrary environment keys.

`internal/postgresdb/service.go` already owns `ServiceURL`, `Service`,
`Database`, registry encoding, and environment projection. The new helper
belongs next to `ServiceURL`; it must not read environment/configuration,
compile, open a pool, provision, or infer resource ownership.

No child `AGENTS.md` applies to these three implementation directories. Root
rules apply. `scripts/verify/AGENTS.md` and `internal/testsuite/AGENTS.md` govern
the read-only use of the retained timing machinery. Root/current verifier
ownership wins over the latter document's old `cmd/scenery` routing text;
this work does not edit that engine or its policy.

## Milestones

### M0 — Pin the two input contracts

Record the base SHA, branch, package/root inventory and nested modules under
`.scenery/harness/sql-endpoint-resolution/baseline/`. Add in-process
characterization cases at both existing consumers and run them against the
unchanged baseline implementation. Keep every existing root and assertion.

Cover explicit override versus base precedence, registry fallback and its
different precedence in `db`, reserved framework behavior, remote durable,
missing/invalid endpoints, invalid registries, unknown binding names, schema
identity, exact matching/nonmatching managed provenance, and safe diagnostics.
Record any pre-existing behavior that prevents the approved contract before
changing it; do not quietly expand this refactor into a behavior fix.

### M1 — Delete one implementation of the common choice

Return a small typed value identifying the chosen URL and whether it was
derived from the base. An explicit override remains opaque to this helper;
`ServiceURL` retains base validation and schema/search-path derivation.
An absent override and base return an empty selection for the consumer to
handle according to its own contract. Run the identical M0 tables afterward.

Do not consolidate the CLI's external/managed service materialization loops,
change `resolveSQLSupply`, or move registry/default/provenance decisions.
Show exactly which duplicate branches disappeared; distinguish relocated
lines from net source growth and added proof.

### M2 — Prove the full selected scope

Run the union selected by the refreshed changed-area oracle. The current
classes are `cli-json-contract`, `go-package`, and `release-sensitive-or-runtime`;
the quick knowledge/schema checks also validate the documentation changes.
Freeze authored inputs during measurements and external release proof.

Inventory every Go package, including packages without tests and authored
nested modules. Resolve actual test roots through the Go tool and reconcile
them against source declarations. Feed every exact `TestX` root to the retained
confirmation runner, using 20 fresh serial processes, current active-root
accounting, nearest-rank p95, and the strict `< 0.100` comparison. Preserve
raw events, invocation cwd/arguments, binary hashes, source hashes, toolchain,
and a complete per-root result. A skipped, missing, failed, or incomplete root
cannot pass. Do not replace native evidence with Docker/VM timing.

The full release/fresh verifier and shell gate must pass. Their existing
capability-authority and worktree/PostgreSQL probes supply owned real-process
SQL/auth/durable/external/recovery proof. Confirm that no mandatory probe was
skipped and that their fixture cleanup succeeded.

### M1a — Give live auth assertions a release owner

Preserve all 15 existing live root identities as focused in-process checks.
Move the database, transaction durability and HTTP journey assertions into
an explicit `standard auth lifecycle` release step, with one named case per
original root and a checked assertion map in this plan. The fixture uses only
current public auth/runtime APIs and direct SQL observation; no runtime auth
implementation is copied or exported solely for testing. Retain real rollback,
replay, scope, encryption, single-flight, impersonation and ownership checks.

The release owner provisions one disposable cluster using the retained public
worktree helpers, gives each fixture process a fresh database, and verifies
cleanup. It must fail if any case, process, expected report or required Docker
prerequisite is missing. The fixture is an ordinary executable, not an alternate
Go test graph. The verifier's fast test covers case inventory and failure
propagation without starting tools. Auth fast tests use in-process SQL/HTTP
fakes only where needed to exercise real production decision boundaries.

Update the verifier subtree instructions, architecture, local release contract,
environment documentation if the obsolete live-test variable is removed, and
knowledge entries together. Run `go test ./auth ./scripts/verify`, all previously
selected SQL/CLI commands, complete `go test ./...`, lint, full-root timing,
release/fresh, and shell gate. Do not change timing exceptions or testsuite.

## Plan of Work

First pin both boundary contracts without changing production behavior. Then
extract only the shared override/base decision and delete its duplicate in
the same change. Finally complete correctness and the full timing inventory
before running the existing release loops. Keep unrelated review proposals,
completed plans, live applications and shared resources untouched.

## Concrete Steps

All commands run from the repository root unless stated otherwise:

```sh
git status --short --branch
git rev-parse HEAD
go list -json ./...
go test -list '^Test' ./...
go test ./runtime ./db ./internal/postgresdb
go test ./...
golangci-lint run ./...
go run ./scripts/verify --quick --summary --write
```

Read `.scenery/harness/agent-context.json` after editing and execute its exact
`changed_area.recommended_commands` union. Once the disposable all-root driver
exists, build its symlinked verifier-source package and run it:

```sh
go run ./.scenery/harness/sql-endpoint-resolution/audit
go run ./scripts/verify --release --fresh-tests --summary --write
SCENERY_BIN="$PWD/.scenery/harness/bin/scenery" scripts/release-gate.sh
bash -n scripts/release-gate.sh
git diff --check
```

The driver is local evidence tooling, not a supported command or checked-in
source. It inventories nested modules explicitly; any nested test roots use
their own module cwd with the same retained confirmation runner. Full
release/fresh supersedes `go run ./scripts/verify --summary --write` only after
its entire scope passes; quick still supplies the post-edit oracle handoff.

## Validation and Acceptance

| Requirement | Authoritative evidence |
| --- | --- |
| Common selection has one owner | implementation diff and both real consumers calling the helper |
| Both input contracts unchanged | identical pre/post characterization tables, existing consumer tests and exact diagnostic assertions |
| No authority/ABI expansion | unchanged public names, registry fields/env projection, CLI provisioning and allocation paths; unchanged compiler dependencies |
| Current Go correctness | all three affected packages, `go test ./...`, lint and the oracle union |
| All-root timing | complete native module/package/root inventory, 20 raw fresh-process samples per exact root, each p95 strictly below 100 ms |
| Timing policy unchanged | no changes to `internal/testsuite`, `scripts/testsuite` or verifier timing files; empty exception inventory and full release confirmation |
| Real SQL behavior retained | release capability authority plus existing worktree/PostgreSQL acceptance and successful fixture cleanup |
| Release/packaging | complete release/fresh report and passed shell gate, with all distinct packaging and binary checks retained |
| Honest simplification | separate production branch/line accounting and test/evidence growth; no claim that extraction alone is deletion |

The compiler, generator, schema/registry ABI, dashboard source, routes and RPC
behavior are unchanged by the intended implementation. Standalone fixture
regeneration and manual UI work are therefore not independently selected;
the retained release generator, TypeScript, dashboard and HTTP checks still
run. If the actual diff or oracle selects additional classes, perform their
exact root-matrix commands before completion.

The only permitted optional release skip is external-app smoke when
`SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` is unset. Missing Docker, an incomplete
timing inventory, or an unavailable mandatory fixture blocks completion.
No shared CLI installation, commit, push, or application restart is implied.

## Idempotence and Recovery

Store each attempt separately and preserve failed results. Run measurements
only on frozen authored source. If an implementation repair is needed, record
why, finish it, and renew the affected final evidence against that source.
Use only the existing release probes' owned disposable resources. Never prune
shared Docker resources, restart a machine agent, or modify a live database to
make proof pass. A test-binary cache is not a test-result cache.

## Artifacts and Notes

Use `.scenery/harness/sql-endpoint-resolution/` for baseline/final inventories,
characterization logs, full native timing samples, candidate reports, release
logs and deletion accounting. These are ignored machine-local evidence, not
new production schemas or committed artifacts. Preserve plan 0168 unchanged.

## Interfaces and Dependencies

### Auth assertion ownership map

Every original root remains in the fast Go lane. Its live persistence and HTTP
assertions are owned by the named case below, rather than being skipped or
given a different timing budget. The initial successful release-fixture report
is `final/auth-release-2019728057/summary.json`; it records all named assertions
and `cleanup_ok: true`. The earlier failed fixture attempt remains separately
recorded; its three redirect mismatches came from adding forwarded-proxy headers
to direct-origin requests. Removing that fixture-only header restored the
original direct-origin contract without changing auth production behavior.

| Original auth root | Release case | Preserved external-boundary assertions |
| --- | --- | --- |
| `TestStandardAuthBootstrapPostgresSchema` | `schema` | public initialization creates the framework auth table in `scenery` |
| `TestDevBootstrapDefaultEmailCreatesUserTenantAndMembership` | `dev-new` | stable user/tenant, verified dev user, one owner membership, no login identity or duplicate user, refresh cookies |
| `TestDevBootstrapAttachesExistingUserToConfiguredTenant` | `dev-existing` | reuse existing user, configured tenant wins, attach owner membership, cookie, no duplicate |
| `TestGoogleOAuthBrowserFlowWithFakeGoogle` | `oauth-browser` | PKCE and nonce, callback cookie/refresh/Me, replay rejection, verified-email and provider-free linking, unverified-password-account rejection without identity creation, bad nonce and unverified Google email rejection |
| `TestGoogleConnectionStartFallsBackToConfiguredAPIBaseURL` | `connection-redirect` | authenticated start uses the configured callback and current Google endpoint |
| `TestGoogleConnectionFlowStoresEncryptedTokenAndDisconnects` | `connection-store` | persisted email/status/scope, ciphertext not plaintext, exact refresh token consumed through the public API, HTTP status/disconnect, one revoke |
| `TestGoogleConnectionCallbackOAuthErrorUsesStateRedirect` | `connection-error` | stored OAuth state routes denial back to settings |
| `TestGoogleAccessTokenRefreshesRotatesAndSingleFlights` | `token-rotation` | cached access token, two concurrent callers with one provider refresh, encrypted rotation and subsequent exact-token consumption |
| `TestGoogleAccessTokenRetriesTransientAndMarksPermanentRefreshFailures` | `token-retry` | exactly two requests on transient failure, durable invalid-grant state, subsequent no-network failure |
| `TestGoogleAccessTokenReportsMissingScopes` | `token-missing-scope` | allowed but ungranted scope reports the distinct missing-scope error |
| `TestGoogleAccessTokenEnforcesAllowedScopes` | `token-disallowed-scope` | provider grants do not bypass the app allowlist |
| `TestPrepareImpersonationTargetAndStartUnverifiedSession` | `impersonation` | idempotent unverified provider-free target, membership, HTTP session with effective/actor claims, no verification, nested denial |
| `TestPrepareImpersonationTargetRequiresPrivilege` | `impersonation-privilege` | ordinary membership cannot prepare a target |
| `TestRefreshReplayRevokesSessionAcrossTransaction` | `refresh-replay` | real signup/login and two rotations, stale rejection, current token remains dead after transaction |
| `TestUserLifecyclePostgres` | `user-lifecycle` | forced rollback, scoped revocation/reasons, disable/enable idempotence, no session revival, actor impersonation revocation, missing-user errors |

The only new interface is a small internal pure endpoint-selection value and
function in the existing PostgreSQL owner. It has two concrete consumers and
no new dependency. Compiled runtime bindings, standalone `db` calls, and
`SCENERY_DATABASE_JSON` retain their existing entrypoints and wire shape.
