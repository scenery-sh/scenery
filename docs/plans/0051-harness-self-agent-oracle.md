# Harness Self Agent Oracle

This ExecPlan is a living document. Update `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` as work proceeds.

## Purpose / Big Picture

`onlava harness self` should become a machine-readable development oracle for the onlava repository. Today it already runs a useful collection of checks, but agents still have to infer too much from terminal output, local knowledge, or stale docs. The target state is that a human or AI agent can run:

```sh
onlava harness self --json --write
```

and receive one stable answer to these questions:

- Did the normal Go correctness gate pass?
- Which files changed, which packages are affected, and what commands should be run next?
- Did the change slow down tests, violate budgets, or create a new slow test hotspot?
- Do JSON outputs still match the checked-in schemas?
- Did CLI, docs, environment variables, generated artifacts, or embedded files drift?
- Which optional tools are missing, and which missing tools actually matter for this run?
- What context file should the next agent read before editing?

The outcome is not just more checks. The outcome is a default local harness that can guide edits, validate them, and write durable JSON artifacts under `.onlava/harness/` for other tools.

## Progress

- [x] 2026-05-29: Created this ExecPlan from the AI-agent DX review and linked it from `docs/plans/active.md`.
- [x] 2026-05-29: Confirmed the current working tree already partially addresses two items: default `harness self` runs `go test -count=1 ./...`, and installed-binary freshness scans broader non-test source inputs.
- [x] 2026-05-29: Implemented the changed-area oracle, affected-package mapping, relevant-doc/risk-flag summaries, and `.onlava/harness/changed-area-latest.json`.
- [x] 2026-05-29: Added a structured `go test -count=1 -json ./...` timing report with warning-mode budgets and `.onlava/harness/test-timing-latest.json`.
- [x] 2026-05-29: Added the agent context pack at `.onlava/harness/agent-context.json`.
- [x] 2026-05-29: Added JSON schemas for changed-area, test-timing, and agent-context artifacts, and extended the self-harness schema to include the new reports.
- [x] 2026-05-29: Added a standard-library JSON Schema subset validator and a schema-validation report for the self-harness, changed-area, test-timing, schema-validation, and agent-context artifacts.
- [x] 2026-05-29: Added CLI contract, environment variable, generated-artifact hygiene, and `//go:embed` binary-freshness drift checks under `.onlava/harness/drift-latest.json`.
- [x] 2026-05-29: Added toolchain preflight under `.onlava/harness/toolchain-latest.json`, including required and optional tool classification.
- [x] 2026-05-29: Added fixture matrix checks for `testdata/apps/*` under `.onlava/harness/fixture-matrix-latest.json`.
- [x] 2026-05-29: Added `--quick`, `--race`, and `--release` self-harness modes and per-step side-effect classification.
- [x] 2026-05-29: Added data-driven package-layering rules to the architecture check.
- [x] 2026-05-29: Verified `go test -count=1 ./cmd/onlava`, `go test -count=1 ./...`, `go install ./cmd/onlava`, and default `onlava harness self --json --write`; the harness wrote all oracle artifacts, validated 10 JSON payloads, reported zero drift diagnostics, and checked 6 fixture apps.
- [x] 2026-05-29: Trimmed oracle unit-test overhead without changing report scope. Changed-area and toolchain preflight tests now inject deterministic collection/probe data while still exercising the public report builders and assertions.
- [x] 2026-05-29: Continued the speed dependency in `docs/plans/0050-test-suite-speed-hardening.md`; objectstore scoped lock hardening and test-pool caps keep isolated `internal/objectstore` around 1s, while the latest warmed full `go test -count=1 ./...` remains above the strict target at about 7.4s wall.
- [x] 2026-05-29: Fused the default self-harness Go correctness gate and timing pass. The default harness now runs the full suite once with `go test -count=1 -json ./...`, uses that run as the pass/fail gate, and derives `.onlava/harness/test-timing-latest.json` from the same output.
- [x] 2026-05-29: Revalidated the fused default harness after splitting workers/devdash tests out of `internal/relocatedtests`. `onlava harness self --json --write` passed with 16 steps, 10 schema validations, zero drift diagnostics, and a Go timing total of 7.720s.
- [x] 2026-05-29: Extended the root integration freshness helpers to match the oracle's embedded-file expectation: non-test embedded/source inputs now affect installed-binary freshness and generated-app fixture fingerprints, while test files and ignored generated/cache directories do not.
- [x] 2026-05-29: Re-ran the default oracle after the freshness correction. `onlava harness self --json --write` passed with 16 steps, 10 schema validations, zero drift diagnostics, and a Go timing total of 7.203s.
- [x] 2026-05-29: Refreshed the default oracle after the plan updates. It remained green with 16 steps, 10 schema validations, and zero drift diagnostics; the Go timing total was 9.000s, so budget enforcement remains blocked on the speed plan.
- [x] 2026-05-29: Reordered default self-harness execution so `go install ./cmd/onlava` and the installed-binary freshness check run before the full Go test/timing gate. This keeps the full-suite scope intact while ensuring root integration sees the freshly installed CLI binary during the harness run.
- [x] 2026-05-29: Verified the reordered harness. It passed with 16 steps, zero drift diagnostics, 10 schema validations, and the expected install/freshness-before-test order. The Go timing total was 11.178s, so budget enforcement remains blocked on the speed plan.
- [x] 2026-05-29: Capped the default self-harness Go-test package scheduler at `-p 8`. The harness still runs the complete `./...` suite with `-count=1` and JSON timing, but avoids some cross-package process/database oversubscription.
- [x] 2026-05-29: Revalidated the oracle after the scheduler cap and generator fingerprint fixes. `onlava harness self --json --write` passed with 16 steps, zero drift diagnostics, 10 schema validations, and Go timing total 9.063s from `go test -count=1 -p 8 -json ./...`.
- [x] 2026-05-29: Expanded the fixture matrix from cheap `check`/summary inspection to schema-backed inspection for `app`, `routes`, `services`, and `endpoints` across all fixture apps.
- [x] 2026-05-29: Fixed the contract drift surfaced by the stronger fixture matrix. The internal schema validator now resolves same-directory external `$ref`s with the referenced schema's own local-ref context, `inspect services` emits empty middleware arrays instead of `null`, and the config schema covers actual inspect output fields and zero values.
- [x] 2026-05-29: Rebuilt `onlava` and re-ran the default oracle. `onlava harness self --json --write` passed with 16 steps, 6 fixture apps fully inspected, 10 schema validations, zero failed steps, and Go timing total 9.030s from `go test -count=1 -p 8 -json ./...`.
- [x] 2026-05-29: Raised the root integration process slot cap from 4 to 6 after measurement showed the warmed fixture cache was underutilized. Rebuilt `onlava` and refreshed the oracle; it passed with 16 steps, all fixture schema inspections green, 10 schema validations, and Go timing total 8.545s.
- [x] 2026-05-29: Retuned the default self-harness full-suite command to `go test -count=1 -p 6 -json ./...` based on the latest scheduler comparison. This keeps the full `./...` scope and records the exact command in the timing artifact. The refreshed oracle passed with Go timing total 8.017s.
- [x] 2026-05-29: Moved root integration process-slot acquisition to immediately before long-lived process start, then retuned the default self-harness full-suite command back to `go test -count=1 -p 8 -json ./...` with 8 root integration slots. The full scope is unchanged and the refreshed oracle passed; recent timing samples were 7.165s and 8.638s.
- [x] 2026-05-29: Made the priority order explicit so the plan preserves the highest-leverage agent-DX sequence: full tests, changed-area oracle, timing budgets, schema validation, and agent context pack.
- [x] 2026-05-29: Refreshed the default oracle with local Postgres.app supplied through `ONLAVA_TEST_DATABASE_URL`. `onlava harness self --json --write` passed with 16 steps, zero failed steps, 6 fixture apps, no schema-validation failures, and a Go timing total of 5.595s. The timing budget remains warning-mode because the target is still just above five seconds.
- [x] 2026-05-29: Continued the speed dependency by retuning root integration process fanout from 6 to 4 with focused coverage for the helper. A warm `go test -count=1 -p 8 -json ./...` pass completed in 8.53s after cache cleanup, compared with 9.08s at cap 6 and 8.75s at cap 3. The oracle still cannot make the five-second timing budget fatal.
- [x] 2026-05-29: Continued the speed dependency by removing `testcontainers-go` from the DB test compile graph and replacing it with a Docker CLI fallback in `internal/testpostgres`. A warm compile-only `go test -count=1 -run '^$' -p 8 -json ./...` run is now 4.91s, but full test execution still exceeds the default five-second oracle budget.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle after the Docker CLI fallback. `onlava harness self --json --write` passed with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 7.867s. The strict timing gate remains open.
- [x] 2026-05-29: Retuned the default self-harness Go command to `go test -count=1 -p 8 -parallel 16 -json ./...` after focused full-suite samples showed better overlap without changing the package or assertion scope. The refreshed oracle passed with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 6.139s.
- [x] 2026-05-29: Continued the speed dependency by trimming `cmd/onlava` and aggregate unit-test delay: detached dev startup polling is now test-overridable, independent `internal/relocatedtests` checks run in parallel, and root integration process fanout is capped at 6 for the current workload. The refreshed oracle passed with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 6.983s.
- [x] 2026-05-29: Continued the speed dependency without weakening the harness scope. `onlava worker` now reuses a current generated app binary like `onlava run`, and the dashboard/MCP root integration test uses a smaller synthetic app while preserving proxy, dashboard websocket, MCP, trace, and reload assertions. The self-harness command remains the complete `go test -count=1 -p 8 -parallel 16 -json ./...` suite; recent direct samples are still just above the strict budget at roughly 5.01s, 5.05s, and 5.44s.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle after the worker/dashboard changes. `onlava harness self --json --write` passed with zero failed steps, zero drift errors, no schema-validation failures, 6 fixture apps, and Go timing total 5.925s from the full `go test -count=1 -p 8 -parallel 16 -json ./...` gate.
- [x] 2026-05-29: Retuned the self-harness Go command to `go test -count=1 -p 10 -parallel 32 -json ./...` after sequential scheduler samples showed the previous lower in-package parallelism queued root integration tests, while higher in-package parallelism kept root assertions overlapped. Added safe `t.Parallel()` coverage for isolated datainspect, workers, localproxy, runtime cron, and dev-service unit tests without changing assertions.
- [x] 2026-05-29: Revalidated the latest pass with focused package tests and full-suite samples after `go install ./cmd/onlava`. The suite remains green; recent `-p 10 -parallel 32` samples were 4.94s, 5.20s, and 6.04s wall, so the timing budget is improved but still too noisy to make fatal.
- [x] 2026-05-29: Refreshed the default oracle artifacts after the latest tuning. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with 16 green steps, zero drift errors, no schema-validation failures, and Go timing total 6.980s from the full `go test -count=1 -p 10 -parallel 32 -json ./...` gate.
- [x] 2026-05-29: Retuned the self-harness Go command to `go test -count=1 -p 12 -parallel 10 -json ./...` after the latest full-suite samples showed lower in-package parallelism avoids DB/process over-contention while preserving the complete `./...` scope. Direct samples are now green and near the target but still noisy, so timing remains warning-mode.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with 16 green steps, all oracle artifacts written, zero failed steps, and Go timing total 5.500s from the full `go test -count=1 -p 12 -parallel 10 -json ./...` gate. The strict timing budget remains warning-mode.
- [x] 2026-05-29: Refreshed the final default oracle snapshot after the latest plan updates. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with zero failed steps, zero drift errors, no schema-validation failures, and Go timing total 6.710s from the full `go test -count=1 -p 12 -parallel 10 -json ./...` gate. The oracle is complete enough to use, while the timing budget remains warning-mode.
- [x] 2026-05-29: Improved the default oracle's DB-backed path without changing test scope. The explicit `ONLAVA_TEST_DATABASE_URL` helper now creates package-scoped databases, matching the Docker fallback isolation and reducing DB lock contention. The full harness-shaped Go command passed at 5.22s wall after the change, still above the fatal threshold but closer and less DB-contention bound.
- [x] 2026-05-29: Moved the default oracle's full Go test/timing gate before the parallel dev-session check. The harness still runs both steps, but the Go timing artifact is now captured before dev-session supervision can add unrelated process pressure. The refreshed oracle passed with 16 green steps and Go timing total 6.625s.
- [x] 2026-05-29: Retuned the root integration process fanout used by the full-suite gate to `GOMAXPROCS+2` capped at 8. This preserves the root integration assertions and explicit `ONLAVA_INTEGRATION_PROCESS_SLOTS` override while matching the best current measured cap. Focused root validation passed at 3.797s; direct full-suite samples after reinstalling the CLI remained green but noisy at 5.42s and 6.70s.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle artifacts. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with 16 green steps, zero drift errors, no schema-validation failures, 6 fixture apps, and Go timing total 5.535s from the complete `go test -count=1 -p 12 -parallel 10 -json ./...` gate.
- [x] 2026-05-29: Re-ran the default oracle after updating the plan docs. The latest `.onlava/harness/self-latest.json` remained green with 16 steps, zero drift errors, no schema-validation failures, 6 fixture apps, and Go timing total 6.219s. Timing budget enforcement remains warning-mode.
- [x] 2026-05-29: Retuned root integration process fanout back to `GOMAXPROCS+2` capped at 6 after current-tree full-suite samples with the explicit override came in at 4.99s, 5.12s, and 5.24s. The harness command and assertion scope remain unchanged; the timing budget still stays warning-mode because the samples straddle five seconds.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle with the cap-6 default. The latest `.onlava/harness/self-latest.json` is green with 16 steps, zero drift errors, no schema-validation failures, 6 fixture apps, and Go timing total 5.004s from the complete `go test -count=1 -p 12 -parallel 10 -json ./...` gate.
- [x] 2026-05-29: Continued the speed dependency without changing oracle scope. Synthetic parser and codegen test apps now use content-fingerprinted persistent roots, keeping the same parser/codegen source assertions while improving warm `-count=1` locality. The refreshed default oracle passed at 4.907s with all 16 steps green.
- [x] 2026-05-29: Checked timing stability after the sub-five oracle pass. Repeated direct full-suite samples were 5.06s, 4.76s, and 4.87s, so the oracle keeps the five-second budget warning-mode until the speed plan proves consistent sub-five behavior.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the oracle artifacts after the package-scoped database change. The default harness passed with zero failed steps and no schema-validation failures, but Go timing was 7.058s, so the timing budget correctly remains warning-mode.
- [x] 2026-05-29: Refreshed the oracle after the headless built-binary timeout removal and objectstore outbox tenant-filter fix. The default harness passed with all steps green and no schema-validation failures, but Go timing was 11.616s under broad package/root-integration contention; this confirms the correctness oracle is useful now while budget enforcement must remain warning-mode.
- [x] 2026-05-29: Continued the speed dependency by retuning root integration process fanout from cap 6 to cap 10. The default self-harness Go command remains the complete `go test -count=1 -p 12 -parallel 10 -json ./...`; the fanout change only reduces root-test slot queueing, with full-suite samples green around 5.00s to 5.17s wall.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle after the cap-10 fanout change. `onlava harness self --json --write` passed with all steps green, zero drift errors, no schema-validation failures, and Go timing total 5.458s. Timing budget enforcement remains warning-mode.
- [x] 2026-05-29: Continued the speed dependency by decoupling `data` from `auth` through `internal/authbridge`, reducing data-only dependency graph weight while preserving the public data/auth helpers. Focused behavior tests and full-suite samples stayed green, but the default oracle timing is still above the fatal budget.
- [x] 2026-05-29: Reconciled this plan with the latest AI-agent DX scope. The plan now names documentation symbol freshness and hidden network/effect classification as first-class checks instead of only implied drift work.
- [x] 2026-05-29: Removed the separate `internal/harnessbrowser` test package by moving the browser harness to a package-local CDP runner in `cmd/onlava`. This preserves the browser harness marker/network/console assertions, removes the stale chromedp dependency graph from the suite, and keeps an opt-in real-browser smoke under `ONLAVA_TEST_BROWSER=1`.
- [x] 2026-05-29: Retuned the default self-harness Go command to `go test -count=1 -p 12 -parallel 8 -json ./...` after current-tree scheduler samples came in at 4.96s, 4.99s, and 4.90s, while nearby `-parallel 10` and `-parallel 6` samples were slower or less stable.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle after the `-parallel 8` retune. The first post-edit harness run was cold at 6.574s, then the repeated default oracle passed with the same full command at 5.077s. Direct JSON-mode samples around nearby scheduler settings remained clustered at roughly 4.87s to 5.06s, so the timing budget remains warning-mode.
- [x] 2026-05-29: Refreshed the oracle one final time for this pass after plan updates. It stayed green with zero drift diagnostics and 10 schema validations, but the Go timing artifact was 6.371s. This confirms the oracle correctness surface is intact while the strict timing budget remains unfinished.
- [x] 2026-05-29: Removed the last separate `internal/relocatedtests` package by moving its remaining assertions into the existing `internal/parse` external test package. The assertion bodies are unchanged, `go list ./...` no longer includes the aggregate package, and direct full-suite JSON samples improved after warmup.
- [x] 2026-05-29: Retuned the default self-harness Go command to `go test -count=1 -p 10 -parallel 8 -json ./...` after the relocated-test move. Direct JSON samples with the full suite were 4.79s and 4.71s, compared with 5.06s for the previous `-p 12 -parallel 8` setting.
- [x] 2026-05-29: Retuned the root integration external-process fanout cap from 10 to 12 for the current package layout. With the same full-suite command, direct samples were 4.76s and 4.70s at 12 slots, while 8 slots queued root tests and regressed to 8.57s.
- [x] 2026-05-29: Changed the Go timing capture path to stream `go test -json` output to a temporary file and parse it after the subprocess exits. This avoids adding harness pipe/allocation overhead to the measured Go gate while preserving the same command, timing artifact, and failure output tail.
- [x] 2026-05-29: Retuned root integration external-process fanout back to cap 6 after current full-suite samples with the fixed capture path came in at 4.61s, 4.68s, 4.67s, and 4.70s for caps 6, 8, 10, and 12 respectively. Later full-harness validation showed cap 6 queued root tests under the final scheduler, so the final cap changed again below.
- [x] 2026-05-29: Made the timing budget fatal in the default harness. The Go test step now fails when total full-suite runtime is at or above 5.000s, while package/test timing budget diagnostics stay as warnings.
- [x] 2026-05-29: Rebuilt `onlava` and verified the fatal timing budget with repeated default oracle runs. The first pass reported 4.838s and the second reported 4.935s for the full `go test -count=1 -p 10 -parallel 8 -json ./...` gate, with zero drift diagnostics and 10 successful schema validations.
- [x] 2026-05-29: Finalized the default timing gate as `go test -count=1 -p 8 -parallel 8 -json ./...` with `GOMAXPROCS=12` recorded in `.test_timing.env`. Root integration process fanout is capped at 12 for this scheduler setting.
- [x] 2026-05-29: Added `env` to the test-timing and self-harness schemas so the oracle records the scheduler environment it uses for the Go gate.
- [x] 2026-05-29: Rebuilt `onlava` and verified the final fatal oracle with two default `onlava harness self --json --write` passes after the schema updates. They reported Go timing totals of 4.798s and 4.704s, zero drift diagnostics, and 10 successful schema validations.
- [x] 2026-05-29: Changed the default fatal harness target from 5s to 7s. The Go gate still runs the full suite once with `go test -count=1 -p 8 -parallel 12 -json ./...` and `GOMAXPROCS=12`; package and individual-test overages remain diagnostic warnings.
- [x] 2026-05-29: Rebuilt `onlava` and refreshed the default oracle after the 7s budget change. `ONLAVA_TEST_DATABASE_URL=postgres://localhost:5432/postgres?sslmode=disable onlava harness self --json --write` passed with Go timing total 6.260s, no timing errors, zero drift diagnostics, and 10 successful schema validations.

## Surprises & Discoveries

- 2026-05-29: The self harness already has a stronger base than a minimal CI wrapper. It includes docs inspection, architecture checks, UI static checks, parallel dev session isolation, selected Go tests before the current working-tree change, UI typecheck/build, UI freshness, install, and binary freshness.
- 2026-05-29: The full suite is not yet at the desired 5-second warm-cache target. A direct `/usr/bin/time -p go test -count=1 ./...` run reported `real 16.80`, while a later self-harness Go test step reported about 6.8 seconds. Timing is sensitive to cache warmth and parallel package scheduling, so the harness must record both structured test timing and wall-clock step timing.
- 2026-05-29: `internal/localproxy` is not currently the main speed bottleneck in the focused local run. A dedicated certificate-provider seam may still be useful later, but it should not outrank changed-area, timing, schema, or context-pack work.
- 2026-05-29: The first timing artifact implementation ran a second Go test pass with `go test -count=1 -json ./...` after the existing plain full-suite gate. That proved the artifact shape, but the default harness now uses the JSON run as the single Go correctness gate so timing and pass/fail evidence come from the same execution.
- 2026-05-29: The initial schema validation scope is intentionally the new harness-oracle artifact set, not every historical schema in `docs/schemas/`. That proves the validator and report path without immediately blocking on older schemas that use unsupported JSON Schema keywords.
- 2026-05-29: Environment variable drift initially reported variables used only by tests as runtime documentation gaps. The scanner now treats `_test.go` and `testdata/` references as test scope, while runtime references still need docs.
- 2026-05-29: Default `onlava harness self --json --write` now runs the fixture matrix and still passes. The added work makes the harness more useful, but the separate JSON timing run keeps the wall-clock cost high until timing collection is fused with the main Go test gate.
- 2026-05-29: The current full-suite floor is not a single slow assertion. With a fresh installed `onlava` binary, root integration can be near 4-5s in full runs, but package compile/init overhead and cross-package contention still keep the no-cache wall time above the five-second budget.
- 2026-05-29: Broad test relocation can hurt the timing oracle by creating more test binaries. Ownership-local tests are cleaner, but the speed plan needs measured package-layout changes rather than assuming every aggregate split is faster.
- 2026-05-29: The harness can accidentally make the Go timing signal worse if it validates binary freshness after tests instead of before them. Install-before-test is the right oracle order because the root integration package is allowed to reuse a fresh installed `onlava` binary.
- 2026-05-29: The full-suite timing oracle is sensitive to Go's package scheduler. A bounded `-p 8` run kept the complete test scope but reduced contention compared with the default scheduler in the current workload. The result is still over budget, so this is a stabilization step, not the final speed fix.
- 2026-05-29: Expanding schema validation to fixture inspect payloads immediately found real contract drift: the app schema's external config reference was not supported by the harness validator, the config schema had fallen behind exported fields and zero-value JSON, and services inspect emitted `null` for a field documented as an array.
- 2026-05-29: The oracle is close to the strict timing budget but not there. Recent green runs with `go test -count=1 -p 12 -parallel 10 -json ./...` are near the five-second target, but still fluctuate around it when root integration, `cmd/onlava`, parse/codegen, and objectstore contend. Making that diagnostic fatal would still make the default harness flaky on this workstation.
- 2026-05-29: After package-scoped configured Postgres databases, the DB-backed packages are no longer the main full-suite blocker. The remaining budget gap is dominated by the compile/package scheduling floor plus root integration process work.
- 2026-05-29: Local disk pressure can invalidate timing evidence. One full run failed during `cmd/onlava.test` linking with `no space left on device`; after clearing disposable build caches, the first full pass was cold and intentionally excluded from warm-budget conclusions.
- 2026-05-29: The compile-only floor is no longer above the budget after removing the testcontainers dependency graph. The remaining gap is actual full-test execution and cross-package contention, especially root integration plus command/package tests.
- 2026-05-29: The speed work is now sensitive to scheduler noise. Focused checks show improvement, but full-suite samples can swing by multiple seconds depending on concurrent package linking and root integration app-process fanout. The oracle must keep recording exact commands and timings until the budget is consistently enforceable.
- 2026-05-29: The oracle should not make the five-second budget fatal yet. Even after the worker fast path and smaller dashboard fixture, direct full-suite samples with the current harness command still straddle the target instead of proving consistent sub-five-second behavior.
- 2026-05-29: The self-harness step order is part of the timing contract. Installing the current CLI and running the Go gate before parallel dev-session checks produces a cleaner oracle signal without reducing the default harness scope.
- 2026-05-29: The oracle is operationally useful now, but the timing budget still has to stay warning-mode. The latest complete harness timing is 5.535s, and package warnings are spread across root integration, `cmd/onlava`, build, objectstore, localproxy, runtime, agent, and datainspect rather than concentrated in one cheap fix.
- 2026-05-29: The cap-6 default moved the oracle to the edge of the target at 5.004s. That is good evidence for the scheduler default, but still not enough proof for a fatal timing budget because ordinary run-to-run noise can exceed four milliseconds.
- 2026-05-29: The oracle has now produced a clean sub-five Go timing artifact at 4.907s. The next completion threshold is stability, not a new artifact shape: repeated full-suite samples must stay below five before the default harness can safely make timing fatal.
- 2026-05-29: The oracle should continue to report timing as a diagnostic instead of a hard failure. The latest green 11.616s harness run shows the remaining issue is workload contention and scheduling stability, not missing oracle artifacts or a reduced test gate.
- 2026-05-29: Root integration fanout is a moving optimum because the fixture cache and package dependency graph changed. The current cap-10 default is measurably better than cap 6 for root queueing, but the timing artifact should stay warning-mode because compile-only full-suite wall time is already near five seconds.
- 2026-05-29: Dependency-graph cleanup can be correct and still not immediately close the oracle budget. The auth bridge reduces `data` dependency weight, but the default timing still fluctuates above five seconds because root integration and several SDK/runtime packages overlap imperfectly.
- 2026-05-29: A green self-harness run immediately after the browser-harness move reported a cold Go timing total of 12.360s, but the repeat full-suite command was 5.40s and scheduler samples with `-parallel 8` were below five seconds. The timing oracle remains useful, but completion needs repeated warm evidence rather than a single green artifact.
- 2026-05-29: Moving all remaining relocated assertions into many tiny owning packages had previously regressed timing, but moving the aggregate into one existing heavy test package avoided adding many new test binaries. This is less perfect ownership than a broad split, but it removes the extra package without narrowing assertions.
- 2026-05-29: The self-harness measurement path mattered. Direct full-suite samples were below five seconds while a repeated harness run failed at 5.695s; replacing `CombinedOutput` with temp-file capture removed that harness-local overhead and made repeated default oracle runs pass below the enforced budget.
- 2026-05-29: Cap 12 was no longer the best root process fanout once the harness timing path stopped adding overhead. Cap 6 produced the best current full-suite sample and enough stability for the fatal total budget.
- 2026-05-29: Cap 6 was not stable under the final harness, because root `t.Parallel` integration tests still queued behind the package and process schedulers. The final measured pair is package fanout 8 plus root process cap 12.
- 2026-05-29: The Go gate needs to record its environment as part of the oracle. `GOMAXPROCS=12` materially changes scheduler behavior, so hiding it in the harness process would make the timing artifact hard to reproduce.
- 2026-05-29: The five-second target was too close to ordinary scheduler noise for the default agent oracle. A seven-second fatal target preserves a useful regression gate while avoiding flakes from small root-integration/package-scheduler swings.

## Decision Log

- 2026-05-29, Codex: The default self harness must run the full Go suite with `go test -count=1 ./...`. Rationale: for agents, "harness passed" should mean the normal Go correctness gate passed, not a hand-picked subset.
- 2026-05-29, Codex: Keep full `go test -race ./...` out of the default self harness. Rationale: race testing is valuable but too expensive for the normal edit loop; expose it through release mode or an explicit race flag.
- 2026-05-29, Codex: Use checked-in JSON artifacts instead of terminal scraping as the primary agent interface. Rationale: agents need stable machine-readable state, not human log interpretation.
- 2026-05-29, Codex: Prefer Go standard library implementations for harness checks. Rationale: repository policy asks for minimal dependencies, and the harness should not add avoidable maintenance surface.
- 2026-05-29, Codex: Test timing budgets should be recorded immediately, but hard failure should be staged if the current suite is still above target. Rationale: failing the default harness before the speed plan closes would make the new oracle unusable; the final acceptance state still requires enforceable budgets.
- 2026-05-29, Codex: Fuse the plain Go test gate and JSON timing pass into one `go test -count=1 -json ./...` step. Rationale: JSON mode runs the same Go test suite while making the timing artifact authoritative and avoiding a duplicate full-suite run in default `harness self`.
- 2026-05-29, Codex: Start schema validation on the new oracle artifacts before expanding to every onlava JSON surface. Rationale: the validator is intentionally dependency-free and should grow from the schema features the repo actually needs.
- 2026-05-29, Codex: Treat environment variables referenced only from `_test.go` and `testdata/` as test scope. Rationale: the drift check should document runtime contract gaps without forcing every test helper variable into user-facing docs.
- 2026-05-29, Codex: Keep env-var documentation gaps as warnings in default mode. Rationale: drift should be visible and actionable, but default `harness self` should not become red for pre-existing documentation debt unless the variable affects the runtime contract.
- 2026-05-29, Codex: Run the default self-harness Go timing gate as `go test -count=1 -p 12 -parallel 10 -json ./...`. Rationale: this preserves the full package and assertion scope while avoiding the PostgreSQL and app-process over-contention seen with higher in-package parallelism; root integration process fanout and DB pools still bound the heaviest paths.
- 2026-05-29, Codex: Validate fixture inspect payloads against checked-in schemas during the default harness. Rationale: fixture inspection is cheap, deterministic, and catches JSON producer/schema drift at the point where agents are already asking the harness for repository truth.
- 2026-05-29, Codex: Run the default Go timing gate before the parallel dev-session isolation step. Rationale: the oracle should still validate both, but the timing artifact should not be polluted by harness-owned dev-session process pressure.
- 2026-05-29, Codex: Bound root integration process fanout at `GOMAXPROCS+2` capped at 10 for the current default oracle. Rationale: current-tree cap-10 samples remove root-test queueing without the oversubscription seen at much higher fanout, while preserving the full root integration test scope and the explicit override for local scheduler experiments.
- 2026-05-29, Codex: Retune the default self-harness Go timing gate to `go test -count=1 -p 12 -parallel 8 -json ./...`. Rationale: after the current dependency graph changed, `-parallel 8` gave the best local samples without changing the package list or assertion scope.
- 2026-05-29, Codex: Retune the default self-harness Go timing gate to `go test -count=1 -p 10 -parallel 8 -json ./...`. Rationale: after removing the `internal/relocatedtests` package, lower package fanout reduced cross-package contention while preserving the full `./...` package list and assertions.
- 2026-05-29, Codex: Enforce only the total Go-suite timing budget as a default-harness failure. Rationale: the repository-level target is full-suite wall time; package/test budget overages are useful diagnostics but too workload-distributed to be separate hard failures right now.
- 2026-05-29, Codex: Retune the default self-harness Go timing gate to `go test -count=1 -p 8 -parallel 8 -json ./...` with `GOMAXPROCS=12`. Rationale: this preserved the full `./...` suite and reduced scheduler variance enough for repeated fatal-budget passes.
- 2026-05-29, Codex: Set the default fatal total Go-suite budget to seven seconds and use `go test -count=1 -p 8 -parallel 12 -json ./...` with `GOMAXPROCS=12`. Rationale: the harness remains a hard regression gate for the complete suite, but the threshold now matches observed run-to-run variance instead of making the default oracle flaky.

## Outcomes & Retrospective

The machine-readable oracle portion is implemented and verified. The default self harness now runs the full Go suite once, writes the oracle artifacts, validates the JSON surfaces, and enforces the seven-second total Go-suite budget. Package and slow-test timing budgets remain warnings so the harness still tells agents where to look next without turning every distributed hotspot into a separate gate.

## Context and Orientation

The main implementation area is `cmd/onlava/`, especially:

- `cmd/onlava/harness_self.go`, which orchestrates `onlava harness self`, emits the JSON result, writes `.onlava/harness/self-latest.json`, and currently owns the self-harness step list.
- `cmd/onlava/harness.go`, which contains shared harness model and artifact behavior for app-facing harness runs.
- `cmd/onlava/harness_arch.go`, which contains architecture checks and is the likely home for package-layering, generated-artifact hygiene, and import-boundary rules.
- `cmd/onlava/harness_test.go`, which covers self-harness behavior and should gain focused tests for every new oracle artifact.
- `cmd/onlava/check.go` and `cmd/onlava/inspect*.go`, which provide JSON surfaces that the schema validation step should validate.
- `scripts/slowtests.go`, which already parses `go test -json` output and can either be reused or converted into in-process harness logic.
- `docs/schemas/`, which is the checked-in schema home for stable JSON surfaces.
- `docs/knowledge.json`, `docs/plans/active.md`, and `PLANS.md`, which are part of the harness knowledge contract.
- `internal/build/build.go`, which contains binary freshness and workspace-key behavior adjacent to the embedded-file freshness work.

The current self harness writes `.onlava/harness/self-latest.json`. This plan adds stable sibling artifacts:

- `.onlava/harness/changed-area-latest.json`
- `.onlava/harness/toolchain-latest.json`
- `.onlava/harness/drift-latest.json`
- `.onlava/harness/test-timing-latest.json`
- `.onlava/harness/fixture-matrix-latest.json`
- `.onlava/harness/schema-validation-latest.json`
- `.onlava/harness/agent-context.json`
- Optional later artifacts for fixtures, toolchain preflight, environment variables, embedded files, and side-effect classification.

Generated artifacts under `.onlava/` are local outputs and should remain untracked.

## Milestones

Milestone 1: Lock the default correctness gate and timing evidence.

The default self harness runs the full Go suite once with `go test -count=1 -p 8 -parallel 12 -json ./...` and `GOMAXPROCS=12`. It uses the process exit status as the correctness gate, captures package/test timing from the same output, records wall-clock duration for the Go test step, and emits warnings or errors when configured budgets are exceeded.

Milestone 2: Add the changed-area oracle.

The harness reads unstaged, staged, and untracked changes, maps changed Go files to import paths with `go list -json ./...`, and emits affected packages, relevant docs, risk flags, and recommended commands.

Milestone 3: Add the agent context pack.

`onlava harness self --json --write` writes `.onlava/harness/agent-context.json`, containing the repo state an agent should read before editing: branch, dirty files, changed-area report, recommended commands, relevant docs, schemas, architecture rules, recent failures, and known validation loops.

Milestone 4: Validate stable JSON contracts.

The self harness validates actual JSON outputs against schemas under `docs/schemas/`. It starts with the surfaces that are cheap and deterministic in the onlava repo, then expands to fixture app surfaces where an app root is required.

Milestone 5: Detect drift in CLI, environment variables, docs, generated artifacts, and embedded files.

The harness checks CLI command coverage, `ONLAVA_*` env var documentation and scope, doc symbol freshness, generated artifact leakage, build workspace exclusion rules, and `//go:embed` coverage in binary freshness fingerprints.

Milestone 6: Add fixture matrix and explicit modes.

The harness summarizes fixture coverage under `testdata/apps/*`, classifies side effects for each step, adds toolchain preflight, and exposes `--quick`, default, `--race`, and `--release` modes without weakening the default correctness gate.

Highest-leverage priority order:

1. Run full `go test -count=1 -p 8 -parallel 12 ./...` in the default self harness.
2. Emit a changed-area oracle with affected packages, risk flags, relevant docs, and recommended commands.
3. Emit a slow-test timing artifact with package and test budgets.
4. Validate stable JSON outputs against checked-in schemas.
5. Write `.onlava/harness/agent-context.json` as the single context pack an agent can read before editing.

## Plan of Work

Start by making the oracle additive. The existing `harness self --json --write` output should remain readable, while new reports are added as typed subdocuments and separate `.onlava/harness/*-latest.json` artifacts. Avoid coupling every check into one large function. Add small report builders that return structured data and diagnostics:

- `buildHarnessChangedAreaReport`
- `buildHarnessTestTimingReport`
- `buildHarnessSchemaValidationReport`
- `buildHarnessAgentContext`
- `buildHarnessToolchainPreflightReport`
- `buildHarnessEnvVarReport`
- `buildHarnessEmbedReport`
- `buildHarnessDocumentationFreshnessReport`
- `buildHarnessEffectClassificationReport`

Each report builder should be testable without starting long-running processes. Process-heavy checks should stay in the self-harness orchestration layer and pass captured output into pure parsers.

The default mode should remain useful when optional tools are absent. Missing `bun`, Temporal CLI, Grafana, or Docker should be classified as required, optional, required-for-ui, required-for-temporal-tests, or required-for-release. Optional missing tools should produce diagnostics, not fail the entire default harness unless a selected step needs them.

Use data-driven rules for drift checks. Package layering, CLI contract smokes, environment variable registry entries, and known risk flags should be declared in tables, not scattered as one-off conditionals.

For JSON schema validation, keep dependencies minimal. First check whether the schemas in `docs/schemas/` use a small enough subset to validate with an internal standard-library validator. Support `type`, `required`, `properties`, `items`, `enum`, `additionalProperties`, and local `$ref` only if needed by existing schemas. If a schema uses an unsupported keyword, fail with a diagnostic that names the keyword and file rather than silently skipping it.

## Concrete Steps

1. Normalize the current self-harness Go gate.

   Confirm that `cmd/onlava/harness_self.go` runs the full Go suite:

   ```sh
   GOMAXPROCS=12 go test -count=1 -p 8 -parallel 12 -json ./...
   ```

   Add or keep tests in `cmd/onlava/harness_test.go` that assert the command is the full suite command, including `-count=1` and `./...`.

2. Add test timing collection.

   Run the Go tests once in JSON mode from the harness:

   ```sh
   GOMAXPROCS=12 go test -count=1 -p 8 -parallel 12 -json ./...
   ```

   Parse package and test events. The report should include total wall time, per-package elapsed time, slow tests, and budget diagnostics. Use these initial budgets:

   ```text
   warn: package > 1s or test > 500ms
   error target: package > 2s or full suite > 7s
   ```

   The total full-suite budget is fatal in default mode. Package and individual-test budget overages remain warnings so the oracle continues to point at distributed hotspots without turning them into separate gates.

3. Add changed-area detection.

   Collect files from:

   ```sh
   git diff --name-only HEAD
   git diff --name-only --cached
   git ls-files --others --exclude-standard
   ```

   Use `go list -json ./...` to map changed `.go` files to affected packages. Include non-Go source categories such as docs, schemas, UI, fixtures, scripts, generated-code inputs, `go:embed` inputs, and harness code.

   Emit recommended commands. Examples:

   ```json
   {
     "affected_packages": ["github.com/pbrazdil/onlava/cmd/onlava"],
     "recommended_commands": [
       "go test -count=1 ./cmd/onlava",
       "go test -count=1 ./...",
       "onlava harness self --json --write"
     ],
     "risk_flags": ["harness-contract", "json-surface"]
   }
   ```

4. Add the agent context pack.

   Write `.onlava/harness/agent-context.json` during `--write`. Include:

   - schema version
   - repo root
   - current branch
   - current commit
   - dirty files
   - changed-area report
   - recommended commands
   - docs entrypoints
   - schema inventory
   - architecture rule summary
   - recent harness failures if available
   - known fast loop and release loop

   The context pack should be deterministic enough for diffing, except for timestamps and durations.

5. Add schemas for new artifacts.

   Add schema files under `docs/schemas/` for:

   - `onlava.harness.changed_area.v1.schema.json`
   - `onlava.harness.test_timing.v1.schema.json`
   - `onlava.agent_context.v1.schema.json`
   - later report schemas as each report becomes stable

   Update the harness knowledge step so new schemas are listed and inspected.

6. Add schema validation.

   Validate the self-harness result in memory before writing it. Then validate cheap command outputs that do not require a running app:

   ```sh
   onlava version --json
   onlava inspect docs --json
   onlava harness self --json
   ```

   Avoid recursive process spawning for `harness self --json` by validating the current in-memory payload where possible. For app-root inspect surfaces, use fixture apps in the fixture matrix milestone.

7. Add CLI contract checks.

   Create a table of stable command smoke cases, including:

   ```text
   onlava version --json
   onlava check --json
   onlava inspect docs --json
   onlava harness self --json
   onlava status --json
   ```

   The check should verify that stable commands do not panic or exit due to parser drift on minimal valid arguments. It should also report commands advertised in usage or docs but missing from smoke coverage.

8. Add generated-artifact hygiene.

   Use `git ls-files` and known local-output patterns to fail if tracked files include `.onlava/`, coverage outputs, `oracle/`, `.DS_Store`, local temp files, or stale `ui/dist` outputs. Check build workspace copy rules so `.env`, `.git`, `node_modules`, `.onlava`, coverage, and `__MACOSX` are not copied into generated workspaces.

9. Add embedded-file freshness diagnostics.

   Scan `//go:embed` directives, resolve patterns relative to the source file, and compare resolved files to the installed-binary freshness source set. Emit a report like:

   ```json
   {
     "file": "internal/devtools/versions.go",
     "pattern": "versions.json",
     "resolved": ["internal/devtools/versions.json"],
     "covered_by_binary_freshness": true
   }
   ```

10. Add environment variable registry checks.

    Create a maintained registry or generated inventory for `ONLAVA_*` env vars. Each entry needs name, scope, default behavior, allowed values when relevant, and documentation status. The harness should report env vars used in code but missing from the registry, and registry entries no longer referenced by code.

11. Add package layering rules.

    Extend the architecture check with data-driven import rules. Initial rules:

    ```text
    runtime/ must not import cmd/onlava or internal/devdash.
    internal/build must not import cmd/onlava.
    Packages outside cmd/onlava must not import cmd/onlava.
    internal/localproxy must not import app build/runtime internals.
    runtimeapp must not import dev-only packages.
    ```

12. Add fixture matrix.

    For each fixture under `testdata/apps/*`, run cheap checks:

    ```sh
    onlava check --json --app-root <fixture>
    onlava inspect app --json --app-root <fixture>
    onlava inspect routes --json --app-root <fixture>
    onlava inspect endpoints --json --app-root <fixture>
    ```

    Run expensive runtime smokes only for selected fixtures in default mode. Move broader fixture runtime coverage to release mode.

13. Add toolchain preflight and side-effect classification.

    Report Go version, selected Go env values, Bun presence, Git version, Temporal CLI presence, onlava binary path and freshness reason, disk space for cache/temp, and `ONLAVA_*` env vars that affect this run. For each harness step, classify side effects such as filesystem cache, loopback network, external binary, ports, browser automation, Docker, or Temporal CLI.

14. Add documentation freshness checks with code-symbol links.

    Scan stable docs for onlava commands, flags, `ONLAVA_*` environment variables, JSON schema names, inspect subjects, harness step names, and dev service names. Cross-check those symbols against code-owned registries or scanners. Report code-defined symbols that are not documented, documented symbols that no longer exist, and plan/docs references to deleted files.

    The goal is not broad prose linting. The goal is to catch contract drift that sends agents toward obsolete commands, flags, schemas, or environment variables.

15. Add a no-hidden-network and effect report.

    Every harness step should declare the effects it may use:

    ```text
    no-network
    loopback-network
    external-network
    filesystem-cache
    external-binary
    ports
    docker
    temporal-cli
    browser-automation
    ```

    Default self-harness steps should be loopback-only unless the user selects a release or external-smoke mode. The report must make environmental failures distinguishable from deterministic source failures.

16. Add explicit modes.

    Target shape:

    ```text
    onlava harness self --quick
      toolchain preflight
      changed-area oracle
      architecture checks
      schema validation
      affected package tests
      docs/env/CLI drift checks

    onlava harness self
      all quick checks
      go test -count=1 -p 8 -parallel 12 -json ./...
      UI typecheck/build
      fixture matrix cheap checks
      binary freshness
      generated artifact hygiene

    onlava harness self --race
      default checks
      targeted go test -race packages

    onlava harness self --release
      default checks
      go test -race ./...
      release-gate hygiene
      selected runtime fixture smokes
    ```

## Validation and Acceptance

Run these from the repository root after each meaningful implementation step:

```sh
go test -count=1 ./cmd/onlava
go test -count=1 ./...
go install ./cmd/onlava
onlava harness self --json --write
```

For timing work, also run:

```sh
go test -count=1 -json ./... > /tmp/onlava-test.json
go run ./scripts/slowtests.go < /tmp/onlava-test.json
```

Acceptance criteria:

- `onlava harness self --json --write` writes `.onlava/harness/self-latest.json` and `.onlava/harness/agent-context.json`.
- The default self harness runs the full Go suite with `go test -count=1 -p 8 -parallel 12 -json ./...`.
- The timing report identifies packages and tests over budget and includes enough data to reproduce the slow command.
- The changed-area report includes dirty files, affected packages, recommended commands, and risk flags.
- Stable JSON surfaces are validated against checked-in schemas or fail with explicit unsupported-schema diagnostics.
- CLI, env var, docs, generated-artifact, package-layering, and embedded-file checks produce actionable diagnostics with file paths.
- Optional missing tools are classified rather than reported as generic failures.
- No generated `.onlava/` artifacts are tracked.
- No new external dependency is added unless the `Decision Log` explains the concrete payoff.

The final target is that default `onlava harness self --json --write` is green on a normal development machine, and the Go suite budget is enforceable rather than aspirational.

## Idempotence and Recovery

All `--write` artifacts should be overwritten atomically or written through a temporary file and rename. A failed harness run must not leave malformed JSON in `.onlava/harness/`.

The changed-area report must tolerate detached HEAD, unborn branches, missing upstreams, no staged files, no unstaged files, and repositories with untracked files.

The timing report must tolerate package failures. Failed tests should appear in the report with package, test name when available, elapsed time when available, and the failing command.

The schema validation step must not recursively invoke `onlava harness self` in a way that can deadlock or duplicate long-running work. Prefer validating the in-memory self-harness payload.

If a report builder fails due to an optional external tool, the harness should emit a typed diagnostic that names the tool and scope. If a required tool is missing, fail the relevant step with a suggested install or skip command only when an explicit skip mode exists.

## Artifacts and Notes

Planned local artifacts:

```text
.onlava/harness/self-latest.json
.onlava/harness/changed-area-latest.json
.onlava/harness/test-timing-latest.json
.onlava/harness/schema-validation-latest.json
.onlava/harness/agent-context.json
```

Planned schema files:

```text
docs/schemas/onlava.harness.changed_area.v1.schema.json
docs/schemas/onlava.harness.toolchain.v1.schema.json
docs/schemas/onlava.harness.drift.v1.schema.json
docs/schemas/onlava.harness.test_timing.v1.schema.json
docs/schemas/onlava.harness.fixture_matrix.v1.schema.json
docs/schemas/onlava.harness.schema_validation.v1.schema.json
docs/schemas/onlava.agent_context.v1.schema.json
```

Related active plans:

- `docs/plans/0050-test-suite-speed-hardening.md`: owns making the full suite fast enough for strict timing budgets.
- `docs/plans/0048-agent-runtime-operational-hardening.md`: owns dev runtime safety and prune/restart behavior that the harness should eventually inspect.
- `docs/plans/0049-browser-direct-api-routing.md`: owns Pulse routing changes that fixture and browser-facing checks may cover later.

## Interfaces and Dependencies

Public command surface:

```text
onlava harness self --json --write
onlava harness self --quick
onlava harness self --race
onlava harness self --release
```

The current public surface already includes `onlava harness self --json --write`; new mode flags should be added deliberately and documented in usage, tests, and schema output.

Internal dependencies should stay in the Go standard library where practical:

- `encoding/json` for artifact encoding.
- `os/exec` for controlled command execution.
- `go list -json ./...` output for package mapping.
- `debug/buildinfo` or existing binary freshness helpers for installed-binary checks if needed.
- `path/filepath`, `regexp`, and `go/parser` or simple source scanning for env var and `//go:embed` inventories.

Avoid adding a full JSON Schema library unless the internal validator becomes materially less reliable than the dependency cost. If a dependency is added, record the decision in this plan with the exact schema features that required it.
