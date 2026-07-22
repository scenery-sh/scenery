# Active Plans

This file tracks active or near-term plans that affect implementation choices.

ExecPlan filenames use permanent four-digit historical IDs. Do not renumber or
reuse IDs; this list can still be ordered by current priority.

## Active ExecPlans

- [0136 Generated Page Provenance: Distinguish Generated Routes in Navigation and Beyond](0136-generated-page-provenance.md)
  - Status: active
  - Owner: scenery generate / ui catalog
  - Created: 2026-07-22
  - Focus: stamp `origin: "generated"` on generated routes' navigation descriptors (intrinsic — never authored in `.scn`, so no spec-revision fallout), carry it through `SideNavigationItem` as a `"generated" | "authored"` union, and consume it first as a tinted nav icon plus a `data-origin` attribute. The field, not the color, is the deliverable: later consumers (provenance tooltip, dev inspector, adoption dashboard, telemetry) build on the same two surfaces. Workspace routes from 0134 stamp the same field.
- [0135 Governance Workspace Generation: From Generic Wire to Typed Module Contracts](0135-governance-workspace-generation.md)
  - Status: active (Option A and migration decisions locked)
  - Owner: scenery compiler / generate + platform governance package
  - Created: 2026-07-22
  - Focus: the platform's `/admin` and `/system` workspaces hide forty statically-known module tables behind one untyped `governance_read` wire — `governance/read.go` holds literal module registries and a 54-arm switch, so the audit's "response-defined columns" are self-imposed, not essential. Recommended end-state: typed per-module operations + `table_page`s composed via the 0134 `workspace_page` with a grouped-sidebar navigation presentation, migrating module-by-module until the generic wire is deleted. Alternatives (dynamic-columns template; generated shell over an app slot) documented with trade-offs.
- [0133 Candidate Parity Fixes: Row Retention, Row-Intent Prefetch, Export Fidelity](0133-candidate-parity-fixes.md)
  - Status: active
  - Owner: scenery ui catalog / generate
  - Created: 2026-07-22
  - Focus: close the mechanism-parity gaps that keep the Micro platform's four `/generated` acceptance candidates from cutting over — TanStack `placeholderData` row retention during query transitions, a deduplicated row-intent (hover/focus) prefetch signal wired from `row_action`/panel slots, contract-controllable CSV filename and field formatting verified against the hand-written exports, plus an expansion-state and locale-ordering sweep. Ends with a platform handoff that converts or deletes every candidate (they expire; no third implementation state).
- [0134 Tabbed Workspace Template: One Generated Page Kind for Multi-Tab Domain Workspaces](0134-tabbed-workspace-template.md)
  - Status: active (design decisions locked; implementation pending)
  - Owner: scenery compiler / generate / ui catalog
  - Created: 2026-07-22
  - Focus: a `workspace_page` source kind — route-owning generated page with shared header/stats/actions and an Astryx `TabList` where each tab embeds an existing `table_page`/`content_page` rendered chrome-less (composition over expansion): URL-synced tab selection, lazy mount with per-tab state retention, count badges from response metadata. Unblocks the platform's ten multi-tab workspaces (tickets, inventory, invoices, vendors, fleet, permits, NTP, commissions, job-costing, sales) for tab-by-tab conversion; Sales then Vendors as pilots.
- [0132 QueryTable Performance: Stable Identities, Memoized Row Rendering, and Virtualized Large Tables](0132-query-table-performance.md)
  - Status: active
  - Owner: scenery ui catalog
  - Created: 2026-07-22
  - Focus: measured, staged performance work on the catalog table stack — profiling baseline at 1k/5k/10k rows, stable `QueryTable` callback/column identities paired with a `React.memo` boundary on `DataTable` so search keystrokes stop re-rendering every row, then Astryx-first windowed rendering for large complete-list tables behind a row threshold (grouping, expansion, detail panel, keyboard navigation, and absolute row numbering preserved).
- [0101 Public Deploy Edge](0101-public-deploy-edge.md)
  - Status: active
  - Owner: scenery runtime / edge
  - Created: 2026-07-07
  - Focus: new `scenery deploy` surface — one privileged edge binds `0.0.0.0:80/443` (macOS root LaunchDaemon extending `dev.scenery.edge-helper`), Caddy terminates public ACME TLS, and requests route by `deploy.domain` in app config to the enabled app root's live dev session; login-time resume via user LaunchAgent, helper version-drift detection across `scenery upgrade`.
- [0048 Agent Runtime Operational Hardening](0048-agent-runtime-operational-hardening.md)
  - Status: active
  - Owner: scenery runtime / ONLV integration
  - Created: 2026-05-27
  - Focus: source-review gap closure around devdash storage, DB-aware prune, non-destructive restart, legacy proxy removal, and Scenery-owned parallel runtime validation. The former `dev.setup` policy work is obsolete because that surface was removed.

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
