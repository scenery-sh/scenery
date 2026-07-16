# Scenery UI Catalog

## Purpose

`ui/` is the single editable source for `@scenery/ui`. Scenery embeds this tree and materializes it under each React-enabled TypeScript client's `react/scenery-ui/` directory.

## Local contracts

- Components are reusable across client applications. Never import app-owned routes, state, assets, or generated API types here; expose data and composition through typed props and slots.
- Use Astryx components and tokens with StyleX. Keep React, Astryx, and StyleX as peer dependencies so each Vite app supplies one runtime copy.
- Apps must not edit or copy the materialized catalog. They may alias `@scenery/ui` to `<output_root>/react/scenery-ui/index.ts` in both TypeScript and Vite.
- `embed.go` must include every catalog source directory. `internal/generate` adds generated ownership markers during materialization.
- Export the supported surface from `index.ts`; do not expose internal subpath imports.

## Verification

From the repository root, run:

```sh
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
go test ./internal/generate
```

For a consuming Vite app, regenerate its declared TypeScript client and run its typecheck, lint, tests, production build, and `scenery generate --check` lane.
