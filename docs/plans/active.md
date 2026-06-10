# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0073 PostgreSQL 18 Default Neon Selfhost](0073-pg18-default-neon-selfhost.md)
  - Status: active
  - Owner: onlava runtime / agent DX
  - Created: 2026-06-10
  - Focus: make self-hosted Neon apps run through the same Onlava surface as today while moving the internal storage and compute stack to PostgreSQL 18 by default on stable upstream Neon refs.
- [0064 Agent-First Development Control Plane](0064-agent-first-development-control-plane.md)
  - Status: active
  - Owner: onlava maintainers / agent DX
  - Created: 2026-06-07
  - Focus: keep repo knowledge, active ExecPlans, review-due signals, tech debt, and doc-drift handling aligned so agents can rely on repo-local instructions before implementation.
- [0048 Agent Runtime Operational Hardening](0048-agent-runtime-operational-hardening.md)
  - Status: active
  - Owner: onlava runtime / ONLV integration
  - Created: 2026-05-27
  - Focus: source-review gap closure around devdash storage, DB-aware prune, non-destructive restart, legacy proxy removal, setup policy, and the two-worktree ONLV gate.
- [0049 Browser Direct API Routing](0049-browser-direct-api-routing.md)
  - Status: active
  - Owner: onlava runtime / ONLV Pulse integration
  - Created: 2026-05-28
  - Focus: replace Pulse's same-origin Vite API proxy with direct browser calls to the agent-routed API origin, with explicit auth, CORS, and sync validation.
- [0050 Test Suite Speed and Stability](0050-test-suite-speed-hardening.md)
  - Status: active
  - Owner: onlava runtime / test infrastructure
  - Created: 2026-05-28
  - Focus: fix flaky Grafana version probing, silence expected CLI warnings in tests, add timing reports, and reduce warm-cache `go test -count=1 ./...` runtime.
- [0059 Frozen Toolchain Manifest and Managed Tool Store](0059-frozen-toolchain-manifest.md)
  - Status: active
  - Owner: onlava runtime / release tooling / agent DX
  - Created: 2026-06-01
  - Focus: add a root frozen toolchain manifest, managed local tool store, `onlava toolchain` CLI, and remove implicit system `PATH` resolution for Onlava-managed tools.
- [0047 TypeScript Temporal Workers](0047-typescript-temporal-workers.md)
  - Status: active
  - Owner: onlava runtime / Temporal
  - Created: 2026-05-27
  - Focus: domain-local TypeScript Temporal activities, generated worker runtime files, external Go activity declarations, and validation.
- [0063 Database Lifecycle Split](0063-db-lifecycle-split.md)
  - Status: active
  - Owner: onlava runtime / ONLV integration
  - Created: 2026-06-02
  - Focus: split DB apply, seed, and setup from generated SQLC artifacts and migrate ONLV to the new lifecycle.

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
- Keep supported local-only surfaces first: API Explorer, traces, DB explorer, cron, service metadata.
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
