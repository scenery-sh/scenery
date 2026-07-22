# 0133 Candidate Parity Fixes: Row Retention, Row-Intent Prefetch, Export Fidelity

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

The Micro platform's ExecPlan 0051 (completed, its repo,
`docs/agent/exec-plans/completed/0051-generated-page-adoption.md`) cut five
routes over to generated pages but retained four as non-navigation
`/generated` acceptance candidates — Warranty, Service, Documents, In
Service — because each still fails at least one item of the absolute
functionality-parity gate. Petr has green-lit closing those gaps in the
catalog and contract rather than relaxing the gate: the remaining blockers
are mechanism differences, and two of them (row retention during query
transitions, row-intent prefetch) are improvements every table should have
anyway.

The recorded blockers, verbatim from 0051's final parity audit:

- **In Service**: no hover/focus prefetch of project details; no
  `placeholderData` row retention during transitions. (Everything else
  passes.)
- **Warranty**: page-local status-filter total/pagination behavior, CSV
  filename and field semantics, the `No End Date` label, sortable-header
  interaction, locale ordering, exact project activation surface.
- **Service**: CSV filename/formatting, expansion state scoped
  module-global instead of clearing with the page, column order/density,
  sortable-header behavior, locale ordering, broadened row activation.
- **Documents**: shared search/filter/sort state across its two views is
  lost by two-route navigation; weaker notice/loading/error copy; sort
  headers; pagination layout. (The two-view state issue is explicitly OUT
  of this plan — it needs its own template concept and stays a candidate
  blocker unless Petr rules otherwise.)

When this plan completes, In Service should pass its full inventory
outright, and Warranty/Service should be reduced to page-local contract
tuning (labels, column order) that the platform-side cutover pass finishes.
The candidates then expire: each is either cut over or deleted by the
platform follow-up — they must not persist as a third implementation state.

## Progress

- [x] (2026-07-22) Plan authored from 0051's final-disposition audit and
  Petr's green light.
- [x] (2026-07-22) Milestone 1: row retention (`placeholderData`) in `QueryTable`, with retained-result refresh state exposed to slots and visible in the result label.
- [x] (2026-07-22) Milestone 2: row-intent prefetch hook (catalog + contract + generator), deduplicated by row key for each query result.
- [x] (2026-07-22) Milestone 3: export fidelity (contract-controllable filename and
  field formatting; verified against the hand-written CSVs).
- [x] (2026-07-22) Milestone 4: small-gap sweep — expansion-state scoping, locale
  ordering guidance, sortable-header parity notes for the platform pass.
- [x] (2026-07-22) Milestone 5: the platform re-audit cut In Service,
  Warranty, Service, and Documents over to their generated production routes;
  every handwritten owner and `*_candidate` acceptance twin was deleted.
  Authenticated browser acceptance covered their live tables, filters,
  pagination, exact interaction boundaries, and retained workspace state.

## Surprises & Discoveries

- (2026-07-22) Service's expansion state is not module-global in the catalog: `expandedKey` is component-local `QueryTable` state, every search/filter/sort/page transition clears it, and unmount discards it. No Scenery fix was required.
- (2026-07-22) Generated sortable headers already use Astryx `useTableSortable` and call the same `applySort` state transition as the compact sort menu. The platform pass still needs browser comparison of density/copy, but there is no second sort implementation in Scenery.
- (2026-07-22) Warranty and Service server sorting uses Go `cmp.Compare` over strings (`warranty/service.go` and `tickets/service.go`), while the accepted clients used JavaScript `localeCompare`. Exact locale/case/accent ordering is therefore an operation-level platform requirement, not something the generated header can repair.
- (2026-07-22) The legacy CSVs quote every cell. Warranty exports twelve fields, uses empty strings for zero years/missing days, maps status semantically, and downloads `warranties-YYYY-MM-DD.csv`; Service exports eleven fields, truncates `created` to an ISO date, preserves raw scheduled/resolution text, and downloads `service-calls-YYYY-MM-DD.csv`.
- (2026-07-22) Live Service acceptance found that the app-owned Issue button's
  `minWidth: 470` hit box overlapped adjacent project-opening cells. Removing
  that oversized hit area restored component-local expansion; a query change
  reset it, and clicking Issue no longer opened project detail. The shared
  table row guard now also checks the native composed path before activation.

## Decision Log

- (2026-07-22, Petr) **Fix the catalog/contract instead of relaxing the
  parity gate.** Row retention and prefetch are wanted improvements
  globally; export fidelity belongs in the contract. The gate stays
  absolute.
- (2026-07-22, Petr + agent) **Candidates expire with this work.** The
  non-navigation `/generated` twins were sanctioned as acceptance surfaces
  only; Milestone 5 ends with zero candidate routes in the platform repo —
  every one converted or deleted.
- (2026-07-22, agent) **Documents' two-view shared state is out of scope**
  here (template-concept work, not a catalog fix); it is the one blocker
  this plan knowingly leaves standing.
- (2026-07-22, agent) **Use one explicit slot-module hook named by `prefetch_export`.** It is valid only on `row_action` or panel `row_detail`, is imported from the same module as the component, and is typed as `(row) => void | Promise<void>` through `defineTablePageSlots`; inline detail cannot opt in.
- (2026-07-22, agent) **Keep CSV controls column-local and literal.** `export_header`, `export_format = "display" | "raw" | "date"`, `export_empty`, and `export_zero_empty` cover the measured Warranty/Service differences. Existing `status_map` supplies semantic labels, and `{date}` in `file_name` is the sole dynamic filename token.

## Outcomes & Retrospective

The catalog mechanisms and every platform candidate are complete. Generated
production owners now preserve retained rows, deduplicated row intent, exact
CSV bytes, locale-equivalent ordering, and component-local interaction state.
The final browser pass found and fixed the Service hit-box overlap instead of
weakening the parity gate; no `/generated` adoption twin remains.

## Context and Orientation

- **`QueryTable`** (`ui/components/QueryTable.tsx`): owns the TanStack
  `useQuery` for table data (`resultQuery`), all query state, expansion
  (`expandedKey`), selection, and CSV export (`exportRows` at the bottom of
  the file — builds the CSV from `columns`/`cellText` and downloads with
  the contract-supplied `fileName`).
- **Export contract**: `table_page` `export` child currently has `label`,
  `icon`, `file_name` (`internal/spec/source_schemas.go`,
  `tablePageExportSourceSchema`); columns opt out via `export = false`.
  There is no per-column export formatting or header-label override.
- **Row activation**: `row_action` slot child; catalog mounts the app
  component with `{ row, onClose }` on click. There is no pre-activation
  (hover/focus) signal an app could use to prefetch.
- **Coordination**: ExecPlan 0132 (QueryTable performance) touches the same
  file for memoization/stable identities. If both run concurrently,
  sequence the `QueryTable` edits (0133's are small and localized; land
  them first or rebase onto 0132's stabilization — record the order here).
- Validation lanes, fixture regeneration, and the consuming-app loop are as
  documented in ExecPlan 0131's `Concrete Steps`; they apply unchanged.
- The candidates live in the Micro platform repo as
  `apps/platform/src/generated/scenery/react/*_candidate.generated.tsx`
  plus their contracts in `warranty/`, `tickets/`, `documents/`,
  `projects/` package files; their hand-written counterparts are the parity
  reference.

## Milestones

1. **Row retention.** `resultQuery` keeps previous rows during query
   transitions: `placeholderData: (previous) => previous` (TanStack v5
   idiom) so filter/search/page changes never blank the grid. Loading
   affordance must still be visible (the toolbar/result context already
   knows a request is in flight — surface `isPlaceholderData` to the footer
   context so "Showing X of Y" can indicate refresh). Verify the empty and
   error states still render correctly (placeholder must not mask a real
   empty result or an error). Acceptance: on the demo page and the In
   Service candidate, changing a filter keeps rows visible until the new
   result lands.
2. **Row-intent prefetch.** A pre-activation signal, once per row per
   result: catalog `QueryTable` prop `onRowIntent?: (row: Row) => void`
   fired on row `pointerenter`/`focus` (deduplicated per rowKey until the
   query changes). Contract/generator: the `row_action` (and
   `row_detail presentation = "panel"`) slot module may export an optional
   prefetch hook the generated adapter wires to `onRowIntent` — exact
   spelling decided at implementation against how slots are declared today
   (component-reference modules), recorded here. Acceptance: In Service
   candidate prefetches project details on hover, observable in the network
   panel before click.
3. **Export fidelity.** Extend the export contract so a generated CSV can
   byte-match a hand-written one: per-column export header override and
   value formatting where the current `cellText` output differs (dates,
   status labels, empty-value spelling such as `No End Date`). Start by
   diffing the hand-written Warranty/Service CSVs against the candidates'
   output (record the diff in `Artifacts and Notes`), then add the minimal
   contract surface that closes it — prefer reusing `status_map`/appearance
   semantics over inventing formatting attrs. `file_name` already exists;
   confirm both candidates set it to the legacy names.
4. **Small-gap sweep.** (a) Expansion state: investigate Service's
   "module-global expansion state" finding — expansion must reset when the
   page unmounts or the query changes; fix in `QueryTable` state scoping if
   confirmed. (b) Locale ordering: server-mapped sorts must match the old
   client-side `localeCompare` ordering — this is backend collation in the
   platform repo; document the requirement per operation for Milestone 5
   rather than fixing here. (c) Sortable-header parity: verify the
   candidates' clickable-header behavior matches the hand-written pages'
   after the compact-sort work; note any residue.
5. **Platform handoff.** Author the follow-up platform ExecPlan (next free
   number there): re-run each candidate's full feature inventory under
   0051's rules with these fixes live; cut over In Service (expected to
   pass), finish Warranty/Service page-local tuning (labels, column order,
   activation surface) and cut them over; Documents converts only if its
   two-view blocker is separately resolved, otherwise its candidates are
   deleted and the blocker stays recorded. Zero `*_candidate` files remain
   either way.

## Plan of Work

Milestones 1–2 are catalog-first with contract/generator wiring in 2;
Milestone 3 is contract+generator with a verification-driven scope;
Milestone 4 is investigation with targeted fixes. Keep every change
additive; existing contracts must compile unchanged. Extend
`internal/generate` and `internal/compiler` tests per new attr/prop, update
`ui/AGENTS.md`'s QueryTable paragraph (row retention + intent signal), and
regenerate fixture clients per the known spec-revision recipe. Verify each
milestone live against the platform candidates via `ui_catalog` dev mode —
they are the acceptance fixtures this plan exists for.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery` (same lanes as 0131/0132):

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./internal/spec ./internal/compiler ./internal/generate
    go test ./...

Fixture regeneration and consumer verification exactly as in ExecPlan 0131
`Concrete Steps` (lock digests, testdata app regeneration, platform
`scenery generate` + validate + four bun lanes + browser).

Candidate verification URLs (platform dev session):
`/platform/in-service/generated`, plus the warranty/service/documents
candidate routes registered in the platform router.

## Validation and Acceptance

- All scenery lanes green per milestone; staged materialization passes.
- Milestone-level acceptance as listed above, each verified in the browser
  against the live candidates.
- Final acceptance belongs to the platform handoff plan: candidates cut
  over or deleted, `git grep -l "_candidate"` empty in the platform repo,
  and the 0051 parity inventories checked off item-by-item for every page
  that converts.

## Idempotence and Recovery

Ordinary source edits with the known spec-revision recovery recipe. Row
retention has a trivial revert (drop `placeholderData`); the intent signal
is additive and unused until wired; export attrs are additive. If 0132
lands concurrent QueryTable changes, rebase these small diffs onto it —
never fork the file.

## Artifacts and Notes

- Source of requirements: platform 0051 `Decision Log`, "Final disposition"
  entries (2026-07-21, Codex final parity audit) — quoted in `Purpose`.
- CSV diff for Milestone 3 lands here.
- CSV contract diff for Milestone 3:

      Warranty generated before: Project, Serial #, Start, End, Days Left headers; display em dashes for empty cells; static warranties.csv.
      Warranty legacy: Project ID, Serial Number, Warranty Start, Warranty End, Days Remaining; raw empty cells; zero Years empty; status label from the status map; warranties-YYYY-MM-DD.csv.
      Service generated before: display text for every exported field, including full Created; static service-calls.csv.
      Service legacy: raw eleven-field order, Created sliced to YYYY-MM-DD, raw empty Scheduled/Resolution, service-calls-YYYY-MM-DD.csv.

  Platform Milestone 5 should set the measured header overrides, `raw` for scalar legacy fields, `date` for Service `created`, `export_zero_empty` for Warranty years, `{date}` filenames, and change Warranty status-map `unknown` from `Unknown` to `No End Date`. These are consumer declarations, not changes to this Scenery milestone.

## Interfaces and Dependencies

- Catalog: `QueryTableProps.onRowIntent`, footer-context
  `isPlaceholderData`/refresh indicator; no removals.
- Contract: additive export-formatting surface (Milestone 3, spelling
  decided at implementation) and the optional prefetch wiring for
  `row_action`/panel slots.
- TanStack Query v5 `placeholderData` — already a peer dependency; no new
  dependencies.
- Downstream: the platform follow-up plan (Milestone 5) consumes all of
  this; ExecPlan 0132 shares the file and must be sequenced, not merged
  blindly.
