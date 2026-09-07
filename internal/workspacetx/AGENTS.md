# Workspace Transaction Instructions

## Purpose

`internal/workspacetx` owns crash-safe source/generated publication metadata,
process ownership checks, and recovery before any compiler source read.

## Local Contracts

- Stay below compiler and evolution; never import either package.
- Normal reads recover stale unreceipted work or reject a live owner.
- Only the current transaction owner may perform staged validation reads.
- Preserve strict current artifact identities and safely refuse legacy state.
- Generated publication uses the same lock/journal boundary; callers establish
  descriptor/digest ownership before returning updates. Serialize recovery with
  the kernel-owned recovery gate, verify backup bytes, and remove journals before
  committed markers/backups during cleanup.

## Verification

```sh
go test ./internal/workspacetx ./internal/compiler ./internal/evolution
```
