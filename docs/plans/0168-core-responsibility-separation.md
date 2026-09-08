# Smaller Scenery Core — Preserve the 100 ms Test Contract

This living ExecPlan adopts the developer's 2026-09-08 execution request and
2026-09-07 proposal. Maintain it according to [PLANS.md](../../PLANS.md).
This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective up to date as work proceeds.
Historical baseline c56e3e96 is a source map only; execution starts at the
post-generated-Go/post-worktree-runtime commit recorded below. This is not a
completed implementation report.

## Purpose / Big Picture

Remove repository-verification execution from the application CLI, then remove
duplicate interpretations of SQL capability requirements. Preserve application
features, typed contracts, ownership/crash recovery, and every required proof.
Move one responsibility at a time. Distinguish relocated code from deleted
mechanisms; a smaller product dependency closure need not mean fewer total lines.

`scripts/verify` will be an ordinary repository-local Go command in this module,
not a product command, plugin, new module, second test framework or scheduler.
It reuses `internal/testsuite` and its manual adapter `scripts/testsuite`.
The canonical compiled graph owns application requirements, environment
resolution owns supply choices, and verified persisted resource records own
actual allocation and recovery. These are different facts.

The exact top-level Go test contract remains p95 strictly below 100 ms, with
60 ms candidate target, 20 confirmation samples, nearest-rank percentile 95,
an empty exception inventory, full active-root accounting including subtests,
and unchanged regression/failure classification and release enforcement. Keep
cached correctness, fresh execution and isolated confirmation distinct. Linked
binaries may be reused; timing results may not. The developer additionally
requires 20 separate serial processes for every changed/moved/new exact root;
`go test -count=20` in one process cannot satisfy that final evidence.

Do not tune the fresh engine, scheduler, binary cache, concurrency or budgets.
Do not hide slow proof in TestMain, setup, subtests, build tags, omitted modules
or renamed tests. Real process, network, Docker, OS and toolchain journeys
belong in explicit release probes, with fast in-process coverage retained.

Symphony was removed before this plan; do not restore its implementation,
tests, UI, replacement app or migration requirements. Do not count its removal
as this plan's deletion. Do not remove desktop, libraries, assistants,
generated UI, deployment or agent inspection. Do not merge .scn/config,
infer schemas from SQL/imports, change object-sharing semantics, add environment
knobs, generic app contexts, registries, caches or compatibility fallbacks.
No shared CLI installation, existing app/agent restart, live data migration or
existing database/container mutation is authorized. Proof uses owned disposable
fixtures and the exact prepared worktree-local candidate binary.

## Progress

- [x] (2026-09-07 22:38Z; September 8 local time) Adopted the supplied reduced-scope proposal as permanent plan 0168; no existing adopted plan matched. Recorded clean main SHA 49cdc652b81e5e6b4884679ebbd7836b680d231e and current worktree inventory.
- [x] (2026-09-07 23:06Z) Captured the frozen baseline dependency/package/module inventory and 917 source failure/check anchors. Baseline release/fresh completed all functional and real-boundary steps, but its timing gate failed on five existing `internal/deployplan` roots; do not describe the baseline as green. Separate-process reconfirmation and the shell gate remain pending.
- [x] (2026-09-07 23:35Z) Preserved the pre-implementation source archive and binary hashes. The original shell release gate passed with only its explicitly disabled external-app check skipped. Twenty separate Go processes per failing baseline root produced reported p95 values of 70–90 ms; retain the original red report and raw reconfirmation events rather than rewriting it.
- [x] (2026-09-07 23:35Z) First M1 cuts: shared report data, evidence IO, payload identities and pure repository classification extracted; existing process mechanics moved to `internal/devprocess`. Affected consumer tests pass. Repository execution still temporarily exists in the product while the unshipped verifier is being prepared.
- [x] (2026-09-08 00:13Z) Created the unshipped `scripts/verify` comparison owner. Product reads now have an absolute prepared-binary boundary; assistant init, worktree commands, Go task execution, detached startup, desktop and frontend-restart probes are being converted to public commands. This is implementation progress, not runtime parity: the verifier still has unresolved private dependencies and does not yet build.
- [x] (2026-09-08 00:13Z) Moved agent-scope discovery to `internal/repoinfo`, session cleanup to `internal/agent`, watch-input policy to `internal/watchignore`, and concrete process observation/startup arbitration/named locks to `internal/devprocess`. Moved the dual-stack binding primitive to `internal/edge` without moving privileged address policy. Package correctness checks pass for these product consumers; final isolated timing and real-process validation remain pending.
- [x] (2026-09-08 00:34Z) The comparison verifier now builds and `go test ./scripts/verify` passes. Follower, parallel runtime, PostgreSQL/snapshot, Victoria and dashboard-bundle probes now use public product boundaries or their concrete production owners. Product/agent/process/edge/watch/repository-info/dashboard package tests pass. These converted real-process journeys have not yet run; no M1/M2 parity claim is made.
- [x] (2026-09-08 00:48Z) `go test ./...` passed, followed by the candidate verifier's `--quick --summary --write` (warnings only). Explicit disposable release-journey runs passed desktop/frontend restart, follower owner exit, Victoria replacement/recovery, product dashboard HTTP/hash, parallel SQL runtimes, full PostgreSQL/auth/durable/snapshot, assistant init, Git worktrees, task execution, docs resolution, named locks, detached startup and SSH exit/output preservation. Raw first failures and successful retries are retained under `.scenery/harness/core-simplification/`; this does not replace the complete release/fresh/timing scope or final cutover proof.
- [x] (2026-09-08 01:03Z) The complete candidate default run passed after explicitly generating fixture contracts before the matrix checks. Preserved its full report and both first-failure/retry summaries. Candidate timing confirmation now invokes each exact root in 20 separate serial processes, rejects incomplete/duplicate/cached event shapes, and fails closed for mandatory release confirmation; pure policy tests pass. Final native timing evidence is still pending.
- [x] (2026-09-08 01:03Z) Copied the remaining repository report tests and selected mixed-file tests into the unshipped candidate. Their product/toolchain boundaries now have call-local injected readers; real CLI/schema/fixture/toolchain behavior remains in the actual verifier steps. Candidate package correctness passes. An initial 917-anchor comparison finds 663 identical function bodies, 206 changed anchors requiring explicit replacement mapping, 47 retained-product anchors and one renamed root-discovery owner; these counts are not yet final acceptance.
- [x] (2026-09-08 01:33Z) The complete candidate release comparison finished. Every step except worktree cost-input stability and the native contract probe passed, including the full race suite. The cost gate correctly rejected edits made during measurement; retain that red report. The native probe's private build initialization was replaced with public generation/dev startup, real TypeScript-to-Go calls and an unchanged down/up cache-reuse check; its focused retry passed. Frozen worktree parity and the final integrated release remain pending.
- [x] (2026-09-08 01:52Z) The frozen candidate worktree/PostgreSQL retry passed all 18 acceptance scenarios in 1,056,137 ms, including all nine 1/5/10-worktree cost repetitions and the unchanged-source digest. No processes from its owned fixture remained. Together with the retained complete release comparison and native-contract retry, converted functional assertions now have replacement evidence; the singular cutover and final integrated fresh/timing run remain open.
- [x] (2026-09-08 02:31Z) Removed repository execution/timing/report-writer files from `cmd/scenery`, retained app/UI harnesses and bounded readers, and cut current docs, active-plan commands, help, recommendations, CI and the release shell over to `go run ./scripts/verify`. Product and verifier package tests passed; quick verification passed with known warnings. Product dependencies exclude `internal/testsuite`, `scripts/*` and test-only `internal/schemacheck`. Final orchestration and integrated evidence remain pending.
- [x] (2026-09-08 02:31Z) The actual core-boundary retry passed source-only verifier build, stale/matched product HTTP bundle checks, unavailable-toolchain diagnostics, removed-grammar SCN8001/exit 2, and app generate/check/harness/inspect/up after removing repository tools. A disposable slow root produced 20 separate-process samples with p95 112 ms and was correctly rejected. This is rejection proof, not evidence that candidate roots meet the budget.
- [x] (2026-09-08 02:31Z) Ran the same 31 policy replay inputs against frozen baseline source and the candidate: 25 outcomes matched; six intentionally strengthen rejection of malformed/cached/incomplete evidence. Thresholds, rank allowance, regression selection, warning/error classification for valid samples and forbidden exceptions matched. Raw records and the explicit differences are retained in `final/policy-replay-*.json*`. Actual moved/changed-root native confirmation and final full release/fresh proof remain pending.
- [x] (2026-09-08 02:45Z) Completed 20 separate serial linked-test processes for all 186 selected new/moved/changed roots (3,720 uncached executions). Every root passed; maximum raw p95 was 54.437 ms and the before/after authored-source digest matched. All 98 moved roots are included. Root inventory maps all 1,832 baseline roots to 1,838 current roots with zero missing roots; native test-binary packages increased from 58 to 60 without omitting no-test packages.
- [x] (2026-09-08 02:45Z) Reviewed every changed function in the 917-anchor assertion map and attached concrete replacement reports and retained production test owners. Corrected the PostgreSQL step's stale internal `--full` reproduction flag and the edge instruction's old source location. The new shell release gate is the remaining M2 integrated cutover check before SQL work begins.
- [x] (2026-09-08 02:49Z) The first cutover shell run stopped at lint. Removed 109 now-unused declarations (eight functions, 31 variable aliases, 62 type aliases and eight constants), fixed four error strings and an unchecked reader close, then `golangci-lint run ./...` passed with zero issues. Product/verifier/evidence package tests and `git diff --check` passed. Updated the PostgreSQL mode test to require a valid public release reproduction command; its additional 20-process p95 was 1.617 ms with unchanged source. Retained the first failure and reran the complete shell gate against frozen source.
- [x] M0: finish assertion/dependency/test/fact inventories, timing-policy snapshot, ownership handoffs and safe baseline proof.
- [x] (2026-09-08 03:13Z) M1: extract repository verification with preserved assertions and proof semantics.
- [x] (2026-09-08 03:13Z) M2: prove parity, cut every live consumer over, remove product execution and equivalent duplicate shell work. The complete shell release gate passed against frozen authored source: all 48 repository steps, the full race suite, source-snapshot build, fixture HTTP smoke, router safety and artifact hygiene. The optional existing external-app smoke was explicitly skipped because no external app was selected. Evidence is retained in `final/candidate-release-cutover.json`, `final/candidate-agent-context-cutover.json` and `final/cutover-release-gate-retry/`. M3 has not started implementation; M4 still requires the final integrated fresh/timing and workflow comparisons after SQL cutover.
- [x] (2026-09-08 04:36Z) M3: consolidate SQL requirements through the completed runtime resolver. Final targeted capability-authority run passed all seven named journeys with cleanup, including source-invalid snapshot recovery and the separate external worker. Full Go correctness and lint passed, quick verification passed with existing warnings, and both owned dashboard harnesses passed six routes. Manual Chrome proof found and fixed missing SQL metadata in control-plane publication; the dashboard now browses the actual table and returns both `inbox` and `scenery` from a read-only schema query. The no-SQL dashboard remains empty. Both owned browser runtimes were stopped and their retained PostgreSQL records are null. Final M4 timing and integrated release evidence remain open.
- [x] (2026-09-08 03:42Z) M3 implementation in progress: removed the duplicate generator dependency interpreter, moved native-service/durable selection to compiler ownership, and added the immutable SQL requirement projection with canonical identities and schema validation. Product startup, worker supply, generation inspection and setup/seed now consume it. Removed config `dev.services` with actionable rejection. Product build passes, but fixture/test migration, current schemas and integrated proof are still open; this is not M3 acceptance.
- [x] (2026-09-08 04:18Z) Migrated all config-list consumers and fixtures; the full Go suite passed, client fixtures regenerated, and lint passed. The new named capability-authority release step passed targeted execution, including no-SQL absence, auth-only SQL, typed managed and external webhook behavior, shared/multiple module bindings, pre-allocation lifecycle/schema rejection, and snapshot recovery with invalid/removed source. Tightened the restore assertion to read the original row before any replacement enqueue. Retained four probe attempts and cleanup evidence. Quick validation identified a redundant framework-derived endpoint name; framework SQL now continues to use canonical DATABASE_URL, with only its schema metadata in the existing registry. Final quick/release and isolated timing remain pending.
- [x] (2026-09-08 10:28Z) M4: complete integrated functional/timing proof, measured deletion accounting, documentation and handoff. All 49 release/fresh steps and the final shell release gate passed. All 339 changed/moved/new roots passed 6,780 isolated fresh processes with maximum p95 90 ms; all 917 original assertion anchors have replacement evidence, and no surviving root is unmapped. Final results, limitations and exact validation commands are recorded below. Changes remain uncommitted on `feat/core-responsibility-separation`; no shared CLI installation or live-resource migration was performed.
- [x] (2026-09-08 05:48Z) Completed 6,120 native separate-process executions covering all 306 selected roots, maximum p95 64.453 ms, with matching authored-source hashes. All 1,832 baseline roots map to 1,845 current roots (98 moved, three transferred, none missing). Three alternating same-scope cached and manifest-hit fresh Go workflows passed. The first full release/fresh audit was deliberately interrupted after more than 42 minutes of confirmation: calling the Go build driver for every sample introduced unnecessary launch overhead. Its preserved report also contains six genuine hard timing failures, not just cancellation errors. Confirmation now links one disposable binary per package and launches 20 independent serial processes through `go tool test2json`; this uses the plan's explicit linked-binary allowance without changing `internal/testsuite`, its cache, scheduling, scope or any timing budget. Renew final timing/release evidence after this execution-only repair.
- [x] (2026-09-08 05:57Z) Rechecked all six previously failing roots with 20 fresh processes each: p95 24-90 ms. The explicit slow fixture was correctly rejected at 112 ms. Two roots needed fixture repairs: edge lease tests now supply a branch rather than incidentally launching Git, while native MCP revision proof retains all four independent compilations and every assertion but reuses one owned fixture tree. The actual Git branch boundary remains in the worktree release probe. Final all-changed-root and integrated release evidence must still be renewed.
- [x] (2026-09-08 06:39Z) The renewed 309-root proof passed 6,180 separate-process samples, maximum p95 90 ms and identical source hashes. Equal-scope workflow observations passed, but the full release/fresh report correctly failed: two other timing roots exceeded budget, the worktree fixture client was stale, and storage/native restart both exposed a nil compiled contract on executable-cache reuse. Preserve `final/candidate-release-final-failed.json` and timing/raw logs. `refreshCachedGoProjection` already compiles and validates the current contract; it now retains that result for runtime setup instead of discarding it. The existing cached-compile test checks this handoff. Regenerated the worktree fixture client. Targeted fixture matrix, storage restart and native public restart all passed. Without test-body edits, 20 new isolated samples for the two timing roots measured p95 80 and 84 ms; this does not erase the earlier failure or waive the final full audit. Renew final integrated evidence after the cache handoff repair.

- [x] (2026-09-08 07:50Z) Preserved the latest failed integrated audit as `final/candidate-release-space-failed.json`, `final/candidate-agent-context-space-failed.json`, and `final/final-release-fresh-space.log`. The preceding attempt exhausted disk space and could not write its final report; its empty command log and explicit ENOSPC fixture log are retained. Only task-owned, hash-checked disposable measurement binaries (744,172,256 bytes) were removed; no shared cache was cleared. Available disk space subsequently recovered externally. The final 316-root standalone measurement passed 6,320 fresh serial samples with maximum p95 90 ms and unchanged authored-source hashes (`final/native-roots-restart-final/report.json`), but the later full audit failed MCP fixture timing at p95 100 ms and DNS state timing at 120 ms. Earlier passing samples do not supersede these failures. Full race and the repaired storage/native restart probes passed. Docker/OrbStack is now unavailable (its Unix socket is absent), so mandatory capability/worktree SQL acceptance did not run; these are failures, not accepted skips. Do not start the engine without operator direction because existing containers could restart. M4 remains open, and the final shell gate has not run on the M3 candidate.
- [x] (2026-09-08 07:50Z) Retained the equal-scope 1,829-root workflow comparison in `final/workflow-cost-space-final/report.json`: cached baseline 1.511/1.552/1.538 s versus candidate 1.589/1.545/1.530 s; fresh manifest-hit baseline 4.416/4.261/4.480 s versus candidate 4.511/4.570/4.503 s. These observations do not demonstrate a speedup. The earlier 8.663 s candidate outlier remains recorded separately in `final/workflow-cost-restart-final/report.json`. Resume by resolving the two timing failures without policy relaxation, restoring operator-approved Docker availability, then rerunning complete release/fresh and shell-gate proof before closing the plan.

- [x] (2026-09-08 07:55Z) The pause-time quick audit found the moved durable DNS migration release fixture missing from the architecture check's existing migration-owner path map. Registered its actual release owner (`scripts/verify/harness_self_edge.go`) without adding a new identity, migration, or timing exception. This source change requires final evidence renewal; the earlier 316-root source hash does not certify the updated tree.

- [x] (2026-09-08 07:57Z) The developer restored OrbStack; `docker info --format '{{.ServerVersion}}'` now returns 29.4.0. Before restoration, the DNS test's durable writes were moved to the existing edge release journey, preserving exact backup bytes and resolver ownership; the ordinary root retains conversion and current-state read checks. Its 20-process p95 is now 30 ms. The MCP fixture keeps four independent compilations, reuses its owned implementation directory through rename/restore, and retains only baseline revision strings needed across mutations; 20-process p95 is 94 ms with limited headroom. `final/linked-mcp-dns-recheck.json` retains these samples and deliberate slow-fixture rejection. `final/edge-migration-recheck.json` proves the real DNS migration; edge race, full Go, lint and the corrected quick audit passed. These focused results do not replace the pending integrated audit. Freeze authored inputs and renew all changed-root, workflow, release/fresh and shell-gate evidence now that Docker is available.

- [x] (2026-09-08 08:11Z) The renewed 321-root run finished all 6,420 samples with unchanged source and exactly one failure: MCP p95 102 ms (`final/native-roots-release-final/report.json`). Profiling showed canonical encoding allocation overhead. Removed two avoidable string/byte copying round trips and the reflected map-key slice in `internal/spec/canonical.go`; encoding, UTF-8 validation, revision semantics and every MCP assertion remain unchanged. Nine native/house/assistant view hashes plus `spec_revision` match byte-for-byte before/after (`final/canonical-parity-{before,after}.json`). Added focused string escaping/nested-map/invalid-UTF-8 coverage. Spec/compiler/contract-agent/CLI tests pass; fresh isolated MCP p95 is 91 ms and DNS 30 ms, with the slow fixture still rejected (`final/linked-canonical-recheck.json`). This is an allocation-only implementation repair, not a new cache or test-engine optimization. Renew final integrated evidence again; no failing report is discarded.

- [x] (2026-09-08 09:02Z) The 322-root standalone audit passed all 6,440 samples (maximum p95 92.413 ms, source hash `99d0d8af6909c70a0322c53be3881950b4694bd7ae2a1435296a17501ff52f09`), and the equal-scope workflow comparison passed. The complete release/fresh audit nevertheless failed three isolated roots (domain ownership 100 ms, cache source refresh 111 ms, MCP 100 ms) and worktree case A9. Preserve `final/candidate-release-canonical-failed.json`, its timing/agent reports and `final/final-release-fresh-canonical.log`. All other release steps, including capability authority, storage/native restart and race, passed. A9's retained private log proves the historical binary lacked its old `dev.services.library` selector after the current fixture removed that field. Only old/sibling sandbox copies now receive that historical selector; current product/config fixtures remain singular and reject it.
- [x] (2026-09-08 09:02Z) Pinned the domain-ownership fixture's unrelated Git branch. Source-refresh coverage now supplies a fixed generator identity through the existing private once-cached function, scoped to a non-parallel test with restoration; real generator content/invalidation coverage remains at its owning tests and release build paths. The production once cache is expressed with `sync.OnceValues`, not a new cache or changed validity rule. Canonical encoding now writes only provably unescaped ASCII directly and keeps standard JSON encoding for every control, quote, backslash, HTML-sensitive or non-ASCII value; exhaustive ASCII comparison and the nine-view/spec-revision parity remain green. A direct-Encoder experiment was discarded. Four focused 20-process measurements pass: domain ownership 20 ms, source refresh 70 ms, MCP 82 ms and DNS 21 ms (`final/linked-ascii-three-repairs.json`); the deliberate slow fixture remains rejected. Renew standalone and integrated evidence after these repairs; M4 is still open.

- [x] (2026-09-08 09:07Z) The isolated A9 retry passed in 51.754 seconds (`final/legacy-selector-recheck.json`): historical sibling served 269 requests with zero failures, the original shared resource record stayed unchanged, native migration preserved rows/roles/grants/extension/framework data, and the disposable sandbox was removed. The full Go suite, lint and build-package race check also passed after the repairs (`final/repair-{go-test,lint,build-race}.log`). No existing operator resource was changed. Freeze the repaired implementation for the remaining final evidence runs.

- [x] (2026-09-08 09:16Z) Stopped the next standalone audit after 115 completed roots because the session-report test failed at p95 123 ms; its partial report and raw samples remain in `final/native-roots-last-final/`. The report handler test was still starting a real agent and polling a Unix socket. It now uses the existing registry interface and real session construction in process, retaining report acceptance through the session token. The existing local-agent restart release probe now explicitly checks that real socket registration preserves that token. Targeted CLI/verifier tests and the release probe pass; 20 fresh processes measure the handler root at p95 20 ms (`final/linked-report-token-recheck.json`, `final/report-token-release-recheck.json`). All four prior repaired roots still pass and the slow fixture is rejected. No assertion, timing rule or fresh-test cache behavior was removed. Renew complete frozen-source evidence after this boundary repair.

Each milestone stays open until its mandatory evidence passes. Update timestamps
at meaningful stopping points, including failures and blockers. Completing one
independent milestone does not complete the plan.

## Surprises & Discoveries

- The first final integrated release found a cold-versus-reused executable
  distinction missed by the earlier managed/no-SQL startup probes. Cached build
  refresh already validates current source but discarded its compiler result;
  SQL seed setup then dereferenced a nil contract on public restart. Retaining
  that already-validated immutable result fixes both storage and native restart
  without recompiling a second time or trusting persisted requirements.

- The worktree cost probe hashes all authored repository inputs, including new
  verifier files. Editing them while it runs invalidates its evidence even when
  the runtime scenarios succeed. The first candidate release correctly failed
  this gate; all subsequent cost comparisons must freeze authored inputs.
- The native fixture declares a contract-only assistant implementation, so
  public `build --target development` still enters production-asset packaging
  and is not equivalent to the original development build. Public `up` preserves
  the original runtime scope. Its release proof now checks the actual compiled
  manifest, generated entrypoint, runtime bundle, generated client calls and
  unchanged binary inode/mtime plus graph identity across public down/up.
  Prepared-phase publication remains in
  `internal/build.TestPrepareAndCompileWriteLatestBuildManifestInProcess`;
  configured-flag non-mutation was added to the existing
  `TestCompilePassesConfiguredGoBuildFlags` root. No build-hook initializer was
  copied or exported for verification.
- After the follower deliberately killed its owner, public down left one
  VictoriaTraces process alive in two comparison runs. Both were verified
  against their exact owned fixture paths and stopped. The probe now retains
  all component fingerprints through cleanup, signals only still-verified
  children, and waits for exit before deleting its fixture. The focused retry
  passed; this is fixture cleanup, not a new product lifecycle path.
- Contract-test schema validation now has one owner in
  `internal/schemacheck`, used by the verifier and product tests. It creates no
  new test binary and must stay outside the final product dependency closure.

- A normal public `down` can terminate VictoriaLogs before the follower's
  two-second owner poll, causing a log-transport error rather than a graceful
  detach. The original preserved release assertion killed the owner process;
  the candidate does the same against a fingerprint-verified real owner, then
  uses public `down` for retained-child cleanup. The broader graceful-down race
  is recorded here, not silently fixed through runtime redesign during M1.
- A failed transactional snapshot merge correctly retains `restore-failed`
  authority and blocks ordinary `db drop`. The old private drop helper bypassed
  that product gate. Candidate proof exercises A's successful drop before
  recreating its snapshot target, leaves failed-merge recovery authority intact,
  and explicitly prunes only the owned cluster during final cleanup. B's drop
  is still independently exercised.
- Public checks require current generated fixture contracts. CLI drift and SSH
  probes now generate their own copied fixture first instead of depending on
  source-tree cache freshness. Dynamic construction of environment-variable
  names was removed so the existing environment registry can audit exact names.
- One initial Victoria process-attribution run timed out while the whole Go
  suite ran concurrently; isolated retries preserved the exit-42 assertion and
  passed. Keep the first failure artifact; final full release still must pass.

- A separately compiled verifier cannot prove product dashboard freshness by
  embedding another copy. Its candidate freshness probe reads the running
  prepared product's HTTP bundle hash and compares it with the shared
  asset-name hash function now owned by `internal/devdash`.
- Public fixture PostgreSQL preparation uses `db server start` plus `db setup`;
  teardown uses exact-root `down` and `prune --db`, then checks the retained
  allocation is retired. Snapshot proof uses public save/load and storage
  put/get. Private resolver orchestration is not duplicated in the verifier.
- Parser-only `inspect ui --frontend` and `ps --watch` checks moved to
  `TestRepositoryProbeParserContract`; the verifier executes read-only public
  commands. Exact registration-count coverage remains in
  `TestRegisterDevSessionOnceWithFrontendBackendsInProcess`. Victoria's exact
  same-process arbitration remains in its existing in-process test, while
  release covers the real lock, one reused runtime, stale owner replacement,
  full-stack recovery and public shutdown. Dashboard substrate adaptation
  remains covered by `TestAgentDashboardControllerUsesVictoriaSubstrate`.

- Extraction found additional real OS behavior inside ordinary named-lock
  coverage. `TestDevNamedLockSerializesSameProcessAcquisition` now uses fake
  lock operations and `synctest`; the same-process OS assertion joins the
  existing cross-process release probe. Call-local `LockOptions` replaces
  mutable global timing overrides while preserving production defaults.
- The detached-startup probe had started an unrelated machine agent and then
  checked its empty session list. Current worktree runtimes do not register
  there. The candidate checks actual private worktree sessions through public
  `ps`; no machine-agent/dashboard fixture is needed for this assertion.
- Public desktop proof now uses authored basic-app copies, an owned upstream,
  dynamically returned API/frontend routes, fake local Tauri/Vite executables,
  real `up --desktop`, and public `down`. Default restart policy is exercised;
  the verifier does not export or copy the CLI supervisor.

- Baseline checkout is `/Users/petrbrazdil/Repos/scenery`, clean main at
  `49cdc652b81e5e6b4884679ebbd7836b680d231e`. Plans 0165 and 0167 are completed
  and indexed as such. Another detached worktree exists at c206930c; it is not
  an execution target and no edits there are authorized. Active plans are 0145
  (test-loop attribution) and 0101 (public-edge operator observation).
- Current `harness_timing.go:confirmHarnessTimingOutliers` actually groups
  candidates by package and invokes `go test -count=20 -parallel=1` once per
  package. Local Contract documents that shared-process behavior. The user's
  stronger separate-process requirement is therefore a real discrepancy,
  not proof that current execution already meets it. Retain the calculation,
  accounting, thresholds and candidate policy; explicitly correct invocation
  isolation at the verifier boundary and record comparison evidence.
- Existing timing diagnostic text still suggests an integration exception even
  though `validateHarnessTimingIntegrationExceptions` rejects every entry.
  Correct this contradictory remediation without introducing an exception.
- Webhook's graph declares SQL `data_source.inbox` with `lifecycle = external`,
  while config declares `dev.services.inbox`. These cannot be treated as blanket
  managed-provisioning authority. M3 must preserve external semantics; managed
  fixture proof needs an explicitly managed declaration and external proof an
  explicit external binding.
- `internal/testsuite` is in the baseline application CLI dependency closure.
  The current self-harness includes 170 named run/probe functions across its
  repository helpers; file counts alone are not an assertion map.
- The full baseline report contains five timing failures (110–150 ms p95),
  although the bounded human summary prints only three. Full raw reports are
  retained under `baseline/`; the displayed summary is not a complete finding
  inventory. Every non-timing step passed, including worktree/runtime cost and
  recovery scenarios and the full race suite.
- Current confirmation quantizes samples to milliseconds before nearest-rank
  calculation. `timingConfirmationFailure` emits a warning even in release
  mode. The supplied acceptance requires unusable timing evidence to fail;
  explicitly preserve the existing conservative threshold behavior while
  adding separate-process evidence and fail-closed mandatory confirmation.
- Three existing process-runner test roots contained shell/network work despite
  the ordinary-test boundary rule. Keep their names with in-process completion,
  readiness and once-guard coverage; their actual early-exit/output, TCP probe
  success and ready/unready repeated-stop assertions now belong to the managed
  process release probe. This is assertion relocation, not a deleted feature.

## Decision Log

- 2026-09-08, implementation owner: reuse only disposable package-linked test
  binaries for confirmation, as explicitly allowed by this plan. Twenty fresh
  serial processes, exact-root active-time accounting, nearest-rank p95 and the
  strict 100 ms cutoff remain unchanged. This removes Go-driver startup overhead
  without changing the fresh engine or adding a persistent cache. Keep the
  failed/canceled measurements and fix actual fixture costs rather than changing
  timing policy. Failure diagnostics retain commands usable after cleanup.
- 2026-09-08, developer: execute the supplied plan until done; preserve the
  absolute 100 ms rule, all evidence and exclusions. No shared installation or
  operator-resource mutation follows from this authorization.
- 2026-09-08, implementation owner: allocate 0168 once (0167 is highest existing
  permanent plan); all work remains in this main session without subagents.
- 2026-09-08, implementation owner: accept the already completed 0165/0167
  production interfaces as the cutover base, then verify their current source
  and disposable acceptance. Do not rewrite completed plans. Transfer only
  verification ownership from active 0145; its optimization goals remain open.
- 2026-09-08, implementation owner: separate-process confirmation strengthens
  invocation isolation requested by the developer; it is not permission to
  alter the fresh engine, nearest-rank algorithm or budgets. Baseline results
  must accurately identify their shared-process sampling.
- 2026-09-08, implementation owner: SQL projection reuses the existing service
  registration selection, resolved Go dependency bindings and durable execution
  registration selection, moving their pure interpretation into the compiler.
  Standard-auth SQL follows the existing config-selected framework registration;
  it is distinct from Google-only endpoint projection. Remote durable workers
  retain their explicit endpoint supply and do not require local framework SQL.
  This introduces no provider registry or new resource lifecycle owner.
- 2026-09-08, implementation owner: `dev.services.*.env` is accepted by the
  current config decoder but has no product reader; it is not a supported supply
  override. Remove the duplicate section with actionable rejection. Supported
  endpoint supply stays in selected-environment dotenv/process `DATABASE_URL`;
  explicit database apply, seed/import and auth bootstrap choices stay unchanged.
  Logical schema validation moves to canonical bindings. Disposable managed
  fixtures must declare managed lifecycle; the webhook example remains external.
- 2026-09-08, implementation owner: the public `db.Get` package also independently
  read the config list. Remove that reader without linking compiler into runtime.
  Generated entrypoints pass compiled name/schema bindings through the existing
  SQL environment before constructors. Preserve explicit per-binding
  `<NAME>_DATABASE_URL` supply and `SCENERY_DATABASE_JSON` for standalone runtime
  callers; these were real supported supply paths, unlike `dev.services.*.env`.
  Default `db.Get()` uses the supplied binding set, excluding framework-only
  schema entries when application bindings exist.
- 2026-09-08, implementation owner: snapshots use verified retained allocation
  and observed PostgreSQL service schemas, never current requirement discovery.
  Explicit archive overwrite supplies restore authority; merge checks actual
  target schemas. Explicit start of a retained server can recover invalid or
  removed source without permitting new allocation. Fresh allocation still
  requires a valid compiled managed requirement and the existing owner guards.

Future decisions require date, owner, rationale and evidence. Private placement
may be refined; public test hooks, hidden production release dispatchers,
go:linkname and test-only production exports are prohibited.

## Outcomes & Retrospective

Completed against the reconciled M0 source baseline at `49cdc652b81e5e6b4884679ebbd7836b680d231e`, including its recorded pre-existing state. Repository verification now lives in `scripts/verify`; the product neither executes it nor depends on `internal/testsuite`. The product retains application validation and bounded evidence inspection. Equivalent release-shell orchestration was removed, while clean-source packaging and binary-level checks remain in the shell. The compiler's immutable SQL requirements now feed generation, supply resolution, runtime bindings, inspection and database tooling; the duplicate `dev.services` requirement list is gone. Actual allocation records still own cleanup and recovery independently of current source validity.

The timing policy and `internal/testsuite` / `scripts/testsuite` implementation are unchanged. Real OS assertions moved to named release probes where required; ordinary tests retain their in-process assertions. Earlier retired features are neither restored nor credited to this plan. No existing application, database or agent was restarted or migrated; the developer explicitly authorized starting OrbStack when the disposable Docker proof required it.

| Measured surface | M0 baseline | Final candidate | Interpretation |
| --- | ---: | ---: | --- |
| Product dependency closure | 456 packages | 442 packages | 18 removed, four narrow production owners added; no verifier/test engine dependency |
| `cmd/scenery` Go source, including tests | 355 files / 87,595 lines | 261 files / 64,549 lines | 94 fewer files and 23,046 fewer lines in the product command tree; not all are deletions |
| Repository Go source | 1,157 files / 254,107 lines | 1,231 files / 258,046 lines | Net **increase** of 74 files and 3,939 lines after extraction, explicit boundaries and proof |
| Repository-local verifier | 0 files | 129 files / 21,776 lines | Moved ownership plus preserved and added boundary proof |
| Exact top-level test roots | 1,832 | 1,846 | 98 moved, three assertions transferred to release boundaries, zero unmapped surviving roots |
| Original assertion anchors | 917 | 917 mapped | 549 unchanged declaration bodies; 368 reviewed changed/product-retained anchors with explicit replacement evidence |

The complete standalone native audit measured 339 new, moved or materially changed exact roots in 20 fresh serial processes each: 6,780 samples, zero failures, maximum p95 90 ms. The authored-source hash before and after was `2d5475ac35175249c247f282ac63cb60c0898502a054b6aae4849869c9f6fdd6`. The final release/fresh audit independently confirmed 202 observed candidates, with zero confirmed slow tests, zero deferred confirmations and an empty exception inventory. Its fresh correctness run took 4.118 seconds with a manifest hit and zero test binaries rebuilt. All 49 release steps passed, including full race, capability authority, source-invalid recovery, historical Linux/Docker coexistence, native migration and all nine 1/5/10-worktree cost repetitions. The shell gate then independently passed repository verification, source-snapshot build, fixture smoke, router safety and artifact hygiene. Documentation-only closure followed the frozen implementation proof.

Equal-scope workflow observations used 1,829 surviving roots, two warmups and three alternating baseline/candidate observations per mode. Cached baseline seconds were `1.459, 1.445, 1.436`, candidate `1.442, 1.431, 1.389`; manifest-hit fresh baseline seconds were `4.377, 4.312, 4.385`, candidate `4.320, 4.471, 4.380`. These observations do not establish a general speedup. Performance evidence is native macOS only; Linux/Docker functional proof passed, but Linux performance was not measured.

Every command in the Validation and Acceptance matrix passed, with the default verifier scope superseded by successful release/fresh and release-gate runs. The final command/result record is:

| Command and working directory | Result / evidence |
| --- | --- |
| From root: each `go test` affected-package command listed below, plus `go test .`, `go test ./internal/contractagent`, `go test ./internal/harnessevidence`, `go test ./internal/harnessreport`, `go test ./internal/schemacheck` | PASS; final exact package invocations in `final/closure-targeted.log` |
| From root: `go test ./...` | PASS; `final/closure-go-test.log`, also both release runs |
| From root: `go test -race ./...` | PASS; final release step `race full suite` and shell-gate release |
| From root: `golangci-lint run ./...` | PASS; `final/report-final-lint.log` and shell gate |
| From root: all three `go run ./cmd/scenery generate --target typescript_client.public_api --app-root ... -o json` commands below | PASS; `final/regenerate-{native,house,assistant}-last.json`; committed fixture outputs refreshed in this change |
| From root: `bun test internal/generate/testdata/typescript_client_conformance.test.ts` and both listed `tsc -p` commands | PASS; final release conformance, generated-client and UI-catalog steps |
| In `apps/console`: `bun run lint`, `bun run typecheck`, `bun run build`; from root: `scripts/build-dashboard-ui-embed.sh` | PASS; M3 console proof and final release/gate typecheck/build/embed |
| Product `harness ui -o json --write` against the two owned M3 browser fixtures | PASS, six routes; Chrome confirmed managed SQL table preview and read-only schema query, and no SQL metadata/resource for the basic fixture; `final/browser-evidence/` and `final/browser-{webhook-fixed,basic}.json` |
| From root: `go build -o .scenery/harness/bin/verify ./scripts/verify` and `.scenery/harness/bin/verify --quick --summary --write` | PASS; final implementation quick and documentation-closure quick reports |
| From root: `.scenery/harness/bin/verify --release --fresh-tests --summary --write` | PASS, all 49 steps; `final/candidate-release-final.json`, `final/candidate-timing-final.json`, `final/final-release-fresh-last.log` |
| From root: `SCENERY_BIN="$PWD/.scenery/harness/bin/scenery" SCENERY_RELEASE_GATE_LOG_DIR="$PWD/.scenery/harness/core-simplification/final/final-release-gate" scripts/release-gate.sh` | PASS; `final/final-release-gate-run.log`, `final/final-release-gate/`, `final/candidate-shell-release-final.json` |
| From root: `bash -n scripts/release-gate.sh`, `git diff --check` | PASS |

All evidence paths in this section are relative to `.scenery/harness/core-simplification/` and remain ignored machine-local artifacts. Failed and interrupted earlier attempts remain preserved and are not presented as passing evidence. The only skipped applicable optional check was external-app smoke because `SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT` was unset; no mandatory disposable proof was skipped. The passing integrated reports retain 42 documentation-freshness warnings and 23 large-file warnings with zero errors. Those warnings are not a claim of repository-wide debt elimination. No unresolved implementation decision remains within this plan. Work is implemented and verified in the named uncommitted branch, not claimed pushed or released to users.

## Context and Orientation

Read root/nearest child AGENTS, PLANS, ARCHITECTURE, current validation,
generation and runtime sections of Local Contract and Agent Guide, Harness
Engineering, active plans, and matching technical-debt entries before edits.

| Responsibility | Current source | Intended owner |
| --- | --- | --- |
| Repository execution/timing/release proof | cmd/scenery/harness_self*, harness_timing*, harness_oracle | scripts/verify |
| Fresh execution/cache/scheduling | internal/testsuite, scripts/testsuite | unchanged existing engine/adapter |
| Application validation/UI/evidence reads | harness.go, harness_browser*, inspect_harness* | product plus narrow data-only shared contracts |
| Docs/changed-area classification | harness_knowledge*, harness_validation_selection, inspect_docs* | one pure owner where product readers actually need it |
| Release packaging | scripts/release-gate.sh, CI | shell unique artifact/binary checks; verifier common suites once |
| Required SQL facts | graph/compiler, Config.DatabaseServices, appenv, db and generation | one immutable canonical requirements projection |
| SQL allocation/recovery | worktree_postgres*, internal/agent/worktree* | existing runtime resolver and persisted verified records |

`internal/generate/api.RuntimeIntegrationPlan` is not assumed to be an allocation
plan and must not become a universal context. `internal/app` stays compiler- and
PostgreSQL-driver-free. Compilation remains side-effect-free.

### M0 ownership and acceptance mapping

| Existing owner | Handoff required / status |
| --- | --- |
| 0165 generated Go | completed at baseline; ignored in-module rendering/publication remains untouched |
| 0167 runtime/PostgreSQL | completed; inspect current resolve/ensure/observe/stop/delete interfaces and carry allocation independently from requirements |
| 0145 harness | active; transfer policy/execution location only, preserve 4 build / 6 package parallelism and all budgets |
| 0101 public edge | leave live services/operator observation unchanged; relocate assertions only |

Baseline artifacts live in `.scenery/harness/core-simplification/baseline/`.
The final inventory will include package/test roots including no-test and nested
modules, production dependencies, CLI entry points, modes, report writers,
assertion identities and capability fact classifications. Every assertion row
must name current entry, actual assertions, scope, environment/binary identity,
freshness, failure/skip behavior, new destination and replacement evidence.
The generated `baseline/assertion-map.json` records 917 source-anchored failure
expressions/diagnostic messages with parent-function SHA-256 values. These are
conservative preservation anchors, not a claim of 917 independent behaviors;
replacement evidence remains empty until the corresponding candidate executes.
The mixed `harness_run_test.go` must keep application and artifact-reader tests
in the product package while moving repository-execution tests.

| Current assertion family | Scope / binary / freshness | Destination and preserved boundary |
| --- | --- | --- |
| Knowledge, links, architecture, drift, changed paths and checked schemas | quick/default/race/release; actual source; current read | `scripts/verify`; shared pure classification and report data, no product test execution |
| Cached Go, fresh engine, timing candidates/confirmation and race | selected mode; current toolchain and exact source; cached correctness distinct from fresh samples | `scripts/verify`, unchanged `internal/testsuite` and manual adapter; exact-root rename map and timing replay |
| Console dependency/build/type checks and embedded freshness | default/race/release; prepared worktree product binary | repository runner; actual product dashboard provenance, never verifier embedding |
| Parallel dev, PostgreSQL and object storage | default/race/release; owned app copies and recorded resources | repository runner via product CLI/runtime and existing allocation owners |
| Worktree runtime A1–A18, generated-Go/native contracts and snapshot recovery | release; prepared product and deliberately identified disposable variants; no outcome cache | move complete scenario assertions and cleanup ownership to repository runner |
| CLI exits, telemetry, assistant initialization/production, scripts, TypeScript, validation, Git worktrees and SSH | release; real product CLI or declared tool boundary | public CLI subprocesses with current typed decoding; fake external executables only inside owned fixtures |
| Child startup, detach/follow, named locks, stale-session cleanup, desktop/frontends | release; owned child processes and private control roots | public runtime commands or genuinely shared production process/session owners; ordinary tests remain fast |
| Edge listeners/static routes and Victoria process lifecycle | release; exact owned listeners/processes and existing production owners | `internal/edge` / `internal/victoria` production boundaries; no operator service changes |
| Report summary and bounded artifact reading | product read-only inspection; existing artifact identities | data-only `internal/harnessreport`; one payload identity registry in `internal/machine` |
| Release shell packaging, source-only build, router safety and artifact exclusion | explicitly prepared binary or separately identified source-snapshot binary | retain unique shell assertions; common suites delegate once to repository runner |

First concrete rename map: `TestCLIPayloadSchemaRevisionsMatchCheckedSchemas`
moves from `cmd/scenery` to `internal/machine`; the five
`TestDevManagedProcess*` roots and `TestStripANSIDoesNotAliasInput` move to
`internal/devprocess` with their names retained. All need new isolated timing.
The first three process roots' OS assertions additionally map to
`runHarnessManagedProcessBasics` and the unready-child repeated-stop assertion
inside the release managed-process probe. Pure report/index/evidence leaves
have no new test binary; their actual command consumers retain coverage.

Every source failure path remains an error or explicit diagnostic of its parent
step; existing platform/not-applicable guards retain their reason. A mandatory
plan acceptance cannot be satisfied by an optional skip. Baseline and candidate
results are compared by these assertions and scopes, not absolute paths or time.

### Capability fact ownership (M0)

| Source / fact | Meaning | Authority after consolidation |
| --- | --- | --- |
| Webhook `data_source.inbox`, SQL capability IDs and module input reference | reachable typed requirement, canonical identity and provenance | one compiled-graph requirement projection |
| Webhook `config.database = inbox` / injected `datasource.SQL` | logical database/schema binding, not resource ownership | same projection consumed by generation and runtime |
| Webhook datasource `lifecycle = external` | forbids implicit managed provisioning for that requirement | preserved lifecycle plus explicit environment supply |
| Webhook `execution_engine.tasks`, authentication declaration | framework runtime requirements | existing canonical framework projection only |
| Webhook `dev.services.inbox` and assistant fixture `assistant_runtime` | currently a second positive requirement list | remove requirement interpretation; preserve any actual supported supply choice separately |
| Webhook `auth.auto_bootstrap_database`, `dev_bootstrap`, database apply command | setup and seed policy, not semantic SQL discovery | existing explicit setup/seed owner |
| Selected environment and `DATABASE_URL` | supply selection and external endpoint | environment resolver; external supply is never implicit allocation authority |
| Config app ID and canonical checkout root | durable identity | existing app/root identity and worktree resolver |
| Worktree `Postgres` record, exact Docker identities and restore journal | observed/owned allocation and recovery evidence | existing retained resource owner, even when current source is invalid |

Root `./...` inventory has 58 test-binary packages and also preserves no-test
packages. Seven `go.mod` paths are recorded in `baseline/module-paths.txt`,
including webhook, basic, assistant, storage, worktree SQL and native compiler
fixtures. Nested-module correctness is a separate release assertion, not covered
by the root package list alone.

### Preserved timing-policy snapshot (initial current-source read)

| Item | Current implementation contract |
| --- | --- |
| Candidate / hard limit | 0.060 s / p95 < 0.100 s; equality fails |
| Samples / percentile | 20 / nearest-rank 95 (second-highest of 20) |
| Scope | fresh: regressions; release fresh: all candidates |
| Regression | root >=25% and >=10 ms worse; package >=25% and >=0.5 s worse; missing/stale baseline confirms everything |
| Mandatory reconfirmation | observation >=hard limit or prior confirmed p95 >=hard limit |
| Accounting | complete active root including subtests, exclude only scheduler pause; package includes setup/TestMain |
| Suite | cached/fresh advisory 5 s; release enforced 30 s; optimization target 5 s |
| Package | 10 s; existing cmd/scenery override 15 s |
| Cache/scheduling | fresh bodies count=1, build-ID linked cache, six packages/four builds, no cached results |
| Cold bounds | <=60 test binaries; prepare strictly below 30 s when every test binary rebuilt |
| Failure class | fresh confirmed violation warning; release/all violation error; baseline missing samples/command failure is warning (mandatory final confirmation must fail closed) |
| Quantization | baseline rounds each sample and the selected p95 to 1 ms before comparison; preserve its conservative rejection while retaining raw evidence |
| Exceptions | serialized empty array; any entry rejected |
| Discrepancy | baseline uses shared same-package count=20; required final proof uses separate serial processes |

Capture exact source/schema hashes and remaining skip/rounding rules before M0
closure. Unrounded threshold behavior must be tested rather than inferred from
display fields. Preserve native platform identity and cache preparation evidence.

## Milestones

### M0 — Evidence and deletion map

Record exact baseline SHA/dirty state, current owners, dependency/test/mode/report
inventories and assertion-preservation map. Map webhook and auth/durable fixture
fields as requirement, supply, setup, identity or observed allocation. Preserve
canonical resource/module-instance identities and lifecycle distinctions.
Audit disposable isolation before baseline runtime execution; a private agent
home alone never authorizes globally named container mutation. Missing safe
isolation blocks that proof, not permission to run it against shared state.
M0 closes only with no unowned migration item and concrete acceptance commands.

### M1 — Repository verification owner

Implement `scripts/verify` with optional `--repo-root`, mutually exclusive
`--quick|--race|--release`, `--fresh-tests`, `--summary`, `--write`, and
`-o human|json`, preserving useful mode semantics and current machine envelope,
report identities, diagnostics, bounds and reproduction commands. It builds
from a fresh source checkout without generated app contracts or a running agent.
Keep one selected invocation's writer for agent-context and timing evidence.
Read-only product inspection must not import testsuite or execution orchestration.

Private cmd/scenery probe calls must become real product CLI/runtime assertions
or call genuine shared production owners. Never copy production behavior into
the verifier, export test hooks, or hide a release dispatcher in the product.
Keep fast ordinary tests under their appropriate owner; map renamed roots.
Verify embedded-dashboard freshness through the product binary under test,
not the verifier's own embed. Prove stale-product/fresh-verifier rejection and
matched-product acceptance through a real boundary.

### M2 — Parity and singular cutover

Before removal compare old/candidate verification against equivalent source,
binary/environment/freshness inputs; compare assertions, diagnostics and scope,
not timestamps/temp names. Temporary dual paths remain unshipped. Replay timing
decisions below/at/above threshold, allowed outlier ranks, missing/truncated
samples, stale baselines, cached output and forbidden exceptions. Separately
remeasure actual new/moved/changed roots in 20 serial processes each.

Cut CI, all active-plan/instruction/docs consumers, help/schema/command
recommendations and release shell over together. Remove harness self dispatch
with normal invalid-request rejection and no forwarding alias. Keep app harness,
UI harness and bounded readers. Separate common suites from unique shell
packaging/artifact/clean-source/binary probes; no verifier-to-shell recursion,
cross-run pass cache or duplicate execution with identical semantics.
Closure: product dependencies exclude repository execution and testsuite; ordinary
app commands work without verifier, test sources or test cache installed.

### M3 — One SQL requirements interpretation

After the recorded stable runtime handoff, derive the smallest immutable typed
projection from one canonical graph snapshot plus selected program/environment.
Use existing reachability rules, canonical addresses, resolved module instances,
SQL capability identity, logical database/schema mapping and declaration
provenance. Include framework-only auth/durable SQL through current canonical
projection; do not eagerly require every unused declared capability.

Generation, startup, checks, setup/seed selection and inspection consume the same
interpretation. Separate invocations may compile separately; no global cache.
Remove duplicate dev.services requirements only after recording the source of
every distinct supported supply/setup choice. Removed fields fail actionably,
not through old-reader fallback. Keep different schemas, shared bindings and
multiple module instances distinct. External lifecycle/DATABASE_URL never
authorizes create/delete/rename or a false worktree-isolation claim.

Read-only inspection must not provision/open databases. Down/snapshot/cleanup
must still use verified actual allocations when source is invalid, requirements
were removed or environment changed. Prior success is not current compile proof.
Keep explicit schema/setup/seed semantics and storage sharing intact.

### M4 — Integrated evidence and measured removals

Run the final cumulative validation union. Compare product dependencies,
execution paths, report writers, duplicate requirement sites, package/test/root
inventories and assertion evidence. Every removed assertion needs replacement
proof or explicit developer approval of feature removal. Record actual deleted
mechanisms separately from moved code. Binary size/wall times are secondary.
Finish living docs/schema/CI contracts without editing completed numbered plans.

## Plan of Work

M1/M2 form one coordinated verifier cutover; M3 then consumes the runtime
resolver. Use small responsibility-based cuts, not simultaneous competing edits.
Extract data-only types only for real product readers; keep one pure path
classification owner, one timing calculator, one scheduler/cache and one selected
report writer. No compatibility branch survives the cutover.

The shell currently uses disposable `go build`, not the historical proposal's
shared install; preserve the actual binary-specific source/packaging assertions.
The owner of each retained report and unique shell check must be named in M0.

## Concrete Steps

From repository root, capture `git status --short`, `git rev-parse HEAD`,
`git worktree list --porcelain`, `go list -deps ./cmd/scenery`,
`go list -json ./...`, and `go test -list '^Test' ./...` beneath baseline/.
Search all live harness self/testsuite consumers and DatabaseServices/dev.services
interpretations. Inventory nested go.mod files separately.

Before cutover:

```sh
./scripts/build-dashboard-ui-embed.sh
go build -o .scenery/harness/bin/scenery ./cmd/scenery
.scenery/harness/bin/scenery harness self --quick --summary --write
.scenery/harness/bin/scenery harness self --release --fresh-tests --summary --write
SCENERY_BIN="$PWD/.scenery/harness/bin/scenery" scripts/release-gate.sh
```

Retain baseline artifacts before later latest writers. Successful release/fresh
supersedes default scope; initial quick still provides changed-area handoff.
Inspect safe fixture ownership before starting runtime lanes. Never use installed
PATH scenery as the implicit product under test.

After M1:

```sh
go build -o .scenery/harness/bin/verify ./scripts/verify
go test ./scripts/verify
go test ./internal/testsuite
go run ./scripts/testsuite -run 'a^' -record-timings=false
go run ./scripts/testsuite
.scenery/harness/bin/verify --quick --summary --write
.scenery/harness/bin/verify --release --fresh-tests --summary --write
```

Add fixed release steps `core responsibility separation` and `capability
authority`, with individual assertion/skip evidence. They own disposable source-only
basic/webhook/auth/durable/module-instance fixtures, candidate framework inputs,
returned route manifests, typed HTTP/client calls and cleanup. Fixture product
commands are `generate -o json`, `check -o json`, `go test ./...`,
`harness -o json --write`, `up --detach -o json`, `ps -o json`, `db list -o json`,
and `down -o json`, all with absolute candidate CLI. Snapshot save/verify/load
uses current exact documented commands and the runner's own stopped target;
record identities and no mutation outside the owned fixture.

Search removed grammar/symbols after cutover. Classify hits as immutable history,
negative tests or a blocker (live consumer). Recreate final dependency/package/
root inventories under final/ and map every rename.

## Validation and Acceptance

Expected classes: go-package, cli-json-contract, release-sensitive-or-runtime,
compiler-or-generator and documentation-only. Current oracle spelling and its
cumulative recommended-command union are authoritative. Add commands for any
additional changed package before that milestone's execution.

From repository root:

```sh
go test ./cmd/scenery
go test ./internal/testsuite
go test ./internal/app
go test ./internal/spec
go test ./internal/compiler ./internal/parse
go test ./internal/generate
go test ./internal/build
go test ./runtime ./db ./internal/postgresdb ./internal/doctor ./internal/inspect ./internal/codegen
go test ./internal/agent ./internal/edge
go test ./internal/devdash ./internal/devprocess ./internal/repoinfo ./internal/watchignore ./internal/machine
go test ./scripts/verify
go test ./...
go test -race ./...
golangci-lint run ./...
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go build -o .scenery/harness/bin/verify ./scripts/verify
.scenery/harness/bin/verify --quick --summary --write
.scenery/harness/bin/verify --release --fresh-tests --summary --write
scripts/release-gate.sh
bash -n scripts/release-gate.sh
git diff --check
```

Final release/fresh supersedes the default `verify --summary --write` scope only
when successful; quick provides changed-area handoff. Removed test packages need
explicit replacement mappings, never a silent omitted command.

The M3 changed-area oracle selects `cli-json-contract`, `compiler-or-generator`,
`go-package` and `release-sensitive-or-runtime`. Although it does not select the
dashboard path class, actual resolved SQL metadata is browser-visible, so the
conditional console lint/typecheck/build, product `harness ui` and Chrome SQL
view proof apply. Use only this plan's owned disposable webhook/basic copies.

Console standalone group is mandatory if oracle selects dashboard OR console
source/routing/RPC/browser behavior changes: in apps/console run `bun run lint`,
`bun run typecheck`, `bun run build`; at root run embed script, build the local
product and `scenery harness ui -o json --write` with its owned disposable target.
Otherwise record paths/oracle proving this standalone group inapplicable. Retained
verifier console build/typecheck/freshness remains mandatory regardless.

| ID | Mandatory evidence |
| --- | --- |
| A1 | cached/fresh/race/release scopes and all assertions preserved; nested/no-test/moved roots mapped |
| A2 | policy replay parity, forbidden/unusable evidence rejected, actual changed-root 20-process p95 <100 ms |
| A3 | product excludes verifier execution/testsuite; removed grammar rejected; app validation/readers work |
| A4 | source-only verifier build, exact product identity, stale-product rejection, valid current reports |
| A5 | no recursive/equivalent duplicate orchestration; unique clean-source/artifact/binary checks retained |
| A6 | no-SQL generate/check/inspect/console startup creates no SQL resource/process |
| A7 | managed SQL without duplicate list yields correct typed capability/schema and HTTP/client result |
| A8 | auth/durable-only, multiple module instances, shared bindings, distinct schemas, invalid binding pre-mutation rejection |
| A9 | external DATABASE_URL remains external; inspection does not provision/open resources |
| A10 | source-invalid/removed/changed-environment down/snapshot/cleanup preserves verified allocation ownership |
| A11 | existing generated-Go/worktree acceptance passes; no synthetic editor/global DB fallback |
| A12 | one live command/owner across docs/schema/CI/active plans; completed plans unchanged; measured deletion ledger |

Native timing records host/toolchain/binary/cache/command identities and raw
samples. Measure every new/moved/materially changed exact root in 20 separate
serial processes, never one count=20 process. Deliberately slow enforcement
helpers exist only in disposable release fixtures. Keep every supported module's
roots and no-test packages. Missing native/Docker/browser capability leaves the
mandatory milestone open; missing evidence is not a green result.

Collect at least three alternating same-host baseline/candidate cached and
manifest-hit fresh workflow observations with equivalent scope/cache conditions.
Keep raw wall/preparation attribution, do not flush shared caches or tune
parallelism. Report observations without inventing a pass from mean/median or
claiming relocation alone reduced total source/time.

The optional external-app shell check is skipped only when
SCENERY_RELEASE_GATE_EXTERNAL_APP_ROOT is unset; a live selected app gets only
the existing read-only check. This never excuses plan-owned disposable proof.

## Idempotence and Recovery

Use owned disposable roots and recorded process/container/volume identities.
Stop verified resources before deleting files. No blanket Docker prune, shared
agent restart, existing app restart, shared install or implicit SQL copy.
Preserve old/candidate evidence and input identities before report cutover.
Rollback is a targeted reviewed source change, never a destructive reset or
permanent fallback. Existing allocation records survive failed compilation;
removing source requirements never implies deleting stored data.

## Artifacts and Notes

All raw machine-local artifacts remain ignored beneath
`.scenery/harness/core-simplification/{baseline,final}/` and focused subfolders.
Keep a concise assertion map, capability fact table and deletion ledger here,
not a new maintained architecture/complexity registry. Every deletion names
removed mechanism, surviving owner and protecting evidence. Do not commit
credentials, SQL data, cache, generated app Go or machine outputs.

### Responsibility deletion ledger

These are mechanism removals, not claims that moved lines disappeared. Final
dependency/root counts and workflow measurements are recorded in Outcomes above
and in `final/deletion-counts.json`, `final/root-map.json`,
`final/assertion-map.json` and `final/workflow-cost-last-final/report.json`.

| Removed mechanism | Surviving owner | Protecting evidence |
| --- | --- | --- |
| Product execution of repository verification, timing and release journeys | Unshipped `scripts/verify`; unchanged `internal/testsuite` execution engine | Core-boundary probe, 917-anchor assertion map, complete cutover release |
| Duplicate release-shell orchestration of repository checks | One verifier invocation; shell retains source-snapshot/packaging assertions | Cutover shell report and frozen worktree probe |
| CLI-local process/lock/session-cleanup copies needed by verifier | `internal/devprocess`, `internal/edge`, `internal/agent` | Mapped in-process roots and named real-process release journeys |
| CLI-local repository classification and report-value copies | `internal/repoinfo`, `internal/harnessevidence`, `internal/machine` | Policy replay, schema checks and bounded product readers |
| Config `dev.services` requirement list and generator dependency interpretation | Immutable compiler SQL requirement projection and compiler-owned native selectors | Compiler requirement/schema tests; generated entrypoint tests; capability authority probe |
| Independent runtime/setup/default-pool SQL binding inference | Current supply resolver and runtime binding installation before constructors | SQL supply/runtime/db tests and managed/external webhook journeys |
| Dashboard reconstruction of database identity from declarations | Resolved non-secret runtime metadata published through the existing control-plane writer | Metadata publication test; Chrome table preview and read-only schema query |
| Four duplicate inspection payload revisions and ignored descriptor argument | Existing singular `internal/machine` payload identity owner | CLI inspection/schema suite |

Retained allocation records still own cleanup and recovery when source is invalid
or absent. No snapshot, ownership, external-DSN protection, runtime feature or
timing-budget exception was removed.

## Interfaces and Dependencies

`scripts/verify` owns repository orchestration/policy/release proof and uses the
existing internal/testsuite. Product CLI retains app compilation, generation,
runtime, harness/UI and bounded inspection but no verifier dependency. Shared
machine/report values are narrow data-only leaves where actual readers require
them. Requirements are immutable canonical facts; environment supply and
persisted observed allocation remain separate. Prefer stdlib and existing
packages, with no new scheduler/provider/plugin/general context abstraction.
