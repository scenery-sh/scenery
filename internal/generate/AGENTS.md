# Scenery Generation Instructions

## Purpose

`internal/generate` owns deterministic Go contracts, runtime composition,
TypeScript clients, OpenAPI documents, and their generated-file transactions.
`internal/generate/api` is the stdlib-only leaf for library build specs,
editor-workspace inspection, runtime-integration plans, and assistant-asset
descriptor types so callers that do not render artifacts do not link the
generator. Production `internal/build` consumes those types through injected
hooks; CLI wires the live generate functions.

## Local Contracts

- Consume immutable `internal/compiler.Result` and canonical `internal/graph`
  resources; never depend on legacy umbrella packages.
- Keep `internal/generate/api` free of compiler, parse, tscheck, and generate
  imports. Types and editor-workspace inspection live there; rendering,
  verification, and inspection tests stay in `internal/generate` so the leaf
  does not grow a test binary.
- Live predicted-artifact and native implementation-check coverage lives here,
  not in `internal/evolution` tests.
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
- Generated `typescript_client` HTTP methods are thin typed wrappers over a
  per-binding descriptor table and `Runtime.invoke` / `Runtime.matchResponse`.
  Do not inline the request/response machine per method or emit TanStack hooks
  from this target. Omit empty query and cookie mapping arrays. Emit shared
  runtime query, binding header/cookie, multipart, retry, and record-validation
  sections only when the covered descriptors, reachable records, or target
  configuration require them. Intern
  identical response cases and repeated consecutive shared runs into
  `sharedResponses` / `sharedResponseSets`. Share repeated exact failure unions
  with private aliases; retain each name as a separate discriminated member.
  Mark fresh-literal metadata freezing as pure so unused metadata is removable
  through the public barrel while imported metadata remains deeply frozen.
- React-enabled TypeScript targets render generated content, table, and split pages and the
  binary-owned UI catalog from its editable source at `ui/` into the
  same artifact transaction. Generated pages use the consuming app's TanStack
  Query client for caching, deduplication, retry, and invalidation. Typecheck a
  sibling staging tree with the exact managed TypeScript 7 `tsc` before commit;
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
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
bun test internal/generate/testdata/typescript_client_conformance.test.ts
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json
```
