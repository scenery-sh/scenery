# Task-Scoped Agent Instructions

## Purpose / Big Picture

Implement all six accepted recommendations: a small app-skill router, lean
repository instructions, one validation selection, observable completion,
preserved authorization boundaries, and three before/after task exercises.

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current as work proceeds.

## Progress

- [x] 2026-09-14: Capture instruction bytes, hashes and word counts under
  `.scenery/harness/instruction-refresh-0191/before`; record unrelated changes.
- [x] 2026-09-14: Find unconditional full-verifier insertion in path-scoped
  documentation inspection despite the existing changed-area classifier.
- [ ] Compact entrypoints and preserve displaced operational guidance.
- [ ] Correct validation selection and align current contracts.
- [ ] Execute documentation, Go, and runtime exercises and record evidence.
- [ ] Complete required validation and the six-item acceptance audit.

## Surprises & Discoveries

`inspect docs --for-path SKILL.md` returns both quick and full commands.
`buildInspectDocsPathRoute` inserts full unconditionally after classification.
The classifier already selects quick for documentation and full for runtime.
Root `README.md` and `ARCHITECTURE.md` also fell through to generic source
validation; classify these known documentation entrypoints explicitly.
The baseline skill has 3,870 whitespace-separated words; root instructions have
2,500. These are text sizes, not token counts or latency measurements.

## Decision Log

- 2026-09-14, Codex: Reuse existing guide, cookbook, and runbook sections. Keep
  essential protections in the skill and route hazardous operations explicitly.
- 2026-09-14, Codex: Retain cached full Go-suite coverage, named external probes,
  and the absolute 100ms root policy. Fix selection without another classifier.
- 2026-09-14, Codex: Evaluate in the main session because delegation remains
  opt-in. Separate deterministic context/routing measurements and executed
  outcomes from independent model-performance claims.

## Outcomes & Retrospective

Not yet completed.

## Context and Orientation

`AGENTS.md` governs Scenery development; `SKILL.md` is the portable app skill.
`docs/agent-guide.md` holds contextual workflows. `internal/repoinfo/validation.go`
owns changed-area selection; `cmd/scenery/inspect_docs_routing.go` projects it
into read-only discovery. Root policy and `scripts/verify` own execution.
Another task is editing Plan 0190, its comparison report, and `internal/build`.
Preserve those files and their shared-index entries.

## Milestones

1. Compact instructions with discoverable references and unchanged safeguards.
2. Correct validation selection with focused behavior coverage.
3. Three executed exercises, final checks, and an explicit acceptance audit.

## Plan of Work

Shorten the skill to routing, ownership, permissions, and completion. Place
package-specific rules at their owning boundaries. Read architecture and
contracts according to the changed surface. Choose quick or full before
execution and write current evidence with that selected run. Remove the CLI's
unconditional full insertion; preserve local additions and full replacing quick.

Use captured before bytes for reference resolution and context accounting.
Exercise a documentation correction, small Go behavior change, and running
fixture endpoint change in disposable locations. Record requested outcomes,
selected reading/commands, interventions, and actual validation. Do not infer
causal model-speed improvements from a single main-session evaluation.

## Concrete Steps

Repository commands run from the Scenery root. Evidence is written beneath
`.scenery/harness/instruction-refresh-0191/`; durable results belong in
`docs/agent-instruction-evaluation.md`. App commands run only in recorded
fixture roots with an absolute selected Scenery executable.

## Validation and Acceptance

- Run `python3 -B /Users/petrbrazdil/.codex/skills/.system/skill-creator/scripts/quick_validate.py .`.
- Run `go test ./cmd/scenery` for CLI routing and JSON behavior.
- Run `golangci-lint run ./...` for root lint policy.
- Expected classes are documentation, Go package, and CLI JSON. Run
  `go run ./scripts/verify --quick --summary --write` and `go test ./...`.
  If working-tree classification also selects full, run
  `go run ./scripts/verify --summary --write` instead of quick. Its successful
  Go-suite step covers `go test ./...` for the same source state.
- Execute `inspect docs --for-path <path> -o json` for documentation, an ordinary
  Go package, and runtime. Require classifier-selected commands, local checks,
  and fixture regeneration without automatic full or simultaneous quick/full.
- In disposable app exercises run `scenery check -o json`,
  `scenery generate --check -o json`, `go test ./...`, and
  `scenery harness -o json --write`. Runtime acceptance additionally requires
  changed HTTP behavior and verified served build identity.
- Resolve skill references and audit preserved generated-output, ownership,
  destructive-action, delegation, installation, and test-budget requirements.
- No product external boundary is changed by docs routing. Named external
  probes, release certification, and all-root timing audits are not selected.
  The fixture runtime check is a task-scoped acceptance exercise.

## Idempotence and Recovery

Use owned temporary roots. Stop only exercise runtimes and verify termination;
retain evidence. Never reset unrelated edits, installed executables, or retained
data. Patch shared indexes against current contents so other entries survive.

## Artifacts and Notes

The baseline records capture time, HEAD, dirty paths, content hashes, words and
bytes. Save command outputs and final hashes. Planned checks and successful
builds alone are not runtime acceptance.

## Interfaces and Dependencies

No dependencies, environment controls, machine JSON fields, flags, or runtime
modes are added. Documentation inspection keeps the shared classifier and
current schema. Instructions remain model-neutral with explicit authorization.
