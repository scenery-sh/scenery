# Static Model/View IR

This ExecPlan is a living document and must be updated as work proceeds, as discoveries appear, and as implementation decisions change.

## Purpose / Big Picture

Scenery apps should be able to describe data models and pages in Go source using a strict vocabulary that the parser can understand without executing application code. The product is not a new language; the product is compile-time knowledge of the app that can later drive schema, backend, and web generation deterministically.

This plan introduces the static model/view intermediate representation (IR). A `//scenery:model` struct is the model source of truth. Optional `scenery.sh/model` builder calls add static metadata such as table name, enum values, filters, relationships, computed fields, and rename hints. A `//scenery:page` variable using `scenery.sh/page` declares a collection view over a model. The parser rejects non-static inputs so downstream generators consume data, not executable app metadata.

## Progress

- [x] 2026-06-12: Plan checked into `docs/plans/0077-static-model-view-ir.md` and linked from `docs/plans/active.md`.
- [x] 2026-06-12: M1 static IR candidate implemented: public `scenery.sh/model` and `scenery.sh/page` vocabulary, parser IR, diagnostics, `inspect models|views`, schemas, generated inspect cache files, and `testdata/apps/model-dsl` fixture coverage.
- [x] 2026-06-12: M2 schema diff mode candidate implemented: generated desired Atlas HCL under `.scenery/gen/db/<service>/schema.hcl`, `scenery generate data --dry-run --json`, `scenery db diff --generated --json`, `scenery check` `model-schema` drift diagnostics, schema contract docs, and model-dsl fixture schema coverage.
- [x] 2026-06-12: M3 backend foundation candidate implemented: explicit `model.Generate`, `model.Disable`, and `model.Override` action policy parsing; undeclared generated-route collision diagnostics; generated create/patch payload types; transient generated CRUD endpoints/stores; and `inspect endpoints|routes` generated markers. The first backend store is process-local and deterministic.
- [ ] M3 follow-up: typed seed declarations into idempotent upsert SQL consumed by `scenery db seed`, database-backed generated stores, and full convention-profile tenancy enforcement.
- [ ] M4 frontend generation: generated TypeScript row types, sync shapes, collection/materializer layer, route registration, default page, slot assertion glue, and hidden generated package layout.

## Surprises & Discoveries

- 2026-06-12: The existing `runtimeImportAliases` helper intentionally only tracks runtime packages such as `temporal` and `cron`; the model/page parser needs its own generic import alias map so aliases like `model` and `page` are visible.
- 2026-06-12: `go/types` can attach a constant value to an expression that references a mutable string variable initialized from a literal. The model/page static evaluator therefore checks identifier objects and rejects non-`types.Const` identifiers before accepting a string value.
- 2026-06-12: `scenery check` can reuse a compiled build before parsing. M2 schema drift is not a compiled-binary property, so the check path now parses and validates model-schema drift before returning cached success. `SERVICE/db/schema.hcl` is also a watched input so cache fingerprints change when app-owned schema changes.
- 2026-06-12: Generated model endpoints cannot reuse the handwritten endpoint wrapper path directly because that path assumes an AST `FuncDecl` to rename and wrap. M3 foundation therefore registers generated runtime endpoints from generated source while carrying separate generated endpoint IR for inspect output.

## Decision Log

- 2026-06-12, maintainer worker: Land M1 as an inert public/inspect surface before schema/backend/web generators. This keeps existing apps unaffected and creates a stable acceptance corpus for later generator milestones.
- 2026-06-12, maintainer worker: Keep `scenery.sh/model` and `scenery.sh/page` as tiny compile-time vocabulary packages. They intentionally have no runtime metadata registry and do not import parser, build, or CLI internals.
- 2026-06-12, maintainer worker: Resolve page slots in M1 by checking for a matching `.ts` or `.tsx` component filename under the app root, skipping `.git`, `.scenery`, `node_modules`, and `vendor`. M4 can replace this with frontend-aware alias/type assertion generation.
- 2026-06-12, maintainer worker: Implement M2 as a read-only schema contract, not a migration engine. Stored and relationship fields render as Atlas columns, computed fields are omitted, `ID` is the primary key when present, and static enum metadata renders as a Postgres enum. `scenery generate data --dry-run` writes only disposable generated desired HCL; it does not mutate databases.
- 2026-06-12, maintainer worker: Land the first M3 backend slice as transient generated CRUD endpoints with a process-local generated store. This proves parser/action policy/codegen/inspect/runtime seams without adding a premature database abstraction. Database-backed generated stores, typed seed SQL, and tenancy conventions remain tracked M3 follow-up work.

## Outcomes & Retrospective

Not yet completed. M1, M2, and the M3 backend foundation are implemented as independently reviewable foundations; M3 database/seed/tenancy completion and M4 frontend generation remain active follow-on milestones tracked by this plan.

## Context and Orientation

The central flow is `.scenery.json` plus Go source through `internal/parse`, then `internal/model`, then `internal/inspect`, `internal/codegen`, and `internal/build`. This work touches the parser/model/inspect side first. It does not change runtime registration, endpoint dispatch, database mutation, or frontend builds.

Relevant files:

- `model/model.go` and `page/page.go` define the public compile-time vocabulary for app authors.
- `internal/model/model.go` stores parsed `Entity` and `View` IR nodes.
- `internal/parse/parser.go` reads `//scenery:model`, static `model.Entity[T](...)` calls, and `//scenery:page` `page.Collection[T]{...}` literals.
- `internal/inspect/inspect.go` renders `scenery.inspect.models.v1` and `scenery.inspect.views.v1` JSON.
- `cmd/scenery/inspect.go` exposes `scenery inspect models --json` and `scenery inspect views --json`.
- `internal/build/build.go` writes `.scenery/gen/models.json` and `.scenery/gen/views.json` beside the existing generated inspect artifacts.
- `internal/schemagen/schema.go` renders deterministic desired Atlas HCL from the static model IR.
- `cmd/scenery/generated_schema.go` wires data-schema generation, generated schema diff JSON, and check diagnostics.
- `internal/codegen/generator.go` renders generated model CRUD payload types, endpoint registrations, and process-local stores into the transient build workspace.
- `testdata/apps/model-dsl` is the acceptance fixture for M1.

## Milestones

M1 is static IR and diagnostics only. Acceptance is deterministic parsing and inspection for stored, relationship, computed, enum, filterable, and collection-page nodes, plus source-pointed diagnostics for non-static builder input, unknown field names, and missing slot components.

M2 adds read-only schema diff mode. It should emit desired Atlas HCL under `.scenery/gen/db/<service>/schema.hcl`, expose `scenery generate data --dry-run --json`, add `scenery db diff --generated`, and surface drift from `scenery check` without writing databases.

M3 adds backend generation. The landed foundation generates CRUD stores/endpoints into the transient build workspace with explicit action policy and collision checks. Follow-up M3 work should replace or extend the process-local generated store with database-backed generated stores, generate seed SQL into the existing `db seed` machinery, and enforce tenancy conventions.

M4 adds frontend generation. It should generate TypeScript row types, sync shapes, collection/materializer code, route registration, and default collection pages into a hidden generated package while verifying slot contracts.

## Plan of Work

Keep each milestone independently shippable and inert for apps that do not use model/page directives. The parser remains the authority for source validation; later stages must not rediscover invalid shapes. Generated outputs are pure functions of the IR and app config. JSON contracts are versioned under `docs/schemas/` and validated by harness commands.

M1 should not introduce new dependencies. M2 may use existing SQL/HCL rendering patterns or stdlib string rendering. M3 should reuse existing transient build workspace generation and runtime endpoint registration. M4 should follow `internal/clientgen` rendering style and the app frontend conventions that exist when that milestone starts.

## Concrete Steps

1. Add `scenery.sh/model` and `scenery.sh/page` public packages with compile-time-only DSL types and functions.
2. Extend `internal/model.App` with `Entities` and `Views`, and define entity/field/view IR records.
3. Extend `internal/parse` to discover `//scenery:model`, static `model.Entity[T](...)` metadata, and `//scenery:page` `page.Collection[T]{...}` literals.
4. Add diagnostics for non-static strings, unknown field names, unknown model references, and missing page slot components.
5. Add `scenery inspect models|views --json`, schemas, help text, harness knowledge, generated inspect artifacts, and fixture/golden-style tests.
6. Update docs and this plan before publishing the PR.
7. Add M2 desired schema rendering from parsed entity IR and write generated HCL under `.scenery/gen/db/<service>/schema.hcl`.
8. Add `scenery generate data --dry-run --json`, `scenery db diff --generated --json`, `scenery check` drift diagnostics, JSON schema docs, and model-dsl fixture coverage.
9. Add explicit model CRUD action policy parsing with `model.Generate`, `model.Disable`, and `model.Override`.
10. Add generated endpoint IR, inspect `generated` markers, collision diagnostics, generated create/patch payload types, and transient generated CRUD endpoint registrations.

## Validation and Acceptance

For M1, run:

- `go test ./internal/parse`
- `go test ./internal/build -run TestPrepareWritesInspectArtifacts`
- `go test ./cmd/scenery -run 'TestRunSceneryInspectOutputsModelDSLJSON'`
- `go test ./cmd/scenery`
- `go test ./...`
- `golangci-lint run ./...`
- `scenery check --app-root testdata/apps/model-dsl --json`
- `scenery inspect models --app-root testdata/apps/model-dsl --json`
- `scenery inspect views --app-root testdata/apps/model-dsl --json`
- `scenery harness self --summary --write`
- `scenery harness self --release --summary --write`

Run `scripts/release-gate.sh` before merge if release-gate-relevant behavior changes beyond the M1 inspect/parser surface, or before any release execution.

For M2, run:

- `go test ./cmd/scenery -run 'TestGenerateData|TestDBDiffGenerated|TestRunSceneryCheckReportsGeneratedSchemaDrift'`
- `go test ./cmd/scenery`
- `go test ./...`
- `golangci-lint run ./...`
- `go run ./cmd/scenery generate data --dry-run --json --app-root testdata/apps/model-dsl`
- `go run ./cmd/scenery db diff --generated --json --app-root testdata/apps/model-dsl`
- `go run ./cmd/scenery check --app-root testdata/apps/model-dsl --json`
- `scenery harness self --summary --write`
- `scenery harness self --release --summary --write`

Run `scripts/release-gate.sh` before merge if release-gate-relevant behavior changes beyond generated schema diff/check behavior, or before any release execution.

For the M3 backend foundation, run:

- `go test ./internal/parse -run 'TestModelDSL'`
- `go test ./internal/codegen -run 'TestGenerateModelCRUDBackend'`
- `go test ./cmd/scenery -run 'TestRunSceneryInspectOutputsModelDSLJSON'`
- `go run ./cmd/scenery build --app-root testdata/apps/model-dsl -o /tmp/scenery-model-dsl-m3`
- Run the built fixture and exercise generated create/list/get/update endpoints plus disabled delete behavior.
- `go test ./cmd/scenery`
- `go test ./...`
- `golangci-lint run ./...`
- `go run ./cmd/scenery check --app-root testdata/apps/model-dsl --json`
- `go run ./cmd/scenery inspect endpoints --app-root testdata/apps/model-dsl --json`
- `scenery harness self --summary --write`
- `scenery harness self --release --summary --write`

## Idempotence and Recovery

M1 has no app runtime side effects. It parses source and writes disposable generated inspect cache files under `.scenery/gen/` when the build path runs. Delete `.scenery/gen/models.json` or `.scenery/gen/views.json` and rerun `scenery build`, `scenery serve`, or `scenery inspect` to regenerate or recompute them.

M2 writes disposable generated desired schema files under `.scenery/gen/db/<service>/schema.hcl`. Delete those files and rerun `scenery generate data --dry-run --json` to regenerate them. M2 diff mode must remain read-only and must not apply databases. M3 seed application must reuse existing sha256 seed tracking and changed-after-apply protection.

The M3 backend foundation writes only transient build-workspace Go files and process-local runtime state. Restarting `scenery serve` clears generated CRUD rows. Future database-backed generated stores and seed SQL must keep generated artifacts disposable and reuse existing seed idempotence ledgers.

## Artifacts and Notes

- M1 fixture: `testdata/apps/model-dsl`.
- M1 schemas: `docs/schemas/scenery.inspect.models.v1.schema.json` and `docs/schemas/scenery.inspect.views.v1.schema.json`.
- M1 generated inspect artifacts: `.scenery/gen/models.json` and `.scenery/gen/views.json`.
- M2 fixture schema: `testdata/apps/model-dsl/tasks/db/schema.hcl`.
- M2 schema: `docs/schemas/scenery.db.generated_diff.v1.schema.json`.
- M2 generated desired schema artifact: `.scenery/gen/db/<service>/schema.hcl`.
- M3 generated endpoint marker: `generated: true` in `scenery inspect endpoints|routes --json` for endpoints produced from model CRUD policy.
- M3 generated build artifacts: transient `scenery.gen.go` content in the build workspace package that owns the model entity.

## Interfaces and Dependencies

Consumes existing packages: `internal/parse`, `internal/model`, `internal/inspect`, `internal/build`, `cmd/scenery`, and the docs/harness schema validation surfaces.

Public app imports added by M1: `scenery.sh/model` and `scenery.sh/page`.

M2 depends on existing database config and Atlas HCL conventions. M3 depends on `internal/codegen`, `internal/build`, runtime endpoint registration, standard-auth tenancy conventions, and `cmd/scenery/db_seed.go`. M4 depends on `internal/clientgen` rendering patterns and the app frontend layout/slot contract available at the time of implementation.
