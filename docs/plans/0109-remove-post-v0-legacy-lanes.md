# Remove Post-v0 Legacy Lanes

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current while the cleanup runs.

## Purpose / Big Picture

Delete the parallel runtime, configuration, routing, generation, and CLI paths
that became unnecessary once edition-2027 `.scn` and the singular CLI v1
protocol became the only supported model. Scenery should have one path for
declared tasks, HTTP contracts, route data, generation, and standard auth.

## Progress

- [x] 2026-07-12 - Re-read current repository instructions, documentation status, active plans, contracts, and the tracked-file Ponytail audit.
- [x] 2026-07-12 - Moved standard auth to typed contract codecs and deleted the reflection HTTP lane and runtime compatibility overloads.
- [x] 2026-07-12 - Removed config-defined shell tasks and obsolete CLI/config dispatch helpers.
- [x] 2026-07-12 - Removed duplicate route data, generic plumbing, compatibility fields, dependency, dashboard metadata, and orphan fixture.
- [x] 2026-07-12 - Synchronized current docs, schemas, indexes, generated fixtures, and agent instructions.
- [x] 2026-07-12 - Passed focused and full Go tests, Go vet, static searches, docs inspection, UI checks, fixture probes, and self-harness.
- [x] 2026-07-12 - Follow-up removed `dev.setup`, configurable app database URL env naming, Symphony app-ID fallback, the old database snapshot command, and non-output short CLI aliases.

## Surprises & Discoveries

- The installed `scenery` binary still predates the singular v1 envelope, so all source-of-truth inspection uses `go run ./cmd/scenery` or the worktree-local harness binary.
- Current docs still describe config-defined shell tasks even though root agent policy requires every task identity in `.scn`.
- Removing `Config.MarshalJSON` exposed empty optional storage and deploy objects in inspection JSON; the serializer remains because Go cannot omit zero struct values, but its task compatibility field is gone.
- The generator signature cleanup changed native fixture output; self-harness found and verified the required storage fixture regeneration.

## Decision Log

- Decision: Delete every audited parallel path instead of preserving adapters or deprecation parsers.
  Rationale: v0 is unsupported and the user explicitly requested the remaining simplification findings be fixed.
  Date/Author: 2026-07-12 / user and Codex.
- Decision: Preserve edition-2027 compatibility analysis, immutable migration plans, code tasks, and active-plan functionality.
  Rationale: Those are current product contracts, not v0 compatibility scaffolding.
  Date/Author: 2026-07-12 / Codex.

## Outcomes & Retrospective

Completed on 2026-07-12. Scenery now has one `.scn` code-task path, one route
manifest, typed standard-auth codecs, canonical auth environment names, and no
reflection endpoint decoder/encoder or internal call compatibility API. The
cleanup removed more than a thousand lines while preserving the current
edition-2027 contract. Full self-harness passed with only pre-existing advisory
review-date, file-size, and timing warnings.

## Context and Orientation

The reflection HTTP path is under `runtime` and currently serves hand-written
standard auth under `auth`. Configured shell tasks and CLI dispatch live under
`internal/app` and `cmd/scenery`. Agent route state is under `internal/agent`;
code generation is under `internal/codegen`; dashboard source is under
`apps/consolenext`.

## Milestones

1. Collapse runtime/auth and CLI/config onto their singular current paths.
2. Delete smaller duplicate data, wrappers, dependencies, and fixtures.
3. Update all current contract layers and prove the supported flows still work.

## Plan of Work

Work in independent compileable slices. Trace all callers before deletion,
retain validation at trust boundaries, and migrate current consumers directly
instead of adding translation helpers. Historical ExecPlans remain historical.

## Concrete Steps

1. Move standard auth registration to the native contract or raw endpoint path, then delete reflection decoding, encoding, internal calls, and zero-caller overloads.
2. Remove config task parsing/execution, old generate sniffing, rejected legacy flags, unused marshaling, attach fallback, and no-op environment hooks.
3. Replace `Session.Routes` consumers with `RouteManifest.Routes`; simplify codegen; delete unused PostgreSQL fields, dashboard metadata, Astryx CLI dependency, and orphan bridge fixture.
4. Update AGENTS, SKILL, README, local contract, agent guide, cookbook, indexes, and schemas where affected.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/scenery`:

    gofmt -w <changed Go files>
    go test ./auth ./runtime ./internal/app ./internal/agent ./internal/codegen ./cmd/scenery
    go test -count=1 ./...
    go vet ./...
    go run ./cmd/scenery inspect docs -o json
    go run ./cmd/scenery harness self --summary -o json --write

Acceptance requires no config-defined shell-task path, reflection endpoint lane,
duplicate route map, obsolete dispatch/parser, or audited zero-caller shim. The
full self-harness must report no blocking error.

## Idempotence and Recovery

All edits are Git-tracked. Retry failed tests after fixing the owning slice.
Do not install a shared binary or commit `.scenery` output.

## Artifacts and Notes

Durable evidence is the final diff statistics, caller searches, focused and
full test output, documentation inspection, and self-harness summary.

## Interfaces and Dependencies

Reuse the existing edition-2027 native runtime, route manifest, `.scn` task
model, strict CLI parser, canonical auth configuration, and existing package
manager. Add no dependency, alias, fallback, or compatibility layer.
