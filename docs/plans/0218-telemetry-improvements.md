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
- [x] 2026-10-08 Captured the entire completed first review in the originating
  task, assessed its nine source findings, and deleted the exact consultation
  monitor with confirmed cleanup. Its conversation remains available.
- [x] 2026-10-08 Verified the preliminary native command privacy finding and
  implemented catalog-derived classification. Focused package tests pass;
  the process probe now expects `unknown` and covers a private task operand.
- [x] 2026-10-08 Reject negative native durations in report intake, with
  valid-following-record, coverage, aggregation and checked-schema tests.
- [x] Verify actionable advice, select a bounded implementation, and record its
  observable acceptance scenario before editing runtime behavior.
- [x] 2026-10-08 Implemented N2/N4/N5/N9 and the narrow N3 lookup correction.
  Their focused regression cases failed before the corrections and the affected
  package tests now pass. N1's exact pinned classifier also fails the new privacy
  cases through an explicit Go source overlay; candidate surrounding code is
  retained, so this is not whole-target execution proof.
- [x] Complete final full/default, lint, CLI-process and observability acceptance
  for this small batch; retain source/cleanup proof and warnings.
- [x] 2026-10-08 Final source validation: full/default archive
  `20261008T083822.678926000Z`, CLI-process `20261008T083926.834884000Z`,
  observability `20261008T084234.531468000Z`, all at stable input
  `sha256:2ac4a4ca87fabfc1967005b5b46c85ae4ca618834edb40809f85d31217d72962`.
  Zero errors; lint 0 issues. Full had 12 existing knowledge warnings and a
  5.286 s whole-suite advisory warning; probes had only the 12 knowledge warnings.
  All 11 changed test roots passed 20 isolated serial samples, reported p95 at
  most 30 ms. Observability verified native/SQL/HTTP traces, scoped RPC queries,
  structured logs and duration metrics, with source build-input identity and
  cleanup of its runtime, Victoria processes and PostgreSQL cluster.
- [x] 2026-10-08 Published the first validated implementation batch as
  `35d94253caa2538d2d938d058bed86f8c23498c0` and updated draft PR #240.
- [ ] Complete cumulative validation and update the draft PR for each subsequent
  implementation batch.
- [ ] Start the next consultation with the draft PR number and exact current
  head, repeating until Petr stops the loop.

### Resume here

2026-10-08: draft [PR #240](https://github.com/scenery-sh/scenery/pull/240) contains
the first validated source batch at `35d94253caa2538d2d938d058bed86f8c23498c0`.
The first review is complete and captured; its exact monitor was deleted. N1,
N2, narrow N3, N4, N5, N9 and the independent negative-duration defect are fixed.
Final full/default archive `20261008T084548.900946000Z` covers the changed-area
union with zero errors, 12 existing knowledge warnings and a 15.257 s whole-suite
advisory warning. Lint and both named probes passed; their compiled framework
digest matches the final verifier, and owned runtime cleanup is confirmed.
This publication-status update is documentation only against the source commit;
verify that delta before its checkpoint. Then start a fresh second review of the
exact latest published head, or recover its accepted conversation from durable
state if already pending. N6-N8, general wrapper/redirection semantics and ZIP
publication remain explicit later milestones.
The independent `telemetry-improvements-loop` heartbeat persists until Petr
stops. Local durable state, actual submission limits, complete-answer capture
location, assessment and synthetic proof live under
`.scenery/telemetry-improvements/`. Use paste for future multiline questions,
verify Chat GPT-6 plus Pro and the actual GitHub chip, and name the next exact
published head with PR #240. Read live Git/PR/scheduler state before relying on
this checkpoint; the first review stays bound to its original source.

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
- 2026-10-08: Verified the first review's preliminary command-classification
  finding against the pinned source and current argument-free contract. The
  first bounded change uses the existing help-derived command families, keeps
  only a recognized root and first subcommand, and groups every unrecognized
  root as `unknown`. It never changes command execution or diagnostics.
- 2026-10-08: The complete review confirms additional source defects: agent
  p50 includes failures (N2), shell lookup wrappers count as execution (N3),
  rebuild findings use the combined failure cohort (N4), query invents a clean
  historical dirty state (N5), trace-buffer losses are uncounted (N6), retained
  report state/snapshot coverage lacks bounds (N7), finite backend reads lack
  bounds (N8), and metrics decode errors are discarded (N9). Verify N8/N9's
  concrete boundaries before changing them. Select small N2/N4/N5/N9 corrections
  with focused failing-before/passing-after proof; N3 remains a narrow shell
  lookup correction. N6-N8 need separate counting/coverage/budget milestones.

## Outcomes & Retrospective

Not yet completed. This plan remains active while the authorized loop runs.
The first implementation batch fixes native argument capture, negative report
durations, successful agent percentiles, rebuild cause cohorts, historical dirty
identity, command lookup attribution and swallowed metric decode errors. It
adds no dependency, environment knob or alternate decoder. Existing schemas
already describe the corrected types; JSON shapes and schema revisions stay
unchanged. Completed historical plans and `VNEXT.md` remain untouched.
Real runtime acceptance used an owned disposable application and verified
build-input digest `sha256:d02c2e568820474e77d0b3f81f9edbbc5bf11fe9bcde18346972823f4cb7a952`;
it is evidence for this candidate, not for a live installed agent or deployment.
Release certification and unrelated lifecycle probes were not selected; CLI
grammar, runtime RPC output and generated-client templates did not change.

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

The first observable scenario is a native CLI invocation with an unknown root,
unknown subcommand or positional SSH target. Its retained `command` must contain
only a known catalog name or `unknown`, while known subcommands still retain
their useful coarse identity and help/long-running classification is preserved.
Focused classification tests cover the catalog and synthetic sensitive operands;
the `cli-process` probe verifies native recording, exit status and isolated agent
home through the real CLI. No SSH connection or private data is needed.

The independently reproduced malformed-input scenario adds successful and failed
CLI records with negative durations to an owned fixture. The report must count
them as invalid source records, exclude them from all timing/outcome/cohort
aggregates, retain valid following records and satisfy its unchanged schema.

The small report/query batch additionally proves these observable results:

- Claude command failures taking 10/20 ms and a success taking 1,000 ms report
  p50 1,000 ms; only-failure commands have null p50. Total attributable waiting
  time and attempt/failure counts retain every known outcome.
- A rebuild failure-rate finding uses rebuild causes even when initial failures
  dominate the combined cause list, in both warning and critical cases.
- Native query JSON preserves absent, false and true historical dirty identity;
  new native invocations still record an explicit boolean.
- Malformed VictoriaMetrics result objects fail JSON decoding instead of
  returning successful empty/partial series. Existing vector, matrix, label and
  series shapes remain valid. The named `observability` probe retains the real
  generated-app, trace/log/metric query, source identity and cleanup acceptance.
- `command -v`/`-V`, including combined short flags and nested `env`, count no
  Scenery invocation. Ordinary `command` and `command -p --` remain direct;
  unknown wrapper flags do not attribute a shell result to Scenery. File
  redirection semantics remain a separately tracked contract refinement.

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
