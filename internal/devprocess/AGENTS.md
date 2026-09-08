# Development Child Processes

## Purpose

Own concrete child-process start, output capture, readiness, cancellation,
idempotent stop, process-group signaling, bounded line tails, process-table
observation, and the existing concrete named substrate locks.

## Local Contracts

- Keep application/session decisions and restart policy in their existing owners.
- `Done` is receive-only; only the process runner closes the live completion signal.
- Preserve Linux parent-death behavior and detached-child distinctions.
- Preserve current default deadlines and process-tree cancellation semantics.
- Process-table rows are observations, never ownership credentials. Session
  selection and ownership-checked cleanup belong to `internal/agent`.
- Lock options are call-local; zero durations retain the original production
  policy. Keep lock ordering and both within/cross-process serialization.
- Do not add a scheduler, job registry, runtime dispatcher, or test-only exports.
- Ordinary tests are in-process; real process/network assertions belong in the
  repository release runner, including early exit, readiness, and repeated stop.

## Verification

Run `go test ./internal/devprocess ./cmd/scenery`, then the root validation union.
Every moved or changed exact test root needs the unchanged 100 ms timing proof.
