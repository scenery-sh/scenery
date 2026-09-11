# Halve the Verified Development Loop

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current. It follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

The developer requests at least 50% faster semantic edits and unchanged starts.
Use the stricter latest verified 0178 medians as the reference: 6306.755440 ms
for edits and 8034.490750 ms for starts. Success requires medians no greater
than 3153.377720 ms and 4017.245375 ms respectively, without weakening current
source validation, exact served identity, readiness, isolation or rollback.

## Progress

- [x] (2026-09-11) Audit the 0180 measurement in
  [0181](0181-native-worker-measurement-audit.md). Remove redundant input discovery
  and rendering, preserve full freshness, and repeat two six-pair series. With
  matched post-build checks, medians are 6414.336 ms control and 6532.735 ms
  worker (+118.399 ms, 1.85%). Withdraw the architectural rejection based on the
  earlier 19% slowdown; this corrected prototype still proves no substantial
  speedup and does not satisfy either full-loop threshold.

- [x] (2026-09-11) Complete the architectural feasibility gate in
  [0180](0180-native-worker-feasibility.md). The real native worker preserves
  closure, target checks and behavior, but all six paired candidates are slower:
  4998.205 ms control median versus 5945.482 ms worker. The interpretation of
  this historical result is corrected by 0181 above. No BuildSession migration
  has followed. This bounded API path
  excludes the full supervisor/frontend/assistant loop and does not replace
  this plan's unchanged end-to-end thresholds or prior observations.

- [x] (2026-09-11) Complete the final authorized Oracle consultation and delete
  its exact monitor. Oracle recommends no further speculative optimization in
  this series; a different runtime-artifact model needs an explicit decision.
- [x] (2026-09-11) Complete the missing three semantic edits on the exact
  initial-scan source/executable pair. All identity and focused checks pass;
  times are 5474.657086, 5461.476051 and 4410.091002 ms. The 5461.476051 ms
  median fails the fixed 3153.377720 ms threshold. Preserve all samples.
- [x] (2026-09-11) Overlap the one initial source scan with control-plane setup,
  after lifetime ownership, framework freshness and migration-source checks.
  Startup and cleanup join the scan; duplicate-up and source validation remain
  unchanged. CLI/full Go tests, targeted race, lint, default verifier, isolated
  root timing and `worktree`/`dev-process` probes pass. Three verified starts have
  a 4032.03 ms median (49.8% improvement), still above the 4017.25 ms threshold.
- [x] (2026-09-11) Verify clean Scenery 2c1bcfc2 and inspect prior raw phase evidence.
- [x] (2026-09-11) Fix explicit success thresholds and preserve the owned 4070 fixture.
- [ ] Attribute and remove redundant analysis, candidate and assistant preparation work.
- [ ] (2026-09-11) Overlap only verified retained PostgreSQL startup with initial
  Go preparation. A fresh source/SQL-supply check gates it, and the existing
  resolver performs ownership and authenticated cluster checks. No allocation,
  container recreation, database/schema preparation, migrations or seeds run in
  that branch. Every exit joins or cancels it; post-build endpoint resolution
  remains fresh. Focused unit/race tests pass. Three real starts with the
  asynchronous source gate reached a 4193.13 ms median (47.8% improvement);
  the 4017.25 ms startup threshold remains unmet. The `worktree`, `dev-process`,
  `assistant-runtime` and `build-info` external probes pass for this increment.
- [x] (2026-09-11) Implement a bounded two-worker assistant stage with one
  invocation-local Node resolution; targeted ownership/concurrency tests added.
  Three starts measured a 14.1% median improvement before further snapshot reuse.
- [x] (2026-09-11) Measure three non-overlapping real semantic edits and unchanged
  starts on the same final source. Both performance thresholds remain unmet.
- [x] (2026-09-11) Reject the four-worker assistant-file copy experiment after
  three measured starts showed no improvement; retain the serial verified copy.
- [x] (2026-09-11) Capture the complete Oracle answer and delete its exact monitor.
- [x] (2026-09-11) Share HTTP input decoding behind the unchanged typed API,
  preserving partial results and custom decoder pointer types. Runtime/full Go
  tests, race checks, lint, isolated root timing, default verifier and final-source
  `native-contract`/`dev-process` probes pass. The same-source 3x3 series reaches
  edit 4494.71 ms and start 4163.22 ms, still above both required thresholds.
  Current-source ONLV check, full Go tests and app harness pass.
- [x] (2026-09-11) Preserve failed-start helper ownership, overlap startup staging
  and bound helper starts; three verified starts reached a 4609.05 ms median.
- [x] Share complete pure projections and remove the typed-model entrypoint dependency.
  Entrypoint rendering now accepts only the application name; selected-target
  analysis runs alongside every default target inside the owned private workspace.
  Private application projection now uses the bounded pure-render cache with
  workspace and implementation identities included. Live retirement and overlay
  checks remain outside the cache; numerical acceptance is still pending.
  A separate bounded adapter-value cache reuses declaration-only adapters across
  implementation edits, while composition and descriptors retain fresh identity.
  Build preparation now publishes and retains one public Go projection for the
  private workspace, replacing separate publish/render hooks. Current snapshot,
  module and retirement validation remain live; cached-workspace preparation
  uses the same operation. Three semantic edits reached a 4635.36 ms median;
  three unchanged starts reached 4418.78 ms. Both numerical goals remain open.
- [ ] Add operation-owned private build/full-verification branches with a publication barrier.
  Implemented for fresh compilation: materialize/prime under the workspace lock,
  full default-plus-selected target checking in the same private workspace,
  cancellation/join, and success publication afterwards. Membership/byte checks
  bracket the operation. Unit/race, lint, full verifier and the `dev-process`
  probe pass. Measured edits still miss the numerical target. Real runtime preflight
  remains after the join for now.
- [ ] Reuse pure compiler work and complete Go input selection without reusing validation success.
  CPU profiling identified graph-view JSON cloning and its allocation/GC cost.
  Structured deep copies now preserve the existing JSON-normalized graph shape
  without serializing provenance. Ten isolated compiler invocations fell from
  roughly 573 ms to 354 ms; end-to-end acceptance remains pending. All source
  discovery, module validation, digest enrichment and semantic checks still run.
  Build-input discovery now requests every consumed `go list` field explicitly;
  a three-pair comparison of the current 619-package private workspace preserved
  the complete input projection while reducing output from 2.86 MB to 303 kB
  and median command time from 462 to 266 ms. No package or file is pruned.
- [ ] Pass affected tests, full repository verification and named external probes.
- [ ] Pass ONLV current-source restart and Chrome acceptance, restore temporary fixture inputs.

## Surprises & Discoveries

The final `latency-0179-final-source-edit-{1,2,3}` series uses the same
`86c1101bf94b28234022007d7102f02e5e8dae0f28fdc1a84d06bbe185a87ed0` source and
`89b1f9ad22afbfb12c05d530890a4a33f076ea26db8b6fa71a069864536b2686` executable
as the initial-scan startup series below. Its median is 5461.476051 ms, not the
older source's 4494.714741 ms. Compile-parent durations are 2999, 2969 and
1944 ms; generation is 727, 722 and 701 ms and first preflight 783, 777 and
803 ms. This localizes the observed sample variation but does not establish
its causal owner. Warmup after source selection preceded the measured series;
none of the three measured samples is excluded. Each sample changes the real
project summary, serves a new process and implementation identity, and passes
exact build identity plus focused development validation. No task-owned
validation overlaps the measurements; machine-wide isolation is not claimed.

The initial-scan overlap uses source
`86c1101bf94b28234022007d7102f02e5e8dae0f28fdc1a84d06bbe185a87ed0`
and executable `89b1f9ad22afbfb12c05d530890a4a33f076ea26db8b6fa71a069864536b2686`.
Three uncontended `latency-0179-parallel-initial-scan-start-{1,2,3}` samples are
4556.645625, 4032.029584 and 4031.621500 ms; all pass exact served identity and
focused development acceptance. The median remains 14.784209 ms above the goal;
the first slower sample is retained, not dismissed as a warmup. Phase evidence
shows source scanning starting before control setup completes, preserving the
same complete source snapshot. The first sample also waits 385 ms for assistant
staging, while the other two have no residual stage wait. These separate series
are not an interleaved causal A/B attribution of the full timing difference.
The `worktree` probe passes in 261.890 s and `dev-process` in 51.020 s. Twenty
isolated runs of each new join/cleanup test report sub-100ms roots (quantized to
0 ms). Current-source ONLV `check`, full Go tests and application harness pass.
No semantic-edit improvement is claimed for this startup-only change.

An isolated Go action-graph diagnostic after a body-only overlay edit found
about 170 ms in the changed service plus its generated adapter, versus about
960 ms in the link action. These are diagnostic builds of the restored
workload, not end-to-end acceptance. Binary symbol inspection also found about
790 kB of duplicated generic HTTP request decoding code. A temporary overlay
that leaves `DecodeContractInput[T]` as a typed wrapper around a shared decoder
reduced an otherwise equivalent current-framework diagnostic binary from
53,460,514 to 51,940,418 bytes without removing DWARF. The warm build samples
were noisy (reference 1824/1416 ms, candidate 1455/1350 ms), so they do not
prove either latency threshold. The bounded refactor now lives in
`runtime/contract_input.go`, leaving `runtime/contract_http.go` below its file-size
warning. Pointer/custom decoder and partial-error result semantics remain
unchanged. All runtime tests, the full Go suite, focused race tests and lint
pass. Twenty isolated runs of the new typed-target test remain below 100 ms
(Go event timing quantizes each to 0 ms). Final-source `dev-process` and
`native-contract` probes pass in 51.500 s and 10.839 s. An earlier native probe
correctly rejected SCN8003 because the source-file split occurred after its CLI
build; that mixed-source run is not acceptance evidence.

Current-source end-to-end samples use source
`766a4d5eaa0563f97800020a4c54faa25ebffa976583eaa8d77519e1fe602f50`
and executable `3fb4fd78672112747a4e0f6b7ae4b7a21a5cdaac2c93043cbf03a05f5c60b953`.
Edits are 4643.491145, 4494.714741 and 4491.547543 ms (median 4494.714741);
unchanged starts are 4134.722541, 4163.216791 and 4168.595458 ms (median
4163.216791). All six `latency-0179-shared-decoder-*` samples pass exact served
identity and focused development acceptance. Warmups and handler restoration
are excluded. The smaller binary and reduced duplicate code justify retaining
the simple shared decoder, but these separate before/after series do not
attribute their entire timing difference to it. The original two numerical
gates remain open, with about 1341 ms edit and 146 ms startup still missing.

The third AskOracle consultation completed at
`https://chatgpt.com/c/6aa3543c-2cf8-83eb-bae3-543508184539`, with the
`oracle-0179-critical-path.md` complete replacement overlay and latest same-source
3x3 evidence. The entire latest answer was captured and its exact temporary
heartbeat `scenery-0179-critical-path-oracle` was deleted. The proposed disposable
empty-assets link cut removed only 259,792 bytes (0.50%) and did not improve warm
link times, so it was not promoted. A three-binary first-exec diagnostic found
709–715 ms before main, versus 16–19 ms in preflight identity construction;
repeated preflights took about 50 ms in total. Additional Go init tracing and OS
sampling do not establish which system component owns the pre-main delay. No
security controls or debug information were changed. These diagnostics are not
end-to-end acceptance. The local state artifact records exact results and scope.

A smaller candidate-preparation change now starts independent retained-binary
copying alongside fresh runtime-environment resolution, only after successful
Go compilation. Both operations join before constructing the exact preflight
request. Environment failure waits for copying and retires only an unused
retained executable through the supervisor's existing ownership check. No
allocation is moved before Go verification; no preflight or environment success
is cached. Focused join/error/cleanup tests, race tests, lint, full repository
verification and the `dev-process` probe (53.551 s) pass. Twenty isolated
invocations of each new root stay below 100 ms; reported 0 ms p95 values reflect
Go event-time quantization, not zero elapsed work.

On source `8a22506e37d72a51eb810b6541cf243ee3bcc692b6f82af14dc58b5f44725406`
and executable `bd04481f0bf8631a3d76340e5af063281c59c50c77c5eff94bbac3aee121e8f2`,
three semantic edits measured 4724.127, 4481.001 and 4539.597 ms (median
4539.597 ms). Candidate preparation consistently took 178–185 ms, but total
edit acceptance remains above 3153.378 ms. Three unchanged starts measured
4353.505, 4135.965 and 4330.726 ms (median 4330.726 ms), also above the
4017.245 ms threshold. All six passed exact served identity and focused
development acceptance under `latency-0179-parallel-retention-*` labels. The
bounded preparation overlap is retained for further evaluation, not claimed
as achievement of either numerical goal. Other desktop workloads remained
present during this series; no machine-wide isolation is claimed.

An isolated linker diagnostic also compared default output with `-w` (no DWARF)
using unique linked identities, not a reused executable. Default samples were
1996.428, 1142.319 and 1149.931 ms; no-DWARF samples were 870.352, 907.483 and
875.694 ms. Output fell from 53,477,026 to 41,522,338 bytes. The roughly 274 ms
median link difference is not whole-edit acceptance and would remove debugger
information. No production build flag was changed or debugger capability waived.
The owned ONLV current-source check, full Go suite and application harness pass
for the retained parallel-preparation increment.

The next retained-executable increment uses macOS descriptor-relative
`fclonefileat` for independent copy-on-write staging, with ordinary bounded copy
on unsupported or cross-filesystem volumes. It keeps both full SHA-256 checks,
regular-file/length checks, independent inode identity, final permissions,
atomic rename and file/directory synchronization. It does not change first-exec
security behavior or candidate handshake/rollback. An isolated diagnostic of
three new executable copies measured first launches around 604–634 ms versus
55–59 ms on the second launch, while Go package initialization ended at 27–40 ms.
The delay therefore primarily precedes application initialization; no claim
that the new copy mechanism removes that delay is made. End-to-end acceptance
for this increment did not demonstrate an improvement. Source
`3ed031a54fe674c5481c79687cae0acc59de898869210274d003c79499137724`, executable
`1ef5ef7c72d8035babfd11495cd4f8452625b92f9344ae37e04c2cdbc7ac9c30`, measured
starts of 4467.614, 4123.252 and 4125.205 ms (median 4125.205 ms), and edits
of 4713.018, 4749.956 and 4718.460 ms (median 4718.460 ms). All samples passed
exact identity and focused acceptance under `latency-0179-cow-binary-*` labels.
The first edit spent 284 ms in candidate preparation and 829 ms in preflight.
The copy-on-write product code, new atomic-copy helper/tests and architectural
note were removed after this series; the isolated filesystem result alone did
not justify the extra platform-specific path. Full package/default verification,
lint, Linux test compilation and a 20 ms p95 exact-root test had passed before
removal. Named external probes were not repeated for this rejected variant.

Candidate preparation also includes `prepareRuntimeEnvironment` when database
setup is unchanged. That operation is not read-only: it can allocate retained
PostgreSQL and storage and ensure databases/schemas. It must not simply move
before successful Go verification. Any overlap must preserve current allocation,
readiness and failure boundaries and use the exact final environment for the
candidate handshake. This is an unresolved architectural direction, not an
implemented optimization.

The next compiler increment routes declaration-dependent reads through one
operation-owned `declarationInputs` layer. It records actual source membership
and bytes, local module selection, symlink/path checks, file-kind decisions
(including failed resolver alternatives), lock bytes and presence, SQL/renderer
bytes, assistant package inputs, and the actual ancestor-aware auth-config
discovery operation. Registry modules/providers remain ordinary cache misses.
A bounded process-local snapshot owns cloned pure compiler data and source
bytes. Hits replay the observations, reconstruct independent parsed sources,
and recompute the complete workspace revision; full Go/build/runtime checks are
outside this cache. The existing `SnapshotUnchanged` contract is not narrowed.
No performance acceptance is claimed until differential/invalidation tests and
actual identical-source edit/start series pass.

The first declaration-cache profile exposed a weighted-size rejection: the
expanded manifest was copied twice through its two public references, exceeding
the 64 MiB budget. Preserving that alias inside the independent copy fixes the
miss without increasing the budget. Ten same-process ONLV compiles measured
466.001 ms for initial capture and 232.396–249.898 ms for subsequent invocations,
compared with roughly 354 ms before declaration reuse. This is a compiler-only
trade-off, not edit/start acceptance. Focused tests cover exact value ownership,
source reconstruction, fresh declared implementation revisions, declaration
byte invalidation, same-size/same-time content changes, source membership,
negative resolver alternatives, and symlink replacement.

Actual declaration-cache acceptance on source
`4d0cfe1544c608e23591d549c605d0a9e739996407c6aa9397839f8c2107cb18`
and executable `fea5d7c8c4262a45cb41323cbd75ff19f571f230ecc5682542daf16570e1771b`
did not improve the overall loop: starts measured 4336.876, 4250.682 and
4237.311 ms (median 4250.682 ms); semantic edits measured 4573.623, 4617.952
and 4507.098 ms (median 4573.623 ms). All six samples passed exact served
identity and focused development acceptance, under labels
`latency-0179-declaration-cache-{start,edit}-{1,2,3}`. The third edit's contract
phase was 236.625 ms, confirming reuse in the actual supervisor, but generation
still took 565 ms, compilation 1991 ms, candidate preparation 344 ms and candidate
preflight 799 ms. Both numerical gates remain open. The declaration snapshot/input
observer experiment was then removed completely, including its dedicated tests
and `ParseBytes` helper. The previously accepted structured graph-view copy
remains. This series does not justify keeping the additional cache or declaring
it a successful end-to-end optimization.

The follow-up Oracle response completed after 17m18s and was captured in full
in the originating task on 2026-09-11; its exact heartbeat was deleted and the
app confirmed deletion. It reviewed the complete replacement overlay with
SHA-256 `f6bef79f39f4a97498a97192f010eb491589a02b1f47ef1529ce9790f9bb19fe`
against accessible base `11f3b5b8504e659b0c339bd6043fb666fb562685` using
6 Astra Pro and the actual GitHub connector. It explicitly withdrew confidence
in the earlier 300/200/75ms budgets: even zero graph/projection/discovery cost
would not close the edit gap under the reported remaining costs. Its suggested
common projection digest was independently implemented after that attachment;
its stale coverage gaps were subsequently closed by the shared-key 3x3 series
and current-source ONLV/Chrome acceptance above. No claim of a reproduced
Oracle timing or an achieved 50% goal is made.

An explicitly measured, subsequently rejected hypothesis separated assistant generation identities into a
small private Go package. The source confirms that `renderAssistantRegistration`
previously embedded workspace revision into the large composition package's
canonical MCP literal on every edit. The new package uses variables, not
exported constants, so changing initial values need not invalidate composition
export dependencies. Composition concatenates the exact canonical source-value
JSON at its structurally located top-level field; nested schema fields and all
other bytes remain unchanged. Runtime/implementation fallback semantics,
descriptor digests, full verification and publication ownership remain intact.
This changes a Go package boundary rather than merely splitting a source file.
Three actual semantic edits measured4783.631463,4737.629305 and4703.368427ms
(median4737.63ms), all passing exact identity and focused acceptance. Labels:
`latency-0179-generation-package-edit-{1,2,3}`; source
`c2e9e0d1354358e6809462368db2c7152b51d464d3a2de8e9bedb743035050b8`,
executable `be0c69bcae01400e427e25ae334ad53131806107567ef7223f6f90acfe6cc271`.
The private composition hash stayed identical across edits2and3 while the
identity file changed, so the intended source stabilization actually happened.
Nevertheless the series did not improve the prior4498.56ms median; the complete
experiment, its tests and its instruction additions were reverted. This is not
a controlled hardware-level regression claim, but provides no justification to
retain extra package/binding complexity for this goal. Full Go tests, lint,
default verifier, three unchanged TS regenerations and TS conformance/typechecks
passed before measurement; the experiment's new external probes and startup
series were not run because it was rejected on its target edit workflow.

While the follow-up Oracle consultation was pending, pure Go projection
profiling identified repeated complete-input key encoding. Public packages,
private composition and adapters now share one freshly captured common digest
within the same render operation. Each new operation still reads every key
input; private workspace/implementation identity and target/catalog/import
extras remain distinct, and non-serializable synthetic input bypasses caching.
No identity is memoized by compiler-result pointer or across operations.
On the real ONLV declaration graph, twenty pure in-memory projections changed
from about105 ms on warm calls to about55 ms (first miss524 to416 ms), with
199 files and3326588bytes. This is not end-to-end numerical acceptance.
Focused invalidation/ownership tests, the full Go suite, three fixture
regenerations (all unchanged), TypeScript conformance and both generated/client
catalog typechecks pass. The documented CLI `-run TestGenerate` command matches
no current roots; the complete CLI package ran with `go test ./...` instead.
No public contract changed, so the current local-contract and agent-guide
descriptions intentionally remain unchanged for this implementation-only step.
The actual three-start series then measured4136.450167,4028.019167 and
4035.592667ms (median4035.59ms,49.8% below baseline), and three semantic edits
measured4498.564182,4503.849015 and4468.062780ms (median4498.56ms,28.7% below
baseline). Every sample passed exact served identity and focused development
acceptance. Labels:`latency-0179-shared-key-{start,edit}-{1,2,3}`; source
`95e8937e4a10c176b71266628e7a4fe7773f22da18392665f2cc6c93293d8e53`, executable
`d2adbf97ade3cb3895a5f9085a043d8922effe4f87c6c4671ed58f6588d8ec07`.
The warmup is excluded. Both goals remain unmet:18.35ms startup and1345.19ms
edit gaps remain. Full/default verification and the48.141s dev-process probe
pass; the new key test measured below100ms p95 in20 isolated invocations.
The selected source also passed the owned ONLV fixture's `./scripts/scenery
check -o json`, `go test ./...`, and `./scripts/scenery harness -o json --write`.
Chrome at `http://localhost:4070/ahjs` displayed the 28,355-entry catalog,
returned City of San Ramon for a search, and opened its populated detail panel;
the rendered panel was visually inspected. The semantic handler is restored
without a diff. The follow-up Oracle answer remains in generation; its partial
observations are not treated as final recommendations or acceptance evidence.

After the retained PostgreSQL increment, `go test ./cmd/scenery ./internal/build`,
`go test ./...`, `golangci-lint run ./...`, and the default verifier passed.
The verifier retained documentation/architecture warnings and an advisory
6.779 s suite duration under concurrent framework preparation; that duration
is not an isolated test-root acceptance measurement. The assistant polling
experiment was then reverted. The owned ONLV fixture's temporary Scenery
replacement and semantic handler edits are absent, and its original published
pin is serving HTTP 200 again on port 4070. No personal runtime was changed.
Twenty isolated invocations of each of the five new PostgreSQL test roots also
passed the absolute 100 ms p95 limit (30, 0, 0, 20 and 60 ms respectively).
These were explicit root measurements during external verification, not an
unchanged-start or edit latency series.
The combined external verifier subsequently passed `worktree` (382.252 s),
`assistant-runtime` (1.477 s), `build-info` (0.403 s), and `dev-process`
(49.389 s), retaining only documentation/architecture warnings. Full release
certification was not selected. Current-source Chrome acceptance and a new
semantic-edit series were not repeated for the PostgreSQL-only increment;
the previous projection increment's proof does not cover those unrun lanes.

The retained PostgreSQL overlap initially saved only about 16 ms because its
fresh source gate and resolver construction remained on the main startup path.
Moving that gate into the joined attempt and avoiding a duplicate producer-tree
hash within the same framework verification invocation gave unchanged starts of
4097.817875, 4210.484500 and 4193.129083 ms. Labels:
`latency-0179-retained-postgres-async-start-{1,2,3}`; source
`08eef221a64a7e20270158de73defcdc2c1d8e5654b23d3b8506b588412ade38`.
Every ordinary post-build database resolution still independently checks the
endpoint. No new semantic-edit series has yet been measured for this increment.

An isolated APFS experiment measured independent executable clones plus first
preflight about 159 ms faster at the median than byte copies plus preflight.
This is not configured startup acceptance and no clone mechanism is implemented
in product code. A separate 20 ms assistant readiness polling experiment retained
strict health/info checks but produced starts of 5111.420208, 4214.626834 and
4324.110042 ms (median 4324.11 ms), with all identity and focused acceptance
checks passing. Labels: `latency-0179-helper-poll-start-{1,2,3}`; source
`0f91f8efa77b45b88da4f68d7f2bd9702640ac54429613b7117d850ea6d44d04`.
It did not demonstrate improvement and was reverted to the existing 100 ms
interval; no reduced-polling gain is claimed.

One unchanged startup with finer attribution took 4420.54 ms; resolving database
and storage capabilities consumed 452 ms of the 725 ms database setup phase.
Label: `latency-0179-database-attribution-start-1`; source
`a352d8bded9e2b6e1bd8fd804023c3a14457ab3aa6464397ddbb436a73e50b93`.
This motivated the retained-server-only overlap; it does not justify moving
write-capable schema setup ahead of compilation.

The combined-projection test initially included durable filesystem publication
and reached an isolated 100 ms p95. Its ordinary root now pre-materializes the
fixture and tests shared byte identity plus stale-snapshot rejection (20 ms p95);
actual publication remains in the named native process probe. The test fixture
also needed a declared Go target to exercise private composition; a declaration
byte change, rather than an undeclared implementation file, exercises its
snapshot-drift boundary.

The private-projection change's first `dev-process` probe failed its final PID
stability assertion; an unchanged-source rerun passed (54.720 s). Inspection
found that the probe paired a session read with a later response without checking
the response's process identity. The probe now requires the response's
`X-Scenery-Process-ID` to match that session; build-count and stability assertions
are unchanged. This closes an evidence race, not proof that the first failure
had no product cause. The strengthened probe must pass before acceptance.

The first full verifier rejected the abbreviated living-document statement;
the required standard wording is restored. Go tests, vet, schemas and lint
passed in that run; the concurrently measured suite duration is advisory only.

The final 0178 edit spends 604 ms on contract checking, 1465 ms on implementation
analysis, 464 ms on runtime-bundle preparation, 1291 ms in Go build and 813 ms
on candidate preflight. Unchanged startup spends about 1400 ms on workspace
verification and 2244 ms staging two private assistant copies. The current
graph key still includes full implementation content; keep this exact identity.

## Decision Log

- Decision: finish the current-source evidence without adding another speculative
  performance patch; retain the original success thresholds and failed samples.
  The final Oracle assessment identifies no supported route to both thresholds
  from the experiments already performed. Its recommendation to stop this series
  is not proof that 50% is impossible and does not complete this plan. A proposed
  split runtime/generation artifact model changes build, retention, identity,
  distribution and debugger/platform qualification and must not be adopted
  without an explicit developer decision. Date: 2026-09-11. Author: Codex,
  following the user's final Oracle consultation request.

- (2026-09-11, Codex) Start the single initial source scan after the existing
  owner/framework/migration gates and join it before use or cleanup. This reorders
  read-only work without adding a long-lived observer or reusing verification
  success. Keep this change only if final-source startup measurements improve;
  its effect cannot be credited to semantic edits, where startup does not run.

- 2026-09-11, Codex: permit initial preparation to wake only a ready, retained
  managed PostgreSQL container after current graph, framework and SQL-supply
  checks. Reuse the ordinary authenticated endpoint resolver branch, without
  granting allocation or recovery authority. Join before database setup and
  resolve the endpoint again there; retain sequential migration/seed ordering.
- 2026-09-11, Codex: use latest verified medians, not the slower pre-0178 baseline.
  This is a stronger reading of the requested 50% improvement.
- 2026-09-11, Codex: work in the main session. No subagents or remote publication
  are authorized. AskOracle is not present in available skills or tool metadata;
  continue concrete local investigation and report that limitation if needed.
- 2026-09-11, Codex: keep real compilation, candidate handshake, database truth,
  independent writable assistant state and sequential write-capable generations.
  Optimizations must remove or overlap redundant work, not move readiness early.
- 2026-09-11, Codex: adopt the Oracle's operation-owned fork/join direction,
  subject to local source verification and actual timing. Its 3100 ms edit and
  3907 ms startup figures are conditional budgets, not performance evidence.
  First close failed helper shutdown ownership before enabling concurrent starts.
- 2026-09-11, Codex: preserve selected-target analysis while decoupling the
  entrypoint renderer. `VerificationGoTargets` selects `verify_by_default`
  targets (or the development fallback), not every build target; `compiler.Check`
  is compile-only. Removing the preparation analysis globally would weaken
  non-default artifact-target validation. Private composition cache inputs must
  also include workspace and implementation revisions: assistant registrations
  and generated descriptors consume them, unlike the public package projection.

## Outcomes & Retrospective

Final same-source numerical evidence is now complete but unsuccessful: semantic
edit median 5461.476051 ms versus threshold 3153.377720 ms; unchanged start
median 4032.029584 ms versus threshold 4017.245375 ms. The older 28.7% edit
improvement belongs to the previous source/measurement series and is not the
final retained candidate's measured result. This plan remains incomplete.

Not completed. Both numerical goals and final correctness acceptance remain open.

Latest retained increment: initial source scanning overlaps the remaining control
setup, with a 4032.029584 ms startup median (49.8% below baseline, 14.784209 ms
above target). Current-source CLI/full Go/race/lint, default and quick verifier,
worktree/dev-process probes, ONLV check/full Go/harness and all three strict
startup identity checks pass. The owned ONLV fixture is restored to its original
published pin and ready origin; module and handler diffs are empty. The last
semantic-edit median remains 4494.714741 ms on the preceding shared-decoder
source; no new edit-performance claim is made for this startup-only increment.
Full release certification was not selected. Older series below are historical.

### Fork/join measurement

The combined public/private Go preparation passed semantic edits at 4648.33,
4635.36 and 4585.15 ms (median 4635.36, 26.5% below the reference). Unchanged
starts passed at 4769.61, 4411.42 and 4418.78 ms (median 4418.78, 45.0% below
the reference). Labels: `latency-0179-shared-projection-{edit,start}-{1,2,3}`.
Source `f07cb7a5a2bfcd324b62ca31445f7658a860c4798f75c521e3ed0f5a5a3b9fc2`,
executable `c2e02cd32c5381e9b0c33ec02a3411f2e24d0d8bf4948be64e4f7ee9bbdb8c3f`.
Every sample passed served-identity and focused development acceptance. The
original handler was restored before an excluded warming start and the measured
unchanged starts. The second edit spent 348 ms checking the contract, 304 ms
preparing the combined Go projection, 331 ms preparing runtime identity, 1317 ms
in Go build, 266 ms preparing the retained candidate and 786 ms in preflight.
Current gaps are 1481.98 ms for edits and 401.53 ms for starts.

An isolated linker experiment retained all DWARF data but disabled compression:
the median fell from 1072.07 to 1017.44 ms while output grew from 53,477,026
to 87,557,794 bytes. This is not an end-to-end result; no linker policy change
was adopted. The full default verifier passed with existing knowledge,
architecture and aggregate timing warnings. ONLV `check -o json` and
`go test ./...` passed. Chrome loaded the AHJ catalog (28,355 entries), and
search returned the single City of San Ramon record with FIPS 06-68378.
The initial saved Alcosta scene reported an unavailable capture; this does not
establish 3D asset acceptance and was not repaired by this framework task.

Structured graph copying and narrowed Go metadata passed three semantic edits
at 4742.04, 4672.92 and 4707.64 ms (median 4707.64, 25.4% below the reference).
Three unchanged starts passed at 4416.07, 4498.37 and 4607.54 ms (median
4498.37, 44.0% below the reference). Labels:
`latency-0179-narrow-input-{edit,start}-{1,2,3}`. Source
`be8c3a157116b82bf245360ab87cd06982c25123fcc6b8b6ba904eaba17f49bc`, executable
`1d9309bd5d488d6216972feff62d87355d90103cd1b486f1ec5a09958390994d`.
The original handler was restored and its rebuilding start excluded before
the unchanged-start series. All identity and focused acceptance checks pass.
The first edit's graph check was 362 ms, private projection 164 ms, complete
Go input discovery 175 ms, input hashing 128 ms, and Go build 1416 ms.
Current gaps are 1554.26 ms for edits and 481.12 ms for starts.

The intermediate graph-copy-only series remains recorded at 6381.27,
4817.14 and 4754.46 ms. The first sample had a 2322 ms Go build alongside
observed unrelated host activity; it is not a clean isolated comparison and
was not silently discarded. The current-series medians above use all three
samples. Both new exact roots passed twenty serial samples at p95 0 ms
(event quantization); full verifier and lint pass, with existing freshness,
architecture and advisory aggregate-suite timing warnings. Named final-source
external probes and ONLV browser acceptance remain outstanding.

The subsequent declaration-only adapter cache passed edits at 5041.91,
4970.20 and 5108.68 ms (median 5041.91, 20.1% below the reference). Private
projection in the first sample fell from about 366 to 178 ms; contract
compilation remained 613 ms, runtime identity 543 ms, Go build 1204 ms and
candidate preflight 821 ms. Labels: `latency-0179-adapter-cache-edit-{1,2,3}`.
Source `e8fb7615be5333ebfb093fc919da21f7a643e7b1da2b5fcfd097eb10e695338c`,
executable `37f4f877150b55db416c25f42aaf3cef875b1c90244e89bbf3b4e685a1518098`.
All served-identity and focused checks passed; the summary edit is restored.
Generator tests, focused race tests, lint, three fixture regenerations, both
TypeScript checks and full verification pass. The new cache root has twenty
isolated samples with p95 0 ms (event quantization). Startup timing on this
source and final external acceptance remain unverified.

Source `b64c6f9d7df957c14a6ed2d3b5df2419265144893f12bd5d7971b7af411794f0`
and executable `8bae629cf67ae6d50bb6a8275b0af0d2c81d2ae61d45dbd2203c22db1dd905ef`
passed semantic edits at 5406.06, 5131.54 and 5653.80 ms (median 5406.06,
14.3% below the reference). Labels: `latency-0179-fork-join-edit-{1,2,3}`.
Unchanged starts 2/3/4 measured 5056.22, 4646.89 and 4826.47 ms (median
4826.47, 39.9% below the reference). Start 1 was 7523.52 ms after restoring
the original handler and rebuilding its pruned binary; it is retained as a
cold restoration sample, not an unchanged-start sample. Every sample passed
exact served identity and focused acceptance. The temporary summary edit is
restored; the owned fixture retains this source selection for continued work.

The first edit spends 584 ms on contract compilation, 145 ms on public Go
projection, 366 ms on private Go projection, 491 ms on runtime identity,
951 ms on full implementation verification and 1234 ms on Go build. The last
two overlap, but runtime-identity preparation precedes the build. Candidate
preparation and first preflight remain material sequential costs. Pure graph,
adapter and input-selection reuse are still open; validation results must not
be reused in place of current checks.

The full verifier passes with 41 knowledge-freshness and 22 architecture
warnings; lint reports zero issues. The corrected `dev-process` probe passes
in 53.513 s. Fixture generation, both TypeScript checks and 27 conformance tests
pass. Twenty serial isolated samples of each new root pass: prepared Go target
context p95 0 ms, workspace membership/bytes 10 ms, publication barrier 30 ms,
and cancellation/join 0 ms (Go event quantization). Final worktree/ONLV gates
and the two numerical thresholds remain outstanding.

The first bounded-parallel assistant experiment passed three unchanged starts:
7026.08, 6900.65 and 6893.92 ms (median 6900.65; 14.1% below final 0178).
Focused acceptance finished at 10205.85, 10030.14 and 10046.03 ms. Staging
took 1299, 1301 and 1271 ms versus about 2244 ms before. All samples passed
exact served identity and graph/workspace cache checks; these do not yet meet
the 4017.25 ms target. Source digest:
`c8a106ecdd7c7fd3ee96090f034658cbb62a13cfc9cd1e70bca9c81bfde1023e`;
executable: `70dba816fb4d506ab7497d48dd6e4f304807168c631acb3df84541b33472da40`.
Labels: `latency-0179-parallel-stage-start-{1,2,3}`. Subsequent code makes
the shared Node selection explicitly invocation-local so retries revalidate it.

That first implementation passed `go test ./cmd/scenery`, focused assistant
stage/preparation race tests, `golangci-lint run ./...`, the full verifier,
and `--probe dev-process --probe assistant-runtime` (53.010 s / 1.404 s).
The full verifier retains 41 knowledge and 22 architecture warnings.

The next implementation reuses the initial assistant-watch discovery compiler
snapshot during startup preparation, only after `compiler.SnapshotUnchanged`
verifies current membership, declaration bytes and the entire workspace
revision. Changed inputs still compile anew, and build diagnostics have separate
ownership. Its timing and complete validation remain pending.

That startup snapshot implementation subsequently passed starts at 6516.84,
6406.54 and 6403.82 ms (median 6406.54), labels
`latency-0179-startup-snapshot-start-{1,2,3}`. It used source
`66a678c7025c2dceb074615c987a3dafe0dc8eb1006ae7db039a86d46099cd30`
and executable `4f78023dbbe24598057dc4a435177bdbac5ab2cdbabf473a430dab18ca3ff006`.
All three current-source identity and focused checks passed. A further
operation-local reuse now carries the migration-preflight graph into watch
discovery, after a fresh complete snapshot check; its runtime timing is pending.

An edit-path experiment moves complete native Go analysis onto the exact
private workspace consumed by the following build. Entrypoint rendering uses
only the application name, so it need not wait for a typed model. This retains
full body/type checks and does not cache or combine Go type universes. The first
native process probe exposed that fresh generated runtime imports need module
tidy before workspace analysis; the preparation now primes that module under
the workspace lock and still reports native diagnostics before replacement.
The corrected `dev-process` probe passed (52.517 s). The experiment then
passed three semantic edits at 6483.68, 6343.72 and 6481.47 ms (median
6481.47 ms, worse than the 6306.76 ms reference). Private projection rendering
added about 366 ms while implementation analysis remained about 1480 ms;
the saved Go-build time did not offset that cost. The workspace-analysis
experiment and its extra generator/hook API were therefore removed. Full
native checking remains on its original path; startup snapshot reuse and
bounded assistant preparation remain.

Before removing that experiment, unchanged starts passed at 6038.32,
5925.94 and 5935.61 ms (median 5935.61 ms, 26.1% below the reference).
All six samples verified exact served identity and focused acceptance.
Source: `dfc2051be058db922e736a8490526ced8e6fd970f0b2c89b9b895affe0c0dc62`;
executable: `f39886c679598c9a640ab8dec28245ca35ac07b92e4155e6186d465c0acc81d1`.
Labels: `latency-0179-workspace-analysis-{edit,start}-{1,2,3}`. These are
intermediate measurements, not final-source acceptance. The temporary project
summary edits were restored immediately after measurement. Both 50% goals
remain open, as do final verification and fixture-selection restoration.

After removing the experiment, `go test ./internal/build ./cmd/scenery`
passed, `golangci-lint run ./...` reported zero issues, and
`go run ./scripts/verify --summary --write` passed with 41 knowledge and 22
architecture warnings. The full Go suite passed; its concurrent 6.971 s
duration is advisory, not an isolated timing result. Named final-source
external probes and final ONLV acceptance remain pending. No generator
implementation changes remain in the patch.

The four-worker file-copy experiment passed focused cache tests, their race
lane, lint, and the full verifier, but starts were 6425.11, 6105.64 and
6333.54 ms (median 6333.54). Per-assistant copying remained about 1280 ms;
concurrent metadata/writes erased any benefit. That experiment was removed
in full. Source: `23cfa8b1168990b56b883ce44f158bebf65ba880d9a63986859123eeeb52b5e1`;
executable: `6aa7cad13a35e47a87b5a2e43d87f3e93d2757d0e60f9b92482ded9df791ee8a`.
Labels: `latency-0179-parallel-copy-start-{1,2,3}`. No fixture handler edits
were needed for this series.

The explicitly requested AskOracle skill became discoverable locally during
this continuation. A single consultation was submitted through Chrome with
the actual GitHub connector, UI model `6 Pro` (Latest, Pro power), and the
sanitized `.scenery/harness/oracle-0179-request.md` attachment. The GitHub API
confirmed accessible base `11f3b5b8504e659b0c339bd6043fb666fb562685`, but
returned 422 for local HEAD `2c1bcfc2ef8fd38e7e3651a3f27071b8c6ed2fe0`;
the attachment therefore includes the relevant full overlay from that base,
including unpublished committed changes and untracked source files. No push
was made. Consultation URL and monitor state are recorded in the local
consultation artifact. The accepted conversation is
`https://chatgpt.com/c/6aa3543c-2cf8-83eb-bae3-543508184539`; temporary
heartbeat `scenery-0179-oracle` checks it every five minutes and must be
deleted after the entire final answer is captured.

A separate disposable copy-on-write experiment used macOS `/bin/cp -cR`
on an existing 8046-entry prepared cache tree, then verified the entire
descriptor and checked that a destination write did not alter the source
inode/content. Three copy-plus-verification samples were 2298.44, 2211.85
and 2291.55 ms (median 2291.55 ms), slower than the existing approximately
1195 ms serial verified copy. Copy alone was 1496.70–1563.25 ms. It is not
a production change and does not establish a speedup; each owned temporary
destination was removed after its check. The reproducible local probe is
`.scenery/harness/measure-assistant-clone.go`.

The retained implementation subsequently passed the explicit probe command
`go run ./scripts/verify --summary --write --probe worktree --probe dev-process --probe assistant-runtime --probe build-info`.
Worktree runtime/PostgreSQL acceptance passed in 271.868 s, managed process
startup in 53.879 s, assistant production process in 1.511 s, and trimpath
build identity in 0.518 s. Schema and contract checks passed with the same
41 knowledge and 22 architecture warnings. This selected-probe run does not
claim full release coverage or final latency acceptance. Oracle remained
visibly generating at the 01:18 UTC check; no final advice was captured yet.

After all rejected experiments were removed, the retained source was selected
into the owned fixture and warmed outside measurement. Three unchanged starts
passed at 6024.65, 5899.93 and 5805.44 ms (median 5899.93 ms, 26.6% below
the 0178 reference). Focused acceptance finished at 9243.93, 8979.83 and
8942.83 ms; all samples passed current-source served identity. Source:
`e8a73e6d9c5b44ecef52bf3309dd8df0457c6d549e1b0f7c309dccde98407cc6`;
executable: `56b8e9889e0269599c591485011d88e9d2f0edb22bb1026694d6dd96ffb2d976`.
Labels: `latency-0179-retained-start-{1,2,3}`. Remaining sequential phases
include control 813–815 ms, initial scan 238–248 ms, workspace refresh
829–855 ms, database setup 690–756 ms, assistant staging 1248–1285 ms,
and assistant startup 727–732 ms. This verifies the retained improvement,
not either 50% goal. No handler edits were made in this series.

Oracle completed after 25m 23s; the entire final answer was captured in this
task at 01:33 UTC and `scenery-0179-oracle` was deleted (confirmed by the app
and absence of its automation record). The local assessment is
`.scenery/harness/oracle-0179-assessment.md`. The source confirms its reported
failed-start helper shutdown gap. Additional inspection found that repeated
`ManagedProcess.Stop` lost an earlier error through a local variable inside
`sync.Once`; it now retains that error until `Done` confirms actual exit.
Failed-start helpers retain their process, private files and PID, block retry,
and remain available to repeated cleanup. Focused fake-time tests cover client
and readiness failures plus eventual confirmed exit.

The next startup candidate overlaps private assistant staging with existing
framework/workspace/DB preparation. It joins or cancels and joins every stage,
matches the final graph/definitions and revalidates the complete compiler
snapshot before activation. A changed graph discards the speculative stage.
At most two distinct helper handshakes run after the API listener; callbacks
and shared output are serialized. Focused concurrency/ownership and race tests
pass for the helper-start work; integrated timing and named probes are pending.

## Context and Orientation

`cmd/scenery/dev_build_pipeline.go` owns build orchestration; `internal/build`
owns workspace and bundle verification; `internal/parse` owns Go analysis;
`internal/generate` owns typed projections. The supervisor owns candidate
preflight and assistant lifecycle. Existing trace spans and ONLV
`development/measure-latency.ts` establish observed completion.

## Milestones

First identify avoidable repeated work, then implement bounded changes with
invalidation and ownership tests. Evaluate actual runtime timings after each
material change. Continue until both thresholds are independently verified.

## Plan of Work

Inspect live source and complete inputs before reusing analysis or artifacts.
Prefer operation-local reuse and exact-byte validation. Do not introduce
mtime-only authority, mixed Go type universes, shared writable hard links,
persisted database-success gates or alternative runtime protocols. Record
unsuccessful experiments rather than selecting convenient samples.

## Concrete Steps

Use task-scoped `scenery inspect docs --for-path <path> -o json` and owning
instructions before edits. Run affected Go package tests, `go test ./...`,
`golangci-lint run ./...`, and `go run ./scripts/verify --summary --write`.
Generator changes also require the two committed native/house TypeScript
fixture generations, assistant fixture generation, conformance tests and both
generated-client/catalog typechecks from `internal/generate/AGENTS.md`.
Select `--probe dev-process --probe build-info --probe assistant-runtime`
for changed runtime/cache boundaries, and `--probe worktree` if retained
producer/worktree behavior changes. Add PostgreSQL proof only if its boundary
changes. Full release and all-root test timing remain unselected.

Use `/Users/petrbrazdil/Repos/onlv-coherent-task-environments`, marker-owned
port 4070, with an explicit local Scenery source selection. Preserve the
personal ONLV 4920 runtime. Run the canonical latency driver for at least three
uncontended samples per workflow; semantic edits must change the real project
summary, response process and implementation identity. Stop the fixture before
each unchanged-start sample. Record source/executable digests and all samples.
Finish with `./scripts/scenery check -o json`, `go test ./...`,
`./scripts/scenery harness -o json --write`, `just smoke`, and
`just feature ahjs` against the intended source. Restore authored inputs and
the original published selection without deleting data.

## Validation and Acceptance

Both final medians must satisfy the exact thresholds above. Report individual
samples, ranges and medians, not a three-sample percentile. Warm-up, source
restoration builds and overlapping unrelated checks are separate evidence.
Required invalidation includes declarations, signatures, shared types, build
tags, imports/modules, producer, generated-file tampering and concurrent edits.
Failed preparation cannot stop the previous generation; replacement rollback
must retain exact executable/environment identity and confirmed shutdown.

### Early-stage startup evidence

The startup overlap and bounded helper starts passed three unchanged starts at
4601.936500, 4609.051292 and 4724.785000 ms (median 4609.051292 ms,
42.63% below the 8034.490750 ms reference). The 4017.245375 ms target remains
591.805917 ms away. All three exact served-identity and focused checks passed;
focused acceptance took 7743.188333, 7736.616083 and 7915.160875 ms.
Labels: `latency-0179-early-stage-start-{1,2,3}`. Source:
`b5817ce4c026050f3de2104359187dde03230503a7bf51a7a2abd6d32d3ebaf2`;
executable: `478adaf2179fa5566632597a3f4b8fdd7aacd5500cd11c272eabddea7ed14ea2`.
The assistant-stage join added zero measured phase milliseconds in each run;
helper startup took 401, 393 and 391 ms. Workspace contention remains visible,
so further copying concurrency is not indicated. This source has not yet been
measured for semantic edits and is not final acceptance.

Affected package tests, focused race tests, lint and the full verifier passed.
The `dev-process`, `assistant-runtime` and `build-info` probes passed (51.492 s,
1.400 s and 0.353 s). Five changed exact test roots each passed 20 fresh isolated
runs with measured p95 at most 10 ms; zero values reflect Go event quantization.
The full verifier retained 41 knowledge and 22 architecture warnings. The final
worktree probe, ONLV smoke/AHJ acceptance and fixture restoration remain open.

## Idempotence and Recovery

Preserve all unrelated source and personal runtime state. Use retained runtime
controls before changing selection. Restore only owned temporary edits after
checking for drift. Never install to the shared Go binary path, push, alter
ONLV's published pin or delete fixture data without explicit authorization.

## Artifacts and Notes

Baseline raw evidence is the owned fixture's ignored
`.scenery/harness/loop-attribution.json`, `latency-0178-C-*` labels. Keep new
measurements under distinct `latency-0179-*` labels and record their identities.

## Interfaces and Dependencies

No public CLI/schema addition is planned. Update affected current docs and
schemas together if implementation requires a contract change. Avoid new
dependencies or environment knobs without a justified recorded decision.
