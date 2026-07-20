# 0128 Workbench Table Pages: Stats Band, Declarative Filters, Status Maps, Row Detail, and Generated Form Dialogs

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as implementation proceeds.

## Purpose / Big Picture

Scenery generates three page kinds today — `table_page`, `split_page`, and
`content_page` (plans 0120, 0123) — and generates the route tree, navigation,
and app shell around them (plan 0126). The product direction (root `AGENTS.md`,
"Direction: fully generated clients") is that a client app's pages are mostly
declared in `.scn` with occasional `react_component` slot fills. The reference
consumer, the Micro platform app (`~/Repos/Micro/platform/apps/platform`),
shows how far we are from that: of its ~70 hand-written pages (~47,000 lines
under `src/pages/`), exactly one production route uses a generated page kind
in earnest (the mails `split_page`).

A census of every page in that app (2026-07-20, recorded in Artifacts and
Notes below) shows one archetype dominates: the **ops workbench**. Roughly
20 pages — about half the app's page line count — are the same design with
different domain vocabulary: a stat summary band, a filter toolbar (search +
enum selects + result count + export), a data table with badge columns and
expandable rows, and create/edit form dialogs. Examples: `work-orders.tsx`,
`inventory.tsx`, `invoices.tsx`, `permits.tsx`, `tickets.tsx`, `ntp.tsx`,
`warranty.tsx`, `vendors.tsx`, `service-calls.tsx`, `fleet.tsx`,
`engineering.tsx`, `funding.tsx`, `commissions.tsx`, `sales.tsx`,
`edge.tsx`, `job-costing.tsx`, `queue.tsx`, `change-orders.tsx`,
`saved-searches.tsx`, `warranty-claims.tsx`.

The single generated `table_page` in that app today
(`projects/scenery.package.scn`, mounted at `/projects/generated`) emits
`filters={[]}` and no stats — the declaration surface exists but cannot yet
express the archetype, so no production page has moved onto it.

This plan makes exactly two additions, chosen because they compose to
convert that archetype and nothing else:

1. **`table_page` grows into the full workbench.** Declared in `.scn`:
   a stats band binding a metrics record to `StatTile`s above the table;
   declarative filters (search plus enum-backed selects wired to the list
   operation's existing query parameters); declarative status maps
   (status → `{label, variant}`) reused by badge columns; a row detail slot
   (expandable row rendering a `react_component`); and toolbar extras
   (result count, export action). Catalog work is limited to a new
   `FilterToolbar` component and expandable-row support in `QueryTable` —
   everything else already exists (`StatTile`/`StatGrid`, `StatusBadge`,
   `Page`, shared request state).
2. **`form_dialog` generation.** After tables, dialogs are the biggest line
   sink in the app (`inventory.tsx` has 13 `FormDialog` usages,
   `invoices.tsx` 7, `ntp.tsx` 5). The catalog `FormDialog`/`Field`
   primitives exist; what's missing is generating the dialog from a record
   type plus a mutation binding: fields from the record schema, enum →
   `SelectField`, submit → mutation → query invalidation. This composes
   with every workbench page.

Other archetypes found in the census (tabbed record detail, dashboards,
kanban boards) are deliberately **out of scope**; they are recorded in
Artifacts and Notes as candidate follow-on plans.

Success is observable in the Micro platform app only when the generated
work-orders page is functionally identical to the hand-written production
page. Milestone 4 keeps `work-orders.tsx` on `/work-orders` and the generated
candidate on `/work-orders/generated` until a feature-by-feature inventory,
focused tests, and authenticated browser acceptance prove parity. Green
typecheck/lint/test/build lanes and an HTTP 200 are necessary but are not
cutover evidence by themselves.

## Progress

- [x] M1: `status_map` resource kind end to end
- [x] M2: workbench `table_page` (stats band, declarative filters via
      `FilterToolbar`, badge columns via status maps, row detail slot,
      toolbar count/export)
- [x] M3: `form_dialog` resource kind and page/row action wiring
- [ ] M4: docs, SKILL.md, cookbook, conformance fixtures, and a functionally
      identical Micro platform work-orders pilot conversion
- [ ] Final validation matrix and feature-by-feature browser acceptance green

2026-07-20: Plan created from a Micro platform page census; no
implementation started.
2026-07-20: Scope narrowed by the maintainer to the workbench `table_page`
and `form_dialog` only; record detail, dashboard, and board page kinds were
removed from this plan and noted as follow-on candidates.
2026-07-20: Implemented M1–M3 across the current source catalog, compiler,
CRUD runtime, generated React adapters, and Astryx catalog. Focused Go tests
and managed catalog TypeScript compilation pass.
2026-07-20: Completed the Micro pilot declaration, metrics and audited
quick-create handlers, generated route/client, and typed row-detail slot.
Deleted the replaced 1,377-line hand-written work-orders page and its
presentation helpers after app typecheck, lint, 97 tests, and production
build passed.
2026-07-20: Re-read the maintainer's filter-presentation update and aligned
the completed implementation: only `pinned = true` selectors are duplicated
inline, the Filters popover remains the complete filter editor, active values
render as removable chips, and sort/direction remain separate always-visible
query controls.
2026-07-20: Final Scenery and Micro validation passed. The regenerated Micro
runtime reached detached readiness and served
`https://micro.scenery.sh/platform/work-orders` with HTTP 200.
2026-07-20: Reopened M4 after comparing the deleted page to the generated
replacement. The earlier acceptance covered only a subset of the production
workflow and therefore did not establish parity. Restored the exact
hand-written page and helpers from Micro commit `5612e2a`, restored its
`/work-orders` route and navigation entry, and moved the generated candidate
to the non-navigation route `/work-orders/generated`.

## Surprises & Discoveries

Recorded from the motivating census (2026-07-20), before implementation:

- One generated `table_page` exists in Micro platform
  (`projects/scenery.package.scn`, `table_page "projects"` at
  `/projects/generated`) and its generated adapter emits `filters={[]}` and
  no stats — the declaration surface exists but no production page has moved
  onto it, because the archetype needs stats, filters-with-labels, badges,
  and row expansion to be competitive with the hand-written pages.
- The filter query parameters the workbench needs are already declared per
  list binding in the app's `.scn` sources — e.g.
  `tickets/scenery.package.scn` binds `page`, `page_size`, `search`,
  `status`, `category`, `priority`, `rep_type`, `project_id`, `sort`,
  `direction` on `ticket_list_http`. The gap is purely presentational:
  nothing declares which parameters surface as toolbar controls, with what
  labels and option sets.
- Every workbench page hand-writes the same `StatusMap` objects (e.g.
  `workOrderStatusBadges`, `workOrderPriorityBadges` in
  `apps/platform/src/pages/work-orders.tsx:67-80`) mapping status values to
  `{label, variant}`. The same maps are re-declared wherever the same status
  renders. This is exactly the kind of contract that belongs in `.scn`.
- Form dialogs are the second-largest line sink after tables:
  `inventory.tsx` contains 13 `FormDialog` usages, `invoices.tsx` 7,
  `ntp.tsx` 5, `warranty-claims.tsx` 5, `work-orders.tsx` 3. All follow the
  same shape: fields from a record, enum selects, submit → mutation →
  invalidate the page query.
- Workbench stat bands cannot be computed client-side from the visible page:
  generated table pages fetch paged data, while e.g. `workOrderStats(orders)`
  today runs over the full unpaged list. The stats band must bind to a
  server-side metrics operation (the app already has this pattern:
  `record "project_command_metrics"` in `projects/scenery.package.scn`).

Add new discoveries here with evidence as implementation proceeds.

- The pilot's existing `GET /projects/work-orders` returns one aggregate
  dashboard document with orders and crews, not a cursor-paged entity list.
  A generated workbench cannot truthfully reuse that endpoint as a CRUD list.
  The pilot therefore declares a read-only `work_order_list` entity/CRUD
  projection over the same table while the existing audited command methods
  remain the mutation owner.
- Search was not part of the pre-plan CRUD list contract. Adding a toolbar
  text input without a server capability would have searched only one loaded
  page, so `crud.list.search` now allowlists string fields and participates in
  the cursor fingerprint. `datasource` escapes `%`, `_`, and the escape
  character before applying case-insensitive substring matching.
- Generated Go contracts are cache-only in current Scenery. Running generation
  updated the ownership-verified external editor generation and root `go.work`;
  the pilot adds only app implementation files and committed TypeScript output,
  not visible `scenerycontract` directories.
- `QueryTable` already owned controlled list search/filter/sort/pagination
  state before this plan. Keeping that state with the catalog component
  preserves the current 0123 boundary; the generated adapter owns only
  domain binding translation, stats, header actions, and mutations.
- Every source-schema change resets the current catalog revision and therefore
  the built-in provider descriptor digest. The stable digest tests and the
  pilot lockfile were updated together so old state fails closed rather than
  selecting another specification.
- The pilot database contains both `complete` and historical `completed`
  status values, and stores an unset scheduled date as an empty string.
  The current declaration models those actual wire values explicitly so the
  generated client does not hide or reject existing rows.
- The first toolbar implementation rendered every generated enum selector
  inline. Re-reading the maintainer's recorded presentation decision caught
  that drift before final validation; the catalog now treats pinning as
  duplicated quick access while keeping one complete popover editor.
- Ordinary TypeScript accepted the theme-scoped semantic token aliases used
  by the light/dark acceptance fix, while Scenery's managed checker rejected
  their branded cross-var types. Keeping the cast at the one StyleX theme
  boundary made the checker, Vite transform, and runtime theme behavior agree.
- The original 97-test green run was a false cutover signal: deleting
  `work-orders.test.ts` removed four focused tests, and the acceptance list
  covered only the generated subset. The generated candidate was missing
  project name/link navigation; detailed assignment, status, type, and
  priority presentation; checklist progress/toggle/add/delete; notes and
  time-on-site editing; customer signature collection and signed state;
  status transitions and cancellation; inspection-photo requirements;
  create-dialog project search, live crew selection, native date input,
  default-checklist preview/toggle, custom checklist items; and full filtered
  export. Restoring the page brings the frontend suite back to 101 tests.

## Decision Log

- 2026-07-20, maintainer: scope this plan to the workbench `table_page` and
  `form_dialog` generation only. Record detail (`record_page`), dashboards
  (`dashboard_page`), and boards (`board_page`) were considered in an
  earlier draft and cut; they convert smaller page families and can be
  planned separately once the workbench ships.
- 2026-07-20, agent (proposed, pending maintainer review): grow `table_page`
  into the workbench rather than adding a new `workbench_page` kind. Plan
  0123 already decided `table_page` stays as authored sugar over
  `content_page` + `QueryTable`; the workbench is the same archetype with
  more optional blocks (`stats`, `row_detail`, toolbar extras), not a new
  layout. A page that declares none of the new blocks generates exactly
  today's output.
- 2026-07-20, agent: model status presentation as a standalone `status_map`
  resource (status value → label + variant) rather than attaching badge
  metadata to `enum` values. Rationale: the same enum renders differently in
  different contexts (priority `high` is `orange` in a table but may be
  plain text in an export), status fields in existing CRUD contracts are
  frequently `string` rather than `enum`, and a standalone resource can be
  referenced by column blocks (and future consumers) without entangling the
  type system. The variant vocabulary is the catalog `StatusBadge` variant
  set.
- 2026-07-20, agent: `form_dialog` is a resource, not a page kind. Dialogs
  have no route; they are opened by a page action or row action and their
  lifecycle (open state, submit, invalidate) is generated into the owning
  page's adapter.
- 2026-07-20, agent: `FilterToolbar` is a controlled catalog component and
  `QueryTable` owns search/filter/sort/pagination state and query wiring.
  The generated adapter stays responsible for domain binding translation,
  the surrounding `Page` shell, stats, header actions, and mutations.
  `FilterToolbar` receives typed filter descriptors and values through props.
- 2026-07-20, agent: the reusable `table_page` default export acts on the
  currently loaded, filtered rows and is generated client-side as CSV; docs
  label it as such. That default is not sufficient for the Micro cutover:
  the generated candidate must bind an explicit operation or extension that
  preserves the production page's full filtered-dataset export.
- 2026-07-20, agent: add `crud.list.search` as the only new data capability
  needed by the workbench. It is an explicit string-field allowlist, not a
  generic full-text framework, and it binds cursors to the normalized search
  value.
- 2026-07-20, agent: keep query controls and list request state in the existing
  catalog `QueryTable`; keep metrics queries and dialog mutations in generated
  adapters. This corrects the draft's claim that adapters already owned list
  state without creating a second controlled/uncontrolled table API.
- 2026-07-20, agent: `row_detail.dialog` is the singular row mutation surface.
  The compiler requires every dialog input field to have a type-compatible row
  field; generation seeds those values and renders one secondary edit action
  in the expanded row.
- 2026-07-20, agent: the Micro pilot uses a dedicated list-only CRUD projection
  and delegates its generated quick-create handler to the existing audited
  `CreateWorkOrder` service method. This preserves existing work-order command
  policy, checklist defaults, and audit writes while replacing only UI/query
  boilerplate.
- 2026-07-20, maintainer: `FilterToolbar` presentation is "quiet row +
  Filters popover + active-filter chips". Four directions were mocked
  against the pipeline page's toolbar (the densest in the reference app:
  12+ controls in one row across three control metaphors,
  `apps/platform/src/pages/pipeline.tsx:102-235`): (A) quiet row with a
  Filters-button popover and removable active-filter chips, (B)
  Linear-style filter tokens added via a command menu, (C) a facet rail
  with per-value counts, (D) everything visible in two labeled bands. A
  was chosen: calm default, filter state always readable as chips, scales
  to any facet count, and one presentation serves both simple workbenches
  and facet-heavy pages like pipeline. Consequences: `filter` blocks gain
  an optional `pinned = true` attribute (pinned filters render as inline
  selects before the Filters button); C is off the table for this plan
  (it would require facet counts in the CRUD list contract); D's grouping
  may return later as popover section headers, not as a separate
  presentation.
- 2026-07-20, agent: pinning is duplicated quick access, not a second filter
  set. Pinned selectors render inline and remain in the complete Filters
  popover. Sort and direction stay immediately after that button and never
  contribute to its active count or chips.
- 2026-07-20, maintainer: there is no generated-page cutover until the
  replacement is identical in functionality. A production hand-written route
  must remain active while its generated candidate is mounted separately.
  Deletion requires an explicit feature inventory, focused regression tests
  that remain after cutover, and authenticated browser proof of every
  workflow; green build lanes or subset acceptance cannot waive that gate.

## Outcomes & Retrospective

M1-M3 shipped the reusable workbench primitives:

- `status_map` is reusable, compiler-validated presentation metadata emitted
  as typed generated constants;
- `table_page` now composes server metrics, declared finite filters and
  ordering, active-filter chips, badge columns, row expansion, typed detail
  slots, current-page CSV, empty actions, and generated dialog actions;
- `form_dialog` derives typed controls and mutation lifecycles from a current
  binding contract, keeps failures inline, and invalidates list/stats data;
- CRUD list search is an explicit string allowlist whose normalized value
  participates in cursor identity.

M4 remains open. The Micro pilot is deliberately side by side:

- `/work-orders` is the restored 1,377-line production page from Micro commit
  `5612e2a`;
- `/work-orders/generated` is the generated candidate and has no navigation
  entry;
- the production page has live authenticated browser proof for project
  navigation, detailed assignment metadata, checklist operations, notes and
  time-on-site editing, signature collection, transitions/cancellation, and
  the full create form;
- cutover is blocked until the generated candidate proves the complete
  feature inventory below and the hand-written page can be deleted without
  deleting its focused parity tests.

The main implementation lesson was to keep query capability and presentation
separate: `.scn` declares what can be searched, filtered, and ordered;
`QueryTable` owns the controlled query state; `FilterToolbar` owns only the
chosen quiet-row/popover/chips presentation.

## Context and Orientation

Terms:

- **Catalog**: `ui/` in this repository, the single editable source of
  `@scenery/ui`, embedded via `ui/embed.go` and materialized into
  React-enabled TypeScript clients under `react/scenery-ui/`
  (`ui/AGENTS.md`). Components are Astryx + StyleX, domain-free, composed
  through typed props and slots.
- **Page kinds**: authored source kinds (`table_page`, `split_page`,
  `content_page`) that the compiler expands into ordinary `scenery.page` +
  `scenery.renderer` resources. Source schemas live in
  `internal/spec/source_schemas.go` (schema map at the `"table_page"`,
  `"split_page"`, `"content_page"` entries); expansions live in
  `internal/compiler/table_page.go`, `split_page.go`, `content_page.go`;
  route/search handling in `internal/compiler/page_route.go`; React emission
  in `internal/generate/generate_typescript_react.go`.
- **CRUD list contract**: `crud` resources with a `list` block generate list
  operations/bindings with filter/sort allowlists; `table_page.source` must
  resolve to one (`internal/compiler/table_page.go`, diagnostic SCN2608).
- **Reference consumer**: the Micro platform app at
  `~/Repos/Micro/platform` (separate repository). Its
  `envs.local.ui_catalog` points at this repo's `ui/`, so catalog edits
  reach its browser via HMR without rebuilding the binary (plan 0122). Its
  page census and archetype evidence are in Artifacts and Notes.

Current `table_page` authored surface (for the M2 delta): blocks `column`
(attrs `label`, `appearance` ∈ auto|text|number|datetime|badge,
`component`), `filter` (attrs `label`, `component`; must name
CRUD-allowlisted fields, SCN2610), `sort`, singleton slots `toolbar` and
`empty`, repeated `search`. The expansion emits a `scenery.page` with the
list binding as `load` and a `scenery.renderer` with module
`scenery.ui.table_page`; the React generator composes catalog `Page` +
`QueryTable` (chrome-less per `ui/AGENTS.md`).

Catalog components that already exist and are reused by this plan:
`QueryTable`, `FormDialog`/`Field`/`TextField`/`SelectField`/
`TextAreaField`, `QueryState` + shared `Problem`/`RequestState`
(`ui/components/request-state.ts`), `StatTile`/`StatGrid`, `StatusBadge`
(takes a `StatusMap` of `status → {label, variant}`), `Page`/`PageShell`,
`EmptyState`. New catalog work in this plan: `FilterToolbar` and
expandable-row support in `QueryTable` — nothing else.

The target authored shape, sketched against the real Micro platform
work-orders page (directional; exact attribute names are settled per
milestone against `internal/spec` conventions):

    status_map "work_order_status" {
      status "draft"       { label = "Draft"       variant = "neutral" }
      status "assigned"    { label = "Assigned"    variant = "neutral" }
      status "in_progress" { label = "In Progress" variant = "neutral" }
      status "complete"    { label = "Complete"    variant = "green" }
      status "cancelled"   { label = "Cancelled"   variant = "error" }
    }

    form_dialog "work_order_create" {
      source = binding.work_order_create_http   # mutation binding
      title  = "New Work Order"
      field "project_id" { label = "Project" }
      field "type"       { label = "Type" }     # enum → SelectField
      field "priority"   { label = "Priority" }
      field "notes"      { label = "Notes" control = "textarea" }
    }

    table_page "work_orders" {
      path   = "/work-orders"
      source = crud.work_orders
      title  = "Work orders"

      stats {
        source = binding.work_order_metrics_http   # returns a metrics record
        tile "open"            { label = "Open" }
        tile "in_progress"     { label = "In Progress" }
        tile "completed_today" { label = "Completed Today" }
        tile "total"           { label = "Total" }
      }

      column "wo_number" { label = "WO#" }
      column "status"    { label = "Status" appearance = "badge" status_map = status_map.work_order_status }
      filter "status"    { label = "Status" status_map = status_map.work_order_status }
      filter "type"      { label = "Type" }
      action "create"    { label = "New Work Order" dialog = form_dialog.work_order_create }
      row_detail { component = react_component.work_order_row_detail }
    }

### Presentation contract (target look and Astryx usage)

The generated workbench must be visually competitive with the hand-written
pages it replaces, or the conversions will stall. The reference look is
`apps/platform/src/pages/work-orders.tsx` (line references in Artifacts and
Notes); this section pins the composition to blessed catalog exports so M2
implementation and review have a concrete target. All components below are
already exported from `ui/index.ts` unless marked new.

Page composition, top to bottom inside the catalog `Page` shell:

1. **Page header** (existing `Page` title + actions): declared `action`
   blocks render as Astryx `Button` in the header actions slot — the
   primary action (one with a `dialog` ref marked primary, or the first)
   as `variant="primary" size="sm"` with a leading icon, the rest
   `variant="secondary"`. Matches work-orders' "New Work Order" button
   (`work-orders.tsx:120-128`).
2. **Stats band**: `StatGrid` with `columns` equal to the declared tile
   count (the census norm is 3–5), each tile a `StatTile` with the declared
   label and the raw server value from the metrics record. No trend/delta
   affordance in this plan. Sits between the header and the toolbar, full
   content width (`work-orders.tsx:136-143`).
3. **`FilterToolbar`** (new catalog component), in the "quiet row +
   Filters popover + chips" presentation (Decision Log, 2026-07-20; three
   alternatives were mocked and rejected — see Artifacts and Notes). One
   calm horizontal row built from `HStack` with the catalog's standard
   control gap (spacing-2/spacing-3 as used in `QueryTable` today):
   - Left group: one Astryx `TextInput` for search — `size="sm"`,
     `isLabelHidden` with an `aria` label from the page title, clear
     affordance (`hasClear`), leading search `Icon`, fixed width in the
     240px range — followed by `Selector`s for *pinned* filters only
     (a filter block may declare `pinned = true`; the census norm is
     zero to two pinned filters per page).
   - A **Filters button**: `Button variant="secondary" size="sm"` with a
     filter `Icon`, label "Filters", and a count badge showing how many
     filters are active. It opens a popover listing every declared
     filter with its current value — including filters duplicated inline
     through `pinned = true` — as generated enum/`status_map` selectors or
     exact typed app-owned controls.
   - Declared sort field and direction selectors remain always-visible query
     controls immediately after the Filters button. They are ordering state,
     not filter state, so they do not produce chips or contribute to the
     Filters count.
   - Right group, pushed to the row end: the result count as
     `Text type="supporting" color="secondary"` (e.g. "12 work orders",
     singular/plural handled), then the export `Button`
     `variant="secondary" size="sm"` with a download `Icon`.
   - A **chips row** rendered under the toolbar only when filters are
     active: one removable chip per active filter reading
     "Label · Value ×", plus a "Clear all" affordance. Chips are the
     always-visible summary of filter state; the popover is where state
     is edited.
4. **Table**: the existing `QueryTable` grid. Badge columns render
   `StatusBadge` with the generated status-map constant — the variant set
   is the catalog `StatusBadge` vocabulary (`neutral`, `green`, `orange`,
   `error`, …), which already matches the colors the hand-written pages
   use. Expandable rows add a narrow leading chevron column: an
   `IconButton` with the Astryx chevron-right/chevron-down `Icon`,
   `aria-expanded` set, one row expanded at a time (controlled by the
   generated adapter). The expanded detail row spans all columns and
   renders the `row_detail` slot inside a padded container (spacing-4
   padding, muted surface and top border from semantic tokens) so app slot
   content sits visually inside the table.
5. **Empty states**: reuse catalog `EmptyState`/`TableEmptyRow`,
   distinguishing "no rows match these filters" (filters active) from "no
   rows yet" plus the primary create action when a create dialog is
   declared — the distinction the hand-written pages already make
   (`work-orders.tsx:242-255`).
6. **Form dialogs**: the existing catalog `FormDialog` look — title,
   vertical field stack, primary submit / secondary cancel. Field mapping:
   string → `TextField`, `control = "textarea"` → `TextAreaField`,
   enum-typed → `SelectField` with options labeled from the enum or a
   `status_map`. Submit failures render the shared `Problem` inline in the
   dialog, not as a toast.

Cross-cutting rules, restating `ui/AGENTS.md` obligations that apply here:
new catalog code uses only Astryx components plus semantic tokens imported
as `t` from `@scenery/ui/tokens.stylex` (and Astryx spacing vars where
`QueryTable` already does) — no hardcoded hex, so light and dark themes work
without additional effort. Catalog components take icons as Astryx
`IconType` names; the `.scn` `action`/export icon attributes validate
against that set (app-side pages use lucide today — that stays an app
concern, catalog components do not import lucide). Toolbar controls are
uniformly `size="sm"` to match the density of the hand-written workbenches.
`FilterToolbar` is a controlled, fetch-free component: typed filter
descriptors and current values in via props, change events out; the
generated adapter owns state and query wiring.

## Milestones

Each milestone is additive, leaves `go test ./...` green, and regenerates
conformance fixtures in the same change.

**M1 — `status_map` resource kind.** New source schema + resource kind
`scenery.status-map`; validation (unique statuses, non-empty labels, variant
within the catalog `StatusBadge` variant set); generator emits status maps
into the TypeScript client as typed `StatusMap` constants. No consumer yet;
`table_page` badge columns keep current auto behavior until M2.

**M2 — workbench `table_page`.** Extend the `table_page` schema with:

- a singleton `stats` block (attrs: `source` referencing a binding whose
  success result is a flat record of numeric/string tiles; repeated `tile`
  blocks naming record fields with labels), rendered as a `StatGrid` band
  above the table;
- a `status_map` attribute on `column` (valid only with
  `appearance = "badge"`) and on `filter` (supplying select option labels
  for string-typed fields; enum-typed fields derive options from the enum
  and may use a `status_map` for labels);
- a singleton `row_detail` slot whose `react_component` receives the typed
  row, rendered as an expandable row;
- toolbar result count and a declared `export` action (client-side CSV of
  the currently loaded filtered rows).

Filters surface through `FilterToolbar` in the chosen popover-plus-chips
presentation (search input, pinned-filter selects, Filters button with
active count opening a popover over all declared filters, active-filter
chips row) wired to the list operation's existing query parameters — the
CRUD-allowlist validation (SCN2610) already guarantees each filter names a
real list parameter, and `filter` blocks gain an optional `pinned`
attribute. Catalog work: `FilterToolbar` (controlled component, no fetch
logic — typed filter descriptors and values in, change events out) and
expandable-row support in `QueryTable` (chevron column, controlled
expanded-row state, `rowDetail` render slot typed by row), both built to
the Presentation contract in Context and Orientation.
`StatGrid` band and toolbar compose in the generated adapter, not inside
`QueryTable`, which stays chrome-less per `ui/AGENTS.md`.

**M3 — `form_dialog`.** New resource kind `scenery.form-dialog`: `source` is
a mutation binding; fields derive from the binding's input record; per-field
`field` blocks override label/control/options (enum-typed fields render
`SelectField`; a `status_map` may supply option labels); required/optional
follows the record type (`optional(...)` fields are optional inputs). Page
wiring: `table_page` gains repeated `action` blocks (label, icon, `dialog`
ref) rendered as page actions, and `row_detail` may reference an edit dialog
seeded from the row. The generated adapter owns open state, submit via the
generated client, TanStack mutation + invalidation of the page's list query,
and problem display inside the dialog using the shared `Problem` vocabulary.

**M4 — docs, fixtures, and the Micro platform pilot.** Update `SKILL.md`,
`docs/agent-guide.md`, `docs/local-contract.md`,
`docs/app-development-cookbook.md`, `docs/spec/` conformance fixtures,
`ui/AGENTS.md`, and `docs/knowledge.json` for `status_map`, the extended
`table_page`, and `form_dialog`. In Micro platform: convert the work-orders
page (workbench + create dialog + row-detail slot) behind
`/work-orders/generated`. Keep the hand-written `/work-orders` page and
navigation entry until every item in the acceptance inventory below passes
focused tests and authenticated browser acceptance. Only then move the
generated page to `/work-orders`, remove the handwritten implementation, and
rerun the complete app verification.

## Plan of Work

Work proceeds milestone by milestone; within each milestone the order is:
source schema (`internal/spec/source_schemas.go`, metadata, diagnostics
catalog entries with new SCN codes) → compiler expansion + validation
(`internal/compiler/<kind>.go` + tests, following the existing
`expandTablePageResources` shape: resolve refs, emit `scenery.page`/
`scenery.renderer`/auxiliary resources with expansion lineage, collision
diagnostics) → catalog components (`ui/components/`, exports in
`ui/index.ts`, `ui/embed.go` already covers `components/`) → React
generation (`internal/generate/generate_typescript_react.go` + staged
fixture-client compilation in `internal/generate/testdata/`) → docs and
conformance fixtures.

Catalog development runs live against Micro platform via `ui_catalog`
(plan 0122): with `scenery up` running in that app, edits under `ui/` here
re-materialize within about a second, so `FilterToolbar` and the
expandable-row `QueryTable` are validated against real domain data before
the generator work lands.

The generated adapter (not the catalog) owns: the stat-band query and
`StatGrid` composition, filter state and its wiring to list query
parameters, toolbar count/export, dialog open state and mutations. The
catalog owns only reusable presentation with typed props and slots. This
mirrors the 0123 split ("generated `table_page` adapters own the
surrounding `Page` shell").

Slot typing follows the existing pattern (`defineTablePageSlots` in
`ui/components/QueryTable.tsx`): the `row_detail` slot and any new slot
surfaces extend the existing `TablePageSlots` helper so slot fills are typed
against the generated row types.

## Concrete Steps

All commands run from this repository's root unless stated.

1. **M1.** Add `statusMapSourceSchema` and the `"status_map"` entry in
   `internal/spec/source_schemas.go`; register metadata in
   `internal/spec/source_schema_metadata.go` and diagnostics in
   `internal/spec/diagnostics_catalog.go` (allocate the next free SCN26xx
   codes; check `internal/spec/catalog_test.go`). Add
   `internal/compiler/status_map.go` validation (no expansion needed —
   status maps are referenced, not expanded) and tests. Emit TypeScript
   constants in `internal/generate/generate_typescript_react.go`; assert in
   `internal/generate/generate_test.go` fixtures. Run `go test ./internal/...`.
2. **M2.** Extend `tablePageColumnSourceSchema` and
   `tablePageFilterSourceSchema` (`status_map` attr), add
   `tablePageStatsSourceSchema` + `tile` child schema and the `row_detail`
   slot to the `"table_page"` schema map entry. Validate in
   `internal/compiler/table_page.go`: stats source resolves to a binding
   whose success record contains every named tile field; `status_map` only
   on badge columns; filter `status_map` only on string/enum fields;
   row-detail component exists (reuse `validateTablePageComponent`).
   Catalog: `ui/components/FilterToolbar.tsx` and expandable-row support in
   `ui/components/QueryTable.tsx`; export both from `ui/index.ts`.
   Generator: emit the stats query + `StatGrid` band, `FilterToolbar`
   wiring, count text, export action, `StatusBadge` cells bound to
   generated status-map constants. Verify catalog typing with
   `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
   and `go test ./internal/generate`.
3. **M3.** Add `formDialogSourceSchema` + `field` child schema and compiler
   `internal/compiler/form_dialog.go` (source must be a mutation binding;
   fields must name input-record fields; control values within the catalog
   field set). Add `action` blocks to `table_page` (extend
   `pageActionSourceSchema` usage or add a table-page action schema with a
   `dialog` ref). Generator: per-dialog component in the page adapter using
   catalog `FormDialog`, TanStack `useMutation`, list-query invalidation.
4. **M4.** Docs sweep (files listed in Milestones), `docs/spec/` conformance
   updates per `docs/spec/AGENTS.md`. Micro platform pilot:
   in `~/Repos/Micro/platform` declare the work-orders metrics
   operation/binding if not present, write the
   `status_map`, `form_dialog`, and `table_page` declarations plus the
   row-detail slot module at `/work-orders/generated`, then run generation,
   app typecheck/lint/test/build, and the complete side-by-side acceptance
   inventory. Move it to `/work-orders` and delete the hand-written page only
   after every item is proven identical, then rerun `make verify`.

## Validation and Acceptance

Scenery-side, after every milestone:

    go test ./...
    go install ./cmd/scenery
    scenery harness self -o json --write
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./internal/generate

Frontend lanes for catalog changes: `bun run typecheck` and `bun run build`
in `apps/console` (the in-repo consumer), plus the staged fixture-client
compilation above.

App-side acceptance (M4, in `~/Repos/Micro/platform`): `scenery generate
--check` clean; typecheck/lint/test/build green; and side-by-side acceptance
between `/work-orders` and `/work-orders/generated` proves all of:

- four stat tiles with server values; server-backed search/status/type
  filters; status/priority badges; full filtered-result count and export;
- project name plus clickable project navigation; complete assignment,
  status, type, priority, schedule, description, special-instruction, and
  inspection-photo-requirement presentation;
- checklist progress plus completion toggle, per-item notes, add, and delete;
  notes and time-on-site editing; customer signature collection and signed
  state; all valid status transitions and cancellation;
- create-dialog project search/selection, live crew selector, native date
  input, type and priority, description and instructions, default-checklist
  preview/toggle, and custom checklist items;
- mutation persistence, query invalidation without reload, loading/empty/error
  behavior, keyboard/focus behavior, and correct light/dark semantic-token
  surfaces.

The focused parity tests must survive deletion of the handwritten page.
Only after this inventory is identical may the generated route claim
`/work-orders` and its navigation entry. Until then the handwritten page is
the production implementation and the generated page is a non-navigation
candidate.

The plan's structural contract is validated by `scenery harness self`
(required ExecPlan sections).

## Idempotence and Recovery

Every milestone is a normal additive code change; re-running generation and
tests is idempotent. Schema additions are new optional blocks/attributes, so
existing `.scn` sources (including Micro platform's current `table_page`,
`split_page`, `content_page` declarations) compile unchanged at every
intermediate state. If a milestone must be abandoned mid-way, the new source
kind simply remains unreferenced; no data or migration state is involved.
The Micro platform pilot is recoverable without a revert because the
hand-written production page remains active while the generated candidate
uses a separate non-navigation route. Per repo policy (Decision Log of
0123), no back-compat shims: if a schema shape needs revision mid-plan,
update the spec, fixtures, and current consumers in the same change.

## Artifacts and Notes

Micro platform page census (2026-07-20, `apps/platform/src/pages/`, ~70
files, ~47,000 lines). Component-usage profile per page (grep of catalog
component identifiers) grouped into archetypes:

- Workbench (StatGrid/StatTile + filters + DataTable + FormDialog) — the
  target of this plan: inventory (2,202 lines, 13 StatTile / 13 FormDialog /
  5 DataTable), funding (1,813), commissions (1,714), invoices (1,527),
  permits (1,428), job-costing (1,419), change-orders (1,396), work-orders
  (1,377), fleet (1,287), engineering (1,285), tickets (1,266), vendors
  (1,172), ntp (887), queue (875), edge (853), warranty-claims (719), sales,
  service-calls, warranty, saved-searches.
- Out of scope for this plan, candidate follow-on plans: record detail
  (`project-detail-dialog.tsx`, 1,064 lines, 8 tabs, opened from nearly
  every workbench), dashboards (`command.tsx`, `mobile-leadership.tsx`,
  `analytics-*-sections.tsx`; note `analytics-ui.tsx` contains
  catalog-shaped viz primitives — `MetricStrip`, `BigValue`, `Bar`,
  `StageBars`, `CountBars`, `Panel`, `Cards` — promotable if a
  `dashboard_page` kind is ever planned), kanban boards
  (`projects-board.tsx`, `pipeline.tsx`, `crew.tsx`), and genuinely bespoke
  pages (calendar, map, scanner, login), which remain hand-written pages
  registered against the generated route tree.

Anatomy reference for the workbench archetype:
`apps/platform/src/pages/work-orders.tsx` — `PageShell` + primary action
(117–128), `QueryState` (130), `StatGrid` band (136–143), toolbar with
search/`Selector` filters/count/export (144–196), `DataTable` with badge
columns and chevron expand (257–317), `ProjectDetailDialog` +
`NewWorkOrderDialog` (212–222), `workOrderStatusBadges` status maps (67–80).

Filter-presentation study (2026-07-20): four toolbar directions were mocked
with the pipeline page's real vocabulary (portfolio, PM, financier, AHJ,
utility, aging buckets, blocked flag, Days/Name/$/Cycle sort) — quiet row +
popover + chips, filter tokens + command menu, facet rail with counts, and
two labeled bands. The maintainer chose the first; the rendered mockups
live in the design artifact "ExecPlan 0128 — Workbench Table Pages"
(claude.ai artifact, section "Filter & sort — four directions"), and the
decision with trade-offs is in the Decision Log.

Filter-parameter evidence: `tickets/scenery.package.scn` binds `search`,
`status`, `category`, `priority`, `rep_type`, `project_id` (plus paging and
sort) as query parameters on `ticket_list_http` — the workbench filter
surface maps one-to-one onto parameters the contracts already declare.

Existing `.scn` UI declarations in that app:
`projects/scenery.package.scn` `table_page "projects"` (columns with
`appearance = "badge"`, sorts, no filters/stats);
`mail/scenery.package.scn` `split_page "mails_next"` and `content_page
"inbox_summary"` with `react_component` slot fills — the slot-fill pattern
the workbench blocks must match.

## Interfaces and Dependencies

Upstream (consumed): plan 0123's page-kind architecture (`content_page`
shell, chrome-less `QueryTable`, shared `Problem`/`RequestState`,
`defineTablePageSlots`); plan 0126's generated route tree/nav/shell (the
extended `table_page` keeps emitting route descriptors with path + search
schema so it mounts in the generated tree); plan 0120's CRUD list contract
(workbench filters/sorts remain CRUD-allowlisted); plan 0122's `ui_catalog`
live iteration for catalog development against Micro platform.

Touched interfaces: `internal/spec` source schemas + diagnostics catalog
(new kinds `status_map` and `form_dialog`; extended `table_page`);
`internal/compiler` validation/expansion (`table_page.go`, new
`status_map.go`, `form_dialog.go`); `internal/generate/
generate_typescript_react.go` and its fixture clients; `ui/` catalog
surface (`FilterToolbar`, expandable rows in `QueryTable`, exports in
`ui/index.ts`); docs listed in M4. The `StatusBadge` variant vocabulary
becomes a cross-boundary contract (spec validation ↔ catalog component);
record the allowed variant set in one place in `internal/spec` and assert it
against the catalog type in a generate test so drift fails loudly.

Downstream (consumers): Micro platform is the pilot consumer; any other
React-enabled client app gains the same capabilities. No external service,
migration, or runtime dependency is involved — this is compiler, generator,
and catalog work plus documentation.
