# Scenery UI Catalog

## Purpose

`ui/` is the single editable source for `@scenery/ui`. Scenery embeds this tree and materializes it under each React-enabled TypeScript client's `react/scenery-ui/` directory.

## Local contracts

- Components are reusable across client applications. Never add domain-specific pages (mail, projects, orders, or similar), or import app-owned routes, state, assets, or generated API types here; expose data and composition through typed props and slots.
- Use Astryx components and tokens with StyleX. Keep React, Astryx, and StyleX as peer dependencies so each Vite app supplies one runtime copy.
- Apps must not edit or copy the materialized catalog. They may alias `@scenery/ui` to `<output_root>/react/scenery-ui/index.ts` in both TypeScript and Vite.
- `embed.go` must include every catalog source directory. `internal/generate` adds generated ownership markers during materialization.
- Export the supported surface from `index.ts`; do not expose internal subpath imports.
- Use the shared `Problem` / `RequestState` vocabulary and `queryStateProps` adapter for catalog request lifecycles instead of introducing component-local loading/error unions.
- Keep `QueryTable` chrome-less. Generated `table_page` adapters own the surrounding `Page` shell and map `toolbar` to page actions; the catalog component owns only query controls, request states, grid, and pagination.
- Do not constrain `ui/` changes to compatibility with older installed Scenery versions or apps that have not upgraded yet. When the current catalog needs compiler, generator, schema, or runtime changes, update Scenery in the same work and regenerate current consumers; do not add compatibility aliases or preserve an inferior UI contract for stale apps.

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
