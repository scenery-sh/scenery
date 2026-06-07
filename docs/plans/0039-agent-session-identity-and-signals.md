# Agent Session Identity and Signals

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series requires every local development session to have isolated identity in runtime metadata, auth/local URLs, logs, traces, metrics, and Temporal task queues. The 0037 agent MVP records `session_id` and `runtime_app_id` in the session manifest, but the app runtime still primarily runs under the source app ID and most emitted signals are not session-scoped.

After this work, the runtime receives and exposes the agent session identity, dev output and observability records can be filtered by session, and Temporal local development uses session-specific task queue/build identifiers so parallel worktrees cannot consume each other's work.

## Progress

* [x] 2026-05-26: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Pass `ONLAVA_SESSION_ID`, `ONLAVA_BASE_APP_ID`, and `ONLAVA_RUNTIME_APP_ID` into dev children.
* [x] 2026-05-26: Expose session/base/runtime identity fields through `onlava.Meta()` and `/__onlava/config`.
* [x] 2026-05-26: Add session fields to devdash app records and process output, expose `session_id` in logs JSONL, and support `onlava logs --session <id>`.
* [x] 2026-05-26: Add session fields to trace/log observability records and JSON inspect surfaces.
* [x] 2026-05-26: Attach session labels to traces and metrics emitted by the runtime and exported to Victoria.
* [x] 2026-05-26: Add `--session current|<id>` filters to `onlava logs`, `inspect traces`, and `inspect metrics`.
* [x] 2026-05-26: Scope local auth URLs and Temporal task queue/deployment/build IDs to the session.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-26: Kept `Meta().AppID` as the source app ID for this slice and added `BaseAppID`, `RuntimeAppID`, and `SessionID` as additive metadata. Switching stored devdash app IDs to runtime IDs belongs with the devdash/session filtering milestone so dashboard records do not split unexpectedly.
* 2026-05-26: Standard auth reads app/API URLs through configurable env names plus fallbacks, so the dev supervisor now sets the configured env names and the fallback names. It also writes empty cookie-domain env overrides so local auth cookies stay host-only when a session URL is active.
* 2026-05-26: Temporal task queue prefix did not previously have an environment override. Added `ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX` so `onlava dev` can make a session-scoped task queue without mutating `.onlava.json`.

## Decision Log

* Decision: Prefer additive session fields over replacing app IDs in stored records.
  Rationale: Existing dashboards and scripts understand app IDs. Additive session fields preserve compatibility while enabling isolation and filtering.
  Date/Author: 2026-05-26 / Codex

* Decision: Scope local Temporal by task queue/deployment/build ID in this milestone, not by namespace.
  Rationale: Task queue isolation is enough to stop parallel workers from consuming each other's work and avoids adding namespace lifecycle management before the daemon-owned Temporal substrate work in 0040.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed on 2026-05-26.

Shipped outcome:

* `onlava dev` passes `ONLAVA_SESSION_ID`, `ONLAVA_BASE_APP_ID`, and `ONLAVA_RUNTIME_APP_ID` into app children and exposes the same identity through `onlava.Meta()` plus `/__onlava/config`.
* Devdash app records, process output, logs JSONL, trace summaries, trace events, log events, inspect traces, and inspect metrics now carry session identity where applicable.
* `onlava logs --session current|<id>`, `onlava inspect traces --session current|<id> --json`, and `onlava inspect metrics --session current|<id> --json` filter session-scoped local records.
* Runtime development reports propagate session identity into stored observability events and Victoria trace/log/metric labels, including `onlava.session_id` and `onlava_session_id`.
* `onlava dev` sets session-scoped standard-auth URL env vars and clears local auth cookie-domain env vars for host-only local cookies.
* `onlava dev` sets `ONLAVA_TEMPORAL_TASK_QUEUE_PREFIX`, `ONLAVA_TEMPORAL_DEPLOYMENT_NAME`, and `ONLAVA_BUILD_ID` from the active session so local Temporal workers do not share default queues/build IDs.

Validation:

```sh
go test ./cmd/onlava ./internal/devdash ./runtime
go test ./...
```

Focused and full Go tests passed. Final install and self-harness validation are tracked by the surrounding agent-native local-dev worktree validation.

## Context and Orientation

Relevant files:

```text
internal/agent/session.go
cmd/onlava/dev_supervisor.go
cmd/onlava/logs.go
cmd/onlava/inspect_observability.go
internal/devdash/*
runtime/observability.go
runtime/current.go
runtime/temporal*.go
auth/*
```

## Milestones

Milestone 1 propagates session identity from agent registration into the child runtime environment.

Milestone 2 stores session identity in devdash records and exposes it through existing JSON contracts.

Milestone 3 labels runtime traces/metrics/logs with session fields and adds session-aware inspect filters.

Milestone 4 scopes local auth URLs and Temporal local worker identifiers by session.

## Plan of Work

Keep contracts versioned where schemas already exist. Where schema files cover JSON output, update the schemas and harness fixtures in the same change as the Go code.

## Concrete Steps

1. Extend agent/dev session registration so the supervisor has a stable `session_id` and `runtime_app_id` before launching child processes.
2. Pass `ONLAVA_SESSION_ID`, `ONLAVA_BASE_APP_ID`, and `ONLAVA_RUNTIME_APP_ID` into app and worker children.
3. Add nullable session columns/fields to devdash app, process event, and process output records, preserving reads from older local stores.
4. Add session labels to runtime observability records and update inspect/log schemas where JSON contracts expose those records.
5. Add `--session current|<id>` filters to logs, traces, and metrics commands.
6. Scope standard local auth URLs and Temporal task queue/build IDs to the active session.
7. Add tests for session propagation, filtering, schema updates, and Temporal task queue isolation.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/onlava ./runtime ./internal/devdash
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Observable behavior:

* Session manifests and runtime metadata agree on `session_id` and `runtime_app_id`.
* `onlava logs --session current --json` only returns records for the current session.
* `onlava inspect traces --session current --json` and `onlava inspect metrics --session current --json` include and filter session labels.
* Parallel local Temporal workers use distinct task queues/build IDs by default.

## Idempotence and Recovery

Session fields should be optional when reading older devdash stores so existing local caches continue to load.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/onlava/dev_supervisor.go
cmd/onlava/logs.go
cmd/onlava/inspect_observability.go
internal/devdash/*
runtime/observability.go
runtime/temporal*.go
docs/schemas/onlava.logs.event.v1.schema.json
docs/schemas/onlava.inspect.traces.v1.schema.json
docs/schemas/onlava.inspect.metrics.v1.schema.json
```

## Interfaces and Dependencies

No new external dependencies expected.
