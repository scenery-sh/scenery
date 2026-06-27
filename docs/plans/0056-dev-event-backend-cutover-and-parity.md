# Dev Event Backend Cutover and Parity

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The dev-event plane is now VictoriaLogs-backed for CLI and console reads. The local dashboard/session metadata store is JSON-backed, and scenery no longer carries an embedded SQL driver dependency for local dev state.

## Progress

- [x] 2026-05-31: Created this ExecPlan and linked it from `docs/plans/active.md`.
- [x] 2026-06-01: Added a shared dev-event reader path for `scenery logs`, `scenery attach`, `scenery attach --tui`, and `scenery console`.
- [x] 2026-06-01: Removed the migration-only log comparison command and the legacy local dev-event read backend.
- [x] 2026-06-01: Made `--backend auto` and `--backend victoria` select the VictoriaLogs read path.
- [x] 2026-06-01: Moved dev-event ID assignment to the producer path before VictoriaLogs export.
- [x] 2026-06-01: Replaced the remaining dashboard/session metadata store with `devdash.json`.
- [x] 2026-06-01: Removed the embedded local SQL driver module dependency and architecture allowlist entry.
- [x] 2026-06-01: Renamed the default legacy async runtime dev-server database filename from `dev.sqlite` to `dev.db`.

## Surprises & Discoveries

- 2026-06-01: Replacing the store still needs to preserve `sql.ErrNoRows` as the caller-facing not-found sentinel because dashboard, inspect, and agent code already branch on it.
- 2026-06-01: `json.RawMessage` fields are pretty-printed by `json.MarshalIndent`; store reads compact those fields before returning user-facing records so existing output assertions stay stable.

## Decision Log

- Decision: VictoriaLogs is the dev-event read substrate.
  Rationale: Keeping a second local event reader after the cutover would preserve obsolete fallback behavior and split test coverage.
  Date/Author: 2026-06-01 / Codex

- Decision: Keep dashboard/session metadata in a small JSON store.
  Rationale: The data is local dev state and saved dashboard requests, so a standard-library file store is enough and avoids carrying an embedded database driver.
  Date/Author: 2026-06-01 / Codex

- Decision: Do not migrate old local dashboard cache files.
  Rationale: They are disposable local dev-session caches, not app source of truth.
  Date/Author: 2026-06-01 / Codex

## Outcomes & Retrospective

Completed on 2026-06-01.

Drift note (2026-06-12): the "small JSON metadata store" outcome did not hold — `devdash.json` remained the report-ingest write path for trace summaries, trace events, and log events, and grew to 422 MB. The current contract and resolution path live in ExecPlan 0076 (Devdash Control-Plane Store Slimming).

The implementation now has one current dev-event read path for logs, attach, TUI, and console: VictoriaLogs. Local dashboard/session metadata and saved dashboard requests are stored in `devdash.json`; trace summaries, trace events, and report log events are Victoria-backed and no longer persisted there.

The module graph and active source/docs no longer reference an embedded local SQL driver. Historical plans may still describe the migration history, but they are not current contract.

## Context and Orientation

Relevant files:

```text
cmd/scenery/logs.go
cmd/scenery/dev_event_backend.go
cmd/scenery/dev_event_ids.go
cmd/scenery/dev_supervisor.go
cmd/scenery/dev_frontends.go
cmd/scenery/victoria_query.go
internal/devdash/store.go
internal/devdash/dev_events.go
docs/local-contract.md
```

Current contract:

- `scenery logs`, `scenery attach`, `scenery attach --tui`, and `scenery console` read structured dev events from VictoriaLogs.
- `--backend auto` and `--backend victoria` are equivalent for the current read path.
- Dev-event IDs are assigned before export.
- Dashboard session metadata, process events, and saved requests live in `devdash.json`; trace summaries, trace events, and report log events live in Victoria.
- `scenery prune` does not delete VictoriaLogs storage.

## Milestones

- [x] Shared dev-event reader path.
- [x] VictoriaLogs-only dev-event read contract.
- [x] Producer-side dev-event IDs.
- [x] JSON-backed dashboard/session metadata store.
- [x] Dependency graph cleanup.
- [x] Documentation cleanup.
- [x] Validation.

## Plan of Work

The completed work removed the obsolete local dev-event backend, moved event IDs to the producer/export path, replaced the dashboard store with a standard-library JSON store, and updated contracts and validation guidance.

## Concrete Steps

1. Remove stale backend selection and comparison code.
2. Export supervisor and frontend events directly to VictoriaLogs with preassigned IDs.
3. Replace the old dashboard store implementation with `devdash.json`.
4. Remove the embedded database module dependency and direct-dependency allowlist entry.
5. Update tests, docs, and harness guidance.
6. Verify the module graph and active source/docs scans.

## Validation and Acceptance

Latest validation on 2026-06-01:

```sh
go test ./internal/devdash ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
go list -m all | rg -n "sqlite|modernc"
go list -deps ./... | rg -n "sqlite|modernc"
rg -n "sqlite|SQLite" --glob '!docs/plans/**' --glob '!pbcopy' --glob '!vendor/**' --glob '!node_modules/**' .
```

The test, install, and diff commands passed. The dependency and active-tree scans returned no matches. The final self-harness run should report `ok: true`; advisory timing and review-due warnings are acceptable in default mode.

## Idempotence and Recovery

- Store writes rewrite `devdash.json` atomically through a temporary file and rename.
- The store rebuilds missing in-memory defaults when opening an empty or missing file.
- Old local cache files are ignored; deleting `devdash.json` only clears dashboard/session cache state.
- Dev-event reads remain independent of the dashboard metadata store.

## Artifacts and Notes

- `.scenery/harness/self-latest.json` records the latest self-harness result.
- `devdash.json` is runtime cache state and must not be committed.
- `docs/plans/0056-dev-event-backend-cutover-and-parity.md` is the completed plan record for this migration.

## Interfaces and Dependencies

Public CLI surface:

```sh
scenery logs --backend auto|victoria
scenery console --backend auto|victoria
scenery attach --tui --backend auto|victoria
```

The implementation uses the Go standard library for local dashboard/session metadata persistence and does not add a replacement database dependency.
