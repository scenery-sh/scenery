# 0129 Table Grouping and Resizable Detail Panel for `table_page`

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

`table_page` currently renders a flat data grid: search, filters, sort, optional
pagination, one-row inline expansion (`row_detail`), and CSV export. Two proven
patterns from task-tracker UIs (reference: the Astryx playground "grouped
issues" demo, a Linear-style list) are missing:

1. **Grouping** — rows bucketed under collapsible section headers (chevron +
   label + count badge), with a runtime "Group by" control that includes
   "None". Grouping is a *view mode* of the same grid, not a different page
   shape: with grouping off, the table renders exactly as today. That is why
   this plan extends the existing Table template rather than adding a new
   `grouped_table_page` template — a build-time template fork cannot model a
   runtime toggle, and a second template would duplicate the entire
   columns/filters/sorts/load surface and drift.
2. **Resizable right-hand detail panel** — clicking a row opens a side panel
   (Astryx `Resizable` + drag handle) showing row detail, as an alternative
   presentation to today's inline row expansion.

After this plan, `@scenery/ui`'s `QueryTable` supports both capabilities
behind optional props, the `.scn` `table_page` contract can declare them
(`group` children; `row_detail { presentation = "panel" }`), and generated
React adapters wire them through with staged TypeScript verification passing.

## Progress

- [x] (2026-07-21) Plan authored; scope agreed with Petr (extend `table_page`,
  include resizable detail panel).
- [x] (2026-07-21) Milestone 1: catalog grouping (`DataTable` sections,
  `QueryTable.groups`, toolbar Group selector).
- [x] (2026-07-21) Milestone 2: catalog resizable detail panel.
- [x] (2026-07-21) Milestone 3: contract + compiler + generator (`group`
  child, `row_detail.presentation`, focused tests and regenerated fixtures).
- [x] (2026-07-21) Milestone 4: docs and agent contracts updated; Micro
  platform generated work orders and the catalog demo verified live.
- [x] (2026-07-21) Post-completion design revision after Petr's browser
  review: displacement layout replaces the float-over panel; selected-row
  highlight, card-style panel with title header, full-viewport sticky height,
  slide-in motion, and arrow-key row navigation added. Re-verified live on
  the Micro platform work-orders page and catalog demo (drag resize
  360→460px, Escape/ArrowDown via synthetic key events, group headers now
  status-mapped).

## Surprises & Discoveries

- (2026-07-21) The Astryx playground URL encodes its source as base64url +
  raw-deflate (not lz-string). Decoded reference source is summarized in
  `Artifacts and Notes`.
- (2026-07-21) Astryx version skew between checkouts: the catalog typecheck
  lane (`apps/console`) has `@astryxdesign/core` 0.1.2 while the reference
  consumer (Micro platform) has 0.1.5. Both ship `Resizable`, `Layout`,
  `RadioList`, `Popover`, `StatusDot`, `MetadataList`, but component prop
  unions differ across versions (a required-`hasClear` `Selector` union in
  0.1.5 already broke staged verification once, surfaced as SCN6321). Every
  new Astryx usage must be verified against both lanes.
- (2026-07-21) Astryx `ResizeHandle`'s inline horizontal style uses
  `height: 100%`, which resolves to zero inside an auto-height flex row even
  when the parent uses `align-items: stretch`. Browser acceptance caught the
  non-functional drag target; a catalog-owned `alignSelf: stretch` plus
  `height: auto` override gives the separator the panel's full height.
- (2026-07-21) The current specification revision changes builtin provider
  descriptor identities. The Micro reference consumer therefore needed both
  `integrity` and `compile_descriptor_digest` refreshed before current
  generation could validate; there is intentionally no old-identity fallback.

## Decision Log

- (2026-07-21, Petr + agent) **Extend `table_page`; no new template.**
  Rationale: grouping with "None" selected is byte-identical to the current
  flat table; users switch grouping at runtime, so the capability cannot be a
  compile-time template choice; a parallel template would duplicate the whole
  table surface. Precedent: `rowDetail`, `exportAction`, `paginated` are
  already optional capabilities of one `QueryTable`.
- (2026-07-21, Petr) **Include the resizable right-hand detail panel** in the
  same plan (it was initially deferred as split-page territory). It becomes a
  second *presentation* of the existing `row_detail` slot, not a new slot
  kind.
- (2026-07-21, agent) **Grouping is client-side over loaded rows and is
  contract-valid only for non-paginated tables.** Grouped sections with
  counts are only honest over the complete filtered set; grouping within a
  cursor page shows misleading counts and repeats headers across pages.
  CRUD-backed pages paginate, binding-backed complete-list pages do not
  (see `ui/AGENTS.md`), so the compiler rejects `group` on paginated pages
  and `QueryTable` ignores `groups` (with a dev-console warning) when
  `paginated` is true.
- (2026-07-21, agent) **`QueryTable` stays chrome-less** (`ui/AGENTS.md`
  contract): the detail panel renders inside the component's own root
  `<section>` as a horizontal flex (table scroller + `ResizeHandle` + panel),
  not via an app-shell `Layout end` slot. Generated adapters keep owning the
  `Page` shell.
- (2026-07-21, agent) **Inline expansion and panel are mutually exclusive
  presentations** of `row_detail`. The contract expresses this as
  `presentation = "inline" | "panel"` (default `inline`) so no new child kind
  is introduced; the generator emits either `rowDetail` or `detailPanel`,
  never both.
- (2026-07-21, Petr + agent) ~~The panel floats over the right side of the
  table; it never shrinks the table or its toolbar.~~ **Superseded the same
  day after browser review: the panel displaces the table instead of
  overlaying it.** The float-over version amputated columns mid-cell and read
  as "something is covering my data". Now the workspace row is a real
  two-column flex — the table keeps `flex: 1; min-width: 0` with its own
  horizontal scroll, the panel takes its resizable width — so columns are
  never half-covered. The toolbar and stats stay full width above; only the
  grid row splits. Additional refinements from the same review: the selected
  row gets a persistent accent-muted background plus an inset accent bar on
  its first cell (`DataTable.selectedKey`); the panel is a raised card
  (card background, border, container radius, `--shadow-med`) with a single
  sticky header line combining a title (`QueryTable.detailTitle`, falling
  back to the first visible column's text) and the close button; the panel is
  sticky and viewport-height (`calc(100dvh - 24px)`) rather than capped at
  `min(80vh, 900px)`; it slides in over `--duration-medium`/`--ease-standard`
  with the animation removed under `prefers-reduced-motion`; and ArrowUp /
  ArrowDown move the selection through rows in display order while the panel
  is open (ignored while typing in form controls).
- (2026-07-21, Petr + agent) **Table pages use Linear-style scrolling: no
  page scroll at all.** `Page` gained a `fill` mode (scroll area stops
  scrolling and flex-fills) and `QueryTable` a matching `fill` prop (section,
  workspace, and grid become a flex chain; `DataTable` gets its `fill`
  scroller). The generator emits `fill` on both for every `table_page`.
  Result: header, stats, description, and toolbar never move; the grid
  scrolls vertically in its own region with the sticky column header; the
  detail panel fills the region height and scrolls its body independently.
  This supersedes the sticky/`100cqh` panel sizing for generated pages (that
  path remains the fallback for non-fill QueryTable usage).
- (2026-07-21, agent) **Changing the active group remounts `DataTable`.**
  Collapse state is local to one grouping view and always begins expanded;
  switching between Stage, Owner, and None cannot leak stale collapsed keys.

## Outcomes & Retrospective

Completed 2026-07-21. `table_page` now has one optional complete-list grouping
mode and two explicit row-detail presentations without forking the page kind.
The catalog renders ordered, collapsible sections with counts, a runtime Group
selector including None, and a bounded 280–560px side panel that closes by
button, row re-click, or Escape. The compiler rejects dishonest paginated
grouping and invalid panel combinations under SCN2623; generated React adapters
emit the singular matching props.

The Micro platform reference consumer compiled and regenerated cleanly, passed
typecheck, lint, 101 Bun tests, and production build, then started with the
current Scenery binary and logged `ui_catalog.synced`. Authenticated Chrome
acceptance proved Stage/Owner/None switching, count headers, collapse, panel
open/close/Escape, and a real pointer drag from 370px to 429px. The generated
work-orders page preserved its full workbench detail and opened it in the
declared 520px panel. Repository validation passed `go test ./...`, focused
package tests, both generated-client TypeScript checks, 16 TypeScript codec
conformance tests, and self-harness with no diagnostics.

## Context and Orientation

Definitions:

- **Catalog** — `ui/` in this repo, the editable source of `@scenery/ui`.
  Scenery embeds it (`ui/embed.go`) and materializes it into each
  React-enabled client under `react/scenery-ui/`, gated by staged TypeScript
  verification (`internal/tscheck`, diagnostics SCN632x).
- **`table_page`** — a `.scn` source kind that expands to a full generated
  React list page. Child kinds today: `column`, `filter`, `sort`, `action`,
  `stats`, `row_detail`, `export`, `toolbar`, `empty`, `search`.
- **`QueryTable`** — the catalog runtime component generated table pages
  instantiate (`ui/components/QueryTable.tsx`). It owns search/filter/sort
  state, TanStack Query list state, row expansion, export, pagination.
- **`DataTable`** — the presentational grid under `QueryTable`
  (`ui/components/DataTable.tsx`), a hand-rolled `<table>` with StyleX (it
  does not use Astryx `Table`).

Key files:

- `ui/components/QueryTable.tsx` — `QueryTableProps`, `TablePageFilter`,
  `TablePageSort`, `TablePageSlots`, `defineTablePageSlots`, `FilterToolbar`
  integration (Sort/Direction `Selector`s are passed as `FilterToolbar`
  children around line 454).
- `ui/components/DataTable.tsx` — flat `rows` rendering, `expandedKey` /
  `renderExpanded` inline expansion.
- `ui/index.ts` — public catalog surface; new types must be exported here.
- `internal/spec/source_schemas.go:253` — the `table_page` authored child
  schema map; sibling schemas (`tablePageSortSourceSchema`, positional name
  argument + attrs) are the model for the new `group` child.
- `internal/compiler/table_page.go` — table-page validation (slot handling
  near line 196, `row_detail` dialog rules near line 347, SCN26xx
  diagnostics).
- `internal/generate/generate_typescript_react.go` — `renderReactTablePage`
  (line ~412) emits the `QueryTable` JSX; slot alias wiring for
  `column`/`filter`/`toolbar`/`empty`/`row_detail` near lines 437 and 607–620;
  `reactTablePage.paginated` distinguishes CRUD (paginated) from
  binding-backed (complete list) pages.
- Tests: `internal/generate/generate_test.go` (golden table-page assertions,
  `row_detail` around line 1341), `internal/generate/catalog_test.go`
  (materialized-catalog content assertions),
  `internal/generate/generate_typescript_react_binding_table_test.go`
  (binding-backed, `paginated={false}`).
- `ui/AGENTS.md` — catalog contracts including the `QueryTable` ownership
  paragraph (must be updated).

Reference design (decoded Astryx playground demo): one table where each group
renders a full-width clickable header row (chevron toggles collapse, bold
label, neutral count `Badge`), rows follow while expanded; a "Group by" radio
(None/Status/Priority/Project/Assignee) lives in a toolbar popover; empty
groups are skipped; clicking a row opens a right panel built from
`useResizable({defaultSize: 360, minSizePx: 280, maxSizePx: 500})` +
`ResizeHandle` + a padded panel with a close button, title, and a
`MetadataList` of fields.

## Milestones

Each milestone leaves the repo green (`go test ./...`, catalog tsc) and is
independently observable.

1. **Catalog grouping.** `DataTable` learns to render grouped sections;
   `QueryTable` accepts `groups` declarations and renders a "Group" selector
   beside Sort/Direction. Observable in a consuming app via `ui_catalog` dev
   mode: a binding-backed table with `groups` shows collapsible sections with
   counts and a Group selector including "None".
2. **Catalog detail panel.** `QueryTable` accepts `detailPanel` (+ width
   config); clicking a row opens a resizable right panel; Escape or the close
   button clears it. Observable the same way.
3. **Contract + generator.** `.scn` `table_page` accepts repeated `group`
   children and `row_detail { presentation = "panel" }`; compiler validates;
   generator emits the new props; golden + catalog tests updated. Observable:
   `go test ./internal/spec ./internal/compiler ./internal/generate` passes
   with new cases, and a fixture app using the new syntax generates a page
   that typechecks under staged verification.
4. **Docs + downstream.** `ui/AGENTS.md` QueryTable paragraph and any
   `table_page` child listings in `docs/` updated; a real consuming app
   (Micro platform, `envs.local.ui_catalog` dev mode) verified in the
   browser.

## Plan of Work

### Milestone 1 — grouping in the catalog

New public types in `ui/components/QueryTable.tsx`, exported from
`ui/index.ts`:

    export interface TablePageGroup {
      readonly field: string;                       // row field to bucket on
      readonly label: string;                       // shown in the Group selector
      readonly order?: readonly string[];           // fixed leading section order
      readonly default?: boolean;                   // initially active grouping
    }

`QueryTableProps` gains `readonly groups?: readonly TablePageGroup[]`.
Behavior:

- When `groups` is empty/absent, nothing changes.
- When present and `paginated` is false, `QueryTable` renders a third
  `Selector` ("Group", options: None + declared groups, `size="sm"`) next to
  the existing Sort/Direction selectors inside `FilterToolbar`'s children.
  Initial value: the entry with `default: true`, else None.
- Bucketing is pure client-side post-processing of the loaded `items`,
  preserving server sort order within each bucket. Section order: `order`
  entries first, then remaining keys by `localeCompare`; rows whose group
  value is null/empty land in a trailing "—" section; empty sections are
  skipped.
- Section labels: if the grouped field's `column` declares a `statusMap`, use
  its labels (and let the header show the value via `StatusBadge`-consistent
  text); otherwise show the raw value.
- If `paginated` is true and `groups` is non-empty, ignore grouping and emit
  one `console.warn` in development (contract validation prevents this for
  generated pages; the warning covers hand-written consumers).

`DataTable` rendering: add an optional `sections` input (exact shape at
implementer's discretion, e.g.
`readonly { key: string; label: ReactNode; rows: readonly T[] }[]` as an
alternative to flat `rows`). Each section renders a header row: one `<td>`
with `colSpan={columns.length}`, muted background, `cursor: pointer`,
chevron icon, label, and a count badge; clicking (or Enter/Space, the row is
keyboard-focusable with `role="button"`) toggles collapse. Collapse state is
component-local, all-expanded initially; `QueryTable` remounts the table
(React `key` = active group field) when the grouping changes so state resets.
Inline expansion (`expandedKey`/`renderExpanded`) must keep working inside
sections.

### Milestone 2 — resizable detail panel in the catalog

New public types:

    export interface TablePageDetailPanelProps<Row> {
      readonly row: Row;
      readonly onClose: () => void;
    }

`QueryTableProps` gains:

    readonly detailPanel?: ComponentType<TablePageDetailPanelProps<Row>>;
    readonly detailPanelWidth?: number;   // default 360; resize clamps ~[280, 560]

Behavior:

- `detailPanel` and `rowDetail` are mutually exclusive; if both are passed,
  `detailPanel` wins and a dev-console warning fires.
- With `detailPanel` set, rows become clickable (reuse `DataTable`'s
  `onRowClick`); clicking selects the row (keyed by the existing `rowKey`),
  clicking the selected row again, pressing Escape, or the panel's close
  button clears the selection. `rowLink` still renders the first-column link;
  link clicks navigate (anchor default), row clicks select.
- Layout: `QueryTable`'s root `<section>` switches to a horizontal flex when
  a row is selected: the existing content column (toolbar + grid +
  pagination) at `flex: 1; min-width: 0`, then Astryx `ResizeHandle`
  (`isReversed`, `isAlwaysVisible={false}`), then an `<aside>` panel (left
  border, own vertical scroll, padding, close `IconButton` in a header row)
  sized by `useResizable({defaultSize: detailPanelWidth ?? 360, minSizePx:
  280, maxSizePx: 560})` from `@astryxdesign/core/Resizable`. The toolbar may
  alternatively stay full-width with only the grid row flexing — decide
  during implementation by trying both in the browser; record the choice in
  the Decision Log.
- The panel chrome (border, close button, scroll) is catalog-owned; all
  content comes from the slot component.
- Astryx caveat: verify `useResizable` / `ResizeHandle` prop names against
  BOTH `apps/console/node_modules/@astryxdesign/core` (0.1.2, catalog tsc
  lane) and the consuming app's newer version (0.1.5, staged verification
  lane). API drift surfaces as SCN6321 in `scenery up` logs.

`TablePageSlots` gains `readonly detailPanel?:
ComponentType<TablePageDetailPanelProps<Row>>` so `defineTablePageSlots`
covers it.

### Milestone 3 — contract, compiler, generator

Schema (`internal/spec/source_schemas.go`):

- New `tablePageGroupSourceSchema = sourceSchema("scenery.table-page.group",
  1, []string{"label", "order", "default"}, nil, nil)` (positional name =
  field, mirroring `sort`).
- Register `"group": repeated(tablePageGroupSourceSchema)` in the
  `table_page` child map (line ~253).
- Extend `tablePageRowDetailSourceSchema` attrs with `"presentation"` and
  `"panel_width"`.

Compiler (`internal/compiler/table_page.go`):

- `group.field` must name a declared row field (same resolution as
  `sort`/`filter` fields); duplicate group fields are diagnostics.
- `group` on a paginated (CRUD-backed) table page → diagnostic (new SCN26xx:
  "table_page group requires a complete-list data source; paginated pages
  cannot group").
- `row_detail presentation` must be `inline` or `panel`; `panel_width` only
  valid with `presentation = "panel"`; `dialog`-based `row_detail` (which
  seeds an edit form dialog) stays inline-only → diagnostic when combined
  with `panel`.

Generator (`internal/generate/generate_typescript_react.go`,
`renderReactTablePage`):

- Emit `groups={[{ field: "…", label: "…", order: […], default: true }]}`
  from `group` children.
- When `row_detail` has `presentation = "panel"`, wire the component alias to
  `detailPanel` (and emit `detailPanelWidth={…}` when `panel_width` is set)
  instead of `rowDetail`, in both the direct-JSX path and the slots path
  (lines ~437 and ~607–620); `defineTablePageSlots` call sites gain the
  `detailPanel` key.

Tests:

- `internal/generate/generate_test.go`: golden assertions for `groups={…}`
  and `detailPanel={slots.detailPanel}`.
- `internal/generate/generate_typescript_react_binding_table_test.go`:
  binding-backed page with `group` children.
- `internal/compiler` tests: the three new diagnostics.
- `internal/generate/catalog_test.go`: materialized `QueryTable.tsx` contains
  the new API markers (e.g. `TablePageGroup`, `detailPanel`).

### Milestone 4 — docs and downstream verification

- Update `ui/AGENTS.md`'s `QueryTable` ownership paragraph (grouping, panel,
  the paginated-grouping rule).
- Grep `docs/` for `row_detail` / `table_page` child listings
  (`docs/ui-agent-contract.md`, `docs/local-contract.md`,
  `docs/app-development-cookbook.md`) and update any enumerations.
- Verify end-to-end in a consuming app via `ui_catalog` dev mode (the Micro
  platform repo points `envs.local.ui_catalog` here); its
  `/scenery-ui/table-page` demo page is the natural place for a grouping
  toggle + panel demo (owned by that repo, not this one).

## Concrete Steps

All commands run from the repository root
(`/Users/petrbrazdil/Repos/scenery`) unless stated.

1. Milestone 1: edit `ui/components/DataTable.tsx`,
   `ui/components/QueryTable.tsx`, `ui/index.ts`. Validate:

       apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
       go test ./internal/generate

2. Milestone 2: edit `ui/components/QueryTable.tsx` (panel + resize),
   `ui/index.ts`. Same validation as step 1. For live browser verification,
   run `scenery up` in a consuming app root with `envs.local.ui_catalog`
   pointing at this checkout and watch for `ui catalog synced` (a `ui catalog
   sync failed` + SCN6321 line means an Astryx API mismatch in the app's
   newer Astryx).
3. Milestone 3: edit `internal/spec/source_schemas.go`,
   `internal/compiler/table_page.go`,
   `internal/generate/generate_typescript_react.go`, plus the four test
   files. Validate:

       go test ./internal/spec ./internal/compiler ./internal/generate
       go test ./...
       go install ./cmd/scenery

4. Milestone 4: docs edits; then `scenery harness self -o json --write` and
   in the consuming app run its typecheck/lint/build lanes plus
   `scenery generate --check`.
5. Update this plan's `Progress`, `Decision Log`, `Surprises & Discoveries`
   at each stopping point; on completion fill `Outcomes & Retrospective` and
   move the `docs/plans/active.md` entry to `docs/plans/completed.md`.

## Validation and Acceptance

- `apps/console/node_modules/.bin/tsc -p
  internal/generate/testdata/tsconfig.catalog.json` passes (catalog lane,
  Astryx 0.1.2).
- `go test ./...` and `go install ./cmd/scenery` pass.
- `scenery harness self -o json --write` reports no new diagnostics.
- In a consuming app with `ui_catalog` dev mode: staged verification passes
  against the app's Astryx (0.1.5); a binding-backed table page with `group`
  declarations shows collapsible sections with counts and a Group selector
  whose "None" mode is pixel-equivalent to the pre-change table; a page with
  `row_detail { presentation = "panel" }` opens a right panel on row click
  that drag-resizes within its clamps and closes via button, re-click, and
  Escape.
- Acceptance for the contract: a `.scn` fixture with `group` on a paginated
  CRUD page fails compilation with the new diagnostic; the same child on a
  binding-backed page compiles and generates JSX containing `groups={`.

## Idempotence and Recovery

All work is ordinary source edits in one repo; every step is re-runnable.
Milestones are additive — each lands green independently, so a half-finished
later milestone never breaks an earlier one. If staged verification fails in
a consuming app (SCN6321), the materialized catalog simply stays on the last
good version; fix `ui/` and save again — `scenery up` re-materializes within
seconds, no state to clean up. Golden-test churn in `internal/generate` is
regenerated by the tests themselves; if a golden update goes wrong, `git
checkout -- internal/generate/testdata` restores it.

## Artifacts and Notes

- Reference demo: Astryx playground, decoded from the URL fragment
  (base64url → raw-inflate). Structural notes preserved in `Context and
  Orientation`; key numbers: panel `defaultSize: 360, minSizePx: 280,
  maxSizePx: 500`; group header = colSpan cell, muted background, chevron +
  bold label + neutral count badge; groups all-expanded initially; empty
  groups skipped; "Group by" options included "None".
- The demo also used `PowerSearch` and row `DropdownMenu` actions — both
  explicitly out of scope here (PowerSearch would replace `FilterToolbar`
  search and is its own conversation).
- Astryx versions at planning time: `apps/console` 0.1.2, Micro platform
  0.1.5; both ship `Resizable`, `RadioList`, `Popover`, `StatusDot`,
  `MetadataList`, `Layout`.

## Interfaces and Dependencies

- **`@scenery/ui` public API additions**: `TablePageGroup`,
  `TablePageDetailPanelProps`, `QueryTableProps.groups`,
  `QueryTableProps.detailPanel`, `QueryTableProps.detailPanelWidth`,
  `TablePageSlots.detailPanel`, plus whatever `sections` shape `DataTable`
  exposes. All exported via `ui/index.ts`.
- **Astryx dependencies** (peer, `>=0.1.2 <0.2.0`): `Resizable`
  (`useResizable`, `ResizeHandle`), `Icon`, `IconButton`, `Badge`,
  `Selector` — all already in the blessed surface or available in both
  installed versions.
- **Contract surface**: `table_page` gains repeated `group(field)` child
  (attrs `label`, `order`, `default`); `row_detail` gains `presentation`,
  `panel_width`.
- **Consumers**: per `ui/AGENTS.md`, no backward-compatibility constraint for
  stale apps — current consumers are regenerated in the same work. The
  Micro platform repo is the live reference consumer via `ui_catalog` dev
  mode; its demo-page work happens in that repo after Milestones 1–2 land.
