# Scenery UI Catalog

## Purpose

`ui/` is the single editable source for `@scenery/ui`. Scenery embeds this tree and materializes it under each React-enabled TypeScript client's `react/scenery-ui/` directory.

## Local contracts

- Components are reusable across client applications. Never add domain-specific pages (mail, projects, orders, or similar), or import app-owned routes, state, assets, or generated API types here; expose data and composition through typed props and slots.
- Use Astryx components and tokens with StyleX. Keep React, TanStack Query, Astryx, and StyleX as peer dependencies so each Vite app supplies one runtime copy and one QueryClient.
- Apps must not edit or copy the materialized catalog. Alias `@scenery/ui` to `<output_root>/react/scenery-ui/index.ts` in both TypeScript and Vite. Apps using semantic tokens must also alias `@scenery/ui/tokens.stylex` to `<output_root>/react/scenery-ui/tokens.stylex.ts` in TypeScript, Vite, and the StyleX compiler plugin's `aliases` option.
- `embed.go` must include every catalog source directory. `internal/generate` adds generated ownership markers during materialization.
- Export the supported component surface from `index.ts`; do not expose internal subpath imports. `tokens.stylex.ts` is the only subpath exception because StyleX variables must be imported from their defining module.
- The blessed Astryx primitives are `Text`, `Button`, `IconButton`, `Badge`, `TextInput`, `Selector`, `VStack`, `HStack`, `Heading`, and `Icon`, with their component types. Keep them as plain re-exports until a concrete Scenery convention requires a wrapper.
- Import the semantic var group as `t` from `@scenery/ui/tokens.stylex`. Its curated vocabulary is `accent`, `accentMuted`, `body`, `surface`, `popover`, `muted`, `overlay`, `overlayHover`, `neutral`, `border`, `borderEmphasized`, `textPrimary`, `textSecondary`, `onDark`, `success`, `successMuted`, `warning`, `warningMuted`, `error`, `errorMuted`, `borderWidth`, `radius`, `radiusElement`, `radiusFull`, `shadowLow`, `shadowMedium`, `space0_5`, `space1`, `space1_5`, `space2`, `space3`, `space4`, `space5`, `space6`, `space8`, `space10`, `space12`, `fontBody`, `fontCode`, `supportingSize`, `supportingLeading`, `pageGutter`, and `panelWidth`.
- Direct Astryx imports remain the escape hatch for unblessed components or tokens. Recurring direct use is evidence to curate a new root export or semantic token, not a reason to mirror Astryx wholesale.
- Use the shared `Problem` / `RequestState` vocabulary and `queryStateProps` adapter for catalog request lifecycles instead of introducing component-local loading/error unions.
- Keep `ClientAppShell` router-agnostic. Generated adapters own route
  selection, active-state calculation, and the router outlet; the catalog
  shell accepts resolved navigation plus fixed visual slots.
- Custom page headers use `PageNavigationToggle` to consume the
  `PageLayoutProvider` navigation state; do not create a second sidebar
  collapse store in the consuming app.
- Keep `QueryTable` chrome-less. Generated `table_page` adapters own the surrounding `Page` shell and map `toolbar` to page actions; the catalog component owns only query controls, TanStack Query request state, grid, and pagination.
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
