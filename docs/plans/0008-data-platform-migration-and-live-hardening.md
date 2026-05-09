# Data Platform Migration and Live Hardening

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

After PostgreSQL validation and inspectability are in place, the next data-platform priority is correctness under stress. The risky areas are schema migration, physical name derivation, concurrent DDL, drift detection, and live-sync matching. These are foundation concerns; product UI, reporting, dynamic GraphQL, workflow automation, and extra CRM features should wait.

This plan hardens the existing vertical slice:

```text
metadata-defined objects/fields
        |
        v
deterministic physical names + advisory locks + migration history
        |
        v
real PostgreSQL DDL and schema verification
        |
        v
record mutations + transactional outbox
        |
        v
live query matching using before/after/delete semantics
```

Success means the migration layer fails clearly and recovers predictably, live updates handle list-view edge cases, and the public `github.com/pbrazdil/onlava/data` API is cleaned up before accidental beta shapes become hard to change.

## Progress

- [x] (2026-05-08 21:08Z) Created this ExecPlan and assigned historical ID 0008.
- [x] (2026-05-08 21:08Z) Scoped this plan as the second data-platform hardening follow-up after `0007-data-platform-validation-and-inspect.md`.
- [x] (2026-05-08 22:14Z) Finalize and test physical table/column name derivation.
- [x] (2026-05-08 22:25Z) Add migration idempotence, failure, retry, concurrency, drift, and malicious-name tests.
- [x] (2026-05-08 22:14Z) Harden live event matching for created/updated/deleted before/after semantics.
- [x] (2026-05-08 22:25Z) Add permission, selected-field, reconnect, heartbeat, fanout, cleanup, and slow-client live tests.
- [x] (2026-05-08 22:25Z) Add first public `data` query/filter/sort helpers.
- [x] (2026-05-08 22:25Z) Wrap the public `data.Store` so app code no longer aliases `internal/objectstore.Store` directly.
- [x] (2026-05-08 22:33Z) Finish public `data` API cleanup before it hardens accidentally.
- [x] (2026-05-08 22:33Z) Validate and update Outcomes & Retrospective.

## Surprises & Discoveries

- `0007` local PostgreSQL validation exposed that subscribing from seq 0 replays metadata outbox rows before `ready`. The vertical-slice test now subscribes from the record query watermark when it wants live-only behavior.
- Root-level `go test ./testdata/...` does not cross into fixture app modules. Use `onlava check --app-root testdata/apps/data-platform --json` for fixture validation from the repository root.
- Repeated create semantics need to verify physical schema before returning existing metadata. Otherwise idempotent retries can hide DB Studio/manual schema drift.
- SSE heartbeat is now covered by lowering the package heartbeat interval inside the integration test; production still uses the 25 second default.

## Decision Log

- Decision: Do not add more field types or product features until migration and live-sync correctness are hardened.
  Rationale: More product surface would hide foundation bugs. The current high-leverage work is correctness under concurrency, retries, drift, and live query movement.
  Date/Author: 2026-05-08 / Codex

- Decision: Live event matching must use action-specific before/after rules.
  Rationale: List views need inserts when a record moves into a query and removals when a record moves out. Matching only `after` or only `before` is wrong.
  Date/Author: 2026-05-08 / Codex

- Decision: Physical names must include a stable suffix or hash, not raw user names alone.
  Rationale: Raw names collide, exceed PostgreSQL identifier length limits, and break on renames. Names should be readable enough for DB Studio but guaranteed stable and collision-resistant.
  Date/Author: 2026-05-08 / Codex

- Decision: Public `data` API ergonomics belong in this hardening window.
  Rationale: The first slice exposed aliases close to `internal/objectstore`. That was acceptable for a vertical slice, but now is the right time to simplify the app-facing surface before users depend on awkward internals.
  Date/Author: 2026-05-08 / Codex

- Decision: Use readable physical identifiers with stable short-ID suffixes.
  Rationale: Object tables now use `<object_name>__<object_short_id>` and field columns use `<field_name[_part]>__<field_short_id>`. This keeps DB Studio readable while preventing collisions, preserving suffixes under PostgreSQL's 63-byte identifier limit, and avoiding raw user names as the only physical identity.
  Date/Author: 2026-05-08 / Codex

- Decision: Treat repeated object/field creation as idempotent only when the requested shape matches existing metadata and physical schema verification passes.
  Rationale: Retry-safe migrations are valuable, but silently accepting incompatible metadata or drift would make later failures harder to debug. The mutation layer now returns existing metadata for matching repeats, rejects incompatible type/shape changes, and reports physical drift if expected tables or columns are missing.
  Date/Author: 2026-05-08 / Codex

- Decision: Start public `data` ergonomics with additive query/filter/sort helpers.
  Rationale: Helpers such as `data.EQ`, `data.GTE`, `data.And`, and `data.Desc` make user code app-facing without forcing a large wrapper refactor while migration/live correctness is still being hardened.
  Date/Author: 2026-05-08 / Codex

- Decision: Make `data.Store` an app-facing wrapper over `internal/objectstore.Store`.
  Rationale: The public package should not make the internal store type itself part of the API. The wrapper preserves the current method surface for fixture code while keeping implementation ownership inside `internal/objectstore`.
  Date/Author: 2026-05-08 / Codex

## Outcomes & Retrospective

Completed 2026-05-08.

Shipped:

- Deterministic readable physical names with stable short-ID suffixes for object tables and field columns.
- Retry-safe object and field creation when requested metadata matches and physical schema verification passes.
- Clear failures for incompatible repeated creates and detected physical schema drift.
- PostgreSQL-backed tests for repeated creates, concurrent creates, failed DDL recording, retry-after-failure, and drift detection.
- Live-router tests for before/after update matching, selected field stripping, permission row filters, reconnect replay, heartbeats, unsubscribe cleanup, and slow subscriber behavior.
- Public `data.Store` wrapper plus app-facing query/filter/sort helpers such as `data.EQ`, `data.GTE`, `data.And`, `data.Or`, `data.Not`, `data.Asc`, and `data.Desc`.

Deferred:

- Destructive field deletion remains unsupported instead of being implemented as DDL in this plan.
- Trigger-backed outbox remains in `0009-trigger-backed-outbox.md`; direct SQL/DB Studio edits still need that plan to emit outbox rows.

Retrospective:

The main lesson was that "idempotent" migration APIs must still verify PostgreSQL state. Returning existing metadata without checking the physical table or columns would have hidden drift introduced by manual DB Studio edits. The public `data` package now has a clearer facade, but it should stay intentionally small until trigger-backed outbox and index metadata settle.

## Context and Orientation

This plan depends on `0007-data-platform-validation-and-inspect.md` because real PostgreSQL CI and inspectability make migration/live hardening much safer. If `0007` is not complete, start there unless the user explicitly reprioritizes.

Relevant files:

- `internal/objectstore/ident.go`: identifier validation, safe names, quoting.
- `internal/objectstore/fields.go`: field type mapping and composite expansion.
- `internal/objectstore/metadata.go`: metadata bootstrap and metadata reads.
- `internal/objectstore/migrate.go`: object and field DDL, advisory locks, schema verification, migration history.
- `internal/objectstore/mutate.go`: record mutations and outbox writes.
- `internal/objectstore/query.go`: filter compiler and record matching helpers.
- `internal/objectstore/live.go`: subscriptions and event matching.
- `internal/objectstore/sse.go`: SSE replay, heartbeats, and disconnect handling.
- `internal/objectstore/objectstore_test.go`: focused unit tests.
- `internal/objectstore/objectstore_integration_test.go`: PostgreSQL integration tests.
- `data/data.go`: public package facade.
- `testdata/apps/data-platform`: fixture app.

Key correctness rule for event matching:

```text
created:  match after
updated:  match before OR after
deleted:  match before
```

The event payload can contain enough information for clients to decide whether to insert, patch, remove, or refetch. The server's job is to deliver relevant events to every query that could be affected.

Physical name goals:

```text
- no collisions
- stable after label rename
- max PostgreSQL identifier length compliance
- readable enough in DB Studio
- deterministic reconstruction from metadata
- safe with reserved words, quotes, semicolons, comments, unicode, and long names
```

One acceptable direction is:

```text
object table:
  <safe_object_name>__<short_object_id>

field column:
  <safe_field_name>__<short_field_id>
```

If tenant-level uniqueness is not guaranteed by schema/table placement, include a tenant hash in table names:

```text
t_<tenant_short_hash>__<safe_object_name>__<object_short_id>
```

## Milestones

Milestone 1: Physical naming contract.

Finalize table and column name derivation. Add tests for max length, collisions, reserved words, malicious names, rename stability, and deterministic reconstruction from metadata.

Milestone 2: Migration hardening.

Add tests for repeated bootstrap, repeated object creation, repeated field creation, concurrent object creation for the same tenant, concurrent field creation for the same object, failed DDL status, retry after failure, drift detection, lossy type changes, and conservative field deletion behavior.

Milestone 3: Live-sync hardening.

Add tests for matching update delivery, non-matching update suppression, reconnect via `after_seq`, reconnect via `Last-Event-ID`, multiple clients with different queries, selected-field stripping, permission row filters, permission changes, record movement into/out of queries, delete delivery to previously matching queries, heartbeat delivery, and slow/disconnected client cleanup.

Milestone 4: Public `data` API cleanup.

Shape `github.com/pbrazdil/onlava/data` around app-facing concepts and hide internals. Keep physical names inspectable but not primary API. Add helpers for common filter construction and sort direction if they improve readability.

Milestone 5: Fixture walkthrough and final validation.

Ensure `testdata/apps/data-platform/README.md` from `0007` stays accurate after API changes. Run full validation and update docs if public API changes.

## Plan of Work

Start with physical naming. Changing naming after more migration tests exist will create churn. Pick a deterministic contract, update metadata creation, and make every DDL test assert the resulting names.

Then harden migration behavior. Tests should force real failure states where possible. If inducing actual PostgreSQL DDL failure is hard without brittle SQL injection, add narrow test hooks or use invalid type-change requests that go through public APIs and produce failed migration rows. Do not fake success or only test string generation.

Then harden live matching. The subtle cases are records moving into or out of query result sets. For an update, subscriptions should receive the event if either `before` or `after` matches their query. For delete, only `before` can match. For create, only `after` can match. Permission filters must be merged before matching, and selected-field stripping should apply to delivered payloads.

Finally clean up `data/data.go`. The public API should feel like:

```go
store, err := data.Open(ctx, pool, data.Options{
    Tenant: "acme",
    Permissions: perms,
})

obj, err := store.CreateObject(ctx, data.ActorFromContext(ctx), data.CreateObjectRequest{
    Name: "company",
})

field, err := store.CreateField(ctx, actor, "company", data.CreateFieldRequest{
    Name: "arr",
    Type: data.FieldTypeNumeric,
})

page, err := store.QueryRecords(ctx, actor, "company", data.Query{
    Select: []string{"id", "name", "arr"},
    Filter: data.GTE("arr", 100000),
    Sort: []data.Sort{{Field: "arr", Direction: data.Desc}},
})
```

Do expose app-facing concepts:

```text
Store, Actor, Permissions, Object, Field, Record, Query, Filter, Sort, Event, Subscription
```

Do not expose migration internals, raw DDL structs, SQL compiler internals, outbox implementation types, or physical names as primary app APIs.

## Concrete Steps

1. Read `0007-data-platform-validation-and-inspect.md` outcomes. If CI PostgreSQL validation and inspect data are not complete, pause and finish or consciously reprioritize.
2. Update `internal/objectstore/ident.go` with final physical name derivation.
3. Add tests for identifier derivation, max length, reserved words, malicious names, collision resistance, and rename stability.
4. Update object and field metadata creation to persist final physical names.
5. Add migration idempotence tests for repeated bootstrap/object/field calls.
6. Add concurrency tests around object and field creation. Use real PostgreSQL integration tests when advisory locks matter.
7. Add failed migration and retry tests. Verify `schema_migrations.status`, error text, and final schema version behavior.
8. Add physical schema drift tests by manually altering physical tables/columns and verifying detection.
9. Add lossy type-change and destructive field-delete tests with clear user-facing errors.
10. Update live event matching according to created/updated/deleted before/after rules.
11. Add live tests for movement into query, movement out of query, delete from previously matching query, multiple subscribers, reconnect via `after_seq`, and `Last-Event-ID`.
12. Add live permission tests for `RowFilter`, selected-field stripping, permission changes, and unreadable field removal.
13. Add slow/disconnected client cleanup tests around SSE/live router internals.
14. Refine public `data` API and update fixture code.
15. Update docs and run final validation.

## Validation and Acceptance

Required validation:

```sh
go test ./...
go test ./internal/objectstore -count=1
go run ./cmd/onlava check --app-root testdata/apps/data-platform --json
go install ./cmd/onlava
onlava harness self --json --write
```

Focused test acceptance:

- Repeated bootstrap/object/field creation is idempotent.
- Concurrent object/field creation does not create duplicate metadata or broken physical schema.
- Failed DDL writes a failed migration row.
- Retry after failure behaves predictably.
- Physical schema drift is detected clearly.
- Name derivation is stable, collision-resistant, and PostgreSQL identifier-length compliant.
- Reserved words and malicious names are safe.
- Lossy type changes fail clearly.
- Destructive field deletion remains conservative.
- Live matcher uses `created = after`, `updated = before OR after`, `deleted = before`.
- Reconnect works through both `after_seq` and `Last-Event-ID`.
- Permissions and selected fields affect delivered live events.
- Slow/disconnected clients are cleaned up.

Public API acceptance:

- `github.com/pbrazdil/onlava/data` exposes app-facing concepts, not implementation internals.
- Fixture app code reads cleanly and uses the public package rather than `internal/objectstore`.
- Physical names remain available through inspect output, not as primary public API.

## Idempotence and Recovery

Migration tests must isolate their tenants and objects. When testing drift or failures, use unique physical names and cleanup only those names.

Concurrency tests should use timeouts and context cancellation so they do not hang CI. If PostgreSQL advisory lock behavior differs across versions, record the version and observed behavior in Surprises & Discoveries rather than weakening the test without explanation.

If public API cleanup causes fixture churn, update the fixture and README in the same commit. Avoid introducing compatibility wrappers unless they preserve a clearly intended public contract.

If a hardening test exposes a real design flaw that requires a larger change, update Decision Log before implementing the redesign.

## Artifacts and Notes

Expected changed files:

- `internal/objectstore/ident.go`
- `internal/objectstore/migrate.go`
- `internal/objectstore/live.go`
- `internal/objectstore/query.go`
- `internal/objectstore/objectstore_test.go`
- `internal/objectstore/objectstore_integration_test.go`
- `data/data.go`
- `testdata/apps/data-platform/*`
- `docs/local-contract.md` if public API or inspect behavior changes

Do not add:

- Dashboard UI
- Reporting
- Dynamic GraphQL
- External broker
- New data source directives
- New product CRM surface

## Interfaces and Dependencies

No new external runtime dependencies should be needed. Use existing PostgreSQL, pgx, and onlava test helpers.

The public package remains:

```go
import "github.com/pbrazdil/onlava/data"
```

Potential public helpers to add:

```go
data.ActorFromContext(ctx)
data.EQ(field, value)
data.GTE(field, value)
data.And(filters...)
data.Or(filters...)
data.Not(filter)
data.Asc
data.Desc
```

Only add helpers that materially improve app code and are covered by tests.
