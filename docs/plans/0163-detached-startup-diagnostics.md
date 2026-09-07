# Detached Startup Diagnostics

This ExecPlan is a living document and must be updated as work proceeds.

## Purpose / Big Picture

Make `scenery up --detach` report a terminal supervisor startup failure promptly,
with its structured diagnostic and exit classification intact. Session cleanup
must not erase the result. A child exit without a result and a real readiness
timeout must remain distinguishable, and public errors must retain a log pointer
without publishing raw child stdout or stderr.

## Progress

- [x] 2026-09-07: Verified the detached parent only polls sessions, releases its
  child without observing exit, and wraps every wait failure as a timeout.
- [x] 2026-09-07: Verified `runWithWatch` intentionally exits on an initial build
  failure and its console marks the returned error already rendered; the public
  renderer otherwise regenerates an internal diagnostic from an untyped error.
- [x] 2026-09-07: Implement one private startup-result pipe using the current CLI envelope,
  preserve typed diagnostics, and observe child exit independently of sessions.
- [x] 2026-09-07: Cover terminal failure, exit/result ordering, timeout, successful detach,
  protocol validation, and raw-output isolation with focused tests and release
  process proof.
- [x] 2026-09-07: Update living documentation and complete repository validation.

## Surprises & Discoveries

- The working tree already contains the completed optional-dotenv fix. Preserve
  it and validate the combined tree; do not restore files shared by both changes.
- `runConsole.Finish` currently carries a string error and wraps the returned
  error in `silentCLIError`. Startup reporting must retain the original typed
  diagnostic across that wrapper rather than relying on another render pass.
- The native release fixture needs a real private agent dashboard: a dummy
  backend makes control-plane initialization fail before source compilation.

## Decision Log

- 2026-09-07, Codex: Keep stdout/stderr directed to the durable detached log.
  Pass a private pipe on inherited descriptor 3, using the existing
  `SCENERY_DEV_DETACHED_CHILD` marker and current `scenery.cli.event` summary
  envelope. Mark the descriptor close-on-exec in the supervisor so application
  children cannot write startup results or keep the pipe alive.
- 2026-09-07, Codex: Use `exec.Cmd.Wait` and the existing session/readiness loop,
  with cancellation when the child reports failure or exits. Do not add a
  general process framework or parse human-readable log lines.
- 2026-09-07, Codex: Preserve the child diagnostic and report token in a small CLI
  error carrier. Known local environment preconditions receive their existing
  SCN8003 classification at the source; unknown internal errors stay sanitized.

## Outcomes & Retrospective

Completed 2026-09-07. Detached startup preserves the original structured
diagnostic, classification and internal report token independently of session
cleanup. Parent-owned PID/wait/log context remains visible in JSON. A private
bounded current-protocol pipe and direct child-exit observation replace the
timeout-only failure path; no log parser or general process framework was added.

Native release proof covers malformed dotenv (exit 3), invalid source (exit 2),
internal build failure (exit 10 with the same token in the child summary),
successful readiness and same-PID reacquisition despite stdout/stderr noise.
Real shell children cover exit without a result and actual timeout. Unit tests
cover decoding, race ordering, cancellation and diagnostic preservation.

The full release self-harness and `scripts/release-gate.sh` passed. The gate's
external-app smoke was skipped because no external app root was configured.
No shared CLI installation or global Postgres mutation was performed.

## Context and Orientation

`cmd/scenery/dev_detach.go` launches the supervisor and polls agent sessions.
`cmd/scenery/watch.go` owns startup and cleanup. `cmd/scenery/console.go` emits
`scenery.run.event` payloads inside CLI event envelopes. `cmd/scenery/main.go`
classifies and renders CLI failures. `internal/machine` already validates exact
envelope/spec/producer identities; `internal/compiler` owns transport diagnostic
construction and internal-message sanitization. These shared protocols remain
the authority. Agent session schemas need no new retained failure state.

## Milestones

1. Preserve a typed startup diagnostic through child reporting and parent JSON
   rendering, including SCN9000 report-token identity.
2. Stop readiness waiting on failure/exit, reap failed children, and leave a
   successfully detached supervisor independent of the launcher.
3. Complete deterministic unit coverage and real-process release acceptance.

## Plan of Work

Add narrowly scoped startup transport helpers in `cmd/scenery`, wire the pipe
into detached launch and the watcher, and reuse existing CLI envelopes and
diagnostics. Keep the existing readiness contract for `--wait ready` and
`--wait registered`. Add parent-owned context (PID, wait mode, failure kind and
log path) only after validating the structured child result. Update the local
contract, agent guide, installed skill, environment reference and knowledge
index to describe the result and its public diagnostics.

## Concrete Steps

From the repository root, use `go test ./cmd/scenery` during implementation.
Build the local CLI with `go build -o .scenery/harness/bin/scenery ./cmd/scenery`.
Refresh classification with
`.scenery/harness/bin/scenery harness self --quick --summary --write`.
Exercise real subprocess/pipe behavior in the existing release harness rather
than ordinary Go test roots. For a native app probe, copy
`testdata/apps/basic` to a disposable directory, point its Go replacement at this
checkout, add malformed `.env` content, and run
`.scenery/harness/bin/scenery up --detach --app-root <copy> -o json` against a
private agent with no production routes. Remove only the probe's processes and
temporary files after verification.

## Validation and Acceptance

Expected classes are `go-package`, `cli-json-contract`, and
`release-sensitive-or-runtime`. From the repository root run:

- `go test ./cmd/scenery`
- `go test ./...`
- `golangci-lint run ./...`
- `.scenery/harness/bin/scenery harness self --quick --summary --write`
- `.scenery/harness/bin/scenery harness self --release --summary --write`
- `scripts/release-gate.sh`

Release mode supersedes the default self-harness command. Record any skipped
external-app gate explicitly when no external app root is configured. New test
roots must satisfy the repeated isolated 100ms p95 budget. The process proof
must show that a child failure survives session removal, EOF/exit ordering does
not erase the diagnostic, a live child with no result can time out, and stdout
or stderr resembling a diagnostic is never treated as authoritative. Native
startup failure must return the expected error classification and log pointer
well before the normal readiness deadline. Successful detached startup and
already-running acquisition must continue to use existing readiness semantics.

## Idempotence and Recovery

No shared CLI install or agent restart is part of this change. Parent failure
cleanup targets only the child it started and waits for termination. Successful
detach closes the parent's startup reader and leaves the child alive. Temporary
acceptance apps and private agent state are disposable. Never remove the
machine-global Postgres container to repair a private-probe mismatch.

## Artifacts and Notes

Self-harness evidence lives under `.scenery/harness/`; release-gate logs live
under `.scenery/release-gate/`. Those paths are ignored machine-local evidence.
Keep this plan current until the acceptance results are recorded.

- Focused startup tests, `go test ./cmd/scenery`, `go test ./...`, and
  `golangci-lint run ./...` passed. Six new top-level test roots ran 20 times
  each in isolated test-binary processes: maximum p95 30ms, maximum single
  observed body 50ms (Go verbose duration precision is 10ms).
- The release harness passed native dotenv/source failure propagation,
  successful ready detach, idempotent reacquisition with the same supervisor,
  stdout/stderr noise isolation, real child exit without a result, and real
  timeout. The final rerun also passed the internal build failure's report-token
  comparison against the child summary.
- Final gate evidence: `.scenery/release-gate/20260907T100939Z/`. An earlier
  concurrent gate failed `TestAssistantStatusReadsSnapshotAndRemainsProviderNeutral`
  under race; 20 isolated race repetitions, the release harness race suite, and
  a sequential full gate rerun passed. This unrelated intermittent failure was
  not hidden or patched as part of the detached-startup fix.

## Interfaces and Dependencies

Use Go's standard library and the existing `internal/machine`, `internal/graph`,
and `internal/compiler` contracts. The startup channel is private between the
launcher and its supervisor, not a new public command, flag, environment knob,
session-store record, or general process API.
