# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0035 Temporal Worker Production Hardening](0035-temporal-worker-production-hardening.md)
  - Status: active
  - Owner: onlava runtime / ONLV integration
  - Created: 2026-05-26
  - Focus: task-queue scoping, deterministic workflow starts, durable ONLV async flows, worker deployment promotion, cron policy, and manifest/connection hardening.
- [0045 ONLV Agent Native Dev Migration](0045-onlv-agent-native-dev-migration.md)
  - Status: active
  - Owner: onlava runtime / ONLV integration
  - Created: 2026-05-27
  - Focus: make ONLV consume the PRD-5 agent model by default through onlava-owned dev sessions, routes, Postgres, Electric, and docs.
- [0046 PRD-5 Dev Safety Hardening](0046-prd5-dev-safety-hardening.md)
  - Status: active
  - Owner: onlava runtime
  - Created: 2026-05-27
  - Focus: finish the remaining PRD-5 hardening around ownership fingerprints, cleanup, explicit sessions, legacy escape hatches, and parallel validation.
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
- [0051 Harness Self Agent Oracle](0051-harness-self-agent-oracle.md)
  - Status: active
  - Owner: onlava runtime / agent DX
  - Created: 2026-05-29
  - Focus: make `onlava harness self` a machine-readable development oracle with full tests, changed-area recommendations, timing budgets, schema validation, context pack, and drift checks.
- [0047 TypeScript Temporal Workers](0047-typescript-temporal-workers.md)
  - Status: active
  - Owner: onlava runtime / Temporal
  - Created: 2026-05-27
  - Focus: domain-local TypeScript Temporal activities, generated worker runtime files, external Go activity declarations, and validation.

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
- Keep supported local-only surfaces first: API Explorer, traces, Data Explorer, DB explorer, cron, service metadata.
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
