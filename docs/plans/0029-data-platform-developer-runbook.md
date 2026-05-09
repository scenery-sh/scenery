# Data Platform Developer Runbook

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The beta data platform now spans dynamic metadata, real PostgreSQL schema, indexes, relationships, saved views, search, import/export, standard-auth data tenant permissions, trigger-backed outbox, SSE live updates, dashboard Data Explorer, and `onlava inspect data`.

`docs/data-platform.md` should remain the conceptual overview. This plan adds an operational runbook that tells developers and agents how to use and debug the platform day to day.

The goal is:

```text
developer has DATABASE_URL and an onlava app
        |
        v
developer follows runbook to create objects, query records, inspect schema, and debug outbox/migrations
        |
        v
developer avoids reading internal/objectstore as the first step
```

## Progress

* [x] 2026-05-09: Create this ExecPlan as `docs/plans/0029-data-platform-developer-runbook.md`.
* [x] 2026-05-09: Stage this plan in `docs/plans/active.md` without making it active.
* [x] 2026-05-09: Execute after `0027 Skill Refresh and Agent Onboarding`.
* [x] 2026-05-09: Audit `docs/data-platform.md`, `docs/local-contract.md`, `data/*`, and `examples/data-platform`.
* [x] 2026-05-09: Create `docs/data-platform-runbook.md`.
* [x] 2026-05-09: Decide not to create `docs/data-platform-recipes.md` yet; the runbook remains readable as one document.
* [x] 2026-05-09: Update docs entrypoints and knowledge index.
* [x] 2026-05-09: Run validation.

## Surprises & Discoveries

Record discoveries here as work proceeds.

Known starting discoveries:

* `docs/local-contract.md` documents more data-platform behavior than a new agent can comfortably absorb at once.
* The public package is `github.com/pbrazdil/onlava/data`; internal implementation details live under `internal/objectstore` and should not be the first reader-facing surface.
* Data-platform workflows need database-specific debugging commands such as `onlava inspect data --json --database-url "$DATABASE_URL"`.
* `docs/data-platform.md` is already a useful overview, so the new runbook could focus on operations, failure recovery, and caveats.

## Decision Log

* Decision: Keep `docs/data-platform.md` as overview and add `docs/data-platform-runbook.md` for operational flows.
  Rationale: Overview and runbook have different readers and should not be fused into one sprawling document.
  Date/Author: 2026-05-09 / Codex

* Decision: Document current beta limitations explicitly.
  Rationale: Agents need to know where the platform is intentionally incomplete so they do not overpromise or build against unstable assumptions.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed on 2026-05-09.

Added `docs/data-platform-runbook.md` with operational flows for opening stores, creating metadata, field options, composite fields, relations, indexes, saved views, record CRUD, queries/cursors/search, SSE events, trigger-backed outbox, import/export, standard-auth tenant permissions, inspect output, migration recovery, schema drift debugging, DB Studio caveats, performance notes, and known beta limitations. The runbook is linked from `docs/index.md` and tracked in `docs/knowledge.json`.

## Context and Orientation

Relevant files:

```text
docs/data-platform.md
docs/local-contract.md
docs/schemas/onlava.inspect.data.v1.schema.json
docs/schemas/onlava.data.export.v1.schema.json
data/*
internal/objectstore/*
internal/datainspect/*
examples/data-platform/*
testdata/apps/data-platform/*
cmd/onlava/*inspect*
```

The runbook should document public behavior and debugging flows. It can mention internal packages when explaining validation or tests, but app code should use `github.com/pbrazdil/onlava/data`.

## Scope

Create:

```text
docs/data-platform-runbook.md
```

Optional split if needed:

```text
docs/data-platform-recipes.md
```

Runbook sections:

```text
1. Opening a store
2. Creating tenant/object/field
3. Select/multi-select options
4. Composite fields
5. Relation fields
6. Indexes
7. Saved views
8. Record CRUD
9. Query filters/sorts/cursors
10. Live events and SSE
11. Trigger-backed outbox
12. Import/export
13. Standard auth tenant permissions
14. Inspect data output
15. Migration failure recovery
16. Schema drift debugging
17. DB Studio/direct SQL caveats
18. Performance notes
19. Known beta limitations
```

Non-goals:

```text
changing data package API
adding new data-platform features
documenting internal objectstore as public API
building dashboard UI
stabilizing beta surfaces
```

## Milestones

### Milestone 1: Surface audit

Audit current public data APIs and documented local-contract behavior.

Acceptance:

```text
- runbook outline matches actual data package capabilities
- beta limitations are listed before writing final prose
```

### Milestone 2: Operational flows

Write the core runbook flows.

Acceptance:

```text
- a new agent can create a tenant/object/field and query records using data.Open
- runbook includes inspect commands for metadata, migrations, outbox, triggers, indexes, and views
```

### Milestone 3: Debugging and recovery

Document failure handling.

Acceptance:

```text
- migration failure recovery is described
- schema drift debugging is described
- DB Studio/direct SQL trigger caveats are clear
```

### Milestone 4: Entry points

Update docs entrypoints.

Acceptance:

```text
- docs/index.md links the runbook
- docs/knowledge.json includes the runbook
- SKILL.md can link to the runbook after 0027
```

## Plan of Work

Begin with the public `data` package and existing examples. Use `docs/local-contract.md` to verify naming and command surfaces. Avoid copying long internal type definitions into the runbook.

Write command examples that agents can paste:

```sh
onlava inspect data --json --database-url "$DATABASE_URL"
onlava inspect data --json --database-url "$DATABASE_URL" --tenant acme --object company
ONLAVA_TEST_DATABASE_URL="$DATABASE_URL" go test ./internal/objectstore ./internal/datainspect
```

## Concrete Steps

1. Read `docs/data-platform.md` and data-related sections of `docs/local-contract.md`.
2. Inspect `data/*` for public API names.
3. Inspect `examples/data-platform` and `testdata/apps/data-platform`.
4. Draft `docs/data-platform-runbook.md`.
5. Add `docs/data-platform-recipes.md` only if the runbook becomes too long.
6. Update `docs/index.md`.
7. Update `docs/knowledge.json`.
8. Run validation.
9. Update this plan's living sections.

## Validation and Acceptance

Run from the onlava repo root:

```sh
onlava inspect docs --json
go test ./data ./internal/objectstore ./internal/datainspect
go install ./cmd/onlava
onlava harness self --json --write
```

Acceptance criteria:

```text
- docs/data-platform-runbook.md exists and is linked.
- docs clearly separate public data API from internal objectstore implementation.
- current beta limitations are explicit.
- inspect data and DB test commands are included.
- self-harness passes.
```

## Idempotence and Recovery

This is docs-only unless broken examples are discovered. If examples are stale, prefer opening a follow-up implementation plan unless the fix is small.

If the runbook grows too long, split copy-paste recipes into `docs/data-platform-recipes.md` and keep the runbook as the operational map.

## Artifacts and Notes

Expected changed artifacts:

```text
docs/data-platform-runbook.md
docs/data-platform-recipes.md, optional
docs/index.md
docs/knowledge.json
.onlava/harness/self-latest.json
```

## Interfaces and Dependencies

No new runtime dependencies are expected.

Documented public surfaces:

```text
github.com/pbrazdil/onlava/data
github.com/pbrazdil/onlava/auth
onlava inspect data --json
onlava harness self --json --write
onlava harness --json --write
```
