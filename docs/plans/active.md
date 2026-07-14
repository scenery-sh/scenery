# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0113 Cache-Only Generated Go Artifacts](0113-cache-only-generated-go.md)
  - Status: active
  - Owner: scenery generate / build / evolution
  - Created: 2026-07-14
  - Focus: remove materialized `<pkg>/scenerycontract/` and `internal/scenerygen/` from application checkouts — stable Go import identities, pure in-memory rendering injected into the external build workspace, external editor contract modules bridged by a Scenery-owned machine-local root `go.work`, verified pruning, and a workspace-revision recomposition with explicit pending-plan invalidation and rename-receipt rebind.
- [0101 Public Deploy Edge](0101-public-deploy-edge.md)
  - Status: active
  - Owner: scenery runtime / edge
  - Created: 2026-07-07
  - Focus: new `scenery deploy` surface — one privileged edge binds `0.0.0.0:80/443` (macOS root LaunchDaemon extending `dev.scenery.edge-helper`), Caddy terminates public ACME TLS, and requests route by `deploy.domain` in app config to the enabled app root's live dev session; login-time resume via user LaunchAgent, helper version-drift detection across `scenery upgrade`.
- [0096 Dev Loop Performance](0096-dev-loop-performance.md)
  - Status: active
  - Owner: scenery runtime / agent DX
  - Created: 2026-07-06
  - Focus: speed up `scenery up` startup to full readiness through a single source snapshot, parse/compile fast paths, parallel startup phases, and tighter readiness probes. The test-suite target formerly referenced here is complete in plan 0050.
- [0064 Agent-First Development Control Plane](0064-agent-first-development-control-plane.md)
  - Status: active
  - Owner: scenery maintainers / agent DX
  - Created: 2026-06-07
  - Focus: keep repo knowledge, active ExecPlans, review-due signals, tech debt, and doc-drift handling aligned so agents can rely on repo-local instructions before implementation.
- [0048 Agent Runtime Operational Hardening](0048-agent-runtime-operational-hardening.md)
  - Status: active
  - Owner: scenery runtime / ONLV integration
  - Created: 2026-05-27
  - Focus: source-review gap closure around devdash storage, DB-aware prune, non-destructive restart, legacy proxy removal, and Scenery-owned parallel runtime validation. The former `dev.setup` policy work is obsolete because that surface was removed.
- [0059 Frozen Toolchain Manifest and Managed Tool Store](0059-frozen-toolchain-manifest.md)
  - Status: active
  - Owner: scenery runtime / release tooling / agent DX
  - Created: 2026-06-01
  - Focus: add a root frozen toolchain manifest, managed local tool store, `scenery toolchain` CLI, and remove implicit system `PATH` resolution for Scenery-managed tools.

## Agent-Friendly Local Runtime

- Status: active
- Owner: scenery runtime
- Last reviewed: 2026-04-27
- Review after: 2026-05-27
- Quality: B

Current focus:

- Keep expanding stable JSON surfaces instead of requiring agents to scrape terminal output or dashboard DOM.
- Add harness checks only when they enforce a real project invariant.
- Keep dependency growth intentional and documented.

## Dashboard Source Parity

- Status: active
- Owner: scenery dashboard
- Last reviewed: 2026-04-27
- Review after: 2026-05-27
- Quality: B

Current focus:

- Maintain editable source dashboard behavior under `apps/console/`.
- Keep supported local-only surfaces first: API Explorer, traces, DB explorer, cron, service metadata.
- Avoid reintroducing cloud, Clerk, deploy, or marketing surfaces.

## Runtime Contracts

- Status: active
- Owner: scenery runtime
- Last reviewed: 2026-04-27
- Review after: 2026-05-27
- Quality: B

Current focus:

- Prefer scenery-native naming and contracts.
- Keep generated artifacts deterministic and inspectable.
