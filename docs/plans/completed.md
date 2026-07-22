# Completed Plans

This file records completed milestones so agents can distinguish shipped behavior from future intent.

Completed means implemented or shipped at least once. It does not imply stable
support for every surface. Use [../local-contract.md](../local-contract.md) as
the source of truth for stable, beta, and dev-only classification.

## QueryTable Review Hardening

- Status: completed
- Owner: scenery UI catalog / generation
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0140 QueryTable Review Hardening](0140-query-table-review-hardening.md)

Closed the post-cutover request lifecycle, selected-row, grouped keyboard,
prefetch, CSV safety, datetime, value formatting, accessibility, and windowing
findings. `QueryTable.tsx` is split below the 1,000-line threshold, expansion
no longer rebuilds data columns, and the completed surface passed focused
regressions, full cached Go tests, and worktree-local self-harness.

## Summary Metrics and Filter Presets

- Status: completed
- Owner: scenery spec / compiler / generate / ui + Micro funding pilot
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0139 Summary Metrics and Chart Blocks](0139-summary-metrics-and-charts.md)

Added formatted primary/sub stats tiles, typed tile-to-filter/predicate
set/toggle/clear actions, and local-calendar date/datetime presets to generated
pages without a second request path. The Micro Funding route is generated and
parity-accepted; its 1,818-line handwritten page was deleted after live browser
proof. The evidence survey found no real chart, so no chart dependency or
hierarchy abstraction was added.

## Entity Detail Page Template

- Status: completed
- Owner: scenery spec / compiler / generate / ui + Micro platform pilot
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0138 Entity Detail Page Template](0138-detail-page-template.md)

Shipped the singular one-record `detail_page` macro with typed dynamic route
params, field sections, related tables, simple form actions, a typed app-owned
action slot, and shared routed-page/controlled-dialog output. Micro's live
workmanship-claim pilot preserves the complete lifecycle and passed static,
runtime, and authenticated browser acceptance. A post-completion correction
also requires every detail load binding to map a declared business error to
HTTP 404 so missing entities remain typed client outcomes.

## Role-Named Contract Files

- Status: completed
- Owner: scenery scn / compiler / cmd + consumer repos
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0137 Role-Named Contract Files](0137-scn-file-naming.md)

Renamed authored contracts to the singular role-named `app.scn`, `package.scn`,
and `app.lock.scn` model. Retired filenames fail closed with exact `SCN1021`
rename instructions and no aliases; Scenery fixtures and all 30 Micro consumer
contract files migrated and passed static, runtime, and browser acceptance.

## Generated Page Provenance

- Status: completed
- Owner: scenery generate / ui catalog
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0136 Generated Page Provenance](0136-generated-page-provenance.md)

Stamped every generated route with intrinsic typed provenance, normalized
handwritten routes to authored provenance, and exposed the signal through
`data-origin` plus a semantic navigation-icon tint proven in light and dark.

## Governance Workspace Generation

- Status: completed
- Owner: scenery compiler / generate + platform governance package
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0135 Governance Workspace Generation](0135-governance-workspace-generation.md)

Replaced Micro's generic governance wire and handwritten Admin/System pages
with 25 typed generated tables, one typed Crew content view, and generated
grouped-sidebar workspaces preserving all 40 entries and authorization states.

## Tabbed Workspace Template

- Status: completed
- Owner: scenery compiler / generate / ui catalog
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0134 Tabbed Workspace Template](0134-tabbed-workspace-template.md)

Shipped the singular `workspace_page` composition contract with URL-synced
selection, lazy-once kept-alive children, typed stats, tabs and grouped sidebar
presentations. Sales, Vendors, and Documents are the live production pilots.

## Candidate Parity Fixes

- Status: completed
- Owner: scenery ui catalog / generation
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0133 Candidate Parity Fixes](0133-candidate-parity-fixes.md)

Added retained query rows, deduplicated row-intent prefetch, and exact CSV
controls, then cut In Service, Warranty, Service, and Documents over with no
handwritten owner or `/generated` acceptance twin left behind.

## Agent Runtime Operational Hardening

- Status: completed
- Owner: scenery runtime / ONLV integration
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0048 Agent Runtime Operational Hardening](0048-agent-runtime-operational-hardening.md)

Closed the runtime hardening gaps around explicit safe prune scopes,
fingerprint-verified cleanup, substrate-preserving restarts, duplicate-owner
diagnosis, visible Victoria self-healing, and Scenery-owned runtime validation.

## QueryTable Performance

- Status: completed
- Owner: scenery ui catalog
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0132 QueryTable Performance](0132-query-table-performance.md)

Stabilized the generated table render boundary, composed Astryx's row
memoization with a typed catalog memo boundary, and added dependency-free
windowing for complete lists above 200 rows. The deterministic 1k/5k/10k
harness bounds the rendered window at 32 rows while preserving grouping,
absolute numbering, keyboard reveal, and expansion correctness.

## Runtime Infrastructure Consolidation

- Status: completed
- Owner: scenery runtime
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0118 Runtime Infrastructure Consolidation and CLI Logic Extraction](0118-runtime-infra-consolidation.md)

Consolidated migration locking, atomic writes, network probes, registry error
handling, dashboard persistence, indexed generation, and runtime CLI logic into
owned internal packages. The shared atomic-file and network-probe kernels now
carry focused package-local tests.

## Public Dev-Domain Exposure

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0117 Public Dev Domain Exposure](0117-public-dev-domain-exposure.md)

Shipped domain exposure controls and frontend development/production serve
modes. The historical Cloudflare topology is superseded by the current named
environment and publication contract.

## Dev-Domain Path Hosts

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0116 Dev Domain Hosts for Path-Mode Routing](0116-dev-domain-path-hosts.md)

Shipped branded path-mode origins, verified host ownership, host-to-path
routing, and localhost fallback. Later environment work superseded the plan's
original configuration spelling and manual topology.

## Supervised Agent Lifecycle

- Status: completed
- Owner: scenery runtime / edge
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0114 Supervised Agent Lifecycle](0114-supervised-agent-lifecycle.md)

Made launchd the availability owner for the local agent, exposed supervision
truth through deploy status, and added cooperative restarts plus bounded edge
retry behavior.

## Dev Loop Performance

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0096 Dev Loop Performance](0096-dev-loop-performance.md)

Reduced fixture-app readiness to 3.12 seconds cold and 0.42 seconds warm through
snapshot reuse, compile fast paths, parallel startup, and tighter probes.

## Agent-First Development Control Plane

- Status: completed
- Owner: scenery maintainers / agent DX
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0064 Agent-First Development Control Plane](0064-agent-first-development-control-plane.md)

Aligned the repository's knowledge, plan, review, and drift contracts. The
knowledge harness now enforces the active ExecPlan index bidirectionally.

## Frozen Toolchain Manifest and Managed Tool Store

- Status: completed
- Owner: scenery runtime / release tooling / agent DX
- Completed: 2026-07-22
- Quality: B
- ExecPlan: [0059 Frozen Toolchain Manifest and Managed Tool Store](0059-frozen-toolchain-manifest.md)

Shipped the singular frozen toolchain manifest, deterministic managed tool
store, strict integrity verification, and toolchain inspection commands without
ambient PATH fallback.

## Table Page Adoption Prerequisites

- Status: completed
- Owner: scenery compiler / generation / ui catalog
- Completed: 2026-07-21
- Quality: B
- ExecPlan: [0131 Table Page Adoption Prerequisites: Response-Aware Slots, Page Pagination, Row Actions](0131-table-page-adoption-prerequisites.md)

Shipped response-aware table slots, explicit page-number pagination and query
mapping for binding sources, fixed typed source predicates, app-owned row
actions, and source-less static content pages. A generated Micro In Service
candidate proved backend totals, sorting, search, fixed EDGE/In Service
predicates, numeric pagination, footer context, and project-detail activation;
a generated Testing candidate proved static content without a dummy operation.

## Astryx-Native UI Catalog

- Status: completed
- Owner: scenery ui catalog
- Completed: 2026-07-21
- Quality: B
- ExecPlan: [0130 Astryx-Native Catalog: Table Migration and Hand-Rolled Component Elimination](0130-astryx-native-catalog.md)

Migrated the catalog table, statistics, empty state, top bar, page layout, and
split layout onto Astryx primitives while retaining the existing public APIs.
Native table grouping and opt-in row numbering replace hand-rolled table
semantics; the resizable detail sheet now overlays the full-width table. A
self-harness architecture guard rejects raw interactive catalog HTML except the
documented bespoke `FilterPills` pattern.

## Table Grouping and Resizable Detail Panels

- Status: completed
- Owner: scenery ui catalog / compiler / generation
- Completed: 2026-07-21
- Quality: B
- ExecPlan: [0129 Table Grouping and Resizable Detail Panel for `table_page`](0129-table-grouping-and-detail-panel.md)

Shipped complete-list grouping as an optional mode of the existing Table
template: ordered collapsible sections with counts, a runtime None/group
selector, and compile-time rejection on paginated sources. Row details now have
explicit inline or bounded resizable-panel presentation, with generated adapter
wiring and live Micro work-orders acceptance preserving the full workbench.

## Workbench Table Pages

- Status: completed
- Owner: scenery spec / compiler / generation / ui catalog
- Completed: 2026-07-20
- Quality: B
- ExecPlan: [0128 Workbench Table Pages](0128-workbench-table-pages.md)

Shipped reusable status maps, stats, declarative filter/sort controls,
expandable typed row details, mutation dialogs, and loaded-result CSV for
generated `table_page` workbenches. Table sources now support both cursor-paged
CRUD lists and unpaginated call-delivery HTTP aggregates with explicit
display/export columns. Micro platform's generated work-orders page passed the
full handwritten feature inventory and now owns `/work-orders`; the temporary
candidate route, shadow CRUD model, and 1,377-line handwritten page are gone.

## Shared Library Linkage

- Status: completed
- Owner: scenery contract / generation / runtime
- Completed: 2026-07-18
- Quality: B
- ExecPlan: [0127 Shared Library Linkage](0127-shared-library-linkage.md)

Shipped declared `pkg/` Go libraries, generated source/shared typed facades and
c-shared export shims, the strict cgo-free `scenery.sh/library` loader,
load-alongside hot swaps, and `scenery build --lib` for the exact
darwin/arm64 plus linux/amd64 matrix. ONLV maps3d is the first adopter, with
House source/shared byte parity, real two-version swapping, measured overhead,
Linux container loading, and app-side repoharness guardrails.

## Fully Generated Client Apps

- Status: completed
- Owner: scenery spec / compiler / generation / ui catalog
- Completed: 2026-07-17
- Quality: B
- ExecPlan: [0126 Fully Generated Client Apps](0126-fully-generated-client-apps.md)

Shipped typed `.scn` page search/navigation metadata, `SCN2619` validation,
generated route descriptors and search validators, one TanStack route-tree
adapter, generated navigation, the router-agnostic catalog `ClientAppShell`,
and fixed auth/top-bar/content/link/icon extension slots. The Micro platform
reference app now mounts every page through `createSceneryApp`; its hand-written
route tree, `AppPages`/`RoutePane`, duplicated generated-page validators,
manual navigation list, and layout shell were deleted and verified live.

## Single @scenery/ui Import Surface

- Status: completed
- Owner: scenery ui catalog / generation / agent DX
- Completed: 2026-07-17
- Quality: B
- ExecPlan: [0125 Single @scenery/ui Import Surface](0125-scenery-ui-single-surface.md)

Shipped the catalog-owned semantic `t` StyleX facade, its sole direct
`@scenery/ui/tokens.stylex` defining-module subpath, curated Astryx primitive
re-exports with component types, embedded and live catalog materialization,
the three-resolver client alias contract, and a real Micro warranty-page
migration proven by production build, current generation, adoption metrics,
and a live Astryx light/dark theme flip.

## UI Guardrails Report

- Status: completed
- Owner: scenery inspect / ui catalog / agent DX
- Completed: 2026-07-17
- Quality: B
- ExecPlan: [0124 UI Guardrails Report](0124-ui-guardrails-report.md)

Shipped the read-only `scenery inspect ui` human/JSON surface, tokenizer-aware
React and StyleX classification, safe hand-authored-source collection,
independent markup/token shares, deterministic slop ranking, exact machine
schema, UI-cleanup workflow docs, and live Micro/platform plus ONLV acceptance.
Check-time baseline enforcement remains a separate deferred decision.

## Composable Page Kinds

- Status: completed
- Owner: scenery generation / ui catalog
- Completed: 2026-07-17
- Quality: B
- ExecPlan: [0123 Composable Page Kinds](0123-composable-page-kinds.md)

Shipped `content_page` as the single-column generated shell, recomposed
`table_page` through the chrome-less Astryx `QueryTable`, unified catalog
request state, removed the standalone table page and parallel CSS theme, and
fixed generated JSX escaping, split-page history synchronization, unexpected
error rendering, and UTF-8 labels.

## Declarative Table Pages

- Status: completed
- Owner: scenery compiler / generation / data runtime
- Completed: 2026-07-16
- Quality: B
- ExecPlan: [0120 Declarative Table Pages](0120-declarative-table-pages.md)

Shipped explicit CRUD list capabilities with fingerprint-bound keyset cursors,
declarative table-page expansion, a binary-owned React catalog, exact typed
slot overrides, transactional React generation, and fail-closed verification
by a checksummed Scenery-managed native TypeScript checker.

## Cache-Only Generated Go Artifacts

- Status: completed
- Owner: scenery generate / build / evolution
- Completed: 2026-07-14
- Quality: B
- ExecPlan: [0113 Cache-Only Generated Go Artifacts](0113-cache-only-generated-go.md)

Shipped external build/editor rendering with stable import identities,
ownership-verified root Go workspaces, descriptor-authenticated legacy prune,
explicit published-module materialization, authored-only workspace revisions,
revision-rebind evidence, TypeScript source/cache modes, and full ONLV
check/test/build/detached-runtime acceptance with zero regenerated Go trees.

## Google Connections and Gmail Platform

- Status: completed
- Owner: scenery auth / ONLV integration
- Completed: 2026-07-14
- Quality: B
- ExecPlan: [0099 Google Connections and Gmail Platform](0099-google-connections-and-gmail.md)

Shipped encrypted per-user offline Google connections, cross-process refresh serialization, explicit reconnect/scope failures, typed token access, and real ONLV Gmail API acceptance. Optional proactive worker refresh remains a downstream product decision.

## Symphony Hardening

- Status: completed
- Owner: scenery dashboard / agent DX
- Completed: 2026-07-03
- Quality: B
- ExecPlan: [0095 Symphony Hardening](0095-symphony-hardening.md)

Shipped local-trust auto-mode gating, run leases and stale recovery, terminal timeout/stall routing, distinct attempt limits, race fixes, and workspace reset/cleanup.

## Local Filesystem Storage and ZeroFS Removal

- Status: completed
- Owner: scenery runtime / storage
- Completed: 2026-07-02
- Quality: B
- ExecPlan: [0094 Local Filesystem Storage Promotion and Complete ZeroFS Removal](0094-local-storage-and-zerofs-removal.md)

Promoted the checked-fsync local backend for app and headless runtimes and removed the ZeroFS adapter, process, toolchain artifact, dependency, configuration, and operational surfaces.

## Local Path Routing

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-27
- Quality: B
- ExecPlan: [0090 Local Path Routing and Per-Runtime Dev Ports](0090-local-path-routing.md)

Made one leased localhost base URL plus path routes the default per-runtime dev surface, with route manifests for agents and optional host-mode routing retained.

## Victoria Shared Substrate Visibility

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0079 Victoria Shared Substrate Visibility](0079-victoria-shared-substrate-visibility.md)

Kept Victoria shared through the existing agent substrate registry, exposed reuse and ownership through CLI state, and retained the simple no-agent fallback without a new OS service.

## Database Lifecycle Split

- Status: completed
- Owner: scenery runtime / ONLV integration
- Completed: 2026-07-06
- Quality: B
- ExecPlan: [0063 Database Lifecycle Split](0063-db-lifecycle-split.md)

Split database apply, seed, and setup from SQLC source generation, added fail-closed seed safety and setup fingerprinting, and completed ONLV migration through plan 0097's Postgres acceptance.

## Snapshot Save/Load

- Status: completed
- Owner: scenery runtime / data platform
- Completed: 2026-07-13
- Quality: B
- ExecPlan: [0100 Snapshot Save/Load](0100-snapshot-save-load.md)

Shipped:

- Added explicit `scenery snapshot save|load` for one portable Postgres-plus-storage zip.
- Added complete SHA-256 preflight, strict current manifest identity, container-matched Postgres tools, atomic archive replacement, and recoverable database/storage overwrite.
- Added atomic database merge, storage conflict policies, dry-run, JSON schemas, docs, focused corruption/interruption tests, and a live Docker round-trip probe.

Validation:

- Passed focused and full command tests, schema validation, architecture checks, and live database-plus-storage self-harness proof.

## Single Current Identity Correctness

- Status: completed
- Owner: scenery compiler / graph / machine / specification
- Completed: 2026-07-13
- Quality: B
- ExecPlan: [0112 Single Current Identity Correctness](0112-single-current-identity-correctness.md)

Shipped:

- Restored production source-transaction recovery/rejection below compiler and evolution, with direct compiler regression coverage.
- Added exact raw-manifest/current-envelope decoding with shared producer, catalog, resource, ordering, and recomputed-revision validation.
- Made implementation verification status explicit across CLI check, build, and staged evolution validation.
- Bound CLI and event envelope identities to their complete checked JSON Schemas and rejected invalid revision-value wire shapes.
- Expanded `spec_revision` across structural grammar and explicit behavioral revisions, separated diagnostic prose identity, and returned deep copies from catalog accessors.

Validation:

- Passed full Scenery tests, vet, docs/schema/architecture checks, and self-harness; regenerated all revision-bound fixtures.
- Refreshed ONLV provider locks and generated artifacts, then passed current-source check with a valid native implementation, full Go tests, and the app harness.

## Single Current Scenery Specification

- Status: completed
- Owner: scenery language / runtime / CLI / agent DX
- Completed: 2026-07-13
- Quality: B
- ExecPlan: [0111 Single Current Scenery Specification](0111-single-current-scenery-specification.md)

Shipped:

- Removed edition, profile, first-party schema-version, package/provider semver, historical upgrade-target, and temporary refresh-cookie selectors without replacement compatibility dispatchers.
- Established one current unversioned catalog and machine protocol with exact schema, catalog, artifact, lock, and ABI revisions; made those semantic identities stable across source and built binaries.
- Split the former current `internal/vnext` implementation into responsibility packages, moved the normative spec to `docs/spec`, and moved the sole dashboard to `apps/console` at `/console/`.
- Added explicit idempotent migrations for durable identity-bearing state, safe refusal for interrupted legacy recovery state, and architecture/residue guardrails against reintroducing removed lanes.
- Migrated ONLV to the unversioned generated client and validated its current-source runtime path.

Validation:

- Passed full Scenery self-harness, Go tests and vet, schema and architecture checks, fixture generation/checks, dashboard and TypeScript validation, PostgreSQL/storage/parallel-runtime probes, ONLV application validation, and live current-source ONLV runtime acceptance.

## Bounded Refresh-Cookie Compatibility

- Status: completed by supersession
- Owner: scenery auth / runtime
- Completed: 2026-07-13
- Quality: B
- ExecPlan: [0110 Bounded Refresh-Cookie Compatibility](0110-bounded-refresh-cookie-compatibility.md)

Shipped:

- Temporarily preserved sessions under the former fixed cookie name, then removed that read/clear compatibility under plan 0111's one-current-contract flag day.
- Standard auth now reads, issues, and clears only `scenery_refresh`; browser sessions carrying only the former name must log in again.

Validation:

- Focused auth, config, and runtime tests cover canonical cookie selection, issuance, clearing, strict config rejection, and repeated response-header transport.

## Post-v0 Legacy Lane Removal

- Status: completed
- Owner: scenery maintainers / runtime / CLI / agent routing
- Completed: 2026-07-12
- Quality: B
- ExecPlan: [0109 Remove Post-v0 Legacy Lanes](0109-remove-post-v0-legacy-lanes.md)

Shipped:

- Moved standard auth onto typed contract codecs and deleted reflection request decoding, response encoding, endpoint metadata, and internal-call compatibility APIs.
- Removed config-defined shell tasks, old generate sniffing, rejected legacy flags, duplicate routes, unused wrappers and PostgreSQL fields, dashboard compatibility metadata, and orphan dependencies and fixtures.
- Standardized auth on canonical environment names and the `scenery_refresh` cookie, simplified code generation, and synchronized docs, schemas, generated fixtures, and agent guidance.
- Plan 0110's temporary fixed-cookie compatibility was removed by plan 0111; standard auth reads, issues, and clears only `scenery_refresh`. Configurable cookie/env naming remains removed.
- Removed migration-only `dev.setup`, configurable app database URL env naming, Symphony's missing-base-ID fallback, the superseded database-only snapshot command, and non-output short CLI aliases.

Validation:

- Passed focused and full Go tests, Go vet, tracked-source searches, docs inspection, schema validation, UI validation, fixture/runtime/Postgres/storage probes, and full self-harness; advisory review-date, file-size, and timing warnings remain.

## Remaining v0 Compatibility Removal

- Status: completed
- Owner: scenery maintainers / CLI / runtime / auth
- Completed: 2026-07-12
- Quality: B
- ExecPlan: [0108 Remove Remaining v0 Compatibility](0108-remove-remaining-v0-compatibility.md)

Shipped:

- Made `-o json|jsonl` and `scenery.cli.v1` / `scenery.cli.event.v1` the singular machine-output contract and removed `scenery.cli.v0`, `--api-version`, and command-local `--json` / `--jsonl` aliases.
- Removed `.config.json`, public `et` and middleware shims, runtime mocks and unchecked registration wrappers, implicit secret reflection, legacy auth environment fallbacks and callback alias, rejected dev-service fields, and no-op devdash observability persistence.
- Updated current docs, schemas, fixtures, harness consumers, workflows, templates, scripts, and generated-contract checks without adding replacement compatibility layers.

Validation:

- Passed focused and uncached full Go tests, Go vet, tracked-source contract searches, docs inspection, schema validation, TypeScript conformance and typechecking, UI validation, fixture/runtime/Postgres probes, and full self-harness with no errors; advisory review-date, file-size, and timing warnings remain.

## Go-Directive Frontend Removal

- Status: completed
- Owner: scenery compiler / runtime / agent interfaces
- Completed: 2026-07-12
- Quality: B
- ExecPlan: [0107 Remove the Go-Directive Frontend](0107-remove-go-directive-frontend.md)

Shipped:

- Deleted all `//scenery:*` parsing and directive-owned IR, code generation, inspection, models, pages, durable tasks, cron jobs, schemas, fixtures, and public DSL packages.
- Deleted the mixed legacy bridge, migration manifest/compiler/CLI, compatibility schemas, generated bridge artifacts, and bounded legacy TypeScript selection path.
- Made edition-2027 `.scn` source the singular application model across build, check, generate, inspect, dev, worker, task, and test flows while retaining independent `.scenery.json` runtime configuration and CLI JSON wire contracts.
- Converted the supported basic and storage fixtures to generated native composition and synchronized current docs, specifications, schemas, indexes, and agent guidance.

Validation:

- Passed the uncached Go suite, Go vet, native byte-stable generation, TypeScript conformance and typechecking, docs inspection, schema validation, dashboard checks, fixture/runtime/Postgres/storage probes, and full self-harness with no errors; advisory review, file-size, and timing warnings remain.

## HTTP Path-Tail Profile

- Status: completed
- Owner: scenery compiler / HTTP runtime / migration
- Completed: 2026-07-12
- Quality: B
- ExecPlan: [0106 HTTP Path-Tail Profile](0106-http-path-tail-profile.md)

Shipped:

- Added explicit `scenery.http-path-tail/v1` and `scenery.runtime-http-path-tail/v1` profiles with terminal `{name...}` syntax, exact typed mappings, canonical effective metadata, revisions, and profile advertisement.
- Added deterministic literal/parameter/exact/tail precedence, zero-or-more strict routing, compiler and startup conflict checks, one-time segment decoding, typed Go input construction, no-fallback behavior, and CORS ownership.
- Added independently encoded TypeScript segments, path-tail descriptor identities, an honest OpenAPI vendor extension, compatibility/security consequences, and profile-gated Drive-shaped GET/DELETE legacy candidate lowering.
- Kept generic legacy wildcard behavior distinct: unsupported/raw facets retain `SCN5401`, while `SCN5405` makes slash/selection/decoding comparison explicit and the existing risk gate prevents advisory evidence from being called verified equality.

Validation:

- Passed the uncached full Go suite, focused compiler/runtime/migration tests, byte-stable House/native generation, sixteen TypeScript codec tests, generated-client typechecking, docs inspection, and self-harness with zero errors and advisory review, generated-file-size, and timing warnings only.

## Edition-2027 Transaction and Migration Conformance

- Status: completed
- Owner: scenery compiler / runtime / agent interfaces
- Completed: 2026-07-11
- Quality: B
- ExecPlan: [0105 Edition-2027 Transaction and Migration Conformance](0105-edition-2027-transaction-and-migration-conformance.md)

Shipped:

- Authenticated exact app-local issuance for change, deployment, and every migration plan family before apply trusts expiry, approvals, operations, edits, or provider actions; strict single-value plan decoding rejects unknown fields, and approval-bearing migration transitions now retain and apply one exact plan.
- Made legacy migration evidence truthful across static, behavioral, and operational dimensions; service status aggregates typed construct evidence, cutover classes include both candidates, and candidate validation preserves every other active owner while detecting route, durable, schedule, and event identities.
- Completed mixed-app rename lineage, config-free bounded legacy TypeScript generation from canonical resources, default-before-patch ordering, exact package-input provenance for generated Go config schemas, and near-linear Unicode source positions.
- Added public-boundary regressions for caller-tampered plans, apply grammar/non-mutation, advisory activation, cross-owner references and collisions, migration/module rename, config-free generation/planning, exact provenance, and source scaling.

Validation:

- Passed uncached full Go tests, fifteen TypeScript codec tests, generated-client typechecking, byte-identical House/native/bridge generation, docs and schema inspection, and the default self-harness with no failing steps.
- Repeated independent standards and specification reviews until both reported no actionable findings.

## Edition-2027 Conformance Hardening

- Status: completed
- Owner: scenery compiler / runtime / agent interfaces
- Completed: 2026-07-11
- Quality: B
- ExecPlan: [0104 Edition-2027 Conformance Hardening](0104-edition-2027-conformance-hardening.md)

Shipped:

- Closed all seven findings from the corrected review of `1dcba053`: source-level typed Go service config, mixed native/bridge operation handlers, schema-driven structured mutation, recursive agent schemas, a checked diagnostic catalog, collision-resistant Unicode-correct source maps, and exact scalar normalization.
- Closed all follow-up findings from the exact review of `9dbc245f`: truthful source/effective/expanded graphs with RFC 6901 field provenance and complete schema defaults, nested/composite semantic rename with durable validated receipts, wire-label authoring parity, static receiver-aware mixed bridge verification, strict datetime lexing, normalized plan identities, constrained Go config aliases, known unsupported extension syntax, and mechanically checked normative summaries.
- Added explicit Appendix E capability rejection, clarified hierarchical network URL semantics, and kept public claims at feature-complete draft / conformance hardening rather than stable.
- Closed the final two-axis review findings by splitting migration status from lowering, consolidating field metadata, requiring complete binding authoring metadata, and exposing ordered composite idempotency keys through schema discovery and mutation.

Validation:

- Passed full Go, zero-drift house/native/bridge generation, fifteen TypeScript codec tests, generated-client typechecking, docs/schema, and self-harness gates. Final self-harness reported `ok: true`, 34/34 schemas valid, no failing steps, and no timing or drift findings; both final review axes reported no remaining actionable findings.

## Scenery vNext Language and ONLV House Migration

- Status: completed
- Owner: scenery compiler / runtime / ONLV integration
- Completed: 2026-07-11
- Quality: B
- ExecPlan: [0103 Scenery vNext Language and ONLV House Migration](0103-vnext-language-and-onlv-house-migration.md)

This is the historical first integrated delivery, not a stable-conformance claim. A corrected independent review of its exact commit found seven remaining gaps; [0104 Edition-2027 Conformance Hardening](0104-edition-2027-conformance-hardening.md) closed them.

Shipped:

- Implemented the broad feature-complete draft edition-2027 surface from the six normative specifications: compiler/graph/revisions, compatibility, immutable changes and deployments, HTTP and TypeScript codecs, generated Go/application composition, durable/events/data/UI profiles, agent operations, and the bounded legacy bridge.
- Migrated all eighteen ONLV House HTTP operations and both House durable workers to native generated ABIs, retired every House legacy adapter and hidden runtime registration, and preserved forty-three explicit non-House legacy service owners in one mixed graph/runtime.
- Added exact generated Go/TypeScript/client-selection artifacts, strict schemas and fixture coverage, clean-clone retired-service readiness, and a readiness waiter that accommodates real application setup without canceling the detached child.

Validation:

- Passed full Go, focused CLI/vNext/runtime, Bun codec, generated TypeScript, docs, 34-schema, ten-fixture, Postgres/runtime, ONLV repo/app/browser, ownership, and fresh detached authenticated House HTTP proof gates.
- Completed the then-current standards, specification, maintainability, and simplification reviews; the later exact-SHA conformance review and its remaining findings are recorded in plan 0104.

## Ponytail Cleanup

- Status: completed
- Owner: scenery maintainers
- Completed: 2026-07-09
- Quality: B
- ExecPlan: [0102 Ponytail Cleanup](0102-ponytail-cleanup.md)

Shipped:

- Retired the obsolete runnable `ui/` dashboard and three direct dependencies while preserving the reusable `@scenery` registry; ConsoleNext is now the sole dashboard source.
- Consolidated all 44 audited command parsers on a scoped Go `flag.FlagSet` adapter without removing documented positional or pass-through grammar.
- Removed legacy GraphQL and compatibility RPC paths, MemoryStore, ErrDetails, and every dead-code finding under `cmd/scenery` and `internal`.

Validation:

- Passed `go test ./...`, ConsoleNext lint/typecheck/build, registry typecheck/tests, dashboard embed rebuild, docs inspection, dead-code and removal checks, self-harness, and the full fixture browser UI harness.

## Google Social Login

- Status: completed
- Owner: scenery auth / ONLV integration
- Completed: 2026-07-07
- Quality: B
- ExecPlan: [0098 Google Social Login](0098-google-social-login.md)

Shipped:

- Made Google standard-auth login opt-in through `auth.google_oauth.enabled`; disabled apps expose no `/auth/google/*` runtime routes, inspect endpoints, or generated TypeScript client methods.
- Added Google OAuth hardening: cached JWKS with one forced refresh on unknown `kid`, browser-friendly callback error redirects, and expired OAuth state cleanup.
- Added fake-Google live-Postgres coverage for the browser sign-in round trip, `/auth/me` bootstrap from the issued session, verified email account linking, unverified account refusal, replay rejection, nonce mismatch, and `email_verified=false`.
- Added docs, environment registry entries, ONLV setup notes, and `scenery check` warnings for missing Google OAuth credentials.

Validation:

- Passed `go test ./auth ./cmd/scenery`, `go test ./...`, the live fake-Google flow test with `SCENERY_TEST_DATABASE_URL`, enabled/disabled inspect/client/runtime probes, and `scenery harness self --summary --write` with warning-class findings only.

## Postgres-Only Data Platform

- Status: completed
- Owner: scenery runtime / database
- Completed: 2026-07-06
- Quality: B
- ExecPlan: [0097 Postgres-Only Data Platform](0097-postgres-only-data-platform.md)

Shipped:

- Removed SQLite entirely; Postgres 18 is the only database engine.
- One managed database per app root/worktree on the shared Docker server, one schema per service, scenery-native tables (auth, durable execution, seed ledger) in the `scenery` schema, external `DATABASE_URL` precedence.
- Single shared durable job store with `FOR UPDATE SKIP LOCKED` leasing; Postgres-only DB CLI (`scenery.db.list.v3`); `db path`/`db branch` removed; symphony store and dashboard DB explorer on Postgres.
- Migrated the onlv client app end to end.

## Symphony Dashboard

- Status: completed
- Owner: scenery dashboard / agent DX
- Completed: 2026-07-02
- Quality: B
- ExecPlan: [0092 Symphony Dashboard](0092-symphony-dashboard.md)

Shipped:

- Replaced the consolenext `Observability` page with `Symphony`.
- Added app-scoped SQLite board storage under `<dashboard-cache-root>/symphony.sqlite`, keyed by stable base app ID for agent sessions and direct dashboard app ID when no session id exists.
- Added dashboard RPC methods for board state, task CRUD, task movement, status visibility, and workflow config.
- Added a responsive Kanban board with visible and hidden columns, task cards, create/edit modal, explicit refresh, and reload persistence.
- Added consolenext-specific browser-harness markers for the Symphony page.

Not shipped:

- Process-starting `symphony/run/*` RPCs; they remain blocked until an authenticated runner channel exists.

Validation:

- Passed focused Symphony store/RPC/harness tests, `go test ./cmd/scenery`, `go test ./...`, consolenext lint/typecheck/build, dashboard embed rebuild, harness UI fixture validation, and Chrome fixture validation.

## First-Class Postgres Service Databases on a Shared Dev Server

- Status: completed
- Owner: scenery runtime / database
- Completed: 2026-07-02
- Quality: B
- ExecPlan: [0093 First-Class Postgres Service Databases on a Shared Dev Server](0093-postgres-service-databases.md)

Shipped:

- Added `dev.services.<name>.kind: "postgres"` with external-DSN precedence and managed shared-server fallback for `scenery up`.
- Added `internal/postgresdb`, pgx database/sql dispatch in `scenery.sh/db`, deterministic per-worktree database names, env registry encoding, admin reset/drop/list helpers, and Postgres seed ledgers.
- Added a Docker-backed shared Postgres 18 dev server, digest-pinned toolchain artifact, agent substrate registration, doctor readiness, `scenery db server`, DB CLI list/shell/reset/drop/snapshot/seed support, and headless fail-closed DSN validation.
- Kept SQLite behavior intact, including file paths, snapshots, branches, branch worktrees, and the single-service `DatabaseURL` alias rule generalized across engines.
- Added no-Docker unit coverage, fixture config, self-harness Postgres probe with explicit Docker skip, schemas, environment registry entries, README/guide/skill/cookbook/local-contract updates, and 0088 closure.

Validation:

- Focused tests passed during implementation: `go test ./cmd/scenery ./internal/app ./internal/postgresdb ./db ./internal/toolchain`.
- Final validation passed: `go test ./...`, `go test ./cmd/scenery`, and `go run ./cmd/scenery harness self --summary --write`. Self-harness reported `pass_with_warnings` and `can_proceed: true`; Docker was unavailable, so the live Postgres probe recorded an explicit skip warning.

## SQLite Durable Execution Runtime Per Service

- Status: completed
- Owner: scenery runtime / durable execution
- Completed: 2026-06-27
- Quality: B
- ExecPlan: [0089 SQLite Durable Execution Runtime Per Service](0089-sqlite-durable-execution-runtime.md)

Shipped:

- Added `scenery.sh/durable` task declarations, `durable.Start`, `durable.Schedule`, `durable.Step`, and `durable.Signal`.
- Created one service-owned durable SQLite DB per declaring service at `.scenery/state/db/<service>.durable.sqlite`, with durable table-name invariants and no NATS dependency.
- Added local worker leasing/execution, retries, interval schedules, step reuse, signals, job events, and job admin.
- Added authenticated remote durable worker lease, heartbeat, complete, and fail endpoints plus `scenery worker durable` and worker-token CLI support.
- Added `scenery inspect durable --json`, durable job/token schemas, docs, environment registry entries, and migration guidance away from legacy async runtime-backed durable work.

Validation:

- Passed focused durable/store/runtime/parser/cmd tests, `go test ./...`, docs inspection, `git diff --check`, and `go run ./cmd/scenery harness self --summary --write` with warning-class harness findings only.

## ZeroFS Production Readiness

- Status: completed
- Owner: scenery runtime / storage
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0080 ZeroFS Production Readiness](0080-zerofs-production-readiness.md)

Shipped:

- Promoted the app-facing `scenery.sh/storage` API, reserved runtime routes, and generated browser helpers for production only when headless runtimes receive explicit operator-provided proxy-backed `SCENERY_STORAGE_CONFIG`.
- Kept managed ZeroFS, storage CLI/status/Web UI, and inspect lease surfaces beta local-dev/operator tooling because the pinned ZeroFS artifact is AGPL-licensed and P9 fsync is unsafe with the current backend.
- Added tenant scoping, durable object metadata, atomic `IfNoneMatch` writes, range metadata fixes, headless fail-closed behavior, restart proof, lease-aware cleanup, and CLI import/export/rollback proof.

Validation:

- Passed focused storage/headless/docs checks, `go test ./...`, and `go run ./cmd/scenery harness self --summary --write` with warning-class harness findings only.

## Devdash Control-Plane Store Slimming

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-23
- Quality: B
- ExecPlan: [0076 Devdash Control-Plane Store Slimming](0076-devdash-control-plane-store-slimming.md)

Shipped:

- Removed trace summaries, trace events, and report log events from the persisted dashboard store.
- Cut dashboard and CLI trace/metric reads over to Victoria-backed query paths.
- Kept report ingestion store-free for observability event data.
- Enforced a compact `devdash.json` byte budget, with app-model sidecar blobs covering large metadata/API-encoding payloads.
- Routed agent-backed `scenery up` app/session/process mutations through the agent dashboard control-plane endpoint so the agent dashboard process owns global store writes.

Validation:

- `go test ./cmd/scenery ./internal/devdash`
- `go test ./...`
- `scenery harness self --summary --write` passed with warnings only.

## Devdash App-Model Blob Store

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-23
- Quality: B
- ExecPlan: [0078 Devdash App-Model Blob Store](0078-devdash-app-model-blob-store.md)

Shipped:

- Split persisted devdash app/session records from the public `AppRecord` compatibility shape.
- Moved large app-model `Metadata` and `APIEncoding` payloads into content-addressed sidecar blobs under the dashboard cache root.
- Migrated legacy fat `apps` and `app_sessions` records on load/save.
- Kept list paths compact while hydrating detail/status paths.
- Added serialized-size budget enforcement with a 2 MB soft target, 8 MB hard cap, byte-aware pruning, and top-level size diagnostics.

Validation:

- `go test ./internal/devdash`
- `go test ./cmd/scenery`
- `go test ./...`
- `.scenery/harness/bin/scenery harness self --summary --write` passed with warnings only.

## Static Model/View IR

- Status: completed
- Owner: scenery app model / generators
- Completed: 2026-06-13
- Quality: B
- ExecPlan: [0077 Static Model/View IR](0077-static-model-view-ir.md)

Shipped:

- Added beta `scenery.sh/model` and `scenery.sh/page` static IR vocabulary, parser diagnostics, and `scenery inspect models|views --json`.
- Added generated desired Atlas HCL, `scenery generate data --dry-run --json`, `scenery db diff --generated --json`, generated seed SQL, and `scenery check` drift diagnostics.
- Added generated model CRUD endpoints/stores with explicit action policy, generated endpoint markers, app-owned schema/table targeting, auth-by-default access, tenant scoping, UUID tenant support, configured DB URL env support, shared pgx pools, and bounded list pagination.
- Added generated hidden frontend packages with row/create/patch types, entity source metadata, collection/materializer definitions, runtime adapter factories, route registration helpers, default collection pages, slot assertions, and fixture typecheck/render proof.
- Closed production-readiness follow-ups discovered by the ONLV pilot: app-owned schemas, safe route bases, Atlas label collisions, access defaults, UUID tenants, database URL env selection, reserved route diagnostics, timestamp payloads, shared pools, and bounded list results.

Validation:

- Merged PRs #127, #131, #132, #133, #134, #135, #136, #140, #142, #150, #151, #152, #153, #154, #155, and #156 carried focused tests, full Go tests, lint, self-harness, and release-gate proof as appropriate.
- `testdata/apps/model-dsl` exercises generated schema/seed/backend/frontend contracts.
- The ONLV `tasksnew` pilot in https://github.com/pbrazdil/onlv/issues/95 proved the generated model/page stack in a production app and fed discovered Scenery gaps back into the closed follow-up issues.

## Page Record Projection IR

- Status: completed
- Owner: scenery app model / generators
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0081 Page Record Projection IR](0081-page-record-projection-ir.md)

Shipped:

- Added internal page-record projection IR for collection views.
- `scenery inspect views --json` now exposes each view projection record type, source row, and projected fields.
- Generated web packages now include `projections.ts`; generated collections/runtime/routes use page record types while entity sources keep storage row types.
- Computed page columns now fail with a deterministic diagnostic instead of being silently skipped.

## Generated Page Mount Surface

- Status: completed
- Owner: scenery app model / generators
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0083 Generated Page Mount Surface](0083-generated-page-mount-surface.md)

Shipped:

- Confirmed the generated `TaskListPage` component is the minimal mount surface.
- Kept generated pages exported through the existing `index.ts` barrel and host alias such as `@scenery/generated`.
- Added tests proving generated pages consume `TaskListRecord` page records and type against layout-kit through `createCollectionPage`.

Validation:

- Passed focused generated-web tests, generated data dry-run, and model DSL web typecheck/render proof.

## Simple Entity Page Pilot

- Status: completed
- Owner: scenery app model / ONLV integration
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0084 Simple Entity Page Pilot](0084-simple-entity-page-pilot.md)

Shipped:

- Used `testdata/apps/model-dsl` as the self-contained simple entity pilot.
- Host fixture code imports `TaskListPage` and `TaskListRecord` through `@scenery/generated`.
- Host fixture mounts the generated page with page records, without handwritten raw `TaskRow` usage.
- Added the generated-page adoption recipe to agent-facing docs.

Validation:

- Passed Scenery-side Go checks, generated data dry-run, docs inspection, and model DSL web typecheck/render proof.

## Tasks Read-Only Stress Test

- Status: completed
- Owner: scenery app model / generators
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0085 Tasks Read-Only Stress Test](0085-tasks-readonly-stress-test.md)

Shipped:

- Added static page filter, sort, and column display metadata to the beta `scenery.sh/page` DSL.
- Exposed `column_displays`, `filters`, and `sorts` through `scenery inspect views --json` and its schema.
- Generated collection materializers now apply static source-row filters/sorts before producing page records.
- Upgraded `testdata/apps/model-dsl` into a realistic read-only Tasks stress fixture with status, priority, assignee, due/created/updated dates, static open-task filtering, default ordering, and host render proof.

Validation:

- Passed focused parser/webgen/inspect/generated-data checks and model DSL web typecheck/build/render proof; full-repo validation is recorded in the ExecPlan.

## Existing Table Model Bindings

- Status: completed
- Owner: scenery app model / generators
- Completed: 2026-06-26
- Quality: B
- ExecPlan: [0086 Existing Table Model Bindings](0086-existing-table-model-bindings.md)

Shipped:

- Added `model.ExistingTable(schema, table)` as the explicit existing physical table binding API.
- Added entity source ownership metadata to the model IR and `scenery inspect models --json`.
- Kept `model.Table(name)` backward-compatible as a generated Scenery-owned table in the service schema.
- Made generated schema and seed output skip existing-table entities while generated read-only endpoints, entity source metadata, projections, collections, pages, routes, and barrel exports continue to target the explicit schema-qualified table.
- Added `testdata/apps/existing-table-dsl` with a handwritten `legacy.customers` schema source and read-only generated Customer page proof.

Validation:

- Focused parser, schemagen, inspect, generated data, and existing-table fixture checks passed during implementation; full-repo validation is recorded in the ExecPlan.

## Scenery Storage

- Status: completed
- Owner: scenery runtime / storage
- Completed: 2026-06-22
- Quality: B
- ExecPlan: [0078 Scenery Storage](0078-scenery-storage.md)

Shipped:

- Added beta `.scenery.json` storage declarations, `scenery.sh/storage`, runtime env injection, reserved storage HTTP routes, generated TypeScript `client.storage` helpers, `scenery inspect storage --json`, and `scenery storage status|webui|ls|stat|put|get|rm --json`.
- Added managed ZeroFS dev-service planning, startup, attach/reuse, lease accounting, protected Web UI routing, session-local storage proxying, and a pure-Go 9P data-plane adapter so target app packages do not link ZeroFS FFI.
- Made storage cells worktree-shared by default, with durable cell data under the agent storage root and short temp Unix socket paths for macOS 9P/RPC socket limits.
- Added `testdata/apps/storage-basic`, fixture probes, two-worktree visibility proof, real ZeroFS 1.2.5 self-harness proof when `SCENERY_DEV_ZEROFS_BIN` is available, schemas, docs, environment registry entries, and installable skill guidance.
- Proved the ONLV replacement path: ONLV declares storage cell `onlv`, backend file helpers and job/map artifacts use `scenery.sh/storage`, and Pulse Drive/viewer/contact/annotation write/list/delete paths use generated `client.storage` helpers.

Validation:

- Scenery passed `go test ./...`, `go test ./cmd/scenery`, `go test ./cmd/scenery ./storage ./internal/storage -count=1`, and `SCENERY_DEV_ZEROFS_BIN=.scenery/harness/zerofs-bin/zerofs go run ./cmd/scenery harness self --summary --write` with only existing warning-class architecture/timing findings.
- ONLV passed local-Scenery `scenery check --json`, `go test ./drive ./maps ./jobs -count=1`, `go test ./...`, `(cd apps/pulse && bun run typecheck)`, `(cd apps/pulse && bun run lint)`, `scenery harness --json --write`, `scenery inspect storage --json`, and an isolated `scenery storage put|get` byte-for-byte smoke. `just repo-harness` was attempted but blocked by pre-existing untracked rooftopology conversation dumps with broken markdown links; those user files were left untouched.

## Postgres-Only Managed Branching

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-10
- Quality: B
- ExecPlan: [0074 Postgres-Only Managed Branching](0074-postgres-only-managed-branching.md)

Shipped:

- Removed Neon as an active Scenery database substrate, including lifecycle commands, schemas, selfhost driver/runtime code, image/toolchain refs, fixtures, and active docs guidance.
- Added local PostgreSQL 18 managed branch databases through provider-neutral branch commands and `scenery db postgres install|start|status|logs|stop|restart|uninstall`.
- Added Postgres registry v2 under the agent Postgres state root, with endpoint metadata instead of persisted raw connection URLs.
- Preserved app-facing `DatabaseURL`, `SCENERY_MANAGED_DATABASE_URL`, `SCENERY_MANAGED_DATABASE_NAME`, DB lifecycle, auth, worktree, and session behavior.
- Implemented the phase-one `template_database` branch strategy with checkout, reset, delete, expire, prune, restore-as-template-reset, and schema-catalog diff.
- Recorded `dump_restore`, `cluster_basebackup`, PITR, filesystem snapshots, and deeper data diff as explicit future strategies that fail closed until implemented.

Validation:

- Focused `cmd/scenery`, `internal/app`, and `internal/toolchain` tests passed during implementation.
- `go test ./...` passed.
- `scenery inspect docs --json`, `scenery doctor --json`, and `scenery harness self --summary --write` passed, with only existing warning-class findings.
- ONLV passed `scenery check --json`, `go test ./...`, `just repo-harness`, `just db`, a PostgreSQL 18/`uuidv7()` branch SQL smoke, and a restarted `scenery up --json --detach` session whose `/healthy` endpoint returned `{"status":"ok"}`.

## Rebrand to Scenery

- Status: completed
- Owner: scenery maintainers / release tooling / agent DX
- Completed: 2026-06-12
- Quality: B
- ExecPlan: [0075 Rebrand to Scenery](0075-rebrand-scenery.md)

Shipped:

- Renamed the repository, module path, CLI, app model tokens, docs, CI, GoReleaser config, local state paths, and release assets to Scenery.
- Served Go vanity import metadata from `https://scenery.sh?go-get=1`.
- Published `v0.2.1` as the first artifact-bearing Scenery release after the public `v0.2.0` source tag failed before artifact publication.
- Verified `go install scenery.sh/cmd/scenery@v0.2.1` installs a binary reporting `version:"v0.2.1"`.

Validation:

- Main CI and tag CI passed for the release commit.
- Release-mode self-harness passed with `can_proceed:true`.
- GoReleaser published macOS, Linux, and Windows archives for amd64 and arm64 plus checksums.

## Remove Legacy Agent Transport

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-01
- Quality: B
- ExecPlan: [0062 Remove Legacy Agent Transport](0062-remove-legacy-agent-transport.md)

Shipped:

- Removed the obsolete agent transport from runtime startup, generated config, local proxy routes, agent session manifests, dashboard handlers, UI service labels, current docs, schemas, and tests.
- Strict config decoding rejects stale removed-transport keys.
- Self-harness residue checks prevent the removed transport surface from returning in tracked product/source/docs.

Validation:

- See the ExecPlan Outcomes for the full validation set recorded at completion.

## scenery Doctor Command

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-01
- Quality: B
- ExecPlan: [0060 scenery Doctor Command](0060-scenery-doctor-command.md)

Shipped:

- `scenery doctor` and `scenery doctor --json` for read-only host readiness diagnostics.
- OS, CPU, memory, disk, version, Go, optional dependency, and app-sensitive checks.
- JSON schema coverage, docs, README/agent guidance, and focused command tests.

Validation:

- See the ExecPlan Outcomes for focused, full-suite, cross-platform compile, smoke, docs, and self-harness validation recorded at completion.

## Dev Event Backend Cutover and Parity

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-01
- Quality: B
- ExecPlan: [0056 Dev Event Backend Cutover and Parity](0056-dev-event-backend-cutover-and-parity.md)

Shipped:

- VictoriaLogs is the current dev-event read path for logs, attach, TUI, and console.
- Dev-event IDs are assigned before VictoriaLogs export.
- Dashboard/session metadata moved to `devdash.json`.
- The embedded local SQL driver dependency and current-source docs references were removed.

Validation:

- See the ExecPlan Outcomes and Validation sections for focused tests, full Go tests, install, dependency scans, and active-tree residue checks.

## Structured Dev Events and Console

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-05-31
- Quality: B
- ExecPlan: [0055 Structured Dev Events and Console](0055-structured-dev-events-and-console.md)

Shipped:

- Source-aware `scenery.dev.event.v1` records for app output, TypeScript worker output, managed frontends, build phases, supervisor lifecycle, and substrate readiness/status.
- `scenery logs` and `scenery attach` filtering by source, kind, level, grep, and since.
- JSONL structured output plus observing-only `scenery attach --tui`, `scenery console`, grouped errors, and non-TTY fallback.

Validation:

- See the ExecPlan Outcomes for focused tests, full Go tests, install, diff checks, and self-harness evidence recorded at completion.

## Remove Objectstore Functionality

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-30
- Quality: B
- ExecPlan: [0054 Remove Objectstore Functionality](0054-remove-objectstore-functionality.md)

Shipped:

- Removed the beta data/objectstore Go packages, CLI subject, dashboard RPC/UI, registry item, schemas, examples, fixtures, and current docs.
- `scenery inspect data` is gone rather than preserved as a dormant compatibility path.
- Current-source residue checks exclude only historical plan references.

Validation:

- See the ExecPlan Outcomes for Go, UI, install, self-harness, and residue-search validation recorded at completion.

## Harness Self Agent Oracle

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-05-29
- Quality: B
- ExecPlan: [0051 Harness Self Agent Oracle](0051-harness-self-agent-oracle.md)

Shipped:

- Default self-harness runs the full Go suite once, writes oracle artifacts, validates JSON surfaces, and enforces the total Go-suite budget.
- Changed-area, toolchain, drift, timing, fixture matrix, schema-validation, and agent-context artifacts are written under `.scenery/harness/`.
- Package and slow-test timing overages remain warnings for agent guidance.

Validation:

- See the ExecPlan Outcomes for the final oracle behavior and validation evidence.

## Agent Dev Safety Hardening

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-27
- Quality: B
- ExecPlan: [0046 Agent Dev Safety Hardening](0046-prd5-dev-safety-hardening.md)

Shipped:

- Explicit session control, cleanup/prune commands, stronger process ownership checks, and legacy escape-hatch warnings.
- Shared Victoria dashboard wiring and a self-harness parallel-session check for routes, DBs, task queues, logs, traces, frontend routes, and cleanup behavior.

Validation:

- See the ExecPlan Outcomes for the recorded self-harness evidence.

## legacy async runtime Worker Production Hardening

- Status: completed
- Owner: scenery runtime / ONLV integration
- Completed: 2026-05-26
- Quality: B
- ExecPlan: historical plan file removed with the retired async runtime source surface.

Shipped:

- Strict worker task-queue selection, explicit activity queues, compile-time workflow identity, typed workflow operations, local-only worker deployment promotion, cron policy controls, manifest v2 registration hashes, and production legacy async runtime connection validation.
- ONLV deterministic starts, parent workflows for staged flows, workflow-result waits, durable jobs log streaming, explicit legacy async runtime config, and RabbitMQ residue removal.

Validation:

- See the ExecPlan Outcomes for scenery and ONLV validation recorded at completion.

## Neon Selfhost Project-Tenant Mapping

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-09
- Quality: B+
- ExecPlan: [0072 Neon Selfhost Project-Tenant Mapping](0072-neon-project-tenants.md)

Shipped:

- `backend.json` writes `scenery.db.neon.selfhost.backend.v2`.
- Legacy top-level tenant/branch backend state migrates to project-local tenant and branch maps on read.
- The built-in selfhost driver scopes ensure, reset, restore, delete, and diff to the selected `dev.services.postgres.project`.
- Status JSON reports backend project summaries without changing the status envelope version.
- The default real Neon self-harness proves two projects can use the same branch label without sharing tenant, compute, port, data, delete scope, or diff lookup.

Validation:

- Focused `internal/neonselfhost` and `cmd/scenery` tests passed during implementation.
- `go test ./...` passed.
- The Docker-backed `scenery harness self --json --write` Neon proof passed during implementation.

## Bind-Mounted Neon Storage

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-09
- Quality: B+
- ExecPlan: [0071 Bind-Mounted Neon Storage](0071-neon-bind-mounted-storage.md)

Shipped:

- Self-hosted Neon durable `/data` paths are bind-mounted under the shared agent-home Neon substrate root.
- Generated Compose no longer relies on Docker anonymous volumes for MinIO, pageserver, safekeepers, or storage broker state.
- Existing anonymous-volume cells fail closed at start with an explicit fresh-start recovery path.
- `scenery db neon uninstall` preserves bind-mounted data by default; `--destroy-data` removes it.
- Worktrees continue to isolate through branch pins, leases, timelines, and compute endpoints rather than per-worktree storage roots.

Validation:

- Focused Neon/worktree tests passed during implementation.
- `go test ./cmd/scenery` and `go test ./...` passed.
- `scenery inspect docs --json`, JSON parsing, and whitespace checks passed.

## Built-In Neon Selfhost Driver

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-09
- Quality: B+
- ExecPlan: [0070 Built-In Neon Selfhost Driver](0070-toolchain-managed-neon-selfhost-driver.md)

Shipped:

- `scenery db neon install --json` records the built-in `scenery internal neon-selfhost-driver`.
- The branch driver is built into the main CLI, with external-driver env overrides preserved for development and tests.
- The generated storage-cell topology boots against real Docker Neon images.
- The driver creates project-scoped tenants/timelines, starts SQL-ready branch compute containers, creates the requested database, and returns redacted ready endpoint metadata.
- Reset, restore, delete, and schema diff run behind existing Scenery branch guards.
- Default, race, and release self-harness modes run the real Docker-backed Neon lifecycle proof; `--quick` keeps the smaller non-Docker path.

Validation:

- Focused `internal/neonselfhost` and `cmd/scenery` tests passed during implementation.
- `go test ./...` passed.
- `scenery harness self --json --write` passed with warnings only during implementation.

## CLI Observability Query Surface

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-08
- Quality: B+
- ExecPlan: [0067 CLI Observability Query Surface](0067-cli-observability-query.md)

Shipped:

- `scenery inspect observability --json` for backend readiness, native dialects,
  examples, warnings, and echoed app/session scope.
- `scenery logs query` and `scenery logs tail` for scoped VictoriaLogs LogsQL,
  with JSON/JSONL output, bounded defaults, and explicit LogQL rejection.
- `scenery metrics query`, `scenery metrics labels`, and `scenery metrics series`
  for scoped PromQL/MetricsQL range, instant, and catalog queries.
- Backend-enforced scope via VictoriaLogs `extra_filters` and VictoriaMetrics
  repeated `extra_label` parameters, plus normalized versioned JSON envelopes.
- Schema, contract, cookbook, skill, agent-guide, and knowledge-index updates
  for the new query surface.

Validation:

- `go test ./internal/observability ./cmd/scenery` passed during implementation.
- Full validation was run before PR creation for the implementation change.

## CLI Help and Human Session Status

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-09
- Quality: B+
- ExecPlan: [0069 CLI Help and Human Session Status](0069-cli-help-and-human-ps.md)

Shipped:

- Compact orienting root help for bare `scenery` and `scenery help`.
- `scenery help all` as the grouped full command reference.
- `scenery help <command>` for exact usage, subcommands, flags, and notes.
- `scenery help --json` with schema `scenery.help.v1`.
- Bare `scenery ps` human table output, while `scenery ps --json` keeps the existing agent-facing status JSON shape.
- Drift checks, self-harness schema validation, local contract docs, README, agent guide, installable skill, and focused CLI tests updated for the new contract.

Validation:

- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `go run ./cmd/scenery help --json | python3 -m json.tool` passed.
- Source-driven help smokes passed for root help, `help all`, and `help logs`.
- `go run ./cmd/scenery inspect docs --json` passed.
- `go run ./cmd/scenery harness self --summary --write` passed with warnings only.

## App Validation Profiles

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-08
- Quality: B+
- ExecPlan: [0068 App Validation Profiles](0068-app-validation-profiles.md)

Shipped:

- `.scenery.json` `validation` profiles with default profile selection, metadata, cost, path globs, env overlays, steps, and advisory artifacts.
- `scenery inspect validation --json`, `scenery validate list|inspect|graph`, `scenery validate <profile> --dry-run --json`, `scenery validate <profile> --json --write`, and `scenery validate changed --base <ref>`.
- Sequential fail-fast execution over nested profiles, configured tasks, code-backed tasks, core harness/UI harness, check/test/generate, and DB lifecycle built-ins.
- Harness-style evidence with output tails, repro commands, validation artifacts under `.scenery/harness/validation/artifacts/<run-id>/`, and latest result files.
- Optional `scenery harness --with-validation[=<profile>]` bridge that adds a compact validation pointer to the harness result.
- JSON schemas, local contract docs, agent guide, installable skill, app cookbook recipe, README command list, self-harness schema inventory, and focused tests.

Validation:

- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `python3 -m json.tool docs/knowledge.json docs/schemas/*.json` passed.
- `scenery inspect docs --json` passed.
- Source-driven CLI smoke tests with `go run ./cmd/scenery` passed for inspect, dry-run, execution/write, and harness bridge paths.

## Harness Self Summary Output

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-08
- Quality: B+
- ExecPlan: [0066 Harness Self Summary Output](0066-harness-self-summary-output.md)

Shipped:

- Summary-first self-harness stdout through `scenery.harness.self.summary.v1` for `--summary`, `--json`, and `--json=summary`.
- Explicit full archive stdout through `--json=full`, with `.scenery/harness/self-latest.json` preserved as the full evidence artifact.
- Compact `.scenery/harness/self-summary-latest.json` plus focused `scenery inspect harness artifact`, `diagnostics`, and `timing` drill-downs.
- Worktree-local `.scenery/harness/bin/scenery` build/freshness checks so agent validation does not overwrite the shared installed `scenery` binary.
- Changed-area ignore rules for local harness/report artifacts, repo-relative summary paths, and JSON-aware `scenery version --json` parsing.

Validation:

- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `go build -o .scenery/harness/bin/scenery ./cmd/scenery` passed.
- `scenery harness self --summary --write` passed with warnings.
- `scenery harness self --json=summary --write` passed with warnings.
- `scenery harness self --json=full --write` passed with warnings.
- `scenery inspect harness --json` and focused harness drill-downs passed.

## ENV Harness

- Status: completed
- Owner: scenery runtime / agent DX
- Completed: 2026-06-01
- Quality: A-
- ExecPlan: [0061 ENV Harness](0061-env-harness.md)

Shipped:

- Machine-readable env registry in `docs/environment.registry.json`, validated by `docs/schemas/scenery.environment.registry.v1.schema.json`.
- Registry-backed self-harness drift checks for unregistered production env usage, test-only env leakage into production code, undocumented runtime env entries, and direct production `os.*env` calls outside `internal/envpolicy`.
- `internal/envpolicy` as the small central env access and registry layer, with production env reads/writes migrated through it.
- Secret redaction for live harness toolchain env capture based on registry secret metadata and secret-like names.
- Docs and agent guidance updates that make `.scenery.json`, CLI flags, and checked-in manifests the default configuration surfaces.

Validation:

- `go test ./cmd/scenery -run 'TestHarness.*Env|TestEnvPolicy|TestHarnessSelf'` passed.
- `go test ./internal/envpolicy` passed.
- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `go install ./cmd/scenery` passed.
- `scenery inspect docs --json` passed.
- `scenery harness self --json --write` passed.
- `git diff --check` passed.

## scenery Script Runner

- Status: completed
- Owner: scenery runtime / developer experience
- Completed: 2026-06-01
- Quality: B+
- ExecPlan: [0058 scenery Script Runner](0058-scenery-script-runner.md)

Shipped:

- `scenery run list`, `scenery run inspect`, and `scenery run <domain>:<script> [script args...]` for app-local operational scripts.
- Filesystem-first discovery for `<domain>/scripts/<name>.script.go`, `<domain>/scripts/<name>.script.ts`, `<domain>/scripts/<name>/main.go`, and `<domain>/scripts/<name>/index.ts`.
- Strict target parsing, clear missing-script errors, and ambiguity errors unless `--lang go|typescript` disambiguates.
- Go execution via `go run`, requiring `//go:build ignore` for single-file Go scripts, plus TypeScript execution through Bun or Node with `tsx`.
- Focused tests, usage text, local-contract/cookbook docs, and a script fixture that also passes the normal app fixture matrix.

Validation:

- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `git diff --check` passed.
- `go install ./cmd/scenery` passed.
- Focused `scenery run` fixture scenarios passed.
- `scenery harness self --json --write` was run after fixes; all feature-relevant checks and fixture matrix passed, but the overall harness remained red on the pre-existing full-suite timing budget tracked by `docs/plans/0050-test-suite-speed-hardening.md`.

## Typed Lifecycle Graph Phase 1

- Status: completed
- Owner: scenery runtime / ONLV integration
- Completed: 2026-06-01
- Quality: B+
- ExecPlan: [0057 Typed Lifecycle Graph Phase 1](0057-typed-lifecycle-graph-phase1.md)

Shipped:

- `scenery generate`, `scenery generate client`, and `scenery generate sqlc` for configured file-producing lifecycle work.
- `scenery inspect generators --json` and `scenery generate --dry-run --json` for generator graph inspection.
- `scenery db sync` with an explicit `database.apply` exec provider followed by dependent SQLC regeneration.
- `scenery task list`, `scenery task run <name>`, and `scenery task graph --json` as a thin repo-local task layer.
- `.scenery.json` config/schema support for `generators`, `database.apply`, and `tasks`, plus focused tests and docs.

Validation:

- `go test ./cmd/scenery -run 'Test(ParseGenerate|BuildSQLC|RunGenerate|RunSQLC|DBSync|TaskGraph|DBCommand)'` passed.
- `go test ./cmd/scenery` passed.
- `go test ./...` passed.
- `go install ./cmd/scenery` passed.
- `scenery harness self --json --write` was run after fixes; all feature-relevant checks passed, but the overall harness remained red on the pre-existing full-suite timing budget tracked by `docs/plans/0050-test-suite-speed-hardening.md`.

## Browser Worker Operational Hardening

- Status: completed
- Owner: scenery runtime / legacy async runtime TypeScript workers
- Completed: 2026-05-30
- Quality: B+
- ExecPlan: [0052 Browser Worker Operational Hardening](0052-browser-worker-operational-hardening.md)

Shipped:

- Build prep skips browser runtime artifact directories: `var/browser`, `var/chrome`, and `var/playwright`.
- Build source listing and workspace copying skip unsupported non-regular files such as Unix sockets without changing symlink behavior.
- Generated TypeScript legacy async runtime worker tests now lock supervisor PID monitoring through `SCENERY_DEV_SUPERVISOR_PID`.
- Dev supervisor shutdown tests prove TypeScript worker children are interrupted, waited on, and detached from supervisor state.
- Detached `scenery dev` children write a generated TypeScript worker registry and conservatively reap stale registry-matched workers for the current app root and generated `worker.ts` path.
- Stale worker cleanup records a dev dashboard process event and leaves foreground `scenery worker typescript` behavior unchanged.
- Focused tests, full `go test -count=1 ./...`, binary install, `git diff --check`, and `scenery harness self --json --write` validation.

## Agent HTTPS Ingress

- Status: completed
- Owner: scenery runtime / dev agent
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0044 Agent HTTPS Ingress](0044-agent-https-ingress.md)

Shipped:

- Explicit agent router TLS mode through `scenery agent --router-tls`; the short-lived router TLS env override was removed later.
- Trust-install controls through `scenery agent --trust` and `SCENERY_AGENT_TRUST=1`, reusing the existing scenery local CA.
- Agent session routes use `https://...scenery.localhost` when the agent router runs with TLS.
- SNI-based on-demand leaf certificates for routed agent hostnames, including two-label session hosts.
- Router scheme metadata in agent health/state plus CLI docs, local contract updates, focused tests, and full `go test ./...` validation.

## Agent Detached Dev and Attach

- Status: completed
- Owner: scenery runtime / dev agent
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0043 Agent Detached Dev and Attach](0043-agent-detached-dev-and-attach.md)

Shipped:

- `scenery dev --detach` starts an agent-backed background dev supervisor, waits for the child PID to register as session owner, writes detached supervisor output under the agent directory, and returns session details.
- Detached child supervisors skip parent-process monitoring while normal attached `scenery dev` keeps parent-death cleanup.
- `scenery attach` follows the current session logs by default and supports explicit app-root, session, limit, stream, and JSONL options.
- Command usage, README, local contract docs, focused tests, and full `go test ./...` validation.

## Agent Global Dashboard

- Status: completed
- Owner: scenery runtime / dev dashboard
- Completed: 2026-05-27
- Quality: B+
- ExecPlan: [0042 Agent Global Dashboard](0042-agent-global-dashboard.md)

Shipped:

- Agent-owned visible dashboard backend for `console.scenery.localhost/s/<session_id>`.
- Session-addressable dashboard app records so multiple worktrees for the same base app can appear independently.
- Runtime reports sent to the agent dashboard using per-session report tokens carried over the Unix-socket control API and omitted from manifests.
- Direct/per-session dashboard fallback for agent-disabled, unavailable-agent, and explicit local-proxy paths.
- Local contract updates, focused tests, full Go test suite, binary install, and self-harness snapshot refresh.

## Agent Shared Substrates and Dev Services

- Status: completed
- Owner: scenery runtime / dev services
- Completed: 2026-05-26
- Quality: B+
- ExecPlan: [0040 Agent Shared Substrates and Dev Services](0040-agent-shared-substrates-and-dev-services.md)

Shipped:

- Agent substrate registry for shared local dev processes.
- Shared agent-registered VictoriaMetrics, VictoriaLogs, VictoriaTraces, Grafana, and legacy async runtime dev server reuse across sessions.
- Grafana dashboards with a `Session` variable backed by `scenery_session_id`.
- Session-scoped legacy async runtime task queue/deployment/build env for app child processes.
- Agent-routed frontend URLs for configured frontend upstreams.
- Beta `.scenery.json` `dev.services` declarations for Postgres.
- `scenery db psql` as the current managed database shell helper.

## Grafana Dev Hardening

- Status: completed
- Owner: scenery dev platform / observability
- Completed: 2026-05-26
- Quality: A-
- ExecPlan: [0036 Grafana Dev Hardening](0036-grafana-dev-hardening.md)

Shipped:

- Verified Grafana readiness requires server health plus expected datasource and dashboard UIDs.
- External Grafana reuse is verified-only; unverified external instances are degraded and do not get dashboard links.
- Grafana upstream and browser public URLs are split, including local proxy `root_url` provisioning.
- Managed pinned Grafana is preferred over `PATH`; `PATH` fallback is version-probed.
- Grafana archives are checksum-verified before extraction, including custom download SHA support.
- Child Grafana processes filter inherited `GF_*` overrides by default.
- Datasource provisioning prunes stale datasources and includes org/version metadata.
- Dashboard state exposes availability/readiness booleans, and the UI disables links unless Grafana is verified usable.
- Dashboard metrics now use the emitted `scenery_request_duration_seconds` contract.
- Fake-process, external-verification, provisioning, local-proxy URL, and optional live-smoke test coverage.

## Grafana Dev Integration

- Status: completed
- Owner: scenery dev runtime
- Completed: 2026-05-25
- Quality: B+
- ExecPlan: [0033 Grafana Dev Integration](0033-grafana-dev-integration.md)

Shipped:

- `scenery dev` can supervise local Grafana alongside VictoriaMetrics, VictoriaLogs, and VictoriaTraces.
- Generated Grafana config, datasource provisioning, and dashboard JSON live under `.scenery/grafana/`.
- Stable datasource UIDs for VictoriaMetrics, VictoriaLogs, and Jaeger-compatible VictoriaTraces.
- Stable dashboard UIDs for overview, logs, and endpoint debugging dashboards.
- Scenery dashboard Observability route with Grafana status, paths, datasource status, and deep links.
- `scenery dev --json` Grafana events and `run.ready` metadata.
- Env controls for opt-in, disable, required mode, binary resolution, download, port, root directory, version, and plugin preinstall.
- Browser validation against a live `scenery dev` stack plus supervised shutdown and headless runtime smoke coverage.

## UI Guardrail Hardening

- Status: completed
- Owner: scenery dashboard
- Completed: 2026-05-09
- Quality: A-
- ExecPlan: [0012 UI Guardrail Hardening](0012-ui-guardrail-hardening.md)

Shipped:

- Pinned, stricter `bun run shadcn:add @scenery/<item>` wrapper that rejects unsupported flags, non-scenery items, unsafe overwrite, and occupied registry port.
- UI static validation for registry item source and target declarations.
- Stronger UI import scanning for multiline imports, re-exports, dynamic imports, and CommonJS requires.
- Stronger className drift warnings for `cn(...)`, template literal, and conditional literal forms.
- Fixture tests for UI static guardrail bypasses.
- Explicit `tailwindcss` UI devDependency.
- `PageToolbar` layout and `@scenery/page-toolbar` registry item.
- Optional sidebar/inspector/event-stream slots no longer create empty fixed-width layout columns.

## Dashboard Data Explorer

- Status: completed
- Owner: scenery dashboard
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0013 Dashboard Data Explorer](0013-dashboard-data-explorer.md)

Shipped:

- Dashboard `/$appId/data` route.
- Data Explorer page composed from scenery `DataExplorerLayout`, `PageToolbar`, and primitives.
- Dashboard RPC bridge for data inspect, metadata-validated record queries, and outbox event tail reads.
- Tenant/object/field/index/migration/trigger/outbox inspection panels.
- Record table with limit and JSON filter controls.
- Focused backend and UI coverage for the new bridge and route surface.

## Browser UI Harness

- Status: completed
- Owner: scenery dashboard
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0014 Browser UI Harness](0014-browser-ui-harness.md)

Shipped:

- `scenery harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]`.
- `scenery.harness.ui.v1` JSON schema.
- Temporary `scenery dev --json` startup path with isolated app/dashboard ports when no dashboard URL is provided.
- Browser route checks for dashboard home, API Explorer, service catalog, traces, Data Explorer, and DB Explorer.
- Screenshot artifacts plus console and network JSONL artifacts under `.scenery/harness/ui/`.
- Focused command tests using a fake browser runner so normal Go tests do not require Chrome.
- Current follow-up debt is deeper fixture-backed mutation coverage; the browser harness itself and route-specific journeys are implemented.

## Dashboard Slot-Layout Migration

- Status: completed
- Owner: scenery dashboard
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0015 Dashboard Slot-Layout Migration](0015-dashboard-slot-layout-migration.md)

Shipped:

- Dashboard shell now composes `AppShell` instead of duplicating shell structure and style ownership.
- Top navigation class recipes live in the scenery layout layer.
- API Explorer and Pub/Sub route actions now use the scenery `Button` primitive.
- `AppShell` render coverage for stable layout markers and styling helpers.
- Self-harness UI static architecture check reports 0 className warnings.

## Data Platform Indexes and Cursor Pagination

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0016 Data Platform Indexes and Cursor Pagination](0016-data-platform-indexes-and-cursor-pagination.md)

Shipped:

- `scenery_data.indexes` and `scenery_data.index_fields` metadata tables.
- Public `data.Store.CreateIndex` and `data.Store.ListIndexes` APIs.
- PostgreSQL btree and GIN physical index creation through migration rows and advisory locks.
- `scenery inspect data` index output with physical existence and drift status.
- Keyset cursor pagination with `id` tie-breaker, encoded cursor state, and sort-shape rejection.
- PostgreSQL-backed coverage for index creation, inspect output, and cursor pagination.

## Data Platform Relationships

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0017 Data Platform Relationships](0017-data-platform-relationships.md)

Shipped:

- Public relation settings for dynamic data fields.
- `many_to_one` relation fields backed by UUID columns and PostgreSQL foreign keys.
- `many_to_many` relation fields backed by physical join tables.
- One-hop `many_to_one` relation path support for filters, sorts, and selected fields.
- Inspect data relation output for target object, relation kind, delete behavior, inverse field, and join table metadata.
- PostgreSQL-backed tests for FK enforcement, join-table creation, relation-path queries, and inspect output.

## Data Platform Saved Views

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0018 Data Platform Saved Views](0018-data-platform-saved-views.md)

Shipped:

- `scenery_data.views` and `scenery_data.view_fields` metadata tables.
- Public saved-view API through `data.Store`.
- Query-by-view execution through the existing metadata SQL compiler.
- Inspect data output for saved views.
- Data Explorer saved view selector.
- PostgreSQL-backed tests for persistence, validation, query execution, updates, deletes, and inspect output.

## Data Platform Public Contract

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0019 Data Platform Public Contract](0019-data-platform-public-contract.md)

Shipped:

- `docs/data-platform.md` as the human-facing beta data package guide.
- Public `data.Error`, `data.ErrorCode`, and `data.CodeOf(err)` helpers.
- Public contract notes for indexes, relations, saved views, cursors, live events, triggers, and error codes.
- Compile-only `examples/data-platform` package.
- Focused public package tests for error classification.

## scenery UI Registry and Agent Guardrails

- Status: completed
- Owner: scenery dashboard
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0011 scenery UI Registry and Agent Guardrails](0011-scenery-ui-registry-and-agent-guardrails.md)

Shipped:

- `@scenery/*` shadcn registry configuration under `ui/components.json`.
- Guarded `bun run shadcn:add @scenery/<item>` wrapper with local registry serving and dry-run-first behavior.
- scenery-owned UI primitives and slot layouts under `ui/src/components/primitives` and `ui/src/components/layouts`.
- Initial registry items for dashboard/data layouts plus ONLV-ported button/card/dialog/input/app surface/filter/sidebar components.
- `docs/ui-agent-contract.md`.
- Self-harness UI static architecture checks for registry/script/import boundaries and className migration warnings.
- ONLV app screen imports switched to scenery-facing primitives/layout paths while preserving current rendered UI.

## scenery Go Runner Phase 1

- Status: completed
- Owner: scenery runtime
- Completed: 2026-04-27
- Quality: B

Shipped:

- `scenery serve`, `scenery run`, `scenery build`, `scenery test`, `scenery check`, `scenery logs`, and beta `scenery psql`
- scenery API parser/codegen/runtime for common Go service behavior
- Secrets from `.env`
- local HTTPS proxy support
- cron, middleware, Pub/Sub, tracing, logging, DB query tracing, and dashboard support

## Stable Inspect And Harness Contracts

- Status: completed
- Owner: scenery runtime
- Completed: 2026-04-27
- Quality: A

Shipped:

- `scenery inspect app|routes|services|endpoints|wire|build|paths --json`
- beta `scenery inspect traces|metrics --json`
- `scenery inspect docs --json`
- `.scenery/gen/*` and `.scenery/build/latest.json`
- `scenery harness --json --write`
- `scenery harness self --json --write`

## Split `scenery dev` From Headless Runtime

- Status: completed
- Owner: scenery runtime
- Completed: 2026-04-27
- Quality: B+
- ExecPlan: [0001 Split `scenery dev` From Headless `scenery run`](0001-devrun-command-split.md)

Shipped:

- `scenery dev` owns the development supervisor, dashboard, removed agent transport, local proxy, watch/rebuild loop, and development logs.
- The headless runtime command builds once and starts the app without dashboard, local proxy, removed agent transport, or file watching. It is now spelled `scenery serve`; the historical plan used `scenery run`.
- Generated app binaries are headless by default unless development behavior is explicitly enabled.
- Command parsing, tests, usage text, and local contract were updated for the split.

## scenery v0 Release Readiness

- Status: completed
- Owner: scenery runtime
- Completed: 2026-04-27
- Quality: B+
- ExecPlan: [0002 scenery v0 Release Readiness](0002-v0-release-readiness.md)

Shipped:

- Stable/dev/beta surface classification in `docs/local-contract.md`.
- `scenery version --json` and `scenery.version.v1` schema.
- Dev/admin/pprof route gating so public app listeners stay production-like by default.
- Opt-in local proxy/trust behavior for `scenery dev`.
- Central `.env` parsing and production secret validation.
- Build workspace filtering for local artifacts and secret files.
- Response JSON semantics tests and `scripts/release-gate.sh`.

## Queryable Observability

- Status: completed
- Owner: scenery observability
- Completed: 2026-04-27
- Quality: B

Shipped:

- Trace query filters for service, endpoint, trace ID, status, duration, time window, and sort order.
- Metrics rollups by service and endpoint.
- Log-level counts and trace event counts from the dashboard SQLite store.

## Victoria Observability Sidecars

- Status: completed
- Owner: scenery runtime
- Completed: 2026-04-27
- Quality: A
- ExecPlan: [0003 Victoria Observability Sidecars](0003-victoria-observability-sidecars.md)

Shipped:

- `scenery dev` starts VictoriaMetrics, VictoriaLogs, and VictoriaTraces sidecars by default while preserving SQLite observability writes.
- Sidecars use loopback ports, `.scenery/victoria/` storage, automatic binary resolution/download, and graceful shutdown with the dev supervisor.
- scenery exports built-in trace, log, and request-duration metric reports to Victoria over OTLP protobuf.
- Dashboard and inspect trace reads prefer VictoriaTraces with SQLite fallback.

## scenery-Native Local HTTPS Proxy

- Status: completed
- Owner: scenery runtime
- Completed: 2026-04-27
- Quality: B
- ExecPlan: [0004 scenery-Native Local HTTPS Proxy](0004-scenery-native-localproxy.md)

Shipped:

- Replaced embedded Caddy local HTTPS proxying with a standard-library route table, TLS certificate cache, trust installer hooks, HTTPS reverse proxy, and optional HTTP redirect listener.
- Preserved `internal/localproxy` public API names and the existing scenery local URL shape.
- Removed `internal/localproxy/caddyimports.go` plus Caddy, CertMagic, and ZeroSSL module dependencies.
- Added behavior tests for routing, frontend config/catch-all handling, Host rewriting, redirects, certificate SANs and reuse, trust installer injection, and lifecycle cleanup.

## scenery Data Platform Vertical Slice

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-08
- Quality: B
- ExecPlan: [0005 scenery Data Platform](0005-scenery-data-platform.md)

Shipped:

- `scenery.sh/data` public facade and `internal/objectstore` implementation.
- PostgreSQL metadata bootstrap, real object tables, real field columns, schema migration rows, advisory locks, and physical schema verification.
- Metadata-validated SQL query compiler, transactional record mutations, transactional outbox rows, in-process query-aware live routing, and SSE replay/fanout.
- `testdata/apps/data-platform` fixture app using ordinary scenery services and raw SSE.
- Unit coverage plus testcontainers-backed PostgreSQL integration coverage with `SCENERY_TEST_DATABASE_URL` override support.

Follow-ups:

- [0007 Data Platform Validation and Inspect](0007-data-platform-validation-and-inspect.md) for PostgreSQL CI and inspectability.
- [0008 Data Platform Migration and Live Hardening](0008-data-platform-migration-and-live-hardening.md) for migration/live correctness.
- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) for direct SQL change capture after hardening.

## scenery Standard Auth

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0006 scenery Standard Auth](0006-scenery-standard-auth.md)

Shipped:

- scenery-owned standard auth module under `scenery.sh/auth`.
- HCL/sqlc auth database tooling for the `scenery_auth` PostgreSQL schema.
- Built-in auth handler and endpoint registration for apps with `"auth": {"enabled": true}`.
- Standard auth TypeScript client generation and inspect visibility.
- ONLV cutover to consume the top-level scenery auth surface instead of owning auth business logic.
- Production migration runbook for preserving existing users, tenants, memberships, password hashes, sessions, and one-time tokens.

## Data Platform Validation and Inspect

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0007 Data Platform Validation and Inspect](0007-data-platform-validation-and-inspect.md)

Shipped:

- `testcontainers-go` PostgreSQL coverage in the regular Go CI job, with DB-backed objectstore and data-inspect tests.
- `scenery inspect data --json --database-url <postgres-url> [--tenant <key>] [--object <name>]`.
- Data inspect JSON schema, docs, self-harness schema tracking, and fixture README.
- More reliable PostgreSQL integration cleanup and explicit SSE watermark usage in the live test.

Follow-ups:

- [0008 Data Platform Migration and Live Hardening](0008-data-platform-migration-and-live-hardening.md) for migration edge cases, live-data correctness, and public `data` API cleanup.
- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) after migration/live hardening.

## Data Platform Migration and Live Hardening

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0008 Data Platform Migration and Live Hardening](0008-data-platform-migration-and-live-hardening.md)

Shipped:

- Deterministic readable physical table and column names with stable suffixes.
- Retry-safe object and field creation with physical schema verification, drift detection, and failed migration recording.
- PostgreSQL-backed idempotence, concurrency, failure/retry, and drift tests.
- Live update hardening for created/updated/deleted matching, reconnects, selected-field stripping, permission row filters, heartbeats, unsubscribe cleanup, and slow subscribers.
- Public `data.Store` wrapper and app-facing filter/sort helpers.

Follow-ups:

- [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md) for direct SQL outbox events.

## Trigger-Backed Outbox

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-08
- Quality: B+
- ExecPlan: [0009 Trigger-Backed Outbox](0009-trigger-backed-outbox.md)

Shipped:

- Optional per-object record-table triggers that capture direct SQL changes.
- Shared `scenery_data.record_change_trigger()` function that writes logical events to `scenery_data.outbox_events`.
- Transaction-local actor context and explicit-mutation skip flag to avoid duplicate events.
- SSE polling/replay compatibility for trigger-created events.
- Inspect output for trigger enablement and physical trigger presence.

## Data Platform Indexes and Cursor Pagination

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0010 Data Platform Indexes and Cursor Pagination](0010-data-platform-indexes-and-pagination.md)

Shipped:

- Metadata-backed logical indexes in `scenery_data.indexes` and `scenery_data.index_fields`.
- Public `data.Store.CreateIndex` and `data.Store.ListIndexes` APIs.
- Migration-managed deterministic physical PostgreSQL indexes with advisory locks, migration rows, and catalog verification.
- Btree scalar and compound index support plus explicit GIN indexes for multi-select and JSON/raw JSON fields.
- `scenery inspect data --json` index reporting with physical presence/drift state.
- Keyset cursor pagination for `QueryRecords` and opaque `RecordPage.NextCursor` values.
- Fixture app endpoints and README examples for index creation/listing and cursor pagination.

## Data Platform Search

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0020 Data Platform Search](0020-data-platform-search.md)

Shipped:

- Field-level search metadata with `is_searchable` and `search_weight`.
- PostgreSQL-backed `scenery_data.search_documents` table with a GIN-indexed `tsvector` document.
- Transactional search document maintenance for create, update, and delete through the public data mutation path.
- Object-wide `search` query filter, public `data.Search(...)` helper, and live-event search matching.
- `scenery inspect data --json` searchable-field reporting and Data Explorer search input.

Follow-ups:

- Direct SQL edits do not refresh search documents in this version. Add trigger-backed search refresh or explicit rebuild tooling before treating direct SQL search freshness as stable.

## Standard Auth x Data Tenant Permissions

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0021 Standard Auth x Data Tenant Permissions](0021-auth-data-tenant-permissions.md)

Shipped:

- `data.Actor` tenant awareness and `data.ActorFromContext` standard-auth tenant mapping.
- `data.TenantKeyFromContext`, `data.RequireTenantKeyFromContext`, and `data.TenantKeyFromActor` helpers.
- `data.StandardAuthPermissions`, which maps standard-auth `tenant_id` directly to data `TenantKey`, fails closed on mismatches, and delegates to an optional base permission provider.
- Tenant key propagation through object and field permission refs.
- Tests for same-tenant access, cross-tenant denial, delegated row filters, and live subscription denial.

## Data Import, Export, and Fixtures

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B
- ExecPlan: [0022 Data Import, Export, and Fixtures](0022-data-import-export-fixtures.md)

Shipped:

- `scenery.data.export.v1` JSON schema.
- Public `data.Store.ExportTenant` and `data.Store.ImportTenant` APIs.
- Portable bundles for logical tenants, objects, fields/options, indexes, saved views, and records.
- Transactional import through existing mutation paths, with new record IDs and `record_id_map` reconciliation.
- Fixture app export/import endpoints and `company-export.json` fixture data.
- PostgreSQL-backed round-trip coverage for metadata, records, indexes, saved views, and ID remapping.

## Skill Refresh and Agent Onboarding

- Status: completed
- Owner: scenery maintainers
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0027 Skill Refresh and Agent Onboarding](0027-skill-refresh-and-agent-onboarding.md)

Shipped:

- Refreshed `SKILL.md` for current scenery workflows.
- Added current coverage for the data platform, standard auth tenant permissions, dashboard Data Explorer, browser UI harness, UI registry guardrails, ONLV layout migration expectations, and validation command matrices.
- Linked the skill to the local contract, app cookbook, data-platform overview/runbook, UI agent contract, and active plans.

## scenery App Development Cookbook

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0028 scenery App Development Cookbook](0028-scenery-app-development-cookbook.md)

Shipped:

- `docs/app-development-cookbook.md` with practical recipes for building scenery apps.
- Recipes for typed endpoints, auth endpoints, private calls, service initialization, middleware, request tags, status responses, coded errors, Pub/Sub, cron, pgxpool tracing, TypeScript clients, local proxy config, debugging, harness workflows, and common mistakes.
- Docs index and knowledge index entries for agent discovery.

## Data Platform Developer Runbook

- Status: completed
- Owner: scenery data platform
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0029 Data Platform Developer Runbook](0029-data-platform-developer-runbook.md)

Shipped:

- `docs/data-platform-runbook.md` for operational data-platform workflows.
- Runbook coverage for object/field creation, options, composites, relations, indexes, saved views, CRUD, queries/cursors/search, SSE, trigger-backed outbox, import/export, standard-auth permissions, inspect output, migration recovery, drift debugging caveats, performance notes, and beta limitations.
- Docs index and knowledge index entries for agent discovery.

## Documentation Drift Harness

- Status: completed
- Owner: scenery maintainers
- Completed: 2026-05-09
- Quality: B+
- ExecPlan: [0030 Documentation Drift Harness](0030-documentation-drift-harness.md)

Shipped:

- `SKILL.md` is now a self-harness knowledge entrypoint.
- Self-harness checks required installed-skill capability mentions such as `scenery inspect data --json`, `scenery harness ui --json`, `scenery.sh/data`, the `@scenery` registry, and `scenery harness self --json --write`.
- `docs/knowledge.json` is checked for important docs including `SKILL.md`, the app cookbook, the data-platform runbook, the UI agent contract, and the local contract.
- Regression coverage for stale `SKILL.md` detection.

## ONLV Direct scenery Registry Adoption

- Status: completed
- Owner: scenery dashboard / ONLV app
- Completed: 2026-05-10
- Quality: B+
- ExecPlan: [0031 ONLV Direct scenery Registry Adoption](0031-onlv-direct-scenery-registry-adoption.md)

Shipped:

- scenery-approved primitive registry source under `ui/src/components/registry/primitives`.
- Individual `@scenery/*` primitive registry items plus the aggregate `@scenery/primitives` item.
- ONLV app mirrored registry outputs under `apps/app/src/components/primitives`.
- ONLV app-facing imports moved away from raw `@/components/ui/*` and local product-layout compatibility imports.
- ONLV primitive barrel now explicitly exports registry-owned primitive files instead of re-exporting `../ui`.
- Removed unused ONLV app generic compatibility shims and the old local `components/ui` source tree, and updated ONLV app agent instructions to use registry-owned primitives/layouts.
- Added `.ts` public entrypoint re-exports for migrated primitives that Vite may still request during hot reload.
- `apps/app/scripts/check-scenery-ui-registry.mjs`, wired into `bun run typecheck`, to prevent future drift back to local raw shadcn imports.
- ONLV app visual harness remained stable with 24/24 snapshots passing.

## Remove Pub/Sub Package

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-25
- Quality: B+
- ExecPlan: [0034 Remove Pub/Sub Package](0034-remove-pubsub-package.md)

Shipped:

- Removed the public `scenery.sh/pubsub` package, runtime hooks, dashboard/admin surfaces, schemas, and current docs.
- Moved service-method background handler support to `scenery.sh/legacy-async-runtime`.
- Migrated ONLV async jobs in `codexsvc`, `jobs`, `house`, and `maps` to native legacy async runtime workflows and activities.
- Validation passed for scenery; ONLV validation is blocked only by the native house `torch/torch.h` environment prerequisite.

## scenery Agent MVP

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-26
- Quality: B
- ExecPlan: [0037 scenery Agent MVP](0037-scenery-agent-mvp.md)

Shipped:

- `internal/agent`, a standard-library local daemon package with Unix control socket, JSON session registry, host-based HTTP router, session manifest writing, and Unix-socket aware reverse proxying.
- `scenery agent`, `scenery status --json`, and `scenery down`.
- `scenery dev` auto-starts/connects to the agent unless disabled, registers the worktree session, writes `.scenery/sessions/<session_id>/manifest.json`, updates status, and advertises routed API/dashboard/removed agent transport URLs when no explicit local proxy is active.
- Runtime servers support `SCENERY_LISTEN_NETWORK=unix` with TCP still available.

## Agent Private Dev Backends

- Status: completed
- Owner: scenery runtime
- Completed: 2026-05-26
- Quality: B+
- ExecPlan: [0038 Agent Private Dev Backends](0038-agent-private-dev-backends.md)

Shipped:

- `scenery dev` with no explicit listen flags now registers a session-private Unix API backend at `.scenery/sessions/<session_id>/run/api.sock` when the agent is available.
- Explicit `--listen` and `--port` continue to use TCP and register TCP API backends.
- The legacy local HTTPS proxy is opt-in through `--proxy`, `--trust`, or `SCENERY_LOCAL_PROXY=1`; those paths use hidden loopback TCP because the proxy only supports TCP upstreams.
- App children receive `SCENERY_LISTEN_NETWORK` and `SCENERY_LISTEN_ADDR`, and supervisor startup probes support both TCP and Unix listeners.

## Agent Session Identity and Signals

- Status: completed
- Owner: scenery runtime / observability
- Completed: 2026-05-26
- Quality: B+
- ExecPlan: [0039 Agent Session Identity and Signals](0039-agent-session-identity-and-signals.md)

Shipped:

- Session, base-app, and runtime-app identity are passed into dev children and exposed through runtime metadata plus `/__scenery/config`.
- Devdash app records, process output, logs JSONL, trace summaries, trace events, log events, inspect traces, and inspect metrics carry session identity where applicable.
- `scenery logs --session current|<id>`, `scenery inspect traces --session current|<id> --json`, and `scenery inspect metrics --session current|<id> --json` filter session-scoped records.
- Victoria trace/log/metric export includes session labels.
- Dev-mode standard auth receives session-routed local URL env vars and host-only cookie-domain defaults.
- Dev-mode legacy async runtime receives session-scoped task queue prefix, worker deployment name, and build ID env vars.
