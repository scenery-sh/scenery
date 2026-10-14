# Storage Production Readiness

Historical summary of the storage hardening completed on 2026-06-26. Current
storage behavior is defined in [the local contract](../local-contract.md#databases-snapshots-and-storage).

## Purpose / Big Picture

Harden the app-facing `scenery.sh/storage` API, reserved runtime routes, and
generated browser helpers before recommending them for production use.

## Progress

- [x] 2026-06-26: Enforced tenant scoping in the storage runtime and proxy path.
- [x] 2026-06-26: Persisted file-backed object metadata in sidecar JSON.
- [x] 2026-06-26: Added atomic `IfNoneMatch` writes and checked local filesystem
  synchronization errors.
- [x] 2026-06-26: Fixed range metadata beyond EOF.
- [x] 2026-06-26: Made headless `serve` and standalone `worker` fail closed without
  explicit `SCENERY_STORAGE_CONFIG`.
- [x] 2026-06-26: Completed restart, cleanup, import/export, and rollback proof.

## Surprises & Discoveries

- `tenant_scoped` existed in configuration before the store implementation
  enforced it. A store wrapper kept caller-visible keys stable while enforcing
  tenant namespaces.
- Range requests beyond EOF returned the correct bytes but reported the
  requested length. Returned-length metadata required a separate fix.
- HTTP transport canonicalizes metadata header names; direct Go store calls
  preserve the caller's map keys.

## Decision Log

- Keep the app-facing API as `scenery.sh/storage`.
- Enforce tenant scoping inside Scenery and keep tenant IDs out of visible keys.
- Persist file-backed metadata in Scenery-owned sidecar JSON.
- Require explicit storage configuration for headless runtimes.

## Outcomes & Retrospective

The app-facing API and runtime route contract were promoted for deployments
using an explicit operator-provided storage proxy. Storage gained tenant
isolation, durable metadata, atomic conditional writes, correct range lengths,
and fail-closed headless startup. Later changes also promoted the local
filesystem backend; current deployment requirements belong to the local
contract linked above.

## Validation

The completed work passed focused storage/headless/docs checks, `go test ./...`,
and the then-current self-harness with warning-class findings only.
