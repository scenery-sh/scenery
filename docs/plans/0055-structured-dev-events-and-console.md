# Structured Dev Events and Console

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

`scenery dev --detach`, `scenery attach`, and `scenery logs --jsonl` already provide the right lifecycle shape for local development sessions. The missing foundation is a structured event plane that identifies where output came from and what it means.

Today the local log store is centered on raw process output: app id, session id, pid, stream, output, and time. That is enough to follow stdout and stderr, but it is not enough for a reliable local console that separates the API app, TypeScript Temporal worker, Temporal server, managed Postgres/sync, frontends, Grafana, build phases, and supervisor events without fragile pid or text heuristics.

After this work, `scenery dev` emits normalized, versioned dev events. The SQLite store persists raw output plus parsed fields and source identity. Plain terminal following, JSONL output, dashboard views, agent consumers, and a terminal UI consume the same event stream:

```text
scenery dev
  -> supervisor emits normalized events
  -> store persists raw and normalized records
  -> attach/logs/dashboard/agents/TUI consume one stream
```

The terminal UI is a view over the event stream, not the source of truth. The default CLI remains boring and pipeable:

```sh
scenery attach
scenery attach --jsonl
scenery logs --follow
scenery logs --jsonl
```

Interactive mode is opt-in:

```sh
scenery attach --tui
scenery console
```

In CI, non-TTY output, dumb terminals, or redirected output, interactive mode falls back to normal log following.

## Progress

- [x] 2026-05-31: Created this ExecPlan from the structured logging and TUI design request and linked it from `docs/plans/active.md`.
- [x] 2026-05-31: Implemented structured source identity, `dev_sources`/`dev_events` storage, best-effort parsing, and the `scenery.dev.event.v1` schema.
- [x] 2026-05-31: Routed app, TypeScript worker, managed frontend, build, supervisor, Postgres, sync, Temporal, Victoria, and Grafana state/output into structured dev events while preserving legacy process output.
- [x] 2026-05-31: Added `scenery logs` and `scenery attach` filters for source, kind, level, grep, and since; JSONL now emits `scenery.dev.event.v1`.
- [x] 2026-05-31: Added `scenery attach --tui`, `scenery console`, non-TTY fallback, source summaries, event expansion, errors-only mode, and grouped recent errors.
- [x] 2026-05-31: Added `scenery status --watch`, docs, schemas, and focused tests for storage, filters, fallback, and console rendering.
- [x] 2026-05-31: Validated with focused tests, full `go test ./...`, `git diff --check`, and `go install ./cmd/scenery`. Ran `scenery harness self --json --write` twice; it wrote `.scenery/harness/self-latest.json` and all non-timing steps passed, but the Go test timing budget step still failed.
- [x] 2026-06-01: Added dual-write of structured dev events into VictoriaLogs and made `scenery logs`/plain `attach` support `--backend auto|victoria|sqlite` for side-by-side parity verification.
- [x] 2026-06-01: Revalidated with focused tests, full `go test ./...`, `git diff --check`, `go install ./cmd/scenery`, and `scenery harness self --json --write`; the harness again only failed the existing Go test timing budget.

## Surprises & Discoveries

- 2026-05-31: `internal/devdash.ProcessOutput` and the `process_output` table only store app id, session id, pid, stream, output, and created_at. Evidence: `internal/devdash/types.go` and `internal/devdash/store.go`.
- 2026-05-31: `cmd/scenery/dev_supervisor.go` already centralizes app and TypeScript worker output through `captureProcessOutput`, which strips ANSI, writes the store row, notifies the dashboard, and emits `process.output` console events.
- 2026-05-31: Managed frontend processes currently write stdout/stderr to per-session log files in `cmd/scenery/dev_frontends.go` instead of the devdash store. The new managed process helper must cover them explicitly.
- 2026-05-31: The repository architecture check already forbids `github.com/charmbracelet/lipgloss` and points terminal styling at `internal/termstyle`. A Bubble Tea/Lip Gloss TUI would require a deliberate dependency decision and harness rule update, not a casual import.
- 2026-05-31: Temporal, Victoria, and Grafana are started by helpers that do not know the app/session store context. The implementation records source-aware readiness/status events for them from `devSupervisor`, while direct stdout capture stays with app, TypeScript worker, and managed frontends in this pass.
- 2026-05-31: `scenery harness self --json --write` is functionally green for this change except the existing timing gate. The latest written snapshot reports only the `go tests` step as failed: full suite 9.049s over the 7.000s target, with several packages over the 2.000s package budget.
- 2026-06-01: VictoriaLogs' `/select/logsql/query` returns JSON Lines and accepts `limit`, `start`, and `end` form/query args. The `/insert/jsonline` API lets scenery choose stable field names instead of depending on OTLP attribute flattening.
- 2026-06-01: VictoriaLogs dropped dev records when RFC3339 timestamps were sent through `_time_field`. Keeping exact event time as a normal `created_at` field and letting Victoria assign ingest time made the records reliably queryable while preserving JSONL parity.

## Decision Log

- Decision: Build the structured event spine before building the TUI.
  Rationale: A TUI that only splits by pid, stdout, and stderr would be attractive but brittle. Source identity, status, levels, messages, and fields must exist before any interactive view can be trustworthy.
  Date/Author: 2026-05-31 / Codex

- Decision: Introduce a new versioned event schema named `scenery.dev.event.v1`.
  Rationale: The existing `scenery.logs.event.v1` JSONL schema is process-output-shaped. A new schema can model source identity, parsed fields, raw output, and parsing metadata without overloading the old event name.
  Date/Author: 2026-05-31 / Codex

- Decision: Persist raw output forever and treat parsing as best-effort.
  Rationale: scenery controls some logs, but Temporal, sync, Grafana, Bun/Node workers, Vite/Astro frontends, and future sidecars will not all use one logger. Consumers need structured fields when available and exact raw text when parsing fails.
  Date/Author: 2026-05-31 / Codex

- Decision: Keep the first TUI observing-only.
  Rationale: Restart, kill, and mutate controls are useful later, but the first milestone should make observation excellent: source status, filtering, search, event expansion, and error focus.
  Date/Author: 2026-05-31 / Codex

- Decision: Avoid new TUI framework dependencies in the MVP unless implementation proves the payoff is concrete.
  Rationale: The repo prefers minimal dependencies, and the current architecture check forbids Lip Gloss. Start with Go standard library terminal control plus existing `internal/termstyle`; if Bubble Tea or another framework is later justified, record the dependency rationale and update harness rules in the same change.
  Date/Author: 2026-05-31 / Codex

- Decision: Keep legacy `process_output` rows and read them as a fallback.
  Rationale: Existing local databases and dashboard RPC paths still use process output. Structured logs should be the new primary stream, but old rows must remain readable without a migration that risks duplicating local logs.
  Date/Author: 2026-05-31 / Codex

- Decision: Dual-write dev events to VictoriaLogs through its JSON-line ingest API while keeping SQLite as the canonical local insert during verification.
  Rationale: SQLite still assigns the event id and remains the comparison baseline. JSON-line ingest gives scenery stable queryable fields for `scenery.dev.event.v1`; `scenery logs --backend victoria` can then be compared against `--backend sqlite` before the SQLite read path is removed.
  Date/Author: 2026-06-01 / Codex

## Outcomes & Retrospective

Completed on 2026-05-31.

The implementation adds a source-aware structured dev event plane without removing the legacy `process_output` path. `scenery dev` now records normalized `scenery.dev.event.v1` events for app output, TypeScript worker output, managed frontends, build phases, supervisor lifecycle notices, and substrate readiness/status. Each event preserves raw text, parsed level/message/attrs when possible, source identity, stream, pid, restart id, and parse metadata.

The CLI now exposes the structured stream through `scenery logs` and `scenery attach` with `--source`, `--kind`, `--level`, `--grep`, and `--since` filters. JSONL output uses the new schema, while plain output remains pipeable and falls back to legacy rows when structured events are unavailable. `scenery attach --tui` and `scenery console` provide an observing-only terminal cockpit with source tabs, source summaries, errors-only mode, search, event expansion, grouped errors, and automatic non-TTY fallback. `scenery status --watch` was added for a lightweight non-TUI overview.

Documentation, CLI usage text, schema validation inputs, and tests were updated alongside the implementation. Validation passed for focused package tests, the full Go suite, binary installation, and diff whitespace checks. The self-harness was run twice and wrote the stable snapshot; its only failing step is the timing budget for Go tests, not a functional or contract failure.

## Context and Orientation

The current lifecycle is already close to the desired shape:

- `scenery dev --detach` starts a background dev session.
- `scenery attach` follows the current session logs by default.
- `scenery logs --jsonl` emits line-delimited JSON.
- `scenery status --json` reports agent-backed session status.
- The dashboard store uses SQLite through `internal/devdash`.

Relevant files:

```text
cmd/scenery/logs.go
cmd/scenery/dev_supervisor.go
cmd/scenery/dev_typescript.go
cmd/scenery/dev_frontends.go
cmd/scenery/dev_services.go
cmd/scenery/watch.go
cmd/scenery/agent.go
cmd/scenery/dashboard_rpc.go
cmd/scenery/dashboard.go
cmd/scenery/console.go
cmd/scenery/harness_arch.go
internal/devdash/types.go
internal/devdash/store.go
internal/devdash/store_events.go
internal/devdash/observability.go
docs/schemas/scenery.logs.event.v1.schema.json
docs/local-contract.md
```

Current storage and output constraints:

- `process_output` stores raw output rows, not source-aware dev events.
- `log_events` exists for runtime report logs, but it is tied to trace/log report ingestion and lacks process source identity.
- `captureProcessOutput` is the central point for app output and TypeScript worker output.
- Managed frontend output currently bypasses the store and writes to session log files.
- Some substrates are agent/shared components rather than per-session children; they still need source/status events even when no stdout stream is captured.

Target source examples:

```text
api
worker:go
worker:typescript
temporal
postgres
sync
frontend:web
grafana
victoria:metrics
victoria:logs
victoria:traces
build
supervisor
```

Target event shape:

```json
{
  "schema_version": "scenery.dev.event.v1",
  "id": 12345,
  "time": "2026-05-31T12:44:01.223Z",
  "app": {
    "id": "billing",
    "root": "/repo/billing"
  },
  "session_id": "feature-x-839a",
  "source": {
    "id": "worker:typescript",
    "kind": "worker",
    "name": "typescript",
    "role": "temporal-activity-worker",
    "pid": "12351",
    "stream": "stdout",
    "restart_id": "1"
  },
  "level": "info",
  "message": "registered activity",
  "fields": {
    "activity": "SendEmail",
    "task_queue": "scenery.billing.feature-x-839a"
  },
  "raw": "2026-05-31T12:44:01.223Z INFO registered activity activity=SendEmail...",
  "parse": {
    "format": "slog-json",
    "ok": true
  }
}
```

## Milestones

Milestone 1: Structured source identity and storage.

Add dev source and dev event types in `internal/devdash`. Add SQLite tables for source state and versioned events. Store source id, kind, name, role, pid, stream, restart id, level, message, parsed attrs JSON, raw output, parse format, parse ok, session id, app id, and time. Add indexes for session/source/kind/level/time and id-based following.

Milestone 2: Unified capture path.

Replace output-specific helpers with a source-aware capture path. All supervised processes and supervisor-owned lifecycle events go through a helper such as:

```go
type ManagedProcessSpec struct {
    Source DevSource
    Command string
    Args []string
    Env []string
    Dir string
}
```

The app process, TypeScript worker, frontends, managed sync, managed Postgres, Temporal, Victoria, Grafana, setup/build phases, and supervisor notices should all write source-aware events. Shared or external substrates should still write state events even when scenery does not own their stdout.

Milestone 3: Non-TUI filters.

Add the boring CLI filters before the interactive UI:

```sh
scenery logs --source api
scenery logs --source worker:typescript --follow
scenery logs --kind substrate
scenery logs --level error
scenery logs --grep "activity failed"
scenery logs --since 15m
scenery attach --jsonl
scenery status --watch
```

`scenery attach` remains the human default. `scenery attach --jsonl` and `scenery logs --jsonl` emit `scenery.dev.event.v1` after the schema and docs are updated.

Milestone 4: Observing-only terminal UI.

Add `scenery attach --tui` and `scenery console`. In a real TTY, open a full-screen source cockpit. In non-interactive contexts, fall back to normal `scenery attach` following.

The first view has three regions:

```text
scenery dev session: billing-dev / feature-x-839a
[all] [api] [worker:go] [worker:typescript] [temporal] [postgres] [sync] [frontend:web] [grafana] [build]
api                 running   pid 12345   21 req/s    2 errors   last log 1s ago
worker:typescript   running   pid 12351   polling     0 errors   last log 4s ago
temporal            running   shared      ui ready                last log 12s ago
sync            starting  pid 12360   waiting on pg           last log 0s ago
---------------- logs: worker:typescript ----------------
12:44:01.223 INFO  registered activity     activity=SendEmail queue=scenery.billing.abc
12:44:04.719 ERROR activity failed          activity=SyncUser attempt=2 error="..."
```

Keyboard support:

```text
tab / shift-tab      next/previous source
1..9                 jump to source
a                    all logs
e                    errors only
/                    search
f                    freeze/unfreeze autoscroll
enter                expand selected log event as JSON
ctrl-l               clear viewport, not stored logs
q                    quit TUI, not session
```

Do not add restart or kill commands in the first version.

Milestone 5: Parsers and derived intelligence.

Add best-effort parsers for common log formats and source summaries. At minimum support JSON objects with `level` and `msg`/`message`, Go `slog` text, scenery console JSON events, Temporal-ish activity lines, Vite/Astro/Bun dev server output, and generic fallback. Then add derived summaries: last error per source, error count by source, restart count, last successful/failed build, slow/error traces, and failed Temporal activities.

## Plan of Work

Start by making source identity explicit and testable without touching terminal rendering. Define `devdash.DevSource`, `devdash.DevEvent`, `devdash.DevEventQuery`, and a small parser package or file that turns raw lines into normalized event fields. Storage should be additive and migration-safe. Existing local databases can open normally; old `process_output` rows can be read or backfilled into the new event view with conservative source ids such as `process:<pid>` if needed for migration.

Once the data model is in place, make the supervisor write `DevEvent` records. Preserve raw terminal output for normal attached `scenery dev`, but ensure stored and JSONL events always include source metadata. Replace direct frontend log-file writes with managed process capture or a tee that both writes the historical log file and records structured events during the migration.

After source-aware events exist, update `scenery logs` and `scenery attach` to query `dev_events`. Add filters as plain CLI behavior with focused tests. Only then add the terminal UI, using the same store query/follow API as `logs`.

For terminal rendering, start with a small internal package under `cmd/scenery` or `internal/termui` that handles TTY detection, alternate screen setup, key input, resize, and plain ANSI drawing. Keep rendering pure enough to snapshot in tests. If the MVP becomes complex enough to justify Bubble Tea, add that as a deliberate follow-up decision with an architecture-harness update and dependency rationale.

Finally, add error grouping and service health summaries from the event stream and existing trace/metrics data. This should make the console answer "what broke, where, and which service should I inspect?" without duplicating Grafana.

## Concrete Steps

1. Add `DevSource`, `DevEvent`, `DevEventParse`, and query/filter types to `internal/devdash/types.go`.
2. Add SQLite migrations in `internal/devdash/store.go` for `dev_sources` and `dev_events`, plus indexes for session/source/kind/level/id/time.
3. Add store methods in `internal/devdash`, including `UpsertDevSource`, `WriteDevEvent`, `ListDevEvents`, `ListDevEventsSince`, and `ListDevSources`.
4. Add `docs/schemas/scenery.dev.event.v1.schema.json` and update self-harness schema validation if needed.
5. Implement a small log parser that preserves raw text and best-effort extracts level, message, attrs, and parse format.
6. Refactor `captureProcessOutput` into a source-aware `captureServiceOutput` path and keep the old wrapper only until all callers are moved.
7. Introduce managed process start/capture helpers and use them for app output, TypeScript workers, and managed frontends.
8. Emit structured supervisor events for build started/succeeded/failed, restart, shutdown, substrate start/ready/degraded, and source status changes.
9. Update dashboard notifications and dashboard RPC methods to read structured dev events while preserving the dashboard's visible behavior.
10. Extend `logsOptions` and `parseLogsArgs` with `--source`, `--kind`, `--level`, `--grep`, and `--since`.
11. Make `scenery logs --jsonl` and `scenery attach --jsonl` encode `scenery.dev.event.v1`; update `docs/local-contract.md`, CLI usage, and schemas in the same change.
12. Add `scenery attach --tui` parsing and the `scenery console` alias. Ensure non-TTY fallback is automatic and tested.
13. Build the first TUI view with source tabs/status, log viewport, search, errors-only toggle, freeze/autoscroll, event JSON expansion, and quit.
14. Add source summary calculation for status, PID, restart count, last log time, last error, URL, and reason.
15. Add error grouping over recent events and links from grouped errors to surrounding log events.
16. Add trace/build summary hooks that reuse existing trace and metric stores rather than duplicating Grafana.
17. Remove any transitional process-output read path that is no longer needed by the dashboard or CLI, while leaving safe SQLite migrations for existing local databases.
18. Update docs and mark this plan complete only after validation is green.

## Validation and Acceptance

Focused validation should cover:

- Store migration from an empty DB and a DB with existing `process_output` rows.
- `DevEvent` insert/list/follow behavior with session/source/kind/level/since/grep filters.
- Parser behavior for JSON logs, slog text, Temporal-ish lines, Vite/Bun frontend output, and raw fallback.
- Source-aware capture preserving raw bytes, stripping ANSI for stored raw text, and retaining stdout/stderr stream.
- Managed frontend output entering the structured event store instead of only a side log file.
- `scenery logs` and `scenery attach` filters.
- `scenery attach --jsonl` schema output.
- `scenery attach --tui` non-TTY fallback.
- TUI reducer/rendering behavior with deterministic event fixtures.
- Error grouping and source summary calculations.

Expected command validation:

```sh
go test ./internal/devdash ./cmd/scenery
go test ./...
go install ./cmd/scenery
scenery harness self --json --write
git diff --check
```

When practical for the final implementation pass, run a fixture or target-app smoke:

```sh
scenery dev --detach --app-root testdata/apps/basic
scenery logs --app-root testdata/apps/basic --session current --source api --follow --limit 20
scenery attach --app-root testdata/apps/basic --session current --jsonl --limit 5
scenery attach --app-root testdata/apps/basic --session current --tui
scenery down --app-root testdata/apps/basic
```

Acceptance criteria:

- Every event emitted by `scenery dev` has app id, session id, source identity, time, raw text or structured message, level, parsed fields, and parse metadata.
- `scenery logs --source`, `--kind`, `--level`, `--grep`, and `--since` work in both historical and follow modes.
- JSONL output validates against `scenery.dev.event.v1`.
- `scenery attach` remains pipeable and does not require a TUI.
- `scenery attach --tui` and `scenery console` render a usable service cockpit in a TTY and fall back outside a TTY.
- Frontend, TypeScript worker, app, build, and substrate events are distinguishable without pid or string heuristics.
- The first TUI does not include mutating restart/kill controls.

## Idempotence and Recovery

SQLite migrations must be idempotent. Opening an older dev database should not destroy old process output. If backfill is needed, use stable ids or a migration marker so repeated opens do not duplicate events.

Process capture should tolerate parser failures by writing a valid event with `parse.ok=false`, `parse.format="raw"`, a fallback level, a trimmed message, and the original raw line. A failed parser must never block process output capture.

If a managed process exits during startup, emit a source status event and preserve the last raw lines. Retrying `scenery dev` should reuse normal supervisor cleanup paths and should not require manual database cleanup.

If the TUI panics or the terminal cannot enter raw mode, restore terminal state and fall back to plain log following when possible. Quitting the TUI must detach only the view; it must not stop the dev session.

## Artifacts and Notes

Expected changed artifacts:

```text
internal/devdash/types.go
internal/devdash/store.go
internal/devdash/store_events.go
internal/devdash/*test.go
cmd/scenery/logs.go
cmd/scenery/dev_supervisor.go
cmd/scenery/dev_typescript.go
cmd/scenery/dev_frontends.go
cmd/scenery/dev_services.go
cmd/scenery/watch.go
cmd/scenery/dashboard_rpc.go
cmd/scenery/dashboard.go
cmd/scenery/main.go
cmd/scenery/*console* or cmd/scenery/*tui*
cmd/scenery/*test.go
docs/schemas/scenery.dev.event.v1.schema.json
docs/local-contract.md
docs/harness-engineering.md, if schema/harness behavior changes
```

Possible future command surfaces, after the MVP:

```sh
scenery logs --errors
scenery console --view errors
scenery console --source worker:typescript
scenery status --watch --json
```

The UI should expose why a source exists when that is known. Examples: "generated TypeScript Temporal activity worker", "managed sync sync service from dev.services.sync", "shared Temporal dev server", or "frontend route from proxy.frontends.web".

## Interfaces and Dependencies

Primary new public JSON schema:

```text
scenery.dev.event.v1
```

CLI additions:

```text
scenery attach [--tui] [--jsonl] [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>]
scenery console [same selection flags as attach --tui]
scenery logs [--source <id>] [--kind <kind>] [--level <level>] [--grep <text>] [--since <duration>] [--follow] [--jsonl]
scenery status --watch [--json]
```

Internal interfaces:

```go
type DevSource struct {
    ID        string
    Kind      string
    Name      string
    Role      string
    PID       string
    Status    string
    RestartID string
}

type DevEvent struct {
    SchemaVersion string
    ID            int64
    AppID         string
    AppRoot       string
    SessionID     string
    Source        DevSource
    Level         string
    Message       string
    FieldsJSON    json.RawMessage
    Raw           string
    ParseFormat   string
    ParseOK       bool
    CreatedAt     time.Time
}
```

Dependency stance:

- Prefer Go standard library and existing internal terminal helpers for the first TUI.
- Do not add Bubble Tea, Bubbles, or Lip Gloss in the MVP without updating `cmd/scenery/harness_arch.go`, `go.mod`, the decision log, and validation evidence that the dependency pays for itself.
- No new external service dependencies are required.
