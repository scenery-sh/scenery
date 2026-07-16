# 0120 Declarative Table Pages: CRUD List Contract, Binary-Owned UI Catalog, Verified React Generation

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
the Decision Log, and Outcomes & Retrospective as work proceeds; a future
agent must be able to resume from this file alone.

## Purpose / Big Picture

Most admin/product pages in scenery client apps are structurally identical: a
one-column layout with a table listing an entity, filtered on timestamp ranges
or enum fields, sortable, cursor-paginated, with each row linking to a detail
route. Today every one of these pages is hand-written React against the
generated TypeScript client. This plan makes that page cost one small `.scn`
declaration:

```hcl
table_page "orders" {
  path   = "/admin/orders"
  source = crud.orders
  title  = "Orders"

  column "number" {}
  column "customer_name" { label = "Customer" }
  column "status" {
    appearance = "badge"
    component  = react_component.order_status_cell
  }
  column "created_at" { label = "Created", appearance = "datetime" }

  filter "status" { component = react_component.order_status_filter }
  filter "created_at" {}

  sort "created_at" { default = "desc" }

  row_link  = "/admin/orders/{id}"
  page_size = 50
}
```

`table_page` is deliberately a component library entry, not a page builder.
It is a small macro over machinery scenery already has: the compiler expands
it into the existing `scenery.page` and `scenery.renderer` resources, and the
TypeScript client target emits one thin generated React component per table
page. All table behavior — layout, table markup, filter widgets, loading and
empty states, pagination, accessibility — lives in one Scenery-owned
`TablePage` React component distributed by the scenery binary itself. The
generator produces typed configuration plus transport wiring, never UI
internals.

The design is fail-closed end to end: the compiler validates every parameter,
field reference, and slot against a component contract before rendering
TypeScript, and the generator typechecks the staged output (including
app-declared override components) with the exact Scenery-managed native
TypeScript checker before
the generated artifact transaction commits. There are no fallbacks, no `as
any`, no runtime component registry, no dynamic imports, and no eject flow.

## Progress

- [x] M1: CRUD list capability allowlist and standard list query contract (2026-07-16)
- [x] M2: Binary-owned `@scenery/ui` catalog with the `TablePage` component and theme tokens (2026-07-16)
- [x] M3: `react_component` and `table_page` resource kinds, validation, and expansion (2026-07-16)
- [x] M4: `typescript_client` `react` block and generated page/adapter/route output (2026-07-16)
- [x] M5: Staged TypeScript verification with the managed native `tsgo` checker (2026-07-16)
- [x] M6: Documentation layer sync, schemas, and harness coverage (2026-07-16)
- [x] Relocated the editable catalog to root `ui/` and removed the obsolete shadcn registry workspace and enforcement (2026-07-16)
- [x] Expanded `@scenery/ui` with the Micro Platform Astryx + StyleX primitives and navigation chrome; the pilot app now consumes only the generated catalog through a bare alias (2026-07-16)
- [x] Moved the pilot's one-column and split-page scaffolds into `@scenery/ui`; one root provider supplies the app-owned navigation toggle (2026-07-16)
- [x] Added the domain-neutral `split_page` macro and typed split slot contract; the Micro pilot's `/mailsnext` transport and wrapper are generated while all mail rendering remains app-owned (2026-07-16)

(M1-M6 completed 2026-07-16.)

## Surprises & Discoveries

- 2026-07-16: A production-built frontend under `scenery up` still needs
  local-only auth injection. Generated table pages now accept an optional
  generated client so the app can reuse its authenticated fetch path without
  editing generated code.
- 2026-07-16: Live pagination in the MicroGRID pilot found that contract
  `int` values are encoded as canonical JSON strings, while the CRUD runtime
  accepted only JSON numbers; it also found that a framework-only rebuild
  could reuse a cached graph without its target contract and emit an unbound
  runtime binary. CRUD pagination now accepts the contract wire encoding, and
  framework drift forces full build preparation so linker metadata is always
  regenerated.
- 2026-07-16: The first MicroGRID Platform pilot exposed strict-TypeScript
  unused imports on pages without custom cells or filters, alphabetical UI
  field ordering, and loaders targeting the frontend root instead of Scenery's
  `/api/` route; the first retry also showed that catalog assets had no trusted
  ownership markers. The generator now emits only used imports, preserves
  authored table order, calls the browser-facing API path, and marks every
  catalog artifact so repeated generation stays idempotent. Runtime build
  preparation also now verifies only cache-materialized React targets during
  cache sync instead of passing source targets with an empty staging set.
- 2026-07-16: `github.com/microsoft/typescript-go` cannot be embedded by an
  external module. The current module has no public compiler package, Go's
  `internal` import rule rejects all checker packages, and upstream documents
  its API as not ready. The supported native-preview distribution does ship
  self-contained platform binaries plus TypeScript library declarations, so
  the existing checksummed Scenery toolchain store can own the exact checker
  without Node, bun, or PATH drift.
- 2026-07-16: A Vite consumer does not need a linked workspace package or a
  copied component tree. Pointing the same `@scenery/ui` alias in TypeScript
  and Vite at the source-materialized `react/scenery-ui/index.ts` lets the
  app's existing Astryx, React, and StyleX toolchain compile the binary-owned
  catalog directly. The staged generator check remains independent because it
  verifies the generated subtree from its sibling stage root.

## Decision Log

All decisions below were made 2026-07-15 by Petr Brazdil with agent design
assistance, during the design conversation that produced this plan.

- **The editable catalog source lives directly under root `ui/`.** The old
  reusable registry workspace was removed. `ui/embed.go` is the only Go bridge;
  `internal/generate` consumes the embedded files and adds ownership markers
  only when materializing them into generated apps. Rationale: the UI source is
  a frequent human/agent iteration surface and must be immediately discoverable
  without a mirrored copy or sync step. Decided 2026-07-16 by Petr Brazdil.

- **Vite apps consume reusable catalog components through one exact bare
  alias.** Both `tsconfig.json` and Vite map `@scenery/ui` to the declared
  client's `react/scenery-ui/index.ts`; the app supplies the React, Astryx,
  and StyleX peers and its normal StyleX transform compiles the materialized
  TSX. There is no local component copy, symlink, or separately versioned npm
  package. Decided 2026-07-16 by Petr Brazdil.

- **`split_page` is generic composition; Scenery never owns a domain page.**
  The declaration binds one typed operation result to required app-owned
  `pane` and `detail` slots plus optional `pane_actions` and `detail_header`
  slots. Scenery owns only layout, transport, loading/error state, and URL
  selection. Mail, project, order, and other domain components stay in the
  client app. Decided 2026-07-16 by Petr Brazdil.

- **`table_page` is a macro, not a new query IR or page platform.** It expands
  to existing `scenery.page` + `scenery.renderer` resources. No runtime
  registry, no renderer plugin system, no eject flow. Rationale: the earlier
  generalized designs (query capability graphs, universal slot abstractions)
  were judged over-designed relative to the "80% of pages are a filtered
  table" problem.
- **CRUD list filter/sort capabilities come from an explicit allowlist block
  on the `crud` resource**, not from "all scalar fields" and not derived from
  `table_page` declarations. Rationale: "all scalars" makes every entity field
  public sortable API surface and bumps the list contract revision on every
  field addition; deriving from UI declarations makes UI edits silently mutate
  the HTTP contract. An explicit allowlist keeps contract changes deliberate
  and visible, and `table_page` validation checks against it.
- **Enum filters are array-shaped on the wire** (`status?: OrderStatus[]`)
  even though v1 UI renders single-select. Rationale: multi-select is the
  first thing every real admin table wants; a scalar wire shape would be a
  breaking contract change to fix later.
- **Cursors bind the query fingerprint.** The opaque cursor encodes the sort
  field, direction, and filter set it was issued under; the server rejects a
  cursor replayed against a different query, and the generated client resets
  the cursor on any filter or sort change. The provider appends the primary
  key as a stable sort tie-breaker. Rationale: a cursor detached from its
  ordering is meaningless and returns garbage silently.
- **The scenery binary owns and materializes the UI catalog package.**
  `@scenery/ui` is written into the app workspace as a managed artifact by the
  same transactional mechanism as generated roots; apps never edit catalog
  component source. Rationale: the binary and an npm-installed package upgrade
  on independent schedules, and under scenery's no-backwards-compatibility
  policy any drift is fatal. Binary-owned materialization makes version skew
  unrepresentable instead of merely detected, so no separate "UI ABI digest
  mismatch" user-facing error class is needed.
- **Styling is theme/tokens only.** Catalog components read design tokens (CSS
  variables) supplied by the app. There is no whole-layout override slot and
  no supported copy-in fork of catalog components. A page that outgrows
  `table_page` is explicitly downgraded: delete the `table_page` block and
  replace it with an ordinary `page` plus a custom application `renderer`.
  Nothing generated is copied; nothing silently stops receiving updates.
- **Per-page customization is declared slots with exact generated prop
  types.** Apps declare `react_component` resources (module + export only) and
  reference them from `column`/`filter`/`toolbar`/`empty` positions. Generated
  code imports them statically and asserts exact slot prop compatibility
  (including rejection of unknown slot keys). No registry calls, no
  convention-based file discovery, no dynamic lookup strings.
- **Verification is two strict phases.** Phase A: compiler validation of the
  declaration against the component contract, with stable SCN diagnostic
  codes. Phase B: the generator stages output beside the final generated
  directory, typechecks it with the app's declared tsconfig, and commits the
  artifact set atomically only on zero diagnostics.
- **The TypeScript checker is the pinned native `tsgo` toolchain artifact.**
  The 2026-07-16 feasibility spike found that Microsoft's native TypeScript
  port exposes only `cmd/tsgo`; all compiler packages are Go-`internal`, and
  its README marks the embedding API "not ready". Scenery therefore owns an
  exact, checksummed native-preview binary through `scenery.toolchain.json`
  and invokes it directly. Generation still requires neither Node nor bun,
  and cannot drift to an app-local compiler. The app's `node_modules` remains
  an authored input for React `.d.ts` resolution. In-process incremental
  checking is deferred until Microsoft publishes a supported API; cold native
  checking is the current fail-closed contract.
- **Verification failures are classified.** "Your override component's props
  are incompatible with the slot" and "your app has unrelated pre-existing
  type errors reachable from an override import" are different diagnostics
  requiring different user actions. Both fail the transaction (fail-closed),
  but the message must say which one occurred.
- **v1 ships a hand-authored component contract in `internal/spec`; the
  `contract.ts` builder DSL and emitted manifest pipeline are deferred** until
  a second declarative component kind (e.g. `detail_page`) exists. Rationale:
  building the `definePageComponent` combinator machinery, manifest format,
  and freshness harness for exactly one component is speculative generality.
  The end state (contracts beside components, committed manifest embedded in
  the binary, freshness-checked so `go build` never invokes bun) is recorded
  here as the destination, not built now.
- **Only scenery releases add declarative component kinds.** Apps cannot
  register their own page kinds. This is the boundary that keeps `table_page`
  a component library rather than a low-code language.
- **Generated routing output is a neutral descriptor array**
  (`generatedPages: [{ path, component }]`), not `RouteObject[]` or any other
  router-specific type. The app writes a three-line adapter to its router.
  Rationale: the generated root stays dependency-free except for the catalog
  contract, matching the rule that generated code must not depend on a
  specific table library either.
- **The generated data loader preserves the typed outcome.** The `TablePage`
  query result type carries the typed problem/outcome channel (auth failure
  vs. server error vs. validation) instead of `throw new Error(message)`, so
  the catalog component can render distinct error states. Decided now because
  it shapes the generated signature that apps will depend on.
- **v1 boundary.** Ships: CRUD-backed list page, explicit columns with
  optional `label`, enum equality filter (array wire shape), datetime range
  filter, single-column sort with declared default, cursor pagination with
  server-side limit clamp, row link, `badge`/`datetime`/`text`/`number`
  appearance hints, cell/filter/toolbar/empty slot overrides. Explicitly not
  built: view-backed sources (`source = view.x` is a follow-up that must not
  change the React generator), arbitrary layout declarations, conditional
  visibility, nested/master-detail tables, inline editing, computed sortable
  columns (display-only computed values are a custom cell; filterable/sortable
  computed values belong in a view), bulk actions, multi-column sort, layout
  override slots, copy-in component forks.

## Outcomes & Retrospective

Completed 2026-07-16. Scenery now has one declarative CRUD-backed table-page
path from `.scn` through compiler expansion, generated React, and runtime list
queries. The shipped contract includes explicit enum/datetime filter and sort
allowlists, fingerprint-bound keyset cursors with primary-key tie-breaking,
typed slot overrides, a binary-owned `TablePage` catalog, neutral route
descriptors, and fail-closed staged checking by an exact checksummed native
`tsgo` artifact. Generation preserves the previous artifact set when checking
fails and classifies override incompatibility, reachable application errors,
and toolchain readiness separately.

The editable catalog source now lives directly under `ui/`; the obsolete
shadcn registry workspace, installer, toolchain lock, environment knobs, and
self-harness enforcement were removed.

The Micro Platform pilot now imports its tables, query states, form controls,
status badges, stat tiles, side navigation, and top bar from the generated
catalog. Its former `src/ui/` and `src/nav/` component trees were removed; the
app retains only route/icon data and slot composition. TypeScript and Vite use
the same `@scenery/ui` alias, and the app's Astryx + StyleX pipeline compiles
the raw materialized catalog in both development and production builds.
The same catalog now owns `Page`, `PageShell`, `SplitPage`, and `PageHeader`;
`PageLayoutProvider` injects the app's navigation toggle once at the shell.

The pilot also proves the second declarative page kind without widening that
catalog boundary: `/mailsnext` is emitted from a generic `split_page`, while
Micro owns the typed inbox operation and every mail-specific slot. Scenery has
no mail resource kind, compiler path, renderer, or catalog component. Live
browser verification matched the handwritten page's split geometry and core
content, exercised URL-backed message selection, and recorded successful
`mail/InboxHttp` traces with no application console errors.

The planned in-process TypeScript embedding was not viable because upstream
does not expose a public embeddable compiler API. Reusing Scenery's existing
managed-toolchain protocol produced a smaller current contract with no PATH,
Node, or bun dependency at generation time; a resident incremental checker
remains intentionally deferred.

Validation completed with the full Go suite, generated TypeScript conformance
and typechecking, catalog contract exactness, live checking by the managed
native `tsgo`, schema CLI inspection, and `scenery harness self --summary
--write`. A live browser acceptance page rendered two linked rows through the
catalog `TablePage`, exercised its enum control, exposed no console errors,
and applied a changed `--scenery-ui-background` token without regeneration.
The first external pilot additionally served 28 real MicroGRID project rows
through the generated page endpoint and verified the next-page cursor.

## Context and Orientation

Terms and where things live today:

- **`.scn` source** is the singular current app model (HCL-like blocks). The
  compiler in `internal/compiler` loads, validates, and expands it into a
  resource graph. Source block schemas and the diagnostic catalog live in
  `internal/spec` (`internal/spec/source_schemas.go`,
  `internal/spec/schemas.go`, `internal/spec/diagnostics_catalog.go`).
  Expansion generators (e.g. CRUD expansion) live in `internal/compiler`
  (see `internal/compiler/data_expand.go`).
- **`scenery.page` / `scenery.renderer`** already exist: a page references a
  `load` binding plus named `action` bindings; renderers attach to a page and
  carry a canonical-JSON `config`. Server-side registration is rendered by
  `internal/generate/generate_application_ui.go`.
- **CRUD expansion** (`scenery.crud`) generates typed list/get/create/update/
  delete services, operations, and HTTP/internal bindings from an entity.
- **TypeScript client generation** is a root `typescript_client "name"` target
  rendered by `internal/generate/generate_typescript*.go` into a declared
  `workspace.managed_generated_roots` entry, with a committed descriptor,
  transactional atomic writes, stale-file cleanup, and `--check` support
  (`scenery generate --target typescript_client.<name> [--check] -o json`).
- **UI code today**: `apps/console/` is the scenery dashboard (Astryx +
  StyleX); `ui/` is the reusable shadcn-style component registry governed by
  `docs/ui-agent-contract.md`. Neither is compiler-known. This plan introduces
  a third thing: a scenery-owned, binary-embedded React catalog whose first
  entry is `TablePage`. Where its source lives in this repo (extending `ui/`
  or a new `apps/ui/`) is decided in M2; what matters architecturally is that
  the binary embeds it and materializes it into app workspaces.
- **Safe filesystem access** for workspace-relative module paths (no symlink
  escapes, stays inside the app workspace) is owned by `internal/scn`; Phase B
  path checks must route through that machinery, not reimplement it.
- **Validation infrastructure**: `scenery harness self` enforces repo
  contracts with a five-second advisory budget for cached lanes; the staged
  TypeScript verification lane gets its own budget and is not added to the
  five-second lane.

The critical path is M1: the standard list query contract is independently
useful (any hand-written page benefits), independently testable, and every
later milestone consumes it. M2–M5 build the vertical for exactly one
component kind, proving declaration → expansion → generated adapter → slot
type assertion → staged typecheck → transactional commit before any
generalization.

## Milestones

Each milestone keeps `go test ./...` green and the repo shippable.

### M1 — CRUD list capability allowlist and standard list query contract

Add an explicit list-capability block to the `scenery.crud` source schema:

    crud "orders" {
      entity = entity.order
      list {
        filters = ["status", "created_at"]
        sorts   = ["created_at", "status"]
        default_sort = { field = "created_at", direction = "desc" }
        max_page_size = 200
      }
    }

Compiler validation rejects filter/sort fields that do not exist on the
entity, non-enum/non-datetime filter fields (v1 supports exactly enum
equality and datetime range), and non-scalar sort fields. Omitting the `list`
block keeps today's behavior (no filter/sort/cursor parameters generated), so
existing apps are unaffected until they opt in.

The generated list operation gains the standard query shape, flowing through
Go contracts, the HTTP binding, OpenAPI, and the TypeScript client:

    ordersList({
      status?: OrderStatus[];          // enum filters: array-shaped, equality/IN
      createdAtFrom?: DateTimeString;  // datetime filters: from/to pair
      createdAtTo?: DateTimeString;
      sort?: "created_at" | "status";
      direction?: "asc" | "desc";
      cursor?: string;
      limit?: number;                  // clamped server-side to max_page_size
    }) -> { items: readonly Order[]; nextCursor?: string }

Cursor rules: the opaque cursor encodes a fingerprint of (sort field,
direction, filter set); the provider rejects a mismatched cursor with a
request-protocol diagnostic (SCN8000 range); the primary key is appended as a
stable tie-breaker; `limit` above `max_page_size` is clamped, not rejected.

### M2 — Binary-owned UI catalog with `TablePage`

Create the catalog package (working name `@scenery/ui`) inside this repo with
its first component:

    <catalog>/pages/TablePage/TablePage.tsx   — layout, table, filters, loading,
                                                empty, pagination, a11y
    <catalog>/pages/TablePage/contract-types.ts — TablePageProps, TablePageQuery,
                                                TablePageResult (typed outcome
                                                channel), TablePageSlots,
                                                TablePageCellProps<Row, Field>,
                                                TablePageFilterProps<Value>,
                                                TablePageToolbarProps,
                                                TablePageEmptyProps,
                                                defineTablePageSlots (with
                                                exactness: unknown slot keys are
                                                type errors)

Styling reads CSS-variable design tokens with documented names and defaults;
that token list is the app's entire styling surface for catalog pages.

The scenery binary embeds the catalog and materializes it into the app
workspace as a managed artifact (same transactional write/check discipline as
generated roots) at a path recorded in the generated descriptor. Apps never
edit materialized catalog files; the materializer treats local edits as
staleness and rewrites them.

### M3 — `react_component` and `table_page` resource kinds

`react_component` is minimal: `module` (workspace-relative path, validated
through `internal/scn` safe filesystem access) and `export`.

`table_page` gets a hand-authored source schema and validation in
`internal/spec` + `internal/compiler` (no contract-builder DSL yet):
parameters `path` (route, required), `source` (crud reference only in v1,
required), `title` (required), `description`, `page_size` (default 50, 1..
`max_page_size`), `row_link` (route template whose parameters must be entity
fields); repeatable labeled `column` blocks (`label`, `appearance` in
auto|text|number|datetime|badge, optional `component` reference), repeatable
labeled `filter` and `sort` blocks (filters/sorts must appear in the source
crud's `list` allowlist), singleton `toolbar` and `empty` slot blocks.

Phase A validation produces stable diagnostics in the spec catalog, roughly a
dozen new codes: unknown parameter (with did-you-mean), missing required
parameter, wrong literal type, out-of-range value, unknown child block,
column/filter/sort referencing a nonexistent entity field, filter/sort not in
the crud list allowlist, duplicate singleton slot, reference to a nonexistent
`react_component`, `row_link` parameter not an entity field.

Expansion produces the derived resources (never authored by the user):

    table_page.orders
      ├── page.orders          path, load = the crud list internal binding
      └── renderer.orders_web  component = scenery.ui.table_page (built-in
                               catalog identity), config = normalized
                               table_page declaration

`scenery schema scenery.table-page -o json` exposes the authored shape;
`scenery list page -o json` and expanded-graph provenance show the derived
resources with RFC 6901 pointers back to the `table_page` spec.

### M4 — `typescript_client` `react` block and generated output

    typescript_client "admin" {
      gateways    = [http_gateway.admin]
      package     = "@acme/admin-client"
      module      = "esm"
      runtime     = "fetch"
      output_root = "apps/admin/src/generated"
      react {
        tsconfig = "apps/admin/tsconfig.json"
      }
    }

No new top-level generation target. The generated root gains a `react/`
subtree participating in the same descriptor, transaction, stale-file
cleanup, and `--check`:

    react/
      orders.generated.tsx   — one per table_page: static imports of the
                               catalog TablePage and declared react_component
                               overrides; typed column/filter/sort definition;
                               loader wiring the generated client list method
                               (typed outcome preserved into TablePageResult);
                               exact slot assertion via defineTablePageSlots
      pages.generated.ts     — neutral descriptor: export const generatedPages
                               = [{ path: "/admin/orders", component: OrdersPage }]
      index.ts

Generated code contains no `as any`, no `as unknown as`, no dynamic imports,
no default-on-failure fallbacks, and no dependency on any router or table
library — only the catalog contract and the declared override modules.

### M5 — Staged TypeScript verification

Pipeline (all before the artifact transaction commits):

    compile .scn → Phase A diagnostics
      → render generated files into a staging directory beside the final root
      → typecheck staged output with the managed native tsgo checker using
        the declared tsconfig
      → zero diagnostics? no → delete staging, generation fails with
        classified diagnostics; yes → atomically commit the artifact set

Requirements: the checker is the exact Scenery-managed native `tsgo` artifact
(no node/bun invocation and no PATH lookup at generation time); the app's
`node_modules` must exist on disk and `scenery
doctor -o json` reports that readiness; failure diagnostics distinguish
(a) declared override module missing / export missing / not a component /
props incompatible with the exact slot type from (b) unrelated pre-existing
type errors reachable through override imports — both fail closed, with
different messages and suggested actions; the verification lane has its own
timing budget outside the self-harness five-second lane. A resident
incremental checker is not part of v1 because the upstream API and incremental
watch implementation are not ready.

An early spike (Concrete Steps, step 0) validated typescript-go embedding
before M2–M4 built on it and selected the managed native artifact path.

### M6 — Documentation and harness sync

Update in the same change series: `docs/local-contract.md` (crud `list` block,
`table_page`, `react_component`, `react` block grammar and JSON schemas,
generated `react/` artifact paths, stability labels), `docs/agent-guide.md`,
`SKILL.md`, `docs/app-development-cookbook.md` (a table-page recipe),
`README.md` if the human-facing overview gains the feature,
`docs/schemas/` for new envelope data schemas, `docs/knowledge.json` for
changed docs, `AGENTS.md` child index if a new catalog directory gains its own
`AGENTS.md`, and this plan's Progress/Decision Log/Outcomes sections.

## Plan of Work

Work proceeds M1 → M6 in order, except step 0 (typescript-go spike) which
happens first because M5 feasibility shapes M4's output layout. M1 lands as
its own PR series and is useful standalone. M2 and M3 can proceed in parallel
after M1's contract shape is fixed (M3 validation needs the `list` allowlist;
M2 needs the query/result types). M4 depends on both. M5 wires verification
into M4's transaction. M6 accompanies every milestone incrementally (each PR
updates its affected doc layers) with a final sweep.

Within each milestone, prefer tests at stable boundaries: source-schema
validation tests in `internal/spec`/`internal/compiler`, codegen golden
output under `internal/generate/testdata/`, TypeScript conformance via
`internal/generate/testdata/typescript_client_conformance.test.ts` and the
generated-clients tsconfig typecheck, runtime HTTP behavior for the list
contract, and CLI JSON envelope tests in `cmd/scenery`.

## Concrete Steps

0. **Spike: embed typescript-go.** Completed 2026-07-16: embedding is not
   viable because the upstream API is not public. This plan was explicitly
   revised to use the exact Scenery-managed native `tsgo` artifact; no
   app-local or silent fallback is permitted.
1. **M1 schema + validation:** extend the `scenery.crud` source schema in
   `internal/spec/source_schemas.go` with the `list` block; add compiler
   validation and new SCN diagnostics with tests.
2. **M1 generation + runtime:** extend CRUD list generation (Go contract,
   provider query with fingerprinted cursor + PK tie-breaker + limit clamp,
   HTTP binding, OpenAPI) in `internal/generate` and the data provider layer;
   golden tests plus an HTTP-level test exercising filter/sort/cursor,
   including the mismatched-cursor rejection.
3. **M1 TS client:** emit the query shape and result type in
   `generate_typescript*.go`; update conformance test and generated-clients
   tsconfig lane.
4. **M2 catalog:** create the catalog package with `TablePage`,
   contract types, `defineTablePageSlots` with exactness, and the token list;
   embed it in the binary; implement managed materialization into the app
   workspace with staleness rewrite; document token names.
5. **M3 kinds:** add `react_component` and `table_page` source schemas,
   validation, diagnostics, and expansion to `page` + `renderer` with
   provenance; `scenery schema scenery.table-page -o json`; fixture-app coverage.
6. **M4 generation:** add the `react` block to the `typescript_client`
   schema; render `react/` files into the same transaction and descriptor;
   golden tests for the generated page, slot assertions, loader, and the
   neutral route descriptor; `--check` covers the new files.
7. **M5 verification:** wire `internal/tscheck` into the generation
   transaction with staging, classification, doctor readiness probe, and the
   exact managed native checker; tests for both failure classes and for
   commit-only-on-zero-diagnostics. A resident checker was explicitly
   deferred after the feasibility spike.
8. **M6 docs:** final documentation sweep per the milestone list; update
   `docs/plans/active.md` status and this plan's Outcomes & Retrospective.

## Validation and Acceptance

Per-change validation (scenery repo default):

    go test ./...
    go test ./cmd/scenery
    scenery harness self --summary --write     # substantial changes

Generated TypeScript lanes when touched:

    scenery generate --target typescript_client.<name> --check -o json
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json

Catalog package lanes:

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json

Run the catalog package lane whenever `ui/` changes.

Acceptance is observable behavior on a fixture app:

1. Declare a crud with a `list` allowlist, a `table_page`, two
   `react_component` overrides, and a `typescript_client` with `react {}`.
2. `scenery check -o json` passes; introducing each Phase A error class
   (unknown param, missing source, bad field, filter outside the allowlist,
   duplicate toolbar) produces its stable diagnostic.
3. `scenery generate --target typescript_client.<name> -o json` materializes
   the catalog, renders `react/orders.generated.tsx` + `pages.generated.ts`,
   typechecks, and commits atomically; `--check` is clean immediately after.
4. Breaking an override component's props (e.g. requiring a prop the slot
   never supplies) fails generation with the incompatible-override
   classification and leaves the previous committed output untouched.
5. Introducing an unrelated type error in an app file imported by an override
   fails generation with the unrelated-app-error classification.
6. Against the running fixture app, the generated list endpoint honors enum
   array filters, datetime ranges, sort + direction, limit clamping, and
   rejects a cursor replayed under a different sort.
7. Mounting `generatedPages` in the fixture frontend renders the table page;
   changing only catalog theme tokens restyles it without regeneration.

## Idempotence and Recovery

All generation is transactional: staged rendering plus atomic commit means a
failed or interrupted `scenery generate` leaves the previously committed
artifact set intact; re-running the command is always safe. Catalog
materialization is idempotent (content-compared, rewritten only when stale).
Schema and diagnostic additions are additive; the only behavior change for
existing apps is opt-in via the crud `list` block and the `react` block.
If a milestone lands partially, the repo remains testable because each
milestone is additive; recovery is re-running the validation matrix and
continuing from the Progress checklist. The checker implementation lives in
`internal/tscheck`, and the exact native artifact is declared in the bundled
toolchain manifest.

## Artifacts and Notes

- Design conversation summary (2026-07-15): three iterations converged from a
  generalized page-builder, to a `table_page` macro with an app-owned
  `TablePage`, to the final shape — binary-owned catalog, declared
  `react_component` slots, staged managed-native TypeScript verification.
- Deferred destination (record only, do not build now): per-component
  `contract.ts` files built with a `definePageComponent` combinator DSL,
  emitting a committed canonical `scenery-ui-manifest.json` that is
  freshness-checked by self-harness and embedded in the binary, so `go build`
  never invokes bun. Trigger to build it: the second declarative component
  kind (e.g. `detail_page`).
- Follow-ups explicitly out of v1 scope are listed in the Decision Log v1
  boundary entry; `source = view.<name>` is the first planned follow-up and
  must not require React generator changes (views must expose the same
  standard list query shape).

## Interfaces and Dependencies

- **New managed toolchain artifact:** the checksummed platform-native
  `@typescript/native-preview` `tsgo` executable. `internal/tscheck` owns its
  invocation and structured diagnostic parsing; app PATH is never consulted.
- **`.scn` surface added:** `crud.<name>.list` block; `react_component` kind;
  `table_page` kind with `column`/`filter`/`sort`/`toolbar`/`empty` children;
  `typescript_client.<name>.react` block. All documented in
  `docs/local-contract.md` with schemas and stability labels.
- **Generated artifacts added:** `react/` subtree in the typescript_client
  managed root; materialized catalog package path recorded in the generated
  descriptor.
- **CLI surfaces touched:** `scenery schema scenery.table-page -o json`,
  `scenery check`, `scenery generate --target typescript_client.<name>
  [--check]`, `scenery doctor` (node_modules readiness probe), expanded-graph
  `scenery list page -o json` showing derived resources.
- **Diagnostics:** ~a dozen new stable SCN codes in the checked-in catalog;
  cursor-mismatch is a request-protocol SCN8000-range code.
- **No new environment variables.** Configuration is `.scn` blocks only.
