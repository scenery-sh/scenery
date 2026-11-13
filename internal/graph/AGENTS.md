# Scenery Graph Instructions

## Purpose

`internal/graph` owns the canonical resource graph model, graph views, provenance values, canonical graph projections, and general content revisions.

## Local Contracts

- Depend only on foundational packages such as `internal/scn`, `internal/spec`, and the shared machine identity; never import compiler, generation, evolution, deployment planning, or runtime orchestration.
- Keep resource addresses, graph ordering, provenance paths, and revision hashes deterministic.
- Graph views are immutable compiler outputs: source, effective, and expanded.
- Cross-process graph and context results carry strict current artifact identities; opaque continuation tokens have one unversioned hash domain and no decoder selection.
- Raw manifests and compile-envelope manifests pass through the same exact
  decoder and validator, including producer/catalog identity, resource schema
  validation, canonical ordering, and recomputed contract revision.
- Keep HTTP, generator, evolution, and deployment-specific projections in their owning packages.

## Verification

```sh
go test ./internal/graph
go test ./internal/compiler -run 'Test(Graph|Context|Provenance|ContractRevision|WorkspaceRevision)'
```
