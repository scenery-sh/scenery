# Devdash Control-Plane Store Slimming

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

Make `devdash.json` what ExecPlan 0056 declared it to be: a small local control-plane store for apps, sessions, saved dashboard requests, and onboarding state. Today it is still the hot ingest path for trace summaries, trace events, report log events, and process events, written by two processes through a full-file read/marshal cycle under one mutex. On 2026-06-12 this design produced a 422 MB store file: every store touch took ~5 s, dev report POSTs (2 s client timeout) failed continuously, the `scenery console` TUI froze, and `scenery up` reached ~5 GB RSS. A payload-size cap on process events was hot-fixed the same day, but the architecture that allowed the incident is unchanged.

After this plan, observability event data (traces, report logs) lives only in the Victoria substrate that already receives it, `devdash.json` has an enforced byte budget, and exactly one process owns writes to it.

## Progress

- [x] 2026-06-23: Inventory all readers of trace summaries, trace events, and report log events served from the devdash store (dashboard RPCs, `scenery traces`, inspect surfaces) and map each to an existing or new Victoria query.
- [x] 2026-06-23: Cut dashboard/CLI trace reads over to VictoriaTraces; delete `TraceSummaries`, `TraceEvents`, and `LogEvents` from `storeState` and the report-ingest writes in `cmd/scenery/dashboard.go`.
- [x] 2026-06-23: Make the report ingest path enqueue-only: `handleReport` must not hold the store mutex; Victoria export becomes the single sink.
- [x] 2026-06-23: Give `devdash.json` a single writer (the agent dashboard process); route agent-backed `scenery up` app/session/process writes through the agent dashboard control-plane endpoint instead of opening the shared store directly.
- [x] 2026-06-23: Add a store byte budget (soft prune target plus hard cap) and a self-harness check that fails when `devdash.json` exceeds it.
- [x] 2026-06-23: Update `docs/local-contract.md`, `docs/agent-guide.md`, and `docs/knowledge.json`; add a drift note to ExecPlan 0056 pointing here.

## Surprises & Discoveries

- 2026-06-12: `~/.scenery/agent/dashboard/devdash.json` reached 422 MB. Profiling showed 465 `process_events` items at ~1.8 MB each: every `process/reload` persisted the full `appStatus()` including `Meta` and `APIEncoding`. Prune caps were item counts, never bytes. Evidence: `scenery logs --limit 1` took 5.4 s wall clock; after compaction to 15 MB it took 0.21 s.
- 2026-06-12: `app_sessions` records each carry the same ~1.7 MB `Metadata` blob (6 records ≈ 10 MB), so even the "small" residue is dominated by duplicated app metadata.
- 2026-06-12: `handleReport` writes every trace/log report into the JSON store *and* exports to Victoria — the data is duplicated, and the JSON copy is what dashboard reads still depend on, so it cannot simply be dropped without the read cutover.
- 2026-06-23: ExecPlan 0078 landed the first store-slimming slice: `apps` and `app_sessions` persist compact records with content-addressed app-model blob refs, and `devdash.json` has a 2 MB soft budget plus 8 MB hard cap. That slice initially carried migration shims for old fat session records. Evidence: `go test ./internal/devdash` and `go test ./cmd/scenery` passed.
- 2026-06-23: The observability cutover is now implemented: `handleReport` no longer writes trace summaries, trace events, or report logs to the store; dashboard trace RPCs and CLI trace/metric inspect paths use Victoria; legacy observability arrays are dropped on the next store save. Evidence: `go test ./internal/devdash ./cmd/scenery` passed.
- 2026-07-09: The migration shims for old fat app/session JSON records were removed; `devdash.json` now accepts only current compact `StoredApp` and `StoredAppSession` records.
- 2026-06-23: Single-writer ownership is now implemented for the hot control-plane writer path. Agent-backed `scenery up` no longer opens the global dashboard store directly; it posts app/session and process-event mutations to the agent dashboard backend over an authenticated internal control-plane endpoint. Agent-disabled and explicit local-proxy fallback paths still use the in-process local store. Evidence: `go test ./cmd/scenery ./internal/devdash` passed.

## Decision Log

- Decision: Allocate ID 0076 and treat this as a follow-up that records drift from ExecPlan 0056's "small JSON metadata store" outcome rather than rewriting 0056.
  Rationale: AGENTS.md requires recording doc/implementation drift in an ExecPlan instead of silently editing historical plans.
  Date/Author: 2026-06-12 / Claude

- Decision: Victoria substrates are the only event-data store; `devdash.json` keeps no observability event history.
  Rationale: 0056 already made VictoriaLogs the dev-event read substrate; keeping a parallel JSON copy split reads from writes and caused the 422 MB incident.
  Date/Author: 2026-06-12 / Claude

## Outcomes & Retrospective

Completed on 2026-06-23. `devdash.json` is now a compact, bounded control-plane store: large app-model blobs live in content-addressed sidecars, trace/report-log history is Victoria-only, legacy observability arrays are dropped on save, and agent-backed dev supervisors route store mutations through the agent-owned dashboard process. The remaining risk is live soak coverage under the original 2026-06-12 rebuild-storm workload; the implementation-level tests and self-harness cover migration, byte budget, Victoria read paths, and authenticated control-plane writes.

## Context and Orientation

Relevant files:

```text
cmd/scenery/dashboard.go          report ingest (handleReport), trace notifications, Victoria export
cmd/scenery/dev_supervisor.go     process event writes (compactAppStatus), report URL/token wiring
cmd/scenery/devdash_store.go      store root selection (agent dir vs SCENERY_DEV_CACHE_DIR)
cmd/scenery/victoria_query.go     existing Victoria read path for dev events
internal/devdash/store.go         storeState, withStatePersist, stamp reload, prune caps
internal/devdash/types.go         ReportEnvelope, TraceSummary, TraceEvent, LogEvent
runtime/devreport.go              app-side reporter (2 s POST timeout, backoff)
```

Key terms: the "agent dashboard" is the long-lived `scenery system agent` dashboard server; "report ingest" is the POST `/__scenery/report` handler that supervised app processes call; "stamp reload" is `refreshForExternalChangeLocked`, which re-reads and re-parses the whole store file whenever its size/mtime changes because another process flushed it.

Related history: ExecPlan 0048 (devdash storage hardening items), ExecPlan 0056 (dev event backend cutover; declared the store metadata-only), memory of the 2026-06-12 incident in the hot-fix commit "Stop devdash store bloat and quiet scenery up setup output".

## Milestones

1. Trace and report-log reads served from Victoria with parity coverage; JSON store no longer written by report ingest.
2. Event fields removed from `storeState`; one-time load tolerates and drops legacy fields.
3. Single-writer ownership and byte budget enforced, with a self-harness check.
4. Docs and knowledge index updated; 0056 drift note added.

## Plan of Work

First cut reads over: enumerate every consumer of `queryTraceSummaries`, `GetTraceEvents*`, and stored `LogEvents`, and back each with VictoriaTraces/VictoriaLogs queries through the existing `victoria_query.go` machinery. Add parity tests that ingest a fixture report stream and assert CLI/dashboard reads return equivalent results from Victoria.

Then remove the JSON event writes: `handleReport` keeps authorization and notification fan-out but only exports to Victoria; delete the three event slices from `storeState` (loading tolerates unknown fields, so old files shrink on first save). Process events stay (they are small after the 64 KB cap) unless inventory shows no reader, in which case delete them too.

Then ownership and budget: only the agent dashboard process opens the store read-write; `scenery up` and CLI commands use the agent API (or read-only snapshots) instead of sharing the file. Add `maxStoreFileBytes` with prune-to-target on save and a self-harness diagnostic that stats the store file.

## Concrete Steps

Run from the repo root:

    go test ./internal/devdash ./cmd/scenery
    scenery harness self --summary --write

For live verification, run a fixture app under `scenery up`, generate traffic, and confirm:

    ls -lh ~/.scenery/agent/dashboard/devdash.json   # stays under budget
    scenery traces list --json                        # served from Victoria
    scenery logs --limit 1                            # returns in well under 1 s

## Validation and Acceptance

- `devdash.json` stays under the byte budget during a sustained rebuild loop (the 2026-06-12 reproduction: an app with ~300 endpoints rebuilding every 1–3 minutes for an hour).
- Dev report POSTs do not time out during rebuild storms; no `scenery: dev report failed` lines.
- `scenery traces list --json` and the dashboard traces page return the same data before and after the cutover for a recorded fixture stream.
- Self-harness fails if the store file exceeds the hard cap.
- `go test ./...` passes.

## Idempotence and Recovery

All steps are re-runnable. The store loader already tolerates unknown/missing fields, so downgrading or re-running against an old fat file is safe: the next save drops removed fields and prunes to budget. If the Victoria read cutover regresses, the ingest path can temporarily re-enable JSON event writes behind a single code-level switch while keeping the byte budget as the backstop.

## Artifacts and Notes

The 2026-06-12 incident profile (top-level key sizes of the 422 MB file) and the hop-by-hop measurements are recorded in the hot-fix commit message and in the maintainer's session notes. Reproduce the profile with a small script that loads `devdash.json` and prints `len(json.dumps(v))` per top-level key.

## Interfaces and Dependencies

Depends on the managed Victoria substrates (VictoriaLogs, VictoriaTraces) that `scenery up` already starts, and on the agent control API for store access by non-owner processes. Changes the internal shape of `storeState` (removing event slices) and the report ingest contract internally; the external `/__scenery/report` envelope schema is unchanged. CLI JSON output shapes for `scenery traces` and `scenery logs` must remain stable per `docs/local-contract.md`.
