# onlava Agent-First Harness Plan

Source: [OpenAI Harness Engineering](https://openai.com/index/harness-engineering/), read on 2026-04-27.

This plan translates the article's agent-first engineering ideas into concrete onlava work. It is intentionally repo-local because agent-visible knowledge must live in the repository, not in chat history.

## Operating Model

onlava should optimize for agent legibility and fast feedback loops:

- Humans set direction, constraints, and acceptance criteria.
- Agents execute implementation and verification.
- Missing capability is treated as a product gap in the repo, not as a reason to prompt harder.
- Repeated review feedback becomes docs, schemas, harness checks, or architecture checks.
- `AGENTS.md` stays short and points to indexed docs instead of becoming a large manual.

## Current Baseline

Already implemented:

- Stable inspect surfaces: `onlava inspect app|routes|services|endpoints|wire|build|paths|traces|metrics|docs --json`.
- App harness: `onlava harness --json --write`.
- Repo self-harness: `onlava harness self --json --write`.
- Queryable local observability over traces, logs, and metrics rollups.
- Indexed docs knowledge base through [docs/knowledge.json](docs/knowledge.json).
- Docs entrypoint through [docs/index.md](docs/index.md).
- Active/completed plans and tech-debt tracker under [docs/plans/](docs/plans/) and [docs/tech-debt.md](docs/tech-debt.md).
- Architecture checks inside the self-harness.

## Priority 1: Browser And UI Harness

Goal: make dashboard and UI behavior directly legible to agents, not only humans.

Deliverables:

- Add `onlava harness ui --json [--repo-root <path>] [--app-root <path>] [--headed]`.
- Start or reuse a local onlava app fixture.
- Visit core dashboard routes: home, API Explorer, traces, trace details, DB Explorer, Data Explorer, Cron, and docs/help surfaces if present.
- Assert stable DOM markers rather than brittle visual-only selectors.
- Capture screenshots into `.onlava/harness/ui/`.
- Capture browser console errors, failed network requests, and route timing.
- Return a versioned JSON result with route status, artifacts, and remediation text.

Acceptance:

- A broken dashboard route causes `onlava harness ui --json` to fail with a specific route and screenshot path.
- The command can run headless by default and headed for local debugging.
- `onlava harness self --json --write` can optionally include UI harness output later without making the fast path heavy.

## Priority 2: Failure Evidence Artifacts

Goal: every failed harness result should include enough evidence for another agent to reproduce and fix without asking a human.

Deliverables:

- Extend harness result steps with optional artifact references.
- Store failure artifacts under `.onlava/harness/artifacts/`.
- Capture command output tails, screenshots, trace IDs, request/response transcripts, and repro commands where applicable.
- Add `onlava inspect harness --json` or extend `onlava inspect paths --json` to expose latest harness artifact locations.

Acceptance:

- For a UI failure, the harness JSON includes screenshot and console-log artifact paths.
- For a compile/test failure, the harness JSON includes command, cwd, output tail, and suggested next command.
- For an observability assertion failure, the harness JSON includes the relevant trace query and trace IDs.

## Priority 3: Schema Validation Against Real Outputs

Goal: schemas should not just be valid JSON; CLI outputs should conform to them.

Deliverables:

- Validate representative command outputs for `onlava inspect ... --json`, `onlava check --json`, `onlava harness --json`, `onlava harness self --json`, `onlava logs --jsonl`, and `onlava admin ... --json`.
- Prefer a small internal validator for the subset of JSON Schema used by onlava before adding a new dependency.
- If a dependency becomes necessary, document the concrete payoff in the architecture allowlist.

Acceptance:

- `onlava harness self` fails if a schema and command output drift.
- Validation errors identify the field path and expected type.
- Schema validation remains fast enough for the self-harness.

## Priority 4: Deeper Architecture Boundaries

Goal: keep speed without architectural drift.

Deliverables:

- Extend architecture checks with package dependency direction rules.
- Define allowed imports for public packages (`auth`, `errs`, `cron`, `middleware`, `temporal`, `pgxpool`, `rlog`) versus internal packages.
- Add source ownership metadata for major areas: CLI, parser/build/codegen, runtime, dashboard UI, DB Studio UI, docs, fixtures.
- Detect repeated local helper patterns that should be centralized.

Acceptance:

- A forbidden package edge fails `onlava harness self` with a remediation.
- Public packages stay small and do not accidentally depend on CLI/dashboard internals.
- New dependency direction rules are documented in [docs/harness-engineering.md](docs/harness-engineering.md).

## Priority 5: Recurring Garbage Collection

Goal: keep the repo from accumulating stale docs, large files, unused paths, and uneven patterns.

Deliverables:

- Add `onlava inspect debt --json` or extend `onlava inspect docs --json` with debt rollups.
- Track stale docs, review-due docs, large files, direct dependencies, repeated warnings, and slow tests.
- Add a recommended periodic command sequence for cleanup agents.
- Keep [docs/tech-debt.md](docs/tech-debt.md) as the human-readable debt tracker and `docs/knowledge.json` as the metadata source.

Acceptance:

- An agent can run one command and get prioritized cleanup candidates.
- Debt output is deterministic and suitable for small follow-up PRs.
- Cleanup suggestions include exact files and suggested actions.

## Priority 6: Agent Review Loop

Goal: make local agent review repeatable and mechanical.

Deliverables:

- Add `onlava harness review --json` or document a standard command sequence.
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

Implement `onlava harness ui --json` as the next major harness capability. Keep the first version small:

- one fixture app
- five dashboard routes
- DOM marker checks
- screenshots
- console/network error capture
- versioned JSON result

After that lands, wire it as an optional self-harness mode rather than forcing it into every fast validation run.
