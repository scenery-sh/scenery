# Remove Former Realtime Service and Reserve Entity Stream

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Remove the former managed realtime service as a supported Scenery feature.

After this plan, Scenery must reject the removed service kind and removed service-name shorthand, must not start or proxy that service, must not inject its environment variables, and must not generate frontend contracts with service-specific names or runtime options.

The future replacement is a Scenery-native entity stream on reserved `/sync`, but this plan does not implement that stream.

## Progress

* [x] 2026-06-27: Created this ExecPlan and linked it from `docs/plans/active.md`.
* [x] 2026-06-27: Ran the initial removed-name audit with `rg`.
* [x] 2026-06-27: Removed managed runtime startup, routing, env injection, and tests for the former service.
* [x] 2026-06-27: Removed the former service from config validation, schema, current docs, env policy, fixtures, and UI labels.
* [x] 2026-06-27: Renamed generated web contracts away from service-specific vocabulary while preserving row-source materializers.
* [x] 2026-06-27: Ran focused and full validation.
* [x] 2026-06-27: Updated Outcomes & Retrospective and proved the final audit.

## Surprises & Discoveries

`go run ./cmd/scenery inspect docs --json` reported `review_due_count: 0`, `stale_count: 0`, and one AGENTS scope, so there is no doc-gardening prerequisite before this removal.

The initial audit command targeted the removed service name, managed runtime symbols, environment variables, and generated web shape/runtime vocabulary.

It showed the former service in the dev supervisor, dev service planner, session route registration, frontend env injection, generated web code, config schema, env policy, dashboard service labels, parser/root tests, self-harness parallel proof, fixtures, README, SKILL, and current docs.

## Decision Log

Decision: the former realtime service is removed with no compatibility mode.

Rationale: The requested end state is no removed product name in the codebase. Keeping compatibility would preserve exactly the surface being removed.

Date/Author: 2026-06-27 / Codex.

Decision: `/sync` stays reserved, but no websocket API is added.

Rationale: The router already reserves `/sync` from frontend fallback. A placeholder API would be misleading and is not needed to remove the former service.

Date/Author: 2026-06-27 / Codex.

Decision: Generated web code keeps source-row materializers but renames former realtime shape concepts to neutral source metadata.

Rationale: Existing generated pages can still render from explicit row sources without a live sync engine. The future stream can feed that boundary later.

Date/Author: 2026-06-27 / Codex.

## Outcomes & Retrospective

Completed on 2026-06-27.

Scenery no longer supports the former managed realtime service in dev service config, managed process startup, agent route registration, app/frontend env injection, generated web runtime options, current docs, schemas, env policy, UI labels, or fixtures.

The removed config value still fails closed by assembling the removed token in validation code, so legacy configs produce a clear deletion diagnostic without keeping that token in repository text.

`/sync` remains reserved from frontend fallback for a future Scenery-native entity stream, but this plan intentionally added no stream API.

Validation passed:

* `go test ./cmd/scenery ./internal/app ./internal/agent ./internal/envpolicy ./internal/webgen ./internal/clientgen`
* `go test ./...`
* `go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json`
* `go run ./cmd/scenery inspect docs --json`
* `cd ui && bun run typecheck`
* `cd ui && bun run test`
* `cd ui && bun run build`
* `go run ./cmd/scenery harness self --summary --write`

Self-harness passed with warning-class findings only: existing architecture size warnings and slow-test timing warnings.

## Context and Orientation

Repo rules live in `AGENTS.md`. The root instruction chain says to read `PLANS.md`, run `scenery inspect docs --json` before non-trivial work, keep docs and code synchronized when behavior changes, and validate with Go tests plus UI checks where relevant.

Primary runtime files:

* `cmd/scenery/dev_services.go` contains managed Postgres plus the old managed realtime planner/startup path.
* `cmd/scenery/dev_supervisor.go` starts managed services and builds app env.
* `cmd/scenery/dev_build_pipeline.go` runs build-time service preparation.
* `cmd/scenery/dev_session_controller.go` registers agent session backends.
* `cmd/scenery/dev_frontends.go` builds managed frontend env.

Primary generated-web files:

* `internal/webgen/webgen.go`
* `internal/webgen/webgen_test.go`
* fixture entrypoints under `testdata/apps/*/web/src/generated-entry.ts`

Primary contract files:

* `internal/app/root.go`
* `docs/schemas/scenery.config.v1.schema.json`
* `docs/local-contract.md`
* `docs/environment.md`
* `docs/environment.registry.json`
* `docs/agent-guide.md`
* `docs/app-development-cookbook.md`
* `README.md`
* `SKILL.md`
* `internal/envpolicy/scan.go`

## Milestones

M1 removes runtime startup and session registration for the former service.

M2 removes former-service env injection for apps and managed frontends.

M3 removes former-service support from config validation, schemas, current docs, env policy, fixtures, and UI labels.

M4 renames former realtime shape/runtime vocabulary to neutral source metadata.

M5 validates and closes with a final audit.

## Plan of Work

Delete the managed former-service code instead of hiding it behind flags. Add explicit fail-closed config validation for both the removed service kind and the removed service-name shorthand, with the removed token assembled in code so it does not remain in the repository text.

Keep Postgres, ZeroFS, legacy async runtime, Victoria, Grafana, storage, generated row types, generated projections, materializers, and `/sync` reservation intact.

Historical completed ExecPlans may keep old service-history wording unless the final acceptance check requires all plan history to be rewritten. Current contracts, code, fixtures, tests, and generated outputs must not present the former service as supported.

## Concrete Steps

1. Remove managed former-service constants, types, startup, env injection, backend registration, process cleanup, and slot cleanup.
2. Update config validation and tests so removed declarations fail clearly.
3. Update generated web output and fixture entrypoints to use `entitySources` and source metadata instead of former shape/runtime options.
4. Update current docs, schema, env registry, env policy, dashboard UI, and fixtures.
5. Run focused tests, UI checks, generation checks, full tests, docs inspection, self-harness when practical, and `git diff --check`.
6. Run a final audit for the removed product token in repository text and filenames.

## Validation and Acceptance

Run from the repository root:

```sh
go test ./cmd/scenery ./internal/app ./internal/agent ./internal/envpolicy ./internal/webgen ./internal/clientgen
go test ./...
go run ./cmd/scenery generate data --app-root testdata/apps/model-dsl --dry-run --json
go run ./cmd/scenery inspect docs --json
go run ./cmd/scenery harness self --summary --write
git diff --check
```

Because this touches dashboard UI, also run:

```sh
cd ui
bun run typecheck
bun run test
bun run build
```

Final audit:

```sh
rg -n -i "<removed service token>" .
find . -iname "*<removed service token>*" -print
```

Acceptance criteria:

* The removed `dev.services.*.kind` value fails with a clear removed diagnostic.
* A service named with the removed token and empty kind fails.
* `scenery up` has no former-service startup phase.
* Agent sessions no longer register a former-service backend.
* App processes and managed frontends no longer receive former-service env vars.
* Generated web packages contain no former-service vocabulary, `shapeURL`, or required former-service runtime config.
* Current docs, config schema, env registry, dashboard UI, and fixtures no longer present the former service as supported.
* `/sync` remains reserved/protected, without an implemented stream API.

## Idempotence and Recovery

This is a source deletion/refactor. If a step fails, rerun the focused package tests first to catch stale symbol references, then patch the remaining callers.

Generated `.scenery/` output from validation is cache and must not be committed.

## Artifacts and Notes

Possible follow-up plan: `0088-entity-websocket-stream.md`, if and when the Scenery-native entity stream contract is designed.

Do not add placeholder public APIs in this plan.

## Interfaces and Dependencies

Removed interfaces:

* `dev.services` former-service kind and service-name shorthand.
* Managed former-service binary/container/upstream startup.
* Agent session route/backend named for the former service.
* Former-service app, frontend, dev-upstream, port, replication, and usage-reporting environment variables.
* Generated former-service shape definitions, shape registries, runtime config, and runtime options.

Preserved interfaces:

* Managed Postgres and Postgres branch database env selection.
* ZeroFS/storage.
* legacy async runtime, Victoria, and Grafana.
* Generated row types, page projection records, materializers, route/page helpers.
* `/sync` as a reserved non-frontend path.
