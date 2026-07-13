# Scenery Generation Instructions

## Purpose

`internal/generate` owns deterministic Go contracts, runtime composition,
TypeScript clients, OpenAPI documents, and their generated-file transactions.

## Local Contracts

- Consume immutable `internal/compiler.Result` and canonical `internal/graph`
  resources; never depend on legacy umbrella packages.
- Render every selected artifact before atomically committing any output.
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
```
