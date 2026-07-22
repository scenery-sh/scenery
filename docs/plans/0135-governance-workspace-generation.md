# 0135 Governance Workspace Generation: From Generic Wire to Typed Module Contracts

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

The Micro platform's `/admin` and `/system` routes render one hand-written
workspace (`apps/platform/src/pages/governance.tsx`) over a single generic
operation: `governance_read(module, scope, search)` returns a module list
(with groups, descriptions, counts, availability), a column-descriptor
array, and untyped rows (`governance_column` / `governance_cell` records).
The 0051 parity audit blocked it: 22 admin + 18 system modules, counted
navigation, response-defined columns, and a bespoke crew manager cannot be
replaced by a couple of static table pages.

The audit's framing hid the decisive fact, confirmed 2026-07-22 by reading
`governance/read.go`: **every module's schema is statically known in Go.**
`adminModules` / `systemModules` are literal definition slices, and a
54-arm switch builds each module's columns, rows, and counts. The
"dynamic" wire is self-imposed, not essential. Governance is therefore not
a dynamic-schema problem; it is forty small, statically-known tables
hidden behind one untyped endpoint.

This plan converts Governance to generated pages by promoting that static
knowledge into typed contracts, composed in a workspace shell. Milestone 1
records the selected end-state below. Petr's instruction to finish every
active ExecPlan selects the recommended smallest typed architecture: Option A.

**Option A — typed module contracts composed via the 0134 workspace
(recommended).** Each module becomes a small typed operation +
`table_page` (columns, counts, search declared in contract); `/admin` and
`/system` become `workspace_page`s whose tab navigation is extended with a
grouped-sidebar presentation for large workspaces. The generic
`governance_read` shrinks module-by-module and dies at the end.
Pros: full typing end-to-end, agent-legible contracts, every table
capability (filters, export, response-aware slots) available per module,
matches the repo's fully-generated-clients direction; the 40 contracts are
mechanical because the Go switch already enumerates them.
Cons: the largest backend refactor of the three; incremental migration
means the generic path coexists until the last module moves.

**Option B — dynamic-columns table template.** Teach `table_page` a
columns-from-response mode (column descriptors in result metadata), keep
the generic wire, generate the shell + one dynamic table.
Pros: smallest platform-side change. Cons: a new template concept that
nothing else needs, permanently untyped rows, filters/export/status maps
stay inexpressible per module — it generates the page without ever making
Governance contract-legible. Single-use template features are suspect.

**Option C — generated shell, app-owned table slot.** A workspace shell is
generated (module nav with counts/availability); the module table remains
an app slot using catalog `DataTable`.
Pros: quickest parity. Cons: the actual content stays hand-written; this
is a facade, not a conversion.

## Progress

- [x] (2026-07-22) Plan authored with the static-registry finding.
- [x] 2026-07-22 Milestone 1 design decisions recorded: Option A,
  module-by-module migration, workspace sidebar presentation, app-owned crew
  content tab, and nav-exposed modules first.

## Surprises & Discoveries

- (2026-07-22) The 0051 audit's "response-defined columns" are statically
  defined in Go: `governance/read.go` lines ~21–80 hold literal
  `adminModules` / `systemModules` definition slices (id, label, group,
  description, availability), and a switch with 54 cases builds per-module
  counts, columns, and rows. Evidence: grep for `moduleDefinition{` and
  `case "` in that file. Consequence: typed per-module contracts are
  mechanical extraction, not new design.

## Decision Log

- (2026-07-22, Petr + agent) **Option A: typed module contracts.** The generic
  response-defined-column wire is deleted after extracting every statically
  known module. Options B and C are rejected because they preserve an untyped
  or hand-written content path solely for this page.
- (2026-07-22, Petr + agent) **Incremental extraction, singular final state.**
  Modules move one at a time while the old read serves only the unconverted
  set; the same plan deletes the generic operation, records, switch, and page
  immediately after the final extraction.
- (2026-07-22, Petr + agent) **Extend `workspace_page`, do not invent a
  Governance shell.** `presentation = "sidebar"` adds grouped entries with
  count and availability fields; the ordinary tab presentation remains the
  default.
- (2026-07-22, Petr + agent) **`SplitPage` is reused at the layout layer
  only, never at the contract layer.** Petr proposed using split_page for
  Governance's layout. Resolution: the workspace sidebar presentation
  renders through the existing catalog `SplitPage` layout primitives
  (sidebar pane = generated module directory; detail pane = embedded
  chrome-less page; selection via the same query-parameter mechanics) — no
  new layout chrome. But Governance is NOT a `split_page` contract:
  split_page's sidebar is a data list (query rows, app slots, selection =
  a record), while a workspace sidebar is a page directory
  (contract-declared entries, selection = which embedded page renders).
  Overloading split_page's contract with page-embedding children would
  make one kind mean two things; the semantic split keeps both legible.
- (2026-07-22, Petr + agent) **Crew manager is an app-owned `content_page`
  tab.** Its domain-specific interactions stay outside the catalog while the
  route, grouped navigation, URL state, availability, and counts remain
  generated.
- (2026-07-22, Petr + agent) **Conversion order is risk-first.** Extract `ahj`
  and `organizations`, then convert remaining modules group-by-group, leaving
  the crew content tab and final generic-wire deletion for the final batch.
- (2026-07-22, Petr + agent) **Sequence after the shared foundations.** Land
  0133, 0132, and 0134 before platform governance conversion so the consumer
  migrates once onto the final table/workspace APIs.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

- **Hand-written page**: `apps/platform/src/pages/governance.tsx`
  (~584 lines; `/admin` and `/system` wrap it with `defaultModule` +
  `scope`). Module selection is URL state (`?module=`); columns are built
  from `data.columns`; module navigation shows groups, counts, and
  unavailable states; a bespoke crew manager mounts for the crews module.
- **Backend**: `governance/` package at the platform repo root —
  `read.go` (registry + switch), `types.go`, `authz.go`,
  `notifications.go` (separate notification operations also in the
  package), `db/` queries. The contract
  (`governance/scenery.package.scn`) declares the generic records and
  `governance_read_http` plus notification bindings.
- **Scenery side**: Option A needs the 0134 `workspace_page` (dependency)
  plus, likely, one extension to it — a navigation presentation suited to
  40 grouped entries (sidebar with group headers, counts, availability
  states) instead of a horizontal tab strip. That extension is the only
  new template surface Option A requires and should be designed as part
  of 0134's Milestone 1 or immediately after it, recorded in both plans.
- **Parity gate**: platform 0051 rules apply unchanged to the eventual
  conversion — full feature inventory per module view, including the
  counted navigation, availability handling, search behavior, truncation
  notices, and the crew manager.
- Validation lanes and spec-revision recovery: as in ExecPlan 0131
  `Concrete Steps`.

## Milestones

1. **Design gate (Petr + agent).** Answer the pending Decision Log
   questions; record each with rationale. Deliverable: the chosen option
   and, if A, the migration order (recommended: start with the two
   nav-exposed modules — `ahj`, `organizations` — then batch the
   remaining modules by group).
2. *(shape assumes Option A; rewrite this section if B/C is chosen)*
   **Workspace navigation extension.** In coordination with 0134: a
   sidebar-style navigation presentation for `workspace_page` (grouped
   entries, count badges, unavailable states, URL-synced selection).
3. **Module extraction pattern.** Convert the first module end-to-end as
   the pattern: typed operation (input: search and any module filters;
   result: typed rows + count), binding, `table_page`, workspace tab
   entry; delete its arm from the `read.go` switch. Prove the 0051 parity
   inventory for that module view. Record the per-module recipe here so
   the remaining modules are mechanical.
4. **Batch conversion.** Remaining modules by group, several per change,
   each with the parity inventory; the crew manager converts per the
   Milestone 1 decision. `governance_read` shrinks as modules leave it.
5. **Retire the generic wire.** Delete `governance_read`, the generic
   records, and the hand-written page + router entries once the last
   module is typed; `/admin` and `/system` are generated `workspace_page`
   routes. Nav/notification operations in the package are untouched.

## Plan of Work

Milestone 1 is a conversation, not code — the questions are enumerated in
the Decision Log and mirrored to Petr directly. Milestones 2–5 execute in
order once decided; Milestone 3's single-module pattern is the risk
burn-down (it exercises the workspace sidebar, a typed extraction, and
the parity inventory at minimum cost). All contract work is additive
until Milestone 5's deletion pass. The platform-side conversions run
under a companion platform ExecPlan created at Milestone 3, following the
0051/0133 handoff pattern.

## Concrete Steps

Scenery lanes, fixture regeneration, and consumer verification exactly as
ExecPlan 0131 `Concrete Steps`. Platform-side per module: extend the
domain package (operation + handler + tests), author the `table_page`,
regenerate, run the four `apps/platform` bun lanes from that directory,
`make verify`, and browser-verify the module view against its hand-written
counterpart at `https://micro.scenery.sh/platform/admin?module=<id>`.

## Validation and Acceptance

- Milestone 1: decisions recorded here; no code accepted before it.
- Milestone 3: the pilot module passes its full 0051-style inventory in
  the browser (navigation counts, availability, search, truncation, row
  surfaces), with its typed contract compiled and its switch arm deleted.
- Final: `/admin` and `/system` are generated; `git grep governance_read`
  finds only history; every module view passed its inventory; the
  hand-written page is deleted.

## Idempotence and Recovery

Module-by-module migration is inherently resumable: each module's
extraction is an independent change, and the generic read keeps serving
unconverted modules throughout (Option A's coexistence is deliberate,
time-boxed by Milestone 5, and is not a "dead compatibility path" while
migration is in flight — it becomes one the moment the last module
converts, which is why Milestone 5 deletes it in the same plan).
Spec-revision fallout recovery per the 0131 recipe.

## Artifacts and Notes

- Static-registry evidence (2026-07-22): `governance/read.go` —
  `adminModules`/`systemModules` literals; 54 `case "` arms covering
  counts and module content; `buildModules(definitions, counts)`;
  `chooseModule`. The audit's "response-defined columns" claim traced to
  `governance.tsx` building `Column[]` from `data.columns` — true at the
  wire, false at the source.
- Per-module extraction recipe lands here after Milestone 3.

## Interfaces and Dependencies

- **Depends on ExecPlan 0134** (`workspace_page`) for Option A, plus the
  sidebar-navigation presentation this plan proposes into it. If 0134's
  design gate rejects that extension, this plan's Milestone 2 hosts it as
  a Governance-specific shell instead — record the split.
- Platform `governance/` package: operations, handlers, db queries, and
  contract evolve per module; notification operations unaffected.
- The 0051 parity gate (platform repo) is the acceptance standard for
  every module conversion.
- No new runtime dependencies.
