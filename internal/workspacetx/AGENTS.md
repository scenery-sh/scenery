# Workspace Transaction Instructions

## Purpose

`internal/workspacetx` owns crash-safe source transaction metadata, process
ownership checks, and recovery before any compiler source read.

## Local Contracts

- Stay below compiler and evolution; never import either package.
- Normal reads recover stale unreceipted work or reject a live owner.
- Only the current transaction owner may perform staged validation reads.
- Preserve strict current artifact identities and safely refuse legacy state.

## Verification

```sh
go test ./internal/workspacetx ./internal/compiler ./internal/evolution
```
