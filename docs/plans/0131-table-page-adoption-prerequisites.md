# 0131 Table Page Adoption Prerequisites: Response-Aware Slots, Page Pagination, Row Actions

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

The Micro platform's ExecPlan 0051 (its repo,
`docs/agent/exec-plans/active/0051-generated-page-adoption.md`) audited every
hand-written route against the current templates under a non-negotiable
functionality-parity rule. The result: **zero pages could be converted.**
Every candidate hit at least one template gap that would have lost data or
removed an interaction. The audit's retrospective is effectively a
requirements list for this repo; this plan implements it.

Capabilities, ranked by how many blocked pages each unlocks:

1. **Response-aware slots** — filter, toolbar, footer, and empty slots that
   can see the loaded result (rows, total, truncation) so pages can render
   response-derived filter facets, "Showing X of Y", and truncation notices.
   Unblocks Warranty, Service, Documents, Task Audit, Portfolio.
2. **Page-number pagination for binding sources** — map page/page-size
   inputs and total metadata so server-paginated operations generate honest
   paginated tables instead of complete-list pages that silently show page 1
   as everything. Unblocks Change Log, In Service, Documents (and any
   paginated Wave 1 backend).
3. **Row activation delegated to app code** — a contract concept for "row
   click runs an app-owned component/handler" (the platform's
   `ProjectDetailDialog` pattern, used by ~15 pages). Today only `row_link`
   (navigate) and `row_detail` (inline/panel) exist.
4. **Fixed source predicates** — contract-pinned filter values (e.g. a page
   that is always `disposition = "In Service"`).
5. **Static `content_page`** — drop the mandatory unit-input HTTP binding
   when a content page is purely static (currently SCN2617 forces a dummy
   backend operation).
6. Smaller, only if cheap after the above: clickable stats tiles (tile click
   sets a filter) and datetime filter presets.

When this plan completes, the platform's 0051 reopens and its Wave 1
conversions become executable at full parity.

## Progress

- [x] (2026-07-21) Plan authored from the 0051 parity-audit retrospective.
- [x] (2026-07-21) Milestone 0: verified backend pagination against the
  platform handlers — all four Wave 1 domains are server-paginated with
  totals (table in `Artifacts and Notes`). Milestone 2 is mandatory for
  every Wave 1 page, and Milestone 1's context must carry `total`.
- [ ] Milestone 1: response-aware slot context (catalog + generator).
- [ ] Milestone 2: page-number pagination for binding sources
  (contract + compiler + generator + catalog).
- [ ] Milestone 3: row activation slot (contract + generator + catalog).
- [ ] Milestone 4: fixed source predicates (contract + compiler + generator).
- [ ] Milestone 5: static `content_page` (compiler + generator).
- [ ] Milestone 6 (optional): clickable stats, datetime presets.

## Surprises & Discoveries

- (2026-07-21) The 0051 audit's server-pagination claim is confirmed and the
  earlier page-inventory reading ("complete reads, client-side filtering")
  was wrong: warranty, service calls, documents, and audit trail all
  paginate server-side with page/page-size inputs and total counts
  (evidence: `warranty/service.go` `InventoryParams{Page: 1, PageSize: 50}`
  with `maxPageSize = 100`; `tickets/types.go`, `documents/types.go`,
  `audit/types.go` — see `Artifacts and Notes`). Any complete-list
  conversion of these pages would have silently shown one page as the whole
  dataset — the parity gate caught a real data-loss bug before it shipped.

## Decision Log

- (2026-07-21, Petr + agent) **Template capability, not page-local
  workarounds.** The 0051 audit rejected dummy operations, truncated
  conversions, and slot-side reimplementation of template features; the
  fixes belong in Scenery's contract/generator/catalog so every future app
  benefits.
- (2026-07-21, agent) **Response-aware slots are a catalog/generator
  concern only — no contract change.** Slots are app-owned components; the
  contract already names them. What changes is the props they receive.
- (2026-07-21, agent) **Exact contract spellings for Milestones 2–5 are
  decided at implementation time** against `internal/spec/source_schemas.go`
  conventions, and recorded here. The plan fixes semantics, not syntax.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

Definitions and key files (all paths in this repo unless noted):

- **`QueryTable`** (`ui/components/QueryTable.tsx`) — the catalog runtime
  behind generated `table_page`s. Slot prop types live here:
  `TablePageFilterProps` is currently `{ value, onChange, label }` — no
  result visibility. `TablePageQuery` is
  `{ search, filters, sort, direction, cursor, limit }` — cursor-shaped;
  there is no page-number concept. Pagination UI is Astryx `Pagination`
  driven by `nextCursor` + a client-side cursor history stack.
- **Generator** (`internal/generate/generate_typescript_react.go`) —
  `selectedReactTablePages` sets `paginated: true` only for CRUD sources;
  binding sources are complete-list (`paginated={false}`), and the load
  adapter maps `TablePageQuery` fields onto operation-input fields by name.
- **Compiler** (`internal/compiler/table_page.go`,
  `internal/compiler/content_page.go`) — table_page validation
  (SCN26xx family; grouping/pagination interactions at SCN2623) and the
  content_page source requirement (SCN2617).
- **Schemas** (`internal/spec/source_schemas.go`, `internal/spec/schemas.go`,
  `internal/spec/source_schema_metadata.go`) — authored child/attr shapes for
  the page kinds. Any new attr changes the spec revision: budget for
  builtin-lock digest refresh (`internal/compiler/lock_test.go`), fixture
  client regeneration (`internal/compiler/testdata/{house,native}` app
  roots), and consumer lock/clients refresh — this bit three times in plans
  0129/0130; the recovery commands are in `Concrete Steps`.
- **Reference consumer** — the Micro platform repo
  (`/Users/petrbrazdil/Repos/Micro/platform`), live via `envs.local.ui_catalog`
  dev mode; its blocked pages (named per milestone below) are the acceptance
  fixtures. Its ExecPlan 0051 holds the parity requirements this plan serves.

## Milestones

Each milestone lands green independently
(catalog tsc lane + `go test ./...` + staged consumer verification).

**Milestone 0 — pagination reality check (no code).** For warranty
(`warranty_inventory_read`), service calls (`service_call_list`), documents
(`document_files_list` / `document_missing_list`), change log
(`audit_trail_read`), and in-service (`project_list_projects` usage): read
the platform backend handlers and record per operation whether it truly
paginates server-side, what its input/result pagination fields are, and
whether totals are available. Record the table in `Artifacts and Notes`.
This decides which pages need Milestone 2 at all and what result-metadata
shape Milestone 1 should carry.

**Milestone 1 — response-aware slot context.** Extend the slot contract so
filter, toolbar, empty, and (new) footer slots can see the current result:

- New catalog type, e.g. `TablePageResultContext<Row>`:
  `{ rows, total?, truncated?, filtered, query }` (exact shape from
  Milestone 0's findings).
- `TablePageFilterProps` gains the context (additive prop); `toolbar` and
  `empty` slot components accept it; a new optional `footer` slot renders
  below the grid ("Showing X of Y", truncation notices).
- Generator: wire the new slot pass-throughs (`defineTablePageSlots` gains
  `footer`); contract gains a `footer` slot child mirroring
  `toolbar`/`empty`.
- Acceptance: a demo on the platform's `/scenery-ui/table-page` page shows a
  response-derived filter (options computed from loaded rows) and a footer
  count — the exact patterns Portfolio/Warranty need.

**Milestone 2 — page-number pagination for binding sources.** For binding
operations whose input declares page/page-size fields and whose result
carries a total (names mapped in the contract, not guessed):

- Contract: pagination mapping on the table_page source (spelling decided at
  implementation; something like
  `pagination { page = "page" page_size = "page_size" total = "total" }`).
- Compiler: validates the named fields exist on the operation input/result;
  keeps the grouping-requires-complete-list rule (SCN2623) intact —
  page-paginated binding pages cannot group either.
- Catalog: `QueryTable` gains a page-number mode (numeric `page` in the
  query, total-driven Astryx `Pagination`) alongside the cursor mode.
- Generator: emits the mapping; adapter translates `TablePageQuery` to the
  operation's page fields and surfaces the total to the footer context.
- Acceptance: the platform Change Log page's operation
  (`audit_trail_read`: `page`/`pageSize`/`sortField`/`sortDirection`)
  generates a correctly paginated table in a fixture or behind a test
  contract, with totals in the footer.

**Milestone 3 — row activation slot.** A `row_action` singleton child with a
`component` reference: the slot component receives
`{ row, onClose }` and renders whatever the app wants (typically a dialog —
the platform's `ProjectDetailDialog`); `QueryTable` mounts it when a row is
activated and clears on close. Mutually exclusive with `row_detail`
(diagnostic). Acceptance: a fixture table where row click opens an app-owned
dialog, plus the demo page.

**Milestone 4 — fixed source predicates.** Contract-pinned filter values on
the table_page source (spelling at implementation; the semantic: constant
input fields sent with every load, invisible to the toolbar). Compiler
validates field names against the operation input. Acceptance: a fixture
proving the pinned predicate reaches the operation and no toolbar control
appears for it.

**Milestone 5 — static `content_page`.** Make the source optional: when
absent, no client/load is generated and the content slot renders directly;
SCN2617 only fires when content declares data usage. Acceptance: a fixture
static page compiles without any operation; the platform's Privacy page
becomes convertible.

**Milestone 6 (optional, only if cheap).** Stats tiles accept an optional
filter binding (click → set toolbar filter) and datetime filters accept
preset ranges. If either grows beyond a day, record it as future work and
stop.

## Plan of Work

Work milestones in order; 1 and 2 are the bulk. For each: read the current
types/generator paths named in `Context and Orientation` first; keep every
addition additive (existing contracts must compile unchanged — run the
platform's `scenery generate --check` to prove it); extend
`internal/generate` golden tests and `internal/compiler` diagnostics tests
with each new attr/child; update `docs/ui-agent-contract.md` and
`ui/AGENTS.md` where they enumerate slot props or table_page children; and
verify live against the platform demo page via `ui_catalog` dev mode.

After Milestone 5, hand back to the platform repo: update its ExecPlan 0051
(the Blocked decisions that this plan's milestones resolve get follow-up
entries naming the now-available capability) so the adoption waves can be
re-run there.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./internal/spec ./internal/compiler ./internal/generate
    go test ./...
    go install ./cmd/scenery

Schema-revision fallout recovery (needed after any new contract attr):

    # pinned digests: update internal/compiler/lock_test.go from test output
    (cd internal/compiler/testdata/native && scenery generate --target typescript_client.public_api -o json)
    (cd internal/compiler/testdata/house  && scenery generate --target typescript_client.public_api -o json)

Consumer verification, from `/Users/petrbrazdil/Repos/Micro/platform`:

    # refresh lock integrity/compile_descriptor_digest in scenery.lock.scn, then
    scenery generate --target typescript_client.public_api -o json
    scenery validate
    (cd apps/platform && bun run typecheck && bun run lint && bun test && bun run build)

Browser checks against https://micro.scenery.sh/platform/scenery-ui/table-page
and the relevant blocked routes. `scenery harness self -o json` before
closing each milestone.

## Validation and Acceptance

- All lanes in `Concrete Steps` green per milestone; staged materialization
  (SCN632x) passes against the platform's Astryx.
- Per-milestone acceptance fixtures as listed under `Milestones`.
- Final acceptance: the platform's 0051 Wave 1 blockers that map to
  Milestones 1–5 are each demonstrably resolved — for at least one real page
  (Change Log is the best single end-to-end proof: page pagination + mapped
  sort/filter inputs + response-aware footer + row activation), a full
  parity conversion succeeds in the platform repo under 0051's rules.
  That conversion itself belongs to 0051, not this plan.

## Idempotence and Recovery

Ordinary source edits; every lane re-runs safely. Contract-schema changes
have the known spec-revision blast radius — the recovery commands above are
idempotent. If a milestone's design proves wrong mid-flight, revert the
schema addition (additive attrs are cheap to remove before consumers use
them) and record why in the Decision Log.

## Artifacts and Notes

- Requirements source: Micro platform
  `docs/agent/exec-plans/active/0051-generated-page-adoption.md` — its
  Decision Log's Blocked entries and Outcomes retrospective (2026-07-21
  "Codex parity audit") enumerate the observed gaps with evidence pointers
  into this repo (`internal/compiler/table_page.go`,
  `internal/compiler/content_page.go`, `ui/components/QueryTable.tsx`).
- Milestone 0 backend-pagination table (2026-07-21, read from platform
  domain packages):

  | Domain / operation | Input fields | Result metadata |
  |---|---|---|
  | `warranty_inventory_read` (`warranty/service.go`, `types.go`) | `page`, `page_size` (default 50, max 100) | `total`, `total_count`, `total_pages` |
  | `service_call_list` (`tickets/types.go`) | `page`, `page_size` | `total`, `total_count` |
  | `document_files_list` / `document_missing_list` (`documents/types.go`) | `page`, `page_size` (each) | `total_count` (string-encoded int64), `page`, `page_size` |
  | `audit_trail_read` (`audit/types.go`, `trail.go`) | `Page`, `PageSize` (default 50), `SortField`, `SortDirection` | `TotalCount`, `Page`, `PageSize` |

  Consequence for Milestone 2: the pagination mapping must tolerate both
  snake_case JSON fields and string-encoded totals
  (`total_count,string` in documents).

## Interfaces and Dependencies

- Additive contract surface on `table_page` (`footer` slot child, pagination
  mapping, `row_action`, fixed predicates) and `content_page` (optional
  source); additive catalog types (`TablePageResultContext`, footer slot,
  page-number pagination mode, row-activation mounting).
- No new external dependencies; Astryx `Pagination` already supports the
  numeric-page UI.
- Downstream: the Micro platform repo's 0051 plan consumes every milestone;
  its parity rule ("never less functionality") is the acceptance standard
  this plan is built to satisfy.
