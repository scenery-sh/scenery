# scenery Agent-First Harness Plan

Source: [OpenAI Harness Engineering](https://openai.com/index/harness-engineering/), read on 2026-04-27.

This plan translates the article's agent-first engineering ideas into concrete scenery work. It is intentionally repo-local because agent-visible knowledge must live in the repository, not in chat history.

## Operating Model

scenery should optimize for agent legibility and fast feedback loops:

- Humans set direction, constraints, and acceptance criteria.
- Agents execute implementation and verification.
- Missing capability is treated as a product gap in the repo, not as a reason to prompt harder.
- Repeated review feedback becomes docs, schemas, harness checks, or architecture checks.
- `AGENTS.md` stays short and points to indexed docs instead of becoming a large manual.

## Current Baseline

Already implemented:

- Stable inspect surfaces: `scenery inspect app|routes|services|endpoints|wire|build|paths|docs --json`.
- Beta static IR inspect surfaces: `scenery inspect models|views --json`.
- Queryable diagnostics through `scenery traces list --json`, `scenery metrics list --json`, and `scenery logs --jsonl`.
- App harness: `scenery harness --json --write`.
- Repo self-harness: `scenery harness self --json --write`.
- Browser UI harness: `scenery harness ui --json [--app-root <path>] [--dashboard-url <url>] [--headed] [--write]`.
- Indexed docs knowledge base through [docs/knowledge.json](docs/knowledge.json).
- Docs entrypoint through [docs/index.md](docs/index.md).
- Active/completed plans and tech-debt tracker under [docs/plans/](docs/plans/) and [docs/tech-debt.md](docs/tech-debt.md).
- Architecture checks inside the self-harness.

## Priority 1: Browser And UI Harness Baseline

Goal: keep dashboard and UI behavior directly legible to agents, not only humans.

Implemented baseline:

- `scenery harness ui --json` starts or reuses a local dashboard target.
- It visits core dashboard routes and asserts stable `data-scenery-ui` markers.
- It captures screenshots plus console and network artifacts under `.scenery/harness/ui/`.
- It returns a versioned JSON result with per-route status and remediation text.

Current debt:

- Add deeper route-specific journeys for API Explorer, traces, DB/Data Explorer, Cron, and docs/help surfaces.
- Keep `scenery harness ui --json` explicit rather than making the fast self-harness path depend on a browser.
- Expand route assertions only where they represent stable product behavior, not visual noise.

## Priority 2: Failure Evidence Artifacts

Goal: every failed harness result should include enough evidence for another agent to reproduce and fix without asking a human.

Deliverables:

- Extend harness result steps with optional artifact references.
- Store failure artifacts under `.scenery/harness/artifacts/`.
- Capture command output tails, screenshots, trace IDs, request/response transcripts, and repro commands where applicable.
- Add `scenery inspect harness --json` or extend `scenery inspect paths --json` to expose latest harness artifact locations.

Acceptance:

- For a UI failure, the harness JSON includes screenshot and console-log artifact paths.
- For a compile/test failure, the harness JSON includes command, cwd, output tail, and suggested next command.
- For an observability assertion failure, the harness JSON includes the relevant trace query and trace IDs.

## Priority 3: Schema Validation Against Real Outputs

Goal: schemas should not just be valid JSON; CLI outputs should conform to them.

Deliverables:

- Validate representative command outputs for `scenery inspect ... --json`, `scenery check --json`, `scenery harness --json`, `scenery harness self --json`, `scenery logs --jsonl`, and `scenery traces clear --json`.
- Prefer a small internal validator for the subset of JSON Schema used by scenery before adding a new dependency.
- If a dependency becomes necessary, document the concrete payoff in the architecture allowlist.

Acceptance:

- `scenery harness self` fails if a schema and command output drift.
- Validation errors identify the field path and expected type.
- Schema validation remains fast enough for the self-harness.

## Priority 4: Deeper Architecture Boundaries

Goal: keep speed without architectural drift.

Deliverables:

- Extend architecture checks with package dependency direction rules.
- Define allowed imports for public packages (`auth`, `errs`, `cron`, `middleware`, `temporal`, `db`, `rlog`) versus internal packages.
- Add source ownership metadata for major areas: CLI, parser/build/codegen, runtime, dashboard UI, docs, fixtures.
- Detect repeated local helper patterns that should be centralized.

Acceptance:

- A forbidden package edge fails `scenery harness self` with a remediation.
- Public packages stay small and do not accidentally depend on CLI/dashboard internals.
- New dependency direction rules are documented in [docs/harness-engineering.md](docs/harness-engineering.md).

## Priority 5: Recurring Garbage Collection

Goal: keep the repo from accumulating stale docs, large files, unused paths, and uneven patterns.

Deliverables:

- Add `scenery inspect debt --json` or extend `scenery inspect docs --json` with debt rollups.
- Continue exposing stale docs and review-due docs through `scenery inspect docs --json` summary counts and per-document fields.
- Keep self-harness summaries surfacing docs review-due state alongside missing and stale docs.
- Track large files, direct dependencies, repeated warnings, and slow tests.
- Add a recommended periodic command sequence for cleanup agents.
- Keep [docs/tech-debt.md](docs/tech-debt.md) as the human-readable debt tracker and `docs/knowledge.json` as the metadata source.

Acceptance:

- An agent can run one command and get prioritized cleanup candidates.
- Debt output is deterministic and suitable for small follow-up PRs.
- Cleanup suggestions include exact files and suggested actions.

## Priority 6: Agent Review Loop

Goal: make local agent review repeatable and mechanical.

Deliverables:

- Add `scenery harness review --json` or document a standard command sequence.
- Include self-harness, app harness, observability queries, architecture checks, and optional UI harness.
- Add a concise checklist to [docs/harness-engineering.md](docs/harness-engineering.md).

Acceptance:

- Before handoff or commit, an agent can run one documented sequence and produce a stable validation snapshot.
- The output is useful to a second agent without reading the whole terminal scrollback.

## Non-Goals

- Do not turn `AGENTS.md` into the knowledge base.
- Do not require external SaaS observability or cloud services for local validation.
- Do not add large dependencies just to implement checks that can be handled with the Go standard library.
- Do not make the default self-harness so slow that agents stop running it.

## Next Concrete Step

Execute [0064 Agent-First Development Control Plane](docs/plans/0064-agent-first-development-control-plane.md) as the next repo-knowledge step. Keep the first PR knowledge-only:

- mark the browser UI harness as implemented everywhere agents look
- reframe browser harness debt as route-specific journey depth
- index all active ExecPlans in `docs/knowledge.json`
- document `review_due` visibility in docs inspection and self-harness summaries
- add a doc-gardening loop and a hard docs/behavior drift rule

After that lands, implement any remaining mechanical enforcement in focused follow-up PRs.
