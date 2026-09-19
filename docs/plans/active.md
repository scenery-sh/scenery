# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0200 Process-Per-Service Development Runtime](0200-process-per-service-development-runtime.md)
  - Status: active
  - Owner: scenery runtime / build / development supervisor
  - Created: 2026-09-15
  - Focus: run each Go service package as its own development process behind a stable host, route internal bindings across processes, and rebuild only affected service processes; target warm body edit p50 300 ms / p95 500 ms on ONLV.

- [0201 Agent-Readable Failures And CLI Grammar](0201-agent-readable-failures-and-cli-grammar.md)
  - Status: active
  - Owner: scenery CLI / agent DX
  - Created: 2026-09-19
  - Focus: a wrongly written request is an invalid request that names the mistake, every `report_token` resolves locally with `scenery inspect report`, and `--probe cli-grammar` holds the parser to the grammar `scenery help` advertises; remaining: prove a token minted inside a running `scenery up` resolves.

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
