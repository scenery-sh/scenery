# Scenery UI Catalog

## Purpose

`ui/` is the single editable source for `@scenery/ui`. Scenery embeds this tree and materializes it under each React-enabled TypeScript client's `react/scenery-ui/` directory.

## Ownership

- Components are reusable across client applications. Never add domain-specific pages (mail, projects, orders) or import app-owned routes, state, assets, or generated API types; expose data and composition through typed props and slots.
- Apps never edit or copy the materialized catalog; they alias `@scenery/ui` and its token subpath per `docs/ui-agent-contract.md` § Vite consumers.
- Do not constrain `ui/` changes to older installed Scenery versions or un-upgraded apps: update compiler, generator, schema, or runtime in the same work, regenerate current consumers, and never add compatibility aliases for stale apps.

## Local Contracts

- Compose Astryx components and tokens with StyleX; a hand-rolled equivalent of an existing Astryx component is a defect, and raw interactive HTML under `ui/components/` fails the architecture check (`FilterPills` is the sole documented exception). Keep React, TanStack Query, Astryx, and StyleX as peer dependencies so each Vite app supplies one runtime copy and one QueryClient.
- Alias imported primitives by role (`BaseEmptyState`, `BaseTheme`), never by vendor prefix, and name conversion helpers for the catalog contract (`tableAlign`, `tableWidth`), not for the dependency implementing them.
- `embed.go` must include every catalog source directory. `internal/generate` adds generated ownership markers during materialization.
- Export the supported component surface from root `index.ts` only; `tokens.stylex.ts` is the sole subpath exception because StyleX variables must be imported from their defining module. `index.ts` mirrors the full Astryx surface through pinned explicit exports plus a generated `export *` block, and catalog-owned names shadow same-named Astryx exports by design; read `docs/ui-agent-contract.md` § Astryx Surface Mirror before an Astryx bump or export edit.
- Import the semantic var group as `t` from `@scenery/ui/tokens.stylex`; keep its vocabulary curated (app-facing roles, spacing, shape, elevation, typography, duration) and add a token only when a current consumer needs an Astryx variable with no semantic equivalent.
- Use the catalog `Theme` export around apps that consume `t`. It scopes the facade's mode-dependent aliases inside the active Astryx theme; do not replace it with a direct Astryx `Theme` re-export or apply `t` outside that provider.
- App code imports components from `@scenery/ui`, never `@astryxdesign/core/*`; direct imports are warranted only for subpaths the mirror cannot cover (token modules, `theme/*`), and recurring direct use of anything else is a mirror bug to fix.

## Work Guidance

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
