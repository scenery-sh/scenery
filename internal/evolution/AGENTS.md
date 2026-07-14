# Scenery Evolution Instructions

## Purpose

`internal/evolution` owns semantic comparison, source mutation planning,
migration consequences, approvals, and revision-bound receipts.

## Local Contracts

- Consume immutable compiler output through `internal/graph`; do not define a
  second graph model.
- Keep plans, approvals, and rename receipts bound to exact content revisions.
- Reject pending artifacts from another spec with `revision_scheme_changed`;
  never translate an older plan shape. Preserve applied receipts byte-for-byte
  and accept revision rebind evidence only for an unchanged contract projection.
- Source mutations must remain confined to the app workspace. Transaction
  metadata and recovery are owned by `internal/workspacetx`; evolution writes
  that shared exact shape and never creates a parallel recovery reader.

## Verification

```sh
go test ./internal/evolution
go test ./cmd/scenery
```
