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
- Keep generated-page customization in exact typed slots declared by `defineTablePageSlots`. Table filters, toolbar, empty, and footer consume the shared `TablePageResultContext` (`rows`, optional `total`/`truncated`, filtered state, and current query); toolbar context is optional before the first result. `row_action` receives `row` and `onClose` and is mutually exclusive with `row_detail`.
- Preserve the CSS variables documented in `docs/local-contract.md`; they are the supported styling surface.
- Keep request/result types aligned with generated React adapters and all three table source modes: cursor-paginated CRUD, explicitly mapped numeric-page bindings, and complete-list bindings. Only complete-list tables may group.
- Static `content_page` slots receive no props; sourced content slots receive typed request state. Do not add a client or query to the static path.
- Source files remain plainly editable. The generator adds ownership markers only to materialized copies.
- Do not move the source back under `internal/generate`; `ui/` is the discoverable UI iteration surface.
- Export supported components from root `index.ts`; generated apps import `@scenery/ui`, never internal catalog subpaths.

## Vite consumers

Map `@scenery/ui` to the declared TypeScript client's `<output_root>/react/scenery-ui/index.ts` in both `tsconfig.json` and `vite.config.ts`. Keep the two paths identical. The app's existing StyleX transform compiles the materialized TSX, so no symlink, workspace package, npm install, or copied component tree is needed. The app must install compatible versions of the peer dependencies declared in `ui/package.json`.

Wrap the app shell once with `PageLayoutProvider` when shared page headers need app-owned navigation state. `Page`, `PageShell`, `SplitPage`, and `PageHeader` then consume that configuration without importing the app store or threading navigation props through every route.

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
