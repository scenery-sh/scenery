# Direct Executable Retention Experiment

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current under PLANS.md.

Completed experiment, rejected for performance on 2026-09-11. The completed
record is now historical. No executable-owner implementation remains in product
source; the reproducible candidate and raw evidence are retained locally.

## Purpose / Big Picture

Evaluate ordinary Go builds whose output is created directly under a worktree
retention owner, removing the second Scenery executable copy. The baseline is
PR 194 at 741c0d7a (implementation cc484d2d), including final source admission.
The developer requested one concrete vertical, six ordinary AB/BA pairs and a
250 ms median paired edit saving before accepting ownership complexity.

The tested candidate failed that gate: all six edits were slower, with a
91.926 ms median paired regression. The independent unchanged backend startup
series improved, but cannot compensate for the failed edit requirement.
Neither historical 50 percent goal is closed.

## Progress

- [x] Confirm clean baseline 741c0d7a; create feat/direct-executable-retention.
- [x] Implement private output, publication, cache and runtime references.
- [x] Exercise ownership failures and real preflight, replacement and rollback.
- [x] Complete six fixed edit pairs and six separate unchanged startup pairs.
- [x] Verify bounded executable count and bytes across every measured edit.
- [x] Reject the candidate and remove its product changes, preserving the patch.
- [x] Complete the final documentation verifier and lint; selected checks pass.

## Surprises & Discoveries

Changing only the Go output flag is insufficient: workspace cache loading and
current-runtime identity inspection reconstruct the executable location from a
build fingerprint. Session release uses a path, whereas shared retention needs
independent cache, preparation, active and rollback references.

The earlier plan 0184 attribution's 491 ms copy was not reproduced in this
series. Here the entire baseline retain phase had a 91.520 ms median, versus
33.704 ms for candidate retention. That component saving did not survive the
whole lifecycle. Candidate compile/admission, reference persistence and release
add work; the experiment does not assign their aggregate difference to a single
filesystem operation. Medians of nested phases are not additive.

The first smoke run rejected an outdated test storage configuration at actual
preflight. Rebuilding the fixture configuration with the current code resolved
it. No failed smoke run was substituted into or removed from the timed pairs.

## Decision Log

- Developer: require at least 250 ms median paired edit saving, no startup or
  correctness regression; no worker, COW, hardlink, background GC or new session
  framework. Keep target/ABI, freshness, preflight, readiness and debugger flags.
- Agent: use the same producer executable for both arms. Control leaves the new
  output owner unset and follows ordinary workspace build plus session copy;
  candidate selects the retention owner. Both retain PR 194 final admission.
  This controls producer/input identity rather than comparing different CLI
  executables or reverting the baseline to main.
- Agent: reject this implementation after the fixed series. Do not optimize its
  reference persistence, change the toolchain, or expand recovery to rescue the
  result. Preserve the unshipped experiment, including its limitations.

## Outcomes & Retrospective

Primary interval starts before the actual semantic source write and ends at the
verified authenticated SQL response of the new generation. The old generation
continues during preparation, then actual preflight runs, replacement confirms
old-child shutdown, readiness runs, and old executable/preparation releases
finish before the response. Both arms have one long-lived preparation owner for
all edit pairs and perform identical authored changes with identical build input
entries, including the producer executable entry. No samples were excluded.

| Pair | Order | Ordinary ms | Direct ms | Saving ms |
| --- | --- | ---: | ---: | ---: |
| 1 | AB | 4139.532 | 4154.790 | -15.258 |
| 2 | BA | 3927.855 | 4018.128 | -90.273 |
| 3 | AB | 3911.215 | 4031.881 | -120.666 |
| 4 | BA | 3991.051 | 4084.630 | -93.579 |
| 5 | AB | 3868.976 | 3974.031 | -105.055 |
| 6 | BA | 3933.631 | 3966.792 | -33.161 |

Ordinary median: 3930.743 ms. Direct median: 4025.004 ms. Difference of medians:
94.261 ms regression (2.40%). Median paired regression: 91.926 ms. The latter is
the declared decision statistic, and is 341.926 ms short of a 250 ms saving.

Captured-edit to verified retained artifact medians were 2822.848 ms ordinary
and 2892.290 ms direct; median paired regression was 46.199 ms. This auxiliary
boundary is not the full edit interval.

Separate unchanged startup used BA, AB, BA, AB, BA, AB. Each sample cleanly stops
and reopens its preparation owner against the retained same-source cache, then
performs actual preflight, first runtime launch, readiness and response. All
12 graph-cache lookups hit. The inclusive owner-start through response receipt
and reporting medians were 2704.839 ms ordinary and 2177.493 ms direct, with a
544.775 ms median paired saving. This inclusive clock also contains bounded
post-response executable inventory and report handling. The raw records retain
response timestamps and the narrower preparation-start-to-response interval.

This is a backend startup comparison, not a measurement of complete scenery up
with dashboard, browser routes, managed database startup, frontend and assistant
startup. Those services were preprovisioned or outside the fixture. The result
supports no startup regression in this tested vertical; it does not establish
the original full-startup 50 percent goal.

Every measured direct edit settled at one executable, 51,989,954 bytes. Ordinary
settled at three executables, 155,969,862 bytes (two workspace binaries and one
session binary). Counts and bytes were identical across all six samples in each
arm. The candidate saves retained disk bytes, but that is not the declared
performance acceptance criterion.

## Context and Orientation

The candidate touched internal/build's explicit output path, cache state and
runtime identity, plus the supervisor's preparation, launch references and
release. The owner held a private same-filesystem attempt directory, hashed and
synced the original Go output, published its directory under the executable
SHA-256, independently verified bytes, and persisted independent references.
Its cache slot could be evicted without invalidating runtime handles. No second
Scenery copy, COW, hardlink, alternate runtime, changed Go flag or background
cleanup was introduced. Standard Go may still perform its own internal copies.

## Milestones

1. Narrow output owner and process handoff implemented and exercised.
2. Fixed end-to-end experiment and independent unchanged startup completed.
3. Performance rejection applied; production source restored to the baseline.

## Plan of Work

The experiment is complete. No further implementation is authorized by this
plan. A future proposal must acknowledge these results and identify genuinely
different work; reducing reference persistence would be a new experiment, not a
result already established here.

## Concrete Steps

From this worktree, the local evidence root is
.scenery/harness/direct-executable-retention/.

The archived candidate patch applies to 741c0d7a, verified with git apply --check.
The native driver is built with a Go overlay adding only the harness entrypoint
and renaming product main. build-driver.go binds the framework producer digest.
Both arms invoke production devBuildPreparation, prepareAppStart,
preflightAppStart, replaceAppGeneration, startPreparedApp and listener readiness.
The fixture is an isolated ONLV clone with owned PostgreSQL 18 and local storage.
The served project summary contains the semantic edit's unique pair label;
response PID, build input, implementation and contract headers must match.

Run the archived sources in an isolated checkout with the candidate applied;
measure.py declares the pair order before launching samples. Its --proof mode
uses proof-driver.go to exercise native failure ownership; --smoke only warms
and checks both arms. These harness flags add no product switch or API.

## Validation and Acceptance

Candidate validation performed before removal:

- go test ./internal/build ./cmd/scenery: passed, including eight output-owner
  cases and existing source-admission/handoff tests.
- go test ./...: passed, using the normal Go result cache.
- golangci-lint run ./...: failed with eight unchecked Close results in the
  experiment owner/tests. These were not repaired after performance rejection;
  the affected source was removed, not submitted as a shippable patch.
- Native smoke, six edit pairs and six unchanged startup pairs: passed semantic
  response, linked identity, preflight, readiness, clean owner shutdown and
  owned-container cleanup assertions.
- Separate native failure run: passed cache eviction while serving, rejection
  of a deliberately mismatched linked preflight identity, real failed spawn
  after old-child stop followed by exact retained-launch rollback and identical
  authenticated response. Injected unconfirmed shutdown prevented any start.

In-process ownership tests cover independent active/rollback references through
cache eviction, failed private-output cleanup, existing digest tampering without
overwrite, symlink rejection, ambiguous directory sync disabling reclamation,
bounded repeated releases, reopened cache byte tampering, and pending publication
refusing reuse or recovery. Existing handoff tests cover candidate shutdown
uncertainty and cancelled/failed recovery; source-admission tests keep edits
pending after rejection. Ordinary tests inject durability operations; the native
runs use actual file and directory synchronization.

This is not complete production-hardening proof. Interrupted publication fails
closed but does not implement a user-facing reconciliation workflow. Crash-left
references are preserved; recovery after unexpected supervisor death was not
integrated. Failure cleanup errors in some supervisor defers remain unchecked.
There was no separate actual verifier-failure injection for the new owner, no
full assistant lifecycle proof and no debugger attachment. The experiment kept
these runtime paths and flags unchanged, but does not claim exhaustive parity.

Full/default self-harness and dev-process/native-contract/build-info catalog
probes were not run on the rejected candidate. Shipping correctness acceptance
was intentionally stopped at the failed investment gate. Compiler/fixture
regeneration, UI lanes, full release and all-root timing were unselected. Final
source is documentation-only. go run ./scripts/verify --quick --summary --write
passed with 41 knowledge and 22 architecture warnings, zero errors;
golangci-lint run ./... passed with zero issues. The changed-area oracle selected
only documentation-only validation. No product validation exception is shipped.

## Lifecycle Accounting

| Work | Ordinary | Direct | Boundary |
| --- | --- | --- | --- |
| Standard Go build output | Disposable workspace binary | Fresh private owner output | Compile/admission |
| Full checks, input/target capture and admission | Unchanged | Unchanged | Before preflight |
| Artifact hash, copy, fsync and independent verification | Hash, CopyRoot, file/dir sync, verify | Hash, file/dir sync, durable intent, directory rename, verify | Before preflight |
| Cache/state/manifest writes and workspace pruning | Existing writes/pruning | Existing writes plus owner cache record | Compile/admission |
| Preparation/candidate references | Session path ownership | Durable independent handles | Before preflight and before response |
| First exec | Actual runtime preflight | Actual runtime preflight | Timed |
| Previous child stop and executable release | Session removal; two workspace cache binaries retained | Last unreferenced digest removed and directory synced | Handoff before response |
| Generation readiness and authenticated response | Actual child/listener | Actual child/listener | Timed |
| Inventory/report serialization | Read-only inventory | Read-only inventory | Outside edit response timestamp; included in inclusive startup clock |

No output write, sync, publication, old-generation deletion or reference release
was deferred to background work. End-of-series clean shutdown is separately
outside the edit samples for both arms. Per-step trace durations are nested,
so their medians must not be summed into an apparent additive saving.

## Idempotence and Recovery

Both measured owners exited successfully and all owned containers were removed.
Only fixture source bytes last written by the runner were restored. Product
changes were archived, hash-checked and restored from HEAD individually; no
unrelated work or global Go cache was removed. Evidence executables are retained
as local experiment artifacts, not counted as one app's live retention store.

## Artifacts and Notes

Paths relative to the local evidence root:

- pairs-4ccaf01619/measurement-plan.json and report.json: complete predeclared
  edit/startup series, all input manifests, phase traces, responses and counts.
- pairs-bc0c923178/: rejected old-storage-configuration smoke; container removed.
- pairs-a2262abb8a/: successful two-arm smoke.
- pairs-8603599e17/: separate successful native ownership/rollback proof.
- candidate.patch, candidate-source/ and candidate-source-hashes.json: exact
  rejected implementation plus focused tests, with applicability proof.
- measured-driver.go and driver: reproduced exact timed producer executable
  SHA-256 0de26c71244c7f65ff1c31304c16a15b5928b65b63f50249f2a1aaf3ebc1fd85.
- proof-driver.go and proof-driver: separate native failure instrumentation.
- producer-source.json: framework source digest
  sha256:863ac21dc6efb0f8422a686a0c44bafb16d1cc7807ebfafef5e3847797f9a61d.
- candidate.patch SHA-256:
  9c45581dc7313735add42e61ce85f7a1e32f7dc236b8996b70e4abaffea59ac7.

The patch includes two extra post-measurement tests; tests do not change the
framework source digest. The measured driver was rebuilt and its exact recorded
binary hash matched before product restoration. All evidence remains local and
ignored; no PR was created, updated or merged for this experiment.

## Interfaces and Dependencies

Final product interfaces, ownership, schemas, runtime, flags and dependencies
are unchanged from 741c0d7a. Only the completed experiment and living knowledge
index change. Public contract and agent workflow documentation were deliberately
left unchanged because the candidate was rejected.
