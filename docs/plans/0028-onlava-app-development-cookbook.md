# onlava App Development Cookbook

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

onlava needs a practical cookbook for building and evolving apps. `docs/local-contract.md` defines contracts, and `docs/index.md` routes readers through the knowledge base, but agents need recipe-style "how to build X" guidance with commands, examples, and common failure modes.

The goal is:

```text
agent reads SKILL.md
        |
        v
agent opens docs/app-development-cookbook.md
        |
        v
agent implements an app feature with the right onlava APIs and validation commands
```

## Progress

* [x] 2026-05-09: Create this ExecPlan as `docs/plans/0028-onlava-app-development-cookbook.md`.
* [x] 2026-05-09: Stage this plan in `docs/plans/active.md` without making it active.
* [x] 2026-05-09: Execute after `0027 Skill Refresh and Agent Onboarding`.
* [x] 2026-05-09: Inventory existing fixture apps and examples.
* [x] 2026-05-09: Create `docs/app-development-cookbook.md`.
* [x] 2026-05-09: Link recipes to existing fixture apps and examples instead of adding unnecessary new example directories.
* [x] 2026-05-09: Update `docs/index.md` and `docs/knowledge.json`.
* [x] 2026-05-09: Run validation.

## Surprises & Discoveries

Record discoveries here as work proceeds.

Known starting discoveries:

* onlava has many current app-facing surfaces: typed APIs, raw APIs, auth, middleware, Pub/Sub, cron, pgxpool tracing, generated TypeScript clients, local proxy/frontend config, inspect commands, logs, traces, metrics, and harnesses.
* The contract docs are accurate but intentionally not written as a cookbook.
* Fixture apps and `testdata` should be reused before inventing new examples.
* Existing `testdata/apps` and `examples/data-platform` were enough for this docs pass; no new example app directories were needed.

## Decision Log

* Decision: Create a cookbook separate from `docs/local-contract.md`.
  Rationale: Contracts should remain precise and stable; recipes should be practical and workflow-oriented.
  Date/Author: 2026-05-09 / Codex

* Decision: Prefer examples backed by existing fixtures or compiling code.
  Rationale: Agent-facing docs drift quickly if examples are not validated by tests or real fixture apps.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed on 2026-05-09.

Added `docs/app-development-cookbook.md` with recipes for minimal apps, typed endpoints, auth endpoints, private/internal calls, service initialization, middleware, request tags, status responses, coded errors, request/auth context, Pub/Sub, cron, pgxpool tracing, TypeScript client generation, local proxy config, debugging, harness workflows, and common failures. The cookbook is linked from `docs/index.md` and tracked in `docs/knowledge.json`.

## Context and Orientation

Relevant files:

```text
docs/index.md
docs/knowledge.json
docs/local-contract.md
docs/harness-engineering.md
docs/data-platform.md
testdata/apps/*
examples/*
cmd/onlava/*
internal/parse/*
internal/codegen/*
runtime/*
auth/*
errs/*
pubsub/*
cron/*
pgxpool/*
middleware/*
```

The cookbook should explain how to build with public onlava surfaces, not how to modify internals.

## Scope

Create:

```text
docs/app-development-cookbook.md
```

Suggested chapters:

```text
1. Minimal app
2. Typed public endpoint
3. Auth endpoint
4. Private/internal endpoint call
5. Service struct initialization
6. Middleware
7. Request decoding tags
8. HTTP status responses
9. errs coded errors
10. CurrentRequest/auth context
11. Pub/Sub handler
12. Cron job
13. pgxpool tracing
14. TypeScript client generation
15. Local proxy/frontend config
16. Debugging with inspect/logs/traces/metrics
17. Harness workflow
18. Common mistakes and fixes
```

Potential examples:

```text
examples/minimal-app
examples/auth-app
examples/data-platform
examples/pubsub-cron
examples/middleware
```

Only add new example directories if existing fixture apps cannot serve the recipe. Keep examples small.

Non-goals:

```text
changing public APIs
documenting every internal package
building UI
stabilizing beta data APIs
adding a full tutorial site
```

## Milestones

### Milestone 1: Inventory recipes and fixtures

Map each cookbook chapter to an existing fixture, example, or source package.

Acceptance:

```text
- every proposed chapter has a source of truth
- missing examples are listed before implementation
```

### Milestone 2: Cookbook draft

Write `docs/app-development-cookbook.md`.

Acceptance:

```text
- each recipe has a short purpose, minimal code shape, validation commands, and common failure modes
- recipes link to contract docs for deeper details
```

### Milestone 3: Examples

Add missing examples only when they materially improve cookbook usefulness.

Acceptance:

```text
- examples compile or are represented by fixture apps
- examples avoid unrelated dependencies
```

### Milestone 4: Knowledge index

Update docs entrypoints.

Acceptance:

```text
- docs/index.md links the cookbook
- docs/knowledge.json includes the cookbook with ownership/review metadata
```

## Plan of Work

Start with inventory. Do not write new examples until the existing testdata apps are mapped. The cookbook should be direct and copy-paste friendly, but each recipe should point back to `docs/local-contract.md` for contract details.

Keep recipes short. If a recipe requires more than a screen of code, link to the fixture instead of inlining everything.

## Concrete Steps

1. Search `testdata/apps` and `examples` for existing minimal, auth, data, pubsub, cron, middleware, and TypeScript client examples.
2. Draft `docs/app-development-cookbook.md`.
3. Add missing examples only if justified.
4. Update `docs/index.md`.
5. Update `docs/knowledge.json`.
6. Run validation.
7. Update this plan's living sections.

## Validation and Acceptance

Run from the onlava repo root:

```sh
onlava inspect docs --json
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

Acceptance criteria:

```text
- cookbook exists and is linked from docs/index.md.
- docs/knowledge.json tracks the cookbook.
- every recipe has validation commands.
- examples compile or are backed by fixture apps.
- self-harness passes.
```

## Idempotence and Recovery

The cookbook can land incrementally. If a chapter is blocked by missing examples, add a short "planned" note and record the gap in this plan rather than inventing unvalidated code.

If `docs/knowledge.json` schema validation fails, inspect the existing entries and follow the same metadata shape.

## Artifacts and Notes

Expected changed artifacts:

```text
docs/app-development-cookbook.md
docs/index.md
docs/knowledge.json
examples/*, only if needed
.onlava/harness/self-latest.json
```

## Interfaces and Dependencies

No new runtime dependencies are expected.

Document public app-facing interfaces:

```text
//onlava:api
//onlava:service
//onlava:authhandler
github.com/pbrazdil/onlava
github.com/pbrazdil/onlava/auth
github.com/pbrazdil/onlava/errs
github.com/pbrazdil/onlava/pubsub
github.com/pbrazdil/onlava/cron
github.com/pbrazdil/onlava/pgxpool
github.com/pbrazdil/onlava/middleware
```
