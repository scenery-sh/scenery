# Devdash App-Model Blob Store

This ExecPlan is a living document. Keep `Progress`, `Surprises & Discoveries`,
`Decision Log`, and `Outcomes & Retrospective` current as work proceeds.

## Purpose / Big Picture

Shrink the hottest measured part of `~/.scenery/agent/dashboard/devdash.json` by
stopping persisted app session records from copying full app inspection output.
ExecPlan 0076 already defines the broader end state: `devdash.json` becomes a
small single-writer control-plane store, while observability events move fully to
Victoria. This plan is the highest-leverage first implementation slice for that
larger outcome.

On 2026-06-23 the live global store was about 42 MB. A simple `jq type` parse
took 0.58 s, which is too expensive for a file that every control-plane store
touch may parse and rewrite. The largest key was `app_sessions` at about 32 MB.
Inspection showed 19 session records and repeated app-model JSON: the largest
sessions were about 1.9 MB each, with `Metadata.svcs` alone about 1.85 MB across
46 services. `apps` also contained one large app record for the same app root.
The current code makes this duplication expected: `internal/devdash/store.go`
persists both `Apps map[string]AppRecord` and `AppSessions map[string]AppRecord`,
and `Store.UpsertApp` writes a normalized full `AppRecord` into both maps.

After this plan, `app_sessions` persists only session/control-plane fields plus a
reference to app-model blobs. Full `Metadata` and `APIEncoding` live in a
content-addressed sidecar cache and are hydrated only for APIs that need the full
app model. List/status paths stay compact by default.

## Progress

- [x] 2026-06-23: Created this ExecPlan from the 2026-06-23 performance audit and Oracle review, as a focused first slice under ExecPlan 0076.
- [x] 2026-06-23: Added tests that reproduce duplicated large session metadata and prove load/save shrinks legacy fat stores below the target budget.
- [x] 2026-06-23: Split persistent app/session structs from the public `AppRecord` API shape.
- [x] 2026-06-23: Added a content-addressed app-model blob store for `Metadata` and `APIEncoding`.
- [x] 2026-06-23: Migrated legacy fat `apps` and `app_sessions` records to compact records plus blob references on load/save.
- [x] 2026-06-23: Kept full `AppRecord` behavior compatible for detail/status paths, while preventing list calls from hydrating large blobs.
- [x] 2026-06-23: Added byte-budget enforcement and diagnostics that make future store bloat visible.
- [x] 2026-06-23: Updated `docs/local-contract.md`, `docs/agent-guide.md`, `docs/knowledge.json`, and ExecPlan 0076 for the durable cache-layout behavior.

## Surprises & Discoveries

- 2026-06-23: Current live store profile: `~/.scenery/agent/dashboard/devdash.json` was 43,584,945 bytes. Top-level serialized sizes were `app_sessions` 31,997,566 bytes, `log_events` 4,165,839 bytes, `trace_events` 3,754,451 bytes, `apps` 2,054,907 bytes, `trace_summaries` 839,876 bytes, and `process_events` 761,718 bytes. `jq type` took 0.58 s.
- 2026-06-23: The file contained 19 `app_sessions`, 3 `apps`, 2,000 `trace_summaries`, 6,000 `trace_events`, 5,000 `log_events`, and 1,000 `process_events`. Count caps limited event counts but did not keep the file small.
- 2026-06-23: The largest session records were about 1.9 MB each. Their `Metadata` objects included `app_revision`, `buckets`, `cache_clusters`, `cron_jobs`, `decls`, `experiments`, `gateways`, `language`, `metrics`, `middleware`, `module_path`, `pkgs`, `sql_databases`, `svcs`, and `uncommitted_changes`. `Metadata.svcs` was the dominant field at about 1,849,430 bytes across 46 services.
- 2026-06-23: `Routes`, `Aliases`, and `Grafana` appear to be control-plane fields and were not the measured bloat source. Keep them in session records unless a later measurement proves otherwise.
- 2026-06-23: `ProcessOutput` exists in `storeState` and is count-capped, not byte-capped. It did not show up in the current top-level profile, but it is a latent sibling risk.

## Decision Log

- Decision: Allocate ExecPlan 0078 for app-model/session slimming instead of expanding 0076 further.
  Rationale: 0076 covers the broader store architecture and Victoria event cutover. The new evidence shows the first highest-leverage PR is narrower: stop duplicating app-model metadata in `app_sessions`.
  Date/Author: 2026-06-23 / Codex

- Decision: Treat persisted session records as compact control-plane records, not as `AppRecord`.
  Rationale: `AppRecord` combines session status, routing, Grafana state, full `Metadata`, and `APIEncoding`. Reusing it for persistence makes large accidental writes easy and caused the measured duplication.
  Date/Author: 2026-06-23 / Codex

- Decision: Store large app-model JSON as content-addressed sidecar blobs, referenced from compact app/session records.
  Rationale: A `sha256` reference deduplicates repeated model output across sessions and app records, keeps `devdash.json` parseable, and allows the store to hydrate only the paths that require full model detail.
  Date/Author: 2026-06-23 / Codex

## Outcomes & Retrospective

Completed on 2026-06-23.

`internal/devdash/store.go` now persists compact `StoredApp` and
`StoredAppSession` records instead of full `AppRecord` values. Full app-model
`Metadata` and `APIEncoding` values are written once per content hash under
`app-model/<metadata|api-encoding>/sha256/<hash>.json`, and `devdash.json`
stores only refs and small control-plane fields. Legacy fat stores migrate on
load/save. List paths return compact app/session records without hydrating large
blobs, while detail/status paths hydrate through the existing `AppRecord`
compatibility API.

The store now has a 2 MB soft budget, an 8 MB hard cap, byte-aware pruning for
diagnostic/event arrays, and a top-level size breakdown in the hard-cap error.
Focused tests cover repeated 2 MB session metadata, legacy fat session
migration, and pathological process output pruning. Targeted validation passed
for `go test ./internal/devdash` and `go test ./cmd/scenery`; full
`go test ./...` passed; worktree-local `.scenery/harness/bin/scenery harness self --summary --write` passed with warnings only for existing large-file and
slow-test timing diagnostics.

## Context and Orientation

Relevant files:

```text
internal/devdash/store.go       storeState, UpsertApp, app/session list/get APIs, load/save/prune
internal/devdash/types.go       AppRecord, AppStatus, GrafanaState, ProcessOutput
cmd/scenery/dashboard.go        dashboard status/report handlers that consume devdash records
cmd/scenery/devdash_store.go    global vs cache-root store selection
cmd/scenery/inspect_observability_test.go
cmd/scenery/victoria_query.go   broader 0076 event-read cutover context
docs/plans/0076-devdash-control-plane-store-slimming.md
```

Key terms:

- `devdash.json` is the local dashboard/control-plane JSON file under the
  Scenery cache or global agent dashboard directory.
- `AppRecord` is the current in-memory/API compatibility shape that includes
  full app model JSON in `Metadata` and `APIEncoding`.
- `StoredApp` and `StoredAppSession` are the new compact persistent shapes this
  plan introduces.
- An app-model blob is a sidecar JSON file that contains large app inspection
  output, addressed by content hash.

Current implementation shape in `internal/devdash/store.go`:

```go
type storeState struct {
    Apps        map[string]AppRecord `json:"apps,omitempty"`
    AppSessions map[string]AppRecord `json:"app_sessions,omitempty"`
}

func (s *Store) UpsertApp(ctx context.Context, app AppRecord) error {
    app = normalizeAppRecord(app)
    return s.withState(ctx, true, func(state *storeState) error {
        legacy := app
        legacy.RouteID = legacy.ID
        state.Apps[app.ID] = legacy
        session := app
        session.RouteID = appSessionRecordKey(app)
        state.AppSessions[session.RouteID] = session
        return nil
    })
}
```

This is the duplication mechanism. `normalizeAppRecord` also fills missing
`Metadata`, `APIEncoding`, and `Grafana` with `{}`. That is useful for API
compatibility but should not be the persistence boundary after this plan.

## Milestones

Milestone 1 is measurement-backed tests. Add internal devdash store tests that
construct 20 sessions sharing a 2 MB `Metadata` blob. The legacy/current shape
should exceed the future budget; the new shape should keep `devdash.json` small
after save. Add a legacy fixture or test that loads fat `AppRecord` session JSON
and proves the next save strips inline app-model fields.

Milestone 2 is persistent shape split. Introduce `StoredApp`,
`StoredAppSession`, and app-model reference fields. Change `storeState.Apps` and
`storeState.AppSessions` to use the compact types. Keep `AppRecord` as the
public compatibility type returned by existing APIs until callers are split.

Milestone 3 is the app-model blob store. Add a small internal helper that writes
large `Metadata` and `APIEncoding` payloads under a content-addressed cache path,
such as `app-model/sha256/<hash>.json`, and returns the hash/ref/byte count.
Reuse existing blobs when hashes match.

Milestone 4 is migration and hydration. On load/save, migrate old fat `apps` and
`app_sessions` entries by writing/reusing blobs, recording refs, and dropping
inline `Metadata` and `APIEncoding` from the persisted state. Hydrate full
`AppRecord` only for `GetApp`, `GetAppSession`, or other call sites that truly
need it; keep list calls compact unless existing behavior requires a transitional
compatibility path.

Milestone 5 is budget enforcement. Add serialized-size measurement and a
soft/hard budget, with byte-aware pruning of non-critical arrays before failure.
The initial product target is a soft 2 MB store and hard 8 MB cap for
`devdash.json`, unless implementation evidence shows a different budget is
needed. This milestone may be shared with ExecPlan 0076 if the event-array
cutover lands first.

Milestone 6 is docs and integration. Update the public/local contract docs only
where behavior changes are visible. Update ExecPlan 0076's progress/discoveries
to note that this metadata-slimming slice landed first.

## Plan of Work

Start with tests in `internal/devdash`. Add helpers that report serialized
top-level store sizes so failures explain whether `app_sessions`, `apps`, event
arrays, or process output are responsible. Build a fixture with repeated large
metadata because the current live evidence shows duplication, not unique data,
is the largest problem.

Then split persistence from compatibility. Do not remove `AppRecord` from public
method signatures in the same change. Instead, convert on write from `AppRecord`
to compact persistent records, and convert on read back to `AppRecord` where
callers still expect it. Avoid calling `normalizeAppRecord` before persistence;
normalization belongs at API boundaries.

Add the blob store with a simple file layout under the same cache root that owns
`devdash.json`. Hash raw bytes as stored. A first version does not need semantic
JSON canonicalization, because deduplication across repeated sessions uses the
same bytes from the same inspection output. Prefer one app-model blob containing
both `Metadata` and `APIEncoding` unless code review proves callers often need
one without the other.

Implement migration in the load/save path, not as a one-off CLI. Loading an old
fat file should be safe and the next save should shrink it. Because this is a
local dev cache/control-plane file, prefer forward migration and safe recovery
over preserving old fat shape for older binaries. If a downgrade sees missing
metadata, deleting the cache or re-inspecting the app should recover.

Only after the store shape is compact should the byte budget become strict.
Budget enforcement should first prune non-critical arrays such as
`process_output`, `dev_events`, and `process_events`. It should never silently
drop `apps`, `app_sessions`, onboarding, or stored requests. If the file remains
over the hard cap, fail loudly with a size breakdown.

## Concrete Steps

Run from the repo root:

```sh
go test ./internal/devdash ./cmd/scenery
```

Add or update tests before the production changes:

```text
internal/devdash: TestStoreBudgetRejectsLargeSessions
internal/devdash: TestLegacyFatSessionMetadataMigratesToRefs
internal/devdash: TestStoreStateSerializedSizeUnderBudget
```

Implement the new persistent types near `storeState` or in a small sibling file:

```go
type StoredApp struct {
    ID           string    `json:"id"`
    Name         string    `json:"name,omitempty"`
    Root         string    `json:"root,omitempty"`
    BaseAppID    string    `json:"base_app_id,omitempty"`
    RuntimeAppID string    `json:"runtime_app_id,omitempty"`
    MetadataRef  string    `json:"metadata_ref,omitempty"`
    MetadataHash string    `json:"metadata_hash,omitempty"`
    AppRevision  string    `json:"app_revision,omitempty"`
    UpdatedAt    time.Time `json:"updated_at,omitempty"`
}

type StoredAppSession struct {
    RouteID      string            `json:"route_id,omitempty"`
    ID           string            `json:"id"`
    BaseAppID    string            `json:"base_app_id,omitempty"`
    RuntimeAppID string            `json:"runtime_app_id,omitempty"`
    SessionID    string            `json:"session_id,omitempty"`
    Name         string            `json:"name,omitempty"`
    Root         string            `json:"root,omitempty"`
    ListenAddr   string            `json:"listen_addr,omitempty"`
    PID          string            `json:"pid,omitempty"`
    Routes       map[string]string `json:"routes,omitempty"`
    Aliases      map[string]string `json:"aliases,omitempty"`
    Grafana      json.RawMessage   `json:"grafana,omitempty"`
    Running      bool              `json:"running,omitempty"`
    Compiling    bool              `json:"compiling,omitempty"`
    CompileError string            `json:"compile_error,omitempty"`
    Offline      bool              `json:"offline,omitempty"`
    UpdatedAt    time.Time         `json:"updated_at,omitempty"`
    MetadataRef  string            `json:"metadata_ref,omitempty"`
    MetadataHash string            `json:"metadata_hash,omitempty"`
    AppRevision  string            `json:"app_revision,omitempty"`
}
```

Add a blob reference shape:

```go
type StoredAppModelRef struct {
    Ref         string    `json:"ref"`
    Hash        string    `json:"hash"`
    AppID       string    `json:"app_id"`
    Root        string    `json:"root,omitempty"`
    AppRevision string    `json:"app_revision,omitempty"`
    Path        string    `json:"path,omitempty"`
    Bytes       int64     `json:"bytes,omitempty"`
    UpdatedAt   time.Time `json:"updated_at,omitempty"`
}
```

Change `Store.UpsertApp` so it splits the incoming `AppRecord`, writes the
app-model blob, stores one compact app record, and stores one compact session
record keyed by `appSessionRecordKey(app)`.

Audit and update:

```text
normalizeStoreState
pruneStoreState
ListApps
ListAppSessions
GetApp
GetAppSession
GetAppForSession
cmd/scenery/dashboard.go callers
cmd/scenery/inspect_observability_test.go
```

Run validation after implementation:

```sh
go test ./internal/devdash ./cmd/scenery
go test ./...
scenery harness self --summary --write
```

Do not run `go install ./cmd/scenery` unless the maintainer explicitly asks.

## Validation and Acceptance

- A fixture with 20 sessions sharing a 2 MB app-model payload persists a
  `devdash.json` file below the selected hard budget.
- A legacy store containing duplicated `AppRecord.Metadata` and `APIEncoding`
  in `app_sessions` shrinks after load/save and records stable blob references.
- `ListApps` and `ListAppSessions` do not load large app-model blobs unless a
  compatibility requirement is explicitly documented in code and tests.
- `GetApp`, `GetAppSession`, or replacement detail APIs still return the full
  app model for dashboard/status paths that require it.
- Existing CLI/dashboard behavior remains compatible: `scenery ps --json`,
  `scenery inspect observability --json`, dashboard app/session status, and
  route control continue to work.
- The store budget reports a top-level size breakdown when it prunes or fails.
- `go test ./...` and `scenery harness self --summary --write` pass.

## Idempotence and Recovery

All migrations must be safe to rerun. Writing the same app-model bytes should
produce the same hash and reuse the same blob path. If a save fails after a blob
is written but before `devdash.json` is updated, the orphaned blob is harmless
and can be garbage-collected later. If `devdash.json` references a missing blob,
the detail read should return a clear error and the next app inspection/upsert
should rewrite the blob.

The loader should tolerate old stores with inline `Metadata`/`APIEncoding`, new
stores with refs only, and partially migrated stores. Unknown fields remain safe
because JSON unmarshalling ignores them, but known legacy large fields must be
actively stripped from the persisted compact records.

If the change causes dashboard regressions, revert the hydration behavior at API
boundaries while keeping the compact persisted shape and byte budget in place.
Do not reintroduce full `AppRecord` persistence for sessions as the fallback.

## Artifacts and Notes

The 2026-06-23 audit was performed against the live global store:

```text
~/.scenery/agent/dashboard/devdash.json
size: 43,584,945 bytes
parse probe: jq type => 0.58 s
top-level sizes:
  app_sessions     31,997,566 bytes
  log_events        4,165,839 bytes
  trace_events      3,754,451 bytes
  apps              2,054,907 bytes
  trace_summaries     839,876 bytes
  process_events      761,718 bytes
counts:
  app_sessions 19
  apps 3
  trace_summaries 2000
  trace_events 6000
  log_events 5000
  process_events 1000
```

The Oracle review agreed with the local hypothesis and sharpened the priority:
fix `app_sessions` metadata duplication first, then continue 0076's event-array
cutover to Victoria. The metadata/session slice landed on 2026-06-23; 0076
remains active for the Victoria-only trace/log cutover and single-writer store
ownership.

## Interfaces and Dependencies

This plan changes the internal persisted shape of `internal/devdash.storeState`.
It should not change the external `/__scenery/report` envelope, CLI JSON schemas,
or dashboard route contracts in the first implementation PR. Any visible CLI or
dashboard API shape change must update `docs/local-contract.md`, relevant tests,
and schemas in the same change.

This plan depends on ExecPlan 0076 for the broader Victoria event-store cutover
and single-writer ownership. It may share the byte-budget helper and self-harness
diagnostic with 0076. It does not require deleting `trace_summaries`,
`trace_events`, or `log_events` before landing the app-model slimming PR.
