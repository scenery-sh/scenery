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
- [ ] Submit the first complete Oracle question and record its URL/revisions.
- [ ] Verify actionable advice, select a bounded implementation, and record its
  observable acceptance scenario before editing runtime behavior.
- [ ] Complete cumulative validation and update the draft PR.
- [ ] Start the next consultation with the draft PR number and exact current
  head, repeating until Petr stops the loop.

### Resume here

2026-10-08: initial documentation checkpoint; no product changes yet. Validate
this checkpoint, open the draft PR, then submit the first consultation. Local
consultation prompts, full answers and scheduler IDs belong under ignored
`.scenery/telemetry-improvements/`. Recover the recorded conversation before
resending; a pending generation never authorizes a duplicate request. Read live
Git/PR/scheduler state before relying on recorded status.

## Surprises & Discoveries

- Plan 0207 retains historical review/merge tasks, while current main already
  contains later telemetry reliability closure. Resolve behavior from the
  pinned implementation and current contracts, not stale plan status.

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
