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
  `https://chatgpt.com/c/6ac77146-7eb8-83eb-826f-b9cb242b9eb6`. The entire final answer and immutable references are captured locally; exact
  monitor `telemetry-oracle-cycle-003` is deleted with absence verified.
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
- [x] Complete N6b backend admission evidence: pinned same-owner counter delta,
  exact healthy control event inventory, rejected span absence, distinct loss
  units and unchanged baseline/TTL/client/cleanup acceptance.
- [x] 2026-10-08 N6b actual backend admission proof passes: same pinned owner
  and stable binary hash, known counter zero-to-one, exact 196-event control at
  1,000 fields, absent rejected trace at 1,005 fields, confirming diagnostic and
  unchanged Scenery loss counters. Final full `20261008T112628.810591000Z`,
  observability `20261008T112205.059409000Z`, CLI-process
  `20261008T112509.045832000Z`, lint and 22-root isolated timing pass. Corrected
  the earlier report-test gofmt omission observed in remote CI; the local full
  formatting check passes. The published push and PR CI jobs subsequently passed.
- [x] 2026-10-08 Fourth review submitted exactly once with GPT-6 Pro and the
  actual GitHub connector, bound to published `b996b28815f9a207d099b785ac411386fc23d1a9`,
  original base and draft PR #240. Push CI checks out the exact head; PR CI
  checks out a synthetic merge with the identical tree. Canonical
  conversation is `https://chatgpt.com/c/6ac78102-d1f0-83eb-8847-1b0bcc340ff1`;
  the complete answer is captured, and exact `telemetry-oracle-004` monitor
  deletion and absence are verified. The independent overall loop stays active.
- [x] 2026-10-08 Independently confirm the fourth review's preliminary null-label
  concern through public `QueryMetrics`: vector, matrix and after-limit null
  labels silently become empty strings. A test-only overlay fails those three
  cases while preserving valid empty labels and refusing other JSON kinds.
  Local correction validates decoded label types before normalization; all nine
  before-proof cases and focused observability package tests pass afterward.
- [x] 2026-10-08 Reproduce ZIP publication overwrite through the actual prepared
  worktree CLI. An explicitly included disposable FIFO synchronizes staging and
  a competing exclusive destination creation; the CLI replaces that completed
  competing file with a complete ZIP and exits successfully. Initial-existing
  destination refusal, child exit and owned disposable cleanup are verified.
  This establishes overwrite capability, not production incidence or a regular
  file race frequency; permanent correction and named acceptance remain open.
- [x] 2026-10-08 Complete null-label development validation: full
  `20261008T115207.245305000Z`, lint zero issues, four modified observability
  roots with 20 isolated samples each below 1 ms p95, and named observability
  `20261008T115713.218092000Z` pass with owned cleanup. These receipts precede
  the additional admission-owner refinement and do not prove its acceptance.
- [x] 2026-10-08 Independently confirm the general `VerifyOwner` policy permits
  missing observed fields and executable-basename equality. Scope the correction
  to admission evidence: capture live ownership before and after each metrics
  request, require complete exact PID/start/executable/command equality, and
  retain those observations in the artifact. New missing-field/basename cases
  exercise the pure comparison; focused packages pass. Global owner policy is
  intentionally unchanged; current full/lint/runtime acceptance now passes.
- [x] 2026-10-08 Capture and assess the whole fourth answer (29,035 characters,
  all immutable sources). Accept null-label validation and local strict-owner
  proof refinement; retain qualified CI identity, report/read limits,
  redirection, ZIP correction and delivery prevention as separate work.
- [x] 2026-10-08 Strict-owner named observability
  `20261008T120919.934915000Z` and CLI-process
  `20261008T121238.958537000Z` pass at stable input
  `sha256:4fe1baab4da2e16f3879bea0f9a1cfbf440680f24d3577980680154ddca0d8d9`.
  All six stored/live fingerprint records agree on complete PID/start/full
  executable/command identity; counter delta is one and all 196 control events
  survive. Baseline/TTL/client and owned cleanup pass. Serving build input is
  `sha256:f0304a40e67f318369a782aaae779f58b5a0bc2ab31f185e2e0ed5b9bad03dff`.
  Supplemental owned synthetic HTTP proof exercises actual public QueryMetrics:
  four null/mixed/after-limit refusals and three valid empty-label/object/special
  value controls, exact timestamps, sanitized errors and listener cleanup.
- [x] 2026-10-08 Fourth-batch final full/default
  `20261008T121438.390242000Z` passes with stable input
  `sha256:0f209d0a7d784ed59d1ffd48edc524d1936f9639fab2be3809cd3d0a67a10cbc`.
  Zero errors, lint 0 and repository-wide gofmt pass; 12 existing knowledge
  warnings and a 5.061-second suite advisory. Full and both named probes share
  framework source `sha256:224464940a295c6e3a2076b7a27bf0ea8f190355ee0254bb3e7daf8ba7c24aef`.
  Seven changed roots are remeasured after final test-case deduplication; all 22
  cumulative changed roots pass 20 isolated samples, max p95 10 ms. Fifteen
  unchanged relevant scopes and unchanged generator/Bun/TS receipts are reused.
- [x] 2026-10-08 Commit independently reviewable fourth-batch fixes:
  `e473e336` refuses non-string metric labels; `2425ed0a` requires complete
  observed telemetry-backend ownership. Only the living-plan checkpoint remains.
- [x] 2026-10-08 Publish fourth-batch checkpoint
  `40d126590842a02a9dda444721625ef277016ff6`; documentation quick
  `20261008T121710.115124000Z` passes. Draft PR #240 exact head/base/body and
  clean checkout verified. Push CI37775859071 passes on that head; PR
  CI37775866755 passes on merge a8fba7d9 with the identical tree. Checkout logs
  are independently opened; neither job runs observability or CLI-process.
- [x] 2026-10-08 Submit one fresh complete GPT-6 Pro/GitHub review005 at
  `https://chatgpt.com/c/6ac78b34-44d0-83eb-b40c-e62d9560d5dc`, bound to that
  target, original base and PR #240 progress. Complete primary text is verified,
  no attachment/overlay. The entire final answer and immutable/grouped sources
  are captured and independently assessed; exact telemetry-oracle-005 is
  deleted with absence verified.
- [x] 2026-10-08 Complete and publish ZIP no-replace/collision correction as
  `c90fe94dd439095a8f928c558dc26cae8db2d2fa` on draft PR #240. Final full/default
  `20261008T125809.219733000Z` and named CLI-process
  `20261008T125850.233062000Z` pass at stable input
  `sha256:a6162dc33b6171040267173ab7ac9d1a9b9a0858a0f8aa1dfa48a5cc922baf5e`,
  compiled framework source
  `sha256:d4a16821b8645bcba441081244dda76fd534b35cbc2ceb08e241be96d387bed8`.
  All eight actual CLI publication scenarios pass; two writers are staged and
  rendezvoused before release, exits 3/0 with one complete hash-matching winner.
  Existing competitor bytes/inode survive, all owned children/descriptors/stages
  and temporary roots are cleaned. Lint zero issues, 1,789 Go files format clean.
  Cumulative 24 changed roots each have 20 isolated serial samples, max p95 90 ms;
  two ZIP roots are remeasured (90/10 ms), other 22 unchanged scopes reused.
  Zero errors, 12 knowledge and 5.176 s suite advisories. The first probe failed
  only because its hash comparison omitted the manifest's existing `sha256:`
  prefix; correction and final full/lint/probe are complete. Historical
  observability/source224464 and generator/Bun/TS proof is reused only for
  unchanged owning scope; it does not share the new ZIP digest.
- [ ] Complete cumulative validation and update the draft PR for each subsequent
  implementation batch.
- [ ] Start the next consultation with the draft PR number and exact current
  head, repeating until Petr stops the loop.

### Resume here

Petr's loop remains active and unbounded until manual stop. ZIP source
`c90fe94dd439095a8f928c558dc26cae8db2d2fa` and documentation checkpoint
`b4eb34ac1c18e4bc93ba5ac8876e82297c63ab27` are published to draft PR #240.
Six whole reviews with immutable source links are saved and assessed; their
exact monitors are deleted with absence verified. Current b4eb CI is green on exact-head push and an
independently checked identical-tree PR merge. Neither CI lane runs observability
or CLI-process. Prior ZIP full/default, lint, 24 changed-root timing and eight
actual publication scenarios remain recorded above with their scope limits.

Oracle006 completed at
`https://chatgpt.com/c/6ac79593-b1ac-83eb-9366-13ae0ba61340` against b4eb/base,
naming PR240 progress. Its entire 32,140-character final and 45 DOM links are
saved and independently assessed; exact monitor `telemetry-oracle-006` is deleted
with absence verified. Raw token validity, independent cache counters and truthful
human/latency wording refinements were accepted. Successful trace-buffer drains
add no loss themselves, but can trigger the existing lazy TTL pruning; this is a
wording qualification, not a behavior change. The overall loop remains active.

Actual owned CLI before-proof confirms supervisor request/child negative
percentiles, fractional-negative false zero, unrepresentable durations/sums and
cache corruption, including a sanitized serialization failure. This is synthetic
malformed input proof, not normal producer incidence. Local correction validates
individual durations and child sums before integer conversion, preserves valid
fractional sums/zero and known untimed outcomes/joins/streaks/terminal cleanup,
counts invalid source timing, and keeps cache cohorts coherent. Six focused
roots cover before/after timing, raw token, joins, cache and latency evidence;
affected package tests pass. The cache report contract now retains all decoded
non-timing work with required `timing_sample_count` and nullable
`accumulated_ms`, advancing its checked identity and static registry together. Complete cumulative
full/lint, changed-root timing and the named actual CLI/schema/owned-cleanup
probe before publication. Update the same draft and start fresh Oracle007 at the resulting exact head,
original base and current PR progress after this validated publication.

N7 snapshot/coverage/aggregate budgets, N8 finite query limits, broader redirection
attribution and lossless delivery prevention remain separate. Oracle006's live
source progress also identifies a separate MetricsSeries null-label decoder
candidate; verify it independently before scope selection. Generator/status/
client and observability inputs remain unchanged: reuse receipts only for those
owning scopes, never claim old observability matches the current source digest.
Do not add arbitrary caps, truncate detail, modify VNEXT or restart a live agent.

Durable questions, answers, assessments, proofs and active process state live
under `.scenery/telemetry-improvements/`. Read live Git/PR/scheduler/runtime state
before relying on this checkpoint. Preserve unrelated data and healthy pending
generation; repeat validated publish/review cycles until Petr stops.

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

- 2026-10-08: Select N6b after the complete third review and independent upstream
  source inspection. VictoriaTraces v0.9.2 expands each event into row fields,
  rejects rows above 1,000 fields, and increments its existing rejection counter
  without making OTLP HTTP completion fail. Ingested-row counts precede admission.
  Preserve production encoding and detail semantics; add named runtime evidence,
  not an arbitrary cap, span fragmentation or a delivery guarantee.

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
update. Finite-read/report budgets, snapshot coverage, shell redirection
remain later milestones; supervisor timing is the current bounded correction, alongside the reproduced backend field cap.

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

N6b acceptance uses the same owned disposable app and production authenticated
report/export path. One control span stays below the pinned backend's 1,000-field
row limit; a second exceeds it. Their total event inventory stays below 4,096 and
finishes within the 30-second buffer TTL. Observe the backend rejection metric
before/after, require a known exact delta of one, and bind both snapshots to the
same verified process owner and pinned binary. Missing metrics, reset or changed
identity fail evidence, never become zero. Require the control's exact event
names, timestamps, structured ordinals and uniqueness through scoped RPC; pair
rejected-span absence with that healthy query and unchanged buffer/export-loss
counters. Keep the earlier native/SQL/HTTP/context/log/metric/client/status/TTL
surface and cleanup. Application intake204 is directly observed; exporter success
is a source-supported inference unless its response is directly captured. This
scenario proves backend rejection evidence, not prevention or delivered detail.
Run the named `observability` probe, cumulative full/default, lint and focused
service-free evidence tests; no release gate or all-root timing audit.

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
