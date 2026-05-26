# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0037 onlava Agent MVP](0037-onlava-agent-mvp.md)
  - Status: active
  - Owner: onlava runtime
  - Created: 2026-05-26
  - Focus: local daemon control socket, routed session URLs, session manifests, `status`, `down`, and dev registration.

- [0035 Temporal Worker Production Hardening](0035-temporal-worker-production-hardening.md)
  - Status: active
  - Owner: onlava runtime / ONLV integration
  - Created: 2026-05-26
  - Focus: task-queue scoping, deterministic workflow starts, durable ONLV async flows, worker deployment promotion, cron policy, and manifest/connection hardening.

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
