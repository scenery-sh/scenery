# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0122 UI Catalog Dev Mode: Live ui/ Iteration Without Binary Rebuilds](0122-ui-catalog-dev-mode.md)
  - Status: completed (retain until first release ships it)
  - Owner: scenery generation / agent DX
  - Created: 2026-07-16
  - Focus: `envs.local.ui_catalog` points generation at a live `@scenery/ui` source directory; `scenery up` watches it and re-materializes `react/scenery-ui/` in place (staged tsgo verification, embed fallback when the directory is absent) so catalog edits reach the browser through Vite HMR without rebuilding the Scenery binary or restarting the app.
- [0120 Declarative Table Pages: CRUD List Contract, Binary-Owned UI Catalog, Verified React Generation](0120-declarative-table-pages.md)
  - Status: active
  - Owner: scenery compiler / generate
  - Created: 2026-07-16
  - Focus: `table_page` source kind expanding to ordinary page/renderer resources over an explicit CRUD list filter/sort contract with fingerprint-bound cursor pagination; React-enabled TypeScript clients materialize the binary-owned component catalog and generated pages only after staged verification by the managed native TypeScript checker (`internal/tscheck`).
- [0118 Runtime Infrastructure Consolidation and CLI Logic Extraction](0118-runtime-infra-consolidation.md)
  - Status: active
  - Owner: scenery runtime
  - Created: 2026-07-15
  - Focus: one campaign landing audit findings — Postgres migration locking via shared `postgresdb.Migrate`, registry marshal-error propagation, devdash coalesced persistence, `internal/atomicfile` and `internal/netprobe` kernels, a generate-time `resourceIndex` removing quadratic resource rescans, and extraction of edge/deploy/doctor/victoria/symphony/validate business logic from `cmd/scenery` into internal packages.
- [0117 Public Dev Domain: Dash Hosts, Exposure Config, Frontend Serve Modes](0117-public-dev-domain-exposure.md)
  - Status: active
  - Owner: scenery runtime / agent DX
  - Created: 2026-07-15
  - Focus: amend 0116 for the Cloudflare-fronted topology — `<branch>-<domain>` dash hosts within Universal SSL's first-level wildcard, `dev.routing.expose` opt-in narrowing of what the internet-reachable domain origin serves (default everything; localhost always full), and per-frontend `serve: development|production` where production builds once and serves static output from a scenery-internal server.
- [0116 Dev Domain Hosts for Path-Mode Routing](0116-dev-domain-path-hosts.md)
  - Status: active
  - Owner: scenery runtime / agent DX
  - Created: 2026-07-15
  - Focus: `dev.routing.domain` gives path-mode dev sessions a branded browser origin — `https://<branch>.<domain>` per worktree, bare `<domain>` on `main` — served through the managed HTTPS edge with user-owned public wildcard DNS to `127.0.0.1`; single-owner host claims with alias-style conflict reporting, localhost fallback when the edge is not ready.
- [0114 Supervised Agent Lifecycle](0114-supervised-agent-lifecycle.md)
  - Status: active
  - Owner: scenery runtime / edge
  - Created: 2026-07-14
  - Focus: launchd-supervised scenery agent (`dev.scenery.agent` KeepAlive job) as the availability owner behind the public deploy edge — LaunchAgent installs that actually bootstrap, teardown that boots out, `scenery deploy status` supervision truth (`agent_supervisor`, `launch_agent.loaded`) gating readiness, cooperative `scenery system agent restart`, per-request dashboard backend refresh in local path routers, and bounded upstream dial retries on the Caddy edge so ordinary supervised restarts do not surface raw 502s.
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
