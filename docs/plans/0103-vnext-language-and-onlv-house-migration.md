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
- [x] 2026-07-10 - Expanded the feature-complete draft from the kernel boundary across the claimed resolved profile set in all six supplied specifications; workflow execution remains an explicit unsupported-profile diagnostic as specified for unavailable profiles.
- [x] 2026-07-10 - Added the shared compatibility, revision, type-validation, HTTP-validation, graph/context, and agent-read foundations with focused tests.
- [x] 2026-07-10 - Replaced the compatibility leaf heuristic with directional, position-aware compatibility rules, deterministic migration/risk records, rename evidence, artifact consequences, and semantic-version recommendations.
- [x] 2026-07-10 - Strengthened mutation planning with repair plans, generated-artifact transaction edits, null contract-revision preconditions, immutable caller/capability binding, approval validation, replay refusal, staged verification, and symlink-safe rollback.
- [x] 2026-07-10 - Implemented generated application composition, exact HTTP codecs, durable/schedule/event/data/deployment/UI profiles, registry/provider locks, and source-local runtime integration.
- [x] 2026-07-10 - Migrated all eighteen ONLV House operations to native generated ABIs, retired every House compatibility adapter, and retained all forty-three non-House services as explicit legacy owners.
- [x] 2026-07-10 - Closed strict review findings for record validation in Go/TypeScript, exact integer bodies, forwarded-header trust, wrapped deployment inputs, finish operational gates, CLI schema nullability, and typed exit classification.
- [x] 2026-07-10 - Closed the second specification review findings with recursive authored-block schemas, UTF-8 TypeScript set ordering, compile-time streaming rejection, normative HTTP limits/outcomes, receipt-backed migration status, and the edition-defined glob matcher.
- [x] 2026-07-11 - Completed the then-current two-axis standards/specification review, thermo-nuclear maintainability review, and ponytail review/audit; later exact-SHA findings are tracked in plan 0104.
- [x] 2026-07-11 - Completed final full-suite, 34-schema self-harness, ONLV repository/app/browser validation, exact ownership verification, and a fresh detached `--wait ready` authenticated House proof.
- [x] 2026-07-11 - Re-audited the delivered revisions against the independent review and closed the residual arbitrary-precision scalar, artifact framing, path confinement, command-wide generation transaction, declared-target legacy lowering, static compatibility truth, generated-client server proof, and native-runtime ownership gaps.
- [x] 2026-07-11 - Repeated uncached Scenery/ONLV suites, fixture generation checks, the 14-case exact-codec suite plus a generated-client/generated-Go-server integration, both harnesses, browser-client checks, native durable inspection, and a fresh authenticated source-local runtime proof after the residual fixes.
- [x] 2026-07-11 - Closed the final independent review findings: stable public system errors, arbitrary-precision formatting, normative bridge gateway/namespace/config lifecycle and normalized paths, symbol-level hidden-builder detection, explicit House constructor config, durable external-name revision safety, and a real generated Go reference server.
- [x] 2026-07-11 - Recorded a corrected exact-SHA review that found seven later conformance gaps; this file remains the historical first-delivery record and plan 0104 owns the follow-up proof.

## Surprises & Discoveries

- The supplied language specification explicitly calls itself a design specification rather than current documentation. Section 26.2 defines a finite kernel spike and a migration-capable first product release, so conformance must be claimed profile-by-profile instead of pretending that durable, data, deployment, workflow, event, UI, registry, or agent-mutation profiles already exist.
- ONLV House exposes eighteen HTTP operations under one service lifecycle. The bridge's minimum activation unit is the entire service, so native ownership cannot cover only `/house/process` while leaving sibling House routes legacy-owned.
- The kernel implementation accepted arbitrary block contents, hashed only `.scn` source into `workspace_revision`, exposed no semantic diff/graph/agent server, and generated only a subset of Go and TypeScript types. The full-spec pass must replace those permissive shortcuts rather than merely advertise additional profile names.
- Public generated Go packages cannot import compiler-only HCL/cty dependencies without forcing client modules to acquire Scenery compiler sums. Cross-field rules therefore compile once into a deterministic data AST; the public Go and TypeScript runtimes evaluate that AST without dynamic code execution or compiler dependencies.
- Bridge completion is not implied by native source ownership. The finish plan must bind operational evidence and receipt state so v0 CLI consumers, legacy generated clients, stateful drains/cursors/owners, and still-open rollback ownership cannot disappear silently.
- Two House bindings carried an ignored `legacy_path` attribute. Recursive schemas exposed the silent no-op; the attributes were removed because canonical route segment positions already preserve the frozen external paths.
- Independent specification review found five wire-boundary defects that focused happy-path tests had missed: nominal same-status response matching, incomplete multipart enforcement, deny-all authorization misclassification, slash aliasing, and non-canonical set mappings. A second pass then found Fetch's inability to preserve repeated request-header lines, response/request compatibility leakage, and `std.authorization.none` still being rejected by compilation. Each now has a focused regression.
- A source-declared retired native service was incorrectly made dependent on machine-local activation receipts, and detached readiness shared the 30-second registration deadline. Real ONLV validation exposed both: retired House now remains ready after a clean clone, while `--wait ready` has a separate two-minute budget and preserves the real child PID in errors.
- The first complete self-harness found one schema drift: an empty manifest diagnostic set serialized as `null`. All manifest views now emit `[]`, and the repeated self-harness validated all 34 schema examples.
- Reopening the completed milestone against the reviewed revisions exposed defects that happy-path conformance did not: machine-width public scalar storage, ambiguous multi-file digest framing, per-family generation commits, host-context legacy parsing, and native packages that could still hide legacy runtime builders. Focused adversarial regressions now cover each boundary.
- The bridge schema had parsed `namespace` without using it, rejected its own normative `legacy_gateway` example, and could not continue after the shared config was removed. Lowering now derives addresses from the declared namespace, routes through the explicit default gateway, accepts omission only after the config disappears, and rejects non-normalized manifest paths.
- A package-import ban was too broad for native handlers that legitimately use non-registering durable/cron APIs. Hidden ownership detection now resolves only `durable.NewTask` and `cron.NewJob` symbols through `go/types`, including dot imports and constructor aliases.
- A hand-written Bun server was not proof of generated server compatibility. The build integration now starts the compiled generated Scenery Go application and runs its generated TypeScript client against that process.

## Decision Log

- Decision: Store the supplied documents under `docs/specs/vnext/` with the duplicate download suffix removed from `SCENERY_LANGUAGE_SPEC.md` and otherwise preserve exact bytes.
  Rationale: They are a linked normative set and are not current-user documentation; a dedicated versioned specification boundary keeps that status explicit.
  Date/Author: 2026-07-10 / Codex.
- Decision: Define “vNext fully done” for this migration as conformance to the section 26.2 kernel profiles plus `scenery.legacy-bridge/v1`, including the mandatory House proof and one generated TypeScript client.
  Rationale: The specifications explicitly exclude later profiles from the kernel and require unsupported features to be rejected rather than approximated.
  Date/Author: 2026-07-10 / Codex.
- Decision: Supersede the kernel-only completion boundary and require the complete six-document normative surface before this plan can finish.
  Rationale: The user explicitly rejected the earlier unilateral narrowing of “fully done.” Section 26.2 remains historical milestone context, not the acceptance boundary.
  Date/Author: 2026-07-10 / Codex.
- Decision: Activate House as one native service boundary while allowing its existing Go handler bodies to remain behind the bridge-owned `legacy_go_v0` adapter during the first contract migration.
  Rationale: The bridge expressly separates native contract migration from native Go ABI migration, while requiring exactly one active adapter and lifecycle per operation/service.
  Date/Author: 2026-07-10 / Codex.
- Decision: Retire every House `legacy_go_v0` service and handler adapter before this plan completes; only non-House ONLV services may remain legacy-owned.
  Rationale: The user explicitly required the complete specification plans rather than the earlier contract-only House milestone.
  Date/Author: 2026-07-10 / Codex.
- Decision: Compile named record-validation expressions into a canonical AST shared by generated Go and TypeScript rather than embedding HCL evaluation in application runtimes.
  Rationale: It preserves compiler type checking and runtime parity without `eval`, dynamic code, or transitive compiler dependencies in client modules.
  Date/Author: 2026-07-10 / Codex.
- Decision: Make bridge finish require explicit evidence keys derived from every non-stateless cutover class plus v0 CLI clearance, and bind the current activation/retirement receipt revision into plan/apply.
  Rationale: Source ownership alone cannot prove durable drains, schema/event handoff, deployed-client adoption, or closure of rollback authority.
  Date/Author: 2026-07-10 / Codex.
- Decision: Validate the authored block tree against revisioned nested schemas before flattening it into resource maps.
  Rationale: Label cardinality, duplicate singleton blocks, and the distinction between attributes and blocks are otherwise irrecoverably lost during lowering.
  Date/Author: 2026-07-10 / Codex.
- Decision: Reject Fetch-unrepresentable repeated collection request headers and same-status response mappings whose observable wire decoders cannot be proven disjoint.
  Rationale: The v1 profiles require exact wire behavior; silent header coalescing or nominal/source-order outcome selection would be approximation.
  Date/Author: 2026-07-11 / Codex.
- Decision: Treat the bridge's product-level release gates as conditional on advertising global no-flag-day migration or bridge retirement, neither of which this mixed-mode plan claims.
  Rationale: The requested acceptance state explicitly keeps forty-three ONLV services legacy-owned. House is fully retired native, while app-wide finish and global bridge-retirement evidence remain independently enforced.
  Date/Author: 2026-07-11 / Codex.
- Decision: Static legacy lowering may describe verified shape but must classify semantic equivalence and migration disposition as advisory until an exact frontend-and-codec behavioral fixture passes.
  Rationale: Similar graph fields are not executable parity evidence, and generated metadata must never manufacture a fixture-catalog digest.
  Date/Author: 2026-07-11 / Codex.
- Decision: A migration-owned native package may not hide undeclared legacy models, pages, or runtime builders; House background work is declared as native durable executions and dispatched through generated execution addresses. Explicit bridge-visible `legacy_go_v0` handlers remain permitted until retirement.
  Rationale: Hidden `durable.NewTask` registrations create a second lifecycle owner outside the canonical graph even when HTTP ownership is native.
  Date/Author: 2026-07-11 / Codex.
- Decision: One `scenery generate` invocation validates and commits every selected Go and TypeScript family as one confined artifact transaction.
  Rationale: A later family failure must not leave earlier generated roots updated, and all output roots must be explicitly workspace-managed before any write.
  Date/Author: 2026-07-11 / Codex.
- Decision: Make bridge identity and routing entirely manifest-driven: namespace controls lowered addresses, `legacy_gateway "default"` controls HTTP bindings, and `legacy_config` disappears only with the shared config itself.
  Rationale: Parsed-but-unused identity and a hard-coded gateway contradict the bounded bridge and cannot represent the normative lifecycle.
  Date/Author: 2026-07-11 / Codex.
- Decision: Preserve legacy durable task names through `external_name`, while treating `revision` as the persisted input ABI and requiring an explicit drain or migration on incompatible changes.
  Rationale: ONLV's process worker added a concurrency envelope; revision 2 plus a database-proven empty House queue prevents revision-1 rows from being decoded as the new shape.
  Date/Author: 2026-07-11 / Codex.

## Outcomes & Retrospective

Current-status note: this section records the first integrated delivery at commit `1dcba0536b307aa0411e8a26df865178881cc426`; it is not a stable-conformance claim. The corrected exact-SHA review and follow-up fixes live in `docs/plans/0104-edition-2027-conformance-hardening.md`.

Completed on 2026-07-11. The broad six-specification feature-complete draft and ONLV migration are present. All eighteen House HTTP handlers and both House background workers use generated native ABIs and execution addresses with no House legacy adapter or hidden runtime registration; the merged graph contains exactly forty-four services with House as the sole native owner and forty-three explicit legacy owners. `migrate verify house` reports contract, operational, readiness, and retirement true with zero remaining adapters. Generated Go, TypeScript, application, selection, and descriptor artifacts are byte-current.

Final validation passed uncached `go test ./...`, isolated `go test ./cmd/scenery`, Bun's 14-test exact-codec suite, the generated TypeScript client against the compiled generated Go server, generated-client TypeScript checking, docs inspection with zero missing/stale/index drift, and `scenery harness self --summary --write` with `ok=true`, 34/34 schemas, ten fixtures, Postgres/runtime probes, and warning-only timing/known architecture debt. ONLV passed source-local fmt/check/compile/migrate/generate checks, `go test ./house/...`, uncached `go test ./...`, `just repo-harness`, the app harness, and Next lint/Deno check/build. Durable inspection reports both House tasks from `house/scenery.package.scn` and no House Go runtime builder. The process task preserves `house.ProcessRoofmapnet/v1` at revision 2 with concurrency 1; a direct database query returned zero House durable jobs before the revision change. A fresh detached `scenery up --wait ready` returned `running`; an authenticated `POST /api/house/process` with `{}` returned HTTP 400, `application/problem+json`, `invalid_argument`, and the sanitized message `invalid request`. The verified detached runtime remains available.

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

Use House declarations generated from bounded legacy metadata as a proposal, check them into ONLV as explicit human-editable `.scn`, and activate the whole House service. Preserve existing House business implementations behind handwritten native ABI wrappers, not compatibility adapters. Generated contracts and application composition own transport and registration; all eighteen wrappers map declared outcomes to the existing business methods.

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

    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery check --app-root /Users/petrbrazdil/Repos/onlv -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery compile --app-root /Users/petrbrazdil/Repos/onlv --view expanded -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery migrate status --app-root /Users/petrbrazdil/Repos/onlv -o json
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery generate --app-root /Users/petrbrazdil/Repos/onlv --check
    go test ./house/...
    go test ./...
    just repo-harness
    go -C /Users/petrbrazdil/Repos/scenery run ./cmd/scenery harness --app-root /Users/petrbrazdil/Repos/onlv --json --write

Acceptance additionally requires a detached `scenery up --wait ready`, merged inspection showing House `active=native` and every other service `active=legacy`, no duplicate route/lifecycle owners, a generated TypeScript client descriptor matching its files, ordinary House unit tests against the generated contract, and a live authenticated `/house/process` response matching its declared error contract. The detached runtime remains available for the maintainer after verification.

## Idempotence and Recovery

Native compilation and check are read-only. Generation writes descriptor-covered sets atomically. House has completed native activation and retirement, so it has no shadow owner or rollback receipt; recovery now uses source control plus a new revisioned migration. Existing legacy-only services continue through the frozen bridge, and runtime validation rejects duplicate route or lifecycle owners.

## Artifacts and Notes

The original supplied spec checksums are recorded by Git history and were verified byte-for-byte after placement. Conformance fixtures, generated descriptors, migration receipts, and live harness output are the implementation evidence; generated cache under `.scenery/` is never committed.

## Interfaces and Dependencies

The implementation uses HashiCorp HCL v2 for syntax/token support while Scenery owns the edition schema, lossless-source guarantees, canonical graph, and formatter behavior. Existing `internal/parse`, `internal/model`, build preparation, router/runtime, and client generation are now fed by the canonical graph through explicit bridge integration points. ONLV House continues using its current database, storage, SQL, and native subprocess implementations; this migration changes declaration and adapter ownership, not business data or native algorithms.
