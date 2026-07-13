# Scenery Evolution Instructions

## Purpose

`internal/evolution` owns semantic comparison, source mutation planning,
migration consequences, approvals, and revision-bound receipts.

## Local Contracts

- Consume immutable compiler output through `internal/graph`; do not define a
  second graph model.
- Keep plans, approvals, and rename receipts bound to exact content revisions.
- Reject stale or tampered artifacts; never translate an older plan shape.
- Source mutations must remain confined to the app workspace and preserve
  transactional recovery.

## Verification

```sh
go test ./internal/evolution
go test ./cmd/scenery
```
