# Data Platform Indexes and Cursor Pagination

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

The onlava data platform now has the first vertical slice, PostgreSQL validation and inspectability, migration/live hardening, and trigger-backed outbox support. The next foundation step is query usability and performance: metadata-backed physical indexes and stable cursor pagination.

Do not add CRM UI in this plan. The current goal is to make dynamic objects scale as real PostgreSQL tables while preserving onlava's existing migration discipline, inspectability, and live-update correctness.

Target flow:

```text
onlava data.Store public API
        |
        v
metadata-backed index request + query request
        |
        v
internal/datastore validates object, fields, field types, sort shape, and permissions
        |
        v
schema migration history + advisory lock + deterministic CREATE INDEX
        |
        v
PostgreSQL physical indexes and keyset pagination over real columns
        |
        v
RecordPage{Records, NextCursor} + inspectable index/drift state
```

Success means app code can create/list indexes through a small public API, inspect output shows logical and physical index state, and record queries can page forward with a stable keyset cursor without offset pagination.

## Progress

- [x] (2026-05-09 00:41Z) Created this ExecPlan and assigned historical ID 0010.
- [x] (2026-05-09 00:41Z) Linked this ExecPlan from `docs/plans/active.md`.
- [x] (2026-05-09 00:58Z) Implement metadata tables for logical indexes and index fields.
- [x] (2026-05-09 00:58Z) Implement deterministic physical index names and DDL generation.
- [x] (2026-05-09 00:58Z) Add `CreateIndex` and `ListIndexes` public data APIs.
- [x] (2026-05-09 00:58Z) Add inspect output for logical indexes, physical indexes, and drift.
- [x] (2026-05-09 00:58Z) Implement stable keyset cursor pagination and `RecordPage.NextCursor`.
- [x] (2026-05-09 00:52Z) Validate with PostgreSQL integration tests, fixture checks, full tests, install, and harness.

## Surprises & Discoveries

- `RecordPage.NextCursor` was already present in the public type, so the API change for pagination is behavioral rather than structural. Validation still needs to prove cursor shape rejection and stable second-page reads.
- The existing query compiler always includes `id`, `created_at`, and `updated_at` in record output. Cursor support keeps that behavior and adds hidden cursor columns only in SQL result scanning, not in returned records.

## Decision Log

- Decision: Do indexes and cursor pagination before CRM UI or dashboard data explorer.
  Rationale: The data platform already has real tables, real columns, transactional outbox, SSE, and direct-SQL trigger capture. Query performance and pagination are the next foundation risks; UI built before this would teach the wrong performance model.
  Date/Author: 2026-05-09 / Codex

- Decision: Use keyset cursor pagination, not offset pagination.
  Rationale: Dynamic tables can grow large. Offset pagination becomes slower and unstable under concurrent inserts/updates. Keyset cursors keep pages stable when the sort shape is deterministic.
  Date/Author: 2026-05-09 / Codex

- Decision: Always append `id` as a deterministic tie-breaker when building cursor pagination.
  Rationale: User-provided sort fields may not be unique. Appending `id` makes ordering stable and gives every cursor an unambiguous position.
  Date/Author: 2026-05-09 / Codex

- Decision: Keep physical index names inspectable but not central to the app-facing public API.
  Rationale: The public `data` API should be about objects, fields, queries, and indexes. Physical names are operational details for DB Studio and `onlava inspect data`.
  Date/Author: 2026-05-09 / Codex

- Decision: Keep arbitrary partial-index predicates out of the first implementation.
  Rationale: Accepting raw predicate SQL would violate the data platform's identifier/value safety model. The first implementation focuses on deterministic full-table btree/GIN indexes; constrained onlava-owned partial predicates can be added later.
  Date/Author: 2026-05-09 / Codex

- Decision: Include object schema version in cursors and reject mismatches.
  Rationale: Cursor values are interpreted through current metadata and sort fields. Rejecting stale schema-version cursors gives a clear failure instead of silently comparing against changed field definitions.
  Date/Author: 2026-05-09 / Codex

## Outcomes & Retrospective

Completed 2026-05-09.

Shipped:

- Metadata tables `onlava_data.indexes` and `onlava_data.index_fields`.
- Public `data.Store.CreateIndex` and `data.Store.ListIndexes` APIs plus `Index`, `IndexField`, `CreateIndexRequest`, `ListIndexesRequest`, and index method constants.
- Deterministic physical index names using the same readable stable-suffix strategy as object tables and field columns.
- Migration-managed physical index creation with advisory locks, schema migration rows, catalog verification, idempotent repeated creates, and failed-migration recording on DDL failure.
- Btree scalar and compound indexes, plus explicit GIN indexes for multi-select and JSON/raw JSON fields.
- `onlava inspect data --json` index output with logical fields and physical presence/drift state.
- Keyset cursor pagination in `QueryRecords`, including automatic `id` tie-breaker, base64url opaque cursors, schema-version validation, sort-shape rejection, and `RecordPage.NextCursor`.
- Data-platform fixture endpoints and README examples for index create/list and cursor usage.

Retrospective:

The index work fit naturally into the existing migration path. The main cursor design choice was to add hidden SQL result columns for cursor values so sort fields do not leak into returned records when they were not selected. Partial indexes remain intentionally deferred because accepting arbitrary predicate SQL would break the data platform's SQL-safety model.

## Context and Orientation

This plan follows completed data-platform plans:

- `docs/plans/0005-onlava-data-platform.md`: initial metadata/object/field/record/outbox/SSE vertical slice.
- `docs/plans/0007-data-platform-validation-and-inspect.md`: PostgreSQL CI and `onlava inspect data`.
- `docs/plans/0008-data-platform-migration-and-live-hardening.md`: migration correctness, live matching, and public data API cleanup.
- `docs/plans/0009-trigger-backed-outbox.md`: direct SQL and DB Studio changes write outbox rows through optional triggers.

Relevant files and packages:

- `data/data.go`: public data package facade.
- `internal/datastore/types.go`: internal request/response and metadata types.
- `internal/datastore/metadata.go`: bootstrap, tenants, objects, fields, metadata reads.
- `internal/datastore/migrate.go`: migration transactions, advisory locks, DDL verification.
- `internal/datastore/ident.go`: identifier validation, safe names, quoting.
- `internal/datastore/query.go`: query compiler, filter compiler, record matching helpers.
- `internal/datastore/mutate.go`: record mutations and outbox writes.
- `internal/datastore/live.go`: subscription resolution and event matching.
- `internal/datainspect`: inspect data JSON builder.
- `cmd/onlava`: inspect command wiring.
- `testdata/apps/data-platform`: fixture app and README walkthrough.
- `docs/local-contract.md`: public/beta contract documentation for stable surfaces only.
- `docs/schemas`: JSON schemas for inspect output if the shape changes.

Existing query behavior supports selected fields, filters, sort, and limit. `RecordPage` already has a `NextCursor` field, but the query implementation currently returns records without a cursor. This plan should fill that behavior.

Metadata tables to add in the `onlava_data` schema:

```text
onlava_data.indexes
  id
  tenant_id
  object_id
  name
  physical_name
  method
  predicate
  is_unique
  is_system
  created_at
  updated_at

onlava_data.index_fields
  id
  tenant_id
  index_id
  field_id
  position
  direction
  opclass
  expression
  created_at
  updated_at
```

The exact columns may change during implementation, but the first pass must preserve the ability to inspect logical index definitions and verify corresponding PostgreSQL catalog state.

Supported first-pass index methods:

```text
btree index on scalar fields
compound btree indexes
GIN index for multi_select text[]
GIN index for json/raw_json only when explicitly requested
optional partial indexes when the predicate is generated from a constrained onlava-owned shape
```

Do not add an ORM, migration framework, external broker, dynamic GraphQL, or UI in this plan.

## Milestones

Milestone 1: Metadata and physical naming.

Add index metadata tables to bootstrap. Define deterministic physical index names that are readable enough for DB Studio, stable after label rename, unique per physical table, safe under PostgreSQL's 63-byte identifier limit, and derived only from validated metadata.

Milestone 2: Index creation and listing API.

Add internal and public `CreateIndex` and `ListIndexes` APIs. The public shape should feel like ordinary app code:

```go
idx, err := store.CreateIndex(ctx, actor, "company", data.CreateIndexRequest{
    TenantKey: "acme",
    Name:      "company_stage_arr",
    Fields: []data.IndexField{
        {Field: "stage"},
        {Field: "arr", Desc: true},
    },
})

indexes, err := store.ListIndexes(ctx, actor, "company", data.ListIndexesRequest{
    TenantKey: "acme",
})
```

The first milestone should support a single-field scalar btree index and one compound btree index.

Milestone 3: Migration history, advisory locks, and verification.

Index creation must use the existing migration discipline: explicit transaction boundaries where possible, advisory locks per tenant/object, `onlava_data.schema_migrations` rows, deterministic DDL, successful catalog verification before returning success, and clear failed migration rows for failed DDL.

Milestone 4: Inspectability.

Extend `onlava inspect data --json` so it shows logical index definitions and physical index state. It should answer: does metadata know about this index, does PostgreSQL have it, what method/fields does it use, and is there drift?

Milestone 5: Cursor pagination.

Implement keyset pagination in `QueryRecords`. Always append `id` to the effective sort if the caller did not include it. Return a base64url-encoded JSON cursor in `RecordPage.NextCursor` when more records exist. Reject cursors whose object or sort shape does not match the current query.

Cursor payload shape:

```json
{
  "v": 1,
  "object": "company",
  "sort": [
    {"field": "arr", "desc": true},
    {"field": "id", "desc": false}
  ],
  "values": [100000, "record-id"]
}
```

Milestone 6: Fixture walkthrough and final validation.

Update `testdata/apps/data-platform` to expose index creation/listing if needed. Extend the README with sample calls. Run the full validation set and update docs only for stable public behavior that changed.

## Plan of Work

Start by reading the current metadata bootstrap and migration helpers. Reuse the existing schema naming style, advisory lock helpers, migration row recording, and physical verification style. Index DDL should be another migration-managed operation, not a standalone best-effort `CREATE INDEX` call hidden inside query code.

Define public types in `data/data.go` and internal types in `internal/datastore` before writing DDL. The app-facing API should be small:

```text
CreateIndexRequest
ListIndexesRequest
Index
IndexField
IndexMethod or string constants for btree/gin
```

Avoid exposing raw DDL structs or PostgreSQL catalog details as primary public API. Catalog state belongs in inspect output.

Then implement metadata bootstrap and loading. Index metadata should reference fields by ID, not by repeated field names, while public requests use logical field names resolved through current metadata. For compound indexes, preserve field order.

Next implement DDL generation. Only metadata-resolved physical table/column names may become SQL identifiers. Quote identifiers with existing helpers. Values such as names and predicates must not be interpolated from user input unless they are generated from a constrained onlava-owned shape. Do not accept arbitrary SQL predicates in the first pass unless the plan is updated with a clear safety design.

After index APIs are tested, implement cursor pagination. The query compiler must understand the effective sort, produce a lexicographic keyset predicate, fetch `limit + 1` rows, return only `limit`, and build `NextCursor` from the last returned row when another row exists.

Cursor validation rules:

```text
- cursor JSON must decode and have v=1
- cursor object must equal requested object
- cursor sort fields and directions must equal the effective sort
- cursor value count must equal sort count
- cursor values must be compatible with field types
- changed schema_version should either reject clearly or be recorded in the cursor and validated
```

Live updates should continue to work with paginated queries. The first version does not need to maintain page windows perfectly. It must not break existing subscription matching, and tests should verify that paginated query filters still resolve and relevant live updates still deliver.

## Concrete Steps

1. Read `internal/datastore/metadata.go`, `migrate.go`, `ident.go`, `query.go`, `data/data.go`, and `internal/datainspect`.
2. Add index metadata types and bootstrap DDL for `onlava_data.indexes` and `onlava_data.index_fields`.
3. Add deterministic index physical-name derivation and unit tests for long names, duplicate names, reserved words, and malicious names.
4. Add internal `CreateIndex` and `ListIndexes` methods in `internal/datastore`.
5. Wire public wrapper methods and types in `data/data.go`.
6. Add DDL generation for scalar btree indexes and compound btree indexes.
7. Add migration row recording, advisory locking, failed migration behavior, and PostgreSQL catalog verification.
8. Add GIN support for `multi_select` and explicit json/raw_json indexes if the btree path is stable.
9. Extend inspect data output to include index definitions and physical drift state.
10. Add fixture endpoints and README examples for create/list index if the existing fixture API needs explicit coverage.
11. Implement cursor encoding/decoding helpers.
12. Update query compilation to apply keyset predicates and `limit + 1` fetching.
13. Return `RecordPage.NextCursor` and reject mismatched cursors with user-facing errors.
14. Add unit and PostgreSQL integration tests.
15. Run validation commands and update this plan's Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective.

## Validation and Acceptance

Required validation:

```sh
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

PostgreSQL-specific validation should run through the existing testcontainers or `ONLAVA_TEST_DATABASE_URL` path:

```sh
go test ./internal/datastore ./internal/datainspect -count=1
onlava check --app-root testdata/apps/data-platform --json
```

Acceptance criteria:

```text
- index metadata is stored in onlava_data tables
- physical indexes are created with deterministic names
- index creation uses migration rows and advisory locks
- failed index DDL records a failed migration status
- inspect data shows logical indexes and physical index presence/drift
- public data.Store exposes CreateIndex and ListIndexes without exposing internal store types
- btree scalar and compound indexes are covered by PostgreSQL integration tests
- multi_select GIN and explicit json/raw_json indexes are either implemented and tested or explicitly deferred in this plan
- QueryRecords returns stable NextCursor values when more rows exist
- cursor pagination uses keyset predicates, not OFFSET
- cursor rejects changed object/sort shape clearly
- id is appended as deterministic tie-breaker when absent
- live updates still work for paginated query subscriptions
```

Do not mark this plan complete if `go test ./...` only passes by skipping DB-backed tests. The data-platform tests must execute against real PostgreSQL through the existing repository mechanism.

## Idempotence and Recovery

Bootstrap should be safe to run repeatedly. Creating the same index with the same logical name and same field/method shape should either return the existing metadata after verifying physical state or fail with a clear duplicate/incompatible shape error. It must not silently create a second physical index for the same logical index request.

If DDL fails, write a failed schema migration row with the error, roll back partial metadata where the transaction model allows it, and make retry behavior predictable. A retry with the same request should either succeed after fixing the underlying problem or report the previous failed state clearly.

If PostgreSQL contains an index but metadata does not, inspect should report unmanaged physical state if feasible, but the mutation API does not need to adopt it in this plan. If metadata contains an index but PostgreSQL does not, inspect must report drift and repeated create/list behavior must not hide the drift.

Cursor recovery rules:

```text
- malformed cursor: fail before SQL execution
- cursor for different object: fail before SQL execution
- cursor for different sort shape: fail before SQL execution
- cursor with incompatible value types: fail before SQL execution
- cursor beyond the end: return an empty page with no NextCursor
```

## Artifacts and Notes

Possible inspect JSON addition:

```json
{
  "objects": [
    {
      "name": "company",
      "indexes": [
        {
          "name": "company_stage_arr",
          "physical_name": "company_stage_arr__abc123",
          "method": "btree",
          "fields": [
            {"name": "stage", "direction": "asc"},
            {"name": "arr", "direction": "desc"}
          ],
          "physical": {
            "exists": true,
            "drift": false
          }
        }
      ]
    }
  ]
}
```

Possible query example:

```go
page, err := store.QueryRecords(ctx, actor, "company", data.QueryRecordsRequest{
    TenantKey: "acme",
    Query: data.Query{
        Select: []string{"id", "name", "stage", "arr"},
        Filter: data.EQ("stage", "won"),
        Sort:   []data.Sort{data.Desc("arr")},
        Limit:  50,
        Cursor: cursor,
    },
})
```

Follow-up plans after this one:

```text
0011 Data Platform Relationships
0012 Data Platform Public Contract
0013 Browser/UI Harness
0014 Dashboard Data Explorer
```

## Interfaces and Dependencies

Public package changes:

- `github.com/pbrazdil/onlava/data`
  - add `CreateIndexRequest`
  - add `ListIndexesRequest`
  - add `Index`
  - add `IndexField`
  - add method constants or typed constants for supported index methods if useful
  - add `(*Store).CreateIndex`
  - add `(*Store).ListIndexes`
  - make `QueryRecordsRequest.Query.Cursor` or an equivalent cursor field work if it already exists; otherwise add the smallest public field needed

Internal package changes:

- `internal/datastore`
  - metadata bootstrap for index tables
  - metadata loading for index definitions
  - DDL generation and verification for physical indexes
  - migration integration and advisory locking
  - cursor encode/decode and keyset query compilation

- `internal/datainspect`
  - logical index output
  - physical PostgreSQL catalog verification
  - drift reporting

Documentation and schemas:

- Update `docs/local-contract.md` only if the public API or inspect JSON is stable enough to document.
- Update `docs/schemas` if `onlava inspect data --json` has a schema file for its response.
- Update `testdata/apps/data-platform/README.md` for runnable examples.

Dependencies:

- Do not add an ORM, migration framework, external broker, dynamic GraphQL package, or UI dependency.
- Prefer the standard library and existing `pgx`/`pgxpool` dependencies.
- Only add a new dependency if this plan is updated with a concrete payoff and maintenance rationale.
