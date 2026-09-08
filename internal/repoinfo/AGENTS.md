# Read-Only Repository Information

## Purpose

Own repository-root discovery, strict knowledge-index data, document freshness,
the read-only agent-scope/child-index scan, and the single pure
changed-path/validation-command classification table.

## Local Contracts

- Discovery reads only; it must not execute tests, provision resources, or write.
- Classification consumes supplied paths/package metadata and produces values.
- Keep full product documentation rendering and CLI parsing in `cmd/scenery`.
- Completed numbered ExecPlans are immutable history, not freshness-review work.
- Keep existing schema identities exact; no old-index decoder or fallback.

## Verification

Run `go test ./cmd/scenery`; documentation inspection and classification consumers
retain the exact behavior tests without a new leaf test binary.
