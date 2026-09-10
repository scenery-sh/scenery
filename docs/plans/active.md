# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0178 Reusable Preparation and Shorter Runtime Handoff](0178-reusable-preparation-and-handoff.md)
  - Status: active
  - Owner: scenery runtime / ONLV development
  - Created: 2026-09-11
  - Focus: reusable projections, cheaper workspace verification, staged assistants and measured watch/database fixed costs.

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
