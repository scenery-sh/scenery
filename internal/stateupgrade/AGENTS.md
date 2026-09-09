# Retained-State Upgrade Transactions

## Purpose

Publish explicit same-schema metadata identity upgrades beneath one verified
private worktree root. Agent/storage owners supply domain-validated records.

## Local Contracts

- Ordinary decoding remains strictly current. This package never discovers app
  authority, converts data, rewrites credentials or operates on object payloads.
- Callers hold existing stopped-owner/operation and storage maintenance locks.
  Resolve/preview is read-only; apply requires the exact preview revision.
- Every path stays under the anchored private root, with owned non-symlink
  parents/files that others cannot write. Existing control history can retain
  read bits beneath that private root; domain owners enforce stricter metadata
  modes. Backups preserve exact bytes and are owner-only and fsynced.
- Persist the backup and pending marker before replacing metadata. Resume only
  matching before/after bytes; never recreate missing ownership or silently
  discard a pending transaction. Domain owners reject pending ordinary access.
- No environment switches or runtime fault injection. Package-private I/O hooks
  support deterministic interruption tests.

## Verification

Run `go test ./internal/stateupgrade` and the root runtime/CLI validation union.
Real CLI, process and service proof belongs in `scripts/verify` probes.
