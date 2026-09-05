# scenery Agent-First Roadmap

This is the strategic roadmap for agent legibility and fast feedback.
[Active ExecPlans](docs/plans/active.md) own executable work;
[completed plans](docs/plans/completed.md) preserve acceptance evidence.
The original harness direction is recorded in completed plan
[0064](docs/plans/0064-agent-first-development-control-plane.md).

## Operating Model

- Humans set direction, constraints, and acceptance criteria.
- Agents implement and verify within repository instructions.
- Repeated feedback becomes a current contract or a focused mechanical check.
- Keep `AGENTS.md` short; discover detailed guidance through the docs index.
- Keep application concepts visible and substrate details available on demand.

## Implemented Baseline

These capabilities exist; their current contracts live in
[Harness Engineering](docs/harness-engineering.md) and
[the Local Contract](docs/local-contract.md).

- Typed application, graph, diagnostics, and runtime inspection.
- App, repository, and browser dashboard harnesses with structured evidence.
- Failure artifacts and focused `scenery inspect harness` drill-downs.
- Schema validation against representative outputs and committed fixtures.
- Architecture checks for dependency boundaries, generated-file hygiene,
  and source size.
- Task-scoped documentation discovery, review-due inspection, active-plan
  indexing, and a changed-area validation command union.
- A single `.scenery/harness/agent-context.json` handoff with failures,
  relevant docs, required commands, and risk classifications.

## Current Priorities

1. **Developer feedback cost.** Continue the measured work in
   [0145](docs/plans/0145-test-loop-attribution.md). Separate cached results,
   fresh test execution, binary preparation, and release integration evidence;
   preserve the root timing policy and complete test surface.
2. **Public runtime reliability.** Complete the outstanding operator
   reboot/login acceptance in [0101](docs/plans/0101-public-deploy-edge.md).
   Existing controlled-resume evidence does not substitute for that observation.
3. **Useful browser proof.** Extend fixture-backed mutation journeys where
   [the debt tracker](docs/tech-debt.md#browser-harness-fixture-backed-mutation-depth)
   identifies a real gap. Keep browser validation explicit.
4. **Maintainable repository knowledge.** Fix contradictory instructions,
   broken references, obsolete work queues, and duplicated contract prose.
   Review a document before advancing its freshness metadata; preserve
   completed plans as historical evidence.
5. **Focused architecture enforcement.** Address measured boundary or file-size
   problems from the harness. Add checks only for established invariants.

## Choosing The Next Change

Start with `scenery inspect docs --for-path <path> -o json` for planned work or
`scenery inspect docs --review-due -o json` for documentation cleanup. Refresh
and follow the validation oracle as described in
[AGENTS.md](AGENTS.md#validation-matrix). Record new multi-stage implementation
work using [PLANS.md](PLANS.md).

Do not treat completed milestones or suggested command names in historical
plans as pending features. Current CLI grammar belongs to the Local Contract;
current implementation work belongs to the active-plan index.
