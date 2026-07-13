# Scenery Deployment Planning Instructions

## Purpose

`internal/deployplan` owns immutable deployment plans, provider coordination,
approval binding, and crash-safe plan application.

## Local Contracts

- Consume immutable compiler deployment projections; never parse or compile
  application source independently.
- Provider plans, approvals, journals, locks, and receipts bind exact workspace,
  contract, implementation, deployment, and provider-plan revisions.
- Never import generation, command, or runtime orchestration.

## Verification

```sh
go test ./internal/deployplan
```
