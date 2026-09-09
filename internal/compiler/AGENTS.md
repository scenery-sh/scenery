# Scenery Compiler Instructions

## Purpose

`internal/compiler` owns source loading, expansion, defaults, semantic
validation, immutable compile results, and workspace/source state for the
current application graph.

## Local Contracts

- Depend only on foundational graph/source/spec packages, `internal/gotarget`,
  and narrow machine and model support needed to validate declared contracts.
  Do not import `internal/parse`. `gotarget.Environment` coverage lives in
  this package's tests so the leaf does not grow a test binary.
- Compiler results are immutable graph snapshots; evolution, generation, and
  deployment planning consume them without redefining the graph model.
- Local directory groups prefix HTTP binding/CRUD paths once after patches and
  before expansion. Source paths stay authored; effective paths and provenance
  own routing for every consumer. Registry cache paths never supply URL groups.
- `Result.SQLRequirements` owns reachable typed SQL bindings and selected
  framework auth/durable requirements, including declaration provenance and
  logical schema validation. Service/dependency/durable selection is shared with
  generation; supply endpoints and allocation records never enter compilation.
- Provider descriptor content binds its schema and capabilities/config/ABIs,
  not producer metadata or the ambient global spec revision. Explicit builtin
  lock planning is offline and retains the exact validated pre-edit bytes;
  ordinary compilation never updates dependency locks.
- Runtime-config-selected, framework-owned endpoint projections belong in
  `Result.FrameworkResources`. Inspection and client generation consume them,
  while `Manifest.Resources` and generated runtime composition remain authored.
- Workspace snapshots exclude VCS, Scenery state, and dependency caches and
  reject symlinks or non-regular entries.
- `workspace_revision` excludes exact descriptor-covered generated paths under
  managed roots, not unrelated authored files in those directories. User-owned
  workfiles follow ordinary explicitly declared revision membership.
- Every normal source read first asks `internal/workspacetx` to recover an
  abandoned transaction or reject a live owner. Staged validation admits only
  the current transaction owner.
- Validate CRUD list and table-page field capabilities plus split-page,
  content-page, workspace-page, and detail-page binding, route-parameter,
  field-section, action, related-table, and slot contracts before expansion.
  Declarative page kinds are macros over
  ordinary page and renderer resources, not a parallel UI graph or query
  model. Split-page domain rendering belongs to app-owned component slots.
- Never import evolution, generation, deployment planning, or
  runtime orchestration.

## Verification

```sh
go test ./internal/compiler ./internal/parse
```
