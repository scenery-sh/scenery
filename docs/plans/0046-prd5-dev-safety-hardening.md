# agent-native local-dev Dev Safety Hardening

This ExecPlan is a living document. Update the Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective sections as work proceeds.

## Purpose / Big Picture

Finish the remaining agent-native local-dev local-dev safety work so `scenery dev` is robust for parallel worktrees. The target state is that the default development path is agent-routed, session-scoped, explicit about unsafe escape hatches, safe when cleaning up or signaling processes, and backed by validation that can catch regressions.

The immediate user-facing problem is that several legacy/manual paths can still reintroduce global ports, ambiguous ownership, or mixed session state. This plan records the remaining work so future agents can continue without relying on chat history.

## Progress

- [x] 2026-05-27: Confirmed ONLV already defaults to `scenery dev --app-root` and declares managed Postgres/frontends.
- [x] 2026-05-27: Removed silent managed frontend fallback unless `SCENERY_FRONTEND_<NAME>_ADDR` or `allow_shared_upstream: true` is explicit.
- [x] 2026-05-27: Decoupled `SCENERY_DEV_CACHE_DIR` from local agent home.
- [x] 2026-05-27: Preserved public host/proto/port context through the agent router.
- [x] 2026-05-27: Migrated SQLite trace-summary uniqueness to include `session_id`.
- [x] 2026-05-27: Added `docs/environment.md` as the environment-variable reference.
- [x] 2026-05-27: Added owner fingerprints for sessions/substrates, conservative verification before session/substrate signaling, and owner checks before shared substrate reuse.
- [x] 2026-05-27: Added managed Postgres port owner marker checks so a reachable saved port is reused only when its recorded owner verifies.
- [x] 2026-05-27: Extended shared substrate ownership with per-component owner fingerprints, so each component PID is verified before interrupt.
- [x] 2026-05-27: Added `scenery down --db --state --all` and `scenery db drop` for explicit managed DB/state cleanup.
- [x] 2026-05-27: Added stale non-running session registry/state cleanup, now exposed as `scenery prune --older-than`.
- [x] 2026-05-27: Added explicit `scenery dev --session <id>` and `scenery dev --new-session`, and preserved session ID/branch across later registrations.
- [x] 2026-05-27: Persisted current-session selection by app root so `current` follows the latest registered session instead of incidental list ordering.
- [x] 2026-05-27: Added visible warnings for legacy proxy and manual TCP listen/port escape hatches.
- [x] 2026-05-27: Wired the agent dashboard to the shared Victoria substrate for Victoria-backed trace reads.
- [x] 2026-05-27: Added a self-harness parallel dev-session check covering distinct sessions, Unix API backends, managed DB names, task queues, frontend/Grafana/legacy async runtime routes, logs, traces, Victoria substrate reads, and sibling session deletion.

## Surprises & Discoveries

- 2026-05-27: `onlv` had already been migrated to the managed scenery path; the stale review item was no longer true in `/Users/petrbrazdil/Repos/onlv/.scenery.json` and `Justfile`.
- 2026-05-27: Agent dashboard controller still returned `nil` from `dashboardVictoria()` during review; it now builds a cached Victoria query stack from the shared agent substrate.
- 2026-05-27: The safer owner for shared substrates is the primary child process, not the dev supervisor PID. The registry now captures the child PID when one exists, so a later session can verify the actual shared service before reuse.

## Decision Log

- Decision: Preserve old `owner_pid` and `pids` JSON fields while adding richer owner blocks.
  Rationale: Existing session manifests, tests, and scripts may still read the old fields. Additive metadata gives safer behavior without breaking compatibility.
  Date/Author: 2026-05-27 / Codex.
- Decision: Reuse shared substrates only when the recorded owner verifies.
  Rationale: A reachable port alone does not prove that the listener belongs to scenery. If the fingerprint does not verify, scenery deletes the stale registry record and starts fresh rather than routing sessions to an ambiguous listener.
  Date/Author: 2026-05-27 / Codex.
- Decision: Store per-component substrate owner fingerprints in addition to substrate-level owner metadata.
  Rationale: Multi-process substrates such as Victoria have several component PIDs. Verifying only the primary substrate owner before interrupting every component can still be unsafe if a sibling PID is stale or reused.
  Date/Author: 2026-05-27 / Codex.

## Outcomes & Retrospective

Completed on 2026-05-27. The remaining agent-native local-dev review risks were addressed with explicit session control, cleanup/prune commands, stronger process ownership, explicit legacy escape-hatch warnings, shared Victoria dashboard wiring, and a self-harness parallel-session check. Validation is recorded in `.scenery/harness/self-latest.json`.

## Context and Orientation

Relevant files:

- `internal/agent/types.go` defines session and substrate registry JSON types.
- `internal/agent/registry.go` writes sessions and substrates to `<agent-dir>/sessions.json`.
- `internal/agent/server.go` deletes sessions and currently interrupts `OwnerPID` directly.
- `cmd/scenery/dev_services.go`, `cmd/scenery/grafana.go`, `cmd/scenery/victoria.go`, and `cmd/scenery/legacy-async-runtime_dev.go` register shared substrates.
- `cmd/scenery/dev_supervisor.go` and `cmd/scenery/watch.go` register sessions and child backends.
- `cmd/scenery/agent.go`, `cmd/scenery/db.go`, and related command files own user-facing cleanup surfaces.

## Milestones

1. Ownership safety: add owner fingerprints to sessions/substrates, verify before signaling, and avoid reusing stale owned resources when verification fails.
2. Cleanup surfaces: implement `scenery down --db --state --all`, `scenery db drop`, and `scenery prune --older-than`.
3. Explicit session control: implement `scenery dev --session <id>` and `scenery dev --new-session`, then make `current` session resolution explicit.
4. Escape-hatch hardening: make `--proxy`, explicit `--port`, and explicit `--listen` clearly legacy/manual or route through the agent.
5. Parallel harness: run two dev sessions from fixture worktrees and prove routes, DBs, task queues, frontend backends, logs, and traces remain distinct.

## Plan of Work

Start with additive internal safety. Owner fingerprints are lower risk than changing CLI behavior and directly reduce the chance of interrupting or reusing the wrong process. Then add cleanup commands because safe ownership verification makes destructive options more defensible. Session override and proxy semantics should follow once cleanup and ownership are clear.

## Concrete Steps

1. Add `Owner` metadata to `internal/agent/types.go`.
2. Capture owner metadata in `RegisterRequest` and `UpsertSubstrateRequest` handling when a PID is provided.
3. Add platform helpers that can fingerprint a live process from PID using `/proc` on Linux and `ps` on Unix-like systems where needed.
4. Verify owner metadata before `handleSession` signals a session owner and before `Server.Close` signals substrate PIDs.
5. Add unit tests for owner capture, registry persistence, and skipped signaling on mismatched fingerprints where practical.
6. Run `go test ./internal/agent ./cmd/scenery`, `go test ./...`, `go install ./cmd/scenery`, and `scenery harness self --json --write`.

## Validation and Acceptance

The full plan is complete only when:

- Default `scenery dev` uses agent/session-safe resources without relying on fixed public ports.
- Legacy/manual escape hatches are explicit and documented as non-parallel-safe or are agent-backed.
- Session and substrate signaling verifies owner fingerprints before interrupting processes.
- Cleanup commands can remove session DB/state safely and intentionally.
- `--session` and `--new-session` work without breaking current-session resolution.
- A parallel integration harness proves two worktrees do not mix routes, DBs, task queues, logs, traces, frontend routes, or process cleanup.

Per-change validation should include:

```text
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
```

## Idempotence and Recovery

All registry schema changes must be additive or include migrations that tolerate old JSON. If a local agent registry contains only `owner_pid`, scenery should continue to work and should capture richer owner data on the next upsert. Cleanup commands must default to non-destructive behavior unless the user passes explicit flags.

## Artifacts and Notes

The current self-harness artifact lives at `.scenery/harness/self-latest.json`. The environment variable reference is `docs/environment.md`.

## Interfaces and Dependencies

The work should avoid new external dependencies. Use Go standard library process/file APIs and small platform-specific helpers. CLI interfaces added by this plan must be documented in `docs/local-contract.md` and, when env vars are involved, `docs/environment.md`.
