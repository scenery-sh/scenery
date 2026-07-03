# 0095 - Symphony Hardening

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Harden the Symphony dashboard and auto-runner shipped by `docs/plans/0092-symphony-dashboard.md`.

The hardening pass closes the indirect auto-run escalation path on the unauthenticated dashboard WebSocket, adds run leases so stale active rows recover after dashboard or runner crashes, separates retry attempts from upstream `max_turns`, fixes app-server client races, makes latest-run selection deterministic, and keeps task workspaces clean across retries and terminal cleanup.

The user-facing contract after this plan is implemented:

```text
Unauthenticated dashboard RPC cannot escalate a workflow into auto mode.
The local CLI is the sanctioned trust path for enabling Symphony auto mode.
Queued/running runs have leases and expired leases become terminal stalled runs.
Timed-out and app-server-stalled runs route tasks to Rework.
Restarted dashboards can reclaim tasks wedged by stale active runs.
agent.max_attempts controls retry attempts; agent.max_turns is not used as retry count.
Existing task worktrees are reset before retry.
Terminal task worktrees are removed from the Symphony cache and Git worktree registry.
```

## Progress

* [x] 2026-07-03 - Read root `AGENTS.md`, `apps/consolenext/AGENTS.md`, `PLANS.md`, `docs/plans/0092-symphony-dashboard.md`, `docs/local-contract.md`, `docs/agent-guide.md`, and `docs/tech-debt.md`; `scenery inspect docs --json` reported no review-due or stale docs.
* [x] 2026-07-03 - Implemented the local trust split: same-origin dashboard WebSocket origin checks, dashboard RPC rejection for manual/disabled to `auto`, and `scenery symphony auto --on|--off [--app-root <path>]` as the sanctioned local path.
* [x] 2026-07-03 - Implemented ExecPlan 0092's recovery rules with run leases, runner heartbeats, expired-run `stalled` marking, and task release for stale active runs.
* [x] 2026-07-03 - Added `agent.max_attempts`, `agent.turn_timeout_ms`, and `agent.stall_timeout_ms` parsing; `agent.max_turns` is no longer used as a retry-attempt bound.
* [x] 2026-07-03 - Fixed `codexAppServerClient` notification races by installing handlers before process start, calling handlers before `turnDone` close, and protecting shared result state.
* [x] 2026-07-03 - Switched latest-run selection from `max(created_at)` to `max(attempt)`.
* [x] 2026-07-03 - Added workspace reset before retry and terminal-workspace cleanup under `<dashboard-cache-root>/workspaces`.
* [x] 2026-07-03 - Ran focused race validation: `go test ./internal/symphony -count=1 -race` and `go test ./cmd/scenery -run 'TestDashboardSymphony|TestDashboardRPC|TestHarnessBrowser|TestCodexAppServerClient|TestPrepareSymphonyWorkspace|TestCleanupSymphonyRunWorkspace|TestDashboardWebSocketOriginCheck' -count=1 -race`.
* [x] 2026-07-03 - Ran full Go and frontend validation: `go test ./...`, `bun run lint`, `bun run typecheck`, `bun run build`, and `./scripts/build-dashboard-ui-embed.sh`.
* [x] 2026-07-03 - Ran `scenery harness self --summary --write`; all steps passed except the existing managed Postgres service probe, which failed because Docker had no `scenery-postgres` container and `.scenery/harness/bin/scenery db server start --json` failed with the same missing-container inspect error.

## Surprises & Discoveries

* `cmd/scenery/dashboard.go` still had `CheckOrigin: func(*http.Request) bool { return true }`. The hardening slice now accepts missing `Origin` for non-browser clients but requires browser origins to match `req.Host`.
* The safer short path is not full dashboard WebSocket token auth. The implemented trust model blocks auto escalation over WS and adds a local CLI write path instead.
* `symphony_runs.owner_started_at` and `lease_expires_at` already existed in the create-table schema but older stores may not have them, so migration now includes idempotent `ALTER TABLE` statements for those columns.
* A real 10ms timeout test expired during Git worktree preparation, not during the agent run. The routing test now stubs `context.DeadlineExceeded` from `symphonyRunCodexAgent` so it proves timeout classification directly.
* `scenery harness self --summary --write` is currently blocked by managed Postgres local state, not by Symphony: `docker container inspect scenery-postgres --format {{.State.Status}}` fails with `No such container: scenery-postgres`, and an explicit `.scenery/harness/bin/scenery db server start --json` returns the same error.

## Decision Log

* Decision: Reject `mode=auto` over dashboard RPC unless the workflow is already auto.

  Rationale: The current dashboard WebSocket is not authenticated. Blocking only the escalation preserves markdown/concurrency edits and auto-to-manual de-escalation while closing the process-start capability path.

  Date: 2026-07-03

  Author: Codex

* Decision: Add `scenery symphony auto --on|--off [--app-root <path>]` as the local trust path instead of implementing a per-run dashboard capability token in this slice.

  Rationale: A correct token protocol would touch HTML bootstrapping, proxy behavior, and the Vite client. The CLI path is smaller, local, explicit, uses existing app-root discovery, and writes through `symphony.Store.UpdateWorkflow`.

  Date: 2026-07-03

  Author: Codex

* Decision: Use leases and heartbeats, not PID checks, for run recovery.

  Rationale: ExecPlan 0092 warned that PIDs are insufficient. A database lease handles dashboard restarts, dead goroutines, and stale rows with one recovery mechanism.

  Date: 2026-07-03

  Author: Codex

* Decision: Keep Symphony sessions single-turn for this slice.

  Rationale: `agent.max_turns` is parsed and carried on the run request but is not repurposed as attempts. A real multi-turn continuation loop needs app-server protocol semantics beyond this hardening pass.

  Date: 2026-07-03

  Author: Codex

## Outcomes & Retrospective

Implemented the Symphony hardening slice: dashboard RPC auto-mode escalation is blocked, local CLI auto opt-in exists, run leases and stale recovery are implemented, timeout/stall statuses are terminal and routed, `max_attempts` is distinct from `max_turns`, client races are fixed, latest-run selection is attempt-based, and workspace reset/cleanup is covered by tests.

Validation passed for focused race tests, full Go tests, and consolenext lint/typecheck/build. `scenery harness self --summary --write` remains blocked by the existing missing `scenery-postgres` managed container state described in Surprises & Discoveries.

## Context and Orientation

Primary implementation files:

* `internal/symphony/store.go` owns SQLite persistence, migrations, task/run queries, leases, and run events.
* `cmd/scenery/dashboard_symphony.go` owns dashboard RPC dispatch and the unauthenticated run-RPC gate.
* `cmd/scenery/dashboard_symphony_runner.go` owns the server-side auto runner, worktree preparation, Codex app-server client, workflow front matter parsing, and workspace cleanup.
* `cmd/scenery/dashboard.go` owns dashboard WebSocket upgrade behavior.
* `cmd/scenery/symphony_cli.go` owns the local CLI trust path for enabling/disabling auto mode.
* `apps/consolenext/src/scenery.ts` owns the TypeScript shape consumed by the Symphony page.

Relevant docs:

* `docs/local-contract.md` records CLI grammar, dashboard RPC behavior, run statuses, and WORKFLOW.md keys.
* `docs/agent-guide.md` records agent workflow behavior.
* `apps/consolenext/AGENTS.md` records the frontend's supported RPC surface.
* `docs/plans/0092-symphony-dashboard.md` is the completed parent plan. This plan implements 0092's lease/recovery follow-up rules.

## Milestones

Milestone 1 closes auto-mode escalation from the dashboard WebSocket and adds the local CLI opt-in.

Milestone 2 adds run leases, heartbeats, stale-run marking, timeout/stall routing, and recovery tests.

Milestone 3 separates `max_attempts` from `max_turns` and documents current single-turn behavior.

Milestone 4 fixes client synchronization and latest-run selection.

Milestone 5 resets/reclaims Symphony workspaces and updates docs, frontend types, and validation evidence.

## Plan of Work

Start at the shared store and runner choke points. Write leases in `StartRunWithRepo`, renew them from the owning runner goroutine, and mark expired active rows before active-run counting. Keep terminal statuses out of `activeRunStatuses`.

Gate only the dangerous auto escalation in `dispatchSymphonyRPC`. Let the CLI enable auto mode through app-root discovery and the same store update path.

Keep `max_turns` separate. Parse `max_attempts` for retry filtering and parse timeout keys as durations.

For races, install the app-server notification handler before `cmd.Start`, call it before closing `turnDone`, and protect shared result/summary state.

For workspace hygiene, reset existing worktrees during preparation and remove terminal task worktrees using only paths under the Symphony cache root.

## Concrete Steps

1. Update `internal/symphony.Store` run schema, selectors, migration, lease methods, expired-run marking, event recording, terminal workspace listing, and latest-run query.
2. Update `cmd/scenery/dashboard_symphony_runner.go` to heartbeat active runs, recover stale rows on each tick, parse the new workflow keys, classify timeout/stall statuses, reset workspaces, and clean terminal workspaces.
3. Update dashboard RPC and WebSocket origin checks.
4. Add `scenery symphony auto --on|--off [--app-root <path>]`.
5. Update tests in `internal/symphony/store_test.go` and `cmd/scenery/dashboard_symphony_test.go`.
6. Update `docs/local-contract.md`, `docs/agent-guide.md`, `apps/consolenext/AGENTS.md`, `docs/plans/active.md`, and `docs/knowledge.json`.
7. Run the validation commands listed below and record any skips.

## Validation and Acceptance

Required validation:

```sh
go test ./internal/symphony -count=1 -race
go test ./cmd/scenery -run 'TestDashboardSymphony|TestDashboardRPC|TestHarnessBrowser|TestCodexAppServerClient|TestPrepareSymphonyWorkspace|TestCleanupSymphonyRunWorkspace|TestDashboardWebSocketOriginCheck' -count=1 -race
go test ./...
cd apps/consolenext && bun run lint && bun run typecheck && bun run build && cd ../..
./scripts/build-dashboard-ui-embed.sh
scenery harness self --summary --write
```

Acceptance criteria:

* Dashboard RPC cannot set a non-auto workflow to `auto`.
* `scenery symphony auto --on --app-root <path>` enables auto mode and the runner can pick up a task.
* Expired queued/running leases become terminal `stalled` runs and release `todo`/`in_progress` tasks.
* App-server turn timeout maps to `timed_out`; no-notification stall maps to `stalled`; both route tasks to `rework`.
* `max_attempts` limits retry attempts and `max_turns` does not.
* Latest run is selected by max attempt.
* Existing worktrees are reset before retry and terminal task worktrees are removed only under the Symphony cache root.

## Idempotence and Recovery

All migrations use `CREATE TABLE IF NOT EXISTS`, `CREATE INDEX IF NOT EXISTS`, or duplicate-column-tolerant `ALTER TABLE` statements.

If validation fails after a partial run, rerun the focused package test first:

```sh
go test ./internal/symphony -count=1 -race
go test ./cmd/scenery -run 'TestDashboardSymphony|TestCodexAppServerClient|TestPrepareSymphonyWorkspace|TestCleanupSymphonyRunWorkspace' -count=1 -race
```

Workspace cleanup refuses paths outside `<symphonyCacheRoot()>/workspaces`, tolerates already-missing workspaces, and uses `git worktree remove --force` from the recorded repo root when available.

## Artifacts and Notes

The new CLI command intentionally has no environment-variable knob.

The dashboard trust model for this slice is same-origin WebSocket upgrade plus RPC-level rejection of auto escalation. Enabling auto mode is a local CLI action, not a browser RPC action.

ExecPlan 0092's recovery rules for leases and stalled marking are implemented by this plan.

## Interfaces and Dependencies

No new third-party dependencies.

New/changed CLI:

```text
scenery symphony auto --on|--off [--app-root <path>]
```

New WORKFLOW.md agent keys:

```yaml
agent:
  max_attempts: 3
  max_turns: 20
  turn_timeout_ms: 3600000
  stall_timeout_ms: 300000
```

New terminal run statuses:

```text
stalled
timed_out
```
