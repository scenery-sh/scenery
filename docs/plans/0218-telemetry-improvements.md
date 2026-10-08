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
- [x] 2026-10-08 Published documentation checkpoint
  `b258a7d57ddf697a670e7d56587da25d84a6962a`; its documentation-only quick
  archive `20261008T085625.155150000Z` has stable inputs and zero errors.
- [x] 2026-10-08 Submitted one complete fresh Chat GPT-6 Pro review with the
  actual GitHub connector against that exact head and the original base, naming
  PR #240 and all first-batch progress. Canonical conversation:
  `https://chatgpt.com/c/6ac75c6f-8f80-83eb-8af5-660a5d206a55`.
  The complete final answer was captured in the originating task; its exact
  five-minute monitor `telemetry-oracle-cycle-002` was deleted and absence verified.
- [x] 2026-10-08 Independently reproduced trace-buffer capacity/age losses with
  a test-only overlay and the public status boundary. Preliminary cycle-002
  feedback also identified reversed Claude clocks; its regression failed with
  negative wall time, then passed after excluding that timing while retaining
  known outcomes and legitimate zero-duration samples.
- [x] Complete N6 with a separately named trace-event loss count, deterministic
  eviction tests, checked status/client contract, and real owned runtime proof.
- [x] 2026-10-08 Named observability archive `20261008T102236.074295000Z`
  passes with stable inputs, zero errors and 12 existing knowledge warnings.
  Its capacity phase records seven buffer evictions and 4,092 retained synthetic
  events; ordinary SDK reports share that buffer, so the capacity delta need not
  be one. The natural 31-second span records exactly one TTL eviction, healthy
  summary/end evidence and a 31.001038-second metric. An immediate operation
  adds zero. Scoped RPC and the actual generated client agree on current status
  revision `sha256:50d87d2565ab839da6883c12d91522fad5f9c9cca1efd93f92e214f8ad8a4b13`.
  Serving build input is
  `sha256:7dcaf18c4680c1ed344d0d8d342b25c4669e7b00106cf970d36c750989d7fcc5`;
  owned runtime, Victoria processes and PostgreSQL cleanup is verified.
- [x] Capture and assess the entire second review, then delete its exact monitor.
- [x] 2026-10-08 Reproduced malformed metric samples as partial success through
  `QueryMetrics` with a private injected transport. Local correction validates
  supported shapes and every sample, including series beyond the output limit;
  valid NaN/infinity value strings remain accepted. Focused packages pass.
  Cumulative/runtime validation is complete; publish with the other second-batch
  corrections in the same draft checkpoint.
- [x] 2026-10-08 Second-batch final full/default archive
  `20261008T102633.892751000Z` and CLI-process archive
  `20261008T102633.893718000Z` pass at stable input
  `sha256:d501119a2f099c2118f198035b809031c669aaddd3eb7881523f071010e8499d`.
  Zero errors, lint zero issues; 12 existing knowledge warnings and one 6.547 s
  whole-suite advisory in full. All 18 cumulative changed Go test roots pass 20
  isolated serial samples with p95 at most 50 ms, below 100 ms. Native, house and
  assistant regeneration, both TypeScript checks and 56 Bun tests pass; unchanged
  generator inputs/scope permit reusing those development receipts. Full covers
  the changed-area Go command union. Probe/full framework source digest matches
  `sha256:9138cd78aee5f90642b02bfc46db0d50a8574e90c5bf4a86a591a3e12e3859e0`.
- [x] 2026-10-08 Committed independently reviewable source changes:
  `fbce55d4` reversed agent timing, `6bc63937` complete metric validation,
  and `e792dd40` trace-buffer event loss contract and runtime proof.
- [x] 2026-10-08 Published second-batch checkpoint
  `637a0e1669574bf76722788c41ab5526c038e378`; draft PR #240 head/base/body
  and clean feature checkout verified. Its documentation-only quick archive
  `20261008T102943.177603000Z` passes with stable inputs and the identical
  compiled framework source digest.
- [x] 2026-10-08 Submitted a single complete fresh GPT-6 Pro question with the
  actual GitHub connector against that head/base and PR #240 progress:
  `https://chatgpt.com/c/6ac77146-7eb8-83eb-826f-b9cb242b9eb6`. The exact
  five-minute monitor `telemetry-oracle-cycle-003` is active; the answer is pending.
- [x] 2026-10-08 Independently reproduced decimal timestamp defects through the
  public `QueryMetrics` boundary at that exact published source: the valid upper
  int64 nanosecond endpoint is rejected, the valid lower endpoint shifts 574 ns,
  one nanosecond below it is accepted, and ordinary `.123456789` loses 73 ns.
  This is a verified correction to the prior batch, not completed Oracle feedback.
- [x] Complete the exact-decimal correction: preserve `json.Number` through the
  nested result decoder, compare the exact range before truncating subnanosecond
  fractions toward zero, and avoid exponent-sized allocation. Focused public
  query cases now pass both endpoints, near-boundary fractions, negative/zero
  times and exponent spellings. Source commit `d53b36c0` is locally complete.
- [x] 2026-10-08 Exact timestamp correction: cumulative full/default archive
  `20261008T104731.424521000Z`, observability `20261008T104851.438112000Z`
  and CLI-process `20261008T105655.542833000Z` pass at stable input
  `sha256:fcdb58919e5d9280db8f1e9a3df797efdc5e8b5452df4d68b664d6eddc3ca23c`.
  Zero errors and 12 existing knowledge warnings; lint zero issues. All 19
  cumulative changed test roots pass 20 isolated serial samples, max p95 10 ms.
  All three archives share compiled framework source digest
  `sha256:b645acd3c5729c2d821cceff320d1838a938be69de8326e99d25d13856766797`.
  Ordinary backend metrics, trace/log/RPC/client acceptance, exact natural TTL
  loss one, immediate zero and owned runtime/Victoria/PostgreSQL cleanup pass.
  The unchanged generator/schema/client input scope reuses second-batch
  regeneration, Bun and TypeScript receipts.
- [ ] Complete cumulative validation and update the draft PR for each subsequent
  implementation batch.
- [ ] Start the next consultation with the draft PR number and exact current
  head, repeating until Petr stops the loop.

### Resume here

2026-10-08: draft [PR #240](https://github.com/scenery-sh/scenery/pull/240)
has published head `637a0e1669574bf76722788c41ab5526c038e378`. The third
review targets that exact source and the original base; its complete question is
accepted and generating, with an exact five-minute monitor. Capture the entire
final answer before deleting that monitor. Local source proofs independently
confirm an exact timestamp range/precision defect; its correction is implemented
and cumulative full, lint, changed-root timing and owned runtime acceptance
now pass; publish its source and this documentation checkpoint.
Finite-query proof also accepts 9,437,299 input bytes and 4,194,868 normalized
output bytes despite a series limit of one. The neighboring 8 MiB/4 MiB precedents
are not yet new finite-query contracts, and this establishes no actual backend
incidence or OOM. Assess completed Oracle advice, then publish the exact-decimal
correction and choose the next bounded milestone. N7/N8, broader wrapper/redirection
semantics and ZIP publication remain later milestones, alongside the reproduced
VictoriaTraces field-cap admission gap. These development checks establish no
production incidence, release certification or installed-agent/deployment proof.
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

- 2026-10-08: The first N6 runtime attempt exposed a separate backend limit:
  one synthetic span with 4,093 events exceeded VictoriaTraces' 1,000-field
  line cap (20,485 fields) and was discarded despite accepted export transport.
  Cleanup succeeded. Partition the capacity fixture across at most 128 events
  per span, preserving its total 4,097-event workload and natural TTL scenario.
  Backend admission/loss evidence is an explicit later candidate; the new buffer
  counter does not promise end-to-end delivery.
- 2026-10-08: Two later probe failures were in the acceptance code: a reused
  decode destination retained obsolete map keys, and the lifecycle payload was
  incorrectly expected to have event name `span_end` rather than `scenery.event`.
  Fresh decoding and assertions on `data.span_end` correct those expectations.
  VictoriaMetrics also normally hides fresh samples for 30 seconds; the original
  15-second metric poll was insufficient. The named probe now permits 45 seconds
  and retains a useful last-query diagnostic. No production timing or export
  policy changed to make these checks pass.

## Decision Log

- 2026-10-08: Correct metric timestamps using the original decimal JSON number,
  including exponent notation, rather than a float64 intermediary. Nested result
  decoding must retain `json.Number` too. Reject exact values beyond either
  int64 nanosecond endpoint before truncating subnanosecond fractions toward zero.
  Compare decimal digit counts and at most 19 whole-nanosecond digits; huge
  exponents must not allocate exponent-sized integers. Numeric-string metric
  values and current JSON/schema shapes stay unchanged.

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

- 2026-10-08: N6 uses `observability.export.trace_buffer_dropped_events` for individual
  events removed from the waiting buffer by age or capacity. Keep existing
  `dropped` units and `failed` report/signal pairs; do not add unlike units or
  redesign delivery accounting in this batch. The buffer owns this counter under
  its existing mutex; successful drains never increment it. Age eviction remains
  lazy on add/drain, as before. The checked status revision and fixed generated
  client advance together. This choice follows verified source behavior and the
  completed second review's counting critique. Its source-only conclusions
  require local tests and runtime proof; they establish no production incidence.

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
grammar, runtime RPC output and generated-client templates did not change
in the first batch. The second batch advances the current status schema, registry
and generated client together for explicit N6 event-loss evidence. Architecture
boundaries stay unchanged; the TypeScript specification already delegates exact
status identity to the checked schema. Those owning documents need no additional
update. Finite-read/report budgets, snapshot coverage, shell redirection and ZIP
publication remain later milestones, alongside the reproduced backend field cap.

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

The N6 runtime scenario first proves the existing native/SQL/HTTP round trip.
Then the disposable app sends 4,097 authenticated synthetic orphan trace events
to its own report endpoint, exceeding the documented 4,096-event capacity, and
sends their 33 summaries (at most 128 events per span) to transfer surviving
events into successful export. Confirm
capacity loss, retained backend events, unchanged existing loss units and current
serving identity. A separate natural SDK WORK span remains alive for 31 seconds
while its HTTP parent completes immediately. Its one start event must expire;
its summary, end event and duration metric remain queryable, and its exact loss
increment is one. An immediately completed operation adds zero. Scoped RPC and
the freshly generated development client must agree on current schema/status.
The named `observability` probe owns these HTTP/process/timer boundaries and
verified cleanup; ordinary tests use deterministic ages and no process, socket
or sleep. Strict TTL boundary, multiple occurrences, combined TTL/capacity,
app/session/trace/span key isolation and successful/repeated drains must prove
exactly-once accounting. No personal telemetry or installed runtime is involved.
Reversed/missing Claude timestamps retain outcomes but no duration; equal times
remain a valid zero-duration sample. Decoding-invalid coverage retains its
existing meaning; this small correction adds no timing-coverage counter.

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
