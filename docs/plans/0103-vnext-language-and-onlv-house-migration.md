# Scenery vNext Language and ONLV House Migration

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`, `Decision Log`, and `Outcomes & Retrospective` current throughout implementation.

## Purpose / Big Picture

Implement the migration-capable first release of Scenery language edition 2027, preserve the complete normative design set in this repository, and prove the transition in the real ONLV application. ONLV House becomes the first native service while every other ONLV service remains explicitly legacy-owned. One compiler graph and one runtime plan must continue serving the application without route or lifecycle duplication.

## Progress

- [x] 2026-07-10 - Read the six supplied specifications and identified the section 26 kernel plus legacy bridge as the first-release boundary.
- [x] 2026-07-10 - Preserved byte-identical specification files under `docs/specs/vnext/`.
- [x] 2026-07-10 - Implemented the edition-2027 compiler, canonical graph, diagnostics, inspection, Go contract generation, HTTP runtime projection, TypeScript client, and bridge ownership linker.
- [x] 2026-07-10 - Added mixed-mode source to ONLV, made House native, and inventoried every other service as legacy.
- [x] 2026-07-10 - Verified Scenery conformance, deterministic generation, ONLV tests, merged inspection, and live House HTTP behavior.

## Surprises & Discoveries

- The supplied language specification explicitly calls itself a design specification rather than current documentation. Section 26.2 defines a finite kernel spike and a migration-capable first product release, so conformance must be claimed profile-by-profile instead of pretending that durable, data, deployment, workflow, event, UI, registry, or agent-mutation profiles already exist.
- ONLV House exposes eighteen HTTP operations under one service lifecycle. The bridge's minimum activation unit is the entire service, so native ownership cannot cover only `/house/process` while leaving sibling House routes legacy-owned.

## Decision Log

- Decision: Store the supplied documents under `docs/specs/vnext/` with the duplicate download suffix removed from `SCENERY_LANGUAGE_SPEC.md` and otherwise preserve exact bytes.
  Rationale: They are a linked normative set and are not current-user documentation; a dedicated versioned specification boundary keeps that status explicit.
  Date/Author: 2026-07-10 / Codex.
- Decision: Define “vNext fully done” for this migration as conformance to the section 26.2 kernel profiles plus `scenery.legacy-bridge/v1`, including the mandatory House proof and one generated TypeScript client.
  Rationale: The specifications explicitly exclude later profiles from the kernel and require unsupported features to be rejected rather than approximated.
  Date/Author: 2026-07-10 / Codex.
- Decision: Activate House as one native service boundary while allowing its existing Go handler bodies to remain behind the bridge-owned `legacy_go_v0` adapter during the first contract migration.
  Rationale: The bridge expressly separates native contract migration from native Go ABI migration, while requiring exactly one active adapter and lifecycle per operation/service.
  Date/Author: 2026-07-10 / Codex.

## Outcomes & Retrospective

The edition-2027 first-release profile set is implemented behind explicit `scenery.scn` discovery. Scenery now compiles deterministic canonical manifests, reports structured diagnostics and revisions, exposes the vNext CLI inspection and migration protocol, generates Go contracts and a versioned TypeScript client, and links an explicit legacy inventory into one ownership graph.

ONLV is the first mixed-mode application: House owns all eighteen route keys natively, `ProcessSceneVNext` implements the generated Go ABI, and the other seventeen House operations retain explicit `legacy_go_v0` handler adapters. All forty-three non-House services remain explicitly legacy-owned. `scenery migrate verify house` therefore reports `contract_ready: true` and `retirement_ready: false`; that distinction is intentional and prevents contract activation from being mistaken for complete handler-adapter retirement.

Validation passed across both repositories: Scenery `go test ./...`, docs inspection, schema validation, and self-harness; ONLV `go test ./...`, repo harness, generated-artifact check, frontend lint/type checking, and app harness. A fresh detached ONLV runtime reached `running`; authenticated `GET /api/house/scenes` returned HTTP 200, and authenticated invalid `POST /api/house/process` returned the declared HTTP 400 argument error. The native handler's declared invalid-input outcome is also covered directly by `house/vnext_test.go` without requiring an external scene fixture.

## Context and Orientation

Current Scenery declarations are discovered by `internal/parse` from `.scenery.json`, `//scenery:*` directives, Go types/tags, and runtime builders. `cmd/scenery/check.go`, `cmd/scenery/inspect.go`, `cmd/scenery/generate.go`, and the build/runtime preparation packages consume that model. The new edition compiler belongs in a distinct `internal/vnext` boundary and must feed the same downstream runtime plan rather than start a second server.

ONLV lives at `/Users/petrbrazdil/Repos/onlv`. Its app root is `.scenery.json`; House is under `house/`, and generated browser clients currently live under app-configured generator outputs. ONLV requires its own ExecPlan under `docs/agent/exec-plans/` for the app-side migration.

## Milestones

1. Preserve and index the normative specifications and establish compiler/profile metadata.
2. Parse and validate native root/package source into deterministic canonical resources with stable revisions, diagnostics, and source maps.
3. Lower bounded legacy services, link explicit ownership, and project the merged graph through current and vNext inspection commands.
4. Generate committed Go contract packages/adapters and the v1 TypeScript client, and make the HTTP runtime consume the linked plan.
5. Migrate all House operations as one native service in ONLV, list every other service explicitly as legacy, and prove live behavior.

## Plan of Work

Build the new compiler behind explicit `scenery.scn` discovery so legacy-only applications remain unchanged. Keep the canonical graph independent from current `internal/model`, then provide one deliberate projection into the existing build/runtime boundary while runtime internals are incrementally made graph-native. Mixed mode reads only paths named by `scenery.migration.scn`; it rejects ambiguous roots, duplicate owners, route conflicts, and unlisted services.

Use House declarations generated from bounded legacy metadata as a proposal, check them into ONLV as explicit human-editable `.scn`, and activate the whole House service. Preserve existing House Go business code through generated compatibility adapters first. Generate native Go contract types and unit-test one native ABI path without starting Scenery, satisfying the end-to-end spike while leaving no ambiguity about the adapter still in use by other House handlers.

## Concrete Steps

1. Add profile/schema constants, CST parsing, root/package loading, typed references, core types/resources, canonical ordering/encoding, revisions, source maps, and structured diagnostics under `internal/vnext`.
2. Add `fmt`, `compile`, `schema`, `list`, `get`, and `explain` CLI paths using `-o` protocol selection without regressing current `--json` compatibility.
3. Add the bounded legacy manifest parser, lower current model metadata into canonical resources, enforce explicit service ownership, and implement `migrate init/status/service/compare/activate/verify/finish` planning surfaces needed by the House transition.
4. Generate `scenerycontract`, application adapters, descriptors, and v1 TypeScript client artifacts deterministically; verify stale artifacts through overlay checks.
5. Feed active HTTP routes and service lifecycle ownership into one runtime plan, retaining bridge adapters only where declared.
6. Add ONLV root/package/migration declarations, generated House contracts, an app-side ExecPlan, and explicit legacy inventory. Remove House legacy ownership directives only when the native candidate owns every House route and lifecycle key.
7. Run focused tests after each layer, then full Scenery and ONLV validation and a live authenticated House request.

## Validation and Acceptance

From `/Users/petrbrazdil/Repos/scenery`:

    go test ./internal/vnext/...
    go test ./cmd/scenery
    go test ./...
    go run ./cmd/scenery inspect docs --json
    go run ./cmd/scenery harness self --summary --write

From `/Users/petrbrazdil/Repos/onlv` using the worktree-local Scenery source:

    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery check --app-root . -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery compile --app-root . --view expanded -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery migrate status --app-root . -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery generate --app-root . --check
    go test ./house/...
    go test ./...
    just repo-harness
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery harness --app-root . --json --write

Acceptance additionally requires a detached `scenery up --wait ready`, merged inspection showing House `active=native` and every other service `active=legacy`, no duplicate route/lifecycle owners, a generated TypeScript client descriptor matching its files, ordinary House unit tests against the generated contract, and a live authenticated `/house/process` response matching its declared error contract. The detached runtime remains available for the maintainer after verification.

## Idempotence and Recovery

Native compilation and check are read-only. Generation writes descriptor-covered sets atomically. Migration activation uses an explicit source ownership edit and can be reverted only while its receipt reports rollback-safe. Existing legacy-only apps continue through their current frontend until `scenery.scn` is present. If ONLV activation fails, leave House shadowed with legacy active; do not run two route tables or partially activate operations.

## Artifacts and Notes

The original supplied spec checksums are recorded by Git history and were verified byte-for-byte after placement. Conformance fixtures, generated descriptors, migration receipts, and live harness output are the implementation evidence; generated cache under `.scenery/` is never committed.

## Interfaces and Dependencies

The implementation may add HashiCorp HCL v2 for syntax/token support, but Scenery owns the edition schema, lossless-source guarantees, canonical graph, and formatter behavior. Existing `internal/parse`, `internal/model`, build preparation, router/runtime, and client generation remain the bridge integration points until the canonical graph becomes their direct input. ONLV House continues using its current database, storage, SQL, and native subprocess implementations; this migration changes declaration and adapter ownership, not business data or native algorithms.
