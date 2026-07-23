# scenery Execution Plans (ExecPlans)

Source: [OpenAI Codex ExecPlans](https://developers.openai.com/cookbook/articles/codex_exec_plans), adapted for scenery on 2026-04-27.

An ExecPlan is a living implementation specification for a complex feature, migration, or refactor. It is different from [PLAN.md](PLAN.md): `PLAN.md` is the strategic roadmap, while an ExecPlan is the self-contained file an agent can execute from start to finish.

Use an ExecPlan when the work is likely to span multiple hours, touch multiple subsystems, require staged validation, or need decisions preserved for the next agent. Small fixes do not need an ExecPlan.

## Storage

Put active ExecPlans in `docs/plans/<0000-short-slug>.md` and link them from [docs/plans/active.md](docs/plans/active.md). The four-digit number is a permanent historical sequence ID: allocate the next number once, do not renumber existing plans, and do not reuse numbers after a plan is completed, abandoned, merged, or superseded. `active.md` may still order plans by current priority rather than historical sequence. When a plan is complete, update its `Outcomes & Retrospective` section and move or reference it from [docs/plans/completed.md](docs/plans/completed.md). The completed plan then becomes immutable historical evidence: do not refresh its review dates or rewrite it to match later behavior. Put later guidance in current contract docs or the living completed-plan index.

## Non-Negotiable Rules

Every ExecPlan must be self-contained. A reader should need only the current working tree and the ExecPlan file. Do not rely on chat history, hidden assumptions, or external docs for required context.

While active, every ExecPlan must be a living document. Update it as implementation progresses, as surprises appear, and as decisions are made. A future agent should be able to resume from the file without asking what happened.

Every ExecPlan must produce observable behavior. Compilation alone is not enough unless the change is purely internal and the plan explains the test or command that proves the internal behavior.

Every ExecPlan must use plain language. Define project-specific terms the first time they appear and name the files, commands, and runtime behavior where those terms show up.

Every ExecPlan must be safe to resume. Commands should be idempotent where possible. If a step can fail halfway, explain how to retry or recover.

## Required Sections

Every ExecPlan file must contain these section headings exactly:

- `## Purpose / Big Picture`
- `## Progress`
- `## Surprises & Discoveries`
- `## Decision Log`
- `## Outcomes & Retrospective`
- `## Context and Orientation`
- `## Milestones`
- `## Plan of Work`
- `## Concrete Steps`
- `## Validation and Acceptance`
- `## Idempotence and Recovery`
- `## Artifacts and Notes`
- `## Interfaces and Dependencies`

The `Progress` section must use checkboxes and timestamps. Update it at every meaningful stopping point.

The `Decision Log` section must record the decision, rationale, date, and author for every meaningful implementation choice.

The `Surprises & Discoveries` section must record unexpected findings with evidence, such as test output, trace IDs, benchmark output, or the command that exposed the issue.

The `Outcomes & Retrospective` section starts empty or with `Not yet completed.` and must be updated when the plan finishes or changes scope.

## Writing Style

Write prose first. Use lists when they make the plan easier to execute, but do not turn the plan into a vague checklist. The plan should explain why the work matters, what changes, where the relevant code lives, what commands to run, and what a successful result looks like.

Name files with repository-relative paths. Name functions, packages, routes, commands, schemas, and generated artifacts precisely. If a command needs a working directory, state it.

Prefer additive milestones that keep the repo testable. If a prototype is needed, label it as a prototype, define how to run it, and state the criteria for keeping or deleting it.

## Validation Requirements

Every ExecPlan must include project-specific validation commands. For scenery repo changes, name the expected changed-area classes from the root [Validation Matrix](AGENTS.md#validation-matrix), refresh `.scenery/harness/agent-context.json`, and run the exact union in `changed_area.recommended_commands`. A plan that spans more than one class must carry every matching command.

Every validation item must name its exact command and working directory. A conditional item must state the exact observable condition that permits it to be skipped and the evidence that will record that condition. Do not use phrases such as “when practical,” “as appropriate,” “relevant validation,” or “for substantial changes” as substitutes for a command or skip condition.

Release-sensitive or runtime plans must include `.scenery/harness/bin/scenery harness self --summary --write` after the [Fresh Worktree Preflight](docs/agent-guide.md#fresh-worktree-preflight), plus the plan-specific real-process proof. Dashboard plans must include the exact lint, typecheck, build, and browser-acceptance commands from the root matrix.

For app-facing runtime changes, include an example command against a fixture app or another read-only scenery app available to the contributor.

## Harness Enforcement

`scenery harness self` validates this contract:

- `PLANS.md` must exist and define the required ExecPlan sections.
- Any Markdown file directly under `docs/plans/` except `active.md` and `completed.md` must contain all required ExecPlan section headings.
- Current ExecPlan validation requirements must not use subjective skip phrases in place of exact commands or conditions.
- Completed numbered ExecPlans are excluded from scheduled freshness review. Broken completed-index links, stale knowledge metadata that flags a current contradiction, and completed plans linked from the active index remain actionable diagnostics.
- Missing sections are reported as knowledge-contract diagnostics with file paths and suggested actions.

The harness enforces resumable structure and rejects known subjective validation phrases. Engineering acceptance criteria remain the plan author's responsibility.
