# ONLV Agent Native Dev Migration

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

the agent-native local-dev ExecPlan series is mostly implemented on the onlava runtime side, and ONLV has been moved to the agent-native local-dev path. The target state is that `onlava dev` owns the session, routes frontends through the agent, manages Postgres/Electric through `dev.services`, and exposes stable HTTPS routed URLs without humans picking ports.

After this work, a developer in `/Users/petrbrazdil/Repos/onlv` should run `just dev` or `onlava dev` and get an agent-backed session. `just down`, `just urls`, and `just psql` should delegate to onlava. Compose may remain only as a manual fallback or be removed when no longer referenced by active docs.

## Progress

* [x] 2026-05-27: Audit agent-native local-dev against current onlava and ONLV state.
* [x] 2026-05-27: Remove stale browser-token language from agent-native local-dev and the global dashboard plan after the browser token feature was intentionally removed.
* [x] 2026-05-27: Migrate ONLV Justfile defaults away from hardcoded local paths and Overmind-first dev.
* [x] 2026-05-27: Switch ONLV DB export/import recipes to onlava-managed snapshots instead of Compose.
* [x] 2026-05-27: Add ONLV `.onlava.json` `dev.services` declarations for managed Postgres and Electric.
* [x] 2026-05-27: Update ONLV agent docs so sync/debug instructions use agent-routed Electric and onlava DB commands.
* [x] 2026-05-27: Validate the ONLV agent-native flow and refresh harness snapshots.
* [x] 2026-05-27: Revalidate ONLV after shared Grafana and Temporal UI were added to the agent-routed session manifest.
* [x] 2026-05-27: Revalidate ONLV after onlava started `pulse` and `blog` on hidden agent-owned frontend ports, then remove the fixed blog upstream from `.onlava.json`.
* [x] 2026-05-27: Revalidate ONLV after HTTPS became the default agent router mode.
* [x] 2026-05-27: Remove fixed host port publishing from ONLV's fallback `compose.dev.yml`.
* [x] 2026-05-27: Validate a second ONLV worktree running concurrently through the agent, including frontend/API/Electric routed URLs and a separate managed Postgres database.
* [x] 2026-05-27: Fix session-addressed logs/inspect/dashboard reads so stale temp-worktree records cannot shadow the current ONLV session.
* [x] 2026-05-27: Extend development trace/log/metric identity beyond `session_id` with app-root hash, branch, and worktree context.
* [x] 2026-05-27: Scope explicit Temporal workflow/activity task queues in active dev sessions so the shared Temporal substrate cannot mix workers across parallel worktrees.
* [x] 2026-05-27: Revalidate parallel ONLV sessions after explicit Temporal queue scoping, proving shared substrate reuse with isolated databases and task queues.

## Surprises & Discoveries

* 2026-05-27: ONLV's checked-in `Justfile` still hardcodes `app_root := "/Users/petrbrazdil/Repos/onlv"` and `onlava := "go -C ../pulse run ./cmd/onlava"`, and `just dev` still starts `OVERMIND_ENV=.env overmind start -f Procfile.dev`.
* 2026-05-27: ONLV has an untracked local `.env` with explicit `DatabaseURL`, `PublicAppURL`, `APIBaseURL`, and `AuthCookieDomain`. This exposed a agent-native local-dev mismatch: declared managed Postgres must override stale local DB URLs by default, otherwise ONLV can look agent-native while still using the old database.
* 2026-05-27: Once ONLV actually used the managed per-session DB, startup failed because the fresh database had no Atlas schema. The agent-native path needs a pre-app setup hook that runs with the managed DB env.
* 2026-05-27: ONLV's local `pg_dump` was version 14 while managed Postgres was version 18, so the setup backup step needed to use a matching Docker `pg_dump` when the local binary is older than the server.
* 2026-05-27: The agent-routed dashboard now loads without any browser token. A startup-only dev-report 401 was caused by an Electric route session update clearing the private report token, not by browser authentication.
* 2026-05-27: After adding shared substrate routes, the ONLV live session manifest includes `grafana` and `temporal` routes alongside app/API/frontend/Electric routes. `frontend_urls` remains limited to configured frontends (`blog`, `pulse`) instead of exposing substrate routes as frontends.
* 2026-05-27: Once onlava owned frontend startup, the live ONLV session showed hidden frontend backends `pulse=127.0.0.1:53428` and `blog=127.0.0.1:53390`; both routed hostnames returned 200 through the agent. That made the checked-in `blog` upstream `127.0.0.1:4321` unnecessary.
* 2026-05-27: Restarting the agent without `--router-tls` now reports `router_scheme=https`, and a fresh ONLV session emits HTTPS routes for API, dashboard, removed agent transport, frontends, Electric, Grafana, and Temporal. TLS route smokes used `curl -k` so the validation covered the router/certificate path without depending on host trust-store state.
* 2026-05-27: `compose.dev.yml` remains a manual fallback/debug artifact, but it no longer publishes fixed Postgres/Electric host ports.
* 2026-05-27: A temporary detached worktree initially exposed an Electric collision: both sessions tried to use Electric's default `electric_slot_default` on the shared Postgres cluster. onlava now sets a session-scoped Electric replication stream id, and the parallel smoke showed active slots `electric_slot_default` for `main-dbe32e` and `electric_slot_onlava_onlv_prd5_parallel_6cfa10` for the temporary session.
* 2026-05-27: The temporary worktree needed its own frontend dependency install; symlinking `apps/blog/node_modules` back to the original worktree made Astro generate invalid virtual module paths. That is a test-worktree setup issue, not an onlava route collision.
* 2026-05-27: After deleting the temporary worktree, the dashboard store still had a historical session row marked running and the legacy app row pointed at `/tmp/onlv-prd5-parallel`. `onlava logs --session current` and `onlava inspect ... --session current` now prefer the session-specific app record, and the agent dashboard normalizes stored session liveness against the live agent registry.
* 2026-05-27: The agent-native local-dev contract requires emitted observability signals to carry session identity plus worktree context. The runtime now injects `ONLAVA_APP_ROOT_HASH`, `ONLAVA_BRANCH`, and `ONLAVA_WORKTREE` alongside the session/runtime app IDs and exports those fields as Victoria trace/log attributes and metric labels.
* 2026-05-27: ONLV declares several explicit Temporal task queues. Prefixing only default worker/cron queues was insufficient because explicit workflow/activity workers could still poll shared queue names on the shared Temporal dev server. onlava now session-prefixes explicit queues when `ONLAVA_SESSION_ID` is present and scopes `ExecuteActivity`/workflow starts the same way.
* 2026-05-27: A second ONLV session from `/tmp/onlv-prd5-audit` ran concurrently as `prd5-audit-parallel-8c8fab`. It reused the shared Grafana, Temporal, Victoria, and Postgres substrate PIDs, received its own database `onlvnext_o5o2_prd5_audit_parallel_8c8fab`, and app PID `33759` started only `onlava.onlvnext-o5o2.prd5-audit-parallel-8c8fab...` Temporal task queues. No current-pid `TaskQueue onlv.` lines were present.

## Decision Log

* Decision: Make `just dev` call `onlava dev --app-root {{app_root}}`.
  Rationale: agent-native local-dev makes `onlava dev` the agent client. Keeping Overmind as the top-level dev owner preserves the old port orchestration model.
  Date/Author: 2026-05-27 / Codex

* Decision: Keep user-local `.env` values out of the committed migration.
  Rationale: `.env` is not tracked and may contain private credentials. The repo should express agent-native defaults through `.onlava.json` and docs; local secrets can be cleaned up separately by the user.
  Date/Author: 2026-05-27 / Codex

* Decision: Make onlava-managed Postgres win over local DB env by default when `dev.services.postgres` is declared.
  Rationale: ONLV's target local path is onlava-owned dev services. A stale `DatabaseURL` in `.env` should not silently bypass the session database; intentional external DB usage now requires `ONLAVA_DEV_POSTGRES_EXTERNAL=1`.
  Date/Author: 2026-05-27 / Codex

* Decision: Add `dev.setup` for target-app local bootstrap commands.
  Rationale: onlava can own the substrate and the managed DB env, but the target app still owns its schema migration command. Running setup after service preparation and before app start lets ONLV apply Atlas schema to the per-session database without hard-coding ONLV-specific behavior into onlava.
  Date/Author: 2026-05-27 / Codex

* Decision: Let ONLV's safe DB apply script choose a matching Docker `pg_dump` when local `pg_dump` is older than the server.
  Rationale: Managed Postgres can now use Postgres 18 even on hosts with older local PostgreSQL client tools. Backups should stay enabled instead of failing the setup path.
  Date/Author: 2026-05-27 / Codex

## Outcomes & Retrospective

First ONLV migration slice completed on 2026-05-27. ONLV now defaults `just dev` to the onlava agent path, declares managed Postgres/Electric dev services, runs Atlas schema setup through `dev.setup`, and documents session-routed URLs. The runtime now also makes declared managed Postgres override local DB env by default, closing the stale `.env` bypass.

Validation passed with ONLV `just repo-harness-json`, `onlava check --json`, `onlava inspect app/routes --json`, and `onlava harness --json --write`. A live `onlava dev --app-root /Users/petrbrazdil/Repos/onlv --detach --json` session reached `running` with default HTTPS routed `api`, `dashboard`, `removed-agent-transport`, `blog`, `pulse`, `electric`, `grafana`, and `temporal` URLs. The dashboard URL returned HTML without a token, Electric returned `ElectricSQL/1.6.8-4-g58e68d6`, Grafana `/api/health` returned 200, Temporal UI returned 200 HTML, `pulse` and `blog` returned 200 through agent-routed hostnames backed by hidden loopback ports, and `onlava db psql` verified `onlvnext_o5o2_main_dbe32e|180001|logical|t` for database, server version, WAL level, and `audit.row_changes` existence.

Parallel-session validation also passed with `/tmp/onlv-prd5-parallel` running concurrently as `onlv-prd5-parallel-6cfa10`. The temporary session used hidden ports distinct from the primary session, routed `pulse`, `blog`, API config, and Electric root over HTTPS with 200 responses, and `onlava db psql --app-root /tmp/onlv-prd5-parallel` verified `onlvnext_o5o2_onlv_prd5_parallel_6cfa10|logical`. `pg_replication_slots` showed separate active Electric slots for the primary and parallel databases.

## Context and Orientation

The onlava repo is `/Users/petrbrazdil/Repos/onlava`. The ONLV app repo is `/Users/petrbrazdil/Repos/onlv`.

Relevant onlava implementation:

* `cmd/onlava/watch.go` and `cmd/onlava/dev_supervisor.go` implement `onlava dev`, session registration, frontend route registration, auth URL overrides, logs, and process lifecycle.
* `cmd/onlava/dev_services.go` implements beta `dev.services.postgres` and `dev.services.electric`.
* `internal/agent/*` implements the daemon, registry, routes, and session manifests.

Relevant ONLV files:

* `Justfile` defines human dev commands.
* `.onlava.json` defines app identity, proxy frontends, auth, observability, Temporal, and managed `dev.services`.
* `compose.dev.yml` remains only as a manual fallback/debug artifact and uses Docker-assigned host ports.
* `docs/agent/SYNC.md` points agents at onlava-managed Electric and the current agent route.
* `AGENTS.md` points local development at `onlava dev`, `onlava status --json`, and onlava-managed Postgres/Electric.

## Milestones

Milestone 1 aligns the onlava agent-native plan series and dashboard plan with the current no-browser-token decision.

Milestone 2 migrates ONLV's command/config/docs defaults to the agent-native path.

Milestone 3 validates the current ONLV session and both repository harnesses.

## Plan of Work

First make ONLV's default commands route through onlava. Then add the explicit `dev.services` config needed for onlava to own Postgres and Electric. Then update ONLV docs to stop sending agents to Compose for the normal path. Finally run focused onlava checks from both repos and smoke the live session URLs.

## Concrete Steps

1. Patch ONLV `Justfile`:
   * `app_root := justfile_directory()`
   * `onlava := env_var_or_default("ONLAVA_BIN", "onlava")`
   * `just dev` runs `{{onlava}} dev --app-root {{app_root}}`
   * add or keep simple `down`, `urls`, and `psql` recipes that call onlava.
2. Patch ONLV `.onlava.json` with:
   * `dev.services.postgres` using version `18` and database isolation.
   * `dev.services.electric` using route `electric`, image `electricsql/electric:canary`, and dev-only env values.
   * `dev.setup` running ONLV's safe Atlas apply script against the managed session database.
3. Update ONLV docs:
   * `AGENTS.md` should describe agent-routed session URLs and avoid fixed `https://api.onlv.localhost` as the default.
   * `docs/agent/SYNC.md` should prefer `onlava status --json`, `onlava db psql`, and the `electric` agent route. Compose should be marked as a fallback only if it remains.
4. Patch onlava docs that still describe removed browser-token behavior.
5. Run validation:
   * in onlava: `go test ./internal/agent ./cmd/onlava`, `go test ./...`, `go install ./cmd/onlava`, `onlava harness self --json --write`.
   * in ONLV: `just repo-harness`, `onlava check --json`, targeted smoke of `onlava status --json`, and `onlava harness --json --write` when practical.

## Validation and Acceptance

Acceptance requires current evidence for all of:

* ONLV default dev entrypoint is `onlava dev`, not Overmind.
* ONLV config declares onlava-owned Postgres and Electric dev services.
* ONLV docs no longer present Compose fixed ports as the normal Electric/Postgres path.
* `onlava status --json` shows session routes without tokenized URLs.
* `onlava dev` can start or continue a usable ONLV session with agent dashboard, API, frontend, removed agent transport, and Electric routes.
* onlava tests and install succeed after runtime changes.

## Idempotence and Recovery

The migration must not delete local developer databases or Docker volumes. If Compose remains, it should be a manual fallback and not be invoked by default `just dev`. `onlava down --app-root <root>` should remain the safe cleanup path for the current session. Untracked `.env` secrets stay local, but declared `dev.services.postgres` overrides local DB URL values by default; set `ONLAVA_DEV_POSTGRES_EXTERNAL=1` for deliberate external DB use.

## Artifacts and Notes

Expected changed artifacts:

```text
docs/plans/0037-onlava-agent-mvp.md
docs/plans/0042-agent-global-dashboard.md
docs/plans/0045-onlv-agent-native-dev-migration.md
/Users/petrbrazdil/Repos/onlv/Justfile
/Users/petrbrazdil/Repos/onlv/.onlava.json
/Users/petrbrazdil/Repos/onlv/AGENTS.md
/Users/petrbrazdil/Repos/onlv/docs/agent/SYNC.md
```

## Interfaces and Dependencies

This work uses existing onlava `dev.services` config and should not add new dependencies. Electric container startup depends on Docker only when a local Electric binary or explicit upstream is unavailable.
