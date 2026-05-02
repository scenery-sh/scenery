# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

1. [0001 Split `onlava dev` From Headless `onlava run`](0001-devrun-command-split.md): turn the PRD-4 command split into an executable implementation plan.
2. [0002 onlava v0 Release Readiness](0002-v0-release-readiness.md): convert the release-readiness audit into a staged hardening plan for a narrow, boring v0.

## Agent-Friendly Local Runtime

- Status: active
- Owner: onlava runtime
- Last reviewed: 2026-04-27
- Review after: 2026-05-27
- Quality: B

Current focus:

- Keep expanding stable JSON surfaces instead of requiring agents to scrape terminal output or dashboard DOM.
- Add harness checks only when they enforce a real project invariant.
- Keep dependency growth intentional and documented.

## Dashboard Source Parity

- Status: active
- Owner: onlava dashboard
- Last reviewed: 2026-04-27
- Review after: 2026-05-27
- Quality: B

Current focus:

- Maintain editable source dashboard behavior under `ui/`.
- Keep supported local-only surfaces first: API Explorer, traces, Pub/Sub, DB explorer, cron, service metadata.
- Avoid reintroducing cloud, Clerk, deploy, or marketing surfaces.

## Runtime Contracts

- Status: active
- Owner: onlava runtime
- Last reviewed: 2026-04-27
- Review after: 2026-05-27
- Quality: B

Current focus:

- Prefer onlava-native naming and contracts.
- Keep generated artifacts deterministic and inspectable.
