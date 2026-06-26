# Electric Page Projection Materializers

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

`docs/plans/0081-page-record-projection-ir.md` split collection views into three concepts: the source row synced by Electric, the page-facing projection record, and the view that consumes that record. This plan makes that boundary operational for the Electric-backed generated web happy path.

For each generated collection view, the hidden frontend package should keep Electric and TanStack DB source collections raw and table-shaped, then expose page records through a generated client-side materializer based on `ViewProjection`. Generated runtime, route, and page code should consume those page records instead of treating storage rows as view records.

Keep this narrow. This is not the Tasks stress test, not computed projections, not server-side read endpoints, and not a new model syntax.

## Progress

- [x] 2026-06-26: Added this ExecPlan from the requested Oracle follow-up.
- [x] 2026-06-26: Audited current 0081 generated output; the implementation already had the materializer boundary, so no production generator patch was needed.
- [x] 2026-06-26: Tightened generated-web golden assertions for raw `TaskRow`, page-facing `TaskListRecord`, row-to-record materializer output, and generated consumers typed as `TaskListRecord`.
- [x] 2026-06-26: Ran focused Go, full Go, docs, generated data, and temporary generated-web typecheck/render validation.
- [x] 2026-06-26: Completed this ExecPlan and moved it to completed plan indexing.

## Surprises & Discoveries

- The 0081 implementation had already moved runtime row sources to raw shape rows and route/page consumers to materialized projection records. The missing 0082 work was durable proof, not another abstraction.

## Decision Log

- Decision: Limit 0082 to Electric-backed generated web collections.
  Rationale: Raw Electric rows plus client-side projection materializers are the smallest operational proof of the 0081 IR boundary.
  Date/Author: 2026-06-26 / Codex.

- Decision: Continue rejecting computed projection fields.
  Rationale: Direct source-field copies are deterministic today; computed fields need a later expression and evaluation contract.
  Date/Author: 2026-06-26 / Codex.

- Decision: Do not change entity modeling syntax, schema migration behavior, or app routing for this slice.
  Rationale: Those follow-on features depend on the materialized page-record boundary and would blur the acceptance criteria.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Completed 2026-06-26. The Electric-backed generated web package now has tested proof of the operational projection boundary: `TaskRow` remains the raw Electric/source row, `TaskListRecord` is the page-facing record, `projections.ts` materializes from source rows to page records, collections and runtime keep `TaskRow`/`TaskListRecord` distinct, and generated routes pass materialized page records to layout-kit. Computed projection fields remain rejected.

## Context and Orientation

Relevant files:

- `internal/model/model.go` defines `ViewProjection` and `ProjectionField`.
- `internal/parse/parser.go` builds and validates collection view projections.
- `internal/webgen/webgen.go` renders the hidden generated frontend package under `.scenery/gen/web/<frontend>/`.
- `internal/webgen/webgen_test.go` and `cmd/scenery/generated_schema_test.go` cover generated frontend output.
- `testdata/apps/model-dsl` is the fixture for source rows, projection records, and generated route/runtime code.
- `docs/local-contract.md` and `docs/agent-guide.md` describe generated frontend packages and the beta static model/view contract.

The current 0081 work may already contain part of the desired materializer plumbing. Start by inspecting generated output and tests, then finish the smallest missing piece rather than adding a parallel abstraction.

## Milestones

M1 audits the current generated frontend output and records exactly what is already true.

M2 makes generated materializers use `ViewProjection.Fields` as the only row-to-record mapping source.

M3 keeps Electric/TanStack source rows raw while generated collection/runtime/page code exposes page-facing projection records.

M4 adds fixture proof and temporary generated-web validation so TypeScript consumers see the intended boundary.

## Plan of Work

Work inside the existing generated web package. Do not add public DSL, new dependencies, new config, or new runtime services. The generator should produce the raw/source row type used by Electric, the page record projection type, and a generated materializer from source row to page record. Collection/runtime/page templates should flow through that materializer before handing rows to layout-kit.

The exact function names can follow current generator conventions, but the invariant must be visible in generated output: `TaskRow` remains the raw source row, `TaskListRecord` is the page record, and generated consumers use `TaskListRecord`.

## Concrete Steps

1. Regenerate or inspect the model DSL fixture output with `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json`.
2. Check whether `projections.ts`, `collections.ts`, `runtime.ts`, and `routes.tsx` already satisfy the source-row-to-page-record boundary.
3. Patch `internal/webgen/webgen.go` only where generated code still infers projection shape ad hoc or consumes storage row types at the page boundary.
4. Keep parser rejection for computed projection columns unchanged.
5. Add or tighten the smallest golden checks that prove raw row type, page record type, materializer, and generated consumer type.
6. Update `docs/local-contract.md`, `docs/agent-guide.md`, or `SKILL.md` only if the public generated contract prose is stale after implementation.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/pulse`:

- `go test ./internal/parse ./internal/webgen ./internal/inspect ./internal/codegen ./internal/schemagen ./cmd/scenery`
- `go test ./...`
- `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json`
- `go run ./cmd/scenery inspect docs --json`
- `git diff --check`

Also keep the temporary generated-web proof for `testdata/apps/model-dsl/web` when touching generated frontend behavior:

- install temporary dependencies only inside `testdata/apps/model-dsl/web` if needed,
- run the fixture's typecheck/render commands,
- remove temporary `node_modules` or lockfile churn afterward unless those files were already intentionally tracked.

Call this plan complete when the model DSL fixture proves:

- `TaskRow` is the raw Electric/source row type,
- `TaskListRecord` is the page-facing projection record type,
- a generated materializer maps `TaskRow` to `TaskListRecord`,
- generated runtime/page/slot consumers use `TaskListRecord`,
- computed projection fields still fail with the existing deterministic diagnostic.

## Idempotence and Recovery

Generated files under `testdata/apps/model-dsl/.scenery/gen` are disposable. Delete that cache and rerun `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json` to recreate it.

If temporary frontend dependencies are installed for proof, remove generated dependency directories and unintended lockfile churn before finishing. Do not commit `.scenery/`, `node_modules`, or other machine-local state.

## Artifacts and Notes

The source prompt for this plan recommended keeping 0082 narrow: realize the 0081 projection IR for the Electric-backed happy path only. The next likely plan after this is a simple generated entity page pilot, then a Tasks stress test.

## Interfaces and Dependencies

This plan affects beta generated frontend contracts:

- raw Electric shape/source row types remain storage-table shaped,
- page projection records are generated from `ViewProjection`,
- generated materializers copy supported source fields into page records,
- generated collection/runtime/page code consumes page records,
- computed, joined, aggregate, client-only, and server-side projection fields remain out of scope.
