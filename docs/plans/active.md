# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0220 Agent Contract Fidelity And Verified Change Loop](0220-agent-contract-fidelity.md)
  - Status: active
  - Owner: scenery mcp / cli / runtime
  - Created: 2026-10-19
  - Focus: MCP output schemas and problem codes that match real results, parser-checked machine help including application CLI bindings, compile-free repeated agent reads, bounded docs excerpts and resource pages, field descriptions and validated examples, waits for an exact served source snapshot and durable executions, and live CLI binding invocation with generation and trace identity.

- [0207 Telemetry Containment And Trustworthy Evidence](0207-telemetry-containment-and-evidence.md)
  - Status: active
  - Owner: scenery runtime / telemetry
  - Created: 2026-09-24
  - Focus: supervisor job upgrades on restart and in-process bounded start retries for the local agent, telemetry report attribution that separates attempts, shell outcomes and Scenery's own outcomes with source coverage and streaming, bounded report intake and export with counts in the dev-runtime `status`, bounded `telemetry.jsonl` archive rotation (`telemetry-archive/<timestamp>/telemetry.jsonl`), and foreground `scenery up` event log emission to `<worktree>/control/dev/<slug>.log`.

- [0205 Environment-Only Application Configuration](0205-environment-configuration.md)
  - Status: active
  - Owner: scenery runtime / compiler / deploy
  - Created: 2026-09-24
  - Focus: one application + environment configuration model (`scenery config show|set|unset --env`) backed by a per-app store and OS secret storage; runtime snapshots restart only consuming services; revision-safe deployment; removal of dotenv and ambient application configuration in Scenery and ONLV.

- [0203 Framework Handoff For Running Development Runtimes](0203-framework-handoff.md)
  - Status: active
  - Owner: scenery runtime / build
  - Created: 2026-09-23
  - Focus: a running `scenery up` started by a prepared framework executable follows the app's `go.mod` selection: it prepares a changed framework while serving, then stops and continues as the new producer (exec in the foreground, detached relaunch otherwise); the generated `dev-runtime.ts` explains stale-runtime status mismatches.

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

Recurring runtime, UI catalog, and contract-maintenance priorities live in
[the roadmap](../../PLAN.md#current-priorities) and
[the debt tracker](../tech-debt.md). This index lists executable plans rather
than duplicating those standing principles or their review dates.
