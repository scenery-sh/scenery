# Generated Data Lifecycle Agent Instructions

## Purpose

`internal/generateddata` owns artifacts derived from `//scenery:model`: schema, seed, generated web planning, deterministic writes, and generated-schema drift calculation.

## Ownership

- `internal/schemagen` and `internal/webgen` produce individual artifacts; this package composes their lifecycle.
- `cmd/scenery` owns CLI discovery, flags, JSON/text rendering, and command errors.
- Do not import `cmd/scenery`; command adapters consume `Plan`, `Record`, and `SchemaDiff`.

## Local Contracts

- `Build` returns one deterministic plan for database and web outputs.
- `Write` changes files only when contents differ and writes generated artifacts below their planned paths.
- `BuildDiff` compares generated schemas with source schemas without writing either side.
- Existing-table models may generate web artifacts without generating database schema or seed artifacts.

## Work Guidance

Keep planning and artifact IO together so callers do not reimplement lifecycle ordering. Keep output formatting and CLI grammar out of this package. Tests should use isolated fixture copies and may run in parallel.

## Verification

```sh
go test ./internal/generateddata
go test -race ./internal/generateddata
go test ./cmd/scenery
```

## Child Agent Index

None.
