# Scenery Generation Instructions

## Purpose

`internal/generate` owns deterministic Go contracts, runtime composition,
TypeScript clients, OpenAPI documents, and their generated-file transactions.

## Local Contracts

- Consume immutable `internal/compiler.Result` and canonical `internal/graph`
  resources; never depend on legacy umbrella packages.
- Render Go artifacts into external build/editor workspaces by default; source
  materialization is an explicit published-module export.
- For declared Go libraries, render the typed `scenerylib_<name>` facade,
  source/shared backends, c-shared export shim, and detached descriptor into
  that external workspace. The app imports the facade; it never commits or
  edits those projections.
- Own the fail-closed editor `go.work` protocol and descriptor-verified legacy
  pruning. Never replace or delete bytes whose ownership cannot be proven.
- TypeScript targets route to source or `.scenery` cache from their declared
  `materialization` mode.
- React-enabled TypeScript targets render generated content, table, and split pages and the
  binary-owned UI catalog from its editable source at `ui/` into the
  same artifact transaction. Generated pages use the consuming app's TanStack
  Query client for caching, deduplication, retry, and invalidation. Typecheck a
  sibling staging tree with the exact managed native checker before commit;
  redirect the consuming app's `@scenery/ui` aliases to that sibling tree while
  preserving its other resolved TypeScript path aliases, so a catalog API
  cutover verifies atomically against the replacement rather than the previous
  materialization;
  never consult PATH or fall back when the checker or app dependencies are
  unavailable.
- Emit authored strings in JSX attributes as brace-wrapped JavaScript string expressions (`prop={"..."}`), never HTML-like quoted attributes; keep ordinary quoted literals only inside JavaScript object/array expressions. Generated URL-backed state that creates history entries must also subscribe to `popstate`.
- Generated React page adapters must preserve typed client failures as data and let transport or decoding exceptions reach TanStack Query for the host retry policy. Map the final query state, including exhausted exceptions, into the page contract's renderable error state.
- Generated `detail_page` adapters own typed dynamic route parameters and one shared content component used by routed and controlled-dialog wrappers. They compose declared sections, generated form-dialog actions, related table pages with exact input injection, and app-owned typed action slots; mutations invalidate the detail and every related query without moving domain workflows into generation.
- Generated descriptors carry current machine identity and exact revisions.
- Keep output beneath compiler-declared managed roots and reject symlinks.
- Generation checks return diagnostics plus an explicit implementation state:
  native verification is `valid` or `invalid`; compile-only/non-native checks
  remain `not_requested`.

## Verification

```sh
go test ./internal/generate
go test ./cmd/scenery -run 'TestGenerate'
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
```
