# Agent Global Dashboard

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series calls for one machine-local daemon/router and one dashboard. The current agent-native local-dev implementation has an agent router and hidden session dashboard backends, but `onlava dev` still starts a dashboard server per attached worktree and the agent mainly routes browser traffic to those per-session servers.

After this work, the local agent owns the dashboard HTTP surface and dashboard store, `console.onlava.localhost` is a true global dashboard, and `/s/<session_id>` selects session context inside that global UI. Per-session app processes continue to report status, logs, traces, metrics, and removed agent transport data through the same JSON contracts, but the visible dashboard process no longer belongs to one worktree.

## Progress

* [x] 2026-05-27: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-27: Move dashboard store ownership under the agent state directory when the agent is active.
* [x] 2026-05-27: Run an agent-owned dashboard server and route `console.onlava.localhost` plus `/s/<session_id>` through it.
* [x] 2026-05-27: Add session-addressable dashboard app records so a global store can list multiple sessions for the same base app.
* [x] 2026-05-27: Change `onlava dev` so the visible dashboard route points at the agent dashboard instead of a per-session dashboard backend.
* [x] 2026-05-27: Preserve a direct/per-session dashboard fallback when the agent is disabled or unavailable.
* [x] 2026-05-27: Update console URLs, session manifests, docs, schemas, and harness checks.
* [x] 2026-05-27: Add focused tests and run repository validation.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-27: Dashboard store ownership can move before the global dashboard server. When an agent is reachable and `ONLAVA_DEV_CACHE_DIR` is not explicitly set, store resolution now points to `<agent-dir>/dashboard`; agent-disabled fallback keeps the previous user-cache behavior.

* 2026-05-27: A true global dashboard cannot key its app list only by `app_id`: concurrent sessions from two worktrees of the same app overwrite each other. `internal/devdash` now keeps the legacy `apps` table as the latest-by-app compatibility view and adds `app_sessions` keyed by session route id for global dashboard routing.

* 2026-05-27: The dashboard HTTP handlers can be reused by the agent once they depend on a small controller interface instead of directly reaching through `devSupervisor`. The agent command now starts one private dashboard backend and the agent router sends `console.onlava.localhost` traffic to it.

* 2026-05-27: `onlava dev` no longer starts or registers a per-session dashboard/removed agent transport backend on the normal active-agent path. Runtime reports post to the agent dashboard route with a per-session report token carried over the Unix-socket control API and omitted from session manifests.

## Decision Log

* Decision: Keep the existing per-session dashboard as a fallback while introducing the global dashboard.
  Rationale: The dashboard is central to local diagnosis. Agent unavailability should not make `onlava dev` unusable, and a fallback lets the migration land without removing the existing debug path.
  Date/Author: 2026-05-27 / Codex

## Outcomes & Retrospective

Completed on 2026-05-27.

Shipped outcome:

* `onlava agent` now starts one private dashboard backend and the router sends `console.onlava.localhost/s/<session_id>` traffic to it.
* `onlava dev` no longer starts or registers a per-session dashboard/removed agent transport backend on the normal active-agent path. Runtime reports post to the agent dashboard route with a per-session report token carried over the Unix-socket control API and omitted from manifests.
* The dashboard store lives under the agent directory when the agent is active and stores session-addressable app records so multiple worktrees for one base app can be listed independently.
* Direct/per-session dashboard behavior remains as fallback when the agent is disabled, unavailable, or an explicit local proxy needs the older upstream.

Validation:

```sh
go test ./cmd/onlava ./internal/agent ./internal/devdash
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

All validation passed. The self harness wrote `.onlava/harness/self-latest.json` and reported existing review/large-file warnings, including `cmd/onlava/dev_supervisor.go`, but no errors.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-onlava-agent-mvp.md
cmd/onlava/agent.go
cmd/onlava/dashboard.go
cmd/onlava/dashboard_rpc.go
cmd/onlava/dev_supervisor.go
cmd/onlava/removed-agent-transport.go
cmd/onlava/watch.go
internal/agent/*
internal/devdash/*
docs/local-contract.md
docs/schemas/onlava.run.event.v1.schema.json
```

Current state:

* `onlava agent` owns the control socket, registry, and router.
* `onlava agent` starts one private dashboard backend using the agent-owned dashboard store and routes `console.onlava.localhost/s/<session_id>` traffic to it.
* `onlava dev` reports into the agent-owned dashboard backend on the normal active-agent path. Direct/per-session dashboard servers remain fallback behavior for agent-disabled, unavailable-agent, or explicit local-proxy paths.
* The dashboard store has both a legacy latest-by-app `apps` view and session-addressable `app_sessions` records for global dashboard routing.
* The next migration step is to broaden browser/harness coverage around the global dashboard route and keep tightening session-scoped dashboard RPCs.

## Milestones

Milestone 1 defines the global dashboard routing contract and keeps the Unix-socket control API protected by filesystem permissions.

Milestone 2 moves dashboard storage to the agent state directory when the agent is active, while preserving old local cache reads.

Milestone 3 starts one dashboard server from the agent command and registers it as the global console backend.

Milestone 4 changes dev supervisors to report process and runtime events into the agent-owned dashboard store/server without owning a visible dashboard backend.

Milestone 5 updates removed agent transport/browser routing, docs, schemas, and harness coverage.

## Plan of Work

Start by making the control contract explicit and testable. Prefer additive routing and store changes so the existing dashboard UI and RPC handlers can be reused.

## Concrete Steps

1. Teach the router to serve a global dashboard backend for `console.onlava.localhost` and pass `/s/<session_id>` context through headers or query state.
2. Move dashboard store path resolution so an active agent uses `<agent-dir>/dashboard` and non-agent fallback keeps the current cache behavior.
3. Start a dashboard server from the agent command using the global store, and make session dev supervisors skip their own dashboard HTTP listener when the agent dashboard is available.
6. Redirect dev supervisor notifications/reports to the global dashboard path and keep direct dashboard fallback for `ONLAVA_AGENT_DISABLE=1` or agent startup failure.
7. Update console/banner URLs, session manifests, `onlava status --json`, docs, and self-harness expectations.
8. Add unit tests for global route construction, fallback behavior, and store path selection.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/onlava ./internal/agent ./internal/devdash
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
git diff --check
```

Observable behavior:

* Multiple `onlava dev` sessions share one dashboard process when the agent is active.
* `console.onlava.localhost/s/<session_id>` works even when the session app backend is private.
* `onlava dev` still has a usable dashboard fallback when the agent is disabled or unavailable.
* The dashboard store can show sessions from multiple worktrees without per-session port conflicts.

## Idempotence and Recovery

Dashboard store migration must tolerate older per-user stores and should not delete existing per-session data.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/onlava/agent.go
cmd/onlava/dashboard*.go
cmd/onlava/dev_supervisor.go
cmd/onlava/watch.go
internal/agent/*
internal/devdash/*
docs/local-contract.md
docs/plans/0042-agent-global-dashboard.md
```

## Interfaces and Dependencies

No new external dependencies expected.

The agent control socket remains protected by filesystem permissions. Browser and removed agent transport routes are machine-local development surfaces and do not use an additional browser token.
