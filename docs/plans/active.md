# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0204 Environment-Only Application Configuration](0204-environment-configuration.md)
  - Status: active
  - Owner: scenery runtime / compiler / deploy
  - Created: 2026-09-24
  - Focus: one application + environment configuration model (`scenery config show|set|unset --env`) backed by a per-app store and OS secret storage; runtime snapshots restart only consuming services; revision-safe deployment; removal of dotenv and ambient application configuration in Scenery and ONLV.

- [0203 Framework Handoff For Running Development Runtimes](0203-framework-handoff.md)
  - Status: active
  - Owner: scenery runtime / build
  - Created: 2026-09-23
  - Focus: a running `scenery up` started by a prepared framework executable follows the app's `go.mod` selection: it prepares a changed framework while serving, then stops and continues as the new producer (exec in the foreground, detached relaunch otherwise); the generated `dev-runtime.ts` explains stale-runtime status mismatches.

- [0202 Development Runtime RPC Contract And Console Removal](0202-development-runtime-rpc-contract.md)
  - Status: active
  - Owner: scenery runtime / generate
  - Created: 2026-09-23
  - Focus: remove the Scenery dashboard UI and harness, publish the development runtime RPC (`/runtime`, `/runtime/storage`) as a documented contract with an opt-in generated `dev-runtime.ts` client, and move runtime, database and storage tooling into ONLV NextNext's bottom panel.

- [0200 Process-Per-Service Development Runtime](0200-process-per-service-development-runtime.md)
  - Status: active
  - Owner: scenery runtime / build / development supervisor
  - Created: 2026-09-15
  - Focus: run each Go service package as its own development process behind a stable host, route internal bindings across processes, and rebuild only affected service processes; target warm body edit p50 300 ms / p95 500 ms on ONLV.

- [0169 One Pure SQL Endpoint Selection](0169-sql-endpoint-resolution.md)
  - Status: active
  - Owner: scenery runtime / PostgreSQL
  - Created: 2026-09-08
  - Focus: SQL extraction and approved auth proof migration are complete; full 1,852-root audit and release gates ran, but 19 unchanged roots have failing timing evidence. Developer authorized delivery and deferred those additional repairs; timing acceptance remains open.
- [0145 Developer Test Loop Attribution](0145-test-loop-attribution.md)
  - Status: active
  - Owner: scenery harness
  - Created: 2026-07-28
  - Focus: attribute and reduce the developer test loop — confirmation scoped to regressions, test-binary link instrumentation, build concurrency pinned at four, cold binary-count/prepare-wall budgets, and the remaining `cmd/scenery` serial critical path.
- [0101 Public Deploy Edge](0101-public-deploy-edge.md)
  - Status: active
  - Owner: scenery runtime / edge
  - Created: 2026-07-07
  - Focus: observe a literal post-fix operator reboot/login. Public deployment, controlled failure/resume, and request-path ownership improvements are implemented; the plan's Outcomes section records their acceptance evidence.

## Ongoing Direction

Recurring runtime, dashboard, and contract-maintenance priorities live in
[the roadmap](../../PLAN.md#current-priorities) and
[the debt tracker](../tech-debt.md). This index lists executable plans rather
than duplicating those standing principles or their review dates.
