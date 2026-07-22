# QueryTable Review Hardening

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current while implementing it.

## Purpose / Big Picture

Close the correctness, safety, accessibility, and performance findings from the
post-cutover review of the generated table workbench. After this work,
`QueryTable` must surface failed refreshes, avoid stale or duplicate requests,
keep selected-row UI synchronized with delivered results, export safe and
portable CSV, expose honest request state to app-owned slots, and retain the
bounded `DataTable` performance contract. `StatTile` must render zero-valued
secondary metrics.

The change preserves the singular current UI catalog API. It does not add a
second table implementation, compatibility layer, or application-specific
behavior.

## Progress

- [x] 2026-07-22: Reproduced every review finding against the pre-change source
  and grouped the confirmed work by request lifecycle, row interaction, export,
  formatting, and performance.
- [x] 2026-07-22: Split the 1,533-line `QueryTable.tsx` into focused contract,
  filter, datetime, value, cell, export, and style modules; the main component
  is below 1,000 lines.
- [x] 2026-07-22: Implemented the confirmed request-lifecycle, selection,
  pagination, prefetch, export, formatting, grouping, windowing, and StatTile
  fixes.
- [x] 2026-07-22: Added dependency-free Bun regression tests, catalog source
  assertions, generated singular-label coverage, and catalog typechecking.
- [x] 2026-07-22: Ran catalog/generated TypeScript, focused Bun, full cached Go,
  and worktree-local self-harness validation; all passed.
- [x] 2026-07-22: Performed the final requirement-by-requirement audit, updated
  outcomes and indexes, and moved this plan from active to completed.

## Surprises & Discoveries

- Bun testdata below `internal/generate` cannot resolve React's automatic JSX
  runtime from `apps/console/node_modules`. Evidence: the first regression run
  failed with `Cannot find module 'react/jsx-dev-runtime'`. The durable fix was
  to extract date/value/export serialization into dependency-free `.ts`
  modules, which made the important rules directly testable without a DOM or a
  second test dependency.
- A `useEffect` reset after `queryKey` changes is too late: TanStack Query can
  observe the new outer key with the previous cursor during the intervening
  render. The table now keys an inner state owner with TanStack's `hashKey`, so
  scope changes remount before any foreign cursor can be requested.
- Lifting collapsed groups exposed that visible-row indexes are not ordinary
  array indexes. Grouped `DataTable` indexes include rows in collapsed sections;
  keyboard reconciliation must retain those absolute flattened indexes while
  excluding hidden rows.
- Both mandatory fixture-client generation commands reported `changed: []`.
  The generated React catalog is materialized outside those committed client
  barrels, while generator unit tests directly prove the new
  `resourceSingular` output.
- The worktree-local self-harness passed every lane, including architecture,
  Go tests, vet, UI catalog typecheck, generated-client conformance/typecheck,
  fixture matrix, and dashboard freshness. Evidence: `.scenery/harness/bin/scenery
  harness self --summary --write` completed with `scenery: self harness ok`.

## Decision Log

- Decision: Keep the public enum filter control single-select while retaining
  array values in `TablePageQuery.filters` for generated list inputs.
  Rationale: every built-in control is single-select; accepting arrays in
  `setFilter` created state the UI could neither display nor edit honestly.
  Date/Author: 2026-07-22 / Codex.
- Decision: Use `dataUpdatedAt` as the prefetch result generation.
  Rationale: TanStack structural sharing may preserve `items` identity across
  a successful refresh, but the contract is once per delivered result.
  Date/Author: 2026-07-22 / Codex.
- Decision: Generate `resourceSingular` from the declared row record name and
  fall back to the neutral word `result` for direct callers.
  Rationale: stripping one trailing `s` corrupts words such as statuses and
  categories, including screen-reader labels.
  Date/Author: 2026-07-22 / Codex.
- Decision: Move the inline expansion column into `DataTable` and feed its
  changing state through context.
  Rationale: the stable data-column memo must not be rebuilt on every expansion;
  the expansion cell alone needs the current expanded key.
  Date/Author: 2026-07-22 / Codex.

## Outcomes & Retrospective

Completed on 2026-07-22. Every review finding was confirmed and closed. Failed
same-key refreshes now replace retained data with an error state; search resets
pagination only when its debounced value commits; a hashed inner query scope
prevents foreign cursors after parent scope changes; and cursor controls are
both visibly disabled and defensively guarded during replacement fetches.

Selected and expanded rows reconcile against each delivered visible result.
Collapsed groups are shared with keyboard navigation, editable/popover Escape
and modifier chords are left alone, duplicate href keys are disambiguated, and
row-intent prefetch is once per successful result generation with rejection
retry.

CSV is formula-hardened UTF-8+BOM with RFC 4180 CRLF rows, empty default cells,
local-calendar dated names, and deferred object-URL cleanup. Datetime chips are
local, malformed values cannot throw, unchanged minute inputs retain hidden
seconds, and datetime cells accept strings, finite epoch numbers, and `Date`.
Numeric group order, safe object fallback text, generated singular labels,
window shrink clamping, and zero StatTile sub-lines are covered.

`QueryTable.tsx` fell from 1,533 to 858 lines through responsibility-based
modules. Inline expansion is now a `DataTable` concern whose context updates
only expansion cells; ordinary data-column definitions no longer depend on
`expandedKey`. No dependency was added.

Validation passed: 17 focused Bun regressions; catalog and generated-client
TypeScript checks; 16 client conformance tests; `go test ./internal/generate`;
`go test ./...`; `go test ./cmd/scenery`; clean fixture regeneration; and the
complete worktree-local self-harness.

## Context and Orientation

`ui/components/QueryTable.tsx` owns table query state and composes
`ui/components/DataTable.tsx`. Generated adapters are emitted by
`internal/generate/generate_typescript_react.go`. Public catalog types are
re-exported from `ui/index.ts`; materialized catalog integrity is checked by
`internal/generate/catalog_test.go`. The catalog TypeScript conformance project
is `internal/generate/testdata/tsconfig.catalog.json`.

The review found these confirmed surfaces:

- request lifecycle: same-key refresh errors, debounced search pagination,
  query-scope reset, placeholder export, honest `isRefreshing`;
- row interaction: editable Escape handling, modifier arrows, cursor races,
  stale selected/expanded rows, collapsed-group navigation, duplicate href
  keys, and retryable per-result prefetch;
- export and formatting: formula injection, BOM/charset, CRLF, local date
  filenames, empty cells, deferred object-URL cleanup, local datetime chips,
  precision-preserving datetime edits, numeric/Date cells, numeric group order,
  safe object fallback, and honest singular labels;
- performance and robustness: stable expansion columns, stable empty group
  defaults, clamped row windows, split source files, and nullish StatTile
  rendering.

## Milestones

1. Make request state and query ownership correct under refresh, debounce,
   pagination, and parent scope changes.
2. Synchronize all selected/expanded/prefetched row state with the current
   delivered result and visible grouped rows.
3. Make CSV and datetime/value formatting safe, portable, and local-calendar
   correct.
4. Preserve bounded rendering and stable column identities while splitting the
   oversized component along existing seams.
5. Prove the behavior through pure regression tests, catalog type/source tests,
   generator tests, full repository tests, and self-harness.

## Plan of Work

Keep `QueryTable` as the state owner but remount its inner state on a hashed
external query scope. Build result context from the currently deliverable
request state and surface any error even when TanStack retains previous data.
Debounce visible search before resetting pagination. Disable and guard
pagination while replacement data is pending.

Lift collapse state into `QueryTable`, reconcile selected and expanded keys
against visible result entries, and make window key handling ignore editable or
nested interactive surfaces and OS modifier chords. Dedupe row intent by
successful-result generation and remove rejected keys for retry.

Extract dependency-free value, datetime, and CSV helpers. Serialize RFC 4180
rows, create a UTF-8 BOM blob, harden formula-leading cells, preserve exact
datetime values when the visible minute did not change, and format local UI
labels without changing wire instants.

Move the expansion control into `DataTable` so ordinary data-column definitions
remain stable. Keep row windowing dependency-free and clamp stale scroll
positions after a result shrinks.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

1. Edit the catalog and generator files named in Context and Orientation.
2. Format touched TypeScript with `bunx biome check --write <files>` and Go with
   `gofmt -w <files>`.
3. Run:

       apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
       bun test internal/generate/testdata/query_table_perf.test.tsx internal/generate/testdata/query_table_regressions.test.tsx
       go test ./internal/generate

4. Regenerate the committed native and house fixture clients because the
   generated adapter now emits an explicit singular resource label.
5. Run the full validation commands in the next section using Go's cache.

## Validation and Acceptance

Acceptance requires all of the following:

- a failed same-key refresh renders the error state rather than stale rows;
- typing from a later page produces no request with the old search and reset
  page; cursor navigation cannot double-advance on placeholder data;
- a changed parent query scope cannot send the previous cursor or filters;
- row selection, inline expansion, keyboard navigation, and prefetch track only
  the current delivered and visible result;
- `TablePageResultContext` distinguishes placeholder replacement from any
  refresh, and enum controls accept one value;
- exported CSV is formula-hardened UTF-8+BOM with CRLF, local `{date}`, empty
  default cells, and deferred URL cleanup;
- datetime filters and cells do not throw or lose unchanged seconds, and local
  chips match local inputs;
- numeric groups, object fallback cells, singular labels, window shrink, and
  `StatTile sub={0}` behave correctly;
- `QueryTable.tsx` remains below 1,000 lines and expansion does not rebuild data
  columns.

Run, without `-count=1`:

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    bun test internal/generate/testdata/query_table_perf.test.tsx internal/generate/testdata/query_table_regressions.test.tsx
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    go test ./internal/generate
    go test ./...
    go test ./cmd/scenery
    .scenery/harness/bin/scenery harness self --summary --write

## Idempotence and Recovery

Formatting, generation, and cached tests are safe to rerun. Fixture generation
must use the worktree source through `go run ./cmd/scenery`; do not install a
shared binary. If validation exposes unrelated dirty work, preserve it and
limit fixes to files owned by this plan. If self-harness needs a local embedded
dashboard, run `./scripts/build-dashboard-ui-embed.sh` and then use the
worktree-local harness binary as required by root `AGENTS.md`.

## Artifacts and Notes

- Original review checklist:
  `/Users/petrbrazdil/.codex/attachments/a1e5cc76-0d35-44b3-97df-d152c7fec099/pasted-text-1.txt`
- Focused logic tests:
  `internal/generate/testdata/query_table_regressions.test.tsx`
- Performance tests:
  `internal/generate/testdata/query_table_perf.test.tsx`
- Completion audit:
  - request errors, debounced search, query-scope remount, refresh context,
    placeholder export, and cursor guards are asserted in
    `internal/generate/catalog_test.go` and typechecked in the catalog project;
  - row reconciliation, collapsed navigation, editable key handling,
    per-result retryable intent, unique row keys, and stable expansion columns
    are visible in `QueryTable.tsx`/`DataTable.tsx`, with performance source
    invariants in `query_table_perf.test.tsx`;
  - formula hardening, BOM/charset, CRLF, local dates, empty cells, URL cleanup,
    datetime safety/precision/value types, numeric group order, object fallback,
    window clamping, and zero secondary stats are covered by the 17 focused Bun
    tests;
  - single-value enum controls have an explicit negative TypeScript assertion,
    and generated singular labels have Go generator assertions.

## Interfaces and Dependencies

`TablePageResultContext` adds `isRefreshing: boolean`.
`TablePageFilterValue` is `string | undefined`.
`QueryTableProps` adds optional `resourceSingular`; generated adapters always
provide it from the row record name. `DataTableProps` adds controlled collapsed
groups and controlled expansion changes. No runtime dependency is added.
