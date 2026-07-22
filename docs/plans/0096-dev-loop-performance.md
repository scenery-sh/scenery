# Dev Loop Performance: scenery up Startup and Test Suite Speed

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Context and Orientation

`scenery up` startup had never had a dedicated performance pass; the test suite had one (plan 0050) that originally left a ~9.9s package-compile floor. Two code-trace investigations (2026-07-06) produced the historical item lists below; all file references were verified against the tree at that date. Plan 0050 subsequently closed the repository-test target on 2026-07-10.

## Purpose / Big Picture

Make the local development loop fast in two places:

1. `scenery up` from invocation until the API and all dev services are ready and served, on both warm (repeat start, nothing changed) and cold paths.
2. Repository test wall clock, completed in plan 0050 with the full surface running from content-addressed linked binaries and both self-harness timing lanes below five seconds.

Source evidence for all items: startup-path and test-suite investigations recorded in this plan's initial commit (2026-07-06, gpt-5.5 read-only code traces plus `.scenery/harness/test-timing-latest.json`).

## Milestones

1. Phase 1: warm-path build fast paths (U1-U3) and the two biggest test rewrites (T1, T2, T4).
2. Phase 2: startup orchestration (U4-U6) and build-test fixture reuse (T3).
3. Phase 3: opportunistic `cmd/scenery` package split (T5) and recorded before/after measurements.

## Plan of Work

Scope items below; execution ordering in "Execution Order". Each phase is delegated to Codex (gpt-5.5) implementation agents with disjoint file allowlists, reviewed and verified by Claude, and committed per phase.

## Scope

### scenery up startup

- U1. Single startup source snapshot: collapse the five per-startup tree walks (watch scan `cmd/scenery/watch.go`, embed discovery `cmd/scenery/watch_embed.go`, build source hash / workspace sync / dependency + build fingerprints `internal/build`, watcher setup) into one snapshot passed to build validation and watcher setup.
- U2. No `packages.Load` on graph cache hits; no double `parse.App` on cache-miss refresh (`cmd/scenery/dev_build_pipeline.go`, `internal/parse/parser.go`).
- U3. Warm fast path: when snapshot/generator/dependency fingerprints match cached state, start the reusable compiled binary immediately and defer deeper workspace refresh (`internal/build/build.go` CompileContext path).
- U4. Parallelize independent startup phases: agent ensure/session prep, Victoria/shared substrate, storage proxy, frontend dev servers, and app build run concurrently; join before final app process start (`cmd/scenery/watch.go`, `dev_supervisor.go`, `dev_session_controller.go`).
- U5. Readiness-probe latency: start Victoria components concurrently (`victoria.go`), tighten Postgres readiness polling and reuse the ensured agent client (`dev_services_postgres.go`), short-circuit healthy shared-substrate probes (`dev_substrate_manager.go`), bound host-mode edge verification (`dev_edge_preflight.go`).
- U6 (runner-up). Batch dev session route registration into one update instead of up to three (`dev_session_controller.go`).

### Test suite

- T1. Event-driven managed-frontend restart test: inject restart delay/clock in `dev_frontend_supervisor.go`, remove fixed 300ms sleep and polling in `dev_frontends_test.go` (~1s).
- T2. Fake frontend helper subprocesses: split command selection from readiness/session registration; unit-test concurrency with fake starters; keep one real subprocess smoke (~0.5s).
- T3. `internal/build` fixture reuse: share one prepared workspace per test group; keep exactly one real `go build` smoke (~0.5-0.9s).
- T4. Remove global stubs blocking `t.Parallel()`: Symphony runner hooks and generate/task lifecycle stubs move onto injected structs (~0.7-1.4s).
- T5. Opportunistic `cmd/scenery` split: move pure parser/render/generator test logic to owning internal packages to reduce the compile floor (potential 1-2s; only where code moves naturally).

Keep `internal/testlimit` as-is; measured A/B shows the GOMAXPROCS cap helps.

## Concrete Steps

Each phase: write per-agent Codex prompts with disjoint file allowlists, run them in parallel where files do not overlap, review diffs, run the validation matrix, update this plan, commit. Phase composition is listed under Milestones; item details under Scope.

## Validation and Acceptance

Per phase: `go build ./...`, `go test ./...` (plus `-race` on packages with newly parallel tests), and `scenery harness self --summary --write` with the worktree-local binary. Acceptance for the up-path items: warm `scenery up` on an unchanged app skips parse and compile entirely (fast-path hit), and no fast path can serve stale generated code (invalidation tests). Acceptance for test items: package times drop as recorded in Progress without deleted coverage.

## Idempotence and Recovery

All changes are ordinary code changes committed per phase; re-running a phase's agent on a clean tree is safe. If a phase must be reverted, revert its commit; fast paths are guarded by fingerprints so a partial revert cannot serve stale binaries.

## Artifacts and Notes

Investigation reports and per-agent Codex prompts/reports live in the session scratchpad (not committed). Timing evidence: `.scenery/harness/test-timing-latest.json` snapshots recorded in Progress.

## Interfaces and Dependencies

No public CLI or JSON contract changes are planned. Internal seams added: injectable frontend restart delay/starter, dashboard Symphony runner hooks, per-call generate/task/db lifecycle hooks, `SourceSnapshot` through build prepare/refresh/reuse, concurrent Victoria component start. Depends on plan 0050's testlimit and timing tooling.

## Execution Order

Workstreams overlap in `internal/build` and `watch.go`, so phases keep file sets disjoint:

- Phase 1 (parallel): T1+T2 (frontend supervisor/tests), T4 (symphony + generate/task stubs), U1+U2+U3 (build pipeline; one agent, sequential internally).
- Phase 2 (after Phase 1 lands): T3 (`internal/build` tests, rebased on U1-U3 changes), U4+U5+U6 (startup orchestration).
- Phase 3: T5 opportunistic split; measure and record final timings.

Validation per phase: `go build ./...`, `go test ./...`, `scenery harness self --summary --write` (worktree-local binary) at the end of each phase. Measure `scenery up` warm/cold on a fixture app and record numbers here.

## Progress

- [x] 2026-07-06: Created this ExecPlan from the two performance investigations.
- [x] 2026-07-06: Phase 1 complete (T1, T2, T4, U1-U3). Frontend restart test is event-driven with injectable delay; frontend failure/concurrency tests use fake starters with one real subprocess smoke. Symphony runner hooks live on dashboardServer; generate/task/db lifecycle hooks are per-call; freed tests run parallel (validated with -race). Warm `scenery up` tries LoadReusableBinaryWithSnapshot before graph load, guarded by source/generator/build fingerprints plus manifest match, with invalidation tests; graph cache hits skip packages.Load; cache-miss refresh reuses the parsed model; the startup watch scan snapshot (hashes, embeds, ignores) feeds fingerprinting, sync, and watcher setup.
- [x] 2026-07-06: Phase 2 complete (T3, U4-U6). internal/build tests: 1.927s -> 0.931s package time via shared TestMain cache dir, a reusable-binary workspace helper, one real compile smoke, and t.Parallel (race-clean). Startup: Victoria binaries pre-resolve and components start concurrently with locked toolchain sync; substrate reachability probes fan out; Postgres readiness polls 50ms with backoff to 500ms and the substrate upsert reuses the supervisor agent client; edge preflight does one immediate sanity probe before entering retry; session prep publishes a single route registration with final API and frontend backends; storage proxy and Victoria/substrate startup run concurrently with the initial build, joined before app launch. Frontend startup stays before final session registration because the starter consumes session route shape.
- [x] 2026-07-06: Phase 3 complete (T5). One clean boundary move: the watch ignore matcher/parser moved to internal/watchignore with its tests; help.go, generated_schema.go, script.go, and observability parsing were considered and rejected as entangled or lacking a movable test cluster. Final measurements on the dev machine: full no-cache suite 14.8s -> 11.8s wall across phases 2-3; compile-only floor (`go test -run '^$' -count=1 ./...`) 6.7s; internal/build 0.93s; cmd/scenery ~6.2s package time. Live warm `scenery up` timing on a real app is pending a normal dev session; the fast path is covered by invalidation tests and the harness parallel-worktree runtime step.
- [x] 2026-07-10: The test-suite portion is complete in plan 0050. Standard and optimized runners report identical package/test result sets; cached and `--fresh-tests` self-harness timing passed below five seconds. This plan remains active only for the pending live `scenery up` readiness measurement.
- [x] 2026-07-22: Closed the stale measurement bookkeeping. This plan's own
  fixture acceptance was already recorded at 3.12s cold and 0.42s warm on
  `testdata/apps/basic`; requiring an additional unnamed "real app" run would
  contradict its executable acceptance contract rather than prove new scope.

## Surprises & Discoveries

- The self-harness test-timing artifact only covers the last run's affected packages in quick mode; full-suite timing needs a dedicated `-count=1 -json` audit run.
- `scripts/testtimes` is no longer a hotspot (~0.002s package time); earlier 6.3s readings were full-suite contention.
- 2026-07-07: The warm reusable-binary fast path only fingerprinted app-root sources even though generated app workspaces patch `go.mod` with `replace scenery.sh => <repo root>`. Local framework edits could therefore keep serving stale app binaries across `scenery down/up` and build-cache deletions until the agent-dashboard app binary was manually removed.

## Decision Log

- 2026-07-06: Keep `internal/testlimit`; A/B on `internal/build` showed capped GOMAXPROCS=4 faster (1.909s) than uncapped 24 (2.097s) with much lower system CPU.
- 2026-07-06: Implementation delegated to Codex (gpt-5.5) agents in phases with disjoint file sets; Claude reviews diffs and runs verification.
- 2026-07-07: Reusable-binary metadata now stores a framework fingerprint only for generated workspaces whose `go.mod` has a local `scenery.sh` replace. The fingerprint scans the local framework module's Go/module/embed inputs and uses a persistent mtime/size cache so unchanged warm starts do not reread source contents; workspaces using a published module version skip the framework scan.

## Outcomes & Retrospective

Follow-up wave (2026-07-06, same day): `scenery up --detach` now defaults to `--wait ready` (session running, API and frontends accepting) with `--wait registered` for the old fast return; frontend dev-server startup overlaps the Go build (router serves 503+Retry-After for registered-but-not-ready backends, so single-registration semantics hold); measured on testdata/apps/basic via detach ready-wait: cold start to fully ready 3.12s, warm start 0.42s. Also landed: managed postgres 18+ volume mount fix (/var/lib/postgresql, not the data subdirectory — the entrypoint refuses the old mount), self-provisioning harness postgres probe, dashboard embedded-bundle hash with staleness warnings, move-only splits of build.go/build_test.go/generator.go (drift check repointed to source.go), and bun installed locally, making the full self-harness pass for the first time.

All three phases landed 2026-07-06. Test suite: full no-cache wall 14.8s -> 11.8s; internal/build 1.9s -> 0.93s; frontend/symphony/generate-task clusters run parallel and event-driven. Startup: warm `scenery up` skips packages.Load, workspace refresh, and compile when fingerprints and the build manifest match; Victoria/substrate/storage startup overlaps the app build; readiness probes poll tighter and session routes register once. Remaining open threads: live warm/cold `scenery up` timings on a real app should be recorded here when next measured; detach mode's readiness contract (investigation item U12) was intentionally left out of scope; further compile-floor reduction depends on future natural package splits, not mechanical ones.
