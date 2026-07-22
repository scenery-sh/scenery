# 0132 QueryTable Performance: Stable Identities, Memoized Row Rendering, and Virtualized Large Tables

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

`QueryTable` is the one table component every generated `table_page` and every
app-owned table composition uses, so its render cost multiplies across the
whole application surface. Today it has three compounding performance problems:

1. **Unstable identities.** `rowKey`, `visibleColumns`, `dataColumns` (with a
   fresh `render` closure per column), `applySort`, and the toolbar filter
   arrays are rebuilt on every render of `QueryTable`
   ([ui/components/QueryTable.tsx](../../ui/components/QueryTable.tsx), the
   block starting near the `rowKey` declaration after the result-context memo).
   The keydown navigation `useEffect` has **no dependency array** — it tears
   down and re-registers a window listener on every render precisely because
   `rowKey` cannot be listed as a stable dependency.
2. **No memoized render boundary.** `DataTable`
   ([ui/components/DataTable.tsx](../../ui/components/DataTable.tsx)) is a
   plain function component and Astryx's `TableRow`/`TableBody` are not
   memoized either. Every `QueryTable` state change re-renders every row. The
   worst everyday case: each keystroke in the search box is a `setSearch`
   state update (the debounce only delays the query, not the render), so
   typing into a 5,000-row complete-list table re-renders 5,000 rows per
   keystroke.
3. **No virtualization.** All loaded rows become DOM rows. Cursor and numeric
   page tables are bounded by `pageSize`, but binding-backed complete-list
   tables (the only mode that may group) can legitimately load thousands of
   rows, and initial mount cost plus layout cost grows linearly with row
   count.

This plan fixes them in dependency order — stabilization enables memoization,
memoization makes interaction cheap, and profiling then tells us exactly what
virtualization must recover — and lands windowed rendering for large
complete-list tables so table-heavy apps stay responsive at 10k rows.

Fix 1 without fix 2 is dead weight: because `DataTable` is not wrapped in
`React.memo`, stabilizing props alone changes nothing about re-render counts.
The two must land as a pair.

## Progress

- [x] 2026-07-22 Plan created; code archaeology of `QueryTable`, `DataTable`,
  and the installed Astryx `Table` plugin surface completed (findings recorded
  under Surprises & Discoveries).
- [ ] Milestone 1: profiling harness and committed baseline numbers at
  1k/5k/10k rows.
- [ ] Milestone 2: stable identities in `QueryTable` (callbacks, column
  arrays, keydown effect dependencies).
- [ ] Milestone 3: memoized render boundary (`DataTable` and row-level
  memoization) with profiler evidence that search keystrokes no longer
  re-render rows.
- [ ] Milestone 4: virtualization decision recorded (Astryx-native vs catalog
  windowing plugin) after reading current Astryx release notes.
- [ ] Milestone 5: virtualized complete-list rendering shipped behind a row
  threshold, with grouping/expansion/detail-panel/keyboard semantics intact.
- [ ] Milestone 6: full validation matrix green, fixture clients regenerated,
  docs (`ui/AGENTS.md`, this plan) updated; browser acceptance in a consuming
  app.

## Surprises & Discoveries

- 2026-07-22 — Astryx's `Table` plugin pipeline explicitly anticipates
  virtualization: `ScrollWrapperRenderProps` and `transformScrollWrapper` doc
  comments both name virtualization as an intended use (attach a `ref` to the
  scroll container, inject chrome before/after the `<table>`). Evidence:
  `apps/console/node_modules/@astryxdesign/core/dist/Table/types.d.ts` lines
  ~279 and ~388. No shipped Astryx plugin implements it yet (the plugin
  directory contains columnResize, columnSettings, filtering, groupedRows,
  pagination, rowExpansion, rowIndex, selection, sortable, stickyColumns).
- 2026-07-22 — The keydown `useEffect` in `QueryTable` deliberately omits its
  dependency array (re-subscribes every render). This is a symptom of the
  unstable `rowKey`, not an oversight to "fix" in isolation: once `rowKey` is
  a `useCallback`, the effect can take a real dependency list.
- 2026-07-22 — `Table.perf.test.tsx` exists upstream in Astryx's own source
  tree, confirming render-count testing against this table is practical in a
  plain test runner.

## Decision Log

- 2026-07-22 (Petr + agent) — **Stabilization and memoization land together.**
  Rationale: `DataTable` is not `React.memo`-wrapped, so stable props alone
  cannot reduce re-renders; memoization without stable props is defeated by
  fresh `dataColumns`/`rowKey` identities every render. Neither half is
  observable alone.
- 2026-07-22 (Petr + agent) — **Profile before and after every milestone, and
  before choosing the virtualization implementation.** Rationale: memoization
  fixes interaction cost (keystrokes, selection) but cannot fix initial mount
  cost — only virtualization reduces DOM row count. Numbers decide how much
  each layer must deliver and at what row count windowing should engage.
- 2026-07-22 (Petr + agent) — **Astryx-first for virtualization.** Per
  [ui/AGENTS.md](../../ui/AGENTS.md), a hand-rolled equivalent of an existing
  Astryx primitive is a defect, and Astryx is externally maintained (never
  vendor or fork it). Milestone 4 therefore starts by reading the current
  Astryx changelog for a virtualization plugin; only if none exists do we
  write a catalog-owned windowing `TablePlugin` against the documented
  `transformScrollWrapper` extension point.
- 2026-07-22 (Petr + agent) — **Default to no new runtime dependency.**
  A fixed-row-height windowing hook over the scroll container is small and
  sufficient for the current table contract (uniform row heights outside
  expansion). `@tanstack/react-virtual` as a peer dependency is the fallback
  only if measurement-based heights become unavoidable, because every
  React-enabled client app must then install it and generated client
  scaffolding/docs must declare it. Record the final call here when Milestone
  4 completes.
- 2026-07-22 (Petr + agent) — **Virtualization engages only above a row
  threshold and only where it can be honest.** Small tables keep the simple
  full-render path (no behavior change, no windowing edge cases). Expanded
  inline rows and grouped section headers have non-uniform heights; the plan
  scopes windowing to keep those correct (see Plan of Work) rather than
  silently degrading them.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Definitions, for a reader landing cold:

- **`QueryTable`** ([ui/components/QueryTable.tsx](../../ui/components/QueryTable.tsx))
  is the catalog's chrome-less query-driven table: controlled
  search/filter/sort/group state, TanStack Query loading, pagination modes
  (`cursor`, `page`, or none for complete lists), CSV export, row expansion,
  resizable detail panel, row actions, and slot wiring. Generated `table_page`
  adapters render it; app-owned pages may too.
- **`DataTable`** ([ui/components/DataTable.tsx](../../ui/components/DataTable.tsx))
  is the catalog's presentational layer over Astryx's `Table`: it converts
  catalog `Column`/`DataTableSection` types into Astryx `TableColumn` rows and
  plugins (`useTableGroupedRows`, `useTableRowIndex`, `useTableSortable`, and
  a catalog-owned behavior plugin for expansion/selection/click handling).
- **Astryx** (`@astryxdesign/core`) is the externally maintained design
  system. Its `Table` accepts a `plugins` array; each plugin can transform
  rows, cells, the `<table>` element, and the scroll wrapper `<div>`
  (`transformScrollWrapper`). The catalog must compose Astryx primitives, not
  reimplement them.
- **Complete-list tables** are `table_page` bindings whose result is one
  complete typed list: no pagination, grouping allowed. These are the
  unbounded-row-count case this plan targets for virtualization. Cursor and
  numeric-page tables stay bounded by `pageSize` and only benefit from
  Milestones 2–3.
- **Materialization**: `ui/` is embedded in the Scenery binary and
  materialized into each React-enabled client under `react/scenery-ui/`.
  Catalog changes therefore alter generated-client artifact revisions; the
  committed fixture clients under `internal/compiler/testdata/native` and
  `internal/compiler/testdata/house` must be regenerated in the same change.
- **Live iteration**: set `envs.local.ui_catalog` in a consuming app's
  `.scenery.json` to this repo's `ui/` directory and run `scenery up`; catalog
  edits reach the browser through Vite HMR without rebuilding the binary
  (see ui/AGENTS.md "Local iteration").

Current defect map (verified 2026-07-22 against the working tree):

- `QueryTable`: `rowKey` is a plain arrow function; `visibleColumns`,
  `dataColumns`, `applySort`, `toolbarFilters`, `activeDateTimeFilters` are
  plain per-render computations; the arrow-key/Escape `useEffect` subscribes a
  `window` keydown listener with no dependency array. Already-stable pieces to
  preserve: `query`, `resultContext`, `queryControls`, `sections`,
  `orderedRows` are `useMemo`/`useCallback`-backed.
- `DataTable`: exported as a plain generic function; its `sourceRows` memo
  lists `getRowKey` as an input, so the unstable `rowKey` from `QueryTable`
  invalidates it every render. The `behaviorPlugin` memo similarly depends on
  per-render closures from its props.
- Astryx `TableRow`/`TableBody` are not memoized; row-level memoization must
  live in the catalog layer (memoized row content / cell render results), not
  in Astryx.

## Milestones

Each milestone keeps the repo green and independently shippable.

1. **Baseline profiling harness.** A committed render-count/timing test plus a
   repeatable browser profiling recipe, with baseline numbers recorded in this
   plan for 1k/5k/10k rows: initial mount, one search keystroke, one row
   selection.
2. **Stable identities in `QueryTable`.** `useCallback`/`useMemo` for the
   listed values; keydown effect gains a real dependency array. No behavior
   change; render-count test unchanged (this milestone alone is not expected
   to move numbers — see Decision Log).
3. **Memoized render boundary.** `React.memo` on `DataTable` (typed cast to
   preserve the generic signature) plus row-level memoization so unchanged
   rows skip re-render. Acceptance: profiling shows a search keystroke and a
   selection change re-render O(1) rows, not O(n).
4. **Virtualization decision.** Read the current Astryx changelog/release
   notes for a virtualization or windowing plugin per the ui/AGENTS.md Astryx
   bump rule. Record adopt-vs-build and the dependency decision in the
   Decision Log before writing code.
5. **Windowed complete-list rendering.** Virtualized row rendering for large
   ungrouped-or-grouped complete lists behind a row threshold, preserving
   sticky headers, grouping, expansion, selection highlight, keyboard
   navigation (selected row scrolls into view), row numbering, and CSV export
   (export already reads data, not DOM).
6. **Validation, regeneration, docs, acceptance.** Full matrix below, fixture
   client regeneration, `ui/AGENTS.md` update if the catalog contract gains a
   threshold/prop, browser acceptance in a consuming app.

## Plan of Work

**Milestone 1 — measure first.** Add a catalog render-count test (Bun +
Testing Library, mirroring the approach of Astryx's own `Table.perf.test.tsx`)
that renders `QueryTable` with a synthetic complete-list `load` returning 1k
rows and counts row-render invocations across (a) mount, (b) a simulated
search keystroke, (c) a selection change. Counting renders is deterministic in
a test runner; wall-clock timing is not, so timings come from the browser: use
`ui_catalog` live iteration against a consuming app (or a fixture-app page)
with React DevTools Profiler at 1k/5k/10k rows. Record the numbers in this
plan under Artifacts and Notes. If adding a test-only dev dependency for the
catalog is needed, keep it out of `ui/package.json` peer dependencies — tests
can live under `internal/generate/testdata/` beside the existing conformance
test where a Bun environment already exists.

**Milestone 2 — stabilization.** In `QueryTable`: wrap `rowKey` in
`useCallback([rowLink])`; derive `visibleColumns` with
`useMemo([columns])`; build `dataColumns` (including the `__expand` column
currently unshifted conditionally) inside one `useMemo` keyed on
`visibleColumns`, `rowLink`, `sorts`, `rowKey`, `expandedKey`, and the
expansion-mode flags; wrap `applySort` in `useCallback`; memoize
`toolbarFilters` and `activeDateTimeFilters` on `visibleDeclaredFilters` and
`filters`. Give the keydown `useEffect` its real dependency array
(`selectedRow`, `orderedRows`, `rowKey`, `DetailPanel`, `RowAction`). Note
`expandedKey` in the column memo is acceptable: expansion toggles are rare
user actions, unlike keystrokes.

**Milestone 3 — memo boundary.** Export `DataTable` wrapped in `React.memo`
via the standard typed-cast idiom so the generic call signature survives.
Add row-level memoization in the catalog layer: memoize each row's rendered
cell content keyed on the row object identity and column list, so a re-render
of `DataTable` with the same `rows` array identity skips per-row work. TanStack
Query already returns referentially stable `items` between unrelated state
updates, which this relies on; the render-count test from Milestone 1 is the
proof. If Astryx's `components` prop (`TableRowComponentProps`) offers a
cleaner row-component seam, prefer it over wrapping cell output — decide in
implementation and record in the Decision Log.

**Milestone 4 — Astryx-first check.** Read the installed and latest Astryx
`CHANGELOG.md` for table virtualization/windowing. If Astryx ships one, adopt
it (bumping Astryx follows the ui/AGENTS.md bump rule: read release notes,
record overlap decisions here). If not, proceed to Milestone 5 with a
catalog-owned plugin and note the upstream gap here so a later Astryx bump
can replace it.

**Milestone 5 — windowing.** Implement a catalog-owned `useTableWindowing`
`TablePlugin` in `ui/components/` (or inside `DataTable`) that: attaches a
scroll listener/`ref` through `transformScrollWrapper`; computes the visible
index range from a fixed measured row height (measure the first rendered row,
fall back to a constant); renders only the visible slice plus overscan; and
preserves total scroll height with top/bottom spacer rows. Contract decisions:

- Engage only when the flattened row count exceeds a threshold (default 200,
  a `DataTable` prop so `QueryTable` and future callers can tune it). Below
  the threshold, rendering is exactly today's path.
- Grouped sections: window over the flattened ordered rows (the same order
  `orderedRows` uses) and render a section header row whenever the window
  crosses a section boundary; collapsed groups contribute zero rows. Sticky
  section headers within the window are a stretch goal, not a gate.
- Inline row expansion breaks the uniform-height assumption: while a row is
  expanded, pin the expanded row into the rendered slice and measure it, or —
  if that proves fragile — document that expansion switches the table off the
  fast path for that render. Decide by profiling, record here.
- Keyboard navigation and the detail panel's selected-row highlight must
  scroll the selected row into view when arrow keys move selection beyond the
  window.
- Row numbering (`useTableRowIndex`) must reflect absolute indices, not
  window-relative ones.

**Milestone 6 — validation and rollout.** Run the matrix below. Because `ui/`
is embedded and materialized, regenerate both fixture clients and commit the
diff. Update `ui/AGENTS.md`'s `QueryTable`/`DataTable` contract paragraph with
the windowing threshold and any new prop. Browser acceptance: a real consuming
app via `ui_catalog` live iteration with a 5k-row complete-list table —
scroll, group, expand, select, arrow-key through, export CSV, and confirm
profiler numbers against the Milestone 1 baseline.

## Concrete Steps

All commands run from the repository root unless stated.

1. Baseline (Milestone 1):

       apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
       bun test internal/generate/testdata/query_table_perf.test.tsx

   (new test file; name final at implementation). Record render counts here.
   For browser timings, in a consuming app: set `envs.local.ui_catalog`,
   `scenery up`, open a complete-list table page, profile with React DevTools.

2. Stabilization + memo boundary (Milestones 2–3): edit
   `ui/components/QueryTable.tsx` and `ui/components/DataTable.tsx`, then:

       apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
       bun test internal/generate/testdata/query_table_perf.test.tsx
       go test ./internal/generate

3. Astryx check (Milestone 4): read
   `apps/console/node_modules/@astryxdesign/core/CHANGELOG.md` and the latest
   published release notes; record the decision in the Decision Log.

4. Windowing (Milestone 5): implement the plugin/threshold, extend the perf
   test with a 10k-row DOM-row-count assertion (rendered `<tr>` count stays
   below threshold + overscan), rerun step 2's commands.

5. Regenerate fixture clients (required — catalog changes shift artifact
   revisions; stale fixtures fail `go test ./...` with SCN6204):

       go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
       go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json

6. Full validation (Milestone 6):

       go test ./...
       bun test internal/generate/testdata/typescript_client_conformance.test.ts
       .scenery/harness/bin/scenery harness self --summary --write

   (Self-harness through the worktree-local binary; in a fresh worktree run
   `./scripts/build-dashboard-ui-embed.sh` once first.)

## Validation and Acceptance

The change is done when all of the following hold:

- The render-count test proves: mount renders each row once; a search
  keystroke re-renders zero data rows; a selection change re-renders only the
  affected rows; with 10k complete-list rows the DOM contains fewer than
  (threshold + 2×overscan) `<tr>` elements while scroll height matches the
  full list.
- `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
  passes (catalog typecheck).
- `go test ./...` passes with regenerated fixture clients committed (no
  SCN6204).
- `bun test internal/generate/testdata/typescript_client_conformance.test.ts`
  passes.
- `.scenery/harness/bin/scenery harness self --summary --write` passes,
  including the architecture check (no raw interactive HTML added to
  `ui/components/` — windowing spacers are non-interactive table rows).
- Browser acceptance in a consuming app (via `ui_catalog`): 5k-row
  complete-list table scrolls smoothly; grouping, expansion, detail panel,
  arrow-key navigation with scroll-into-view, absolute row numbers, and CSV
  export all behave as before; React DevTools Profiler numbers for mount and
  keystroke are recorded here alongside the baseline.
- Behavior below the row threshold is byte-identical to today's rendering
  path (existing catalog tests unchanged).

## Idempotence and Recovery

Every milestone is a plain edit to `ui/` plus tests; re-running any listed
command is safe and side-effect free. Fixture regeneration is deterministic —
rerun the two `generate --target` commands after any catalog edit and commit
the resulting diff; if a partial regeneration is interrupted, rerunning it
converges. If Milestone 5 destabilizes grouped/expanded behavior, revert only
the windowing plugin: Milestones 2–3 stand alone and already fix interaction
cost. The threshold prop provides a runtime escape hatch (set it above any
realistic row count to disable windowing) without reverting code.

## Artifacts and Notes

Baseline and post-change measurements (fill in during Milestone 1/6):

    rows   mount(ms)  keystroke(ms)  keystroke row renders   DOM <tr> count
    1k     TBD        TBD            TBD                     TBD
    5k     TBD        TBD            TBD                     TBD
    10k    TBD        TBD            TBD                     TBD

Evidence trail from planning (2026-07-22):

- `transformScrollWrapper` virtualization affordance:
  `apps/console/node_modules/@astryxdesign/core/dist/Table/types.d.ts`
  (`ScrollWrapperRenderProps` doc comment).
- Shipped Astryx table plugins (no virtualization):
  `apps/console/node_modules/@astryxdesign/core/src/Table/plugins/`.
- Unstable identities and dependency-array-less keydown effect:
  `ui/components/QueryTable.tsx` working tree as of 2026-07-22.

## Interfaces and Dependencies

- **Astryx `Table` plugin API** (`TablePlugin`, `transformScrollWrapper`,
  `TableRowComponentProps`, `useTableRowIndex`, `useTableGroupedRows`): the
  extension surface windowing builds on. Astryx is externally maintained —
  never vendor, fork, or hand-roll an equivalent of one of its primitives.
- **`ui/package.json` peer dependencies**: unchanged by default. Adding
  `@tanstack/react-virtual` (fallback only) is a contract change for every
  React-enabled client app and for generated client scaffolding/docs; it
  requires an explicit Decision Log entry and updates to `ui/AGENTS.md`,
  `docs/agent-guide.md`, and `SKILL.md` in the same change.
- **`DataTable` public props**: gains at most a windowing threshold prop;
  `QueryTable`'s public props are unchanged unless profiling proves a
  per-page threshold override is needed (record here if so).
- **Generated `table_page` adapters** (`internal/generate`): no contract
  change expected; they consume `QueryTable` as today. Fixture clients under
  `internal/compiler/testdata/{native,house}` must be regenerated because the
  materialized catalog bytes change.
- **`ui/AGENTS.md`**: its `QueryTable` contract paragraph must gain the
  windowing threshold semantics when Milestone 5 lands.
