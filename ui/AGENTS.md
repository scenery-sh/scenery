# Scenery UI Catalog

## Purpose

`ui/` is the single editable source for `@scenery/ui`. Scenery embeds this tree and materializes it under each React-enabled TypeScript client's `react/scenery-ui/` directory.

## Local contracts

- Components are reusable across client applications. Never add domain-specific pages (mail, projects, orders, or similar), or import app-owned routes, state, assets, or generated API types here; expose data and composition through typed props and slots.
- Compose Astryx components and tokens with StyleX. This is a catalog contract, not a preference: a hand-rolled equivalent of an existing Astryx component is a defect. Raw interactive HTML under `ui/components/` fails the architecture check; `FilterPills` is the sole documented exception because Astryx has no matching faceted-filter primitive. Keep React, TanStack Query, Astryx, and StyleX as peer dependencies so each Vite app supplies one runtime copy and one QueryClient.
- Keep dependency names at the import and documentation boundary. When an imported primitive needs an alias because a catalog wrapper has the same name, use its role (`BaseEmptyState`, `BaseTheme`) rather than a vendor-prefixed local identifier; name conversion helpers for the catalog contract (`tableAlign`, `tableWidth`), not for the dependency implementing them.
- Apps must not edit or copy the materialized catalog. Alias `@scenery/ui` to `<output_root>/react/scenery-ui/index.ts` in both TypeScript and Vite. Apps using semantic tokens must also alias `@scenery/ui/tokens.stylex` to `<output_root>/react/scenery-ui/tokens.stylex.ts` in TypeScript, Vite, and the StyleX compiler plugin's `aliases` option.
- `embed.go` must include every catalog source directory. `internal/generate` adds generated ownership markers during materialization.
- Export the supported component surface from `index.ts`; do not expose internal subpath imports. `tokens.stylex.ts` is the only subpath exception because StyleX variables must be imported from their defining module.
- `index.ts` re-exports the full Astryx component surface: explicit named exports pin every name that predates the mirror (explicit exports take precedence, and `export *` silently drops names two modules both export), and a generated `export * from "@astryxdesign/core/<Module>"` block covers everything else so new Astryx components are available from `@scenery/ui` without catalog edits. When bumping Astryx, add `export *` lines for any new component subpaths and pin (explicitly export) any name a consumer relies on that could become star-star ambiguous. Do not wrap or rename a primitive merely to offer alternate prop spelling; the catalog has one current component API, and catalog-owned names (`Theme`, `QueryTable`, `DataTable`, …) shadow same-named Astryx exports by design.
- Import the semantic var group as `t` from `@scenery/ui/tokens.stylex`. Keep its vocabulary curated around app-facing roles, spacing, shape, elevation, typography, and duration. Add a token only when a current consumer needs an Astryx variable that has no existing semantic equivalent.
- Use the catalog `Theme` export around apps that consume `t`. It scopes the facade's mode-dependent aliases inside the active Astryx theme; do not replace it with a direct Astryx `Theme` re-export or apply `t` outside that provider.
- App code should import components from `@scenery/ui`, not `@astryxdesign/core/*` — the full surface is re-exported, so direct component imports are only warranted for Astryx subpaths the mirror cannot cover (token modules, `theme/*`); recurring direct use of anything else is a bug in the mirror to fix, not a pattern to keep.
- Use the shared `Problem` / `RequestState` vocabulary and `queryStateProps` adapter for catalog request lifecycles instead of introducing component-local loading/error unions.
- Keep `ClientAppShell` router-agnostic. Generated adapters own route
  selection, active-state calculation, and the router outlet; the catalog
  shell accepts resolved navigation plus fixed visual slots.
- Custom page headers use `PageNavigationToggle` to consume the
  `PageLayoutProvider` navigation state; do not create a second sidebar
  collapse store in the consuming app.
- Keep `QueryTable` chrome-less. Generated `table_page` adapters own the surrounding `Page` shell, stats query, header actions, and form-dialog mutations. The catalog component owns controlled search/filter/sort/group controls through `FilterToolbar` (search is debounced before it reaches the query key), TanStack Query list state (the query's `AbortSignal` is forwarded into `load` so superseded requests cancel), loaded-row count/CSV, display-versus-export column selection, Astryx-native collapsible table sections and optional row numbering, inline row expansion or a resizable right-hand detail panel (the opaque panel floats over the table without shrinking it, highlights the selected row, stays viewport-capped on long tables, and supports Escape/arrow-key navigation), row-detail/action composition, empty states, grid, and optional pagination. CRUD pages paginate and cannot group because one cursor page cannot provide honest section counts; binding-backed complete-list pages may group and do not paginate. `FilterToolbar` keeps search visible, duplicates only explicitly pinned selectors inline while retaining every filter in its popover, renders active-filter chips, and keeps group/sort/direction outside filter state and counts.
- Do not constrain `ui/` changes to compatibility with older installed Scenery versions or apps that have not upgraded yet. When the current catalog needs compiler, generator, schema, or runtime changes, update Scenery in the same work and regenerate current consumers; do not add compatibility aliases or preserve an inferior UI contract for stale apps.
- When bumping Astryx, read its release notes and compare new primitives/plugins against catalog-owned behavior before adding or extending wrappers. Record any overlap and the adoption decision in the active UI ExecPlan so upstream capability is not reimplemented locally.

## Local iteration

To see catalog edits live in a running client app without rebuilding the
Scenery binary, set `envs.local.ui_catalog` in that app's `.scenery.json` to
this directory (relative to the app root), for example
`"ui_catalog": "../../scenery/ui"`, and run `scenery up`. Saved edits under
`ui/` re-materialize `react/scenery-ui/` within about a second (staged
TypeScript verification included) and reach the browser through Vite HMR.
Shipping still uses the embedded copy: rebuild the binary before release and
keep `ui_catalog` out of deployable environments (validation enforces this).

## Verification

From the repository root, run:

```sh
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go test ./internal/generate
```

For a consuming Vite app, regenerate its declared TypeScript client and run its typecheck, lint, tests, production build, and `scenery generate --check` lane.
