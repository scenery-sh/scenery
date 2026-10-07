# Telemetry Reliability Closure

## Purpose / Big Picture

Finish the implementation and live acceptance work identified by the October 6
review of `a9a23313`. Plan 0214 is completed history. This follow-up owns the
remaining deterministic build recovery, diagnostic classification, verification
isolation, complete retained telemetry, missing-source lifecycle and rollout
evidence. Scenery owns framework behavior; ONLV owns recorder behavior.

## Progress

- [x] 2026-10-07 Read the supplied review, current instructions and previous plans.
- [x] 2026-10-07 Capture personal-home worktree inventory before mutations.
- [x] Implement typed request/precondition/build diagnostics and coherent rebuild recovery.
- [x] Isolate every verification/release lane and record explicit execution purpose.
- [x] Include archived CLI evidence with coverage, deduplication and segmented summaries.
- [x] Expose independent source/owner/metadata/freshness state and enforce source-loss policy.
- [x] Recover the surviving blocked ONLV worktree and verify recorder/framework rollout.
- [x] Verify installed agent containment and controlled runtime/request timing.
- [x] Complete cumulative repository checks, named probes and evidence review.

### Resume here

2026-10-07: completed. All implementation and operational acceptance checks
passed, followed by stable-input full verification and the complete changed-area
command union. Full source validation is archived in
`.scenery/harness/runs/20261006T233425.259186000Z/`; worktree stale-client/data
acceptance in `20261006T232723.760145000Z/`; final TypeScript conformance and both
TypeScript checks in `20261006T233922.227180000Z/`. Generator checks were unchanged
on final house/native regeneration, and lint reported zero issues. Personal
telemetry contains zero verification/release lane records. Local supporting
rollout, request, containment, timing and retention evidence remains under
`.scenery/harness/telemetry-closure/`. Changes remain local and uncommitted;
preserve the unrelated `.scratch/telemetry-analysis-20261006/` and ONLV work.

## Surprises & Discoveries

- The exported runtime conditions are historical; current worktree source and
  installed job state differ. Do not recreate removed checkouts or equate missing
  metadata with permission to adopt processes or databases.
- Direct damage to a generated client correctly triggers its ownership guard;
  the stale-client acceptance fixture instead changes a public `.scn` input
  while preserving the owned checked output.
- The verifier's generic product runner inherited the personal environment.
  Private homes in individual probes do not isolate generic command batches.

## Decision Log

- 2026-10-07: Preserve unrelated source, index state and retained data. Source
  disappearance stops only verified runtime owners/children; database and
  storage retention remain separate. Invalid metadata is observation-only.
- 2026-10-07: Use one internal execution-purpose environment marker for descendant
  attribution. A command flag cannot reach arbitrary subprocesses and test
  binaries, and app configuration must not control verifier identity. This
  marker changes telemetry attribution only; it is not a runtime behavior knob.
- 2026-10-07: Keep passive first-header semantics. Controlled requests must prove
  expected successful behavior and exact serving identity independently.
- 2026-10-07: Do not install the global CLI, reset retained resources or run a
  repository-wide benchmark/timing audit. This request authorizes the review's
  focused operational recovery and performance investigation.

## Outcomes & Retrospective

Implementation, operational acceptance and cumulative source validation are
complete. The full archive has stable input identity, zero errors, 13 existing
knowledge-review warnings and one advisory cached-suite timing warning
(5.502 s against 5 s). Lint, final regeneration and all TypeScript checks pass.
The closure evidence includes:

- Typed migration/request/build failures, transaction deferral with one coherent
  publication, persistent stale-client blocking and source-loss shutdown. The
  private process fixture proves exact generation/build identity, successful
  behavior and preserved retained data. Worktree A6 proves zero repeat builds
  for an unrelated Go edit and recovery through public client regeneration.
- Retained CLI coverage now includes 95,225 archived in-window rows. The live
  JSON snapshot contained 104,209 rows; the subsequent human invocation saw
  104,210 because the first command appended its own completion. Per-file
  cutoffs make that difference visible. Legacy unknown purpose stays unknown.
- Recorder diagnostics correlate one auth episode, refresh attempts, queue age
  and ACKs without event payloads. On main, nextnext and image-lidar, a private
  synthetic recorder encountered two actual HTTP 401s, one refresh attempt,
  eight paused keepalive calls with no dispatch, then ACKed the prior queued
  chunk and resumed. Unit coverage includes expiry/account fencing and logout.
  Separate HTTP cookie-jar sessions proved that nextnext logout leaves main and
  image-lidar authenticated, without changing the browser cookie store.
- Nextnext returned HTTP 200 with seven projects and the selected project
  present under generation 1 / build `79f93432…`; source freshness was current.
  Main already served the existing cookie fix and remains under its separately
  active owner; its older status producer reports freshness as unknown.
- Two controlled 43-service startups with warm local caches and two concurrent
  rollout roots took 33.944/33.888 s. Initial-build spans were 31.929/32.400 s,
  Go build spans 16.951/16.964 s, activation 5.415/4.617 s. These overlapping
  spans are not summed. An isolated 20-edit successful-response fixture had
  p50 931.536 ms / p95 982.231 ms; this is not a clean-tech production SLO.
- Public-coordinate Earth requests took 1,137/10 ms with identical stored DEM
  and RGB paths, supporting the inspected ground-capture cache path. NSRDB
  weather lookup took 12,066/12,010 ms and returned pending at the inspected
  12-second wait bound; a completed weather profile was not observed. The
  configured frontend-dependencies profile passed every consumer and build;
  retained per-command timings show its intentional full-profile fanout.
- The installed launchd job uses `--supervised` and retained its original PID
  and run count. Owned launchd jobs blocked after one persistent/eight transient
  attempts even when incident persistence failed. No restart storm or leftover
  owned job remained.
- Prune was preview-only. Logical regular-file sizes are retained per resource;
  overlapping inventory rows are not a physical-disk or total-reclaim estimate.
  Incompatible/unknown ownership remains non-reclaimable. No retained database
  or storage was deleted.

Full release certification, all-root fresh timing/benchmarks, global CLI
installation, remote publication and unrelated freshness gardening are outside
this closure. `VNEXT.md` and completed plan 0214 remain unchanged.

## Plan of Work

1. Type pending migration and missing-app requests; preserve wrappers. Extend
   deterministic blocks to stale TypeScript clients with relevant-input keys.
   Defer scans/builds during workspace transactions and rescan after release.
2. Establish a run-owned agent home before any verifier descendant. Keep narrower
   fixture homes. Give the release shell the same boundary. Native command
   telemetry uses resolved producer version and explicit dirty/purpose fields.
3. Stream active and archived CLI history, deduplicate identified invocations,
   expose retained source cutoffs and unknown legacy purpose. Segment by app,
   purpose and producer. Keep initial/rebuild cause totals and rejected work apart.
4. Report root/source existence, verified owner state, metadata compatibility and
   source freshness separately. Stop source-less verified owners without touching
   retained data. Prune preview reports exact resources/bytes and refuses unsafe
   records. Capture current storage inventory before any cleanup proposal.
5. Inspect the surviving ONLV worktrees and prescribed migration status, recover
   down/migrate/up where required, and exercise recorder recovery and cookie
   isolation. Verify actual served revisions, not only filesystem revisions.
6. Inspect installed launchd supervision, prove storage-failure containment in
   an owned probe, and attribute controlled startup/activation/weather/map and
   frontend-validation paths. Record unavailable external acceptance explicitly.

## Validation and Acceptance

All commands below run from the Scenery root. Source/runtime changes select full
`go run ./scripts/verify --summary --write`, then read the immutable archive's
`agent-context-summary.json` and complete `agent-context.json`'s changed-area
command union. Run `golangci-lint run ./...`. Focused package tests cover each
changed owner and are reused by the full lane for identical inputs.

Compiler/generator changes require both public_api fixture regenerations named
in `docs/harness-engineering.md`. Changed runtime boundaries select
`go run ./scripts/verify --probe cli-process --probe dev-process --probe process-model --probe agent-restart --summary --write`;
auth and database changes additionally select `--probe auth` and `--probe worktree`.
Inspect each selected assertion inventory and owned cleanup. No release
certification is claimed without `scripts/release-gate.sh`; isolation of that
entrypoint is checked separately before considering its expensive full catalog.

ONLV acceptance uses its current root/child instructions and declared checks.
Observe bounded failed authentication, explicit reauthentication, acknowledged
queued chunks and resumed recording, plus independent sibling-runtime cookies.
Read-only application requests must return the expected successful response with
the current generation/build identity. Missing roots are recorded as removed,
not as failed rollout targets. Unavailable credentials or unsafe retained-state
incompatibility remain exact external blockers, not passing evidence.

## Idempotence and Recovery

All fixture lanes own private homes and processes and stop them on completion.
Never broadly stash, reset or replace dirty worktree files. Review exact pending
migration bytes before applying against a stopped verified owner. A failed build
keeps the last good generation; deterministic blocks clear after relevant valid
inputs change. Live transaction deferral never bypasses compiler read protection.
Prune is preview-only until exact deletion is authorized. Installation, remote
publication and service-manager replacement are separately reported actions.
