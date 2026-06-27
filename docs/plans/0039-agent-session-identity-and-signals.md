# Agent Session Identity and Signals

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series requires every local development session to have isolated identity in runtime metadata, auth/local URLs, logs, traces, metrics, and legacy async runtime task queues. The 0037 agent MVP records `session_id` and `runtime_app_id` in the session manifest, but the app runtime still primarily runs under the source app ID and most emitted signals are not session-scoped.

After this work, the runtime receives and exposes the agent session identity, dev output and observability records can be filtered by session, and legacy async runtime local development uses session-specific task queue/build identifiers so parallel worktrees cannot consume each other's work.

## Progress

* [x] 2026-05-26: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Pass `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, and `SCENERY_RUNTIME_APP_ID` into dev children.
* [x] 2026-05-26: Expose session/base/runtime identity fields through `scenery.Meta()` and `/__scenery/config`.
* [x] 2026-05-26: Add session fields to devdash app records and process output, expose `session_id` in logs JSONL, and support `scenery logs --session <id>`.
* [x] 2026-05-26: Add session fields to trace/log observability records and JSON inspect surfaces.
* [x] 2026-05-26: Attach session labels to traces and metrics emitted by the runtime and exported to Victoria.
* [x] 2026-05-26: Add `--session current|<id>` filters to `scenery logs`, `inspect traces`, and `inspect metrics`.
* [x] 2026-05-26: Scope local auth URLs and legacy async runtime task queue/deployment/build IDs to the session.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-26: Kept `Meta().AppID` as the source app ID for this slice and added `BaseAppID`, `RuntimeAppID`, and `SessionID` as additive metadata. Switching stored devdash app IDs to runtime IDs belongs with the devdash/session filtering milestone so dashboard records do not split unexpectedly.
* 2026-05-26: Standard auth reads app/API URLs through configurable env names plus fallbacks, so the dev supervisor now sets the configured env names and the fallback names. It also writes empty cookie-domain env overrides so local auth cookies stay host-only when a session URL is active.
* 2026-05-26: legacy async runtime task queue prefix did not previously have an environment override. Added `SCENERY_LEGACY_ASYNC_RUNTIME_TASK_QUEUE_PREFIX` so `scenery dev` can make a session-scoped task queue without mutating `.scenery.json`.

## Decision Log

* Decision: Prefer additive session fields over replacing app IDs in stored records.
  Rationale: Existing dashboards and scripts understand app IDs. Additive session fields preserve compatibility while enabling isolation and filtering.
  Date/Author: 2026-05-26 / Codex

* Decision: Scope local legacy async runtime by task queue/deployment/build ID in this milestone, not by namespace.
  Rationale: Task queue isolation is enough to stop parallel workers from consuming each other's work and avoids adding namespace lifecycle management before the daemon-owned legacy async runtime substrate work in 0040.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed on 2026-05-26.

Shipped outcome:

* `scenery dev` passes `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, and `SCENERY_RUNTIME_APP_ID` into app children and exposes the same identity through `scenery.Meta()` plus `/__scenery/config`.
* Devdash app records, process output, logs JSONL, trace summaries, trace events, log events, inspect traces, and inspect metrics now carry session identity where applicable.
* `scenery logs --session current|<id>`, `scenery inspect traces --session current|<id> --json`, and `scenery inspect metrics --session current|<id> --json` filter session-scoped local records.
* Runtime development reports propagate session identity into stored observability events and Victoria trace/log/metric labels, including `scenery.session_id` and `scenery_session_id`.
* `scenery dev` sets session-scoped standard-auth URL env vars and clears local auth cookie-domain env vars for host-only local cookies.
* `scenery dev` sets `SCENERY_LEGACY_ASYNC_RUNTIME_TASK_QUEUE_PREFIX`, `SCENERY_LEGACY_ASYNC_RUNTIME_DEPLOYMENT_NAME`, and `SCENERY_BUILD_ID` from the active session so local legacy async runtime workers do not share default queues/build IDs.

Validation:

```sh
go test ./cmd/scenery ./internal/devdash ./runtime
go test ./...
```

Focused and full Go tests passed. Final install and self-harness validation are tracked by the surrounding agent-native local-dev worktree validation.

## Context and Orientation

Relevant files:

```text
internal/agent/session.go
cmd/scenery/dev_supervisor.go
cmd/scenery/logs.go
cmd/scenery/inspect_observability.go
internal/devdash/*
runtime/observability.go
runtime/current.go
runtime/legacy-async-runtime*.go
auth/*
```

## Milestones

Milestone 1 propagates session identity from agent registration into the child runtime environment.

Milestone 2 stores session identity in devdash records and exposes it through existing JSON contracts.

Milestone 3 labels runtime traces/metrics/logs with session fields and adds session-aware inspect filters.

Milestone 4 scopes local auth URLs and legacy async runtime local worker identifiers by session.

## Plan of Work

Keep contracts versioned where schemas already exist. Where schema files cover JSON output, update the schemas and harness fixtures in the same change as the Go code.

## Concrete Steps

1. Extend agent/dev session registration so the supervisor has a stable `session_id` and `runtime_app_id` before launching child processes.
2. Pass `SCENERY_SESSION_ID`, `SCENERY_BASE_APP_ID`, and `SCENERY_RUNTIME_APP_ID` into app and worker children.
3. Add nullable session columns/fields to devdash app, process event, and process output records, preserving reads from older local stores.
4. Add session labels to runtime observability records and update inspect/log schemas where JSON contracts expose those records.
5. Add `--session current|<id>` filters to logs, traces, and metrics commands.
6. Scope standard local auth URLs and legacy async runtime task queue/build IDs to the active session.
7. Add tests for session propagation, filtering, schema updates, and legacy async runtime task queue isolation.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/scenery ./runtime ./internal/devdash
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Observable behavior:

* Session manifests and runtime metadata agree on `session_id` and `runtime_app_id`.
* `scenery logs --session current --json` only returns records for the current session.
* `scenery inspect traces --session current --json` and `scenery inspect metrics --session current --json` include and filter session labels.
* Parallel local legacy async runtime workers use distinct task queues/build IDs by default.

## Idempotence and Recovery

Session fields should be optional when reading older devdash stores so existing local caches continue to load.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/scenery/dev_supervisor.go
cmd/scenery/logs.go
cmd/scenery/inspect_observability.go
internal/devdash/*
runtime/observability.go
runtime/legacy-async-runtime*.go
docs/schemas/scenery.logs.event.v1.schema.json
docs/schemas/scenery.inspect.traces.v1.schema.json
docs/schemas/scenery.inspect.metrics.v1.schema.json
```

## Interfaces and Dependencies

No new external dependencies expected.
