# Documentation Drift Harness

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The repo already has `onlava inspect docs --json` and self-harness knowledge checks, but `SKILL.md` drifted behind the implemented and documented platform. This plan strengthens docs validation so the installable skill, docs index, local contract, schemas, CLI help, and implemented high-level surfaces stay aligned.

The goal is:

```text
feature lands
        |
        v
docs and skill must mention the new high-level surface
        |
        v
self-harness warns or fails before drift becomes agent-visible
```

## Progress

* [x] 2026-05-09: Create this ExecPlan as `docs/plans/0030-documentation-drift-harness.md`.
* [x] 2026-05-09: Stage this plan in `docs/plans/active.md` without making it active.
* [x] 2026-05-09: Activate after `0027`, `0028`, and `0029` landed enough docs for the harness to enforce.
* [x] 2026-05-09: Audit current docs inspection and knowledge-check implementation.
* [x] 2026-05-09: Add `SKILL.md` freshness and capability coverage checks.
* [x] 2026-05-09: Add docs/knowledge coverage checks for important docs.
* [x] 2026-05-09: Add schema and plan coverage through existing self-harness paths; defer brittle CLI grammar parsing.
* [x] 2026-05-09: Run validation.

## Surprises & Discoveries

Record discoveries here as work proceeds.

Known starting discoveries:

* `SKILL.md` was not strongly represented as a first-class contract in current docs checks.
* `docs/knowledge.json` tracks review dates and freshness windows, but high-level capability coverage still needs explicit enforcement.
* The harness already validates required ExecPlan sections and docs existence; this plan should extend that pattern, not build a separate lint framework.
* Existing `onlava inspect docs --json` already exposes review-due and stale state, so the new implementation only needed to make SKILL and important-doc coverage first-class.

## Decision Log

* Decision: Enforce documentation drift through self-harness rather than a new standalone tool first.
  Rationale: Agents and humans already run `onlava harness self --json --write`, and adding checks there keeps one validation path.
  Date/Author: 2026-05-09 / Codex

* Decision: Start with high-signal capability coverage strings.
  Rationale: Exact prose validation is brittle, but missing high-level surfaces such as `onlava inspect data --json` or `@onlava` registry are concrete and worth catching.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed on 2026-05-09.

`onlava harness self` now treats `SKILL.md`, the cookbook, the data runbook, data-platform docs, and the UI agent contract as knowledge entrypoints. The knowledge contract checks required `SKILL.md` capability mentions and verifies that important docs are indexed in `docs/knowledge.json`. A regression test covers stale skill detection.

## Context and Orientation

Relevant files:

```text
SKILL.md
docs/index.md
docs/knowledge.json
docs/local-contract.md
docs/schemas/*
docs/plans/active.md
docs/plans/completed.md
cmd/onlava/*harness*
cmd/onlava/*inspect*
internal/inspect/*
docs/harness-engineering.md
```

Existing command surfaces:

```sh
onlava inspect docs --json
onlava harness self --json --write
```

Optional future command shape:

```sh
onlava inspect docs --json --include-skill
```

Prefer self-harness-only integration unless a new inspect flag makes the implementation cleaner and useful to users.

## Scope

Add harness checks for:

```text
1. SKILL.md freshness
2. SKILL.md capability coverage
3. docs/local-contract.md vs CLI grammar, where practical
4. docs/knowledge.json important-doc coverage
5. schema list coverage
6. active/completed plan consistency
7. stale review_after warning
```

Initial `SKILL.md` required capability mentions:

```text
onlava harness ui --json
onlava inspect data --json
github.com/pbrazdil/onlava/data
docs/data-platform.md
docs/ui-agent-contract.md
@onlava registry
bun run shadcn:add @onlava/
onlava harness self --json --write
```

Initial `docs/knowledge.json` important entries:

```text
SKILL.md
docs/app-development-cookbook.md
docs/data-platform-runbook.md
docs/ui-agent-contract.md
docs/local-contract.md
```

Non-goals:

```text
natural-language correctness grading
rewriting all docs
adding heavyweight documentation tooling
blocking unrelated development on warnings unless explicitly promoted to errors
```

## Milestones

### Milestone 1: Current docs harness audit

Find where docs and knowledge checks live.

Acceptance:

```text
- current docs inspection flow is documented in this plan
- extension point is chosen
```

### Milestone 2: Skill coverage checks

Add explicit `SKILL.md` checks.

Acceptance:

```text
- self-harness reports missing skill capability strings with file and suggested action
- tests cover at least one missing-capability case
```

### Milestone 3: Knowledge index coverage

Ensure important docs are present in `docs/knowledge.json`.

Acceptance:

```text
- missing important docs are reported
- newly created cookbook/runbook docs are included
```

### Milestone 4: Contract/schema/plan checks

Add small high-signal checks for local-contract schema coverage and plan consistency.

Acceptance:

```text
- docs/schemas files are represented where expected
- active plan links resolve
- completed plan links resolve
- stale review_after appears in inspect docs output or self-harness diagnostics
```

## Plan of Work

Implement the smallest useful checks first. Do not attempt semantic doc verification. The harness should catch missing high-level surfaces and stale review metadata, then let humans judge prose quality.

Prefer warnings initially unless a check protects an existing hard contract.

## Concrete Steps

1. Inspect `cmd/onlava` and `internal/inspect` docs/harness code.
2. Add a focused checker for `SKILL.md`.
3. Add tests for missing skill strings.
4. Add important-doc coverage checks for `docs/knowledge.json`.
5. Add plan link consistency checks if they are not already present.
6. Add schema/local-contract checks only if they can be implemented without brittle parsing.
7. Run validation.
8. Update this plan's living sections.

## Validation and Acceptance

Run from the onlava repo root:

```sh
go test ./cmd/onlava ./internal/inspect
onlava inspect docs --json
onlava harness self --json --write
go install ./cmd/onlava
```

Acceptance criteria:

```text
- self-harness warns or fails when SKILL.md omits implemented high-level surfaces.
- docs/knowledge.json includes SKILL.md and the new cookbook/runbook docs.
- stale review_after metadata is visible to agents.
- active/completed plan links remain valid.
- self-harness passes after docs are current.
```

## Idempotence and Recovery

Start checks as warnings when the repo is transitioning. Promote a warning to an error only after the relevant docs exist and are stable.

If a check becomes noisy, narrow it to fewer high-signal required strings or move it behind a self-harness summary warning rather than deleting it.

## Artifacts and Notes

Expected changed artifacts:

```text
cmd/onlava/*harness*
internal/inspect/*
docs/harness-engineering.md
docs/knowledge.json
docs/schemas/*, only if an inspect shape changes
.onlava/harness/self-latest.json
```

## Interfaces and Dependencies

No new external dependencies are expected.

Relevant interfaces:

```text
onlava inspect docs --json
onlava harness self --json --write
docs/knowledge.json
SKILL.md
docs/local-contract.md
docs/schemas/*
```
