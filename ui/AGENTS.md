# Scenery UI Catalog

## Purpose

`ui/` is the single editable source for `@scenery/ui`. Scenery embeds this tree and materializes it under each React-enabled TypeScript client's `react/scenery-ui/` directory.

## Local contracts

- Components are reusable across client applications. Never add domain-specific pages (mail, projects, orders) or import app-owned routes, state, assets, or generated API types; expose data and composition through typed props and slots.
- Compose Astryx components and tokens with StyleX; a hand-rolled equivalent of an existing Astryx component is a defect, and raw interactive HTML under `ui/components/` fails the architecture check (`FilterPills` is the sole documented exception). Keep React, TanStack Query, Astryx, and StyleX as peer dependencies so each Vite app supplies one runtime copy and one QueryClient.
- Alias imported primitives by role (`BaseEmptyState`, `BaseTheme`), never by vendor prefix, and name conversion helpers for the catalog contract (`tableAlign`, `tableWidth`), not for the dependency implementing them.
- Apps never edit or copy the materialized catalog; they alias `@scenery/ui` and its token subpath per `docs/ui-agent-contract.md` § Vite consumers.
- `embed.go` must include every catalog source directory. `internal/generate` adds generated ownership markers during materialization.
- Export the supported component surface from root `index.ts` only; `tokens.stylex.ts` is the sole subpath exception because StyleX variables must be imported from their defining module. `index.ts` mirrors the full Astryx surface through pinned explicit exports plus a generated `export *` block, and catalog-owned names shadow same-named Astryx exports by design; read `docs/ui-agent-contract.md` § Astryx Surface Mirror before an Astryx bump or export edit.
- Import the semantic var group as `t` from `@scenery/ui/tokens.stylex`; keep its vocabulary curated (app-facing roles, spacing, shape, elevation, typography, duration) and add a token only when a current consumer needs an Astryx variable with no semantic equivalent.
- Use the catalog `Theme` export around apps that consume `t`. It scopes the facade's mode-dependent aliases inside the active Astryx theme; do not replace it with a direct Astryx `Theme` re-export or apply `t` outside that provider.
- App code imports components from `@scenery/ui`, never `@astryxdesign/core/*`; direct imports are warranted only for subpaths the mirror cannot cover (token modules, `theme/*`), and recurring direct use of anything else is a mirror bug to fix.
- Use the shared `Problem`/`RequestState` vocabulary and `queryStateProps` adapter for catalog request lifecycles instead of component-local loading/error unions.
- Keep `ClientAppShell` router-agnostic: generated adapters own route
  selection, active state, and the router outlet, while the shell accepts
  resolved navigation plus fixed visual slots.
- Route provenance is intrinsic: generated descriptors stamp
  `origin: "generated"` and app adapters normalize omitted app-owned
  descriptors to `"authored"` before `SideNavigation`. Reuse the exported
  `NavigationOrigin` and `data-origin` attribute; do not add an authored
  `.scn` field or recreate the icon tint in app code.
- Custom page headers use `PageNavigationToggle` to consume the
  `PageLayoutProvider` navigation state; do not create a second sidebar
  collapse store in the consuming app.
- Windowed `DataTable` rendering observes the Astryx scroll container's size;
  layout or viewport changes must update the visible range without a scroll
  event, and the observer disconnects on unmount.
- Keep `QueryTable` chrome-less: generated `table_page` adapters own the surrounding `Page` shell, stats, header actions, and dialogs, while the catalog component owns controlled query state, CSV export, row detail, and windowing. Read the full behavior contract in `docs/ui-agent-contract.md` § QueryTable before changing any of these behaviors.
- Generated stats tiles share `QueryTable` request state when they set, toggle, or clear a typed filter or predicate; do not build a parallel filter store. Date/datetime presets write local-calendar inclusive bounds into the ordinary paired filter inputs, and invalid datetime cells must render safely instead of exposing `Invalid Date`.
- `WorkspacePage` is the thin generated-workspace shell (Astryx tabs or grouped sidebar, adapter-controlled `?tab=` selection, mount-on-first-visit-then-keep). Full contract: `docs/ui-agent-contract.md` § WorkspacePage.
- `DetailPageLayout`, `DetailSection`, `DetailField`, `DetailRelated`, and `DetailDialog` are the shared layout surface for generated `detail_page` content; one generated content component feeds both routed and dialog wrappers, and domain workflows stay in the app. Full contract: `docs/ui-agent-contract.md` § Detail Pages.
- Do not constrain `ui/` changes to older installed Scenery versions or un-upgraded apps: update compiler, generator, schema, or runtime in the same work, regenerate current consumers, and never add compatibility aliases for stale apps.

## Local iteration

To see catalog edits live in a running client app without rebuilding the
Scenery binary, set that app's `envs.local.ui_catalog` to this directory
(relative to the app root) and run `scenery up`; saved edits re-materialize
`react/scenery-ui/` within about a second, staged TypeScript verification
included, and reach the browser through Vite HMR. Shipping still uses the
embedded copy: rebuild the binary before release and keep `ui_catalog` out of
deployable environments (validation enforces this).

## Verification

From the repository root, run:

```sh
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go test ./internal/generate
```

For a consuming Vite app, regenerate its TypeScript client, then run its typecheck, lint, tests, build, and `scenery generate --check`.
