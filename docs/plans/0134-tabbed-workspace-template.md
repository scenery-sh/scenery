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

- [x] (2026-07-22) Plan authored.
- [x] 2026-07-22 Milestone 1: design decision — embedding contract (chrome-less page
  rendering) and the `workspace_page` source-kind spelling, recorded here
  before code.
- [x] 2026-07-22 Milestone 2: compiler + schema — `workspace_page` kind, tab
  references, diagnostics.
- [x] 2026-07-22 Milestone 3: generator — embeddable tab components from existing
  page kinds; the workspace adapter (tab strip, URL state, lazy tabs,
  shared header/stats).
- [x] 2026-07-22 Milestone 4: catalog — `WorkspacePage` shell (Astryx `TabList`),
  tab-count badges from response metadata, slot pass-throughs.
- [x] 2026-07-22 Milestone 5: fixture + catalog contract demo.
  - [x] 2026-07-22 A table + content workspace with typed stats, counts,
    availability, and grouped sidebar presentation compiles in the house
    fixture.
  - [x] The catalog TypeScript contract fixture renders both sidebar and tab
    workspace inputs. The visible `/scenery-ui` consumer entry moves with the
    Milestone 6 platform pilot so it cannot drift from the real generated page.
- [x] (2026-07-22) Milestone 6: Sales, Vendors, and Documents are generated
  production workspaces with their handwritten route owners removed. Live
  browser proof exercised all seven Sales views, Vendors directory/scorecard
  state retention, Documents Files/Missing deep links and search retention,
  and the visible `/scenery-ui` Workspace template entry.
- [x] (2026-07-22) Review follow-up hard-loaded the two Sales `content_page`
  tabs through their workspace owner: `/platform/sales` rendered Teams data
  and `?tab=compensation` rendered its content. Neither remained blank, and
  the sibling generated table tabs stayed intact.

## Surprises & Discoveries

- (2026-07-22) The reference workspaces confirm that tab-local state is a
  real contract requirement: Sales has six read-only table views, Vendors
  keeps independent directory/scorecard controls, and Inventory carries
  substantial per-tab dialogs and scanner state. Remount-on-switch would be
  a functional regression.
- (2026-07-22) A catalog-owned embedding context is smaller than forking every
  generated page adapter into shell and content forms. The ordinary generated
  page mounts unchanged; inside `WorkspacePage`, its `Page` suppresses its
  shell and portals only active actions into the workspace header. Outside a
  workspace it retains its standalone behavior, while route generation omits
  embedded resources entirely.
- (2026-07-22) The first production pilot exposed two generator reachability
  edges that fixtures had not exercised: workspace stats bindings must be
  selected into React TypeScript clients, and a queryless complete-list table
  must mark its required `TablePageQuery` callback argument unused. Focused
  generator tests now lock both behaviors.
- (2026-07-22) Exact Documents parity required authored request-state copy.
  `table_page.loading_label` and `table_page.error_title` now flow through the
  generated adapter into `QueryState` while omitted values retain existing
  defaults.

## Decision Log

- (2026-07-22, Petr + agent)
  **Composition over expansion.** A tab references an existing
  `table_page`/`content_page` resource; the workspace does not inline a
  second copy of the table grammar. Rationale: every table capability
  (filters, pagination, slots, dialogs, export) keeps exactly one
  contract spelling and one generator path; tabs stay individually
  testable; a tab can later be promoted to a standalone route (or
  demoted) by moving one reference.
- (2026-07-22, Petr + agent) **Embedded pages surrender their own route and
  chrome.** A page referenced by a workspace tab does not register its own
  route or navigation entry. There is no dual-mount compatibility switch;
  authors create a distinct page resource if they genuinely need a second
  route.
- (2026-07-22, Petr + agent) **Lazy once, then keep alive.** The selected tab
  mounts on first visit; never-visited tabs neither mount nor query. A visited
  tab remains mounted but hidden so its search, filter, selection, expansion,
  dialogs, and scanner state survive switches. Its existing query observer may
  retain/refetch cached data; the contract does not promise suspended network
  activity for hidden visited tabs.
- (2026-07-22, agent) **Tab selection is URL state** (`?tab=<name>`, the
  first declared tab as default) so deep links and refresh keep the
  active tab — the hand-written workspaces' behavior.
- (2026-07-22, Petr + agent) **Counts come from workspace stats.** Optional
  `tab.count` names a typed field on the result of the workspace `stats`
  source. This gives the shell one bounded request and avoids mounting every
  tab merely to discover its count.
- (2026-07-22, Petr + agent) **Actions merge into one header.** Workspace
  actions are always visible; the active embedded page contributes its normal
  page actions to the same action area. Inactive-page actions are absent.
- (2026-07-22, Petr + agent) **The URL key is singular and fixed.** Workspaces
  use `?tab=<tab-name>`; it is not configurable per page.
- (2026-07-22, Petr + agent) **Only table and content pages embed.** `split_page`
  is excluded until a concrete consumer proves it is needed.
- (2026-07-22, Petr + agent) **One presentation field serves small and large
  workspaces.** `presentation = "tabs"` is the default; `"sidebar"` groups
  entries through `tab.group` and projects optional integer `count` and boolean
  `available` fields from the same typed stats result. There is no Governance-
  specific shell.
- (2026-07-22, agent) **Embedding is catalog context, not duplicated generated
  bodies.** This preserves one query/dialog/state implementation per child page
  and makes lazy-once keep-alive a property of the workspace shell. The route
  generator still enforces the product contract by registering only the
  workspace route for referenced pages.

## Outcomes & Retrospective

All milestones are complete. Scenery now has one typed `workspace_page`
contract, compiler validation/expansion, singular route generation, lazy-once
kept-alive child pages, fixed `?tab=` deep links, typed counts/availability,
merged active-child actions, and tab/sidebar Astryx-native presentations. The
house fixture proves table + content composition and the catalog contract
fixture typechecks the public shell. Sales, Vendors, and Documents proved the
contract in production: visited child state survives tab switches, `?tab=`
deep links select the intended child, and the generated workspace is the only
route owner.

## Context and Orientation

- **Page kinds today** (`internal/spec/source_schemas.go`,
  `internal/spec/schemas.go`, `internal/compiler/{table_page,content_page}.go`,
  `internal/generate/generate_typescript_react.go`): `table_page`,
  `split_page`, `content_page` each expand to route-owning page resources;
  generated adapters wrap content in the catalog `Page` shell
  (`fill` mode for tables) and register routes/nav via
  `routes.generated.ts`.
- **The generated table adapter** remains a single exported `<Name>Page`
  component that owns its client, query state, dialogs, and `Page` shell
  (see `renderReactTablePage`). Embedding uses the catalog workspace context:
  the same `Page` suppresses its shell only while mounted inside a workspace,
  so query/dialog/content generation is not duplicated. Route generation
  omits referenced child routes and imports those child components only into
  their owning workspace.
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
