# Source Installation and Agent Coexistence

This ExecPlan is a living document maintained under PLANS.md.

## Purpose / Big Picture

Make source builds the sole CLI installation path and verify that using another
Scenery build cannot silently replace the shared agent or harm another worktree.

## Progress

- [x] (2026-09-05) Inspect source install, release updater, and agent replacement.
- [x] (2026-09-05) Remove release updater, payload schema, obsolete tests/probe,
  release workflow and GoReleaser configuration; align current documentation.
- [x] (2026-09-05) Establish fail-closed shared-agent compatibility and focused coverage.
- [x] (2026-09-05) Run isolated old/candidate build and two-worktree proof,
  full Go/lint, release harness, and local release gate.

## Surprises & Discoveries

At the start, EnsureWith replaced an older agent based on build age without first
checking its machine contract. Health decoding itself did not validate identity.

The incompatible-agent error initially became opaque SCN9000 at the CLI
boundary. A failed_precondition prefix now exposes SCN8003 with the observed
build identities, required spec identity, and safe operator choices. Health
transport failures other than absent/refused sockets also fail without starting
a replacement. An unused test transport helper belonged to the removed updater;
it now lives with its remaining router test consumer.

## Decision Log

- 2026-09-05 / Codex: Remove the release-only upgrade command instead of adding
  another source-build wrapper. Users choose and build a checkout explicitly.
- 2026-09-05 / Codex: Do not touch the developer's live agent, global binary,
  databases, edge, or launch services during acceptance. Use private temporary
  homes, loopback ports, and independently owned processes.
- 2026-09-05 / Codex: Different build versions with identical current machine
  contracts share the existing agent. Build ordering is not a compatibility
  check. Remove age comparison and implicit replacement; keep explicit restart.
- 2026-09-05 / Codex: Keep local release-mode safety validation. Removing public
  binary publication does not remove local native process and runtime probes.

## Outcomes & Retrospective

Completed on 2026-09-05. Source builds are the sole supported CLI delivery path;
the release updater and publication configuration are removed. Agent ensure
checks the current machine contract and does not replace a live agent implicitly.
Old/candidate builds shared two isolated app worktrees successfully; injected
incompatibility failed read-only with SCN8003. This does not establish support
for arbitrary historical protocols or mixed-version database migrations.

## Context and Orientation

CLI upgrade implementation and its process probe are in cmd/scenery. Shared
agent connection and replacement are in internal/agent/client.go. Existing
restart probes cover live substrate ownership and routed requests.

## Milestones

1. Remove obsolete distribution plumbing and document pinned source installs.
2. Check shared-agent compatibility before mutation or replacement.
3. Prove isolation and record limitations.

## Plan of Work

Preserve current application recovery preflights and explicit operator restart.
Use existing machine schema/spec identity for protocol compatibility; build age
is not evidence of compatibility. Extend existing release probes rather than
adding slow ordinary Go test roots.

## Concrete Steps

From repository root run go test ./internal/agent ./internal/edge ./cmd/scenery,
golangci-lint run ./..., go test ./..., and go build -o
.scenery/harness/bin/scenery ./cmd/scenery. Run
.scenery/harness/bin/scenery harness self --quick --summary --write, then
.scenery/harness/bin/scenery harness self --release --summary --write and
scripts/release-gate.sh. Run the union of changed-area recommendations.

## Validation and Acceptance

Classes: CLI JSON contract, Go package, release-sensitive/runtime. Build and
exercise separate versioned CLI executables in private agent homes. Verify
reachable routes and unchanged session/data ownership after incompatible
access. Source installation uses a disposable GOBIN. External-app smoke is
skipped only when no external app root is selected. No public release or CI
run is required. Do not claim database isolation from a process-liveness check.

## Idempotence and Recovery

Temporary probe resources are owned by the probe and cleaned on exit. No shared
installation, service restart, or application mutation is authorized by tests.

## Artifacts and Notes

Reports live under ignored .scenery/harness/source-coexistence-* paths.

Native macOS evidence on 2026-09-05:

- A private probe copied the basic fixture into a temporary Git repository and
  created two detached worktrees. The installed unmodified 16f156a2 CLI and a
  separate candidate built from this dirty checkout each built a headless app.
  The candidate received its actual later build timestamp via sceneryBuiltAt,
  exercising the ordering that formerly triggered replacement.
- The old CLI owned a private agent; both app processes registered separate
  sessions. Candidate `up --detach --wait registered -o json` reused the second
  session while the agent PID stayed unchanged. Both direct and agent-routed
  HTTP echo endpoints returned 200; separate worktree sentinel files survived.
- A private Unix-socket health endpoint advertised a deliberately mismatched
  spec revision. Candidate `up` returned SCN8003 with an actionable message;
  the endpoint received zero mutating requests and the original agent survived.
- This is old-versus-candidate source-build proof, not two historical releases.
  Incompatibility is injected at the health boundary, not a claim that every
  past protocol is supported. Basic has no database. The separate PostgreSQL
  harness proves worktree database/schema isolation; a mixed-version database
  migration or durable-state upgrade is not exercised or claimed here.
- Probe source and output: `.scenery/harness/source-coexistence.mjs` and
  `.scenery/harness/source-coexistence-proof.log`. All temporary processes,
  worktrees, data files, sockets, and homes were cleaned; the shared installed
  CLI, live agent, and application data were not changed.
- `go test ./internal/agent ./internal/edge ./cmd/scenery`, `go test ./...`, and
  `golangci-lint run ./...` passed (zero lint issues).
- Local release harness passed, including full race and native process probes;
  output: `.scenery/harness/source-coexistence-release.log`.
- `scripts/release-gate.sh` passed; logs:
  `.scenery/release-gate/20260905T193816Z/`. This includes disposable source
  installation, UI build, full/race Go tests, fixture HTTP, router safety, and
  artifact hygiene. External-app smoke was explicitly skipped: no app selected.
- Generator implementation and app contracts are unchanged; fixture client
  regeneration is not selected by the changed-area oracle. Default harness is
  superseded by release mode. Final quick mode refreshes documentation metadata.

## Interfaces and Dependencies

Remove scenery.upgrade machine payload. Keep explicit toolchain sync and
operator-controlled agent restart. No new dependencies or environment knobs.
