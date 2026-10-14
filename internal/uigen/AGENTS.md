# Optional UI Rendering

## Purpose

Render optional React pages, routes, hooks, access adapters and the UI catalog
from a validated canonical graph. The parent generator owns staging, checks,
descriptor ownership and one atomic artifact publication.

## Ownership

`types.go` defines the narrow input and in-memory file contract. Page/route
renderers own presentation only. `catalog.go` reads the live or embedded catalog.
Shared pure graph/naming queries live in `internal/clientprojection`.

## Local Contracts

- Consume canonical `graph.Resource` values; never parse or compile declarations
  again, import `internal/generate`, or publish files.
- A target without a `react` map returns no UI files or catalog roots.
- Keep ordinary clients, backend SQL/CRUD and server authorization in their
  owning packages. UI metadata never supplies server authorization.
- Preserve app-owned component slots and the existing generated query/error/
  route contracts. Render into memory; all selected outputs are checked and
  published together by the parent generator.
- Do not put renderer-specific types in the stdlib-only `internal/generate/api`.

## Verification

```sh
go test ./internal/generate ./internal/uigen
```

Complete the cumulative generator matrix. Named `ui` owns managed TypeScript
checks; `core-separation` owns backend runtime and transitive dependency proof.
