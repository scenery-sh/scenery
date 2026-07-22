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
- Route provenance is intrinsic. Generated route descriptors stamp
  `origin: "generated"`; generated app adapters normalize omitted app-owned
  descriptors to `"authored"` before they reach `SideNavigation`. Reuse the
  exported `NavigationOrigin` and `data-origin` navigation-entry attribute for
  future provenance consumers; do not add an authored `.scn` field or recreate
  the icon tint in app code.
- Custom page headers use `PageNavigationToggle` to consume the
  `PageLayoutProvider` navigation state; do not create a second sidebar
  collapse store in the consuming app.
- Windowed `DataTable` rendering observes the Astryx scroll container's size;
  layout or viewport changes must update the visible range without requiring a
  scroll event, and the observer must be disconnected on unmount.
- Keep `QueryTable` chrome-less. Generated `table_page` adapters own the surrounding `Page` shell, stats query, header actions, form-dialog mutations, typed auxiliary result-metadata projection, authored loading/error copy, and response-aware toolbar placement. A toolbar defaults to the page header; `placement = "content"` puts a large workbench immediately above the table. The catalog component owns controlled search/filter/sort/group controls through `FilterToolbar` (search is debounced before it reaches the query key), TanStack Query list state (the query's `AbortSignal` is forwarded into `load` so superseded requests cancel, and prior rows remain visible with an explicit refreshing state until the replacement resolves), loaded-row count/CSV, display-versus-export column selection and exact CSV header/value/dated-filename controls, Astryx-native collapsible table sections and optional row numbering, inline row expansion or a resizable right-hand detail panel (the opaque panel floats over the table without shrinking it, highlights the selected row, stays viewport-capped on long tables, and supports Escape/arrow-key navigation), app-owned row activation, empty/footer slots, grid, and pagination. CRUD pages use cursor pagination, explicitly mapped binding pages use numeric pagination, and neither may group because one page cannot provide honest section counts; binding-backed complete-list pages may group and do not paginate. Complete-list tables above 200 flattened rows use the catalog's Astryx-plugin-compatible fixed-row window: it keeps an overscanned viewport plus spacer rows, preserves absolute numbering/group context, scrolls keyboard selection into view, and temporarily returns to full rendering for inline expansion. Direct `DataTable` callers may tune `windowThreshold` or set it to `Infinity`; paginated `QueryTable` paths disable windowing because their page size is already bounded. Filters, toolbar, empty, and footer consume the typed current-result context (`rows`, optional `total`/`truncated`/projected metadata, filtered state, placeholder/refresh state, query, and query controls); toolbar context is optional before the first result is available. Toolbar controls may set or clear a declared enum filter, set search through the table's debounce/reset path, and refresh the current query; `query.search_hidden = true` hides only the native search input when the app toolbar owns that control. Filter and search changes reset pagination, row expansion, and selection, while refresh preserves query inputs and closes stale row UI. A declared filter with `hidden = true` remains query-mapped and toolbar-controllable but is omitted from the built-in selectors, popover, and chips. `row_action` receives the selected row and `onClose`, mounts outside request-state rendering, and is mutually exclusive with `row_detail`; a row action or panel-detail module may declare one `prefetch_export`, which receives each row once per result on pointer entry or row focus before activation. `FilterToolbar` keeps search visible unless the contract explicitly delegates it, duplicates only explicitly pinned selectors inline while retaining every non-hidden filter in its popover, renders active-filter chips, and keeps group/sort/direction outside filter state and counts.
- `WorkspacePage` is the thin generated-workspace shell. It uses Astryx tabs for the default presentation and the catalog side navigation plus mobile Selector for grouped sidebar presentation. Selection is controlled by the generated adapter through the fixed `?tab=` URL key. It mounts a page-backed tab only after its first visit, then keeps that tab mounted and hidden across switches. Sidebar entries may instead navigate to a destination or remain disabled without content; destinations stay clickable regardless of projected availability, while disabled entries expose their reason. The sidebar content pane visibly owns the active entry label and description because nested generated `Page` components surrender their own shell; those child pages still portal only their active header actions into the workspace header. The workspace route and navigation entry remain singular. Descriptions, counts, and availability are contract values projected without mounting inactive pages, and availability is typed through the generated workspace stats request.
- `DetailPageLayout`, `DetailSection`, `DetailField`, `DetailRelated`, and `DetailDialog` are the shared layout surface for generated `detail_page` content. One generated content component feeds both its routed `Page` wrapper and controlled dialog wrapper. App-owned detail action slots receive the loaded entity, exact route params, a callback that invalidates the detail and related queries, and an optional dialog close callback; domain workflows stay in the app rather than entering this catalog.
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
