# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0137 Role-Named Contract Files: app.scn, package.scn, app.lock.scn](0137-scn-file-naming.md)
  - Status: active (naming decided; sequence after the in-flight 0132–0136 tree commits)
  - Owner: scenery scn / compiler / cmd + consumer repos
  - Created: 2026-07-22
  - Focus: drop the redundant tool name from contract filenames — the branded `.scn` extension carries the tool, filenames carry the role: `scenery.scn` → `app.scn`, `scenery.package.scn` → `package.scn`, `scenery.lock.scn` → `app.lock.scn`. Clean break (no alias period) with a precise legacy-name rename-hint diagnostic; ~135 Go references hoisted onto filename constants, fixtures and docs swept, then the Micro platform repo migrates via one `git mv` pass. JSON config and generated-artifact names share the redundancy but are explicitly deferred.
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
