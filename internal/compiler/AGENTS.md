# Scenery Compiler Instructions

## Purpose

`internal/compiler` owns source loading, expansion, defaults, semantic
validation, immutable compile results, and workspace/source state for the
current application graph.

## Local Contracts

- Depend only on foundational graph/source/spec packages and narrow machine,
  parser, and model support needed to validate declared contracts and Go target
  contexts.
- Compiler results are immutable graph snapshots; evolution, generation, and
  deployment planning consume them without redefining the graph model.
- Workspace snapshots exclude VCS, Scenery state, and dependency caches and
  reject symlinks or non-regular entries.
- `workspace_revision` excludes derived generated roots and Scenery-owned
  editor workfiles; it hashes authored and explicitly declared inputs only.
- Every normal source read first asks `internal/workspacetx` to recover an
  abandoned transaction or reject a live owner. Staged validation admits only
  the current transaction owner.
- Validate CRUD list and table-page field capabilities plus split-page and
  content-page binding and slot contracts before expansion. Declarative page kinds are macros over
  ordinary page and renderer resources, not a parallel UI graph or query
  model. Split-page domain rendering belongs to app-owned component slots.
- Never import evolution, generation, deployment planning, or
  runtime orchestration.

## Verification

```sh
go test ./internal/compiler
```
