# Shared Executable Reuse Domain

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under `PLANS.md`.

## Purpose / Big Picture

Close the remaining mutable-external-input time-of-check/time-of-use (TOCTOU)
gap in shared executable publication. A compiler can consume source B while
both input inspections see A. Restored bytes, size, modification time, mode and
file identity cannot prove what the compiler read when change time is absent.
Do not substitute another metadata heuristic for an ownership boundary.

This plan narrows shared executable reuse to inputs owned by the locked private
build workspace and the selected Go toolchain. Builds that consume mutable
external source, including local replacements and framework checkouts, still
compile normally but cannot read or publish shared executables. Go's own package
cache, captured application inputs, current validation, and independently owned
running/rollback executables remain unchanged. No public API or new mode is added.

Plan 0182 is immutable historical evidence: its input observations catch some
changes but do not establish this invariant. Plans 0180 and 0181 retain their
unmet architectural/latency gates; this is a correctness repair, not renewed
monolithic-build optimization or a performance acceptance claim.

## Progress

- [x] 2026-09-13: Confirm clean checkout at
  `3ab90b51492ade089c24e850cd27a1c9f202e1d6` on
  `feat/incremental-worktree-native-loop`; read owning instructions and trace
  compilation, input discovery and publication. No subagents or remote writes.
- [x] 2026-09-13: Allocate 0183 and record the enforcement strategy and commands.
- [x] 2026-09-13: Reproduce compilation of B followed by A restoration without change time;
  enforce the supported reuse domain before both cache lookup and publication.
- [x] 2026-09-13: Remove metadata as publication authority, preserve existing live
  validation, add negative/control coverage and real native probe evidence.
- [x] 2026-09-13: Run the exact cumulative validation union, review alternate bypasses,
  update current documentation and record acceptance evidence.

## Surprises & Discoveries

`produceSharedBinary` invokes the compiler in `Result.Dir` but Go follows
replacement/module paths outside that lock. `verifySharedBinaryInputs` then
re-reads live bytes and compares run-local file/directory stamps. With B restored
to A, stamps without change time are identical. Neither this comparison nor a
content-addressed directory name makes an external directory immutable.

The three initial deterministic regressions failed against 0182: local and
framework B were published under A's key, and a seeded entry skipped compilation
entirely. With the ownership restriction, these regressions and a legitimate
owned-workspace hit pass. The native tagged probe also compiled and executed B,
restored A before the input guard, and proved no shared entry plus a fresh A
retry. It passed all existing cross-process cases in 4.899 seconds before the
final cumulative run.

Twenty isolated processes per exact root confirmed the five new roots at
20–30 ms p95, but the existing full-fixture cache corruption/reuse root reached
110 ms. Its fixture loaded the complete compiler/generator identity despite
testing only artifact ownership/copy/repair. Replaced that setup with the same
small owned-workspace fixture used by the new domain control, retaining every
assertion and actual store operation. Original raw timings remain in
`.scenery/harness/0183-review/timing/`; the corrected root gets a separate rerun,
not selective sample replacement. A second quick failure was the invalid
escaping-embed test encountering the existing `/var` symlink rejection before
admission; the test now recognizes that earlier fail-closed boundary.

The corrected cache-control root passed a separate 20-process series at 50 ms
p95 (60 ms worst). The other 14 roots passed at 20–90 ms p95. The first seven-probe
union passed generation, build-info, parallel-runtime, core-separation and all
A1–A17 worktree cases with verified cleanup, but two probes still required the
now-forbidden whole-executable hit/publication for external frameworks. Their
current assertions require exactly one private native build, positive bypass,
current input checking, link-budget admission and no shared action, correlated
to the successful request. They also retain graph/projection/write-count and
normal-endpoint generation assertions. A pure verifier test rejects old-generation,
cached/published, unchecked, uncompiled and unbounded substitute evidence.
The targeted `--probe dev-process --probe native-contract` rerun passed. The full
union is rerun below against the final prepared producer rather than attributing
the earlier five passing probes to its later framework source identity.

The separate local bypass/compatibility review identified two controls worth
retaining: unsupported private builds must still use the existing fair link
budget, and the old best-effort mutation detector must still reject changes it
can observe. Neither may grant cache admission. The first quick run found only
a missing living-document statement in this plan; source tests and lint passed.

## Decision Log

- 2026-09-13, Codex: choose the explicitly authorized narrower reuse domain.
  Snapshotting arbitrary replacements, module resolution and native inputs is a
  separate materialization change. Rejecting unsupported cache reuse is the
  smaller complete correction; ordinary compilation is still supported.
- 2026-09-13, Codex: metadata is not cache-admission authority. Unknown input
  provenance fails closed. Check admission before any shared artifact side effect,
  including hits and joining another producer; retain current post-build input
  checks as freshness checks, not proof against external A-to-B-to-A changes.
- 2026-09-13, Codex: perform the security skill's investigation and bypass review
  as separate local passes because project/user instructions prohibit subagents.
- 2026-09-13, Codex: admit only a standalone module (no require, replace or
  exclude directives), current non-toolchain files inside the locked workspace,
  pure Go without custom tool flags, and the exact discovery environment. Missing
  module/entrypoint data, persisted manifests and the unimplemented Windows
  workspace lock fail closed. The selected Go toolchain remains the existing
  trusted producer boundary; this is not sandboxing a compromised toolchain.
- 2026-09-13, Codex: keep the existing metadata observations as best-effort
  freshness rejection, including its old regression. They never authorize reuse;
  the new regression forces unavailable change time and identical other stamps.
  Keep the same link scheduler for private builds, without shared subscribers or
  artifact lookup/publication. No second scheduler or cache version is needed.

## Outcomes & Retrospective

Completed the requested shared-publication correction using the narrower reuse
domain. A mutable external compilation cannot look up an existing shared entry,
join another shared producer, or publish new shared bytes. Unknown provenance
and persisted manifests also cannot grant admission. Current live checks, the
existing detectable-mutation rejection, fair link scheduling, producer ownership,
corruption/permission recovery and independent retained executables remain.

The deterministic regression models a compiler reading B while final bytes and
all metadata observed by the input checker return to A with change time
unavailable. It fails on the previous implementation and now passes for local
replacement and framework source; a seeded matching entry cannot bypass the
compiler either. The explicit native probe executes a real Go-built B, restores
A before the guard, proves no shared publication, and rebuilds A on the next
request. A supported owned-workspace control still publishes/hits and executes
current input checks. This is not a claim that private external-source
compilation has become an immutable snapshot.

All final required commands and all seven selected probes passed. The normal
endpoint served the edited implementation with its exact linked identity. Both
the implementation edit and unchanged public restart explicitly proved private
compilation/bypass; graph and Go/TypeScript projection reuse remained intact,
with one workspace file written for the implementation edit. Worktree A1–A17
passed and reported verified removal of owned clusters. Sixteen exact test roots
have 20-process confirmation below 100 ms p95: new build roots 20–30 ms, the
corrected cache-control root 50 ms, other affected controls at most 90 ms, and
the pure probe-evidence root below the 10 ms reporting resolution.

The tradeoff is intentional: ordinary applications with external Scenery/module
source no longer reuse complete executables. Go package caching remains. No
immutable replacement materializer, runtime mode, SDK change, latency claim,
global install or remote Git mutation was added. Plans 0180/0181 remain open.

## Context and Orientation

`internal/build/compile.go` holds the private workspace lock until native build
and implementation verification join. `build_input.go` projects consumed inputs
from Go's package listing and hashes them into the existing exact manifest.
`shared_binary_inputs.go` owns the current post-build checks.
`shared_binary_cache.go` owns lookup, subscriber leases, bounded linking and
atomic shared publication. The shared producer borrows the caller's workspace;
cancellation must continue to join that producer before releasing the lock.

The real-process cache proof lives in
`internal/build/shared_binary_cache_integration_test.go`, selected only by the
existing verifier `dev-process` probe. Ordinary tests inject the Go command and
keep file/content and cache behavior in process. Current architecture lives in
`ARCHITECTURE.md`; verifier ownership is in `scripts/verify/AGENTS.md`.

## Milestones

1. Establish an explicit, conservative input-ownership eligibility rule and a
   failing deterministic regression for restored external inputs.
2. Enforce eligibility across hits, in-flight joins and publication. Unsupported
   builds run privately with existing live checks, without shared artifacts.
   Prove a supported owned-workspace build can still publish and hit.
3. Extend native proof, update living documentation, complete verification and
   close only this correctness plan.

## Plan of Work

Track cache eligibility during current Go input discovery, separately from
serialized application identity. Every consumed non-toolchain source and module
input must belong to the protected workspace; unknown and native/external-tool
inputs remain conservative misses. Do not trust module-cache permissions or a
framework snapshot's directory name as immutability. Examine resolver and build
flag alternatives before finalizing admission. Run-local observations and the
bounded file-digest optimization retain their existing best-effort roles; neither
establishes the supported reuse domain.

The regression must cause the injected compiler to read B, restore A in the
same file with the original observable non-change-time metadata, and finish
successfully. The shared artifact key for A must remain absent; another build
must compile A rather than restore B. Repeat with an already present matching
cache entry to prove lookup also respects the narrower domain. Test local
replacement and framework paths, and simulate unavailable change time without
skipping the regression. Native/process evidence belongs in the tagged probe.

## Concrete Steps

All commands run in `/Users/petrbrazdil/Repos/scenery` unless stated otherwise.
Fresh Worktree Preflight requires no UI provisioning for this change: the
dashboard is unchanged and quick/default/race prepare their own local product.

1. Implement source and deterministic test changes using the existing Go runner
   seam; run `go test ./internal/build`.
2. Extend tagged native proof and its bounded report; run
   `go test ./scripts/verify` and
   `go test -tags=scenery_build_cache_integration ./internal/build -run=^TestSharedBinaryCrossProcess -count=1`.
3. Refresh selection with
   `go run ./scripts/verify --quick --summary --write`, read
   `.scenery/harness/agent-context.json`, and run its exact cumulative commands.
4. Run the acceptance commands below, saving compact reports/raw failure evidence
   under ignored `.scenery/harness/0183-review/`. Do not commit generated caches.

## Validation and Acceptance

Expected changed-area classes are Go package and release-sensitive/runtime.
Run affected `go test ./internal/build` and `go test ./scripts/verify` first, then:

```sh
go test ./...
golangci-lint run ./...
go run ./scripts/verify --summary --write
go run ./scripts/verify --race --summary --write
go test -race ./internal/build
go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe worktree --probe parallel-runtime --probe core-separation --summary --write
```

The full verifier supersedes quick proof, but quick first refreshes selection.
Run `go test ./cmd/scenery` if the cumulative oracle selects it. The probe union
proves normal endpoint generation identity, native conformance, input invalidation,
candidate recovery and owned worktree/process cleanup with the exact prepared
`.scenery/harness/bin/scenery`. Its native cache segment must show B was compiled,
restored A does not authorize shared B, and a subsequent request compiles A.
The deterministic regression must pass without change-time support. Legitimate
owned-workspace hits, cancellation ownership, corrupt artifact repair, executable
permissions and crash cleanup retain coverage.

No compiler/generator files are planned; consumer regeneration is unselected
unless those paths change. No SQL/auth/storage/assistant/lifecycle protocol is
changed; their separate promotion probes are unselected unless that boundary
changes. No host integration, ONLV source change, new performance benchmark,
all-root timing audit or release certification is selected. Do not present
unselected commands as passed. New ordinary roots must retain the exact-root
100 ms p95 contract; use bounded in-process coverage and existing tagged native
proof rather than hiding external execution in ordinary tests.

## Idempotence and Recovery

Preserve the existing workspace lock, cancel/join behavior, leased staging,
atomic publication and independent retained executables. Ineligible builds must
not acquire shared-artifact/subscriber authority or restore old shared bytes.
Do not remove old-version caches or unrelated processes. If validation fails,
retain its output and fix only a confirmed scoped regression before rerunning.

## Artifacts and Notes

Starting HEAD and clean state are recorded above. Plan 0182, its evidence, and
the 0180/0181 measurements remain historical. The final source changes are local
on the same branch; no commit, push, merge or remote write was performed by this
repair. No original ONLV checkout, live service or data was changed.

All commands below ran from `/Users/petrbrazdil/Repos/scenery` and passed on the
final source. The oracle's cumulative classes were `cli-json-contract`,
`go-package` and `release-sensitive-or-runtime`.

| Command | Final result |
| --- | --- |
| `go test ./internal/build ./scripts/verify` | pass |
| `go test ./cmd/scenery` | pass |
| `go test ./...` | pass |
| `golangci-lint run ./...` | pass, 0 issues |
| `go test -race ./internal/build` | pass |
| `go test -tags=scenery_build_cache_integration ./internal/build -run=^TestSharedBinaryCrossProcess -count=1` | pass directly and through final dev-process probe |
| `go run ./scripts/verify --quick --summary --write` | pass with existing warnings; refreshed the exact oracle |
| `go run ./scripts/verify --summary --write` | pass with warnings |
| `go run ./scripts/verify --race --summary --write` | pass with existing warnings |
| `go run ./scripts/verify --probe generation --probe native-contract --probe build-info --probe dev-process --probe worktree --probe parallel-runtime --probe core-separation --summary --write` | all seven pass in one final run |
| `go run ./scripts/verify --probe dev-process --probe native-contract --summary --write` | focused corrected-probe run passed before the final union |
| `git diff --check` | pass |

Final quick/default/race/probe reports and the refreshed `agent-context.json`
are under `.scenery/harness/0183-review/` (`*-final.json`). The earlier failing
union is retained as `probes-initial.json`; the focused correction is
`probes-corrected.json`. Final runs retain the existing 41 knowledge and 21
architecture warnings. The default suite's 5.815-second cached-wall warning is
advisory; the final race lane's ordinary suite was under that wall budget.
Exact-root confirmation is separate, not inferred from suite duration.

The reference host is Mac14,14, 64 GiB RAM, Darwin 25.5.0, Go1.27.0 darwin/arm64.
The targeted confirmation runner is the ignored
`.scenery/harness/0183-confirm-tests.mjs`, using one disposable test binary per
package and 20 serial fresh processes per root; no shared caches were cleared
and no background developer workloads were stopped. Commands were:

```sh
node .scenery/harness/0183-confirm-tests.mjs
node .scenery/harness/0183-confirm-tests.mjs TestSharedDevelopmentBinaryReusesExactArtifactAndRepairsCorruption
node .scenery/harness/0183-confirm-tests.mjs TestHarnessPrivateExternalBuildEvidence scripts/verify
```

The initial timing summary SHA-256 is
`1b43a56637ca294e0df3b9b694c7c47149a2c408bad05f47c22acdafbc20bcf6`;
the corrected control summary is
`3e3a426bff2a9656ad0bbdefa568a7b4ce98b3d81d004190819e44aaf6e59733`;
the probe-evidence summary is
`9ed2afd0f2dcfd8286fcbce2bcefe0eb86862b7383f9a641cc99de8bb0711b09`.
Individual raw event samples remain alongside each summary, including the
original 110 ms failure. No samples were dropped or replaced within a series.

The final probe report SHA-256 is
`8347b8d310cc9f7a8adbea2a92f91aae5742d8de701bdb128fe61a495e65c278`.
Its absolute product path is
`/Users/petrbrazdil/Repos/scenery/.scenery/harness/bin/scenery`, SHA-256
`4dc3097679dd30e3810c0838cba510466b6aa3a67a8cadcb72a7e98db1e70163`.
The framework source digest is
`sha256:485826e9c11bf4464a9d7f4d467fbf4ba318ae551b02ea74742b88cd551e8741`.
The handoff fixture's selected snapshot CLI digest is separately
`sha256:1504f8988dfdf7fb42c02b751a53850345673129ca4798cd159709c12c8273b1`;
the report binds it to that same framework source and the served generation.
No framework source was edited during this final probe run.

Release certification, new ONLV/performance/worktree-cost benchmarks, an
all-root timing audit and native Windows proof were not performed. No
compiler/generator implementation, SQL/auth/storage/assistant protocol or shared
agent ownership changed, so their additional refresh/promotion lanes were not
selected. The selected worktree probe does include its real SQL isolation proof;
that is not standalone SQL/auth/streaming promotion of a new execution model.

## Interfaces and Dependencies

Keep `BuildInputManifest`'s serialized shape and digest exact. Cache eligibility
is private run-local ownership information, not a new public identity field or
a saved validation verdict. The existing Go runner, target environment, concrete
supervisor, full target checker and shared-artifact store retain their owners.
No new module, daemon, configuration knob or general cache framework is needed.
