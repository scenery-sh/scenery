# Schema Catalog Canonicalization and Performance Baselines

This ExecPlan is a living document. Keep its progress, discoveries, decisions,
and outcomes current as implementation and validation proceed.

## Purpose / Big Picture

Scenery's authored source-schema catalog is static for the lifetime of one
binary, but schema lookup currently reconstructs resource roots, recursively
rehashes child schemas, and makes compiler validation deep-clone a complete
schema tree per source block. This plan makes the catalog canonical in memory,
caches its content revisions without changing a single emitted digest, and
keeps cloning only at public mutation boundaries.

The same audit found one deploy-resume test using the real process-status probe,
watch benchmarks whose evidence existed only in `/tmp`, and QueryTable timing
claims without a repeatable React Profiler harness. Those independent evidence
gaps are included because they are small and share the same performance-review
acceptance pass.

## Progress

- [x] 2026-07-23 10:00 CEST — Read the owning spec/compiler contracts and traced every audit claim to its call path.
- [x] 2026-07-23 10:10 CEST — Captured pre-change watch and source-schema allocation baselines.
- [x] 2026-07-23 10:07 CEST — Canonicalized resource schemas and source-schema revision/index lookup.
- [x] 2026-07-23 10:07 CEST — Reused one compiler-owned detached schema catalog in validation and evolution read paths.
- [x] 2026-07-23 10:02 CEST — Stubbed the deploy-resume status seam in the lifecycle test.
- [x] 2026-07-23 10:08 CEST — Recorded watch baselines and added a repeatable React Profiler measurement harness.
- [x] 2026-07-23 10:12 CEST — Ran focused profiles, fixture regeneration, full tests, and self-harness.
- [x] 2026-07-23 10:13 CEST — Recorded outcomes and moved this plan to the completed index.

## Surprises & Discoveries

- The `internal/spec` exported-clone contract forbids an exported live
  singleton pointer. The compiler can obtain the same hot-path benefit from one
  detached whole-catalog clone at package initialization, preserving both
  immutability and shared child identity.
- The focused single metadata test allocates only about 3 MB on this worktree;
  the audit's multi-gigabyte profile came from repeated catalog consumers.
  Post-change proof therefore includes direct zero-allocation canonical revision
  lookups plus package-level allocation profiles rather than relying on one
  misleading test size.
- The first post-change evolution profile still attributed 1.26 GB to schema
  cloning. `internal/scn/format_schema.go` was cloning and searching the full
  catalog for every nested block, while `internal/graph/revision.go` cloned the
  whole resource map per projected resource. Package-local indexes and
  single-kind accessors reduced the full evolution suite from the audit's
  4.57 GB to 1.84 GB total allocation.

## Decision Log

- 2026-07-23, Codex — Build resource roots once and precompute pointer-to-digest,
  digest-to-schema, internal-name-to-digest, and dynamic-field indexes. Rationale:
  catalog content is immutable and these are pure indexes over current content.
- 2026-07-23, Codex — Keep `spec.ResourceSourceSchema` cloning and add one
  whole-catalog cloning accessor for the compiler. Rationale: this honors
  `internal/spec/AGENTS.md`; evolution's writable rendering path remains
  detached while compiler validation stops cloning per block.
- 2026-07-23, Codex — Treat hashes and public schema shapes as compatibility
  invariants. Rationale: caching is an implementation optimization, not a
  specification revision.
- 2026-07-23, Codex — Extend canonical lookup to the formatter and graph
  projection consumers exposed by the post-change profile. Rationale: these
  were the same full-catalog cloning defect at foundational package boundaries,
  not a new abstraction.
- 2026-07-23, Codex — Keep React commit timings informational and typechecked,
  not a noisy CI budget. Rationale: the Profiler harness is a repeatable
  comparison tool; deterministic row/window tests remain the regression gate.

## Outcomes & Retrospective

Completed 2026-07-23. Resource source schemas are constructed once; their
pointer revisions, public-digest lookup, internal-name lookup, and dynamic
revision rules are indexed once. Canonical revision lookup is allocation-free,
while exported spec schemas remain detached and mutated detached schemas are
rehashable. Compiler validation and evolution rendering share one compiler-owned
read-only detached catalog. The formatter uses one package-local parent/child
index, and graph contract projection clones only one resource schema rather than
the catalog per resource.

The focused metadata profile fell from the audit's 2.70 GB to 36.5 MB. The full
evolution suite fell from 4.57 GB to 1.84 GB. Fixture generation reported
`changed: []` for both native and house, proving the optimization did not change
the current specification, contract revisions, or generated artifacts.

The deploy-resume unit test no longer touches process status. Watch benchmarks
retained the same allocation counts and comparable timings. The new
`apps/console` React Profiler command records 1k/5k/10k full versus windowed
mount/update commits without becoming a noisy CI threshold.

All focused tests, 33 Bun tests, console lint/typecheck/build, catalog
typecheck, `go test ./...`, and worktree-local self-harness passed. The first
self-harness run encountered a stale storage fixture process; the exact repro
passed with the fresh worktree binary and the full rerun passed every lane.

## Context and Orientation

`internal/spec/source_schemas.go` defines authored block schemas.
`internal/spec/schemas.go` renders their public form and computes canonical JSON
digests. `internal/compiler/schema_bridge.go` adapts those definitions into
validation. `cmd/scenery/deploy_test.go` owns the slow resume test.
`cmd/scenery/watch_bench_test.go` and
`internal/watchignore/watchignore_test.go` own the watch benchmarks.
`internal/generate/testdata/query_table_perf.test.tsx` owns deterministic table
windowing checks; the profiler harness complements rather than replaces them.

## Milestones

1. Make every resource root a singleton and build immutable lookup indexes.
2. Move compiler hot paths to one detached, shared catalog and add boundary tests.
3. Remove the real process probe from the deploy-resume unit test.
4. Preserve watch baselines and add repeatable React commit measurements.
5. Prove exact schema identity, profile allocation reduction, regenerate
   fixtures, and pass the repository validation matrix.

## Plan of Work

Construct resource schemas once after their authored child map exists. Walk
structural and resource roots post-order to calculate each source schema digest
once, then use direct maps for digest and internal-name lookup. For a detached
caller-owned schema, calculate a local post-order revision map so mutations
still affect its digest correctly.

Expose a whole resource catalog as one deep copy. Initialize the compiler bridge
from that copy and return stable pointers only inside the compiler package.
Leave the evolution-facing exported accessor detached.

Add focused tests for stable canonical pointer identity, detached exports,
shared child identity within a detached catalog, zero-allocation canonical
revision lookup, compiler pointer reuse, and unchanged public lookup behavior.

## Concrete Steps

All commands run from the repository root.

1. Edit `internal/spec/source_schemas.go`, `internal/spec/schemas.go`, and
   `internal/compiler/schema_bridge.go`.
2. Run `go test ./internal/spec ./internal/compiler ./internal/evolution`.
3. Stub `deployPublicEdgeStatusFunc` in
   `TestDeployResumeStartsMissingTargetsAndSkipsLiveSessions`, then time its
   focused cached test.
4. Run the watch benchmarks and React profiler harness; record machine/date,
   commands, and ballpark results under `docs/performance/`.
5. Regenerate both committed fixture clients and run the full matrix.

## Validation and Acceptance

- Every resource-root lookup returns the same canonical pointer internally.
- Exported spec accessors remain detached and mutable without changing the
  canonical catalog.
- Canonical `SourceSchemaRevision` lookup allocates zero bytes and public
  schema digests remain unchanged.
- Compiler validation reuses one schema pointer across blocks; evolution's
  mutation renderer still receives a detached tree.
- The deploy-resume focused test no longer calls a real launchd/process probe.
- Watch baselines and 1k/5k/10k React commit measurements are reproducible from
  checked-in commands.
- Both fixture generation commands report no unexpected semantic drift.
- `go test ./...`, catalog TypeScript validation, focused Bun tests, and
  `.scenery/harness/bin/scenery harness self --summary --write` pass.

## Idempotence and Recovery

All index construction is process-local and deterministic. Tests and benchmarks
write only to temporary paths. Fixture generation is transactional and can be
rerun. If a cached revision differs, stop and revert the index implementation
rather than updating expected specification hashes.

## Artifacts and Notes

Pre-change watch baseline on 2026-07-23, Apple M2 Ultra:

    BenchmarkScanWatchedFilesReusing-4   660179 ns/op   134289 B/op   1101 allocs/op
    BenchmarkSnapshotFingerprint-4        33592 ns/op     3872 B/op      7 allocs/op
    BenchmarkWatchIgnoreMatcher-24         10329 ns/op      448 B/op      7 allocs/op

Post-change evidence on the same machine:

    metadata schema profile       2.70 GB audit baseline -> 36.5 MB
    evolution full-suite profile  4.57 GB audit baseline -> 1.84 GB

`bun run profile:query-table` from `apps/console` produced:

    rows   full mount/update        windowed mount/update
    1k     10.56ms / 6.89ms         0.43ms / 0.39ms
    5k     22.34ms / 24.68ms        2.94ms / 0.17ms
    10k    39.79ms / 31.29ms        0.50ms / 0.90ms

React's test renderer currently emits its upstream deprecation notice; it is a
dev-only measurement dependency and does not enter the dashboard or catalog
runtime graph.

## Interfaces and Dependencies

No runtime dependency or public schema shape is added. The new spec catalog
accessor returns a deep copy and is consumed once by `internal/compiler`.
React measurement may use existing repository React tooling only; it must not
add a runtime catalog dependency.
