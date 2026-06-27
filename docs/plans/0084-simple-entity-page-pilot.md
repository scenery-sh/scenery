# Simple Entity Page Pilot

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

After the generated page mount surface exists, Scenery needs one simple adoption proof in a real or realistic host app. The goal is to prove the path a project will actually use: declare a simple entity and page in Go, run Scenery generation, import the generated page through the stable alias, mount it, and pass host frontend validation without hand-editing generated files.

This is an adoption proof, not a feature expansion. It should discover integration friction before the later Tasks stress test forces more DSL design.

## Progress

- [x] 2026-06-26: Added this ExecPlan from the requested next-plan note.
- [x] 2026-06-26: Picked `testdata/apps/model-dsl` as the self-contained simple entity pilot because it already mirrors the intended host alias/layout-kit structure.
- [x] 2026-06-26: Used the existing Go `Task`/`TaskList` declaration and host alias wiring.
- [x] 2026-06-26: Updated the host fixture to import `TaskListPage` and `TaskListRecord` from `@scenery/generated` and render with page records only.
- [x] 2026-06-26: Documented the short generated-page adoption recipe in agent-facing docs.
- [x] 2026-06-26: Ran Scenery-side and host-app validation.

## Surprises & Discoveries

- A separate pilot fixture was unnecessary. `testdata/apps/model-dsl` already had a simple scalar entity, Go page declaration, alias config, layout-kit fixture, and render smoke; using it kept the adoption proof small and self-contained.

## Decision Log

- Decision: Make 0084 depend on the 0083 generated mount surface.
  Rationale: The pilot should validate the intended host-app contract, not invent a parallel mounting path.
  Date/Author: 2026-06-26 / Codex.

- Decision: Prefer a real ONLV simple entity page if the worktree is available; otherwise create a faithful Scenery fixture.
  Rationale: A real project exposes alias, gitignore, route, and validation friction better than a hermetic fixture, but the plan must remain executable from this repo.
  Date/Author: 2026-06-26 / Codex.

- Decision: Keep Tasks out of this plan.
  Rationale: Tasks is valuable as a later stress test, but it can force premature DSL design before the simple adoption path is proven.
  Date/Author: 2026-06-26 / Codex.

## Outcomes & Retrospective

Completed 2026-06-26. The `testdata/apps/model-dsl` fixture now proves the simple adoption path: a Go entity/page declaration generates a projection and page, host code imports `TaskListPage` and `TaskListRecord` through `@scenery/generated`, mounts the generated page without raw `TaskRow`, and passes typecheck/render validation. The adoption recipe is documented in the agent guide, installable skill, local contract, and app cookbook.

## Context and Orientation

Relevant Scenery files:

- `docs/plans/0083-generated-page-mount-surface.md` defines the generated page mount contract that this plan pilots.
- `internal/webgen/webgen.go` and `cmd/scenery/generated_schema_test.go` define the generated web package behavior.
- `testdata/apps/model-dsl` is the existing self-contained generated model/page fixture.
- `docs/agent-guide.md`, `docs/local-contract.md`, and `SKILL.md` describe host-app generated package expectations.

Potential pilot targets:

- A real ONLV simple entity page if `/Users/petrbrazdil/Repos/onlv` is available and has a suitable low-risk entity.
- A Scenery fixture such as `testdata/apps/onlv-simple-entity/` if the real app is unavailable or would make the slice too broad.

Good entity shape: `Project`, `Customer`, `Note`, `Task-lite`, or another table-backed entity with `id` plus three to six scalar fields. Avoid joins, computed fields, mutations, nested collections, and custom UI.

## Milestones

M1 chooses and records the pilot target.

M2 declares one simple entity and read-only page in Go.

M3 wires the host frontend alias and mounts the generated page or generated route through the 0083 barrel surface.

M4 proves typecheck/build or render smoke without hand-editing generated files.

M5 records the exact adoption recipe and any small generator fixes discovered by the pilot.

## Plan of Work

Start from the smallest credible host-app path. Do not use raw generated source row types in handwritten host app code unless deliberately testing lower-level APIs. The ordinary app-facing surface should be the generated page, route, or page-record type exported through the stable alias.

When integration friction appears, fix only small contract bugs exposed by the pilot: alias path mismatch, missing barrel exports, route naming instability, layout-kit import mismatch, generated package gitignore/docs mismatch, non-deterministic output order, stale generated package diagnostics, or missing setup docs.

## Concrete Steps

1. Confirm 0083 is complete and identify the generated page/route import surface.
2. Inspect the candidate host app or fixture and choose one simple entity.
3. Add one Go entity/page declaration with scalar fields only.
4. Run Scenery generation and inspect the projection in `scenery inspect views --json`.
5. Configure the host TypeScript alias if needed.
6. Mount the generated page or route through the stable alias.
7. Run host typecheck/build or render smoke.
8. Update docs with the short adoption recipe: declare entity/page, run generation, configure alias, import generated route/page, mount it, run validation.

## Validation and Acceptance

Run Scenery-side validation from `/Users/petrbrazdil/Repos/scenery`:

- `go test ./internal/parse ./internal/webgen ./internal/inspect ./internal/codegen ./internal/schemagen ./cmd/scenery`
- `go test ./...`
- `go run ./cmd/scenery inspect docs --json`
- `git diff --check`

Run pilot validation in the selected host app or fixture:

- `scenery generate ...`
- `scenery inspect views --json`
- `npm run typecheck`
- `npm run build` or the host's current render smoke

Call this complete when evidence shows:

- a simple entity is declared in Go,
- the projection is visible in inspect JSON,
- the generated materializer exists,
- the generated page exists,
- host app code imports the generated page through the stable alias,
- host typecheck/build/render smoke passes,
- generated files were not hand-edited.

## Idempotence and Recovery

Generated files under `.scenery/gen/` are disposable and should be regenerated rather than edited. If the pilot uses ONLV, preserve unrelated user work and follow that app's local `AGENTS.md` instructions. If a fixture is added in this repo, keep generated cache output ignored and make validation reproducible from source.

Temporary frontend dependency installs must be removed before finishing unless they are part of the app's intentional tracked dependency set.

## Artifacts and Notes

Tasks should be a later stress test, likely 0085. This plan should feed real adoption friction into that stress test instead of guessing.

## Interfaces and Dependencies

This plan depends on the 0083 generated page mount surface. It may touch:

- one simple host app or fixture entity/page declaration,
- host-app TypeScript alias configuration,
- host route mounting,
- Scenery docs for the generated page adoption recipe,
- small generator fixes directly exposed by the pilot.

It must not expand into computed fields, relationships, CRUD/actions, server-side non-synced reads, migrations, or generalized page composition.
