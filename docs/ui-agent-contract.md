# scenery UI Agent Contract

`ui/` is the editable source for Scenery's binary-owned generated-app React catalog. The runnable dashboard lives under `apps/console/` and follows its local `AGENTS.md`.

## Ownership

- Edit catalog components, types, and CSS tokens directly under `ui/`.
- `ui/embed.go` embeds that source into the Scenery binary.
- `internal/generate` materializes it under each React-enabled TypeScript target's `react/scenery-ui/` root.
- Generated apps must not edit the materialized copy; regeneration replaces it.

## Contracts

- Keep the package router-neutral. React, Astryx, and StyleX are peer dependencies supplied once by each consuming app.
- Prefer Astryx components and tokens with StyleX; do not add a second component or styling system.
- Keep generated-page customization in exact typed slots declared by `defineTablePageSlots`. Table filters, toolbar, empty, and footer consume the shared `TablePageResultContext` (`rows`, optional `total`/`truncated`, filtered state, `isPlaceholderData`, `isRefreshing`, and current query); toolbar context is optional before the first result. Enum-filter controls set one value at a time. `row_action` receives `row` and `onClose` and is mutually exclusive with `row_detail`.
- Preserve the CSS variables documented in `docs/local-contract.md`; they are the supported styling surface.
- Keep request/result types aligned with generated React adapters and all three table source modes: cursor-paginated CRUD, explicitly mapped numeric-page bindings, and complete-list bindings. Only complete-list tables may group.
- Static `content_page` slots receive no props; sourced content slots receive typed request state. Do not add a client or query to the static path.
- Source files remain plainly editable. The generator adds ownership markers only to materialized copies.
- Do not move the source back under `internal/generate`; `ui/` is the discoverable UI iteration surface.
- Export supported components from root `index.ts`; generated apps import `@scenery/ui`, never internal catalog subpaths.

## Vite consumers

Map `@scenery/ui` to the declared TypeScript client's `<output_root>/react/scenery-ui/index.ts` in both `tsconfig.json` and `vite.config.ts`. Keep the two paths identical. Apps using semantic tokens must also alias `@scenery/ui/tokens.stylex` to `<output_root>/react/scenery-ui/tokens.stylex.ts` in TypeScript, Vite, and the StyleX compiler plugin's `aliases` option. The app's existing StyleX transform compiles the materialized TSX, so no symlink, workspace package, npm install, or copied component tree is needed. The app must install compatible versions of the peer dependencies declared in `ui/package.json`.

Wrap the app shell once with `PageLayoutProvider` when shared page headers need app-owned navigation state. `Page`, `PageShell`, `SplitPage`, and `PageHeader` then consume that configuration without importing the app store or threading navigation props through every route.

## Astryx Surface Mirror

`ui/index.ts` re-exports the full Astryx component surface: explicit named exports pin every name that predates the mirror (explicit exports take precedence, and `export *` silently drops names two modules both export), and a generated `export * from "@astryxdesign/core/<Module>"` block covers everything else so new Astryx components are available from `@scenery/ui` without catalog edits. When bumping Astryx, add `export *` lines for any new component subpaths and pin (explicitly export) any name a consumer relies on that could become star-star ambiguous. Do not wrap or rename a primitive merely to offer alternate prop spelling; the catalog has one current component API, and catalog-owned names (`Theme`, `QueryTable`, `DataTable`, …) shadow same-named Astryx exports by design.

When bumping Astryx, also read its release notes and compare new primitives/plugins against catalog-owned behavior before adding or extending wrappers. Record any overlap and the adoption decision in the active UI ExecPlan so upstream capability is not reimplemented locally.

## Component Behavior Contracts

### QueryTable

Keep `QueryTable` chrome-less. Generated `table_page` adapters own the surrounding `Page` shell, stats query, header actions, form-dialog mutations, typed auxiliary result-metadata projection, authored loading/error copy, and response-aware toolbar placement. A toolbar defaults to the page header; `placement = "content"` puts a large workbench immediately above the table. The catalog component owns controlled search/filter/sort/group controls through `FilterToolbar` (search is debounced before it reaches the query key), TanStack Query list state (the query's `AbortSignal` is forwarded into `load` so superseded requests cancel, and prior rows remain visible with an explicit refreshing state until the replacement resolves), loaded-row count/CSV, display-versus-export column selection and exact CSV header/value/dated-filename controls, Astryx-native collapsible table sections and optional row numbering, inline row expansion or a resizable right-hand detail panel (the opaque panel floats over the table without shrinking it, highlights the selected row, stays viewport-capped on long tables, and supports Escape/arrow-key navigation), app-owned row activation, empty/footer slots, grid, and pagination.

CRUD pages use cursor pagination, explicitly mapped binding pages use numeric pagination, and neither may group because one page cannot provide honest section counts; binding-backed complete-list pages may group and do not paginate. Complete-list tables above 200 flattened rows use the catalog's Astryx-plugin-compatible fixed-row window: it keeps an overscanned viewport plus spacer rows, preserves absolute numbering/group context, scrolls keyboard selection into view, and temporarily returns to full rendering for inline expansion. Direct `DataTable` callers may tune `windowThreshold` or set it to `Infinity`; paginated `QueryTable` paths disable windowing because their page size is already bounded.

Filters, toolbar, empty, and footer consume the typed current-result context (`rows`, optional `total`/`truncated`/projected metadata, filtered state, `isPlaceholderData` for retained rows from another query, `isRefreshing` for any fetch over delivered rows, query, and query controls); toolbar context is optional before the first result is available. Toolbar controls may set or clear one declared enum-filter value, set search through the table's debounce/reset path, and refresh the current query; `query.search_hidden = true` hides only the native search input when the app toolbar owns that control. Filter and search changes reset pagination, row expansion, and selection, while refresh preserves query inputs and closes stale row UI. A declared filter with `hidden = true` remains query-mapped and toolbar-controllable but is omitted from the built-in selectors, popover, and chips.

Loaded-row CSV uses UTF-8 with a BOM, RFC 4180 line endings, empty data cells, local-calendar dated names, and spreadsheet-formula hardening. `row_action` receives the selected row and `onClose`, mounts outside request-state rendering, and is mutually exclusive with `row_detail`; a row action or panel-detail module may declare one `prefetch_export`, which receives each row once per delivered result on pointer entry or row focus before activation and retries after rejection. `FilterToolbar` keeps search visible unless the contract explicitly delegates it, duplicates only explicitly pinned selectors inline while retaining every non-hidden filter in its popover, renders active-filter chips, and keeps group/sort/direction outside filter state and counts.

### WorkspacePage

`WorkspacePage` is the thin generated-workspace shell. It uses Astryx tabs for the default presentation and the catalog side navigation plus mobile Selector for grouped sidebar presentation. Selection is controlled by the generated adapter through the fixed `?tab=` URL key. It mounts a page-backed tab only after its first visit, then keeps that tab mounted and hidden across switches. Sidebar entries may instead navigate to a destination or remain disabled without content; destinations stay clickable regardless of projected availability, while disabled entries expose their reason. The sidebar content pane visibly owns the active entry label and description because nested generated `Page` components surrender their own shell; those child pages still portal only their active header actions into the workspace header. The workspace route and navigation entry remain singular. Descriptions, counts, and availability are contract values projected without mounting inactive pages, and availability is typed through the generated workspace stats request.

### Detail Pages

`DetailPageLayout`, `DetailSection`, `DetailField`, `DetailRelated`, and `DetailDialog` are the shared layout surface for generated `detail_page` content. One generated content component feeds both its routed `Page` wrapper and controlled dialog wrapper. App-owned detail action slots receive the loaded entity, exact route params, a callback that invalidates the detail and related queries, and an optional dialog close callback; domain workflows stay in the app rather than entering this catalog.

## Work Guidance

Prefer extending the existing catalog over adding another component system. Shared structure and behavior belong here; app-specific routing, authentication, assets, state, route data, and product composition stay in the client app and flow through typed props or slots.

## Verification

Run from the repository root:

```sh
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go test ./internal/generate
go test ./cmd/scenery -run 'TestGenerate|TestHarnessKnowledge'
```

For substantial changes, also run:

```sh
go test ./...
scenery harness self --summary --write
```
