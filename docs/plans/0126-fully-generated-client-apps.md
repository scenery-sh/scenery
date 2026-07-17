# 0126 Fully Generated Client Apps: Route Tree, Shell, and Navigation Generation

This ExecPlan is a living document: update Progress, Surprises & Discoveries,
and the Decision Log as work proceeds.

## Purpose / Big Picture

The product direction (recorded in the root `AGENTS.md` under "Direction:
fully generated clients") is that React-enabled Scenery client apps are
fully generated: Scenery owns the route tree, the app shell, the
navigation, and page mounting, while app-owned code shrinks to declared
local overrides — slot fills and `react_component` resources. Today
Scenery generates individual pages (`content_page`, `table_page`,
`split_page` expand to page/renderer resources per plans 0120 and 0123)
and materializes the binary-owned `@scenery/ui` catalog, but every
consuming app still hand-writes its router, shell, and navigation.

The reference consumer, the Micro platform app
(`~/Repos/Micro/platform/apps/platform`), shows the cost of that split.
Its hand-written `src/router.tsx` maintains a TanStack Router route tree
where most routes declare only a path, a shell component reads the
pathname and selects pages through a parallel hand-rolled pane system
(`AppPages`/`RoutePane` with ~35 `isXRoute` booleans, CSS
visibility-based page switching, and inconsistent mounting lifecycles),
and search-parameter validators for *generated* pages (for example the
mails page's `?mail=` selection) live in hand-written router code the
generator cannot see. That duplication already produced a real defect: a
render-preload storm on navigation mount that made cold navigation to a
generated page appear unresponsive for over a second (fixed app-side on
2026-07-17 by switching to intent preloading).

When this plan is complete, a client app's routing surface is data
declared in `.scn`: each page declaration carries its path and
search-parameter contract; the generator emits per-page route
descriptors, a composed route tree, navigation structure, and an app
shell with declared override slots; and a consuming app's entry point is
reduced to mounting the generated app and registering overrides. Success
is observable in the Micro platform app: `src/router.tsx` (today ~580
lines including the hand-rolled pane system) collapses to registration
of a few hand-written routes against the generated tree, navigation
renders from generated metadata, and `scenery generate --check` plus the
app's typecheck/lint/test/build lanes stay green.

## Progress

- [x] (2026-07-17) Audited the current spec/compiler/generator path and
  the Micro platform router, layout, generated pages, and page-state
  lifecycles.
- [x] (2026-07-17) Reviewed and fixed the M1 authored schema shape:
  repeated typed `search` blocks plus flat optional `nav_*` metadata.
- [x] M1 route contract: page declarations own path + search-parameter
  schema; generator emits per-page route descriptors.
- [x] M2 generated route tree: one generated module composes descriptors
  plus app-registered hand-written routes; TanStack Router adapter.
- [x] M3 generated navigation: nav structure (groups, order, labels,
  hrefs, active-state) generated from package metadata; catalog
  `SideNavigation` consumes it through props/slots.
- [x] M4 generated app shell: layout + router `Outlet` + auth gating
  hooks + declared override slots; app entry mounts the generated app.
- [x] M5 Micro platform cutover: delete hand-written `AppPages`,
  `RoutePane`, and manual route booleans; verify behavior parity.
- [x] M6 docs/validation sync: `docs/local-contract.md`,
  `docs/agent-guide.md`, `SKILL.md`, `ui/AGENTS.md` if touched,
  `docs/knowledge.json`, active plans, harness expectations.

## Surprises & Discoveries

- (2026-07-17) The existing page macros already carry the complete
  route path and title through authored resources into their expanded
  `page`/`renderer` pair. The only duplicated generated-page routing
  data is search validation and navigation placement; no new route
  resource kind is required.
- (2026-07-17) Micro's hand-written route extension surface is larger
  than the three generated pages: roughly 35 app-owned pages must remain
  registerable. One descriptor array can replace both its explicit
  `createRoute` tree and its separate `navigationSections` list, so the
  extension point must carry the same optional navigation record as a
  generated descriptor.
- (2026-07-17) Most Micro pages accept `isActive` only to suppress
  queries while the old router keeps every page mounted. Under real
  route mounting their wrapper can pass `true`; the mails page's
  `hasOpened` workaround becomes unnecessary because React Query owns
  the durable response cache.
- (2026-07-17, pre-plan) Cold navigation to a generated page in the
  Micro platform app stalled >1s while the URL had already changed;
  cause was app-wide `preload="render"` on every nav link firing dozens
  of simultaneous `preloadRoute()` calls at sidebar mount, compounded by
  the hand-rolled pane system giving generated pages a
  conditional-mount lifecycle that hand-written pages did not have.
  Evidence: reproduced and measured in the platform repo (before: >1s
  to route activation; after intent preloading: ~87 ms). This is the
  concrete defect class that owning routing in the generator removes.

## Decision Log

- (2026-07-17, Petr + agent) The router *library* stays an app-side
  peer dependency, like React and TanStack Query already are for the
  catalog. Generated routing output is data plus a thin adapter for the
  app's router (TanStack Router first); the `ui/` catalog remains
  router-agnostic. Rationale: catalog `AGENTS.md` forbids app-owned
  route imports, and pinning a router version inside the binary-owned
  catalog would couple every consumer's upgrade cadence.
- (2026-07-17, Petr + agent) Route-descriptor and route-tree generation
  lives in `internal/generate`, not in `ui/`. Rationale: it is
  TypeScript client generation from compiled `.scn` resources, the same
  ownership as generated pages.
- (2026-07-17, Petr + agent) Overrides flow only through declared slots
  and app-owned `react_component` resources. No mechanism that requires
  apps to edit or fork materialized output. Rationale: existing
  catalog/materialization contract; "fully generated" must not degrade
  into forked generated files.
- (2026-07-17, Petr + agent) No compatibility mode. Consistent with
  scenery's no-legacy rule, apps that adopt generated routing adopt it
  whole per frontend; there is no hybrid page-selection fallback owned
  by Scenery. Hand-written routes register into the generated tree
  through one declared extension point instead.
- (2026-07-17, agent, after consumer review) Page query parameters use
  repeated `search "<name>" { type = <type expression> }` blocks. Query
  parameters are optional by definition; a missing or invalid value is
  omitted by the generated validator. String, boolean, and closed-enum
  types use Scenery's existing type-expression vocabulary. Numeric query
  types remain unsupported until their exact URL/JavaScript range contract
  is specified.
  Rationale: this directly covers `?mail=` and `?view=slots`, keeps
  authored query contracts compact, and avoids a parallel query type
  system.
- (2026-07-17, agent, after consumer review) Optional generated
  navigation data is authored as `nav_group`, `nav_order`, `nav_label`,
  `nav_icon`, and `nav_active_paths` on each page macro. The app route
  extension descriptor exposes the same record. Rationale: page title
  remains the label default, a page with no `nav_group` stays out of
  navigation, and generated and hand-written routes flow through one
  sorting/active-state implementation.
- (2026-07-17, agent, after catalog review) The generated shell owns the
  router `Outlet`, navigation derivation, and catalog shell frame. Apps
  may fill a fixed typed set of slots (auth gate, top bar, pre-content,
  post-content, link component, and icon resolver) when creating the
  generated app. Rationale: this preserves app-specific identity and
  auth without allowing a parallel app-owned shell or route tree.

## Outcomes & Retrospective

Completed on 2026-07-17.

Scenery now emits one current React app surface: `routes.generated.ts` owns
typed generated-page descriptors and search validators, while
`app.generated.tsx` owns the TanStack route tree, intent preloading, `Outlet`,
active navigation, and catalog `ClientAppShell`. `pages.generated.ts` was
removed instead of retained as a compatibility path. App-owned routes use one
`SceneryRouteDescriptor` extension array; auth, top bar, pre/post content,
router-aware links, and icons use fixed typed slots.

The Micro platform cutover deleted its 639-line layout shell and replaced the
580-plus-line parallel route/mount system with a 285-line descriptor
registration file plus focused visual overrides. Every page now mounts through
the generated tree. The mails page's permanent-mount `hasOpened` workaround,
the duplicated generated-page search validators, every `isXRoute` comparison,
and the manual navigation structure are gone.

Validation passed: full Scenery Go tests, generator and catalog TypeScript
lanes, self-harness, Micro generation check, `scenery check`, frontend
typecheck/lint/73 tests/production build, `make verify`,
`make verify-scenery`, and live Chrome verification of generated page
navigation, both search round-trips, active navigation, and auth entry with no
application console errors.

## Context and Orientation

Definitions used throughout:

- *Page declaration*: a `.scn` resource (`content_page`, `table_page`,
  `split_page`, or a plain page/renderer pair) that expands, during
  compilation, into page/renderer resources and is generated into a
  React component under the client's output root (for example
  `react/mails_next.generated.tsx` in the Micro platform app).
- *Route descriptor*: a generated, serializable record per page:
  route path, search-parameter schema and validator, lazy component
  reference, and display metadata (title, nav placement).
- *Shell*: the persistent chrome around routed pages — layout frame,
  navigation, top bar, auth gating — today hand-written per app
  (`src/layout.tsx` + `src/router.tsx` in the platform app).
- *Override slot*: the existing app-owned customization mechanism —
  `react_component` resources referenced from `.scn` and slot-file
  wiring such as the platform app's `mail/mails-next-slots.tsx`.

Where the relevant code lives in this repo:

- `internal/generate/` — TypeScript client generation, page generation,
  catalog materialization, conformance fixtures
  (`internal/generate/testdata/`). New generation targets land here.
- `internal/compiler/`, `internal/spec/` — `.scn` schema and expansion.
  New page-level routing fields (path, search parameters, nav metadata)
  are schema work here, surfaced through `scenery schema` and validated
  diagnostics.
- `ui/` — the binary-embedded catalog. `SideNavigation.tsx`,
  `PageLayout.tsx`, and `TopBar.tsx` are the shell-adjacent components;
  they stay router-agnostic and gain props/slots as needed.
- Consumer reference: `~/Repos/Micro/platform/apps/platform/src/`
  (`router.tsx`, `router-link.tsx`, `layout.tsx`, generated output under
  `src/generated/scenery/`). Read-only from this repo's perspective;
  cutover work happens in that repo against a source-built scenery.

Current state to build from: generated pages already exist and export
components plus path constants consumed by hand-written app routers;
search-parameter validation for generated pages is duplicated in app
code; navigation is fully hand-written; there is no generated route or
shell artifact of any kind.

## Milestones

Each milestone keeps the repo and the reference app testable.

**M1 — Page routing contract and route descriptors.** Page declarations
own their routing surface in `.scn`: route path (already implicit
today), a typed search-parameter contract, and optional display
metadata (title, nav group, nav order, icon reference). The compiler
validates these (new diagnostics in the current specification catalog),
`scenery schema` exposes them, and `internal/generate` emits one route
descriptor module per frontend, for example
`react/routes.generated.ts`, exporting typed descriptors with generated
search validators. No app adoption required yet; descriptors are
additive output.

**M2 — Generated route tree with a TanStack Router adapter.** The
generator emits a composed route-tree module for the frontend: all
generated page descriptors assembled under a shell route, plus one
typed extension point where the app registers hand-written routes
(descriptor-shaped: path, optional validator, component). A thin
generated adapter turns the tree into TanStack Router `createRoute`
calls; the adapter is the only file that imports the router library,
and the router remains an app peer dependency. Micro platform adopts
this for its generated pages while keeping hand-written pages
registered through the extension point.

**M3 — Generated navigation.** Navigation structure (groups, ordering,
labels, hrefs, active-route matching data) is generated from the same
declarations that placed pages in M1. The catalog `SideNavigation`
consumes the structure through props and renders links through an
app-supplied link slot (the app passes its router-aware link
component), keeping the catalog router-agnostic. The platform app's
hand-maintained nav list is replaced by generated structure plus its
existing `RouterLink` passed into the slot.

**M4 — Generated app shell.** The generator emits a shell component per
frontend: layout frame composed from catalog components, generated
navigation, the router `Outlet`, and declared override slots (top-bar
actions, auth gate, pre/post content). Auth gating is a declared slot
the app fills — the shell does not hard-code an auth model. The app
entry point becomes: create client, register overrides and
hand-written routes, mount the generated app.

**M5 — Micro platform cutover.** In the platform repo: every page
renders through the generated tree and shell; `AppPages`, `RoutePane`,
manual `isXRoute` comparisons, and duplicated search validators are
deleted; page state that silently depended on hidden permanent mounting
is moved to URL/Zustand/React Query first (that inventory and migration
is platform-repo work, tracked there, but this milestone gates on it).
Behavior parity is verified in the running app.

**M6 — Documentation and harness sync**, per the Documentation Update
Rules in `AGENTS.md`.

## Plan of Work

Work proceeds milestone-by-milestone; M1 and M2 are the schema- and
generator-heavy core and should land behind golden-output tests before
any consumer adoption. Start in `internal/spec` and `internal/compiler`
with the page routing fields and their diagnostics, driving the shape
from the two concrete consumers that exist today: the platform app's
mails page (`?mail=` string selection) and inbox summary
(`?view=slots` enum), which between them exercise optional strings and
enumerated values. Search-parameter schemas reuse the existing typed
input vocabulary already used for HTTP query decoding rather than
inventing a parallel type system.

Then extend `internal/generate` with the descriptor and tree emitters,
alongside the existing page generation so descriptors and pages come
from one compiled view and cannot drift. Golden outputs live with the
existing TypeScript conformance fixtures under
`internal/generate/testdata/`; the conformance `tsc` project typechecks
the generated route modules against a pinned TanStack Router dev
dependency in the fixture package, which is acceptable because the
*adapter fixture* pins a version for testing while real apps supply
their own.

M3 and M4 interleave generator work with `ui/` catalog work; catalog
edits follow `ui/AGENTS.md` and are iterated live against the platform
app through `envs.local.ui_catalog` dev mode (plan 0122). M5 is
executed in the platform repo against a source-built scenery
(worktree-local `.scenery/harness/bin/scenery`), and its state-migration
prerequisite should be sequenced early there so the cutover is not
blocked at the end.

Interfaces likely to change and their owners: `.scn` page schema
(`internal/spec`, diagnostics catalog), generated TypeScript client
layout (`internal/generate`, `docs/local-contract.md` artifact paths),
catalog component props (`ui/`), and the client-app integration story
(`docs/agent-guide.md`, `SKILL.md`).

## Concrete Steps

These are the first executable steps; extend as milestones open.

1. In this repo, read the current page expansion and generation path
   end-to-end before schema edits: `internal/spec` page kinds,
   `internal/compiler` expansion of `content_page`/`table_page`/
   `split_page`, and `internal/generate` React page emission plus
   `internal/generate/testdata/` fixtures. Record in this plan the
   exact types where path and search metadata already exist.
2. Draft the `.scn` search-parameter contract on the two reference
   pages (mails `?mail=`, inbox summary `?view=slots`) as a schema
   proposal in this plan's Artifacts section; get it reviewed before
   compiler edits.
3. Implement M1 schema + diagnostics; validate with `go test ./...` and
   targeted `go test ./internal/spec ./internal/compiler`.
4. Implement descriptor emission; add golden fixtures; validate with
   `go test ./internal/generate` and
   `bun test internal/generate/testdata/typescript_client_conformance.test.ts`
   plus
   `apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json`
   from the repository root.
5. Point the platform app at the source-built binary, run
   `scenery generate --target typescript_client.public_api --check -o json`
   (from the platform app root), and confirm descriptors materialize
   without breaking existing output.
6. Continue per milestone; keep this section updated with the next
   concrete step at every stopping point.

## Validation and Acceptance

For this repo, per change: `go test ./...`; for generator work
additionally the conformance lanes named in step 4; for catalog work
`apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json`
from the repository root; for substantial changes
`scenery harness self --summary --write`.

For the reference app (platform repo, app root
`~/Repos/Micro/platform`): `scenery generate --check -o json`, frontend
typecheck/lint/tests/production build, and manual navigation
verification in the running dev app (`scenery up`) covering: cold
navigation to a generated page immediately after load (the defect class
in Surprises), search-parameter round-trips (`?mail=`, `?view=slots`),
active-nav highlighting, and auth-gated entry.

Acceptance for the whole plan: the platform app renders every page
through the generated route tree and shell; its hand-written
page-selection system is deleted; navigation is generated; hand-written
routes and overrides go through declared extension points only; all
validation lanes above are green.

## Idempotence and Recovery

All generator and schema work is ordinary source-plus-golden-test
development; re-running tests and `scenery generate --check` is
idempotent. Generated output in consumer apps is materialized
atomically by existing generation transactions; a failed generation
leaves the previous artifact set in place. The platform cutover (M5) is
the one stateful step: land it as a single reviewed change in that repo
so a revert restores the hand-written router wholesale, and keep the
state-migration commits (URL/Zustand moves) separate and earlier so
they survive a cutover revert.

## Artifacts and Notes

- Root `AGENTS.md` section "Direction: fully generated clients"
  (added 2026-07-17) records the standing direction this plan executes.
- Platform-repo context: the 2026-07-17 routing review brief (TanStack
  render-preload fix, `AppPages`/`RoutePane` analysis) lives in that
  repo's session history; its durable conclusions are restated in this
  plan's Purpose and Surprises sections so this file stays
  self-contained.
- Reviewed schema proposal for page search-parameter contracts:

  ```scn
  split_page "mails_next" {
    path      = "/mailsnext"
    # Existing source/title/slots omitted.
    nav_group = "Main"
    nav_order = 20
    nav_label = "MailsNext"
    nav_icon  = "mail"

    search "mail" {
      type = string
    }
  }

  content_page "inbox_summary" {
    path             = "/mailsnext/summary"
    # Existing source/title/slots omitted.
    nav_group        = "UI"
    nav_order        = 60
    nav_label        = "ContentPage"
    nav_active_paths = ["/mailsnext/summary"]

    search "view" {
      type = enum.inbox_summary_view
    }
  }

  enum "inbox_summary_view" {
    value "slots" {}
  }
  ```

  The compiled page-macro resource retains each `search` child and
  `nav_*` field. Expansion copies them into renderer config as it does
  today. `routes.generated.ts` exports component-bearing descriptors
  and exact validators; the TanStack adapter is the only generated
  module that imports `@tanstack/react-router`.

## Interfaces and Dependencies

- Depends on plan 0120/0123 page generation (shipped) and plan 0122
  ui-catalog dev mode (shipped) for the iteration loop.
- Coordinates with plan 0125 (single `@scenery/ui` surface): the
  generated shell should import only through the catalog surface that
  plan blesses; sequence-sensitive only at M4.
- `.scn` schema and diagnostics: `internal/spec`, `internal/compiler`;
  new fields must appear in `scenery schema` output and the checked-in
  diagnostics catalog.
- Generation: `internal/generate` emits `routes.generated.ts` (route
  descriptors), the composed route-tree module, the TanStack Router
  adapter, generated navigation structure, and the shell component;
  artifact paths must be recorded in `docs/local-contract.md` when they
  stabilize.
- Catalog: `ui/SideNavigation.tsx`, `ui/PageLayout.tsx`, `ui/TopBar.tsx`
  gain props/slots; no router imports in `ui/`.
- Consumers: TanStack Router remains an app peer dependency; the
  fixture package pins one version for conformance typechecking only.
