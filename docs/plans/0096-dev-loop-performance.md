# Dev Loop Performance: scenery up Startup and Test Suite Speed

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Make the local development loop fast in two places:

1. `scenery up` from invocation until the API and all dev services are ready and served, on both warm (repeat start, nothing changed) and cold paths.
2. `go test ./...` wall clock, continuing plan 0050. The no-cache suite is ~11-14s with a ~9.9s package-compile floor; remaining wins are targeted test rewrites and structural package splits.

Source evidence for all items: startup-path and test-suite investigations recorded in this plan's initial commit (2026-07-06, gpt-5.5 read-only code traces plus `.scenery/harness/test-timing-latest.json`).

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

## Execution Order

Workstreams overlap in `internal/build` and `watch.go`, so phases keep file sets disjoint:

- Phase 1 (parallel): T1+T2 (frontend supervisor/tests), T4 (symphony + generate/task stubs), U1+U2+U3 (build pipeline; one agent, sequential internally).
- Phase 2 (after Phase 1 lands): T3 (`internal/build` tests, rebased on U1-U3 changes), U4+U5+U6 (startup orchestration).
- Phase 3: T5 opportunistic split; measure and record final timings.

Validation per phase: `go build ./...`, `go test ./...`, `scenery harness self --summary --write` (worktree-local binary) at the end of each phase. Measure `scenery up` warm/cold on a fixture app and record numbers here.

## Progress

- [x] 2026-07-06: Created this ExecPlan from the two performance investigations.
- [x] 2026-07-06: Phase 1 complete (T1, T2, T4, U1-U3). Frontend restart test is event-driven with injectable delay; frontend failure/concurrency tests use fake starters with one real subprocess smoke. Symphony runner hooks live on dashboardServer; generate/task/db lifecycle hooks are per-call; freed tests run parallel (validated with -race). Warm `scenery up` tries LoadReusableBinaryWithSnapshot before graph load, guarded by source/generator/build fingerprints plus manifest match, with invalidation tests; graph cache hits skip packages.Load; cache-miss refresh reuses the parsed model; the startup watch scan snapshot (hashes, embeds, ignores) feeds fingerprinting, sync, and watcher setup.
- [ ] Phase 2: T3, U4-U6.
- [ ] Phase 3: T5 and final measurements.

## Surprises & Discoveries

- The self-harness test-timing artifact only covers the last run's affected packages in quick mode; full-suite timing needs a dedicated `-count=1 -json` audit run.
- `scripts/testtimes` is no longer a hotspot (~0.002s package time); earlier 6.3s readings were full-suite contention.

## Decision Log

- 2026-07-06: Keep `internal/testlimit`; A/B on `internal/build` showed capped GOMAXPROCS=4 faster (1.909s) than uncapped 24 (2.097s) with much lower system CPU.
- 2026-07-06: Implementation delegated to Codex (gpt-5.5) agents in phases with disjoint file sets; Claude reviews diffs and runs verification.

## Outcomes & Retrospective

Pending.
