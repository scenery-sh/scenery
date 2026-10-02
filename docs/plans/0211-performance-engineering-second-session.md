# Performance Engineering Second Session

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries, Decision Log and Outcomes & Retrospective current as work proceeds.

## Purpose / Big Picture

Spend at least five hours improving native Scenery performance and stability without adding speculative caches or weakening correctness. This session starts at 2026-10-01 19:23:12 UTC and may close no earlier than 2026-10-02 00:43:57 UTC (extended by the developer at 19:43:57 UTC). It builds on completed plans 0208–0210 and the separately measured module-input follow-up. The developer explicitly authorizes implementation and targeted before/after native benchmarks, requires no subagents, and has not authorized installation, commits, pushes or publication.

## Progress

- [x] (2026-10-02 00:10 UTC) All four sixty-minute cohorts complete: 144,000 read-only requests, zero failures, 48 stable process fingerprints per hour, exact repeat source/executable/observer identity. Runtime allocation flow falls approximately 0.75% and 0.62%; candidate descriptor deltas are 0 and -2. HTTP p95 worsens in both full comparisons and repeat candidate startup RSS is higher; neither HTTP nor RAM gains are claimed. Captured unrelated native race load reaches 1,975% CPU/load 42 during the worst interval. The hook-free shipping producer is now in its 35-minute acceptance; app checks and closing validation remain pending.

- [x] (2026-10-01 23:02 UTC) Repeat baseline completes 36,000 requests with zero failures, 48 stable process fingerprints, zero descriptor growth and 118 GC cycles. Identical baseline source/executable yields HTTP p95 3.436ms and quiet CPU median 5.361% of one core, versus 7.028ms and 9.279% in the first baseline. Host/time variation exceeds the small shipping allocation gain; no causal CPU or HTTP win is claimed. Candidate repeat observer PID 74631 is live in warmup. Baseline 2,264-file immutability and current product/profiler byte equivalence pass again; main Scenery remains clean and main ONLV supervisor identity is preserved.

- [x] (2026-10-01 21:55 UTC) First candidate cohort completes sixty measured minutes with 36,000 requests, zero failures, 48 stable process fingerprints and zero descriptor growth. Allocated MiB/s falls from 4.55555 to 4.52158. Whole-run HTTP p95 is 7.111ms versus 7.028ms before, so latency improvement is not established. Repeat baseline/candidate cohorts remain required; the native sequence runner is live and repeat baseline is ready.

- [x] (2026-10-01 20:55 UTC) Baseline completes sixty measured minutes: 36,000 read-only requests, zero failures, all 48 process fingerprints stable, descriptor delta +2, post-GC heap 151.08 to 152.04 MiB. Candidate snapshot is identity-attested (source 118f017a..., executable a1a9f5a...), its observer PID 6018 is live, and measurement starts at 20:54:56 UTC after equal warmup/profile setup.

- [x] (2026-10-01 19:43 UTC) Developer reinforced another five hours of continuation. This block must now finish no earlier than 2026-10-02 00:43:57 UTC. The existing goal remains active.

- [x] (2026-10-01 19:23 UTC) Confirmed main Scenery clean at a1fd9485356a5e7a0d117ce62870a300296bd5ab and existing performance worktree state.
- [x] (2026-10-01 19:25 UTC) Frozen 2,264 nonignored source paths in the independent session-two baseline; source manifest and bytes retained.
- [x] (2026-10-01 19:38 UTC) Semantic validator children reuse one local address index; specification-only contextual before-image removes unnecessary provenance copies. Eight-pair incremental allocation measurements and complete native/house/ONLV semantic parity pass. Timing benefit remains uncertain.
- [x] (2026-10-01 20:09 UTC) Watcher reuses its already computed relative and absolute paths; direct metadata reads remove 77,344 allocated bytes and 570 allocations per warm fixture scan. Sequential aggregate timing measurement is running; runtime control cohort remains live.
- [x] (2026-10-01 20:55 UTC) Current full verifier, lint, catalog typecheck, both unchanged client regenerations, compiler/CLI races, valid and invalid semantic parity, forty-one affected isolated root timings, and dev-process/native-contract/storage probes pass. Current normal-producer app acceptance and final closing receipt remain pending.
- [x] (2026-10-02 00:45 UTC) Completed more than five hours from the developer extension: normal hook-free producer serves 21,000 requests with zero failures, 48 stable native identities, HTTP p95 3.119ms and descriptor delta -48. Current app check, Go suite, runtime harness, declared quick repo-harness and logs pass. Owned runtime is identity-guarded down; all 48 old native identities are absent and SQL/storage state is retained. Baseline/source checks pass again. Closing full verification follows the plan/index update and is retained as a separate current receipt.

## Surprises & Discoveries

- (2026-10-02 00:10 UTC) External native race/concurrency work in another ONLV checkout overlaps the repeat candidate and consumes almost twenty cores. Both DNS and non-DNS latency spike; retain the adverse p95/p99 results and do not subtract this window to manufacture a latency win. Repeat candidate startup RSS/HeapSys reservation is also higher (737/695 MiB versus baseline 403/499 MiB) despite stable approximately 151 MiB live post-GC heap. Startup memory attribution remains unresolved. No physical-RAM or causal CPU/HTTP gain is established by this block.

- (2026-10-01 20:55 UTC) Audited the high descriptor floor instead of assuming a counting error: lsof selects only the candidate supervisor, 6,071 numeric rows are 6,071 unique descriptors (5,060 regular files, 892 directories, plus pipes/sockets/kqueue). The existing fsnotify kqueue backend opens watched entries. Descriptor growth and verified cleanup remain distinct from this platform-specific absolute floor; no watcher-membership shortcut is introduced.

- (2026-10-01 20:33 UTC) Both refreshed ordinary roots pass twenty isolated runs with p95 below the 10ms reporting resolution; all other 39 measured affected roots remain below 100ms (maximum 70ms). Preserved full router native journey passes directly and through dev-process; storage probe passes its tenant-isolation journey. The first tagged router build lacked net/http and was corrected; lint then required cleanup error handling, now propagated through the native probe result instead of ignored. Refreshed full/lint/probe validation is running.

- (2026-10-01 20:25 UTC) Forty-one affected roots received twenty isolated native samples each. Two pre-existing OS-boundary tests exceed the 100ms p95 policy (storage 220ms and router 280ms), so they are not reported as passes. Preserve their complete native assertions in explicit storage/dev-process probes; ordinary roots cover tenant/prefix dispatch and dynamic retry policy directly without weakening production durability or hiding setup cost. Storage native proof already passes; router probe and refreshed isolated roots are pending.

- (2026-10-01 20:15 UTC) Native dev-process and native-contract probes both pass with their cleanup assertions, but the selected probe run rejects this plan for a missing required living-document sentence. Added that sentence. Probe mode does not run the complete Go suite; a separate full verifier is selected instead of treating probe success as full-suite evidence.

- (2026-10-01 20:09 UTC) Corrected ignored invalid-corpus HCL block formatting and explicitly refreshed its file-driven measurement (ordinary Go cache did not track those external fixture bytes). Nine of ten complete compiler payloads match exactly, including valid contextual scalar provenance; the CLI-invalid case preserves all diagnostic rows and remaining payload but inherits pre-existing map-order nondeterminism. Original fixtures and raw observations remain preserved.
- (2026-10-01 20:09 UTC) Sequential twelve-pair complete compilation reduces median allocated bytes by 8,761,020 and allocations by 38,617. Ten timing pairs improve and two worsen; median paired time ratio is 0.97683. Timing attribution remains scoped to this native fixture. A watcher command initially selected a nonexistent benchmark name; exit failure is retained and selection corrected before measurement.

- (2026-10-01 20:01 UTC) The first phase-three compiler and direct-stat watcher samples were inadvertently launched concurrently. Their allocation counters remain evidence (all eight pairs lower), but timing attribution is rejected. A sequential aggregate compiler comparison is selected next; raw contended runs are retained.
- (2026-10-01 20:01 UTC) Empty-map fast-path measurement after correction improves from 38.37 to 32.235 ns/op; 9/16/50/100-member clones avoid growth and materially reduce time/bytes. One/eight-member results remain near parity; no blanket map-size speedup is asserted.

- (2026-10-01 19:58 UTC) Rejected an Entry prototype pointing into immutable member arrays. It reduced warm scan allocated bytes from 5,267,184 to 4,832,256, but retaining one entry after dropping the walk pinned about 3,990,376 bytes for a 100,000-member directory, versus no material retained increment before. The prototype is restored out of shipping source; exact before/after code, binaries, raw allocation and retained-heap evidence remain ignored.
- (2026-10-01 19:58 UTC) Private watcher candidates already contain the exact path that Entry.Info reconstructs before os.Lstat. Calling os.Lstat on that path directly removes the redundant join and allows candidates to stop retaining the directory-entry interface. Regular-file/physical identity checks and current metadata reads remain unchanged.
- The benchmark parser initially assumed allocation metrics immediately followed ns/op, but watcher output has custom directory/file counts between them. Raw successful first-run output is preserved and the parser now accepts the intervening metrics; no missing result is reported as a pass.

- Conversation interruption terminated the managed runtime observer after 1200 measured requests; raw incomplete evidence is preserved in s2-baseline-interrupted-soak and is not an accepted cohort. The same live supervisor remains running. A detached observer records its exact native PID/start identity and resumes a new complete cohort with the frozen observer bytes.

- (2026-10-01 19:38 UTC) Five semantic child indexes duplicate the parent's existing immutable-phase map. Synthetic 500-resource validation falls from 787,825 to 131,304 B/op; complete ONLV compilation falls from 431,345,828 to 429,757,392 B/op.
- (2026-10-01 19:38 UTC) Contextual provenance reads only resource addresses and specs, but its before-image deep-cloned every Origin. Copying only the participating fields removes about 6.8 MB and 31,400 allocations per full compilation. Eight incremental pairs confirm the allocation gain; time ratios straddle parity.
- Tool-only setup mistakes: an initial baseline overlay command used the baseline cwd for candidate-owned evidence paths; corrected to absolute paths. The paired runner initially retained relative binary paths across a package cwd; corrected with Path.resolve(). No product change or failed comparison was accepted.

Available disk space at entry is 18 GiB. New baselines contain source only; retain necessary raw measurements and exact binaries without copying dependency caches or fixture data. Earlier allocation gains did not establish lower whole-application CPU or HTTP latency. These remain separate measurement questions.

## Decision Log

- 2026-10-01, Codex: preserve the measured direct spec-index overlay as a follow-up proposal instead of applying it during frozen runtime cohorts. Reason: it saves another approximately 517 kB per compile, but timing is unproven and changing shipping source would invalidate producer equivalence. The patch is not an accepted shipping gain.

- 2026-10-01, Codex: reject the smaller Entry prototype despite an allocation win. Reason: it introduces whole-directory retained-array coupling for a single exported entry, contradicting the no-regression/no-debt objective.
- 2026-10-01, Codex: keep map-clone pre-sizing with the existing empty-map fast path. Reason: known nonempty capacity avoids growth; empty/wrong-typed/nil inputs still yield a nonnil empty map and do not pay capacity setup.

- 2026-10-01, Codex: pass the existing address map directly to private semantic child validators, update existing private-call tests without compatibility wrappers, and leave spec ownership unchanged. Reason: no new retained state or graph mutation is needed.
- 2026-10-01, Codex: copy only Address and deeply cloned Spec into the contextual-provenance before-image. Reason: the comparison reads no Origin fields; source/effective/expanded view ownership stays on the existing deep clone.
- 2026-10-01, Codex: begin a 60-minute baseline runtime observation with five-minute warmup and the same frozen observer used in prior matched cohorts. Reason: longer CPU/GC/FD/process and request evidence can resolve watcher cost without conflating local allocation gains with runtime latency.

- 2026-10-01, Codex: work in /Users/petrbrazdil/.codex/worktrees/performance-review/scenery, retain previous uncommitted changes, use no subagents, and preserve completed numbered plans. Reason: explicit developer authorization and existing ownership.
- 2026-10-01, Codex: use local immutable-phase indexes and removal of redundant work before retained graph caches. Reason: existing allocation profiles identify repeated indexes and graph copies; lifetime-local reuse has a small correctness surface and no invalidation policy.

## Outcomes & Retrospective

Completed the authorized second block after its extended minimum end time. Current complete ONLV compilation allocates approximately 8.76 MB less (2.03%) and performs 38,617 fewer allocations (1.23%); twelve sequential paired runs show a median time ratio 0.97683. The warm watcher allocates 77,344 fewer bytes (1.47%) and 570 fewer objects (1.14%), with paired median time ratio 0.97665. Two adverse timing pairs remain in each fixture comparison. Private local index reuse, reduced before-image fields, known capacities and existing path reuse introduce no new dependency, environment knob, retained graph cache or compatibility surface.

Four sixty-minute frozen profiler cohorts complete 144,000 successful requests; normal hook-free acceptance adds 21,000. Each cohort preserves all 48 recorded native process identities. Exact valid payloads and complete invalid diagnostic-row parity, generated-client equality, targeted root timing, current repository tests/races/lint/catalog checks and selected native storage/dev-process/native-contract probes pass. Normal app check, Go suite, runtime harness, declared quick repo-harness and a 500-event log inspection also pass. Stop signals only the verified task-owned instance; data and unrelated runtimes remain preserved.

Runtime allocated-byte flow falls about 0.75% and 0.62%. CPU/HTTP/physical RAM wins are not established. Adverse HTTP and startup-RSS observations are retained: another checkout's native race load consumes almost twenty cores during the worst request window, and repeat candidate startup RSS/HeapSys is higher despite stable approximately 151 MiB live heap. Twelve fresh-process compiler RSS pairs also fail to show lower peak RSS (paired median +1.48%, descriptive interval spans parity). These limits preclude a blanket no-performance-regression claim beyond the measured fixtures and correctness gates.

Rejected directory-entry retention is absent from shipping source. A direct address-to-spec before-image prototype removes another approximately 517 kB per compile, but remains an explicitly measured follow-up patch outside the frozen accepted candidate, with timing unproven and promotion gates pending. It is not counted as shipped improvement or a required part of this block.

The closing full verifier refreshes final documentation/index inputs; its receipt, final recommended-command audit and command/result ledger live under .scenery/harness/perf-session2. Earlier completed plans remain immutable. No global installation, commit, push or release was performed. Public contracts and instruction documents are intentionally unchanged by this implementation-only session; the owning probe catalog and living plan/index metadata are updated.

## Context and Orientation

The shipping candidate is the existing perf/runtime-resource-efficiency worktree. Its independent baseline is /Users/petrbrazdil/.codex/worktrees/performance-session2-baseline/scenery, detached at a1fd9485 with the exact current nonignored source bytes copied in. .scenery/harness/perf-session2/baseline-manifest.json records all paths and hashes. The baseline includes the previous module-input temporary-slice removal; never compare later work only against the older main checkout and label it this session's gain.

internal/compiler loads and validates source into independent immutable source/effective/expanded graph views. Its private resourcesByAddress helper builds shallow Resource values whose Spec maps retain the resource ownership; validators must not mutate their shared inputs. cmd/scenery/watch* owns captured source bytes and their filesystem identity; internal/watchignore and internal/dirlisting supply bounded helper caches. Source membership and bytes remain authoritative.

The task-owned ONLV fixture is /Users/petrbrazdil/Repos/onlv-scenery-perf-followup. It starts stopped and selected to the preceding producer. The primary /Users/petrbrazdil/Repos/onlv and all unrelated app instances are outside mutation scope. Existing helper scripts under .scenery/harness/perf-session1 record process identity, source origin/digests, executable digests, and framework input bytes; inspect current ownership before reusing them. Do not use paths alone to attest a materialized framework source copy.

## Milestones

First establish exact source and native profile controls. Next retain only bounded improvements supported by randomized repeated measurements and semantic/input-ownership checks. Finally use required repository verification and any named native probes for changed OS/process boundaries, then measure the normal producer where runtime behavior changed. Keep the CPU, allocation, retained-memory, and timing results separate.

## Plan of Work

Inspect the allocation profile's concrete callers, benchmark a bounded candidate against the frozen session-two source, and change one ownership boundary at a time. Reuse a read-only index only inside a fixed resource phase; never keep one across expansion or mutation. Preserve diagnostic ordering and duplicate-address precedence. For watcher work, preserve exact membership, nonregular/symlink refusal, physical-file identity and fallback parsing when metadata is unknown. Retain rejected candidate code only as ignored evidence, restoring shipping source before continuing.

Use longer native runtime controls only when a changed path can affect them. Record every request/process/FD failure and adverse cohort; do not infer CPU or HTTP gains from allocation changes. End the block with reviewable code and evidence, not a new installed global binary.

## Concrete Steps

From the candidate root, record raw commands and stdout/stderr in .scenery/harness/perf-session2/. Build benchmark-only test files through Go overlays so ordinary tests do not acquire benchmark fixtures. Run before/after lanes sequentially in randomized pair order with the same fixture bytes and observer implementation. Compare complete semantic compiler outputs for internal/compiler/testdata/native, internal/compiler/testdata/house and the owned ONLV fixture. Add focused ownership tests only when a new sharing boundary needs proof.

After each accepted compiler group run `go test ./internal/compiler ./internal/parse`, then required client regenerations. After accepted watcher changes run `go test ./cmd/scenery ./internal/watchignore ./internal/dirlisting`. Record product-source and benchmark-source hashes. Keep the active plan's progress, surprises, decisions and outcomes current.

## Validation and Acceptance

All commands below run from /Users/petrbrazdil/.codex/worktrees/performance-review/scenery unless another cwd is explicit. Cumulative changed-area classes already include cli-json-contract, compiler-or-generator, go-package, release-sensitive-or-runtime and ui-catalog; select full directly. After final edits execute `go run ./scripts/verify --summary --write`, inspect current .scenery/harness/agent-context.json and fulfill its complete recommended-command union. The full verifier's current repository Go suite satisfies `go test ./...` and included affected packages; preserve its receipt.

Execute `golangci-lint run ./...`; `go test -race ./internal/compiler` for shared compiler ownership; `go test -race ./cmd/scenery ./internal/watchignore ./internal/dirlisting` if watcher source changes. Regenerate both committed clients with `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json` and the same command with internal/compiler/testdata/house. Execute `tools/typescript/node_modules/.bin/tsc -p internal/generate/testdata/tsconfig.catalog.json` if UI/client inputs differ from their last verified source; otherwise record their exact unchanged hashes and reused receipt. `git diff --check` must pass.

For any source-capture, process, or build boundary changed, execute `go run ./scripts/verify --summary --write --probe dev-process --probe native-contract`, retaining its assertions and cleanup. Skip new boundary probes only if the final delta is private compiler allocation work with unchanged public/runtime/process/source-capture behavior; record the source diff and exact-output comparison supporting that condition. If runtime code or source-capture behavior changes, use the owned ONLV fixture's ./scripts/scenery wrapper, current generated framework selection, doctor JSON and current ps identity, plus `./scripts/scenery check -o json`, `go test ./...`, `./scripts/scenery harness -o json --write` from that app root and its declared checks. A normal-producer current identity-attested read-only request sequence must succeed with stable process identities and bounded descriptors. Stop only the session-owned app afterward.

Investigate affected roots observed above the 100ms threshold with twenty exact-root isolated native fresh process samples. Do not turn one concurrent-suite timing into a violation or alter timeouts to conceal it. Release certification and a whole-repository fresh timing audit are not selected; Linux runtime performance remains unmeasured.

## Idempotence and Recovery

Source snapshots and measurement assets are session-owned and ignored. Verify the baseline hashes before accepting comparisons; never update it after shipping edits. Keep exact patches for rejected experiments and restore only files this session changed. If interrupted, consult this plan and running session IDs/process fingerprints before restarting observers. No blanket kill, cache deletion, framework adoption or database reset is authorized. Generated outputs can be regenerated atomically through the existing command. Completed plans 0208–0210 remain immutable.

## Artifacts and Notes

Evidence lives under .scenery/harness/perf-session2/. Entry source digest (uint64 length-framed sorted path/kind/bytes) is 8ffb0832f9c88db5d7d0ca18c6fcf069e2373f40addefbfffbb50d6954cce4d7. The initial 2,264 paths occupy 19,472,740 bytes. Preserve raw runs, command/exit-code ledgers, complete semantic parity hashes, profiler binaries and sample identities. Ignore all .scenery evidence in commits.

## Interfaces and Dependencies

Keep the existing current CLI, machine schemas, Scenery source format, generated clients, graph views and runtime contracts. Prefer the standard library. No new dependency, environment knob, retained graph cache, compatibility route, test timing exception or global installation is planned.
