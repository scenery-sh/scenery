# Scenery Contract Agent Instructions

## Purpose

`internal/contractagent` owns the schema catalog lookup behind `scenery schema`:
resource, value, diagnostic and mutation schemas.

## Local Contracts

- Schema responses come from the compiler/spec catalogs, not duplicated shapes.
- Never import generation or commands.

## Verification

```sh
go test ./internal/contractagent
```
