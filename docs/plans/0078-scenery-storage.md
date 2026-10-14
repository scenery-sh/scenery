# Scenery Storage

Historical summary of the storage capability introduced on 2026-06-22. Current
storage behavior is defined in [the local contract](../local-contract.md#databases-snapshots-and-storage).

## Purpose / Big Picture

Make storage a Scenery-owned capability. Apps declare durable file/object
storage and use `scenery.sh/storage` or generated storage routes without owning
filesystem roots, ad hoc endpoints, or substrate lifecycle.

## Progress

- [x] 2026-06-22: Added storage declarations to app configuration and its schema.
- [x] 2026-06-22: Added the stream-first `scenery.sh/storage` API, key validation,
  an HTTP object-serving helper, and an internal memory test backend.
- [x] 2026-06-22: Added storage inspection and CLI object operations.
- [x] 2026-06-22: Wired runtime configuration into app, worker, setup, and
  code-task processes.
- [x] 2026-06-22: Added auth-gated upload, list, HEAD/ranged download, delete,
  private-store denial, and private/internal runtime routes.
- [x] 2026-06-22: Added generated TypeScript `client.storage` helpers and a
  session-local storage proxy over a Unix socket.
- [x] 2026-06-22: Added `testdata/apps/storage-basic`, fixture probes, schemas,
  documentation, and environment registry entries.
- [x] 2026-06-22: Completed the ONLV migration to the Go capability and generated
  browser helpers.

## Surprises & Discoveries

- Deep repository paths can exceed macOS Unix socket limits. The storage proxy
  used a short temporary socket when necessary and removed it on shutdown.
- Private/internal routing belongs in the runtime route table; public storage
  requests still enforce auth and private-store policy.
- CLI writes must enforce the same configured object-size limits as runtime
  stores.

## Decision Log

- Expose storage through `scenery.sh/storage` so app code depends on a Scenery
  capability.
- Keep memory and temporary-directory test backends for service-free tests.
- Keep storage implementation dependencies behind a Scenery-owned runtime
  boundary.
- Keep generated public browser helpers separate from private/internal routes.

## Outcomes & Retrospective

Scenery gained app storage declarations, typed Go and browser APIs, CLI object
operations, and fixture proof. ONLV file helpers, job/map artifacts, and Pulse
Drive/viewer/contact/annotation paths migrated to these APIs. The original
namespace and operator surfaces have since changed; this summary records the
capability milestone, and the local contract owns current behavior.

## Validation

The completed work passed focused storage tests, `go test ./...`, and the
then-current self-harness with warning-class findings only. ONLV passed Scenery
checks, Go tests, frontend typecheck/lint, and an isolated storage put/get
round-trip.
