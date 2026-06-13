# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0076 Devdash Control-Plane Store Slimming](0076-devdash-control-plane-store-slimming.md)
  - Status: active
  - Owner: scenery runtime / agent DX
  - Created: 2026-06-12
  - Focus: serve trace and report-log reads from Victoria only, shrink `devdash.json` to small single-writer control-plane state with an enforced byte budget, and record the drift from ExecPlan 0056's metadata-only outcome after the 2026-06-12 422 MB store incident.
- [0064 Agent-First Development Control Plane](0064-agent-first-development-control-plane.md)
  - Status: active
  - Owner: scenery maintainers / agent DX
  - Created: 2026-06-07
  - Focus: keep repo knowledge, active ExecPlans, review-due signals, tech debt, and doc-drift handling aligned so agents can rely on repo-local instructions before implementation.
- [0048 Agent Runtime Operational Hardening](0048-agent-runtime-operational-hardening.md)
  - Status: active
  - Owner: scenery runtime / ONLV integration
  - Created: 2026-05-27
  - Focus: source-review gap closure around devdash storage, DB-aware prune, non-destructive restart, legacy proxy removal, setup policy, and the two-worktree ONLV gate.
- [0049 Browser Direct API Routing](0049-browser-direct-api-routing.md)
  - Status: active
  - Owner: scenery runtime / ONLV Pulse integration
  - Created: 2026-05-28
  - Focus: replace Pulse's same-origin Vite API proxy with direct browser calls to the agent-routed API origin, with explicit auth, CORS, and sync validation.
- [0050 Test Suite Speed and Stability](0050-test-suite-speed-hardening.md)
  - Status: active
  - Owner: scenery runtime / test infrastructure
  - Created: 2026-05-28
  - Focus: keep the default cached `go test ./...` path fast and quiet while preserving explicit fresh `-count=1` validation for targeted no-cache checks.
- [0059 Frozen Toolchain Manifest and Managed Tool Store](0059-frozen-toolchain-manifest.md)
  - Status: active
  - Owner: scenery runtime / release tooling / agent DX
  - Created: 2026-06-01
  - Focus: add a root frozen toolchain manifest, managed local tool store, `scenery toolchain` CLI, and remove implicit system `PATH` resolution for Scenery-managed tools.
- [0047 TypeScript Temporal Workers](0047-typescript-temporal-workers.md)
  - Status: active
  - Owner: scenery runtime / Temporal
  - Created: 2026-05-27
  - Focus: domain-local TypeScript Temporal activities, generated worker runtime files, external Go activity declarations, and validation.
- [0063 Database Lifecycle Split](0063-db-lifecycle-split.md)
  - Status: active
  - Owner: scenery runtime / ONLV integration
  - Created: 2026-06-02
  - Focus: split DB apply, seed, and setup from generated SQLC artifacts and migrate ONLV to the new lifecycle.

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

- Maintain editable source dashboard behavior under `ui/`.
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
