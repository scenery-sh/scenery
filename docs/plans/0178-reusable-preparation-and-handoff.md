# Reusable Preparation and Shorter Runtime Handoff

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current. It follows [PLANS.md](../../PLANS.md).

## Purpose / Big Picture

Make ordinary ONLV implementation edits reuse unchanged generated projections
while validating and compiling the actual edited application. Make unchanged
startup cheaper without trusting timestamps, and move candidate-private
assistant preparation outside the API-unavailable interval. Finally reduce
watch settling and redundant database setup work without weakening readiness,
ownership, migration truth, isolation or rollback.

The developer authorized implementation and testing of the completed latency
reply in ChatGPT conversation `6aa2a026-7a68-83eb-b970-17c47068ee79`, message
`f98a7c8b-4b10-4084-aa34-3842d4d438c3`. It requests three separately attributable
deliverables, detailed below. The reply was already complete when inspected;
no recurring monitor is necessary.

## Progress

- [x] (2026-09-11) Read the completed latency proposal and confirm clean Scenery
  11f3b5b8 and ONLV ebc67dcd source roots.
- [x] (2026-09-11) A: capture three semantic-edit and three unchanged-start
  samples; identify the pure public projection boundary and live validations.
- [x] (2026-09-11) A: implement bounded pure-render reuse, live snapshot validation
  without no-op publication, and an operation-local workspace byte inventory.
- [x] (2026-09-11) A: verify source/target/catalog invalidation, mutation isolation,
  current module/deleted-output rejection, no-op transaction absence, real served
  behavior/identity, and repeated edit/start measurements.
- [ ] B: stage assistant files privately before stopping the working application.
- [ ] B: measure materialization, evaluate independent copy-on-write files, and
  prove failed staging, isolation and rollback.
- [ ] C: validate shorter watcher settling without redundant builds.
- [ ] C: optimize authoritative database status/setup reuse and verify reset/restore.
- [ ] Complete repository and ONLV acceptance, record individual samples and
  separate commits for A, B and C, then publish the coherent dependency pair.

## Surprises & Discoveries

The first isolated renderer/inventory implementation produced 8.032, 7.945 and
8.000 second semantic-edit samples. Public Go and TypeScript preparation still
spent about 0.69 and 0.64 seconds respectively because no-op publication rebuilt
the entire declaration graph. Local snapshot revalidation now reads exact
source membership/bytes and recomputes the full existing workspace revision;
registry-backed snapshots retain ordinary compiler validation. This does not
cache implementation analysis or weaken the full runtime snapshot fingerprint.

The graph cache key currently includes the whole runtime snapshot. A Go edit
therefore misses it. The cached-workspace path still recompiles declarations,
publishes/checks projections, synchronizes sources and separately rereads files
for dependency and build fingerprints. The existing measurement driver is
`.scenery/harness/measure-loop.ts` in the owned ONLV 4070 fixture; it records
served identity and phase events but not focused-acceptance completion or API
unavailability.

## Decision Log

- 2026-09-11, Codex: retain the full implementation/build fingerprint and current
  Go analysis. Cache only pure rendered artifacts with their complete input
  identity; do not mix `go/types` objects from different analyses.
- 2026-09-11, Codex: preserve the personal ONLV root and port 4920. Use the
  marker-owned fixture at `/Users/petrbrazdil/Repos/onlv-coherent-task-environments`
  (4070) for explicit source edits, restarts and measurements, restoring all
  temporary inputs and retaining database/object state.
- 2026-09-11, Codex: no new public command, environment knob, remote cache,
  orchestration layer or additional framework. Extend existing trace/probe and
  app-acceptance mechanisms. Timing targets are measured aspirations, not
  permission to skip work or claim percentiles from three samples.
- 2026-09-11, Codex: use a 32 MiB/64-entry process-local renderer cache rather
  than a new persistent artifact protocol. Copy bytes at both ownership
  boundaries. Source, target, producer and live UI catalog changes invalidate
  it; module/path checks and generated-file inspection execute on every hit.

## Outcomes & Retrospective

A is implemented and validated; B and C remain open. The owned fixture's handler
was restored after measurement. Its temporary local Scenery replacement remains
intentional until the final published-pair acceptance.

Final A semantic-edit samples 1, 2 and 4 were 7.373, 6.687 and 6.840 seconds
(median 6.840, range 6.687–7.373); focused acceptance finished at 10.405, 9.763
and 9.891 seconds. Sample 3 (7.374 seconds) is retained in raw evidence but
excluded from this comparison because fixture-generation validation overlapped
the measured build. Baseline edit median was 8.269 seconds.

Final A unchanged starts were 8.324, 8.260 and 8.236 seconds (median 8.260,
range 8.236–8.324), versus baseline median 9.429. Focused acceptance completed
at 11.565, 11.480 and 11.510 seconds. All three graph/workspace cache checks hit;
workspace verification took 1.425, 1.393 and 1.430 seconds versus baseline
2.557, 2.512 and 2.511. No Go build command ran on these starts. Public Go and
TypeScript preparation on edits fell to 151–181 ms and 69–82 ms; current Go
implementation analysis still executes. The sub-5-second aspiration is not met.

Edit API polling observed 9–10 failed samples in the three uncontended runs,
spanning approximately 217–245 ms between first/last failed observations;
this sampling is not an exact outage boundary. The measurement driver records
every poll and phase in the owned fixture's
`.scenery/harness/loop-attribution.json`, labels `latency-0178-A-final-*`.
The measured source is `sha256:17bb98f72d4787c151bc465363c25558061ce33732c5f18887d502e26a62403e`
and executable `sha256:2eaedc7fea57bf5ec704c1d6ed57bc6b8880f3affba4d8591e0054643b00463a`.

A checks passed: affected compiler/parse/generator/build/CLI tests, all Go tests
through the full verifier, all three fixture generations (no output changes),
27 TypeScript conformance tests, both generated-client/catalog typechecks,
`golangci-lint run ./...`, and explicit `dev-process`, `parallel-runtime`,
`build-info` probes. Existing documentation-review and large-file warnings remain;
the changed Go/React renderer files became smaller. The generator instruction
addition initially exceeded its 800-word limit and was shortened before delivery.
The timing driver is promoted to ONLV `development/measure-latency.ts`; its task
recipe tests, typecheck and repository harness passed. No release/all-root timing
audit was requested or run.

## Context and Orientation

`cmd/scenery/dev_build_pipeline.go` orchestrates preparation and compilation.
`internal/build/prepare.go` and `workspace_cache.go` own source synchronization,
build identities and reusable workspaces. `internal/generate` owns projections
and current native implementation checks; build reaches it through hooks.
`cmd/scenery/dev_app_start.go`, `dev_assistant_prepare.go` and
`dev_assistant_cache.go` own assistant materialization and activation. Watcher
and database setup paths remain in the supervisor; named native probes in
`scripts/verify` own external proof. ONLV's `development` helpers verify real
API/Chrome behavior against the intended linked build.

## Milestones

A: pure projection reuse and cheaper unchanged workspace verification. Target
generation/preparation toward 2 seconds and workspace-hit toward 1 second.
B: candidate-private assistant staging before shutdown and measured independent
file materialization. C: test 100 ms settle without faster full-tree polling,
and reduce repeated database endpoint/connection/ledger work while querying
actual database truth. Preserve separate A/B/C commits and attribution.

## Plan of Work

First extend the existing measurement driver to record edit-to-served-revision,
edit-to-focused-acceptance and API-unavailable duration. Capture three baseline
semantic edits and unchanged starts with actual behavior, identities, cache
decisions, read/hash/publication counts and phase spans. Keep spans separate
when nested. Implement A and compare the same workload. Then implement and
validate B and C independently. If an optional optimization has no measured
benefit or violates an invariant, record the evidence and retain the safe path.

## Concrete Steps

From Scenery, use `.scenery/harness/bin/scenery inspect docs --for-path <path>
-o json` for changed surfaces. Run affected `go test ./internal/build`,
`go test ./internal/generate`, `go test ./cmd/scenery`, and
`go test ./scripts/verify`, then `go test ./...` and `golangci-lint run ./...`.
Run `go run ./scripts/verify --summary --write` and the exact command union in
its changed-area report. Runtime/generator/probe and documentation classes apply.

For generator changes run both committed fixture commands:
`go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`
and the same command with `--app-root internal/compiler/testdata/house`.
Also run the generator child checks: assistant fixture generation,
`bun test internal/generate/testdata/typescript_client_conformance.test.ts`,
and console-installed `tsc` with `tsconfig.generated-clients.json` and
`tsconfig.catalog.json` under `internal/generate/testdata`.

Changed external boundaries require
`go run ./scripts/verify --probe dev-process --probe parallel-runtime --probe build-info --summary --write`
for A; add `--probe assistant-runtime` for B and `--probe postgres --probe worktree`
for C. Preserve each probe's cleanup and assertion inventory.

From ONLV run `bun test development`,
`apps/nextnext/node_modules/.bin/tsc -p development/tsconfig.json --noEmit`,
`just check-harness`, and `just repo-harness`. In the owned fixture run
`./scripts/scenery check -o json`, `go test ./...`, `./scripts/scenery harness -o json --write`,
`just feature projects`, `just feature ahjs`, and `just smoke` after the final
producer selection. Repeat the updated loop measurement commands for three
samples per workflow and milestone; write exact invocations/results below.
Before final delivery run the published-checkout and retained-runtime proofs
against the resulting published pin. Full release and all-root timing audits
are not requested; these local workflow measurements are explicitly authorized.

## Validation and Acceptance

A must serve an actual changed handler value with a new exact build identity,
not rewrite unchanged generated files, and reject/invalidate changed handler
signatures, shared types, build tags, module dependencies and declarations.
Deleted/tampered generated files must repair or reject; concurrent edits or
branch changes must not bless stale candidates. Unchanged startup must perform
no Go build and no unnecessary artifact publication.

B staging must not change active descriptors/helpers or stop the API. Failed
staging leaves the existing generation usable; private writes cannot change
cache bytes or sibling worktrees. Activation preserves readiness and rollback.
No writable hardlinks or simultaneous write-capable application generations.

C covers atomic saves, multi-file patches, rapid edits during compilation and
generated-file publication, counting builds per completed edit. Database
reset/restore/recreation and migration-state changes remain authoritative even
when source bytes are unchanged. Never persist a source-only setup-success gate.

Report individual samples, medians and ranges, not small-sample p95. Keep the
under-five-second semantic-edit/start goals as targets until measurements prove
them. Correctness completion does not imply an unachieved performance target.

## Idempotence and Recovery

Keep unrelated changes/data. Restore temporary edits byte-for-byte only after
checking ownership of those edits; refuse concurrent input drift. Control each
runtime through its retained producer. Failed preparation cannot replace a
working generation. Stop newly created measurement environments without data
cleanup. Never install a worktree build into the shared Go bin during validation.

## Artifacts and Notes

Evidence belongs in ignored `.scenery/harness/` under each exact tested root.
Record baseline and final source SHAs, producer digests, command results,
individual samples and measured cache work here as each milestone completes.

Baseline uses the retained `de2d81028baf` producer (source digest
`97c68a43754a56026b2c6538d2b417e7d5f33ae8076e6f0778438aa241ccee4d`)
in the owned 4070 fixture. Its ignored `measure-loop.ts` now records 25 ms
availability samples with 500 ms request deadlines, current-process log events,
and the completion of `scenery validate development` after identity verification.
The implementation edit assigns a unique summary in the real ListProjects
response without changing stored data. The fixture source was restored and
`git status --short` is empty after all measurements.

| Workflow | Individual milliseconds | Median | Range |
| --- | --- | --- | --- |
| Edit to served revision | 8765.68, 8268.61, 8060.20 | 8268.61 | 8060.20–8765.68 |
| Edit to identity and focused validation | 11998.02, 11344.27, 11117.72 | 11344.27 | 11117.72–11998.02 |
| Unchanged start to ready | 11161.34, 9428.93, 9329.79 | 9428.93 | 9329.79–11161.34 |

All three starts report graph/workspace hits and no Go command. Workspace
verification takes 2556.87, 2512.30 and 2510.66 ms. Assistant private dependency
copies together take 3193.65, 2215.53 and 2129.02 ms; database setup takes
879, 804 and 808 ms. The third edit reports public Go projection 775.57 ms,
implementation check 1622.88 ms, TypeScript projection 673.06 ms and actual
Go build 1318.10 ms. Its assistant identity already hits; the listener handoff
accounts for roughly 250 ms of sampled API unavailability.

Evidence: `loop-attribution.json` labels `latency-0178-baseline-{1,2,3}` and
`latency-0178-baseline-start-{1,2,3}`. First two edit samples have valid HTTP and
identity evidence but no phase events because the old script retained a stale
log path; the third resolves the current owner stdout with `lsof`. A separate
`restore` observation overlapped the first explicit shutdown and failed its
single-live-session assertion; exclude it from timing claims. The subsequent
three startups independently verify the restored implementation identity.

## Interfaces and Dependencies

Keep singular public artifacts and CLI schemas. Internal trace additions can
carry bounded work counters; any public shape change requires its checked
schema, specification, documentation and consumer changes in the same commit.
No dependency addition or current specification relaxation is planned.
