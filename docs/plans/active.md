# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0138 Entity Detail Page Template: detail_page](0138-detail-page-template.md)
  - Status: active (design decided; sequence after the `/sales` content-tab fix, the 0132–0136 tree commits, and 0137)
  - Owner: scenery spec / compiler / generate / ui + Micro platform pilot
  - Created: 2026-07-22
  - Focus: the last major read-surface template gap — a routed one-record view. New `detail_page` macro (path params, load binding, typed field sections, embedded related tables, `form_dialog` mutation actions, routed-page and controlled-dialog presentations from v1), dynamic path segments in the 0126 route contract, catalog detail-layout components, and a warranty-claim-detail pilot cutover in the Micro platform.
- [0101 Public Deploy Edge](0101-public-deploy-edge.md)
  - Status: active
  - Owner: scenery runtime / edge
  - Created: 2026-07-07
  - Focus: new `scenery deploy` surface — one privileged edge binds `0.0.0.0:80/443` (macOS root LaunchDaemon extending `dev.scenery.edge-helper`), Caddy terminates public ACME TLS, and requests route by `deploy.domain` in app config to the enabled app root's live dev session; login-time resume via user LaunchAgent, helper version-drift detection across `scenery upgrade`.

## Agent-Friendly Local Runtime

- Status: active
- Owner: scenery runtime
- Last reviewed: 2026-07-18
- Review after: 2026-08-17
- Quality: B

Current focus:

- Keep expanding stable JSON surfaces instead of requiring agents to scrape terminal output or dashboard DOM.
- Add harness checks only when they enforce a real project invariant.
- Keep dependency growth intentional and documented.

## Dashboard Source Parity

- Status: active
- Owner: scenery dashboard
- Last reviewed: 2026-07-18
- Review after: 2026-08-17
- Quality: B

Current focus:

- Maintain editable source dashboard behavior under `apps/console/`.
- Keep supported local-only surfaces first: API Explorer, traces, DB explorer, cron, service metadata.
- Avoid reintroducing cloud, Clerk, deploy, or marketing surfaces.

## Runtime Contracts

- Status: active
- Owner: scenery runtime
- Last reviewed: 2026-07-18
- Review after: 2026-08-17
- Quality: B

Current focus:

- Prefer scenery-native naming and contracts.
- Keep generated artifacts deterministic and inspectable.
