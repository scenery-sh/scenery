# 0136 Generated Page Provenance: Distinguish Generated Routes in Navigation and Beyond

This ExecPlan is a living document: update `Progress`, `Surprises &
Discoveries`, and the `Decision Log` as work proceeds, and fill `Outcomes &
Retrospective` on completion.

## Purpose / Big Picture

As the Micro platform converts routes to generated pages (0051 and its
successors), nothing in the running app tells you which routes are
contract-generated and which are hand-written. Petr wants that visible:
first as a different icon color on the navigation menu entry, and — the
important part — as a durable **provenance signal** the platform can reuse
later for other purposes (badges, a dev inspector, adoption dashboards,
telemetry) without another plumbing pass.

The mechanism: provenance is intrinsic, not authored. A generated route
*knows* it is generated — the generator stamps `origin: "generated"` on the
navigation descriptor it already emits into `routes.generated.ts`; the
catalog's navigation item type carries the field; hand-written router
entries default to `"authored"`. The first consumer is a tinted nav icon;
the field itself is the deliverable.

Nothing is authored in `.scn` and no contract attr is added — this is a
generator-output and catalog-type change only, so there is **no spec
revision fallout** (no lock digests, no fixture-contract changes beyond
regenerated client artifacts).

## Progress

- [ ] (2026-07-22) Plan authored; not started.
- [ ] Milestone 1: `origin` on generated navigation descriptors + catalog
  `SideNavigationItem`.
- [ ] Milestone 2: icon tint for generated entries in `SideNavigation`.
- [ ] Milestone 3: platform adapter pass-through and browser acceptance.

## Surprises & Discoveries

- (2026-07-22) Nothing yet.

## Decision Log

- (2026-07-22, Petr) **Provenance must be a reusable signal, not a styling
  hack.** The icon color is the first consumer; the field outlives it.
- (2026-07-22, agent) **Provenance is intrinsic — never authored.** No
  `.scn` attr: the generator is the sole authority for
  `origin: "generated"`; anything not stamped is `"authored"` by default.
  A contract attr would let the two drift.
- (2026-07-22, agent) **Two values today, room for more.** The type is a
  string union (`"generated" | "authored"`), not a boolean, so future
  granularity (e.g. a workspace shell that is generated but hosts app-owned
  slot tabs) can extend it without churning consumers. Do not add a third
  value speculatively.
- (2026-07-22, agent) **The catalog owns the visual mapping in one place.**
  `SideNavigation` maps `origin` to the icon tint internally (a semantic
  token, not a hardcoded color) and also stamps a
  `data-origin="generated"` attribute on the entry, so app-level styling or
  tooling can hook the same signal without new props.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

- **Generator**: `internal/generate/generate_typescript_react.go` emits
  `routes.generated.ts` for each React-enabled client; every generated
  page route carries a `SceneryNavigationDescriptor`
  (`{ group, order, label, icon, activePaths }` — see any consumer's
  `apps/platform/src/generated/scenery/react/routes.generated.ts`). Find
  the descriptor type definition and its emission site; that is where
  `origin` is stamped.
- **Catalog**: `ui/components/SideNavigation.tsx` defines
  `SideNavigationItem` (a `Pick` over a wider shape) and renders entries
  with their icons; `ClientAppShell` passes resolved navigation through.
  The icon tint should use a semantic token (`t` vocabulary or an Astryx
  color var) that reads clearly in both themes — pick one that is
  noticeable on inspection without shouting (e.g. the accent icon color),
  and verify contrast in light and dark.
- **Platform adapter**: the platform's `apps/platform/src/router.tsx`
  merges generated route descriptors with hand-written navigation entries
  into the items `ClientAppShell` receives. The merge must pass `origin`
  through; hand-written entries simply omit it.
- Validation lanes as in ExecPlan 0131 `Concrete Steps`, minus the
  spec-revision recovery (not needed here); regenerating the platform
  client updates `routes.generated.ts` in place.

## Milestones

1. **The field.** Generator: stamp `origin: "generated"` on every emitted
   navigation descriptor (table, split, content, and — when 0134 lands —
   workspace pages). Catalog: `SideNavigationItem` gains
   `origin?: "generated" | "authored"`; `ClientAppShell` passes it
   through untouched. Golden-test the emission; export the union type
   from `ui/index.ts`.
2. **The first consumer.** `SideNavigation` tints the entry icon when
   `origin === "generated"` (semantic token, both themes verified) and
   stamps `data-origin` on the entry element. No layout change, no text
   change; a11y unaffected (color is additive information here, not the
   only carrier — the data attribute and, later, a tooltip can carry it
   for assistive tech if Petr wants it surfaced).
3. **Platform pass-through + acceptance.** Regenerate the platform
   client; update the router merge to forward `origin`; browser-verify:
   the five converted routes (Change Log, Task Audit, Privacy, Help,
   Testing) plus Projects/Work orders/Mails show tinted icons, every
   hand-written entry does not, and toggling OS theme keeps the tint
   legible. Screenshot into the platform PR/commit message.

## Plan of Work

Small, three sittings. Milestone 1 first end-to-end (field visible in a
regenerated `routes.generated.ts` and in the item type), then the tint,
then the platform sweep. Keep the union and the data attribute exactly as
decided — future consumers (inspector, dashboard, telemetry) build on
those two surfaces and nothing else. Update `ui/AGENTS.md`'s navigation
paragraph with the provenance contract.

## Concrete Steps

From `/Users/petrbrazdil/Repos/scenery`:

    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
    go test ./internal/generate
    go test ./...

From `/Users/petrbrazdil/Repos/Micro/platform`:

    scenery generate --target typescript_client.public_api -o json
    (cd apps/platform && bun run typecheck && bun run lint && bun test && bun run build)

Browser acceptance at `https://micro.scenery.sh/platform/*` (both OS
themes).

## Validation and Acceptance

- Generated `routes.generated.ts` carries `origin: "generated"` on every
  generated route's navigation descriptor; golden tests assert it.
- Catalog typecheck and `go test ./...` green; no spec-revision fallout
  (assert: `scenery validate` in the platform repo passes without lock
  changes).
- Browser: generated entries tinted, hand-written entries not, both
  themes; `data-origin="generated"` present on tinted entries.
- The union type and data attribute are documented in `ui/AGENTS.md` as
  the provenance surface future consumers must use.

## Idempotence and Recovery

Additive field + styling; trivially revertable. Regeneration is
deterministic. No lock or fixture-contract churn expected — if any
appears, something leaked into the contract layer and violates the
"intrinsic, never authored" decision; stop and re-check.

## Artifacts and Notes

- Future-consumer ideas parked here so they don't creep into scope:
  provenance tooltip/badge on the page header, a dev-mode inspector
  overlay listing the owning contract file per route, an adoption
  dashboard ("N of M nav entries generated"), telemetry dimension, and a
  possible third origin value for generated shells hosting app-owned
  slot content (decide only when a real consumer needs it).

## Interfaces and Dependencies

- Generator navigation descriptor (+ golden tests), catalog
  `SideNavigationItem`/`ClientAppShell`/`SideNavigation`, platform router
  merge. No contract surface, no new dependencies.
- Coordinate with 0134: workspace routes must stamp the same field when
  they exist (one line in its generator milestone; cross-referenced
  there).
