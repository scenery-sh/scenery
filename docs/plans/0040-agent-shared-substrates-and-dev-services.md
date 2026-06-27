# Agent Shared Substrates and Dev Services

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series targets one machine-local daemon/router with shared or hidden substrates instead of each worktree publishing its own Grafana, Victoria, legacy async runtime, Postgres, sync, frontend, and proxy ports. Earlier agent-native local-dev work establishes the agent and private app backends; this plan moves the remaining local platform pieces under daemon/session ownership.

After this work, Grafana and Victoria are daemon-owned shared observability substrates, legacy async runtime local dev is shared with session isolation, Postgres can be shared with a per-session database, sync and frontend tools are routed through the daemon, and checked-in app config no longer needs per-worktree port orchestration.

## Progress

* [x] 2026-05-26: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-26: Promote this ExecPlan to active after completing session identity and signal scoping in 0039.
* [x] 2026-05-26: Add a Grafana session variable backed by the `scenery_session_id` metric label so generated dashboards can filter shared observability data by session.
* [x] 2026-05-26: Add an agent shared-substrate registry and use it for Victoria endpoint ownership/reuse across dev sessions.
* [x] 2026-05-26: Move VictoriaMetrics, VictoriaLogs, VictoriaTraces, and Grafana to agent-registered shared processes by default when the local agent is active.
* [x] 2026-05-26: Move legacy async runtime dev server ownership to the agent substrate registry while keeping per-session task queue prefixes for isolation.
* [x] 2026-05-26: Design and implement the `.scenery.json` `dev.services` config surface for Postgres and sync declarations.
* [x] 2026-05-26: Add `scenery db psql` as the current-contract alias for the existing beta Postgres shell helper.
* [x] 2026-05-26: Route configured frontend upstreams through the agent router and expose stable `<frontend>.<session>.scenery.localhost` URLs.
* [x] 2026-05-26: Split scenery-managed Postgres/sync lifecycle plus `db reset`/snapshot commands into ExecPlan 0041.
* [x] 2026-05-27: Register shared Grafana and legacy async runtime UI upstreams as per-session agent routes so live sessions expose `grafana` and `legacy-async-runtime` URLs in their manifests.
* [x] 2026-05-27: Start supported Vite/Astro frontends on hidden loopback ports for agent sessions instead of depending on fixed checked-in upstream ports.

## Surprises & Discoveries

Record implementation findings here with commands, test output, or file references.

* 2026-05-26: The agent package intentionally does not import `cmd/scenery`, so the first shared-substrate step keeps process startup in the existing Victoria helpers and transfers ownership through an agent registry record. This avoids a large package move while giving later work a real control-plane API for daemon-owned substrates.
* 2026-05-26: Existing Victoria startup already reuses occupied default ports as external components, but the first owning dev session previously killed its started components on shutdown. Registered shared Victoria components are now marked external from the dev supervisor's point of view after agent registration, so shutdown no longer tears down the shared substrate.
* 2026-05-26: Grafana has the same ownership issue as Victoria plus a `root_url` wrinkle. Shared agent Grafana now uses the direct loopback URL for provisioning by default and lets the per-session proxy update dashboard links after proxy startup, avoiding a first-session-specific `root_url` in the shared config.
* 2026-05-26: legacy async runtime local dev already supported external-server reuse, so the shared-agent change is mostly lifecycle ownership: store the local SQLite state under the agent directory, register the legacy async runtime address/UI URL/PID, and let app child env continue to isolate workers with `SCENERY_LEGACY_ASYNC_RUNTIME_TASK_QUEUE_PREFIX`.
* 2026-05-26: The repo already had `scenery psql`; agent-native local-dev uses `scenery db psql`. The new command keeps the old helper as a beta compatibility alias while making the current managed-DB spelling available.
* 2026-05-26: Frontend routing does not start frontend dev servers. It discovers configured/reachable upstreams through the existing localproxy frontend resolver, registers them as agent session backends, and lets the agent router serve stable session hostnames.
* 2026-05-27: Shared Grafana and legacy async runtime were daemon-owned but still surfaced primarily through direct local URLs in session state. Registering them as session backends gives every app session routed `grafana.<session>.scenery.localhost` and `legacy-async-runtime.<session>.scenery.localhost` URLs without changing the shared process model.
* 2026-05-27: The first frontend-ownership slice keeps the generic localproxy resolver as a fallback but starts package-local Vite/Astro dev servers directly when an agent session is active. This gives ONLV hidden `pulse` and `blog` loopback ports, and the checked-in ONLV config no longer needs a fixed blog upstream fallback.

## Decision Log

* Decision: Treat Postgres/sync as a separate milestone from observability/legacy async runtime.
  Rationale: Observability and legacy async runtime already have scenery dev supervisors; Postgres/sync require a local service declaration and database lifecycle contract.
  Date/Author: 2026-05-26 / Codex

* Decision: Introduce an agent substrate registry before moving process launch code fully into the daemon.
  Rationale: It creates the control-plane contract needed by Victoria, Grafana, legacy async runtime, Postgres, sync, and frontend routing while keeping this slice testable and avoiding a premature package split of the large dev supervisor.
  Date/Author: 2026-05-26 / Codex

* Decision: Let the first dev supervisor start shared observability processes, then transfer lifecycle ownership to the agent registry.
  Rationale: This delivers the user-facing daemon-owned behavior quickly: shared agent state roots, reusable endpoints across sessions, and agent shutdown cleanup, without moving the large Victoria/Grafana startup implementation across package boundaries in the same slice.
  Date/Author: 2026-05-26 / Codex

## Outcomes & Retrospective

Completed 2026-05-26.

This plan delivered the agent substrate control-plane contract and used it for shared Victoria, Grafana, and legacy async runtime dev processes. It also added session-aware Grafana dashboards, registered configured frontend upstreams plus shared Grafana and legacy async runtime UI upstreams with the agent router, started supported frontends on hidden agent-owned loopback ports, introduced the beta `dev.services` config surface for Postgres/sync declarations, and added the current-contract `scenery db psql` command alias.

Full scenery-managed Postgres cluster lifecycle, per-session database reset/snapshot behavior, and sync process ownership are intentionally split to [0041 Agent Managed Postgres and sync](0041-agent-managed-postgres-and-sync.md) because that work needs its own database lifecycle contract and validation strategy.

## Context and Orientation

Relevant files:

```text
docs/plans/0037-scenery-agent-mvp.md
cmd/scenery/dev_supervisor.go
cmd/scenery/victoria.go
cmd/scenery/grafana.go
cmd/scenery/legacy-async-runtime_dev.go
cmd/scenery/psql.go
internal/agent/*
internal/localproxy/*
internal/app/root.go
docs/schemas/scenery.config.v1.schema.json
```

## Milestones

Milestone 1 moves observability startup and health state behind the agent while keeping the existing component implementations.

Milestone 2 moves legacy async runtime local dev startup behind the agent and adds session-aware task queue/namespace handling.

Milestone 3 adds app config for scenery-owned dev services and implements shared Postgres cluster/per-session database lifecycle.

Milestone 4 adds sync and frontend daemon routing with stable session URLs.

Milestone 5 updates docs, schemas, harness checks, and ONLV-facing runbooks.

## Plan of Work

Prefer reusing current component startup code by moving ownership boundaries first. Avoid introducing Docker or external service dependencies into tests; use small fakes for lifecycle and routing tests, then gate real integrations behind practical local checks.

## Concrete Steps

1. Extract Victoria/Grafana lifecycle management from the per-session dev supervisor into daemon-owned substrate managers.
2. Register shared observability routes and session-labeled datasource/dashboard URLs in agent session manifests.
3. Extract legacy async runtime local dev startup into an agent substrate with session namespaces or strict task queue prefixes.
4. Add app config parsing and schema support for scenery-owned dev services.
5. Implement shared Postgres cluster/per-session database lifecycle and wire `scenery db` commands to session identity.
6. Implement sync as a hidden per-session backend registered with the daemon router.
7. Register frontend backends with daemon host routing and remove checked-in per-worktree port assumptions from effective dev config.
8. Add fake-backed unit tests for substrate ownership and practical integration checks where local dependencies are available.

## Validation and Acceptance

Expected validation:

```sh
go test ./cmd/scenery ./internal/agent ./internal/localproxy ./internal/app
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Observable behavior:

* Multiple worktrees can run `scenery dev` without competing for Grafana, Victoria, legacy async runtime, app, dashboard, proxy, frontend, Postgres, or sync public ports.
* Grafana dashboards include a session variable and can show one session or compare sessions.
* legacy async runtime workers from one worktree cannot consume another worktree's tasks.
* Postgres state is isolated per session by default.
* sync, frontend, Grafana, and legacy async runtime UI URLs are stable daemon-routed session URLs.

## Idempotence and Recovery

The daemon should own cleanup of substrates it starts and should never kill unrelated processes based only on a port match.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/scenery/dev_supervisor.go
cmd/scenery/victoria.go
cmd/scenery/grafana.go
cmd/scenery/legacy-async-runtime_dev.go
cmd/scenery/psql.go
internal/agent/*
internal/app/root.go
docs/schemas/scenery.config.v1.schema.json
docs/plans/0040-agent-shared-substrates-and-dev-services.md
```

## Interfaces and Dependencies

Potential new config surface:

```json
{
  "dev": {
    "services": {
      "postgres": {
        "kind": "postgres",
        "version": "18",
        "isolation": "database"
      }
    }
  }
}
```

Any external dependency or long-running service requirement must have a clear operational payoff and be represented in harness/docs.
