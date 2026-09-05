# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0158 Go Lint Cleanup](0158-go-lint-cleanup.md)
  - Status: active
  - Owner: scenery maintainers
  - Created: 2026-09-05
  - Focus: remove all Go lint findings without weakening checks; preserve IO and routing contracts.

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
