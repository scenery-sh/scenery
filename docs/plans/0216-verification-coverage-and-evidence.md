# Verification Coverage and Trustworthy Timing

This ExecPlan is a living document. Update Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Implement the October 7 follow-up to the telemetry review of `a9a23313`.
Plan 0215 is completed history and its reliability fixes remain intact. Every
validation result should identify what ran, what it proved, and where time was
spent. Preserve actual Go lifecycle events, explicitly own JavaScript tests,
retain immutable application results and referenced runner artifacts, qualify
timing comparisons, and collect representative frontend evidence.

## Progress

- [x] 2026-10-07 Read the supplied follow-up, live source state and owner contracts.
- [x] 2026-10-07 Read GitHub workflow metadata; the CI workflow is active.
- [x] Assign every conventional JavaScript test an owning lane and select it in CI.
- [ ] Obtain fresh remote CI execution after publishing final inputs.
- [x] Preserve Go lifecycle timestamps, complete discovery and failed preparation.
- [x] Archive application plans/results and export exact referenced run artifacts.
- [x] Retain normalized Bun/Node case results and separate compiler/setup stages.
- [x] Qualify durable timing baselines and expose verification freshness.
- [x] Collect frontend readiness, edit/interaction and controlled rendering evidence.
- [ ] Complete cumulative validation and required native probes; review all receipts.

### Resume here

2026-10-07: current native probes `20261007T025116.459149000Z` passed all seven
selected boundaries including dev-process with verified cleanup. Fresh full
`20261007T023112.713669000Z` passed correctness with complete native discovery:
99 packages, 2,435 exact roots and 3,475 terminal cases, no replay/parser errors.
It retained 7,880 isolated samples and 45 roots over 100 ms p95. One fixture-copy
defect exposed by generated caches is repaired: selected twenty-process catalog
confirmation `20261007T025756.111269Z` has 20 ms p95. Remaining timing findings
are retained warnings, not performance acceptance. Resource benchmark
`20261007T015701.970770000Z` passed all nine
1/5/10-worktree cohorts, all twenty handler generations per worktree and verified
owned cleanup with stable inputs. Backend edit benchmark
`20261007T012505.495870000Z` passed correctness; candidate median/p95
644.883/766.135 ms misses the observational 300/500 ms targets. Actual Bun/Node
negative run `runner-failure/runs/20261007T022917.882257000Z/result.json` retains
complete pass/fail/timeout/skip/todo evidence. The final controlled profiler has
twenty measured samples and two warmups per configuration. Earlier native
probe run `20261007T010243.139434000Z` and browser run
`frontend-browser/runs/20261007T010401.279753Z/result.json` remain scoped proof;
the latter records three component and three style HMR edits, three successful
interactions and byte-identical restoration. Next: finish current cumulative
proof, including dev-process after its resource-helper extraction, publish the
final commit and verify remote CI. Preserve Plan 0215 and unrelated scratch.

## Surprises & Discoveries

- The UI probe selects only two of the five conventional TypeScript test files
  under `internal/generate/testdata` and `internal/build`.
- The fresh runner reconstructs terminal JSON after package completion with one
  timestamp. It drops native `run`, `pause` and `cont` events.
- Recent repository Actions activity is Dependabot activity; the live repository
  Actions permission is disabled even though the CI workflow is active.
- Node JUnit writes its declared count in an XML comment and direct root cases.
  The first pilot retained raw passing output but correctly failed completeness;
  parsing now handles this format without manufacturing case duration/history.
- Bun 1.3.14 emits a nonfatal directory-mismatch diagnostic for external JSX
  tsconfig resolution; reporter terminals are complete and stderr is retained.
- Repository Actions permission was restored from disabled to enabled without
  changing the existing action allowlist or token permissions.
- Actual negative Node JUnit distinguishes todo through `skipped type="todo"`;
  Bun timeout uses `failure type="TimeoutError"`. Both are now preserved in the
  common outcomes. Missing terminals, duplicate identities and unexpected files
  disqualify completeness.
- Optional assistant readiness can follow required API readiness. Cost proof
  waits for a real deterministic Eve completion; it distinguishes readiness
  polling from retry outcomes. After source churn, ownership must be resampled
  until the complete generation has settled, rather than dropping stale owners.
- Cost measurement needs process membership/RSS, not descriptor enumeration.
  Extracting that smaller resource reader removes an unrelated `lsof` failure
  while the dev-process probe retains its descriptor/cache growth checks.
- The compiled catalog test copied 196 MB of fixture-local managed cache and
  generated projections. Copying only authored inputs preserves compilation
  assertions and reduces its isolated p95 from 1.823 s to 20 ms. The first
  cache-only exclusion retained a borderline 100 ms p95 and is preserved as a
  failed confirmation; the final measured result excludes both categories.

- The first published push CI reached the existing formatting gate and found
  six pre-existing Go files with whitespace-only differences. Normalize those
  files with gofmt, retain the failed run, then repeat current-head verification.

- That CI also retained successful JS cases while offline CLI/native fixtures
  failed on uncached transitive Go module metadata. CI now explicitly prepares
  the declared module graph in a temporary module before the offline probes;
  tracked go.mod/go.sum and fixture network isolation remain unchanged.

## Decision Log

- 2026-10-07: Preserve the separate cached, fresh, isolated-confirmation and
  explicitly selected benchmark lanes. The requested timing work authorizes
  native fresh execution and controlled measurements needed to prove its
  changes; release certification and global CLI installation are unselected.
- 2026-10-07: Keep shared measurement values in `internal/harnessreport`, artifact
  parsing/serialization in `internal/harnessevidence`, repository execution in
  `scripts/verify`, and application validation in its current product owners.
  Do not add a second scheduler or a product dependency on the verifier.
- 2026-10-07: Missing evidence, skipped selection, replayed results and blocked
  prerequisites are explicit outcomes. No inferred per-stage subtraction or
  percentile from an insufficient sample count counts as measurement.

## Outcomes & Retrospective

Implementation and representative measurements are complete; final cumulative
validation and current-head remote CI remain open. Full fresh discovery is
complete. The 45 original confirmed timing violations, fresh suite 18.472 s
versus 5 s and 70 test binaries versus 60 remain explicit evidence; after the
selected catalog repair, the other recorded violations are outside this
instrumentation change's performance acceptance. Owners are the affected
packages and the existing test-loop plan 0145. No release or all-root timing
certification is claimed. The resource result covers a
minimal Bun frontend and a real Eve helper with a deterministic model, not a
bundler/browser workload or remote model latency. Native startup peaks are
one-second sampled RSS maxima; Docker startup peaks and PSS remain unmeasured.
Three repetitions are retained individually without a cohort percentile or
capacity estimate. All forty-eight measured worktrees completed twenty unique
served handler generations (960 attributed responses), with idle/load and
post-churn observations. Failed resource pilots and their verified cleanup
remain in immutable history; the successful final run replaces no records.

Actual consumer-browser component/style HMR and dialog interaction are separate
from framework route/module readiness and React test-renderer timing. No browser
layout/paint or whole-application performance improvement is inferred. The
backend observational latency targets remain missed and are explicitly recorded.

## Plan of Work

1. Add one JS test ownership inventory with exact lanes for Bun conformance,
   table behavior, runtime identity, prepared native reference server and the
   generated Node assistant helper. Validate conventional-file coverage.
   CI runs ordinary Go verification and the explicit service-free JS lane and
   uploads immutable run/artifact directories even after failure/cancellation.
2. Execute reusable binaries through the selected Go tool's native `test2json`
   stream. Preserve timestamped lifecycle events and live output; retain partial
   preparation costs and failed links. Reconcile started/terminal discovery and
   parser failures; incomplete evidence disqualifies mandatory timing proof.
   Use an explicit native fixture to prove a slow parallel-child parent enters
   candidate selection and isolated confirmation.
3. Archive each application validation result and its resolved selection before
   updating latest navigation copies. Include run/input/producer identity, dry
   run, exact command stages, omitted reasons, artifact references and outcome.
   Extend bounded telemetry export to include matching immutable evidence and
   referenced raw files, with completeness and missing-file reasons.
4. Use Bun JUnit and retained Node TAP/JUnit to report full case identities,
   durations, outcomes, attempt, runner version and complete counts. Retain
   successful and failed raw output. Keep overlay generation, runner, dependency
   preparation and `tsc` costs separate. Replace console-substring acceptance.
5. Bind fresh timing baselines to immutable runs and comparable host, toolchain,
   concurrency, mode and cache definitions. Cached correctness must not erase a
   fresh baseline. Expose latest applicable proof and CI metadata with explicit
   missing/incomplete lanes. Keep source differences as the comparison subject.
6. Strengthen table behavioral assertions, repeat the existing controlled React
   profiler with workload/toolchain identity, and preserve its renderer boundary.
   Record frontend listener/route/module readiness, production-rebuild terminal
   spans, observability health and exact cache/edit classes. Collect a native
   browser scenario with actual HMR applied, rendered generation and successful
   interaction; retain full reloads and uncaught errors separately. Collect the
   existing controlled edit-latency result with correctness and target status
   distinct. Extend resource cohorts with a managed frontend, real deterministic
   Eve helper, sampled native startup peaks and post-20-edit idle observations.
   Retain selected trace evidence and the boundary between controlled fixtures
   and consuming-application performance.

## Validation and Acceptance

All repository commands run from the Scenery root. Source and contract changes
select full `go run ./scripts/verify --summary --write`; read the immutable
`agent-context-summary.json` first, then fulfill the exact `agent-context.json`
command union, reusing successful same-input/scope receipts. Always run
`golangci-lint run ./...` and the cumulative generator/TypeScript matrix for
changed generation or catalog inputs.

The testsuite owner requires `go test ./internal/testsuite`,
`go run ./scripts/testsuite -run 'a^' -record-timings=false`, and
`go run ./scripts/testsuite`; retain the latter as explicitly requested fresh
execution evidence. Run `go test ./scripts/verify ./cmd/scenery ./internal/machine`
for report/parser contracts. Run
`go run ./scripts/verify --probe test-cache --probe ui --probe frontend --probe assistant-helper --probe native-contract --probe validation-git --summary --write`
for changed external runner/public-validation boundaries; inspect their exact
assertions and owned cleanup. Add and select the named Go lifecycle fixture
probe when implemented. Use `.scenery/harness/bin/scenery` for product commands.

Controlled measurement selection is
`go run ./scripts/verify --benchmark edit-latency --summary --write` and
`go run ./scripts/verify --benchmark worktree-cost --summary --write` and
`bun run --cwd tools/typescript profile:query-table` after checking their current
grammar; record actual commands and input identity. They prove their declared
fixtures, not all application workloads. Browser work uses the running native
Chrome profile in the background. Establish a successful route/module response,
apply an owned reversible frontend edit, observe the matching HMR/rendered marker,
and exercise one representative interaction. Missing browser access or external
CI execution is an exact unresolved boundary, never a passing result.

## Idempotence and Recovery

Use run-owned private homes and disposable fixture processes. Preserve all
retained app data and live owner identities. Latest files are navigation copies;
immutable run directories and hashes establish evidence. Failed or canceled
execution retains available artifacts and marks missing terminals explicitly.
Restore owned temporary frontend edits and stop owned fixtures on every exit.
Do not reset/stash unrelated work, install the shared binary, modify `VNEXT.md`,
or rewrite completed numbered plans.
