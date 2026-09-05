# Scenery Source Instructions

## Purpose

`internal/scn` owns `.scn` source discovery, safe filesystem access, parsing, source positions, lossless CSTs, and canonical formatting.

## Local Contracts

- Keep this package foundational: use `internal/spec` for read-only source-schema metadata and `internal/contract` for scalar values. Do not import the root `scenery.sh` facade, compiler, graph, generation, deployment, or runtime orchestration packages.
- Parsing preserves portable source IDs, Unicode-scalar columns, UTF-8 byte offsets, comments, trivia, and line endings.
- Files and local module sources must remain beneath the workspace and traverse no symlink.
- Contract filenames are singular: `app.scn`, `package.scn`, and `app.lock.scn`. Retired names fail with `SCN1021`; never add aliases.
- Formatting is canonical and idempotent.

## Verification

```sh
go test ./internal/scn
go test ./internal/compiler -run 'Test(CompileRejectsSymlinkedSourceFiles|Format)'
go test ./cmd/scenery -run 'TestContractQuietSuppressesHumanOutput'
```
