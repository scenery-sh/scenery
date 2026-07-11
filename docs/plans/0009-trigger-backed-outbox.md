# Trigger-Backed Outbox

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The first data-platform slice writes outbox events from the explicit Go mutation path. That is correct for app-driven changes, but it leaves a known local-development gap: direct SQL edits to physical dynamic object tables do not emit outbox rows, so SSE live updates and replay miss those changes.

This plan adds optional trigger-backed outbox support after validation, inspectability, migration hardening, and live-data hardening are complete.

Target flow:

```text
direct SQL / maintenance script
        |
        v
INSERT / UPDATE / DELETE on physical object table
        |
        v
PostgreSQL trigger
        |
        v
scenery_data.outbox_events
        |
        v
existing replay + live SSE routing
```

The trigger path should be compatible with existing event payloads. It may produce less precise actor information than explicit mutations. Explicit mutation path remains the primary path and should keep writing precise outbox rows.

## Progress

- [x] (2026-05-08 21:08Z) Created this ExecPlan and assigned historical ID 0009.
- [x] (2026-05-08 21:08Z) Marked this as later work after `0007-data-platform-validation-and-inspect.md` and `0008-data-platform-migration-and-live-hardening.md`.
- [x] (2026-05-08 22:55Z) Design trigger function and metadata toggles.
- [x] (2026-05-08 22:55Z) Implement optional per-object triggers.
- [x] (2026-05-08 22:55Z) Add transaction-local actor context for explicit mutation path.
- [x] (2026-05-08 22:55Z) Validate direct SQL changes produce outbox rows.
- [x] (2026-05-08 22:55Z) Preserve existing SSE replay compatibility.

## Surprises & Discoveries

- Trigger-created events need logical field values, not physical columns, otherwise existing query-aware live matching cannot work. The trigger now reconstructs logical records from `scenery_data.fields.storage_columns`.
- SSE originally only received in-process router publishes after the initial replay. Trigger-written rows happen outside Go, so SSE streams now poll the PostgreSQL outbox sequence while connected.

## Decision Log

- Decision: Trigger-backed outbox is later work, not part of the immediate hardening plans.
  Rationale: The migration and live-data foundations should be proven first. Trigger-backed outbox touches DDL generation, event semantics, actor context, and direct SQL behavior.
  Date/Author: 2026-05-08 / Codex

- Decision: Make trigger-backed outbox optional per object at first.
  Rationale: Explicit mutation path already writes precise events. Optional triggers let scenery close the direct SQL gap without forcing every object into trigger overhead before the design is proven.
  Date/Author: 2026-05-08 / Codex

- Decision: Use transaction-local PostgreSQL settings for actor context when available.
  Rationale: Triggers cannot see Go request state directly. `SET LOCAL scenery.actor_id = '...'` lets explicit mutations provide actor context while direct  edits can fall back to anonymous/system actor.
  Date/Author: 2026-05-08 / Codex

- Decision: Use duplicate-event strategy Option B.
  Rationale: Explicit scenery mutations still write precise outbox rows. They set `scenery.outbox_explicit=true` inside the transaction, and record-table triggers skip those transactions. Direct SQL/ changes do not set that flag, so triggers write generic logical events without duplicate delivery.
  Date/Author: 2026-05-08 / Codex

- Decision: Reconstruct logical field values inside the trigger from metadata.
  Rationale: Live query filters and selected-field stripping operate on logical field names such as `stage` and `full_name`, not physical names such as `stage__abc123`. Trigger-backed events must therefore translate physical rows back into logical records before inserting outbox rows.
  Date/Author: 2026-05-08 / Codex

- Decision: Poll the outbox from open SSE streams.
  Rationale: PostgreSQL triggers cannot publish through the in-process `LiveRouter`. Polling by monotonically increasing `seq` keeps direct SQL changes compatible with existing SSE connections and reconnect/replay semantics without adding Redis, NATS, or LISTEN/NOTIFY in this pass.
  Date/Author: 2026-05-08 / Codex

## Outcomes & Retrospective

Completed 2026-05-08.

Shipped:

- `outbox_triggers_enabled` metadata on objects.
- Shared PostgreSQL trigger function `scenery_data.record_change_trigger()`.
- Per-object trigger installation through `Store.EnableOutboxTriggers` and `data.Store.EnableOutboxTriggers`.
- Transaction-local `scenery.actor_id` and `scenery.outbox_explicit` settings for explicit mutation transactions.
- Direct SQL insert/update/delete trigger coverage that writes to the existing `scenery_data.outbox_events` table.
- Logical trigger event payloads reconstructed from metadata, including composite fields.
- SSE polling for trigger-created outbox rows while preserving replay through `after_seq` and `Last-Event-ID`.
- Inspect output showing trigger enablement and physical trigger presence.
- Fixture endpoint and README walkthrough for enabling outbox triggers.

Retrospective:

The tricky part was not the trigger DDL itself; it was keeping trigger-created events compatible with the existing logical query matcher. Reusing the same outbox table worked well once the trigger translated physical rows back into logical records. The current implementation intentionally uses lightweight SSE polling rather than adding a database notification channel; if polling becomes noisy, LISTEN/NOTIFY can be added later without changing the outbox event shape.

## Context and Orientation

This plan depends on the data-platform foundation:

- `0005-scenery-data-platform.md`: completed first vertical slice.
- `0007-data-platform-validation-and-inspect.md`: PostgreSQL CI and `scenery inspect data`.
- `0008-data-platform-migration-and-live-hardening.md`: migration/live correctness.

Relevant files after those plans:

- `internal/objectstore/migrate.go`: object/field DDL and schema verification.
- `internal/objectstore/outbox.go`: explicit outbox writes.
- `internal/objectstore/mutate.go`: transaction boundaries and actor context.
- `internal/objectstore/live.go`: event matching and routing.
- `internal/objectstore/sse.go`: replay and SSE delivery.
- `internal/objectstore/objectstore_integration_test.go`: PostgreSQL tests.
- `internal/datainspect` or equivalent inspect package from `0007`.

Trigger-backed outbox should use the same `scenery_data.outbox_events` table and existing live routing. It should not introduce Redis, NATS, Kafka, or an external broker.

Proposed trigger function:

```sql
scenery_data.record_change_trigger()
```

Each physical object table can get a trigger like:

```sql
CREATE TRIGGER scenery_data_outbox_company
AFTER INSERT OR UPDATE OR DELETE ON scenery_data_records.company__...
FOR EACH ROW EXECUTE FUNCTION scenery_data.record_change_trigger();
```

The trigger should read `TG_OP`, `OLD`, `NEW`, and trigger arguments that identify tenant/object metadata. It should write generic `before` and `after` JSON using `row_to_json`.

## Milestones

Milestone 1: Trigger design and metadata.

Decide how an object records trigger-backed outbox status. Options include a boolean on `scenery_data.objects`, a separate settings table, or migration history only. Add inspect output showing whether triggers are enabled and physically present.

Milestone 2: Trigger function migration.

Add deterministic DDL for the shared trigger function under `scenery_data`. Record it in migration history or a separate bootstrap path. Verify function existence through PostgreSQL catalogs.

Milestone 3: Per-object trigger migration.

Add API or internal option to enable triggers for an object. Create, verify, and drop/recreate triggers safely through the migration layer.

Milestone 4: Actor context.

In explicit mutations, set transaction-local context such as `SET LOCAL scenery.actor_id = $1` before DML. Ensure explicit mutation path does not double-write events when triggers are enabled, or decide and test a deduplication strategy.

Milestone 5: Direct SQL integration tests.

Use real PostgreSQL tests to insert, update, and delete rows directly from physical tables. Verify outbox rows are produced, replay works, and SSE routing remains compatible.

## Plan of Work

Start with design. The biggest risk is duplicate outbox rows when explicit mutation path and triggers both exist. Pick one strategy:

```text
Option A: When triggers are enabled, explicit mutation path relies on triggers for record events.
Option B: Explicit mutation path still writes outbox; triggers skip when a transaction-local flag says explicit path already handled it.
Option C: Triggers always write generic events and explicit path writes precise events, with deduplication in live routing.
```

Prefer Option B if it keeps explicit mutation precision and avoids duplicate delivery. Use transaction-local variables:

```sql
SET LOCAL scenery.actor_id = '...';
SET LOCAL scenery.outbox_explicit = 'true';
```

Then implement the shared function and per-object triggers through existing migration primitives. The trigger function must be deterministic and idempotent. It should not depend on app code being present.

Finally test direct SQL changes. Tests need to discover the physical table from metadata, perform raw SQL insert/update/delete, read outbox events after each operation, and optionally route through the existing live matcher.

## Concrete Steps

1. Read outcomes from `0007` and `0008`; do not start if DB-backed CI and migration/live hardening are incomplete.
2. Add a design note to this plan selecting the duplicate-event strategy.
3. Add metadata fields or settings for trigger-backed outbox enablement.
4. Implement shared trigger function DDL and catalog verification.
5. Implement per-object trigger DDL generation, creation, verification, and migration records.
6. Add transaction-local actor context in explicit mutation transactions.
7. Add direct SQL integration tests for insert, update, and delete.
8. Add tests for anonymous/system actor when direct SQL does not set actor context.
9. Add tests showing existing SSE replay/live routing consumes trigger-written events.
10. Add inspect output showing trigger enablement and physical trigger status.
11. Update docs and run final validation.

## Validation and Acceptance

Required validation:

```sh
go test ./...
go test ./internal/objectstore -count=1
go run ./cmd/scenery check --app-root testdata/apps/data-platform --json
go install ./cmd/scenery
scenery harness self --json --write
```

Acceptance:

- Trigger-backed outbox can be enabled per object.
- Shared trigger function and per-object triggers are created idempotently.
- Direct SQL insert/update/delete on a physical object table writes outbox events.
- Explicit mutation path does not double-deliver events.
- Actor context is recorded for explicit mutations through transaction-local settings.
- Direct SQL/ changes use anonymous/system actor when no actor context is set.
- Existing SSE replay and live matching work with trigger-created events.
- Inspect output shows trigger configuration and physical trigger presence.

## Idempotence and Recovery

Trigger DDL must be idempotent. Use deterministic trigger names and verify PostgreSQL catalogs after applying DDL.

If a trigger migration fails, write a failed migration status and leave object metadata in a state that allows retry. Do not partially mark triggers enabled without verifying the physical trigger exists.

If duplicate outbox rows are detected during tests, pause and update the Decision Log with the chosen deduplication or skip strategy before modifying event delivery.

Direct SQL tests should use unique tenants and objects and should not drop shared schemas. Cleanup only test-owned objects.

## Artifacts and Notes

Expected changed files:

- `internal/objectstore/migrate.go`
- `internal/objectstore/outbox.go`
- `internal/objectstore/mutate.go`
- `internal/objectstore/metadata.go`
- `internal/objectstore/objectstore_integration_test.go`
- data inspect output/tests from `0007`
- `docs/local-contract.md`
- `testdata/apps/data-platform/README.md`

Do not add:

- Dashboard UI
- Reporting
- Dynamic GraphQL
- External broker
- New product CRM surface

## Interfaces and Dependencies

Potential public API shape, only if needed:

```go
store.EnableOutboxTriggers(ctx, actor, "company")
store.DisableOutboxTriggers(ctx, actor, "company")
```

If the API is not ready to expose publicly, keep trigger enablement internal or fixture-only and document it as beta. Inspect output should still show trigger status once implemented.

No new non-PostgreSQL dependencies should be needed.
