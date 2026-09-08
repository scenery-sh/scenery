# Harness Report Data

## Purpose

Own the current shared evidence/report value types and bounded, pure report
summaries consumed by verification and product artifact inspection.

## Local Contracts

- Do not execute commands, tests, probes, timers, or filesystem mutations here.
- Keep report JSON shapes and identities synchronized with checked schemas.
- Payload schema identities have one registry in `internal/machine`.
- Formatting must preserve failure status, selected scope, and omitted counts.

## Verification

Run `go test ./cmd/scenery ./internal/machine`. Consumer tests own coverage;
do not add another test binary just for aliases or moved value declarations.
