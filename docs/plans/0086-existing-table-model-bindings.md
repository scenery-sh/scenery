# Existing Table Model Bindings

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery's static model/view DSL currently assumes that a declared entity owns a generated database table. That works for greenfield fixtures, but production apps often need read-only pages over already-existing tables such as `legacy.customers` or app-owned service schemas.

This plan adds a first-class source ownership seam:

```text
existing physical table -> Scenery entity binding -> projection/page record -> generated read-only page
```

The first slice is intentionally read-only. Existing table entities may generate list/get endpoints and frontend page artifacts, but Scenery must not emit schema or seed ownership artifacts for those tables and must reject generated mutations.

## Progress

- [x] 2026-06-26: Added this ExecPlan after the Tasks read-only stress test landed.
- [x] 2026-06-26: Added public `model.ExistingTable(schema, table)` DSL for existing table bindings.
- [x] 2026-06-26: Parsed schema/table/ownership into model IR and `scenery inspect models --json`.
- [x] 2026-06-26: Kept generated schema/seed output from claiming existing tables.
- [x] 2026-06-26: Generated read-only endpoints and frontend/page artifacts over existing table bindings.
- [x] 2026-06-26: Added `testdata/apps/existing-table-dsl` and validation coverage.

## Surprises & Discoveries

- The static fixture frontend `node_modules` directory must be removed after local smoke validation, or self-harness architecture checks correctly scan large dependency files.

## Decision Log

- Decision: Add `model.ExistingTable(schema, table)` instead of overloading `model.Table` with a hidden ownership flag.
  Rationale: Existing table ownership should be hard to miss at the call site.
  Date/Author: 2026-06-26 / Codex.

- Decision: Keep the first implementation read-only for existing tables.
  Rationale: Reading from existing schemas is useful now, while generated mutation safety needs explicit writable-field and tenant/concurrency contracts.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Completed 2026-06-26. `model.ExistingTable("legacy", "customers")` now marks an entity as bound to an existing physical table, exposes source ownership in inspect models JSON, skips generated DB ownership artifacts, rejects generated mutations and seed rows, and keeps generated list/get plus frontend projections/pages working against the explicit `legacy.customers` table. The new existing-table fixture proves a handwritten app-owned schema source, read-only generated Customer endpoints, entity source metadata, page projection records, generated collections/routes, and host frontend render behavior.

Validation passed: JSON/schema/diff checks, focused Go packages, full `go test ./...`, docs inspection, existing-table inspect models/views, generated data dry-run, fixture web typecheck/build/render, and `scenery harness self --summary --write`. Self-harness passed with warning-class existing parser size and slow-test timing findings only.

## Context and Orientation

Relevant files:

- `model/model.go` defines the public static model DSL.
- `internal/model/model.go` stores entity table/source metadata.
- `internal/parse/parser.go` parses `model.Entity` options and attaches generated model endpoints.
- `internal/inspect/inspect.go` and `docs/schemas/scenery.inspect.models.v1.schema.json` define `scenery inspect models --json`.
- `internal/schemagen/schema.go` and `internal/schemagen/seed.go` generate disposable DB artifacts.
- `internal/codegen/generator.go` writes generated list/get backend stores.
- `internal/webgen/webgen.go` writes entity source metadata and page packages.
- `testdata/apps/existing-table-dsl` is the focused fixture for this plan.

## Milestones

M1 adds the public `model.ExistingTable(schema, table)` helper and model IR source metadata.

M2 exposes the source binding through inspect JSON and schema.

M3 prevents generated schema and seed artifacts from claiming existing-source entities.

M4 keeps generated read-only endpoints, entity source metadata, projections, collections, pages, routes, and barrel exports working over the explicit schema-qualified table.

M5 adds a focused existing-table fixture and validation proof.

## Plan of Work

1. Add public model DSL helpers for existing table binding.
2. Extend internal entity IR with table schema and source ownership.
3. Update the parser to resolve `model.ExistingTable(schema, table)` and preserve `model.Table(name)` behavior.
4. Update table helper functions so generators can ask for schema, table, and qualified table without assuming service-derived ownership.
5. Update inspect models JSON/schema to include source kind and schema-qualified table.
6. Update schema and seed generators to skip existing-source entities.
7. Reject generated create/update/delete and seed rows for existing-source entities.
8. Add `testdata/apps/existing-table-dsl` with an existing-table Customer page.
9. Add focused parser, inspect, generator, and command tests.
10. Update README, local contract, SKILL, agent guide, app cookbook, knowledge, and completed plan index when complete.

## Concrete Steps

1. Add `model.ExistingTable(schema, table)`.
2. Add entity source kind/schema/table metadata and helper functions.
3. Parse and validate existing table options, with non-empty schema/table requirements.
4. Reject generated create/update/delete and `model.Seed(...)` rows for existing-source entities.
5. Expose model source metadata through inspect JSON and schema.
6. Skip existing-source entities in generated Atlas HCL and generated seed SQL.
7. Reuse qualified table helpers in generated read endpoints and web/source metadata.
8. Add `testdata/apps/existing-table-dsl` and focused parser/command tests.
9. Update docs, knowledge index, and completed plan index.

## Validation and Acceptance

Required validation from repo root:

- `python3 -m json.tool docs/knowledge.json >/tmp/knowledge.json.check`
- `python3 -m json.tool docs/schemas/scenery.inspect.models.v1.schema.json >/tmp/models-schema.json.check`
- `git diff --check`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go test ./internal/parse ./internal/webgen ./internal/inspect ./internal/codegen ./internal/schemagen ./cmd/scenery`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go test ./...`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery inspect docs --json`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery inspect models --app-root testdata/apps/existing-table-dsl --json`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery inspect views --app-root testdata/apps/existing-table-dsl --json`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery generate data --app-root testdata/apps/existing-table-dsl --dry-run --json`

Required fixture frontend validation from `testdata/apps/existing-table-dsl/web`, if dependencies are available:

- `npm run typecheck`
- `npm run build`
- `npm run render`

Acceptance evidence:

- Public DSL can declare `model.ExistingTable("legacy", "customers")`.
- `scenery inspect models --json` exposes `source.kind=existing`, `source.schema=legacy`, `source.table=customers`, and `source.qualified_table=legacy.customers`.
- Generated schema/seed artifacts do not claim ownership of the existing table.
- Generated entity source metadata and frontend source rows use the explicit schema-qualified table.
- Generated page projection and materializer work unchanged over the existing table source row.
- Generated create/update/delete actions are rejected for existing tables with clear diagnostics.
- Existing generated-table behavior remains backward-compatible.

## Explicit Non-Goals

- Live database introspection.
- Automatic Go model generation from a database.
- Automatic migrations from existing tables into Scenery-owned tables.
- Generated create/update/delete over existing tables.
- Relationships, joins, computed projection fields, nullable/type inference from live schema, index/constraint ownership, multi-database routing, or production replication publication management.

## Idempotence and Recovery

Generated `.scenery/` files remain disposable and should not be committed. If stale ignored cache files shadow source changes, remove the fixture `.scenery/gen/models.json` and `.scenery/gen/views.json`, then rerun inspect or generation.

If frontend validation lacks dependencies, run `npm install --no-audit --no-fund --package-lock=false` inside `testdata/apps/existing-table-dsl/web`. Remove `node_modules` and package-lock churn before finishing unless the repo intentionally starts tracking them.

## Artifacts and Notes

- `testdata/apps/existing-table-dsl/legacy/db/schema.hcl` is an app-owned existing schema source, not generated model output.
- `scenery generate data --dry-run --json` for the fixture emits generated web package files under `.scenery/gen/web/web/` and no `.scenery/gen/db/legacy/schema.hcl` or `.scenery/gen/db/legacy/seed.sql`.
- Generated `.scenery/` output and fixture `node_modules/` remain disposable local artifacts.

## Interfaces and Dependencies

- `scenery.sh/model` gains `ExistingTable(schema, table)`.
- Internal entity IR records source ownership as generated or existing.
- `scenery inspect models --json` includes model `source` metadata.
- Generated DB schema/seed output depends on source ownership and skips existing-source entities.
- Generated read-only backend and web/source output depend on `EntityDatabaseSchema` and `EntityQualifiedTable` so generated and existing-source entities share the same table lookup path.
