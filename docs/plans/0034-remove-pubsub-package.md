# Remove Pub/Sub Package

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Remove `scenery.sh/pubsub` as a public package and as a Scenery runtime concept. Apps that need background execution should use `scenery.sh/legacy-async-runtime` workflow and activity declarations directly.

The repository just moved Pub/Sub onto legacy async runtime as a compatibility layer. The new direction is stricter: there are no app dependencies that must preserve the Pub/Sub API, so scenery should delete the compatibility package instead of keeping a second async programming model. This reduces parser/codegen/runtime surface area, removes Pub/Sub dashboard/admin/docs affordances, and makes legacy async runtime the single background execution interface.

The companion app repo `/Users/petrbrazdil/Repos/onlv` currently imports `scenery.sh/pubsub` for async work. That repo must be migrated to native legacy async runtime workflows and activities before the package removal is considered complete.

## Progress

* [x] 2026-05-25: Create this ExecPlan and link it from `docs/plans/active.md`.
* [x] 2026-05-25: Remove Pub/Sub declarations and runtime hooks from parser, model, codegen, runtime, dashboard, admin, schemas, docs, and tests.
* [x] 2026-05-25: Add legacy async runtime service-method activity support so service-struct handlers can replace `pubsub.MethodHandler`.
* [x] 2026-05-25: Migrate `/Users/petrbrazdil/Repos/onlv` async jobs from `pubsub.NewTopic`/`NewSubscription` to native legacy async runtime declarations.
* [x] 2026-05-25: Add `legacy-async-runtime.ActivityConfig.MaxConcurrency` so dedicated legacy async runtime task queues can preserve former subscription concurrency caps without retaining Pub/Sub.
* [x] 2026-05-25: Validate scenery with `go test ./...`, UI typecheck/build, `go install ./cmd/scenery`, and `scenery harness self --json --write`.
* [x] 2026-05-25: Validate ONLV where practical: `go test ./codexsvc ./jobs ./maps` passed, while `go test ./house` and `scenery check --json` are blocked by the pre-existing native roofmapnet dependency error `fatal error: 'torch/torch.h' file not found`.

## Surprises & Discoveries

* `onlv` uses Pub/Sub in `codexsvc/exec_async.go`, `jobs/submissions_async.go`, `house/process_async.go`, and `maps/earth_async.go`.
* The Pub/Sub package currently owns the only generated service accessor bridge for background service methods. Removing the package requires moving that helper into `scenery.sh/legacy-async-runtime`.
* ONLV's house package cannot be fully validated in this environment without the native torch headers used by roofmapnet. The failure occurs before the migrated Go async code is exercised.
* A previous Grafana dev-server validation left ignored generated files under `testdata/apps/basic/.scenery/grafana`, which made the self-harness architecture check see vendored Grafana source as repository source. Removing that generated cache restored the harness to green.

## Decision Log

* Decision: Replace Pub/Sub usage with native legacy async runtime workflow/activity declarations, not with a renamed Pub/Sub compatibility API.
  Rationale: The user explicitly asked to remove Pub/Sub completely. Keeping a compatibility layer would preserve the old mental model under a different name.
  Date/Author: 2026-05-25 / Codex

* Decision: Move service-method activity accessors into the `legacy-async-runtime` package.
  Rationale: ONLV has service-struct background handlers. Native legacy async runtime activities need a way to resolve generated service instances without reintroducing Pub/Sub.
  Date/Author: 2026-05-25 / Codex

* Decision: Add `ActivityConfig.MaxConcurrency` instead of reintroducing a Pub/Sub-specific `MaxConcurrency` concept.
  Rationale: legacy async runtime worker concurrency is a task-queue worker option. ONLV uses dedicated task queues for the migrated heavy jobs, so this preserves the operational cap while keeping the public async interface legacy async runtime-only.
  Date/Author: 2026-05-25 / Codex

## Outcomes & Retrospective

Completed for source migration. scenery no longer exports `scenery.sh/pubsub`, the active docs/dashboard/admin/schema surfaces no longer mention Pub/Sub, and ONLV async flows now declare native legacy async runtime workflows and activities with dedicated task queues and preserved concurrency caps. Remaining validation risk is environmental in ONLV's native house dependencies, not in the scenery Pub/Sub removal itself.

## Context and Orientation

Relevant scenery files:

```text
pubsub/
legacy-async-runtime/legacy-async-runtime.go
runtime/pubsub.go
runtime/app.go
runtime/server.go
runtime/devreport.go
internal/model/model.go
internal/parse/parser.go
internal/codegen/generator.go
cmd/scenery/admin.go
cmd/scenery/dashboard.go
cmd/scenery/inspect.go
docs/local-contract.md
docs/app-development-cookbook.md
SKILL.md
AGENTS.md
README.md
ui/src/router.tsx
ui/src/routes/pubsub.tsx
```

Relevant ONLV files:

```text
/Users/petrbrazdil/Repos/onlv/codexsvc/exec_async.go
/Users/petrbrazdil/Repos/onlv/jobs/submissions_async.go
/Users/petrbrazdil/Repos/onlv/house/process_async.go
/Users/petrbrazdil/Repos/onlv/maps/earth_async.go
/Users/petrbrazdil/Repos/onlv/AGENTS.md
/Users/petrbrazdil/Repos/onlv/README.md
```

## Milestones

Milestone 1 removes the public package and scenery runtime wiring. The repo should no longer contain `pubsub/` or parse `scenery.sh/pubsub` declarations.

Milestone 2 extends the native legacy async runtime package with the small service-method bridge needed by existing apps.

Milestone 3 migrates ONLV async job declarations to legacy async runtime workflows and activities.

Milestone 4 removes docs, dashboard/admin affordances, schemas, and test fixtures that document or exercise Pub/Sub.

## Plan of Work

First remove code paths that make Pub/Sub a discovered runtime declaration. Then migrate tests to legacy async runtime declarations or delete tests that only exercised Pub/Sub compatibility. The generated main should enable legacy async runtime for native legacy async runtime declarations only.

Add legacy async runtime helper APIs that are explicitly activity-oriented. A minimal shape is enough:

```go
type Void struct{}
func MethodActivity[I any, Svc any](handler func(Svc, context.Context, I) error) func(context.Context, I) (Void, error)
func RegisterServiceAccessorFor[T any](getter func() (any, error))
```

ONLV replacements should define one workflow per former topic publish path. The workflow executes one activity with retry policy and timeout derived from the old Pub/Sub subscription config. Publish call sites become `legacy-async-runtime.Start(ctx, workflowDecl, input)` and return `run.ID() + ":" + run.RunID()` where API responses previously returned message IDs.

## Concrete Steps

1. Delete `pubsub/` and remove the dependency from generated service files.
2. Remove Pub/Sub runtime hooks, clear endpoint, dashboard RPC, admin command, dashboard route, and devreport types.
3. Remove `RuntimeDeclarationPubSubTopic` and `RuntimeDeclarationPubSubSubscription`; update parser and codegen tests.
4. Remove `legacy-async-runtime.replace_pubsub` from config structs and schemas.
5. Add legacy async runtime service-method helper tests.
6. Rewrite ONLV async job declarations and publish call sites to `legacy-async-runtime.NewWorkflow`, `legacy-async-runtime.NewActivity`, `legacy-async-runtime.ExecuteActivity`, and `legacy-async-runtime.Start`.
7. Update `SKILL.md`, `AGENTS.md`, docs, README, knowledge indexes, and any harness allowlists.
8. Run validation.

## Validation and Acceptance

Run from `/Users/petrbrazdil/Repos/scenery`:

```sh
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

Run from `/Users/petrbrazdil/Repos/onlv`:

```sh
just repo-harness
scenery check --json
go test ./...
scenery harness --json --write
git diff --check
```

Acceptance:

```text
- `rg "scenery.sh/pubsub|pubsub.New|scenery admin pubsub|/__scenery/pubsub|Pub/Sub" .` has no active scenery product/API hits outside historical completed plans where preserving history is acceptable.
- `/Users/petrbrazdil/Repos/onlv` has no imports of `scenery.sh/pubsub`.
- Async ONLV flows still compile against native legacy async runtime declarations.
- scenery no longer exports a `pubsub` package.
```

## Idempotence and Recovery

Most steps are source edits and can be rerun. If ONLV migration fails, leave the legacy async runtime helper API in scenery only if it has tests and no Pub/Sub dependency. Do not reintroduce the Pub/Sub package as a fallback.

If dashboard/admin removal causes frontend build failures, remove the route and navigation entries first, then delete now-unused component files.

## Artifacts and Notes

Expected removed or heavily edited artifacts:

```text
pubsub/
removed historical pubsub product prompt
ui/src/routes/pubsub.tsx
ui/src/routes/pubsub-components.tsx
runtime/pubsub.go
```

Expected added or changed artifacts:

```text
legacy-async-runtime/legacy-async-runtime.go
legacy-async-runtime/legacy-async-runtime_test.go
docs/plans/0034-remove-pubsub-package.md
```

## Interfaces and Dependencies

The public async interface after this plan is `scenery.sh/legacy-async-runtime`. The package must support:

```go
legacy-async-runtime.NewWorkflow[I, O](name, cfg, handler)
legacy-async-runtime.NewActivity[I, O](name, cfg, handler)
legacy-async-runtime.Start(ctx, workflow, input)
legacy-async-runtime.ExecuteActivity(ctx, activity, input)
legacy-async-runtime.MethodActivity(...)
legacy-async-runtime.RegisterServiceAccessorFor(...)
```

No new external dependencies are expected.
