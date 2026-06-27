# Page Record Projection IR

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Static `//scenery:model` and `//scenery:page` generation currently has one useful but limiting assumption: a collection page row is the same TypeScript type as the generated database/sync row. That collapses storage entity, sync shape row, runtime row source, and page record into one contract.

This plan adds the first explicit page-record projection seam. A `page.Collection[T]` now has a generated projection record derived from its columns plus the ID field. Generated sync shapes still use the storage row, but generated layout/runtime code consumes the page record.

## Progress

- [x] 2026-06-26: Asked Oracle for the next model/page generation ExecPlan; it recommended a narrow Page Record Projection IR slice instead of generic joins, aggregates, or migration identity work.
- [x] 2026-06-26: Rechecked the requested roadmap context in `/Users/petrbrazdil/Repos/onlv/NEXT.md`; Oracle confirmed this remains the right narrow projection IR slice.
- [x] 2026-06-26: Added internal `ViewProjection` and `ProjectionField` records.
- [x] 2026-06-26: Added parser validation for collection projections; computed page columns now fail with a deterministic diagnostic.
- [x] 2026-06-26: Added `projection` to `scenery inspect views --json` and its schema.
- [x] 2026-06-26: Added generated `projections.ts` and switched generated collection/runtime/routes code to page record types.
- [x] 2026-06-26: Updated docs and focused tests.

## Surprises & Discoveries

- `docs/plans/0080-zerofs-production-readiness.md` already exists, so this plan uses `0081`.
- `scenery inspect views` reads `.scenery/gen/views.json` when the cache exists. The local model-dsl cache needed regeneration to show the new projection, but `.scenery/gen` remains ignored generated cache.
- The generated runtime already had a clean `rows` abstraction; the minimal change was to keep row sources as shape rows and add `materialize()` for page records.

## Decision Log

- Decision: Do not add public `model.Joined`, `model.Aggregate`, `model.ClientOnly`, or projection DSL yet.
  Rationale: The smallest missing seam is generated page record shape, not app-authored query semantics.
  Date/Author: 2026-06-26 / Codex.

- Decision: Derive projection fields from `ID` plus `page.Collection.Columns`.
  Rationale: It gives generated pages a stable record type while preserving existing page declarations.
  Date/Author: 2026-06-26 / Codex.

- Decision: Reject computed projection columns for now.
  Rationale: Current projection realization supports page records sourced from storage-row fields only; computed fields need a later materializer/source decision, and failing closed is clearer than silently skipping columns.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Completed 2026-06-26. This is a Stage 1-compatible structural IR slice with minimal frontend type plumbing: generated frontend packages now include `projections.ts`, collection/runtime types distinguish page records from source shape rows, and `scenery inspect views --json` exposes each collection projection. This is intentionally not full Stage 4 frontend generation or a full read-model engine; joins, aggregates, client-only fields, TypeScript manifests, non-synced read endpoints, and stable migration identity remain future work.

## Context and Orientation

The previous plan, `docs/plans/0077-static-model-view-ir.md`, completed the beta static model/page surface. Relevant files for this plan:

- `internal/model/model.go` stores parsed entities, views, and projection records.
- `internal/parse/parser.go` builds and validates page projections.
- `internal/inspect/inspect.go` renders `scenery.inspect.views.v1`.
- `internal/webgen/webgen.go` renders the hidden generated frontend package.
- `testdata/apps/model-dsl` is the acceptance fixture.

## Milestones

M1 adds the internal projection record and parser validation.

M2 exposes projection data through inspect JSON and schema docs.

M3 generates `projections.ts` and makes generated runtime/routes consume page records while preserving storage rows for sync.

## Plan of Work

Keep the slice internal and generated. Do not add new public DSL or app config. Use existing page columns as the source of truth and fail early on fields that cannot be materialized from the current row.

## Concrete Steps

1. Add `ViewProjection` and `ProjectionField`.
2. Build projections during view validation.
3. Add inspect/schema output.
4. Add generated `projections.ts`.
5. Update collection/runtime/routes TypeScript templates.
6. Update docs and tests.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/scenery`:

- `go test ./internal/parse ./internal/webgen ./internal/inspect ./internal/codegen ./internal/schemagen ./cmd/scenery`
- `go run ./cmd/scenery inspect views --app-root testdata/apps/model-dsl --json`
- `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json`
- `go run ./cmd/scenery inspect docs --json`

Successful inspect output includes `TaskListRecord` sourced from `TaskRow`. Generated web output includes `.scenery/gen/web/web/projections.ts`.

## Idempotence and Recovery

The generated web and inspect caches under `.scenery/gen` are disposable. Delete `testdata/apps/model-dsl/.scenery/gen` and rerun `go run ./cmd/scenery build --app-root testdata/apps/model-dsl -o .scenery/tmp/model-dsl-projection` or `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json` to regenerate them.

## Artifacts and Notes

Oracle was used as a second opinion through ChatGPT Pro in Chrome. It confirmed repository visibility for `scenery-sh/scenery` at `main fac84f55` and recommended this narrow projection seam.

The requested roadmap context lives in `/Users/petrbrazdil/Repos/onlv/NEXT.md`. That file calls for a three-layer entity/projection/view IR where the page-facing record is a projection, not the entity. `/Users/petrbrazdil/Repos/scenery/NEXT.md` is absent, but that was not the governing context.

## Interfaces and Dependencies

This changes beta generated/inspect contracts:

- `scenery.inspect.views.v1` view records include `projection` as part of the model/view IR, not only as a webgen artifact.
- Generated frontend packages include `projections.ts`.
- Generated collections use `TanStackDBCollectionDefinition<Record, ShapeRow>`.
- Generated runtime page rows are materialized projection records.
