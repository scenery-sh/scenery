# Scenery Contract Agent Instructions

## Purpose

`internal/contractagent` owns the JSON-RPC capability surface for reading the
compiled graph, querying schemas, and dispatching explicit evolution plans.

## Local Contracts

- Consume immutable compiler results and graph queries.
- Route source mutations through `internal/evolution`; never edit source here.
- Schema responses come from the compiler/spec catalogs, not duplicated shapes.
- Never import generation, deployment planning, or commands.

## Verification

```sh
go test ./internal/contractagent
```
