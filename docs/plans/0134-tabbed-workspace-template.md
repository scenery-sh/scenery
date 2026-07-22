# 0134 Tabbed Workspace Template: One Generated Page Kind for Multi-Tab Domain Workspaces

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

The Micro platform's page inventory (its ExecPlan 0051) found that the
single most common reason an otherwise template-shaped page cannot be
generated is **domain tabs**: tickets, inventory, invoices, vendors, fleet,
permits, NTP requests, commissions, job-costing, and sales are all "stats
tiles + tab strip + one table (or content block) per tab + create/edit
dialogs" workspaces. Each *tab* fits `table_page`; the *page* fits nothing.
That is roughly ten of the platform's largest hand-written pages
(~14k lines) blocked on one missing concept.

This plan adds a tabbed workspace page kind to Scenery: a generated page
that owns the route, nav entry, shared header (title, actions, stats), and
a tab strip, where each tab embeds the content of an existing generated
page resource (table or content) rendered without its own page chrome.
Composition over expansion: tabs reference page resources; they do not
re-declare columns/filters inline. When it completes, the platform's
multi-tab workspaces become convertible tab-by-tab under the 0051 parity
rules, and each tab conversion reuses everything table pages already have
(response-aware slots, pagination modes, dialogs, row actions, export,
grouping).

## Progress

- [ ] (2026-07-22) Plan authored. No implementation started.
- [ ] Milestone 1: design decision — embedding contract (chrome-less page
  rendering) and the `workspace_page` source-kind spelling, recorded here
  before code.
- [ ] Milestone 2: compiler + schema — `workspace_page` kind, tab
  references, diagnostics.
- [ ] Milestone 3: generator — embeddable tab components from existing
  page kinds; the workspace adapter (tab strip, URL state, lazy tabs,
  shared header/stats).
- [ ] Milestone 4: catalog — `WorkspacePage` shell (Astryx `TabList`),
  tab-count badges from response metadata, slot pass-throughs.
- [ ] Milestone 5: fixture + demo — a workspace fixture in the compiler
  testdata apps and a `/scenery-ui` demo entry.
- [ ] Milestone 6: platform pilot handoff — convert one real workspace
  (recommended: Sales first as the read-only pilot, then Vendors as the
  CRUD pilot) under the parity gate, in a platform ExecPlan.

## Surprises & Discoveries

- (2026-07-22) Nothing yet.

## Decision Log

- (2026-07-22, agent, needs Petr's confirmation in Milestone 1)
  **Composition over expansion.** A tab references an existing
  `table_page`/`content_page` resource; the workspace does not inline a
  second copy of the table grammar. Rationale: every table capability
  (filters, pagination, slots, dialogs, export) keeps exactly one
  contract spelling and one generator path; tabs stay individually
  testable; a tab can later be promoted to a standalone route (or
  demoted) by moving one reference.
- (2026-07-22, agent) **Embedded pages surrender their own route and
  chrome.** A page referenced by a workspace tab must not also register
  its own route/nav entry (compiler-enforced, with an explicit attr to
  keep a standalone route too if a real case appears — decide in
  Milestone 1 whether to allow dual-mounting at all; default no).
- (2026-07-22, agent) **One tab active, others lazy.** Only the active
  tab mounts and queries; switching preserves each previously-mounted
  tab's state for the life of the page (this directly addresses the
  Documents-style "shared state lost when switching views" class of
  blocker — a two-view page is a two-tab workspace).
- (2026-07-22, agent) **Tab selection is URL state** (`?tab=<name>`, the
  first declared tab as default) so deep links and refresh keep the
  active tab — the hand-written workspaces' behavior.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

- **Page kinds today** (`internal/spec/source_schemas.go`,
  `internal/spec/schemas.go`, `internal/compiler/{table_page,content_page}.go`,
  `internal/generate/generate_typescript_react.go`): `table_page`,
  `split_page`, `content_page` each expand to route-owning page resources;
  generated adapters wrap content in the catalog `Page` shell
  (`fill` mode for tables) and register routes/nav via
  `routes.generated.ts`.
- **The generated table adapter** is a single exported `<Name>Page`
  component that owns its client, query state, dialogs, and `Page` shell
  (see `renderReactTablePage`). Embedding requires splitting that into
  shell + content so a workspace can mount the content under its own
  shell — this split is the heart of Milestone 3 and must not fork the
  generator (one path emits both the standalone page and, when referenced
  by a workspace, the chrome-less content component).
- **Catalog pieces to reuse**: `Page`/`PageShell` (`ui/components/PageLayout.tsx`),
  Astryx `TabList`/`Tab` (re-exported), `StatGrid` stats, and the
  response-aware contexts from 0131 (tab-count badges read the same
  result metadata).
- **Reference workspaces** (Micro platform repo, hand-written): `sales.tsx`
  (read-only, `Tab` strip + `StatGrid` + one `DataTable` per tab — the
  simplest real target), `vendors.tsx` (tabs + full CRUD dialogs + doc
  upload), `tickets.tsx`, `inventory.tsx` (largest; also embeds a barcode
  scanner dialog — its scanner tab will need a content tab with an
  app-owned slot). Read them before fixing the contract spelling.
- Validation lanes, spec-revision fallout recovery, and consumer
  verification: as documented in ExecPlan 0131 `Concrete Steps`.

## Milestones

1. **Design.** Fix the contract spelling and the embedding mechanics
   before code; record both here. Strawman to evaluate:

       workspace_page "vendors" {
         path      = "/vendors"
         title     = "Vendors"
         nav_group = "Main"
         stats { source = binding.vendor_metrics_http }
         action "Add vendor" { dialog = form_dialog.vendor_create }
         tab "directory" { page = table_page.vendor_directory  label = "Directory" }
         tab "documents" { page = table_page.vendor_documents  label = "Documents" count = "documents_total" }
         tab "insights"  { page = content_page.vendor_insights label = "Insights" }
       }

   Decisions to lock: tab reference targets (`table_page` and
   `content_page` first; `split_page` later if ever), whether embedded
   pages may keep standalone routes (default no), where per-tab counts
   come from (a named field of the workspace stats result vs per-tab
   metadata), how header actions merge (workspace-level actions plus the
   active tab's own header dialogs), and the URL parameter name.
2. **Compiler + schema.** New source kind with `tab` children
   (positional name, `page` reference, `label`, optional `count`);
   diagnostics: at least one tab, unique tab names, referenced page
   exists and is an embeddable kind, referenced page not independently
   routed, count fields exist on the stats result. New SCN26xx codes in
   the diagnostics catalog.
3. **Generator.** Emit (a) chrome-less content components for referenced
   pages — the existing table adapter body minus `Page` shell, exported
   alongside the standalone form only when referenced; (b) the workspace
   adapter: `Page fill` shell, stats query, workspace actions, Astryx
   `TabList` with URL-synced selection, lazy mount with kept-alive
   previously-visited tabs, per-tab count badges. Golden tests for both.
4. **Catalog.** A thin `WorkspaceTabs` (or equivalent) component if the
   adapter needs shared chrome beyond raw `TabList` — keep it minimal;
   the workspace adapter composes existing catalog pieces. Update
   `ui/AGENTS.md` contract paragraphs.
5. **Fixture + demo.** A two-tab workspace in
   `internal/compiler/testdata/house` (table tab + content tab)
   exercising counts, lazy mount, and URL state; a `/scenery-ui`
   templates-section entry in the platform demo describing the fourth
   template.
6. **Platform pilot.** Hand off via a platform ExecPlan: convert Sales
   (read-only, lowest risk) under the full 0051 parity inventory; then
   Vendors to prove CRUD dialogs, doc upload slots, and per-tab export.
   The remaining workspaces convert tab-by-tab in follow-up platform
   plans — not part of this plan's scope.

## Plan of Work

Milestone 1 is a half-day reading pass (the four reference workspaces +
the generator's page-shell emission) ending in recorded decisions — do
not start Milestone 2 without it; the embedding split (Milestone 3a) is
the riskiest piece and its shape depends on those decisions. Then work
the milestones in order; each lands green with the full lane set. All
contract surface is additive; existing page kinds compile unchanged.
Expect the spec-revision fallout on Milestone 2 (lock digests, fixture
clients, consumer lock) — the 0131 recipe applies.

## Concrete Steps

Lanes, fixture regeneration, and consumer verification identical to
ExecPlan 0131 `Concrete Steps`. Additionally for Milestone 5:

    go test ./internal/compiler -run TestWorkspace
    go test ./internal/generate -run TestWorkspace

Browser acceptance on the platform dev session once the pilot lands:
`/platform/sales` with tab deep links (`?tab=...`), per-tab state
retention across switches, counts in tab labels, and the standalone
routes gone.

## Validation and Acceptance

- Scenery lanes green per milestone; staged materialization passes.
- Fixture workspace proves: URL-synced tabs, lazy mount, state retention
  across switches, count badges, workspace + tab action merging, and that
  a referenced page compiles to both forms without duplication.
- Final acceptance is the Sales pilot passing its complete 0051-style
  feature inventory in the platform repo, with the hand-written page and
  route deleted.

## Idempotence and Recovery

Additive source kind; nothing existing changes behavior until a
`workspace_page` is authored. If the embedding split (Milestone 3) proves
wrong-shaped, the standalone page path is untouched — revert the
workspace kind alone. Spec-revision recovery per the 0131 recipe.

## Artifacts and Notes

- Requirements source: Micro platform ExecPlan 0051 (completed) — the
  multi-tab workspace entries in its Blocked list, and the page inventory's
  observation that domain tabs are the single most common generation
  blocker. Record the Milestone 1 design decisions and the fixture
  workspace's golden output pointers here as work proceeds.

## Interfaces and Dependencies

- New contract surface: `workspace_page` kind + `tab` children; embeddable
  (chrome-less) emission for `table_page`/`content_page`.
- Catalog: Astryx `TabList`/`Tab`, existing `Page`/`StatGrid`, 0131
  response contexts for counts; at most one thin new component.
- Depends on nothing from 0132/0133 but shares `QueryTable`-adjacent
  files with them — sequence merges, don't fork.
- Downstream: platform pilot plan (Milestone 6), then per-workspace
  conversion plans in the platform repo.
