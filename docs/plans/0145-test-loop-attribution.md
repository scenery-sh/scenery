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
- [x] 2026-08-21: Made Victoria test-process configuration explicit per
      `devSupervisor`: component specs, helper-binary paths, and port-availability
      checks no longer use process-wide environment mutation. The five
      process-starting lifecycle tests now run in parallel while continuing to
      exercise the real `TestVictoriaManagedProcessHelper` subprocess, PID,
      readiness, ownership, interruption, and recovery boundaries. Three
      alternating full-package A/B pairs against detached `acd13082` measured
      before 12.208/10.055/11.611s (median 11.611s) and after
      8.768/8.625/8.590s (median 8.625s): 2.986s and 25.7% faster, well above
      the 0.5s / 5% retain threshold. Three focused `-race` repetitions passed.
- [x] 2026-08-24: Coordinated the remaining subprocess-heavy `cmd/scenery`
      tail with contract-specific injected runners. Deploy orchestration records
      `exec.Cmd` values in memory, desktop orchestration accepts a
      `desktop.Command` runner, and validation injects the resolved Go-task
      command after cwd and environment construction. Real subprocess coverage
      remains in the deploy stream/exit test, `internal/desktop.Run`, and
      `TestRunSceneryScriptRunsGoFileFromAppRoot`. Three alternating full-package
      A/B pairs measured before 9.871/9.242/8.998s (median 9.242s) and after
      9.099/7.817/7.712s (median 7.817s): 1.425s and 15.4% faster, above the
      0.5s / 5% retain threshold. Three focused race repetitions, every
      changed-area Go command, and the full self-harness passed.
- [x] 2026-08-24: Moved the two desktop-agent lifecycle tests, assistant init,
      and snapshot backup into one parallel-safe batch. The desktop tests start
      their real agent servers at explicit per-test paths instead of mutating
      `commandAgentPathsOverride`; their real Tauri helper processes and Unix
      agent sockets remain covered. Three alternating full-package A/B pairs
      measured before 7.760/8.880/7.763s (median 7.763s) and after
      9.591/6.996/6.692s (median 6.996s): 0.767s and 9.9% faster, above the
      0.5s / 5% retain threshold. Three focused `-race` repetitions, one full
      package `-race` run, the exact changed-area command union, and the full
      self-harness passed.

- [x] 2026-07-29: Optimized runtime hot paths on the developer loop, separate
      from test scheduling. Six changes, each with byte-identical output proven
      before landing:
      1. `internal/compiler` label patterns compile once per process instead of
         `regexp.MatchString` per block label.
      2. Workspace-revision glob matching pre-splits patterns once per
         implementation root and matches segments with iterative star
         backtracking instead of a memoized recursion that allocated two maps
         per segment: **10074ns -> 329ns per file (30x), 64 allocs -> 1**.
      3. Go toolchain identity is memoized per process, so the `go env`
         subprocess and the SHA-256 over the ~15MB `go` binary and ~20MB
         compiler run once instead of 3+ times per command:
         **29.4ms -> 0.02ms per redundant resolution, 39MB -> 11KB allocated**.
         Both cache layers revalidate against the filesystem (digests key on
         path+size+mtime; the env cache re-stats its resolved `go` binary), so a
         replaced toolchain still invalidates.
      4. The `.DS_Store` scan and the embed-report walk now prune skipped trees
         instead of enumerating and discarding: **37,362 -> 1,802 entries per
         walk (95.2% fewer), on two walks**.
      5. The changed-area oracle asks `go list` for only the two fields it
         decodes (`-json=ImportPath,Dir`): **363KB -> 7.7KB of output, 0.70s ->
         0.32s**, verified to produce an identical package set and order.
      Harness step effect, stable across three runs: `contract drift checks`
      141ms then 106-109ms (was 220-1270ms), `changed area oracle` 335-347ms
      (was 610-880ms), `architecture checks` 1610-1645ms (was 1690-1840ms).

- [x] 2026-07-29: Worked the unverified remainder of the performance scan.
      Landed, each measured and output-compared:
      1. Canonical JSON object-key comparison takes an ASCII fast path in both
         `internal/spec` and `internal/contract`: **90.3ns -> 12.4ns, 2 allocs
         -> 0**. ASCII keys sort identically under a byte compare; non-ASCII
         still uses the exact UTF-16 encoding.
      2. Contract constraint patterns compile once per process:
         **1614ns -> 180ns, 37 allocs -> 0**, preserving the previous contract
         that an invalid pattern reports as a non-match.
      3. Contract wire type expressions parse once per process:
         **91.7ns -> 12.6ns, 2 allocs -> 0**. Parsed values are read-only
         everywhere they are used.
      4. Compiled validation programs decode once per encoded program instead of
         per `ValidateContractRecord` call.
      5. `spec.CoreSchema` memoizes only the schema revision digest, which was
         half its cost: **276us/4380 allocs -> 126us/2138 allocs**. The map
         itself is still rebuilt per call, so nothing a caller stores can alias
         shared state.
      6. Route matching splits the request path once per request instead of once
         per candidate route, and contract CORS patterns parse at registration
         instead of per preflight.
      7. `stripANSI` returns `ReplaceAll`'s buffer instead of copying it again
         for every process output line.
      Equivalence proof: 10 CLI commands x 4 fixture apps byte-identical between
      the pre-change and post-change binaries (masking the embedded build
      version/commit/timestamp and the random `report_token`), both committed
      TypeScript fixtures regenerate with `changed: []`, and two consecutive
      full self-harness runs are green.

- [x] 2026-07-30: Fixed the two desktop tests that failed under full-suite
      package contention. Both used `waitForDesktopTest` with a 3s deadline on a
      positive condition — a freshly forked fake desktop process writing its
      marker and registering — so the deadline encoded an unloaded machine's
      fork/exec latency. Raised to 30s: the wait is a condition poll, so passing
      runs finish as soon as the condition holds and the deadline only fires on
      genuine failure. `go test ./...` at default parallelism is green three
      consecutive runs — the first time that configuration passes without
      `-p 2`.

- [x] 2026-07-30: Closed the preflight gap the module-cache wipe exposed. The
      `toolchain preflight` step now verifies the go.mod-declared Go toolchain
      resolves with `GOPROXY=off` — the same hermetic constraint the fixture
      apps compile under — and fails with the exact
      `GOTOOLCHAIN=go<version> go version` restore command. Previously the wipe
      passed preflight and surfaced as three SCN6202 fixture failures deep in
      the suite. Tested in both directions: healthy checkout silent, a
      never-released declared version produces one error diagnostic with the
      restore command, missing go.mod stays silent. Documented in
      `docs/local-contract.md` § harness self.

- [x] 2026-08-15: Made `internal/mcpprojection` graph-only. Removed its
      compiler-aware convenience wrapper, moved expanded-view selection into
      the two compiler-aware callers, and replaced the projection test's live
      compiler fixture with the equivalent checked-in graph fixture. The test
      binary fell from 12,786,418 to 7,240,434 bytes (43.4%); repeated warm
      `go test -c` links fell from a 0.69s median to 0.35s (49.3%). The graph
      fixture still produces the byte-identical canonical MCP golden.
- [x] 2026-08-19: Added the initial cold binary-count and summed-build regression
      budget. The count budget remains current; the timing budget and its
      mistaken link-CPU label were superseded by the 2026-08-20 wall-time
      correction below.
      The then-current `budgets.test_binary_count` was 60 and the now-superseded
      `budgets.cold_build_seconds` was 180s. Cross-worktree binary reuse was
      still unproven.
- [x] 2026-08-19: Parallelized the four isolated children of
      `TestDeploySSHStopsAfterChildFailureAndPreservesExitCode`. Each child
      already owned its temp dir, fake `ssh`/`rsync`, and command log through
      `deploySSHTools`, so the only missing call was `t.Parallel()`. Isolated
      `-race -count=3` over `^TestDeploySSH` is green. Interleaved package
      A/B did not move the `cmd/scenery` wall: parent wait dropped from
      2.36–2.98s to overlapping children of 0.14–1.69s, but
      `TestBuildDesktopCommandEmitsSchemaValidJSON` (2.73–3.83s) and
      `TestContractCheckJSONReportsValidNativeImplementation` (2.11–3.55s)
      still hold the parallel tail.
- [x] 2026-08-19: Split the generate link boundary. `internal/generate/api`
      now owns library build specs and editor-workspace inspection as a
      stdlib-only leaf. Production `internal/evolution` no longer imports
      `internal/generate`; CLI and agent sessions inject predicted-artifact
      and implementation checks. `internal/librarybuild`, `internal/doctor`,
      and `internal/contractagent` no longer depend on `internal/generate`.
      `internal/build` and `internal/generate` still do.
- [x] 2026-08-19: Finished the explicit agent-home seam. `EnsureWith` and
      `commandAgentPaths` let tests pass `PathsForHome(t.TempDir())` instead of
      `t.Setenv("SCENERY_AGENT_HOME")`. Production still reads the env var at
      the CLI/runtime boundary. Per-test `t.Setenv("SCENERY_AGENT_HOME")` is
      gone; `cmd/scenery` TestMain still pins a process-wide temp home so
      leftover `DefaultClient`/`DefaultPaths` calls cannot touch `~/.scenery`.
- [x] 2026-08-19: Lifted the development cache root into `internal/devcache`.
      Doctor no longer imports `internal/build`. `SCENERY_DEV_CACHE_DIR` stays
      the public env; tests inject `SetRoot` / `isolateCommandCacheRoot`.
- [x] 2026-08-19: Injected generate into `internal/build`. Production build
      no longer imports `internal/generate`; CLI and build tests wire
      `GenerateHooks`. Prepare/check/editor-sync ordering is unchanged.
- [x] 2026-08-19: Stopped the evolution test binary from importing generate.
      Tests use recording/no-op fakes. Live predicted-artifact and native
      implementation proof moved to `internal/generate`.
- [x] 2026-08-19: Cheapened the two `cmd/scenery` warm-tail tests. Desktop
      and check JSON now schema-validate stubbed payloads. Live native
      implementation proof stays in `internal/generate`.
- [x] 2026-08-19: Replaced leftover production `DefaultClient()` sites with
      `commandAgentClient()` and deleted the cache-dir `t.Setenv` cluster
      covered by `devcache.SetRoot`. Child processes still receive
      `SCENERY_DEV_CACHE_DIR` from `dev_session_controller.go`.
- [x] 2026-08-19: Extracted `internal/gotarget` so compiler no longer imports
      `internal/parse`. HTTP method constants are `"GET"`/`"HEAD"`; cookie
      validity still uses `http.Cookie.Valid`.
- [x] 2026-08-19: Cold after-measure for the generate link split. Methodology
      identical to the baseline: `.scenery/harness/test-binaries` wiped, then
      `.scenery/harness/bin/scenery harness self --fresh-tests --summary
      --write`, reading the then-current `test_binaries.build_seconds` and the per-binary
      `builds` array from `.scenery/harness/test-timing-latest.json`. Same
      host as every number in this plan (24 logical cores, 16 performance).

      | Summed concurrent build elapsed (historical attribution, not CPU) | Before (2026-08-19, pre-split) | After (2026-08-19, post-split) |
      |---|---|---|
      | `build`+`contractagent`+`evolution`+`generate` | 13.70s (10.2%) | 11.08s (8.9%) |
      | Aggregate test-binary build elapsed | ~134.3s implied, 60 binaries | 124.73s, 63 binaries |
      | `cmd/scenery` link | 4.842s (2026-07-28 measure) | 4.300s |

      After per-binary: `build` 3.794s, `contractagent` 3.258s, `generate`
      2.129s, `evolution` 1.897s. The three new leaves cost `gotarget` 1.243s,
      `generate/api` 0.668s, `devcache` 0.619s (2.530s combined) and are
      already inside the after aggregate, which still fell ~7% overall. Summed
      build elapsed did not migrate into `cmd/scenery`. Cold prepare was 19.522s (list
      2.947s); the suite ran 76.529s plus 53.753s confirmations. Caveat:
      single run, and a concurrent agent session was editing `auth/` files in
      this worktree during the measure, so background compile contention
      cannot be fully excluded; the direction and magnitude are consistent
      with the warm per-link measurements already recorded above.
- [x] 2026-08-19: Trimmed the fresh-lane test-binary count from 63 to 59
      under the unchanged 60 budget. Folded `devcache` Root/SetRoot tests into
      `internal/doctor`, `gotarget.Environment` into `internal/compiler`, and
      generate/api editor inspection into `internal/generate`. Removed
      `internal/codegen`'s assertion-free `testlimit` test binary. A
      `--fresh-tests --summary --write` run reported
      `test_binaries.test_package_count` 59 and no binary-count warning.
- [x] 2026-08-20: Repaired the cold-build metric and pinned missing-binary build
      concurrency at four in `internal/testsuite`, `scripts/testsuite`, and the
      self-harness. `BuildElapsed` was a sum of overlapping subprocess wall
      durations, not CPU or user-visible wall time. The report now calls it
      `aggregate_build_seconds`, records `build_parallelism`, and gates a full
      cold run on `prepare_seconds` against `budgets.cold_prepare_seconds`.
      Three interleaved idle-host macOS samples rebuilt the identical 59 build
      IDs at each setting: build-p=8 prepare 25.413/24.354/20.754s (median
      24.354s), build-p=4 prepare 19.043/18.380/18.855s (median 18.855s), a
      22.6% median wall improvement. The overlapping-duration sum fell 60.5%
      (177.753s to 70.139s median), demonstrating why it cannot be a gate.
- [x] 2026-08-20: Repeated the alternating measurement in a local Linux arm64
      container (`golang:1.27-bookworm`, Go 1.27.0, 24 vCPUs) after warming only
      the Go object/module cache. All six isolated test-binary caches produced
      the same 59 build IDs (sorted-list SHA-256
      `48cc67e0c573c5b174c65019dde1e9fdc8815ca89abea26aa2ba14baa3af1e6f`).
      Linux favored build-p=8: 6.334/4.425/4.295s (median 4.425s), versus
      build-p=4 at 6.323/6.668/6.337s (median 6.337s), 43.2% slower. The
      aggregate moved in the opposite direction (26.501s versus 21.672s
      medians), further proving it cannot govern the release gate. This repo
      has no hosted CI; the portability evidence is local and requires no push.
- [x] 2026-08-20: Replaced the deploy SSH failure matrix's ten cumulative fake
      subprocesses with an injected `RunCommand` seam. One real failing-child
      test remains and proves `*exec.ExitError` plus CLI exit-code 7. Three full
      `cmd/scenery` samples moved from 11.814/10.632/11.153s (median 11.153s) to
      10.265/10.196/10.173s (median 10.196s): 0.957s and 8.6%, above the 0.5s /
      5% retain threshold. Focused ordinary and `-race -count=3` tests passed.

## Surprises & Discoveries

- 2026-07-28: The cold penalty is linking, and package listing is nearly free.
  A cold run (`.scenery/harness/test-binaries` removed) spent 19.181s in
  prepare, of which package listing was 2.416s and 49 links accounted for
  129.776s of summed, overlapping subprocess elapsed time compressed to roughly
  16.8s of wall time. Total cold run was
  46.267s. The warm run of the same command spent 0.081s in prepare and 18.772s
  overall. So the cold/warm gap is prepare, and prepare is links.
- 2026-07-28: Link parallelism is already saturated. Raising `BuildParallelism`
  from 8 to 16 on a 24-logical-core host left prepare unchanged (19.181s versus
  19.300s) while summed build elapsed rose from 129.776s to 283.700s — the
  extra workers only contended with each other. The sum is not CPU time; the
  unchanged prepare wall is the actual saturation evidence.
- 2026-08-20: Eight-way linking was not the best fixed point. With 59 identical
  build IDs and no competing build job, interleaved build-p=8/4 medians were
  24.354s and 18.855s of prepare wall respectively. Four-way linking is 22.6%
  faster on the maintainer macOS host and produces far less contention.
- 2026-08-20: The deploy SSH failure matrix had become the package tail after
  earlier desktop and contract-check reductions. Its parallel subtests still
  launched one, two, three, and four fake processes cumulatively. An in-memory
  runner removed those ten forks while a single real failure kept the process
  boundary covered; median package wall fell 8.6%.
- 2026-08-24: The next reduction required treating the parallel tail as a
  group. Deploy order/publish, desktop build, and validation Go tasks each
  became slower under package contention, and the last finisher varied by run.
  Replacing their repeated subprocesses together moved the package median by
  15.4%; retaining only one runner seam would have left another real-process
  cluster owning the tail.
- 2026-08-24: The preserved real desktop boundary exposed a pre-existing race:
  `os/exec` drains stdout and stderr concurrently, but `desktop.Run` sent both
  to the same unsynchronized output and tail buffers. One locked combined
  writer now serializes those writes; three focused race repetitions pass.
- 2026-08-24: The desktop lifecycle tests did not need command-global path
  resolution. Their shared helper coupled a unique server home to
  `commandAgentPathsOverride` even though the tests construct the agent client
  directly. Starting each server at explicit paths removed the coupling while
  preserving the real agent and desktop-process boundaries.
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
- 2026-08-15: A leaf package is not graph-only if its tests reconstruct the
  graph through the compiler. Removing `internal/mcpprojection`'s production
  compiler import alone would have left its test closure unchanged because
  `project_test.go` still compiled the native app fixture. Keeping the same
  selected resources as graph JSON reduced the test dependency count from 292
  to 182 and removed `internal/compiler`, `internal/parse`, and
  `golang.org/x/tools/go/packages` from that closure.
- 2026-08-19: Go test BuildIDs are not worktree-portable. Two worktrees with
  identical source still produce different `.test` BuildIDs because `go list
  -export` folds the package directory path into the ID. A shared
  `.scenery/harness/test-binaries` cache keyed on those IDs cannot hit across
  worktrees. Proving a cross-worktree cache needs either path-normalized IDs
  or a content digest, and the runner's local contract still prefers Go build
  IDs over a parallel source-dependency model.
- 2026-08-19: Parallelizing already-isolated subtests of a test that itself
  calls `t.Parallel()` shortens that parent, not the package, unless that
  parent is the tail. Interleaved A/B on this host: before package
  11.637/11.414/11.465s with this parent at 2.36/2.98/2.36s; after
  12.354/11.566/11.084s with parent JSON elapsed 0 (children now overlap).
  Serial cutoff stayed ~7.4s. The tail after the last serial test stayed
  ~4.0–4.4s and finished on `TestBuildDesktopCommandEmitsSchemaValidJSON` or
  `TestContractCheckJSONReportsValidNativeImplementation`, not the deploy SSH
  parent. Isolated idle runs of this test were already ~1.3s; the 3.19s
  figure is a contended package-tail measurement.
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
- 2026-07-29: Precomputing derived state into a struct field is a silent-failure
  trap, and a test caught it. Adding a parsed `pattern` field to
  `contractCORSRoute` broke
  `TestContractPathTailCORSUsesSelectedMethodAndRoutePrecedence`, because the
  test builds route literals directly and a zero `routePattern` matches nothing
  — the CORS policy silently became nil rather than erroring. Making the field a
  pointer with a parse-on-demand fallback means any construction site that omits
  it stays correct, just uncached. Prefer that shape over requiring every caller
  to remember the cache field.
- 2026-07-29: Comparing two CLI binaries byte-for-byte needs the build metadata
  masked or every command "differs". `inspect`/`compile`/`check` envelopes embed
  the producer `version`, `commit`, and `built_at`, which change on every `go
  build`, so the first comparison reported all 10 commands as regressions. The
  only real diff was those three fields; with them and the random `report_token`
  masked, all 10 commands across 4 fixtures matched exactly.
- 2026-07-29: `spec.CoreSchema` costs 276us and 4380 allocations per call, and
  it is called per resource during graph context bundling and per resource kind
  on agent requests. Half the cost is the `SchemaRevision` canonical marshal
  plus SHA-256 over the schema map it just built. Caching the whole map is
  unsafe — `internal/graph/query.go:391` stores the returned map into a bundle,
  so a shared map could be mutated by a caller — but the digest is a pure
  function of a map rebuilt identically from static tables, so caching only the
  digest is both safe and half the win.
- 2026-07-30: The Go module cache was manually wiped to reclaim disk space, and
  the failure it produced looked like a code regression. Three
  tests that run `scenery check` against fixtures started failing in ~0.05s
  with SCN6202 "resolve declared Go toolchain ... toolchain not available":
  the fixtures declare `go1.26.3`, the hermetic resolution env sets
  `GOPROXY=off`, and the toolchain module had vanished from the cache along
  with most of `~/go/pkg/mod`. The tell was `go: downloading golang.org/x/mod`
  during an ordinary vet — dependencies that had been cached for days.
  `GOTOOLCHAIN=go1.26.3 go version` (with the default proxy) restored it. When
  previously-green tests fail immediately after an environment hiccup, check
  the module cache before bisecting code.
- 2026-07-29: A differential test against the replaced implementation caught a
  real bug in the glob rewrite that the existing suite did not. The iterative
  matcher initially tested the literal/`?` case before `*`, so a `*` in the
  pattern consumed a literal `*` in the value as a character match:
  `matchGlobSegment("aa*", "aa*a")` returned false where the memoized recursion
  returned true. The package's own tests passed. Keeping the old algorithm in
  the test file as an oracle and comparing over 390k exhaustive pairs plus 200k
  randomized ones found it immediately. Any rewrite of a matcher in this repo
  should carry its predecessor as a differential oracle.
- 2026-07-29: `inspect build` output is not byte-stable, which makes naive
  before/after comparison useless. Two fixtures produce a fresh random
  `report_token` per run on the internal-error path, so the *same* binary
  disagrees with itself. Comparing old against new only became meaningful after
  masking `rpt_[a-z0-9]+`; with it masked all four fixtures matched.
- 2026-07-29: Two `cmd/scenery` desktop tests fail under full-suite package
  contention and always have. `TestDesktopShellUsesFrontendBackendAndRegistersProcess`
  and `TestDesktopShellExitDoesNotRestart` use a 3s `waitForDesktopTest` window
  and time out under `go test ./...` at default `-p`, while passing when the
  package runs alone or at `-p 2`. Confirmed pre-existing by stashing the
  performance changes and reproducing both failures on the clean tree, so they
  are a fixture-timeout sensitivity rather than a regression from this work.
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
- The graph-only `internal/mcpprojection` boundary is retained because the
  measured improvement is comfortably above link noise: 43.4% less binary and
  49.3% less median warm link time across three like-for-like `go test -c`
  runs. Compiler callers own validity and expanded-view selection; projection
  owns only graph-to-MCP conversion.
  Date/Author: 2026-08-15 / Codex.
- The 2026-08-19 timing budget used the wrong quantity. It treated the sum of
  overlapping per-binary elapsed durations as link CPU and set 180s just above
  a 175.38s observation. That sum changes with concurrency and is retained only
  as attribution under the explicit name `aggregate_build_seconds`; it is not
  an enforceable performance contract. The 60-binary structural budget remains
  valid.
  Date/Author: 2026-08-19 / Grok.
- Full cold preparation is held to 30s of `prepare_seconds` wall time at pinned
  build parallelism four. `build_parallelism` is recorded in the artifact so a
  timing cannot be compared without its scheduling policy. Binary count is
  checked on warm fresh runs; prepare wall is checked only when every test
  binary was built. The pinned value follows three identical-build-ID macOS A/B
  pairs where four-way linking improved median prepare wall by 22.6%. A local
  Linux arm64 container instead favored eight-way linking by 43.2%, while
  remaining well under the 30s budget at either setting. The platform split is
  why the artifact carries concurrency and the gate measures wall time; four is
  retained as the explicit default for the macOS development/release host.
  Date/Author: 2026-08-20 / Codex.
- The deploy SSH child-failure table is safe to parallelize without a new
  production seam. Each subtest already installs its own fake binaries and
  failure env through `deploySSHTools`; the parent already called
  `t.Parallel()`. Adding `t.Parallel()` on the children is the whole change.
  Date/Author: 2026-08-19 / Grok.
- The measured command-runner seam is retained. It is private to
  `deploySSHTools`, production defaults directly to `cmd.Run`, the matrix still
  checks fail-fast ordering and exit-code propagation in memory, and a separate
  real subprocess test covers `*exec.ExitError`. The 0.957s / 8.6% median package
  reduction exceeds the predeclared 0.5s / 5% materiality threshold.
  Date/Author: 2026-08-20 / Codex.
- The coordinated deploy, desktop-build, and validation command runners are
  retained. Each seam describes its own command contract, production behavior
  still executes the real child, and one focused real subprocess test remains
  at each owning boundary. The alternating package median improved by 1.425s /
  15.4%, so a shared generic runner abstraction is unnecessary.
  Date/Author: 2026-08-24 / Codex.
- The desktop-agent, assistant-init, and snapshot-backup parallel batch is
  retained. All state is temp-rooted per test, and the desktop pair uses a
  test-only explicit-path server helper rather than a new production seam. The
  alternating package median improved by 0.767s / 9.9%, above the declared
  materiality threshold.
  Date/Author: 2026-08-24 / Codex.
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
- The agent-home seam is now explicit. `EnsureWith` and `commandAgentPaths`
  are the in-process injection points; `SCENERY_AGENT_HOME` stays the public
  user/runtime override and is read only at that CLI/runtime boundary. Tests
  pass `PathsForHome` or `isolateCommandAgentHome` instead of `t.Setenv`.
  Date/Author: 2026-08-19 / Grok.
- Four test files are deliberately excluded from parallelization:
  `dev_ports_test.go`, `dev_named_lock_test.go`,
  `dev_session_cleanup_unix_test.go`, and `snapshot_backup_script_test.go`.
  They coordinate through shared on-disk state, and parallelizing them
  regressed the `-race` lane while adding little. Anything that makes them
  parallel-safe has to isolate that state first, not add `t.Parallel`.
  Date/Author: 2026-07-28 / Claude.
- The 2026-07-28 decision to leave Victoria test processes on the production
  environment contract is superseded by the measured 2026-08-21 experiment.
  The production contract remains singular: a zero-value supervisor still uses
  `SCENERY_VICTORIA_*` through `victoria.StartAtRoot`. Tests instead provide a
  private per-supervisor process configuration with explicit component specs,
  binary paths, and port checks. That isolates state without weakening the two
  tests that assert same-root serialization; those assertions remain internal to
  each parallel test. Interleaved package A/B improved median wall by 2.986s /
  25.7%, so the seam is retained.
  Date/Author: 2026-08-21 / Codex.
- Toolchain identity is memoized with self-validating caches rather than an
  invocation-scoped cache. A `scenery up` session lives for hours and a
  toolchain can be replaced under it, so keying digests on path+size+mtime and
  re-stating the resolved `go` binary before reusing an environment hit keeps a
  swap detectable. This is strictly more consistent than the previous behavior,
  which recomputed independently per resolution and could therefore report two
  different toolchain digests within one command if a binary changed mid-run.
  Resolution failures are deliberately never cached so a transient error does
  not stick for the process lifetime.
  Date/Author: 2026-07-29 / Claude.
- The `ps` fork in agent owner verification is deliberately left in place.
  `sessionOwnerVerifies` runs on every proxied request and reaches
  `processOwnerInfo`, which forks `ps -p <pid> -o lstart= -o command=` on unix —
  genuinely hot and genuinely expensive. It is not fixed because both available
  fixes break something the repo guarantees. Caching per PID reintroduces a
  PID-reuse window into a check whose whole purpose is to fail closed on
  ownership conflicts, and replacing `ps` with a `sysctl` lookup would have to
  reproduce `lstart`'s exact string byte-for-byte, because `Owner.StartedAt` is
  persisted and compared for equality — a format change would make every
  recorded owner mismatch and stop running sessions. Both are contract changes,
  not optimizations, so they need explicit direction.
  Date/Author: 2026-07-29 / Claude.
- The next reduction target is the `cmd/scenery` serial phase, not fixture
  reuse. The measurement above contradicts the fixture/duplicate-compile
  hypothesis for this package. The candidate work is removing `t.Setenv` from
  heavy tests by threading configuration through existing seams, and replacing
  fixed-port binding with port-zero listeners, so those tests can join the
  parallel phase. Mass-adding `t.Parallel` without those changes is unsafe, as
  0050 already recorded.
  Date/Author: 2026-07-28 / Claude.
- Cache-root and generate-hook seams stay in-process. `SCENERY_DEV_CACHE_DIR`
  remains the only public cache env; `devcache.SetRoot` is not a second knob.
  Production `internal/build` fails closed when generate hooks are unwired.
  Evolution tests must not relink generate: live artifact proof belongs in
  `internal/generate`. Architecture checks enforce compiler↛parse,
  build↛generate (tests may wire hooks), doctor↛build/generate, and the
  `devcache`/`gotarget` leaves.
  Date/Author: 2026-08-19 / Grok.
- The 60-binary count budget is now three over. The link split added the
  `generate/api`, `devcache`, and `gotarget` test binaries (2.53s of summed build elapsed
  combined), so every fresh run reports an advisory
  "63 test binaries, over the 60 binary-count budget" warning and `--release`
  would fail it. Whether to raise `budgets.test_binary_count` to 63 or trim
  binaries elsewhere is an open decision; the cold measure below shows the
  three leaves pay for themselves in aggregate.
  Date/Author: 2026-08-19 / Claude.
- Trim to stay under 60; do not raise `budgets.test_binary_count`. The three
  new leaf test binaries move into consumers that already link them:
  `devcache.Root`/`SetRoot` into `internal/doctor`, `gotarget.Environment`
  into `internal/compiler`, and generate/api editor inspection into
  `internal/generate`. `internal/codegen`'s test-only `testlimit` import is
  removed because that package had no assertions. Coverage is unchanged; the
  count budget stays 60.
  Date/Author: 2026-08-19 / Grok.

## Outcomes & Retrospective

Open. The confirmation-scope change removes the largest reported cost
(99.506s of confirmation) from everyday fresh runs without losing regression
detection, and the prepare instrumentation answers the cold-run question. The
generate link split is measured and paid off cold: the four consumer binaries
  fell from 13.70s (10.2%) to 11.08s (8.9%) of summed build elapsed, the aggregate
fell to 124.73s even with three new leaf binaries, and `cmd/scenery` did not
absorb the cost (see the 2026-08-19 cold after-measure in Progress). The
binary-count budget stays 60; the extra leaf test binaries were folded into
consumers rather than raising the budget. The cold timing gate now measures
prepare wall at pinned four-way build concurrency instead of a concurrency-
sensitive sum mislabeled as CPU. The warm-suite target is unmet and now has a
precise, measured owner. Explicit per-supervisor Victoria process configuration
moved five real-process lifecycle tests into the parallel phase and reduced the
measured `cmd/scenery` package median from 11.611s to 8.625s. The coordinated
runner experiment then reduced its measured median from 9.242s to 7.817s, and
the remaining desktop-agent/assistant-init/snapshot-backup batch reduced its
current median from 7.763s to 6.996s.

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
   in order of leverage: `internal/generate` still anchors `go/types` and
   `net/http` for `build` and `generate` (and for `evolution` tests);
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
# cold attribution: isolate the cache, then read prepare wall and ranked links
cold_cache="$(mktemp -d /tmp/scenery-test-binaries.XXXXXX)"
go run ./scripts/testsuite -cache "$cold_cache" -p 3 -build-p 4 -run 'a^' -builds 20 -record-timings=false

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
measured reduction in `test_binaries.prepare_seconds` and in the `cmd/scenery`
package wall respectively, each shown against the numbers in Surprises &
Discoveries on the same host. Dependency-boundary attribution may additionally
use `aggregate_build_seconds` and the per-build array, but neither is a gate.

## Idempotence and Recovery

Every step is repeatable. `.scenery/harness/test-binaries` and the isolated
temporary cache above are disposable; an empty cache forces a cold run and
nothing else. A missing, truncated, or
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

Unless a measurement says otherwise, the host used 24 logical cores and 16
performance cores. The Linux concurrency result used the local 24-vCPU arm64
container described in Progress. Timing conclusions do not transfer across
machines; re-measure before comparing.

## Interfaces and Dependencies

- `scenery.harness.test_timing` gained `test_binaries`,
  `deferred_confirmations`, `budgets.confirmation_scope`,
  `budgets.test_binary_count`, `budgets.cold_prepare_seconds`,
  `test_binaries.test_package_count`, `test_binaries.build_parallelism`, and
  `test_binaries.aggregate_build_seconds`. The current schema revision is
  recorded in `cmd/scenery/payload_identity.go`.
  `scenery.harness.self` inlines that nested shape; its current schema revision
  is `sha256:1754e319963b4e57b46f9cc28ce17f685a2406184d80db80c185dd6eba0a0be4`.
- `testsuite.Result` gained `Prepare` and `TestPackageCount`; `testsuite.Run`
  is otherwise unchanged.
- `scripts/testsuite` gained `-builds N`, which writes to stderr only, leaving
  the stdout Go JSON event stream intact.
- `deploySSHTools` gained a private `RunCommand` injection point; the zero value
  retains direct `exec.Cmd.Run` behavior.
- Desktop builds have a private `desktopBuildCommandRunner` around
  `desktop.Command`; production passes `internal/desktop.Run`.
- Code-task options have a private `scriptCommandRunner` around the fully
  configured `exec.Cmd`; validation threads it only into task steps and
  production leaves it nil.
- `internal/desktop.Run` shares one locked combined writer between child stdout
  and stderr so caller output and the error tail remain race-free.
- `startTestAgentServerAtPaths` is a test-only helper for real agent servers
  whose callers already own explicit paths; it does not alter production agent
  path resolution.
- `internal/victoria.StartConfig` and `StartAtRootWithConfig` accept explicit
  component specs and binary paths. `devSupervisor` carries the private
  `victoriaProcessConfig` that pairs startup with the matching port-availability
  check; its zero value retains the production environment path.
- No new environment variables, and no new CLI flags: confirmation scope is
  derived from the existing `--release` and `--fresh-tests` modes.
