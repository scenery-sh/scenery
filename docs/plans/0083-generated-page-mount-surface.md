# Generated Page Mount Surface

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Scenery now has static model/view IR, page-record projections, and generated Electric-backed materializers. A host frontend can inspect and typecheck those pieces, but the next adoption blocker is a stable generated surface that lets an app mount a generated page without importing deep cache paths or reassembling generated internals.

This plan makes each `//scenery:page` collection view produce a boring mountable default page through the hidden generated web package. The page consumes materialized projection records, not raw source rows, and exports through the package barrel that host apps can alias.

## Progress

- [x] 2026-06-26: Added this ExecPlan from the requested next-plan note.
- [x] 2026-06-26: Audited current generated web exports, route helpers, layout-kit fixture, and alias contract.
- [x] 2026-06-26: Confirmed the existing `TaskListPage` export is the page component boundary; no new `pages/` directory was needed.
- [x] 2026-06-26: Added golden checks for the stable barrel export and generated page component shape.
- [x] 2026-06-26: Proved the layout-kit contract through generated `createCollectionPage<TaskListRecord, ...>` typing and fixture TypeScript validation.
- [x] 2026-06-26: Ran focused Go and generated-web typecheck/render validation.

## Surprises & Discoveries

- The existing `routes.tsx` plus `index.ts` barrel already provided the mount surface. The smallest fix was to prove and document it, not split generated pages into another module.

## Decision Log

- Decision: Keep 0083 limited to the mountable generated page contract.
  Rationale: The existing DSL already has IR, projection records, and Electric materializers; projects now need a stable import/mount boundary, not more DSL expressiveness.
  Date/Author: 2026-06-26 / Codex.

- Decision: Do not add computed fields, mutations, custom layouts, server-side read endpoints, or Tasks-specific behavior in this plan.
  Rationale: Those features depend on a working mount surface and would make acceptance unclear.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Completed 2026-06-26. The generated web package exposes mountable generated pages through the stable package barrel: `TaskListPage` is exported from `@scenery/generated`, consumes `TaskListRecord` page records, and still routes through layout-kit. Golden tests now assert the page export, route export, page-record props, and layout-kit generic shape. No production generator reshuffle was needed.

## Context and Orientation

Relevant files:

- `internal/webgen/webgen.go` renders `.scenery/gen/web/<frontend>/` files.
- `internal/webgen/webgen_test.go` covers generated frontend package shape.
- `cmd/scenery/generated_schema_test.go` covers fixture-generated web output determinism.
- `testdata/apps/model-dsl/web` is the TypeScript fixture with alias and layout-kit wiring.
- `docs/local-contract.md`, `docs/agent-guide.md`, and `SKILL.md` describe generated package adoption for host apps.
- `docs/plans/0081-page-record-projection-ir.md` and `docs/plans/0082-electric-page-projection-materializers.md` define the projection/materializer boundary this plan must preserve.

Current generated code already exposes route helpers and runtime adapters. Start by auditing whether those helpers are enough or whether the generated page should move into a clearer `pages/` module. Prefer the smallest stable barrel surface over a broad generated package reshuffle.

## Milestones

M1 audits current generated web package exports and records the gap between route helpers and a mountable page contract.

M2 emits or confirms per-view generated page components that consume projection records.

M3 exports pages/routes through one stable public barrel import path.

M4 adds fixture proof that a host app imports the generated page or route through the alias and typechecks/renders without deep generated-cache imports.

## Plan of Work

Keep the hidden generated package as the integration boundary. Host app code should import from the configured alias, such as `@scenery/generated`, and should not need relative paths into `.scenery/gen`.

Generated page code should stay read-only and layout-kit-backed. If there is already a generated route/page helper that satisfies the contract, keep it and harden exports/tests instead of moving files. If a new `pages/` module is necessary, make it deterministic and export it through `index.ts`.

## Concrete Steps

1. Inspect generated `index.ts`, `routes.tsx`, `runtime.ts`, and layout-kit fixture contracts.
2. Decide whether the current `TaskListPage` export is the mount surface or whether a dedicated `pages/TaskListPage.tsx` file is needed.
3. Ensure the generated page consumes `TaskListRecord` page records and never requires host code to pass `TaskRow` source rows.
4. Add a stable public barrel export for generated pages and routes.
5. Add the smallest compile-time assertion that generated page props match the layout-kit contract.
6. Update docs only where the host adoption recipe or alias contract is stale.
7. Keep generated output deterministic.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/pulse`:

- `go test ./internal/parse ./internal/webgen ./internal/inspect ./internal/codegen ./internal/schemagen ./cmd/scenery`
- `go test ./...`
- `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json`
- `go run ./cmd/scenery inspect docs --json`
- `git diff --check`

Run generated-web proof from `testdata/apps/model-dsl/web` when generated frontend behavior changes:

- `npm run typecheck`
- `npm run render` or the fixture's current render/build smoke

Call this complete when the model DSL fixture proves:

- a Go page declaration produces materialized page records,
- a generated default page component consumes projection records,
- a generated route or route manifest can mount that page,
- a stable alias/barrel import reaches the generated page or route,
- no host app code needs raw Electric/source row types for normal page mounting,
- TypeScript typecheck and render smoke pass.

## Idempotence and Recovery

Generated files under `.scenery/gen/` are disposable. Delete `testdata/apps/model-dsl/.scenery/gen` and rerun `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json` to recover.

Temporary frontend dependency installs for validation must stay inside `testdata/apps/model-dsl/web` and be removed before finishing unless the repo already intentionally tracks them.

## Artifacts and Notes

The source note recommends this sequence: 0081 projection IR, 0082 Electric materializers, 0083 generated page mount surface, 0084 simple entity page pilot, then a later Tasks stress test.

## Interfaces and Dependencies

This plan affects beta generated frontend contracts:

- generated page components,
- generated route or route manifest exports,
- generated package barrel exports,
- host-app TypeScript alias guidance,
- layout-kit compile-time compatibility checks.

It must preserve the 0082 boundary: Electric/TanStack source rows stay raw, while generated pages consume projection records.
