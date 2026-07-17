# Scenery Generation Instructions

## Purpose

`internal/generate` owns deterministic Go contracts, runtime composition,
TypeScript clients, OpenAPI documents, and their generated-file transactions.

## Local Contracts

- Consume immutable `internal/compiler.Result` and canonical `internal/graph`
  resources; never depend on legacy umbrella packages.
- Render Go artifacts into external build/editor workspaces by default; source
  materialization is an explicit published-module export.
- Own the fail-closed editor `go.work` protocol and descriptor-verified legacy
  pruning. Never replace or delete bytes whose ownership cannot be proven.
- TypeScript targets route to source or `.scenery` cache from their declared
  `materialization` mode.
- React-enabled TypeScript targets render generated content, table, and split pages and the
  binary-owned UI catalog from its editable source at `ui/` into the
  same artifact transaction. Generated pages use the consuming app's TanStack
  Query client for caching, deduplication, retry, and invalidation. Typecheck a
  sibling staging tree with the exact managed native checker before commit;
  never consult PATH or fall back when the checker or app dependencies are
  unavailable.
- Emit authored strings in JSX attributes as brace-wrapped JavaScript string expressions (`prop={"..."}`), never HTML-like quoted attributes; keep ordinary quoted literals only inside JavaScript object/array expressions. Generated URL-backed state that creates history entries must also subscribe to `popstate`.
- Generated React page adapters must preserve typed client failures as data and let transport or decoding exceptions reach TanStack Query for the host retry policy. Map the final query state, including exhausted exceptions, into the page contract's renderable error state.
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
