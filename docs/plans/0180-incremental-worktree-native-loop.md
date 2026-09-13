# Incremental Worktree-Native Development Loop

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current. It follows
[PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Make the ordinary Scenery development loop incremental and worktree-native
without changing the application-facing language, Go APIs, command grammar,
machine envelopes, generated contracts, runtime behavior, deployment artifacts,
identity meanings, or retained-resource ownership. The measured user outcome is
the time from completion of a real Go handler-body edit until the application's
normal advertised HTTP endpoint returns the genuinely new behavior from the
exact new execution generation.

On the explicitly recorded reference machine, the acceptance target for warm
implementation-only Go handler edits is p50 at most 300 ms and p95 at most
500 ms across at least 30 new, non-repeating compiled behaviors after a separate
warmup. Cold build, unchanged warm start, declaration change, shared Go
dependency change, module/config change, and embedded/native input change are
reported separately. A build, link, process launch, stale response, cache hit,
or successful preflight is not edit acceptance.

This plan succeeds only through implementation and evidence. It first makes the
existing path attributable, removes the root public SDK's unnecessary runtime
linkage, introduces one authoritative captured input set and narrowly keyed
incremental computations in the existing long-lived development owner, and
shares immutable work without sharing mutable session authority. A stable
framework host plus replaceable Go application worker is a measured prototype
and is promoted only if all affected contracts pass and the real workload is
faster. Otherwise the proven earlier slices remain and the precise runtime
artifact floor stays open.

ExecPlan 0179 remains active historical evidence for its unmet 50% goals. Its
final same-source edit median was 5461.476051 ms against 3153.377720 ms and its
unchanged-start median was 4032.029584 ms against 4017.245375 ms. This plan does
not overwrite, complete, or reinterpret those thresholds. It investigates the
architectural boundary that 0179 explicitly left for a separate decision.

## Progress

- [x] (2026-09-13) Confirm the actual checkout is clean `main` at
  `27ebaf12ca87355c6ee7ed340d1be63ffbe814f7`, matching the reviewed reference;
  do not reset or downgrade it.
- [x] (2026-09-13) Read the root and applicable child instructions, `PLANS.md`,
  `ARCHITECTURE.md`, active/debt guidance, affected local/spec/harness contracts,
  ExecPlan 0179, and relevant 0178 evidence.
- [x] (2026-09-13) Allocate this next unused permanent plan number, record the
  contract matrix, baseline commands, phase attribution, validation union, and
  promotion criteria before implementation.
- [x] (2026-09-13) A: capture a live baseline on the current checkout and extend the existing
  `build.step`/development evidence with bounded phase, queue, cache/action,
  written-file, rebuilt-package/action, executable-size, first-execution,
  activation, snapshot, producer, target, generation, response, and owner data.
- [x] (2026-09-13) A measurement: add the explicit `edit-latency` benchmark and
  complete an interleaved comparison of immutable `HEAD` against current source
  with two separately warmed lanes and 30 unique edits per lane. Every normal
  response matched its new behavior, exact candidate, process, and session.
- [x] (2026-09-13) A: prove the instrumentation preserves overlapping interval boundaries,
  keeps raw failures, and verifies the first normal response from the new exact
  generation.
- [x] (2026-09-13) A implementation slice: correlate every rebuild under one
  operation ID; record bounded workspace write/remove paths, exact counts and
  bytes, Go action/package-availability evidence, executable size, candidate
  input identity, queue/build/activation intervals, failures, and owner scope.
  The dev-process handoff probe now verifies all response identity headers
  against either the exact current candidate or the retained rollback identity
  and records edit-to-verified-response latency. Live probe execution remains
  open in the preceding A acceptance items.
- [x] (2026-09-13) B: move `Meta`, `CurrentRequest`, `StartSpan`, `Span`, and the narrow
  request/trace bridge into a lightweight owner while preserving public aliases,
  method sets, context behavior, bootstrap order, and trace output.
- [x] (2026-09-13) B: record root-SDK import and link closure before/after and prove current
  consumer source still compiles without importing a new SDK path.
- [x] (2026-09-13) C correctness slice: captured snapshots retain immutable
  source bytes, workspace materialization consumes those bytes rather than the
  mutable app tree, content digests participate in persisted source stamps,
  same-size edits with restored mtimes invalidate, and two live freshness gates
  reject a superseded candidate before predecessor retirement.
- [x] (2026-09-13) C incremental-preparation slice: separate the complete
  candidate snapshot identity from a contract/configuration preparation key;
  compile the canonical graph once per request; reuse exact persisted Go,
  TypeScript, and private composition artifacts across implementation-only
  edits; synchronize only changed captured source bytes; and proceed directly
  to current target verification plus Go build even when no binary exists yet.
  Persisted artifact digests and managed-output membership reject deletion,
  additions, and tampering and fall back to the full publication transaction.
- [x] (2026-09-13) C authoritative handler-edit slice: capture every file in the
  compiler workspace revision, retain graph-affecting baselines separately from
  implementation inputs, parse `.scenery.json` from captured bytes, and bind an
  unchanged startup graph to the exact captured revision without rereading the
  mutable application tree. The full compiler remains the conservative path
  for declaration/configuration changes and external declaration sources.
- [x] (2026-09-13) C incremental build-input slice: retain a bounded process-local
  SHA-256 cache for unchanged Go build inputs, accepting hits only after current
  path/type/size/mtime/mode/change-time/device/inode checks before and after the
  lookup. Platforms without a usable change timestamp always rehash. The
  `go.input_fingerprint` step reports exact hit/miss/action counts.
- [x] (2026-09-13) C graph-baseline and absent-membership slice: promote a
  successfully served declaration/configuration graph only after recapturing
  its complete current compiler membership and proving it unchanged; retain
  absent `app.lock.scn`, optional revision inputs, and every higher-priority
  renderer resolver candidate so their later appearance invalidates the graph.
- [x] (2026-09-13) C engine slice: introduce one authoritative captured
  input set and a small typed incremental preparation engine inside the existing
  long-lived development owner; ordinary build adapters enter the same
  `internal/build` computations without a second daemon or runtime mode.
- [x] (2026-09-13) C: prove complete invalidation, shared captured bytes, supersession,
  unchanged-output preservation, full target verification, and the absence of
  redundant projection/workspace work on an implementation-only edit. The
  canonical graph-change path now compiles in a private root materialized from
  the captured input set, including inputs newly introduced by the declaration;
  the live dev-process matrix covers handler, shared dependency, revision input,
  semantic contract, configuration, and embedded asset changes.
- [x] (2026-09-13) D engine slice: add content-addressed immutable artifact
  reuse, in-flight deduplication, bounded fair scheduling, atomic publication/
  corruption repair, and exact worktree/session/generation separation using
  existing cache and ownership roots.
- [x] (2026-09-13) D bounded-link slice: add an exact producer/target/build-input
  keyed development-binary artifact, atomic staging, digest validation,
  corruption rebuild, 64-entry/4-GiB retention, 256 bounded action-lock buckets,
  and two cross-process link slots. Identical same-root in-flight requests link
  once; a canceled waiter detaches without canceling the producer. Running and
  rollback generations still receive independent executable copies.
- [x] (2026-09-13) D cross-worktree immutable slice: cache the deterministic
  generated runtime composition independently of mutable workspaces, keyed by
  the exact producer, generator executable, authored config, composition import,
  and SQL requirements. The 1/5/10 benchmark used a fresh cohort-private cache
  per repetition and proved exactly one atomic publish plus 0/4/9 cross-process
  hits respectively in all nine repetitions; warm starts rendered no new
  composition. Payload corruption and incompatible producers rebuild or reject.
- [x] (2026-09-13) D cancellation/scheduling/recovery slice: represent every
  same-key caller with a cross-process OS-lock lease, detach caller cancellation
  without canceling a producer while another lease remains, cancel after the
  last lease closes, and admit only the oldest two queued link tickets. Staging
  directories carry independent lock leases so the next producer removes an
  unlocked crash remnant without touching a live build; entry-count pruning
  preserves the just-published artifact.
- [x] (2026-09-13) D: prove cross-worktree hits, cancellation subscriber semantics,
  incompatible-producer rejection, latest-only activation, cleanup authority,
  and bounded retention under 1/5/10-worktree evidence where available. In
  addition to the repeated cross-process worktree cohorts, the selected
  dev-process probe now runs tagged distinct-process lease/crash and oldest-
  ticket two-slot contention proof; retained and rollback executable copies
  survive cache reclamation in focused evidence.
- [x] (2026-09-13) E experiment: measure the real prepared ONLV application
  dependency closure and prototype one all-application worker in an owned
  disposable worktree. The prototype retained 619 of the monolith's 620
  packages and reduced the 51,974,626-byte current binary by only 121,376 bytes
  (0.23%). Do not promote it: this boundary does not remove enough real Go work
  to justify a second process, private dispatch, or changed lifecycle.
- [x] (2026-09-13) F decision: retain sequential activation. The real ONLV
  closure includes application package initialization and constructors, and no
  evidence makes them, schedules, durable work, migrations, or helpers inert
  before an activation barrier. No launch-once path was added.
- [x] (2026-09-13) Completion audit: remove the unsafe bare-workspace executable
  reuse and global-latest runtime-identity restoration path after an exact ONLV
  A-to-B-to-A journey exposed a mismatched executable/identity candidate. The
  identity-bound shared binary cache is now the sole executable reuse owner;
  focused tests, the live dev-process round-trip, and a second disposable ONLV
  journey prove the prior exact behavior, implementation/build identity, and a
  new serving PID.
- [x] (2026-09-13) Re-run the cumulative selected tests, lint, default/race verifiers, named
  probes, worktree-cost benchmark, repository fixture, disposable ONLV
  correctness, and final comparison after the latest C slice; record every
  skipped condition. Earlier complete evidence remains recorded below.

## Surprises & Discoveries

- The current root `scenery.sh` package directly imports `scenery.sh/runtime`
  for `Meta`, `CurrentRequest`, `StartSpan`, `Span`, and `ByteStream`. On the
  initial checkout, `go list -deps .` reports 315 packages and includes
  `scenery.sh/runtime`; moving only generated `main` cannot shrink that closure.
  Evidence: `go list -f '{{join .Imports "\n"}}' .` and
  `go list -deps -f '{{.ImportPath}}' . | wc -l` on 2026-09-13.
- The existing `internal/build.Step` already records start, duration, cache,
  reason, and success and is emitted as `build.step`; this plan must extend that
  plane rather than add a second telemetry product. Nested and concurrent spans
  are already preserved by `TestBuildTracePreservesNestedIntervalsAndFailure`.
- The stock Go 1.27 successful `go build` result does not provide a rebuilt
  package list without enabling the substantially more expensive action trace.
  The normal loop therefore records one Go action and
  `packages_rebuilt_available: false`; it does not distort the measured path or
  invent package evidence.
- The public SDK closure fell from 315 packages at the starting checkout to 212
  packages after introducing `internal/appsdk`; `scenery.sh/runtime` is absent
  while `runtime` itself remains 315 packages. `Span` and `ByteStream` remain
  aliases across the facade/runtime boundary, and current request/span behavior
  is bound through context/current invocation rather than a callback registry.
- Existing watcher hash reuse trusted size, mode, and mtime, and existing
  workspace source stamps omitted content. That contradicted the explicit
  preserved-timestamp invalidation requirement. Hash reuse now additionally
  requires the filesystem change timestamp (or conservatively rehashes), source
  stamps include SHA-256, and state version 7 invalidates earlier private cache
  metadata.
- The reduced public SDK changes which transitive modules remain visible to a
  source-only application before its generated composition is materialized. On
  macOS, Go analysis now canonicalizes logical and physical roots and uses a
  private temporary writable `-modfile` with `-mod=mod`; it can resolve an
  overlay without mutating the application's `go.mod` or `go.sum`. The
  generation probe and a focused `internal/parse` regression test cover this
  boundary.
- The core-separation probe originally removed its disposable framework source
  after building an unstamped product, making later source-identity validation
  fail for the wrong reason. It now links the exact source-manifest stamp and
  rebuilds that product after the repository-only source deletion. The final
  probe passes while retaining the intended core/source separation assertion.
- The old cached-workspace path treated a bare executable as reusable and then
  restored its runtime identity from the global latest runtime bundle. A real
  ONLV A-to-B-to-A journey paired the earlier A executable with B identity and
  correctly failed preflight. `Result.ReuseCompiled` and global-latest identity
  restoration are now removed. Only an immutable shared-binary manifest bound
  to the current producer, target, build input, implementation, and executable
  digest may avoid linking; authorization, verification, readiness, and
  publication remain live checks.
- The former cached-workspace API returned only whether the exact executable
  already existed. An implementation edit necessarily changed the executable
  fingerprint, so the caller discarded an otherwise valid refreshed workspace
  and reran contract compilation, Go/TypeScript projection, and composition
  rendering. The preparation result now reports workspace readiness without any
  executable-reuse verdict; unit evidence proves an implementation body edit
  invokes neither projection hook while a tampered persisted projection is
  rejected.
- Ordinary Go binaries have not yet been proven path-independent under this
  product's exact flags and runtime diagnostics. The shared executable key
  therefore retains the absolute target module root. This intentionally
  rejects cross-worktree executable hits rather than manufacturing them;
  cross-worktree compile reuse currently remains the Go toolchain's package
  cache. The 1/5/10 evidence below proves resource behavior and shared
  composition, not executable relocatability.
- A fresh explicitly selected 1/5/10 benchmark completed all nine cohorts in
  665,221 ms. Every fresh cohort-private Scenery cache observed exactly one
  composition publish and every other worktree joined the immutable artifact:
  1/5/10 worktrees produced 1+0, 1+4, and 1+9 publish+hit counts in each of
  three repetitions. Warm starts reused their private prepared workspaces and
  did not rerender composition. The benchmark retained distinct mutable
  workspaces and SQL/runtime authority, verified normal endpoints, preserved
  authored source SHA-256
  `36a4a0da5e38d2987420f22a62191f778aa2f9fb5ed82d69ea43388fce258aa4`,
  and removed every owned worktree cluster. One/five/ten-worktree cold serving
  wall-time ranges were 16,147-17,353 ms, 23,663-25,151 ms, and
  40,559-41,658 ms; warm ranges were 1,863-1,866 ms, 3,368-3,573 ms, and
  5,775-5,986 ms. Fresh private Go caches occupied 241,020/298,636/370,656 KiB
  and the corresponding Scenery caches 42,808/213,624/427,144 KiB. These are
  startup/resource measurements, not handler-edit acceptance.
- The disposable ONLV workload at `4f8126a3e3806b7100ab7efaca1b7dd06b894221`
  compiled with the exact current-source Scenery producer and passed
  `scenery check -o json` with 1,138 resources. A development standalone build
  succeeded at 51,690,608 bytes. The current private monolith contained 620 Go
  dependencies and was 51,974,626 bytes. A separate all-application worker that
  imported the real generated composition and retained its registration closure
  still contained 619 dependencies, still imported `scenery.sh/runtime`, and was
  51,853,250 bytes. Its warm unique-main edit linked in 1.36 seconds after a
  2.35-second first build. This is a measured native-artifact floor, not a host
  prototype win.
- The same ONLV production standalone build stopped after 27.26 seconds because
  `.scenery/assistant-assets/.../.output/eve-cache.json` changed during prepared
  workspace membership verification. The development standalone target passed,
  so this is recorded as a separate assistant production-packaging/freshness
  blocker rather than changed or bypassed in this refactor.
- Final ONLV acceptance used an owned disposable worktree at
  `4f8126a3e3806b7100ab7efaca1b7dd06b894221` and the exact current-source
  Scenery executable digest
  `sha256:e9285074fe1c6d053ea1c1c86f0bb05a9b026635d90aab6a367841eccaec107c`.
  `scenery check` reported 1,138 resources, the exact binary's application
  harness passed, and `go test ./...` passed after copying the two ignored CSV
  fixture prerequisites into the disposable checkout only. A real public
  `/api/healthy` request changed from `{"status":"ok"}` on process 70793 to
  `{"status":"scenery-native-edit-proof"}` on process 71552. Build-input
  digest changed from `sha256:a2e99498736dad1e201c9e10bfa467970351c2e44c686e570df14f4cb2854820`
  to `sha256:3435ddba94ba444fb451b357b53f4def6400827db5439a75a73d7df14b158415`
  and implementation revision changed from
  `sha256:a54e1a3573f24f7aac8883048950e73e6d0ab8b1bda4cafc29508bea34e2eac9`
  to `sha256:b75d9284152d69916515925ee9443d77e288679da05896d0d9525c25604f10aa`;
  contract revision and `development` target remained exact. The observed edit
  build request was 4,382.754 ms. This proves the real served behavior and
  identity transition, not the benchmark latency distribution.
- Current hardware is Apple M2 Ultra (`Mac14,14`), 24 logical CPUs, 64 GiB RAM,
  macOS 26.5.2 build 25F84, Darwin 25.5.0, Go 1.27.0 darwin/arm64. The initial
  prepared binary existed at `.scenery/harness/bin/scenery`, size 45,351,074
  bytes and SHA-256
  `3ffc987a21226ec9a50b329a2457cc51294deffa2b2f48f70797ee834b71ffca`;
  subsequent verifiers rebuilt and proved the exact current-source product.
- The final complete interleaved 30+30 comparison used two warmups per lane,
  lane-private Go and Scenery caches, retained module/download and host filesystem
  caches, and did not stop developer workloads. Immutable baseline
  `27ebaf12ca87355c6ee7ed340d1be63ffbe814f7` measured p50 1,638.142 ms, p95
  1,686.666 ms, and worst 1,708.626 ms. The exact current-source candidate
  measured p50 1,585.143 ms, p95 1,617.813 ms, and worst 1,619.716 ms: only
  52.999 ms (3.24%) and 68.853 ms (4.08%) better at p50/p95. All 30 candidate
  operation IDs and implementation revisions were unique. Average candidate Go
  build/link was 528.216 ms, implementation checking was 224.378 ms (overlapping
  152.982 ms runtime-bundle preparation), executable preflight was 503.595 ms,
  framework verification was 59.928 ms, candidate preparation was 48.289 ms,
  activation was 64.319 ms, and complete build request was 1,527.146 ms. The
  300/500 ms target remains unmet.
- The first bounded-link implementation made the caller that acquired the
  action lock own the producer context. Canceling that caller therefore killed
  the shared `go build` even when another same-key subscriber was waiting. The
  producer now uses a cancellation-detached context monitored by explicit
  subscriber leases; the last lease, rather than the first caller, controls
  cancellation. Lease inspection opens existing paths without `O_CREATE`, so a
  file disappearing between directory enumeration and lock acquisition cannot
  be recreated as a phantom subscriber.
- The compiler's startup-graph reuse still called `SnapshotUnchanged`, which
  reread the mutable application tree and could then compute a later workspace
  revision from different bytes. The watch owner now captures the compiler's
  complete revision membership, distinguishes graph-affecting from
  implementation-only inputs, and binds the reused graph to validated captured
  bytes. Handler edits therefore spend about 0.012 ms in `contract.check` on
  this machine; contract/configuration edits intentionally take the full
  compiler path. External declaration sources also remain conservative.
- Rehashing every package and module file reported by `go list` cost about
  83 ms in the preceding candidate. A bounded 16,384-entry process-local digest
  cache removes only byte rereads and SHA-256 work: it never skips current
  membership, path/type, metadata, device/inode, or change-time checks, and
  platforms without change time do not accept hits. The final digest-cache
  comparison reduced average `go.input_fingerprint` to 46.195 ms; the full
  `go.input_discovery` still averaged 59.806 ms.
- The latest complete 30+30 comparison used the same immutable baseline and
  acceptance method with two warmups per lane. Baseline p50/p95/worst were
  1,627.649/1,670.736/1,731.716 ms; candidate results were
  1,518.592/1,549.120/1,553.909 ms, improvements of 109.057 ms (6.70%) and
  121.616 ms (7.28%) at p50/p95. Average candidate `go build` remained
  529.116 ms and exact executable preflight 501.526 ms. The 300/500 ms targets
  remain unmet.
- The final identity-cache comparison reran the complete 30+30 method after
  removing bare-workspace executable reuse. Baseline p50/p95/worst were
  1,631.289/1,696.154/1,699.751 ms; candidate results were
  1,533.570/1,571.196/1,583.519 ms, improvements of 97.719 ms (5.99%) and
  124.958 ms (7.37%) at p50/p95. Every candidate edit still linked genuinely
  new behavior; safe reuse of a previously compiled exact generation was
  measured separately and was not mixed into this series. The 300/500 ms
  targets remain unmet.
- A successful declaration build originally remained local to that request;
  the watcher retained the startup graph baseline, so the next ordinary handler
  edit unnecessarily ran the full compiler again. Successful activation now
  recaptures generated membership and compiler inputs, proves the new result
  still matches the authored tree, and only then promotes it as the next graph
  baseline. A focused test changes the declaration, promotes the result, then
  changes a handler and corrupts the later live declaration to prove the handler
  build consumes the promoted immutable graph rather than rereading the tree.
- A renderer declaration without an extension depends on the nonexistence of
  every higher-priority module candidate, while `app.lock.scn` and optional
  revision inputs also change workspace identity when they appear. The compiler
  now exposes present and relevant-absent membership; the watcher fingerprints
  both states and the captured graph baseline compares graph-affecting absences.
  Focused compiler, build, and watcher tests cover resolver priority, optional
  absence, and absent-to-present invalidation.
- A measured macOS clone-on-write experiment atomically cloned the retained
  executable instead of copying it. Cross-platform compilation and the
  `dev-process` probe passed, but candidate preparation was already only
  34.599 ms and complete edit latency remained 1,630.769 ms; exact executable
  preflight still took 516.111 ms. The 0.23% worker-size result also offered no
  smaller source artifact to clone. The experiment was fully removed rather
  than retaining platform-specific complexity without a material path benefit.
- Capturing complete compiler revision membership initially made a standalone
  `_test.go` edit schedule a runtime restart. The first absent-input extension
  then exposed the related newly-created-test case because only one side of the
  comparison carried its implementation classification. Both violated the
  existing watch contract and were caught by focused tests and `dev-process`.
  The compiler capture still retains those bytes so the next real rebuild can
  bind current workspace identity, but runtime equality, changed-path reporting,
  freshness, and snapshot fingerprints ignore test-only compiler entries on
  edits, additions, and removals unless runtime code explicitly embeds them.
  Declared graph inputs ending in `_test.go` remain runtime-affecting.
- The first captured graph-change implementation still ran the full compiler on
  the mutable authored root. Bracketing that read with later freshness checks
  could not exclude an ABA mutation and did not give every consumer one input
  set. The canonical compiler now receives a private root containing only
  identity-checked captured bytes. A provisional live compile may discover
  membership introduced by a declaration, but its graph baselines remain those
  of the last accepted generation, so it cannot be reused or published.
- The first membership refresh retained the old graph as the next freshness-
  scan scope, causing every newly declared input to appear missing and every
  candidate to be discarded. Keeping the provisional graph solely as the
  membership scope fixes that contradiction while the old contract baselines
  still force canonical captured-byte compilation. A focused regression and
  the normal-endpoint dev-process probe both cover the boundary.

## Decision Log

- Decision: allocate 0180 in this checkout rather than import an uncommitted
  similarly numbered experiment from another worktree. Rationale: permanent
  sequence ownership is established by the current repository, and cross-
  worktree source/evidence is not implicitly authoritative. Date: 2026-09-13.
  Author: Codex.
- Decision: keep one development preparation engine in the current long-lived
  owner and one scheduler. Rationale: a second daemon, task language, or scheduler
  would add coordination and ownership states before removing any measured work.
  Date: 2026-09-13. Author: Codex.
- Decision: treat graph/application identity, worktree identity, session owner
  epoch, build candidate, execution generation, framework source, and executable
  producer as distinct values. Rationale: conflating them would make cache hits
  or host lifetime substitute for current authority. Date: 2026-09-13. Author:
  Codex.
- Decision: cache only pure results keyed by all consumed inputs. Always rerun
  current ownership, source freshness, target conformance, candidate attestation,
  readiness, and normal-endpoint generation proof. Rationale: prior validation
  success is not a present authorization verdict. Date: 2026-09-13. Author:
  Codex.
- Decision: promote host/worker only after a predeclared comparison: every
  contract-matrix row passes, standalone `scenery build` output remains runnable,
  no sample has identity/correctness failure, candidate median and p95 both beat
  the monolith on the same real workload, and retained memory/process overhead is
  bounded. Rationale: an SDK closure reduction or toy worker is enabling evidence,
  not runtime acceptance. Date: 2026-09-13. Author: Codex.
- Decision: retain sequential write-capable generation activation unless package
  initialization, constructors, schedules, durable work, migrations, and helper
  startup are proven inert before the explicit barrier. Rationale: main-argument
  handling occurs after Go package initialization and cannot attest its absence
  of side effects. Date: 2026-09-13. Author: Codex.
- Decision: put the app-facing metadata/request/span bridge in
  `internal/appsdk` and keep stream storage in `runtime/shared`. Rationale:
  both runtime and the root facade use the same alias types, while
  context-scoped span dispatch avoids a mutable initialization-order-sensitive
  callback registry. Date: 2026-09-13. Author: Codex.
- Decision: copy complete watched source bytes into the captured input set and
  reject source drift immediately before candidate preparation and again before
  predecessor retirement. Rationale: a watcher event schedules work but neither
  mutable-tree rereads nor an old successful verification may authorize
  publication. Date: 2026-09-13. Author: Codex.
- Decision: derive compiler workspace-revision membership from the verified
  startup graph, store graph and implementation baselines separately, and bind
  an unchanged graph only from hash-validated captured bytes. Rationale: this
  removes duplicate mutable-tree reads from the handler-edit path without
  treating a declaration/configuration or external-source change as reusable.
  Date: 2026-09-13. Author: Codex.
- Decision: cache only SHA-256 digests for unchanged Go build-input files in the
  long-lived process, bounded to 16,384 paths. Rationale: metadata and identity
  checks remain current on every request, a second stat brackets cache-hit
  acceptance, ctime-less platforms rehash, and the Go tool still owns package
  invalidation. Date: 2026-09-13. Author: Codex.
- Decision: distinguish a declaration/configuration preparation hit from an
  executable hit. Persist exact private/public generated digests, cached
  TypeScript digests, verification patterns, and managed-output membership;
  accept the preparation only when all remain exact, then rerun current native
  verification and compile/link as required. Rationale: handler bytes do not
  affect generated projections, while an old validation verdict or a merely
  present file is never reused. Date: 2026-09-13. Author: Codex.
- Decision: keep the target's absolute module root in the shared executable key
  until a real same-input two-worktree build proves path-independent binary and
  diagnostic behavior. Rationale: build-input content equality does not prove
  relocatability, and stripping path identity merely to obtain a hit would
  weaken the requested isolation contract. Date: 2026-09-13. Author: Codex.
- Decision: share the root-independent generated runtime composition across
  exact worktrees while keeping its materialized workspace private. Rationale:
  it is a real immutable computation with a complete portable key and bounded
  payload, whereas current executable diagnostics remain path-sensitive. A
  fresh cohort-private cache makes cross-process hit evidence observable without
  clearing or relying on a developer's global cache. Date: 2026-09-13. Author:
  Codex.
- Decision: promote each successfully served canonical graph to the watcher's
  next baseline only after generated membership recapture, compiler-input
  recapture, and `SnapshotUnchanged` succeed. Rationale: declaration changes are
  not rare one-off state; retaining the old startup graph repeats full compiler
  work, while promotion before an exact freshness proof could bind later edits
  to a stale contract. Date: 2026-09-13. Author: Codex.
- Decision: model fixed optional inputs and renderer resolution alternatives as
  explicit absent compiler inputs without hashing absence into the aggregate
  workspace revision. Rationale: the existing revision meaning remains bytes of
  present inputs, while membership equality and watcher fingerprints still
  detect newly appearing paths and force canonical recompilation. Date:
  2026-09-13. Author: Codex.
- Decision: reject and remove APFS clone-on-write retained-executable copying.
  Rationale: the live probe showed only 34.599 ms candidate preparation versus
  516.111 ms preflight and 1,630.769 ms complete edit latency, so the added
  platform-specific path could not materially close the acceptance gap. Date:
  2026-09-13. Author: Codex.
- Decision: keep the existing 256 content-key action buckets, place one
  OS-locked lease per subscriber, and add a timestamp-ordered ticket queue in
  front of the two global link slots. Rationale: action buckets keep lock-file
  retention bounded, subscriber leases give cross-process cancellation
  ownership, and oldest-two admission prevents a polling worktree from
  repeatedly overtaking older work without introducing another daemon or
  scheduler. Date: 2026-09-13. Author: Codex.
- Decision: place a lock lease in every shared-binary staging directory and
  serialize stage creation with stale-stage inspection. Rationale: an OS lock
  survives PID/path reuse semantics and is released by process exit, allowing
  exact cleanup of crash remnants while active producers remain protected.
  Date: 2026-09-13. Author: Codex.
- Decision: reject promotion of the measured ONLV host/worker boundary and
  remove the disposable prototype. Rationale: preserving the actual generated
  composition and application constructors leaves 619/620 dependencies and
  saves only 0.23% of executable bytes; a stable host would add dispatch,
  authorization, stream, lifecycle, identity, and packaging work without a
  credible compile/link reduction. Date: 2026-09-13. Author: Codex.
- Decision: retain the current sequential candidate preflight, predecessor
  retirement, launch, and readiness sequence. Rationale: the application closure
  has not proven package initialization or constructor side effects safe before
  a barrier, so overlap would weaken lifecycle and write-fencing semantics.
  Date: 2026-09-13. Author: Codex.
- Decision: compile a changed canonical graph in a private root materialized
  from identity-checked captured bytes. A provisional live compile may only
  discover new membership; the last accepted contract baselines force the
  staged canonical compile and current freshness gates still run before any
  mutation or activation. Rationale: post-read equality cannot exclude an ABA
  tree mutation, while eagerly watching every arbitrary workspace file would
  add incorrect invalidation and unbounded capture. Date: 2026-09-13. Author:
  Codex.
- Decision: keep real shared-binary OS-lock behavior out of ordinary Go test
  timing and execute it as a build-tagged segment of the already selected
  `dev-process` probe. Rationale: distinct processes are required to prove lock
  release on crash and fair global slot admission; in-process tests retain the
  deterministic fast coverage. Date: 2026-09-13. Author: Codex.
- Decision: delete bare-workspace executable reuse and global-latest runtime
  identity restoration, making the exact shared-binary manifest the only owner
  that may reuse executable bytes. Rationale: ONLV A-to-B-to-A proved that a
  workspace executable and a separately selected latest bundle can describe
  different generations; failing preflight prevented publication, but the pair
  must never be constructed. Current shared-cache restoration retains exact
  producer/target/build-input/implementation/executable binding and still runs
  candidate preflight and readiness. Date: 2026-09-13. Author: Codex.
- Decision: run no subagents, no shared binary installation, no remote changes,
  no cache flushing, and no mutation of the original ONLV checkout or its data.
  Rationale: these are explicit task and repository boundaries. Date:
  2026-09-13. Author: Codex.
- Decision: remove the owned disposable ONLV worktree after recording its exact
  evidence, using `git worktree remove` followed by the macOS Trash for the
  task-owned parent. Rationale: preserve the original checkout and avoid leaving
  a 52 MB prototype or generated application state behind; the Trash keeps the
  non-Git residue recoverable. The original checkout remained clean at
  `4f8126a3e3806b7100ab7efaca1b7dd06b894221`. Date: 2026-09-13. Author: Codex.

## Outcomes & Retrospective

The implemented A-D slices preserve the public surface while making the ordinary
build attributable, reducing the root SDK dependency closure from 315 to 212
packages, binding workspace reads to captured bytes, avoiding redundant
projection work on implementation edits, and sharing one immutable generated
composition across matching worktrees. The selected repository, fixture,
probe, race, and real ONLV correctness evidence passes.

The latest performance outcome is a modest 5.99% p50 and 7.37% p95 improvement,
not the requested subsecond loop: current p50 is 1,533.570 ms and p95 is
1,571.196 ms against targets of 300/500 ms. Go build/link and exact executable
preflight remain the largest ordinary boundaries. The real ONLV host/worker experiment
was rejected because it retained 619/620 dependencies and saved only 0.23% of
binary bytes; sequential activation remains because safe early side effects
were not proven.

This plan therefore remains active because the numerical edit-latency target is
unmet, not because correctness evidence may be rounded into performance
acceptance. C now has one captured input set through canonical graph compilation
and the live invalidation matrix. D has real cross-process composition reuse,
subscriber crash cancellation, fair two-slot contention, corruption recovery,
bounded retention, independent retained executable ownership, and repeated
1/5/10-worktree cleanup evidence. Absolute module-root identity intentionally
rejects relocatable executable reuse; this is a safe boundary decision rather
than a missing claim. Cold, declaration, shared-dependency, configuration, and
embedded-input timings are fixture observations on this machine, not universal
bounds. A separate native-input latency mutation remains unperformed because
the available cgo-disabled fixture has no behavior-preserving native input; the
unchanged real ONLV native closure passed correctness but does not substitute
for that measurement. A future performance slice needs a
materially different artifact-boundary hypothesis; the rejected near-monolithic
worker must not be repeated unchanged.

## Context and Orientation

The current ordinary edit crosses these owners:

| Stage | Current owner and entry point | Intended owner after this plan | Equivalence / work proof |
|---|---|---|---|
| watch detection and settle | `cmd/scenery/watch*.go`, `devSupervisor` | same supervisor scheduling the one engine | watcher fake-clock tests plus `--probe dev-process`; missed/overflow/rename/delete/new/same-size-same-time cases |
| source capture and transaction recovery | `cmd/scenery` file snapshot, `internal/compiler`, `internal/workspacetx` | one engine snapshot using the same recovery/ownership gate | compiler/workspacetx tests, concurrent mutation discard, complete membership/byte manifest |
| declaration parse and graph | `internal/compiler`, `internal/scn`, `internal/graph` | dependency-keyed pure computation in the engine; compiler remains semantic owner | compiler fixtures, exact workspace/contract revisions, cache invalidation matrix |
| Go analysis and target context | `internal/parse`, `internal/model`, `internal/gotarget`, generator verification | same package owners, invoked once per exact captured input/target | default-plus-selected target tests and `native-contract` probe |
| projection generation/publication | `internal/generate`, injected through `internal/generate/api` and `internal/build.GenerateHooks` | same renderer and `workspacetx` publication, reused by complete projection key | generator tests, fixture regeneration, unchanged generated-byte/mtime assertions |
| private workspace | `internal/build/prepare.go`, `workspace_cache.go`, inventory/fingerprints | engine computation backed by current cache root; build remains materialization owner | build tests, tamper/corruption/absent-path tests, deterministic written-file counts |
| compile/link | `internal/build/compile.go` and Go toolchain | Go toolchain with package-level cache; optional immutable keyed artifact coordinator | build-input/toolchain identity tests, action graph evidence, no invented Go invalidation |
| candidate retention/preflight | `cmd/scenery/dev_app_*`, retained independent executable, runtime preflight | same supervisor; host/worker pairing only after E promotion | `build-info`, `dev-process`, failure/recovery tests |
| runtime execution | generated entrypoint plus `scenery.sh/runtime` single process | unchanged monolith until E; then one stable host plus one all-app Go worker | contract matrix and normal HTTP response with exact execution generation |
| activation/rollback | `devSupervisor`, `internal/devprocess` | same concrete supervisor; barrier only if F proves safety | wrong ABI/exit/cancel/readiness tests and prior-generation recovery |

The critical orchestration entry is
`cmd/scenery/dev_build_pipeline.go:prepareDevRuntimePlan`. It verifies framework
source, loads or rebuilds graph metadata, refreshes or prepares the private
workspace, performs generation, compiles under the workspace lock, waits for
retained database startup, prepares the runtime environment, and hands a plan to
the application handoff owner. `internal/build.PrepareForCompileWithSnapshotContext`
does contract compilation, Go/TypeScript projection work, source/generated sync,
workspace fingerprints, generator/framework identity, and pending verification.
`internal/build.CompileContext` joins full native verification with `go build`,
checks source/framework freshness, publishes the runtime bundle/build state, and
prunes only stale disposable workspace binaries. The supervisor independently
retains executable bytes for current and rollback generations.

The public SDK boundary currently spans `scenery.go`, `stream.go`,
`runtime/shared/types.go`, `runtime/current.go`, `runtime/span.go`, and
`runtime/registry.go`. Public type aliases and method sets must remain assignable
to existing application source. `scenery.sh/runtime` remains the execution owner;
the lightweight leaf may carry data and narrow bridge behavior but must not gain
a mutable callback registry or import orchestration, HTTP serving, database, or
generated composition.

The historical final 0179 attribution, not a current baseline, was approximately
604 ms contract checking, 1465 ms implementation analysis, 464 ms runtime-bundle
preparation, 1291 ms Go build/link, and 813 ms candidate preflight for the final
0178 source; later 0179 samples varied. Intervals overlap, so this list is not
summed. Final evidence populates the table below:

| Workflow | Samples | p50 | p95 | Worst | Required identity | Status |
|---|---:|---:|---:|---:|---|---|
| warm handler edit, immutable baseline | 30 | 1,631.289 ms | 1,696.154 ms | 1,699.751 ms | source + framework source/executable + target + build input + implementation + process + served generation | passed identity; target unmet |
| warm handler edit, current candidate | 30 | 1,533.570 ms | 1,571.196 ms | 1,583.519 ms | same, with 30 unique behaviors/operation IDs/revisions | 5.99%/7.37% faster; target unmet |
| cold build | 3 repetitions per 1/5/10 cohort | not a percentile series | not a percentile series | 44,759 ms | same build inputs and toolchain | separate resource evidence only |
| unchanged warm start | 3 repetitions per 1/5/10 cohort | not a percentile series | not a percentile series | 6,976 ms | new process, unchanged implementation | separate resource evidence only |
| declaration/shared-dependency/config/embed/native edits | deterministic invalidation plus live fixture samples except native mutation | not a percentile series | not a percentile series | 1,635 ms for measured fixture classes | exact changed dependency class | measured classes passed; native latency open |
| concurrent worktrees 1/5/10 | 3 cohorts each | cold/warm ranges above | not a percentile series | 44,759/6,976 ms | per-root session/owner and shared immutable action IDs | passed composition sharing and isolation |

### Contract matrix

| Behavior | Current owner | Proposed owner | Required proof |
|---|---|---|---|
| `.scn`, `.scenery.json`, CLI and machine envelopes | compiler/app/CLI/machine | unchanged | current schemas, CLI tests, generation/native probes |
| public Go aliases, assignability and method sets | root packages + runtime/shared/runtime | lightweight leaf with root aliases; runtime consumes same leaf | compile-time surface tests, external fixture `go test ./...`, root closure report |
| metadata/current request/context propagation | runtime request state | lightweight context/request leaf populated by runtime | existing and new request/concurrency tests, normal HTTP journey |
| application spans and trace parenting | runtime trace/reporting | leaf span handle using narrow immutable reporter owned by runtime | span/db/http trace tests and app child-span probe |
| authorization/principal/internal calls | runtime + generated adapters | unchanged unless E private dispatch, then host enforces and worker receives scoped context | capability-authority/auth probes, cross-session rejection |
| typed unary and byte-stream outcomes | generated adapters + runtime | one implementation; E transport must stream without buffering | native contract, fixtures, cancellation/backpressure/reader ownership tests |
| SQL handles/transactions and shared app state | in-process worker/runtime | remain in the one application worker | postgres/auth/fixture proof; no RPC SQL proxy |
| constructors/lifecycle/durable/schedules | generated composition + runtime | worker generation, reset per activation | lifecycle order and worker/durable probes; no retained generation state |
| process/public identity headers | linked runtime and supervisor session | same public meanings; optional private host/worker IDs are additional internal state | build-info, dev-process, final normal endpoint generation assertion |
| standalone build/deploy artifact | `internal/build` monolithic binary + sidecar | same runnable documented artifact semantics even if packaged host+worker internally | build fixture launched without dev supervisor; deployment probe if packaging changes |
| source freshness and atomic generation | compiler/workspacetx/generate/build | same gates consuming one captured set | mutation/tamper/publication tests and generation probe |
| worktree ownership/retained executables/rollback | agent + supervisor + independent copies | unchanged; immutable cache cannot own running generations | worktree/parallel-runtime/dev-cleanup and predecessor recovery |

## Milestones

Milestone A first establishes a reproducible live baseline and bounded causal
evidence. Each edit sample records a root operation ID and intervals without
adding overlapping children. Action/cache counters and byte/file counts are
attached only where the owner can measure them exactly. The final response check
requests the normal endpoint and validates new behavior plus exact process and
implementation generation.

Milestone B extracts only the public request metadata, span handle, and stream
value boundary needed by applications. It compares package and linked binary
closures before/after. No public name moves, and the runtime still installs the
request state and reporting implementation through direct typed construction or
context values with deterministic bootstrap order.

Milestone C introduces a deliberately small engine of typed computations:
captured declaration inputs, canonical graph, projection bytes, Go analysis
inputs, workspace materialization, native build, and generation publication.
Each key includes exactly its behavioral dependencies and the producer/toolchain
identity. All tool reads are bound to the captured bytes or staging tree. Watch
events request reconciliation; complete membership/content checks remain
authoritative. Superseded results may populate immutable caches but cannot
publish a generation.

Milestone D persists only immutable complete artifacts under the current cache
root and coordinates identical in-flight actions. It keeps worktree roots,
session owner epochs, execution generations, mutable helper directories,
credentials, database/storage namespaces, and publication ownership private.
Scheduling is one bounded coordinator aware of Go's own parallelism. Cache
entries publish atomically with complete descriptors and are ignored/rebuilt on
partial or corrupt state. Active/rollback executables remain independent and
protected from cache reclamation.

Milestone E measures the real application closure and one-worker split. The
worker contains all application Go implementations and their shared state. The
host moves only separable framework machinery. Private transport is authenticated,
session/generation scoped, deadline/cancellation aware, streaming, and fail
closed. It must not create per-service processes or SQL proxies. Promotion
replaces the monolith and updates architecture; rejection removes the prototype.

Milestone F retains the existing `internal/devprocess` boundary. Candidate
attestation may overlap only work proven side-effect-free before an explicit
activation barrier. Exact host/worker producer, ABI, source/build input and
generation pairing is checked before publication. The final readiness request
uses the normal endpoint and rejects old-generation HTTP 200 responses.

## Plan of Work

Implement reviewable slices that leave the repository passing after each
material boundary. Begin by extending `internal/build.Step` and the existing
supervisor event projection with exact optional data and tests. Extend the
current development process probe/measurement artifact rather than writing an
unbounded trace service.

For the SDK extraction, first pin the public type/method surface and capture
`go list -deps -json` plus a minimal linked consumer binary. Move data values to
an established leaf if ownership fits; otherwise introduce one narrowly named
internal leaf used directly by runtime and the root façade. Keep reporting
implementation on the runtime side and make no callback registry.

For the engine, refactor callers onto typed operations one at a time. The
captured-input structure records file path, presence/absence alternatives,
mode, exact bytes/digest, generated exclusion classification, target/toolchain,
framework source/executable and owner epoch. It is immutable after capture.
Stage external tool reads against those bytes, bracket operations with current
membership/content checks, and retry or discard drift. Preserve `workspacetx`
recovery before capture and atomic publication afterward.

Add cache and coordinator behavior only after the corresponding pure operation
has complete keys and uncached tests. Prove a same-key worktree hit and a
path-sensitive rejection. Cancellation detaches one waiter and cancels the
underlying action only after its last waiter leaves. Queue and action limits are
internal constants/configuration of the owner, not public flags or environment
variables.

Run the host/worker prototype only after A-D results identify link/replacement
artifact cost as the remaining dominant boundary. Record the numerical promote
comparison before running it. If any matrix row lacks proof or the real loop is
not faster, remove product prototype code, keep diagnostic artifacts only under
bounded harness output, and leave E/F unchecked.

## Concrete Steps

All repository commands run from `/Users/petrbrazdil/Repos/scenery`. First run:

    go run ./scripts/verify --quick --summary --write
    jq '.changed_area, .relevant_plans, .risk' .scenery/harness/agent-context.json
    .scenery/harness/bin/scenery inspect docs --for-path cmd/scenery/dev_build_pipeline.go -o json
    .scenery/harness/bin/scenery inspect docs --for-path internal/build/prepare.go -o json

Capture baseline framework and machine identity without changing source after
preparing the executable:

    git status --short --branch
    git rev-parse HEAD
    shasum -a 256 .scenery/harness/bin/scenery
    go version
    go env GOOS GOARCH GOVERSION GOTOOLDIR
    sw_vers
    sysctl -n machdep.cpu.brand_string hw.model hw.memsize hw.ncpu
    go list -deps -json .

Use a repository fixture and an owned disposable ONLV worktree discovered from
current local repositories. Prepare the exact framework selection once with the
worktree-local executable and do not edit framework source afterward. Record
every executable path and cwd. Run a separate warmup, then at least 30 handler
edits whose returned value is unique and never previously compiled in the
series. Preserve raw samples and failures in bounded `.scenery/harness/`
artifacts and restore only task-owned source after verifying no concurrent drift.

After every meaningful source slice:

    go run ./scripts/verify --quick --summary --write
    jq '.changed_area.validation_classes, .changed_area.recommended_commands' .scenery/harness/agent-context.json

Run each newly recommended affected-package command. For the expected initial
areas, run:

    go test ./internal/build ./cmd/scenery
    go test ./internal/compiler ./internal/parse
    go test ./internal/generate
    go test ./internal/workspacetx ./internal/evolution
    go test ./internal/devprocess ./cmd/scenery

If compiler/generator source changes, regenerate and typecheck all required
consumer fixtures:

    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/house -o json
    go run ./cmd/scenery generate --target typescript_client.public_api --app-root testdata/assistant -o json
    bun test internal/generate/testdata/typescript_client_conformance.test.ts
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.generated-clients.json
    apps/console/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json

Before final handoff run:

    go test ./...
    golangci-lint run ./...
    go run ./scripts/verify --summary --write
    go run ./scripts/verify --race --summary --write
    go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe worktree --probe parallel-runtime --probe core-separation --summary --write
    go run ./scripts/verify --benchmark edit-latency --summary --write
    go run ./scripts/verify --benchmark worktree-cost --summary --write

If E is promoted, additionally run:

    go run ./scripts/verify --probe capability-authority --probe auth --probe postgres --probe fixtures --summary --write

If shared agent/coordinator ownership changes rather than remaining in the
worktree supervisor, additionally run exactly:

    go run ./scripts/verify --probe agent-restart --probe dev-lock --probe dev-cleanup --summary --write

This command may be skipped only when the diff and dependency path show that no
shared agent/coordinator lifecycle or lock/cleanup selection changed; record
that path-level evidence here. Add `--probe storage` only if storage ownership or
routes change, `--probe assistant-runtime` only if assistant lifecycle changes,
and the current matching deployment probe only if build packaging changes a
deployment boundary. Full release certification is not selected by this task;
run only `scripts/release-gate.sh` if a later changed-area result explicitly
selects release or the developer separately requests it.

For the fixture and disposable ONLV worktree, use the exact prepared binary for:

    <prepared-scenery> framework use --source /Users/petrbrazdil/Repos/scenery -o json
    <prepared-scenery> check -o json
    go test ./...
    <prepared-scenery> harness -o json --write

Then run the normal served-edit driver and verify the response's new behavior,
`X-Scenery-Implementation-Revision`, `X-Scenery-Build-Input-Digest`,
`X-Scenery-Go-Target`, and `X-Scenery-Process-ID` against the exact candidate and
session generation. Discover the endpoint and ONLV paths from current inspect
output; do not reuse historical hard-coded paths or ports.

## Validation and Acceptance

Functional correctness requires every changed contract-matrix row to pass, no
public surface diff, deterministic ordinary tests, exact source/toolchain/
producer/candidate/session/response bindings, and successful predecessor
recovery. Verified work elimination requires action evidence that an unrelated
handler edit does not rewrite unchanged clients, rerun migrations/seeds,
rebuild unrelated helpers, provision resources, or link a no-op binary, while
current ownership/freshness/target/readiness checks still run.

Measured improvement requires an interleaved baseline/candidate comparison when
feasible, separate warmup, at least 30 valid unique handler edits per final
comparison, all failures retained, and p50/p95/worst/queue/phase reporting.
Any identity or behavior mismatch invalidates the series. Achievement of the
subsecond target is a separate claim and requires p50 at most 300 ms and p95 at
most 500 ms on the recorded reference machine; otherwise report the exact gap.

The invalidation suite covers no-op, handler body, shared Go dependency,
declaration/contract, config, module/lock, embedded file, native input,
generated tamper/deletion, same-size same-timestamp content, rename/deletion/new
membership, missed/overflow watch events, incompatible producer/toolchain,
corrupt/partial cache entries, concurrent publication, superseded generation,
cross-worktree request, cleanup authority, and crash recovery. No watcher or
mtime is accepted as equivalence.

The concurrency suite covers 1/5/10 worktrees where the authorized environment
supports them, identical in-flight action sharing, one-waiter cancellation,
last-waiter cancellation, fairness, bounded Go/link parallelism, latest-only
publication, and active/rollback cache protection. Missing Docker, disk, memory,
or ONLV prerequisites are recorded as unperformed evidence, never passed proof.

The repeated-edit/churn run records process count, file descriptors, runtime
children, cache bytes/entries, and owner memory before/after a bounded series.
Growth must be explained and bounded; active or rollback generation resources
must remain valid throughout reclamation.

## Idempotence and Recovery

All artifact writes use staging plus atomic rename and complete descriptors.
Incomplete or corrupt entries are quarantined only within exact task-owned cache
paths or ignored and rebuilt; never delete a broad cache root. A failed engine
operation leaves the current generation serving. A superseded operation may
finish immutable work but cannot publish. Cancellation joins every started
branch and preserves work still subscribed to by another owner.

Compiler reads continue through `workspacetx` recovery. Generated publication
retains its journal/receipt ordering. Candidate resources are released only
after the candidate process stops. Recovery starts the independently retained
previous executable and exact environment and proves it through the normal
endpoint; it does not claim rollback of irreversible external writes.

Use disposable repository/ONLV worktrees with exact marker files. Never remove
or rewrite the original ONLV checkout, data, live services, or framework pin.
Before restoring a task-owned edited file, compare it with the expected sample
value and stop on concurrent drift. Never run `go install ./cmd/scenery`, clear
shared Go caches, kill unrelated processes, push, merge, or mutate remotes.

## Artifacts and Notes

Keep source-controlled contracts and summaries in this plan. Bounded raw local
evidence belongs under `.scenery/harness/incremental-loop/`, grouped by source
HEAD and candidate label. Each sample must include operation ID, UTC timestamps,
input manifest digest, framework source/executable digest, Go target/toolchain,
cache state, action counts, written paths/count/bytes, rebuilt packages/actions
where the Go tool exposes them, executable size/digest, candidate identity,
session owner epoch, execution generation, first normal response headers/body,
latency, and failure.

Current slice evidence (2026-09-13):

- `go test ./internal/build ./cmd/scenery ./scripts/verify` passed after the
  Milestone A trace/probe slice.
- `go test ./internal/appsdk ./runtime .` passed after the SDK extraction;
  compile-time assertions preserve `*scenery.Span` to `*runtime.Span` and
  `scenery.ByteStream` to `runtime.ContractByteStream` assignability.
- `go list -deps -f '{{.ImportPath}}' .` reports 212 packages and no
  `scenery.sh/runtime`, compared with 315 packages and a runtime dependency at
  the starting checkout. `go list -deps ./runtime` remains 315 packages.
- Focused tests for captured-byte materialization, content-bearing source
  stamps, restored-mtime invalidation, nested request/span restoration, and
  superseded snapshot comparison pass. The named live probes and final selected
  validation matrix subsequently passed as recorded below.
- `go test ./...` and a refreshed
  `go run ./scripts/verify --quick --summary --write` passed after the first C
  incremental-preparation slice. The changed-area union remains
  `cli-json-contract`, `go-package`, and `release-sensitive-or-runtime` with no
  failing verifier steps. `TestCachedPreparationCompilesImplementationEditWithoutFullPrepare`
  proves zero Go/TypeScript projection hook calls, current target/verification
  setup, no stale executable reuse, and exact edited bytes in the private
  workspace; `TestCachedPreparationRejectsTamperedPersistedProjection` proves
  corruption forces the full preparation path.
- The current-source `--probe dev-process` passed after this slice. Its latest normal
  endpoint returned the new unique behavior with exact contract,
  implementation, build-input, target, and process headers in 1775.975 ms from
  edit completion. The atomic multi-file sample took 1874.303 ms and the rapid
  superseding-edit sample 2379.536 ms. The probe also asserts one contract
  computation, Go and TypeScript projection cache hits, and exactly one private
  workspace file write for the ordinary implementation edit. These are single probe observations, not
  the required 30-sample comparison, and remain well above the 300/500 ms
  target.
- `go test ./internal/build`, `go test -race ./internal/build`, and `go test
  ./...` pass with the bounded shared-link cache. Unit tests prove same-key
  in-flight deduplication, canceled-waiter detachment, digest-corruption repair,
  atomic restoration to a distinct destination, and rejection of one
  executable key across distinct absolute module roots. The subsequent live
  `--probe dev-process` passed at 1747.796 ms for its ordinary edit, 1759.898 ms
  for the atomic batch, and 2501.007 ms for the rapid superseding batch; these
  remain individual correctness samples, not acceptance statistics.
- A later focused D pass added explicit producer-caller and last-subscriber
  cancellation tests, oldest-ticket ordering with stale-ticket reclamation,
  missing-lease non-recreation, crash-stage reclamation with active-stage
  protection, and 64-entry retention with preservation of the current artifact.
  `go test ./internal/build`, `go test -race ./internal/build`, and
  `golangci-lint run ./internal/build/...` passed; a Windows/amd64 compile-only
  test also passed for the platform lock helper. Real multi-process contention
  and churn remain open rather than inferred from these focused tests.
- After adding the complete correlated phase evidence, a current-source
  `--probe dev-process` passed with a 1,754.478 ms ordinary edit. Its
  `build-18d4be205c3d4a28-4` operation recorded 54.503 ms framework verification,
  2.069 ms contract checking, 10.295 ms cached workspace refresh, 58.045 ms Go
  input discovery, 83.319 ms input fingerprinting, 141.894 ms runtime-bundle
  preparation overlapping a 214.087 ms implementation check, 513.395 ms Go
  build/link, 38.470 ms candidate preparation, 510.735 ms exact executable
  preflight, 63.354 ms activation, and 123.220 ms from ready activation to the
  first normal endpoint response that verified the exact candidate identity.
  The executable was 21,700,866 bytes and the build request itself was
  1,480.016 ms; overlapping intervals are retained and are not summed.
- `go run ./scripts/verify --benchmark worktree-cost --summary --write` passed
  on Apple M2 Ultra / 24 logical CPUs / 64 GiB with Docker 29.4.0 on OrbStack.
  Fresh private Go caches occupied 241,020 KiB for one worktree, 298,636 KiB for
  five, and 370,656 KiB for ten; private Scenery caches occupied
  42,808/213,624/427,144 KiB. This proves actual shared immutable composition
  and bounded cohort behavior, but not relocatable executable reuse or final
  edit-latency acceptance.
- In an owned disposable ONLV worktree, the exact prepared binary under source
  digest `sha256:580aae23b32b52b8aebf53f38eb639f7f14bb627c441413b30efdb7bb7ef695f`
  and executable digest
  `sha256:e9285074fe1c6d053ea1c1c86f0bb05a9b026635d90aab6a367841eccaec107c`
  passed `scenery check -o json`, `scenery harness -o json --write`, and the real
  served edit described above. `go test ./...` passed after supplying only the
  two repository-ignored CSV fixtures already present in the original checkout.
  The real-closure worker experiment is rejected by the 619/620 dependency and
  51,853,250/51,974,626 byte comparison above. The runtime was stopped, the
  worktree registration was removed, and the remaining owned parent was moved
  to the macOS Trash. The original ONLV checkout stayed clean at
  `4f8126a3e3806b7100ab7efaca1b7dd06b894221`.
- `go run ./scripts/verify --benchmark edit-latency --summary --write` passed on
  Apple M2 Ultra, macOS 26.5.2, Go 1.27.0, with load averages moving from
  3.84/3.89/5.34 to 6.67/5.53/5.78. Baseline framework source/executable were
  `sha256:89caeeb08f3699c6e0e12edf4a69966a975485727599b20a879ed690a0b5c7ca`
  and `sha256:b46d731d68147ab207f5fa2e67971af7017ca579e93c3a4b13c910e2ec65f2aa`;
  candidate source/executable were
  `sha256:580aae23b32b52b8aebf53f38eb639f7f14bb627c441413b30efdb7bb7ef695f`
  and `sha256:881c6947739530a3d7ab0baef6371bc033eed929a8a35cf50331d4ee85fd2ead`.
  The bounded raw 60-sample artifact is
  `.scenery/harness/incremental-loop/20260913-final/edit-latency-samples.json`
  (SHA-256
  `b5874a78548111b8585bcc5effc89b552ec82a81d785ebcf279a44142a2a1fdf`).
  The exact p50/p95/worst results and remaining gap are recorded above; this is
  functional and modest measured improvement, not subsecond acceptance.
- After compiler-capture and Go input-digest reuse, a second complete
  `go run ./scripts/verify --benchmark edit-latency --summary --write` passed
  with load averages moving from 2.19/3.40/4.58 to 3.54/4.16/4.74. Baseline
  source/executable digests were
  `sha256:89caeeb08f3699c6e0e12edf4a69966a975485727599b20a879ed690a0b5c7ca`
  and
  `sha256:9da1adccc815145d338cf77d72beeac75ddb5bed26b4aee9fa72d85b67bf2c61`;
  candidate source/executable digests were
  `sha256:b13ee0c2e3e1d372b576fc1a0d1609d13010dd8fbdfd4e6d8f4eea29f38439eb`
  and
  `sha256:9e1f5dfb0cf79094415208cbd2ad3c48213b67535a8e5759e33d0c233d4fb018`.
  The bounded raw artifact is
  `.scenery/harness/incremental-loop/20260913-digest-cache/edit-latency-samples.json`
  (SHA-256
  `a66fcf4e4df96a5dab086e459fa7f9b159b164ce3d7c4f6720e0ea6c8ff63729`).
  Baseline p50/p95/worst were 1,627.649/1,670.736/1,731.716 ms and candidate
  values were 1,518.592/1,549.120/1,553.909 ms. All 30 candidate behaviors and
  identities were exact and unique; the 300/500 ms target remains unmet. This
  artifact predates the later evidence-rendering-only addition of action and
  digest-cache hit/miss fields to each raw phase; build-step logs carried those
  counters, and the measured product path was unchanged, so the expensive run
  was not repeated merely to reserialize the same evidence.
- The current-source worktree-cost rerun passed in 669,716 ms. Across all nine
  cohorts, one/five/ten roots again produced exactly 1+0, 1+4, and 1+9
  composition publish+hit counts. Cold serving ranges were
  17,444-17,747/24,454-25,054/41,862-42,961 ms and warm ranges were
  1,762-1,766/3,272-4,871/5,482-5,588 ms. The exact authored source digest
  `5c9622d616b068f14db878bd3bb13814e03cc46cd7e6be36f938af2a45a46a20`
  remained unchanged, resource cleanup completed, and cache allocations stayed
  at the earlier 241,020/298,636/370,656 KiB Go and
  42,808/213,624/427,144 KiB Scenery values.
- Current-source ONLV validation used commit
  `4f8126a3e3806b7100ab7efaca1b7dd06b894221` in owned disposable roots.
  Exact framework source digest
  `sha256:f0ff1774b094190aec60a4378a586bc6841fd9556b26bf68972ae9c68dc047c8`
  passed a regenerated `scenery check -o json` with 1,138 resources,
  `go test ./...`, and `scenery harness -o json --write`. A fresh root with a
  separate agent home and provisioned frontend dependencies served
  `/api/healthy`; the response changed from `{"status":"ok"}` on PID 13965 to
  `{"status":"scenery-0180-current-snapshot"}` on PID 14381. Contract revision
  stayed
  `sha256:43abbcaf9c2ab6f1c07a60c2731a1008687f391fc257795d818d2d746eef83ee`,
  while implementation revision changed from
  `sha256:3fce1941454d46f8c7a49977b1eabac6fb164a115e811d0e2a20397e38a311a2`
  to
  `sha256:2bea7cd6d31080c15d511e876ad07c67f307d0f1b3033a142d436091b206f46e`
  and build-input digest from
  `sha256:42555aa88a9933367684bbc8ae471b69aa58295acdab5e5d26803ca5de3d251f`
  to
  `sha256:f5d463ab0f98b29d2ff4d3a10681d15097083926a2d3765ab5cded5a761366fc`.
  The edit build request took 4,037.710 ms. Both runtimes were stopped; the live
  root's exact cluster was pruned, both Git worktrees were removed, and their
  residual parents were moved to the macOS Trash. The original checkout
  remained clean. The first root's empty fail-closed legacy-claim record was
  skipped by the public prune command rather than manually deleted.
- The final absent-membership candidate was revalidated in a new owned ONLV
  worktree at the same commit. Exact framework source digest
  `sha256:daf468b6f2357f91228ad71f38df60ccf55f03138c35e25b1ccb067cf11824d9`
  and executable digest
  `sha256:fe389baec0f3159a175bf515e00ca9c3c52ea4920d1199584c4adb322946a7b5`
  generated the 149 missing ignored contract artifacts, then passed
  `scenery check -o json`, `go test ./...`, and
  `scenery harness -o json --write`. The normal `/api/healthy` response changed
  from `{"status":"ok"}` on PID 68272 to the unique
  `{"status":"scenery-0180-current-absent-proof"}` on PID 68780. Contract
  revision remained
  `sha256:43abbcaf9c2ab6f1c07a60c2731a1008687f391fc257795d818d2d746eef83ee`;
  implementation revision became
  `sha256:025ca15a87f38952428e9b3d9f723b5cb3686ab83d5f53d5ceee3bbfc6e4d62a`
  and build-input digest became
  `sha256:a8a427d1a78a9503058d0a9c184a7730c6baee9e12c4961f3de99435ce155dfc`.
  The exact correlated build request took 4,209.360 ms. The edited source was
  restored, the owned runtime and database were stopped through `scenery down`,
  the worktree was deregistered, and its residual parent was moved to the macOS
  Trash. The original ONLV checkout remained clean.
- Final cumulative validation passed after the compiler-capture, digest-cache,
  absent-membership, and test-only watch fixes: all affected packages; `go test ./...`;
  `golangci-lint run ./...` with zero issues; TypeScript conformance and both
  generated-client typechecks; fixture regeneration with no diffs; default and
  race self-harnesses; the 669,716 ms worktree-cost benchmark; and one combined
  run that passed `parallel-runtime`, `core-separation`, `worktree`,
  `build-info`, `generation`, and `native-contract`. That run's `dev-process`
  check caught a newly appearing implementation `_test.go` restart; after the
  classification fix and focused addition/removal regression, the exact
  `dev-process` rerun passed. Test/doc/identical-content edits caused no restart;
  the ordinary edit reached its verified normal response in 1,557.129 ms;
  `contract.check` was 0.017 ms, Go build/link 527.882 ms, candidate preparation
  42.029 ms, exact executable preflight 497.810 ms, and the complete build
  request 1,455.697 ms. Failed preflight recovered and verified the predecessor
  response in 1,649.580 ms. The final default verifier's Go suite was under its
  cached budget. Default, race, and probe modes retained only 41 pre-existing
  documentation-review warnings and 21 architecture warnings. Full release
  checks were not selected.
- The completion audit found and removed the remaining mutable-tree graph
  compile. `go test ./internal/build ./cmd/scenery ./scripts/verify` passed, as
  did `go test -tags=scenery_build_cache_integration ./internal/build
  -run='^TestSharedBinaryCrossProcess' -count=1`. The tagged proof used distinct
  processes: a producer remained live while a subscriber held its OS lock,
  canceled after the crashed last subscriber released it, reclaimed the stale
  lease, admitted only two of four link contenders, and let the oldest queued
  ticket enter first. The current `--probe dev-process` then passed in
  60,533 ms. Normal-endpoint edit-to-verified-response observations were
  1,736.745 ms for a shared Go dependency, 519.462 ms for a newly declared
  non-Go revision input, 1,731.803 ms for a semantic contract change,
  637.236 ms for configuration, 1,719.524 ms for embed setup, and 1,645.095 ms
  for an embedded asset edit. The probe also proved six exact builds with no
  generated-output feedback, changed contract identity for the semantic edit,
  changed implementation/build identity for the shared Go edit, unique serving
  processes, correct behavior, and cleanup.
- The identity-reuse completion audit first exposed two fail-closed regressions.
  An ONLV A-to-B-to-A journey showed that the removed cached-workspace path
  could pair an A executable with the global latest B bundle; candidate
  preflight rejected it before publication. After deleting that path, the
  native-contract probe showed that unconditional restoration from the exact
  shared artifact replaced the unchanged workspace inode. The final path first
  validates the current shared manifest and cached bytes, then preserves an
  already digest-matching destination. The bare destination alone grants no
  reuse. Focused cache tests prove no relink or inode replacement, and the
  exact native-contract rerun passed.
- The final cumulative probe union passed generation, native-contract,
  build-info, dev-process, worktree, parallel-runtime, and core-separation.
  The current dev-process evidence reached the ordinary verified response in
  1,637.152 ms with a 1,469.165 ms build request, 519.914 ms Go build/link,
  495.689 ms executable preflight, and exact current source digest
  sha256:6f2af0a9de536351fd1c4fb44e8a20a028d16f92bc1bab72b349b2de1ca5108f.
  Its exact previously compiled round-trip returned the prior implementation
  and build-input identities on a new PID in 1,022.192 ms. The same run passed
  shared-dependency, new revision input, semantic contract, configuration,
  embed setup, embedded-asset, failed-candidate recovery, and distinct-process
  subscriber-crash/two-slot fairness proof.
- The final worktree-cost rerun passed in 681,994 ms overall (680,278 ms for
  A18). One/five/ten worktrees produced exactly 1+0, 1+4, and 1+9 immutable
  composition publish+hit counts in all three repetitions. Cold serving ranges
  were 17,445-18,342/25,252-25,459/41,860-44,759 ms; warm ranges were
  1,763-1,861/3,683-3,875/6,675-6,976 ms. Authored source digest
  cc1cfa2f4fe683b97e2b2709a4a0f70ff965036c595c365134f2574599f04144
  remained unchanged and cleanup completed.
- The final 30+30 benchmark passed with exact current framework source digest
  sha256:6f2af0a9de536351fd1c4fb44e8a20a028d16f92bc1bab72b349b2de1ca5108f
  and candidate executable digest
  sha256:fc6fd22d1f83a5816f6b86af92ccf916e607f6d159bc4d2e8b208361b53f0b70.
  Baseline p50/p95/worst were 1,631.289/1,696.154/1,699.751 ms; candidate
  values were 1,533.570/1,571.196/1,583.519 ms. The bounded raw report is
  .scenery/harness/incremental-loop/20260913-final-identity-cache/edit-latency-report.json
  (SHA-256
  2579801404b57ec8fc1887febf14ec3eac854717b05e5e961da37b4ac904f4b0).
- Exact final ONLV acceptance used commit
  4f8126a3e3806b7100ab7efaca1b7dd06b894221, framework source digest
  sha256:6f2af0a9de536351fd1c4fb44e8a20a028d16f92bc1bab72b349b2de1ca5108f,
  and its prepared executable digest
  sha256:32daaa813192c08461e5bd6f01f13158740d5e0ec29675e573d4cd489f52e297.
  Generation produced the 149 ignored contract artifacts; scenery check -o
  json passed with 1,138 resources, go test ./... passed, and scenery harness
  -o json --write passed. The normal endpoint completed status ok on PID 97606
  with build input
  sha256:87ce3f17197326cdf47edb004f050bf3c91e0d005cc3bd6d8c63e28753c0f916
  and implementation
  sha256:6a7bedac776e1f000fa033f2b5a0d1b294176a901b28b5644752be6bfa9421bf,
  served unique behavior on PID 98022 with build input
  sha256:bb7efed6e287d80740920f98308ce2dcf119c9ed0c0f1d5ec13328cf4a814aef
  and implementation
  sha256:de256c11672e491141fddd5e755080a88dd406d9a57dcf8f54f2d1b7db3b9a1a,
  then restored the exact original behavior and identities on new PID 98308.
  The final A restoration was an
  identity-bound shared-artifact hit and its build request was 3,014.328 ms,
  including current preflight/readiness and no link. The task-owned runtime
  stopped, its exact PostgreSQL cluster/state were pruned, and both disposable
  roots were deleted; the original ONLV checkout remained clean. A first
  disposable Git worktree correctly failed closed on a retained pre-cutover
  execution claim, so the live journey used a separate task-owned local clone
  and agent/cache home rather than deleting or claiming existing data.
- After the final exact-destination helper, `go test ./...` and
  `golangci-lint run ./...` passed, as did the default and race self-harnesses.
  The default suite remained within its cached budget. Both self-harness modes
  retained only the 41 pre-existing documentation-review and 21 architecture
  warnings; no new warning or error was introduced.
- Full release certification was not selected. Capability/auth/PostgreSQL/
  fixtures probes were not selected because E was rejected; agent restart/dev
  lock/dev cleanup were not selected because shared-agent ownership did not
  change; storage, assistant-runtime, and deployment probes were not selected
  because their ownership and packaging boundaries did not change. Broad
  invalidation, multi-process contention, cache corruption, and active/rollback
  reclamation now have selected evidence. Remaining open acceptance is the
  numerical 300/500 ms goal, a behavior-preserving native-input latency
  mutation, and a longer bounded process/file-descriptor/memory/cache-growth
  churn series.

Do not paste large Go `-json` action graphs or raw traces into default output.
Reference their exact artifact paths and summarize non-overlapping critical-path
intervals. Update Progress, discoveries, decisions, outcomes, baseline/final
tables, exact commands, and skipped proof at every resumable stopping point.

## Interfaces and Dependencies

No public CLI/configuration/environment variable or dependency is planned. Keep
Go and prefer the standard library. `internal/parse` remains the only
`go/packages` owner. Compiler-side packages remain independent of runtime
orchestration. `internal/generate/api` remains the lightweight build-facing
generation boundary, and `internal/build` keeps injected rendering ownership.

New internal types should be narrow: immutable captured-input records,
dependency keys, computation results, bounded action evidence, subscription
handles, and explicit private host/worker identities only if E proceeds. Avoid a
generic task graph, service locator, mutable global callbacks, exported signature
heuristics, alternative runtime modes, per-service processes, SQL RPC handles,
new database, or mandatory external build service.

If a public JSON shape legitimately changes, update its checked schema, exact
schema revision, docs, tests, and harness together. Instrumentation should remain
inside the existing development event/evidence plane unless a current public
schema already owns the data. Structural runtime changes update
`ARCHITECTURE.md`, affected `AGENTS.md`, `docs/agent-guide.md`,
`docs/local-contract.md`, and this plan in the same slice.
