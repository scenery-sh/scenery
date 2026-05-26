# Temporal Worker Production Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

onlava has removed Pub/Sub as a public/runtime concept and now uses `github.com/pbrazdil/onlava/temporal` as the single async runtime surface. The current implementation is a strong v0: app code declares typed workflows and activities, codegen imports declaration packages, `ONLAVA_ROLE=api|worker|all` separates API and worker processes, `onlava dev` can supervise a local Temporal dev server, cron can run through Temporal Schedules, and worker manifests/bindings exist.

The remaining work is production hardening. The central risk is that ONLV and onlava still have places that behave like an in-process queue once API and workers are split into separate processes. In production, any process polling a Temporal task queue can receive any workflow or activity task for that queue, workflow and activity results must be durable, and deployment promotion must be explicit rather than an incidental side effect of worker startup.

This plan turns the static review dated 2026-05-26 into executable work. It spans this repository and the companion app repository at `/Users/petrbrazdil/Repos/onlv`. It should keep dependencies minimal and use the Go standard library unless a Temporal SDK feature already present in the dependency graph is required.

## Progress

* [x] 2026-05-26: Create this ExecPlan as `docs/plans/0035-temporal-worker-production-hardening.md` and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Fix `onlava worker --task-queue` so selected worker processes only poll the requested declared task queue or queues.
* [x] 2026-05-26: Remove the activity task-queue fallback footgun by requiring explicit activity task queues.
* [x] 2026-05-26: Add strict `temporal.Start` options and typed workflow handles. `Start` initially required explicit `WithWorkflowID` or `WithWorkflowIDPrefix`; this was later replaced by required positional identity.
* [x] 2026-05-26: Make workflow identity compile-time required for `temporal.Start`; old three-argument calls no longer compile.
* [x] 2026-05-26: Add parser/check diagnostics for empty literal `temporal.ActivityConfig{}` task queues and legacy three-argument `temporal.Start` calls.
* [x] 2026-05-26: Add workflow ID conflict/reuse policy support plus typed signal, query, and update helpers.
* [x] 2026-05-26: Migrate ONLV `temporal.Start` call sites to deterministic workflow IDs and idempotent conflict/reuse policy.
* [x] 2026-05-26: Replace ONLV in-memory waiter and cross-process stream-sink patterns with workflow results or durable status/event storage. Codex request/response waits use workflow results; jobs Codex streaming persists events through the existing submission log store from worker-local execution context.
* [x] 2026-05-26: Collapse ONLV house/maps multi-stage activity-starts-workflow chains into parent workflows that execute the stages as activities.
* [x] 2026-05-26: Gate Worker Deployment version promotion so production workers do not self-promote on startup.
* [x] 2026-05-26: Expose Temporal cron policy knobs and use scheduled start time where available.
* [x] 2026-05-26: Strengthen worker manifests with `onlava.worker.manifest.v2`, production Temporal TLS/API-key connection options, payload codec validation, explicit Worker Deployment operator commands, and docs.
* [x] 2026-05-26: Make ONLV Temporal configuration explicit and remove unused RabbitMQ dependency residue from `go.mod`/`go.sum`.
* [x] 2026-05-26: Run onlava and ONLV validation and record outcomes. `go test -timeout=10m ./...` passes in onlava; `go test ./...`, `onlava check --json`, and `onlava harness --json --write` pass in ONLV.

## Surprises & Discoveries

* 2026-05-26: `cmd/onlava/worker.go` parses `--task-queue` and passes `ONLAVA_TEMPORAL_TASK_QUEUE=<queue>` into the generated app process, but `temporal.startWorkerRuntime` groups all declarations by resolved queue and starts a worker for every queue without reading that environment variable.
* 2026-05-26: `temporal.ExecuteActivity` only sets `workflow.ActivityOptions.TaskQueue` when `ActivityConfig.TaskQueue` is non-empty, while activity registration resolves an empty task queue to `defaultWorkerTaskQueue(info)`. Temporal itself inherits the workflow task queue when an activity task queue is omitted, so workflows on custom queues can schedule activities to a queue where the activity is not registered.
* 2026-05-26: `temporal.startWorkerRuntime` and `runtime.startTemporalCronRuntime` call `runtime.EnsureTemporalWorkerDeploymentCurrentVersion` after worker startup. That is convenient in local development but too aggressive for production because any worker with any `ONLAVA_BUILD_ID` can promote itself current.
* 2026-05-26: ONLV still contains process-local waiter or stream callback patterns in `/Users/petrbrazdil/Repos/onlv/codexsvc/exec_async.go`, `/Users/petrbrazdil/Repos/onlv/house/process_async.go`, and `/Users/petrbrazdil/Repos/onlv/maps/earth_async.go`. These patterns work only when API and workers share memory.
* 2026-05-26: ONLV staged flows start downstream workflows from activities in `/Users/petrbrazdil/Repos/onlv/house/process_async.go` and `/Users/petrbrazdil/Repos/onlv/maps/earth_async.go`. Activity retry can create duplicate downstream workflows unless the workflow IDs and conflict policies are made deterministic and idempotent.
* 2026-05-26: `docs/schemas/onlava.worker.manifest.v1.schema.json` models task queues as strings and activities as a flat list. It cannot validate that all workers sharing a queue have identical workflow/activity registrations.
* 2026-05-26: `go test ./cmd/onlava` can fail when `TestDashboardDataRPC` cannot start a PostgreSQL testcontainer because Docker is unavailable. A later `onlava harness self --json --write` run passed after the environment was able to satisfy its command set; targeted command tests for worker parsing also pass with `go test ./cmd/onlava -run 'TestParseWorkerArgs|TestWorkerCommandUsesWorkerPath'`.
* 2026-05-26: A full `go test ./...` run spawned long-running `onlava dev` subprocesses under `cmd/onlava` and had to be terminated during this implementation slice. Use targeted tests first, then rerun full validation once the dev subprocess issue is isolated.
* 2026-05-26: The first StartOptions implementation is strict at runtime but still allows `temporal.Start(ctx, workflow, input)` to compile because the options are variadic. That violates the no-compatibility rule and must be replaced with a compile-time required workflow identity argument.
* 2026-05-26: Static review of `/Users/petrbrazdil/Repos/onlv` still finds waiters/stream sinks and bare `temporal.Start` calls in `codexsvc`, `jobs`, `house`, and `maps`; the onlava API work is not enough until those app flows are migrated.
* 2026-05-26: `go test -timeout=3m ./...` from `/Users/petrbrazdil/Repos/onlava` still fails in the top-level `github.com/pbrazdil/onlava` integration package: `TestOnlavaRunBasicApp` and `TestOnlavaRunMiddlewareApp` report servers did not start, several tests time out waiting for the integration process slot, and the package panics at the 3-minute test timeout. The package-level targeted tests and self harness pass.
* 2026-05-26: The full onlava test failure was reproducible as generated fixture app startup failure, not a Temporal runtime issue. The child process output showed `missing required local env file`; adding explicit empty `.env` files to fixtures that run locally preserves the strict local secret-file contract and lets `go test -timeout=10m ./...` pass.
* 2026-05-26: ONLV no longer imports RabbitMQ anywhere in source. `github.com/rabbitmq/amqp091-go` existed only in `go.mod`/`go.sum`, so it was async-broker residue and could be removed.

## Decision Log

* Decision: Treat this as staged production hardening, not as another compatibility migration.
  Rationale: Pub/Sub is already gone. The work should make the native Temporal API operationally correct when API and worker processes are separate.
  Date/Author: 2026-05-26 / Codex

* Decision: Implement task-queue selection before broader API changes.
  Rationale: `onlava worker --task-queue` is already public CLI surface and currently appears ineffective. Fixing it reduces production risk immediately and gives ONLV a deployable worker layout.
  Date/Author: 2026-05-26 / Codex

* Decision: Prefer strict explicit `ActivityConfig.TaskQueue` over an implicit colocated activity default for now.
  Rationale: The safe colocated behavior would require static or generated knowledge of which workflows reference which activities. Requiring an explicit queue is boring, inspectable, and avoids Temporal's inherited-task-queue mismatch.
  Date/Author: 2026-05-26 / Codex

* Decision: Default `temporal.Start` should not pin executions to the API process build ID once production deployment promotion is controlled by Temporal Worker Deployment current/ramping state.
  Rationale: Always setting a `VersioningOverride` can defeat server-side current/ramping behavior. Pinning should be opt-in through a start option or an explicit config default.
  Date/Author: 2026-05-26 / Codex

* Decision: ONLV synchronous waits should use workflow results when the request can reasonably block, and durable status/event storage when the work is long-running or streaming.
  Rationale: Process-local waiter channels and stream maps cannot work across separate API and worker processes. Temporal workflow state and app-owned durable storage survive process boundaries.
  Date/Author: 2026-05-26 / Codex

* Decision: Use a new worker manifest schema version if the manifest shape changes from flat queues/activities to queue-level registrations and hashes.
  Rationale: The current schema is strict and versioned as `onlava.worker.manifest.v1`. A shape change should be explicit rather than silently breaking existing external-worker manifests.
  Date/Author: 2026-05-26 / Codex

* Decision: Do not preserve legacy Temporal start behavior while adding start options.
  Rationale: The implementation is onlava-native production hardening, not a compatibility layer. `temporal.Start` should require explicit workflow identity options so production callers cannot accidentally create random workflow IDs.
  Date/Author: 2026-05-26 / Codex

* Decision: Make workflow identity a required positional argument to `temporal.Start`, not a variadic option.
  Rationale: A runtime error for missing `WithWorkflowID` still lets old call sites compile. The public API should force source migrations and make deterministic workflow identity visible at every start site.
  Date/Author: 2026-05-26 / Codex

* Decision: Keep ONLV Codex streaming as worker-local execution with durable DB log writes rather than routing streaming events through a second Temporal workflow.
  Rationale: The remaining stream sink is not a cross-process completion map. It is a context callback used while the jobs activity runs and writes durable submission logs. The process-local unsafe pattern was the previous global waiter/sink coordination, which has been removed.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed pending PR creation. onlava now has strict task-queue selection, explicit activity queues, compile-time workflow identity, typed workflow handles/operations, local-only auto-promotion, explicit Temporal Worker Deployment operator commands, cron schedule policy controls, worker manifest v2 queue registration hashes, and production Temporal TLS/API-key/payload profile validation. ONLV has deterministic starts, parent workflows for staged house/maps work, workflow-result waits for bounded async calls, durable jobs log streaming, explicit Temporal config, and no RabbitMQ dependency.

## Context and Orientation

Key onlava concepts:

* API role: generated app runtime with `ONLAVA_ROLE=api`; it starts HTTP services and should not poll normal workflow/activity queues.
* Worker role: generated app runtime with `ONLAVA_ROLE=worker`; it should poll only the requested Temporal task queues.
* All role: generated app runtime with `ONLAVA_ROLE=all`; local development can run HTTP and workers in one process.
* Task queue: Temporal routing name. Any worker polling the same task queue must register compatible workflow and activity types.
* Worker Deployment version: Temporal deployment name plus build ID. Current/ramping promotion controls which worker build gets new executions unless starts explicitly override versioning.
* Durable status/events: state written to Temporal workflow results, the app database, object storage, or another persistent store instead of process-local Go channels or maps.

Relevant onlava files:

```text
cmd/onlava/worker.go
cmd/onlava/inspect.go
cmd/onlava/run_json_test.go
cmd/onlava/inspect_test.go
cmd/onlava/dev_supervisor.go
cmd/onlava/temporal_dev.go
runtime/app.go
runtime/temporal.go
runtime/temporal_test.go
runtime/cron.go
runtime/registry.go
temporal/temporal.go
temporal/temporal_test.go
internal/app/root.go
internal/app/root_test.go
internal/codegen/generator.go
internal/codegen/generator_test.go
internal/parse/parser.go
internal/parse/parser_test.go
internal/workers/*
docs/local-contract.md
docs/app-development-cookbook.md
docs/schemas/onlava.inspect.temporal.v1.schema.json
docs/schemas/onlava.worker.manifest.v1.schema.json
testdata/apps/cron/*
```

Relevant ONLV files:

```text
/Users/petrbrazdil/Repos/onlv/.onlava.json
/Users/petrbrazdil/Repos/onlv/go.mod
/Users/petrbrazdil/Repos/onlv/codexsvc/exec_async.go
/Users/petrbrazdil/Repos/onlv/jobs/submissions_async.go
/Users/petrbrazdil/Repos/onlv/house/process_async.go
/Users/petrbrazdil/Repos/onlv/house/process_runs.go
/Users/petrbrazdil/Repos/onlv/maps/earth_async.go
/Users/petrbrazdil/Repos/onlv/maps/earth.go
```

The current public Temporal package lives at `temporal/temporal.go`. It exposes `NewWorkflow`, `NewActivity`, `Start`, `ExecuteActivity`, `MethodActivity`, `RegisterServiceAccessorFor`, `Run.Get`, `Run.ID`, and `Run.RunID`. The implementation registers declarations in memory during package init and starts SDK workers from `startWorkerRuntime`.

## Milestones

Milestone 1 fixes worker task-queue selection. `onlava worker --task-queue <queue>` and `ONLAVA_TEMPORAL_TASK_QUEUE=<queue>[,<queue>]` must start workers only for those declared queues and fail clearly when a selected queue has no declarations.

Milestone 2 removes ambiguous activity routing and adds the start/handle API needed by real apps. Empty activity task queues must no longer silently register on one queue but execute on another. `temporal.Start` must make workflow identity compile-time required, then accept non-identity options such as task queue, memo, search attributes, timeouts, conflict policy, and opt-in version pinning. Old three-argument start calls should fail to compile.

Milestone 3 migrates ONLV away from process-local async coordination. Short request/response waits should return workflow results through `Run.Get`. Long-running or streaming jobs should write durable job status and events and return a stable job/workflow ID. No process-local waiter, callback, stream sink, or channel map should remain in the async completion path.

Milestone 4 turns staged ONLV pipelines into durable orchestration. House and maps should use parent workflows with multiple activities, or child workflows started from workflow code, instead of activities starting follow-up workflows.

Milestone 5 gates Worker Deployment promotion and cron policy. Local development can self-promote worker builds for convenience. Production and cloud modes must require explicit operator commands for current/ramping/drain changes.

Milestone 6 hardens external workers and production Temporal connectivity. Worker manifests should validate queue-level registrations, connection config should support TLS/API-key inputs, and payload codec configuration should become an enforced runtime profile rather than a string in manifests only.

## Plan of Work

Start with the behavior that is already user-facing: worker queue selection. Add a small parser for the comma-separated `ONLAVA_TEMPORAL_TASK_QUEUE` value, allow repeated `--task-queue` flags in the CLI, and factor worker declaration filtering into a testable helper. The helper should compare selected queues against declaration-resolved queues after applying app task-queue defaults.

Then fix activity task-queue semantics before apps rely on the current fallback. The preferred implementation is to require `ActivityConfig.TaskQueue` in `NewActivity` and in static parser/inspect checks where practical. Update all onlava fixtures and ONLV activities to set explicit task queues. If implementation discovers a stronger generated colocated default, record the evidence and decision before changing course.

Next replace the current `temporal.Start` shape with a source-breaking API that requires workflow identity as a positional argument. Add deterministic workflow ID support first because ONLV already has business identifiers such as `job_id`, `attempt_id`, and request IDs. Then add typed handles for existing workflows so API processes can reconnect to a workflow by ID and run ID without preserving Go memory. Do not keep an overload, shim, alias, or deprecated helper for the old three-argument start form.

After the onlava API exists, migrate ONLV. Do not preserve the in-memory waiter abstraction behind a new name. For Codex, jobs, house, and maps, choose one of two durable patterns per flow: return a workflow result for bounded synchronous waits, or return a workflow/job ID and persist status/events for later polling or streaming. Where stage chaining exists, move orchestration into workflow code.

Finally harden production operations. Split local convenience behavior from production behavior for Worker Deployment promotion, expose explicit CLI commands for promotion/ramping/drain, make cron schedule policy explicit, strengthen worker manifest/schema validation so task-queue collisions are caught before runtime, and make ONLV's `.onlava.json` Temporal block explicit for local and production examples.

## Concrete Steps

1. In `cmd/onlava/worker.go`, change `workerOptions.TaskQueue` to a slice, allow multiple `--task-queue` flags, and split comma-separated values. Preserve the current one-flag case. Pass a normalized comma-separated `ONLAVA_TEMPORAL_TASK_QUEUE` value to the app process.
1. In `temporal/temporal.go`, add a helper like `selectedTemporalTaskQueuesFromEnv()` and a pure filtering helper that takes declaration-resolved `byQueue` data and selected queues. Use it in `startWorkerRuntime` before creating SDK workers. Return an error listing unmatched selected queues.
1. Add tests in `temporal/temporal_test.go` for no selection, one selected queue, multiple selected queues, comma whitespace, and unknown selected queues. Add CLI parser tests in `cmd/onlava/worker_test.go` or the existing worker test file.
1. Update `onlava inspect temporal --json` and `docs/schemas/onlava.inspect.temporal.v1.schema.json` if needed so declared queues and worker-manifest queues are visible enough for operators to choose correct `--task-queue` values.
1. Make `ActivityConfig.TaskQueue` explicit. Prefer a `NewActivity` panic for empty task queues plus static parser diagnostics where the parser can see an empty literal config. Update tests and fixtures that currently use `temporal.ActivityConfig{}`.
1. Update `ExecuteActivity` so it always sets the resolved activity task queue. If resolution needs runtime info that is unsafe in workflow replay, do not read process env in workflow code; instead require explicit config and use `a.config.TaskQueue` directly.
1. Replace `temporal.Start` with a source-breaking, compile-time strict workflow identity API. Use a concrete identity value so the old three-argument call does not compile. One acceptable shape is:

   ```go
   type WorkflowIdentity struct { /* unexported fields */ }
   func WorkflowID(id string) WorkflowIdentity
   func WorkflowIDPrefix(prefix string) WorkflowIdentity
   func Start[I, O any](ctx context.Context, workflow *Workflow[I, O], input I, identity WorkflowIdentity, opts ...StartOption) (Run[O], error)
   ```

   `WorkflowIdentity` constructors must reject empty strings, and `Start` must reject a zero identity defensively. Remove `WorkflowID` and `WorkflowIDPrefix` from `WorkflowConfig`; do not add them back under a different name.
1. Keep non-identity `StartOption` support for routing, metadata, timeouts, conflict/reuse policy, and opt-in version pinning:

   ```go
   type StartOption func(*StartWorkflowOptions)
   func WithTaskQueue(queue string) StartOption
   func WithMemo(memo map[string]any) StartOption
   func WithSearchAttributes(attrs map[string]any) StartOption
   func WithExecutionTimeout(d time.Duration) StartOption
   func WithRunTimeout(d time.Duration) StartOption
   func WithTaskTimeout(d time.Duration) StartOption
   func WithWorkflowIDConflictPolicy(policy WorkflowIDConflictPolicy) StartOption
   func WithWorkflowIDReusePolicy(policy WorkflowIDReusePolicy) StartOption
   func WithPinnedBuildID(buildID string) StartOption
   ```

   Define small onlava enums for conflict/reuse behavior instead of leaking raw SDK enum names into app code. At minimum support fail-on-running, use-existing-running, and reject-completed-duplicate where the SDK supports those semantics.
1. Add typed handle support:

   ```go
   func GetWorkflow[O any](ctx context.Context, workflowID, runID string) Run[O]
   func (r Run[O]) Cancel(ctx context.Context) error
   func (r Run[O]) Terminate(ctx context.Context, reason string) error
   ```

1. Add typed signal, query, and update helpers unless the SDK mapping proves too large for this plan. If deferred, record a new ExecPlan or explicit follow-up issue before marking this plan complete:

   ```go
   func Signal[O, I any](ctx context.Context, run Run[O], name string, input I) error
   func Query[O, R any](ctx context.Context, run Run[R], name string) (O, error)
   func Update[I, O, R any](ctx context.Context, run Run[R], name string, input I) (O, error)
   ```

1. Stop setting `VersioningOverride` on every `Start` by default once Worker Deployment promotion is operator-controlled. Keep explicit opt-in through `WithPinnedBuildID` or a similarly named option. Remove dead default-versioning helper APIs if they become unused.
1. Update `internal/parse` and `onlava check` so literal `temporal.NewActivity(..., temporal.ActivityConfig{}, ...)` or `ActivityConfig{TaskQueue: ""}` produces a source diagnostic. Also flag bare three-argument `temporal.Start` calls once the strict start signature is in place, so agents get a direct migration message instead of only a Go type error.
1. Update `onlava inspect temporal --json` and `docs/schemas/onlava.inspect.temporal.v1.schema.json` so declared Go workflow/activity queues are visible enough for operators to choose correct `--task-queue` values. Include declaration kind, name, resolved task queue, and whether the queue came from an explicit config or default.
1. Migrate ONLV `temporal.Start` call sites to deterministic IDs such as `jobs.submission-attempt.<attempt_id>`, `house.process.<job_id>`, `maps.earth.generate.<job_id>`, and `codex.exec.<job_id>`. Pick a conflict policy that makes retries idempotent and document it in code comments only where the policy is not obvious.
1. Replace ONLV waiting flows. For bounded flows, make workflows return response structs and have API handlers call `run.Get(ctx)`. For streaming or long-running flows, persist events/status in durable app storage and return IDs to clients. Remove process-local waiter maps and stream-sink maps from cross-process completion paths; tests should prove API and worker can be separate processes.
1. Replace ONLV activity-to-workflow chains. In house, a parent scene-processing workflow should execute roofmapnet then rooftopology as activities or child workflows. In maps, generation and save should be coordinated by workflow code. Do not pass local temp directory paths across stages unless both stages are explicitly constrained to the same host; prefer durable artifact storage.
1. Add production gating around `EnsureTemporalWorkerDeploymentCurrentVersion`. Local mode may call it automatically. Production/cloud modes should not. Add explicit CLI commands under an appropriate command group, for example `onlava temporal deployment set-current`, `onlava temporal deployment ramp`, and `onlava temporal deployment drain`.
1. Extend Temporal cron config and docs for overlap policy, catchup window, pause-on-failure, and per-job activity timeout/retry. Use scheduled start time from Temporal schedule metadata where available instead of `time.Now()` inside `runTemporalCronActivity`.
1. Introduce a stronger worker manifest shape, preferably as `onlava.worker.manifest.v2`, with queue-level registrations:

   ```json
   {
     "task_queues": [
       {
         "name": "q1",
         "activities": ["email.Send/v1"],
         "workflows": [],
         "registration_hash": "sha256:..."
       }
     ]
   }
   ```

   Validate that manifests sharing a task queue have the same registration hash, external workers do not accidentally share Go queues, namespace and payload codec match the app, and every external activity is declared by onlava source.
1. Extend Temporal connection config in `internal/app/root.go`, `runtime/temporal.go`, and docs to cover TLS/mTLS/API-key environment variables and a real `onlava-json-v1` payload codec profile. Keep config validation strict and fail with actionable messages when required env vars are missing.
1. Update `docs/local-contract.md`, `docs/app-development-cookbook.md`, inspect schemas, generated binding docs, and ONLV `.onlava.json` examples to make Temporal production behavior explicit. Add explicit ONLV local config for `temporal.enabled`, `temporal.mode`, `temporal.namespace`, `temporal.task_queue_prefix`, and local `auto_start`, plus production env var docs for `TEMPORAL_ADDRESS`, `TEMPORAL_NAMESPACE`, `ONLAVA_BUILD_ID`, `ONLAVA_TEMPORAL_DEPLOYMENT_NAME`, and `ONLAVA_TEMPORAL_VERSIONING_BEHAVIOR`.
1. Audit `/Users/petrbrazdil/Repos/onlv/go.mod` for `github.com/rabbitmq/amqp091-go`. If RabbitMQ is still used for a non-Temporal domain, document that scope. If it is async-broker residue, remove it and any dead code/tests that only supported the old broker path.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/onlava` after each onlava milestone:

```sh
gofmt -w <changed-go-files>
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Run targeted checks when their area changes:

```sh
go test ./temporal ./runtime ./cmd/onlava ./internal/app ./internal/workers
onlava inspect temporal --json
onlava worker --task-queue <declared-queue>
onlava worker --task-queue <missing-queue>
```

Run from `/Users/petrbrazdil/Repos/onlv` after ONLV milestones:

```sh
onlava check --json
go test ./codexsvc ./jobs ./maps
go test ./house
onlava harness --json --write
git diff --check
```

`go test ./house` may be blocked in this environment by the pre-existing native roofmapnet dependency error for missing torch headers. If so, record the exact command output in Surprises & Discoveries and run the narrower tests that do not require the native dependency.

Acceptance criteria:

```text
- `onlava worker --task-queue q` starts workers only for q and fails when q is not declared.
- Empty activity task queues no longer produce a registration/execution mismatch.
- Old three-argument `temporal.Start(ctx, workflow, input)` calls fail to compile; starts require an explicit workflow identity argument.
- `temporal.Start` supports deterministic workflow IDs, conflict/reuse policy, task queue override, memo/search attributes, timeouts, and opt-in version pinning.
- Static checks flag empty activity task queues and non-strict Temporal start declarations where the parser can identify them.
- `onlava inspect temporal --json` shows declared Go workflow/activity queues as well as worker-manifest queues.
- ONLV async APIs no longer depend on process-local waiter channels or process-local stream sink maps for cross-process completion.
- ONLV staged house/maps pipelines are visible as parent workflow orchestration or child workflows, not hidden downstream starts from activities.
- Production workers do not call SetCurrentVersion on startup.
- Cron schedule policy is explicit and documented.
- Worker manifests can detect incompatible registrations on the same task queue.
- Temporal production connection settings and payload codec behavior are validated and documented.
```

## Idempotence and Recovery

Queue filtering is additive, but the strict `temporal.Start` signature is intentionally source-breaking. If a step fails, rerun tests after reverting only the files changed in that step; do not revert unrelated user work in the repository.

When changing public Temporal API behavior, do not preserve the old call form with a shim or compatibility alias. Update all onlava fixtures, parser tests, generated examples, and ONLV call sites in the same branch before treating the milestone as complete.

ONLV migrations should be done flow by flow. Each flow should compile and pass its package tests before moving to the next one. If durable event storage requires schema work, land the onlava API changes first and leave the ONLV flow using workflow results only where that is correct.

Worker Deployment promotion changes must preserve local dev convenience. If explicit production CLI commands are not complete, the safe interim state is: local mode may self-promote; non-local modes log or return an actionable error instructing the operator to promote explicitly.

Manifest schema changes should not silently reinterpret old manifest files. Either keep v1 validation working with a deprecation warning or fail with a clear message that the manifest must be upgraded to the new schema version.

## Artifacts and Notes

Expected onlava artifacts:

```text
temporal/temporal.go
temporal/temporal_test.go
runtime/temporal.go
runtime/temporal_test.go
runtime/cron.go
runtime/cron_test.go
cmd/onlava/worker.go
cmd/onlava/worker_test.go
cmd/onlava/inspect.go
cmd/onlava/inspect_test.go
cmd/onlava/temporal_deployment.go
internal/app/root.go
internal/app/root_test.go
internal/workers/*
docs/schemas/onlava.inspect.temporal.v1.schema.json
docs/schemas/onlava.worker.manifest.v2.schema.json
docs/local-contract.md
docs/app-development-cookbook.md
```

Expected ONLV artifacts:

```text
/Users/petrbrazdil/Repos/onlv/.onlava.json
/Users/petrbrazdil/Repos/onlv/codexsvc/exec_async.go
/Users/petrbrazdil/Repos/onlv/jobs/submissions_async.go
/Users/petrbrazdil/Repos/onlv/house/process_async.go
/Users/petrbrazdil/Repos/onlv/maps/earth_async.go
/Users/petrbrazdil/Repos/onlv/go.mod
```

Useful grep checks while working:

```sh
rg -n "register.*Waiter|waiter|completeWaiter|streamSink|stream sink" /Users/petrbrazdil/Repos/onlv/codexsvc /Users/petrbrazdil/Repos/onlv/jobs /Users/petrbrazdil/Repos/onlv/house /Users/petrbrazdil/Repos/onlv/maps -g '*.go'
rg -n "temporal\\.Start\\([^,]+,[^,]+,[^)]+\\)|ExecuteActivity\\(|NewActivity\\(|NewWorkflow\\(" /Users/petrbrazdil/Repos/onlv -g '*.go'
rg -n "github.com/rabbitmq/amqp091-go|amqp091|rabbitmq" /Users/petrbrazdil/Repos/onlv -g '*.go' -g 'go.mod'
rg -n "EnsureTemporalWorkerDeploymentCurrentVersion|VersioningOverride|ONLAVA_TEMPORAL_TASK_QUEUE|ActivityConfig\\{\\}" . -g '*.go'
```

## Interfaces and Dependencies

The intended public Temporal API after this plan includes the current surface plus options and handles:

```go
temporal.NewWorkflow[I, O](name, cfg, handler)
temporal.NewActivity[I, O](name, cfg, handler)
temporal.ExecuteActivity(ctx, activity, input)
temporal.Start(ctx, workflow, input, temporal.WorkflowID(id), opts...)
temporal.Start(ctx, workflow, input, temporal.WorkflowIDPrefix(prefix), opts...)
temporal.GetWorkflow[O](ctx, workflowID, runID)
temporal.WithTaskQueue(queue)
temporal.WithMemo(memo)
temporal.WithSearchAttributes(attrs)
temporal.WithExecutionTimeout(d)
temporal.WithRunTimeout(d)
temporal.WithTaskTimeout(d)
temporal.WithWorkflowIDConflictPolicy(policy)
temporal.WithWorkflowIDReusePolicy(policy)
temporal.WithPinnedBuildID(buildID)
temporal.Signal(ctx, run, name, input)
temporal.Query[O, R](ctx, run, name)
temporal.Update[I, O, R](ctx, run, name, input)
```

The operational interface after this plan should include:

```sh
onlava worker --task-queue <queue>
onlava worker --task-queue <queue-a> --task-queue <queue-b>
ONLAVA_TEMPORAL_TASK_QUEUE=<queue-a>,<queue-b> <generated-app>
onlava inspect temporal --json
onlava temporal deployment set-current ...
onlava temporal deployment ramp ...
onlava temporal deployment drain ...
```

No new non-standard-library Go dependency is expected. The work should use the Temporal SDK already present in the module for Worker Deployment, schedule policy, TLS/API-key client options, and data-converter integration.
