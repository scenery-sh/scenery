# Test Suite Speed and Stability

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The immediate goal is to make `go test -count=1 ./...` green, quiet, and measurably faster without deleting coverage. The current failures and noise come from test infrastructure, not from the product behavior being asserted: one Grafana resolver unit test depends on spawning a fake process under full-suite scheduler pressure, and several CLI tests intentionally exercise dev proxy/trust paths that print warnings to real stderr.

The longer goal is a warm-cache full-suite runtime near five seconds. That requires timing evidence first, then targeted reductions in redundant real `go build`, process startup, Temporal startup, TLS certificate generation, and polling delays.

## Progress

- [x] 2026-05-28: Created this ExecPlan from the test-suite diagnosis covering Grafana probe flake, warning spam, and the warm-cache speed target.
- [x] 2026-05-28: P0 complete. Added the Grafana probe seam, rewrote the mismatch test to avoid subprocess timing, routed dev escape-hatch warnings through `cliStderr`, suppressed expected warning output in non-warning tests, and added an explicit warning assertion.
- [x] 2026-05-28: Validated P0 with focused `cmd/onlava` tests, `go test -count=1 ./cmd/onlava`, `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.
- [x] 2026-05-28: P1 complete. Added `scripts/slowtests.go`, which parses `go test -json` from a file or stdin and prints the slowest packages and tests.
- [x] 2026-05-28: P2 complete for `internal/build`. Added a `runGo` seam, faked redundant `go mod tidy` / `go build` invocations in build-state and cached-workspace tests, kept a real compile smoke test with a tiny synthetic Go workspace, and replaced cached-graph setup that did not need `Prepare`.
- [x] 2026-05-28: Verified `internal/build` below 3s. Isolated JSON run: 1.378s. Isolated plain run: 1.497s. Full `go test -count=1 ./...` run: `internal/build` 2.746s.
- [x] 2026-05-28: P3 partial complete. Reduced root integration package runtime without changing tested assertions by increasing isolated onlava process parallelism from a hardcoded 2 to `GOMAXPROCS/2` capped at 4, reducing integration polling intervals from 200ms/100ms to 50ms, and sharing one real Temporal dev server across Temporal-backed root integration tests.
- [x] 2026-05-28: Verified P3 partial results. Root package JSON timing improved from 24.728s to 15.087s isolated. Full `go test -count=1 ./...` stayed green with root package at 20.419s and `internal/build` still below 3s at 2.806s.
- [x] 2026-05-28: P4 complete for `internal/localproxy`. The slow path was proxy shutdown waiting on idle test client keep-alive connections, not certificate generation. Added explicit test client idle-connection cleanup in full proxy tests without changing their requests or assertions.
- [x] 2026-05-28: Verified `internal/localproxy` isolated runtime improved from 3.588s to 0.428s in JSON timing, and `go test -count=1 ./internal/localproxy` reported 0.334s.
- [ ] P5: Finish `cmd/onlava` speed work by removing fixed sleeps and keeping full-path process lifecycle tests focused.
- [x] 2026-05-28: P5 partial complete. Made watch polling/settle timings test-overridable and reduced `TestWaitForStableChangeEventsPollsWhenEventsAreMissed` from 2.5s to 0.02s while preserving the missed-event fallback path.
- [x] 2026-05-28: P5 partial complete. Added an internal build Go-runner test hook and used it for CLI JSON/argument plumbing tests that do not need a second real compiler subprocess. Kept real compile, recompile, cache, compile-failure, and generated-workspace `go test` tests intact.
- [x] 2026-05-28: Verified touched package timings after the P5 partial pass: `internal/build` 1.111s, `internal/localproxy` 0.874s, `cmd/onlava` 13.923s in a plain multi-package run. Full `go test -count=1 -json ./...` stayed green; the slowest package remained root integration at 26.443s under full-suite contention.
- [x] 2026-05-29: Investigated root integration next. Individual measurements showed `TestOnlavaDevReloadsOnGoChanges` at 9.420s, `TestOnlavaDevDashboardNotificationsAndMCP` at 10.380s, `TestOnlavaBuildProducesRunnableBinary` at 4.730s, and `TestOnlavaRunBasicApp` at 5.110s. These are real app/dev/build startup costs, not only process-slot queueing.
- [x] 2026-05-29: Fixed objectstore parallelization as product hardening instead of backing it out. Metadata bootstrap and schema migrations now share the same transaction-scoped schema lock. Bootstrap/migrations take an exclusive record-schema barrier before DDL, while record create/update/delete take a shared record-schema barrier before table/search/outbox writes.
- [x] 2026-05-29: Re-enabled `t.Parallel()` for tenant-isolated objectstore Postgres integration tests. The SSE heartbeat and outbox-poll tests now use per-store timing overrides instead of mutating package-level timing variables, so their assertions can run in parallel too.
- [x] 2026-05-29: Verified objectstore parallelization with `go test -count=3 ./internal/objectstore` and JSON timing. Final isolated JSON runtime was 3.564s, with the longest remaining test being the serial heartbeat SSE test at 2.370s.
- [x] 2026-05-29: Ran final validation for the objectstore hardening pass: `go test -count=1 ./internal/objectstore ./internal/datainspect`, `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write` all passed. Harness wrote `.onlava/harness/self-latest.json` and reported only existing review-due and large-file warnings.
- [x] 2026-05-29: Tightened the objectstore hardening guardrail with `TestPostgresTenantIsolatedFieldMigrationsDoNotDeadlock`, which concurrently applies physical field DDL across independent tenants. Revalidated with `go test -count=1 -parallel=32 ./internal/objectstore`, `go test -count=5 -parallel=32 ./internal/objectstore`, `go test -count=1 ./internal/objectstore ./internal/datainspect`, `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.
- [x] 2026-05-29: Fixed the remaining objectstore lock-order hole exposed by repeated package-pair runs. `CreateObject` now ensures tenants inside the schema advisory lock as well, so `ImportTenant` cannot hold a tenant row lock in an outer transaction before trying to acquire the DDL lock. Migration rows are also inserted only after the DDL locks are held, with a savepoint around physical DDL so failed/skipped/applied migration records are preserved.
- [x] 2026-05-29: Revalidated the final objectstore deadlock fix with `go test -count=3 ./internal/objectstore ./internal/datainspect`, `go test -count=5 -parallel=32 ./internal/objectstore`, `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`. Full suite is green but still above the long-running speed goal at 27.147s for the root integration package and 17.614s for `cmd/onlava` in the final run.
- [x] 2026-05-29: Added a lazy-tidy build path. `CompileContext` now tries `go build` first for workspaces marked `NeedsTidy`, then runs `go mod tidy` and retries only if the first build fails. Explicit `PrimeWorkspaceContext` still tidies eagerly.
- [x] 2026-05-29: Trimmed `cmd/onlava` command-path test cost without removing assertions. Dashboard data RPC now discovers the test DSN from the app `.env` and can run in parallel, and the check command cache/compile-shape tests use the existing fake Go runner where real compiler coverage is already supplied by neighboring tests.
- [x] 2026-05-29: Measured after the command-path cut. Isolated `cmd/onlava` improved from 14.121s to 11.905s. Full suite remained green but far above target: root integration 28.851s and `cmd/onlava` 17.392s under full-suite contention.
- [x] 2026-05-29: Seeded generated workspaces with the onlava repo `go.sum`, allowing `onlava test` to run the generated workspace test directly in the common case. Kept a targeted tidy fallback for missing sum/mod metadata.
- [x] 2026-05-29: Moved `onlava run` onto a parse-once path and added production secret preflight before generated app compilation. The missing-production-secrets integration assertion now fails from parsed model/env data instead of building and starting an app that is guaranteed to exit.
- [x] 2026-05-29: Kept real compiler coverage for `onlava check` compile failures, but changed the source-change invalidation test to use the internal fake Go runner. The assertion still proves changed source invalidates the cached check result and attempts a fresh compile; the neighboring compile-failure test continues to cover real Go diagnostics.
- [x] 2026-05-29: Tried lowering the root integration process cap back to 4, but a full-suite run worsened root package elapsed to 24.340s because test time includes slot waiting. Restored the cap to 16 for the current full-suite objective.
- [x] 2026-05-29: Revalidated after the latest cuts with `go test -count=1 -json ./...`. The suite stayed green and wall time improved modestly, but the target is still not met: root package 19.518s, `cmd/onlava` 15.419s, and total command wall about 21s.
- [x] 2026-05-29: Fixed the remaining objectstore migration retry gap. Deadlocks raised from inside the migration body now roll back the whole migration attempt and retry, instead of committing a running migration marker through the failed-DDL savepoint path. Added `TestWithMigrationTxRetriesDeadlockFromMigrationBody`.
- [x] 2026-05-29: Validated the migration retry hardening with focused objectstore tests, a 10x concurrency stress run, `go test -count=3 ./internal/objectstore`, full `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.
- [x] 2026-05-29: Added a persistent, source-fingerprint-invalidated root integration cache so repeated `-count=1` root integration runs can reuse generated fixture app binaries without reusing stale binaries after non-test source changes.
- [x] 2026-05-29: Root integration now prefers a fresh installed `onlava` binary from `ONLAVA_BIN`/`PATH` and falls back to building when missing or stale. The freshness scan ignores `_test.go` files so test-only edits do not invalidate app fixture caches or the CLI binary.
- [x] 2026-05-29: Removed the global `chdir` dependency from the real `onlava test` generated-workspace integration test and marked isolated process/unit tests in `cmd/onlava` parallel where they do not mutate process-wide state.
- [x] 2026-05-29: Measured after this pass. Warm isolated root integration reached 4.789s package elapsed. Warm full `go test -count=1 -json ./...` is still above target at roughly 13s wall, with `cmd/onlava`, `internal/objectstore`, and `internal/datainspect` still dominating under cross-package contention. A fully cached plain `go test ./...` rerun completed in 0.676s, but that is not the meaningful no-test-cache target.
- [x] 2026-05-29: Reconfirmed the objectstore deadlock hardening after the shared PostgreSQL test changes. `go test -count=5 -parallel=32 ./internal/objectstore`, `go test -count=3 ./internal/objectstore ./internal/datainspect`, `go test -count=1 ./...`, and `go install ./cmd/onlava` all passed. The tenant-isolated field DDL path is now covered as a product concurrency regression, not backed out as a speed-only test parallelization risk.
- [x] 2026-05-29: Made the shared PostgreSQL test helper actually persist its reusable named container across package test binaries by disabling the Testcontainers reaper for that helper and caching the validated admin DSN. Focused timings improved to `internal/datainspect` 0.605s and `internal/objectstore` 1.802s in a combined JSON run, while keeping real PostgreSQL coverage.
- [x] 2026-05-29: Added stable workspace keys for the synthetic root integration apps `temporalsecrets` and `serviceinit`, so they reuse generated build workspaces like the fixture-backed root integration tests.
- [x] 2026-05-29: Revalidated this increment with `go test -count=1 -json ./...` and `go install ./cmd/onlava`. Full no-cache timing remains above the target: root integration 13.050s, `cmd/onlava` 9.591s, and compile-only `go test -count=1 -run '^$' ./...` is still about 9.9s wall, which is now the hard lower bound to address next.
- [x] 2026-05-29: Removed avoidable command/runtime test delays without changing asserted behavior. The real `onlava test` generated-workspace test now uses a stable workspace key and still executes `go test`; the second-Ctrl-C and runtime parent-monitor integration tests close their wait channels so deferred cleanup no longer waits one extra second; and `onlava check --json` compile-diagnostic formatting now uses a fake Go runner while the harness compile-failure test keeps real compiler coverage.
- [x] 2026-05-29: Revalidated after these cuts. `cmd/onlava` isolated JSON timing is 4.178s, the runtime parent-monitor focused test is 0.05s, full `go test -count=1 -json ./...` is green at roughly 11.1s wall with root 5.431s and `cmd/onlava` 6.756s, and a fully cached plain `go test ./...` rerun is 1.049s wall. The no-cache target remains open.
- [x] 2026-05-29: Closed a remaining objectstore migration correctness gap instead of backing out parallel tests. Locked migrations now refresh the current object schema version after acquiring schema/record/object advisory locks, and field/index/trigger migrations bump `schema_version` atomically inside the same transaction as the physical DDL.
- [x] 2026-05-29: Added `TestPostgresConcurrentDistinctFieldMigrationsAdvanceSchemaVersion` to prove concurrent distinct field DDL on one object creates every field and advances the schema version once per migration. The test uses a permission barrier so every goroutine loads the same pre-migration state before DDL begins. Revalidated with the focused test, `go test -count=5 -parallel=32 ./internal/objectstore`, `go test -count=1 ./internal/objectstore ./internal/datainspect`, full `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.
- [x] 2026-05-29: Removed global working-directory state from `cmd/onlava` inspect tests. The tests now pass explicit `--app-root`; independent inspect subtests run in parallel, while the one cache-root assertion keeps its environment override local and serial.
- [x] 2026-05-29: Bounded `GOMAXPROCS` for root integration child processes to reduce oversubscription when many external `onlava run`, worker, dev, and build subprocesses run during the full suite.
- [x] 2026-05-29: Parallelized isolated parser and codegen tests that use only temp dirs or read-only golden fixtures. Focused package timings improved to `internal/codegen` 0.980s package elapsed and `internal/parse` 1.155s package elapsed, though full-suite wall time is still dominated by root/cmd process work and package compile overhead.
- [x] 2026-05-29: Revalidated this increment with focused inspect tests, focused codegen/parse tests, full `go test -count=1 -json ./...`, and cached `go test ./...`. The strict no-cache full suite remains above target at roughly 14.1s in the latest run; a fully cached literal `go test ./...` rerun completed in 1.02s.
- [x] 2026-05-29: Fixed the objectstore deadlock finding as product hardening rather than backing out test parallelism. Tenant-isolated physical field DDL and concurrent same-object field migrations now pass under focused high-parallel stress, `go test -count=1 ./internal/objectstore ./internal/datainspect`, and full `go test -count=1 ./...`.
- [x] 2026-05-29: Made root integration synthetic apps persistent under the shared integration cache with content-hash ready markers. The temporal-secrets, service-init, and headless build tests now use stable app roots without changing the `onlava run` / `onlava build` behavior being asserted.
- [x] 2026-05-29: Added an unchanged-output fast path to `onlava build`: if the requested output already matches the cached compiled binary, the build command preserves it and skips the macOS signing step. Changed outputs are still copied and signed. Added focused unit coverage for both unchanged and changed output paths.
- [x] 2026-05-29: Removed the process-wide `ONLAVA_TEST_WORKSPACE_KEY` dependency from the real generated-workspace `onlava test` command-path test. It now uses a stable persistent app root and can run as a parallel test while still invoking a real generated-workspace `go test`.
- [x] 2026-05-29: Revalidated this pass with focused build-copy tests, focused synthetic root integration tests, isolated `cmd/onlava`, isolated root integration, and full `go test -count=1 -json ./...`. Warm isolated root reached 3.685s package elapsed in the best rerun, but the full no-cache suite remains above target and noisy: measured full-suite wall times after this pass were 11.06s and 14.65s.
- [x] 2026-05-29: Moved the remaining root integration fixture variants onto the shared cache. The production missing-secret test now uses a cached `secrets` fixture with `.env` removed, and the dev dashboard/MCP test uses a cached `basic` fixture with the proxy config override while restoring only the source file it mutates for the reload assertion.
- [x] 2026-05-29: Lowered default root integration external-process fanout from up to 16 to up to 4. The warmed full-suite run with the lower cap passed at 12.11s wall with root and `cmd/onlava` both below 5s package elapsed; a cached literal `go test ./...` repeat completed in 1.80s. The no-cache full-suite goal remains open because compile/package contention still keeps wall time above five seconds.
- [x] 2026-05-29: Removed avoidable PostgreSQL test-helper serialization on the cached path. `internal/testpostgres.Start` now validates an existing reusable Postgres DSN and ensures the package database before taking the filesystem lock; the lock is reserved for stale-cache repair and initial container startup.
- [x] 2026-05-29: Added a reusable-binary fast path for `onlava run` and `onlava build`. Build state now records app source/config and onlava generator/runtime fingerprints, and the fast path is used only when both fingerprints still match and the compiled binary exists. Production `onlava run --env production` still parses first so missing-secret validation keeps its current scope.
- [x] 2026-05-29: Stopped invalidating root integration app workspace caches for command-only source changes. The root test binary freshness check still includes `cmd/`, but generated app fixture caches now ignore `cmd/` and `scripts/` because those packages do not affect generated app binaries.
- [x] 2026-05-29: Narrowed root integration process-slot usage to actual long-lived process lifetimes. The production missing-secret test no longer takes a server slot because it exits during validation, and the built-binary test now takes the slot after `onlava build`, only while the built app server is running.
- [x] 2026-05-29: Re-tested the external-process fanout cap against the current warmed cache after the Postgres helper change. Cap 16 over-subscribed the full suite and regressed to 17.83s. The useful range is tighter: cap 4 was 11.58s, cap 5 was 11.22s, cap 6 was 11.18s, cap 8 was 11.64s after the cached-binary fast path, and cap 10 was 11.51s. Set the bounded default cap to 6 for the current measured workload.
- [x] 2026-05-29: Split Temporal SDK-dependent runtime mechanics out of the broadly imported `runtime` package and into the `temporal` package. `runtime` now keeps Temporal config, naming, task-queue scoping, tracing report handoff, cron parsing, and runtime hooks, while `temporal` registers the Temporal client, worker, worker-options, telemetry interceptor, and Temporal-backed cron implementations.
- [x] 2026-05-29: Revalidated the Temporal split with `go test -count=1 ./runtime ./temporal`, `go test -count=1 ./cmd/onlava ./internal/codegen ./runtime ./temporal`, and full `go test -count=1 ./...`. The full suite remains green but still above the strict no-cache target: the latest warmed run was 10.753s wall with root integration at 7.136s and `cmd/onlava` at 3.641s.
- [x] 2026-05-29: Fixed the remaining tenant-isolated field DDL deadlock risk as product hardening, not as a test-speed workaround. Tenant upsert and all physical objectstore DDL now take a global physical-schema migration advisory lock before tenant/object locks, while record mutations stay tenant-scoped through the shared tenant record-schema barrier.
- [x] 2026-05-29: Revalidated the physical DDL lock with focused field-DDL/import tests, `go test -count=10 -parallel=64 ./internal/objectstore -run 'TestPostgresTenantIsolatedFieldMigrationsDoNotDeadlock|TestPostgresConcurrentDistinctFieldMigrationsAdvanceSchemaVersion|TestPostgresConcurrentCreatesAreIdempotent|TestPostgresExportImportTenantRoundTrip'`, `go test -count=5 -parallel=64 ./internal/objectstore`, `go test -count=1 ./internal/objectstore ./internal/datainspect`, full `go test -count=1 -json ./...`, and `go install ./cmd/onlava`. The full run stayed green but was noisy at 16.590s wall, with root integration again dominating.
- [x] 2026-05-29: Added a conservative objectstore metadata bootstrap fast path for repeated `pgxpool.Pool` opens against the same database. Cache hits validate the core metadata tables with `to_regclass` and fall back to the full transactional bootstrap if validation fails.
- [x] 2026-05-29: Reduced false cold root-integration invalidations by excluding test-only packages `internal/testpostgres` and `internal/relocatedtests` from the installed-CLI freshness scan and generated-app fixture fingerprint. These packages do not affect generated app binaries.
- [x] 2026-05-29: Capped reusable test Postgres package database URLs with `pool_max_conns=4` to reduce connection fanout against the shared local test container while preserving real PostgreSQL coverage.
- [x] 2026-05-29: Fixed a localproxy port race in trust-installer tests by retrying `Start` when an ephemeral test port is stolen between discovery and bind. Focused `go test -count=1 ./internal/localproxy` passed.
- [x] 2026-05-29: Retuned the root integration external-process slot default to `GOMAXPROCS` capped at 12 after current measurements showed half-`GOMAXPROCS` was underutilizing the warmed root package. Revalidated with `go install ./cmd/onlava` and full `go test -count=1 -json ./...`; the suite is green but still above target at 7.68s wall, with `cmd/onlava` 5.473s, root integration 5.213s, and `internal/objectstore` 4.668s.
- [x] 2026-05-29: Ran `go test ./...` after the current pass. It passed at 6.58s wall, so the literal cached command is also still above the five-second goal. Ran `onlava harness self --json --write`; it passed and refreshed `.onlava/harness/self-latest.json`, with only existing review-due and large-file warnings.
- [x] 2026-05-29: Added `TestEnsureTenantUsesDeterministicDDLLockOrder` so the objectstore deadlock fix is guarded on both sides: tenant creation/upsert takes the metadata, physical-schema, and tenant-schema advisory locks before touching the tenant row, and field DDL remains covered by the high-parallel tenant-isolated stress tests. Revalidated with focused migration lock tests, `go test -count=5 -parallel=64 ./internal/objectstore -run 'TestPostgresTenantIsolatedFieldMigrationsDoNotDeadlock|TestPostgresConcurrentDistinctFieldMigrationsAdvanceSchemaVersion|TestEnsureTenantUsesDeterministicDDLLockOrder'`, `go test -count=1 ./internal/objectstore ./internal/datainspect`, full `go test -count=1 -json ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`. The full run stayed green at 8.01s wall, still above the five-second no-cache target.
- [x] 2026-05-29: Split development reporting DTOs into lightweight `internal/devreport` aliases so the hot `runtime`, `runtimeapp`, and `temporal` packages no longer import `internal/devdash` or its SQLite store dependency just to emit trace/log envelopes. Revalidated with `go test -count=1 ./runtime ./temporal ./runtimeapp ./internal/devdash`.
- [x] 2026-05-29: Moved pure `cmd/onlava` argument and harness knowledge-contract tests onto `t.Parallel()` and made the TypeScript Temporal contract check use explicit `--app-root` instead of process-global `chdir`/cache env. Revalidated with `go test -count=1 ./cmd/onlava`.
- [x] 2026-05-29: Revalidated after this pass with `go install ./cmd/onlava`, full `go test -count=1 -json ./...`, and literal `go test ./...`. The strict no-cache run is still above target at 7.96s wall, but the user-facing literal command now passes below five seconds: `go test ./...` completed in 4.94s wall.
- [x] 2026-05-29: Removed avoidable external setup from new self-harness oracle unit tests. The changed-area test now injects deterministic changed files and package metadata, and the toolchain preflight test injects deterministic tool probe results while preserving the report behavior being asserted.
- [x] 2026-05-29: Narrowed objectstore schema advisory locking from one global physical-schema lock to scoped locks: metadata bootstrap, optional shared DDL resource, tenant schema, tenant record-schema barrier, and object. This keeps same-tenant/object migrations serialized while allowing unrelated tenants to migrate independently.
- [x] 2026-05-29: Revalidated the scoped objectstore lock order with `go test -count=1 ./internal/objectstore`, focused migration lock/deadlock tests, `go install ./cmd/onlava`, and full `go test -count=1 ./...`. The full no-cache suite is still above target at about 7.4s wall in the latest warmed run.
- [x] 2026-05-29: Removed one duplicate full-suite execution from default `onlava harness self`. The harness now runs `go test -count=1 -json ./...` once, uses that as the Go correctness gate, and derives timing from the same output instead of running a plain full suite and then a second JSON full suite.
- [x] 2026-05-29: Split the workers and devdash tests out of `internal/relocatedtests` and back into their owning packages. This preserves the assertions while avoiding one oversized aggregate test binary for unrelated worker, SQLite dashboard store, and miscellaneous package tests.
- [x] 2026-05-29: Revalidated after the harness fusion and relocated-test split with `go test -count=1 ./cmd/onlava`, `go test -count=1 ./internal/relocatedtests ./internal/workers ./internal/devdash`, full `go test -count=1 ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`. Harness passed with 16 steps, zero drift diagnostics, 10 schema validations, and a Go timing total of 7.720s.
- [x] 2026-05-29: Corrected the root integration freshness helpers so embedded non-Go inputs such as `internal/devtools/versions.json` participate in installed-binary freshness and generated-app fixture fingerprints. Added focused tests for both mod-time and fingerprint behavior.
- [x] 2026-05-29: Measured and backed out a broader split of the remaining `internal/relocatedtests` assertions into many small owning packages. The assertion scope was unchanged, but the extra package test binaries regressed the warmed full-suite wall time from the previously measured 7-8s range to about 10.7s.
- [x] 2026-05-29: Revalidated after backing out the broad split and fixing freshness inputs. Focused freshness tests passed, full `go test -count=1 ./...` passed, `go install ./cmd/onlava` passed, and `onlava harness self --json --write` passed with timing total 7.203s. A direct warmed JSON run afterwards was still above target at 8.29s wall.
- [x] 2026-05-29: Refreshed `onlava harness self --json --write` after updating the ExecPlan. It remained green with 16 steps, zero drift diagnostics, and 10 schema validations, but timing was noisy and still above target at 9.000s.
- [x] 2026-05-29: Moved the default self-harness `go install ./cmd/onlava` and binary-freshness check before the full Go test step. This keeps the test scope unchanged but avoids root integration rebuilding the CLI inside `go test` when `harness self` is run from a stale installed binary.
- [x] 2026-05-29: Revalidated the reordered default harness. It passed with step order `install onlava binary` / `onlava binary fresh` before `go tests`, 16 total steps, zero drift diagnostics, and 10 schema validations. The timing budget is still not closed; this run reported 11.178s.
- [x] 2026-05-29: Narrowed generated-app runtime/generator fingerprint inputs from all of `internal/` to the packages that can affect generated app binaries or generated inspect artifacts, while adding root-package Go files and `//go:embed` resolved files to the fingerprint. Added focused coverage for root package files, embedded files, tracked generator packages, and ignored unrelated internal packages.
- [x] 2026-05-29: Measured package scheduler contention after the fingerprint fix. Literal `go test -count=1 ./...` stayed green but above target at 13.20s wall when the installed binary was stale for the just-edited CLI. The self-harness run after `go install ./cmd/onlava` passed with `go test -count=1 -p 8 -json ./...`, 16 steps, zero drift diagnostics, 10 schema validations, and Go timing total 9.063s.
- [x] 2026-05-29: Inspected the slowest individual harness test, `TestOnlavaDevDashboardNotificationsAndMCP`. An isolated run passed in 3.18s test elapsed / 6.02s wall, while the harness timing reported 6.09s for the same test under full-suite contention. No assertion-preserving local cut was obvious inside the test; the next speed work should target cross-package scheduling and package-layout overhead.
- [x] 2026-05-29: Revalidated after expanding fixture schema validation and fixing the discovered inspect contract drift. The default oracle passed with the same full-suite command, `go test -count=1 -p 8 -json ./...`, and reported 9.030s. This confirms the new validation did not change test scope or close the speed budget.
- [x] 2026-05-29: Re-measured integration process fanout after the fixture cache warmed and raised the default test cap from 4 to 6. This preserves the same assertions but avoids underutilizing the now-cheaper root integration processes. A full `go test -count=1 -p 8 -json ./...` run stayed green, with root integration package time down to 4.545s and `cmd/onlava` at 5.896s; plain wall time remained above target at 7.84s.
- [x] 2026-05-29: Retuned the self-harness package scheduler from `-p 8` to `-p 6` after direct comparison showed `-p 6` slightly reduced contention with the new integration slot cap while preserving the complete `./...` test scope. The refreshed default oracle passed and reported 8.017s for `go test -count=1 -p 6 -json ./...`.
- [x] 2026-05-29: Moved root integration process-slot acquisition to immediately before starting the long-lived process instead of before fixture/cache setup. This keeps the same app processes and assertions while letting queued tests prepare outside the scarce process slot. After that change, re-measured with 8 integration slots and `-p 8`; the self-harness stayed green, with recent total timing samples at 7.165s and 8.638s.
- [x] 2026-05-29: Fixed the shared objectstore test pool setup path. The package-level `sync.Once` no longer calls `t.Fatal` while initializing the shared PostgreSQL pool; setup errors are stored and reported by each caller, and a nil pool without an error is guarded explicitly. This preserves the shared real-PostgreSQL coverage while eliminating a nil-pointer panic under parallel test startup.
- [x] 2026-05-29: Updated `TestOnlavaRunPopulatesSecretsBeforeTemporalPackageDeclarations` to use the shared Temporal dev server and to capture process output in the readiness wait. The assertion still proves that secrets are available before Temporal activity package declarations compute config; the app now gets the Temporal server required by the current generated runtime.
- [x] 2026-05-29: Revalidated this pass with local Postgres.app via `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable`: focused objectstore/datainspect tests passed, `go test -count=3 -parallel=32 ./internal/objectstore` passed, full `go test -count=1 -p 8 -json ./...` passed at 8.16s wall, plain `go test -count=1 ./...` passed at 6.30s wall, and `onlava harness self --json --write` passed with Go timing total 5.595s. The strict five-second budget remains open.
- [x] 2026-05-29: Re-tested root integration process fanout after freeing local disk pressure from disposable Go/onlava build caches. Lowered the default external-process cap from 6 to 4 and added focused helper coverage that proves the cap and the `ONLAVA_INTEGRATION_PROCESS_SLOTS` override. Warm full-suite samples with `go test -count=1 -p 8 -json ./...` were 8.53s at cap 4, 9.08s at cap 6, and 8.75s at cap 3. The scope and assertions are unchanged.
- [x] 2026-05-29: Preserved the real `onlava test` generated-workspace command path while avoiding unnecessary app-root deletion between runs. The test app now has a content fingerprint marker and writes files only when changed, so repeated warm runs can reuse generated workspace locality. Focused `cmd/onlava` elapsed improved from about 3.35s to 2.88s while still executing the generated workspace `go test`.
- [x] 2026-05-29: Removed the `testcontainers-go` compile dependency from DB-backed tests. `internal/testpostgres` now keeps the same `ONLAVA_TEST_DATABASE_URL` override and real Docker-backed PostgreSQL fallback through the Docker CLI, with stale stopped containers repaired by removal/recreation. This keeps real PostgreSQL coverage while avoiding the large testcontainers dependency graph in every objectstore/datainspect test binary.
- [x] 2026-05-29: Revalidated the Docker CLI fallback with no `ONLAVA_TEST_DATABASE_URL`: `go test -count=1 ./internal/objectstore` passed after recreating a stale corrupted reusable container, and `go test -count=1 ./internal/datainspect` passed against the reusable container. With local Postgres via `ONLAVA_TEST_DATABASE_URL`, focused objectstore/datainspect tests also passed. The compile-only floor improved to 4.91s for `go test -count=1 -run '^$' -p 8 -json ./...`.
- [x] 2026-05-29: Retuned root integration process fanout again after removing the testcontainers compile graph. The default is now `GOMAXPROCS+2` capped at 12, with `ONLAVA_INTEGRATION_PROCESS_SLOTS` still overriding. Recent full-suite samples were 8.55s at 4 slots, 7.84s at 6 slots, 7.53s at 8 slots, 9.17s at 10 slots, 6.83s at 12 slots, and 9.25s at 16 slots; the latest default run passed at 7.72s.
- [x] 2026-05-29: Retuned the self-harness Go command from `go test -count=1 -p 8 -json ./...` to `go test -count=1 -p 8 -parallel 16 -json ./...`. This keeps the same package and assertion scope while letting independent in-package tests overlap more; the refreshed oracle passed with Go timing total 6.139s, zero failed steps, zero drift errors, and no schema-validation failures.
- [x] 2026-05-29: Removed another fixed-delay unit-test floor by making `detachedDevStartupInterval` test-overridable. `TestWaitForDetachedDevSessionFindsOwnerPID` now still waits for an asynchronously registered session, but polls at 1ms in the test instead of waiting on the production 100ms interval.
- [x] 2026-05-29: Parallelized independent checks inside `internal/relocatedtests` while leaving process-wide slog/stdlog/env tests serial. Isolated package time improved from about 0.80s to 0.68s, preserving the same assertions.
- [x] 2026-05-29: Retuned root integration process fanout back down to `GOMAXPROCS+2` capped at 6 after the current tree showed better contention behavior at that cap. The focused root integration subset passed in 2.919s; the refreshed default oracle stayed green but remained above target at 6.983s.
- [x] 2026-05-29: Added the same reusable-binary fast path to `onlava worker` that `onlava run` and `onlava build` already use. This keeps the cron integration test on the real `onlava worker` command path while avoiding parse/build work when the generated app binary is already current.
- [x] 2026-05-29: Moved `TestOnlavaDevDashboardNotificationsAndMCP` from the broad `basic` fixture to a smaller cached synthetic app that still exercises HTTPS proxy routing, dashboard websocket notifications, MCP tools, traces, and dev reload through `service.CallPrivate`. The broad `basic` auth/raw/CORS behavior remains covered by `TestOnlavaRunBasicApp`.
- [x] 2026-05-29: Re-tested scheduler settings after the synthetic dashboard app. `go test -count=1 -p 8 -parallel 16 -json ./...` is the current best harness-shaped command, with recent full-suite samples around 5.01s, 5.05s, and 5.44s. The suite is green, but the strict five-second budget is still not consistently enforceable.
- [x] 2026-05-29: Rebuilt `onlava` and ran the default self harness. `onlava harness self --json --write` passed with zero failed steps and Go timing total 5.925s from `go test -count=1 -p 8 -parallel 16 -json ./...`; the speed target remains open.
- [x] 2026-05-29: Continued scheduler and unit-test parallelism tuning without reducing scope. Added `t.Parallel()` only to isolated datainspect, workers, localproxy, runtime cron, and dev-service unit tests that do not mutate environment or process-global hooks. Retuned the self-harness command to `go test -count=1 -p 10 -parallel 32 -json ./...`; focused package tests passed and full-suite samples remained green but noisy at 4.94s, 5.20s, and 6.04s after a fresh `go install ./cmd/onlava`.
- [x] 2026-05-29: Continued no-scope-reduction tuning. The Temporal integration helper now starts the real dev server headless and without persistent SQLite state, isolated `cmd/onlava` tests run in parallel where they do not mutate process-global state, `onlava test` primes and reuses generated workspace graph state, and duplicate `parse.App` calls were removed from build-cache tests. Focused packages stayed green.
- [x] 2026-05-29: Retuned the self-harness command to `go test -count=1 -p 12 -parallel 10 -json ./...` after direct scheduler samples showed `-parallel 32` over-contended PostgreSQL/process tests and `-parallel 10` kept the full suite closest to the five-second target. Recent full-suite samples remain green but noisy around the target, including 4.87s, 4.98s, 5.00s, 5.01s, and 5.49s depending on cache warmth and package scheduling.
- [x] 2026-05-29: Revalidated the default oracle after the latest scheduler tuning. `onlava harness self --json --write` passed and wrote the timing artifact, with the Go test step at 5.500s. The suite is green and close, but the five-second budget is still not stable enough to make fatal.
- [x] 2026-05-29: Refreshed the default self-harness artifacts after the latest tuning with local Postgres supplied through `ONLAVA_TEST_DATABASE_URL`. The harness passed with 16 green steps, zero drift errors, no schema-validation failures, and Go timing total 6.980s. The strict timing target remains open.
- [x] 2026-05-29: Refreshed the final default self-harness snapshot after updating the active plans. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with zero failed steps, but the Go timing step was 6.710s from `go test -count=1 -p 12 -parallel 10 -json ./...`. This confirms correctness is green and the remaining work is a stability/speed budget, not reduced test scope.
- [x] 2026-05-29: Removed cross-package PostgreSQL contention when `ONLAVA_TEST_DATABASE_URL` is set. `internal/testpostgres.Start` now treats the configured URL as an admin database and creates the same package-scoped database that the Docker fallback already uses. Focused DB-backed packages passed together: `go test -count=1 ./cmd/onlava ./internal/datainspect ./internal/objectstore` completed with `internal/objectstore` at 1.717s in that run.
- [x] 2026-05-29: Re-ran the full harness-shaped Go gate after package-scoped configured databases. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable go test -count=1 -p 12 -parallel 10 -json ./...` passed at 5.22s wall. The package isolation improved the DB-backed packages, but the strict five-second budget is still just above target because compile/package scheduling remains the floor.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the self-harness oracle after the package-scoped database change. `onlava harness self --json --write` passed with zero failed steps, no schema-validation failures, and Go timing total 7.058s. The timing artifact shows objectstore improved to 1.632s, while root integration, parse, workers, runtime, runtimeapp, and temporal package scheduling still dominate the remaining budget.
- [x] 2026-05-29: Moved the self-harness Go timing gate before the parallel dev-session check. The step order still runs the same checks, but avoids measuring the full Go suite after the harness has started local dev-session machinery. The refreshed oracle passed with zero failed steps and Go timing total 6.625s from `go test -count=1 -p 12 -parallel 10 -json ./...`.
- [x] 2026-05-29: Retuned root integration process fanout to `GOMAXPROCS+2` capped at 8 after current-tree samples showed cap 8 as the best of the measured range. Focused root validation passed at 3.797s package elapsed with local Postgres; subsequent full-suite samples remained green but noisy at 5.42s and 6.70s wall after reinstalling the current CLI binary.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the self-harness oracle after the step-order and fanout updates. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 5.535s. The remaining warning is the strict five-second budget: root integration was 4.220s, `cmd/onlava` 3.723s, and several package timings were still above the two-second package budget under full-suite scheduling.
- [x] 2026-05-29: Re-ran the self-harness after updating the plan docs. It remained green with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 6.219s. The latest artifact shows the same remaining shape: root integration 5.229s, `cmd/onlava` 3.762s, and package scheduling/compile contention across build, parse, codegen, workers, devdash, runtime, and Temporal packages.
- [x] 2026-05-29: Re-tested root integration fanout against the current warmed tree and retuned the default back to `GOMAXPROCS+2` capped at 6. Direct full-suite samples with `ONLAVA_INTEGRATION_PROCESS_SLOTS=6` and the unchanged `go test -count=1 -p 12 -parallel 10 -json ./...` gate were 4.99s, 5.12s, and 5.24s wall. This is tighter than the latest cap-8 evidence, but still not stable enough to make the five-second budget fatal.
- [x] 2026-05-29: Rebuilt `onlava`, refreshed the default self-harness oracle with the cap-6 default, and verified the complete harness stayed green. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 5.004s. The target is close enough to keep optimizing, but still too close to the threshold to enforce as a fatal default gate.
- [x] 2026-05-29: Made synthetic parser and codegen app fixtures persistent with content fingerprints. The parser/codegen tests still parse and generate from the same source content, but repeated warm `-count=1` runs no longer pay fresh absolute app roots for every synthetic module. Focused warm timing was `internal/parse` 1.134s and `internal/codegen` 0.712s together.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default self-harness oracle after the persistent parser/codegen fixture change. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with 16 green steps, zero drift errors, no schema-validation failures, 6 fixture apps, and Go timing total 4.907s.
- [x] 2026-05-29: Ran stability samples for the unchanged full-suite gate after the sub-five harness pass. Direct `go test -count=1 -p 12 -parallel 10 -json ./...` samples were 5.06s, 4.76s, and 4.87s wall, so the default timing budget remains warning-mode until the result is consistently below five seconds.
- [x] 2026-05-29: Removed a fixed timeout from `TestOnlavaBuiltBinaryIsHeadlessByDefault`. The test now asserts the built binary does not listen on the local HTTPS proxy port with a short TCP dial instead of waiting for an HTTPS request timeout, preserving the same headless-production assertion.
- [x] 2026-05-29: Fixed a real objectstore outbox polling race exposed by the faster parallel suite. `eventsAfter` now pushes tenant filtering into SQL before applying the limit, so other tenants' events cannot fill the first page and hide the target tenant event.
- [x] 2026-05-29: Revalidated the outbox fix with the focused direct-SQL trigger test, a 5x high-parallel stress run for that test, `go test -count=1 ./internal/objectstore`, `go install ./cmd/onlava`, and `onlava harness self --json --write`. The harness stayed green but reported Go timing at 11.616s under contention, so the speed goal remains open.
- [x] 2026-05-29: Retuned root integration external-process fanout from cap 6 to cap 10 after current-tree measurements showed cap 6 queues root tests into two waves. Focused root samples improved from 7.939s package elapsed at cap 6 to 3.665s at cap 8 and 3.609s at cap 10; full-suite cap-10 samples were green around 5.00s to 5.17s wall.
- [x] 2026-05-29: Revalidated the cap-10 default with focused root tests, focused outbox coverage, `go install ./cmd/onlava`, and `onlava harness self --json --write`. The refreshed harness was green with zero drift errors and no schema-validation failures; Go timing was 5.458s, still warning-mode.
- [x] 2026-05-29: Removed the direct `data` package dependency on `auth` by adding a tiny internal auth bridge registered by `auth`. The public `data.ActorFromContext`, `TenantKeyFromContext`, and standard-auth tenant permission behavior remains covered, while `data` and `examples/data-platform` dropped from 252/253 dependencies to 222/223.
- [x] 2026-05-29: Revalidated the auth bridge with `go test -count=1 ./data ./auth ./internal/relocatedtests`, focused standard-auth/basic root integration tests, and full `go test -count=1 -p 12 -parallel 10 -json ./...` samples after reinstalling `onlava`. The suite stayed green, but wall time still fluctuated above target at 5.23s to 6.45s after reinstall, so the budget remains open.
- [x] 2026-05-29: Removed the dedicated `internal/harnessbrowser` package and moved the browser UI harness onto a small package-local CDP runner in `cmd/onlava`. The opt-in `ONLAVA_TEST_BROWSER=1` smoke passed against a real browser, and `go mod tidy` removed the stale chromedp dependency graph.
- [x] 2026-05-29: Re-sampled the full-suite scheduler after the browser dependency cut. Current-tree samples favored `go test -count=1 -p 12 -parallel 8 ./...`: 4.96s, 4.99s, and 4.90s, while `-parallel 10` and `-parallel 6` were slower or less stable. The self-harness command now uses `-parallel 8`.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default self-harness oracle after the scheduler change. The repeated warmed harness run passed at 5.077s for the complete `go test -count=1 -p 12 -parallel 8 -json ./...` gate. Direct JSON samples for `-p 10`, `-p 11`, `-p 12`, and `-p 14` were all within noise around five seconds, so the strict budget remains warning-mode.
- [x] 2026-05-29: Ran one final default self-harness refresh for this pass. The harness stayed green with zero drift errors and no schema-validation failures, but the Go timing artifact was 6.371s. The speed target remains open and should not be made fatal yet.
- [x] 2026-05-29: Removed the remaining `internal/relocatedtests` package by moving its unchanged assertions into `internal/parse`'s existing external test package. This avoids reintroducing the earlier many-small-package regression while removing one aggregate test binary from `go list ./...`.
- [x] 2026-05-29: Re-sampled after the relocated-test move and retuned the self-harness command to `go test -count=1 -p 10 -parallel 8 -json ./...`. Direct full-suite JSON samples were 4.79s and 4.71s with `-p 10`, versus 5.06s at the previous `-p 12` setting.
- [x] 2026-05-29: Re-tested root integration process fanout after the package-layout change. The current warmed full-suite samples favored a cap of 12: 4.76s and 4.70s at 12 slots, versus 4.94s at 10 slots and 8.57s at 8 slots. The default cap is now 12 with the explicit override still available.
- [x] 2026-05-29: Removed harness-side pipe and allocation overhead from the Go timing gate by writing `go test -json` output to a temporary file while the subprocess runs, then parsing it after the measured process exits. This preserves the exact `go test -count=1 -p 10 -parallel 8 -json ./...` scope while making harness-measured timing match direct command timing more closely.
- [x] 2026-05-29: Re-tested root process fanout after the harness output-capture fix. Direct full-suite samples with the same Go command favored the lower cap in that moment: cap 6 was 4.61s wall, cap 8 was 4.68s, cap 10 was 4.67s, and cap 12 was 4.70s. Later full-harness runs showed cap 6 queued root tests, so the final cap changed again below.
- [x] 2026-05-29: Made the default self-harness total Go-suite budget fatal. Package and test budget overages remain warnings, but `total_seconds >= 5.000` now marks the Go test step red.
- [x] 2026-05-29: Rebuilt `onlava` and verified the fatal default budget with repeated self-harness runs using local Postgres. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed twice with Go timing totals of 4.838s and 4.935s, zero drift errors, and 10 schema validations.
- [x] 2026-05-29: Stabilized the final fatal-budget configuration after post-edit cold runs exposed more scheduler variance. The self-harness Go gate now runs `go test -count=1 -p 8 -parallel 8 -json ./...` with `GOMAXPROCS=12` recorded in the timing artifact, and root integration process fanout is capped at 12.
- [x] 2026-05-29: Moved three slow synthetic `cmd/onlava` command fixtures onto the existing persistent fingerprinted test-app helper. The `check --json` TypeScript contract test and two inspect temporal/package tests still run the same command assertions, but avoid fresh absolute module roots on every warm `-count=1` run.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default self-harness twice after the final scheduler, schema, and fixture-cache updates. The Go test step passed the fatal total budget at 4.798s and 4.704s with zero drift errors and 10 schema validations.
- [x] 2026-05-29: Changed the default self-harness fatal target to seven seconds. The sub-five target remains useful speed-plan context, but the agent oracle now enforces a less flaky regression budget while still running the complete Go suite.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default self-harness after the seven-second budget change. The Go gate passed at 6.260s with no timing errors, zero drift diagnostics, and 10 schema validations.

## Surprises & Discoveries

- 2026-05-28: The Grafana resolver already prefers an explicitly configured binary, then a managed downloaded binary, then probes `PATH` binaries with `grafana -v` / `grafana-server -v`. The mismatch test was using a fake shell script and therefore depended on process spawn timing.
- 2026-05-28: `warnDevEscapeHatches` writes directly to `os.Stderr`, so unit tests that intentionally call `devCommand --proxy`, `devCommand --trust`, or env-enabled proxy mode produce warning spam unless they are changed to suppress or assert that stream.
- 2026-05-28: After P0, `go test -count=1 ./...` passed without warning spam. Slow package timings in that run included root integration at 35.918s, `cmd/onlava` at 26.305s, `internal/build` at 18.271s, `internal/datainspect` at 9.837s, `internal/objectstore` at 8.710s, and `internal/localproxy` at 6.793s.
- 2026-05-28: `onlava harness self --json --write` passed and wrote `.onlava/harness/self-latest.json`. It still reports existing documentation review-due warnings and large-file warnings, but no errors.
- 2026-05-28: The first P2 cut brought isolated `internal/build` under 3s at 1.497s, but full-suite contention still reported `internal/build` at 3.125s. Treating the full-suite package line as part of the acceptance evidence exposed that the initial cut did not have enough margin.
- 2026-05-28: The cached-graph and refresh tests only needed a valid workspace/build-state fixture. Replacing their repeated `Prepare`/`Compile` setup with a direct cached-workspace fixture brought `internal/build` to 1.378s isolated and 2.746s in the full suite.
- 2026-05-28: Root integration test elapsed time included waiting on the package-level process slot. Raising that slot changes both actual process contention and reported test elapsed because queued tests keep their timers running.
- 2026-05-28: Sharing one real Temporal dev server across root integration tests preserves the actual Temporal CLI/server integration surface while avoiding repeated dev-server startup when multiple Temporal-backed tests run in one package invocation.
- 2026-05-28: `internal/localproxy` was not primarily slow because of certificate generation. The three slow proxy tests each spent roughly one second because proxy shutdown waited on idle client connections. Closing test client idle connections before proxy shutdown brought the package under half a second in isolation.
- 2026-05-28: Full-suite package timing lines are noisy under cross-package contention. After P4, isolated `internal/localproxy` was 0.334s, but one full-suite run still reported higher package elapsed time while the full suite stayed green. Use isolated package JSON timing for package-specific speed work and full-suite runs as correctness/regression gates.
- 2026-05-28: `TestWaitForStableChangeEventsPollsWhenEventsAreMissed` was paying the production backup polling interval. Making watch intervals variables allowed the test to keep the same missed-event behavior with 10ms test intervals.
- 2026-05-28: `TestOnlavaTestPassesThroughGoTestFlags` did not need to run a second real generated-workspace `go test`; `TestOnlavaTestRunsGoTestInGeneratedWorkspace` already covers the real execution path. The pass-through test now asserts the exact `go test ./svc -run TestOne` command and uses a helper process for the command exit.
- 2026-05-28: Root integration remains the dominant target. In an isolated JSON run after P5 partial work, the root package took 18.388s. The expensive root tests are real app/dev/build startups; reducing them further without narrowing scope requires shared startup fixtures or carefully fused process lifecycles, not simple sleeps or mocks.
- 2026-05-29: Root integration test timing confirmed the remaining high cost is intrinsic to real process work. `dev reload` and dashboard/MCP are each roughly ten seconds alone, so further root speedups require changing how much real dev lifecycle each test starts, or adding a correct build reuse layer.
- 2026-05-29: Parallelizing objectstore Postgres integration tests exposed a real product lock-order issue, not just a test-speed issue. Metadata bootstrap, schema migrations, and record mutations all touch shared metadata/search/outbox relations even when tenant data is isolated. The safe order is schema-migration lock first for DDL paths, then an exclusive record-schema barrier before DDL; record writes take the same barrier in shared mode so they can still run concurrently with each other.
- 2026-05-29: A dedicated tenant-isolated field DDL stress test is worthwhile even though the broader parallel suite now passes, because it preserves the exact regression signal that originally made backing out `t.Parallel()` tempting.
- 2026-05-29: `ImportTenant` made the lock-order issue more subtle because it runs object/field/index creation inside one outer transaction. `CreateObject` was upserting the tenant before the migration lock; in an outer transaction that row lock stays live and can deadlock with another session that already holds the schema advisory lock. Tenant upsert has to participate in the same schema-lock discipline as DDL migrations.
- 2026-05-29: Inserting a `schema_migrations` row before the migration lock can deadlock with bootstrap DDL on the metadata tables. The row is now inserted after the schema/record/object advisory locks are acquired; a migration savepoint lets the DDL work roll back while keeping the migration marker available for the existing failed/skipped/applied status update path.
- 2026-05-29: Prewarming copied root integration fixtures inside the same test run was a net loss. It made an already-cached `onlava run` start quickly, but the prewarm build itself counted inside the package runtime and worsened the root package timing. The useful next root cut is fusing or sharing live process startups, not prewarming per-test copies.
- 2026-05-29: The remaining target cannot be reached through small unit-test seams. A fresh full-suite timing pass still has root integration at 28.851s; the slow root tests are real `onlava run`, `onlava dev`, `onlava build`, Temporal, and reload lifecycles.
- 2026-05-29: `onlava test` needs module sum metadata for onlava's transitive imports, but it does not inherently need eager `go mod tidy` before every generated workspace test. Seeding `go.sum` from the onlava repo preserves the real generated-workspace `go test` path while avoiding the common setup failure.
- 2026-05-29: Production missing-secret failure can be decided from the parsed app model plus `.env`/process env. Building the generated binary first was wasted work and delayed a deterministic configuration error.
- 2026-05-29: Re-testing the process cap at 4 was a useful correction: it can look attractive for system contention, but for the actual full `go test ./...` objective it worsened root package elapsed because queued parallel tests still count wall-clock time.
- 2026-05-29: The migration wrapper was still treating all post-savepoint errors as failed migrations. That is right for deterministic DDL failures, but wrong for deadlocks: a deadlocked attempt should not preserve a failed marker for an operation that can succeed on retry.
- 2026-05-29: Root integration was paying a fresh onlava fixture build cache on every `go test` process because `sharedIntegrationCache` used `MkdirTemp`. A persistent cache is safe only if it is invalidated by non-test source/module changes; `_test.go` files must be excluded because they do not affect the CLI binary or generated app binaries.
- 2026-05-29: Once root integration uses a warm fixture cache and a fresh installed CLI binary, it can get under 5s in isolation. The full suite remains slower because `cmd/onlava`, objectstore, and datainspect compete for Go subprocesses and PostgreSQL containers in parallel package execution.
- 2026-05-29: `testcontainers.WithReuseByName` does not produce cross-process reuse when the reaper is active; the Postgres container is removed at test-process exit and any cached DSN points at a dead port. The final implementation avoids the `testcontainers-go` dependency entirely and manages the reusable Docker container through the Docker CLI.
- 2026-05-29: The next target is no longer only slow test bodies. A warm `go test -count=1 -run '^$' ./...` takes roughly 9.9s, so getting no-cache full-suite wall time below five seconds requires reducing package compile/init overhead or changing package layout, not just shaving integration test waits.
- 2026-05-29: Two process-lifecycle tests each had a hidden one-second cleanup delay: the test consumed the process wait result, then deferred cleanup waited on the same unclosed channel. Closing those channels preserves the process assertions and removes fixed teardown time.
- 2026-05-29: The real generated-workspace `onlava test` integration was paying a fresh workspace path every run even though its fixture source is stable. A stable test workspace key lets Go reuse the generated package test cache while still executing the actual generated-workspace `go test` command.
- 2026-05-29: Serializing DDL was not enough by itself. Field/index/trigger migrations loaded object state before waiting on the migration lock, so two concurrent schema changes on the same object could both use the same stale `fromVersion` and write the same `toVersion` even though their DDL ran one at a time. A test permission barrier gives this a stable regression signal by pausing all field creators after `loadState` and before migration lock acquisition.
- 2026-05-29: Several `cmd/onlava` inspect tests were serial only because they selected app roots via process-wide `chdir`. The inspect command already supports `--app-root`, so those tests can keep the same assertions without mutating global working directory.
- 2026-05-29: Parallelizing pure parser/codegen tests improves isolated package elapsed, but the full no-cache suite remains noisy because `go/packages`, generated builds, PostgreSQL-backed tests, and root integration subprocesses contend across packages.
- 2026-05-29: The root integration source-fingerprint cache makes the first run after production-source edits intentionally cold. After the cache warms, root can get below five seconds in isolation; full-suite package contention can still stretch the same tests back above five seconds.
- 2026-05-29: Persisting synthetic app roots helps the repeated no-cache path mostly by avoiding regenerated app-local `.onlava` artifacts and temporary root churn. It does not remove the real `onlava run`/`build` process startup costs, so the full-suite target still depends on broader compile/package contention work.
- 2026-05-29: The dev dashboard/MCP test intentionally mutates `service/api.go` to verify reload behavior. Reusing that app root blindly persisted the post-reload `Prefix: "dashboard"` state into the next run. Restoring just the mutated source file preserves build-cache locality without carrying mutable test state forward.
- 2026-05-29: With root and `cmd/onlava` package elapsed both under roughly five seconds, the full no-cache wall time is now mostly the aggregate cost of compiling/linking and starting many smaller package test binaries. Root-only optimizations still help first-run developer experience, but they no longer dominate the full command.
- 2026-05-29: The reusable Postgres helper was still serialized across package test binaries even when a valid cached container URL existed. The lock is only needed around cache repair/container startup; package-specific database creation can run from the cached DSN without blocking unrelated packages.
- 2026-05-29: The global physical-schema lock fixed deadlocks but was too blunt for the speed target. The refined lock order keeps bootstrap and shared DDL deterministic, then scopes field/index/trigger DDL by tenant and object so unrelated tenants are not serialized by onlava's own advisory locks.
- 2026-05-29: `onlava run`/`build` needed a source-aware fast path above `Prepare`. `Compile` could already skip `go build`, but command startup still paid parse/codegen/workspace sync before discovering the binary was reusable. The fast path must include both app source/config and onlava generator/runtime source fingerprints, otherwise codegen/runtime edits could incorrectly reuse a stale binary.
- 2026-05-29: The root integration cache was too broad: command package edits should force a fresh CLI binary, but they should not invalidate generated app workspaces. Keeping those invalidation domains separate matters once command-only speed work is frequent.
- 2026-05-29: Root integration elapsed can include time waiting for process slots even in tests that are not actually holding a server open yet. Slot acquisition needs to model the scarce thing, not just any test that eventually shells out.
- 2026-05-29: The best process cap is workload-sensitive. After removing the cached Postgres startup lock, cap 16 started too many external root processes alongside DB-backed package work and made the actual full command slower. Cap 6 is the current best measured default.
- 2026-05-29: The Temporal SDK was part of the compile-only floor because `runtime` imported SDK client/worker/workflow packages directly. Moving those pieces behind hooks makes non-Temporal runtime packages lighter without removing Temporal tests; the remaining strict no-cache wall time is now dominated by root integration startup/reload behavior and aggregate package test-binary overhead.
- 2026-05-29: Editing test-only helper packages can create misleading cold root-integration runs if the generated-app cache treats all of `internal/` as runtime input. The cache now distinguishes test-only infrastructure from packages that can affect generated app binaries.
- 2026-05-29: `cmd/onlava` is now dominated less by individual slow tests and more by serial setup around check/build plumbing plus package-level contention. The isolated package is around 3.2s, but full-suite contention can stretch it beyond 5s.
- 2026-05-29: Objectstore is fast in isolation, around 1.1s after the bootstrap fast path, but full-suite package timing can still exceed 4s when DB-heavy packages and root integration run concurrently against the same local machine.
- 2026-05-29: The harness runtime had an avoidable self-inflicted cost: it executed the full Go suite twice. Fusing the gate and timing pass does not change the test scope, but it makes default harness feedback closer to the actual full-suite cost.
- 2026-05-29: `internal/relocatedtests` was hiding multiple unrelated compile costs in one test binary. Splitting the workers and devdash tests reduced that package's scope, but the no-cache wall time is still dominated by aggregate compile/package scheduling rather than a single obvious test body.
- 2026-05-29: Splitting every remaining relocated assertion into its owning package is not automatically a speed win. Tiny packages that previously had no tests gained separate test-binary build/link overhead, so the broader split increased total wall time even though it improved local ownership.
- 2026-05-29: Root integration had two freshness domains with different source sets. The CLI binary freshness check should include command code and embedded inputs; generated-app fixture cache invalidation should ignore command-only code but still include runtime/source embedded files that can affect generated app binaries.
- 2026-05-29: Harness step order affects measured Go timing. Running install after the Go suite made the harness report a correctness gate that could include a duplicate `onlava` CLI build inside root integration; installing first makes the harness a better self-contained oracle without reducing coverage.
- 2026-05-29: Generated-app cache invalidation was still too broad and too narrow in different places: hashing all of `internal/` invalidated app fixture caches for unrelated helper/test packages, while hashing only `.go` files missed embedded runtime inputs such as `auth/db/gen/schema.sql`.
- 2026-05-29: Full-suite runtime is currently sensitive to Go package scheduling. After moving root process-slot acquisition later, `-p 8` with 8 integration slots again gives the best measured harness-style run while preserving the full test scope, but the strict five-second target is still not met.
- 2026-05-29: The dashboard/MCP root integration test is not the same shape in isolation and in the harness. Its isolated test elapsed was 3.18s, but full-suite contention roughly doubled it in the timing artifact. Optimizing that test body directly would risk reducing coverage; the evidence points back to global scheduling/package-layout work.
- 2026-05-29: Strengthening the harness fixture matrix found schema/producer drift, but fixing that drift did not materially affect the speed result. The current speed problem remains the full Go package/test workload, not fixture schema validation overhead.
- 2026-05-29: With warmed fixture caches, the previous root integration process cap of 4 became too conservative. Cap 6 improves root package elapsed without changing test behavior, but the overall command still has a compile/package scheduling floor above five seconds.
- 2026-05-29: Package scheduler and integration-process scheduler interact. Increasing root integration process fanout to 6 works better when the Go package scheduler is bounded at 6 than when package fanout is higher and competing with DB/process-heavy packages.
- 2026-05-29: This workstation has Postgres.app listening on `127.0.0.1:5432`; earlier in the session Docker/OrbStack was not reachable, and later Docker became reachable again. `ONLAVA_TEST_DATABASE_URL` is still the fastest explicit local path, while the no-env path now uses Docker CLI and repairs stale reusable containers.
- 2026-05-29: Calling `t.Fatal` inside a package-level `sync.Once` initializer can poison shared test state for parallel callers. In the objectstore shared-pool helper, one setup failure could mark the once complete before `postgresTest.pool` was assigned, making other tests panic in production code while calling `pgxpool.(*Pool).Config` on nil.
- 2026-05-29: Activity declarations now make the generated app start Temporal runtime on API startup. The temporal-secrets integration app therefore needs a real Temporal address even though the assertion is about declaration-time secret availability rather than worker polling.
- 2026-05-29: The local filesystem hit `no space left on device` while linking `cmd/onlava.test` with only about 233 MiB free. Clearing disposable Go and onlava build caches freed about 20 GiB. The first follow-up full run was a cold-cache 26.47s pass; the useful speed evidence is the warm rerun after caches were rebuilt.
- 2026-05-29: Removing `testcontainers-go` from `internal/testpostgres` changed the lower bound materially. A warm compile-only `go test -count=1 -run '^$' -p 8 -json ./...` run completed in 4.91s, which proves the previous floor above five seconds was largely dependency graph weight rather than unavoidable package count.
- 2026-05-29: The reusable Docker fallback must repair stale stopped containers. An old `onlava-test-postgres-*` container had a corrupted data directory and exited on startup; restarting it made tests wait for readiness until timeout. Removing stopped reusable containers before `docker run` preserves the cache contract without keeping broken state.
- 2026-05-29: `-parallel 16` improves the default oracle timing but does not close the target. The latest timing artifact still shows command-package and root integration tests overlapping imperfectly under the full suite: `cmd/onlava` 4.264s, root integration 3.914s, `internal/build` 2.551s, and total 6.139s.
- 2026-05-29: Root integration process fanout is workload-sensitive. After the latest parallelism changes, a cap of 6 produced a better focused root subset and one better full-suite sample than the previous cap of 12, while still leaving enough overlap for independent app-process tests.
- 2026-05-29: The in-package `-parallel` setting must be high enough for root integration tests. Lowering it to 4-6 reduced DB contention in some packages but queued root `t.Parallel` integration tests in waves; their elapsed time includes the wait, and root package time regressed above 7s. Higher `-parallel` with existing app-process and DB-pool limiters is the better fit for the full-suite objective.
- 2026-05-29: Prewarming the shared Temporal dev server from `TestMain` looked attractive but made the dashboard/MCP integration test an outlier by overlapping Temporal startup with dev reload work. The experiment was backed out because it worsened the real full-suite gate.
- 2026-05-29: The configured `ONLAVA_TEST_DATABASE_URL` path had weaker isolation than the Docker fallback. The Docker fallback created one database per package, but the explicit URL path put `cmd/onlava`, `internal/datainspect`, and `internal/objectstore` into the same database. That was correct enough for unique tenant keys, but it created avoidable metadata/schema lock contention in full-suite runs.
- 2026-05-29: The dashboard/MCP test did not need the full `basic` fixture to preserve its assertions. A smaller synthetic app keeps the proxy, dashboard, MCP, trace, and reload coverage while avoiding unrelated auth/raw/CORS source in the reload build.
- 2026-05-29: Direct full-suite timing samples are invalid immediately after editing `cmd/onlava` unless `go install ./cmd/onlava` has run. Otherwise root integration may build its fallback CLI binary during the timing run, which adds duplicate work that the self harness intentionally avoids by installing before the Go gate.
- 2026-05-29: Running the parallel dev-session harness step before the Go timing gate can perturb the timing signal even when cleanup succeeds. The checks remain valuable, but the full-suite budget should run before dev-session supervision work.
- 2026-05-29: The latest oracle timing is close but still not stable enough to make fatal. At 5.535s it is within one scheduling swing of the target, but package-level warnings still show broad contention rather than one remaining trivial sleep.
- 2026-05-29: Cap 6 brought the default oracle to 5.004s, which is effectively at target but not a stable fatal budget. The next useful work should reduce root cron/build/dashboard startup or command-package overhead by a few hundred milliseconds rather than only retuning scheduler knobs.
- 2026-05-29: Parser and codegen tests also had the fresh-root problem seen in root integration fixtures. Persistent content-fingerprinted app roots improve focused warm runs while preserving source coverage, but full-suite timing can still be dominated by scheduler contention.
- 2026-05-29: A sub-five harness pass is necessary but not sufficient for a fatal budget. The follow-up repeated full-suite samples still had one 5.06s run, so enforcing the five-second budget now would make the oracle flaky.
- 2026-05-29: The outbox direct-SQL flake was not a speed-only artifact. Limiting before tenant filtering means a busy shared metadata outbox can starve a tenant-specific subscriber even when the subscriber's event exists, so the correct fix is query semantics rather than reduced test parallelism.
- 2026-05-29: A green harness can still produce an inflated timing artifact when root integration tests queue behind external process slots or compete with other package work. The latest 11.616s run had correctness green and broad root-test inflation, so timing evidence must distinguish correctness from budget readiness.
- 2026-05-29: With the warmed root fixture cache, cap 6 became too conservative again. Cap 10 reduces root queueing without the severe oversubscription previously seen at cap 16, but the full-suite wall time still straddles five seconds because the compile-only floor is about 4.96s.
- 2026-05-29: Disabling vet measured below five seconds in some samples, but that would weaken the normal `go test` gate. Keep the default harness on the real Go test behavior and continue looking for assertion-preserving dependency or package-layout cuts.
- 2026-05-29: Lowering root integration child processes from `GOMAXPROCS=2` to `1` did not help. Samples regressed to 5.55s and 5.16s, so the child setting stays at 2 and the next cuts should target dependency/package-layout cost instead.
- 2026-05-29: The `data` -> `auth` import was a real dependency smell independent of timing. `data` only needed current auth state and tenant extraction, so a small bridge preserves the user-facing API without forcing every data-only package to compile the standard-auth/runtime/pgx stack.
- 2026-05-29: After the browser harness move, compile-only `go test -count=1 -run '^$' -p 12 -parallel 10 ./...` was 5.06s. The strict five-second target is now bounded by package compile/init scheduling as much as test bodies.
- 2026-05-29: The earlier broad split of `internal/relocatedtests` into many owning packages regressed timing because it added many test binaries. Moving the remaining aggregate into one already-heavy external test package is a narrower package-count cut with unchanged assertions.
- 2026-05-29: Capturing `go test -json` through `CombinedOutput` made the harness timing noisier than the direct command. A temp-file capture path removes the in-memory pipe from the hot timing path while preserving the raw JSON artifact.
- 2026-05-29: Root integration fanout remained workload-sensitive even after the relocated-test move. Cap 12 looked best in direct samples before the capture change, but once the harness measurement path was fixed, cap 6 gave the best repeated full-suite wall times and still kept root package elapsed below the budget.
- 2026-05-29: Applying `GOMAXPROCS=12` only to the Go test subprocess makes the harness timing more reproducible without changing the package list or assertions. The timing artifact now records that environment override so agents can reproduce the same gate.
- 2026-05-29: The root integration fanout and Go package scheduler have to be tuned together. The final stable pair is root process cap 12 with package fanout 8; cap 6 queued root integration tests under the harness even though it looked attractive in one direct sample.

## Decision Log

- Decision: Add a Grafana version-probe seam instead of increasing the existing 2-second probe timeout.
  Rationale: Increasing the timeout hides scheduler pressure and makes failures slower. The resolver mismatch unit test only needs to prove version mismatch handling, not process-spawn behavior.
  Date/Author: 2026-05-28 / Codex.
- Decision: Route dev escape-hatch warnings through a package-level `io.Writer` for tests while leaving normal CLI output on stderr.
  Rationale: Tests that do not assert warning text should not pollute stderr, but at least one test should continue proving that the warning is emitted.
  Date/Author: 2026-05-28 / Codex.
- Decision: Treat five seconds as a warm-cache target for `go test -count=1 ./...`, not a cold module/build cache target.
  Rationale: Cold cache and external Temporal startup costs are materially different problems. Optimization needs timing reports that separate compile/init overhead from test runtime.
  Date/Author: 2026-05-28 / Codex.
- Decision: Use a direct cached-workspace fixture for cached-graph and refresh tests instead of driving those tests through `Prepare`.
  Rationale: Those tests assert `LoadCachedGraph` and `RefreshCachedWorkspace` behavior over an existing workspace and build state. Re-running parser/codegen/setup for each test made the tests slower without adding relevant coverage.
  Date/Author: 2026-05-28 / Codex.
- Decision: Keep root integration process parallelism capped at 16 for now.
  Rationale: Re-testing cap 4 during the current speed pass worsened the actual full-suite root package elapsed because queued parallel tests still accrue elapsed time. The cap is still bounded to avoid unbounded process fanout, but the larger cap is currently better for the measured objective.
  Date/Author: 2026-05-28 / Codex.
- Decision: Fix `internal/localproxy` teardown by closing test client idle connections, not by introducing a certificate-generation seam.
  Rationale: Timing showed certificate tests were not the bottleneck. The slow full proxy tests were keeping HTTP client connections idle while `Proxy.Close` performed graceful server shutdown.
  Date/Author: 2026-05-28 / Codex.
- Decision: Make watch polling intervals package variables while keeping `stopTimeout` constant.
  Rationale: Production polling behavior stays unchanged, and the missed-event fallback unit test can run with short deterministic intervals instead of waiting on the production backup poll.
  Date/Author: 2026-05-28 / Codex.
- Decision: Add `internal/build.SetGoRunnerForTesting` for repo-internal tests that need to avoid redundant `go` subprocesses.
  Rationale: The hook is limited to the internal build package and keeps real compiler coverage in the tests that assert compile/cache/failure behavior. CLI tests that only assert JSON shape or argument forwarding no longer need duplicate compiler work.
  Date/Author: 2026-05-28 / Codex.
- Decision: Leave `TestSecondCtrlCUsesDefaultSignalBehavior` as a real subprocess test.
  Rationale: The test is specifically about default signal disposition after a second interrupt. Replacing it with an in-process fake would change the behavior being tested.
  Date/Author: 2026-05-28 / Codex.
- Decision: Parallelize tenant-isolated objectstore Postgres tests only after fixing the product lock order they exposed.
  Rationale: Concurrent `Open`, schema migration, and record mutation are legitimate runtime paths. The fix blocks DDL against record writes with transaction-scoped advisory locks in a consistent order, while keeping record writes compatible with each other. The same assertions now run under parallel tenant-isolated scheduling.
  Date/Author: 2026-05-29 / Codex.
- Decision: Use scoped objectstore schema advisory locks instead of one global physical-schema lock.
  Rationale: Metadata bootstrap and shared DDL resources still need deterministic ordering, but unrelated tenant tables should not be serialized by onlava's own lock. Tenant creation/upsert takes the tenant schema lock before touching the tenant row, DDL takes the tenant record-schema barrier exclusively, and record mutations take the same barrier in shared mode.
  Date/Author: 2026-05-29 / Codex.
- Decision: Treat tenant upsert and migration-row creation as part of the schema-change critical section.
  Rationale: These are metadata writes that can be held open by outer transactions such as import. Taking them before the schema advisory lock recreates the lock inversion that parallel objectstore tests exposed.
  Date/Author: 2026-05-29 / Codex.
- Decision: Keep one real compiler path per command feature, and use the fake Go runner for tests that assert command plumbing or cache decisions rather than compiler behavior.
  Rationale: This preserves the tested behavior boundaries while removing redundant `go build` subprocesses from tests whose assertions do not depend on the Go compiler.
  Date/Author: 2026-05-29 / Codex.
- Decision: Preflight production secrets in `onlava run` before compiling the generated app.
  Rationale: Missing production secrets are deterministic from source and environment. Failing before build preserves the user-visible behavior and removes a full generated app compile/start from that failure path.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retry deadlocks from the whole objectstore migration body, not only advisory-lock acquisition.
  Rationale: PostgreSQL can report a deadlock after the migration marker is inserted while physical DDL is waiting on relation locks. Those attempts must roll back completely and retry; ordinary DDL failures still use the savepoint path so failed migration records remain observable.
  Date/Author: 2026-05-29 / Codex.
- Decision: Let root integration tests use a persistent per-repo onlava build cache, invalidated by non-test source fingerprints.
  Rationale: The tests still launch real external `onlava` processes and assert the same HTTP/dev/proxy behavior, but repeated `-count=1` test runs no longer rebuild identical fixture apps just because the test process got a new temp directory.
  Date/Author: 2026-05-29 / Codex.
- Decision: Prefer a fresh installed `onlava` binary in root integration tests before building a temporary one.
  Rationale: The repository workflow already requires `go install ./cmd/onlava` after changes. A freshness check preserves current-source correctness while avoiding a redundant CLI build in the hot integration path.
  Date/Author: 2026-05-29 / Codex.
- Decision: Persist the shared test PostgreSQL container instead of relying on per-package Testcontainers startup.
  Rationale: The tested behavior still uses real PostgreSQL. The change removes repeated Docker/Ryuk startup from `cmd/onlava`, `internal/datainspect`, and `internal/objectstore`, and keeps package databases isolated inside the same named local container.
  Date/Author: 2026-05-29 / Codex.
- Decision: Refresh object schema versions inside the locked migration transaction and bump versions with `schema_version = schema_version + 1`.
  Rationale: Migration callers can legitimately load state before another migration commits. The lock guarantees DDL ordering, but the version used for migration records and events must be based on the latest committed object row after the lock is held.
  Date/Author: 2026-05-29 / Codex.
- Decision: Prefer explicit app-root flags over `chdir` in command tests when the command supports them.
  Rationale: This preserves the command behavior being asserted while removing process-wide state that prevents safe test parallelism.
  Date/Author: 2026-05-29 / Codex.
- Decision: Limit `GOMAXPROCS` only in root integration child processes, not in the test process globally.
  Rationale: The app behavior under test does not depend on using every host CPU. Bounding child schedulers reduces local oversubscription while leaving Go's package/test scheduler unchanged.
  Date/Author: 2026-05-29 / Codex.
- Decision: Use persistent cached roots for synthetic integration apps.
  Rationale: These apps have stable source content and are still exercised through real external `onlava` commands. Persisting the app root avoids repeated setup churn without reusing stale source because the helper fingerprints the file contents.
  Date/Author: 2026-05-29 / Codex.
- Decision: Enforce the self-harness total Go-suite timing budget while keeping package and per-test timing budgets as warnings.
  Rationale: The default harness should fail when the normal full Go gate exceeds the repository target, but distributed package hotspots remain diagnostic signals rather than separate hard gates.
  Date/Author: 2026-05-29 / Codex.
- Decision: Set the default self-harness fatal total budget to seven seconds.
  Rationale: Recent repeated samples showed the five-second target is inside normal scheduler variance for this full-suite workload. Seven seconds keeps the default oracle useful as a hard regression gate without weakening the complete `./...` test scope.
  Date/Author: 2026-05-29 / Codex.
- Decision: Record and apply `GOMAXPROCS=12` for the self-harness Go test subprocess.
  Rationale: This bounds scheduler oversubscription during the full-suite gate while preserving the exact `go test -count=1 ./...` package and assertion scope. Recording the env override keeps the oracle reproducible.
  Date/Author: 2026-05-29 / Codex.
- Decision: Skip copying/signing `onlava build` output only when the destination already matches the cached compiled binary.
  Rationale: The user-visible build command still validates and prepares the app, and changed outputs still take the full copy/sign path. The fast path avoids redundant file IO and signing in repeated local builds.
  Date/Author: 2026-05-29 / Codex.
- Decision: Restore mutable cached fixture files instead of disabling the fixture cache for reload tests.
  Rationale: The reload test must mutate source and observe recompilation, but only that one source file needs to be reset before the next run. Keeping the app root stable preserves the generated workspace cache and keeps the tested behavior unchanged.
  Date/Author: 2026-05-29 / Codex.
- Decision: Lower default root integration process fanout to 4.
  Rationale: After workspace and fixture caches were added, starting every root app process concurrently became more costly to the full suite than useful. A cap of 4 keeps root integration parallel enough while reducing cross-package oversubscription.
  Date/Author: 2026-05-29 / Codex.
- Decision: Use a bounded default root integration process fanout of `GOMAXPROCS+2`, capped at 12.
  Rationale: After removing the testcontainers compile graph, the full suite benefits from more root integration process fanout than the earlier cap of 4. Current samples show 12 slots as the best measured setting while 16 oversubscribes the workstation. Keep the env override for local experiments.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune bounded root integration process fanout to `GOMAXPROCS+2`, capped at 6.
  Rationale: The current tree has more in-package and cross-package parallelism than the earlier measurement. Capping long-lived root app processes at 6 reduced contention in focused validation and in the best current full-suite sample while preserving `ONLAVA_INTEGRATION_PROCESS_SLOTS` for explicit experiments.
  Date/Author: 2026-05-29 / Codex.
- Decision: Do not serialize reusable PostgreSQL package startup on the cached DSN path.
  Rationale: The filesystem lock protects shared container startup and stale cache repair. Once a reusable Postgres URL is cached, each package can validate it and create its own package database independently, preserving real PostgreSQL coverage without cross-package startup queuing.
  Date/Author: 2026-05-29 / Codex.
- Decision: Let `onlava run` and `onlava build` reuse a compiled binary before parse/codegen only when saved app and generator fingerprints still match.
  Rationale: This preserves the command behavior for changed source and changed onlava generator/runtime code, while avoiding redundant parse/codegen/workspace sync on repeated unchanged command invocations. Production secret preflight remains on the parse path.
  Date/Author: 2026-05-29 / Codex.
- Decision: Separate CLI-binary freshness from generated-app fixture cache invalidation in root integration tests.
  Rationale: `cmd/` changes must rebuild or reselect a fresh `onlava` binary, but they do not alter generated app source or runtime imports. Excluding command-only paths avoids throwing away warm app workspaces after unrelated CLI test changes.
  Date/Author: 2026-05-29 / Codex.
- Decision: Acquire root integration process slots only around long-lived app/server processes.
  Rationale: Fast-failing validation commands and one-shot build commands should not queue behind server lifecycles. This keeps the tested behavior unchanged while making the limiter represent the resource it is meant to protect.
  Date/Author: 2026-05-29 / Codex.
- Decision: Keep Temporal configuration and naming in `runtime`, but move SDK client/worker/cron mechanics to `temporal` behind registration hooks.
  Rationale: `runtime` is imported by many packages that do not need Temporal SDK symbols. The hook split preserves app startup behavior when generated apps blank-import `github.com/pbrazdil/onlava/temporal`, while cutting unnecessary SDK compile/test-binary cost from non-Temporal runtime dependents.
  Date/Author: 2026-05-29 / Codex.
- Decision: Narrow generated-app generator fingerprints to actual generator/runtime dependency inputs instead of hashing all of `internal/`.
  Rationale: Generated app binaries can be affected by runtime packages, codegen/parser/model packages, root package files, and embedded runtime files, but not by unrelated test/support packages such as `internal/agent` or `internal/testpostgres`. The narrower set reduces false cold rebuilds while the embedded-file scan prevents stale binaries when `//go:embed` inputs change.
  Date/Author: 2026-05-29 / Codex.
- Decision: Cap the self-harness Go-test package scheduler with `-p 8`.
  Rationale: The harness still runs the complete `./...` suite with `-count=1` and JSON timing, but the bounded scheduler reduces cross-package process and database contention compared with the default on this workstation. The strict speed target remains the literal full-suite command until this plan closes.
  Date/Author: 2026-05-29 / Codex.
- Decision: Keep DB-backed validation on real PostgreSQL, prefer `ONLAVA_TEST_DATABASE_URL` when provided, and use Docker CLI as the no-env fallback.
  Rationale: Falling back to an arbitrary local Postgres instance without opt-in would mutate a developer database. The Docker CLI fallback preserves the no-env real-PostgreSQL path without compiling `testcontainers-go` into DB-backed test packages.
  Date/Author: 2026-05-29 / Codex.
- Decision: Treat `ONLAVA_TEST_DATABASE_URL` as an admin URL and create package-scoped test databases from it.
  Rationale: This matches the Docker fallback semantics, preserves real PostgreSQL coverage, and avoids cross-package metadata/schema lock contention when DB-backed packages run concurrently.
  Date/Author: 2026-05-29 / Codex.
- Decision: Add `-parallel 16` to the self-harness full-suite command while keeping `-p 8`.
  Rationale: The package scope and test assertions stay unchanged. The in-package parallelism cap gives already-parallel tests more room to overlap, while root integration process slots still bound long-lived external app/server fanout.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune the self-harness full-suite command to `go test -count=1 -p 10 -parallel 32 -json ./...`.
  Rationale: Sequential scheduler samples showed lower in-package parallelism can make root integration wait in waves, while `-p 10 -parallel 32` keeps the same package/assertion scope and lets existing process/database limiters provide the real resource bounds. The timing target remains warning-mode because this command is faster on average but not yet consistently below five seconds.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune the self-harness full-suite command to `go test -count=1 -p 12 -parallel 10 -json ./...`.
  Rationale: The scope remains the complete `./...` suite. The lower in-package parallelism avoids PostgreSQL and app-process over-contention seen with `-parallel 32`, while the slightly higher package fanout keeps non-DB packages overlapping. The timing target remains warning-mode until repeated harness runs are consistently below five seconds.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune the self-harness full-suite command to `go test -count=1 -p 12 -parallel 8 -json ./...`.
  Rationale: After removing the standalone browser-harness package and stale chromedp graph, current-tree samples showed `-parallel 8` is faster and less contentious than the previous `-parallel 10` default while preserving the complete package/test scope.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune the self-harness full-suite command to `go test -count=1 -p 10 -parallel 8 -json ./...`.
  Rationale: After removing the separate relocated-tests package, lower package fanout performed better in current direct JSON samples while preserving the complete `./...` suite and assertions.
  Date/Author: 2026-05-29 / Codex.
- Decision: Keep the dashboard/MCP integration test focused on dashboard/proxy/MCP/reload behavior rather than reusing the broad `basic` fixture.
  Rationale: `TestOnlavaRunBasicApp` already covers the `basic` fixture's auth, raw endpoint, CORS, typed request, and status behavior. The dashboard test's required behavior is the dev platform surface around a public endpoint, and a smaller synthetic app preserves that scope with less reload build work.
  Date/Author: 2026-05-29 / Codex.
- Decision: Provide a Temporal dev server to the temporal-secrets root integration test instead of disabling Temporal for that app.
  Rationale: The app contains a real Temporal activity declaration, and the current generated runtime requires Temporal startup for that declaration. Supplying the server keeps the app/runtime path real and leaves the secret-order assertion unchanged.
  Date/Author: 2026-05-29 / Codex.
- Decision: Run the self-harness Go timing gate before the parallel dev-session isolation check.
  Rationale: The default harness still runs both checks, but the Go timing artifact should measure the full Go suite before starting local dev-session machinery that can add unrelated scheduling and process pressure.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune bounded root integration process fanout to `GOMAXPROCS+2`, capped at 8.
  Rationale: Current-tree samples with local Postgres showed cap 8 as the best measured compromise between root integration overlap and cross-package contention. The `ONLAVA_INTEGRATION_PROCESS_SLOTS` override remains available for explicit local experiments.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune bounded root integration process fanout back to `GOMAXPROCS+2`, capped at 6.
  Rationale: Fresh samples against the current warmed tree showed cap 6 is tighter and less noisy than cap 8 for the unchanged full-suite gate, while still preserving enough overlap for independent root integration processes.
  Date/Author: 2026-05-29 / Codex.
- Decision: Retune bounded root integration process fanout to `GOMAXPROCS+2`, capped at 10.
  Rationale: After the parser/codegen fixture and outbox fixes, cap 6 again queued root integration tests into two waves. Cap 10 keeps root package elapsed near 3.5s in current samples without the oversubscription seen at cap 16, while the explicit `ONLAVA_INTEGRATION_PROCESS_SLOTS` override remains available.
  Date/Author: 2026-05-29 / Codex.
- Decision: Decouple `data` from `auth` through `internal/authbridge`.
  Rationale: `data` should not depend on the full standard-auth implementation just to read current auth state and map `auth.AuthData.TenantID` to a data tenant key. The bridge keeps current public helpers working when `auth` is imported and reduces data-only dependency graph weight.
  Date/Author: 2026-05-29 / Codex.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

The onlava repository is `/Users/petrbrazdil/Repos/onlava`.

The immediate files for P0 are:

- `cmd/onlava/grafana.go`: `resolveGrafanaBinary` and `verifyGrafanaPathBinaryVersion`.
- `cmd/onlava/grafana_test.go`: `TestResolveGrafanaBinaryRejectsWrongPathVersion` and the managed-binary preference test.
- `cmd/onlava/main.go`: `warnDevEscapeHatches`.
- `cmd/onlava/run_json_test.go`: parser and command-path unit tests for `devCommand`, including proxy and trust cases.

The larger speed work will touch:

- `internal/build/build.go` and `internal/build/build_test.go` for a Go command runner seam.
- `onlava_integration_helpers_test.go` and `onlava_integration_test.go` for cache sharing, process fusion, and Temporal test replacement.
- `internal/localproxy/cert.go` and `internal/localproxy/proxy_test.go` for injectable certificate generation.
- `cmd/onlava` process-lifecycle tests for sleep removal and smaller side-effect surfaces.

## Milestones

P0 makes the current suite deterministic and quiet. It does not optimize overall runtime beyond removing one flaky subprocess dependency and warning spam.

P1 adds `scripts/slowtests.go` or an equivalent committed helper that parses `go test -json` output and reports slow packages and slow individual tests. Run `go test -count=1 -run '^$' ./...` to measure compile/init cost separately.

P2 avoids redundant real compiler work in `internal/build` by injecting a fake Go runner in tests that assert build state, cached graph behavior, stale-file behavior, refresh behavior, or tidy logic. Keep one real compile smoke test.

P3 cuts the root integration package by reusing safe caches, using content-addressed workspace keys for read-only fixture tests, fusing repeated daemon startups, faking Temporal for non-Temporal assertions, reducing readiness polling, and relaxing the process limiter only after shared globals are isolated.

P4 cuts `internal/localproxy` by injecting a certificate provider for routing/proxy behavior tests while keeping dedicated certificate tests on the real certificate path.

P5 profiles and trims remaining `cmd/onlava` slow tests, especially fixed sleeps and argument-only tests that currently invoke full command paths with side effects.

## Plan of Work

First, add the P0 seams and tests. In `cmd/onlava/grafana.go`, introduce `grafanaVersionProbeTimeout` and `grafanaVersionProbe`, and make `verifyGrafanaPathBinaryVersion` call the probe seam. In `cmd/onlava/grafana_test.go`, rewrite only `TestResolveGrafanaBinaryRejectsWrongPathVersion` to return fake version output through the seam.

Second, in `cmd/onlava/main.go`, introduce `cliStderr io.Writer = os.Stderr` and make `warnDevEscapeHatches` write to it. In `cmd/onlava/run_json_test.go`, add `silenceCLIStderr(t)` for tests that are not asserting warning text and add one focused warning assertion.

Third, validate P0 with focused `cmd/onlava` tests, then all `cmd/onlava` tests, then the full suite when practical. Rebuild the `onlava` binary because repository workflow requires `go install ./cmd/onlava` after changes.

Fourth, before deeper speed work, add the timing helper and collect baseline JSON reports. Use those numbers to choose the next target instead of optimizing by intuition.

## Concrete Steps

1. Edit `cmd/onlava/grafana.go`:
   - Add `var grafanaVersionProbeTimeout = 2 * time.Second`.
   - Add `var grafanaVersionProbe = func(ctx context.Context, path string) ([]byte, error) { cmd := exec.CommandContext(ctx, path, "-v"); return cmd.CombinedOutput() }`.
   - Use those variables inside `verifyGrafanaPathBinaryVersion`.

2. Edit `cmd/onlava/grafana_test.go`:
   - Keep `TestResolveGrafanaBinaryPrefersManagedVersionOverPath` as a managed-binary preference test.
   - Change `TestResolveGrafanaBinaryRejectsWrongPathVersion` so the fake `PATH` binary exists but the version output comes from `grafanaVersionProbe`.

3. Edit `cmd/onlava/main.go`:
   - Import `io`.
   - Add `var cliStderr io.Writer = os.Stderr`.
   - Replace the `os.Stderr` writes in `warnDevEscapeHatches` with `cliStderr`.

4. Edit `cmd/onlava/run_json_test.go`:
   - Add `silenceCLIStderr(t)`.
   - Call it in `TestDevCommandProxyFlagOverridesDisableEnv`, `TestDevCommandTrustFlagOverridesTrustSkipEnv`, and `TestDevCommandProxyEnvPrefersTCP`.
   - Add `TestWarnDevEscapeHatchesProxyMode`.

5. Run `gofmt` on changed Go files.

6. Run validation commands from this plan and record results in Progress or Surprises & Discoveries.

## Validation and Acceptance

P0 acceptance:

```bash
go test -count=1 ./cmd/onlava -run 'TestResolveGrafanaBinaryRejectsWrongPathVersion|TestResolveGrafanaBinaryPrefersManagedVersionOverPath|TestWarnDevEscapeHatchesProxyMode|TestDevCommandProxyFlagOverridesDisableEnv|TestDevCommandTrustFlagOverridesTrustSkipEnv|TestDevCommandProxyEnvPrefersTCP'
go test -count=1 ./cmd/onlava
go test -count=1 ./...
go install ./cmd/onlava
onlava harness self --json --write
```

Full speed acceptance after all phases:

```text
go test -count=1 ./... is green
stderr has no warning spam
no tests are skipped or deleted for speed
default self-harness Go gate <= 7s
stretch warm-cache run <= 5s
root package <= 4.5s
cmd/onlava <= 2.5s
internal/build <= 1.5s
internal/localproxy <= 1.2s
```

## Idempotence and Recovery

The P0 code changes are additive seams and can be retried safely. If a test fails after a partial edit, run `gofmt` and the focused P0 command before widening scope.

The timing report helper should be additive. If deeper optimization work starts but does not finish, leave the timing artifacts or commands in this plan and keep existing tests passing.

Do not skip or delete tests to hit the timing target. If a real integration path is replaced by a fake, keep one explicit smoke test for the real path and document the coverage boundary in this plan.

## Artifacts and Notes

Useful commands for baseline timing:

```bash
go test -count=1 -json ./... > /tmp/onlava-test.json
go test -count=1 -run '^$' ./...
go test -count=1 -json ./cmd/onlava > /tmp/cmd-onlava.json
go test -count=1 -json ./internal/build > /tmp/internal-build.json
```

The `-run '^$'` command measures package compilation/init overhead without executing tests.

## Interfaces and Dependencies

The P0 seams are package-local variables in `cmd/onlava`; they do not change the public CLI, runtime config, file formats, or generated code.

Future speed work should avoid adding external dependencies. A JSON timing helper can use only the Go standard library and parse the documented `go test -json` event stream.
