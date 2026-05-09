# onlava Data Platform

This ExecPlan is a living document. Update Progress, Surprises & Discoveries, Decision Log, and Outcomes & Retrospective as work proceeds.

## Purpose / Big Picture

onlava should gain a native Go data platform for dynamic CRM-style objects without copying Twenty line by line. The useful idea to preserve is metadata-defined objects and fields backed by real PostgreSQL tables and columns. The implementation must be onlava-native, Go-first, migration-aware, and correct for local live updates.

The first implementation is a narrow vertical slice:

```text
ordinary onlava app service
        |
        v
github.com/pbrazdil/onlava/data
        |
        v
internal/objectstore
        |
        v
PostgreSQL metadata + real record tables + transactional outbox
        |
        v
raw onlava SSE endpoint for query-aware live updates
```

When this plan is complete, a fixture app under `testdata/apps/data-platform` can start as a normal onlava app, create a dynamic object such as `company`, add fields such as `name`, `stage`, and `arr`, insert/query/update/delete records, and receive an SSE event only for matching query subscriptions. The feature is not a dynamic ORM, not GraphQL, not a Twenty UI port, and not an external broker integration.

## Progress

- [x] (2026-05-08 19:37Z) Created this ExecPlan and assigned historical ID 0005.
- [x] (2026-05-08 19:37Z) Read `ARCHITECTURE.md`, `docs/local-contract.md`, `PLANS.md`, roadmap context from `PLAN.md`, `go.mod`, and representative runtime, pgxpool, pubsub, cron, inspect, devdash, dbstudio, localproxy, codegen, build, and testdata files.
- [x] (2026-05-08 20:08Z) Defined `github.com/pbrazdil/onlava/data` as the small app-facing facade and `internal/objectstore` as the implementation boundary.
- [x] (2026-05-08 20:08Z) Implemented metadata bootstrap, object table creation, field column creation, migration rows, advisory locks, and physical schema verification.
- [x] (2026-05-08 20:08Z) Implemented metadata-resolved SQL query compilation with parameterized values, quoted metadata identifiers, filter validation, sort, selected fields, and permission row-filter merge.
- [x] (2026-05-08 20:08Z) Implemented create/update/delete/query record operations with explicit transactions and same-transaction outbox rows.
- [x] (2026-05-08 20:08Z) Implemented in-process live subscriptions, outbox replay after sequence, query-aware event matching, selected-field stripping, SSE heartbeats, and clean disconnect handling.
- [x] (2026-05-08 20:08Z) Added `testdata/apps/data-platform` fixture endpoints and unit plus `ONLAVA_TEST_DATABASE_URL`-gated PostgreSQL integration coverage.
- [x] (2026-05-08 19:54Z) Ran validation: `go test ./...`, `go install ./cmd/onlava`, `onlava check --app-root testdata/apps/data-platform --json`, and `onlava harness self --json --write`.

## Surprises & Discoveries

- `pgxpool/pgxpool.go` already wraps `github.com/jackc/pgx/v5/pgxpool` with onlava DB query tracing. The data platform should accept a pool or transaction from app code and benefit from this existing tracing rather than discover database URLs itself.
- Raw endpoints already support streaming when they have no middleware and are not mocked. Evidence: `runtime/server.go` calls `executeStreamingRawEndpoint` when `canStreamRawEndpoint` is true, and `runtime/middleware.go` exposes `http.Flusher` through `rawStreamingResponseWriter`. The first SSE fixture should work through an ordinary raw endpoint without runtime changes.
- There is no repository-wide PostgreSQL integration harness discovered by search. Existing DB-related tests are mostly URL/config/unit tests. The first PostgreSQL integration tests should be skipped unless `ONLAVA_TEST_DATABASE_URL` is set, and the fixture instructions should document that.
- The module already has NATS dependencies for public `pubsub`, but this feature should not use that path for live updates. The requested design is transactional outbox plus in-process fanout for the single local app process.
- `onlava check --app-root testdata/apps/data-platform --json` passes with ordinary onlava typed endpoints and a raw auth SSE endpoint, so the first slice did not require parser, codegen, or runtime route changes.
- `go test ./...` passes without `ONLAVA_TEST_DATABASE_URL`; the PostgreSQL vertical-slice integration test skips clearly when that variable is unset.

## Decision Log

- Decision: Put the core implementation in `internal/objectstore` and expose a deliberately small public package at `github.com/pbrazdil/onlava/data`.
  Rationale: User apps need ordinary onlava Go APIs and fixture endpoint types, but parser/build/codegen should not learn dynamic CRM metadata. Keeping implementation internal preserves room to change storage and query internals.
  Date/Author: 2026-05-08 / Codex

- Decision: Do not add new `//onlava:` directives in the first vertical slice.
  Rationale: Dynamic object metadata lives in PostgreSQL at runtime, while `internal/model` is the parsed app model for source-level onlava semantics. Ordinary services and raw endpoints are enough to prove the vertical slice.
  Date/Author: 2026-05-08 / Codex

- Decision: Use real PostgreSQL columns for scalar and composite fields instead of JSONB as the universal custom-field store.
  Rationale: Real columns preserve database-native filtering, sorting, indexes, constraints, DB Studio visibility, and predictable query plans. JSONB remains appropriate for specific field types such as `json`, `raw_json`, and first-pass `files`.
  Date/Author: 2026-05-08 / Codex

- Decision: Store user-managed select values as `text` and multi-select values as `text[]`, not PostgreSQL enum types.
  Rationale: User-managed options change often. PostgreSQL enum DDL is too rigid for app-level option lifecycle, while text/text[] plus metadata validation keeps migrations conservative.
  Date/Author: 2026-05-08 / Codex

- Decision: Build a metadata-validated SQL compiler instead of introducing an ORM.
  Rationale: The query shape is dynamic but bounded by metadata. A direct compiler can ensure identifiers only come from validated metadata, values are parameterized, and unsupported operators fail before SQL execution.
  Date/Author: 2026-05-08 / Codex

- Decision: Use a transactional outbox as the source for live events.
  Rationale: Record mutations and event creation must commit atomically. In-memory delivery can be best effort, but the outbox sequence gives reconnect/replay and avoids depending on ad-hoc post-commit application emits.
  Date/Author: 2026-05-08 / Codex

- Decision: Use SSE for the first live-update transport.
  Rationale: SSE fits the single-server local runtime, requires only `net/http`, supports reconnect with `Last-Event-ID` or an `after_seq` query parameter, and is already supported by raw onlava endpoint streaming.
  Date/Author: 2026-05-08 / Codex

- Decision: Do not implement trigger-backed outbox in the first pass, but shape the outbox so triggers can write the same rows later.
  Rationale: Explicit mutation-layer outbox rows keep the vertical slice small. A later trigger-backed layer can close the DB Studio/direct SQL gap without changing live event payloads.
  Date/Author: 2026-05-08 / Codex

- Decision: Use `onlava_data` for metadata/outbox tables and `onlava_data_records` for physical dynamic object tables in the first implementation.
  Rationale: Dedicated onlava-owned schemas avoid collisions with user app tables while still creating real PostgreSQL tables and columns. The exact table-name derivation must be deterministic, validated, and recorded in object metadata.
  Date/Author: 2026-05-08 / Codex

- Decision: Expose the public package mainly through aliases to `internal/objectstore` types for this first slice.
  Rationale: The public API is intentionally small and closely mirrors the internal boundary while the feature is beta. This keeps app code ergonomic without duplicating request/response structs.
  Date/Author: 2026-05-08 / Codex

- Decision: Register live subscriptions from the SSE request itself and emit an initial `ready` event after subscription setup and replay.
  Rationale: A separate two-step stream registration protocol would need more public machinery. Query parameters and a `subscriptions` JSON parameter prove the live path with ordinary raw endpoints while keeping tests deterministic.
  Date/Author: 2026-05-08 / Codex

## Outcomes & Retrospective

Completed on 2026-05-08 as a narrow first vertical slice.

Implemented `github.com/pbrazdil/onlava/data` and `internal/objectstore` with PostgreSQL metadata bootstrap, real object tables, real field columns, conservative schema migrations with advisory locks and verification, metadata-validated SQL query compilation, transactional record mutations, outbox events, in-process query-aware live routing, and SSE replay/fanout.

Added `testdata/apps/data-platform`, which exposes ordinary onlava services for object/field/record APIs and a raw auth SSE endpoint. `onlava check --app-root testdata/apps/data-platform --json` passes, proving no new directives or runtime server model were needed.

Validation passed with `go test ./...`, `go install ./cmd/onlava`, and `onlava harness self --json --write`. The harness still reports the pre-existing large-file warnings for `internal/clientgen/typescript_render.go` and `internal/codegen/generator.go`. PostgreSQL integration coverage is present but skipped unless `ONLAVA_TEST_DATABASE_URL` is set; this environment variable was not set during the final validation run, so DB-backed execution remains the one manual follow-up on this machine.

## Context and Orientation

The onlava repository is a Go module at `github.com/pbrazdil/onlava`. User apps depend on public packages such as `github.com/pbrazdil/onlava`, `auth`, `errs`, `middleware`, `pubsub`, `cron`, and `pgxpool`, and declare services with `//onlava:` directives.

Important architecture boundaries from `ARCHITECTURE.md`:

- `cmd/onlava` is CLI orchestration only. Non-CLI packages must not import it.
- `internal/parse` and `internal/model` describe Go source-level app semantics. Runtime dynamic object metadata does not belong there unless a new source directive is added.
- `internal/codegen` should keep generated code boring Go.
- `internal/build` creates disposable transient build workspaces. Runtime data metadata must live in PostgreSQL, not in generated artifacts.
- `runtime` starts one local HTTP server per generated app process.
- Public package behavior is a stable boundary and should stay small.

Relevant existing packages:

- `pgxpool`: public wrapper around pgxpool that installs runtime DB tracing. The data platform should accept this pool type or small interfaces compatible with it.
- `runtime`: registers typed/raw endpoints, tracks request/auth state, supports raw streaming, runs cron and local pubsub, emits local observability, and starts one HTTP server.
- `pubsub`: public local Pub/Sub integration backed by NATS/JetStream. Do not use it for this first data live-update path.
- `cron`: public cron registration around runtime jobs. No direct dependency is expected for this feature.
- `internal/inspect`: stable JSON surfaces. Add inspect output only if a stable first-slice JSON contract is truly needed.
- `internal/devdash`: dashboard store and local observability types. Do not make the dashboard store the source of truth for data metadata or events.
- `internal/dbstudio`: discovers `DATABASE_URL`/`DatabaseURL` and starts DB Studio. Direct DB Studio edits to dynamic record tables will not emit live events until trigger-backed outbox exists.
- `internal/localproxy`: local HTTPS/frontend proxy. No direct change is expected.
- `internal/codegen` and `internal/build`: generated app binary path. The fixture should use ordinary service code and public data APIs so codegen only sees normal endpoints.
- `testdata/apps`: fixture apps use `.onlava.json`, `go.mod`, `replace github.com/pbrazdil/onlava => ../../..`, and ordinary onlava service packages.

Terminology:

- A data tenant is the isolation unit for dynamic object metadata and records. Do not call it a workspace in new code because onlava already uses build workspace terminology.
- An object is a metadata-defined record type such as `company`.
- A field is metadata for one logical property. A scalar field maps to one physical column. A composite field maps to multiple physical columns.
- A record is a row in the physical table for an object, returned to users as `data.Record`, a dynamic map-like value.
- The outbox is the PostgreSQL table of committed data change events. It is the replay source for live subscriptions.

## Milestones

Milestone 1 defines boundaries and public API. This is complete when `data` exposes the small request/response, actor, permission, record, query, and store facade needed by a fixture app, and `internal/objectstore` contains the implementation skeleton with unit tests for validation helpers.

Milestone 2 creates metadata and physical schema. This is complete when opening or initializing the store creates `onlava_data` metadata tables, creates `onlava_data_records`, can create a data tenant, can create an object with a physical table, can create fields with physical columns, records every DDL operation in `onlava_data.schema_migrations`, uses PostgreSQL advisory locks, verifies the resulting schema, and bumps `schema_version` only after verification.

Milestone 3 implements the SQL query compiler. This is complete when a metadata-resolved query supports selected fields, `eq`, `neq`, `gt`, `gte`, `lt`, `lte`, `in`, `is_null`, `contains`, `and`, `or`, `not`, sort, limit, and cursor/page token basics. All identifiers must come from metadata and be quoted, all values must be parameters, and invalid operators for a field type must fail before SQL execution.

Milestone 4 implements transactional mutations and outbox rows. This is complete when create/update/delete record operations run in explicit transactions, validate metadata and permissions, write record data, insert outbox rows in the same transaction, commit, then notify the local publisher. Metadata mutations should also produce metadata-change events through the same outbox path.

Milestone 5 implements live updates. This is complete when an in-process event router can register query subscriptions, poll/replay outbox events after a sequence, match relevant events to active subscriptions, recheck permissions, strip unreadable fields, include matching `query_ids`, send SSE heartbeats, and shut down cleanly.

Milestone 6 adds the onlava fixture app and integration tests. This is complete when `testdata/apps/data-platform` exposes ordinary `//onlava:api` endpoints for object/field/record CRUD/query and a raw SSE endpoint, and tests prove the expected vertical slice against PostgreSQL when `ONLAVA_TEST_DATABASE_URL` is set.

Milestone 7 updates contracts only for stable surfaces. This is complete when `ARCHITECTURE.md` mentions the new package area if implemented, `docs/local-contract.md` documents any stable CLI/API/public package behavior added by the first slice, and any inspect JSON schemas/tests are updated if an inspect command is added.

## Plan of Work

Start with the API shape, not DDL. Add the smallest public `data` package that a normal onlava service can use without knowing internal package names. The public package should define `Store`, `Options`, `Actor`, `Permissions`, `Record`, `Object`, `Field`, `Query`, `Filter`, `Sort`, `RecordPage`, and request/response structs that fixture endpoints can reuse. `Store` should be created from an explicit pgx pool or connection interface supplied by app code.

Then implement `internal/objectstore` in small files with clear boundaries:

```text
internal/objectstore/types.go
internal/objectstore/ident.go
internal/objectstore/fields.go
internal/objectstore/metadata.go
internal/objectstore/migrate.go
internal/objectstore/query.go
internal/objectstore/mutate.go
internal/objectstore/outbox.go
internal/objectstore/live.go
internal/objectstore/sse.go
internal/objectstore/permissions.go
```

Keep SQL generation deterministic. DDL generation should return structured migration records before execution, and execution should write those records with status transitions. Advisory locks should be scoped by data tenant and object when possible. Schema verification should read PostgreSQL catalogs and compare expected tables/columns/indexes to actual state.

Use physical columns for normal fields. Initial field mapping:

```text
text         -> text
rich_text    -> text
number       -> double precision
numeric      -> numeric
currency     -> <field>_amount numeric + <field>_currency_code text
boolean      -> boolean
date         -> date
datetime     -> timestamptz
uuid         -> uuid
select       -> text
multi_select -> text[]
rating       -> smallint
json         -> jsonb
raw_json     -> jsonb
files        -> jsonb
full_name    -> <field>_first_name text + <field>_last_name text
address      -> multiple text columns
emails       -> jsonb
phones       -> jsonb
relation     -> uuid foreign key for many-to-one; join table for many-to-many
```

Document any changes to this mapping in the Decision Log before implementing them.

For record reads, compile from metadata to SQL directly. The compiler should build an internal AST for filters and produce SQL plus args. It must never interpolate user-provided identifiers. It may interpolate only quoted identifiers from validated metadata and internal table/column names. Composite fields should be selected through their component columns and reassembled into logical values before returning records.

For mutations, require an explicit `Actor` argument. A helper may convert current onlava auth state into an actor for convenience, but core mutation methods should not rely on globals. Permission checks go through an interface with an allow-all default:

```text
CanReadObject
CanWriteObject
CanReadField
CanWriteField
RowFilter
```

For live updates, first support one process and one database. The outbox gives durable sequence and replay. The in-process router handles active subscriptions only for the running app server. When a client reconnects with `after_seq`, read missed events from `onlava_data.outbox_events`, then continue with live fanout. Do not add Redis, NATS, Kafka, gqlgen, an ORM, or a migration framework.

Add the fixture only after the package can perform object, field, record, and event operations in focused unit tests. The fixture app should demonstrate that this is native onlava code:

```go
//onlava:api auth path=/data/objects method=POST
func CreateObject(ctx context.Context, req data.CreateObjectRequest) (*data.Object, error)

//onlava:api auth path=/data/objects/:object/fields method=POST
func CreateField(ctx context.Context, object string, req data.CreateFieldRequest) (*data.Field, error)

//onlava:api auth path=/data/objects/:object/records/query method=POST
func QueryRecords(ctx context.Context, object string, req data.QueryRecordsRequest) (*data.RecordPage, error)

//onlava:api auth path=/data/objects/:object/records method=POST
func CreateRecord(ctx context.Context, object string, req data.CreateRecordRequest) (*data.RecordResponse, error)

//onlava:api auth path=/data/objects/:object/records/:id method=PATCH
func UpdateRecord(ctx context.Context, object string, id string, req data.UpdateRecordRequest) (*data.RecordResponse, error)

//onlava:api auth path=/data/objects/:object/records/:id method=DELETE
func DeleteRecord(ctx context.Context, object string, id string) (*data.DeleteRecordResponse, error)

//onlava:api auth raw path=/data/events method=GET
func Events(w http.ResponseWriter, r *http.Request)
```

If adding `onlava inspect data --json` becomes necessary, keep it narrow and stable. Update `docs/schemas`, inspect tests, and `docs/local-contract.md` in the same change. The first slice should prefer fixture endpoints and tests over new CLI surface.

## Concrete Steps

1. Re-read `ARCHITECTURE.md`, `docs/local-contract.md`, `PLANS.md`, `go.mod`, `pgxpool/pgxpool.go`, `runtime/server.go`, `runtime/current.go`, `internal/inspect/inspect.go`, `internal/build/build.go`, and `testdata/apps/basic/service/api.go` before coding.
2. Create `internal/objectstore` and `data` packages with only type definitions, validation helpers, and package-level docs. Add unit tests for object names, field names, physical identifier derivation, and error messages.
3. Implement identifier quoting and SQL builder helpers in `internal/objectstore/ident.go`. Tests must include malicious names and values such as quotes, semicolons, comments, mixed case, and reserved words.
4. Implement field type mapping and composite expansion in `fields.go`. Add tests for every first-pass field type, including select/multi-select text storage and one composite field round trip shape.
5. Implement metadata bootstrap in `metadata.go`: create schemas and metadata tables with deterministic SQL. Include metadata tables for data tenants, objects, fields, field options, schema migrations, and outbox events.
6. Implement object and field migrations in `migrate.go`: advisory lock, migration row with status, apply DDL, verify catalogs, bump schema version, finish migration, and record error on failure.
7. Implement the query model and compiler in `query.go`: selected fields, filters, sort, limit, cursor/page token placeholder, permission row filter merge, SQL text, and args.
8. Implement mutations in `mutate.go`: create object, create field, create record, update record, delete record, query records. All writes must use explicit transactions.
9. Implement outbox creation in `outbox.go`. Record rows with `seq`, `id`, tenant/object/record identity, action, actor, schema version, changed fields, before/after/diff, created time, and nullable published time.
10. Implement live subscriptions in `live.go`: subscription registration, query matching, permission recheck, field stripping, outbox polling/replay, and fanout.
11. Implement SSE helpers in `sse.go`: content type, heartbeats, event IDs from outbox `seq`, `after_seq` and `Last-Event-ID` handling, clean shutdown on request cancellation.
12. Add `testdata/apps/data-platform` with `.onlava.json`, `go.mod`, service code, auth handler, and fixture tests or CLI integration helpers.
13. Add skipped PostgreSQL integration tests controlled by `ONLAVA_TEST_DATABASE_URL`. Tests should create isolated temporary database schemas or unique data tenant keys and clean up after themselves.
14. Add or update docs only for stable behavior that exists in code. If no inspect command is added, say so in this plan and avoid changing inspect schemas.
15. Update this ExecPlan after each milestone with Progress, discoveries, decisions, and final outcomes.

## Validation and Acceptance

Fast validation during implementation:

```sh
go test ./internal/objectstore ./data
go test ./runtime ./pgxpool
go test ./internal/codegen ./internal/build
```

PostgreSQL integration validation, when a local database is available:

```sh
ONLAVA_TEST_DATABASE_URL='postgres://...' go test ./internal/objectstore ./testdata/...
```

Full repository validation before finishing:

```sh
gofmt -w data/*.go internal/objectstore/*.go
go test ./...
go install ./cmd/onlava
onlava harness self --json --write
```

If PostgreSQL is not available, the integration tests must skip with a clear message naming `ONLAVA_TEST_DATABASE_URL`, and the final handoff must say which DB-backed checks remain manual.

Acceptance criteria for the first PR:

- No public Twenty-named packages, imports, commands, directives, or generated app syntax are added.
- No dynamic ORM, GraphQL layer, Redis, external broker, migration framework, or TypeScript UI is added.
- `github.com/pbrazdil/onlava/data` remains small and app-facing.
- `internal/objectstore` owns metadata, migrations, SQL compilation, mutations, outbox, permissions, and live routing.
- Dynamic metadata is stored in PostgreSQL and not in transient build artifacts.
- Creating an object creates metadata and a real PostgreSQL table.
- Creating a scalar field creates metadata and a real column.
- Creating a composite field creates metadata and multiple real columns, then reads reassemble logical values.
- Select fields use text/text[] plus metadata options, not PostgreSQL enums.
- Record create/update/delete write outbox events in the same transaction.
- Query subscriptions receive relevant live events and do not receive irrelevant ones.
- Reconnect with `after_seq` replays missed events.
- Auth/actor context is recorded when available through explicit actor input.
- Existing onlava fixture apps still build and tests pass.

## Idempotence and Recovery

Metadata bootstrap must be safe to call repeatedly. `create schema if not exists` and `create table if not exists` are acceptable for bootstrap, but object and field migrations must be represented in `onlava_data.schema_migrations` and verified.

Migration operations must use PostgreSQL advisory locks scoped by data tenant/object. If a migration fails after writing a migration row, retry should see the failed row, create a new attempt or continue according to recorded status, and never silently bump `schema_version`.

Physical DDL should be deterministic. If a process crashes after DDL but before status update, the next run should verify catalogs and either mark the migration complete if it exactly matches the intended DDL or report a clear recovery error naming the object, field, and migration id.

Destructive operations are conservative in the first pass. Deleting a field should archive metadata or mark deletion pending unless destructive DDL is explicitly implemented and recorded. Lossy type changes should fail with a user-facing error.

Outbox delivery is at least once. SSE clients should deduplicate by `seq` or event id. Publishing must happen after commit. If in-process notification is missed, polling/replay from `onlava_data.outbox_events` must still deliver committed events.

Integration tests must use unique data tenant keys and object names. They should clean up schemas/tables when possible, but cleanup failure should not hide the original test failure.

## Artifacts and Notes

Initial metadata tables:

```text
onlava_data.tenants
onlava_data.objects
onlava_data.fields
onlava_data.field_options
onlava_data.schema_migrations
onlava_data.outbox_events
```

Initial physical record schema:

```text
onlava_data_records
```

Outbox event payload shape:

```json
{
  "seq": 123,
  "event_id": "...",
  "tenant_id": "...",
  "object": "company",
  "record_id": "...",
  "action": "updated",
  "actor_id": "...",
  "schema_version": 7,
  "changed_fields": ["name", "stage"],
  "before": {"name": "Old"},
  "after": {"name": "New"},
  "query_ids": ["companies-index", "company-detail"]
}
```

Direct SQL and DB Studio edits in the first version:

- Metadata changes should go through the data package so migrations, verification, schema versions, and outbox events are recorded.
- Record changes made directly through SQL or DB Studio will update physical tables but will not guarantee outbox rows or live updates until trigger-backed outbox is implemented.
- Trigger-backed outbox should later attach to physical record tables and write the same `onlava_data.outbox_events` shape. It should be designed after explicit mutation-layer behavior is stable.

Open implementation questions to resolve in the plan before coding each area:

- Exact table-name derivation and max-length handling for physical object tables.
- Whether record IDs are always UUIDs generated by the data platform or can use app-provided UUIDs.
- Cursor format for first-pass pagination.
- Whether `number` should stay `double precision` or move to `numeric` for all user-facing numeric fields.
- Whether `emails` and `phones` should stay JSONB in v1 or use normalized child tables earlier.
- Whether `published_at` means attempted fanout, successful fanout, or durable poll acknowledgement.

## Interfaces and Dependencies

Public package sketch:

```go
package data

type Store struct { /* opaque */ }
type Options struct {
    Permissions Permissions
}
type Actor struct {
    ID string
    Data any
}
type Record map[string]any

func Open(ctx context.Context, pool PgxPool, opts Options) (*Store, error)
func ActorFromContext(ctx context.Context) Actor
```

The final public API may differ, but it must stay small and be recorded in the Decision Log before implementation.

The internal package may depend on existing repository dependencies, especially `github.com/jackc/pgx/v5` and `github.com/pbrazdil/onlava/pgxpool` compatible interfaces. It must not add Redis, NATS, Kafka, gqlgen, an ORM, or a migration framework.

Permission hook sketch:

```go
type Permissions interface {
    CanReadObject(context.Context, Actor, ObjectRef) error
    CanWriteObject(context.Context, Actor, ObjectRef) error
    CanReadField(context.Context, Actor, FieldRef) error
    CanWriteField(context.Context, Actor, FieldRef) error
    RowFilter(context.Context, Actor, ObjectRef) (*Filter, error)
}
```

Query model sketch:

```go
type Query struct {
    Object string
    Select []string
    Filter *Filter
    Sort []Sort
    Limit int
    Cursor string
}
```

Supported initial filters:

```text
eq, neq, gt, gte, lt, lte, in, is_null, contains, and, or, not
```

No CLI command is required in the first slice. If `onlava inspect data --json` is added, it must include a schema under `docs/schemas`, tests in `cmd/onlava` or `internal/inspect`, and a local-contract update in the same implementation change.
