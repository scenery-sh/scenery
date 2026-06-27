# ONLV Agent Native Dev Migration

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series is mostly implemented on the scenery runtime side, and ONLV has been moved to the agent-native local-dev path. The target state is that `scenery dev` owns the session, routes frontends through the agent, manages Postgres/sync through `dev.services`, and exposes stable HTTPS routed URLs without humans picking ports.

After this work, a developer in `/Users/petrbrazdil/Repos/onlv` should run `just dev` or `scenery dev` and get an agent-backed session. `just down`, `just urls`, and `just psql` should delegate to scenery. Compose may remain only as a manual fallback or be removed when no longer referenced by active docs.

## Progress

* [x] 2026-05-27: Audit agent-native local-dev against current scenery and ONLV state.
* [x] 2026-05-27: Remove stale browser-token language from agent-native local-dev and the global dashboard plan after the browser token feature was intentionally removed.
* [x] 2026-05-27: Migrate ONLV Justfile defaults away from hardcoded local paths and Overmind-first dev.
* [x] 2026-05-27: Switch ONLV DB export/import recipes to scenery-managed snapshots instead of Compose.
* [x] 2026-05-27: Add ONLV `.scenery.json` `dev.services` declarations for managed Postgres and sync.
* [x] 2026-05-27: Update ONLV agent docs so sync/debug instructions use agent-routed sync and scenery DB commands.
* [x] 2026-05-27: Validate the ONLV agent-native flow and refresh harness snapshots.
* [x] 2026-05-27: Revalidate ONLV after shared Grafana and legacy async runtime UI were added to the agent-routed session manifest.
* [x] 2026-05-27: Revalidate ONLV after scenery started `pulse` and `blog` on hidden agent-owned frontend ports, then remove the fixed blog upstream from `.scenery.json`.
* [x] 2026-05-27: Revalidate ONLV after HTTPS became the default agent router mode.
* [x] 2026-05-27: Remove fixed host port publishing from ONLV's fallback `compose.dev.yml`.
* [x] 2026-05-27: Validate a second ONLV worktree running concurrently through the agent, including frontend/API/sync routed URLs and a separate managed Postgres database.
* [x] 2026-05-27: Fix session-addressed logs/inspect/dashboard reads so stale temp-worktree records cannot shadow the current ONLV session.
* [x] 2026-05-27: Extend development trace/log/metric identity beyond `session_id` with app-root hash, branch, and worktree context.
* [x] 2026-05-27: Scope explicit legacy async runtime workflow/activity task queues in active dev sessions so the shared legacy async runtime substrate cannot mix workers across parallel worktrees.
* [x] 2026-05-27: Revalidate parallel ONLV sessions after explicit legacy async runtime queue scoping, proving shared substrate reuse with isolated databases and task queues.

## Surprises & Discoveries

* 2026-05-27: ONLV's checked-in `Justfile` still hardcodes `app_root := "/Users/petrbrazdil/Repos/onlv"` and `scenery := "go -C ../pulse run ./cmd/scenery"`, and `just dev` still starts `OVERMIND_ENV=.env overmind start -f Procfile.dev`.
* 2026-05-27: ONLV has an untracked local `.env` with explicit `DatabaseURL`, `PublicAppURL`, `APIBaseURL`, and `AuthCookieDomain`. This exposed a agent-native local-dev mismatch: declared managed Postgres must override stale local DB URLs by default, otherwise ONLV can look agent-native while still using the old database.
* 2026-05-27: Once ONLV actually used the managed per-session DB, startup failed because the fresh database had no Atlas schema. The agent-native path needs a pre-app setup hook that runs with the managed DB env.
* 2026-05-27: ONLV's local `pg_dump` was version 14 while managed Postgres was version 18, so the setup backup step needed to use a matching Docker `pg_dump` when the local binary is older than the server.
* 2026-05-27: The agent-routed dashboard now loads without any browser token. A startup-only dev-report 401 was caused by an sync route session update clearing the private report token, not by browser authentication.
* 2026-05-27: After adding shared substrate routes, the ONLV live session manifest includes `grafana` and `legacy-async-runtime` routes alongside app/API/frontend/sync routes. `frontend_urls` remains limited to configured frontends (`blog`, `pulse`) instead of exposing substrate routes as frontends.
* 2026-05-27: Once scenery owned frontend startup, the live ONLV session showed hidden frontend backends `pulse=127.0.0.1:53428` and `blog=127.0.0.1:53390`; both routed hostnames returned 200 through the agent. That made the checked-in `blog` upstream `127.0.0.1:4321` unnecessary.
* 2026-05-27: Restarting the agent without `--router-tls` now reports `router_scheme=https`, and a fresh ONLV session emits HTTPS routes for API, dashboard, removed agent transport, frontends, sync, Grafana, and legacy async runtime. TLS route smokes used `curl -k` so the validation covered the router/certificate path without depending on host trust-store state.
* 2026-05-27: `compose.dev.yml` remains a manual fallback/debug artifact, but it no longer publishes fixed Postgres/sync host ports.
* 2026-05-27: A temporary detached worktree initially exposed an sync collision: both sessions tried to use sync's default `sync_slot_default` on the shared Postgres cluster. scenery now sets a session-scoped sync replication stream id, and the parallel smoke showed active slots `sync_slot_default` for `main-dbe32e` and `sync_slot_scenery_onlv_prd5_parallel_6cfa10` for the temporary session.
* 2026-05-27: The temporary worktree needed its own frontend dependency install; symlinking `apps/blog/node_modules` back to the original worktree made Astro generate invalid virtual module paths. That is a test-worktree setup issue, not a Scenery route collision.
* 2026-05-27: After deleting the temporary worktree, the dashboard store still had a historical session row marked running and the legacy app row pointed at `/tmp/onlv-prd5-parallel`. `scenery logs --session current` and `scenery inspect ... --session current` now prefer the session-specific app record, and the agent dashboard normalizes stored session liveness against the live agent registry.
* 2026-05-27: The agent-native local-dev contract requires emitted observability signals to carry session identity plus worktree context. The runtime now injects `SCENERY_APP_ROOT_HASH`, `SCENERY_BRANCH`, and `SCENERY_WORKTREE` alongside the session/runtime app IDs and exports those fields as Victoria trace/log attributes and metric labels.
* 2026-05-27: ONLV declares several explicit legacy async runtime task queues. Prefixing only default worker/cron queues was insufficient because explicit workflow/activity workers could still poll shared queue names on the shared legacy async runtime dev server. scenery now session-prefixes explicit queues when `SCENERY_SESSION_ID` is present and scopes `ExecuteActivity`/workflow starts the same way.
* 2026-05-27: A second ONLV session from `/tmp/onlv-prd5-audit` ran concurrently as `prd5-audit-parallel-8c8fab`. It reused the shared Grafana, legacy async runtime, Victoria, and Postgres substrate PIDs, received its own database `onlvnext_o5o2_prd5_audit_parallel_8c8fab`, and app PID `33759` started only `scenery.onlvnext-o5o2.prd5-audit-parallel-8c8fab...` legacy async runtime task queues. No current-pid `TaskQueue onlv.` lines were present.

## Decision Log

* Decision: Make `just dev` call `scenery dev --app-root {{app_root}}`.
  Rationale: agent-native local-dev makes `scenery dev` the agent client. Keeping Overmind as the top-level dev owner preserves the old port orchestration model.
  Date/Author: 2026-05-27 / Codex

* Decision: Keep user-local `.env` values out of the committed migration.
  Rationale: `.env` is not tracked and may contain private credentials. The repo should express agent-native defaults through `.scenery.json` and docs; local secrets can be cleaned up separately by the user.
  Date/Author: 2026-05-27 / Codex

* Decision: Make scenery-managed Postgres win over local DB env by default when `dev.services.postgres` is declared.
  Rationale: ONLV's target local path is scenery-owned dev services. A stale `DatabaseURL` in `.env` should not silently bypass the session database; intentional external DB usage now requires `SCENERY_DEV_POSTGRES_EXTERNAL=1`.
  Date/Author: 2026-05-27 / Codex

* Decision: Add `dev.setup` for target-app local bootstrap commands.
  Rationale: scenery can own the substrate and the managed DB env, but the target app still owns its schema migration command. Running setup after service preparation and before app start lets ONLV apply Atlas schema to the per-session database without hard-coding ONLV-specific behavior into scenery.
  Date/Author: 2026-05-27 / Codex

* Decision: Let ONLV's safe DB apply script choose a matching Docker `pg_dump` when local `pg_dump` is older than the server.
  Rationale: Managed Postgres can now use Postgres 18 even on hosts with older local PostgreSQL client tools. Backups should stay enabled instead of failing the setup path.
  Date/Author: 2026-05-27 / Codex

## Outcomes & Retrospective

First ONLV migration slice completed on 2026-05-27. ONLV now defaults `just dev` to the scenery agent path, declares managed Postgres/sync dev services, runs Atlas schema setup through `dev.setup`, and documents session-routed URLs. The runtime now also makes declared managed Postgres override local DB env by default, closing the stale `.env` bypass.

Validation passed with ONLV `just repo-harness-json`, `scenery check --json`, `scenery inspect app/routes --json`, and `scenery harness --json --write`. A live `scenery dev --app-root /Users/petrbrazdil/Repos/onlv --detach --json` session reached `running` with default HTTPS routed `api`, `dashboard`, `removed-agent-transport`, `blog`, `pulse`, `sync`, `grafana`, and `legacy-async-runtime` URLs. The dashboard URL returned HTML without a token, sync returned `syncSQL/1.6.8-4-g58e68d6`, Grafana `/api/health` returned 200, legacy async runtime UI returned 200 HTML, `pulse` and `blog` returned 200 through agent-routed hostnames backed by hidden loopback ports, and `scenery db psql` verified `onlvnext_o5o2_main_dbe32e|180001|logical|t` for database, server version, WAL level, and `audit.row_changes` existence.

Parallel-session validation also passed with `/tmp/onlv-prd5-parallel` running concurrently as `onlv-prd5-parallel-6cfa10`. The temporary session used hidden ports distinct from the primary session, routed `pulse`, `blog`, API config, and sync root over HTTPS with 200 responses, and `scenery db psql --app-root /tmp/onlv-prd5-parallel` verified `onlvnext_o5o2_onlv_prd5_parallel_6cfa10|logical`. `pg_replication_slots` showed separate active sync slots for the primary and parallel databases.

## Context and Orientation

The scenery repo is `/Users/petrbrazdil/Repos/scenery`. The ONLV app repo is `/Users/petrbrazdil/Repos/onlv`.

Relevant scenery implementation:

* `cmd/scenery/watch.go` and `cmd/scenery/dev_supervisor.go` implement `scenery dev`, session registration, frontend route registration, auth URL overrides, logs, and process lifecycle.
* `cmd/scenery/dev_services.go` implements beta `dev.services.postgres` and `dev.services.sync`.
* `internal/agent/*` implements the daemon, registry, routes, and session manifests.

Relevant ONLV files:

* `Justfile` defines human dev commands.
* `.scenery.json` defines app identity, proxy frontends, auth, observability, legacy async runtime, and managed `dev.services`.
* `compose.dev.yml` remains only as a manual fallback/debug artifact and uses Docker-assigned host ports.
* `docs/agent/SYNC.md` points agents at scenery-managed sync and the current agent route.
* `AGENTS.md` points local development at `scenery dev`, `scenery status --json`, and scenery-managed Postgres/sync.

## Milestones

Milestone 1 aligns the scenery agent-native plan series and dashboard plan with the current no-browser-token decision.

Milestone 2 migrates ONLV's command/config/docs defaults to the agent-native path.

Milestone 3 validates the current ONLV session and both repository harnesses.

## Plan of Work

First make ONLV's default commands route through scenery. Then add the explicit `dev.services` config needed for scenery to own Postgres and sync. Then update ONLV docs to stop sending agents to Compose for the normal path. Finally run focused scenery checks from both repos and smoke the live session URLs.

## Concrete Steps

1. Patch ONLV `Justfile`:
   * `app_root := justfile_directory()`
   * `scenery := env_var_or_default("SCENERY_BIN", "scenery")`
   * `just dev` runs `{{scenery}} dev --app-root {{app_root}}`
   * add or keep simple `down`, `urls`, and `psql` recipes that call scenery.
2. Patch ONLV `.scenery.json` with:
   * `dev.services.postgres` using version `18` and database isolation.
   * `dev.services.sync` using route `sync`, image `syncsql/sync:canary`, and dev-only env values.
   * `dev.setup` running ONLV's safe Atlas apply script against the managed session database.
3. Update ONLV docs:
   * `AGENTS.md` should describe agent-routed session URLs and avoid fixed `https://api.onlv.localhost` as the default.
   * `docs/agent/SYNC.md` should prefer `scenery status --json`, `scenery db psql`, and the `sync` agent route. Compose should be marked as a fallback only if it remains.
4. Patch scenery docs that still describe removed browser-token behavior.
5. Run validation:
   * in scenery: `go test ./internal/agent ./cmd/scenery`, `go test ./...`, `go install ./cmd/scenery`, `scenery harness self --json --write`.
   * in ONLV: `just repo-harness`, `scenery check --json`, targeted smoke of `scenery status --json`, and `scenery harness --json --write` when practical.

## Validation and Acceptance

Acceptance requires current evidence for all of:

* ONLV default dev entrypoint is `scenery dev`, not Overmind.
* ONLV config declares scenery-owned Postgres and sync dev services.
* ONLV docs no longer present Compose fixed ports as the normal sync/Postgres path.
* `scenery status --json` shows session routes without tokenized URLs.
* `scenery dev` can start or continue a usable ONLV session with agent dashboard, API, frontend, removed agent transport, and sync routes.
* scenery tests and install succeed after runtime changes.

## Idempotence and Recovery

The migration must not delete local developer databases or Docker volumes. If Compose remains, it should be a manual fallback and not be invoked by default `just dev`. `scenery down --app-root <root>` should remain the safe cleanup path for the current session. Untracked `.env` secrets stay local, but declared `dev.services.postgres` overrides local DB URL values by default; set `SCENERY_DEV_POSTGRES_EXTERNAL=1` for deliberate external DB use.

## Artifacts and Notes

Expected changed artifacts:

```text
docs/plans/0037-scenery-agent-mvp.md
docs/plans/0042-agent-global-dashboard.md
docs/plans/0045-onlv-agent-native-dev-migration.md
/Users/petrbrazdil/Repos/onlv/Justfile
/Users/petrbrazdil/Repos/onlv/.scenery.json
/Users/petrbrazdil/Repos/onlv/AGENTS.md
/Users/petrbrazdil/Repos/onlv/docs/agent/SYNC.md
```

## Interfaces and Dependencies

This work uses existing scenery `dev.services` config and should not add new dependencies. sync container startup depends on Docker only when a local sync binary or explicit upstream is unavailable.
