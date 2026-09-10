# Fast Verified Development Loop

This ExecPlan is a living document. Keep Progress, Surprises & Discoveries,
Decision Log, and Outcomes & Retrospective current throughout implementation.

## Purpose / Big Picture

Implement the September 10 follow-up to producer hardening: make ordinary ONLV
preparation reliable, substantially reduce warm restart/edit latency, and move
generic build/producer/served-response verification from ONLV into Scenery.
Keep the current architecture and exact ownership/revision checks. Fast feature
feedback and full persistence/restart acceptance must share assertions rather
than fork into separate smoke implementations. Add real-browser feature proof
bound to the intended served generation. Replace JSON commands hidden in an
environment variable with one typed command/argument representation.

## Progress

- [x] (2026-09-10) Read the complete follow-up feedback; inspected baseline
  Scenery 884621bf and ONLV 9aa15b74. Both task checkouts were clean.
- [x] (2026-09-10) Fresh-worktree phase measurements reproduced the preparation
  process-publication race and attributed the main startup spans.
- [x] (2026-09-10) Fix framework readiness publication and the ordinary preparation boundary;
  deterministically test delayed publication and permanent-error handling.
- [x] (2026-09-10) Record instrumented baseline: semantic edit 11.676 s,
  restoration 11.072 s, unchanged warm restart 20.354 s, with exact linked
  response/candidate/source/PID identities in the owned fixture.
- [x] (2026-09-10) Instrument build/check/projection/Go commands, candidate
  preflight, process replacement, assistant preparation and identity publication
  in the existing structured event stream, including cache reasons and nested
  start/duration intervals.
- [x] (2026-09-10) Reduce the measured dominant work and record three verified
  samples for each path: unchanged restart median 10.066 s (9.953–10.074),
  semantic edit median 8.519 s (8.488–8.964). The five-second aspiration remains
  unmet; no release threshold or percentile claim is inferred from three runs.
- [x] (2026-09-10) Move generic identity verification behind the supported Scenery surface;
  remove ONLV's Go hashing/schema-digest/locator protocol duplication.
- [x] (2026-09-10) Replace ONLV_VALIDATION_COMMANDS with typed command vectors;
  preserve the command order of all 74 original profiles and expose each direct
  argv step in inspection, dry-run, and bounded execution evidence.
- [x] (2026-09-10) Compose fast affected-feature API/browser checks and full
  restart smoke from the same revision-bound assertions; both pass live.
- [x] (2026-09-10) Fast projects/API and actual Chrome AHJ search/clear/reload
  acceptance pass. Identical-content/mtime and test-only add/remove preserve
  linked build and both owned process IDs across repeated samples.
- [x] (2026-09-10) Complete package, repository, external-boundary and ONLV
  acceptance, including the final different-spec normal-launcher transition.

## Surprises & Discoveries

Fresh published-root measurement reached detached ready in 74.798 s including
creation, then failed because runtimeOrigin lacked the just-started API process
identity. A later real API proof passed. Framework preparation cost 19.676 s,
startup 37.642 s. Startup spans: runtime workspace preparation 13.275 s, Go
compile 7.705 s, application/assistant start 12.754 s. Frontend 4.201 s and
Victoria 3.784 s overlapped the build. These are n=1 warm-cache observations,
not reliable percentiles. The prior 84.25 s had different source-override and
patch-transfer work and is not an identical workload.

The instrumented semantic edit spent 6.686 s in generation/checking, including
two public Go publication transactions (0.805 s and 0.814 s), implementation
checking (1.684 s), and a second Go analysis (0.785 s). Actual Go compilation
and linking took 1.321 s. Framework verification took 0.111 s. Change settling
adds roughly 0.5 s; candidate preparation/preflight and listener publication
consume the remainder. Phase totals contain nested spans and must not be summed
with their children. The unchanged restart missed the graph cache because the
initial scan preceded assistant input registration; later snapshots included
helper inputs. Assistant private overlays also reran dependency installation
and builds for roughly 7 s on each supervisor restart. These are measured
optimization candidates, not grounds to weaken identity checks.

The first bounded optimizations remove the duplicate public Go publication and
register assistant inputs before the initial snapshot. The latter retains
helper input invalidation and is covered by an in-process deterministic scan
test. Repeated live measurements below prove the resulting cache behavior.

The combined implementation additionally derives topology metadata from the
prepared contract, reuses one Go projection and exact target analysis within a
single preparation, and retains checksum-verified assistant dependency/build
snapshots while copying each into private writable runtime directories. An
exploratory warm restart proved assistant cache hits: preparation fell from
about 7 s to 2.741 s. Total restart was still 17.026 s because a graph hit was
followed by a workspace rejection and full preparation. Added workspace-level
miss reasons to diagnose that remaining work. No five-second claim yet.

The new read-only generation inspection initially failed live: source-state
comparison mixed watcher runtime scope with standalone build scope, and the
workspace fingerprint predated Go tidy's dependency pruning. The build now
recomputes its binary key from final consumed workspace bytes after tidy and
rebinds runtime metadata after a tidy retry; private build-state version 6
invalidates old metadata. Verified cache reuse retains those exact bytes rather
than reseeding pruned framework checksums. Inspection compares the raw workspace
key and canonical input manifest without virtual normalization or cache mutation.

With frozen source b79adaf39e2da5f1f49358d713dd74c9f644434c7346005fdb6f8ba4ce113e13,
three restarts took 10.074, 9.953 and 10.066 s; three real handler edits took
8.964, 8.488 and 8.519 s. Every transition proved the expected served value,
current candidate/source identity and process ID. Restarts recorded graph,
workspace and assistant cache hits without any Go build. The temporary handler
change was restored byte-for-byte and its original linked identity returned.
Compared with this plan's 20.354 s restart and 11.676 s semantic-edit baseline,
the median improvements are 50.5% and 27.0%. These are local observations, not p95s.
The warm restart still spends about 2.6 s verifying the reusable workspace,
2.5 s preparing independent assistant overlays and 0.95 s in database setup;
semantic-edit generation is about 4.0 s and compilation/bundle preparation 2.6 s.
Nested and overlapping spans are not summed as independent work.

The external dev-process probe caught a source-error classification regression
after the metadata flow changed: SCN1000 returned exit 3 instead of 2. The initial
scan now rejects an invalid compiler result with the existing source diagnostic
and exit classification. A deterministic unit test and the unchanged real-process
probe pass; no test expectation or public error contract was weakened.

## Decision Log

- 2026-09-10, Codex: keep the existing Scenery checkout and existing isolated
  ONLV task checkout. Do not modify personal ONLV on port 4920 or its dirty
  Settings work. Do not spawn agents. No new build system or parallel protocol.
- 2026-09-10, Codex: attribute before optimizing; retain exact source/build/PID
  evidence, no fixed sleeps, blanket retry, or weakened cache validation.
- 2026-09-10, Codex: use a local explicit source selection in the owned ONLV
  fixture for testing unpublished Scenery changes. Publication requires new
  explicit authorization; do not commit a machine-local module replacement.
- 2026-09-10, Petr: explicitly permitted multiple agents during continuation.
  Delegate typed validation, verification helpers, feature acceptance and
  independent preparation optimizations; root retains live runtime control and
  final integration validation.
- 2026-09-10, Codex: retain minimal launcher confinement/executable integrity
  checks before executing a selected producer. Removing that trust boundary
  without an equivalent trusted bootstrap would weaken verification. ONLV no
  longer owns bundle/schema/Go serialization or response/restart comparison.
- 2026-09-10, Codex: the feedback labels five seconds as an initial engineering
  aspiration, not a feasibility guarantee. Keep that unmet target explicit;
  deliver the measured reduction and attribution without weaker verification,
  a new build system, or a speculative redesign to manufacture a five-second claim.

## Outcomes & Retrospective

Completed locally on 2026-09-10; not committed, published, or release-certified.
Ordinary detached readiness now includes verified process publication, runtime
events attribute the full loop, and measured repeated work has been removed.
Unchanged restart is about half the instrumented baseline; semantic edits are
27% faster. Five seconds remains an unmet engineering aspiration, with remaining
costs recorded above. No source, process, tenant, or build-identity checks were
relaxed to reach the result.

Scenery owns build-input/bundle verification and the supported TypeScript
response/session/restart helper. ONLV retains business assertions and minimal
bootstrap integrity rather than Go serialization/schema digests. Typed direct
command vectors replace environment-encoded commands. Fast feature acceptance
and full restart smoke share the same business proof.

Final native process probes pass after preserving source diagnostic exit 2.
Final ONLV acceptance uses source digest
d7505232e71ba38dd5fd7049c6daec91abc91d68e1dd6e152af088e507d5c609;
the only runtime-path change after the repeated timing snapshot is rejection of
invalid initial compiler results with their existing exit classification.
The full smoke retained build f197d433d2162c419cf9afe23ed77718c9488e5a9d9efe972d63106eb438526f
across API PID 19958 to 21004 and owner PID 18998 to 20836. The producer transition
then proved spec a3a617ba… to 70362606… while A remained controllable, restored A,
and final fast API/Chrome checks passed on PID 22228 / owner 22061. The restored
producer was built from the same source snapshot with its own executable receipt;
final linked build was 33677aa68e6d39570aa280eb0955669e9e2388e53b94a9e09738fa773835455f.

Validation commands and results (all PASS unless qualified):

- `go test ./cmd/scenery`, `go test ./internal/build`, `go test ./internal/app`,
  `go test ./internal/validation`, `go test ./internal/generate`,
  `go test ./internal/generate/api` (no test files), `go test ./internal/machine`,
  `go test ./scripts/verify`, then `go test ./...`.
- `golangci-lint run ./...`: zero issues.
- `go run ./cmd/scenery generate --target typescript_client.public_api --app-root internal/compiler/testdata/native -o json`
  and the same command with `internal/compiler/testdata/house`: no fixture drift.
- `bun test internal/build/runtime_identity.test.ts`: three tests, ten assertions.
- `go run ./scripts/verify --summary --write`: pass with warnings (41 document
  review reminders, 22 architecture warnings including four changed hotspots,
  and an advisory cached-suite duration warning in the recorded default run).
- `go run ./scripts/verify --probe dev-process --summary --write`: PASS after
  fixing the product exit-code regression, with probe expectations unchanged.
- `go run ./scripts/verify --probe parallel-runtime --summary --write`: PASS.
- In the ONLV task root: `just check-harness`, `just repo-harness`,
  `./scripts/scenery check -o json`, `go test ./...`,
  `./scripts/scenery harness -o json --write`, and
  `./scripts/scenery validate development -o json --write`: PASS. Development
  runs `bun test development` (nine tests) and the development TypeScript project.
- `just smoke`, `just feature projects`, `just feature ahjs`, and
  `bun development/prove-producer-transition.ts`: PASS against owned port 4070.
- `git diff --check` in both checkouts: PASS.

No applicable selected validation was skipped. Full release certification,
all-root timing audits, broad unrelated UI/native suites, deployment and
publication were not selected. Existing personal ONLV at port 4920 was untouched.
The task checkout intentionally retains its local framework source selection;
do not publish that machine-local go.mod replacement. Publication must pin the
published Scenery revision and repeat the relevant consumer acceptance.

## Context and Orientation

Scenery cmd/scenery owns detached readiness, watch/rebuild orchestration and
candidate replacement. internal/build owns verified inputs, runtime workspace
preparation, Go compilation and framework selection. internal/validation and
internal/app own validation profile execution/configuration. Existing runtime
headers identify linked builds. ONLV development/runtime-identity.ts now imports
the helper returned by verified Scenery build/inspection rather than reproducing
serialization and schema identities. development/runtime.ts and scripts/scenery
retain session access and the minimal pre-execution bootstrap boundary.
development/prepare.ts uses the tested startup boundary; development/acceptance.ts
shares business proof between fast feature and restart acceptance.

Work in /Users/petrbrazdil/Repos/scenery and
/Users/petrbrazdil/Repos/onlv-coherent-task-environments. Both are currently at
the reviewed pair. Their existing fixture is isolated from personal data.

## Milestones

1. Reliable readiness with deterministic boundary tests.
2. Attributed, materially faster verified edit/restart loop.
3. Framework-owned verification and typed validation; delete replaced app code.
4. Shared fast/full acceptance and real revision-bound browser feature proof.

## Plan of Work

First locate the detached ready predicate and session publication order. Make
successful readiness require the verified published generation, keep permanent
ownership/spec failures immediate, and exercise the ordinary prepare path.
Next extend existing structured runtime phase events rather than inventing a
parallel tracer. Capture one complete source edit and repeated unchanged starts
with cache hit/miss reasons. Optimize only measured repeated work using Go's
existing cache and tracing support. Preserve no restart for identical or
test-only edits. Then add the smallest existing build/inspection extension that
owns candidate and response identity verification and process transitions.
Migrate ONLV atomically to it, replacing its protocol implementation. Replace
environment-encoded validation vectors in the same singular config surface.
Compose fast feature acceptance and full smoke with shared business assertions;
add a real browser journey against the verified generation, never mocks presented
as live acceptance. Keep contracts, schemas and agent guidance aligned.

## Concrete Steps

Use rg and task-scoped inspect docs before each ownership boundary. Run targeted
tests while implementing, and record exact measurements and cache explanations
under ignored .scenery/harness. Keep each milestone's evidence and unresolved
requirements in this plan and the ONLV companion plan. Do not reuse a successful
receipt after changing source without refreshing its proof.

## Validation and Acceptance

From Scenery root run go test ./cmd/scenery, go test ./internal/build,
go test ./internal/app, go test ./internal/validation and every additional changed
package before go test ./.... Run golangci-lint run ./... and
go run ./scripts/verify --summary --write (supersedes quick); inspect cumulative
changed_area recommendations. External lifecycle proof is
go run ./scripts/verify --probe dev-process --summary --write and
go run ./scripts/verify --probe parallel-runtime --summary --write. If compiler
or generator changes, run both exact fixture generation commands from AGENTS.md
and required catalog consumer checks. Full release certification and all-root
timing audit are not selected; local loop measurement is explicitly authorized.

From ONLV task root run just check-harness, just repo-harness,
./scripts/scenery check -o json, go test ./..., and
./scripts/scenery harness -o json --write. Run the development Bun tests and
typecheck using the final typed development profile. Run just smoke plus the
new fast feature path against the owned fixture, and the normal-launcher
different-spec producer transition proof. Browser acceptance must identify the
current generation, exercise a real feature mutation/readback, reject stale
identity, and record screenshots plus browser/network failures. Broader native,
unrelated UI suites and production certification are not implied by this proof.
Repeat warm restart and ordinary implementation edit samples at least three
times, retaining source/build/PID identities and reporting range/median only.
Test-only and identical-content edits must preserve the existing process.

## Idempotence and Recovery

Control active runtimes with their retained producer. Never overwrite personal
database/object state. New fixtures must be marker-owned; stop only fixtures
created here and retain their data unless deletion is separately authorized.
Temporary measurement edits must be restored byte-for-byte and must not mask
concurrent edits. Retain unsuccessful evidence on failures. Before restarting
a timed-out command, inspect the original handle rather than guessing it died.

## Artifacts and Notes

Baseline diagnostic evidence lives in the stopped
onlv-coherent-task-environments-task-phase-measure-20260910 fixture under
.scenery/harness/prepare-phases.json and prepare-analysis.md. It is measurement
evidence only, not proof that ordinary preparation completed successfully.
The prior completed plan 0175 is immutable and does not cover this milestone.

## Interfaces and Dependencies

Keep the current exact CLI envelope, producer receipt, source snapshot and
runtime ownership model. Extend existing supported inspection/build surfaces
instead of adding an app-specific orchestration framework. Any wire change
updates checked schemas, machine identity, local-contract, tests and consumers
together. No new dependency or environment knob is currently justified.
