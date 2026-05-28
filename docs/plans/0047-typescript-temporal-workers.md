# TypeScript Temporal Workers

This ExecPlan is a living document. Update the Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective sections as work proceeds.

## Purpose / Big Picture

Add onlava-native TypeScript activity support for Temporal without requiring user-authored root worker entrypoints, registries, or manifests. The target user experience is that app authors put domain-local files such as `house/preview.worker.ts` next to Go orchestration code, declare matching `temporal.NewExternalActivity` values in Go, and let onlava generate the hidden TypeScript worker runtime under `.onlava/generated/temporal/typescript/`.

The first useful slice is intentionally activity-only. Go remains the orchestration language. TypeScript implements selected Temporal activities. onlava discovers, validates, generates, and can run the TypeScript worker with task-queue filtering.

## Progress

- [x] 2026-05-27: Created this ExecPlan and scoped the first slice to TS activity discovery, generated runtime files, Go external activity declarations, CLI generation/run support, and check/inspect validation surfaces.
- [x] 2026-05-27: Added `internal/workers` TypeScript discovery, validation, deterministic generated runtime files, v2 manifest generation, and task-queue selection validation.
- [x] 2026-05-27: Added `temporal.NewExternalActivity`, made `temporal.ExecuteActivity` accept Go and external activity handles, and taught Go parsing/codegen about external activity declarations.
- [x] 2026-05-27: Added `onlava worker typescript`, `onlava inspect temporal --json` TypeScript activity reporting, and `onlava check --json` Go-to-TS contract diagnostics.
- [x] 2026-05-27: Updated config/schema/docs for `temporal.typescript`, generated manifests under `.onlava/generated/temporal/typescript/`, and CLI grammar.
- [x] 2026-05-27: Validation passed: `go test ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.
- [x] 2026-05-27: Added `onlava dev` auto-start for TypeScript Temporal workers: dev rebuilds now validate contracts, regenerate the hidden worker runtime, start/restart the TS worker with supervised Temporal/session env, and stop it on rebuild disable or supervisor shutdown.
- [x] 2026-05-27: Auto-start validation passed: `go test ./cmd/onlava`, `go test ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`.

## Surprises & Discoveries

- 2026-05-27: Existing worker support validates external manifests under `.onlava/workers/*.json` and generates starter bindings, but it does not own a runnable TypeScript worker entrypoint. The new path should be additive and should write to `.onlava/generated/temporal/typescript/` instead of replacing legacy manifest validation immediately.
- 2026-05-27: `onlava check` uses the dev/watch file fingerprint cache. To avoid stale TS contract results, `*.worker.ts` now participates in watched-file fingerprints; explicit `//onlava:worker` files without the suffix are still a later hardening target.
- 2026-05-27: The self harness markdown checker interpreted the generic example `NewExternalActivity[I, O](...)` as a local link target. The docs now describe the API without that bracketed markdown shape.
- 2026-05-27: Dev rebuild already keeps the previous Go app running through failed compiles and swaps processes only after a successful build. The TypeScript worker now follows the same lifecycle: failed TS validation or generation leaves the previous worker running, while a successful rebuild stops the old worker before starting the new generated one.

## Decision Log

- Decision: Start with `*.worker.ts` discovery and optional `//onlava:worker`, not TypeScript workflows.
  Rationale: Activity-only support avoids workflow sandboxing and bundling concerns while still covering the Go orchestration plus TS implementation model.
  Date/Author: 2026-05-27 / Codex.
- Decision: Use a small Go source scanner for the initial TypeScript DSL rather than adding a TypeScript parser dependency.
  Rationale: onlava prefers minimal dependencies. The first DSL is intentionally tiny: exported `activity<I, O>({...}, handler)` declarations with literal `name` and `taskQueue` fields.
  Date/Author: 2026-05-27 / Codex.
- Decision: Keep generated TypeScript worker manifests visible to existing worker-manifest validation by also reading `.onlava/generated/temporal/typescript/manifest.json`.
  Rationale: `onlava inspect temporal --json` should report the generated manifest after generation instead of treating it as hidden runtime state.
  Date/Author: 2026-05-27 / Codex.
- Decision: Gate `onlava dev` TypeScript worker supervision on both `temporal.typescript.enabled` and `temporal.typescript.auto_start`, and only start a worker when at least one TS activity is discovered.
  Rationale: this makes the beta path explicit, avoids starting empty worker processes in apps that only carry config defaults, and lets contract validation still run for discovered TS worker files.
  Date/Author: 2026-05-27 / Codex.

## Outcomes & Retrospective

The first implementation slice plus dev auto-start is complete. onlava can now discover domain-local TypeScript activity declarations, generate a hidden TypeScript Temporal worker runtime and v2 manifest, validate Go external activity declarations against TypeScript declarations during `onlava check` and `onlava dev`, expose TypeScript activity state through `onlava inspect temporal --json`, generate/run the worker with `onlava worker typescript`, and supervise the generated worker during `onlava dev` when `temporal.typescript.auto_start` is enabled. The broader plan remains active for stronger TypeScript parsing, dashboard/status surfaces, and ONLV migration.

## Context and Orientation

Relevant files:

- `temporal/temporal.go` defines Go workflow/activity declarations and worker execution helpers.
- `internal/parse/parser.go` discovers Go runtime declarations such as `temporal.NewWorkflow` and `temporal.NewActivity`.
- `internal/model/model.go` stores app model runtime declarations.
- `internal/workers/manifest.go` validates v1 and v2 external worker manifests.
- `internal/workers/bindings.go` currently generates stub bindings from manually authored manifests.
- `cmd/onlava/worker.go` owns `onlava worker` and `onlava worker bindings`.
- `cmd/onlava/check.go` and `cmd/onlava/inspect.go` produce JSON validation and Temporal inspection surfaces.

Generated TypeScript worker artifacts should live under `.onlava/generated/temporal/typescript/` and should not be committed.

## Milestones

1. Add TypeScript worker discovery and validation for `*.worker.ts` plus optional `//onlava:worker`.
2. Generate `onlava.ts`, `registry.ts`, `worker.ts`, `manifest.json`, and `tsconfig.json`.
3. Add `temporal.NewExternalActivity[I, O]` and make `temporal.ExecuteActivity` accept both Go and external activities.
4. Teach Go parsing, inspect, and check paths about external activities and TS contract mismatches.
5. Add `onlava worker typescript [--task-queue <name>]` to generate and run the hidden worker runtime through `bun` or `node`.
6. Add `onlava dev` supervision for the generated TypeScript worker when `temporal.typescript.auto_start` is enabled.
7. Later: add dashboard/status surfaces, stronger type-name matching, and ONLV app migration.

## Plan of Work

Keep the change additive and testable. The scanner should return precise diagnostics with file and line when it cannot extract required literal fields. Generated files should be deterministic so golden-style tests can assert registry imports and manifest hashes. Go external activity declarations should not register Go worker handlers, because their task queues are polled by the generated TypeScript worker.

`onlava check --json` should fail before runtime when a Go external activity has no matching TypeScript activity, the task queues differ, a TS activity name is duplicated, a name is not versioned with `/vN`, or the generated payload codec would not be `onlava-json-v1`.

## Concrete Steps

1. Add an internal TypeScript worker package with discovery, validation, manifest hashing, and generation helpers.
2. Add tests for discovery, duplicate detection, generated registry/worker/manifest files, and Go-to-TS validation.
3. Add `ExternalActivity` to `temporal/temporal.go` and update `ExecuteActivity` to accept a generic activity handle.
4. Extend parser runtime declaration kinds and task-queue validation for `temporal.NewExternalActivity`.
5. Wire `onlava worker typescript` into CLI parsing and help text.
6. Wire TypeScript contract validation into `onlava check --json` and `onlava inspect temporal --json`.
7. Wire TypeScript contract validation, generation, start/restart, output capture, and shutdown into `onlava dev`.
8. Run focused tests, then `go test ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write` when practical.

## Validation and Acceptance

The first slice is accepted when:

- A fixture app with `house/preview.worker.ts` generates `.onlava/generated/temporal/typescript/registry.ts`, `worker.ts`, `manifest.json`, `onlava.ts`, and `tsconfig.json`.
- `manifest.json` uses `onlava.worker.manifest.v2`, `language: "typescript"`, and `payload_codec: "onlava-json-v1"`.
- `onlava worker typescript --task-queue <queue>` validates the selected queue before starting.
- `onlava dev` with `temporal.typescript.enabled` and `temporal.typescript.auto_start` validates, generates, and supervises the TypeScript worker with the same Temporal/session environment used by the Go app.
- A Go declaration made with `temporal.NewExternalActivity` can be used with `temporal.ExecuteActivity`.
- `onlava check --json` reports a clear diagnostic when the matching TS activity is missing or on the wrong queue.

Per-change validation:

```text
go test ./temporal ./internal/parse ./internal/workers ./cmd/onlava
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

## Idempotence and Recovery

Generated TypeScript files are overwritten deterministically from source declarations. If generation fails halfway, rerun `onlava worker typescript` after fixing diagnostics. The generator should create directories with `0755` and write files with `0644`. It must not remove user source files or legacy `.onlava/workers/*.json` manifests.

## Artifacts and Notes

Expected generated directory:

```text
.onlava/generated/temporal/typescript/
  manifest.json
  onlava.ts
  registry.ts
  tsconfig.json
  worker.ts
```

The legacy manual worker manifest path `.onlava/workers/*.json` remains valid during this transition.

## Interfaces and Dependencies

No new Go module dependency should be added for the first scanner/generator slice. Runtime execution of generated TypeScript workers expects the app to provide a JavaScript runtime (`bun` preferred, `node` fallback) and Temporal TypeScript SDK packages such as `@temporalio/worker` and `@temporalio/activity` in the app package setup.
