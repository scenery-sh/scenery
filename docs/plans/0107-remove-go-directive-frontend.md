# Remove the Go-Directive Frontend

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current throughout
implementation.

## Purpose / Big Picture

Remove the unsupported v0 Go-source frontend. Scenery applications must define
services, operations, bindings, models, pages, durable work, and schedules in
edition-2027 `.scn` source. Comments such as `//scenery:api` and
`//scenery:service` no longer influence compilation, generation, inspection, or
runtime registration, and the mixed legacy bridge is no longer a supported
profile.

This is a deletion, not a deprecation cycle. `.scenery.json` remains the
application runtime configuration file, `--json` remains the independent
`scenery.cli.v0` wire encoding where documented, and non-registering public
runtime APIs remain. Those contracts do not parse Go directives.

## Progress

- [x] 2026-07-12 - Re-checked repository instructions, documentation status, active plans, and every directive producer and consumer.
- [x] 2026-07-12 - Removed directive parsing, legacy IR, code generation, and package-init ownership.
- [x] 2026-07-12 - Removed the mixed legacy bridge/profile, migration CLI, schemas, fixtures, and generated artifacts.
- [x] 2026-07-12 - Synchronized public docs, specifications, indexes, and client-app guidance around native-only edition 2027.
- [x] 2026-07-12 - Passed focused, full-suite, generation, docs, and self-harness validation.

## Surprises & Discoveries

- ONLV currently has 42 native services and zero legacy operation adapters, but still carries a native-only `scenery.migration.scn` shell and the legacy-bridge profile.
- The Go package loader under `internal/parse` is also used for edition-2027 constructor and handler type checking. Keep that analysis boundary while deleting directive interpretation.
- Standard auth registers its runtime endpoints directly; its directive comments are redundant and can be removed without replacing registration behavior.
- The House fixture is intentionally contract-only, so Go implementation validation must only run when the fixture advertises the implementation profile.
- Native gateway exposure controls runtime router ownership independently of authorization. The converted storage fixture needed an `internet` gateway to preserve its former public probe, and its `int64` response uses the edition-2027 canonical JSON string representation.

## Decision Log

- Decision: Interpret v0 removal as removal of the Go-source application frontend, including directives, model/page static DSL, legacy client/code generators, package-init durable and cron ownership, and the mixed legacy bridge.
  Rationale: These surfaces form one discovery and compatibility path; retaining disconnected pieces would leave unsupported dead behavior.
  Date/Author: 2026-07-12 / user and Codex.
- Decision: Retain `.scenery.json`, current CLI JSON response selectors, and non-registering runtime APIs.
  Rationale: They are current configuration, protocol, and runtime contracts independent of directive parsing.
  Date/Author: 2026-07-12 / Codex.
- Decision: Keep `golang.org/x/tools/go/packages` inside `internal/parse` as a package-analysis utility for edition-2027 Go ABI verification.
  Rationale: Native constructors and handlers still need static type checking; a second analysis abstraction would add code without replacing a legacy contract.
  Date/Author: 2026-07-12 / Codex.

## Outcomes & Retrospective

Scenery now has one application frontend: edition-2027 `.scn` source. The Go
directive parser and IR, directive generators, public model/page DSLs,
package-init durable/cron ownership, mixed legacy bridge, migration CLI,
obsolete schemas, and legacy fixtures are deleted. Build, run, inspect,
generate, task, worker, and test flows no longer fall back to Go directives.

Current documentation and normative specifications now describe the singular
native model. Basic and storage harness fixtures use generated native
composition, and generation is byte-stable. The uncached Go suite, vet,
TypeScript conformance/typecheck, documentation inspection, schema validation,
fixture matrix, dashboard build, runtime probes, and full self-harness all
passed. Self-harness retained advisory pre-existing review, file-size, and test
timing warnings but reported no errors and `can_proceed: true`.

## Context and Orientation

Directive parsing and Go analysis are currently combined in
`internal/parse/parser.go`, with directive-owned IR in `internal/model`.
`internal/codegen`, `internal/clientgen`, `internal/generateddata`,
`internal/schemagen`, and `internal/webgen` consume that IR. Top-level build,
check, generate, inspect, and dev commands call the frontend for applications
without `scenery.scn`.

The mixed compatibility path lives in `internal/vnext/legacy_*`,
`internal/vnext/migration*`, the `scenery.legacy-bridge/v1` profile, and the
`scenery migrate` CLI. Edition-2027 native Go ABI verification also imports
`internal/parse`, but it only needs loaded packages, syntax, and type
information.

## Milestones

1. Reduce `internal/parse` to edition-2027 Go package analysis and reject app execution without `scenery.scn`.
2. Delete directive-owned IR, generators, fixtures, and public model/page or package-init registration surfaces.
3. Delete mixed bridge compilation, migration commands, schemas, fixtures, and profile advertisement.
4. Update all current contracts and prove native-only build, runtime, client generation, inspection, and harness behavior.

## Plan of Work

Start at the shared parser seam. Preserve package loading, overlay/build-target
handling, and native ABI checks, then remove directive dispatch and every
consumer made unreachable. Make command routing require `scenery.scn` before
legacy build/generate paths can run. Remove mixed-mode compiler branches and
the migration CLI rather than returning compatibility placeholders.

After code compiles, remove obsolete fixtures and rewrite only tests that still
prove current native behavior. Historical ExecPlans remain historical; current
docs and indexes must describe the native-only contract and mark retired
documents clearly or remove them from the current normative set.

## Concrete Steps

1. Split package analysis from directive interpretation and delete all directive parsing functions and directive-owned model types.
2. Remove v0 endpoint/service/auth/middleware/model/page code generation, TypeScript generation, inspection metadata, schema/seed/web generation, and public static DSL packages.
3. Remove package-init durable task and cron job registration while retaining non-registering runtime APIs.
4. Require edition-2027 source in build, check, generate, inspect, dev, worker, task, and test flows that previously discovered Go directives.
5. Remove the legacy bridge profile, migration source schema/compiler linking, migration commands, diagnostics, JSON schemas, fixtures, and generated bridge artifacts.
6. Remove directive comments from standard auth and all current fixtures; replace remaining coverage with native `.scn` fixtures where it tests supported behavior.
7. Synchronize `AGENTS.md`, `SKILL.md`, README, DSL/current contracts, cookbook, specifications, knowledge index, and plan indexes.

## Validation and Acceptance

From `/Users/petrbrazdil/Repos/scenery`:

    rg -n '//scenery:' --glob '*.go' .
    go test ./internal/parse ./internal/vnext ./internal/build ./cmd/scenery
    go test -count=1 ./...
    go vet ./...
    bun test internal/vnext/testdata/typescript_client_conformance.test.ts
    apps/consolenext/node_modules/.bin/tsc -p internal/vnext/testdata/tsconfig.generated-clients.json
    go run ./cmd/scenery generate --app-root internal/vnext/testdata/native --check -o json
    go run ./cmd/scenery inspect docs --json
    go run ./cmd/scenery harness self --summary --write

Acceptance requires zero active directive parser or registration path, no
supported mixed legacy profile/CLI, native fixture generation without drift,
and an explicit unsupported error for an app that lacks `scenery.scn`. The
full suite and self-harness must pass without gated tests or configuration
workarounds.

## Idempotence and Recovery

Deletion proceeds in compileable slices. `git diff` is the recovery source;
generated `.scenery/` state remains untracked. Do not install a shared Scenery
binary. If a deleted legacy package still has a current caller, restore only
the minimum analysis/runtime primitive needed by native edition 2027, not the
directive frontend.

## Artifacts and Notes

The primary evidence is the final tracked-source search, native fixture
generation, full test/vet output, docs inspection, and self-harness summary.
ONLV cleanup is a downstream integration follow-up unless it is required to
prove the final native-only Scenery contract during this plan.

## Interfaces and Dependencies

Reuse the existing edition-2027 compiler, generated application composition,
Go package analysis, runtime registry, and TypeScript generator. Add no new
dependency, environment variable, compatibility spelling, fallback parser, or
translation layer.
