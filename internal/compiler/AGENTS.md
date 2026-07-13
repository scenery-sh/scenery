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
- Never import evolution, generation, deployment planning, or
  runtime orchestration.

## Verification

```sh
go test ./internal/compiler
```
