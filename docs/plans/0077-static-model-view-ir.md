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
- [x] 2026-06-12: M3 data-backed slice candidate implemented: `model.Seed` typed static rows, deterministic idempotent upsert SQL under `.scenery/gen/db/<service>/seed.sql`, generated seed artifacts consumed by `scenery db seed`, database-backed generated CRUD stores using `DatabaseURL`, and diagnostic blocking for tenant-field generated CRUD until full tenancy policy lands.
- [x] 2026-06-12: M3 tenancy follow-up candidate implemented: convention `TenantID`/`tenant_id` generated CRUD endpoints are auth-only, derive the active tenant from standard auth, scope list/get/update/delete SQL by tenant, inject tenant on create, and omit tenant fields from create/patch payloads.
- [x] 2026-06-12: M4 frontend foundation candidate implemented: `scenery generate data` now writes beta hidden TypeScript packages under `.scenery/gen/web/<frontend>/` with row/create/patch types, Electric shape definitions, TanStack DB collection descriptors/materializers, route/default page exports, slot type assertions, and a model-dsl fixture alias/typecheck/render proof.
- [x] 2026-06-12: M4 runtime-adapter follow-up candidate implemented: generated web packages now include `runtime.ts` with Electric shape URL/runtime collection factories, richer route records with stable IDs/entity/collection metadata, `createGeneratedRoutes(runtime)`, and `registerGeneratedRoutes(...)`; the model-dsl fixture proves the layout-kit contract consumes runtime-backed routes.
- [x] 2026-06-13: App-owned schema follow-up candidate implemented for issue #137: generated model Atlas HCL, seed SQL, CRUD SQL, and Electric shape metadata consistently use the service-owned schema-qualified table instead of hardcoded `public`.
- [x] 2026-06-13: Route-safety follow-up candidate implemented for issue #138: generated CRUD route bases now default to `/<service>/<table>` independently from the physical table name, and generated routes fail on handwritten/generated or generated/generated collisions.
- [x] 2026-06-13: Atlas label-safety follow-up candidate implemented for issue #141: generated model HCL uses schema-qualified resource labels such as `table "<schema>" "<table>"` and `enum "<schema>" "<enum>"` so app-owned generated schemas do not shadow handwritten multi-schema table references.
- [x] 2026-06-13: Access-default follow-up candidate implemented for issue #143: generated model CRUD endpoints now default to `auth` for every action, including non-tenant entities, until an explicit public opt-in surface exists.
- [x] 2026-06-13: UUID tenant-field follow-up candidate implemented for issue #144: generated CRUD tenant fields support `string`, named string types, and `github.com/google/uuid.UUID`; unsupported tenant types fail parse/check with a clear diagnostic, and UUID tenant values use ordinary parse/error-return paths instead of direct conversion.
- [ ] M4 production app integration follow-up: connect the generated runtime-adapter contract to a real production layout-kit package and app router/Electric/TanStack client in a reference app, then fold any discovered contract gaps back into Scenery.

## Surprises & Discoveries

- 2026-06-12: The existing `runtimeImportAliases` helper intentionally only tracks runtime packages such as `temporal` and `cron`; the model/page parser needs its own generic import alias map so aliases like `model` and `page` are visible.
- 2026-06-12: `go/types` can attach a constant value to an expression that references a mutable string variable initialized from a literal. The model/page static evaluator therefore checks identifier objects and rejects non-`types.Const` identifiers before accepting a string value.
- 2026-06-12: `scenery check` can reuse a compiled build before parsing. M2 schema drift is not a compiled-binary property, so the check path now parses and validates model-schema drift before returning cached success. `SERVICE/db/schema.hcl` is also a watched input so cache fingerprints change when app-owned schema changes.
- 2026-06-12: Generated model endpoints cannot reuse the handwritten endpoint wrapper path directly because that path assumes an AST `FuncDecl` to rename and wrap. M3 foundation therefore registers generated runtime endpoints from generated source while carrying separate generated endpoint IR for inspect output.
- 2026-06-12: Headless `scenery serve` does not expose dev-only `__scenery/config`; live M3 proof should probe generated app routes such as `/tasks/tasks` for readiness instead.
- 2026-06-12: The generated M4 frontend package can expose production-facing adapter seams without importing Electric or TanStack packages directly. Emitting small factory functions over app-provided `electric.baseURL` and row sources keeps Scenery dependency-free while giving production apps a stable place to bind their router, Electric client, and TanStack DB collection instance.
- 2026-06-13: ONLV's safe schema apply correctly rejects generated mutations against protected `public`; generated model data therefore needs an app-owned schema convention before reference-app integration can proceed.
- 2026-06-13: The ONLV pilot also showed that deriving generated HTTP routes from `model.Table(...)` leaks database compromises into public API shape. Generated CRUD needs an app-safe route convention and collision diagnostics distinct from physical table naming.
- 2026-06-13: Atlas HCL resource labels are separate from physical schema/table names. A generated one-label `table "tasks"` in schema `tasksnew` can shadow handwritten references like `table.tasks.projects` in another schema, so generated app-owned schemas need collision-safe resource labels even when the physical table name remains `tasks`.

## Decision Log

- 2026-06-12, maintainer worker: Land M1 as an inert public/inspect surface before schema/backend/web generators. This keeps existing apps unaffected and creates a stable acceptance corpus for later generator milestones.
- 2026-06-12, maintainer worker: Keep `scenery.sh/model` and `scenery.sh/page` as tiny compile-time vocabulary packages. They intentionally have no runtime metadata registry and do not import parser, build, or CLI internals.
- 2026-06-12, maintainer worker: Resolve page slots in M1 by checking for a matching `.ts` or `.tsx` component filename under the app root, skipping `.git`, `.scenery`, `node_modules`, and `vendor`. M4 can replace this with frontend-aware alias/type assertion generation.
- 2026-06-12, maintainer worker: Implement M2 as a read-only schema contract, not a migration engine. Stored and relationship fields render as Atlas columns, computed fields are omitted, `ID` is the primary key when present, and static enum metadata renders as a Postgres enum. `scenery generate data --dry-run` writes only disposable generated desired HCL; it does not mutate databases.
- 2026-06-12, maintainer worker: Land the first M3 backend slice as transient generated CRUD endpoints with a process-local generated store. This proves parser/action policy/codegen/inspect/runtime seams without adding a premature database abstraction. Database-backed generated stores, typed seed SQL, and tenancy conventions remain tracked M3 follow-up work.
- 2026-06-12, maintainer worker: Land the M3 data-backed slice by reusing Scenery's existing generated artifact graph and `db seed` ledger rather than adding a separate seed command. Generated CRUD stores now require `DatabaseURL`/managed database env and talk directly to the generated table. That slice temporarily blocked entities with `TenantID`/`tenant_id`; the later M3 tenancy follow-up replaced that guard with generated tenant scoping.
- 2026-06-12, maintainer worker: Land M4 as a hidden generated frontend package contract first. Generated files live under `.scenery/gen/web/<frontend>/` and rely on app-owned TypeScript aliases for `@scenery/generated` and `@scenery/layout-kit`, keeping real product layout integration outside the cache generator while still proving slot assertions and default collection page rendering in the fixture.
- 2026-06-12, maintainer worker: Replace the tenant-field fail-closed diagnostic with the first full convention-profile enforcement path: generated tenant CRUD is auth-only and tied to standard-auth `AuthData.TenantID`. The tenant column is server-controlled for create/update payloads and every data mutation/read is scoped by tenant predicates.
- 2026-06-12, maintainer worker: Keep the M4 runtime adapters dependency-free and app-owned at the edge. Scenery generates typed route/runtime factories and metadata, but the production app still supplies the real router registration function, Electric base URL/client, TanStack DB runtime, and layout-kit implementation.
- 2026-06-13, maintainer worker: Generated model database artifacts use the service-owned schema derived from the model package/service root. `model.Table(...)` remains the table name; Atlas HCL, seed SQL, generated CRUD SQL, and Electric shape metadata all target the same schema-qualified table.
- 2026-06-13, maintainer worker: Generated CRUD route bases use the service-scoped convention `/<service>/<table>`. This keeps generated endpoint IDs and physical table names stable while making HTTP routes app-safe by default; generated route collisions with handwritten or generated routes are check failures requiring `model.Override` or `model.Disable`.
- 2026-06-13, maintainer worker: Generated Atlas HCL uses schema-qualified resource labels for tables and enums (`table "<schema>" "<table>"`, `enum "<schema>" "<enum>"`). This keeps physical database names unchanged while preventing generated app-owned schemas from colliding with existing multi-label Atlas resources.
- 2026-06-13, maintainer worker: Generated model CRUD access defaults to `auth` for all actions and entity shapes. Public generated reads or mutations need a future explicit opt-in instead of an implicit non-tenant default.
- 2026-06-13, maintainer worker: Keep generated tenant support intentionally narrow: `string`, named string types, and `github.com/google/uuid.UUID`. Unsupported tenant field types produce parser/check diagnostics before generated CRUD can compile or run, while UUID values are parsed from standard-auth tenant IDs and returned as ordinary `InvalidArgument` errors on bad input.

## Outcomes & Retrospective

Not yet completed. M1, M2, the M3 backend foundation, the M3 data-backed slice, M3 tenant enforcement, the M4 frontend foundation, a dependency-free M4 runtime-adapter contract, app-owned generated model schemas, service-scoped generated CRUD route bases, collision-safe generated Atlas labels, auth-by-default generated CRUD access, and UUID tenant-field support are implemented as independently reviewable foundations. A production reference-app integration against the real layout kit, router, Electric client, and TanStack DB runtime remains active follow-on work tracked by this plan.

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
- `internal/codegen/generator.go` renders generated model CRUD payload types, endpoint registrations, and database-backed stores into the transient build workspace.
- `testdata/apps/model-dsl` is the acceptance fixture for M1.

## Milestones

M1 is static IR and diagnostics only. Acceptance is deterministic parsing and inspection for stored, relationship, computed, enum, filterable, and collection-page nodes, plus source-pointed diagnostics for non-static builder input, unknown field names, and missing slot components.

M2 adds read-only schema diff mode. It emits desired Atlas HCL under `.scenery/gen/db/<service>/schema.hcl`, targets the app-owned service schema instead of `public`, uses schema-qualified Atlas resource labels to avoid cross-schema label collisions, exposes `scenery generate data --dry-run --json`, adds `scenery db diff --generated`, and surfaces drift from `scenery check` without writing databases.

M3 adds backend generation. The landed foundation generates CRUD stores/endpoints into the transient build workspace with explicit action policy and collision checks. The data-backed slice adds generated seed SQL and database-backed generated stores. Generated CRUD endpoints default to `auth` for every action; the tenancy follow-up additionally scopes convention `TenantID`/`tenant_id` generated CRUD to the active standard-auth tenant. Generated CRUD HTTP route bases default to `/<service>/<table>`, independent of the physical table name's role in SQL artifacts.

M4 adds frontend generation. It now generates TypeScript row types, sync shapes, collection/materializer code, runtime adapter factories, route registration helpers, and default collection pages into a hidden generated package while verifying slot contracts. Remaining M4 work is production reference integration with the real layout-kit/router/Electric/TanStack stack.

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

For the M3 data-backed slice, also run:

- `go test ./internal/parse ./internal/codegen ./cmd/scenery -run 'TestModelDSL|TestGenerateDataDryRunWritesGeneratedSchema|TestDBSeedDiscoversGeneratedModelSeed|TestGenerateModelCRUDBackend' -count=1`
- `go run ./cmd/scenery build --app-root testdata/apps/model-dsl -o .scenery/tmp/modeldsl-m3-data-bin`
- Live proof against a disposable Postgres database: create the `tasks` table, run `scenery db seed --app-root <fixture> --json`, start `scenery serve --app-root <fixture> --listen 127.0.0.1:<port>`, and exercise generated list/create/update/get routes.

## Idempotence and Recovery

M1 has no app runtime side effects. It parses source and writes disposable generated inspect cache files under `.scenery/gen/` when the build path runs. Delete `.scenery/gen/models.json` or `.scenery/gen/views.json` and rerun `scenery build`, `scenery serve`, or `scenery inspect` to regenerate or recompute them.

M2 writes disposable generated desired schema files under `.scenery/gen/db/<service>/schema.hcl`. M3 seed generation writes disposable generated seed files under `.scenery/gen/db/<service>/seed.sql`. Delete those files and rerun `scenery generate data --dry-run --json` or `scenery db seed` to regenerate them. M2 diff mode must remain read-only and must not apply databases. M3 seed application reuses existing sha256 seed tracking and changed-after-apply protection.

The M3 backend writes transient build-workspace Go files. Generated CRUD rows are persisted in the app database selected by `DatabaseURL` or Scenery's managed database env, under the service-owned schema-qualified table. Generated CRUD endpoints are auth-only by default. Generated CRUD HTTP routes use service-scoped paths, so a service `tasksnew` with table `tasks` exposes generated CRUD under `/tasksnew/tasks` while continuing to read/write the `tasksnew.tasks` database table. Tenant-shaped generated CRUD derives the active tenant from standard auth, keeps tenant IDs server-controlled in create/patch payloads, and scopes generated list/get/update/delete SQL by `tenant_id`. Tenant fields may be `string`, a named string type, or `github.com/google/uuid.UUID`; other tenant field types fail before generated CRUD code is used.

M4 writes disposable hidden frontend packages under `.scenery/gen/web/<frontend>/`. Delete the generated package and rerun `scenery generate data --dry-run --json` to regenerate it. App frontend source owns the TypeScript alias and layout-kit implementation; the generated package imports declared slot components by deterministic relative paths and type-checks them against the layout-kit slot contract. Generated `runtime.ts` and `routes.tsx` expose `createGeneratedRuntime`, per-view runtime factories, `createGeneratedRoutes(runtime)`, and `registerGeneratedRoutes(register, runtime)` so an app can bind the generated package to its own router, Electric URL/client, TanStack DB runtime, and layout kit without editing generated files.

## Artifacts and Notes

- M1 fixture: `testdata/apps/model-dsl`.
- M1 schemas: `docs/schemas/scenery.inspect.models.v1.schema.json` and `docs/schemas/scenery.inspect.views.v1.schema.json`.
- M1 generated inspect artifacts: `.scenery/gen/models.json` and `.scenery/gen/views.json`.
- M2 fixture schema: `testdata/apps/model-dsl/tasks/db/schema.hcl`, using the app-owned `tasks` schema.
- M2 schema: `docs/schemas/scenery.db.generated_diff.v1.schema.json`.
- M2 generated desired schema artifact: `.scenery/gen/db/<service>/schema.hcl`.
- M3 generated endpoint marker: `generated: true` in `scenery inspect endpoints|routes --json` for endpoints produced from model CRUD policy.
- M3 generated build artifacts: transient `scenery.gen.go` content in the build workspace package that owns the model entity.
- M3 generated seed artifact: `.scenery/gen/db/<service>/seed.sql`, registered as a `seed`/`initial-data` DB artifact and consumed by `scenery db seed`.
- M4 generated web artifact: `.scenery/gen/web/<frontend>/` with `models.ts`, `shapes.ts`, `collections.ts`, `runtime.ts`, `routes.tsx`, `index.ts`, and package metadata.

## Interfaces and Dependencies

Consumes existing packages: `internal/parse`, `internal/model`, `internal/inspect`, `internal/build`, `cmd/scenery`, and the docs/harness schema validation surfaces.

Public app imports added by M1: `scenery.sh/model` and `scenery.sh/page`.

M2 depends on existing database config and Atlas HCL conventions. M3 depends on `internal/codegen`, `internal/build`, runtime endpoint registration, standard-auth tenancy conventions, and `cmd/scenery/db_seed.go`. M4 depends on `internal/clientgen` rendering patterns and the app frontend layout/slot contract available at the time of implementation.
