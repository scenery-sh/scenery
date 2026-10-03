# Consistent Agent Workflows and Run-Bound Evidence

## Purpose / Big Picture

Implement the six accepted follow-up recommendations: consistent generated
guidance, resilient instruction checks, smaller plans, task-scoped app validation,
run-bound evidence, and an independent bounded instruction evaluation. This plan
is maintained as work proceeds. Authorization and data ownership do not change.

## Progress

- [x] 2026-10-03: Inspected current policies and captured the before instructions.
- [x] 2026-10-03: Aligned generated guidance, resilient instruction checks and eight-section plans.
- [x] 2026-10-03: Documented cumulative application validation by changed surface.
- [x] 2026-10-03: Published two real immutable report/context bundles with matching input identities.
- [x] 2026-10-03: Completed six independent sequential routing evaluations; recorded raw metrics and limitations.
- [x] 2026-10-03: Completed focused tests, full verification, lint, skill validation and evidence review; closed this plan.

## Surprises & Discoveries

The generated fast loop and local contract still prescribe quick verification
before implementation. The root policy already forbids redundant quick/full
runs. Shared latest reports were overwritten during the preceding evaluation.

- The new anchor check exposed four stale workflow links, now corrected.
- Static-check failure commands also requested release unnecessarily; they now
  reproduce through quick mode. Explicit probe selection remains unchanged.
- The CSS evaluation removed four planned Go/Scenery checks. All six sessions
  needed no clarification; uncached input increased despite fewer total input
  tokens. This is decision evidence, not a causal cost/performance result.
- The first full verifier exposed link failures; the corrected full run passed
  with documentation/architecture warnings and advisory cached-suite timing.
  The detailed [evaluation](../agent-instruction-evaluation-0209.md) records evidence.

## Decision Log

- 2026-10-03, Codex: Keep the existing classifier as the command-policy owner.
  Keep latest snapshots for navigation; acceptance cites the run archive.
- 2026-10-03, Codex: Evaluate instruction decisions in separate sequential
  sessions, with fixed model/settings and captured raw events. This bounded
  experiment measures planning/routing, not end-to-end runtime correctness.

## Outcomes & Retrospective

All six recommendations are implemented and verified. The independent CSS
scenario selected four fewer unrelated checks; repository cases preserved required
acceptance and reused successful tests. Six sessions completed without clarification.
Uncached tokens increased, so no causal cost or speed improvement is claimed.

`go test ./scripts/verify ./cmd/scenery ./internal/machine`, full verification,
`golangci-lint run ./...`, skill validation and `git diff --check` passed.
The full accepted run `20261003T185920.873039000Z` had stable input identity and
38 freshness, 21 architecture and one advisory cached-suite timing warning.
Final closure verification is captured in the task's `final-verification.json`.
See the [evaluation report](../agent-instruction-evaluation-0209.md) for raw evidence,
commands, limitations and unselected external/release/timing lanes. No runtime,
retained data, shared installation, commit or push was part of this work.

## Plan of Work

The repository verifier owns instruction checks, agent context and publication.
Shared report types belong in `internal/harnessreport`; machine payload identities
and checked schemas change together. Update the root, skill, guide, local contract
and knowledge index with the implemented behavior. Existing completed plans stay
immutable. App profiles remain the owner of application-specific acceptance.

Use eight core plan sections; fold context, milestones, concrete steps and
interfaces into this section when needed. Keep optional evidence detail separate
only when it helps the next agent resume.

## Validation and Acceptance

From the repository root run `go test ./scripts/verify ./cmd/scenery ./internal/machine`,
then `go run ./scripts/verify --summary --write` for the cumulative Go, CLI/schema
and release-sensitive script classes. Its successful repository Go suite satisfies
`go test ./...`. Run `golangci-lint run ./...`, the skill validator and
`git diff --check`. Read this run's agent context and fulfill its command union.
No product compiler/generator or external runtime boundary changes are planned;
fixture regeneration and external probes apply only if that scope changes.
No release certification or all-root timing audit is selected.

Acceptance covers full superseding quick in generated context, clean no-op
guidance, wrapped install-policy prose, routed skill references, resumable plans
without boilerplate, archive immutability and two runs with distinguishable inputs.
Run six independent read-only routing sessions (three identical before/after
tasks), capture actual tool calls/tokens/elapsed time and score selected checks.
Report unavailable metrics and unfavorable outcomes without claiming speedup.

## Idempotence and Recovery

Preserve unrelated changes. Snapshot before instructions beneath ignored task
evidence. Publish each archive once with an atomic directory rename; only then
refresh navigation snapshots. Failed publication must not claim a complete
archive. Interrupted evaluation sessions remain failed observations and may be
retried with separate evidence. Never alter application runtimes or retained data.
