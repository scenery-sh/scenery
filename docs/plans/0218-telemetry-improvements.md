# Telemetry Improvements

## Purpose / Big Picture

Improve Scenery's telemetry through repeated, revision-bound external reviews.
Each cycle asks ChatGPT 6 Astra Pro through its GitHub connector for concrete
defects and useful simplifications, verifies the advice locally, implements a
bounded change, validates it, and updates one draft pull request. Petr authorized
this loop on 2026-10-08 until he manually stops it. The draft is the reviewable
output; merging, deployment, global CLI installation and changes to unrelated
applications or retained data are outside this authorization.

Telemetry includes native CLI evidence, retained supervisor/build events,
report attribution and coverage, runtime signal capture/export, and their
inspection contracts. Preserve argument-free records, best-effort recording,
bounded work, explicit producer/purpose identity, successful-only percentiles,
and honest source coverage. Existing plans 0207, 0214 and 0215 establish prior
work; completed plans are immutable history.

## Progress

- [x] 2026-10-08 Verified clean published main at
  `89fdc46c20527939ee1780a240339ba04aa54ef8`; no open repository PR exists.
- [x] 2026-10-08 Created registered feature `telemetry-improvements` at
  `/Users/petrbrazdil/Repos/scenery-telemetry-improvements` on
  `feat/telemetry-improvements`, based on that main revision.
- [x] 2026-10-08 Published draft PR #240, Telemetry Improvements; initial
  base-scoped quick validation passed with zero errors and 12 existing document
  freshness warnings. Lint reported zero issues.
- [x] 2026-10-08 Created the persistent five-minute loop heartbeat
  `telemetry-improvements-loop`; recorded its ID in the local durable state.
- [x] 2026-10-08 Verified Chat GPT-6 in the hidden model submenu and Pro 5 of 5;
  accepted review at `https://chatgpt.com/c/6ac74dcb-56ec-83eb-956f-eaab8823d064`
  targets `2ee9b0f78a00060d8e2bd6da4e51ffe8b4e0d752` against the initial base.
  Actual submitted scope and input fragmentation are recorded in
  `.scenery/telemetry-improvements/cycle-001-submission.md`.
- [x] 2026-10-08 Created exact five-minute consultation monitor
  `telemetry-oracle-cycle-001`; preserve the separate persistent loop heartbeat.
- [ ] Verify actionable advice, select a bounded implementation, and record its
  observable acceptance scenario before editing runtime behavior.
- [ ] Complete cumulative validation and update the draft PR.
- [ ] Start the next consultation with the draft PR number and exact current
  head, repeating until Petr stops the loop.

### Resume here

2026-10-08: draft [PR #240](https://github.com/scenery-sh/scenery/pull/240) is
published; no product changes yet. The first Oracle review is accepted and
generating at the recorded URL with the actual GitHub connector and exact
target/base confirmed by the Oracle. Recover that conversation rather than
resending. Its exact monitor is `telemetry-oracle-cycle-001`; capture the entire
final answer before deleting that monitor, then assess/implement useful advice.
The separate `telemetry-improvements-loop` heartbeat persists until Petr stops
the loop. Local durable state, intended question, actual accepted scope and
synthetic negative-duration proof live under
`.scenery/telemetry-improvements/`. The model clarification is resolved: click
the Pro label within the Chat effort picker to reveal GPT-6 selection, then
return to verify Pro 5 of 5. Use paste for multiline questions; native typeText
submitted paragraphs separately. Read live Git/PR/scheduler state before relying
on this checkpoint; this later documentation update does not change the pending
review's pinned source.

## Surprises & Discoveries

- Plan 0207 retains historical review/merge tasks, while current main already
  contains later telemetry reliability closure. Resolve behavior from the
  pinned implementation and current contracts, not stale plan status.
- 2026-10-08: A synthetic successful CLI record with `duration_ms: -7` is rejected
  by the query but accepted by report, producing p50/p95 of -7 contrary to the
  checked report schema. Private fixture and actual output are retained in
  `.scenery/telemetry-improvements/proofs/negative-record/`; native recording
  clamps durations, so the reproduced trigger is malformed retained input.
- 2026-10-08: Chat's model submenu is hidden behind its effort label. Native
  typeText interprets multiline paragraph breaks as submits; the first review
  accepted three messages containing its source/goal/contracts/constraints.
  The intended validation/output/evidence tail was not submitted. The Oracle
  nevertheless verified the exact source pair through GitHub; assess its final
  coverage explicitly and use paste for subsequent complete questions.

## Decision Log

- 2026-10-08: Use one feature branch and one draft PR across cycles. Start a fresh
  ChatGPT conversation for each independent review; reuse only for a focused
  continuation where its context is still useful. Never run concurrent paid
  consultations for this loop.
- 2026-10-08: Oracle advice is evidence to investigate. Reject unsupported or
  redundant recommendations explicitly instead of expanding product surface
  merely to keep the loop moving. No subagents are authorized.

## Outcomes & Retrospective

Not yet completed. This plan remains active while the authorized loop runs.

## Plan of Work

The CLI capture/query/export paths are `cmd/scenery/telemetry*.go`; retained
evidence aggregation belongs to `internal/telemetryreport`. Runtime signal
capture belongs to `runtime/observability.go`, operation/HTTP tracing and the
existing bounded development report/export path. `internal/victoria` owns
backend reads. Architecture and `docs/local-contract.md` own the corresponding
public behavior; checked schemas establish exact machine output.

For every cycle, pin published target/base SHAs, include the draft PR number,
and provide relevant contracts, source paths, previous accepted/rejected advice
and validation limits to the Oracle. Capture the full answer and classify each
claim as confirmed defect, plausible concern, optional improvement or
inaccessible scope. Choose the next useful bounded change. Update all affected
owning docs/schemas/tests, then validate and publish the reviewed checkpoint to
the draft branch. Record the result and next review question here.

## Validation and Acceptance

All commands run from the feature worktree root. Documentation-only checkpoints
select `go run ./scripts/verify --quick --summary --write`. Go edits require
affected `go test ./<package>` commands and `go test ./...`; runtime or
release-sensitive edits select `go run ./scripts/verify --summary --write`
directly. Always run `golangci-lint run ./...`. Read the selected immutable run's
`agent-context-summary.json` first, then complete the exact changed-area command
union in `agent-context.json`, reusing successful same-input/same-scope checks.
Record each cycle's final archive and outstanding warnings before publication.

CLI invocation/file/process changes additionally run
`go run ./scripts/verify --probe cli-process --summary --write`; CLI grammar
changes run `go run ./scripts/verify --probe cli-grammar --summary --write`.
Runtime export/backend changes select the named `observability` probe; process
generation changes select `process-model`. Add each selected external boundary's
exact command and assertion/cleanup scenario before implementing it. A probe
may be skipped only when that external boundary is unchanged, recorded in the
cycle decision. Generator/UI changes retain the cumulative regeneration and
TypeScript commands in `docs/harness-engineering.md`.

No release certification, benchmarks or all-root timing audit is selected.
Focused ordinary tests remain service-free and preserve the per-root timing
budget. Runtime acceptance must demonstrate expected behavior and actual served
identity in an owned fixture; builds and plans alone are insufficient.

## Idempotence and Recovery

Preserve main, unrelated files, index/worktree distinctions, running apps and
retained resources. Use the worktree-local prepared CLI; do not install globally.
Keep one durable cycle state and one exact consultation monitor. On completion,
capture the entire answer before deleting its monitor. The separately authorized
loop heartbeat persists until Petr stops it; it reads the durable state to resume
implementation or start the next review. Generation errors pause that exact
monitor and require user steering rather than automatically retrying a paid
request. Healthy generation is left alone; if the web view stalls, refresh at
the five-minute observation cadence without resubmitting.
