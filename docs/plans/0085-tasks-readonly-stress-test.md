# Tasks Read-Only Stress Test

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The simple generated page pilot proved that a small entity can declare a page, generate a page-record projection, and mount a generated page through the hidden generated package. The next question is whether the same model/view DSL can support a realistic read-only Tasks page without handwritten generated-code edits.

This plan stress-tests the current DSL with a scalar Tasks list: status, priority, assignee, due date, created date, updated date, static open-task filtering, static default ordering, and minimal display metadata. The goal is to answer whether waiting projects can declare a useful read-only Tasks page now, or whether more foundational seams are still missing.

## Progress

- [x] 2026-06-26: Added this ExecPlan from the requested Tasks stress-test note.
- [x] 2026-06-26: Added minimal static page query/display DSL in `scenery.sh/page`: `page.Filter`, `page.Sort`, and `page.Column`.
- [x] 2026-06-26: Parsed filter, sort, and column display metadata into model/view IR and exposed it through `scenery inspect views --json`.
- [x] 2026-06-26: Generated collection metadata plus source-row filtering/sorting before page-record materialization.
- [x] 2026-06-26: Upgraded `testdata/apps/model-dsl` into a read-only Tasks stress fixture and host render smoke.
- [x] 2026-06-26: Updated schemas, docs, and focused tests for the new model/view contract.
- [x] 2026-06-26: Passed JSON/diff checks, focused Go tests, full `go test ./...`, docs/views inspection, generated data dry-run, model-dsl web typecheck/build/render, and self-harness.

## Surprises & Discoveries

- The ignored fixture cache under `testdata/apps/model-dsl/.scenery/gen/views.json` can shadow source changes during local `scenery inspect views --json`. Removing the stale ignored cache lets inspect rebuild from source; generated cache remains non-contract state.
- The generated runtime already delegates to the collection definition's `materialize` function, so static filters and sorts only needed to live in generated `collections.ts`; routes and runtime did not need another abstraction.

## Decision Log

- Decision: Add one bigger 0085 instead of splitting into separate query IR, display metadata IR, and Tasks pilot plans.
  Rationale: The missing seams were small and tightly coupled by the fixture proof, so splitting would add coordination without reducing implementation risk.
  Date/Author: 2026-06-26 / Codex.

- Decision: Keep static filters to `eq`, `neq`, `is_null`, and `is_not_null`, with one field and one literal value.
  Rationale: This covers the open Tasks filter while avoiding boolean expression trees, SQL fragments, joins, or user-driven filters.
  Date/Author: 2026-06-26 / Codex.

- Decision: Apply static filters and sorts to source rows inside generated collection materialization, then convert to page records.
  Rationale: Filter/sort fields may be storage-row fields, while generated pages must still consume `TaskListRecord` page records.
  Date/Author: 2026-06-26 / Codex.

- Decision: Use display kinds `text`, `datetime`, and `badge`.
  Rationale: Tasks needs enum-ish badges and date/time hints, but the layout-kit can still own concrete rendering.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Completed 2026-06-26. The `testdata/apps/model-dsl` fixture now declares a realistic read-only Tasks page with scalar fields, status/priority enum metadata, static `status != "done"` filtering, default `due_at asc, created_at desc` sorting, and minimal badge/datetime display hints. Generated web output keeps `TaskRow` as the source row, `TaskListRecord` as the page-facing projection, and applies static query rules before materializing records. Host fixture code mounts the generated route/page through `@scenery/generated` and proves filtering/sorting through the render smoke. Self-harness passes with warnings only for existing parser size and slow-test timing debt.

The remaining bigger gaps are relationships, computed projection fields, and mutations; those should be separate follow-up plans.

## Context and Orientation

Relevant Scenery files:

- `page/page.go` defines the public static page DSL used by target apps.
- `internal/parse/parser.go` parses `//scenery:page` collection literals into model/view IR.
- `internal/model/model.go` stores parsed view metadata.
- `internal/inspect/inspect.go` and `docs/schemas/scenery.inspect.views.v1.schema.json` define `scenery inspect views --json`.
- `internal/webgen/webgen.go` writes the generated hidden frontend package under `.scenery/gen/web/<frontend>/`.
- `testdata/apps/model-dsl/tasks/model.go` is the Tasks stress fixture.
- `testdata/apps/model-dsl/web/src/generated-entry.ts` and `render-generated.ts` are the host import/render smoke.

The generated cache under `.scenery/gen/` is disposable. Do not edit generated files by hand; regenerate with `scenery generate data --dry-run --json`.

## Milestones

M1 adds static query and display metadata to the page DSL and parser.

M2 exposes the new metadata in inspect JSON and schema.

M3 makes generated collections carry query/display metadata and apply static filters/sorts before page-record materialization.

M4 upgrades the model DSL fixture into a realistic read-only Tasks page.

M5 proves the fixture through Go tests, inspect JSON, generated data dry-run, and frontend typecheck/build/render smoke.

## Plan of Work

Start from the existing generated collection page path. Add no new runtime service, no new dependency, and no new page framework. The static DSL should only describe field-level query/display metadata. The generator should continue to use the source row type for sync/TanStack DB input, materialize page records through the generated projection function, and expose mountable generated pages through the existing barrel.

If Tasks requires relationships, computed fields, joins, custom renderers, or mutation behavior, record that as follow-up work rather than expanding this plan.

## Concrete Steps

1. Add public `page.Filter`, `page.Sort`, and `page.Column` metadata helpers.
2. Parse `ColumnDisplays`, `Filters`, and `Sorts` from `page.Collection` literals.
3. Validate that referenced fields exist and are stored fields.
4. Add inspect JSON and schema records for column display metadata, static filters, and static sorts.
5. Update generated collection definitions with `columns`, `filters`, `sorts`, and query-aware `materialize`.
6. Extend `testdata/apps/model-dsl` with Tasks fields: status, priority, assignee, due date, created date, and updated date.
7. Update host fixture code to pass raw `TaskRow` values through generated runtime materialization and mount generated page records.
8. Update docs and tests.

## Validation and Acceptance

Required validation from `/Users/petrbrazdil/Repos/pulse`:

- `python3 -m json.tool docs/knowledge.json >/tmp/knowledge.json.check`
- `python3 -m json.tool docs/schemas/scenery.inspect.views.v1.schema.json >/tmp/views-schema.json.check`
- `git diff --check`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go test ./internal/parse ./internal/webgen ./internal/inspect ./internal/codegen ./internal/schemagen ./cmd/scenery`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go test ./...`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery inspect docs --json`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery inspect views --app-root testdata/apps/model-dsl --json`
- `env SCENERY_AGENT_HOME=$(mktemp -d) go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json`

Required fixture frontend validation from `/Users/petrbrazdil/Repos/pulse/testdata/apps/model-dsl/web`:

- `npm run typecheck`
- `npm run build`
- `npm run render`

Acceptance evidence:

- `scenery inspect views --json` includes `TaskList` with projection fields, `column_displays`, `filters`, and `sorts`.
- Generated `projections.ts` includes `TaskListRecord` and `materializeTaskList(row: TaskRow): TaskListRecord`.
- Generated `collections.ts` includes static filters/sorts and `materializeTaskListCollection`.
- Generated route/page exports still consume `TaskListRecord`.
- Host fixture imports through `@scenery/generated`, does not hand-edit generated files, and the render smoke proves `done` rows are filtered and open rows are sorted.

## Idempotence and Recovery

If inspect returns stale model/view data, remove ignored cache files under `testdata/apps/model-dsl/.scenery/gen/models.json` and `testdata/apps/model-dsl/.scenery/gen/views.json`, then rerun inspect or generation. Do not commit `.scenery/` output.

If frontend validation lacks dependencies, run `npm install --no-audit --no-fund --package-lock=false` inside `testdata/apps/model-dsl/web`. Remove `node_modules` and any package-lock churn before finishing unless the repo intentionally starts tracking them.

## Artifacts and Notes

The stress test deliberately remains read-only. It does not add task creation, editing, drag/drop, Kanban, comments, attachments, joins, relationships, computed columns, auth/ownership, or conflict handling.

## Interfaces and Dependencies

This plan changes the beta model/view surface:

- `scenery.sh/page` static DSL gains filter, sort, and display metadata helpers.
- `scenery inspect views --json` gains optional `column_displays`, `filters`, and `sorts` arrays.
- Generated web collections include display/query metadata and apply static source-row query rules during materialization.
- The generated page mount surface remains the existing `TaskListPage`, `createGeneratedRoutes`, `registerGeneratedRoutes`, and barrel export path.
