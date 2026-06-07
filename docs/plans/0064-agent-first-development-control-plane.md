# Agent-First Development Control Plane

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Agents should be able to inspect this repo and receive current instructions before
they change code. This plan makes the repo knowledge layer itself a small
development control plane: current roadmap, active ExecPlans, tech debt,
review-due docs, and doc/behavior drift handling all stay visible through
checked-in files and machine-readable inspection.

The first PR is intentionally documentation-only. It reconciles stale knowledge
so later agents stop treating implemented browser UI harness behavior as future
work.

## Progress

- [x] 2026-06-07: Created plan `0064-agent-first-development-control-plane.md` without renumbering historical plan IDs.
- [x] 2026-06-07: Reconciled roadmap and tech-debt language so the browser UI harness is implemented baseline. Later route-specific journey work moved the remaining debt to fixture-backed mutation depth.
- [x] 2026-06-07: Added active ExecPlans to `docs/knowledge.json` for machine-readable discovery.
- [x] 2026-06-07: Documented `review_due` visibility and the docs/behavior drift rule in agent-facing docs.
- [ ] Add generated or self-harness-enforced active ExecPlan indexing once a later PR is allowed to change code.

## Surprises & Discoveries

- 2026-06-07: `onlava inspect docs --json` already exposes `summary.review_due_count` plus per-document `review_due` and `stale`; the needed change is to make that behavior visible in the repo knowledge docs.
- 2026-06-07: The completed-plan record already marks `onlava harness ui --json` as shipped, while the root harness roadmap and tech-debt tracker still described it as missing.

## Decision Log

- 2026-06-07: Do not implement code in the first PR. The user explicitly scoped this change to repo-knowledge alignment files plus this new ExecPlan.
- 2026-06-07: Index active ExecPlans directly in `docs/knowledge.json` for now. Deterministic generation or validation can be implemented in a follow-up PR.
- 2026-06-07: Keep plan IDs permanent historical sequence IDs. The next plan is `0064`; existing plans are not renumbered.

## Outcomes & Retrospective

Pending. This plan becomes successful when agents can run docs inspection, read
the active plan list, and stop treating browser UI harness functionality as
unimplemented.

## Context and Orientation

Start with these files:

- `AGENTS.md` for repo-local agent rules.
- `SKILL.md` for the portable onlava skill.
- `PLAN.md` for the harness-engineering roadmap.
- `docs/harness-engineering.md` for harness and doc-gardening practice.
- `docs/knowledge.json` for machine-readable docs metadata.
- `docs/plans/active.md` and `docs/plans/completed.md` for ExecPlan state.
- `docs/tech-debt.md` for visible follow-up debt.
- `docs/local-contract.md` for implemented CLI and JSON contracts.

The current implemented contract includes `onlava harness ui --json` and
`onlava inspect docs --json` review-due fields.

## Milestones

1. Repo Knowledge Reconciliation: the roadmap, tech debt, active/completed plan
   lists, skill, and repo agent instructions agree on implemented browser UI
   harness behavior, review-due visibility, and doc drift handling.
2. Machine Index Coverage: every active ExecPlan appears in
   `docs/knowledge.json` until deterministic indexing replaces manual entries.
3. Mechanical Enforcement: a later code PR teaches inspection or self-harness to
   detect an active plan missing from the knowledge index.

## Plan of Work

Update only repo-knowledge files in the first PR. Add this ExecPlan to
`docs/plans/active.md`, revise stale browser harness language, record the
doc-gardening loop in `docs/harness-engineering.md`, refresh the portable and
repo-local agent instructions, and add active ExecPlans to `docs/knowledge.json`.

After the knowledge-only PR lands, implement mechanical enforcement in a focused
follow-up if maintainers still want generated/indexed active plan coverage.

## Concrete Steps

1. Run `onlava inspect docs --json` and inspect current docs knowledge state.
2. Create `docs/plans/0064-agent-first-development-control-plane.md`.
3. Add plan 0064 to `docs/plans/active.md`.
4. Update `PLAN.md`, `docs/tech-debt.md`, and `docs/plans/completed.md` so the
   browser UI harness is treated as implemented baseline.
5. Update `docs/harness-engineering.md`, `AGENTS.md`, and `SKILL.md` with
   review-due visibility and the docs/behavior drift rule.
6. Add all active ExecPlans to `docs/knowledge.json`.
7. Validate JSON and docs inspection, then run the self-harness if the local
   environment allows it.

## Validation and Acceptance

Acceptance criteria:

- `docs/plans/active.md` links plan 0064 without renumbering existing plans.
- `docs/knowledge.json` contains document entries for every active ExecPlan.
- `PLAN.md` and `docs/tech-debt.md` no longer describe the browser UI harness as missing.
- `docs/harness-engineering.md` includes a doc-gardening section.
- `AGENTS.md` and `SKILL.md` contain the rule that docs/behavior drift must be fixed or recorded in an ExecPlan in the same PR.
- `onlava inspect docs --json` succeeds and shows review-due fields.

Validation commands:

```sh
jq empty docs/knowledge.json
onlava inspect docs --json
onlava harness self --json --write
git diff --check
```

## Idempotence and Recovery

The changes are text and JSON only. If validation fails, inspect the reported
path, fix invalid JSON or broken links, and rerun the same validation commands.
Do not renumber any existing ExecPlan while recovering from a failed edit.

## Artifacts and Notes

Expected artifacts:

- `.onlava/harness/self-latest.json` when self-harness validation runs with
  `--write`.

No generated cache output should be committed.

## Interfaces and Dependencies

This plan depends on the existing `onlava inspect docs --json` contract and
`docs/knowledge.json` schema. It intentionally avoids new runtime interfaces in
the first PR.
