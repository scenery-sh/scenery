# 0138 Entity Detail Page Template: detail_page

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

The generated read surface has four page kinds — `content_page`,
`table_page`, `split_page`, `workspace_page` — and none of them can express
a routed one-record view (`/warranty/claims/{claim_id}`). This is the last
major structural template gap: the Micro platform's remaining hand-written
pages are dominated by detail-shaped code (~7,400 lines across
`project-detail-dialog.tsx`, `warranty-claims.tsx`, `change-orders.tsx`,
`tickets.tsx`, `invoices.tsx`, `permits.tsx`), and every 0134 workspace
conversion stalls the moment a row needs to click through to a real record
view. `row_detail` covers in-table peeks, not a first-class entity page.

The spec's own primordial example in `docs/spec/SPEC.md` §16.6 is already a
detail page — `page "scene_detail" { path = "/house/scenes/{scene_id}" }` —
but no authored macro exists and the 0126 route generator only emits
static paths. This plan adds:

- a `detail_page` authored macro that expands to ordinary page/renderer
  resources (same lineage rule as the other four kinds);
- dynamic path segments in the route contract and TanStack adapter, with
  typed param → load-binding input mapping;
- declared field sections, embedded related-record tables, and mutation
  actions that open `form_dialog` resources (reusing the exact machinery
  table pages already have);
- both presentations from the start: a routed page and a controlled
  dialog component (per Petr's decision), sharing one generated content
  component;
- a pilot cutover: the warranty claim detail in the Micro platform, whose
  hand-written page already has field groups plus deploy/invoice
  workflows — an exact field-layout target whose richer actions exercise the
  typed app-owned action-slot boundary.

When complete, a domain can declare its record view in `.scn` and get a
routed detail page with typed loading, generated field sections, embedded
related tables scoped by the path param, and audited command actions —
moving the platform another step toward fully generated client apps.

## Progress

- [x] (2026-07-22) Plan authored; design decisions recorded with Petr.
- [x] (2026-07-22) Milestone 1: spec + compiler (`detail_page` schema, path params,
  sections, actions, related tables, presentation; expansion; diagnostics;
  tests; SPEC.md).
- [x] (2026-07-22) Milestone 2: route + generator (dynamic segments, typed params,
  generated page + dialog components, catalog detail-layout components,
  fixture app example).
- [x] (2026-07-22) Milestone 3: pilot cutover (warranty claim detail in the Micro
  platform, including any missing claim-read-by-id backend surface).
- [x] (2026-07-22) Milestone 4: docs/harness sync, full cached validation,
  installed-binary restart, and authenticated browser acceptance.
- [x] (2026-07-22) Post-completion correction: require a declared business
  error mapped by the load HTTP binding to status 404, preventing missing
  records from collapsing into `system.internal`.

## Surprises & Discoveries

- (2026-07-22) Planning-time reference map: `internal/generate/
  generate_typescript_routes.go` emits `path` as a quoted static string and
  page components receive only `{ client }` — no param plumbing exists
  anywhere in the generated route tree. `internal/compiler/form_dialog.go`
  (49 lines, SCN2621) validates dialogs against a mutation binding and is
  presentation-agnostic enough to reuse. `warranty/package.scn` has claim
  create/transition mutations but list-only reads (`workmanship_claims_read`);
  the pilot needs a claim-read-by-id operation added on the platform side.
- (2026-07-22) The pilot's existing action surface is intentionally richer
  than `form_dialog`: status-dependent visibility, dynamic EPC choices,
  positive amount constraints, confirmation, and direct transitions. The
  smallest parity-preserving contract is the plan's existing hand-written-slot
  escape hatch, exposed as one typed `actions { component = ... }` slot. It
  avoids turning `form_dialog` into a domain workflow language.
- (2026-07-22) Exact field parity also requires optional detail values to be
  absent rather than displayed as empty placeholders. A generic
  `hide_empty = true` field flag covers Replacement EPC, deployed date, and
  notes without introducing warranty-specific rendering.
- (2026-07-22) The first real consumer generation caught two fixture gaps:
  `DetailRelated` was imported unconditionally on pages without related
  tables, and implicit path params were read from authored overrides instead
  of the compiler-normalized expanded page. The staged generator fixture now
  covers an implicit param with no related table under the managed checker.
- (2026-07-22) Embedded content must receive the app-owned authenticated
  client explicitly; the generated routed page already receives it from
  `createSceneryApp`, while the hand-written accordion/dialog mount passes the
  same client itself.
- (2026-07-22) The pilot exposed a contract hole after completion: a detail
  source with only a success result compiles, but an ordinary missing row then
  has no typed client completion and the runtime correctly fails closed as
  `system.internal`.

## Decision Log

- (2026-07-22, Petr) **Detail pages mutate from v1** — declared actions
  open generated `form_dialog` mutation dialogs (typed inputs, in-dialog
  errors, query invalidation). Anything richer than dialog-with-fields
  (for example a free-form notes composer) stays a hand-written slot
  component, not generated.
- (2026-07-22, Petr) **Dialog presentation is supported from the start**,
  not deferred: the generator emits both a routed page and a controlled
  dialog component sharing one content component. The platform's project
  detail is an overlay dialog today; the template must not force a
  routed-only migration.
- (2026-07-22, Petr) **Pilot is the warranty claim detail**, not project
  detail. Project detail (1,064 lines, 7 tabs, lazy per-tab queries) is
  the final boss and likely needs tabs-within-detail — deliberately out of
  v1 scope; if needed later, model it by reusing 0134 workspace embedding
  rather than inventing a third tab system.
- (2026-07-22, Petr) **Sequencing**: land after the `/sales` workspace
  content-tab fix, the in-flight 0132–0136 tree commits, and the 0137
  rename. This plan is authored against the post-0137 filenames
  (`app.scn`, `package.scn`, `app.lock.scn`).
- (2026-07-22, agent) **One generated content component, two wrappers.**
  `presentation` does not fork the contract: expansion emits the same
  sections/actions/related surface; the generator wraps it in a routed
  `Page` (path params from the router) or exports a controlled dialog
  (`{ open, onClose, <param> }` props). A `row_action` or hand-written
  page can mount the dialog; the routed page is registered in the
  generated route tree.
- (2026-07-22, agent) **Path params map to load-binding inputs by name,
  with an explicit override.** A `{claim_id}` segment must resolve to a
  compatible scalar input on the load operation; a `param` attribute
  covers mismatched names. Same claim-input discipline as table_page
  query mappings: no input claimed twice, diagnostics for unresolved or
  type-incompatible params.
- (2026-07-22, Petr) **Richer action behavior uses one typed app-owned action
  slot.** `detail_page` keeps simple declared `action` blocks backed by
  `form_dialog`, and MAY also declare `actions { component = ... }`. The pilot
  uses that slot, receiving the loaded entity and an invalidation callback, so
  none of its status, dynamic-choice, validation, confirmation, or direct
  transition behavior is lost. This is the explicit "richer than
  dialog-with-fields stays hand-written" boundary; no generalized mutation
  DSL is added.
- (2026-07-22, Petr) **Empty detail fields are explicit.** Detail fields render
  by default; `hide_empty = true` omits the labeled field only for absent,
  null, or empty-string values. The warranty pilot uses it for
  `deployed_epc_name`, `deployed_at`, and `notes`.
- (2026-07-22, agent) **Typed absence is mandatory.** Every `detail_page`
  source operation declares at least one business error that its selected HTTP
  binding maps to 404. Validation keys off the observable completion mapping,
  not a fixed error name, so domain labels remain free while generated clients
  always have a typed not-found path.

## Outcomes & Retrospective

Completed on 2026-07-22. Scenery now owns a singular `detail_page` contract
with typed dynamic route params, one-record loading, field sections,
`hide_empty`, status badges, related tables, simple generated form actions,
and a typed app-owned action slot. Generation emits one shared content
component plus routed-page and controlled-dialog wrappers, keeps related table
queries scoped to the detail params, and invalidates the complete detail
surface after mutations.

The Micro workmanship-claim pilot added a typed read-by-ID backend and replaced
the duplicated hand-written detail/action/dialog body while retaining the
existing list summary, filters, create flow, and close-on-second-click
accordion. The list page fell from 731 to 514 lines; the remaining 259-line
domain action slot preserves the exact conditional deploy, invoice, recover,
and void workflows without expanding the generic form language.

Authenticated browser acceptance exercised the route, accordion, and dialog
against the same record; proved empty optional fields stay absent; ran pending
through deployed, invoiced, and recovered with live query refresh; rejected a
zero invoice and accepted `$4,500`; and created a second claim whose exact
native confirmation completed the void transition. The final list showed one
recovered claim with `$4,500` and the voided claim exposed no lifecycle
actions. Cached full Go tests, generated-client checks, catalog and app
TypeScript checks, frontend lint/tests/build, both Micro verification targets,
Scenery self-harness, docs inspection, and diff checks passed.

## Context and Orientation

- **Macro expansion precedent**: `internal/compiler/table_page.go` (648
  lines) and `internal/compiler/workspace_page.go` (183 lines) show the
  two patterns this combines — binding-shape validation with input
  claiming, and page-embedding. `detail_page` expands to page/renderer
  resources with lineage, like all page macros (SPEC.md §16.6: "No page
  macro creates a second runtime path").
- **Source contract**: the load source MUST be a call-delivery HTTP
  binding whose operation input record contains the path-param fields and
  whose sole result record is the entity. Sections name fields in that
  result (same resolution style as table columns: label, optional
  `status_map` badge, format). Nested records/lists in the result are
  addressable by section fields via dot paths only if trivially cheap;
  otherwise start with top-level fields and record the limitation.
- **Related tables**: an embedded `table` block references a
  binding-backed table source and maps the path param into one of its
  operation inputs via the existing `predicate`-style claiming — rendered
  chrome-less (the 0134 workspace embedding already proves the
  chrome-suppression path in `ui/components/PageLayout.tsx`).
- **Actions**: `action` blocks reference `form_dialog` resources
  (`internal/compiler/form_dialog.go`, SCN2621). Seeding rule mirrors the
  existing `row_detail.dialog` rule: dialog input fields with
  type-compatible entity fields are pre-filled. Success invalidates the
  detail query and embedded related-table queries.
- **Routes**: `internal/generate/generate_typescript_routes.go` gains
  dynamic segments — `{claim_id}` becomes the TanStack `$claim_id` form;
  the descriptor type gains a typed-params marker; the adapter passes
  `useParams` output to the page component. Navigation metadata
  (`nav_*`) stays optional — detail pages usually don't appear in the
  side nav but do participate in provenance (0136 origin tagging).
- **Catalog**: `ui/` gains the detail layout pieces (field-section grid,
  labeled field, action bar) as Astryx-composed components per the 0130
  contract; QueryTable is reused for embedded related tables.
- **Fixture**: the `house` fixture's `scene_detail` example in SPEC.md is
  the natural fixture app addition — author a real `detail_page` in
  `internal/compiler/testdata/house` so compiler, generator, and staged
  tsc all exercise it.
- **Spec-revision fallout recipe** (will trigger — new schema surface):
  update pinned builtin-lock digests in `internal/compiler/lock_test.go`,
  regenerate fixture apps in `internal/compiler/testdata/{native,house}`,
  refresh the platform `app.lock.scn` integrity/compile_descriptor_digest,
  `scenery generate --target typescript_client.public_api -o json`,
  `go install ./cmd/scenery`, restart the dev session.
- **Pilot target**: `apps/platform/src/pages/warranty-claims.tsx` (731
  lines) renders an inline claim detail (field grid ~lines 382–410) with
  `DeployDialog` and `InvoiceDialog` FormDialog mutations backed by
  `claim_transition_http` plus status-dependent visibility, dynamic choices,
  validation, confirmation, and direct transitions — the generated equivalent
  is a routed `/warranty/claims/{claim_id}` detail with a typed app-owned
  action component, not two weakened unconditional dialog declarations. The
  platform side needs a claim-read-by-id operation + HTTP binding in
  `warranty/` (service method, `package.scn`, tests), following the
  existing `warranty_project_read` pattern.

## Milestones

1. **Spec + compiler.** `detail_page` source schema (path with `{param}`
   segments, `source` load binding, `section` blocks with typed fields,
   `action` blocks referencing `form_dialog`, embedded related `table`
   blocks, `presentation` attribute), expansion to page/renderer with
   lineage, param/input claim validation, new diagnostics in the catalog,
   SPEC.md §16.6 prose, unit tests for expansion and every diagnostic.
   Run the spec-revision fallout recipe.
2. **Route contract + generator + catalog.** Dynamic path segments in
   route descriptors and the TanStack adapter; typed param plumbing into
   generated components; generated detail content component plus routed
   page and controlled dialog wrappers; `ui/` detail-layout components;
   house-fixture `detail_page`; staged tsc
   (`apps/console/node_modules/.bin/tsc -p
   internal/generate/testdata/tsconfig.catalog.json`) and full Go suite
   green.
3. **Pilot: warranty claim detail.** Platform repo: claim-read-by-id
   operation/binding/tests in `warranty/`; author the `detail_page` in
   `warranty/package.scn` with sections and the typed app-owned actions
   component; if a simple mutation fits `form_dialog`, prove it separately.
   If the claims list converts cleanly, add a `row_action` navigation from the
   claims table; regenerate; cut the claim-detail portion out of
   `warranty-claims.tsx`; preserve its richer mutations in the typed app-owned
   `actions` slot rather than weakening them into unconditional generated
   dialogs; all four
   `apps/platform` lanes plus `make verify`; browser-verify the routed
   page and the dialog presentation in the dev session. Functionality
   parity is non-negotiable (0051 rule): the generated detail must not
   lose any field, action, or state the hand-written detail had.
4. **Docs/harness sync.** `docs/local-contract.md`, `docs/agent-guide.md`,
   `SKILL.md`, `docs/app-development-cookbook.md`, `ui/AGENTS.md` if the
   catalog contract changed, knowledge contract for `scenery harness
   self`, active/completed plan registers.

## Plan of Work

Milestone 1 is contract-first: schema and diagnostics with tests before
any generator work, because param claiming and section/field resolution
are where ambiguity lives. Milestone 2 rides the staged-tsc loop; the
route-descriptor change is the only edit touching every consumer, so keep
it additive (static-path descriptors unchanged; params optional).
Milestone 3 follows the standard consumer recipe and lands as one platform
commit (backend read surface, contract, regenerated client, page cut).
Sequencing per the Decision Log: after the `/sales` fix, the 0132–0136
tree, and 0137.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/spec ./internal/compiler ./internal/generate
    go test ./...
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go install ./cmd/scenery
    scenery harness self -o json

From `/Users/petrbrazdil/Repos/Micro/platform` (Milestone 3):

    go test ./warranty/...
    scenery validate
    scenery generate --target typescript_client.public_api -o json
    (cd apps/platform && bun run typecheck && bun run lint && bun test && bun run build)
    make verify
    # dev session restart if schema fallout requires it:
    scenery down && nohup scenery up > /tmp/scenery-up-0138.log 2>&1 &

## Validation and Acceptance

- Compiler unit tests cover: expansion lineage, path-param resolution
  (by-name and explicit override), unresolved/incompatible param
  diagnostics, double-claimed input rejection, section field resolution,
  action → form_dialog reference validation, related-table param mapping,
  and `presentation` values.
- Generated house fixture compiles under staged tsc; full Go suite and
  `scenery harness self -o json` green.
- Platform pilot: `/warranty/claims/{id}` routed page loads a real claim,
  shows every field the hand-written detail showed, simple declared actions
  execute through generated dialogs where applicable, and the typed app-owned
  action slot preserves every richer warranty transition with query
  invalidation observed. The dialog presentation opens/closes from a list surface.
  All platform lanes plus `make verify` green; browser evidence captured
  in Artifacts.
- Parity gate: no field, action, loading/error state, or navigation
  behavior present in the hand-written claim detail is missing from the
  generated one.

## Idempotence and Recovery

Schema and generator changes are additive to existing page kinds; nothing
existing is renamed. Spec-revision fallout is handled by the documented
recipe and is re-runnable. The platform pilot lands as one commit and is
revertable as one commit; the hand-written claim detail is deleted only in
that commit, so a revert restores it. Fixture regeneration is
deterministic; re-run on interruption.

## Artifacts and Notes

- Capture during Milestone 3: browser screenshots of the routed detail
  and dialog presentation, the generated `warranty_claim_detail`
  adapter's param plumbing, and the contract-revision before/after.
- Out-of-scope notes for future plans: tabs-within-detail (project
  detail), inline non-dialog forms (notes composer), dot-path section
  fields if deferred in Milestone 1.

## Interfaces and Dependencies

- New authored surface: `detail_page` macro (path params, source,
  sections, actions, related tables, presentation). New diagnostics in
  the catalog. Route-descriptor contract gains optional typed params —
  additive for existing consumers.
- Reuses: `form_dialog` (SCN2621) unchanged in shape, extended in where
  it may be referenced; 0134 chrome-suppression for embedded tables;
  0136 provenance tagging applies to generated detail pages.
- Depends on: the `/sales` content-tab fix and 0137 rename landing first
  (Decision Log). Platform pilot depends on a new claim-read-by-id
  backend operation in `warranty/`.
- Expect builtin-lock digest churn (new schema attributes) and consumer
  contract-revision changes; regenerate, do not hand-edit.
