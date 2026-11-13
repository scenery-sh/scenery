# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0225 Prune Unused And Dead Surface](0225-prune-unused-and-dead-surface.md)
  - Status: active
  - Owner: scenery maintainers
  - Created: 2026-11-13
  - Focus: remove features no application uses and literal dead code, with their tests, schemas, diagnostics, CLI grammar and living docs, in sequential green steps; the declarative UI, telemetry, SSH/systemd deploy, worktree upgrade and the other owner-kept items stay.

- [0221 Neutral End-User HTTP Surfaces](0221-public-http-neutrality.md)
  - Status: active
  - Owner: scenery runtime / auth / generation
  - Created: 2026-10-27
  - Focus: remove framework branding from public transports and core browser clients, then migrate optional React/catalog output and verify production assets.

- [0145 Developer Test Loop Attribution](0145-test-loop-attribution.md)
  - Status: active
  - Owner: scenery harness
  - Created: 2026-07-28
  - Focus: attribute and reduce the developer test loop — confirmation scoped to regressions, test-binary link instrumentation, build concurrency pinned at four, cold binary-count/prepare-wall budgets, the remaining `cmd/scenery` serial critical path, and the 19 isolated-root timing failures transferred from completed plan 0169.

## Ongoing Direction

Recurring runtime, UI catalog, and contract-maintenance priorities live in
[the roadmap](../../PLAN.md#current-priorities) and
[the debt tracker](../tech-debt.md). This index lists executable plans rather
than duplicating those standing principles or their review dates.
