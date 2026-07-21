# 0130 Astryx-Native Catalog: Table Migration and Hand-Rolled Component Elimination

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

The `@scenery/ui` catalog promises "Astryx components and tokens with StyleX"
(`ui/AGENTS.md`), but an audit on 2026-07-21 found several catalog components
that hand-roll HTML instead of composing Astryx primitives. The worst case is
`DataTable`: a from-scratch `<table>` with hand-written sticky headers, sort
buttons, section rows, and hover styling — while Astryx 0.1.6/0.1.7 ships
`Table` with `useTableGroupedRows` (collapsible section rows with chevron,
label, and member count — the exact feature we re-implemented by hand in plan
0129) and `useTableRowIndex` (right-aligned monospaced row-number column).

Hand-rolled copies are strictly worse: they drift from Astryx's accessibility
work (live regions, aria patterns), miss upstream fixes and theming, and cost
maintenance. This plan migrates `DataTable` onto Astryx `Table` (adopting
`useTableGroupedRows` and exposing `useTableRowIndex`), replaces the other
hand-rolled components with Astryx-backed implementations, and adds a
guardrail so raw interactive HTML cannot quietly reappear in the catalog.

Both toolchains are on `@astryxdesign/core` 0.1.7 (`apps/console` for the
catalog typecheck lane; the Micro platform reference consumer), so version
skew is no longer a constraint.

## Progress

- [x] (2026-07-21) Audit completed; plan authored. Not yet started.
- [x] (2026-07-21) Scope decision from Petr: `FilterPills` is exempt and
  stays hand-rolled; every other audited component is in scope.
- [ ] Milestone 1: `DataTable` on Astryx `Table` with `useTableGroupedRows`
  and a row-number option via `useTableRowIndex`.
- [ ] Milestone 2: `StatTile`/`StatGrid` on `Card` + typography primitives;
  `QueryState`'s `EmptyState` on Astryx `EmptyState`; `TopBar` raw buttons on
  `Button`/`IconButton`.
- [ ] Milestone 3: `SplitPage` (and `PageLayout` shell internals where they
  overlap) on the Astryx `Layout` family.
- [ ] Milestone 4: shell review — verify `ClientAppShell`, `SideNavigation`,
  and `TopBar` against Astryx `AppShell`; adopt or record the reasoned
  exception.
- [ ] Milestone 5: guardrail — verification that fails on raw interactive
  elements in `ui/components/` (allowlist: `FilterPills`), plus
  `ui/AGENTS.md` rule.

## Surprises & Discoveries

- (2026-07-21) Astryx 0.1.6 shipped `useTableGroupedRows` two days before we
  hand-built the same feature in plan 0129. Root cause of the miss: the
  catalog never tracked Astryx release notes, and `DataTable` predating the
  blessed-surface rule made the hand-rolled path the default for table work.
- (2026-07-21) Audit method and results (raw counts via
  `grep -cE '<(table|thead|tbody|tr|td|th|button|input|select|textarea|nav|header|ul|li)\b'`):
  `DataTable` 12 raw elements; `FilterPills` all-raw pills, zero Astryx
  imports; `StatTile` all-raw, zero Astryx; `SplitPage` zero Astryx layout;
  `TopBar` two raw `<button>`s; `QueryState` hand-rolled `EmptyState` div
  even though the catalog already re-exports Astryx `EmptyState` as
  `RichEmptyState`. Confirmed Astryx-backed: `FormDialog`, `FilterToolbar`,
  `QueryTable` controls, `StatusBadge`, `ClientAppShell`, `SideNavigation`.

## Decision Log

- (2026-07-21, Petr) **Catalog components must be Astryx behind the scenes;
  hand-rolled equivalents of things Astryx provides are defects.** Trigger:
  discovering `DataTable` duplicated `useTableGroupedRows` by hand.
- (2026-07-21, Petr) **`FilterPills` is the one sanctioned exception and
  stays hand-rolled.** It is a bespoke Scenery pattern (count facets with
  sub-metrics, tones, All pill, zero-count collapsing) with no matching
  Astryx component — `ToggleButtonGroup` covers selection semantics but not
  the pill anatomy. It goes on the guardrail allowlist; if Astryx ever ships
  a faceted-filter component, revisit.
- (2026-07-21, agent) **Keep `DataTable`'s public API stable through the
  migration.** `QueryTable`, generated pages, and app-owned views consume
  `Column<T>`, `sections`, `selectedKey`, `expandedKey`, `hideHeader`,
  `fill`, `sticky`, `sort`/`onSort`. Milestone 1 swaps the implementation
  under that API so no consumer churns; API simplifications (e.g. retiring
  `mono` in favor of Astryx column alignment/appearance) are allowed only
  when Astryx `Table` makes the old prop meaningless, and each such change
  must be recorded here and migrated in the same commit.
- (2026-07-21, agent) **Grouping state moves to `useTableGroupedRows`; the
  hand-rolled section-row rendering in `DataTable` is deleted, not kept as a
  fallback** (per the repo rule against dead compatibility paths).
  `QueryTable`'s `groupRows` bucketing/ordering/labeling logic stays — it is
  domain logic (status-map labels, declared order, empty-value bucket) that
  feeds the hook its groups.
- (2026-07-21, agent) **Row numbers become an opt-in catalog feature**
  (`numbered?: boolean` on `DataTable`/`QueryTable`, wired to
  `useTableRowIndex`), not a default — no current page design shows row
  numbers.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Definitions:

- **Catalog** — `ui/` in this repo, the editable source of `@scenery/ui`,
  materialized into React clients under `react/scenery-ui/` after staged
  TypeScript verification (SCN632x diagnostics in `scenery up` logs).
- **Astryx `Table`** — `@astryxdesign/core/Table`: `Table`, `TableRow`,
  `TableCell`, `TableHeader`, `TableHeaderCell`, `TableBody`, plus column
  sizing helpers `proportional`, `pixel`, `resolveColumnWidths` and props
  like `density`, `dividers`, `hasHover`, `textOverflow`. As of 0.1.6/0.1.7
  it also exports `useTableGroupedRows` (collapsible section rows) and
  `useTableRowIndex` (row-number column). Exact hook signatures must be read
  from `apps/console/node_modules/@astryxdesign/core/dist/Table/` at
  implementation time — do not code from this plan's prose.
- **0.1.7 note**: Table *plugin* render-prop interfaces renamed `styles` →
  `xstyle`; relevant only if we author custom plugins (we currently do not).

Key files:

- `ui/components/DataTable.tsx` — the hand-rolled grid: `Column<T>`,
  `DataTableSection<T>`, sticky thead, sort buttons with `SortIndicator`,
  section rows with collapse state, selected-row styling
  (`selectedCell`/`selectedFirstCell`), expanded-row rendering, `hideHeader`
  + colgroup, `fill` scroller.
- `ui/components/QueryTable.tsx` — sole catalog consumer of `DataTable`;
  owns `groupRows` bucketing and all table-page state. Its detail-panel,
  fill, and keyboard behavior from plan 0129 must survive unchanged.
- `ui/components/FilterPills.tsx` — pill row (`FilterPillOption`), single
  select, toggle-to-clear, zero-count collapse; adopted by the Micro
  platform Invoices page (plan 0050 in that repo). EXEMPT from migration by
  decision; guardrail allowlist entry.
- `ui/components/ClientAppShell.tsx`, `ui/components/SideNavigation.tsx` —
  router-agnostic shell and navigation; compose Astryx primitives but
  predate Astryx `AppShell` (Milestone 4 evaluation).
- `ui/components/StatTile.tsx` — `StatGrid`/`StatTile` with `StatTone`.
- `ui/components/QueryState.tsx` — `QueryState`, hand-rolled `EmptyState`,
  `TableEmptyRow` (couples to the table implementation; revisit in
  Milestone 1).
- `ui/components/TopBar.tsx` — two raw `<button>`s (search trigger, actions).
- `ui/components/SplitPage.tsx`, `ui/components/PageLayout.tsx` — raw-div
  layout chrome; Astryx `Layout`/`LayoutHeader`/`LayoutContent`/
  `LayoutPanel` is the native family for this.
- `ui/index.ts` — public surface; any new exports land here.
- Consumers to re-verify: generated table pages (Micro platform
  `work_orders`, `projects`), the `/scenery-ui/table-page` demo, Invoices'
  FilterPills, app-owned `DataTable` usages (grep the platform repo for
  `DataTable` imports from `@scenery/ui`).

Validation lanes (from repo root):

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./internal/generate
    go test ./...

Consumer verification: with `envs.local.ui_catalog` dev mode in the Micro
platform repo, `scenery up` re-materializes on save; then that repo's
typecheck/lint/test/build lanes plus browser checks at
`https://micro.scenery.sh/platform/*`.

## Milestones

1. **`DataTable` on Astryx `Table`.** Same public API; implementation uses
   `Table`/`TableRow`/`TableCell` etc., `resolveColumnWidths` +
   `pixel`/`proportional` for widths (also replacing the `hideHeader`
   colgroup hack), `useTableGroupedRows` for sections, `useTableRowIndex`
   behind a new `numbered?: boolean`, Astryx `density`/`dividers`/`hasHover`
   for chrome. Must preserve: sortable headers with `aria-sort`, sticky
   header in `fill` mode, selected-row highlight, one-row expansion,
   `hideHeader`, empty-row rendering, row click/keyboard activation with the
   interactive-target guard. Observable: `/scenery-ui/table-page` demo and
   the generated work-orders page are pixel-comparable and behaviorally
   identical (grouping, collapse, panel, arrows, hide-header toggle), with
   the DOM now rendered by Astryx `Table`.
2. **Small fry.** `StatTile`/`StatGrid` compose `Card` + `Text`/`Heading`
   (tones map to Astryx color tokens); `QueryState`'s `EmptyState` wraps
   Astryx `EmptyState`; `TopBar`'s raw buttons become
   `Button`/`IconButton` (+ `Kbd` for the shortcut hint). APIs unchanged.
3. **Layout chrome.** `SplitPage` moves onto `Layout` + `LayoutPanel`
   (sidebar divider, detail region); `PageLayout`'s shell internals adopt
   `Layout`/`LayoutHeader`/`LayoutContent` where they map cleanly. This is
   the riskiest visual change — verify every generated page kind
   (table/split/content) in the browser.
4. **Shell review.** `ClientAppShell`, `SideNavigation`, and the remainder
   of `TopBar` compose Astryx primitives today but predate Astryx
   `AppShell`. Compare against `AppShell` (read its 0.1.7 API from `dist/`):
   adopt it where it covers our router-agnostic shell contract, and where it
   does not, record the concrete gap here as a reasoned exception instead of
   leaving the question open. The already-compliant components — 
   `FormDialog`, `FilterToolbar`, `QueryTable` controls, `StatusBadge`,
   `Theme` — need no work but are re-checked by the Milestone 5 guardrail.
   `FilterPills` is exempt by decision (see Decision Log) and stays as-is.
5. **Guardrail.** Add a repo verification that fails when files under
   `ui/components/` (excluding a short documented allowlist: `FilterPills`
   by decision, plus genuinely semantic non-interactive wrappers like
   `<section>`/`<aside>`) introduce raw interactive elements
   (`<button`, `<input`, `<select`, `<textarea`, `<table`).
   Wire it where the repo's existing checks live (`scenery harness self` /
   `make verify` lane — find the current home of repo-local checks before
   adding a new mechanism). Update `ui/AGENTS.md`: composing Astryx is not a
   preference but a contract; a hand-rolled equivalent of an existing Astryx
   component is a defect. Also add: check Astryx release notes when bumping
   the dependency, and record feature overlaps here.

## Plan of Work

Work top-down by milestone; each lands green independently.

Milestone 1 sequencing: first read the actual 0.1.7 `Table` +
`useTableGroupedRows` + `useTableRowIndex` type signatures in
`apps/console/node_modules/@astryxdesign/core/dist/Table/`. Map:

- `Column<T>` → `TableColumn` (+ width mapping: our `width?: string` →
  `pixel(n)`; unspecified → `proportional(1)` for the primary column,
  content-sized otherwise as the Astryx API allows).
- Sections: feed `QueryTable.groupRows` output (key/label/rows) into
  `useTableGroupedRows`; keep collapse-state reset-by-remount (React `key`
  on the table when the active group changes) unless the hook exposes
  controlled state — prefer the hook's own state if it resets cleanly.
- Selected row: Astryx `TableRow` prop if one exists (`isSelected` or
  equivalent); otherwise keep our cell-level StyleX overlay via each cell's
  `xstyle`.
- Expanded row: a full-width `TableCell colSpan` row (the playground demo
  proves Astryx `Table` accepts colSpan rows).
- `TableEmptyRow` in `QueryState.tsx` re-renders through Astryx cells.
- `hideHeader`: omit the header row; widths via `resolveColumnWidths`'
  colgroup styles (the playground pattern).
- `fill`/sticky: keep the catalog-owned scroller div wrapping the Astryx
  table; verify Astryx's own sticky-header support before keeping ours.

Milestones 2–3 are straight substitutions behind stable APIs; read each
Astryx component's props from `dist/` first, keep semantic vocabulary
(`t` tokens) for layout-only styling, and prefer deleting catalog CSS over
porting it. Milestone 4 is an evaluation with a decision obligation: every
shell component ends the milestone either on `AppShell` or with a named gap
in the Decision Log.

Milestone 5: locate the repo's existing self-check infrastructure
(`scenery harness self` sources under `internal/`), add the raw-element scan
with an explicit allowlist, and a test for the check itself.

## Concrete Steps

All commands from `/Users/petrbrazdil/Repos/scenery` unless stated.

1. Per milestone: edit `ui/components/*`, then

       apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
       go test ./internal/generate

2. Live-verify in the Micro platform repo (dev session with `ui_catalog`
   dev mode): watch `scenery up` output for `ui catalog synced` vs SCN6321,
   then browser-check `/platform/scenery-ui/table-page`,
   `/platform/work-orders`, `/platform/invoices`, `/platform/mails`.
3. In the platform repo run `cd apps/platform && bun run typecheck && bun
   run lint && bun test && bun run build` (never from the repo root — the
   root typecheck script recurses).
4. Milestone 5: `go test ./...` and `scenery harness self -o json` must show
   the new check passing on the migrated tree and failing on a fixture with
   a raw `<button>`.
5. Update this plan's living sections at each stopping point; on completion
   move the `docs/plans/active.md` entry to `completed.md`.

## Validation and Acceptance

- Catalog tsc lane and `go test ./...` pass at every milestone.
- The staged materialization (SCN632x) passes against the platform's Astryx
  0.1.7.
- Browser acceptance per milestone as listed in `Milestones` — behavioral
  parity is the bar for Milestone 1 (grouping, collapse, selection,
  panel, arrows, hide-header), visual parity "close enough that a
  page-level screenshot diff shows only intentional Astryx chrome
  differences" for Milestones 2–4.
- Acceptance for the guardrail: introducing `<button>` into a catalog
  component fails verification with a message naming the file and the
  Astryx alternative policy.

## Idempotence and Recovery

Ordinary source edits; every lane is re-runnable. If a milestone's
materialization fails (SCN6321), the consumer keeps the last good catalog —
fix and save. If Astryx `Table` turns out to lack a required behavior
(sticky header interplay with `fill`, colSpan expansion, controlled
selection), record it in `Surprises & Discoveries`, keep that single
behavior catalog-owned *on top of* Astryx `Table` (never revert to the
hand-rolled `<table>`), and file the gap upstream.

## Artifacts and Notes

- Audit (2026-07-21): see `Surprises & Discoveries` for per-file counts.
- Astryx 0.1.7 release notes highlights relevant here: `useTableGroupedRows`
  (0.1.6, refined 0.1.7), `useTableRowIndex`, Table root prop forwarding
  (`className`/`style`/`xstyle`/`aria-*`), plugin render-prop `styles` →
  `xstyle` rename (plugin authors only), full i18n provider (future option
  for catalog strings; out of scope here).
- The Astryx playground "grouped issues" demo (decoded in plan 0129) is a
  working reference for `Table` + `resolveColumnWidths` + colSpan section
  rows + `DropdownMenu` row actions.

## Interfaces and Dependencies

- `@astryxdesign/core` >= 0.1.7 required for `useTableGroupedRows` /
  `useTableRowIndex`; `ui/package.json` peer range already admits it, but
  Milestone 1 should raise the floor to `>=0.1.7 <0.2.0` since the catalog
  will import the hooks.
- Public API changes: additive only — `numbered?: boolean` on
  `DataTable`/`QueryTable` (and, if promoted to contracts later, a
  `numbered` attr on `table_page` — out of scope here).
- Consumers: Micro platform generated pages, demo pages, and any app-owned
  `DataTable`/`StatTile` imports — regenerate/re-verify in the same work,
  per the no-stale-consumer rule in `ui/AGENTS.md`. (`FilterPills` consumers
  are untouched — the component does not change.)
