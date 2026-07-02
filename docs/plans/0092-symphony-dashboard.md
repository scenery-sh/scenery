# 0092 - Symphony Dashboard

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Replace the `Observability` page in `apps/consolenext` with a new `Symphony` page: a Scenery-native work-management and agent-orchestration surface inspired by `openai/symphony`.

The upstream Symphony repository describes an engineering preview where a long-running service watches a tracker, creates isolated workspaces, runs Codex through the Codex app server, and keeps work moving until an issue is completed, blocked, or needs human input. Scenery needs the same product shape, but with different local contracts:

```text
No Linear.
No GraphQL.
No cloud control plane.
Use the Codex app server for agent execution after its authenticated protocol is verified.
Do not expose process-starting runner actions through an unauthenticated local dashboard RPC.
Store Symphony data in SQLite.
Scope stored data by stable Application identity, not by worktree, runtime session, or process.
Render tasks as a Kanban board grouped by task status.
Use the existing consolenext Astryx + StyleX design system.
```

The user-facing contract after this plan is implemented is:

```text
Opening consolenext for app X shows app X's Symphony board.
Opening consolenext for a worktree of app X shows the same Symphony board.
Opening consolenext for app Y shows a different Symphony board.
Tasks persist across runtime restarts.
Tasks can be created, edited, moved between status columns, hidden in terminal columns, and reloaded without data loss.
Codex runs, when enabled after the runner safety gate, are tied to tasks and recorded in the same app-scoped store.
```

This plan intentionally separates the first durable board implementation from the later automated-runner implementation. The board and SQLite model are the foundation. Codex app-server orchestration is added only after the store, app identity, workspace-safety rules, event model, dashboard/auth channel, and UI states are in place.

## Progress

* [x] 2026-07-02 - Read root `AGENTS.md`, `apps/consolenext/AGENTS.md`, `PLANS.md`, current active plans, and the relevant dashboard/local-contract docs before drafting.
* [x] 2026-07-02 - Reviewed the upstream `openai/symphony` repository, `SPEC.md`, and Elixir reference implementation at a planning level.
* [x] 2026-07-02 - Inspected current consolenext page routing and dashboard RPC/storage entry points.
* [x] 2026-07-02 - Drafted this ExecPlan as `docs/plans/0092-symphony-dashboard.md`.
* [x] 2026-07-02 - Linked this ExecPlan from `docs/plans/active.md`.
* [x] 2026-07-02 - Added this ExecPlan to `docs/knowledge.json`.
* [x] 2026-07-02 - Asked `ask-oracle` to review this ExecPlan once using Claude Fable 5 in non-interactive print mode; no subagents were spawned.
* [x] 2026-07-02 - Incorporated oracle findings around dashboard RPC authentication, deterministic SQLite path, multi-process concurrency, harness drift, schema constraints, and runner scope.
* [x] 2026-07-02 - Implemented milestone 1: app-scoped SQLite store, deterministic dashboard cache path, dashboard RPC, board CRUD, base-app sharing, app isolation, and concurrent identifier tests.
* [x] 2026-07-02 - Implemented milestone 2: replaced consolenext `Observability` nav/page with `Symphony`, added board columns, hidden columns, task cards, create/edit modal, task moves, deletes, reload persistence, and no-GraphQL frontend RPC calls.
* [x] 2026-07-02 - Resolved milestone 3 safety gate: workflow config persistence plus run/run-event tables shipped, `codex app-server` protocol/schema surface was checked locally, and process-starting `symphony/run/*` RPCs remain unavailable until an authenticated runner channel exists.
* [x] 2026-07-02 - Implemented milestone 4: added consolenext Symphony browser-harness route markers, updated docs/contracts/knowledge index, rebuilt embedded dashboard assets, and ran focused plus full validation listed in Outcomes.

Update this section at every meaningful stopping point. Every update must include the date, what changed, and whether validation was run.

## Surprises & Discoveries

Initial source-review facts:

* `apps/consolenext/src/dashboard-model.ts` currently includes `Observability` in the page union and nav list. This plan replaces that visible page with `Symphony`.
* `apps/consolenext/src/App.tsx` currently imports and renders `ObservabilityPage`. The implementation should replace that render path with a `SymphonyPage`.
* `apps/consolenext/src/workbench-pages.tsx` currently defines the Observability UI. The Symphony board should probably live in its own source file once it grows beyond a small component.
* `apps/consolenext/src/scenery.ts` already has a WebSocket dashboard RPC client. Symphony board calls may add typed RPC methods there and must not use the stored-request GraphQL helper.
* `cmd/scenery/dashboard_rpc.go` is the current server-side dashboard RPC dispatch point. Its WebSocket path is local but not authenticated today; the plan must not call it an authenticated channel.
* `cmd/scenery/dashboard.go` defines `dashboardUpgrader` with a permissive origin check and `handleWebSocket` does not perform a token check. Process-starting `symphony/run/*` methods must not ship on this channel until origin/token auth is added or the methods move behind an authenticated Codex app-server channel.
* `cmd/scenery/dashboard.go` already has `dashboardStoreAppID(status)`, which prefers `BaseAppID` and falls back to the dashboard app ID for non-session dashboards. That is the right identity boundary for "app X or worktree of app X shows the same data" while still supporting direct non-agent dashboards.
* Symphony handlers must resolve a live app status before choosing storage identity. Falling back directly to the raw request `app_id` can fork storage by session/worktree and violates the user request.
* `internal/devdash/store.go` is JSON-backed and app-keyed, but Symphony needs richer task/run queries and transactional writes. Use a dedicated SQLite store instead of extending the devdash JSON file.
* `internal/sqlitedb` already provides modernc SQLite opening, directory creation, WAL, foreign keys, busy timeout, and metadata table setup. Symphony should reuse that package rather than adding another SQLite dependency.
* `cmd/scenery/dashboard_sqlite.go` exposes application database inspection. It is not the right storage layer for Symphony because it reads app-owned databases; Symphony data is console/control-plane data.
* The existing browser harness journeys exercise the older `ui/` path-based dashboard routes and selectors, not the consolenext `?page=` surface. Implementation must not "fix" those old Observability journeys as if they tested consolenext. Add consolenext-specific Symphony coverage instead.
* `docs/plans/0091-native-observability-and-grafana-removal.md` made the console the Scenery-owned observability workbench. Replacing the visible consolenext `Observability` page should not remove Victoria logs, traces, metrics, or `observability/status` RPCs unless a separate observability plan says to do so.
* `go run ./cmd/scenery inspect docs --json` on 2026-07-02 reported 3 review-due docs: `docs/index.md`, `docs/environment.registry.json`, and `docs/app-development-cookbook.md`. This plan does not garden those unrelated docs, but implementation should avoid making their drift worse.
* `devdashCacheRoot()` can currently return `SCENERY_DEV_CACHE_DIR`, an agent dashboard root if the agent responds quickly, or an empty string that later falls back to the user cache. Symphony needs a deterministic path choice so tasks do not appear to disappear when the local agent ping behavior changes.

Upstream Symphony review facts:

* Upstream Symphony is an engineering-preview project, not a hardened production dependency to vendor directly.
* Its core concepts are useful: workflow config, tracker issue model, bounded orchestrator, workspace manager, Codex app-server runner, event stream, blocked/input-required states, retry/backoff, and a status/dashboard API.
* Its Linear adapter, GraphQL assumptions, and tracker polling model are intentionally not part of Scenery's first implementation.
* The upstream `SPEC.md` is a better source for product semantics than copying the Elixir prototype module-for-module.

Oracle review facts from 2026-07-02:

* The oracle verified the ExecPlan section structure and the `dashboardStoreAppID` identity direction.
* The oracle rejected the original "authenticated dashboard RPC" wording because the current dashboard WebSocket is loopback-only with permissive origin behavior, not authenticated.
* The oracle recommended pinning the SQLite path, defining concurrent worktree/process behavior, making app identity resolution fail closed, correcting browser-harness assumptions, tightening schema constraints, and treating the Codex app-server runner as a protocol spike or follow-up if it grows beyond the board slice.

Implementation facts from 2026-07-02:

* `go test ./internal/symphony -count=1` initially exposed `SQLITE_BUSY` under two store handles creating tasks concurrently. The fix is a per-SQLite-file in-process mutex plus `SetMaxOpenConns(1)` on each store handle; `TestStoreConcurrentCreatesUseUniqueIdentifiers` now proves unique identifiers under concurrent writes.
* `cmd/scenery/dashboard_symphony.go` prefers `BaseAppID` for agent/worktree sessions, falls back to the direct dashboard app ID when no session id exists, and fails closed for session records with no base id. `TestDashboardSymphonyRPCFallsBackToDashboardAppIDWithoutBaseAppID` and `TestDashboardSymphonyRPCFailsClosedForSessionWithoutBaseAppID` cover those paths.
* `.scenery/harness/bin/scenery harness ui --json --write --app-root testdata/apps/basic` exposed that empty stored-request lists encoded as JSON `null`, crashing the API page, and that empty database pages lacked a stable page marker. `internal/devdash.Store.ListStoredRequests` now returns an empty slice, the API page defensively normalizes the list, and the Databases/API empty states keep their semantic root markers.
* Chrome validation against a direct non-agent dashboard exposed that direct `scenery up` can have no `BaseAppID` even when the dashboard app id is stable. The resolver now allows fallback to `AppID` only when `SessionID` is empty, while session records without `BaseAppID` still fail closed.
* `codex app-server --help` reports an experimental app-server with `--listen` transports and `--ws-auth` modes `capability-token` and `signed-bearer-token`. `codex app-server generate-json-schema --experimental --out /tmp/scenery-codex-schema.GAO6IP` generated v1/v2 schema files including `codex_app_server_protocol.schemas.json` and `codex_app_server_protocol.v2.schemas.json`. `codex app-server daemon version` failed because `/Users/petrbrazdil/.codex/app-server-control/app-server-control.sock` did not exist, so no running daemon was available to integrate against.
* The implementation does not ship process-starting runner RPCs. `symphony/run/start` returns `symphony/run/start is unavailable until dashboard runner auth is implemented`, covered by `TestDashboardSymphonyRPCMovesAndRejectsRunnerMethods`.
* `rg -n "postGraphQL|__graphql|GraphQL" apps/consolenext/src/symphony*.tsx apps/consolenext/src/symphony*.ts` exits with no matches; the Symphony frontend uses typed dashboard RPC methods only.

Add new discoveries here with the command, test, or file that exposed them. Use this section to record unexpected compile failures, identity mismatches, SQLite migration issues, Codex app-server protocol details, UI assumptions, stale docs, or harness contract issues.

Suggested implementation inventory commands:

```sh
rg -n "Observability|observability/status|Page =" apps/consolenext cmd/scenery
rg -n "postGraphQL|__graphql|GraphQL" apps/consolenext/src cmd/scenery
rg -n "dashboardStoreAppID|BaseAppID|StoredRequests|devdash" cmd/scenery internal
rg -n "codex app-server|app-server|thread" cmd internal docs apps/consolenext
go run ./cmd/scenery inspect docs --json
```

## Decision Log

* Decision: Replace the visible `Observability` nav item with `Symphony` in consolenext.

  Rationale: The user explicitly requested a page called Symphony. The existing observability data plane can remain available through logs, traces, metrics, overview widgets, and RPC without occupying this top-level page slot.

  Date: 2026-07-02

  Author: initial ExecPlan

* Decision: Scope Symphony persistence by application identity using `dashboardStoreAppID(status)`.

  Rationale: The user specifically wants an app and any worktree of that app to share the same board, while different applications show different data. `BaseAppID` is already the Scenery concept for this boundary, and the direct-dashboard app ID is the available stable identity when no session id exists.

  Date: 2026-07-02

  Author: initial ExecPlan

* Decision: Store Symphony data in a Scenery-owned SQLite database at a deterministic dashboard/control-plane path, with every domain table including `app_id`.

  Rationale: Symphony data is console/control-plane state, not app business data. A single dashboard SQLite file with app-scoped rows gives transactional writes, queryable board ordering, run history, and easy backup/migration without coupling to app service databases. The path must not depend on whether the local agent happens to respond to a ping.

  Date: 2026-07-02

  Author: initial ExecPlan

* Decision: Resolve the Symphony SQLite file to `filepath.Join(symphonyCacheRoot(), "symphony.sqlite")`, where `symphonyCacheRoot()` uses existing `SCENERY_DEV_CACHE_DIR` when set, otherwise uses `filepath.Join(localagent.DefaultPaths().AgentDir, "dashboard")` when available, otherwise uses `filepath.Join(os.UserCacheDir(), "scenery")`.

  Rationale: This preserves existing test isolation through `SCENERY_DEV_CACHE_DIR`, avoids adding a new environment knob, and removes ping-dependent path changes that could split app data between two cache roots.

  Date: 2026-07-02

  Author: oracle-incorporated ExecPlan

* Decision: Do not use GraphQL for Symphony.

  Rationale: The user explicitly rejected GraphQL. Symphony should use typed Scenery/Codex app-server or dashboard RPC methods, and the board implementation must not call the existing stored-request GraphQL helper.

  Date: 2026-07-02

  Author: initial ExecPlan

* Decision: Do not describe the current dashboard WebSocket as authenticated, and do not ship `symphony/run/start`, `symphony/run/stop`, or workspace-mutating runner calls until an authenticated channel exists for them.

  Rationale: The current dashboard WebSocket is local but has permissive origin behavior. Starting Codex processes and writing workspaces is a materially larger capability than rendering dashboard state or saving local board rows.

  Date: 2026-07-02

  Author: oracle-incorporated ExecPlan

* Decision: Implement durable task-board CRUD before automated Codex app-server orchestration.

  Rationale: The board establishes the identity, persistence, API, and UI contracts. Automated agents add workspace, cancellation, approval, and recovery risks that should sit on top of a tested task/run model.

  Date: 2026-07-02

  Author: initial ExecPlan

* Decision: Treat upstream Symphony as a product/spec reference, not as code to port wholesale.

  Rationale: Scenery's storage, authentication, app identity, frontend stack, and runtime are different. Copying the Elixir/Linear shape directly would import the wrong tracker and protocol assumptions.

  Date: 2026-07-02

  Author: initial ExecPlan

* Decision: Treat milestones 1 and 2 plus their validation as a shippable first slice; milestone 3 may become a separate follow-up ExecPlan after a documented Codex app-server protocol spike.

  Rationale: There is no existing Scenery Codex app-server runner. Process spawning, approval/cancellation, workspace management, and protocol-version handling are greenfield enough to deserve a clear gate instead of being hidden inside the board implementation.

  Date: 2026-07-02

  Author: oracle-incorporated ExecPlan

* Decision: Complete this plan without process-starting runner RPCs.

  Rationale: The board, workflow config, run/event schema, and Codex app-server schema spike are in place, but the dashboard WebSocket is still not an authenticated runner channel and no local app-server daemon was running. Shipping a Start/Stop runner through the current dashboard RPC would violate this plan's safety gate.

  Date: 2026-07-02

  Author: implementation

## Outcomes & Retrospective

Completed on 2026-07-02.

Shipped:

* Added `internal/symphony` with Scenery-owned SQLite tables for statuses, tasks, labels, per-app counters, runs, run events, and workflow config at `<dashboard-cache-root>/symphony.sqlite`.
* Added dashboard RPC methods for `symphony/state`, `symphony/task/create`, `symphony/task/update`, `symphony/task/move`, `symphony/task/delete`, `symphony/statuses/update`, `symphony/workflow/get`, and `symphony/workflow/update`.
* Replaced the consolenext `Observability` page/nav item with `Symphony`, including board columns, task cards, hidden columns, create/edit modal, explicit refresh, status moves, and task deletion.
* Added consolenext-specific browser-harness route coverage for `?page=Symphony`.
* Updated `apps/consolenext/AGENTS.md`, `docs/local-contract.md`, `docs/agent-guide.md`, and `docs/knowledge.json`.

Not shipped:

* `symphony/run/*` process-starting RPCs. They remain blocked until Scenery has an authenticated runner channel or a verified authenticated Codex app-server integration path.

Validation:

* `go test ./internal/symphony -count=1` passed.
* `go test ./cmd/scenery -run 'TestDashboardSymphony|TestBuildHarnessUIRoutes|TestHarnessUICommand|TestHarnessBrowser' -count=1` passed.
* `go test ./cmd/scenery -run 'TestDashboardSymphony|TestDashboardRPC|TestHarnessBrowser' -count=1` passed.
* `go test ./cmd/scenery` passed.
* `go test ./...` passed.
* `cd apps/consolenext && bun run lint` passed.
* `cd apps/consolenext && bun run typecheck` passed.
* `cd apps/consolenext && bun run build` passed.
* `rg -n "postGraphQL|__graphql|GraphQL" apps/consolenext/src/symphony-page.tsx` produced no matches.
* `./scripts/build-dashboard-ui-embed.sh` passed and refreshed `cmd/scenery/dashboard_static/dist`.
* `.scenery/harness/bin/scenery harness ui --json --write --app-root testdata/apps/basic` passed and wrote `testdata/apps/basic/.scenery/harness/ui/latest.json`.
* Chrome validation against a temporary copy of `testdata/apps/standard-auth` with a stable fixture id confirmed `Symphony`, `Backlog`, `Todo`, `In Progress`, `Human Review`, and `Hidden columns`; created `SYM-1`, edited it, moved it to In Progress, reloaded, saw it persist, and observed zero browser console errors.

## Context and Orientation

Primary files and areas:

```text
apps/consolenext/AGENTS.md
apps/consolenext/src/App.tsx
apps/consolenext/src/dashboard-model.ts
apps/consolenext/src/scenery.ts
apps/consolenext/src/workbench-pages.tsx
apps/consolenext/src/styles.css
apps/consolenext/src/xstyle.ts
cmd/scenery/dashboard.go
cmd/scenery/dashboard_rpc.go
cmd/scenery/dashboard_sqlite.go
cmd/scenery/harness_browser_test.go
internal/sqlitedb
docs/local-contract.md
docs/agent-guide.md
docs/knowledge.json
docs/plans/active.md
```

Local consolenext rules:

* Use Astryx primitives and StyleX/xstyle tokens.
* Do not introduce a second design system.
* Keep validation under `apps/consolenext`: `bun run lint`, `bun run typecheck`, and `bun run build`.
* Rebuild the embedded dashboard assets after UI changes.

The requested product inspiration comes from:

```text
https://github.com/openai/symphony
https://github.com/openai/symphony/blob/main/SPEC.md
https://github.com/openai/symphony/tree/main/elixir
```

Important difference from upstream:

```text
Upstream issue source: Linear tracker adapter.
Scenery issue source: Scenery-owned SQLite task store.

Upstream tracker API: Linear GraphQL.
Scenery board API: typed local RPC methods, with no GraphQL. The current dashboard WebSocket is not authenticated, so runner-mutating methods need auth hardening or an authenticated Codex app-server channel before shipping.

Upstream persistence: tracker/workspace-driven recovery in the preview.
Scenery persistence: explicit SQLite rows per stable application identity.
```

## Milestones

Milestone 1: App-scoped Symphony storage and RPC.

* Add a small Go store for Symphony data using `internal/sqlitedb`.
* Add deterministic `symphonyCacheRoot()` resolution and store data at `symphony.sqlite` under that root.
* Create/migrate tables for statuses, tasks, task labels, runs, run events, workflow config, and store metadata.
* Key all mutable domain rows by stable `app_id`.
* Use transactional per-app counters for task identifiers; do not allocate identifiers with a racy `SELECT max(...)`.
* Define move semantics as sibling renumbering inside one transaction, unless implementation proves a gapped ordering scheme is simpler and equally deterministic.
* Add typed local RPC methods for state, task create/update/move/delete, status visibility/order, and workflow read/write.
* Make RPC app identity resolution fail closed when `dashboardStatusFor` cannot produce a stable app status/base app id.
* Decide whether `symphony/*` is supported through the agent-dashboard controller; if not, return a typed unsupported-controller error.
* Cover store migration, app isolation, base-app sharing, board ordering, concurrent writes, and RPC validation with Go tests.

Milestone 2: Symphony board UI.

* Replace the `Observability` page label and route with `Symphony`.
* Add a responsive Kanban board with visible columns for active statuses and a right-side hidden/terminal-column list.
* Add task cards with identifier, title, status indicator, labels, updated time, and active run/blocking state.
* Add create/edit modal matching the attached screenshots' interaction shape without copying Linear-specific product assumptions.
* Persist task edits and status moves through RPC, reload state after writes, and show loading/error/empty states.
* Verify no horizontal or vertical scrollbar artifacts regress from the recent consolenext scrollbar fixes.

Milestone 3: Workflow and Codex app-server orchestration.

* Before coding the runner, record a protocol spike in `Surprises & Discoveries` with the exact installed `codex app-server` command/schema output and the official docs consulted.
* Before exposing run-mutating methods, either harden the dashboard WebSocket with origin/token auth or move those methods behind an authenticated Codex app-server channel.
* Add workflow config storage and parsing sufficient to describe polling/manual run mode, concurrency, workspace root, prompts, and terminal statuses.
* Add path-safety helpers for task workspace creation under a Scenery-controlled root.
* Add run records and event records before launching any process.
* Integrate with the current Codex app-server protocol only after verifying the installed CLI/schema and official OpenAI/Codex documentation.
* Support explicit start, stop/cancel, retry, blocked, input-required, succeeded, failed, timed-out, and stalled states.
* Keep automated polling disabled until the workflow, safety, and recovery rules are documented and tested.

Milestone 4: Harness, docs, and acceptance validation.

* Add consolenext-specific browser coverage for `?page=Symphony` using stable `data-scenery-ui` markers.
* Leave old `ui/` path-based Observability harness journeys alone unless a separate cleanup intentionally removes that surface.
* Update `apps/consolenext/AGENTS.md` to list Symphony RPC/data surfaces and codify that Symphony must not use `/__graphql`.
* Update local contract and agent guide only for new public or semi-public RPC/storage/agent workflow contracts.
* Add schema documentation if any new JSON response shape is intended as stable automation surface.
* Run Go, frontend, embed, and browser validation; commit regenerated `cmd/scenery/dashboard_static/dist` assets with the UI change.
* Record exact validation results in this plan.

## Plan of Work

1. Re-run the implementation inventory commands from `Surprises & Discoveries`.
2. Decide package placement for the store. Prefer `internal/symphony` if the model is shared beyond dashboard RPC; otherwise keep a narrow `cmd/scenery/dashboard_symphony.go` facade over an internal store package.
3. Implement deterministic Symphony cache-root resolution and SQLite migrations first, with a schema version and idempotent `CREATE TABLE IF NOT EXISTS` / migration path.
4. Add a Go test fixture that creates two runtime statuses with the same `BaseAppID` and one with a different `BaseAppID`; prove shared and isolated board reads.
5. Add server-side RPC request/response structs. Validate title/status/app identity inputs server-side, and reject requests when the stable app identity cannot be resolved.
6. Add the frontend RPC client methods and TypeScript types.
7. Replace page routing/nav from `Observability` to `Symphony`.
8. Build the board UI in a dedicated component file once the component exceeds a small inline surface.
9. Add modal CRUD flows, keyboard/mouse-safe interactions, and a defined refresh model for multi-process writes.
10. Add run/workflow UI placeholders only when backed by persisted data; do not ship decorative controls that imply an agent run works before it does.
11. Run the Codex app-server protocol spike. If the runner is still larger than a narrow safe slice, split milestone 3 into a new ExecPlan before coding it.
12. Add the runner in a separate patch/commit slice after board CRUD is verified and the auth/protocol gate is satisfied.
13. Update docs/contracts/harness, run validation, and record results.

## Concrete Steps

Initial planning commands already run:

```sh
go run ./cmd/scenery inspect docs --json
sed -n '1,220p' PLANS.md
sed -n '1,220p' apps/consolenext/AGENTS.md
rg -n "Observability|Page =" apps/consolenext/src
rg -n "dashboardStoreAppID|BaseAppID" cmd/scenery internal
git clone --depth 1 https://github.com/openai/symphony /tmp/openai-symphony
find /tmp/openai-symphony -maxdepth 3 -type f | sort
```

Implementation skeleton:

```text
internal/symphony/store.go
internal/symphony/store_test.go
cmd/scenery/dashboard_symphony.go
cmd/scenery/dashboard_symphony_test.go
apps/consolenext/src/symphony-page.tsx
apps/consolenext/src/symphony-types.ts
```

All timestamp columns use RFC 3339 UTC text. `symphony/state` must filter rows with `deleted_at` set. Task identifiers are permanent within an app, even after soft delete; do not reuse deleted identifiers.

Likely SQLite schema, subject to implementation refinement:

```text
symphony_statuses(
  app_id text not null,
  status_key text not null,
  name text not null,
  kind text not null,
  sort_order integer not null,
  hidden integer not null default 0,
  color text not null,
  created_at text not null,
  updated_at text not null,
  primary key(app_id, status_key)
)

symphony_tasks(
  id text primary key,
  app_id text not null,
  identifier text not null,
  title text not null,
  description text not null default '',
  status_key text not null,
  sort_order integer not null,
  priority text not null default '',
  assignee text not null default '',
  estimate text not null default '',
  branch_name text not null default '',
  url text not null default '',
  source text not null default 'manual',
  created_at text not null,
  updated_at text not null,
  deleted_at text,
  unique(app_id, identifier),
  foreign key(app_id, status_key) references symphony_statuses(app_id, status_key)
)

symphony_app_counters(
  app_id text not null,
  counter_key text not null,
  next_value integer not null,
  updated_at text not null,
  primary key(app_id, counter_key)
)

symphony_task_labels(
  app_id text not null,
  task_id text not null,
  label text not null,
  primary key(app_id, task_id, label),
  foreign key(task_id) references symphony_tasks(id)
)

symphony_runs(
  id text primary key,
  app_id text not null,
  task_id text not null,
  attempt integer not null,
  status text not null,
  workspace_path text not null default '',
  thread_id text not null default '',
  turn_id text not null default '',
  process_id integer not null default 0,
  owner_session_id text not null default '',
  owner_started_at text,
  lease_expires_at text,
  summary text not null default '',
  error text not null default '',
  started_at text,
  ended_at text,
  created_at text not null,
  updated_at text not null,
  foreign key(task_id) references symphony_tasks(id)
)

symphony_run_events(
  app_id text not null,
  run_id text not null,
  seq integer not null,
  type text not null,
  payload_json text not null,
  created_at text not null,
  primary key(app_id, run_id, seq),
  foreign key(run_id) references symphony_runs(id)
)

symphony_workflows(
  app_id text primary key,
  workflow_markdown text not null,
  mode text not null default 'manual',
  max_concurrency integer not null default 1,
  updated_at text not null
)
```

Default statuses:

```text
backlog
todo
in_progress
human_review
rework
merging
done
canceled
duplicate
```

Initial visible statuses should match the screenshots:

```text
backlog
todo
in_progress
human_review
```

Terminal or less-common statuses start hidden:

```text
rework
merging
done
canceled
duplicate
```

Initial RPC methods:

```text
symphony/state
symphony/task/create
symphony/task/update
symphony/task/move
symphony/task/delete
symphony/statuses/update
symphony/workflow/get
symphony/workflow/update
```

Later runner RPC methods:

```text
symphony/run/start
symphony/run/stop
symphony/run/retry
symphony/run/events
```

## Validation and Acceptance

Minimum acceptance for the first implementation slice:

* The consolenext nav shows `Symphony` and no longer shows `Observability`.
* Opening `?page=Symphony` renders the Kanban board.
* Opening the old `?page=Observability` does not expose an Observability page; with the current `parsePage` behavior, it falls back to `Overview`.
* Creating a task writes a SQLite row scoped to the stable app id.
* Moving a task between statuses updates persisted order and status.
* Reloading the page preserves tasks and column placement.
* A simulated app worktree with the same `BaseAppID` sees the same tasks.
* A different app id does not see those tasks.
* Two store handles can create and move tasks for the same app without duplicate identifiers or corrupted ordering.
* The frontend has a clear refresh model for writes made by another dashboard process: explicit Refresh is acceptable for the first slice, and focus/poll refresh can be added if cheap.
* No Symphony frontend code calls GraphQL helpers.
* No `symphony/run/*` process-starting method ships until the auth/protocol gate is satisfied.
* Browser validation confirms the board, modal, hidden columns, and scrollbars render correctly on desktop and a narrow viewport.

Suggested validation commands:

```sh
go test ./internal/symphony -count=1
go test ./cmd/scenery -run 'TestDashboardSymphony|TestDashboardRPC|TestHarnessBrowser' -count=1
go test ./...
cd apps/consolenext && bun run lint && bun run typecheck && bun run build
if rg -n "postGraphQL|__graphql|GraphQL" apps/consolenext/src/symphony*.tsx apps/consolenext/src/symphony*.ts; then exit 1; fi
./scripts/build-dashboard-ui-embed.sh
scenery harness ui --json --write
```

If the full `go test ./...` or harness run is too expensive or fails for unrelated dirty-worktree reasons, record the exact command, failure, and the narrower replacement validation that was actually run.

Browser acceptance:

```text
Route: /consolenext/?app=<active-app>&page=Symphony
Expected visible text: Symphony, Backlog, Todo, In Progress, Human Review, Hidden columns
Expected interaction: add task, edit title/body, move to In Progress, reload, task remains
Expected isolation: switch to another app id, created task is absent
Expected scrollbar state: no stray nav scrollbar, board scroll is intentional and visually quiet
```

## Idempotence and Recovery

SQLite migrations must be safe to run repeatedly. If a migration fails, the dashboard should return a typed RPC error and leave existing rows intact.

Task creation should be idempotent only when an explicit identifier is reused for the same app. Otherwise the store may allocate a new identifier for each user action. The implementation should avoid duplicate writes caused by double-clicking Create by disabling the button while the RPC is in flight.

Status seed creation should be idempotent per app. If the user customizes status visibility/order, later default seeding must not overwrite those choices.

Identifier allocation must be transactional per app. Use an app counter row or equivalent `UPDATE ... RETURNING` transaction, not a read-max-then-insert sequence.

Task moves must update sibling order within the same transaction as the moved task status/order change.

Runner recovery rules:

* Persist the run row before starting any Codex process.
* Persist a startup event before launching the Codex app server.
* Claim a run with an owner session id and lease/heartbeat before launching a process so two worktrees cannot start the same task at the same time.
* On dashboard restart, inspect persisted active runs and mark them `stalled` unless a matching live process/thread can be verified by more than PID alone. Pair PID checks with owner session/start-time or thread evidence because PIDs can be reused.
* Cancellation should update the run row and attempt process cleanup, but failure to kill an already-exited process should not corrupt task data.
* Workspace paths must be sanitized, absolute, and inside the configured Symphony workspace root.

Data recovery:

* The SQLite file should live at `filepath.Join(symphonyCacheRoot(), "symphony.sqlite")`, not in `.scenery/gen`, not in app source, and not in app runtime databases.
* Do not commit the SQLite file.
* Expose enough RPC/CLI inspection later to diagnose task rows and run events without opening the database by hand.

## Artifacts and Notes

Upstream artifacts reviewed during planning:

```text
https://github.com/openai/symphony
https://github.com/openai/symphony/blob/main/SPEC.md
https://github.com/openai/symphony/blob/main/elixir/README.md
/tmp/openai-symphony
```

User-provided transient visual references:

```text
/Users/petrbrazdil/Desktop/Screenshot 2026-07-02 at 11.03.52.png
/Users/petrbrazdil/Desktop/Screenshot 2026-07-02 at 11.04.31.png
/Users/petrbrazdil/Desktop/Screenshot 2026-07-02 at 11.04.49.png
```

These desktop screenshot paths are not durable plan dependencies. The design notes below are the self-contained requirements to preserve in the working tree.

Design notes from screenshots:

* The board should be quiet, dense, and operational rather than a marketing surface.
* Columns have stable widths, a compact header, status icon, count, overflow action, and add action.
* Cards show task identifier, title, status/label hints, and relative update date.
* Terminal or hidden statuses belong in a right-side list with counts.
* The create/edit modal should support title, description, status, priority, assignee, estimate, labels, draft/create actions, and a create-more toggle once those fields exist in the store.

Do not copy Linear branding, GraphQL behavior, or tracker terminology into user-facing Scenery contracts unless the user explicitly asks for it later.

## Interfaces and Dependencies

Go dependencies:

* Reuse `internal/sqlitedb` and the existing modernc SQLite dependency.
* Do not add a second SQLite driver.
* Do not add a frontend state-management dependency for the first board.

Frontend dependencies:

* Use current React, Vite, Astryx, StyleX/xstyle, and existing icon patterns.
* Use Astryx components where they exist for buttons, inputs, dialogs, badges, and select/menu controls.
* Use lucide icons only if already available in the consolenext package; otherwise use existing icon conventions.

Dashboard RPC:

* The current dashboard WebSocket is local but not authenticated. Do not describe it as an authenticated channel.
* Initial board RPC may use the existing local dashboard RPC pattern only for board CRUD/state after documenting the local trust model.
* Process-starting and workspace-mutating runner RPC must wait for WebSocket auth hardening or use an authenticated Codex app-server channel.
* Request handlers derive app identity from the selected app/session status and `dashboardStoreAppID(status)`.
* Request handlers must fail closed when stable app identity cannot be resolved; do not fall back to raw session ids.
* Requests must not accept arbitrary filesystem paths for workspaces.
* Requests must validate status keys against the app's status rows.

Codex app-server:

* The runner must use the current authenticated Codex app-server protocol.
* Before implementing runner calls, verify the current installed Codex app-server commands/schema and official OpenAI/Codex docs.
* Record exact protocol-spike evidence in `Surprises & Discoveries` before adding runner code.
* Persist every meaningful run state transition so the UI and agents can recover after a restart.

Docs/contracts:

* Update `apps/consolenext/AGENTS.md` when adding Symphony RPC/data surfaces and the no-GraphQL rule for this page.
* Update `docs/local-contract.md` if Symphony RPC responses become a stable automation contract.
* Update `docs/agent-guide.md` if agents are expected to use Symphony tasks or run events.
* Update `docs/knowledge.json` and plan indexes whenever this plan status changes.
