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
- [x] 2026-10-08 Complete Oracle006 at the exact b4eb checkpoint, save its
  32,140-character whole final and 45 DOM source links, independently assess,
  delete the exact monitor and verify absence. Accept raw timing/outcome
  separation, fractional/zero preservation, poisoned child cohorts, adjacent
  phase range/accounting, cache work preservation and truthful human findings.
- [x] 2026-10-08 Commit supervisor timing correction as `c1ff8939`. Six new
  focused roots plus the revised owning CLI report fixture pass. Raw-token
  before proof overlays exact b4eb source with a retained test snapshot; an
  incomplete helper overlay failed compilation and is excluded as intake proof.
  Actual product before proof and normal-producer incidence limits remain explicit.
- [x] 2026-10-08 Final full/default `20261008T134004.028032000Z` and named
  CLI-process `20261008T134108.894459000Z` pass at stable input
  `sha256:be752f6932519a60d3fba9680632b329e717548a10458dfe821ea45c43ed4b4f`,
  shared framework `sha256:80e805583aabd5b7b503207382b982ba69ddb619c68cb6bb4cba97adbcc9b4cf`.
  Lint 0; 1,780 non-ignored Go sources format clean. Full covers the changed-area
  union including machine identity, vet, architecture, drift and checked schemas;
  suite 4.474 s, zero errors and 12 existing knowledge warnings. The first CLI
  proof exposed a human display gate hiding invalid-only supervisor evidence;
  fixed it, added the owning fixture and repeated final full/lint/probe.
- [x] 2026-10-08 Actual report JSON/human processes preserve 18 rebuild outcomes,
  5 failures and 4 timed successes (p50 10/p95 20), initial failure/error causes,
  named/legacy joins, streaks, terminal cleanup, superseded/deferred, fractional
  sums/zero and following records. Twenty invalid timing records are visible;
  raw missing/null/mistyped/negative-underflow/positive-overflow tokens cannot
  invent samples. Cache counters survive independently, required timing sample
  counts expose partial nullable subtotals. Checked report identity advances to
  `sha256:8269c9d267acbbebf42bb74a608dd6ab3eef9d5afdab719333c4435db7f7048a`.
  Envelope/payload schemas, source byte hash/immutability, actual child exits and
  owned root removal are verified. Existing eight ZIP scenarios still pass.
- [x] 2026-10-08 Thirty cumulative changed exact Go roots each have 20 isolated
  serial samples, p95 maximum 90 ms. Remeasure five existing telemetry roots,
  six new roots and the CLI report root (maximum 60 ms); reuse 18 unchanged
  owning/exercised scopes. Runtime/query/status/backend and generator/client
  proofs remain historical and scoped to unchanged inputs, not the new digest.
- [ ] Start the next consultation with the draft PR number and exact current
  head, repeating until Petr stops the loop.

- [x] 2026-10-08 Publish supervisor source `c1ff8939` and documentation
  checkpoint `2f810c2ad9931fc7e13a3702980a69e036c14913` to the same OPEN draft
  PR #240; exact-head push and identical-tree PR CI both pass. Current remote
  suite advisories are 30.467/30.622s, distinct from the final local 4.474s.
- [x] 2026-10-08 Submit fresh Oracle007 once at the exact 2f810/base checkpoint
  with actual GitHub and verified GPT6/Pro5of5. Save its whole 26,355-character
  final and 39 full DOM links, independently assess the P2 catalog correction,
  and delete exact monitor `telemetry-oracle007` with absence verified.
- [x] 2026-10-08 Independently reproduce MetricsSeries null-label conversion and
  adjacent misleading empty/limited success through 16 public Go calls against
  an owned HTTP responder, built while the target checkout was clean. Retain
  inputs/outputs, actual PID, source/build/binary hashes and listener/server
  cleanup. Supplemental 24-call evidence restores the exact pinned query source
  through an explicit overlay and excludes the unused candidate helper: it also
  confirms emitted null rows, query-shape acceptance and MetricsLabels null/shape
  coercion. The initial supplemental directory-form build was a preparation
  failure, excluded from evidence; explicit main-file compilation succeeded.
- [x] 2026-10-08 Implement endpoint-specific raw catalog validation for series
  objects/string labels and label-name strings, through all omitted rows. Keep
  valid empty distinctions, ordering, literal contents and current public shapes.
  Five service-free public roots and the existing catalog test pass, with request
  scope/bounds, cancellation, body closure and valid calls after rejection.
- [x] 2026-10-08 Final full/default `20261008T142427.364255000Z` and named
  observability `20261008T142758.183438000Z` pass at stable input
  `sha256:516148bb204d7858506aec0c634cbba0621ab54ba7a6fdcaaf4c456fabdd5e0c`
  with shared compiled framework source
  `sha256:5360ce90d6b66095e1b793008a596075a95f2d1f9e82867f7f3cee75c923fd0a`.
  Summary-first/full-context command union is covered by the full Go suite and
  matching-input lint (exit0); all 1,795 Git-listed Go sources are format-clean.
  Thirty genuine public catalog HTTP cases verify method/path/selector/bounds/
  repeated scope, every malformed omitted row, precise empty/string controls,
  current schemas/identities, private-detail-free validation errors and joined
  listener/server cleanup. The actual real-Victoria CLI series control returns
  18 series with enforced scope/current identity. Preserve evidence distinction:
  synthetic public Go calls do not establish malformed-input CLI exit behavior.
- [x] 2026-10-08 Current named runtime host PID23996/service PID23969, 28 spans
  and build-input identity
  `sha256:e46d8f714907e2a0f52fec15d34d96314958d60a5897322e8e9aa4d446243144`
  bind SQL/HTTP/log/metric/RPC/generated-client/trace-buffer/admission evidence.
  Capacity evicts7, natural TTL evicts1, backend independently refuses1 row;
  existing complete owner fingerprints/units remain explicit. Owned runtime,
  Victoria and PostgreSQL cleanup succeeds and the owned root is verified absent.
- [x] 2026-10-08 All 36 cumulative exact changed roots retain 20 isolated serial
  samples/p95<100ms (maximum retained90ms). Remeasure the10 query/catalog roots
  against current inputs; their event-rounded p95 is0s, not an exact wall-time
  claim. Reuse26 unchanged owning/exercised scopes with historical binary/source
  identities. Full has12 existing knowledge warnings and a7.955s suite advisory;
  named observability has12 knowledge warnings/zero errors. Source correction
  `55835630434cac364560d8bee8ae0d8512a67960` is committed locally; its
  documentation-only checkpoint retains the same Go source and acceptance.

- [x] 2026-10-08 Oracle008 completed against published `fc7f545a`/original
  base and draft PR #240 at
  `https://chatgpt.com/c/6ac7afa1-cc84-83ed-8e7a-98ad6fd8f8ae`.
  Whole 34,072-character final, 17 grouped sources and 55 full DOM links saved;
  independently assessed, exact `telemetry-oracle008` deleted/absence verified.
  It recommends pre-existing P2 N7a descriptor extents and actual byte coverage;
  targeted connector review ran no Go/product/OS experiments or local audit.
- [x] 2026-10-08 Three actual prepared-CLI before proofs confirm N7a: a
  captured 64 MiB supervisor file truncated after an observed child read retains
  17,152 of 131,072 valid records yet says complete; a CLI file includes 64 rows
  appended after reading begins; duplicate hard-link segment names report two
  partial logs for one logical source. Exact owned-child descriptor/read offset,
  stop/mutation/resume, producer/source/executable identities and cleanup saved.
  Initial macOS pathname normalization missed the rendezvous without mutation;
  excluded preparation/control and all owned roots/children cleaned. This proves
  capability, not normal incidence, transactional snapshots or exhaustion.
- [x] Complete N7a report-only captured/read coverage, checked schema identity,
  focused controls and actual named CLI append/truncate/rotation/transcript proof.
  Observable acceptance: report JSON/human exposes captured S and consumed B;
  append after capture is excluded, truncation retains complete prefix and
  B<S/partial, intact split rotation preserves one logical record, known empty
  differs from unknown, and byte coverage includes skipped evidence. Preserve
  timing/outcome/correlation/normal self-recording controls and owned cleanup.

- [x] 2026-10-08 N7a full/default `20261008T155455.481943000Z` and
  named CLI-process `20261008T155536.906842000Z` pass with stable input
  `sha256:8db21d15acf0b5d096d875f87be32800044118aa504ce80f6d01dabe6f090118`,
  compiled source
  `sha256:a3365780d43f3035f153c9c86d2002b1b70a60d91efe63e3a49b0d419211099e`.
  Full covers the changed-area Go union, vet, architecture/drift and schemas;
  lint zero issues and all 1,801 Git-listed Go sources formatted. Both runs
  have 12 existing knowledge warnings and zero errors; full Go step wall is
  5.102 seconds, separate from per-root timings and remote CI.
- [x] 2026-10-08 Eighteen actual prepared report children pass: twelve JSON
  envelopes/current report schemas and six human invocations with separate
  fixtures. Active/archive CLI and Claude append bytes remain outside captured
  extents. Supervisor JSON/human truncation retains 10,223,616/10,158,080 bytes
  from 67,108,864 captured; Codex retains 6,160,384/6,225,920, preserving the
  original known command. Strict complete PID/start/executable/command-line
  fingerprints, confirmed owned child stop and exact opened inode/position
  establish capture before mutation. Genuine writer rotation after all four
  descriptors are open retains all 33,554,432 captured bytes; genuine single
  Write splitting a JSON row and valid final JSON without newline are accepted.
  Empty zero versus capture-failed null, duplicate logical partial count one,
  both opt-in transcript formats, valid zero Claude timing and absent Codex
  timing pass. Native report self-recording stays enabled. All 18 exact PIDs
  and private roots are independently verified absent after owned cleanup.
  Raw outputs/identities are in the immutable probe and ignored local receipts.
- [x] 2026-10-08 Fresh CLI proof also retains the eight actual ZIP publication
  scenarios and malformed supervisor timing/outcome/cache/zero/fraction/join
  controls on the current source. Historical CLI binaries do not certify N7a.
  Final report full-document/static schema identity is
  `sha256:450fc94a0691497999230d9633c4142f89cbc0f2f2b009508a29b5a272394554`.
  The first snapshot probe could not observe the fast-filtered Claude input,
  performed no mutation and failed closed; decoder-engaging ordinary ignored
  JSON rows fix the fixture without changing the product. Lint corrections
  were followed by final full/lint/probe runs. Failed fixtures/children cleaned.
- [x] 2026-10-08 All 45 cumulative changed exact roots retain twenty isolated
  serial samples and p95 below 100ms. Twenty-one report/CLI/schema roots are
  remeasured (maximum 50ms), twenty-four unchanged owning/exercised scopes keep
  original binary identities (retained maximum 90ms). This is not an all-root
  audit; rounded zero event times do not imply zero wall time. Unchanged
  observability/runtime/generator/status/client inputs retain earlier named
  runtime/generation/56 Bun/both TypeScript receipts with their original source
  and serving identities; no current whole-binary or Darwin/Linux equivalence.

- [x] 2026-10-08 Publish N7a at `7bd2f9e` and verify draft PR240,
  remote ref, body and both current CI checkouts/logs/identical immutable trees.
  Fresh Oracle009 used actual GitHub/GPT6 Pro with one complete primary; its
  whole final (25139 characters/42 complete source links) and assessment are
  saved, exact monitor deleted and absence verified.
- [x] 2026-10-08 Independently reproduce two pre-existing report defects:
  actual prepared JSON/human supervisor input gives global invalid1 but
  source invalid0/complete; actual shell bare native help records help, while
  four Claude/Codex JSON/human reports omit sole-invocation sessions. Inputs,
  actual outputs/producer/children and owned cleanup are retained privately.
- [x] Implement Oracle009's bounded report evidence corrections: shared
  invalid accounting, bare invocation help identity/session inclusion, exact
  post-stop read descriptor/extent/remaining-byte validation, and source-bound
  numeric human expectations. Add focused ordinary fixtures plus actual CLI
  malformed-error/native-help/transcript and late-read rejection controls.
- [x] Initial full/lint and24 remeasured root timings pass; named CLI
  acceptance rejects its extra late-case fixture because the child completed
  between redundant descriptor observations. No mutation occurred, raw evidence
  and cleanup are preserved. Reject an already observed endpoint with the same
  typed predicate before any unneeded stop; accepted mutation cases still
  require confirmed-stop validation. This corrects the probe schedule only.
- [x] Final full/default `20261008T163646.095234000Z`, lint zero issues and
  CLI-process `20261008T163722.157983000Z` pass stable input `d155d0aa`,
  framework source `32bf0347`. Full suite advisory5.014s and12 existing
  knowledge warnings are separate from isolated p95; zero errors. Gofmt1806
  Go sources passes. Forty-nine cumulative exact roots each retain20 isolated
  serial samples/p95<100ms:24 remeasured maximum40ms,25 unchanged scoped
  receipts keep original binary identities/maximum90ms. The later late-fixture
  correction changes no exercised ordinary predicate/report/CLI test source.
- [x] Fresh named report acceptance covers25 actual report children (16 JSON
  with current envelope/report schemas,9 human on separate fixtures) and four
  actual owned shell bare-native children. Malformed error yields global/source
  invalid1, partial source, physical B=S, read-partial0, valid failure join and
  exit0. Bare native help records actual telemetry, keeps sole-invocation
  Claude/Codex sessions and identity/outcome; actual elapsed Claude versus nil
  Codex timing and earlier zero-duration controls remain distinct.
  Every accepted mutation rechecks selected read-only inode/device/extent
  inventory after confirmed stop, with consumed and remaining bytes; actual
  read endpoint is rejected by the same predicate before an unneeded stop,
  without mutation. Exact numeric human source/status/outcome controls pass.
  Original18 snapshot controls, eight actual ZIP scenarios and supervisor
  timing/cache/joins remain green. Raw before/failure/after evidence is saved;
  all25 report/four native exact PIDs and owned fixture roots independently
  absent. Producer7bd+dirty retains Git metadata built-at16:00:08; actual build
  step16:36:46 and executable SHA `6af7f010` establish this fresh binary.
  Darwin execution does not certify Linux observer execution, a real agent UI,
  normal incidence, atomic contents, totalmemory, delivery or release.

- [x] 2026-10-08 Publish report evidence at `397b058`, verify clean remote,
  same draft240/base/body and both exact current CI checkouts/logs/trees. Fresh
  Oracle010 whole final (19133 characters/47 full DOM links) and independent
  assessment are saved; its exact monitor is removed and absence verified.
- [x] Independently reproduce residual null/missing error-shape poisoning in
  ten actual prepared JSON/human report children, preserving known failure1
  while source/global invalid0 and complete B=S hide the lost later valid cause.
  Numeric wrong-kind remains a corrected control. All input and exact owned
  PID/private-root cleanup evidence retained. Input type unchanged at base/prior.
- [x] Select Oracle010 findings1/3: validate cause evidence before consuming
  error joins, retain explicit empty strings/diagnostic-only historical causes
  and optional operation IDs; require full human CLI totals/help outcomes,
  malformed rebuild/failure/cause counts and actual bare-Claude timing versus
  unavailable Codex percentile. Red ordinary counterexamples retained; add
  null-field/null-data fresh named cases and exact source/semantic assertions.
- [x] Final full/default `20261008T171534.216905000Z`, lint zero issues,
  formatting and CLI-process `20261008T171757.277154000Z` pass the same stable
  input `c03479b3`/framework source `64992087`. Full command union/vet/arch/drift/
  schemas passes; full suite5.322s advisory and12 knowledge warnings are separate
  from isolated timing, zero errors. Initial staticcheck selector correction
  was followed by final full/lint; raw attempt evidence retained.
- [x] Fifty cumulative changed exact roots retain20 isolated serial samples and
  p95<100ms:25 remeasured after final source edits, maximum40ms;25 unchanged
  owning/exercised scopes retain original binary identities, maximum90ms.
  No all-root audit or current whole-binary equivalence.
- [x] Fresh named acceptance has29 actual report children (18 current-schema
  JSON/11 human) and four real owned native-shell children. Numeric/null-field/
  null-data errors all yield global/source invalid1, partial B=S, read-partial0,
  one known failed outcome and its valid subsequent cause/count1, exit0.
  Exact CLI totals/help outcomes, rebuild/failure/cause counts and actual Claude
  elapsed timing versus unavailable Codex percentile pass in fresh human cases.
  Ordinary controls also preserve explicit empty strings, diagnostic-only
  historical causes and optional operation IDs. Original snapshot controls,
  eight ZIP scenarios and supervisor timing/cache/joins remain green. Raw proof
  is saved; all29 report/four native exact PIDs and29 private fixture roots
  independently absent. Actual prepared executable SHA `43d0ba9c`, build step
  17:15:34, producer397b+dirty with Git built-at16:41:03 metadata distinguish
  the fresh executable from its metadata. No native null-incidence, real agent
  UI, Linux, complete retention, totalmemory, delivery or release certification.


### Resume here

Petr's loop is active until manual stop. Oracle010 reviewed published draft
PR #240 at `397b058` against original base89fdc46 and prior7bd. Whole final and
assessment are saved; exact monitor removed. Selected report shape/semantic
assertion changes are implemented and accepted as recorded above. Run the docs
checkpoint with unchanged source hashes, publish to the same draft, inspect
current CI and start fresh revision-bound Oracle011.
Live Git/PR and `.scenery/telemetry-improvements/state.json` own changing heads,
validation/publication/consultation facts; do not duplicate an active operation.
Keep source/input identities when reusing successful checks. Older named
observability and generator/client receipts retain original identities and
apply only to unchanged owners, not the changed report binary. Use native
running Chrome in the background, preserve healthy generation, capture whole
final/source links before exact monitor removal and assess advice independently.

Standalone telemetry query, N7b aggregate/state budgets, N8 finite backend
responses, outer metrics/log framing, active redirection attribution and delivery
prevention remain separately scoped. These file extents do not prove atomic
content, all-reader/total-memory bounds or complete retention. Retain other
receipts only for unchanged owning/exercised scope with original identities.
Do not truncate detail, add arbitrary caps, edit VNEXT or restart a live agent.

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
remain later milestones. Supervisor timing is published; typed catalog validation
has now completed its own cumulative and actual HTTP/runtime acceptance.

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
