# Skill Refresh and Agent Onboarding

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

`SKILL.md` is the installable onlava entrypoint for agents, for example through:

```sh
npx skills add https://github.com/pbrazdil/onlava
```

It must describe how to use the current onlava platform, not only the original core app/runtime slice. The skill should stay short enough to be usable as a skill, but it needs to route agents to the current source docs for data-platform, dashboard, harness, UI registry, standard auth, and ONLV layout migration workflows.

The goal is:

```text
agent installs onlava skill
        |
        v
agent learns current capabilities and validation commands
        |
        v
agent follows focused docs instead of stale chat context
```

## Progress

* [x] 2026-05-09: Create this ExecPlan as `docs/plans/0027-skill-refresh-and-agent-onboarding.md`.
* [x] 2026-05-09: Stage this plan in `docs/plans/active.md` without making it active.
* [x] 2026-05-09: Execute this docs-focused plan alongside active `0026`.
* [x] 2026-05-09: Audit `SKILL.md` against `docs/local-contract.md`, `docs/index.md`, `docs/knowledge.json`, `docs/data-platform.md`, `docs/ui-agent-contract.md`, and `docs/plans/active.md`.
* [x] 2026-05-09: Update `SKILL.md` with current high-impact workflows.
* [x] 2026-05-09: Keep `SKILL.md` concise and link to deeper docs instead of duplicating contracts.
* [x] 2026-05-09: Run docs and self-harness validation.

## Surprises & Discoveries

Record discoveries here as work proceeds.

Known starting discoveries:

* `SKILL.md` still focuses on the older core app/runtime flow and does not cover the current data platform, dashboard Data Explorer, browser UI harness, UI registry guardrails, import/export, search, auth tenant permissions, or ONLV layout migration.
* `docs/local-contract.md` already documents many newer surfaces, including `onlava harness ui --json`, `onlava inspect data --json`, `github.com/pbrazdil/onlava/data`, Data Explorer, UI static architecture checks, relationships, saved views, import/export, search, and standard-auth data tenant permissions.
* `docs/knowledge.json` and `docs/index.md` are the right source-of-truth entrypoints for routing agents to deeper docs.
* `SKILL.md` was short enough to update in place. The new cookbook and runbook now carry deeper operational detail.

## Decision Log

* Decision: Refresh the installable skill before adding more product surface.
  Rationale: A stale skill makes agents rely on chat history and increases misuse of new beta/dev surfaces.
  Date/Author: 2026-05-09 / Codex

* Decision: Keep `SKILL.md` as an onboarding router, not a giant manual.
  Rationale: Skills work best when concise. Long operational detail belongs in focused docs and runbooks linked from the skill.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed on 2026-05-09.

`SKILL.md` now covers current onlava workflows: app runtime, data platform, standard auth tenant permissions, Data Explorer, browser UI harness, UI registry guardrails, ONLV layout migration expectations, and validation command matrices. It links to `docs/local-contract.md`, `docs/app-development-cookbook.md`, `docs/data-platform.md`, `docs/data-platform-runbook.md`, `docs/ui-agent-contract.md`, and `docs/plans/active.md`.

## Context and Orientation

Relevant files:

```text
SKILL.md
docs/index.md
docs/knowledge.json
docs/local-contract.md
docs/data-platform.md
docs/ui-agent-contract.md
docs/plans/active.md
docs/plans/0026-onlv-layout-migration.md
cmd/onlava/*harness*
internal/datainspect/*
data/*
ui/components.json
ui/scripts/onlava-shadcn.mjs
```

The skill should mention the current surfaces that agents actually use:

```text
onlava run
onlava dev
onlava check --json
onlava inspect *
onlava inspect data --json
onlava harness --json --write
onlava harness self --json --write
onlava harness ui --json
github.com/pbrazdil/onlava/data
github.com/pbrazdil/onlava/auth
@onlava/* UI registry
bun run shadcn:add @onlava/<item>
```

## Scope

Update `SKILL.md` only enough to make installed-skill usage accurate.

Add or refresh concise sections for:

```text
current capabilities
data platform
standard auth
dashboard and Data Explorer
browser UI harness
UI registry and shadcn guardrails
ONLV layout migration expectations
validation command matrix
documentation entrypoints
```

Non-goals:

```text
rewriting all docs
copying docs/local-contract.md into SKILL.md
stabilizing beta APIs
adding new product behavior
changing CLI behavior
```

## Milestones

### Milestone 1: Skill audit

Compare `SKILL.md` to the current documented platform surfaces.

Acceptance:

```text
- stale or missing sections are listed in this plan
- each missing workflow has a source doc or source code reference
```

### Milestone 2: Current capability refresh

Add a short current-capabilities section to `SKILL.md`.

Acceptance:

```text
- skill mentions data platform, standard auth, dashboard, harness, UI registry, and inspect commands
- skill links to source docs instead of duplicating long contracts
```

### Milestone 3: Workflow snippets

Add practical snippets for:

```text
data.Open
CreateObject/CreateField/CreateRecord/QueryRecords
onlava inspect data --json
auth.CurrentAuthData
data.StandardAuthPermissions
onlava harness ui --json
bun run shadcn:add @onlava/<item>
```

Acceptance:

```text
- snippets are short and compile-oriented where possible
- each snippet has a validation command nearby
```

### Milestone 4: Validation matrix

Add a command matrix for app work, repo work, UI work, data-platform work, and release/preflight work.

Acceptance:

```text
- onlava repo work includes go test ./..., go install ./cmd/onlava, and self-harness
- target app work includes onlava check --json and onlava harness --json --write when practical
- UI work includes bun typecheck/test/build where applicable
```

## Plan of Work

Start by reading `SKILL.md` and the current contract docs. Update only the skill first. If deeper documentation gaps are found, record them here or in follow-up plans rather than expanding the skill into a long manual.

Prefer a structure like:

```text
What onlava is
Core app workflow
Data platform workflow
Auth workflow
Dashboard/harness workflow
UI workflow
Validation matrix
Where to read next
```

## Concrete Steps

1. Read `SKILL.md`.
2. Read `docs/local-contract.md`, `docs/index.md`, `docs/knowledge.json`, `docs/data-platform.md`, and `docs/ui-agent-contract.md`.
3. Update `SKILL.md` with the current-capabilities section.
4. Add concise workflow snippets and validation commands.
5. Ensure `SKILL.md` links to:

   ```text
   docs/local-contract.md
   docs/data-platform.md
   docs/ui-agent-contract.md
   docs/plans/active.md
   ```

6. Run validation.
7. Update this plan's Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective.

## Validation and Acceptance

Run from the onlava repo root:

```sh
onlava inspect docs --json
onlava harness self --json --write
go install ./cmd/onlava
```

Acceptance criteria:

```text
- SKILL.md mentions all current beta/dev surfaces agents need.
- SKILL.md links to the current source docs.
- SKILL.md remains concise enough for installed-skill use.
- Self-harness passes.
```

## Idempotence and Recovery

This is a docs-only plan. If the skill becomes too long, split operational detail into `docs/app-development-cookbook.md` or `docs/data-platform-runbook.md` and link to those documents.

Do not remove old core-runtime guidance unless it is wrong. Prefer updating it to modern onlava naming and adding a "read next" link.

## Artifacts and Notes

Expected changed artifacts:

```text
SKILL.md
.onlava/harness/self-latest.json
```

Potential follow-up artifacts from later plans:

```text
docs/app-development-cookbook.md
docs/data-platform-runbook.md
docs/data-platform-recipes.md
```

## Interfaces and Dependencies

No new runtime dependencies are expected.

The skill must reference public or documented surfaces only:

```text
cmd/onlava CLI
github.com/pbrazdil/onlava/data
github.com/pbrazdil/onlava/auth
docs/local-contract.md
docs/data-platform.md
docs/ui-agent-contract.md
```
