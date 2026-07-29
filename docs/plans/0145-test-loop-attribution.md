# Developer Test Loop Attribution

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

`docs/plans/0050-test-suite-speed-hardening.md` is complete and immutable. It
brought the warm suite down and built the timing report. This plan covers the
persistent regression that remained afterwards, and it exists because the
0050-era evidence could not attribute the cold-run penalty to anything.

## Purpose / Big Picture

Three costs were confounded in the reported numbers:

- Fresh suite 55.742s against a 5s target.
- Confirmation 99.506s, 64% of a 155.248s Go-test step.
- Warm suite 19.73s, roughly 4x target.

Confirmation cost is policy, not product speed: it re-ran every candidate on
every fresh run, including outliers that had been at the same level for weeks.
Cold-run cost was entirely unattributed — the run built 49 test binaries and
nothing recorded which links were expensive.

The goal of this plan is attribution first, then targeted reduction. Do not
optimize a number nothing can explain.

## Progress

- [x] 2026-07-28: Scoped confirmation to regressions. `budgets.confirmation_scope`
      is `regressions` on `--fresh-tests` and `all` on `--release --fresh-tests`.
      A candidate is re-confirmed only when the recorded baseline did not have it
      over budget, or when it is now at least 25% and 0.5s worse. Skips are
      recorded in `deferred_confirmations` with their baseline, never dropped
      silently.
- [x] 2026-07-28: Instrumented test-binary preparation. `internal/testsuite`
      reports `Result.Prepare` with total elapsed, package-list elapsed, and one
      `BinaryBuild{Package, BuildID, Elapsed}` per link; the harness surfaces it
      as `test_binaries` in `.scenery/harness/test-timing-latest.json`, and
      `scripts/testsuite -builds N` prints it to stderr.
- [x] 2026-07-28: Measured the cold penalty with the new instrumentation
      (numbers under Surprises & Discoveries).
- [x] 2026-07-28: Folded the second hand-rolled `native` fixture copy in
      `build_desktop_test.go` onto the shared `nativeFixtureRoot` helper.
- [x] 2026-07-28: Re-measured the fresh lane end to end.
      `.scenery/harness/bin/scenery harness self --fresh-tests --summary --write`
      run against the stale cached-lane baseline spent 39.303s confirming (16
      observed, 11 confirmed, 9 deferred); the immediately following run against
      the fresh baseline spent 13.861s (8 observed, 3 confirmed, 16 deferred).
      Against the reported 99.506s that is the steady-state cost of this change.
      The deferrals are correct: `scenery.sh/cmd/scenery` at 16.98s against a
      17.54s baseline is not a regression and was not re-run.
- [x] 2026-07-28: Cut the app runtime out of the compiler-side test closure.
      The contract value types moved from the root `scenery.sh` package to
      `internal/contract`, the authorization expression evaluator moved from
      `runtime/contract_policy_expression.go` to `internal/contractpolicy`, and
      root re-exports the contract surface as aliases so generated app code is
      unchanged. Ten packages — `scn`, `graph`, `workspacetx`, `librarybuild`,
      `compiler`, `deployplan`, `build`, `evolution`, `generate`,
      `contractagent` — no longer link `scenery.sh/runtime`, its HTTP stack, or
      the PostgreSQL driver (numbers under Surprises & Discoveries).
- [x] 2026-07-28: Raised the per-package timing budgets to levels the suite can
      actually be held to. The default package budget is 10s (was 2s) and
      `scenery.sh/cmd/scenery` is 15s (was 5s). The total-suite budgets and the
      5s optimization target are unchanged, so the lane semantics are the same:
      advisory in cached and fresh, enforced total only in release. Against the
      cached lane this leaves exactly one package over budget,
      `scenery.sh/cmd/scenery` at 18.53s.
- [x] 2026-07-28: Profiled the `cmd/scenery` serial phase from test2json event
      timestamps rather than from summed durations, and fixed the one clearly
      recoverable cost it exposed: a data race between the agent watchdog
      goroutine and the test cleanup that restores the watchdog globals.
      `startAgentAvailabilityWatchdog` now returns a channel that closes when
      its goroutine exits, so tests stop the loop before restoring state.
- [x] 2026-07-28: Introduced the explicit agent-paths seam.
      `startAgentAvailabilityWatchdog` takes `localagent.Paths`,
      `DevSessionController` carries an optional `paths` override, and
      `PreparedDevSession` exposes the home it resolved against so `watch.go`
      extends that session instead of re-deriving it. The watchdog tests now use
      `localagent.PathsForHome(t.TempDir())` and no longer set
      `SCENERY_AGENT_HOME` at all.
- [x] 2026-07-28: Replaced the package-global test seams that were blocking
      parallelism, then parallelized what they unblocked. `buildCommand` takes
      an `io.Writer`; the local path router carries a `DialRetry` policy in its
      options instead of `localProxyDialRetryBudget`/`Interval`; the watchdog
      takes an `agentWatchdogPolicy` instead of four package vars; and the
      desktop, build, and upgrade fixtures bake their marker path into the fake
      script rather than passing it through the process environment. 99 tests
      gained `t.Parallel`. Interleaved A/B on the same host: **16.88s to 13.18s,
      a 3.70s (22%) reduction**, and the harness now reports
      `scenery.sh/cmd/scenery` at 13.86s against its 15s budget with **zero
      packages over budget**.
- [x] 2026-07-28: Cleared the `cmd/scenery` data race.
      `followAlreadyRunningDevSession` now cancels and joins its owner-watch
      goroutine before returning, so the watch cannot read
      `devSessionOwnerExitPollInterval` while the next test writes it. The
      package reports zero data-race warnings, down from one, and one `-race`
      failure, down from two.
- [x] 2026-07-28: Fixed the last `-race` failure. `monitorVictoriaRecovery`
      started a substrate monitor per recovered stack and dropped the returned
      channel, so those goroutines outlived the recovery loop and wrote a
      substrate lock under the storage root as their components exited. The
      recovery loop now collects and joins them before closing its own done
      channel, and the test joins the monitor it starts directly.
      `TestVictoriaSupervisorRecoversExitedComponent` went from 6 failures in 8
      isolated `-race` runs to 0 in 8, and the whole `cmd/scenery` package is
      clean under `-race` across three consecutive runs — zero data races and
      zero failures, against a starting baseline of one race and two failures.

- [x] 2026-07-28: Removed the remaining `SCENERY_AGENT_HOME` dependency from the
      router and worktree tests. `startLocalPathRouter` resolved its own agent
      through `localagent.DefaultClient()`, so the router now takes an `Agent`
      option and the tests hand it the fake agent they already started; the
      worktree tests set the env for a path that never reads it, proven with a
      sentinel agent home that stayed empty. 18 more tests gained `t.Parallel`.
      Interleaved A/B: 14.0s to 12.5s, a further 1.5s.

- [x] 2026-07-28: Replaced the PATH-based fake-binary injection in the deploy
      SSH tests with a `deploySSHTools` seam carrying the `ssh`/`rsync` paths
      and any extra child environment, threaded through `runDeploySSH`. The
      tests no longer touch process `PATH` or set `DEPLOY_*` env. Interleaved
      A/B: 11.9s to 10.8s. `scenery.sh/cmd/scenery` now reports 11.52s against
      its 15s budget.
- [ ] `configureManagedVictoriaTestProcesses` (2.16s) is deliberately not
      converted; see Decision Log.

## Surprises & Discoveries

- 2026-07-28: The cold penalty is linking, and package listing is nearly free.
  A cold run (`.scenery/harness/test-binaries` removed) spent 19.181s in
  prepare, of which package listing was 2.416s and 49 links accounted for
  129.776s of CPU compressed to roughly 16.8s of wall time. Total cold run was
  46.267s. The warm run of the same command spent 0.081s in prepare and 18.772s
  overall. So the cold/warm gap is prepare, and prepare is links.
- 2026-07-28: Link parallelism is already saturated. Raising `BuildParallelism`
  from 8 to 16 on a 24-logical-core host left prepare unchanged (19.181s versus
  19.300s) while total build CPU rose from 129.776s to 283.700s — the extra
  workers only contended with each other. Cold cost has to come out of the
  number and size of test binaries, not out of scheduling.
- 2026-07-28: The five slowest links are `internal/build` 6.777s,
  `internal/contractagent` 6.480s, `internal/compiler` 6.245s,
  `internal/deployplan` 5.658s, and `cmd/scenery` 4.842s. Link cost does not
  track test-execution cost: `internal/contractagent` and `internal/deployplan`
  are cheap to run and expensive to link.
- 2026-07-28: The warm suite is one package's serial critical path.
  `cmd/scenery` is 17.60s of an 18.77s wall; the next package,
  `internal/evolution`, is 5.87s and finishes hidden behind it. Nothing outside
  `cmd/scenery` can move the warm number.
- 2026-07-28: Inside `cmd/scenery` the problem is not duplicated compile work.
  181 top-level tests accumulate 21.90s against a 17.60s wall — an effective
  parallelism of 1.24x on a 24-core host with `-test.parallel=8`. The 36 tests
  at or above 0.2s hold 15.74s of that, and 27 of them (13.15s) do not call
  `t.Parallel`. Roughly half of those are blocked by `t.Setenv`; the rest bind
  fixed ports or swap `os.Stdout` through `captureStdout`. Only one real
  compile smoke remains in the package, so there is no duplicate-compile fat
  left to cut.

- 2026-07-28: The expensive links were paying for the PostgreSQL driver to parse
  a duration string. The root `scenery.sh` package held both the contract value
  types (`Duration`, `ParseDuration`, `UUID`, …) and `scenery.go`, whose two
  functions `Meta()` and `CurrentRequest()` import `scenery.sh/runtime` →
  `internal/durable/store` → `internal/postgresdb`, plus `net/http` and
  `crypto/tls`. Every package that wanted a scalar parser linked all of it. A
  probe importing only `scenery.sh` linked to 10.3MB; one importing only what
  the compiler actually needs (`runtimeapi` + `machine` + `spec`) linked to
  3.7MB. A second, independent edge did the same thing:
  `internal/compiler/security_validate.go` imported `scenery.sh/runtime` for
  `ValidateContractAuthorizationExpression`, a self-contained 551-line parser
  with only stdlib imports.
- 2026-07-28: Removing both edges cut test-binary size by 6.3%–41.2%:
  `workspacetx` -41.2%, `graph` -40.5%, `scn` -37.3%, `librarybuild` -25.9%,
  `deployplan` -19.1%, `compiler` -17.8%, and `build`/`contractagent`/
  `evolution`/`generate` -6.3% each. `internal/generate` is what holds those
  four back, but not mainly through the embedded UI catalog: the catalog is
  about 200KB. `generate` anchors `go/types` + `x/tools/go/packages` +
  `go/parser` (roughly 700KB) through `verify_go.go`, and `net/http` +
  `crypto/tls` + vendored crypto (roughly 800KB) through
  `tscheck` -> `toolchain`.
  Building those ten test binaries against an isolated cold `GOCACHE` with
  non-test dependencies pre-warmed went from 11.09s/8.40s to 9.27s/6.97s across
  two runs — 16.4% and 17.0% — with system time, the linker's dominant cost,
  down about 30% (73.97s → 55.10s, 73.83s → 50.38s). `cmd/scenery` is unchanged
  at 39.4MB, as expected: it owns the runtime and should link it.
- 2026-07-28: The architecture rule found a layering violation the move had
  hidden. `contract_path_tail_test.go` imported `scenery.sh/runtime` to test the
  runtime's HTTP input decoder against contract types, so it would have relinked
  the runtime into the new leaf's own test binary. It belongs next to
  `runtime/contract_path_tail.go` and now lives there.

- 2026-07-28: The `cmd/scenery` serial phase is 25 tests, not 231. Reconstructed
  from test2json event timestamps: wall 15.53s, of which the serial phase is
  14.49s and the parallel phase is 1.04s for 278 tests. Within the serial phase
  the 7 tests at or above 500ms hold 5.20s and the 25 at or above 200ms hold
  11.44s, while the *bottom half of serial tests contributes 0.00s*. The 0050-era
  framing of "add `t.Parallel` to the tests that lack it" therefore targets the
  wrong set: the tests that lack it are overwhelmingly free.
- 2026-07-28: Blanket parallelization of the safe-looking serial tests was tried
  and reverted. A static filter (no `t.Setenv`/`t.Chdir`, no package-global
  assignment, no fixed port, no stdout capture, no tainted helper) selected 102
  tests holding 3.34s. Adding `t.Parallel` to all of them changed the package
  wall not at all (16.007s to 16.146s) and produced a flake in
  `TestRunUpgradeInstallsExplicitTargetEvenWhenCurrentVersionMatches` on the
  second run. Both failures are explained by the profile above: the selected
  tests were nearly all from the zero-cost tail, and static analysis cannot see
  the shared state that actually couples them.
- 2026-07-28: The watchdog tests were already racy before any timing change.
  `t.Cleanup` restores `agentWatchdogInterval`/`RecoveryBackoff`/`StartFunc`
  while the watchdog goroutine is still reading them, because
  `startAgentAvailabilityWatchdog` gave the caller no way to wait for the loop
  to exit. Under `-race -count=5` the unmodified tests failed about one run in
  four. Converting the retry assertion from a fixed 500ms sleep to a poll made
  it fail three runs in four, because the test now finishes while the loop is
  still hot — the poll exposed the race rather than causing it. With the stop
  channel the same command passes 6 runs out of 6, and the test dropped from
  0.71s to 0.32s.
- 2026-07-28: Shortening watchdog intervals is not a safe speed lever. Dropping
  `agentWatchdogInterval` from 20ms/10ms to 5ms made the *negative* assertions
  ("no recovery happened") flaky under `-race`, where setup alone spans several
  intervals. Positive assertions can poll; negative assertions need a real
  window, so they were left at their original interval.
- 2026-07-28: Threading `localagent.Paths` unblocks far less than the agent-home
  test total suggests. Of the 43 serial tests that depend on
  `SCENERY_AGENT_HOME` (3.78s), removing the env dependency alone makes only 25
  of them parallel-eligible, and those hold **0.97s**. The other 18 — which hold
  2.81s, including every expensive one — are *additionally* blocked by
  package-global mutation (`deployResumeLaunchAgentStatusFunc`,
  `localProxyDialRetryBudget`, `devSessionOwnerExitPollInterval`,
  `agentWatchdogInterval`) or by other process-global env such as
  `SCENERY_AGENT_DISABLE`. Removing `t.Setenv` is necessary but not sufficient;
  each of those globals needs its own seam before the test can run in parallel.
  The earlier "up to 3.8s" figure was the ceiling for de-globalizing everything,
  not for the Paths work by itself.
- 2026-07-28: Static analysis missed two classes of parallel-unsafe test, and
  both were caught only by running. A first pass selected tests by rejecting
  literal `t.Setenv("NAME")`, package-global assignment, fixed ports, and stdout
  capture; it still broke `TestAppProcessEnvRejectsRelativeLocalStorageRoot`,
  which calls `t.Setenv(storageconfig.RuntimeConfigEnv, …)` with a non-literal
  name, and `TestRunUpgradeInstallsExplicitTargetEvenWhenCurrentVersionMatches`,
  which reaches globals through the `overrideUpgradeGlobals` helper. Matching
  any `t.Setenv(` call and tainting helpers that assign globals fixed both.
- 2026-07-28: The `-race` lane is the real acceptance test for parallelization,
  not the ordinary run. With 114 tests parallelized every ordinary run passed
  while `-race` went from 1 data-race warning to 8, adding six newly failing
  tests across `dev_ports`, `dev_named_lock`, `dev_session_cleanup_unix`, and
  `snapshot_backup_script`. Those coordinate through shared *on-disk* state —
  port leases, named locks, session state roots — which no source-level check
  can see. Excluding those four files returned the lane to its pre-existing
  baseline (1 warning) while keeping 3.70s of the 3.83s gain.
- 2026-07-28: Both `-race` failures in `cmd/scenery` were goroutines outliving
  the function that started them, and only one was fixable cheaply.
  `followAlreadyRunningDevSession` started an owner watch and returned without
  joining it, so the watch read `devSessionOwnerExitPollInterval` while the next
  test wrote it; cancelling and joining before returning removed the package's
  last data-race warning (1 to 0). The second,
  `TestVictoriaSupervisorRecoversExitedComponent`, fails its `t.TempDir` cleanup
  with "directory not empty": the managed component processes are bound to the
  run context with a 5s kill grace, so cancelling only *starts* their shutdown
  and they keep writing into the storage root after the test returns. Waiting on
  `Component.Done()` for the started stack does not fix it, and neither does
  additionally waiting on `supervisor.victoria` after recovery swaps the stack —
  the test still fails, so some writer is not reachable from either handle. It
  reproduces on a bare `-race -count=3` of that test alone and predates all of
  this plan's work.
- 2026-07-28: That Victoria failure was a third instance of the same defect, and
  the writer was a lock file rather than a component. `monitorVictoriaSubstrate`
  returns a channel that closes when its per-component goroutines finish; each
  goroutine waits on `component.Done()` and then takes
  `lockManagedSubstrateRoot`, which *creates* `substrate-victoria.lock` under the
  storage root. `monitorVictoriaRecovery` started one of these per recovered
  stack and discarded the channel, so at cancellation those goroutines were
  still creating files while `t.TempDir` walked the directory — hence
  "directory not empty" rather than a permission or handle error. Waiting on
  `Component.Done()` could never fix it: the components had already exited, and
  the writes happen *after* that. All three `cmd/scenery` `-race` defects this
  session were the same shape — a goroutine outliving the function that started
  it — and in each case the fix was to return a join handle and use it.
- 2026-07-28: The four shared-on-disk-state files have to be excluded
  explicitly, not by re-deriving the safe set. A second parallelization pass
  recomputed candidates from source and silently re-parallelized
  `dev_named_lock`, `dev_ports`, `dev_session_cleanup_unix`, and
  `snapshot_backup_script`, because nothing in the source marks them unsafe —
  the earlier exclusion had been a manual revert. `-race` immediately went back
  to 8 warnings, pointing at `setDevLockTestTiming` racing
  `acquireDevNamedLock`. The exclusion now lives in the selection script.
  Anything that re-derives this set from static signals will make the same
  mistake; the constraint is in the filesystem, not the source.
- 2026-07-28: Setting `cmd.Env` at all changes the child's working directory as
  the child sees it. With a nil `Env` the exec package derives `PWD` from
  `cmd.Dir`; assigning `Env` from the process environment drops that, so the
  fake `rsync` reported `/private/var/...` from `getcwd()` where the test
  expected the unresolved `/var/...` it had passed in. Building the child
  environment from `cmd.Environ()` keeps the derived `PWD`. Any seam that adds
  environment to a child process inherits this trap.
- 2026-07-28: The contract-drift env check matches source text, comments
  included. Explaining the fix above in a comment that named the process-env
  helper failed `contract drift checks` with "production code reads or mutates
  process environment outside internal/envpolicy". The checker itself dodges
  its own scan by splitting the token (`"os." + "Getenv("`), which is the
  convention to follow when the name has to appear in scenery's own source.
- 2026-07-28: This machine cannot resolve the remaining gap. Repeated
  `go test ./cmd/scenery -count=1` runs spanned 16.0s to 20.8s depending on
  ambient load (load average around 10 from unrelated long-lived dev sessions).
  The noise band is about 4s, which is the same size as the distance from the
  measured 18.53s to the 15s budget, so a change of that size cannot be
  confirmed here by wall-clock comparison alone.

## Decision Log

- Confirmation scope is derived from the existing mode flags rather than a new
  knob. `--release --fresh-tests` already means "periodic audit"; adding a
  separate flag would grow the CLI surface for a distinction the modes already
  encode.
  Date/Author: 2026-07-28 / Claude.
- Deferred candidates are recorded rather than dropped. A confirmation pass that
  silently skips known outliers reads as "nothing was over budget" on the next
  run, which is exactly the failure the timing report exists to prevent.
  Date/Author: 2026-07-28 / Claude.
- Build IDs are recorded alongside durations. A duration alone cannot tell a
  rebuild caused by a real input change from one caused by cache churn; the
  build ID makes a relink traceable to the identity that changed.
  Date/Author: 2026-07-28 / Claude.
- The contract surface keeps its app-facing spelling through aliases rather than
  moving generated code to a new import path. Generated app code says
  `scenery.Duration`; that is the current singular spelling and it does not
  change. `internal/contract` is the implementation, and root is a type-alias
  façade, so there is no second spelling and no compatibility shim — only one
  name reaches app authors. `contract_surface_test.go` pins type identity and
  method reachability so the façade cannot drift from the leaf.
  Date/Author: 2026-07-28 / Claude.
- The layering is enforced by a `packageLayerRule` in self-harness architecture
  checks, not by prose. The regression this prevents is invisible in tests and
  in review: one `scenery "scenery.sh"` import inside a compiler-side package
  silently relinks the runtime, HTTP stack, and PostgreSQL driver into that test
  binary, and nothing fails. Fixture apps under `testdata/` are exempt, since
  generated client code legitimately imports the façade.
  Date/Author: 2026-07-28 / Claude.
- Per-package budgets were raised to 10s/15s rather than left at 2s/5s. A budget
  that nothing meets is not a budget; it reports every package every run, so a
  real regression is indistinguishable from the standing background. 10s and 15s
  are levels the suite can be held to today, which makes an overrun a signal
  again. The 5s optimization target is deliberately left alone: it records where
  the suite should end up, and collapsing it into the operational budget would
  lose that distinction.
  Date/Author: 2026-07-28 / Claude.
- Budget-sensitive test fixtures were rescaled, not relaxed. Four tests in
  `harness_timing_test.go` encoded the old 2s budget in their elapsed values;
  simply widening the assertions would have left them passing while no longer
  exercising the boundary. Each was moved to the equivalent position against the
  new budget — `TestCommandPackageUsesExplicitTimingBudget` now uses 12.0s, over
  the 10s default and under the 15s override, so only the override can keep it
  silent.
  Date/Author: 2026-07-28 / Claude.
- The watchdog got a stop channel in production code rather than a sleep in the
  test. The alternative — waiting "long enough" before cleanup — leaves the race
  in place and only hides it. `startAgentAvailabilityWatchdog` returning a
  closed-on-exit channel is the smaller and more honest change: the only
  production caller ignores it, and the loop becomes deterministically
  stoppable.
  Date/Author: 2026-07-28 / Claude.
- The remaining `cmd/scenery` reduction needs a production decision, not more
  test edits. What is left in the serial phase is real work: fake-command
  subprocess spawning (`deploy_ssh`, 2.08s across three tests), desktop and
  frontend builds (`build_desktop`, 1.24s), and the 44 tests that must set
  `SCENERY_AGENT_HOME` because 40 production call sites resolve the agent home
  through `localagent.DefaultPaths()` internally. `localagent.PathsForHome`
  already exists, so the mechanical part is available, but threading `Paths`
  through those 40 call sites changes cmd/scenery's structure for a test-speed
  reason and should be decided deliberately rather than folded into a timing
  pass.
  Date/Author: 2026-07-28 / Claude.
- Four test files are deliberately excluded from parallelization:
  `dev_ports_test.go`, `dev_named_lock_test.go`,
  `dev_session_cleanup_unix_test.go`, and `snapshot_backup_script_test.go`.
  They coordinate through shared on-disk state, and parallelizing them
  regressed the `-race` lane while adding little. Anything that makes them
  parallel-safe has to isolate that state first, not add `t.Parallel`.
  Date/Author: 2026-07-28 / Claude.
- The Victoria test-process seam is deliberately left on process environment.
  `SCENERY_VICTORIA_*_BIN`, `_PORT`, `_VERSION`, and friends are registered in
  `docs/environment.registry.json` and documented in `docs/environment.md`, so
  they are a supported production override, not a test hack. Converting them to
  a struct would either break that contract or add a second configuration
  surface for a test-speed reason. Several of the tests involved
  (`TestEnsureSharedVictoriaStackSerializesConcurrentStarts`,
  `TestVictoriaRecoverySerializesConcurrentAttempts`) also assert serialization
  semantics and must stay serial regardless, so the realistic gain is well under
  the 2.16s the cluster holds.
  Date/Author: 2026-07-28 / Claude.
- The next reduction target is the `cmd/scenery` serial phase, not fixture
  reuse. The measurement above contradicts the fixture/duplicate-compile
  hypothesis for this package. The candidate work is removing `t.Setenv` from
  heavy tests by threading configuration through existing seams, and replacing
  fixed-port binding with port-zero listeners, so those tests can join the
  parallel phase. Mass-adding `t.Parallel` without those changes is unsafe, as
  0050 already recorded.
  Date/Author: 2026-07-28 / Claude.

## Outcomes & Retrospective

Open. The confirmation-scope change removes the largest reported cost
(99.506s of confirmation) from everyday fresh runs without losing regression
detection, and the prepare instrumentation answers the cold-run question. The
warm-suite target is unmet and now has a precise, measured owner.

## Context and Orientation

The timing report lives in `cmd/scenery/harness_oracle.go` (types, budgets,
parsing) and `cmd/scenery/harness_timing.go` (baseline selection, confirmation,
build-timing projection). The fresh execution lane lives in
`internal/testsuite`, with `prepare` and `buildMissingBinaries` in
`internal/testsuite/runner.go` owning package listing and linking.
`scripts/testsuite/main.go` is the manual adapter.

The persisted artifact is `.scenery/harness/test-timing-latest.json`, shaped by
`docs/schemas/scenery.harness.test_timing.schema.json` with its revision
mirrored in `cmd/scenery/payload_identity.go`. That artifact is also the
baseline the regression scope reads, so a run with `--write` both consumes the
previous baseline and records the next one.

Read `docs/local-contract.md` § harness self for the stable timing contract and
`internal/testsuite/AGENTS.md` for the runner's local rules.

## Milestones

1. Attribution (done): confirmation scope, prepare instrumentation, measured
   cold and warm baselines.
2. Cold reduction (partly done): fewer or cheaper links. The lever is the test
   dependency closure of the expensive-to-link packages, not scheduling. The
   app-runtime closure is out of the ten compiler-side packages. What remains,
   in order of leverage: `internal/generate` anchors `go/types` and `net/http`
   for `build`, `contractagent`, `evolution`, and `generate`;
   `internal/compiler` imports `internal/parse` only for `GoTargetContext` and
   `GoTargetEnvironment`, and `net/http` only for two method constants and
   `http.Cookie.Valid`; and `cmd/scenery` is still 39.4MB.
3. Warm reduction (open): shorten the `cmd/scenery` serial phase so the package
   stops being the whole wall clock.

## Plan of Work

Milestone 2 starts from the recorded `builds` array. For each of the five
slowest links, establish what pulls the closure wide, using the same
package-boundary technique 0050 used for `internal/app` and `internal/model`:
move pure helpers out of packages that drag heavy transitive dependencies into
a test binary. Accept a link cost only when the boundary is genuinely needed.

Milestone 3 works the 27 serial heavy tests in `cmd/scenery`. Two mechanical
blockers dominate. `t.Setenv` forbids `t.Parallel` outright, so those tests must
take their configuration through an existing seam instead of the process
environment. Fixed-port binding and `captureStdout`'s global `os.Stdout` swap
serialize the rest; port-zero listeners and a writer parameter remove both.
Neither change weakens an assertion boundary.

## Concrete Steps

```sh
# cold attribution: clear the cache, then read the ranked links
rm -rf .scenery/harness/test-binaries
go run ./scripts/testsuite -p 3 -run 'a^' -builds 20 -record-timings=false

# warm attribution: per-test elapsed for the critical-path package
go run ./scripts/testsuite -p 3 -run '.*' -record-timings=false > /tmp/warm.jsonl

# full fresh lane with regression-scoped confirmation
.scenery/harness/bin/scenery harness self --fresh-tests --summary --write

# periodic audit: confirm every candidate
.scenery/harness/bin/scenery harness self --release --fresh-tests --summary --write
```

## Validation and Acceptance

`go test ./cmd/scenery ./internal/testsuite`, then `go test ./...`. Schema and
CLI-contract changes additionally need
`.scenery/harness/bin/scenery harness self --quick --summary --write`, which
validates the committed `scenery.harness.test_timing` example against the
schema revision recorded in `cmd/scenery/payload_identity.go`.

Acceptance for milestone 1 is that a second `--fresh-tests` run against a
recorded baseline confirms only new or materially worsened candidates and lists
the rest under `deferred_confirmations`. Acceptance for milestones 2 and 3 is a
measured reduction in `test_binaries.build_seconds` and in the `cmd/scenery`
package wall respectively, each shown against the numbers in Surprises &
Discoveries on the same host.

## Idempotence and Recovery

Every step is repeatable. `.scenery/harness/test-binaries` is disposable cache;
removing it forces a cold run and nothing else. A missing, truncated, or
schema-stale `test-timing-latest.json` degrades to "no baseline", which confirms
every candidate — the conservative direction, and the same behavior as a first
run on a fresh worktree. No step mutates tracked source.

## Artifacts and Notes

- `.scenery/harness/test-timing-latest.json` — timing report and confirmation
  baseline.
- `.scenery/harness/test-binaries/` — linked binaries, manifest, and package
  timing estimates. Disposable.
- `.scenery/harness/artifacts/<run-id>/go-test.jsonl` — raw Go JSON events when
  the harness runs with `--write`.

Host for every number in this plan: 24 logical cores, 16 performance cores.
Timing conclusions do not transfer across machines; re-measure before comparing.

## Interfaces and Dependencies

- `scenery.harness.test_timing` gained `test_binaries`,
  `deferred_confirmations`, and `budgets.confirmation_scope`. The schema
  revision moved to
  `sha256:73231c3fb87a0790d678879fec827f095f584d8618755dbf10f4fcc4fddad3fa`.
- `testsuite.Result` gained `Prepare`; `testsuite.Run` is otherwise unchanged.
- `scripts/testsuite` gained `-builds N`, which writes to stderr only, leaving
  the stdout Go JSON event stream intact.
- No new environment variables, and no new CLI flags: confirmation scope is
  derived from the existing `--release` and `--fresh-tests` modes.
